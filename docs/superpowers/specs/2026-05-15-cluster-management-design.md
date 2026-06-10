# Groot 单机多实例集群管理设计

## 一、功能设计

### 1.1 概述

Groot 支持同一台机器上运行多个实例共享同一组配置数据，自动选举 Leader 来执行全局唯一任务（清理 session、同步调度、运行用户定时任务等），避免多实例重复执行。Leader 宕机后自动故障转移。

集群成员发现、心跳、选举全部基于 **Storage 抽象层**（`internal/storage`）实现：

- **local 模式**：底层落到 `{GROOT_HOME}/cluster/members/` 目录的物理文件
- **minio 模式**：底层落到 MinIO 桶内 `cluster/members/` 前缀的对象

集群模式默认启用，单实例时自己是天然 Leader，无需用户配置。

### 1.2 设计目标

- 同一台机器上多实例共享同一份配置（local 共享 `GROOT_HOME`，minio 共享 bucket）
- 自动选举 Leader，Leader 负责执行全局唯一任务
- Leader 宕机后自动故障转移
- 不新增配置项
- 对现有代码侵入最小（通过 `onBecomeLeader` / `onLoseLeader` 回调耦合）
- 不感知存储后端类型——所有 IO 走 `storage.Storage` 接口

### 1.3 核心概念

#### 1.3.1 注册编号（regID）

每个实例在注册时生成一个**注册编号**，即注册时刻的时间戳，精确到毫秒，格式 `YYYYMMDDHHMMSSmmm`（17 位纯数字）。示例：`20260515143022123`。

注册编号是实例在集群中的**唯一标识**，保存在内存中，用于：

- 选举排序：注册编号按字符串比较，**最小的当选 Leader**
- 文件定位：心跳时通过注册编号找到自己的注册对象

注册编号在以下时机会更新：
- 实例首次启动注册时生成
- 心跳中发现自己的注册对象丢失，重新注册时生成新的注册编号

其余时间（正常心跳）注册编号保持不变。

由 `GenerateRegID()` 生成（`internal/cluster/member.go`）。

#### 1.3.2 注册对象

注册对象是实例在 Storage 中的存在凭证，路径为：

```
{membersDir}/{regID}
```

`membersDir` 由调用方传入：

- local 模式：`{GROOT_HOME}/cluster/members`（`cmd/groot/main.go` 拼接）
- minio 模式：`cluster/members`（object-key 前缀）

cluster 模块**不在内部拼任何子路径**，原样接收 `membersDir` 并把 `regID` 直接 `filepath.Join` 上去。

**对象内容**（纯文本）：

```
{role}|{host}:{port}|{pid}
```

| 字段 | 说明 |
|---|---|
| `role` | `leader` 或 `follower` |
| `host:port` | 实例的 HTTP 监听地址 |
| `pid` | 操作系统进程号 |

示例：
```
leader|192.168.1.10:8080|12345
follower|192.168.1.10:8081|12346
```

#### 1.3.3 注册编号与注册对象的关系

- 注册编号是注册对象的"文件名"段（路径最后一级）
- 注册对象是注册编号在 Storage 中的物理载体
- 实例内存中始终保存自己的注册编号，心跳时用它定位对应的注册对象
- 心跳只覆盖写入注册对象的内容，**不改变路径名（即不改变注册编号）**

#### 1.3.4 心跳与存活判断

| 参数 | 值 | 来源 |
|---|---|---|
| `heartbeatInterval` | 3 秒 | `cluster.go` 常量 |
| `heartbeatTimeout` | 7 秒 | `cluster.go` 常量 |

- 心跳动作：覆盖写入注册对象内容，更新对象 ModTime
- 存活判断：`time.Since(注册对象 ModTime) > 7s` → 实例视为宕机
- ModTime 来源：`storage.Stat()` / `storage.List()` 返回的 `FileInfo.ModTime`
  - local 实现：文件系统 mtime
  - minio 实现：对象 LastModified

### 1.4 Cluster 结构

```go
type Cluster struct {
    membersDir string                 // 调用方传入的完整路径或 object-key 前缀
    host       string
    port       int
    regID      string                 // 当前注册编号
    role       string                 // "leader" / "follower"
    log        *logger.Logger
    store      istorage.Storage       // 启动期注入的进程级单例

    onBecomeLeader func()
    onLoseLeader   func()

    ctx    context.Context
    cancel context.CancelFunc
    mu     sync.RWMutex
}
```

#### 1.4.1 构造

```go
func New(membersDir, host string, port int, log *logger.Logger, store istorage.Storage) *Cluster
```

- `membersDir` 是完整路径（local）或 object-key 前缀（minio），调用方负责拼接
- `store` 由 `cmd/groot/main.go` 启动期通过 `storage.New(cfg.Storage)` 构造的进程级单例注入

#### 1.4.2 公开方法

| 方法 | 说明 |
|---|---|
| `Join(ctx)` | 注册实例，启动心跳循环 goroutine |
| `Leave()` | 取消 ctx，删除自己的注册对象（幂等：`ErrNotFound` 视为成功） |
| `IsLeader()` | 返回当前角色是否为 leader（读锁） |
| `RegID()` | 返回当前注册编号（读锁） |
| `Role()` | 返回当前角色（读锁） |
| `SetCallbacks(onBecomeLeader, onLoseLeader func())` | 设置角色变更回调 |

### 1.5 注册流程

每个实例在以下场景触发注册流程：

- **首次启动**：`Join` 第一次调用 `register`
- **重新注册**：心跳中发现自己的注册对象丢失，再次调用 `register`

`register(membersDir)` 步骤：

1. `ListMembers(store, membersDir)` 列出现有成员
   - 失败 → 记 ERROR，角色直接置为 follower 返回（保守策略）
2. `GenerateRegID()` 生成新注册编号
3. `DetermineRole(regID, members, heartbeatTimeout)` 判定角色：
   - 过滤存活成员（`time.Since(m.Mtime) < heartbeatTimeout`）
   - 无存活成员 → leader
   - 自己的 regID 排序最小 → leader
   - 否则 → follower
4. `WriteRegistration(store, membersDir, regID, role, host, port, pid)` 写注册对象
   - 失败 → 记 ERROR 直接返回（不更新 `regID` / `role`，下次心跳重试）
5. 记 INFO：`集群注册完成 reg_id=... role=... pid=...`
6. 角色为 leader 且 `onBecomeLeader != nil` → 触发回调

### 1.6 心跳流程

`run(membersDir)` 启动 `time.NewTicker(heartbeatInterval)` 循环；每次 tick 调用 `heartbeat(membersDir)`。

#### 1.6.1 心跳起点：自检

```go
ownPath := filepath.Join(membersDir, c.regID)
_, err := store.Stat(ctx, ownPath)
```

三个分支：

| 分支 | 行为 |
|---|---|
| 成功 | 进入 leader/follower 分支 |
| `errors.Is(err, ErrNotFound)` | 注册对象丢失：原 leader 触发 `onLoseLeader`，重新走 `register` 流程，本轮结束 |
| 其他错误（权限/IO/网络） | 记 WARN 日志（`自检失败,跳过本轮心跳`），跳过本轮，**不乐观写**（避免在不确定状态下产生重复注册或脑裂） |

第三个分支是关键防御：当 Storage 返回非 `ErrNotFound` 错误（minio 短暂网络抖动、local 权限异常）时，必须保守跳过，让下一轮心跳重试。

#### 1.6.2 Leader 心跳（`leaderHeartbeat`）

1. `WriteRegistration(..., RoleLeader, ...)` 覆盖写自己（更新 ModTime）
   - 失败 → 记 ERROR 直接返回
2. `ListMembers(...)` 列出所有成员
   - 失败 → 记 WARN（`列出成员失败,跳过本轮 stale 清理`），返回（不影响自身心跳已成功）
3. 遍历成员，对 `m.ID != regID && time.Since(m.Mtime) > heartbeatTimeout` 的：
   - `RemoveFile(...)` 删除超时对象
   - 成功 → 记 INFO（`清理超时注册文件 file=...`）
   - 失败 → 记 ERROR（`清理超时文件失败 file=...`）

#### 1.6.3 Follower 心跳（`followerHeartbeat`）

1. `ListMembers(...)`
   - 失败 → 记 WARN（`列出成员失败,跳过本轮心跳`），返回
2. 过滤存活成员，按 ID 升序排序
3. 自己 ID 是最小存活成员：
   - `role = leader`
   - `WriteRegistration(..., RoleLeader, ...)`
   - 记 INFO（`提升为 leader reg_id=...`）
   - 清理超时对象（同 leaderHeartbeat 步骤 3）
   - `onBecomeLeader != nil` → 触发回调
4. 否则：
   - `WriteRegistration(..., RoleFollower, ...)` 覆盖写自己

#### 1.6.4 设计要点

- **只有 Leader 负责清理超时对象**，follower 不删除任何对象
- 注册对象丢失时，leader 与 follower 都走同一套 `register` 流程重新注册
- 心跳跳过策略：自检与 List 的非 `ErrNotFound` 错误一律跳过本轮（不乐观写、不退化为重新注册）

### 1.7 全局任务

只有 Leader 负责执行全局唯一任务，通过 gocron 调度器管理：

| 任务 | 来源 | 周期 |
|---|---|---|
| Memory cleanup | `memory.NewCleanupTask` | 每天（配置时间） |
| Schedule sync | `schedule.NewSyncTask` | 30 秒 |
| 用户定时任务 | `schedule.Engine` 加载 | 按 cron 定义 |

#### 1.7.1 角色切换的启停

- 升为 leader → `onBecomeLeader()` 回调：创建新的 gocron 实例，注册全局任务，`Start()`
- 降为 follower → `onLoseLeader()` 回调：`Shutdown()` 当前 gocron 实例

回调由 `cmd/groot/main.go` 注入；cluster 包不感知 memory / schedule / scheduler 的具体逻辑。

### 1.8 故障转移

#### 1.8.1 Leader 宕机

1. Leader 进程崩溃，注册对象 ModTime 停止更新
2. 各 follower 心跳时 `ListMembers` 后过滤存活实例，原 leader 因超时被自然排除
3. 存活实例中 regID 最小的 follower，发现自己最小 → 提升为 leader
4. 新 leader 清理所有超时对象（可能含原 leader 和其他超时 follower），触发 `onBecomeLeader`

#### 1.8.2 Follower 宕机

1. Follower 注册对象 ModTime 停止更新
2. 7 秒后，leader 心跳清理该对象
3. 不影响集群运行

#### 1.8.3 进程被强杀（kill -9）

同上，注册对象残留，由 Leader 心跳清理。

#### 1.8.4 进程正常退出（SIGTERM/SIGINT）

`Leave()` 流程：
1. `cancel()` 取消 ctx，停止心跳 goroutine
2. `RemoveFile(store, membersDir, regID)`（幂等）
3. `regID = ""`

如果是 leader 退出：其注册对象被删除，各 follower 下一轮心跳自然排除之，最小 regID 者提升为 leader。
如果是 follower 退出：leader 照常运行。

#### 1.8.5 心跳写失败 / Storage 不可用

- `WriteRegistration` 失败 → 记 ERROR，本轮跳过；下一轮重试
- 7 秒内连续失败 → 注册对象 ModTime 不更新 → 被 leader 清理 → 该实例下一轮心跳自检失败 → 走 `register` 重新注册

### 1.9 member.go 接口

| 函数 | 说明 |
|---|---|
| `WriteRegistration(store, membersDir, id, role, host, port, pid) error` | 单文件原子写（由 `storage.Write` 接口契约保证） |
| `ListMembers(store, membersDir) ([]MemberInfo, error)` | 列出所有非目录条目；`ErrNotFound` 返回 `(nil, nil)` |
| `RemoveFile(store, membersDir, id) error` | 删除指定注册对象；`ErrNotFound` 视为成功 |
| `GenerateRegID() string` | 17 位纯数字时间戳 ID |

`MemberInfo` 仅含 `ID` 与 `Mtime`，定义在 `election.go`。

cluster 包**不再有** `EnsureMembersDir` / `ReadRegistration` 函数：

- 不预创建目录：local 实现的 `MkdirAll` 由 `storage.Write` 在首次写入时按需建立；minio 模式下目录是隐式前缀
- 不读注册对象内容：选举与心跳只需 `Stat` / `List` 拿到 `Mtime` 与 `ID`，无需解析 `role|host:port|pid` 内容

### 1.10 election.go 接口

```go
const (
    RoleLeader   = "leader"
    RoleFollower = "follower"
)

type MemberInfo struct {
    ID    string
    Mtime time.Time
}

func DetermineRole(selfID string, members []MemberInfo, timeout time.Duration) string
```

`DetermineRole` 行为：

1. 过滤存活成员（`now.Sub(m.Mtime) < timeout`）
2. 无存活成员 → `RoleLeader`
3. 按 ID 升序排序，自己是最小 → `RoleLeader`，否则 `RoleFollower`

### 1.11 边界场景

| 场景 | 处理 |
|---|---|
| 磁盘满 / 无写入权限 | 心跳 Write 失败 → ModTime 不更新 → 被 leader 清理 → 自检 `ErrNotFound` → `register` 重新注册（持续告警日志） |
| Storage 短暂网络抖动（minio） | 自检与 List 非 `ErrNotFound` 错误：跳过本轮（不乐观写）；下一轮重试 |
| 同一毫秒启动两个实例 | 不会发生，不做特殊处理 |
| 时钟回退 | `time.Since(m.Mtime)` 可能为负值，仍小于 timeout，视为存活；自检通过；不影响正确性 |

### 1.12 模块结构

```
internal/cluster/
├── cluster.go         # Cluster 结构体、Join/Leave/SetCallbacks/run/heartbeat/register/leaderHeartbeat/followerHeartbeat
├── member.go          # WriteRegistration / ListMembers / RemoveFile / GenerateRegID
├── election.go        # MemberInfo, DetermineRole, RoleLeader/RoleFollower 常量
├── cluster_test.go
├── member_test.go
└── election_test.go
```

### 1.13 依赖边界

- `cluster/` 依赖 `logger/` 与 `storage/` 接口
- `cluster/` 不感知 memory / schedule / scheduler 的具体逻辑——通过 `onBecomeLeader` / `onLoseLeader` 回调交互
- `cmd/groot/main.go` 是唯一将 cluster 与 storage / scheduler / memory cleanup / schedule sync 粘合的地方
- `cmd/groot/main.go` 负责：
  1. 通过 `storage.New(cfg.Storage)` 构造 Storage 单例
  2. 拼接 `membersDir`（local 拼 `homeDir`，minio 直接给前缀）
  3. `cluster.New(membersDir, host, port, log, store)`
  4. 注入 `SetCallbacks`
  5. `Join(ctx)` / 进程退出时 `Leave()`

### 1.14 配置

不新增 `config.yaml` 配置项。集群模式默认启用。

心跳间隔（3 秒）、超时时间（7 秒）作为常量硬编码在 `internal/cluster/cluster.go`。

### 1.15 日志

| 级别 | 场景 |
|---|---|
| INFO | 注册完成、提升为 leader、清理超时注册文件 |
| WARN | 自检失败（非 NotFound）、列出成员失败 → 跳过本轮 |
| ERROR | 写注册失败、删除注册失败、清理超时文件失败、心跳写入失败 |

### 1.16 测试策略

| 测试类型 | 范围 |
|---|---|
| 单元测试 | `election.go::DetermineRole`：排序、超时、空集合 |
| 单元测试 | `member.go`：WriteRegistration / ListMembers / RemoveFile 各路径 |
| 单元测试 | `cluster.go`：Join/Leave/heartbeat 各分支（mock storage） |
| 系统测试 | 单实例 → 自己是 leader |
| 系统测试 | 第 2 个实例启动 → follower |
| 系统测试 | 杀掉 leader → follower 提升为 leader |
| 系统测试 | 重启老 leader → 变为 follower |
| 系统测试 | 多实例同时运行 → 只有一个 leader |

## 二、迭代说明

### 2.1 与上一版差异

#### 存储抽象

- **调整**：底层 IO 全部从 `os.*` 迁移到 `storage.Storage` 接口
- **新增**：`Cluster.store istorage.Storage` 字段；`New` 构造函数新增 `store` 参数
- **调整**：`membersDir` 由调用方传入完整路径或 object-key 前缀，cluster 包不再拼 `GROOT_HOME`
- **新增**：minio 模式支持——同一 bucket 下多实例自动选举（远程集群 / 容器化部署）

#### 接口删减

- **移除**：`EnsureMembersDir`（不再预创建目录，由 `storage.Write` 按需建立）
- **移除**：`ReadRegistration`（选举与心跳只需 `Stat`/`List` 拿 ModTime 与 ID，无需解析对象内容）

#### 防御性日志

- **新增**：心跳自检 / `ListMembers` 在 Storage 返回非 `ErrNotFound` 错误时，记 WARN 日志并跳过本轮（避免在不确定状态下乐观写引发重复注册或脑裂）

#### 注册流程

- **调整**：注册流程第 1 步原"确保 `{GROOT_HOME}/cluster/members/` 目录存在"由 Storage 层自动兜底；cluster 不再调用 `EnsureMembersDir`
