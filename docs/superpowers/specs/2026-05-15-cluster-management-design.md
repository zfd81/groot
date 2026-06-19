# Groot 多实例集群管理设计

**日期**：2026-05-15（初版）/ 2026-06-10（迁移到数据库后端后重写）
**状态**：实现稿
**作者**：zfd81 + Claude

---

## 一、功能设计

### 1.1 概述

Groot 支持同一集群内运行多个实例共享同一组持久化数据，自动选举 Leader 来执行全局唯一任务（清理 session、同步调度、运行用户定时任务等），避免多实例重复执行。Leader 宕机后自动故障转移。

集群成员发现、心跳、选举全部基于 **`MemberRepo` 接口**（`internal/repo/member.go`）实现，底层落到 `cluster_members` 表：

- **SQLite 模式**（默认）：表落在 `~/.groot/groot.db`，仅同一台机器多实例共享
- **MySQL / PostgreSQL 模式**：表落在远端数据库，多主机集群跨节点实时共享

### 1.2 设计目标

- 多节点共享同一份运行时状态（cluster_members 表中的注册/心跳信息）
- 自动选举 Leader，Leader 负责执行全局唯一任务
- Leader 宕机后自动故障转移
- 不新增配置项
- 对现有代码侵入最小（通过 `onBecomeLeader` / `onLoseLeader` 回调耦合）
- 不感知存储后端类型——所有持久化走 `repo.MemberRepo` 接口

### 1.3 核心概念

#### 1.3.1 注册编号（regID）

每个实例在注册时生成一个**注册编号**，即注册时刻的时间戳，精确到毫秒，格式 `YYYYMMDDHHMMSSmmm`（17 位纯数字）。示例：`20260515143022123`。

注册编号是实例在集群中的**唯一标识**，保存在内存中，用于：

- 选举排序：注册编号按字符串比较，**最小的当选 Leader**
- 行定位：心跳时通过注册编号在 `cluster_members` 表中找到自己的行

注册编号在以下时机会更新：
- 实例首次启动注册时生成
- 心跳中发现自己的行已被清理（`MemberRepo.Get` 返回 `ErrNotFound`），重新注册时生成新的注册编号

其余时间（正常心跳）注册编号保持不变。

由 `GenerateRegID()` 生成（`internal/cluster/cluster.go`）。

#### 1.3.2 cluster_members 行

实例在数据库中的存在凭证是 `cluster_members` 表中以 `reg_id` 为主键的一行。表结构与字段语义见 [数据库后端设计 §1.9.3](2026-06-10-database-backend-design.md#193-cluster_members)。

| 列 | 含义 |
|---|---|
| `reg_id` | 主键，即注册编号 |
| `role` | `'leader'` 或 `'follower'`，缓存当前角色（权威判定以 `IsLeader()` 实时读取为准） |
| `host` / `port` / `pid` | 实例 HTTP 监听地址 + 进程号，仅供运维查看 |
| `heartbeat_at` | 替代历史方案的"文件 mtime"，由实例每 3 秒自更新；存活判定锚点 |
| `created_at` | 注册时刻 |

cluster 模块不在内部拼任何路径——`reg_id` 直接作为表主键使用，无前缀。

#### 1.3.3 心跳与存活判断

| 参数 | 值 | 来源 |
|---|---|---|
| `heartbeatInterval` | 3 秒 | `cluster.go` 常量 |
| `heartbeatTimeout` | 7 秒 | `cluster.go` 常量 |

- 心跳动作：`MemberRepo.Heartbeat(regID)` → `UPDATE cluster_members SET heartbeat_at=now WHERE reg_id=?`
- 存活判断：`time.Since(member.HeartbeatAt) < 7s` → 实例视为存活
- 时间精度：所有时间戳以 `BIGINT` 毫秒戳存储，跨 DB 时区无歧义

### 1.4 Cluster 结构

```go
type Cluster struct {
    host  string
    port  int
    regID string                 // 当前注册编号
    role  string                 // "leader" / "follower"
    log   *logger.Logger
    repo  repo.MemberRepo        // 启动期注入的 MemberRepo 单例

    onBecomeLeader func()
    onLoseLeader   func()

    ctx    context.Context
    cancel context.CancelFunc
    mu     sync.RWMutex
}
```

#### 1.4.1 构造

```go
func New(host string, port int, log *logger.Logger, memberRepo repo.MemberRepo) *Cluster
```

- `memberRepo` 由 `cmd/groot/main.go` 启动期通过 `repofactory.NewRepos` 构造的进程级单例注入
- cluster 模块不持有数据库连接、不感知 dialect

#### 1.4.2 公开方法

| 方法 | 说明 |
|---|---|
| `Join(ctx)` | 注册实例，启动心跳循环 goroutine |
| `Leave()` | 取消 ctx，删除自己的成员行（`MemberRepo.Remove` 幂等） |
| `IsLeader()` | 返回当前角色是否为 leader（读锁） |
| `RegID()` | 返回当前注册编号（读锁） |
| `Role()` | 返回当前角色（读锁） |
| `SetCallbacks(onBecomeLeader, onLoseLeader func())` | 设置角色变更回调 |

### 1.5 注册流程

每个实例在以下场景触发注册流程：

- **首次启动**：`Join` 第一次调用 `register`
- **重新注册**：心跳中 `MemberRepo.Get` 返回 `ErrNotFound`，再次调用 `register`

`register()` 步骤：

1. `MemberRepo.ListAll(ctx)` 列出现有成员
   - 失败 → 记 ERROR，角色直接置为 follower 返回（保守策略）
2. `GenerateRegID()` 生成新注册编号
3. `DetermineRole(regID, members, heartbeatTimeout)` 判定角色：
   - 过滤存活成员（`time.Since(m.HeartbeatAt) < heartbeatTimeout`）
   - 无存活成员 → leader
   - 自己的 regID 排序最小 → leader
   - 否则 → follower
4. `MemberRepo.Register(ctx, &Member{...})` 写入成员行（dialect 层 UPSERT，重复 reg_id 覆盖）
   - 失败 → 记 ERROR 直接返回（不更新 `regID` / `role`，下次心跳重试）
5. 记 INFO：`集群注册完成 reg_id=... role=... pid=...`
6. 角色为 leader 且 `onBecomeLeader != nil` → 触发回调

### 1.6 心跳流程

`run()` 启动 `time.NewTicker(heartbeatInterval)` 循环；每次 tick 调用 `heartbeat()`。

#### 1.6.1 心跳起点：自检

```go
_, err := c.repo.Get(c.ctx, c.regID)
```

三个分支：

| 分支 | 行为 |
|---|---|
| 成功 | 进入 leader/follower 分支 |
| `errors.Is(err, repo.ErrNotFound)` | 成员行已被清理：原 leader 触发 `onLoseLeader`，重新走 `register` 流程，本轮结束 |
| 其他错误（DB 不可用、网络抖动） | 记 WARN（`自检失败,跳过本轮心跳`），跳过本轮，**不乐观写**（避免在不确定状态下产生重复注册或脑裂） |

第三个分支是关键防御：当 DB 返回非 `ErrNotFound` 错误时，必须保守跳过，让下一轮心跳重试。

#### 1.6.2 Leader 心跳（`leaderHeartbeat`）

1. `MemberRepo.Heartbeat(regID)` 更新自己的 `heartbeat_at`
   - 失败 → 记 ERROR 直接返回
2. `MemberRepo.UpdateRole(regID, RoleLeader)` 维持 role 列为 leader
   - 失败 → 记 ERROR 但不返回（不阻塞下一步）
3. `MemberRepo.RemoveExpired(ctx, now - heartbeatTimeout)` 一条 SQL 删除所有 `heartbeat_at < cutoff` 的成员行
   - 成功且删除 N > 0 → 记 INFO（`清理超时成员 count=N`）
   - 失败 → 记 WARN（`清理超时成员失败`）

`RemoveExpired` 在 SQL 层一次性 `DELETE WHERE heartbeat_at < ?`，不再像文件实现那样按行 Stat + Remove。

#### 1.6.3 Follower 心跳（`followerHeartbeat`）

1. `MemberRepo.ListAll(ctx)` 列出所有成员
   - 失败 → 记 WARN（`列出成员失败,跳过本轮心跳`），返回
2. 过滤存活成员（`time.Since(m.HeartbeatAt) < heartbeatTimeout`），按 reg_id 升序排序
3. 自己 reg_id 是最小存活成员：
   - `role = leader`
   - `MemberRepo.UpdateRole(regID, RoleLeader)`
   - 记 INFO（`提升为 leader reg_id=...`）
   - `MemberRepo.RemoveExpired` 清理超时成员
   - `onBecomeLeader != nil` → 触发回调
4. 否则：
   - `MemberRepo.Heartbeat(regID)` 更新自己的心跳

#### 1.6.4 设计要点

- **只有 Leader 调用 `RemoveExpired`**，follower 不删除任何成员
- 自检失败（非 `ErrNotFound`）时一律跳过本轮，不退化为重新注册
- 升级为 leader 时不做 CAS——`reg_id` 时间戳单调递增 + 全量 ListAll 排序保证基本互斥；极端并发下两个实例都自认为最小，下一轮心跳（最长 3 秒）通过 ListAll 重新判断后 reg_id 大者自动降级。**全局唯一任务的真正保护是 Leader 调用方在执行任务前实时调用 `IsLeader()`**，不是一次性的 CAS

### 1.7 全局任务

只有 Leader 负责执行全局唯一任务，通过 gocron 调度器管理：

| 任务 | 来源 | 周期 |
|---|---|---|
| Memory cleanup | `memory.NewCleanupTask` | 每天（配置时间） |
| Schedule sync | `schedule.NewSyncTask` | 30 秒（默认）|
| 用户定时任务 | `schedule.Engine` 加载 | 按 cron / 单次 / interval 定义 |

#### 1.7.1 角色切换的启停

- 升为 leader → `onBecomeLeader()` 回调：创建新的 gocron 实例，注册全局任务，`Start()`
- 降为 follower → `onLoseLeader()` 回调：`Stop()` 当前 gocron 实例

回调由 `cmd/groot/main.go` 注入；cluster 包不感知 memory / schedule / scheduler 的具体逻辑。

### 1.8 故障转移

#### 1.8.1 Leader 宕机

1. Leader 进程崩溃，`heartbeat_at` 列停止更新
2. 各 follower 心跳时 `ListAll` 后过滤存活实例，原 leader 因 `time.Since(HeartbeatAt) > 7s` 被排除
3. 存活实例中 reg_id 最小的 follower 提升为 leader
4. 新 leader 调用 `RemoveExpired` 清理所有超时成员行（含原 leader），触发 `onBecomeLeader`

#### 1.8.2 Follower 宕机

1. Follower `heartbeat_at` 停止更新
2. 7 秒后，leader 心跳的 `RemoveExpired` 一并删除该行
3. 不影响集群运行

#### 1.8.3 进程被强杀（kill -9）

同上，成员行残留，由 Leader 心跳清理。

#### 1.8.4 进程正常退出（SIGTERM/SIGINT）

`Leave()` 流程：
1. `cancel()` 取消 ctx，停止心跳 goroutine
2. `MemberRepo.Remove(regID)`（幂等，不存在视为成功）
3. `regID = ""`

如果是 leader 退出：成员行被删除，各 follower 下一轮心跳自然排除之，最小 reg_id 者提升。
如果是 follower 退出：leader 照常运行。

#### 1.8.5 心跳写失败 / DB 不可用

- `MemberRepo.Heartbeat` 失败 → 记 ERROR，本轮跳过；下一轮重试
- 7 秒内连续失败 → `heartbeat_at` 不更新 → 被 leader `RemoveExpired` 删除 → 该实例下一轮自检 `ErrNotFound` → 走 `register` 重新注册

### 1.9 election.go 接口

```go
const (
    RoleLeader   = "leader"
    RoleFollower = "follower"
)

func DetermineRole(selfID string, members []*repo.Member, timeout time.Duration) string
```

`DetermineRole` 行为：

1. 过滤存活成员（`now.Sub(m.HeartbeatAt) < timeout`）
2. 无存活成员 → `RoleLeader`
3. 按 `RegID` 升序排序，自己是最小 → `RoleLeader`，否则 `RoleFollower`

### 1.10 边界场景

| 场景 | 处理 |
|---|---|
| 数据库连接失败 / DB 不可用 | 自检 / List / Heartbeat 非 `ErrNotFound` 错误：跳过本轮（不乐观写）；下一轮重试。Leader 在 DB 长时间不可用时无法续约 `heartbeat_at`，最终被其它节点视为超时；DB 恢复后该实例自检 `ErrNotFound` → `register` 重新注册 |
| 同一毫秒启动两个实例 | reg_id 由 `time.Now().Format("20060102150405.000")` 生成，理论上同毫秒可能冲突；`Register` 走 UPSERT 后两个实例都成功，但其中一个会丢失（被另一个的 INSERT 覆盖）。极小概率，不做特殊处理 |
| 时钟回退 | `time.Since(m.HeartbeatAt)` 可能为负值，仍小于 timeout，视为存活；自检通过；不影响正确性 |
| Leader 网络分区 | 分区一侧的 follower 看不到 Leader 心跳，会另选新 Leader（split-brain）。当前不做防御——多 Leader 写入时 DB 主表（schedule_tasks）的乐观锁 (`version`) 会拒绝并发覆盖；执行历史 (`schedule_executions`) 走 `INSERT IGNORE` execution_id 唯一约束，重复执行不会重复写库 |

### 1.11 模块结构

```
internal/cluster/
├── cluster.go         # Cluster 结构体、Join/Leave/SetCallbacks/run/heartbeat/register
│                      # leaderHeartbeat/followerHeartbeat/GenerateRegID
├── election.go        # DetermineRole、RoleLeader/RoleFollower 常量
├── cluster_test.go
└── election_test.go
```

注：原 `member.go`（`WriteRegistration` / `ListMembers` / `RemoveFile`）已删除——这些动作现在直接由 `Cluster` 内部对 `MemberRepo` 接口的方法调用承担，无需 helper 中间层。

### 1.12 依赖边界

- `cluster/` 依赖 `logger/` 与 `repo.MemberRepo` 接口
- `cluster/` 不感知 memory / schedule / scheduler 的具体逻辑——通过 `onBecomeLeader` / `onLoseLeader` 回调交互
- `cmd/groot/main.go` 是唯一将 cluster 与 db / memberRepo / scheduler / memory cleanup / schedule sync 粘合的地方
- `cmd/groot/main.go` 负责：
  1. `db.Open(cfg.Database, homeDir)` 构造 `*sqlx.DB` + dialect
  2. `repofactory.NewRepos(sqlxDB, dialect, homeDir)` 构造 `MemberRepo`（同时构造其余 3 个 Repo）
  3. `cluster.New(host, port, log, repos.Member)`
  4. 注入 `SetCallbacks`
  5. `Join(ctx)` / 进程退出时 `Leave()`

### 1.13 配置

不新增 `config.yaml` 配置项。集群模式默认启用。

心跳间隔（3 秒）、超时时间（7 秒）作为常量硬编码在 `internal/cluster/cluster.go`。

数据库后端选择由 `~/.groot/env.yaml` 中的 `database` 节决定（详见 [数据库后端设计 §1.5](2026-06-10-database-backend-design.md#15-envyaml-配置格式)）：
- 不配置 → SQLite 单机模式
- `database.driver = mysql / postgres` → 多主机集群模式

### 1.14 日志

| 级别 | 场景 |
|---|---|
| INFO | 注册完成、提升为 leader、清理超时成员 |
| WARN | 自检失败（非 `ErrNotFound`）、列出成员失败 → 跳过本轮 |
| ERROR | 写注册失败、删除注册失败、清理超时成员失败、心跳写入失败 |

### 1.15 测试策略

| 测试类型 | 范围 |
|---|---|
| 单元测试 | `election.go::DetermineRole`：排序、超时、空集合 |
| 单元测试 | `cluster.go`：Join/Leave/heartbeat 各分支（mock MemberRepo） |
| 系统测试 | 单实例 → 自己是 leader |
| 系统测试 | 第 2 个实例启动 → follower |
| 系统测试 | 杀掉 leader → follower 提升为 leader |
| 系统测试 | 重启老 leader → 变为 follower |
| 系统测试 | 多实例同时运行 → 只有一个 leader |

## 二、迭代说明

### 2.1 与上一版差异

历史版本基于 `storage.Storage` 接口 + 文件 mtime 心跳实现，文档详见 [`archive/2026-05-15-cluster-management-design.md`](archive/2026-05-15-cluster-management-design.md)。本版相对上一版的差异：

#### 持久化抽象

- **新增**：`repo.MemberRepo` 接口（`Register / Heartbeat / UpdateRole / Get / ListAll / Remove / RemoveExpired`）
- **退役**：`storage.Storage` 接口在 cluster 模块内的全部使用；`internal/storage/` 整包退役
- **退役**：`member.go` 内的 `WriteRegistration` / `ListMembers` / `RemoveFile` / `EnsureMembersDir` / `ReadRegistration` 全部删除

#### 数据载体

- **调整**：成员状态从 `cluster/members/{regID}` 文件迁移到 `cluster_members` 表中以 `reg_id` 为主键的一行
- **调整**：心跳锚点从"文件 mtime"改为 `cluster_members.heartbeat_at`（毫秒戳列）
- **调整**：成员对象内容从 `role|host:port|pid` 文本改为多列结构化字段（`role` / `host` / `port` / `pid`）
- **保留**：`reg_id` 17 位毫秒时间戳格式不变

#### 选举与心跳

- **简化**：超时清理由"按行 Stat + Remove"简化为单 SQL `DELETE WHERE heartbeat_at < ?`
- **保留**：心跳间隔 3 秒、超时 7 秒、`reg_id` 升序选举的核心算法不变
- **保留**：自检三分支（成功 / `ErrNotFound` / 其他错误跳过本轮）

#### 配置入口

- **退役**：`env.yaml` 中的 `minio` 节
- **新增**：`env.yaml` 中的 `database` 节决定后端（详见 [数据库后端设计 §1.5](2026-06-10-database-backend-design.md#15-envyaml-配置格式)）
- **保留**：单实例 / 单机多实例零配置（不写 `database` 节即 SQLite 单机模式）
