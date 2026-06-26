# 定时任务调度系统设计

**日期**：2026-05-11（初版）/ 2026-06-10（迁移到数据库后端后重写）/ 2026-06-26（移除 memory 清理调度）
**状态**：实现稿
**作者**：zfd81 + Claude

---

## 一、功能设计

### 1.1 概述

为 Groot 提供定时任务调度能力。用户通过对话创建定时任务，Leader 实例在指定时间自动执行 Agent 指令，执行结果通过消息层推送通知。

### 1.2 核心原则

- **创建走对话**：任务只能由 Agent 通过内置工具创建，CLI 和 API 不提供创建入口
- **统一调度引擎**：使用 gocron 统一管理所有定时任务（用户任务 + active 任务重注册兜底）
- **数据库持久化**：所有任务定义和执行历史走 `schedule.ScheduleRepo` 接口，落到 `schedule_tasks` / `schedule_executions` 两张表（详见 [数据库后端设计 §1.9.1 / §1.9.2](2026-06-10-database-backend-design.md)）
- **三种状态**：`active` / `disabled` / `archive`，存放于 `schedule_tasks.status` 列
- **消息层解耦**：执行结果推入消息层，由消息层统一路由分发，不经过 LLM

### 1.3 源码目录

```
internal/
├── scheduler/              # 通用调度引擎封装（gocron）
│   └── scheduler.go        # Scheduler — Start/Stop/AddCron/AddOnce/AddDuration/AddDaily/RemoveByTag
│
├── schedule/               # 定时任务应用层
│   ├── repo.go             # ScheduleRepo 接口 + ErrNotFound / ErrConflict + TaskStatus 常量
│   ├── types.go            # Task / ExecutionRecord / NotificationConfig / ScheduleType / ParseScheduleType
│   ├── storage.go          # Storage — ScheduleRepo 上层薄壳，统一 task/execution 操作 API
│   ├── engine.go           # Engine — 启动时 ListActiveTasks 并注册到 gocron
│   ├── manager.go          # Manager — 任务生命周期管理（CRUD、启停、归档、Rerun）
│   ├── runner.go           # Runner — 任务执行器，调用 agent.Executor + 消息层
│   ├── sync.go             # 定期重注册 active 任务到 gocron（兜底）
│   ├── tools.go            # 8 个内置工具，Agent 侧
│   ├── idgen.go            # generateExecutionID — 基于随机字节生成 execution_id
│   └── storage_test.go
│
└── repo/scheduledb/        # ScheduleRepo 数据库实现（SQLite / MySQL / PostgreSQL）
```

### 1.4 数据持久化

`schedule_tasks` 与 `schedule_executions` 两张表的 DDL、字段语义、索引设计、`payload` JSON 内容、`detail` JSON 内容详见 [数据库后端设计 §1.9.1 / §1.9.2](2026-06-10-database-backend-design.md)。

#### 1.4.1 状态流转

```
  创建 ─→ active ──执行──→ active（recurring，继续等下次）
            │
            ├── 手动禁用 ──→ disabled
            │                  │
            │                  ├── 手动启用 ──→ active
            │                  └── 手动归档 ──→ archive
            │
            └── 一次性任务完成（status==completed）──→ archive
```

- 只有 `active` 任务被调度器扫描
- 状态变更通过 `ScheduleRepo.MoveStatus(taskID, newStatus, version)` 更新 `schedule_tasks.status` 列，乐观锁 `version` 防并发覆盖
- 不预创建任何"目录"——表由 `Migrate` 在启动期 `CREATE TABLE IF NOT EXISTS`

### 1.5 数据类型

#### 1.5.1 Task

```go
type Task struct {
    ID             string             `json:"id"`
    Name           string             `json:"name"`
    Schedule       string             `json:"schedule"`        // cron / ISO8601 / Go duration
    Status         string             `json:"status,omitempty"` // active / disabled / archive，由 repo 填充
    MissedPolicy   string             `json:"missed_policy"`   // run_once / skip
    TaskDef        TaskDef            `json:"task"`
    Notification   NotificationConfig `json:"notification"`
    CreatedAt      time.Time          `json:"created_at"`
    UpdatedAt      time.Time          `json:"updated_at"`
    Version        int64              `json:"version,omitempty"`
}

type TaskDef struct {
    Instruction  string `json:"instruction"`
    Model        string `json:"model"`
    SystemPrompt string `json:"system_prompt"`
}

type NotificationConfig struct {
    OnSuccess []string `json:"on_success"`
    OnFailure []string `json:"on_failure"`
}
```

| 字段 | 说明 |
|---|---|
| `ID` | 由 `Manager.generateTaskID(name)` 生成 kebab-case，前缀 `task-` |
| `Schedule` | 调度表达式，由 `ParseScheduleType` 自动识别为 cron / once / interval 三种 |
| `Status` | 单独作为 `schedule_tasks.status` 列存储；`SaveTask` 时硬编码为 `active` 写入 status 列（payload 中的同名字段不参与状态判定），读库时由实现层从 status 列回填到 `Task.Status` |
| `MissedPolicy` | 重启后错过任务的策略：`run_once` / `skip`，默认 `run_once` |
| `Version` | 乐观锁版本号，存于 `schedule_tasks.version` 列；`MoveStatus` / `UpdateNextRun` / `CompleteExecution` 都基于该字段 CAS |

#### 1.5.2 ScheduleType 自动识别

`ParseScheduleType(schedule string) ScheduleType`：

| 优先级 | 判定 | ScheduleType |
|---|---|---|
| 1 | `time.Parse(time.RFC3339, schedule)` 成功 | `ScheduleTypeOnce` |
| 2 | `time.ParseDuration(schedule)` 成功 | `ScheduleTypeInterval` |
| 3 | 兜底 | `ScheduleTypeCron` |

在 `Engine.Register` 中按 ScheduleType 分发到 `scheduler.AddCron / AddOnce / AddDuration`。

#### 1.5.3 ExecutionRecord

```go
type ExecutionRecord struct {
    ExecutionID   string               `json:"execution_id"`
    TaskID        string               `json:"task_id"`
    StartedAt     time.Time            `json:"started_at"`
    FinishedAt    *time.Time           `json:"finished_at"`
    TriggerType   string               `json:"trigger_type"`     // cron / once / interval / manual
    SessionID     string               `json:"session_id"`
    ChatID        string               `json:"chat_id"`
    Status        string               `json:"status"`           // running / completed / failed / cancelled
    DurationMs    int64                `json:"duration_ms"`
    StepCount     int                  `json:"step_count"`
    Error         string               `json:"error"`
    Notifications []NotificationResult `json:"notifications"`
}
```

整体序列化为 JSON 后存入 `schedule_executions.detail` 列；`ExecutionID` / `TaskID` / `StartedAt` / `FinishedAt` / `Status` 同时映射到独立列上以便查询/索引。

### 1.6 调度引擎

#### 1.6.1 技术选型

使用 `github.com/go-co-op/gocron/v2`。

#### 1.6.2 统一调度

整个 Groot Leader 实例只有一个 `gocron.Scheduler`：

- **系统 Job**：active 任务定期重注册（`schedule.NewSyncTask`）
- **用户 Job**：每个 active 任务注册为一个 Job，通过 Tag `["user-task", taskID]` 标记便于管理

#### 1.6.3 动态管理

运行时增删任务通过 Tag 查找和操作 Job：

- 创建：`scheduler.AddCron / AddOnce / AddDuration` 带 tag
- 删除：`scheduler.RemoveByTag(taskID)`
- 启用：重新注册
- 禁用：`scheduler.RemoveByTag(taskID)` + `MoveStatus(taskID, "disabled", version)`

#### 1.6.4 Leader 切换的启停

`cmd/groot/main.go` 把两个回调注入 `cluster` 模块：

- `startLeaderTasks`：当前实例升为 Leader 时触发。创建 `scheduler.Scheduler`、构造 `Engine`、`Engine.Start()` 加载 active 任务、注册系统 Job（`schedule.NewSyncTask`），随后按 `cfg.Schedule.Enabled` 决定是否构造 `Manager` 并把 8 个调度工具注册到 `mcpMgr`，最后 `sched.Start()`。
- `stopLeaderTasks`：当前实例降为 follower 时触发。`sched.Stop()` 关闭整个调度器（gocron 内部 `Shutdown`），并 `mcpMgr.UnregisterBuiltinTools()` 注销调度工具。

Follower 实例不持有 gocron 实例，也不会被注册任何 user-task / system Job。但 `Storage` 与 `Runner` 在所有节点上预先实例化（用于 API 层的只读访问，例如 list/inspect/history）。

### 1.7 Storage 模块（ScheduleRepo 上层薄壳）

`internal/schedule/storage.go` 是 `ScheduleRepo` 接口的轻量封装层，对外保留 manager / engine / runner / sync 已习惯的命名（`SaveTask` / `LoadTask` / `MoveTask` 等），内部直接转发到 `ScheduleRepo` 方法。它的存在只为隔离 sql 错误转换、为旧版 API 保持向后兼容（如 `MoveTask(from, to)` 在转发时校验当前状态等于 `from`）。

```go
type Storage struct {
    repo ScheduleRepo
    log  *logger.Logger
}

func NewStorage(r ScheduleRepo, log *logger.Logger) *Storage
```

| 方法 | 转发 |
|---|---|
| `SaveTask(task)` | `repo.SaveTask` |
| `LoadTask(taskID)` | `repo.LoadTask`，`ErrNotFound` 包装为 `任务 %s 不存在` |
| `ListActiveTasks()` / `ListAllTasks()` / `listTasksIn(status)` | `repo.ListByStatus` |
| `MoveTask(taskID, from, to)` | `repo.LoadTask` 校验 status==from → `repo.MoveStatus(taskID, to, version)` |
| `DeleteTask(taskID)` | 先 `LoadTask` 判存在，再 `repo.DeleteTask` |
| `GetTaskStatus(taskID)` | `repo.LoadTask` 取 `task.Status`，不存在返回空串 |
| `SaveExecution(taskID, rec)` | 校验任务存在 → 自动生成 `ExecutionID`（若未填，由 `generateExecutionID` 生成 `{taskID}-{YYYYMMDDTHHMMSS}-{8-hex}`）→ 设置 `rec.TaskID = taskID` → `repo.SaveExecution`（INSERT IGNORE 幂等） |
| `LoadExecutions(taskID)` | `repo.ListExecutions(taskID, 50)`，按 started_at DESC 返回最近 50 条 |
| `EnsureDirs()` | no-op（DB 模式无目录概念，保留签名兼容历史调用） |

### 1.8 任务执行流程

`Runner.Run(taskID)` 返回一个 `func()`，由 gocron 调用：

```
1. storage.LoadTask(taskID)  ── 失败 → 记 ERROR 直接返回
2. startTime = time.Now()
   sessionID = {taskID}-{YYYYMMDDTHHMMSS}-sched
3. memoryMgr.CreateSession(sessionID, "")  ── userID 留空，失败 → 记 ERROR 返回
4. detectTriggerType(task.Schedule)  → cron / once / interval
5. 构造 agent.Task{ID=task.ID, Instruction, Prompt=SystemPrompt, ModelName=Model, Caller="schedule"}
6. executor.Execute(ctx, sessionID, agentTask, nil)
7. duration_ms = time.Since(startTime).Milliseconds()
   status = string(agentTask.Status)，空串兜底为 "completed"
   step_count = len(agentTask.Steps)
8. 构造 ExecutionRecord（chat_id = startTime.Format("20060102150405")，
   error = agentTask.Error.Message 若非 nil）
   storage.SaveExecution(task.ID, record)
9. sendNotifications(task, status, agentTask.Result)
   按 status 选 OnSuccess / OnFailure 渠道，eventType="schedule.{status}"
   发布到 message.Layer，goroutine 异步收集结果并记日志
10. 一次性任务（ParseScheduleType==Once）且 status == "completed" → MoveTask(taskID, "active", "archive")
    （失败任务保留在 active，不归档；用户可查执行记录后手动处理）
11. 记 INFO 完成日志
```

执行状态枚举与 chat 一致：`'running' | 'completed' | 'failed' | 'cancelled'`，由 `agent.Executor` 在执行结束时填充。

#### 1.8.1 RunImmediate（rerun）

`Manager.Rerun(taskID)` → `Runner.RunImmediate(task)` 直接执行一次，不走 gocron：

- `trigger_type = "manual"`
- 不设置 `chat_id`
- 不做一次性任务归档
- 其余流程与 `Run` 相同（创建 session、构造 agent.Task、executor.Execute、保存 ExecutionRecord、发送通知）

#### 1.8.2 任务执行与 Session

每次任务执行创建一个独立 Session，格式 `{taskID}-{YYYYMMDDTHHMMSS}-sched`。Session 元数据写入 `memory_sessions` 表，对话记录走 `memory_chats`。

| 维度 | 普通 Session | 定时任务 Session |
|---|---|---|
| ID 格式 | `{timestamp}_{random}` | `{taskID}-{timestamp}-sched` |
| 创建方式 | 用户通过 `/chat` 发起 | Runner 自动创建 |
| caller 字段 | `user` | `schedule` |
| 生命周期 | 用户手动管理 | 跟随任务执行，完成后写入数据库 |
| 清理策略 | 不做定时清理，长期保留在数据库中 | 同上 |

### 1.9 Manager：任务生命周期

```go
type Manager struct {
    storage *Storage
    engine  *Engine
    runner  *Runner
    log     *logger.Logger
}
```

| 方法 | 行为 |
|---|---|
| `Create(task)` | 生成 ID（如未指定）→ 设置 `CreatedAt/UpdatedAt` → `storage.SaveTask` → `engine.Register` |
| `List(status)` | `status` 为 `all` 或空串 → `ListAllTasks`；否则 `listTasksIn(status)` |
| `Get(taskID)` | `storage.LoadTask` |
| `Delete(taskID)` | `engine.Unregister` → `storage.DeleteTask` |
| `Disable(taskID)` | `engine.Unregister` → `storage.MoveTask(taskID, "active", "disabled")` |
| `Enable(taskID)` | `storage.MoveTask(taskID, "disabled", "active")` → `LoadTask` → `engine.Register` |
| `Archive(taskID)` | 当前状态为 active → `engine.Unregister`；`storage.MoveTask(taskID, status, "archive")` |
| `GetHistory(taskID)` | `storage.LoadExecutions` |
| `Rerun(taskID)` | `LoadTask` → `Runner.RunImmediate` |

#### 1.9.1 generateTaskID

输入 `task.Name`，输出 `task-{kebab-case}`：

- 转小写、空格与下划线 → `-`
- 仅保留 `[a-z0-9-]`
- 合并连续 `-`，去首尾 `-`
- 空字符串兜底为 `task-{UnixNano}`

### 1.10 Engine：调度引擎管理

```go
type Engine struct {
    scheduler *scheduler.Scheduler
    runner    *Runner
    storage   *Storage
    log       *logger.Logger
}
```

`Start()`：

1. `storage.ListActiveTasks()`
2. 对每个 task 调 `Register`，单个失败记 INFO 日志（"注册任务失败，跳过"）继续
3. 记 INFO `调度引擎已启动 active_tasks=N`

`Register(task)` 按 `ParseScheduleType` 分发到 `scheduler.AddCron / AddOnce / AddDuration`，tag 为 `["user-task", task.ID]`。

`Unregister(taskID)`：`scheduler.RemoveByTag(taskID)`。

### 1.11 Sync：定期重注册（兜底）

`SyncTask(engine, storage, log)` 返回 `func()`，由 gocron 以固定间隔（默认 30 秒）触发：

1. `storage.ListActiveTasks()` — 失败 → 记 ERROR，返回
2. 遍历 active 任务，对每个调 `engine.Register(task)`
   - 同 tag 已存在则容错（gocron 内部处理）
   - 单个失败记 INFO 日志，继续

> 当前实现仅做"重注册兜底"，不做差集计算。任务的增删走 Manager 链路本身保持一致性；sync 兜底意外路径，例如 follower 提升为 leader 时需要把所有 active 任务一次性注册到本节点的 gocron 实例。

### 1.12 内置工具

8 个内置工具，由 `NewScheduleTools(mgr)` 构造为 `map[string]tool.BaseTool` 注册到 Agent：

| 工具 | 说明 |
|---|---|
| `schedule_create` | 创建定时任务 |
| `schedule_list` | 查询所有任务（按 status 过滤） |
| `schedule_delete` | 删除任务（物理删除） |
| `schedule_disable` | 禁用任务（active → disabled） |
| `schedule_enable` | 启用任务（disabled → active） |
| `schedule_archive` | 归档任务（→ archive） |
| `schedule_history` | 查看某任务执行历史 |
| `schedule_inspect` | 查看任务详情 |

#### 1.12.1 schedule_create 入参

| 参数 | 必填 | 说明 |
|---|---|---|
| `name` | 是 | 任务名称 |
| `schedule` | 是 | 调度表达式（cron / ISO8601 / duration） |
| `instruction` | 是 | 要执行的指令 |
| `model` | 否 | 指定 LLM 模型 |
| `missed_policy` | 否 | `run_once` / `skip`，默认 `run_once` |
| `notify_on_success` | 否 | 成功通知渠道列表 |
| `notify_on_failure` | 否 | 失败通知渠道列表 |

> `system_prompt` 不在工具入参中暴露：`TaskDef.SystemPrompt` 字段保留供内部预设。

#### 1.12.2 其他工具入参

`schedule_list` 接受 `status`（`active`/`disabled`/`archive`/`all`，默认 `all`）。

`delete/disable/enable/archive/history/inspect` 统一接受 `task_id`。

### 1.13 CLI 子命令

CLI 仅提供查看和管理能力，不提供创建：

```
groot schedule list                    # 列出所有任务
groot schedule history <task-id>        # 查看某任务执行历史
groot schedule inspect <task-id>        # 查看任务详情
groot schedule delete <task-id>         # 删除任务
groot schedule disable <task-id>        # 禁用任务 (active → disabled)
groot schedule enable <task-id>         # 启用任务 (disabled → active)
groot schedule archive <task-id>        # 归档任务（→ archive）
```

> CLI 命令实现于 [`internal/cmd/schedule.go`](../../../internal/cmd/schedule.go)，当前直接读取 `{GROOT_HOME}/schedules/{active,disabled,archive}/<task_id>.json` 与 `{GROOT_HOME}/schedules/executions/<task_id>.json` 的本地目录树（命令行进程不连接数据库），与运行期使用 `ScheduleRepo` 的 DB 后端解耦。
>
> API 端点定义见 [API 设计文档](2026-05-16-api-design.md)，由 leader 进程内的 `Manager` 直接服务，走数据库后端。

### 1.14 配置

`config.yaml` 中的 `schedule` 段：

```yaml
schedule:
  enabled: false                        # 是否允许在对话中创建定时任务（默认关闭，不影响 active 任务的同步兜底）
  max_concurrent_tasks: 3               # 最大并发执行数
  sync_interval: 30s                    # 定期重注册间隔
```

后端选择由 `~/.groot/env.yaml` 中的 `database` 节决定（详见 [数据库后端设计 §1.5](2026-06-10-database-backend-design.md#15-envyaml-配置格式)）。

### 1.15 错误处理

- **任务执行失败**：记 ERROR、保存执行记录（status=failed）、发布失败通知，recurring 任务下次继续执行
- **panic 恢复**：gocron 内置 panic 恢复，不会导致调度器崩溃
- **执行超时**：复用 `agent.Executor` 的超时机制
- **DB 错误**：透传 `schedule.ErrNotFound` / `schedule.ErrConflict`；调用方按需 `errors.Is` 判定
- **乐观锁冲突**：`MoveStatus` / `UpdateNextRun` / `CompleteExecution` 在 version 不匹配时返回 `ErrConflict`，调用方重新 `LoadTask` 后重试

### 1.16 测试

| 文件 | 范围 |
|---|---|
| [`internal/schedule/storage_test.go`](../../../internal/schedule/storage_test.go) | Storage 各方法的端到端行为（背靠 SQLite in-memory 的 scheduledb 实现） |
| [`internal/repo/scheduledb/schedule_test.go`](../../../internal/repo/scheduledb/schedule_test.go) | scheduledb 实现的 SQL 行为（SQLite in-memory） |

## 二、迭代说明

### 2.1 与上一版差异

历史版本基于 `storage.Storage` 接口 + 目录树 + JSON 文件实现，文档详见 [`archive/2026-05-11-schedule-design.md`](archive/2026-05-11-schedule-design.md)。本版相对上一版的差异：

#### 持久化抽象

- **新增**：`schedule.ScheduleRepo` 接口（`SaveTask` / `LoadTask` / `ListByStatus` / `DueTasks` / `UpdateNextRun` / `MoveStatus` / `DeleteTask` / `SaveExecution` / `CompleteExecution` / `ListExecutions`）
- **退役**：`storage.Storage` 接口在 schedule 模块内的全部使用
- **新增**：`internal/repo/scheduledb/` 实现 `ScheduleRepo`

#### 数据载体

- **调整**：任务定义从 `{baseDir}/{status}/{taskID}/task.json` 文件迁移到 `schedule_tasks` 表中以 `task_id` 为业务唯一键的一行；`payload` 列存全量 JSON
- **调整**：执行历史从 `{baseDir}/{status}/{taskID}/executions/{timestamp}.json` 文件迁移到 `schedule_executions` 表的 append-only 行；`detail` 列存完整 JSON
- **调整**：状态切换从"目录 Rename"改为"`UPDATE schedule_tasks SET status=?` 加乐观锁"
- **新增**：`Task.Version` 字段（乐观锁）和 `ExecutionRecord.ExecutionID` / `FinishedAt` 字段
- **重命名**：`ExecutionRecord.ExecTime` → `StartedAt`

#### Storage 模块定位

- **调整**：`internal/schedule/storage.go` 从"目录树 + JSON 文件操作"改为"`ScheduleRepo` 接口的轻量薄壳"，对外保留 manager / engine / runner / sync 已熟悉的方法名
- **保留**：`EnsureDirs()` 签名（无操作），便于历史调用兼容

#### 配置入口

- **退役**：`env.yaml` 中的 `minio` 节
- **新增**：`env.yaml` 中的 `database` 节决定后端
- **保留**：`config.yaml` 中的 `schedule` 段（`enabled` / `max_concurrent_tasks` / `sync_interval`）不变

#### 一次性任务归档

- **保留**：仅在 `status == "completed"` 时归档；失败的一次性任务保留在 `active`，等待用户手动处理

#### 系统 Job

- **移除**：`gocron` 中的 memory 清理 Job（`memory.NewCleanupTask`）及配套的 `ParseCleanupTime` 工具函数
- **保留**：active 任务定期重注册兜底 Job（`schedule.NewSyncTask`）
