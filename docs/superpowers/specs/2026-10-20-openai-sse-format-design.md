# /chat 接口 OpenAI 兼容 SSE 响应格式设计文档

## 一、功能设计

### 1.1 功能概述

`POST /chat` 是 Groot 的核心对话接口，以 Server-Sent Events（SSE）流式返回 Agent 的执行过程。
响应流采用 OpenAI Chat Completions 的流式分块格式（`object: "chat.completion.chunk"`），
使任何支持 OpenAI 流式协议的客户端库、聊天前端或中间件都能直接消费 Groot 的回答文本，
无需为 Groot 编写专用解析器。

Groot 是服务端执行工具的 Agent，而非单纯的模型代理，因此响应流中除模型输出外，还包含
工具执行结果与多 Agent 归属信息。这些信息以扩展字段的形式放在标准结构内：Groot 自有
客户端（Web UI、Python/Java 示例）能完整读取并渲染执行过程；标准 OpenAI 客户端会忽略
不认识的字段，只看到最终回答文本，不会解析失败。

请求入参（请求体 `instruction/prompt/attachments`，请求头 `X-Session-ID`、`X-Model-Name`、
`X-Agent-Name`、`X-User-ID`）与响应头（`X-Session-ID`、`X-Chat-ID`）保持原有定义。

### 1.2 能力清单

- 响应流中的每条事件均为合法的 `chat.completion.chunk` 对象，含 `id`、`object`、`created`、`model`、`choices` 五个顶层字段
- 思考过程、回答正文、工具调用（含 skill 调用、子 Agent 调用）、阶段结束原因均按 OpenAI 流式约定放置于 `choices[0].delta` / `choices[0].finish_reason`
- 工具执行结果、Agent 归属（`agent_name`）作为 `delta` 的扩展字段携带
- 错误以 OpenAI 流式错误对象 `{"error": {...}}` 形式发送
- 流以 `data: [DONE]` 结束
- 客户端可按 `tool_calls[].id` 与工具结果的 `tool_call_id` 配对，自行计算每个环节的耗时

### 1.3 设计细节

#### 1.3.1 分块外层结构

每条事件的 `data` 为一个 JSON 对象：

| 字段 | 类型 | 取值 |
|------|------|------|
| `id` | string | 本次对话的 chat_id，与响应头 `X-Chat-ID` 相同，整条流内不变 |
| `object` | string | 固定 `"chat.completion.chunk"` |
| `created` | int | 对话开始时间的 Unix 秒，整条流内不变 |
| `model` | string | 生成本条事件的模型逻辑名称（`Model.Name`）。主 Agent 事件为 `X-Model-Name` 解析后的主模型名（含走默认模型的情况）；子 Agent 事件（带 `agent_name`）为该子 Agent 实际选用的模型名，按既有优先级解析：agent.md 显式声明的 `model` > 主流程当前模型 > 系统默认模型；未显式声明时与主模型名相同。Solo 模式下为该子 Agent 实际选用的模型名（agent.md 钉死的模型优先） |
| `choices` | array | 恒为单元素数组，`choices[0].index` 恒为 `0` |

`choices[0]` 结构：

| 字段 | 类型 | 说明 |
|------|------|------|
| `index` | int | 恒为 `0` |
| `delta` | object | 本块增量内容，见 1.3.2 |
| `finish_reason` | string \| null | 阶段结束原因，非结束块为 `null` |

#### 1.3.2 delta 事件类型

事件类型由 `delta` 中的字段组合判定。每条事件只承载一种类型，`reasoning_content` 与 `content` 不会出现在同一个 `delta` 中。

| 事件 | delta 字段 | finish_reason | 标准客户端可见 | 说明 |
|------|-----------|---------------|---------------|------|
| thinking | `role:"assistant"`, `reasoning_content` | `null` | 可见（事实标准字段） | 模型思考增量 |
| message | `role:"assistant"`, `content` | `null` | 可见 | 回答正文增量，唯一进入正文区的内容 |
| tool_calls | `role:"assistant"`, `tool_calls[]` | `null` | 可见 | 模型发起的工具调用；skill 调用为 `function.name == "skill"`，子 Agent 调用为 `function.name == "call_agent"`，由客户端按工具名区分 |
| finish | `{}`（可含 `agent_name`） | 上游模型返回的 OpenAI 结束原因，常见为 `"stop"` / `"tool_calls"` / `"length"` / `"content_filter"` | 可见 | 阶段结束；一次对话可出现多次（每轮 ReAct 一次）。取值原样透传，不做映射 |
| tool_result | `role:"tool"`, `tool_call_id`, `tool_name`, `content`, `error` | `null` | 忽略（扩展） | 服务端执行工具的结果，`error:true` 表示 MCP 工具返回错误 |

`agent_name`（string，扩展字段）：事件来自子 Agent 时注入到 `delta` 中，主 Agent 事件不带此字段。适用于以上全部五种事件。

`tool_calls[]` 元素结构与 OpenAI 一致：`index`（可选）、`id`、`type:"function"`、`function.name`、`function.arguments`（JSON 字符串），另有可选 `extra` 对象承载模型附加元数据。

#### 1.3.3 错误事件

```json
{"error": {"message": "LLM 服务连接失败: ...", "type": "server_error", "code": null, "param": null}}
```

错误事件不含 `choices`。事件来自子 Agent 时，在 `error` 对象内附带 `agent_name`。

错误事件是**终结性**的，与 OpenAI 语义一致：它只用于无法继续执行的致命错误（LLM 连接失败、Agent 执行 panic、Solo 模式子 Agent 构建失败等），之后紧跟 `data: [DONE]`，不再有任何 `choices` 事件。客户端收到 `error` 即可视为本次对话结束。

执行过程中可恢复的错误（MCP 工具调用失败、单步节点运行错误等）不使用 `error` 事件，而是以 `tool_result` 事件发送且 `error:true`，Agent 随后继续执行。节点运行错误无对应工具调用时，`tool_call_id` 为空字符串、`tool_name` 为 `"agent_error"`。

#### 1.3.4 结束标记

```
data: [DONE]
```

与 OpenAI 一致，为流的最后一条数据，不是 JSON。

#### 1.3.5 完整示例

一次含思考、skill 调用、工具结果与最终回答的对话（`id/object/created/model` 每块均带，示例中省略重复）：

```
data: {"id":"chat_abc","object":"chat.completion.chunk","created":1760947200,"model":"qwen-max","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"需要先加载 pdf 技能"},"finish_reason":null}]}

data: {..., "choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"skill","arguments":"{\"skill\":\"pdf\"}"}}]},"finish_reason":null}]}

data: {..., "choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {..., "choices":[{"index":0,"delta":{"role":"tool","tool_call_id":"call_1","tool_name":"skill","content":"# PDF skill ...","error":false},"finish_reason":null}]}

data: {..., "choices":[{"index":0,"delta":{"role":"assistant","content":"已加载 pdf 技能，"},"finish_reason":null}]}

data: {..., "choices":[{"index":0,"delta":{"role":"assistant","content":"开始处理。"},"finish_reason":null}]}

data: {..., "choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

子 Agent 参与时，其事件在 `delta` 内多一个 `"agent_name":"coder"`。

#### 1.3.6 客户端事件识别规则

```
data == "[DONE]"                     → 流结束
存在顶层 error                        → 错误
d = choices[0].delta
  d.role == "tool"                   → tool_result
  d.tool_calls 存在                   → tool_calls（按 function.name 判定 skill / call_agent / 普通工具）
  d.reasoning_content 存在            → thinking
  d.content 存在                      → message
choices[0].finish_reason 非 null      → finish
```

工具环节耗时由客户端在收到 tool_calls 事件时记起点、收到同 `tool_call_id` 的 tool_result 时结算；思考环节在首个 thinking 块记起点、下一个非 thinking 事件到达时结算。

#### 1.3.7 服务端实现要点

- `internal/agent/sse.go` 的 `SSEWriter` 新增 `model` 与 `created` 字段，由 `NewSSEWriter` 传入；`WriteThinking/WriteMessage/WriteToolCalls/WriteFinish/WriteToolResult` 统一经由一个内部方法把 `delta` 与 `finish_reason` 包装进 chunk 外层；`WriteError` 输出 `{"error":{...}}`；`WriteDone` 不变
- `internal/api/handler/chat.go` 在构造 `SSEWriter` 时传入 `mdl.Name` 与 `task.StartTime.Unix()`
- `SSEWriter` 维护一张并发安全的「子 Agent 名 → 实际模型名」映射，提供 `SetAgentModel(agentName, model)` 与 `ClearAgentModel(agentName)`；`WriteX` 按事件的 `agentName` 查表填充 `model`，`agentName` 为空或查不到时用主模型名
- `internal/agent/call_agent.go` 在 `BuildAgentTool` 返回 `resolvedModel` 后调用 `SetAgentModel`，该映射保留至请求结束、不主动清除：`SSEWriter` 按请求创建，一次请求内子 Agent 的模型固定，而引擎在另一 goroutine 上转发子 Agent 事件，调用返回后仍可能写出其尾部 content/finish chunk，提前清除会使这些 chunk 误标为主模型名；`SSEWriter` 需通过现有的 Executor → call_agent 构造链路传入
- `internal/agent/engine.go` 中"其他 NodeRunError → 发 error 后继续"的分支改为调用 `WriteToolResult(agentName, "", "agent_error", errStr, true)`，使 `WriteError` 只在致命路径（连接失败后 return、executor 的 panic/Solo 失败）被调用
- `engine.go`、`executor.go` 的回调签名不变

#### 1.3.8 消费方

| 消费方 | 文件 | 改动 |
|--------|------|------|
| Web 前端 | `web/src/api/sse.ts` | 解析 chunk 外层，把 `choices[0].delta` 展开成现有 `SseEvent`，并把 `finish_reason` 并入；顶层 `error` 映射为 `event:"error"` |
| Web 前端 | `web/src/stores/chat.ts` | 字段读取路径随 `SseEvent` 变化（若 `sse.ts` 完成归一化则可不改） |
| Python 示例 | `examples/python/groot_client.py` | 解析层把 `choices[0].delta` 与 `finish_reason` 摊平成原扁平对象后再分类，`on_event` 回调收到的数据结构不变 |
| Java 示例 | `examples/java/.../GrootClient.java` | 同上，监听器收到的数据结构不变 |
| 用户手册 | `README.md` | "SSE 响应格式"及其后示例、伪代码章节整体替换为本文 1.3 内容 |
| 系统测试 | `tests/python/conftest.py` 的 `SSEClient` | 在 `_infer_event_type` 处摊平 `choices[0].delta`，使依赖它的测试文件尽量不改 |
| 系统测试 | `tests/python/test_sse_*.py`、`test_supplementary.py`、`test_errors.py`、`test_performance.py`、`test_multimodal.py`、`test_api_endpoints.py`、`test_real_llm.py`、`test_attachments.py`、`tests/examples/**` | 逐个检查直接读取原始 JSON 的断言，改为新结构 |

设计者决定（非用户指定）：

- Web 前端在 `sse.ts` 一层完成归一化，`chat.ts` 的业务逻辑不感知格式变化，减小改动面
- `created` 取 `task.StartTime`，而非每块各自的当前时间，与 OpenAI 一致（同一响应内 `created` 恒定）
- `model` 使用逻辑名 `Model.Name` 而非上游真实模型 ID `Model.Model`，与 `X-Model-Name` 入参语义对称
- 子 Agent 模型名通过 `SSEWriter` 内部的按 Agent 名查表获得，而不是扩展 `WriteX(agentName, ...)` 回调签名：`resolvedModel` 在 `call_agent.go` 构建子 Agent 时即已确定，且同一子 Agent 的模型在一次对话内固定，按名查表安全
- `finish_reason` 原样透传：Groot 仅接入 OpenAI 兼容上游（`eino-ext/components/model/openai`），上游返回值已是 OpenAI 枚举，不引入映射层
- Python/Java 示例在解析层摊平，对外回调结构不变：示例是面向用户的公开接口，避免用户代码跟着改

用户决定：

- `error` 事件语义与 OpenAI 一致，为终结性事件；可恢复错误改走 `tool_result`（`error:true`）
- `model` 字段反映生成该事件的实际模型，子 Agent 使用不同模型时按子 Agent 的模型填写

### 1.4 非目标

- 不提供 `/v1/chat/completions` 路径别名，不接受 OpenAI 格式的请求体（`messages[]`），入参保持 Groot 自有定义
- 不实现 Responses API（`response.*` 事件）
- 不提供非流式（`stream:false`）响应
- 不输出 `usage` 块

## 二、迭代说明

### 2.1 与上一版差异

上一版响应流为扁平 JSON：`data: {"role":"assistant","content":"..."}`，无外层包裹。

- 新增：每条事件外层增加 `id`、`object`、`created`、`model`、`choices[0].index` 字段；原扁平 payload 整体移入 `choices[0].delta`
- 新增：`call_agent.go` 在子 Agent 启动时向 `SSEWriter` 登记该子 Agent 的实际模型名（保留至请求结束），供 `model` 字段填充
- 调整：`finish_reason` 从 `delta` 内部移到 `choices[0].finish_reason`，finish 事件的 `delta` 为空对象（子 Agent 场景仅含 `agent_name`）
- 调整：错误事件从 `{"event":"error","message":"..."}` 改为 `{"error":{"message":"...","type":"server_error"}}`
- 调整：错误事件改为终结性。原"其他 NodeRunError 发 error 后继续执行"的路径改为发送 `tool_result`（`tool_name:"agent_error"`, `error:true`）后继续；Web 前端对此类错误的展示从错误提示变为带错误标记的工具结果
- 保留：`[DONE]` 结束标记、`reasoning_content`/`content`/`tool_calls`/`role:tool` 工具结果/`agent_name` 的字段名与语义、请求入参与响应头
- 影响：Web 前端、Python 示例、Java 示例、README、Python 系统测试需同步修改字段读取路径；`engine.go` 仅改一处错误分支，`executor.go` 与回调接口不变
