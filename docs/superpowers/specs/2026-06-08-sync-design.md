# sync 模块设计文档

**日期**：2026-06-08（初版）/ 2026-06-10（迁移到数据库后端后重写）
**状态**：实现稿

## 一、功能设计

### 1.1 功能概述

sync 模块负责本地 HOME 目录（`~/.groot/`）与数据库 `shared_resources` 表之间的"集群共享配置"双向镜像同步，并通过 `groot push` / `groot pull` / `groot diff` 三个子命令暴露给用户。

它的存在是为了在多实例集群部署下（MySQL / PostgreSQL 模式），让所有节点共享同一份"配置 / 技能 / 子 Agent / MCP / GROOT.md"等可同步资源——本地编辑后通过 push 推到数据库，新节点或落后节点通过 pull 把远端最新版镜像到本地。

仅在 MySQL / PostgreSQL 模式下可用；SQLite 模式下三个命令统一返回 `ErrSyncDisabled`。

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

- `env.yaml` — 含数据库凭据，按节点本地维护
- `groot.db` — SQLite 数据库文件，不参与同步
- `logs/` — 日志，按节点隔离
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
func NewSyncManager(homeDir string, r repo.ResourceRepo) SyncManager
```

仅以 `r == nil` 作为禁用判据：

- `r == nil` → 返回 `disabledSyncManager`，所有方法返回 `ErrSyncDisabled`
- `r != nil` → 返回可用的 `localSyncManager`

[`repofactory.NewRepos`](../../../internal/repo/repofactory) 在 SQLite dialect 下把 `Resource` 字段绑定到 [`resourcelocal.New(homeDir)`](../../../internal/repo/resourcelocal/resource.go)（落本地文件系统的实现），在 MySQL/PostgreSQL dialect 下绑定到 [`resourcedb.New(...)`](../../../internal/repo/resourcedb/resource.go)。两种情况下 `r` 都是非 nil 的，因此 `disabledSyncManager` 实际不会被构造出来——SQLite 模式下 sync 命令也会执行流程，只是它做的是 local-vs-local 镜像。

#### 1.4.2 ErrSyncDisabled

```go
var ErrSyncDisabled = errors.New("sync: 仅在 MySQL/PostgreSQL 模式下可用 — 请在 env.yaml 中配置 database 节")
```

#### 1.4.3 paths 参数

`paths` 为相对路径列表（相对 `homeDir`），传 `nil` 或空切片表示"操作全部白名单根"。

`paths` 不做"类别目录展开"——目录整体交给 `ComputeDiff`，由它递归扫描双侧（local 与 remote）的并集，否则会漏掉只在远端存在的子项。

### 1.5 DiffResult 结构与语义

```go
type DiffResult struct {
    Added    []string // 本地有，远端没有
    Modified []string // 双侧都有但 size 或 content_hash 不同
    Removed  []string // 远端有，本地没有
    Same     []string // 一致
}
```

**语义恒定**：`Added` / `Removed` 始终以"本地 vs 远端"为锚，与命令方向无关。push / pull / diff 三个命令在渲染层根据自己的语义重新解释这四组（见 §1.10）。

`IsEmpty()` 仅看 `Added + Modified + Removed`，`Same` 不参与判断。

### 1.6 Diff 算法（`ComputeDiff`）

输入：`r repo.ResourceRepo, localBase string, paths []string`
输出：`DiffResult`

对每个 path：

1. **本地侧 walk**（`walkLocalFiles`）
   - `os.Stat(absPath)`：不存在则返回空 map（不报错）
   - 文件：直接收录，但 `*.tmp` 文件**全链路过滤**（diff/push/pull 一律不视作可同步对象，因为它们是 sync 自身原子写中转的临时产物）
   - 目录：`filepath.WalkDir` 递归收录所有非目录、非 `*.tmp` 的文件
   - 对每个本地文件读取内容并计算 SHA-1（`crypto/sha1`），同时记录 size

2. **远端侧 list**
   - `repo.List(ctx, rel)` 拿到 `[]*ResourceEntry`，每条含 `Path` / `Size` / `ContentHash` / `UpdatedAt`
   - `ErrNotFound` 视为空切片

3. **集合比较**
   - 双侧文件按相对路径建 map，相对路径统一用 `/` 分隔（`filepath.ToSlash`）
   - 仅本地存在 → `Added`
   - 仅远端存在 → `Removed`
   - 双侧都存在：
     - `localInfo.size != remote.Size` → `Modified`
     - `localInfo.hash != remote.ContentHash` → `Modified`
     - 否则 → `Same`

**判等维度：size + content_hash（SHA-1 hex 40 字符）**。mtime 仅作为远端资源的"新旧"参考字段保留显示，不参与判等。

### 1.7 path 约定

- **远端 path**：相对 `~/.groot/` 的路径，如 `skills/weather/SKILL.md`、`subagents/qa/skills/lint/run.sh`、`GROOT.md`、`config.yaml`。直接作为 `shared_resources.path` 主键值，**大小写敏感**（MySQL 通过 `utf8mb4_bin` 保证；PG 默认敏感）
- **本地路径**：`filepath.Join(localBase, filepath.FromSlash(rel))`，按 OS 分隔符
- **相对路径键**：`filepath.Rel(localBase, path)` 后 `filepath.ToSlash`，永远用 `/` 分隔

### 1.8 push 流程

`Push(paths) error`：

1. `resolveSyncPaths(paths)` 校验白名单
2. `ComputeDiff` 计算差异
3. 推送 `Added`：`pushOne` 写远端
4. 推送 `Modified`：`pushOne` 写远端
5. 删除 `Removed`：`repo.Delete(ctx, path)`，`ErrNotFound` 视为成功（幂等）

#### 1.8.1 pushOne 流程

```go
func (m *localSyncManager) pushOne(ctx context.Context, rel string) error {
    localPath := filepath.Join(m.homeDir, filepath.FromSlash(rel))
    content, err := os.ReadFile(localPath)
    if err != nil { return ... }
    return m.repo.Put(ctx, &repo.Resource{
        Path:    rel,
        Content: content,
        Size:    int64(len(content)),
    })
}
```

`pushOne` 只填 `Path` / `Content` / `Size` 三个字段，`ContentHash` / `ContentType` / `UpdatedAt` 由调用 `repo.Put` 时透传零值（[sync.go:124](../../../internal/sync/sync.go) → [resourcedb/resource.go:31](../../../internal/repo/resourcedb/resource.go)）。`SHA1Hex` helper 在 `resourcedb` 包中以 `SHA1Hex(content)` 暴露，但当前 push 链路未调用。

### 1.9 pull 流程

`Pull(paths) error`：

1. `resolveSyncPaths(paths)` 校验
2. **`cleanTmpFiles`**（best-effort）：递归删除 `paths` 范围下所有 `*.tmp` 残留。失败时记录但不阻塞 pull
3. `ComputeDiff` 计算差异
4. **Phase A — 写入**（任何中断都保证本地至少有一份完整内容）：
   - `Removed`（远端有 / 本地没有）→ `pullOne` 写本地
   - `Modified`（双侧不同）→ `pullOne` 覆盖本地
5. **Phase B — 删除**（必须严格在 Phase A 全部成功后）：
   - `Added`（本地有 / 远端没有）→ `os.Remove(localPath)`，`os.IsNotExist` 视为成功

Phase A → B 顺序保证："先删后写"中途崩溃的空窗不会出现。

#### 1.9.1 pullOne 流程

```go
res, _ := m.repo.Get(ctx, rel)
localPath := filepath.Join(m.homeDir, filepath.FromSlash(rel))
writeAtomic(localPath, res.Content)   // tmp+rename
```

`writeAtomic` 实现：

```
os.MkdirAll(dir(localPath), 0755)
tmp := localPath + ".tmp"
os.Remove(tmp)                          // 清理孤儿 tmp
open(tmp, O_WRONLY|O_CREATE|O_TRUNC, 0644)
write(data) → sync() → close()          // 失败任意一步删 tmp 后返回
os.Rename(tmp, localPath)
```

写失败时清理 tmp 文件；rename 失败时保留 tmp 等待下次 pull 清理（CleanTmpResidue / cleanTmpFiles）。

`writeAtomic` 完成后不修改本地 mtime——diff 比对维度为 size+SHA-1，本地 mtime 不参与判等。

#### 1.9.2 *.tmp 全链路语义

| 阶段 | 行为 |
|---|---|
| pull 启动前 | `cleanTmpFiles` 删除 `paths` 范围所有 `*.tmp` |
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
- `path` 以 `mcp/` 或 `subagents/` 前缀开始

push 与 diff 不输出重启提示。

### 1.11 ConfirmContinue 交互

`ConfirmContinue(r io.Reader, w io.Writer) bool`：

- 在 `w` 写 `Continue? (y/n): `
- 从 `r` 读一行
- 转小写并 trim 后，仅 `y` 或 `yes` 返回 true，其余（含 EOF / 非 tty）返回 false

### 1.12 命令行接口

#### 1.12.1 通用前置流程

三个命令的入口（`cmd/groot/main.go::openSyncRepo`）按相同顺序：

1. `homeDir := cmd.GetDefaultHome()`
2. `cfg := config.Load(homeDir)`
3. `db.Open(cfg.Database, homeDir)` 初始化数据库连接（SQLite / MySQL / PostgreSQL 任意一种均可）
4. `repofactory.NewRepos(sqlxDB, dialect, homeDir)` 构造 `ResourceRepo`：SQLite dialect 下绑定到 `resourcelocal`，MySQL/PG dialect 下绑定到 `resourcedb`
5. `mgr := sync.NewSyncManager(homeDir, repos.Resource)`

SQLite 模式下 `repos.Resource` 仍是非 nil 的 `resourcelocal` 实例，sync 命令会执行 local-vs-local 镜像；MySQL/PG 模式下走 `resourcedb` 实现，与 `shared_resources` 表交互。

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

push 链路扫描两次差异（确认前 `Diff` 一次 + `Push` 内部 `ComputeDiff` 一次），确认期间发生的内容漂移以执行时点的扫描为准。

#### 1.12.3 `groot pull [path...] [-y]`

参数与 push 相同。

执行：
1. `mgr.CleanTmpResidue(paths)`（best-effort，错误吞掉）
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
| `ErrSyncDisabled` | `r == nil` 时调用 `disabledSyncManager` 任意方法 | 透传到 main.go 由用户看到 |
| `sync: empty path` / `path traversal` / `not in whitelist` | `ValidateSyncPath` | 直接返回给用户，提示路径非法 |
| `sync: path %q is inside a skill directory` | 同上 | 提示用户改用整个 skill 目录 |
| `sync push %s: ...` / `sync pull %s: ...` | pushOne/pullOne 包装 | 透传底层 `repo.Put/Get/Delete` 错误 |
| `repo.ErrNotFound`（push 删远端、pull 删本地） | 幂等场景 | 视为成功，不返回错误 |

### 1.14 安全约束

- 路径遍历：拒绝任何含 `..` 的输入
- 白名单：只接受 `SyncableResourceRoots` 范围内的路径
- skill 目录原子性：拒绝直接操作 `skills/<skill>/<file>` 与 `subagents/<name>/skills/<skill>/<file>`
- 权限：sync 操作不修改文件 mode，pull 写入新文件统一为 `0644`，新建目录 `0755`
- 凭据隔离：数据库凭据从 env.yaml 加载，sync 模块不直接读 env

### 1.15 文件结构

```
internal/sync/
├── sync.go         SyncManager 接口、disabledSyncManager、localSyncManager、push/pull 流程
├── diff.go         DiffResult、ComputeDiff、walkLocalFiles
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

`ResolveLocalPaths` 提供"类别目录展开为子项列表"的能力（`skills` → `skills/weather`、`skills/translator`...），是一个独立的辅助函数。`Push/Pull/Diff` 链路调用的是 `resolveSyncPaths`（仅校验，不展开），递归展开交给 `ComputeDiff` 内部的 walker（`walkLocalFiles` + `ResourceRepo.List`）处理。`ResolveLocalPaths` 不参与主 sync 流程，仅自带单元测试覆盖。

### 1.16 可观测性

- 命令行直接 `fmt.Println` 输出关键状态（"Scanning differences..."、"Push complete."、"Cancelled."）
- 不写日志文件、不接入 message 模块
- 错误通过 `error` 链路向上层传递，由 `main.go` 统一打印并返回非零退出码

## 二、迭代说明

### 2.1 与上一版差异

历史版本基于 MinIO 对象存储 + size+mtime 容差实现，文档详见 [`archive/2026-06-08-sync-design.md`](archive/2026-06-08-sync-design.md)。本版相对上一版的差异：

#### 持久化抽象

- **新增**：sync 模块走 `repo.ResourceRepo` 接口（`Put` / `Get` / `Stat` / `List` / `Delete`）
- **退役**：`storage.Storage` 接口在 sync 模块内的全部使用；`internal/storage/` 整包退役
- **新增**：`internal/repo/resourcedb/` 实现 `ResourceRepo`（数据库后端），`internal/repo/resourcelocal/` 实现本地文件系统占位

#### 数据载体

- **调整**：远端从 MinIO bucket 内的对象迁移到 `shared_resources` 表中以 `path` 为主键的行
- **调整**：远端内容存储从 MinIO 对象字节流迁移到 `shared_resources.content`（MySQL `LONGBLOB` / PG `BYTEA`）；任意字节流（含可执行二进制）原样存储
- **新增**：`shared_resources.content_hash` 列存 SHA-1 hex（40 字符）
- **新增**：`shared_resources.size` / `updated_at` 列冗余存放，避免反复 `LENGTH(content)`

#### Diff 比对

- **调整**：判等维度从"size + mtime ± 1s 容差"改为"size + SHA-1 hex"
- **退役**：`mtimeTolerance = time.Second` 常量及相关比较逻辑
- **退役**：push/pull 完成后 `os.Chtimes(localPath, mtime, mtime)` 锚定本地 mtime 的步骤——hash 比对天然免疫时钟漂移

#### 配置入口

- **退役**：`env.yaml` 中的 `minio` 节
- **新增**：`env.yaml` 中的 `database` 节决定后端
- **调整**：`ErrSyncDisabled` 错误信息更新为 `"sync: 仅在 MySQL/PostgreSQL 模式下可用 — 请在 env.yaml 中配置 database 节"`

#### 命令措辞

- 当前 `RenderDiff` 的输出措辞仍沿用 `HOME → MinIO` / `MinIO → HOME` / `HOME ↔ MinIO`，与底层后端从 MinIO 迁移到数据库的事实存在偏差，但不影响功能正确性
- diff 中性分组的 header 仍写为 `Modified (size or mtime differs)`；实际比对维度是 size + content_hash，header 文案与判等维度存在偏差

#### 保留不变

- SyncManager 接口的 `Push` / `Pull` / `Diff` / `CleanTmpResidue` 方法签名
- 白名单根 `SyncableResourceRoots`、路径遍历防护、skill 目录原子性约束
- `*.tmp` 全链路过滤语义
- push / pull / diff 三种渲染输出 schema
- 重启提示仅 pull 输出
- CLI `-y/--yes` 跳过确认
- Phase A → Phase B 顺序保证
