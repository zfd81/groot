# Groot HTTP API 设计

## 一、概述

Groot 提供 RESTful HTTP API，客户端通过 API 与 AI Agent 交互。核心交互模式为 **POST + SSE 流式响应**——客户端提交指令，服务端通过 Server-Sent Events 实时推送 AI 的思考过程、工具调用和最终结果。

**技术栈：**

| 项目 | 选型 |
|------|------|
| 传输协议 | HTTP/1.1 |
| Web 框架 | Hertz (github.com/cloudwego/hertz) |
| 数据格式 | JSON |
| 流式协议 | Server-Sent Events (SSE) |
| 认证方式 | API Key（`X-API-Key` header，可配置开关） |

**设计原则：**
- **会话模型**：Session（会话）包含多轮 Chat（对话），每轮对话有独立的 `chat_id`
- **横切关注点分离**：认证、附件校验、错误码作为独立章节，各端点引用
- **无状态请求**：每个请求独立创建所需实例（如 ChatModel），不跨请求共享

---

## 二、通用约定

### 2.1 端点总览

| 端点 | 方法 | 用途 | 响应类型 |
|------|------|------|---------|
| `/chat` | POST | 执行对话 | SSE 流 |

| `/chat/status/{sid}` | GET | 查询最新对话状态 | JSON |
| `/chat/{sid}` | GET | 查询最新对话详情（含步骤） | JSON |
| `/chat/{sid}/{cid}` | GET | 查询指定对话详情 | JSON |
| `/sess/{sid}` | GET | 查询会话详情（全量历史） | JSON |
| `/sess/history` | GET | 查询会话列表 | JSON (分页) |
| `/health` | GET | 健康检查 | JSON |
| `/skills` | GET | 列出可用 Skill | JSON |
| `/tools` | GET | 列出可用 MCP 工具 | JSON |
| `/schedule` | GET | 列出定时任务 | JSON |
| `/schedule/{id}` | GET | 查看任务详情 | JSON |
| `/schedule/{id}/history` | GET | 查看执行历史 | JSON |
| `/schedule/{id}` | DELETE | 删除任务 | JSON |
| `/schedule/{id}/disable` | POST | 禁用任务 | JSON |
| `/schedule/{id}/enable` | POST | 启用任务 | JSON |
| `/schedule/{id}/archive` | POST | 归档任务 | JSON |

### 2.2 ID 格式约定

| ID 类型 | 格式 | 示例 |
|---------|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260418103000523_a1b2` |
| `chat_id` | `chat_{YYYYMMDDHHMMSSmmm}` | `chat_20260418103000523` |
| `step_id` | `{YYYYMMDD}-{HHMMSSmmm}-{random6}` | `20260418-103000000-a1b2c3` |

所有端点中的 `{sid}` 为 `session_id`，`{cid}` 为 `chat_id`，`{id}` 为调度任务 ID。

### 2.3 请求体大小限制

最大请求体大小为 **200MB**（为支持大型 Base64 编码附件）。

### 2.4 通用请求头

| Header | 说明 |
|--------|------|
| `Content-Type` | `application/json`（所有 POST 请求必须） |
| `X-Session-ID` | 会话 ID，用于续接已有会话（详见 3.4） |
| `X-Model-Name` | 指定 LLM 模型名（详见 3.5） |
| `X-API-Key` | API Key（启用认证时必需，详见第八章） |

### 2.5 通用响应格式

**对话/会话类端点**的成功响应均包含 `"status": "success"` 作为顶层字段：

```json
{
  "status": "success",
  "<data_field>": { ... }
}
```

以下端点使用各自独立的响应格式（不含 `status` 包装）：
- `/skills`、`/tools` — 直接返回数据对象
- `/schedule` — 列表返回数组，详情返回对象
- `/schedule/{id}/disable` 等操作 — 返回 `{"status": "动作名", "id": "..."}`

**错误响应**包含 `status`（错误码）和 `message`（可读描述）：

```json
{
  "status": "invalid_model",
  "message": "模型 'xxx' 不存在"
}
```

错误码速查见第十一章。

### 2.6 数据模型

**ChatRecord** — 单次对话详细记录（`chats/{chat_id}.json`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `chat_id` | string | 对话 ID |
| `session_id` | string | 所属会话 ID |
| `round` | int | 轮次编号 |
| `timestamp` | time | 记录时间戳 |
| `instruction` | string | 用户指令 |
| `attachments` | []string | 附件文件名列表 |
| `result` | string | 执行结果摘要 |
| `result_attachments` | []string | 结果附件列表 |
| `status` | string | 执行状态：`completed` / `failed` / `cancelled` |
| `started_at` | time | 开始时间 |
| `ended_at` | time | 结束时间 |
| `duration` | int | 执行耗时（秒） |
| `caller` | string | 调用方标识 |
| `error` | *Error | 错误信息（无错误时为 null） |
| `steps` | []Step | 执行步骤列表 |

**Message** — 单轮对话摘要（`history.json` 中）

| 字段 | 类型 | 说明 |
|------|------|------|
| `round` | int | 轮次编号 |
| `chat_id` | string | 对话 ID |
| `timestamp` | time | 时间戳 |
| `instruction` | string | 用户指令 |
| `attachments` | []string | 附件文件名列表 |
| `result` | string | 执行结果摘要 |
| `result_attachments` | []string | 结果附件列表 |
| `status` | string | 执行状态：`completed` / `failed` / `cancelled` |
| `duration` | int | 执行耗时（秒） |
| `steps_count` | int | 步骤数量 |
| `error` | *Error | 错误信息（无错误时为 null） |

**Step** — 单步执行记录

| 字段 | 类型 | 说明 |
|------|------|------|
| `step_id` | string | 步骤 ID（格式见 2.2） |
| `type` | string | 步骤类型：`skill` / `tool` / `llm` / `thinking` |
| `name` | string | Skill 名称或工具名称 |
| `start_time` | time | 开始时间 |
| `end_time` | time | 结束时间 |
| `status` | string | 执行状态：`success` / `failed` |
| `nesting_level` | int | 嵌套深度 |
| `error` | *Error | 错误信息（无错误时为 null） |

**Error** — 错误信息

| 字段 | 类型 | 说明 |
|------|------|------|
| `code` | string | 错误码 |
| `message` | string | 错误描述 |

**SessionInfo** — 会话信息

| 字段 | 类型 | 说明 |
|------|------|------|
| `session_id` | string | 会话 ID（格式见 2.2） |
| `created_at` | time | 创建时间 |
| `round_count` | int | 对话轮次总数 |
| `last_active_at` | string | 最后活跃时间 |
| `path` | string | 会话存储路径 |

---

## 三、对话执行 — POST /chat

Groot 核心端点。客户端提交指令，服务端通过 SSE 流式返回 AI 的思考过程、工具调用和最终回答。

### 3.1 请求

**请求头**

| Header | 必需 | 说明 |
|--------|------|------|
| `Content-Type` | 是 | `application/json` |
| `X-Session-ID` | 否 | 会话 ID。为空则创建新会话；不存在则生成新 ID |
| `X-Model-Name` | 否 | 指定模型名。为空则用 `default_model`；无效返回 400 |
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
| `type` | 是 | `file`、`image`、`audio`、`video` |
| `name` | 是 | 文件名含扩展名 |
| `content` | 是 | Base64 编码内容 |

### 3.2 处理流程

```
POST /chat 请求到达
  │
  ├─ 1. 请求校验
  │     ├─ instruction 不能为空
  │     └─ 附件校验（数量、类型、大小，见第十章）
  │
  ├─ 2. 会话处理（见 3.4）
  │     ├─ 提取 X-Session-ID
  │     ├─ 新建会话：生成 session_id → 创建会话目录和 SESSION.md
  │     └─ 继续会话：检查并发（有活跃对话 → 409）→ 读取历史消息
  │
  ├─ 3. 模型选择（见 3.5）
  │
  ├─ 4. 创建对话记录
  │     ├─ 生成 chat_id
  │     ├─ 注册活跃状态（RuntimeState.Register）
  │     └─ 注册到取消管理器
  │
  ├─ 5. 返回响应头
  │     ├─ X-Session-ID、X-Chat-ID
  │     └─ Content-Type: text/event-stream
  │
  ├─ 6. 附件处理（如有）
  │     ├─ file 类型：Base64 解码 → 拼入 instruction
  │     ├─ image/audio/video 类型：构建 data URL → 多模态消息
  │     └─ 全部落盘到 memory/{sid}/attachments/
  │
  ├─ 7. Agent 执行 → SSE 流式输出（见 3.6）
  │
  └─ 8. 完成处理
        ├─ 保存 ChatRecord → chats/{chat_id}.json
        ├─ 追加 Message → history.json
        └─ 清理活跃状态
```

### 3.3 注意事项

- **并发限制**：同一会话同时只能有一个活跃对话，冲突返回 **409** `chat_limit_exceeded`
- **取消机制**：客户端断开 SSE 连接后，HTTP 请求上下文自动取消，Agent 在下一次循环检查点终止执行
- **轮次管理**：继续会话时 `round` 自增，首次对话 `round=1`

### 3.4 会话处理

会话（Session）是对话的容器，一次会话可包含多轮对话（Chat）。

| 请求 sid | 会话存在? | 行为 | 轮次 |
|----------|----------|------|------|
| 空 | - | 生成新 sid，创建会话 | 1 |
| 有值 | 否 | 生成新 sid，创建会话 | 1 |
| 有值 | 是 | 使用已有 sid，追加轮次 | +1 |

### 3.5 模型选择 (X-Model-Name)

通过 `X-Model-Name` 请求头，每次请求可动态指定 LLM 模型，实现同一会话不同对话使用不同模型。

**使用场景：**
- 先调用视觉模型解析图片，再调用其他模型做后续处理
- 复杂任务用强模型，简单任务用轻量模型

**配置文件格式：**

```yaml
llm:
  default_model: gpt-4o           # 默认模型名称（对应 models 中的某个 key）
  models:
    gpt-4o:                       # 模型配置名称（自定义）
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4o               # 实际调用时的模型名称
      max_completion_tokens: 4096
      temperature: 0.7
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_completion_tokens: 4096
      temperature: 0.7
```

| 字段 | 说明 |
|------|------|
| `default_model` | 默认模型名称，对应 `models` 中的某个 key |
| `models.<key>` | 模型配置名（自定义），用于 `X-Model-Name` 匹配 |
| `models.<key>.base_url` | API Base URL |
| `models.<key>.api_key` | API Key（支持 `${ENV}` 环境变量） |
| `models.<key>.model` | 实际调用 API 时的 model 参数值 |
| `models.<key>.max_completion_tokens` | 最大输出 token 数 |
| `models.<key>.temperature` | 输出随机性（0.0~2.0），默认 0.7 |
| `models.<key>.top_p` | 核采样系数（0.0~1.0），默认 1.0 |
| `models.<key>.frequency_penalty` | 频率惩罚（-2.0~2.0），默认 0.0 |
| `models.<key>.presence_penalty` | 存在惩罚（-2.0~2.0），默认 0.0 |
| `models.<key>.seed` | 随机种子，0 表示不设置 |
| `models.<key>.stop` | 停止序列列表，默认空 |
| `models.<key>.thinking` | 深度思考模式（Qwen/DeepSeek 等），默认 false |

**配置验证（启动时）：**

```go
func ValidateLLMConfig(cfg *LLMConfig) error {
    if len(cfg.Models) == 0 {
        return fmt.Errorf("models 配置不能为空")
    }
    if cfg.DefaultModel == "" {
        for name := range cfg.Models {
            cfg.DefaultModel = name
            break
        }
    }
    if !cfg.ValidateModel(cfg.DefaultModel) {
        return fmt.Errorf("default_model '%s' 不存在于 models 配置中", cfg.DefaultModel)
    }
    return nil
}
```

**LLMConfig 方法（API 层依赖）：**

```go
// 按名称获取模型配置（name 为空时返回默认模型）
func (c *LLMConfig) GetModelByName(name string) *ModelConfig {
    if name == "" {
        return c.GetDefaultModel()
    }
    if model, ok := c.Models[name]; ok {
        model.APIKey = ExpandEnv(model.APIKey)
        return &model
    }
    return nil
}

// 验证模型名称是否存在（空值合法，将使用默认模型）
func (c *LLMConfig) ValidateModel(name string) bool {
    if name == "" {
        return true
    }
    _, exists := c.Models[name]
    return exists
}

// 获取默认模型配置
func (c *LLMConfig) GetDefaultModel() *ModelConfig {
    if model, ok := c.Models[c.DefaultModel]; ok {
        model.APIKey = ExpandEnv(model.APIKey)
        return &model
    }
    return nil
}
```

**ChatHandler 模型提取与验证：**

```go
func (h *ChatHandler) Handle(ctx context.Context, rc *app.RequestContext) {
    modelName := string(rc.GetHeader("X-Model-Name"))

    if modelName != "" && !h.config.LLM.ValidateModel(modelName) {
        rc.JSON(400, utils.H{
            "status":  "invalid_model",
            "message": fmt.Sprintf("模型 '%s' 不存在", modelName),
        })
        return
    }

    task := &agent.Task{
        // ... 其他字段 ...
        ModelName: modelName,
    }
}
```

**数据流：**

```
X-Model-Name header
  → ChatHandler: 提取验证，放入 Task.ModelName
  → Executor.Execute(): 透传至 Engine.Run()
  → Engine: llm.NewChatModel(ctx, llmConfig, modelName)
  → LLMConfig.GetModelByName(name): 按名查找返回 ModelConfig
  → 创建 ChatModel 实例，每个请求独立创建（无缓存）
```

**处理规则：**

| 场景 | 处理 | 结果 |
|------|------|------|
| `X-Model-Name` 指定有效模型 | 使用指定模型 | 正常执行 |
| `X-Model-Name` 为空或不存在 | 使用 `default_model` | 正常执行 |
| `X-Model-Name` 指定不存在的模型 | 严格校验 | 400 `invalid_model` |
| 配置中 `models` 为空 | 启动时报错退出 | 服务启动失败 |
| 配置中 `default_model` 不存在 | 启动时报错退出 | 服务启动失败 |

**设计决策：**
- 按请求创建模型实例（无缓存），不同对话可同时用不同模型
- 模型名严格匹配，大小写敏感
- 严格校验：模型名不存在返回 400，不 fallback
- `X-Model-Name` 为空时使用默认模型（空值合法）

### 3.6 SSE 响应协议

POST /chat 的响应是 SSE 流。所有事件格式为 `data: <JSON>\n\n`，流结束发送 `data: [DONE]\n\n`。

**响应头**

| Header | 说明 |
|--------|------|
| `X-Session-ID` | 会话 ID |
| `X-Chat-ID` | 本次对话 ID |
| `Content-Type` | `text/event-stream` |
| `Cache-Control` | `no-cache` |
| `Connection` | `keep-alive` |

**事件类型**

| 事件 | `role` | 说明 | 出现条件 |
|------|--------|------|---------|
| `thinking` | `assistant` | AI 思考过程，`reasoning_content` 字段流式输出 | 模型输出 thinking |
| `message` | `assistant` | AI 回复内容，`content` 字段流式输出 | 必有，至少一次 |
| `tool_calls` | `assistant` | AI 决定调用工具，含 `tool_calls` 数组 | 调用工具时 |
| `finish` | `assistant` | 当前响应阶段结束，含 `finish_reason` | 必有 |
| `tool_result` | `tool` | 工具执行结果 | 调用工具时 |
| `[DONE]` | - | 整个对话结束 | 必有 |

**finish_reason**

| 值 | 含义 | 后续 |
|----|------|------|
| `tool_calls` | AI 需要调用工具 | 后续 `tool_result`，然后 AI 继续响应 |
| `stop` | 对话正常结束 | 后续 `[DONE]` |
| `length` | 达到最大 token 限制 | 当前回答截断 |
| `content_filter` | 内容被安全过滤 | 当前回答中断 |
| `null` | 未明确结束原因 | 流式传输中的中间状态 |

**tool_calls 结构：**

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
      },
      "extra": {}
    }
  ]
}
```

> `index` 和 `extra` 字段为 omitempty，存在多工具调用或模型附加元数据时可能出现。

**tool_result 结构：**

```json
{
  "role": "tool",
  "tool_call_id": "call_xxx",
  "tool_name": "工具名",
  "content": "执行结果"
}
```

工具调用出错时，错误信息直接包含在 `content` 字段中。


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
data: {"role":"assistant","tool_calls":[{"id":"call_001","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"/etc/hosts\"}"}}]}
data: {"role":"assistant","finish_reason":"tool_calls"}
data: {"role":"tool","tool_call_id":"call_001","tool_name":"file_read","content":"127.0.0.1 localhost"}
data: {"role":"assistant","content":"文件内容为：127.0.0.1 localhost"}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

多工具并行调用：

```
data: {"role":"assistant","tool_calls":[{"id":"call_001",...},{"id":"call_002",...}]}
data: {"role":"assistant","finish_reason":"tool_calls"}
data: {"role":"tool","tool_call_id":"call_001","tool_name":"file_read","content":"结果A"}
data: {"role":"tool","tool_call_id":"call_002","tool_name":"file_read","content":"结果B"}
data: {"role":"assistant","content":"两个文件已读取..."}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

---

## 四、对话管理

### 4.1 GET /chat/status/{sid} — 查询对话状态

查询指定会话中最新一次对话的实时执行状态和进度。

**路径参数**

| 参数 | 说明 |
|------|------|
| `sid` | session_id |

**响应**

```json
{
  "status": "success",
  "session_id": "...",
  "chat": {
    "chat_id": "chat_...",
    "round": 4,
    "status": "running",
    "progress": {
      "current_step": 2,
      "steps_completed": 1,
      "percentage": 50
    },
    "started_at": "2026-04-18T10:30:00Z",
    "elapsed_time": "15s"
  }
}
```

无运行中对话时，根据会话存在与否返回不同信息：

**会话存在 + 无活跃对话：**

```json
{
  "status": "idle",
  "session_id": "...",
  "round_count": 4,
  "last_message": {
    "round": 4,
    "chat_id": "chat_...",
    "status": "completed",
    "duration": 45
  },
  "chat": null
}
```

**会话不存在：**

```json
{
  "status": "idle",
  "session_id": "...",
  "round_count": 0,
  "last_message": null,
  "chat": null
}
```

### 4.2 GET /chat/{sid} — 查询最新对话详情

返回指定会话中最新一次对话的完整记录，包含所有执行步骤。

**路径参数**

| 参数 | 说明 |
|------|------|
| `sid` | session_id |

**响应**

```json
{
  "status": "success",
  "session_id": "...",
  "chat": {
    "chat_id": "chat_...",
    "session_id": "20260418103000523_a1b2",
    "round": 4,
    "timestamp": "2026-04-18T10:30:00Z",
    "instruction": "用户指令",
    "attachments": ["data.csv"],
    "result": "执行结果摘要",
    "result_attachments": [],
    "status": "completed",
    "started_at": "2026-04-18T10:30:00Z",
    "ended_at": "2026-04-18T10:30:45Z",
    "duration": 45,
    "caller": "default",
    "error": null,
    "steps": [
      {
        "step_id": "20260418-103000000-a1b2c3",
        "type": "skill",
        "name": "pdf_analyzer",
        "start_time": "...",
        "end_time": "...",
        "status": "success",
        "nesting_level": 0,
        "error": null
      }
    ]
  }
}
```

无对话记录时 `chat` 为 `null`。

### 4.3 GET /chat/{sid}/{cid} — 查询指定对话详情

与 4.2 格式相同，但定位到特定的 `chat_id`。

**路径参数**

| 参数 | 说明 |
|------|------|
| `sid` | session_id |
| `cid` | chat_id |

---

## 五、会话查询

### 5.1 GET /sess/{sid} — 查询会话详情

返回会话的全量对话历史（不受 `history_window` 限制）。

**路径参数**

| 参数 | 说明 |
|------|------|
| `sid` | session_id |

**响应**

```json
{
  "status": "success",
  "session_id": "...",
  "session": {
    "session_id": "20260418103000523_a1b2",
    "created_at": "2026-04-18T10:00:00Z",
    "round_count": 4,
    "last_active_at": "2026-04-18T10:30:00Z",
    "path": "/home/groot/memory/..."
  },
  "history": {
    "messages": [
      {
        "round": 1,
        "chat_id": "chat_20260418103000523",
        "timestamp": "2026-04-18T10:00:00Z",
        "instruction": "用户指令",
        "attachments": ["data.csv"],
        "result": "执行结果",
        "result_attachments": [],
        "status": "completed",
        "duration": 45,
        "steps_count": 3,
        "error": null
      }
    ]
  }
}
```

### 5.2 GET /sess/history — 查询会话列表

分页查询所有会话，按最后活跃时间倒序排列。

**查询参数**

| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| `limit` | int | 否 | 返回条数，默认 20，最大 100 |
| `offset` | int | 否 | 分页偏移，默认 0 |

**响应**

```json
{
  "status": "success",
  "total": 50,
  "limit": 10,
  "offset": 0,
  "sessions": [
    {
      "session_id": "20260418103000523_a1b2",
      "created_at": "2026-04-18T10:00:00Z",
      "round_count": 4,
      "last_active_at": "2026-04-18T10:30:00Z"
    }
  ]
}
```

---

## 六、系统信息

### 6.1 GET /health — 健康检查

检查各组件运行状态。部分组件异常不影响整体健康状态（部分降级）。

**检查项**

| 检查 | 方法 |
|------|------|
| `llm` | 调用 LLM API `/models` |
| `mcp_servers` | 检查各 MCP 状态与工具数 |
| `skills` | 统计已加载 Skill 数 |
| `memory` | 统计当前会话数 |

**响应**

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "healthy", "info": {"model": "gpt-4o"}},
    "mcp_servers": {"status": "healthy", "info": [
      {"name": "file_operations", "type": "stdio", "description": "文件操作服务", "tools_count": 7, "isActive": true}
    ]},
    "skills": {"status": "healthy", "info": {"count": 4}},
    "memory": {"status": "healthy", "info": {"sessions": 10}}
  },
  "metrics": {
    "chats_running": 2
  }
}
```

组件异常时对应 `status` 为 `"unhealthy"`，`info` 中包含 `error` 字段，整体 `status` 仍为 `"healthy"`。

### 6.2 GET /skills — Skill 列表

返回所有已安装 Skill 的名称和描述。

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

### 6.3 GET /tools — MCP 工具列表

按 MCP Server 名称分组返回所有可用工具。每个 MCP 作为顶层 key，包含 `tools` 数组和 `total` 计数。

**响应格式**

```json
{
  "filesystem": {
    "tools": [
      {"name": "read_file", "description": "读取文件内容"},
      {"name": "write_file", "description": "写入文件内容"}
    ],
    "total": 14
  },
  "pencil": {
    "tools": [
      {"name": "get_editor_state", "description": "获取编辑器状态"}
    ],
    "total": 5
  }
}
```

**实现方案**

采用 Handler 层分组，`mcp.Manager.ListTools()` 返回平铺列表，分组逻辑在 Handler 中完成：

```
请求到达 → ToolsHandler.Serve()
  ├─ 调用 mcpManager.ListTools() 获取平铺列表
  ├─ 遍历工具，按 t.MCP 字段分组到 map[string]ToolsGroup
  │     ├─ 填充 Name、Description
  │     └─ 不填充 MCP 字段（已在分组 key 中体现）
  └─ 返回分组后的 map
```

**类型定义：**

```go
type ToolsGroup struct {
    Tools []ToolInfo `json:"tools"`
    Total int        `json:"total"`
}
```

**设计要点：**
- MCP 名称作为顶层 key，天然保证唯一性
- 工具对象不再包含 `mcp` 字段，避免数据冗余
- `mcp.Manager.ListTools()` 保持不变，其他调用方不受影响
- 向后不兼容：调用方需从平铺格式迁移到分组格式

---

## 七、调度管理

定时任务的创建通过 Agent 对话中的内置工具完成，API 仅提供查看和管理能力。**调度端点仅 Leader 实例可用**，Follower 实例返回 503。

### 7.1 端点列表

| 端点 | 方法 | 说明 |
|------|------|------|
| `/schedule` | GET | 列出所有任务（支持按状态过滤） |
| `/schedule/{id}` | GET | 查看任务详情（含 task.json 内容） |
| `/schedule/{id}/history` | GET | 查看执行历史 |
| `/schedule/{id}` | DELETE | 删除任务（物理删除目录） |
| `/schedule/{id}/disable` | POST | 禁用任务（active → disabled） |
| `/schedule/{id}/enable` | POST | 启用任务（disabled → active） |
| `/schedule/{id}/archive` | POST | 归档任务（→ archive） |

### 7.2 处理流程

```
请求到达 → API Handler
  ├─ 检查 scheduleMgr 是否为 nil
  │     ├─ nil → 返回 503: "schedule service not available"
  │     └─ 非 nil → 继续
  └─ 调用 Manager 对应方法
        ├─ 列表/详情 → 读取 active/、disabled/、archive/ 目录
        ├─ 删除 → 物理删除任务目录
        ├─ 禁用 → 移动目录 active/ → disabled/
        ├─ 启用 → 移动目录 disabled/ → active/
        └─ 归档 → 移动目录 → archive/
```

### 7.3 响应格式

**GET /schedule（列表）：** 直接返回 `[]*Task` 数组。
**GET /schedule/{id}（详情）：** 直接返回 `*Task` 对象。
**GET /schedule/{id}/history（历史）：** 直接返回 `[]ExecutionRecord` 数组。

**操作响应格式：**

```json
{"status": "deleted", "id": "task-check-health"}
{"status": "disabled", "id": "task-check-health"}
{"status": "enabled", "id": "task-check-health"}
{"status": "archived", "id": "task-check-health"}
```

**错误响应：** Follower 实例返回 503 `{"status": "schedule_unavailable", "message": "调度服务不可用"}`，其他错误返回 500 `{"status": "schedule_error", "message": "..."}`。

### 7.4 RESTful 设计说明

调度端点遵循两段式路径 `/schedule` + `/schedule/{id}`，禁用/启用/归档等动作通过动词后缀表达（`/disable`、`/enable`、`/archive`），采用 POST 方法而非 PUT/PATCH，与 HTTP DELETE 及标准查询明确区分。

---

## 八、认证与鉴权

### 8.1 API Key 认证

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

> `header_name` 为空时默认使用 `X-API-Key`。

**认证流程：**

```
请求到达 → Auth 中间件
  ├─ enabled=false → 跳过认证
  └─ enabled=true
        ├─ 提取 X-API-Key header
        ├─ 匹配 keys 列表中的 key 值
        │     ├─ 不匹配 → 401: {"status": "unauthorized", "message": "API Key 无效或缺失"}
        │     └─ 匹配 → 继续
        ├─ 检查该 key 的 permissions 是否覆盖当前端点
        │     ├─ 不覆盖 → 403: {"status": "forbidden", "message": "权限不足"}
        │     └─ 覆盖 → 通过
        └─ 记录调用方 name，继续处理
```

### 8.2 权限定义

| 权限 | 可访问端点 |
|------|-----------|
| `chat` | POST /chat |

| `status` | GET /chat/status/{sid} |
| `detail` | GET /chat/{sid}、GET /chat/{sid}/{cid} |
| `session` | GET /sess/{sid} |
| `history` | GET /sess/history |
| `skills` | GET /skills |
| `tools` | GET /tools |
| `schedule` | 所有 /schedule/* 端点 |
| `all` | 以上全部 |
| （无权限） | GET /health（不经过认证中间件） |

### 8.3 多 Key 配置示例

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

---

## 九、限流

API 支持基于调用方身份的 QPS 限制和并发控制，通过 `RateLimitMiddleware` 实现。

### 9.1 配置

```yaml
security:
  rate_limit:
    enabled: true
    global_qps: 50              # 全局 QPS 限制
    global_concurrency: 10      # 全局并发连接数
    default_qps: 10             # 单调用方默认 QPS
    default_concurrency: 5      # 单调用方默认并发数
    cleanup_interval: 5m        # 过期条目清理间隔
```

### 9.2 限流机制

| 端点 | 限流方式 | 说明 |
|------|---------|------|
| POST /chat | QPS + 并发 | 长连接 SSE，`Acquire(key)` 先检查 QPS，再获取并发槽位；连接关闭时 `Release(key)` 释放 |
| 其他所有端点 | QPS 检查 | 短连接，通过 `Allow(key)` 做一次性 QPS 判定 |

### 9.3 调用方标识

- 已认证调用方：`key:<caller_name>`
- 匿名用户：`ip:<client_ip>`（去除 IPv6 方括号和端口）

### 9.4 响应

触发限流返回 429：

```json
{
  "status": "rate_limited",
  "message": "请求过于频繁，请稍后重试"
}
```

---

## 十、附件校验

### 10.1 配置

```yaml
attachment:
  max_size: 50          # 单个附件最大 (MB)
  max_total_size: 100   # 总大小限制 (MB)
  max_count: 10         # 数量限制
  allowed_types: [pdf, doc, docx, txt, json, csv, xml, yaml, png, jpg, jpeg, zip]
```

### 10.2 校验流程

```
收到附件 → 逐个校验
  ├─ 1. 数量校验: len(attachments) > max_count → 400 (attachment_count_exceeded)
  ├─ 2. 文件名校验: name 为空 → 400 (attachment_missing_name)
  ├─ 3. 类型合法性校验: type 不在 [file, image, audio, video] → 400 (attachment_invalid_type)
  ├─ 4. 内容校验: content 为空 → 400 (attachment_missing_content)
  ├─ 5. 扩展名校验: (仅 file/image 类型) 扩展名不在 allowed_types → 400 (attachment_type_not_allowed)
  ├─ 6. 大小校验: (仅 file 类型) 预估解码后大小 > max_size → 400 (attachment_size_exceeded)
  └─ 7. 总大小校验: 累计 > max_total_size → 400 (attachment_total_size_exceeded)
```

### 10.3 错误码（均返回 400）

| 错误码 | 说明 |
|--------|------|
| `attachment_count_exceeded` | 数量超限 |
| `attachment_type_not_allowed` | 类型不在允许列表 |
| `attachment_size_exceeded` | 单个附件超限 |
| `attachment_total_size_exceeded` | 总大小超限 |
| `attachment_missing_content` | 附件缺少 content 字段 |
| `attachment_missing_name` | 附件缺少文件名 |
| `attachment_invalid_type` | 附件类型不在 [file, image, audio, video] |
| `attachment_validation_error` | 通用附件校验失败 |
| `attachment_decode_error` | Base64 解码失败 |

---

## 十一、错误码速查

| HTTP | 错误码 | 说明 |
|------|--------|------|
| 400 | `invalid_request` | 请求参数无效 |
| 400 | `invalid_model` | 模型名不存在 |
| 400 | `attachment_*` | 附件校验失败（见第十章） |
| 401 | `unauthorized` | API Key 无效或缺失 |
| 403 | `forbidden` | 权限不足 |
| 404 | `session_not_found` | 会话不存在 |
| 404 | `chat_not_found` | 对话记录不存在 |
| 404 | `task_not_found` | 调度任务不存在 |
| 409 | `chat_limit_exceeded` | 会话已有活跃对话 |
| 429 | `rate_limited` | 触发 API 限流 |
| 500 | `config_error` | 配置错误 |
| 500 | `llm_connection_error` | LLM 连接失败 |
| 500 | `tool_call_error` | 工具调用失败 |
| 503 | `schedule_unavailable` | 调度服务不可用（非 Leader 或未启动） |
| 500 | `schedule_error` | 调度操作失败 |

---

**参考设计文档**:
- [Groot Agent 整体设计](./2026-04-18-groot-agent-design.md) — 内部处理流程、附件透传、架构设计
- [Skill 设计](./2026-05-10-skills-design.md) — Skill 定义格式、热插拔、CLI 管理
- [Memory 设计](./2026-05-11-memory-design.md) — 存储结构、读路径、清理策略
- [调度设计](./2026-05-11-schedule-design.md) — 调度引擎、任务执行流程、内置工具
- [集群管理设计](./2026-05-15-cluster-management-design.md) — Leader 选举、故障转移、503 原因
