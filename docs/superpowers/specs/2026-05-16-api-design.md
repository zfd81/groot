# Groot HTTP API 设计

## 一、功能设计

### 1.1 概述

Groot 提供 RESTful HTTP API，客户端通过 API 与 AI Agent 交互。核心交互模式为 **POST + SSE 流式响应**：客户端提交指令，服务端通过 Server-Sent Events 实时推送 AI 的思考过程、工具调用和最终结果。

实现入口：[`internal/api/server.go`](../../../internal/api/server.go) 构造 Hertz 服务并注入各 Handler；路由注册集中在 [`internal/api/router.go`](../../../internal/api/router.go)。

**技术栈：**

| 项目 | 选型 |
|------|------|
| 传输协议 | HTTP/1.1 |
| Web 框架 | Hertz (`github.com/cloudwego/hertz`) |
| 数据格式 | JSON |
| 流式协议 | Server-Sent Events (SSE) |
| 认证方式 | API Key（`X-API-Key` header，可配置开关） |

**设计原则：**

- 会话模型：Session（会话）包含多轮 Chat（对话），每轮对话有独立的 `chat_id`。
- 横切关注点分离：认证、限流、附件校验、错误码作为独立章节，各端点引用。
- 无状态请求：每个请求独立创建所需实例（如 ChatModel），不跨请求共享。

### 1.2 通用约定

#### 1.2.1 端点总览

| 端点 | 方法 | 用途 | 响应类型 |
|------|------|------|---------|
| `/health` | GET | 健康检查 | JSON |
| `/chat` | POST | 执行对话 | SSE 流 |
| `/chat/status/:sid` | GET | 查询最新对话状态 | JSON |
| `/chat/:sid` | GET | 查询最新对话详情（含步骤） | JSON |
| `/chat/:sid/:cid` | GET | 查询指定对话详情 | JSON |
| `/sess/:sid` | GET | 查询会话详情（全量历史） | JSON |
| `/sess/history` | GET | 查询会话列表 | JSON（分页） |
| `/agents` | GET | 列出可调用 Agent（含其 Skills） | JSON |
| `/skills` | GET | 列出可用 Skill | JSON |
| `/tools` | GET | 列出可用 MCP 工具（按 MCP 分组） | JSON |
| `/models` | GET | 列出已配置 LLM 模型 | JSON |
| `/schedule/` | GET | 列出定时任务（支持 `?status=`） | JSON 数组 |
| `/schedule/:id` | GET | 查看任务详情 | JSON |
| `/schedule/:id` | DELETE | 删除任务 | JSON |
| `/schedule/:id/disable` | POST | 禁用任务 | JSON |
| `/schedule/:id/enable` | POST | 启用任务 | JSON |
| `/schedule/:id/archive` | POST | 归档任务 | JSON |
| `/schedule/:id/history` | GET | 查看执行历史 | JSON 数组 |

`/health` 不经过认证 / 限流中间件，其余端点全部挂在带 `AuthMiddleware` + `RateLimitMiddleware` 的 group 下（见 [`router.go`](../../../internal/api/router.go) `apiGroup`）。

#### 1.2.2 ID 格式约定

ID 生成实现见 [`internal/memory/idgen.go`](../../../internal/memory/idgen.go)。

| ID 类型 | 格式 | 示例 |
|---------|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260418103000523_a1b2` |
| `chat_id` | `{YYYYMMDDHHMMSSmmm}`（17 位纯数字） | `20260418103000523` |
| `step_id` | `{YYYYMMDD}-{HHMMSSmmm}-{random6}` | `20260418-103000000-a1b2c3` |
| 子 Agent `chat_id` | `{parentChatID}_{HHMMSSmmm}_{random4}_{agentName}` | — |

路径中的 `:sid` 为 `session_id`，`:cid` 为 `chat_id`，`:id` 为调度任务 ID。

#### 1.2.3 请求体大小限制

最大请求体大小为 **200MB**（在 [`server.go`](../../../internal/api/server.go) 中通过 `server.WithMaxRequestBodySize` 设置），用于支持大型 Base64 编码附件。

#### 1.2.4 通用请求头

| Header | 说明 |
|--------|------|
| `Content-Type` | `application/json`（所有 POST 请求必需） |
| `X-Session-ID` | 会话 ID，用于续接已有会话（详见 1.4.4） |
| `X-Model-Name` | 指定 LLM 模型名（详见 1.4.5） |
| `X-User-ID` | 业务方传入的用户 ID，写入 `memory_sessions.user_id` |
| `X-Agent-Name` | 选择主 / 子 Agent（详见 1.4.6） |
| `X-API-Key` | API Key（启用认证时必需，详见第二章 2.1） |

#### 1.2.5 通用响应格式

对话 / 会话类端点的成功响应均包含 `"status": "success"` 作为顶层字段：

```json
{
  "status": "success",
  "<data_field>": { ... }
}
```

以下端点不使用 `status` 包装：

- `/health` — 自带 `status` 但作为聚合健康字段（`healthy` / `unhealthy`）。
- `/agents` — 直接返回 `{"agents":[...]}`。
- `/skills` — 直接返回 `{"skills":[...], "total":N}`。
- `/tools` — 直接返回 `map[mcp_name]ToolsGroup`。
- `/models` — 直接返回 `{"models":[...], "default":"...", "total":N}`。
- `/schedule/`、`/schedule/:id`、`/schedule/:id/history` — 直接返回数组或对象。
- `/schedule/:id/disable` 等动作 — 返回 `{"status": "动作名", "id": "..."}`。

错误响应包含 `status`（错误码）和 `message`（可读描述）：

```json
{
  "status": "invalid_model",
  "message": "模型 'xxx' 不存在"
}
```

错误码速查见第二章 2.4。

#### 1.2.6 数据模型

**ChatRecord** — 单次对话详细记录（`memory_chats` 表的一行）。结构定义见 [`internal/repo/memory.go`](../../../internal/repo/memory.go)。

| 字段 | 类型 | 说明 |
|------|------|------|
| `chat_id` | string | 对话 ID |
| `session_id` | string | 所属会话 ID |
| `round` | int | 轮次编号 |
| `prompt` | string | 系统提示词 |
| `timestamp` | time | 记录时间戳 |
| `started_at` | time | 开始时间 |
| `ended_at` | time | 结束时间 |
| `instruction` | string | 用户指令 |
| `result` | string | 执行结果摘要 |
| `status` | string | 执行状态：`completed` / `failed` / `cancelled` |
| `duration` | int | 执行耗时（秒） |
| `duration_ms` | int64 | 执行耗时（毫秒） |
| `caller` | string | 调用方标识 |
| `steps` | []Step | 执行步骤列表 |
| `agent_name` | string | 子 Agent 名（编排模式下子 Agent 步骤会写入；主 Agent 为空） |
| `model` | string | 实际使用的模型名 |
| `prompt_tokens` | int | 输入 token 数 |
| `completion_tokens` | int | 输出 token 数 |
| `total_tokens` | int | token 总数 |
| `error` | *Error | 错误信息（无错误时为 null） |

**Message** — 单轮对话摘要（由 `Memory.GetHistory` 从 `memory_chats` 实时聚合生成）。结构定义见 [`internal/memory/types.go`](../../../internal/memory/types.go)。

| 字段 | 类型 | 说明 |
|------|------|------|
| `round` | int | 轮次编号 |
| `chat_id` | string | 对话 ID |
| `timestamp` | time | 时间戳 |
| `instruction` | string | 用户指令 |
| `result` | string | 执行结果摘要 |
| `status` | string | 执行状态：`completed` / `failed` / `cancelled` |
| `duration` | int | 执行耗时（秒） |
| `steps_count` | int | 步骤数量 |
| `agent_name` | string | 子 Agent 名（可选） |
| `error` | *Error | 错误信息（无错误时为 null） |

**Step** — 单步执行记录。

| 字段 | 类型 | 说明 |
|------|------|------|
| `step_id` | string | 步骤 ID（格式见 1.2.2） |
| `type` | string | 步骤类型：`skill` / `tool` / `llm` / `thinking` |
| `name` | string | Skill 名称或工具名称 |
| `start_time` | time | 开始时间 |
| `end_time` | time | 结束时间 |
| `status` | string | 执行状态：`success` / `failed` |
| `nesting_level` | int | 嵌套深度 |
| `error` | *Error | 错误信息（无错误时为 null） |

**Error** — 错误信息：`code`、`message` 两个字符串字段。

**SessionInfo** — 会话信息（[`internal/memory/types.go`](../../../internal/memory/types.go)）。

| 字段 | 类型 | 说明 |
|------|------|------|
| `session_id` | string | 会话 ID |
| `created_at` | time | 创建时间 |
| `round_count` | int | 对话轮次总数 |
| `last_active_at` | string | 最后活跃时间（`memory_sessions.updated_at`，仅当 `round > 0` 才填） |
| `path` | string | 历史字段，DB 模式下固定为空字符串 |

### 1.3 中间件

实现位置：[`internal/api/middleware/`](../../../internal/api/middleware/)。

- `AuthMiddleware`（[`auth.go`](../../../internal/api/middleware/auth.go)）：API Key 鉴权与基于路径的权限校验，未配置或禁用时把 `caller` 设为 `anonymous`。
- `RateLimitMiddleware`（[`ratelimit.go`](../../../internal/api/middleware/ratelimit.go)）：依据已认证 caller 或客户端 IP 进行 QPS / 并发限制；POST `/chat` 走 `Acquire`/`Release`，其它路径走 `Allow`。

详细规则见第二章。

### 1.4 对话执行 — POST /chat

实现：[`internal/api/handler/chat.go`](../../../internal/api/handler/chat.go)。客户端提交指令，服务端通过 SSE 流式返回 AI 的思考过程、工具调用和最终回答。

#### 1.4.1 请求

**请求头**

| Header | 必需 | 说明 |
|--------|------|------|
| `Content-Type` | 是 | `application/json` |
| `X-Session-ID` | 否 | 会话 ID。为空或对应会话不存在则创建新会话 |
| `X-Model-Name` | 否 | 指定模型名。为空使用 `llm.default_model`；模型不存在返回 400 |
| `X-User-ID` | 否 | 业务方用户 ID，仅在创建新会话时写入 `memory_sessions.user_id` |
| `X-Agent-Name` | 否 | Solo 模式入口：指向已注册子 Agent；空值或 `groot` 走主 Agent 编排模式；未注册返回 400 |
| `X-API-Key` | 否* | API Key（启用认证时必需） |

**请求体**

```json
{
  "instruction": "用户任务指令",
  "prompt": "系统提示词（可选）",
  "attachments": [
    {
      "type": "image",
      "name": "screenshot.png",
      "content": "base64编码内容"
    }
  ]
}
```

| 字段 | 必需 | 说明 |
|------|------|------|
| `instruction` | 是 | 用户任务指令，不能为空 |
| `prompt` | 否 | 系统提示词，设定 Agent 角色和行为约束 |
| `attachments` | 否 | 附件列表 |

**附件字段**

| 字段 | 必需 | 说明 |
|------|------|------|
| `type` | 是 | `file` / `image` / `audio` / `video` 之一 |
| `name` | 是 | 文件名含扩展名 |
| `content` | 是 | Base64 编码内容 |

#### 1.4.2 处理流程

```
POST /chat 请求到达
  │
  ├─ 1. 请求校验
  │     ├─ 解析 JSON body，instruction 非空
  │     ├─ 校验 X-Model-Name（不存在 → 400 invalid_model）
  │     ├─ 校验 X-Agent-Name（非空且非 "groot" 时必须命中 SubAgentRegistry，否则 400 unknown_agent）
  │     └─ 附件校验（数量、类型、大小，详见第二章 2.3）
  │
  ├─ 2. 并发预检
  │     └─ 已传 X-Session-ID 且 RuntimeState.IsRunning(sid) 为真 → 409 chat_limit_exceeded
  │
  ├─ 3. 会话处理（见 1.4.4）
  │     ├─ 新会话：生成 session_id；round=1；historyMessages 为空
  │     └─ 续接：round = GetRoundCount + 1；GetContextMessages 取 history_window 条
  │
  ├─ 4. 注册活跃对话
  │     ├─ 生成 chat_id（GenerateChatID）
  │     ├─ RuntimeState.Register（LoadOrStore；冲突 → 409）
  │     └─ 注册成功后再 memory.CreateSession（如果是新会话），写入 user_id
  │
  ├─ 5. 附件处理
  │     ├─ Base64 解码
  │     ├─ file 类型：解码后的文本进入 MultimodalContent.DecodedContent
  │     └─ image / audio / video：保留 Base64 数据进入 MultimodalContent.Base64Data
  │
  ├─ 6. 写响应头
  │     ├─ X-Session-ID、X-Chat-ID
  │     └─ Content-Type: text/event-stream / Cache-Control: no-cache / Connection: keep-alive
  │
  ├─ 7. 异步 Agent 执行 → SSE 流式输出（见 1.4.7）
  │
  └─ 8. 完成处理
        ├─ Executor 内部 SaveChatRecord，事务里更新 memory_sessions.round / updated_at
        └─ goroutine defer 清理 RuntimeState 注册项
```

#### 1.4.3 注意事项

- **并发限制**：同一会话同时只能有一个活跃对话，冲突返回 409 `chat_limit_exceeded`。
- **取消机制**：客户端断开 SSE 连接后，HTTP 请求上下文取消，Agent 在下一次循环检查点终止执行。
- **轮次管理**：续接会话时 `round` 自增，首次对话 `round=1`。

#### 1.4.4 会话处理

会话（Session）是对话的容器，一次会话可包含多轮对话（Chat）。

| 请求 sid | 会话存在 | 行为 | 轮次 |
|----------|----------|------|------|
| 空 | — | 生成新 sid，创建会话 | 1 |
| 有值 | 否 | 生成新 sid，创建会话 | 1 |
| 有值 | 是 | 沿用 sid，追加轮次 | +1 |

#### 1.4.5 模型选择 (`X-Model-Name`)

通过 `X-Model-Name` 请求头，每次请求可动态指定 LLM 模型，实现同一会话不同对话使用不同模型。

**配置文件结构**（[`internal/config/config.go`](../../../internal/config/config.go)）：

```yaml
llm:
  default_model: gpt-4o
  models:
    gpt-4o:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4o
      max_completion_tokens: 4096
      temperature: 0.7
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_completion_tokens: 4096
      temperature: 0.7
```

| 场景 | 处理 | 结果 |
|------|------|------|
| `X-Model-Name` 命中 `models` 中某个 key | 使用指定模型 | 正常执行 |
| `X-Model-Name` 为空 | 使用 `default_model` | 正常执行 |
| `X-Model-Name` 未命中 | 严格校验失败 | 400 `invalid_model` |
| `models` 为空 / `default_model` 缺失 | 启动期 `ValidateLLMConfig` 报错 | 服务启动失败 |

数据流：handler 把 `X-Model-Name` 写入 `Task.ModelName` → `Executor.Execute` 透传 → `Engine.Run` 调用 `llm.NewChatModel(ctx, llmConfig, modelName)`，每次请求独立创建模型实例（无缓存）。

#### 1.4.6 Agent 选择 (`X-Agent-Name`)

`X-Agent-Name` 决定本次对话由哪一个 Agent 处理。

| 取值 | 行为 |
|------|------|
| 空 / `groot`（`agent.MainAgentName`） | 主 Agent 编排模式：`Task.AgentName` 留空，主 Agent 通过 `call_agent` 工具按需调用子 Agent |
| 已注册的子 Agent 名 | Solo 模式：`Task.AgentName` 写入子 Agent 名，绕过主 Agent 直接由子 Agent 处理；不挂 `call_agent` 工具 |
| 任何未注册的非空值 | 400 `unknown_agent` |
| 任何非空值且 `SubAgentRegistry` 未注入 | 400 `unknown_agent`，并打 error 日志 |

`X-Agent-Name` 同样作用于 `/skills`、`/tools`，含义保持一致。

#### 1.4.7 SSE 响应协议

POST `/chat` 的响应是 SSE 流。所有事件格式为 `data: <JSON>\n\n`，流结束发送 `data: [DONE]\n\n`。SSE 写入实现见 [`internal/agent/sse.go`](../../../internal/agent/sse.go)。

**响应头**

| Header | 说明 |
|--------|------|
| `X-Session-ID` | 会话 ID |
| `X-Chat-ID` | 本次对话 ID |
| `Content-Type` | `text/event-stream` |
| `Cache-Control` | `no-cache` |
| `Connection` | `keep-alive` |

**事件载荷字段**

不同事件复用同一条 `data: {json}` 行；下表列出 `role` 与必现字段：

| `role` | 关键字段 | 含义 | 来源 |
|--------|----------|------|------|
| `assistant` | `reasoning_content` | AI 思考过程（流式） | `WriteThinking` |
| `assistant` | `content` | AI 回复正文（流式） | `WriteMessage` |
| `assistant` | `tool_calls`（数组） | AI 决定调用工具 | `WriteToolCalls` |
| `assistant` | `finish_reason` | 当前响应阶段结束原因 | `WriteFinish` |
| `tool` | `tool_call_id` / `tool_name` / `content` / `error` | 工具执行结果 | `WriteToolResult` |
| —（无 role） | `event:"error"`，`message` | 流内错误（如 LLM 服务连接失败） | `WriteError` |

子 Agent 触发的事件会附加 `agent_name` 字段；主 Agent 不附加。

**`finish_reason` 取值**

| 值 | 含义 |
|----|------|
| `tool_calls` | AI 需要调用工具，后续会出现 `tool_result`，然后继续响应 |
| `stop` | 对话正常结束，后续会发送 `[DONE]` |
| `length` | 达到最大 token 限制 |
| `content_filter` | 内容被安全过滤 |
| `null` 或缺省 | 流式中间状态 |

**`tool_calls` 结构**（OpenAI 风格）：

```json
{
  "role": "assistant",
  "tool_calls": [
    {
      "id": "call_xxx",
      "type": "function",
      "function": {
        "name": "工具名",
        "arguments": "{\"key\": \"value\"}"
      }
    }
  ]
}
```

`index`、`extra` 字段为 `omitempty`，多工具调用或模型携带额外元数据时可能出现。

**`tool_result` 结构**：

```json
{
  "role": "tool",
  "tool_call_id": "call_xxx",
  "tool_name": "工具名",
  "content": "执行结果",
  "error": false
}
```

`error=true` 表示工具执行失败，错误信息直接放在 `content` 中，便于客户端区分渲染。

**事件流示例**

纯 LLM 回答（无工具调用）：

```
data: {"role":"assistant","content":"你好！"}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

带 thinking + 工具调用：

```
data: {"role":"assistant","reasoning_content":"用户要求读取文件..."}
data: {"role":"assistant","tool_calls":[{"id":"call_001","type":"function","function":{"name":"groot_file_read","arguments":"{\"name\":\"report.pdf\"}"}}]}
data: {"role":"assistant","finish_reason":"tool_calls"}
data: {"role":"tool","tool_call_id":"call_001","tool_name":"groot_file_read","content":"...文件文本...","error":false}
data: {"role":"assistant","content":"文件内容已读取"}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

### 1.5 对话管理

#### 1.5.1 GET `/chat/status/:sid` — 查询对话状态

实现：[`internal/api/handler/status.go`](../../../internal/api/handler/status.go)。查询指定会话中最新一次对话的实时执行状态。

**响应（有活跃对话）**

```json
{
  "status": "success",
  "session_id": "...",
  "chat": {
    "chat_id": "...",
    "status": "running",
    "started_at": "2026-04-18T10:30:00Z",
    "elapsed_time": "15s",
    "round": 4,
    "progress": {
      "current_step": 0,
      "steps_completed": 0,
      "percentage": 0,
      "sub_agents": []
    }
  }
}
```

`progress` 由 `RuntimeState.SnapshotProgress` 深拷贝得到，`sub_agents` 列表反映当前活跃的子 Agent。

**响应（无活跃对话，会话存在）**

```json
{
  "status": "idle",
  "session_id": "...",
  "round_count": 4,
  "last_message": {
    "round": 4,
    "chat_id": "...",
    "status": "completed",
    "duration": 45
  },
  "chat": null
}
```

**响应（会话不存在）**

```json
{
  "status": "idle",
  "session_id": "...",
  "round_count": 0,
  "last_message": null,
  "chat": null
}
```

#### 1.5.2 GET `/chat/:sid` — 查询最新对话详情

实现：[`internal/api/handler/detail.go`](../../../internal/api/handler/detail.go) `GetLatest`。返回指定会话中最新一次对话的完整 `ChatRecord`。

**响应**

```json
{
  "status": "success",
  "session_id": "...",
  "chat": { "...ChatRecord..." }
}
```

会话不存在时返回 404 `session_not_found`；会话存在但无对话记录时 `chat` 为 `null`。

#### 1.5.3 GET `/chat/:sid/:cid` — 查询指定对话详情

实现：[`internal/api/handler/detail.go`](../../../internal/api/handler/detail.go) `Serve`。

会话不存在 → 404 `session_not_found`；会话存在但 `chat_id` 不匹配 → 404 `chat_not_found`。

### 1.6 会话查询

#### 1.6.1 GET `/sess/:sid` — 查询会话详情

实现：[`internal/api/handler/session.go`](../../../internal/api/handler/session.go) `GetSession`。返回会话的全量对话历史（不受 `history_window` 限制）。

```json
{
  "status": "success",
  "session_id": "...",
  "session": {
    "session_id": "...",
    "created_at": "2026-04-18T10:00:00Z",
    "round_count": 4,
    "path": "",
    "last_active_at": "2026-04-18T10:30:00Z"
  },
  "history": {
    "session_id": "...",
    "created_at": "...",
    "messages": [ { "...Message..." } ]
  }
}
```

`session.path` 在 DB 模式下固定为空字符串。`last_active_at` 取自 `History.Messages` 末条 `timestamp`。

#### 1.6.2 GET `/sess/history` — 查询会话列表

实现：[`internal/api/handler/session.go`](../../../internal/api/handler/session.go) `ListSessions`。分页查询所有会话。

| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| `limit` | int | 否 | 返回条数，默认 20，仅当 1 ≤ 值 ≤ 100 时被采纳 |
| `offset` | int | 否 | 分页偏移，默认 0，仅当值 ≥ 0 时被采纳 |

**响应**

```json
{
  "status": "success",
  "total": 50,
  "limit": 10,
  "offset": 0,
  "sessions": [
    {
      "session_id": "...",
      "created_at": "...",
      "round_count": 4,
      "last_active_at": "...",
      "path": ""
    }
  ]
}
```

### 1.7 系统信息

#### 1.7.1 GET `/health` — 健康检查

实现：[`internal/api/handler/health.go`](../../../internal/api/handler/health.go)。该端点不挂认证 / 限流中间件。

**检查项**

| 检查 | 数据来源 |
|------|---------|
| `llm` | `llm.CheckConnection`（调用 LLM API `/models`） |
| `mcp_servers` | `mcp.Manager.ListWithToolCount`（含每个 MCP 的 `name`/`type`/`description`/`isActive`/`tools_count`/`error`） |
| `skills` | `skillBackend.List` 长度 |
| `memory` | `memory.Manager.ListSessions` 总数 |

**响应**

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "healthy", "info": {"model": "gpt-4o"}},
    "mcp_servers": {"status": "healthy", "info": [
      {"name":"file_operations","type":"stdio","description":"文件操作服务","isActive":true,"tools_count":7}
    ]},
    "skills": {"status": "healthy", "info": {"count": 4}},
    "memory": {"status": "healthy", "info": {"sessions": 10}}
  },
  "metrics": {
    "chats_running": 2
  }
}
```

LLM 检查失败时 `llm.status` 为 `unhealthy`、`info.error` 携带原因；其它组件即便采集异常也会被打日志而不会翻转整体 `status`。

#### 1.7.2 GET `/agents` — Agent 列表

实现：[`internal/api/handler/agents.go`](../../../internal/api/handler/agents.go)。枚举所有可调用的 Agent：主 Agent `groot` 始终位于 `agents[0]`，子 Agent 按 `SubAgentRegistry.Names()` 字典序排列。每个 Agent 携带 skills 摘要（仅 `name`/`description`）。

**响应**

```json
{
  "agents": [
    {
      "name": "groot",
      "description": "默认 Agent（全局配置）",
      "skills": [
        {"name": "pdf_analyzer", "description": "分析 PDF 文档并生成摘要"}
      ]
    },
    {
      "name": "weather-agent",
      "description": "...",
      "skills": []
    }
  ]
}
```

子 Agent 的 `skill.Backend.List` 失败时降级为空数组（仍 200），并打日志。

#### 1.7.3 GET `/skills` — Skill 列表

实现：[`internal/api/handler/skills.go`](../../../internal/api/handler/skills.go)。返回当前选定 Agent 的 Skill 列表。

`X-Agent-Name` 路由约定：

- 不传 / `groot` → 主 Agent backend
- 已注册子 Agent → 该子 Agent 的 `SkillBK`
- 未注册 → 400 `unknown_agent`
- registry 未初始化时同 400，并打日志

**响应**

```json
{
  "skills": [
    {"name": "pdf_analyzer", "description": "分析 PDF 文档并生成摘要"},
    {"name": "code_generator", "description": "根据需求生成代码"}
  ],
  "total": 2
}
```

backend 为 nil 或 `List` 失败时返回 `{"skills":[], "total":0}`。

#### 1.7.4 GET `/tools` — MCP 工具列表

实现：[`internal/api/handler/tools.go`](../../../internal/api/handler/tools.go)。按 MCP Server 名称分组返回所有可用工具。每个 MCP 作为顶层 key，对应 `ToolsGroup`：`tools` 数组 + `total` 计数。

`X-Agent-Name` 路由约定与 `/skills` 一致。

主 Agent 路径下，`call_agent` 内置工具会被合成为一个名为 `_builtin` 的 group 暴露出来（与 `Executor.Execute` 注入的 `ExtraTools` 行为一致）；Solo 模式不挂载。

**响应示例**

```json
{
  "filesystem": {
    "tools": [
      {"name": "read_file", "description": "读取文件内容"},
      {"name": "write_file", "description": "写入文件内容"}
    ],
    "total": 2
  },
  "_builtin": {
    "tools": [
      {"name": "call_agent", "description": "..."}
    ],
    "total": 1
  }
}
```

工具数为 0 的 MCP 也会出现在结果中（`tools` 为空数组、`total: 0`）；MCP 在发现阶段报错时会被记录到日志（不阻断响应）。

#### 1.7.5 GET `/models` — 可用模型列表

实现：[`internal/api/handler/models.go`](../../../internal/api/handler/models.go)。返回所有已配置 LLM 模型及默认模型。

```json
{
  "models": [
    {"name": "gpt-4o", "model": "gpt-4o", "base_url": "https://api.openai.com/v1"},
    {"name": "qwen-local", "model": "Qwen3.5-122B-A10B-6bit", "base_url": "http://127.0.0.1:8230/v1"}
  ],
  "default": "gpt-4o",
  "total": 2
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `models[].name` | string | 模型配置 key，用于 `X-Model-Name` |
| `models[].model` | string | 实际调用 LLM API 的 `model` 参数值 |
| `models[].base_url` | string | LLM API 端点地址 |
| `default` | string | `llm.default_model` |
| `total` | int | 已配置模型总数 |

### 1.8 调度管理

定时任务的创建通过 Agent 对话中的内置工具完成，API 仅提供查看和管理能力。**调度端点仅在 Leader 实例可用**，Follower / 未启动调度的实例返回 503。实现：[`internal/api/handler/schedule.go`](../../../internal/api/handler/schedule.go)。

#### 1.8.1 端点列表

| 端点 | 方法 | 说明 |
|------|------|------|
| `/schedule/` | GET | 列出任务（`?status=active|disabled|archive|all`，默认 `all`） |
| `/schedule/:id` | GET | 查看任务详情 |
| `/schedule/:id` | DELETE | 删除任务（物理删除） |
| `/schedule/:id/disable` | POST | 禁用任务（active → disabled） |
| `/schedule/:id/enable` | POST | 启用任务（disabled → active） |
| `/schedule/:id/archive` | POST | 归档任务 |
| `/schedule/:id/history` | GET | 查看执行历史 |

#### 1.8.2 处理流程

```
请求到达 → ScheduleHandler
  ├─ *h.mgr == nil → 503 schedule_unavailable
  └─ 调用 schedule.Manager 对应方法
        ├─ List(status) / Get(id) / GetHistory(id)
        ├─ Delete(id) → Unregister + 物理删除
        ├─ Disable(id) → Unregister + 目录 active → disabled
        ├─ Enable(id) → 目录 disabled → active + Register
        └─ Archive(id) → 目录 → archive
```

#### 1.8.3 响应格式

- `GET /schedule/`：直接返回 `[]*Task` 数组（即便为空也返回 `[]`）。
- `GET /schedule/:id`：直接返回 `*Task` 对象。
- `GET /schedule/:id/history`：直接返回 `[]ExecutionRecord` 数组。
- 操作类响应：

```json
{"status": "deleted", "id": "task-check-health"}
{"status": "disabled", "id": "task-check-health"}
{"status": "enabled", "id": "task-check-health"}
{"status": "archived", "id": "task-check-health"}
```

- 错误响应：`mgr` 为 nil → 503 `schedule_unavailable`；`Get` 失败 → 404 `task_not_found`；其余失败 → 500 `schedule_error`。

#### 1.8.4 RESTful 设计说明

调度端点遵循两段式路径 `/schedule/` + `/schedule/:id`，禁用 / 启用 / 归档等动作通过动词后缀表达（`/disable`、`/enable`、`/archive`），采用 POST 而非 PUT/PATCH，与 HTTP DELETE 及标准查询明确区分。

---

## 二、横切关注点

### 2.1 认证与鉴权

实现：[`internal/api/middleware/auth.go`](../../../internal/api/middleware/auth.go)。

#### 2.1.1 配置

```yaml
security:
  auth:
    enabled: true
    type: api_key
    api_key:
      header_name: X-API-Key
      keys:
        - name: default
          key: ${GROOT_API_KEY}
          permissions: all
```

`header_name` 为空时回退到 `X-API-Key`。

#### 2.1.2 认证流程

```
请求到达 → AuthMiddleware
  ├─ enabled=false → caller="anonymous"，放行
  └─ enabled=true
        ├─ 读取 header_name（默认 X-API-Key）
        ├─ 空 → 401 unauthorized
        ├─ 遍历 keys：key 命中 → 检查 permissions vs 路径所需权限
        │     ├─ 不覆盖 → 403 forbidden
        │     └─ 覆盖 → caller=key.name，放行
        └─ 全部不命中 → 401 unauthorized
```

权限判定见 `getRequiredPermission`。`permissions` 中包含 `all` 即视为通过；列表为空也视为放行（兼容老配置）。

#### 2.1.3 权限到端点的映射

| 权限 | 端点（来自 `getRequiredPermission`） |
|------|--------------------------------------|
| `chat` | POST `/chat` |
| `status` | `/chat/status/...` |
| `detail` | `GET /chat/:sid`、`GET /chat/:sid/:cid` |
| `history` | `/sess/history` |
| `session` | `/sess/...`（除 `/sess/history` 外） |
| `skills` | `/skills` |
| `tools` | `/tools` |
| `schedule` | `/schedule...` |
| `all` | 上述全部 |
| 无需权限 | `/health`（不进入认证中间件） |

> 路径匹配是字符串前缀 / 字面量匹配，未列入分支的端点（含 `/agents`、`/models`）会落到默认分支，要求 `all` 权限。需要单独授权时配置 `permissions: all` 或扩展 `getRequiredPermission`。

#### 2.1.4 多 Key 配置示例

```yaml
keys:
  - name: internal_system
    key: ${GROOT_INTERNAL_KEY}
    permissions: all
  - name: external_partner
    key: partner-key-2026
    permissions: [chat, status]
  - name: monitor_service
    key: ${GROOT_MONITOR_KEY}
    permissions: [status, skills, tools]
```

### 2.2 限流

实现：[`internal/api/middleware/ratelimit.go`](../../../internal/api/middleware/ratelimit.go) + [`internal/ratelimit/`](../../../internal/ratelimit/)。

#### 2.2.1 配置

```yaml
security:
  rate_limit:
    enabled: true
    global_qps: 50
    global_concurrency: 10
    default_qps: 10
    default_concurrency: 5
    cleanup_interval: 5m
```

`RateLimiter` 初始化失败时会自动禁用限流（中间件直接放行）。

#### 2.2.2 限流机制

| 端点 | 限流方式 |
|------|---------|
| POST `/chat` | `Acquire(key)` 同时校验 QPS 与并发槽位；中间件 `rc.Next` 之后 `Release(key)` |
| 其它端点 | `Allow(key)` 仅做 QPS 判定 |

#### 2.2.3 调用方标识 (`resolveKey`)

- 已认证调用方：`key:<caller_name>`
- 匿名 / 未命中：`ip:<client_ip>`（裁掉端口与 IPv6 方括号）
- IP 也获取不到时退化为 `anonymous`

#### 2.2.4 响应

触发限流返回 429：

```json
{
  "status": "rate_limited",
  "message": "请求过于频繁，请稍后重试"
}
```

### 2.3 附件校验

实现：[`internal/attachment/handler.go`](../../../internal/attachment/handler.go)，由 `ChatHandler` 在解析请求体后调用。

#### 2.3.1 配置

```yaml
attachment:
  max_size: 50          # 单个附件最大 (MB)
  max_total_size: 100   # 总大小限制 (MB)
  max_count: 10         # 数量限制
  allowed_types: [pdf, doc, docx, txt, json, csv, xml, yaml, png, jpg, jpeg, zip]
```

#### 2.3.2 校验顺序

```
收到附件 → 数量校验
  ├─ 1. len(attachments) > max_count → 400 attachment_count_exceeded
  └─ 逐个校验
        ├─ 2. name 为空 → 400 attachment_missing_name
        ├─ 3. type 不在 [file,image,audio,video] → 400 attachment_invalid_type
        ├─ 4. content 为空 → 400 attachment_missing_content
        ├─ 5. file/image：扩展名不在 allowed_types → 400 attachment_type_not_allowed
        ├─ 6. file：估算解码大小 > max_size → 400 attachment_size_exceeded
        └─ 7. 累计 file 大小 > max_total_size → 400 attachment_total_size_exceeded

后续 chat 处理阶段（解码失败）：→ 400 attachment_decode_error
```

`allowed_types` 仅作用于 `file`、`image` 两种类型；`audio` / `video` 不做扩展名校验。`max_size` / `max_total_size` 仅按 `file` 类型估算（image/audio/video 不计入 totalSize）。

#### 2.3.3 错误码（均返回 400）

| 错误码 | 说明 |
|--------|------|
| `attachment_count_exceeded` | 数量超限 |
| `attachment_type_not_allowed` | 扩展名不在 allowed_types |
| `attachment_size_exceeded` | 单个 file 附件超 max_size |
| `attachment_total_size_exceeded` | file 附件总大小超 max_total_size |
| `attachment_missing_content` | 缺少 `content` 字段 |
| `attachment_missing_name` | 缺少文件名 |
| `attachment_invalid_type` | type 不在允许集合 |
| `attachment_validation_error` | 通用附件校验失败（兜底） |
| `attachment_decode_error` | Base64 解码失败 |

### 2.4 错误码速查

| HTTP | 错误码 | 说明 |
|------|--------|------|
| 400 | `invalid_request` | 请求参数无效 |
| 400 | `invalid_model` | 模型名不存在 |
| 400 | `unknown_agent` | `X-Agent-Name` 指向未注册子 Agent |
| 400 | `attachment_*` | 附件校验失败（详见 2.3） |
| 401 | `unauthorized` | API Key 无效或缺失 |
| 403 | `forbidden` | 权限不足 |
| 404 | `session_not_found` | 会话不存在 |
| 404 | `chat_not_found` | 对话记录不存在 |
| 404 | `task_not_found` | 调度任务不存在 |
| 409 | `chat_limit_exceeded` | 会话已有活跃对话 |
| 429 | `rate_limited` | 触发 API 限流 |
| 500 | `error` | 通用服务端错误（如读取历史失败） |
| 500 | `schedule_error` | 调度操作失败 |
| 503 | `schedule_unavailable` | 调度服务不可用（非 Leader 或未启动） |

---

## 三、迭代说明

### 3.1 与上一版差异（基于代码核对）

- **新增端点 `/agents`**：列出主 Agent + 子 Agent，附 skills 摘要。原文档未描述。
- **新增请求头 `X-User-ID`**：`/chat` 在创建新会话时把它写入 `memory_sessions.user_id`（来自提交 `c1f854d`），原文档未描述。
- **新增请求头 `X-Agent-Name`**：作用于 `/chat`、`/skills`、`/tools`，决定主 Agent 编排或 Solo 模式；未注册时返回 400 `unknown_agent`。
- **`/schedule` 路径调整**：列表端点是 `/schedule/`（带尾斜杠），不再是 `/schedule`；列表支持 `?status=active|disabled|archive|all` 过滤（默认 `all`）。
- **SSE 协议补充**：
  - `tool_result` 事件新增 `error: bool` 字段（用于客户端区分工具执行失败）。
  - 子 Agent 触发的事件会附加 `agent_name` 字段。
  - 流内错误以 `event:"error"` + `message` 形式作为 `WriteError` 事件下发，原文档未描述。
- **`ChatRecord` 字段补全**：新增 `prompt`、`duration_ms`、`agent_name`、`model`、`prompt_tokens`、`completion_tokens`、`total_tokens` 字段。
- **`Message` 新增 `agent_name`**。
- **`SessionInfo`**：移除 `path` 的实际语义，DB 模式下固定为空字符串；`last_active_at` 改为 `memory_sessions.updated_at` 派生（仅在 `round > 0` 时填）。
- **`/chat/status/:sid`**：`progress` 来自 `RuntimeState.SnapshotProgress` 深拷贝，含 `sub_agents` 字段；不再保证带 `current_step / steps_completed / percentage` 的具体值（只是结构上存在）。
- **`/tools` 主 Agent 路径**：合成 `_builtin` 分组，把 `call_agent` 内置工具暴露给客户端；Solo 模式不挂载。
- **认证权限映射收紧**：原文档列出的「`status` / `detail` / `session` / `history` / `skills` / `tools` / `schedule`」逐项与代码中的 `getRequiredPermission` 对齐；`/agents`、`/models` 等未在 switch 中分支的端点会落到默认 `all` 权限。
- **限流 key 解析**：明确 `caller == "anonymous"` 也走 IP 分支；端口与 IPv6 方括号会被剥离；IP 缺失时退化为 `"anonymous"` 字面量。
- **错误码增删**：
  - 新增 `unknown_agent`。
  - 移除文档中存在但代码未真正触发的 `config_error`、`llm_connection_error`、`tool_call_error`（运行时错误以 SSE `WriteError` 文本形式下发，不返回 HTTP 错误码）。
  - 通用 500 错误码代码中是字面量 `"error"`，文档原本未列出。
- **请求体大小**：从「200MB 用于附件」改为带代码引用的精确说明。
- **结构调整**：按 CLAUDE.md「功能设计 vs 迭代说明」规范，正文统一改为正面陈述；所有「新增 / 保留 / 相比之前」类描述集中在本节。
