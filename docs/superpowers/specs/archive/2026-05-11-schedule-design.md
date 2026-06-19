# 定时任务调度系统设计

> ⚠️ **本文档已归档**：schedule 模块已迁移到 `schedule.ScheduleRepo` + 数据库 `schedule_tasks` / `schedule_executions` 表实现，原"基于 storage.Storage 接口 + 目录树 + JSON 文件"的方案已退役。
>
> **后续设计**：见同目录 [`../2026-05-11-schedule-design.md`](../2026-05-11-schedule-design.md)（已重写为数据库后端版本）。
>
> 本文档仅作历史参考保留。

---

## 一、功能设计

### 1.1 概述

为 Groot 添加定时任务调度能力。用户通过对话创建定时任务，系统在指定时间自动执行 Agent 指令，执行结果通过消息层推送通知。

### 1.2 核心原则

- **创建走对话**：任务只能由 Agent 通过内置工具创建，CLI 和 API 不提供创建入口
- **统一调度引擎**：用 gocron 统一管理所有系统定时（含 memory 清理、active/ 同步），单 goroutine 轮询
- **Storage 抽象层持久化**：所有任务文件 IO 走 `storage.Storage` 接口，支持 local 文件系统与 MinIO 对象存储两种后端
- **每任务一个目录**：分 `active/` `disabled/` `archive/` 三种状态目录
- **消息层解耦**：执行结果推入消息层（独立模块），由消息层统一路由分发，不经过 LLM

### 1.3 源码目录

```
internal/
├── scheduler/              # 通用调度引擎封装（gocron）
│   └── scheduler.go        # Scheduler — 启动/停止/注册 Job
│
├── schedule/               # 定时任务应用层
│   ├── engine.go           # Engine — 调度器管理，启动时加载 active/ 下任务
│   ├── manager.go          # Manager — 任务生命周期管理（CRUD、启停、归档、Rerun）
│   ├── runner.go           # Runner — 任务执行器，调用 agent.Executor + 消息层
│   ├── storage.go          # Storage — 经 storage.Storage 接口的任务持久化
│   ├── storage_test.go     # storage 单元测试
│   ├── tools.go            # 8 个内置工具，Agent 侧
│   ├── types.go            # 数据类型与 ScheduleType 解析
│   └── sync.go             # 定期重注册 active/ 任务到 gocron（安全网）
│
└── memory/cleanup.go       # 实现 gocron Task 接口，由 Engine 统一调度
```

### 1.4 数据目录

`baseDir` 由 `cmd/groot/main.go` 在启动期注入到 `NewStorage(baseDir, store, log)`：

- local 模式：`{GROOT_HOME}/schedules`
- minio 模式：`schedules`（object-key 前缀）

`Storage` 包内部不做 `GROOT_HOME` 拼接。

```
{baseDir}/
├── active/                     # 调度器只扫描此目录
│   ├── task-check-health/
│   │   ├── task.json           # 任务定义
│   │   └── executions/         # 执行历史
│   │       ├── 2026-05-11-090000.json
│   │       └── 2026-05-12-090005.json
│   └── task-weekly-report/
│       ├── task.json
│       └── executions/
│
├── disabled/                   # 被禁用的任务
│   └── task-pr-reminder/
│       ├── task.json
│       └── executions/
│
└── archive/                    # 已完成/废弃的任务
    └── task-once-migration/
        ├── task.json
        └── executions/
```

local 模式：磁盘真实目录；minio 模式：隐式前缀，由 `Storage.List/Stat` 在 CommonPrefix 上模拟"目录"。

#### 1.4.1 状态流转

```
  创建 ─→ active/ ──执行──→ active/(recurring，继续等下次)
                │
                ├── 手动禁用 ──→ disabled/
                │                  │
                │                  ├── 手动启用 ──→ active/
                │                  └── 手动归档 ──→ archive/
                │
                └── 一次性任务完成（status==completed）──→ archive/
```

- 只有 `active/` 被调度器扫描
- 状态变更通过 `storage.Storage.Rename` 整目录搬迁，不维护额外状态字段
- 不预创建 `active/` `disabled/` `archive/` 目录；`storage.Write` 在首次写入时按需建立

### 1.5 数据类型

#### 1.5.1 task.json

```json
{
  "id": "task-check-health",
  "name": "健康巡检",
  "schedule": "0 9 * * *",
  "missed_policy": "run_once",
  "task": {
    "instruction": "检查所有 MCP 服务健康状态，汇总报告",
    "model": "",
    "system_prompt": ""
  },
  "notification": {
    "on_success": ["webhook"],
    "on_failure": ["webhook", "email"]
  },
  "created_at": "2026-05-11T10:00:00Z",
  "updated_at": "2026-05-11T10:00:00Z"
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 唯一标识，由 Manager 根据 name 生成 kebab-case，前缀 `task-` |
| `name` | string | 任务名称 |
| `schedule` | string | 调度表达式，自动识别三种格式：cron / ISO8601 时间戳 / Go duration |
| `missed_policy` | enum | 重启后错过任务的策略：`run_once` / `skip`，默认 `run_once` |
| `task.instruction` | string | 任务要执行的指令 |
| `task.model` | string | 可选，指定 LLM 模型 |
| `task.system_prompt` | string | 可选，自定义 system prompt（结构体字段保留，但 `schedule_create` 工具入参不暴露——目前仅供内部预设） |
| `notification.on_success` | []string | 成功时通知的渠道名 |
| `notification.on_failure` | []string | 失败时通知的渠道名 |

> 渠道发送逻辑由消息层负责，见 [消息层设计](2026-05-11-message-design.md)。

#### 1.5.2 ScheduleType 自动识别

`ParseScheduleType(schedule string) ScheduleType`：

| 优先级 | 判定 | ScheduleType |
|---|---|---|
| 1 | `time.Parse(time.RFC3339, schedule)` 成功 | `ScheduleTypeOnce` |
| 2 | `time.ParseDuration(schedule)` 成功 | `ScheduleTypeInterval` |
| 3 | 兜底 | `ScheduleTypeCron` |

在 Engine.Register 中按 ScheduleType 分发到 `scheduler.AddCron / AddOnce / AddDuration`。

#### 1.5.3 执行记录 executions/{timestamp}.json

```json
{
  "task_id": "task-check-health",
  "exec_time": "2026-05-11T09:00:00+08:00",
  "trigger_type": "cron",
  "session_id": "task-check-health-20260511T090000-sched",
  "chat_id": "chat_20260511090000",
  "status": "completed",
  "duration_ms": 12340,
  "step_count": 5,
  "error": "",
  "notifications": [...]
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `task_id` | string | 所属任务 ID |
| `exec_time` | string | 计划执行时间（ISO8601） |
| `trigger_type` | string | `cron` / `once` / `interval` / `manual`（rerun） |
| `session_id` | string | 执行对应的 Agent 会话 ID |
| `chat_id` | string | 执行对应的对话 ID |
| `status` | string | `completed` / `failed` / `cancelled` |
| `duration_ms` | int64 | 执行耗时（毫秒） |
| `step_count` | int | Agent 执行步数 |
| `error` | string | 错误信息（成功时为空） |
| `notifications` | array | 通知发送结果列表 |

执行记录文件名 = `record.ExecTime.Format("2006-01-02-150405") + ".json"`，存放在 `{baseDir}/{status}/{taskID}/executions/`。

### 1.6 调度引擎

#### 1.6.1 技术选型

使用 `github.com/go-co-op/gocron/v2`。

#### 1.6.2 统一调度

整个 Groot 只有一个 `gocron.Scheduler` 实例：

- **系统 Job**：内存清理（替代 `CleanupScheduler`）、定期同步任务目录
- **用户 Job**：每个 `active/` 下的任务注册为一个 Job，通过 Tag `["user-task", taskID]` 标记便于管理

#### 1.6.3 动态管理

运行时增删任务通过 Tag 查找和操作 Job：

- 创建：`scheduler.AddCron / AddOnce / AddDuration` 带 tag
- 删除：`scheduler.RemoveByTag(taskID)`
- 启用：重新注册
- 禁用：`scheduler.RemoveByTag(taskID)` + 移目录

#### 1.6.4 取代 CleanupScheduler

原 `internal/memory/cleanup.go` 的自研 `CleanupScheduler`（基于 `time.After`）整体删除。Memory 清理实现 gocron `Task` 接口，作为系统 Job 注册到统一调度器。

`MemoryConfig.CleanupSchedule`（HH:MM 格式）字段保留；`schedule.ParseCleanupTime` 解析为 hour/minute。

### 1.7 Storage 模块（基于 storage.Storage 接口）

`Storage` 结构在启动期注入 `istorage.Storage`，所有持久化操作走接口方法（Read/Write/List/Stat/Delete/DeleteDir/Rename）。原子写、目录前缀语义、错误约定等均由 storage 实现层负责，schedule 包不再直接 `os.*`。

```go
type Storage struct {
    baseDir string             // 调用方拼好的路径或前缀
    store   istorage.Storage
    log     *logger.Logger
}

func NewStorage(baseDir string, store istorage.Storage, log *logger.Logger) *Storage
```

#### 1.7.1 写：SaveTask

```
data := json.MarshalIndent(task)
store.Write(ctx, {baseDir}/active/{id}/task.json, data, len(data), "application/json")
```

- 调用前在内存设置 `task.UpdatedAt = task.CreatedAt`（首次创建时 CreatedAt 同步赋值）
- 原子性由 `storage.Write` 保证
- `active/{id}/` 目录由 `storage.Write` 在首次写入时按需建立

#### 1.7.2 读：LoadTask

按 `[active, disabled, archive]` 顺序逐个 `storage.Read`：

- `ErrNotFound` → 继续下一个
- 其他错误 → 立即返回
- 命中 → `json.Unmarshal` 后返回

全部未命中 → `任务 {id} 不存在`。

#### 1.7.3 删：DeleteTask

按 `[active, disabled, archive]` 顺序：

1. `storage.Stat({baseDir}/{status}/{taskID})`
2. `ErrNotFound` → 继续；其他错误 → 透传
3. 命中 → `storage.DeleteDir({baseDir}/{status}/{taskID})` 整目录递归删

全部未命中 → `任务 {id} 不存在`。

#### 1.7.4 移：MoveTask

```
srcDir := {baseDir}/{from}/{taskID}
dstDir := {baseDir}/{to}/{taskID}
store.Stat(srcDir) — 不存在 → "任务 {id} 不在 {from} 中"
store.Rename(srcDir, dstDir)
```

local 模式：`os.Rename` 整目录搬迁。
minio 模式：`storage.Rename` 通过 CopyObject + DeleteObject 实现前缀级搬迁（具体见存储抽象 spec）。

#### 1.7.5 GetTaskStatus

按 `[active, disabled, archive]` 顺序 `storage.Stat`，命中即返回；都不命中返回空串。

#### 1.7.6 SaveExecution

```
status := GetTaskStatus(taskID)  — 空串 → 报错
filename := record.ExecTime.Format("2006-01-02-150405") + ".json"
storage.Write(ctx, {baseDir}/{status}/{taskID}/executions/{filename}, ...)
```

#### 1.7.7 LoadExecutions

```
status := GetTaskStatus(taskID)  — 空串 → 报错
storage.List({baseDir}/{status}/{taskID}/executions/)
  — ErrNotFound 视为空切片
对每个 .json 条目读取 → json.Unmarshal
跳过 IsDir 与非 .json 条目
单条解析失败 → 记 INFO 日志，跳过
按 ExecTime 倒序排列后返回
```

#### 1.7.8 ListActiveTasks / ListAllTasks / listTasksIn

```
storage.List({baseDir}/{status}/)
  — ErrNotFound 视为空切片
对每个 IsDir 条目，读 {entry.Path}/task.json
读失败 / unmarshal 失败 → 跳过
返回 []*Task
```

`ListAllTasks` 遍历三个状态合并；任意状态错误吞掉，继续下一个。

### 1.8 任务执行流程

`Runner.Run(taskID)` 返回一个 `func()`，由 gocron 调用：

```
1. LoadTask(taskID)  ── 失败 → 记 ERROR 直接返回
2. startTime = time.Now()
   sessionID = {taskID}-{YYYYMMDDTHHMMSS}-sched
3. memory.CreateSession(sessionID)  ── 失败 → 记 ERROR 返回
4. detectTriggerType(task.Schedule)  → cron/once/interval
5. 构造 agent.Task{Instruction, Prompt=SystemPrompt, ModelName=Model, Caller="schedule"}
6. agent.Executor.Execute(ctx, sessionID, agentTask, nil)
7. duration_ms = time.Since(startTime).Milliseconds()
   status = string(agentTask.Status)，空串兜底为 "completed"
8. 构造 ExecutionRecord（含 chat_id = "chat_{YYYYMMDDHHMMSS}"）
   storage.SaveExecution(taskID, record)
9. sendNotifications(task, status, agentTask.Result)
   按 status 选 OnSuccess / OnFailure 渠道，eventType="schedule.{status}"
   发布到 message.Layer，goroutine 异步收集结果并记日志
10. 一次性任务且 status == "completed" → MoveTask(taskID, "active", "archive")
    （失败任务保留在 active/，不归档；用户可查执行记录后手动处理）
11. 记 INFO 完成日志
```

#### 1.8.1 RunImmediate（rerun）

`Manager.Rerun(taskID)` → `Runner.RunImmediate(task)` 直接执行一次，不走 gocron：

- `trigger_type = "manual"`
- 不做一次性任务归档
- 流程其余与 `Run` 相同

#### 1.8.2 任务执行与 Session

每次任务执行创建一个独立 Session，格式 `{taskID}-{YYYYMMDDTHHMMSS}-sched`。

| 维度 | 普通 Session | 定时任务 Session |
|---|---|---|
| ID 格式 | `{timestamp}_{random}` | `{taskID}-{timestamp}-sched` |
| 创建方式 | 用户通过 `/chat` 发起 | Runner 自动创建 |
| caller 字段 | `user` | `schedule` |
| 生命周期 | 用户手动管理 | 跟随任务执行，完成后持久化 |
| 清理策略 | 受 `memory.retention_days` 控制 | 同上 |

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
| `Create(task)` | 生成 ID（如未指定）→ `SaveTask` → `engine.Register` |
| `List(status)` | `status` 为 `all` 或空串 → `ListAllTasks`；否则 `listTasksIn(status)` |
| `Get(taskID)` | `LoadTask` |
| `Delete(taskID)` | `engine.Unregister` → `DeleteTask` |
| `Disable(taskID)` | `engine.Unregister` → `MoveTask(taskID, "active", "disabled")` |
| `Enable(taskID)` | `MoveTask(taskID, "disabled", "active")` → `LoadTask` → `engine.Register` |
| `Archive(taskID)` | 当前状态为 active → `engine.Unregister`；`MoveTask(taskID, status, "archive")` |
| `GetHistory(taskID)` | `LoadExecutions` |
| `Rerun(taskID)` | `LoadTask` → `Runner.RunImmediate` |

#### 1.9.1 generateTaskID

输入 `task.Name`，输出 `task-{kebab-case}`：

- 转小写、空格与下划线 → `-`
- 仅保留 `[a-z0-9-]`
- 合并连续 `-`，去首尾 `-`
- 空字符串兜底为 `task-{UnixNano}`

`Stop` 命令在 spec 中保留供 CLI 暴露（§1.13），Manager 当前未实现 `Stop` 方法，CLI 入口由调用方决定如何对接（如尚未实现可仅提示"暂不支持"）。

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

`Register(task)`：

```go
taskFn := runner.NewTask(task.ID)
tags := []string{"user-task", task.ID}
switch ParseScheduleType(task.Schedule) {
case Cron:     scheduler.AddCron(task.Schedule, taskFn, tags...)
case Once:     scheduler.AddOnce(time.Parse(RFC3339, task.Schedule), taskFn, tags...)
case Interval: scheduler.AddDuration(time.ParseDuration(task.Schedule), taskFn, tags...)
}
```

`Unregister(taskID)`：`scheduler.RemoveByTag(taskID)`。

### 1.11 Sync：定期重注册（安全网）

`SyncTask(engine, storage, log)` 返回 `func()`，由 gocron 以固定间隔（默认 30 秒）触发：

1. `storage.ListActiveTasks()` — 失败 → 记 ERROR，返回
2. 遍历 active 任务，对每个调 `engine.Register(task)`
   - 重复注册由 scheduler 容错（同 tag 已存在则覆盖或忽略）
   - 单个失败记 INFO 日志，继续

> 当前实现仅做"重注册兜底"，不做差集计算（"内存有/磁盘无"或"磁盘有/内存无"）。这是有意的简化：任务的增删走 Manager 链路本身就保持一致性，sync 只是兜底"手工编辑文件"等异常路径，不需要复杂的差集算法。

`ParseCleanupTime(schedule)` 工具函数：解析 `HH:MM` 格式返回 `(hour, minute)`，非法默认 `(2, 0)`。

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

> `system_prompt` 不在工具入参中暴露：结构体字段保留供内部预设，但 Agent 当前无法通过对话直接设置。

#### 1.12.2 其他工具入参

`schedule_list` 接受 `status`（`active`/`disabled`/`archive`/`all`，默认 `all`）。

`delete/disable/enable/archive/history/inspect` 统一接受 `task_id`。

### 1.13 CLI 子命令

CLI 仅提供查看和管理能力，不提供创建：

```
groot schedule list                    # 列出所有任务
groot schedule history <task-id>        # 查看某任务执行历史
groot schedule inspect <task-id>        # 查看任务详情
groot schedule stop <task-id>           # 停止正在执行的任务（依赖 Manager.Stop，可未实现时降级为友好提示）
groot schedule delete <task-id>         # 删除任务（物理删除目录）
groot schedule disable <task-id>        # 禁用任务 (active → disabled)
groot schedule enable <task-id>         # 启用任务 (disabled → active)
groot schedule archive <task-id>        # 归档任务（→ archive）
groot schedule rerun <task-id>          # 立即重新执行一次任务
```

> API 端点定义已抽取至 [API 设计文档](2026-05-16-api-design.md)。

### 1.14 配置

`config.yaml` 新增 `schedule` 段：

```yaml
schedule:
  enabled: false                        # 是否允许在对话中创建定时任务（默认关闭，不影响系统级清理/同步任务）
  max_concurrent_tasks: 3               # 最大并发执行数
  sync_interval: 30s                    # 目录重注册间隔（active/ 任务的 gocron 兜底重注册）
```

### 1.15 错误处理

- **任务执行失败**：记 ERROR、保存执行记录（status=failed）、发布失败通知，recurring 任务下次继续执行
- **panic 恢复**：gocron 内置 panic 恢复，不会导致调度器崩溃
- **目录异常**：sync 定期重注册作为兜底
- **执行超时**：复用 `agent.Executor` 的超时机制
- **Storage 错误**：透传 `istorage.ErrNotFound` 等错误；调用方按需判定

### 1.16 日志规范

#### 1.16.1 任务生命周期

| 位置 | 事件 | 级别 | 内容 |
|---|---|---|---|
| Manager | 任务创建成功 | INFO | task_id、name、schedule |
| Manager | 任务创建/保存失败 | ERROR | task_id、错误 |
| Manager | 任务删除 | INFO | task_id |
| Manager | 任务禁用 | INFO | task_id（active → disabled） |
| Manager | 任务启用 | INFO | task_id（disabled → active） |
| Manager | 任务归档 | INFO | task_id |

#### 1.16.2 任务执行

| 位置 | 事件 | 级别 | 内容 |
|---|---|---|---|
| Runner | 开始执行 | INFO | task_id、name、session_id、trigger |
| Runner | 执行完成 | INFO | task_id、status、duration_ms、steps |
| Runner | 加载任务/创建 session/保存执行记录失败 | ERROR | task_id、错误 |
| Runner | 一次性任务归档 | INFO | task_id、name |
| Runner | 一次性任务归档失败 | ERROR | task_id、错误 |
| Runner | 通知发送成功 | INFO | task_id、channel |
| Runner | 通知发送失败 | ERROR | task_id、channel、reason |

#### 1.16.3 调度引擎

| 位置 | 事件 | 级别 | 内容 |
|---|---|---|---|
| Engine | 启动时加载 active/ | INFO | active_tasks |
| Engine | 单个任务加载失败 | INFO | task_id（跳过） |
| Sync | 同步开始 / 完成 | DEBUG | active_tasks |
| Sync | 同步注册任务失败 | INFO | task_id |
| Sync | 列出 active/ 失败 | ERROR | 错误 |

### 1.17 测试

| 文件 | 范围 |
|---|---|
| `schedule/storage_test.go` | Storage 各方法（与 mock storage.Storage 配合） |

> 当前未保留 `manager_test.go` / `tools_test.go` / `scheduler/scheduler_test.go`：Manager 本质是 Storage + Engine 的薄壳，行为通过 storage 测试覆盖；tools 通过系统测试（Python pytest）的对话端到端验证；scheduler 直接依赖 gocron。

## 二、迭代说明

### 2.1 与上一版差异

#### 存储层

- **调整**：所有持久化操作从 `os.*` 迁移到 `storage.Storage` 接口（Read / Write / List / Stat / Delete / DeleteDir / Rename）
- **新增**：`Storage` 结构体注入 `istorage.Storage`；`NewStorage` 签名增加 `store` 参数
- **调整**：`baseDir` 由调用方拼接传入；`Storage` 包内部不再做 `GROOT_HOME` 拼接
- **移除**：`EnsureDirs` 已删除——所有目录由 `storage.Write` 在首次写入时按需建立
- **新增**：minio 模式支持（同一 bucket 共享调度配置；`MoveTask` 走 `storage.Rename` 的 CopyObject + DeleteObject 实现）

#### 同步策略

- **简化**：原 spec 描述的"对比 active/ 与 gocron 内存状态做差集修复"未实现；当前实现为"对所有 active 任务重新调用 `engine.Register`"，差集计算与 WARN 日志暂未提供
- **澄清**：sync 的定位是"安全网"——任务增删走 Manager 链路本身保持一致性，sync 只兜底"手工编辑文件"等异常

#### 工具

- **澄清**：`schedule_create` 工具入参中**不暴露 `system_prompt`**；`TaskDef.SystemPrompt` 字段保留供内部预设
- **澄清**：8 个工具与原 spec 一致，名称与签名稳定

#### 一次性任务归档

- **明确**：仅在 `status == "completed"` 时归档；失败的一次性任务保留在 `active/`，等待用户手动处理（原 spec 未限定状态条件）

#### CLI

- **保留**：`groot schedule stop` 在 spec 中保留为命令位，但 Manager 当前未实现 `Stop` 方法（依赖 Agent 执行级取消机制，未来补齐）

#### 测试清单

- **调整**：当前仅保留 `storage_test.go`；`manager_test.go` / `tools_test.go` / `scheduler/scheduler_test.go` 已不存在
