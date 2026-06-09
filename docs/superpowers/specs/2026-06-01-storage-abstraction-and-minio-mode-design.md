# 存储抽象与 MinIO 存储模式设计

**日期**：2026-06-06
**状态**：草案
**作者**：zfd81 + Claude

---

## 目录

- [一、功能设计](#一功能设计)
  - [1.1 背景与现状](#11-背景与现状)
  - [1.2 目标](#12-目标)
  - [1.3 为什么选 MinIO](#13-为什么选-minio)
  - [1.4 非目标（本期不做）](#14-非目标本期不做)
  - [1.5 核心设计思想](#15-核心设计思想)
  - [1.6 数据归类](#16-数据归类)
  - [1.7 各模块存储适配](#17-各模块存储适配)
    - [1.7.1 basePath 与 path 拼接规则](#171-basepath-与-path-拼接规则)
    - [1.7.2 memory 模块](#172-memory-模块)
    - [1.7.3 schedule 模块](#173-schedule-模块)
    - [1.7.4 cluster 模块](#174-cluster-模块)
  - [1.8 集群共享配置同步](#18-集群共享配置同步)
    - [1.8.1 资源对象定义](#181-资源对象定义)
    - [1.8.2 SyncManager 接口](#182-syncmanager-接口)
    - [1.8.3 diff 判等算法](#183-diff-判等算法)
    - [1.8.4 命令设计](#184-命令设计)
    - [1.8.5 命令默认行为](#185-命令默认行为)
    - [1.8.6 镜像同步语义与执行顺序](#186-镜像同步语义与执行顺序)
    - [1.8.7 受 sync 管理的资源对象白名单](#187-受-sync-管理的资源对象白名单)
    - [1.8.8 push/pull 后的生效方式](#188-pushpull-后的生效方式)
    - [1.8.9 典型运维流程](#189-典型运维流程)
  - [1.9 已知限制与后续清理项](#19-已知限制与后续清理项)
    - [1.9.1 Local.Rename 目录覆盖契约偏离](#191-localrename-目录覆盖契约偏离)
    - [1.9.2 minio mtime 秒级精度边界](#192-minio-mtime-秒级精度边界)
    - [1.9.3 attachment temp 目录路径耦合 memory](#193-attachment-temp-目录路径耦合-memory)
    - [1.9.4 main.go / chat.go basePath 分流逻辑重复](#194-maingo--chatgo-basepath-分流逻辑重复)
    - [1.9.5 Memory.Directory 自定义绝对路径与 attachment temp 解耦](#195-memorydirectory-自定义绝对路径与-attachment-temp-解耦)

---

## 一、功能设计

### 1.1 背景与现状

Groot 当前所有持久化数据均以本地文件系统存储在 `GROOT_HOME` 目录下：

- 节点本地配置：`env.yaml`（含 MinIO 等基础设施连接凭据，每节点独立）
- 集群共享配置：`config.yaml`、`skills/`、`subagents/`、`mcp/`、`GROOT.md`
- 运行时数据：`memory/`、`schedules/`、`cluster/`
- 运行日志：`logs/`

热加载机制：skills 和 GROOT.md 采用无缓存、按需读取策略（每次读取直接访问文件系统）。跨主机部署时各节点数据互相隔离。集群选举通过本地文件系统的成员目录心跳实现，仅在单主机多实例场景下有效。

### 1.2 目标

`Storage` 接口已在 [存储抽象层设计文档](2026-06-06-storage-interface-design.md) 中完成定义与 local / minio 两种实现。本期的核心工作是把 memory / schedule / cluster 三个业务模块接入这个接口：

1. **local 模式**：memory / schedule / cluster 三个模块的持久化操作改为走 `Storage` 接口（由 `storage.Local` 实现，底层仍为本地文件系统）。其他数据（集群共享配置、运行日志、附件请求级暂存）不受影响，文件读写方式与改造前一致。用户无感知，100% 向后兼容。
2. **minio 模式**：`Storage` 后端切换为 `storage.Minio`，运行时数据直接落在 MinIO bucket，跨节点共享。业务代码与 local 模式完全相同——它只认接口，不区分后端。此外新增 `groot push/pull/diff` 命令，用于集群共享配置在本地 HOME 与 MinIO 之间的显式同步。minio 模式通过 `~/.groot/env.yaml` 中的 `minio` 节启用。

### 1.3 为什么选 MinIO

| 能力 | 在本设计中的作用 |
|------|----------------|
| S3 兼容 API | `Storage` 接口的 minio 实现直接映射到 MinIO SDK |
| 对象元数据（LastModified） | cluster 心跳通过 `List` 读取 ModTime 判活，等效于文件 mtime |
| 流式读写 | 附件上传/下载零拷贝，直接透传 `io.Reader` |
| 部署成熟 | 单节点或分布式部署均可，运维成本低 |
| 跨节点天然共享 | 所有节点读写同一 bucket，无需额外同步机制 |

### 1.4 非目标（本期不做）

- 其他对象存储后端（S3、GCS 等）——`Storage` 接口预留了多后端扩展空间
- 集群共享配置的自动同步（运行时 polling MinIO）——改用主动 `push/pull` 命令
- 数据迁移工具（local → minio）

### 1.5 核心设计思想

minio 模式下，按数据特性采用三类策略：

- **运行时数据**（memory / schedule / cluster）：业务通过 `Storage` 接口**直接读写 MinIO**，不在本地缓存，跨节点强一致
- **集群共享配置**（config.yaml / skills / subagents / mcp / GROOT.md）：通过 `groot push/pull/diff` 命令在本地 HOME 与 MinIO 之间显式同步。业务运行时**从本地 HOME 读取**，使用方式与 local 模式完全一致。热加载由无缓存、按需读取策略提供
- **节点本地配置**（env.yaml）：每个节点独立维护，**不参与 sync**——它包含 MinIO 连接凭据（access_key、secret_key 等基础设施信息），是 push/pull 自身的前置条件，无法靠 sync 自身分发。env.yaml 的位置、格式与加载方式见 [存储抽象层设计文档 §1.6](2026-06-06-storage-interface-design.md)
- **启动 fail-fast**：minio 模式启动时执行 BucketExists 连通校验 + 探活式 PutObject/RemoveObject（写到保留前缀 `__startup/`），确保读写权限均满足。任一失败立即退出，避免运行时第一次 Write 才暴露

### 1.6 数据归类

```
GROOT_HOME/
├── logs/             永远本地（运行日志）
├── env.yaml          节点本地配置（含 MinIO 凭据，不参与 sync）
│
├── config.yaml       集群共享配置（HOME ⇄ MinIO 同步）
├── skills/           集群共享配置（HOME ⇄ MinIO 同步）
├── subagents/        集群共享配置（HOME ⇄ MinIO 同步）
├── mcp/              集群共享配置（HOME ⇄ MinIO 同步）
└── GROOT.md          集群共享配置（HOME ⇄ MinIO 同步）

# minio 模式下，以下数据不在 HOME，业务通过 Storage 接口直连 MinIO：
# memory/    跨节点会话数据
# schedules/ 任务定义与执行历史（leader 节点执行）
# cluster/   选举心跳协调
```

| 数据 | 类别 | local 模式 | minio 模式 | 是否参与 sync |
|------|------|-----------|-----------|------------|
| `logs/` | 运行日志 | HOME | HOME | ❌ |
| `env.yaml` | 节点本地配置 | 不存在 | HOME | ❌ |
| `config.yaml` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `skills/` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `subagents/` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `mcp/` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `GROOT.md` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `memory/` | 运行时数据 | `Storage`（local） | `Storage`（minio） | ❌ |
| `schedules/` | 运行时数据 | `Storage`（local） | `Storage`（minio） | ❌ |
| `cluster/` | 运行时数据 | `Storage`（local） | `Storage`（minio） | ❌ |


### 1.7 各模块存储适配

memory / schedule / cluster 三个模块的持久化操作统一走 `Storage` 接口。接口本身不感知路径拼接；路径由调用方（`main.go`）按 storage 类型注入 `basePath`，各模块内部在 `basePath` 上做相对拼接。

#### 1.7.1 basePath 与 path 拼接规则

启动时 `main.go` 按 storage 类型为每个模块计算 basePath：

| 模块 | local 模式 basePath（绝对路径） | minio 模式 basePath（object key 前缀） |
|------|-------------------------------|---------------------------------------|
| memory | `${GROOT_HOME}/memory` | `memory` |
| schedule | `${GROOT_HOME}/schedules` | `schedules` |
| cluster | `${GROOT_HOME}/cluster/members` | `cluster/members` |

业务代码内部用 `filepath.Join(basePath, ...)` 拼出完整 path，对两种 storage 类型透明。

- **local 模式**：path 是绝对路径，`storage.Local` 强制要求绝对路径，避免 cwd 漂移
- **minio 模式**：path 是 object key（形如 `memory/sessions/abc/history.json`），`storage.Minio` 直接当 key 使用

注：minio 模式下虽然写到对象存储，业务代码仍用 `filepath.Join` 拼路径——POSIX 风格的 `/` 分隔符与 S3 object key 命名约定一致，无需特殊处理。Windows 不在本期支持范围。

#### 1.7.2 memory 模块

memory 模块把所有文件读写改走 `Storage` 接口，业务层不再持有原子写逻辑：

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `saveHistory` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write`（接口内部原子写） |
| `SaveChatRecord` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `GetHistory` | `os.ReadFile` | `storage.Read` |
| `GetChatRecord` | `os.ReadFile` | `storage.Read` |
| `ExistsSession` | `os.Stat(history.json)` | `storage.Stat` + `errors.Is(ErrNotFound)` |
| `ListSessions` | `os.ReadDir(memoryDir)` | `storage.List(memoryDir)` |
| `CreateSession` | `os.MkdirAll(sessionDir/chatsDir)` | 仅调用 `saveHistory`（`Write` 自动建目录） |
| `Cleanup`（附件） | 已走 `storage.DeleteDir` | 不变 |
| `Cleanup`（元数据） | `os.RemoveAll(sessionDir)` | `storage.DeleteDir(sessionDir)`（递归含附件） |

注：`SaveAttachment` 已在前期接入 `storage.Write`，本期无需改动。

#### 1.7.3 schedule 模块

`schedule.Storage` 结构体由直接操作 `os.*` 改为持有 `storage.Storage` 接口实例：

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `SaveTask` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `SaveExecution` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `LoadTask` | `os.ReadFile` 遍历三个状态目录 | `storage.Read` 遍历，`ErrNotFound` 跳过 |
| `LoadExecutions` | `os.ReadDir` + `os.ReadFile` | `storage.List` + `storage.Read` |
| `listTasksIn` | `os.ReadDir` + `os.ReadFile` | `storage.List` + `storage.Read` |
| `MoveTask` | `os.Rename(srcDir, dstDir)` | `storage.Rename`（minio 走目录级补偿流程，详见 [存储抽象层设计](2026-06-06-storage-interface-design.md) §1.12.2） |
| `DeleteTask` | `os.RemoveAll` | `storage.DeleteDir` |
| `EnsureDirs` | `os.MkdirAll` × 3 | local 模式预建 active/disabled/archive 三目录；minio 模式 noop（`Write` 自动建前缀） |
| `GetTaskStatus` | `os.Stat` 三连 | `storage.Stat` 三连 |

#### 1.7.4 cluster 模块

cluster 模块的心跳协调通过 `Storage` 接口读写成员文件：

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `WriteRegistration` | `os.WriteFile` | `storage.Write`（内容格式不变：`role\|host:port\|pid`） |
| `ListMembers` | `os.ReadDir` + `entry.Info().ModTime()` | `storage.List(membersDir)` → 读取 `FileInfo.ModTime` |
| `RemoveFile` | `os.Remove` | `storage.Delete`（`ErrNotFound` 视为已删除） |
| `EnsureMembersDir` | `os.MkdirAll` | local 模式预建目录；minio 模式 noop |
| `heartbeat` 自检（`os.Stat(ownPath)`） | `os.Stat` | `storage.Stat` + `errors.Is(ErrNotFound)` |

心跳判活仍以 `FileInfo.ModTime` 为锚——local 模式下是文件系统 mtime，minio 模式下是 MinIO `LastModified`，语义一致；在 ±秒级精度差异内不影响 `heartbeatTimeout = 7s` 的判定。


### 1.8 集群共享配置同步

minio 模式下，集群共享配置通过 `groot push/pull/diff` 命令在本地 HOME 与 MinIO 之间同步。业务运行时仍直接读本地 HOME 文件。

#### 1.8.1 资源对象定义

sync 操作的最小单位是"资源对象"。不同类型的资源定义不同的资源对象：

| 资源类型 | 资源对象 | 说明 |
|---------|---------|------|
| config.yaml | 文件本身 | 集群共享主配置文件 |
| GROOT.md | 文件本身 | 系统指令文件 |
| skills | skill 目录 | 不支持单独操作 SKILL.md，必须操作整个目录 |
| mcp | 单个 JSON 文件 | mcp 配置文件是独立资源对象 |
| subagents | 子 Agent 目录 | 递归推送：含 agent.md、skills/ 下所有 skill 目录、mcp/ 下所有 JSON 文件 |
| subagents/{name}/agent.md | 单个 Markdown 文件 | 子 Agent 定义文件，可单独操作 |
| subagents/{name}/skills | skill 目录 | 与全局 skills 规则一致：必须操作整个目录 |
| subagents/{name}/mcp | 单个 JSON 文件 | 与全局 mcp 规则一致：独立资源对象 |

**特殊资源对象：`subagents/{name}/agent.md`**

子 Agent 的定义文件是独立的文件级资源对象，可以单独 push/pull：

```bash
groot push subagents/db-agent/agent.md  # 只推送定义文件
groot push subagents/db-agent           # 推送整个子 Agent（递归：含 agent.md、skills/、mcp/）
```

**资源对象层级结构**：

```
config.yaml (文件)
GROOT.md (文件)
├─ skills/ (类别)
│  └─ weather/ (目录资源对象)
│     └─ SKILL.md (非资源对象，随目录同步)
├─ mcp/ (类别)
│  └─ database.json (文件资源对象)
└─ subagents/ (类别)
   └─ db-agent/ (目录资源对象)
      ├─ agent.md (文件资源对象，可单独操作)
      ├─ skills/ (子类别)
      │  └─ weather/ (目录资源对象)
      │     └─ SKILL.md (非资源对象)
      └─ mcp/ (子类别)
         └─ database.json (文件资源对象)
```

**禁止操作**：

- ❌ `groot push skills/weather/SKILL.md` — 必须操作 `skills/weather/` 目录
- ❌ `groot push subagents/db-agent/skills/sql/SKILL.md` — 必须操作 `subagents/db-agent/skills/sql/` 目录
- ❌ `groot push env.yaml` — env.yaml 不在 sync 白名单，命令拒绝（含 MinIO 凭据，节点本地）

**目录递归规则**：

- 目录资源对象：`groot push subagents/db-agent` 递归推送该子 Agent 下所有可 sync 资源
- 类别目录：`groot push subagents/db-agent/skills` 递归推送该目录下所有 skill 目录
- 文件资源对象：`groot push subagents/db-agent/agent.md` 只推送这一个文件
- 递归深度根据资源对象层级自然终止，无需手动指定深度参数

#### 1.8.2 SyncManager 接口

```go
// internal/sync/sync.go

type SyncManager interface {
    Push(paths []string) error                        // HOME → MinIO
    Pull(paths []string) error                        // MinIO → HOME
    Diff(paths []string) (DiffResult, error)          // 显示差异，不修改
}

type DiffResult struct {
    Added    []string   // 本地有，远端没有
    Modified []string   // 本地和远端不同
    Removed  []string   // 远端有，本地没有
    Same     []string   // 一致
}
```

local 模式下 `SyncManager` 不可用，命令提示"未启用 minio 模式"。

**实现分工**：`internal/sync/` 包内同时操作"本地 HOME"与"远端 MinIO"两侧。本地侧直接走 `os.*`（HOME 永远是本地文件系统，不会切到别的存储后端，无抽象需要）；远端侧通过 `Storage`（minio 实现的接口实例）操作 MinIO，**不直接 import minio-go**——保证 sync 模块与 storage 实现的解耦。

#### 1.8.3 diff 判等算法

push/pull 在执行前都要先扫描双边差异。判等采用 **size + mtime** 双字段比较：

```
- 双边 size 不同 → 直接判定 Modified
- size 相同，mtime 不同 → 判定 Modified
- size 与 mtime 均相同 → 判定 Same
```

mtime 比较允许 ±1s 误差（MinIO LastModified 与本地文件系统 mtime 精度差异）。

本地侧与 MinIO 侧均通过 `Storage.Stat` 获取 size / mtime 后直接比较，不做 hash 计算，不修改 `Storage` 接口与 `FileInfo` 结构体。

**push/pull 完成后必须把本地 mtime 锚定到远端 LastModified。** 这一步是判等算法在生产中工作的关键前提：

- 本地 `os.FileInfo.ModTime()` 含义是"文件内容最后修改时间"
- MinIO `LastModified` 含义是"object 上传时间"
- 同一个文件做 push 时，远端 LastModified 必然晚于本地 mtime（差几十秒到几分钟很正常），如果 sync 完不锚定，下一次 diff 就会把刚刚 push 过的文件错误判为 Modified

实现层面：
- `pushFile` 写完远端后立即 `Storage.Stat` 拿到 LastModified，再 `os.Chtimes(localPath, t, t)` 锚定
- `pullFile` 入口 `Storage.Stat` 拿到 LastModified，写完本地 rename 之后 `os.Chtimes(localPath, t, t)` 锚定

锚定后两侧 mtime 完全一致（在 1s 容差内），后续 diff 直接判 Same。用户后续编辑文件时本地 mtime 会被 OS 自然更新，diff 检测到偏离锚点 → 判 Modified → 提示用户该 push，符合预期。

**已知副作用：** push/pull 后本地文件的 `ls -l` / `stat` 显示的"修改时间"是上传/拉取时间，不再反映用户最后一次编辑的时间。运维若依赖 mtime 判断"我什么时候改的"会有误差。这是 sync 工具显式表达"该文件已与远端对齐"的正确语义，无规避方案。

**不识别冲突。** 当前判等只能给出"一致 / 不一致"的二元事实，不区分本地改还是远端改，更不能识别"双侧都改"的冲突场景：
- `groot push` 把所有 Modified 文件无脑推到远端，可能覆盖远端他人改动
- `groot pull` 反向同理，可能覆盖本地未推送的修改

工具假设的工作模型是"运维在一台节点集中编辑，push 出去，其他节点 pull 接收"的**单写多读**场景，方向由命令名（`push`/`pull`）显式表达。多写多机同时编辑同一资源不在本期支持范围；如未来需要冲突检测，再引入"本地账本"记录上次 sync 时双侧时间，diff 时三方比较（本地当前 vs 账本 vs 远端当前）。

#### 1.8.4 命令设计

```bash
# 显示本地和 MinIO 差异（只读，不修改）
groot diff [path...]

# 推送本地到 MinIO
groot push [path...]
groot push                                    # 默认推送所有受 sync 管理的资源
groot push config.yaml                        # 推送主配置文件
groot push skills                             # 推送整个 skills/
groot push skills/weather                     # 推送单个 skill 目录
groot push subagents                          # 推送整个 subagents/
groot push subagents/db-agent                 # 推送单个子 Agent（递归）
groot push subagents/db-agent/agent.md        # 只推送定义文件
groot push subagents/db-agent/skills          # 推送该子 Agent 的所有 skills（递归）
groot push subagents/db-agent/skills/weather  # 推送该子 Agent 的单个 skill
groot push mcp                                # 推送整个 mcp/
groot push mcp/database.json                  # 推送单个 mcp 配置
groot push GROOT.md                           # 推送系统指令文件
groot push skills subagents mcp               # 推送多个类别

# 从 MinIO 拉取到本地（参数同 push）
groot pull [path...]

# 保留现有命令
groot status                                  # 查看运行实例状态（与 sync 无关）
groot skill install/uninstall/list            # 仅操作本地 HOME
```

**命令名说明**：现有 `groot status` 已用于"运行实例状态查询"，不与 sync 复用。集群共享配置的 diff 使用独立动词 `groot diff`，语义明确、无歧义。

#### 1.8.5 命令默认行为

```
1. 扫描差异（本地 vs MinIO）
2. 显示完整 diff：
   - Added: [资源对象列表]
   - Modified: [资源对象列表]
   - Removed: [资源对象列表]
3. 提示：Continue? (y/n)
4. 等待用户输入
   - y → 执行同步
   - n 或 Ctrl+C → 取消
```

**交互示例**：

```bash
$ groot push skills

Scanning differences...

Changes to push (HOME → MinIO):
  Added:
    skills/weather/SKILL.md
    skills/weather/handler.go
  Modified:
    skills/translator/SKILL.md
  Removed:
    skills/deprecated/old.md

Continue? (y/n): _
```

#### 1.8.6 镜像同步语义与执行顺序

**同步语义**：

```
push:  HOME → MinIO
  - 本地新增 → MinIO 新增
  - 本地修改 → MinIO 覆盖
  - 本地删除 → MinIO 删除（镜像）

pull:  MinIO → HOME
  - MinIO 新增 → HOME 新增
  - MinIO 修改 → HOME 覆盖
  - MinIO 删除 → HOME 删除（镜像）
```

镜像同步保证删除操作能传播，不留"幽灵文件"。

**push 执行策略**：逐资源对象原子。单次命令对每个资源对象的操作（新增 / 修改 / 删除）独立提交。多资源对象之间不保证整体事务——先成功的已写入，失败的资源对象在 diff 输出中标识，重新执行 push 即可补齐。MinIO 不支持跨 object 事务，这是 S3 协议的客观限制。

**pull 执行策略**：先写后删 + 单文件原子。

```
Phase A: 写入所有"新增"和"修改"的本地文件
  对每个目标文件：
    1. 写到 ${目标路径}.tmp（同目录，保证 rename 在同文件系统内原子）
    2. fsync
    3. rename 到目标路径
  Phase A 中途失败：已写入的部分保留为完整内容；未写入的本地文件保持原样
  → 业务读取得到的要么是旧版本、要么是新版本，不会读到半成品

Phase B: 删除所有"远端已不存在"的本地文件
  仅在 Phase A 全部成功后才执行
  Phase B 中途失败：已删的不可逆，未删的下次 pull 自动补齐
```

**严格遵守 Phase A → Phase B 顺序**：先删后写的顺序在中途崩溃时会同时丢失被删文件和未写完的新文件——既丢已 pull 的内容、又没拿到新内容；先写后删保证任意中断点本地都至少有一份完整内容（可能是旧版本，下次 pull 收敛即可）。

**pull 启动时的 tmp 残留清理**：上次 pull Phase A 中途崩溃可能留下 `*.tmp` 文件。pull 命令在扫描差异**之前**先递归遍历目标目录，删除所有名为 `*.tmp` 的文件，确保新一轮 pull 从干净状态开始。

**关于 sync 路径下的文件管控**：sync 工具对受 sync 管理的路径（白名单内）拥有完整管控权——pull 启动时清理 `*.tmp`、push 镜像同步删除"远端不存在的本地文件"，都会在没有提示的情况下移除文件。**用户不应在白名单内的目录下放置任何与同步无关的文件**，尤其不应放置以 `.tmp` 结尾的文件，否则会在每次 pull 启动时被清理。这一约束适用于 `skills/`、`subagents/`、`mcp/`、`config.yaml`、`GROOT.md` 全部 sync 范围。

| 保证维度 | push | pull |
|---------|------|------|
| 整体原子（一次命令所有变更同生死） | ❌ MinIO 不支持 | ❌ 多文件之间不原子 |
| 单文件原子（单个文件写到一半被中断不会留半成品） | ✅ MinIO PUT 原子 | ✅ rename 系统调用 |
| 失败可恢复 | ✅ 重新执行 push | ✅ 重新执行 pull（启动时清 tmp，先写后删保证不丢内容） |

#### 1.8.7 受 sync 管理的资源对象白名单

```go
var SyncableResourceRoots = []string{
    "config.yaml",
    "skills",
    "subagents",
    "mcp",
    "GROOT.md",
}
```

push / pull 仅处理白名单内的资源对象。`env.yaml` **不在白名单**——它属于节点本地配置，含 MinIO 凭据，每个节点独立维护，是 push/pull 自身的前置条件，无法靠 sync 自身分发。`memory/` `schedules/` `cluster/` 也不在白名单——它们是运行时数据，由 `Storage` 接口直连 MinIO，不需要 sync。

#### 1.8.8 push/pull 后的生效方式

| 资源对象 | pull 后是否立即生效 | 说明 |
|---------|------------------|------|
| `config.yaml` | ❌ 需重启 | 主配置在启动期加载，运行时不重读 |
| `skills/<skill>/` | ✅ 立即生效 | eino Backend 无缓存，pull 写入后下次读取自动获取 |
| `subagents/<name>/skills/<skill>/` | ✅ 立即生效 | 子 Agent 实例运行时读 skill 内容，与全局 skills 同样走 eino Backend 无缓存路径 |
| `GROOT.md` | ✅ 立即生效 | grootmd 模块按需读取 |
| `mcp/<server>.json` | ❌ 需重启 | MCP 配置不支持热加载 |
| `subagents/<name>/agent.md` | ❌ 需重启 | 子 Agent 入口在启动期固化进 entry.Tool |
| `subagents/<name>/mcp/<server>.json` | ❌ 需重启 | 同上 |
| `subagents/<name>/`（目录新增/删除） | ❌ 需重启 | 子 Agent 注册仅在启动期扫描入口 |

**关于 skills 的热加载差异**：所有 skills（主 Agent 与子 Agent 共用）的 SKILL.md 与脚本内容均通过 eino Backend 在运行期按需读取，无缓存层，pull 后下次执行自动可见新内容。子 Agent 的 **入口**（agent.md / mcp 配置）则在启动期固化进 `entry.Tool` 注册表，pull 后必须重启才能感知。两者是不同生命周期的资源，分别对待。

push / pull 命令在执行结束时输出提示——若本次同步涉及"需重启"类资源，提示用户手动重启服务。

#### 1.8.9 典型运维流程

```
单主机多实例（同 HOME）：
   1. 运维编辑 ~/.groot/skills/x/SKILL.md
   2. groot push                 # 同步到 MinIO（同主机其他节点共享 HOME，eino Backend 自动感知）
   3. 视资源类型决定是否重启

跨主机多实例：
   1. 运维在 host-A 编辑 ~/.groot/skills/x/SKILL.md
   2. host-A: groot push          # 同步到 MinIO
   3. host-B/host-C: groot pull   # 各自从 MinIO 拉取到本地 HOME
   4. 视资源类型决定是否重启各节点
```

新节点接入是部署运维步骤（创建 env.yaml、首次 pull、启动），不属于本期开发范围。


### 1.9 已知限制与后续清理项

本节记录运行时数据接入 Storage 抽象层（Plan A，2026-06-08）实施过程中发现但未在本期修复的偏离与限制。每条都给出场景、风险评估与后续修复方向，作为下次迭代的入口。

#### 1.9.1 Local.Rename 目录覆盖契约偏离

`Storage.Rename` 接口契约写"dst 已存在时按覆盖语义处理（实现负责清理）"，但 [internal/storage/local.go](../../../internal/storage/local.go) 当前直接走 `os.Rename`：POSIX `rename(2)` 对**非空目录**返回 `ENOTEMPTY/EEXIST`，**不覆盖**。`schedule.MoveTask` 在 dst 残留场景下会失败而非覆盖，与接口契约不一致。

- **影响**：`MoveTask` 在重启恢复 / 异常中断后的幂等性受影响。当前 `internal/schedule/storage_test.go::TestMoveTask_DstAlreadyExists` 断言的是"失败 + 两侧数据保留"行为（实测真相），不是接口契约。
- **风险评估**：低。`schedule.MoveTask` 主流程在写入新 task 前 caller 应保证目标目录不存在；只有异常恢复路径会触发，且失败可重试。
- **修复方向**：`Local.Rename` 目录场景下先 `os.RemoveAll(dst)` 再 `os.Rename`，与 `Minio.renameDir` 的 Phase 0 清理逻辑对齐。注意需要更新 `TestMoveTask_DstAlreadyExists` 测试断言。

#### 1.9.2 minio mtime 秒级精度边界

`MemberInfo.Mtime` 在 local 模式来自 fs mtime（纳秒精度），minio 模式来自 S3 `LastModified`（秒级精度）。`heartbeatTimeout = 7s` 边界附近理论上可能产生 ±1s 的判活误差。

- **影响**：minio 模式下网络抖动 + 心跳延迟叠加时，可能误判活节点为过期，触发 leader 错误清理。被清节点在下一轮自检 ErrNotFound 时会自动 re-register 自愈。
- **风险评估**：低。有自愈机制兜底，无数据损坏，无 split-brain。
- **修复方向**：把 `heartbeatTimeout` 调到 `≥ 2 × heartbeatInterval + 1s` 缓冲，降低 false positive 概率。属于运维参数调优，无需代码改造。

#### 1.9.3 attachment temp 目录路径耦合 memory

`attachment.NewHandler` 的本地暂存目录写在 `${homeDir}/memory/temp/{taskID}/`，这是历史耦合（旧版 attachment 复用 memory 路径），与 attachment 自身职责不直接相关。

- **影响**：可读性下降。新读者会以为 attachment 与 memory 模块有隐式联系。
- **风险评估**：极低。纯命名问题，行为正确。
- **修复方向**：单独发一个 commit 把 attachment temp 目录迁到 `${homeDir}/attachments/temp/`，需要附带运维迁移脚本（清理旧 `memory/temp/` 残留）。不要混进其它改动。

#### 1.9.4 main.go / chat.go basePath 分流逻辑重复

`cmd/groot/main.go` 与 `internal/cmd/chat.go` 都有相同的 `if cfg.Storage.Minio != nil { ... } else { ... }` basePath 分流块。当前两个 callsite 内联各自维护。

- **影响**：DRY 违反。如果增加第三个 entry point 或第四个模块的 basePath，重复成本会上升。
- **风险评估**：低。两份代码都很短（一个 4 行 if/else，一个 11 行），同步成本可接受。
- **修复方向**：第三个 callsite 出现时再抽 `config.RuntimePaths(cfg, homeDir) struct{ Memory, Schedule, ClusterMembers string }`。当前不抽。

#### 1.9.5 Memory.Directory 自定义绝对路径与 attachment temp 解耦

旧实现下，如果用户把 `cfg.Memory.Directory` 配置为绝对路径（如 `/data/memory`），attachment temp 会跟着落到 `/data/memory/temp/`。Plan A 改造后 attachment temp 始终落在 `${homeDir}/memory/temp/`，与 `cfg.Memory.Directory` 解耦。

- **影响**：行为变化。把 memory 数据放在主目录之外的用户，attachment temp 仍留在主目录。
- **风险评估**：低。temp 目录是上传 base64 中转，纯临时、最终归宿是 `Storage` 接口下的附件存储；本地 temp 落点对运维监控基本无意义。
- **修复方向**：与 1.9.3 一并处理（新独立 attachment temp 路径），不再蹭 memory.Directory。如确有用户需要可配置，未来给 attachment 单独加配置项。


---
