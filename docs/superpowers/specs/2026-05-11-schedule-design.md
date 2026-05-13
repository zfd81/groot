# 定时任务调度系统设计

## 概述

为 Groot 添加定时任务调度能力，用户通过对话创建定时任务，系统在指定时间自动执行 Agent 指令，执行结果通过消息层推送通知。

## 核心原则

- **创建走对话**：任务只能由 Agent 通过内置工具创建，CLI 和 API 不提供创建入口
- **统一调度引擎**：用 gocron 统一管理所有系统定时（含现有内存清理），单 goroutine 轮询
- **文件存储**：遵循现有架构风格，每任务一个目录，分 active/disabled/archive 三种状态
- **消息层解耦**：执行结果推入消息层（独立模块），由消息层统一路由分发，不经过 LLM

## 目录结构

### 源码目录

```
internal/
├── scheduler/              # 通用调度引擎封装（gocron）
│   └── scheduler.go        # Scheduler - 启动/停止/注册 Job
│
├── schedule/               # 定时任务应用层
│   ├── engine.go           # Engine - 调度器管理，启动时加载 active/ 下任务
│   ├── manager.go          # Manager - 任务生命周期管理（CRUD、启停、归档）
│   ├── runner.go           # Runner - 任务执行器，调用 agent.Executor + 消息层
│   ├── storage.go          # Storage - 文件持久化（读写 task.json、执行记录）
│   ├── tools.go            # built-in tools - Agent 侧工具定义与实现
│   ├── types.go            # 数据类型
│   ├── sync.go             # 定期同步 active/ 目录到 gocron
│   └── manager_test.go     # 单元测试
│
├── memory/cleanup.go       # 改为实现 gocron Task 接口
└── cmd/
    └── schedule.go         # CLI 子命令
```

### 数据目录

```
{GROOT_HOME}/schedules/
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

### 状态流转

```
  创建 ─→ active/ ──执行──→ active/(recurring，继续等下次)
                │
                ├── 手动禁用 ──→ disabled/
                │                  │
                │                  ├── 手动启用 ──→ active/
                │                  └── 手动归档 ──→ archive/
                │
                └── 一次性任务完成 ──→ archive/
```

- 只有 `active/` 被调度器扫描
- 移动目录即状态变更，不需要维护额外的状态字段

## 数据类型

### task.json

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

字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 唯一标识，由 Agent 根据名称生成 kebab-case |
| `name` | string | 任务名称 |
| `schedule` | string | 调度表达式，支持三种格式自动识别：cron 表达式（`0 9 * * *`）、ISO8601 时间戳（一次性任务）、Go duration（`30m`/`1h` 间隔执行） |
| `missed_policy` | enum | 重启后错过任务的策略：`run_once` / `skip` |
| `task.instruction` | string | 任务要执行的指令 |
| `task.model` | string | 可选，指定 LLM 模型 |
| `task.system_prompt` | string | 可选，自定义 system prompt |
| `notification.on_success` | []string | 成功时通知的渠道名 |
| `notification.on_failure` | []string | 失败时通知的渠道名 |

> 渠道的发送逻辑由消息层负责。见 [消息层设计](2026-05-11-message-design.md)。

### 执行记录 executions/{timestamp}.json

```json
{
  "task_id": "task-check-health",
  "exec_time": "2026-05-11T09:00:00+08:00",
  "trigger_type": "cron",
  "session_id": "task-check-health-20260511T090000-sched",
  "chat_id": "chat-mzxa1b2c3d4e5f6g7h8i9j0",
  "status": "completed",
  "duration_ms": 12340,
  "step_count": 5,
  "error": "",
  "notifications": [
    {
      "channel": "webhook",
      "sent": true,
      "message": "发送成功",
      "timestamp": "2026-05-11T09:00:13+08:00"
    }
  ]
}
```

字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `task_id` | string | 所属任务 ID |
| `exec_time` | string | 计划执行时间（ISO8601） |
| `trigger_type` | string | 触发类型：`cron` / `once` / `interval` / `manual`（rerun） |
| `session_id` | string | 执行对应的 Agent 会话 ID |
| `chat_id` | string | 执行对应的对话 ID |
| `status` | string | 执行结果：`completed` / `failed` / `cancelled` |
| `duration_ms` | int | 执行耗时（毫秒） |
| `step_count` | int | Agent 执行步数 |
| `error` | string | 错误信息（成功时为空） |
| `notifications` | array | 通知发送结果列表 |
| `notifications[].channel` | string | 渠道名 |
| `notifications[].sent` | bool | 是否发送成功 |
| `notifications[].message` | string | 发送结果描述 |
| `notifications[].timestamp` | string | 发送时间（ISO8601） |

## 调度引擎

### 技术选型

使用 `github.com/go-co-op/gocron/v2`，替代现有自研的 `time.After` 循环。

### 统一调度

整个 Groot 只有一个 `gocron.Scheduler` 实例：

- **系统 Job**：内存清理（替代 `CleanupScheduler`）、定期同步任务目录
- **用户 Job**：每个 active/ 下的任务注册为一个 Job，通过 Tag 标记便于管理

```go
s, _ := gocron.NewScheduler(gocron.WithLocation(time.Local))

// 系统 Job
s.NewJob(gocron.DailyJob(1, gocron.NewAtTimes(
    gocron.NewAtTime(2, 0, 0),
)), gocron.NewTask(cleanupJob.Run))

s.NewJob(gocron.DurationJob(30*time.Second),
    gocron.NewTask(syncActiveTasks))

// 用户 Job（启动时从 active/ 加载）
for _, task := range loadActiveTasks() {
    s.NewJob(task.CronJob(), gocron.NewTask(runner.Run(task))).
        Tag("user-task", task.ID)
}

s.Start()
```

### 动态管理

运行时增删任务通过 Tag 查找和操作 Job：

- 创建：`s.NewJob(...).Tag("user-task", taskID)`
- 删除：`s.RemoveByTag(taskID)`
- 启用：重新注册
- 禁用：`s.RemoveByTag(taskID)` + 移目录

### 取代现有 CleanupScheduler

现有 `internal/memory/cleanup.go` 中的自研 `CleanupScheduler`（基于 `time.After` 的简易定时器）**整体删除**，Memory 清理逻辑改为实现 gocron 的 `Task` 接口，作为系统 Job 注册到统一调度器中。

- `MemoryConfig.CleanupSchedule`（HH:MM 格式）字段保留，用于配置每日清理时间
- 清理逻辑本身不变（遍历目录、判断过期、删除会话），只改变触发方式
- gocron 提供了 panic 恢复、日志、优雅停止等基础设施，不再需要 Memory 自己管理

> 详细设计见 [Memory 模块设计](2026-05-11-memory-design.md)。

## 任务执行流程

```
gocron 触发任务
  │
  ▼
Runner.Run(task)
  │
  ├── 1. 创建 session（{taskID}-{timestamp}-sched）
  ├── 2. 构建 Task{Instruction, Model, ...}
  ├── 3. agent.Executor.Execute(ctx, sessionID, task, nil, cancelCh)
  │       │
  │       └── Agent 执行指令（复用现有 ReAct 循环）
  │
  ├── 4. 保存执行记录
  │
  ├── 5. 发布事件到消息层
  │       │
  │       └── message.Layer.Publish(Event{Type: "schedule.completed"/"schedule.failed", ...})
  │             │
  │             └── 消息层并发分发到对应渠道
  │
  ├── 6. 一次性任务 → 移到 archive/
  │
  └── 7. 执行完成
```

> 消息层具体架构见 [消息层设计](2026-05-11-message-design.md)。

### 任务执行与 Session

每次任务执行创建一个独立 Session，每次任务执行创建一个独立 Session，格式为 `{taskID}-{timestamp}-sched`。`-sched` 后缀标记放在末尾，不影响时间戳位置，便于识别和过滤。

**与普通 Session 的区分：**

| 维度 | 普通 Session | 定时任务 Session |
|------|------------|----------------|
| ID 格式 | `{timestamp}_{random}` | `{taskID}-{timestamp}-sched` |
| 创建方式 | 用户通过 `/chat` 发起 | Runner 自动创建 |
| caller 字段 | `user` | `schedule` |
| 生命周期 | 用户手动管理 | 跟随任务执行，完成后持久化 |
| 清理策略 | 受 memory.retention_days 控制 | 同上，统一清理 |

**为什么每次执行一个独立 Session：**
- 复用现有 `agent.Executor` 和 Memory 持久化机制，无需额外开发
- 每次执行有独立的 `chat_id`，执行记录可追溯
- 清理逻辑基于 `history.json` 的 `created_at` 字段，不解析 session ID，无需修改
- 通过 `-sched` 后缀和 `caller` 字段即可在列表/查询中过滤定时任务会话

## 内置工具

定时任务管理通过内置工具直接注册到 Agent，用户通过对话触发：

| 工具 | 说明 |
|------|------|
| `schedule_create` | 创建定时任务 |
| `schedule_list` | 查询所有任务（支持状态过滤） |
| `schedule_delete` | 删除任务 |
| `schedule_disable` | 禁用任务（active → disabled） |
| `schedule_enable` | 启用任务（disabled → active） |
| `schedule_archive` | 归档任务（→ archive） |
| `schedule_history` | 查看某任务执行历史 |
| `schedule_inspect` | 查看任务详情 |

`schedule_create` 参数：

| 参数 | 必填 | 说明 |
|------|------|------|
| `name` | 是 | 任务名称 |
| `schedule` | 是 | 调度表达式，支持三种格式：cron 表达式（`0 9 * * *`）、ISO8601 时间戳（一次性）、Go duration（`30m`/`1h` 间隔） |
| `instruction` | 是 | 要执行的指令 |
| `model` | 否 | 指定 LLM 模型 |
| `missed_policy` | 否 | 错过策略，默认 `run_once` |
| `notify_on_success` | 否 | 成功通知渠道列表 |
| `notify_on_failure` | 否 | 失败通知渠道列表 |

## CLI 子命令

CLI 仅提供查看和管理能力，不提供创建：

```
groot schedule list                    # 列出所有任务
groot schedule history <task-id>        # 查看某任务执行历史
groot schedule inspect <task-id>        # 查看任务详情
groot schedule stop <task-id>           # 停止正在执行的任务
groot schedule delete <task-id>         # 删除任务（物理删除目录）
groot schedule disable <task-id>        # 禁用任务 (active → disabled)
groot schedule enable <task-id>         # 启用任务 (disabled → active)
groot schedule archive <task-id>        # 归档任务（→ archive）
groot schedule rerun <task-id>          # 立即重新执行一次任务
```

## API 端点

API 仅提供查看和管理能力，不提供创建：

```
GET    /schedule                     # 列出所有任务
GET    /schedule/:id                 # 查看任务详情
GET    /schedule/:id/history         # 查看执行历史
DELETE /schedule/:id                 # 删除任务
POST   /schedule/:id/stop            # 停止正在执行的任务
POST   /schedule/:id/disable         # 禁用任务
POST   /schedule/:id/enable          # 启用任务
POST   /schedule/:id/archive         # 归档任务
POST   /schedule/:id/rerun           # 立即重新执行
```

## 配置扩展

`config.yaml` 新增 `schedule` 段：

```yaml
schedule:
  enabled: false                        # 是否允许在对话中创建定时任务（默认关闭，不影响系统级清理/同步任务）
  max_concurrent_tasks: 3               # 最大并发执行数
  sync_interval: 30s                    # 目录同步间隔（定期对比 active/ 目录与 gocron 内存状态，修复手动改文件等导致的不一致；正常运行中走 Manager 保证一致性，此仅为安全网）
```

## 错误处理

- **任务执行失败**：记录错误信息到执行记录，发布失败通知，recurring 任务下次继续执行
- **panic 恢复**：gocron 内置 panic 恢复，不会导致调度器崩溃
- **目录异常**：sync 定期扫描 active/ 做一致性修复，修复手动改文件导致的不一致
- **执行超时**：复用现有 agent.Executor 的超时机制

## 日志规范

调度系统使用统一日志记录，关键步骤均有日志，级别 INFO / WARN / ERROR。

### 任务生命周期

| 位置 | 事件 | 级别 | 内容 |
|------|------|------|------|
| Manager | 任务创建成功 | INFO | task_id、name、schedule |
| Manager | 任务创建失败 | ERROR | 失败原因 |
| Manager | 任务删除 | INFO | task_id、name |
| Manager | 任务禁用 | INFO | task_id、原因（active → disabled） |
| Manager | 任务启用 | INFO | task_id（disabled → active） |
| Manager | 任务归档 | INFO | task_id、原因（→ archive） |
| Manager | 操作时目录移动失败 | ERROR | task_id、源目录、目标目录、错误 |

### 任务执行

| 位置 | 事件 | 级别 | 内容 |
|------|------|------|------|
| Runner | 开始执行 | INFO | task_id、name、session_id、触发类型（cron/once/interval/manual） |
| Runner | 执行完成 | INFO | task_id、status（completed/failed/cancelled）、duration_ms、step_count |
| Runner | 执行失败 | ERROR | task_id、错误信息、duration_ms |
| Runner | 一次性任务归档 | INFO | task_id、原因（once 任务执行完毕，移入 archive） |
| Runner | 通知发送成功 | INFO | task_id、渠道名 |
| Runner | 通知发送失败 | ERROR | task_id、渠道名、失败原因 |

### 调度引擎

| 位置 | 事件 | 级别 | 内容 |
|------|------|------|------|
| Engine | 启动时加载 active/ | INFO | 加载到的任务数量 |
| Engine | 单个任务加载失败 | WARN | task_id、失败原因（跳过该任务继续加载） |
| Engine | 调度器启动 | INFO | - |
| Engine | 调度器停止 | INFO | - |
| Sync | 同步开始 | DEBUG | active/ 中任务数、gocron 中 Job 数 |
| Sync | 发现不一致 | WARN | 具体差异（多余/缺失的 task_id） |
| Sync | 同步完成后 | DEBUG | 修复后的任务数 |

### 日志示例

```log
2026-05-11 10:30:00 [INFO] [schedule] 任务创建成功 task_id=task-check-health name=健康巡检 schedule="0 9 * * *"
2026-05-11 09:00:00 [INFO] [schedule] 开始执行 task_id=task-check-health name=健康巡检 session_id=task-check-health-20260511T090000-sched trigger=cron
2026-05-11 09:00:12 [INFO] [schedule] 执行完成 task_id=task-check-health status=completed duration=12340ms steps=5
2026-05-11 09:00:13 [INFO] [schedule] 通知发送成功 task_id=task-check-health channel=webhook
2026-05-11 02:00:00 [DEBUG] [schedule] 同步检查 active_tasks=5 gocron_jobs=5
```

## 测试

- `schedule/manager_test.go` — Manager CRUD 操作测试
- `schedule/storage_test.go` — 文件持久化测试
- `schedule/tools_test.go` — 工具参数校验测试
- `scheduler/scheduler_test.go` — 调度器注册/注销 Job 测试
