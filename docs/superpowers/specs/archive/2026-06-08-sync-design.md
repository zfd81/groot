# sync 模块设计文档

**日期**：2026-06-08

## 一、功能设计

### 1.1 功能概述

sync 模块负责本地 HOME 目录（`~/.groot/`）与 MinIO 远端之间的"集群共享配置"双向镜像同步，并通过 `groot push` / `groot pull` / `groot diff` 三个子命令暴露给用户。

它的存在是为了在多实例集群部署下，让所有节点共享同一份"配置/技能/子 Agent/MCP/GROOT.md"等可同步资源，本地编辑后通过 push 推到 MinIO，新节点或落后节点通过 pull 把远端最新版镜像到本地。

仅在 MinIO 存储模式下可用；local 模式下三个命令统一返回 `ErrSyncDisabled`。

### 1.2 同步资源白名单

只有以下根路径的资源参与 sync：

| 路径 | 类型 | 说明 |
|---|---|---|
| `config.yaml` | 文件 | 主配置 |
| `skills/` | 目录 | 全局技能集合 |
| `subagents/` | 目录 | 子 Agent 定义（含其 `skills/` 子目录） |
| `mcp/` | 目录 | MCP 服务配置 |
| `GROOT.md` | 文件 | 全局系统提示词 |

明确排除（黑名单语义）：

- `env.yaml` —— 含 MinIO 凭据，按节点本地维护
- `memory/` —— 会话历史、附件，按节点隔离
- `schedules/` —— 定时任务，按节点维护
- `cluster/` —— 集群成员注册，按节点动态生成
- 任何根目录之外的路径

### 1.3 路径校验规则（`ValidateSyncPath`）

接受用户输入的相对路径（相对于 `homeDir`），按下列规则依次校验：

1. **空路径** → `sync: empty path`
2. **路径遍历防护**：路径中含 `..` → `sync: path traversal not allowed: %q`
3. **白名单根校验**：路径必须等于某个白名单根，或以 `<root>/` 为前缀，否则 `sync: path %q is not in sync whitelist`
4. **skill 目录原子性**：禁止直接操作 skill 目录内的单个文件；必须以整个 skill 目录为单位同步：
   - `skills/{skill}/{file...}` （深度 ≥ 3，根为 `skills`）→ 拒绝
   - `subagents/{name}/skills/{skill}/{file...}` （深度 ≥ 5，第三段为 `skills`）→ 拒绝
   - 报错示例：`sync: path "skills/weather/SKILL.md" is inside a skill directory — operate the skill directory instead (e.g. "skills/weather")`

绝对路径不会通过白名单根前缀检查，自然被拒绝。

### 1.4 SyncManager 接口

```go
type SyncManager interface {
    Push(paths []string) error
    Pull(paths []string) error
    Diff(paths []string) (DiffResult, error)
    CleanTmpResidue(paths []string) error
}
```

#### 1.4.1 构造与可用性判定

```go
func NewSyncManager(homeDir, remoteBase string, store istorage.Storage) SyncManager
```

仅以 `store == nil` 作为禁用判据：

- `store == nil` → 返回 `disabledSyncManager`，所有方法返回 `ErrSyncDisabled`
- `store != nil` → 返回可用的 `localSyncManager`

`remoteBase` 为空字符串在 minio 模式下表示 bucket 根，是合法值。

#### 1.4.2 ErrSyncDisabled

```go
var ErrSyncDisabled = errors.New("sync: minio 模式未启用 — 请在 env.yaml 中配置 minio 节")
```

#### 1.4.3 paths 参数

`paths` 为相对路径列表（相对 `homeDir`），传 `nil` 或空切片表示"操作全部白名单根"。

`paths` 不做"类别目录展开"——目录整体交给 `ComputeDiff`，由它递归扫描双侧（local 与 remote）的并集，否则会漏掉只在远端存在的子项。

### 1.5 DiffResult 结构与语义

```go
type DiffResult struct {
    Added    []string // 本地有，远端没有
    Modified []string // 双侧都有但内容/时间不同
    Removed  []string // 远端有，本地没有
    Same     []string // 一致
}
```

**语义恒定**：`Added` / `Removed` 始终以"本地 vs 远端"为锚，与命令方向无关。push / pull / diff 三个命令在渲染层根据自己的语义重新解释这四组（见 §1.10）。

`IsEmpty()` 仅看 `Added + Modified + Removed`，`Same` 不参与判断。

### 1.6 Diff 算法（`ComputeDiff`）

输入：`store, localBase, remoteBase, paths`
输出：`DiffResult`

对每个 path：

1. **本地侧 walk**（`walkLocalFiles`）
   - `os.Stat(absPath)`：不存在则返回空切片（不报错）
   - 文件：直接收录，但 `*.tmp` 文件**全链路过滤**（diff/push/pull 一律不视作可同步对象，因为它们是 sync 自身原子写中转的临时产物）
   - 目录：`filepath.Walk` 递归收录所有非目录、非 `*.tmp` 的文件

2. **远端侧 walk**（`walkRemoteFiles`）
   - `store.Stat(remotePath)`：`ErrNotFound` 返回空切片（不报错），其他错误透传
   - `IsDir == false`：直接收录
   - `IsDir == true`：调用 `listRemoteRecursive` 通过 `store.List` 递归列出所有文件

3. **集合比较**
   - 双侧文件按相对路径建 map，相对路径统一用 `/` 分隔（`filepath.ToSlash`）
   - 仅本地存在 → `Added`
   - 仅远端存在 → `Removed`
   - 双侧都存在：用 `differsFromRemote` 判等
     - `local.Size() != remote.size` → `Modified`
     - `|local.ModTime() - remote.ModTime| > 1s` → `Modified`
     - 否则 → `Same`

4. **mtime 容差**：`mtimeTolerance = time.Second`，覆盖 MinIO LastModified 的秒级精度与本地 fs 精度的天然偏差。

### 1.7 路径拼接约定

- **远端路径**：`joinPath(remoteBase, rel)`，统一用 `/` 分隔（minio object-key 语义）。`remoteBase == ""` 时直接返回 `rel`
- **本地路径**：`filepath.Join(localBase, filepath.FromSlash(rel))`，按 OS 分隔符
- **相对路径键**：`relPath(base, path)`，从绝对路径剥掉 `base + "/"` 前缀，永远用 `/` 分隔

### 1.8 push 流程

`Push(paths) error`：

1. `resolveSyncPaths(paths)` 校验白名单
2. `ComputeDiff` 计算差异
3. 推送 `Added`：`pushOne` 写远端
4. 推送 `Modified`：`pushOne` 写远端
5. 删除 `Removed`：`store.Delete(remotePath)`，`ErrNotFound` 视为成功（幂等）

#### 1.8.1 pushOne / pushFile 流程

```
open localPath
  → store.Write(ctx, remotePath, f, size, "")    // Storage 接口保证原子写
  → close f
  → store.Stat(remotePath) 取 LastModified
  → os.Chtimes(localPath, mtime, mtime)          // 锚定本地 mtime
```

#### 1.8.2 mtime 锚定（重要）

push/pull 完成后必须把本地 mtime 锚定到远端 LastModified，否则下一次 diff 会把刚 sync 过的文件错误判为 `Modified`：

- 本地 mtime 是"内容修改时间"
- 远端 LastModified 是"object 上传完成时间"
- 两者必然不同（push 时本地写在前、远端写在后；pull 时远端写在前、本地写在后）
- 同步语义要求"完成后双侧逻辑等价" → 取远端 LastModified 作为统一锚点

### 1.9 pull 流程

`Pull(paths) error`：

1. `resolveSyncPaths(paths)` 校验
2. **`cleanTmpFiles`**（best-effort）：递归删除 `paths` 范围下所有 `*.tmp` 残留。失败时记录但不阻塞 pull
3. `ComputeDiff` 计算差异
4. **Phase A — 写入**（任何中断都保证本地至少有一份完整内容）：
   - `Removed`（远端有/本地没有）→ `pullOne` 写本地
   - `Modified`（双侧不同）→ `pullOne` 覆盖本地
5. **Phase B — 删除**（必须严格在 Phase A 全部成功后）：
   - `Added`（本地有/远端没有）→ `os.Remove(localPath)`，`os.IsNotExist` 视为成功

Phase A → B 顺序保证："先删后写"中途崩溃的空窗不会出现。

#### 1.9.1 pullOne / pullFile 流程

```
store.Stat(remotePath) → ri (LastModified 锚点)
store.Read(remotePath) → rc
io.ReadAll(rc) → data
os.MkdirAll(dir(localPath), 0755)
tmp := localPath + ".tmp"
os.Remove(tmp)                          // 清理孤儿 tmp
open(tmp, O_WRONLY|O_CREATE|O_TRUNC, 0644)
write(data) → sync() → close()          // 失败任意一步删 tmp 后返回
os.Rename(tmp, localPath)
os.Chtimes(localPath, ri.ModTime, ri.ModTime)  // 锚定到远端 LastModified
```

写失败时清理 tmp 文件；rename 失败时保留 tmp 等待下次 pull 清理（CleanTmpResidue / cleanTmpFiles）。

#### 1.9.2 *.tmp 全链路语义

| 阶段 | 行为 |
|---|---|
| pull 启动前 | `CleanTmpResidue` 删除 `paths` 范围所有 `*.tmp` |
| diff 扫描 | `walkLocalFiles` 跳过 `*.tmp`（不计入 `Added/Modified`） |
| push | 同上，`*.tmp` 永远不会被推到远端 |
| pull 写入 | tmp 仅作为单文件 rename 中转，rename 后立即消失 |

### 1.10 输出格式（`RenderDiff` / `FormatDiff`）

`RenderDiff(w, d, direction)` 按 `direction` 选三种渲染分支：

#### 1.10.1 push（`direction == "push"`，默认分支）

```
Changes to push (HOME → MinIO):
  Added:
    <files...>
  Modified:
    <files...>
  Removed:
    <files...>
```

#### 1.10.2 pull（`direction == "pull"`）

按 pull 视角反向措辞：

```
Changes to pull (MinIO → HOME):
  Removed locally:                                   # ← 来自 d.Added
    <files...>
  Modified locally (overwritten by remote):          # ← 来自 d.Modified
    <files...>
  Added locally:                                     # ← 来自 d.Removed
    <files...>
```

#### 1.10.3 diff（`direction == "diff"`，中性措辞）

```
Differences (HOME ↔ MinIO):
  Local only:                                        # ← 来自 d.Added
    <files...>
  Modified (size or mtime differs):
    <files...>
  Remote only:                                       # ← 来自 d.Removed
    <files...>
```

#### 1.10.4 无差异

无论 direction，输出一行：`No differences found — already in sync.`

#### 1.10.5 重启提示（仅 pull）

`anyNeedsRestart(allChanged)` 返回 true 时，pull 输出末尾追加：

```
⚠  Some resources require a service restart to take effect:
   config.yaml, mcp configs, subagent entry files (agent.md).
   Please restart groot after pull completes.
```

判定算法：变更路径满足以下条件之一即为"需重启"：

- `path == "config.yaml"`
- `path` 以 `config.yaml/`、`mcp/`、`subagents/` 中任一前缀开始（即 `needsRestartPaths = ["config.yaml", "mcp/", "subagents/"]`）

push 与 diff 不输出重启提示。

> **已知限制**：当前判定按 `subagents/` 整目录前缀匹配，会把 `subagents/<name>/skills/<skill>/...` 这类应当热加载的资源也标记为"需重启"，存在假阳性。后续应细化判定算法，对 `subagents/<name>/skills/` 子级豁免。

### 1.11 ConfirmContinue 交互

`ConfirmContinue(r io.Reader, w io.Writer) bool`：

- 在 `w` 写 `Continue? (y/n): `
- 从 `r` 读一行
- 转小写并 trim 后，仅 `y` 或 `yes` 返回 true，其余（含 EOF / 非 tty）返回 false

### 1.12 命令行接口

#### 1.12.1 通用前置流程

三个命令的 RunXxx 入口都按相同顺序：

1. `homeDir := GetDefaultHome()`
2. `cfg := config.Load(homeDir)`
3. **`cfg.Storage.Minio == nil` → 报错并退出**：`groot <cmd> 仅在 minio 模式下可用\n请在 ~/.groot/env.yaml 中配置 minio 节`
4. `store := storage.New(cfg.Storage)`
5. `mgr := sync.NewSyncManager(homeDir, "", store)`（`remoteBase = ""` 表示 bucket 根）

#### 1.12.2 `groot push [path...] [-y]`

参数：
- `path...`：要推送的资源路径（可多个），省略时推送全部白名单资源
- `-y, --yes`：跳过交互确认（适用于脚本化部署）
- `-h, --help`：显示帮助

执行：
1. `mgr.Diff(paths)` 扫描差异
2. `FormatDiff(diff, "push")` 输出
3. `IsEmpty()` 直接返回（无差异）
4. 非 `-y` 模式 → `ConfirmContinue`，用户取消则输出 `Cancelled.` 并返回 nil
5. `mgr.Push(paths)` 执行
6. 输出 `Push complete.`

注：push 链路当前会扫描两次差异（确认前一次 + `Push` 内部 `ComputeDiff` 一次）。确认期间发生的 mtime/内容漂移以执行时点的扫描为准。

#### 1.12.3 `groot pull [path...] [-y]`

参数与 push 相同。

执行：
1. `mgr.CleanTmpResidue(paths)`（best-effort，错误吞掉）—— 必须在 Diff 之前，否则 `*.tmp` 不在白名单走查范围（其实会被过滤），但更主要是防止 pull 期间 walk 命中残留时被识别为本地多余文件
2. `mgr.Diff(paths)` 扫描
3. `FormatDiff(diff, "pull")` 输出（含重启提示）
4. `IsEmpty()` 直接返回
5. 非 `-y` 模式 → `ConfirmContinue`
6. `mgr.Pull(paths)` 执行（Phase A → Phase B）
7. 输出 `Pull complete.`

#### 1.12.4 `groot diff [path...]`

参数：
- `path...`：要比较的资源路径（可多个），省略时比较全部白名单资源
- `-h, --help`：显示帮助

只读，不修改任何文件，不做交互确认。

执行：
1. `mgr.Diff(paths)` 扫描
2. `FormatDiff(diff, "diff")` 输出（中性措辞，不含重启提示）

### 1.13 错误约定

| 错误 | 来源 | 处理 |
|---|---|---|
| `ErrSyncDisabled` | local 模式下调用任意方法 | cmd 层在调用前先看 `cfg.Storage.Minio == nil` 直接拦截，给更友好的提示 |
| `sync: empty path` / `path traversal` / `not in whitelist` | `ValidateSyncPath` | 直接返回给用户，提示路径非法 |
| `sync: path %q is inside a skill directory` | 同上 | 提示用户改用整个 skill 目录 |
| `sync push %s: ...` / `sync pull %s: ...` | pushOne/pullOne 包装 | 透传底层 `store.Write/Read/Delete` 错误 |
| `stat remote after push: ...` / `chtimes after push: ...` | mtime 锚定阶段 | 失败即整体失败（同步语义被破坏） |
| `ErrNotFound`（push 删远端、pull 删本地） | 幂等场景 | 视为成功，不返回错误 |

### 1.14 安全约束

- 路径遍历：拒绝任何含 `..` 的输入
- 白名单：只接受 `SyncableResourceRoots` 范围内的路径
- skill 目录原子性：拒绝直接操作 `skills/<skill>/<file>` 与 `subagents/<name>/skills/<skill>/<file>`
- 权限：sync 操作不修改文件 mode，pull 写入新文件统一为 `0644`，新建目录 `0755`
- 凭据隔离：MinIO 凭据从 env.yaml 加载，sync 模块不直接读 env

### 1.15 文件结构

```
internal/sync/
├── sync.go         SyncManager 接口、disabledSyncManager、localSyncManager、push/pull/file 操作
├── diff.go         DiffResult、ComputeDiff、walk*Files、joinPath/relPath
├── render.go       RenderDiff、FormatDiff、重启提示判定
├── resource.go     SyncableResourceRoots、ValidateSyncPath、isDirectSkillFile
├── resolver.go     ResolveLocalPaths（辅助函数，当前 cmd 链路未直接使用）
└── *_test.go       单元测试

internal/cmd/
├── push.go         PushFlags、ParsePushFlags、RunPush
├── pull.go         PullFlags、ParsePullFlags、RunPull
└── diff_cmd.go     DiffFlags、ParseDiffFlags、RunDiff
```

#### 1.15.1 resolver.go 的当前定位

`ResolveLocalPaths` 提供"类别目录展开为子项列表"的能力（`skills` → `skills/weather`、`skills/translator`...），但 `Push/Pull/Diff` 链路实际调用的是 `resolveSyncPaths`（仅校验，不展开），递归交给 `ComputeDiff` 的 walker。

**当前展开策略统一交由双侧 walker（`walkLocalFiles` + `walkRemoteFiles`）处理**，`ResolveLocalPaths` 仅作为可选辅助函数保留，不参与主流程。

### 1.16 可观测性

- 命令行直接 `fmt.Println` 输出关键状态（"Scanning differences..."、"Push complete."、"Cancelled."）
- 不写日志文件、不接入 message 模块
- 错误通过 `error` 链路向上层传递，由 `main.go` 统一打印并返回非零退出码

## 二、迭代说明

### 2.1 与上一版差异

本文档为 sync 模块首次独立成 spec。在此之前，sync 行为分散在：

- `docs/superpowers/specs/2026-06-01-storage-abstraction-and-minio-mode-design.md` §1.8（"集群共享配置同步"）—— 从存储抽象视角带过
- `docs/superpowers/plans/2026-06-08-sync-push-pull-diff.md` —— 实施计划，非设计

本次新增独立 spec 后，§1.8 的内容由本文档接管。后续 sync 模块的设计变更应以本文档为单一来源。

相对 storage spec §1.8 的内容补全：

- **新增**：完整的 SyncManager 接口（含 `CleanTmpResidue`）
- **新增**：路径遍历 / skill 原子性等安全约束（§1.14）
- **新增**：`*.tmp` 全链路过滤语义（§1.9.2）
- **新增**：mtime 锚定到远端 LastModified 的双侧规则（§1.8.2）
- **新增**：push / pull / diff 三种 render 输出 schema（§1.10）
- **新增**：重启提示仅 pull 输出，push 与 diff 沉默（§1.10.5）
- **新增**：CLI `-y/--yes` 跳过确认（§1.12.2 / §1.12.3）
- **新增**：disabledSyncManager 与 `ErrSyncDisabled`（§1.4.1 / §1.4.2）
- **新增**：resolver.go 的当前定位说明（§1.15.1）
- **新增**：差异 mtime 容差 = 1s（§1.6）
- **新增**：Phase A → Phase B 顺序保证（§1.9）

已知限制：

- §1.10.5 重启提示对 `subagents/<name>/skills/` 子级有假阳性（待细化）
- §1.12.2 push 链路两次扫描差异（执行时点为准）
