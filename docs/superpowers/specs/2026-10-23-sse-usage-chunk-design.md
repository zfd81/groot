# SSE 流 token usage 输出设计文档

## 一、功能设计

### 1.1 功能概述

`POST /chat` 的 SSE 流在结束前输出一条 token 用量事件，形状与 OpenAI Chat Completions 流式接口在 `stream_options.include_usage` 开启时的最后一个 chunk 完全一致：`choices` 为空数组，顶层 `usage` 携带本次对话的 `prompt_tokens`、`completion_tokens`、`total_tokens`。该事件无需客户端通过参数开启，每次对话都会输出；请求中即使携带 `stream_options`，也不改变行为。任何按 OpenAI 规范实现的客户端（官方 SDK、LangChain、LiteLLM 等）无需修改即可从 `chunk.usage` 读到用量。

usage 的口径是**本轮对话全部模型调用的累加**：主 Agent 的多轮 ReAct 调用，加上本轮中所有子 Agent（`call_agent`）内部的模型调用。这与数据库中按主/子 Agent 分账的 `memory_chats` 记录互补：数据库记录回答"每个 Agent 各用了多少"，SSE usage 回答"这一次请求总共用了多少"。

### 1.2 能力清单

1. 流式普通 chunk（thinking / message / tool_calls / finish / tool_result）顶层带 `"usage": null`，与 OpenAI 开启 include_usage 后的 wire 形状一致。
2. 对话正常完成或被取消（`/chat/stop`）时，在 `data: [DONE]` 之前输出一条 usage chunk：

   ```json
   data: {"id":"chat_abc","object":"chat.completion.chunk","created":1760947200,"model":"qwen-max","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":45,"total_tokens":165}}

   data: [DONE]
   ```

   其 `id`、`object`、`created` 与本流其他 chunk 相同，`model` 为主模型名。
3. usage 为本轮所有模型调用（主 Agent + 全部子 Agent）的累加；`total_tokens = prompt_tokens + completion_tokens`。
4. 本轮一个 token 都未统计到（上游未返回 usage、或模型创建失败后无调用）时，不输出 usage chunk，直接 `[DONE]`，避免发出全零数据污染客户端统计。
5. 致命错误路径（`error` 事件后紧跟 `[DONE]`）不输出 usage chunk，与 OpenAI 错误流一致。
6. 子 Agent 的 token 能正确归属到其子 chatID：引擎事件循环按"主 chatID + 子 Agent 名"查找子 chatID，不依赖子 Agent 的执行 context 传递到主事件循环。
7. 子 Agent 的 ChatRecord 在任何路径（正常、取消、致命错误）下都写入该子 Agent 自身的 token 与 model：子聚合由 `CallAgentTool` 自己取出并写库，不与引擎收尾产生竞争。引擎对每个 chunk 先累加 token 再写出 finish，使子记录在绝大多数情况下拿到完整用量；极少数情况下子记录可能缺少末尾一个 usage chunk，但全轮合计（usage chunk 的数值）始终完整。
8. 本轮累加项不残留：每轮在引擎 Run 入口显式开启、Run 退出时（三条返回路径）统一关闭；关闭后迟到的登记或归还被丢弃而不是重建条目。

### 1.3 设计细节

#### 1.3.1 SSE 输出（`internal/agent/sse.go`）

`chatChunk` 增加字段：

```go
type chatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chunkUsage   `json:"usage"` // 普通 chunk 为 nil → 序列化为 null
}

type chunkUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
```

`Choices` 在 usage chunk 中必须序列化为 `[]` 而非 `null`，因此 `WriteUsage` 构造时使用 `[]chunkChoice{}`（非 nil 空切片）。

新增方法：

```go
// WriteUsage 写出 usage chunk：choices 为空数组，顶层 usage 为本轮累计用量，model 为主模型名。
func (s *SSEWriter) WriteUsage(u TokenUsage) error
```

`WriteError` 不受影响（无 `choices`、无 `usage`）。

#### 1.3.2 进度回调（`internal/agent/engine.go`）

`ProgressCallback` 增加 `WriteUsage func(usage TokenUsage) error`。Executor 把它绑定到 `sse.WriteUsage`，不做 ctx 检查（与 `WriteFinish`/`WriteDone` 同类，收尾事件尽力写出）。

#### 1.3.3 token 累加器（`internal/agent/token_accumulators.go`）

在现有 `ChatAggregate` 分账结构之上，增加"轮次"概念：一轮以主 chatID 标识，持有子 chatID 登记表和一个"已归还子 Agent 用量"的合计。

```go
// OpenRound 为 mainChatID 开启一轮：建立登记表与归还合计。重复开启幂等。
func (a *TokenAccumulators) OpenRound(mainChatID string)

// RegisterChild 把 (mainChatID, agentName) → childChatID 登记到本轮；轮次未开启或已关闭时丢弃。
// 同一 agentName 重复登记以最新的为准。
func (a *TokenAccumulators) RegisterChild(mainChatID, agentName, childChatID string)

// ChildFor 返回 (mainChatID, agentName) 对应的子 chatID；未登记返回空串。
func (a *TokenAccumulators) ChildFor(mainChatID, agentName string) string

// PopChild 取出并删除 childChatID 的聚合，同时把其 token 累加进 mainChatID 本轮的归还合计，
// 并把 childChatID 记入本轮的已归还 id 列表（retiredIDs）。agentName 的登记保持不变，
// 之后迟到的 Add 仍能通过 ChildFor 归属到该 childChatID。
// 轮次已关闭时仅取出删除、不归还。不存在的 childChatID 返回零值，不重建条目。
func (a *TokenAccumulators) PopChild(mainChatID, agentName, childChatID string) ChatAggregate

// PopRound 关闭 mainChatID 的一轮：取出并删除主聚合、归还合计，并扫描登记表 children 与 retiredIDs
// 的并集，把其中仍存在的子聚合（未归还的、以及归还后迟到写入重建的）取出删除并计入合计，
// 返回主聚合与全轮 token 合计（主 + 已归还子 + 扫描所得）。同一 id 只计一次。不存在的轮次返回零值。
func (a *TokenAccumulators) PopRound(mainChatID string) (main ChatAggregate, roundTokens TokenUsage)
```

内部增加 `rounds map[string]*roundState`（`children map[agentName]childChatID` + `retired TokenUsage` + `retiredIDs []string`），与 `m` 共用同一把锁。现有 `Add` / `AppendStep` / `CompleteStep` / `SetModel` 不变；`PopAndDelete` 在生产路径上没有调用者，保留用于单 chatID 的直接清理（测试使用）。

#### 1.3.4 子 Agent 归属（`internal/agent/call_agent.go`、`engine.go`）

- `CallAgentTool.InvokableRun` 生成 `childChatID` 后调用 `RegisterChild(t.parentChatID, input.AgentName, childChatID)`。`t.parentChatID` 即主 Agent 的 `task.ID`，与引擎 `WithMainChatID` 注入值相同。
- 子 Agent 执行结束后，`CallAgentTool` 用 `PopChild(t.parentChatID, input.AgentName, childChatID)` 取出聚合写入子 ChatRecord。这一步是"自己取出、自己删除"，因此子记录的 token / model 在取消与致命错误路径下同样可用，不受引擎收尾时序影响。
- 引擎事件循环对每个携带 `ResponseMeta` 的 chunk，先执行 `accumulateUsage` 再执行 `WriteFinish`（流式与非流式分支一致）：token 累加是内存操作，放在 JSON 序列化与 HTTP 写出之前，使子 Agent 的最后一个 usage chunk 在绝大多数情况下先于 `PopChild` 落入子聚合。
- `Engine.resolveChatIDForAgent`：事件源非主 Agent 时，先查 `ChildFor(mainChatIDFromContext(ctx), eventAgentName)`；查不到再回退到 `childChatIDFromContext(ctx)`（保留现有单测注入路径）。

说明：引擎事件循环运行在主 Agent 的 Run ctx 上，而子 chatID 只存在于子 Agent 自身的执行 ctx，两者不是同一条 context 链，事件循环无法通过 ctx 得到子 chatID。登记表机制使事件循环无需依赖 ctx 传递即可完成归属；ctx 回退仅服务于单测注入。

时序与竞争：

- 正常完成路径下，ADK 迭代器在全部工具调用返回后才结束，所以每个子 Agent 的 `PopChild` 都先于 `PopRound`。
- `PopChild` 与 `accumulateUsage` 之间存在竞争：子 Agent 的最后一个模型事件以复制流的形式同时送达 agent-tool 侧与引擎事件循环，agent-tool 侧读到 EOF 后 `CallAgentTool` 立即 `PopChild`，而引擎侧还要处理同一个 chunk 并 `accumulateUsage`。两者先后顺序不确定。为此 `PopChild` 不注销登记，迟到的 `accumulateUsage` 仍经 `ChildFor` 解析到该 childChatID 并重建其聚合；`PopRound` 严格在迭代器结束后运行，扫描 `children` 与 `retiredIDs` 的并集把这部分迟到用量计入全轮合计。结果是：usage chunk 的全轮数值始终完整；子 ChatRecord 在 `PopChild` 抢先的极少数情况下可能缺少末尾一个 usage chunk 的数值，这是可接受的残余。
- 取消或子 Agent 致命错误路径下，事件循环可能先于 `CallAgentTool` 收尾。`PopRound` 把登记表中尚未归还的子聚合一并取出计入合计并关闭轮次；之后 `CallAgentTool` 的 `PopChild` 对已删除的 key 得到零值。此时子 ChatRecord 的 token 可能为 0，这是取消路径可接受的损失（原实现同样如此）；而迟到的 `PopChild` / `RegisterChild` / `SetModel` 不会重建条目，累加器不残留。
- 同一子 Agent 在一轮内被多次调用时，登记表只保留最新 childChatID，被覆盖的旧 id 已在 `retiredIDs` 中，`PopRound` 仍能扫描到它；并发调用下个别 chunk 的归属可能落到最近登记的那一次，但全轮合计不受影响，也不会泄漏条目。

#### 1.3.5 引擎收尾（`internal/agent/engine.go`）

`Engine.Run` 的三条返回路径统一收尾：

| 路径 | 行为 |
|------|------|
| 正常完成 | `main, round := PopRound(mainChatID)`；`round.Total > 0` 时 `cb.WriteUsage(round)`；然后 `cb.WriteDone()`；`RunResult.Tokens = main.Tokens` |
| 取消（ctx 取消或 agentCancelled） | 同上（取消前已消耗的 token 同样上报） |
| 致命错误 `return nil, err`（共四处：模型查找、ChatModel 创建、Agent 创建、LLM 连接失败） | 仅 `PopRound` 清理累加项，不写 usage（Executor 随后写 error + DONE） |

`Run` 入口在 `WithMainChatID` 之后调用 `OpenRound(mainChatID)`；`popMainAggregateTokens` 由 `PopRound` 取代。`WriteUsage` 的 `model` 取 `modelFor("")`，Solo 模式下经 `SetModel` 覆盖的主模型名得以体现。`RunResult.Tokens` 语义不变（仅主 Agent），主 ChatRecord 的分账字段不变。

#### 1.3.6 文档与示例

- `README.md` 的 `/chat` SSE 格式章节：
  - 外层字段表含 `usage` 行（普通 chunk 为 `null`，usage chunk 为对象）；`choices` 行说明"普通事件恒为单元素数组；usage 事件为空数组 `[]`"。
  - 事件类型表含 `usage` 事件（`choices` 为 `[]`，顶层 `usage`，位于 `[DONE]` 前，一次对话最多一条）。
  - 客户端解析伪代码含 `choices` 为空时读取 `usage` 的分支。
  - 各示例流末尾均带 usage chunk。
- `examples/python/groot_client.py`、`examples/java/.../GrootClient.java`、`web/src/api/sseChunk.ts` 的 `flattenChunk`：`choices` 为空且顶层含 `usage` 对象时，返回 `{"event": "usage", "usage": {...}}`（Java/TS 同形），事件分类器识别为 `usage`。`choices` 为空且 `usage` 为 null 时维持原样返回 / 忽略的处理，不会崩溃。
- `tests/python/conftest.py` 的 `SSEClient.flatten_chunk` / `_infer_event_type` 增加 `usage` 事件识别；`tests/python/test_sse_events.py` 中"最后一个事件是 finish"的断言调整为"最后一个事件是 usage，其前一个是 finish"。系统测试代码随格式同步修改，运行仍由用户自行决定。
- `tests/TEST_CASES.md` 追加 usage 事件相关用例点。

### 1.4 不做的事

- 不支持 `stream_options.include_usage=false` 关闭 usage 输出。
- 不输出 `prompt_tokens_details` / `completion_tokens_details`：本版按设计只输出 OpenAI 的三个基础字段（eino 的 `schema.TokenUsage` 携带 `PromptTokenDetails.CachedTokens` 与 `CompletionTokensDetails.ReasoningTokens`，后续版本可按需补充）。
- 不改变数据库表结构与 ChatRecord 字段。
- 不做租户级配额限制；配额统计仍基于 `memory_chats` 的分账记录。

## 二、迭代说明

### 2.1 与上一版差异

- 新增：SSE 流在 `[DONE]` 前输出 OpenAI 规范形状的 usage chunk（`choices: []` + 顶层 `usage`），默认开启，无需参数。
- 新增：所有普通 chunk 顶层带 `"usage": null`。
- 新增：`SSEWriter.WriteUsage`、`ProgressCallback.WriteUsage`。
- 新增：`TokenAccumulators.OpenRound` / `RegisterChild` / `ChildFor` / `PopChild` / `PopRound`。
- 调整：`Engine.resolveChatIDForAgent` 优先通过登记表解析子 chatID，修复生产路径上子 Agent token 无法归属（始终记为 0）的问题。
- 调整：`CallAgentTool` 结束时由 `PopAndDelete` 改为 `PopChild`，子聚合在删除的同时归还到本轮合计；引擎 `Run` 入口开启轮次，四处致命错误返回路径也执行 `PopRound` 清理，消除原先该路径的主聚合残留。
- 移除：`Engine.popMainAggregateTokens`。
- 调整：`PopChild` 不再注销 `children[agentName]` 登记，改为把 childChatID 追加到 `roundState.retiredIDs`；`PopRound` 扫描 `children` 与 `retiredIDs` 的并集。原因：子 Agent 最后一个 usage chunk 的 `accumulateUsage` 与 `CallAgentTool` 的 `PopChild` 存在竞争，原先 `PopChild` 删除登记后迟到的累加解析不到 chatID 而被丢弃，单次 LLM 调用的子 Agent 会丢失全部 token。
- 调整：引擎事件循环流式/非流式分支中 `accumulateUsage` 移到 `WriteFinish` 之前，减少子 ChatRecord 丢失末尾 usage chunk 的概率。
- 调整：`PopAndDelete` 不再有生产调用者，保留供测试直接清理单 chatID。
- 调整：`tests/python/test_api_endpoints.py`（TC-001）、`test_sse_flow.py`（TC-Flow-003、TC-Debug-001）、`test_real_llm.py`（TC-REAL-017）的末尾事件断言由 finish 改为 usage（TC-Debug-001 为 usage 或 error）。
- 调整：README `/chat` SSE 章节、三个示例客户端与 `web/src/api/sseChunk.ts` 的 chunk 摊平逻辑、`tests/python/conftest.py` 与 `test_sse_events.py` 的事件识别与末尾断言、`tests/TEST_CASES.md`。
- 原因：客户端需要在流内直接拿到本次请求的 token 用量，且格式须与 OpenAI 一致，不自创规范。
