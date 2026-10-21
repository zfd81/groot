# /chat OpenAI 兼容 SSE 响应格式 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `POST /chat` 的 SSE 流从扁平自定义 JSON 改为 OpenAI Chat Completions `chat.completion.chunk` 格式，入参不变，所有自有消费方同步适配。

**Architecture:** 格式转换全部收敛在 `internal/agent/sse.go` 的 `SSEWriter` 内：每个 `WriteX` 把原扁平 payload 作为 `choices[0].delta` 包进 chunk 外层，`finish_reason` 上提到 `choices[0]`，`WriteError` 改为 OpenAI 错误对象。子 Agent 的 `model` 通过 `SSEWriter` 内部的"Agent 名 → 模型名"映射填充，由 `call_agent.go` 登记/清除。`engine.go` 把可恢复的 NodeRunError 从 `WriteError` 改为 `WriteToolResult(error:true)`，使 `error` 事件成为终结性事件。各消费方（Web 前端 `sse.ts`、Python/Java 示例、测试 `conftest.py`）在解析层把 `choices[0].delta` 与 `finish_reason` 摊平成原扁平对象，上层业务逻辑不动。

**Tech Stack:** Go 1.x（hertz、eino）、Vue 3 + TypeScript、Python requests、Java OkHttp + Jackson、pytest、JUnit 5

**Spec:** `docs/superpowers/specs/2026-10-20-openai-sse-format-design.md`

## Global Constraints

- 请求体 `instruction/prompt/attachments` 与请求头 `X-Session-ID/X-Model-Name/X-Agent-Name/X-User-ID`、响应头 `X-Session-ID/X-Chat-ID` 不改动
- 每条 chunk 必含 `id`（= chat_id）、`object`（= `"chat.completion.chunk"`）、`created`（= `task.StartTime.Unix()`）、`model`、`choices[0].index == 0`
- `reasoning_content` 与 `content` 不出现在同一个 `delta` 中
- `agent_name` 只在非空时注入；主 Agent 事件不带
- `finish_reason` 原样透传，不做映射
- `error` 事件后必跟 `data: [DONE]`，不再有 `choices` 事件
- Go 单测放源码同目录；Python 系统测试放 `tests/python/`；示例测试放 `tests/examples/<语言>/`
- 不加 `/v1/chat/completions` 路径、不接受 `messages[]`、不做非流式、不输出 `usage`
- 所有 git commit 由用户明确请求后才执行；本计划的 Commit 步骤仅表示"此处为一个提交点，向用户请求提交"

## Review Focus

1. 子 Agent 事件在 `SetAgentModel` 之前到达（如 `BuildAgentTool` 失败）或 `ClearAgentModel` 之后到达：`model` 必须回退为主模型名而非空串。→ Task 2 增加查表缺失测试
2. 多个子 Agent 并发运行并同时写 SSE：模型映射表必须并发安全，`go test -race` 通过。→ Task 2 增加并发读写测试
3. `WriteFinish` 的 `delta` 在主 Agent 时必须为 `{}`（JSON 空对象）而非 `null`，标准客户端对 `delta: null` 可能抛错。→ Task 1 断言 `"delta":{}`
4. `WriteError` 不含 `choices`，前端/示例/conftest 的摊平函数遇到无 `choices` 的对象时不能抛异常。→ Task 5/6/7/8 各加 error 事件用例
5. 工具错误 `tool_result`（`tool_call_id` 为空的 `agent_error`）到达前端时无对应 tool 调用，必须作为独立错误步展示而非丢弃。→ Task 6 保留 chat.ts 现有"找不到调用则新建"分支，Task 5 加该事件摊平用例

---

### Task 1: SSEWriter 输出 chunk 外层结构

**Files:**
- Modify: `internal/agent/sse.go`
- Modify: `internal/agent/sse_test.go`
- Modify: `internal/api/handler/chat.go:307`

**Interfaces:**
- Produces: `func NewSSEWriter(w flushWriter, sessionID, chatID string, round int, model string, created int64) *SSEWriter`
- Produces（内部）: `func (s *SSEWriter) writeChunk(agentName string, delta map[string]any, finishReason *string) error`：填 `id/object/created/model/choices`，`agentName` 非空时写入 `delta["agent_name"]`，`model` 取 `s.modelFor(agentName)`（Task 2 实现，本任务先恒返回主模型名）
- 各 `WriteX(agentName, ...)` 签名不变

- [ ] **Step 1: 改写现有测试并新增 chunk 结构测试**

把 `sse_test.go` 中所有 `NewSSEWriter(buf, "sess", "chat", 1)` 改为 `NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)`。新增：

```go
// decodeChunk 把 buf 中最后一条 "data: {...}" 解成 map，供断言用
func decodeChunk(t *testing.T, s string) map[string]any

func TestSSEWriter_ChunkEnvelope(t *testing.T) {
	// WriteMessage("", "hi") 后：
	// chunk["id"] == "chat"; chunk["object"] == "chat.completion.chunk"
	// chunk["created"] == float64(1760947200); chunk["model"] == "qwen-max"
	// choices 长度 1，choices[0]["index"] == float64(0)
	// choices[0]["finish_reason"] == nil
	// choices[0]["delta"]["role"] == "assistant", ["content"] == "hi"
}

func TestSSEWriter_FinishMovesToChoice(t *testing.T) {
	// WriteFinish("", "stop") 后：
	// choices[0]["finish_reason"] == "stop"
	// 原始字符串包含 `"delta":{}`（空对象，不是 null）
	// delta 中不含 "finish_reason" 键
}

func TestSSEWriter_FinishWithAgentName(t *testing.T) {
	// WriteFinish("coder", "tool_calls") 后 delta == {"agent_name":"coder"}
}

func TestSSEWriter_ToolResultInDelta(t *testing.T) {
	// WriteToolResult("", "tc_1", "fs.read", "ok", true) 后 delta 含
	// role=="tool", tool_call_id=="tc_1", tool_name=="fs.read", content=="ok", error==true
}

func TestSSEWriter_ErrorEnvelope(t *testing.T) {
	// WriteError("", "boom") 后：
	// 顶层无 "choices" 键；chunk["error"]["message"]=="boom"; ["type"]=="server_error"
	// WriteError("coder","boom") 后 chunk["error"]["agent_name"]=="coder"
}

func TestSSEWriter_DoneUnchanged(t *testing.T) {
	// WriteDone 输出恰为 "data: [DONE]\n\n"
}
```

现有 `TestSSEWriter_AgentNameInjection` 中对 `"finish_reason":"stop"` 的 `mustHave` 仍成立（在 choices 层），`"agent_name"` 断言改为检查 delta 内。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run 'TestSSEWriter' -v`
Expected: 编译失败（`NewSSEWriter` 参数数量不匹配）

- [ ] **Step 3: 实现 SSEWriter 改动**

`internal/agent/sse.go`：
- `SSEWriter` 增加字段 `model string`、`created int64`
- `NewSSEWriter` 增加参数 `model string, created int64`
- 新增 `writeChunk(agentName string, delta map[string]any, finishReason *string) error`；`delta` 为 nil 时写入空 `map[string]any{}`；chunk 结构体用显式 struct 保证 `delta` 序列化为 `{}`，`finish_reason` 用 `*string` 使 nil 序列化为 `null`
- `WriteThinking/WriteMessage/WriteToolCalls/WriteToolResult` 各自构造原 payload（去掉 `agent_name` 注入逻辑）后调 `writeChunk(agentName, payload, nil)`
- `WriteFinish(agentName, reason)` 调 `writeChunk(agentName, nil, &reason)`
- `WriteError(agentName, message)` 直接 `WriteData(map{"error": {"message","type":"server_error","code":nil,"param":nil, 可选 "agent_name"}})`
- 新增 `modelFor(agentName string) string`，本任务恒返回 `s.model`
- `WriteData`、`WriteDone`、`ToolCall`、`FunctionCall` 不变

`internal/api/handler/chat.go:307`：改为 `agent.NewSSEWriter(pipeFlushWriter{pw}, sessionID, chatID, round, mdl.Name, task.StartTime.Unix())`

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/... ./internal/api/... -v`
Expected: 全部 PASS；`go build -o dist/groot ./cmd/groot` 成功

- [ ] **Step 5: 提交点**

```bash
git add internal/agent/sse.go internal/agent/sse_test.go internal/api/handler/chat.go
git commit -m "feat(sse): 以 OpenAI chat.completion.chunk 格式输出 SSE 事件"
```

---

### Task 2: 子 Agent 模型名登记与 model 字段填充

**Files:**
- Modify: `internal/agent/sse.go`
- Modify: `internal/agent/sse_test.go`
- Modify: `internal/agent/call_agent.go:60-92,145-153`
- Modify: `internal/agent/executor.go:203-216`
- Test: `internal/agent/call_agent_test.go`（若已存在则追加）

**Interfaces:**
- Consumes: Task 1 的 `writeChunk` / `modelFor`
- Produces: `func (s *SSEWriter) SetAgentModel(agentName, model string)`、`func (s *SSEWriter) ClearAgentModel(agentName string)`
- Produces: `CallAgentToolConfig` 新增字段 `SSE *SSEWriter`（可为 nil）

- [ ] **Step 1: 写失败测试**

`sse_test.go` 新增：

```go
func TestSSEWriter_ModelForSubAgent(t *testing.T) {
	// SetAgentModel("coder","deepseek-r1") 后 WriteMessage("coder","x") 的 chunk["model"]=="deepseek-r1"
	// 同时 WriteMessage("","x") 的 chunk["model"] 仍为 "qwen-max"
}

func TestSSEWriter_ModelFallbackWhenUnregistered(t *testing.T) {
	// 未登记的 "other" → model=="qwen-max"
	// SetAgentModel 后 ClearAgentModel("coder") → 再写 "coder" 事件 model 回到 "qwen-max"
	// SetAgentModel("coder","") 空字符串视为未登记，回退主模型
}

func TestSSEWriter_ModelMapConcurrent(t *testing.T) {
	// 10 个 goroutine 并发 SetAgentModel/WriteMessage/ClearAgentModel，不 panic
	// 需在 -race 下运行
}
```

`call_agent_test.go` 新增（使用现有 `testTool` 注入路径与 `AgentMdModel`）：

```go
func TestCallAgentTool_RegistersResolvedModelOnSSE(t *testing.T) {
	// 构造 entry.AgentMdModel="deepseek-r1"、testTool 返回固定串
	// cfg.SSE 为 NewSSEWriter(buf,...,"qwen-max",0)
	// testTool 内部调用 sseWriter.WriteMessage("coder","hi")，断言该 chunk model=="deepseek-r1"
	// InvokableRun 返回后再 WriteMessage("coder","bye")，断言 model=="qwen-max"（已 Clear）
}

func TestCallAgentTool_NilSSEIsSafe(t *testing.T) {
	// cfg.SSE == nil 时 InvokableRun 正常返回，不 panic
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run 'ModelFor|ModelFallback|ModelMapConcurrent|RegistersResolvedModel|NilSSEIsSafe' -race -v`
Expected: 编译失败（方法/字段未定义）

- [ ] **Step 3: 实现**

`sse.go`：
- `SSEWriter` 增加 `mu sync.RWMutex`、`agentModels map[string]string`
- `SetAgentModel`：空 `model` 时等价于 `ClearAgentModel`
- `modelFor(agentName)`：`agentName` 为空或查不到或值为空 → `s.model`

`call_agent.go`：
- `CallAgentToolConfig` 加 `SSE *SSEWriter`，`CallAgentTool` 加 `sse *SSEWriter`，`NewCallAgentTool` 透传
- `InvokableRun` 在 `BuildAgentTool` 成功返回后：`if t.sse != nil && resolvedModel != "" { t.sse.SetAgentModel(input.AgentName, resolvedModel); defer t.sse.ClearAgentModel(input.AgentName) }`

`executor.go:203`：`NewCallAgentTool(CallAgentToolConfig{..., SSE: sse})`

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/agent/... -race -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交点**

```bash
git add internal/agent/sse.go internal/agent/sse_test.go internal/agent/call_agent.go internal/agent/call_agent_test.go internal/agent/executor.go
git commit -m "feat(sse): 子 Agent 事件 model 字段反映其实际使用的模型"
```

---

### Task 3: error 事件终结化

**Files:**
- Modify: `internal/agent/engine.go:226-262`
- Modify: `internal/agent/engine_test.go`

**Interfaces:**
- Consumes: `Callbacks.WriteToolResult(agentName, toolCallID, toolName, content string, isError bool)`
- Produces: `const AgentErrorToolName = "agent_error"`
- Produces: `type runErrorKind int` 与常量 `runErrMCP`、`runErrFatalConn`、`runErrRecoverable`、`runErrIgnored`；`func classifyRunError(errStr string) runErrorKind`（纯函数，把现有 engine 事件循环内四段 `strings.Contains` 判定抽出来，优先级与现有顺序一致：mcp/command_not_allowed → 连接类 → NodeRunError → 其他）

- [ ] **Step 1: 写失败测试**

`engine_test.go` 追加表驱动测试：

```go
func TestClassifyRunError(t *testing.T) {
	cases := []struct{ in string; want runErrorKind }{
		{"mcp tool failed", runErrMCP},
		{"command_not_allowed: rm", runErrMCP},
		{"dial tcp 1.2.3.4: connection refused", runErrFatalConn},
		{"no such host", runErrFatalConn},
		{"request timeout", runErrFatalConn},
		{"NodeRunError: something broke", runErrRecoverable},
		{"unknown garbage", runErrIgnored},
		{"mcp NodeRunError", runErrMCP}, // mcp 优先于 NodeRunError
	}
	// 逐条断言
}

func TestAgentErrorToolName(t *testing.T) {
	// AgentErrorToolName == "agent_error"
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run 'TestClassifyRunError|TestAgentErrorToolName' -v`
Expected: 编译失败（未定义）

- [ ] **Step 3: 实现**

- 新增 `classifyRunError` 与常量；事件循环 `if event.Err != nil` 块改为 `switch classifyRunError(errStr)`，四个分支行为与现状一致，唯一改动是 `runErrRecoverable` 分支调用 `cb.WriteToolResult(errEventAgentName, "", AgentErrorToolName, errStr, true)` 替代 `cb.WriteError(...)`，之后 `continue`
- 函数头注释说明 `WriteError` 此后只在致命路径（连接失败后 return）调用

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/agent/... -race -v && go build -o dist/groot ./cmd/groot`
Expected: PASS

- [ ] **Step 5: 提交点**

```bash
git add internal/agent/engine.go internal/agent/engine_test.go
git commit -m "fix(engine): 可恢复的节点错误改以 tool_result 发送，error 事件仅用于终结"
```

---

### Task 4: Python 系统测试 SSEClient 适配

**Files:**
- Modify: `tests/python/conftest.py:173-310`
- Modify: `tests/python/test_sse_events.py:372-412`
- Check: `tests/python/test_supplementary.py`、`test_errors.py`、`test_performance.py`、`test_multimodal.py`、`test_api_endpoints.py`、`test_real_llm.py`、`test_attachments.py`、`test_sse_flow.py`

**Interfaces:**
- Produces: `SSEClient.flatten_chunk(raw: dict) -> dict`（静态方法）：输入 chunk 原始对象，输出扁平事件对象；`self.events[i]["data"]` 存放扁平对象，并额外保留 `self.events[i]["raw"]` 为原始 chunk

- [ ] **Step 1: 写摊平函数的单元断言（pytest，不依赖服务器）**

在 `tests/python/test_sse_events.py` 顶部新增不带 `server` fixture 的类：

```python
class TestSSEClientFlatten:
    def test_flatten_message(self):
        raw = {"id":"c","object":"chat.completion.chunk","created":1,"model":"m",
               "choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":None}]}
        assert SSEClient.flatten_chunk(raw) == {"role":"assistant","content":"hi"}

    def test_flatten_finish(self):
        raw = {..., "choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
        assert SSEClient.flatten_chunk(raw) == {"finish_reason":"stop"}
        assert SSEClient._infer_event_type(SSEClient.flatten_chunk(raw)) == "finish"

    def test_flatten_finish_with_agent(self):
        # delta {"agent_name":"coder"} + finish_reason "tool_calls" → {"agent_name":"coder","finish_reason":"tool_calls"}

    def test_flatten_error_passthrough(self):
        raw = {"error":{"message":"boom","type":"server_error"}}
        flat = SSEClient.flatten_chunk(raw)
        assert flat == {"event":"error","message":"boom","error":raw["error"]}
        assert SSEClient._infer_event_type(flat) == "error"

    def test_flatten_tool_result(self):
        # delta role=tool → 原样返回 delta，_infer_event_type == "tool_result"
```

- [ ] **Step 2: 运行确认失败**

Run: `cd tests/python && pytest test_sse_events.py::TestSSEClientFlatten -v`
Expected: FAIL（`flatten_chunk` 不存在）

- [ ] **Step 3: 实现 conftest 改动**

- `flatten_chunk(raw)`：有 `"error"` 键 → 返回 `{"event":"error","message":raw["error"].get("message",""),"error":raw["error"]}`；否则 `delta = dict(raw["choices"][0]["delta"])`，若 `finish_reason` 非 None 则 `delta["finish_reason"] = finish_reason`；无 `choices` 且无 `error` → 原样返回
- `_parse_events` 中 `data = json.loads(...)` 后 `flat = self.flatten_chunk(data)`，`events.append({"event": self._infer_event_type(flat), "data": flat, "raw": data})`
- `_infer_event_type` 不变
- 类 docstring 更新为引用新设计文档

`test_sse_events.py:372-412`（TC-SSE-016）的 error 分支断言改为 `data.get("event") == "error" and "message" in data`，并新增断言 `error_events[0]["raw"]` 不含 `choices`。

- [ ] **Step 4: 新增 chunk 外层结构的系统测试**

`test_sse_events.py` `TestSSEEventFields` 追加：

```python
def test_chunk_envelope_fields(self, server, api_headers):
    """TC-SSE-030: 每条非 error 事件的原始 chunk 含 OpenAI 外层字段"""
    # 发起一次简单对话，遍历 sse.events，对 e["raw"] 中含 choices 的：
    # raw["object"]=="chat.completion.chunk"; raw["id"]==response.headers["X-Chat-ID"]
    # isinstance(raw["created"], int); isinstance(raw["model"], str) and raw["model"]
    # raw["choices"][0]["index"]==0; 所有事件 created 相同

def test_error_is_terminal(self, server, api_headers):
    """TC-SSE-031: error 事件之后不再有含 choices 的事件"""
    # 用 X-Model-Name 指向不存在模型以外的方式触发致命错误困难时，
    # 退而断言：若流中存在 error 事件，其索引之后的 events 均为 error 类型
```

- [ ] **Step 5: 运行全部 Python 系统测试并扫残留**

Run: `cd tests/python && pytest -v`（需服务已启动，用户执行；执行者至少运行 `pytest test_sse_events.py::TestSSEClientFlatten -v` 与 `python -m py_compile *.py`）
Run: `grep -n 'data\["role"\]\|data.get("role")\|"finish_reason" in data\|reasoning_content' tests/python/test_*.py`，逐处确认读的是 `e["data"]`（已摊平）而非 `e["raw"]`
Expected: TestSSEClientFlatten PASS；grep 列出的每处都通过摊平后的 `data` 访问

- [ ] **Step 6: 更新 `tests/TEST_CASES.md`**

在 SSE 测试章节追加 TC-SSE-030、TC-SSE-031 两行，并备注 TC-SSE-016 断言调整。

- [ ] **Step 7: 提交点**

```bash
git add tests/python/conftest.py tests/python/test_sse_events.py tests/TEST_CASES.md
git commit -m "test(sse): 系统测试适配 OpenAI chunk 格式"
```

---

### Task 5: Web 前端解析层摊平

**Files:**
- Modify: `web/src/api/sse.ts:9-30,131-147`

**Interfaces:**
- Produces: `export function flattenChunk(raw: unknown): SseEvent | null`：chunk → 现有 `SseEvent`；`error` 对象 → `{event:'error', message}`；无法识别 → `null`
- `SseEvent` 接口不变；`streamChat` 签名不变

- [ ] **Step 1: 写 TypeScript 断言脚本（项目无前端测试框架，按 CLAUDE.md 不得引入新测试工具）**

在 `web/src/api/sse.ts` 文件底部不加测试。改为在 Step 3 实现后，用 `vue-tsc` 类型检查 + 手工在 Node 中验证纯函数：

```bash
cd web && npx tsx -e "
import { flattenChunk } from './src/api/sse';
const base = {id:'c',object:'chat.completion.chunk',created:1,model:'m'};
const eq=(a,b)=>JSON.stringify(a)===JSON.stringify(b)||(()=>{throw new Error(JSON.stringify(a))})();
eq(flattenChunk({...base,choices:[{index:0,delta:{role:'assistant',content:'hi'},finish_reason:null}]}),{role:'assistant',content:'hi'});
eq(flattenChunk({...base,choices:[{index:0,delta:{},finish_reason:'stop'}]}),{finish_reason:'stop'});
eq(flattenChunk({...base,choices:[{index:0,delta:{agent_name:'coder'},finish_reason:'tool_calls'}]}),{agent_name:'coder',finish_reason:'tool_calls'});
eq(flattenChunk({error:{message:'boom',type:'server_error'}}),{event:'error',message:'boom'});
eq(flattenChunk({...base,choices:[{index:0,delta:{role:'tool',tool_call_id:'',tool_name:'agent_error',content:'x',error:true},finish_reason:null}]}),{role:'tool',tool_call_id:'',tool_name:'agent_error',content:'x',error:true});
if(flattenChunk({foo:1})!==null) throw new Error('expect null');
console.log('ok');
"
```

若 `tsx` 不可用，用 `npx vite-node -e` 或先 `npx tsc --outDir /tmp/sse-check src/api/sse.ts` 再 `node` 执行等价脚本，不新增依赖到 `package.json`。该脚本不保存到仓库。

`sse.ts` 顶部 import 了 `./client` 与 `../i18n`，若导致脚本无法独立加载，把 `flattenChunk` 及其类型抽到 `web/src/api/sseChunk.ts`（无副作用导入），`sse.ts` 从中 re-export。

- [ ] **Step 2: 运行确认失败**

Run: 上述脚本
Expected: 报错 `flattenChunk is not exported`

- [ ] **Step 3: 实现**

- `flattenChunk(raw)`：`raw.error` 存在 → `{event:'error', message: raw.error.message ?? ''}`（`agent_name` 若存在也带上）；`raw.choices?.[0]` 存在 → `{...delta, ...(finish_reason != null ? {finish_reason} : {})}`；否则 `null`
- `handleChunk` 中 `JSON.parse(payload)` 后调 `flattenChunk`，为 `null` 时忽略，否则 `onEvent(ev)`
- 文件头注释改为指向新设计文档；`SseEvent` 注释说明它是摊平后的 `choices[0].delta` + `finish_reason`

- [ ] **Step 4: 验证**

Run: Step 1 脚本 → 输出 `ok`
Run: `cd web && npm run build`
Expected: 构建成功，无类型错误

- [ ] **Step 5: 提交点**

```bash
git add web/src/api/sse.ts web/src/api/sseChunk.ts
git commit -m "feat(web): SSE 解析层适配 OpenAI chunk 格式"
```

---

### Task 6: Web 前端业务层核对与 agent_error 展示

**Files:**
- Modify: `web/src/stores/chat.ts:315-395`
- Modify: `web/src/components/chat/TranscriptStep.vue`（仅当 agent_error 需要独立标签时）

**Interfaces:**
- Consumes: Task 5 的 `SseEvent`（结构未变）

- [ ] **Step 1: 核对 applyEvent 对摊平事件的处理**

逐条确认 `applyEvent` 分支：`ev.event==='error'` → 仍成立；`reasoning_content`/`tool_calls`/`role==='tool'`/`content` → 字段名未变。`finish_reason` 原先在 `delta` 内此处本就未使用，无需改。

- [ ] **Step 2: 处理 `agent_error` 工具结果**

`chat.ts:366` 分支 `if (ev.role === 'tool' && ev.tool_call_id)` 要求 `tool_call_id` 非空；`agent_error` 事件的 `tool_call_id` 为 `""`，会落到最后的 `if (ev.content)` 把错误文本当正文追加。改为 `if (ev.role === 'tool')`，并让 `msg.tools.find` 仅在 `ev.tool_call_id` 非空时匹配；找不到时走现有"新建 ToolInvocation"分支（`name: ev.tool_name`，`isError: ev.error`）。

`TranscriptStep.vue` 中为 `tool.name === 'agent_error'` 增加标签 `t('transcript.agentError')`，在 `web/src/i18n/messages/zh-cn.ts` 与 `en.ts` 的 `transcript` 节各加一条（中文「执行错误」，英文 "Execution error"），位置参考现有 `callSkill` 键。

- [ ] **Step 3: 验证**

Run: `cd web && npm run build`
Expected: 成功

手工验证（执行者在浏览器中）：发起一次会调用 skill 的对话，确认思考/skill 调用/工具结果/正文/耗时显示与改动前一致。

- [ ] **Step 4: 提交点**

```bash
git add web/src/stores/chat.ts web/src/components/chat/TranscriptStep.vue web/src/i18n/messages/zh-cn.ts web/src/i18n/messages/en.ts
git commit -m "feat(web): 工具结果分支兼容 agent_error 事件"
```

---

### Task 7: Python 示例客户端适配

**Files:**
- Modify: `examples/python/groot_client.py:88-120,157-173`
- Modify: `tests/examples/python/test_groot_client.py:70-95`

**Interfaces:**
- Produces: `GrootClient._flatten_chunk(raw: dict) -> dict`（静态方法），语义与 Task 4 的 `SSEClient.flatten_chunk` 相同
- `execute_chat(..., callback)` 的 `callback(event_type, data)` 中 `data` 为摊平对象，结构不变

- [ ] **Step 1: 写失败测试**

`tests/examples/python/test_groot_client.py`：
- `MockGrootHandler._handle_chat` 的 `events` 改为 chunk 格式（每条带 `id/object/created/model/choices`），去掉 `{"status":"success",...}` 那条（设计已无此事件），加一条 `tool_result` chunk
- 新增 `unittest` 用例：

```python
def test_flatten_chunk_message(self):
    # 同 Task 4 test_flatten_message
def test_flatten_chunk_finish(self):
def test_flatten_chunk_error(self):
    # → {"event":"error","message":"boom","error":{...}}
def test_execute_chat_event_types(self):
    # collect 回调收到的 event_type 序列包含 thinking, message, finish, tool_result；不包含 completed
    # 收到的 message data["content"] 以 "分析结果如下" 开头（data 已摊平）
```

- [ ] **Step 2: 运行确认失败**

Run: `cd tests/examples/python && python -m pytest test_groot_client.py -v -k 'flatten or event_types'`
Expected: FAIL

- [ ] **Step 3: 实现**

- 增加 `_flatten_chunk`
- 流循环中 `parsed = json.loads(data)` 后 `flat = self._flatten_chunk(parsed)`，`event_type = self._classify_event(flat)`，`callback(event_type, flat)`
- `_classify_event`：开头加 `if data.get("event") == "error": return "error"`；删除 `"status"`→`"completed"` 分支
- 删除结果处理中 `completed` 分支；`error` 分支 `result["error"] = flat.get("message")`
- 文件 docstring 与注释更新

- [ ] **Step 4: 运行确认通过**

Run: `cd tests/examples/python && python -m pytest test_groot_client.py -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交点**

```bash
git add examples/python/groot_client.py tests/examples/python/test_groot_client.py
git commit -m "feat(examples/python): 客户端适配 OpenAI chunk 格式"
```

---

### Task 8: Java 示例客户端适配

**Files:**
- Modify: `examples/java/src/main/java/com/groot/client/GrootClient.java:165-190,309-322`
- Modify: `tests/examples/java/com/groot/client/GrootClientTest.java:32-38,236-253`

**Interfaces:**
- Produces: `static JsonNode flattenChunk(JsonNode raw)`（包级可见，供测试）
- 回调 `callback.accept(eventType, dataJson)` 中 `dataJson` 改为摊平对象的 JSON 字符串

- [ ] **Step 1: 写失败测试**

- `SSE_EVENTS` 常量改为 chunk 格式，去掉 `status:success` 行，加一条 `tool_result` chunk
- `testClassifyEvent` 改为先 `flattenChunk` 再 `classifyEvent`，并新增 error 用例：`{"error":{"message":"boom"}}` → flatten 后 `classifyEvent` 返回 `"error"`；删除 `completed` 断言
- 新增 `testFlattenChunkFinish`：`choices[0].finish_reason="stop"`、`delta={}` → 摊平结果仅含 `finish_reason:"stop"`

- [ ] **Step 2: 运行确认失败**

Run: `cd examples/java && mvn -q test`
Expected: 编译失败（`flattenChunk` 未定义）

- [ ] **Step 3: 实现**

- `flattenChunk`：`raw.has("error")` → `ObjectNode{event:"error", message, error:raw.error}`；`raw.has("choices")` → 复制 `choices[0].delta`，`finish_reason` 非 null 时 put；否则返回 `raw`
- 流循环中 `parsed = flattenChunk(parsed)`，`callback.accept(eventType, parsed.toString())`
- `classifyEvent`：开头加 `event=="error"` 判定；删除 `status`→`completed` 分支
- 删除 `completed` 结果处理；`error` 分支保留

- [ ] **Step 4: 运行确认通过**

Run: `cd examples/java && mvn -q test`
Expected: BUILD SUCCESS

- [ ] **Step 5: 提交点**

```bash
git add examples/java/src/main/java/com/groot/client/GrootClient.java tests/examples/java/com/groot/client/GrootClientTest.java
git commit -m "feat(examples/java): 客户端适配 OpenAI chunk 格式"
```

---

### Task 9: README 用户手册更新

**Files:**
- Modify: `README.md:1337-1534`（"SSE 响应格式" 至 "SSE 前端实现伪代码" 结束，7.4 之前）

- [ ] **Step 1: 重写 SSE 章节**

按设计文档 1.3 节改写，章节结构：
- "SSE 响应格式"：说明为 OpenAI `chat.completion.chunk` 流式格式，标准 OpenAI 客户端可直接消费正文；外层字段表（1.3.1）
- "SSE 事件识别规则"：替换为 1.3.6 的规则
- "SSE 事件类型与处理方式"：表格增加"标准客户端可见"列，`finish` 行说明 `finish_reason` 位于 `choices[0]`，新增 `error` 行（终结性）
- "SSE 事件 JSON 结构"：每种事件给完整 chunk 示例；`error` 给 1.3.3 示例；说明 `agent_error` 工具结果
- "SSE 事件流示例"：三个场景全部改为 chunk 格式，`id/object/created/model` 完整出现在首条，后续用 `...` 省略
- "SSE 前端实现伪代码"：加入 `flattenChunk` 步骤
- 说明入参与响应头不变；无"相比旧版"措辞（迭代差异只放设计文档）

- [ ] **Step 2: 校验**

Run: `grep -n '"role":"assistant","content"' README.md | awk -F: '$1>1337 && $1<1600'`
Expected: 无扁平格式残留（所有示例均在 `choices[0].delta` 内）

- [ ] **Step 3: 提交点**

```bash
git add README.md
git commit -m "docs: README 更新 /chat SSE 响应格式为 OpenAI 兼容格式"
```

---

### Task 10: 端到端验证

**Files:** 无改动

- [ ] **Step 1: 全量 Go 测试与构建**

Run: `gofmt -l internal cmd pkg; go vet ./... && go test ./... -race && go build -o dist/groot ./cmd/groot`
Expected: gofmt 无输出；全部 PASS

- [ ] **Step 2: 前端构建**

Run: `cd web && npm run build`
Expected: 成功

- [ ] **Step 3: 示例测试**

Run: `cd tests/examples/python && python -m pytest -v; cd ../../../examples/java && mvn -q test`
Expected: 全部 PASS

- [ ] **Step 4: 真实流抽检（需本地启动服务）**

```bash
dist/groot serve &  # 或项目实际启动方式
curl -N -s -X POST http://localhost:<port>/chat -H 'Authorization: Bearer <key>' -H 'Content-Type: application/json' -d '{"instruction":"你好"}' | head -5
```
Expected: 首条为 `data: {"id":"...","object":"chat.completion.chunk","created":...,"model":"...","choices":[{"index":0,"delta":{...},"finish_reason":null}]}`，末尾 `data: [DONE]`

- [ ] **Step 5: 用标准 openai SDK 消费一次（可选，不入库）**

```python
# /tmp 下运行，不入库
from openai import OpenAI
c = OpenAI(base_url="http://localhost:<port>", api_key="<key>")
# 该 SDK 会 POST /chat/completions，与 groot 路径不同；此处用 httpx 手动 POST /chat 后把 resp 行喂给
# openai._streaming.Stream 解析，或直接确认 json.loads 后每行可被 openai.types.chat.ChatCompletionChunk.model_validate 接受
```
Expected: 每条非 error chunk 均能通过 `ChatCompletionChunk.model_validate`

- [ ] **Step 6: 通知用户运行 Python 系统测试**

Python 系统测试（`cd tests/python && pytest -v`）由用户执行。
