# 定时任务通知设计文档

**日期**：2026-09-30
**状态**：实现稿

---

## 一、功能设计

### 1.1 功能概述

定时任务执行结束后，Groot 可以把执行结果通知给指定对象。通知要求由用户以自然语言描述，例如"用邮件把结果发给 a@example.com"或"失败时往飞书群发一条提醒"。任务结束后，系统在该次执行的会话里追加一轮简短的 Agent 执行，称为收尾通知轮。LLM 读取任务状态、结果和通知要求，调用已配置的 MCP 工具完成发送。

邮件、Webhook、即时通讯等发送能力都来自 `{GROOT_HOME}/mcp/` 下配置的 MCP 服务。Groot 本身不实现任何发送渠道。

### 1.2 能力清单

- **自然语言通知要求**：每个任务有成功与失败两段通知要求，都是自由文本；留空表示该状态下不通知。
- **按状态选择要求**：`completed` 使用成功要求，`failed`、`cancelled` 使用失败要求。
- **业务失败也能通知**：任务本身执行失败时，只要 LLM 服务可用，失败通知照常发出。
- **同会话执行**：通知轮与任务在同一会话中执行，作为该会话的一轮对话落库，可以在会话日志里查看调用了哪些工具。
- **有界执行**：单次通知轮有 2 分钟超时，结果正文按 4000 字符截断后嵌入指令。
- **旧数据可读**：通知字段不是字符串时（如数组 `["webhook"]` 或 `null`），读取结果为空要求，任务正常加载。

### 1.3 设计细节

#### 1.3.1 数据模型

```go
// internal/schedule/types.go
type NotificationConfig struct {
    OnSuccess string `json:"on_success"`
    OnFailure string `json:"on_failure"`
}
```

`NotificationConfig` 实现了自定义 `UnmarshalJSON`：字段值为 JSON 字符串时取原值，其他任何 JSON 值都解码为空串。通知配置随任务 payload 存入 `schedule_tasks.payload` 列。

#### 1.3.2 创建入口

内置工具 `schedule_create` 提供两个可选的字符串参数：

| 参数 | 说明 |
|---|---|
| `notify_on_success` | 任务成功后的通知要求，用自然语言描述通知方式与对象，需已配置相应的 MCP 工具；留空不通知 |
| `notify_on_failure` | 任务失败后的通知要求，要求同上 |

#### 1.3.3 执行流程

`Runner.Run` 与 `Runner.RunImmediate` 在保存执行记录之后调用 `Runner.notify`：

```
1. requirement = notifyRequirement(task, status)   // 按状态取要求并 TrimSpace
2. requirement 为空 → 返回
3. 构造 agent.Task：
     ID        = {sessionID}-notify               // 每次执行唯一，不与任务轮 chat_id 冲突
     Caller    = "schedule_notify"
     ModelName = task.TaskDef.Model
     Instruction = buildNotifyInstruction(...)
4. ctx 设 2 分钟超时，同步调用 executor.Execute(ctx, sessionID, notifyTask, nil)
5. 按 notifyTask.Status 记录日志：
     completed → INFO  "任务通知已完成"
     其他      → ERROR "任务通知失败"（附 reason）
```

通知轮在任务调度线程中同步执行，执行完才进入一次性任务归档等后续步骤。

#### 1.3.4 通知指令

`buildNotifyInstruction` 生成的指令包含以下几部分：

| 段落 | 内容 |
|---|---|
| 开头 | 说明定时任务已结束，要求按通知要求使用可用工具发送通知 |
| 任务执行信息 | 任务名称、任务 ID、执行状态、开始时间（RFC3339）、耗时；失败时附错误信息 |
| 执行结果 | 任务最终输出，超过 4000 字符时截断并标注"内容过长已截断" |
| 通知要求 | 用户填写的原文 |
| 约束 | 只使用可用工具完成通知，不重新执行任务，不创建、修改或删除定时任务，完成后简要说明通知结果 |

#### 1.3.5 结果回写

`agent.Executor.Execute` 执行结束时会把最终状态、结果正文和错误回写到传入的 `agent.Task` 的 `Status`、`Result`、`Error` 字段，`Runner` 从这里读取任务结果并填进通知指令。Solo 模式下子 Agent 不可用时，`Error.Code` 为 `subagent_unavailable`。

#### 1.3.6 能力边界

- 通知依赖 LLM 服务可用。模型配置失效或服务不可达时通知轮失败，只在日志里留下"任务通知失败"。
- 发送渠道能否使用，取决于 `{GROOT_HOME}/mcp/` 中是否配置了相应的 MCP 服务。没有可用工具时，LLM 会在回复中说明无法完成。
- 通知轮的执行过程不写入 `ExecutionRecord`。

#### 1.3.7 测试

`internal/schedule/notify_test.go`：

- `TestNotificationConfig_UnmarshalJSON`：字符串、旧数组、null、字段缺失四种输入。
- `TestNotificationConfig_TaskRoundTrip`：含旧格式的任务 payload 能加载，序列化往返后通知要求不丢失。
- `TestBuildNotifyInstruction`：成功指令含结果与要求且不含错误段；失败指令含错误；超长结果截断。
- `TestRunner_Notify`：借助 `taskExecutor` 接口注入假执行器，验证要求为空时不执行、按状态选要求、会话与 ID/Caller/模型正确、执行失败不 panic。

## 二、迭代说明

### 2.1 与上一版差异

- 移除：消息通知层 `internal/message`（事件队列、worker 池、Webhook Sender 及其测试）。
- 移除：配置表分类 `setting/message.go`、Web 接口 `GET/PUT /web/settings/senders`、设置面板的"Webhook 通知"分组及对应前端 API、store、文案。
- 移除：`bootstrap.yaml` 的 `message` 段（`queue_size`、`workers`）与 `MessageBootstrap`、`MessageConfig`、`SenderConf` 类型；老 `config.yaml` 迁移不再处理 `message.senders`。旧文件中残留的 `message:` 段读取时会被忽略。配置表中残留的 `message.senders.webhook.*` 行没有读取方。
- 移除：`ExecutionRecord.Notifications` 字段与 `NotificationResult` 类型。
- 调整：`NotificationConfig` 的字段类型由渠道名列表 `[]string` 改为自然语言要求 `string`。旧任务里的 `["webhook"]` 读取后为空，需要重新设置通知要求。
- 调整：`schedule_create` 的 `notify_on_success` / `notify_on_failure` 参数类型由字符串数组改为字符串。
- 调整：`schedule.NewRunner` 去掉消息层参数，`Runner.executor` 改为包内接口 `taskExecutor`。
- 新增：`internal/schedule/notify.go` 收尾通知轮。
- 修复：`agent.Executor.Execute` 执行结束时把 `Result` 与 `Error` 回写到 `agent.Task`（此前只回写 `Status`，调用方拿不到结果正文与错误）。
- 重命名：集群消息仓储层统一加 `Cluster` 前缀，包括 `internal/repo/message.go` → `cluster_message.go`、`MessageRepo` → `ClusterMessageRepo`、`MessageConsumer` → `ClusterMessageConsumer`、`internal/repo/messagedb` → `internal/repo/clustermsgdb`、`Repos.Message` → `Repos.ClusterMessage`。数据库表名不变。
- 归档：`2026-05-11-message-design.md` 与 `2026-09-27-webhook-only-notification-design.md` 移入 `archive/`。
