# SSE 流 token usage 输出 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/chat` 的 SSE 流在 `[DONE]` 前输出 OpenAI 规范形状的 usage chunk（`choices: []` + 顶层 `usage`），口径为本轮主 Agent 加全部子 Agent 的 token 合计，并修复子 Agent token 在生产路径上被丢弃的问题。

**Architecture:** `TokenAccumulators` 增加"轮次"概念（登记表 + 已归还合计），`CallAgentTool` 登记子 chatID 并在结束时 `PopChild` 归还，引擎事件循环通过登记表给子 Agent chunk 归属 token，`Engine.Run` 收尾 `PopRound` 得到全轮合计后经新回调 `WriteUsage` 写出；`SSEWriter` 所有 chunk 顶层带 `usage` 字段。README、三个示例客户端、Web 前端摊平逻辑、Python 系统测试同步更新。

**Tech Stack:** Go 1.x 标准测试框架；Python pytest（系统测试，用户自行运行）；TypeScript（Web 前端，`vue-tsc` 类型检查）。

**Spec:** `docs/superpowers/specs/2026-10-23-sse-usage-chunk-design.md`

## Global Constraints

- 项目规范：开发在 `master` 直接进行；不自动 commit，每个任务结束用 `git write-tree` + `git commit-tree` 记录悬空快照；进度账本 `.superpowers/sdd/sse-usage-chunk/progress.md`。
- Go 单测命令：`go test ./internal/agent/... -v`；编译：`go build -o dist/groot ./cmd`；`gofmt`。
- usage chunk 形状：`{"id","object":"chat.completion.chunk","created","model":<主模型名>,"choices":[],"usage":{"prompt_tokens","completion_tokens","total_tokens"}}`，位于 `[DONE]` 之前。
- 普通 chunk 顶层必须带 `"usage": null`（显式 null，不省略）。
- usage chunk 的 `choices` 必须序列化为 `[]`，不能是 `null`。
- 全轮 `total_tokens == 0` 时不输出 usage chunk；error 事件路径不输出 usage chunk。
- 不读取请求的 `stream_options`；不输出 `*_tokens_details`；不改数据库表与 ChatRecord 字段。
- 命名一致性：token 字段沿用 `PromptTokens` / `CompletionTokens` / `TotalTokens` 与 JSON `prompt_tokens` / `completion_tokens` / `total_tokens`。

## Review Focus

1. 子 Agent 在一轮内被调用两次（串行）：两次用量都要进全轮合计，第二次登记覆盖第一次后第一次的聚合仍通过归还合计计入 → Task 1 `TestTokenAccumulators_PopRound_SumsRetiredAndLive` 覆盖。
2. 取消路径下引擎先 `PopRound`，随后子 Agent 迟到 `PopChild` / `RegisterChild` / `Add`：不得重建条目残留 → Task 1 `TestTokenAccumulators_ClosedRoundDropsLateWrites` 覆盖（`Add` 对未开启轮次仍会 getOrCreate，这是 `Add` 现有语义，不在本次修改范围；靴带见任务说明）。
3. Solo 模式 `SetModel` 覆盖主模型名后，usage chunk 的 `model` 必须是覆盖后的值 → Task 2 `TestSSEWriter_WriteUsageModelFollowsSetModel` 覆盖。
4. 子 Agent 以子 Agent 名发出 usage-only chunk 时（`choices` 空、`ResponseMeta.Usage` 非空），引擎事件循环通过登记表归属到子 chatID → Task 3 `TestEngine_AccumulateUsage_ChildViaRegistry` 覆盖。
5. 示例客户端收到 usage chunk 不得抛异常，且能把 usage 暴露为事件 → Task 6 / Task 7 的示例测试覆盖。

---

### Task 1: TokenAccumulators 轮次 API

**Files:**
- Modify: `internal/agent/token_accumulators.go`
- Test: `internal/agent/token_accumulators_test.go`

**Interfaces:**
- Consumes: 现有 `TokenAccumulators` / `ChatAggregate` / `TokenUsage`。
- Produces:
  ```go
  func (a *TokenAccumulators) OpenRound(mainChatID string)
  func (a *TokenAccumulators) RegisterChild(mainChatID, agentName, childChatID string)
  func (a *TokenAccumulators) ChildFor(mainChatID, agentName string) string
  func (a *TokenAccumulators) PopChild(mainChatID, agentName, childChatID string) ChatAggregate
  func (a *TokenAccumulators) PopRound(mainChatID string) (main ChatAggregate, roundTokens TokenUsage)
  ```
  内部：`rounds map[string]*roundState`，`roundState{ children map[string]string; retired TokenUsage }`，与 `m` 共用 `a.mu`。

- [ ] **Step 1: 写失败测试**

```go
func TestTokenAccumulators_RoundBasic(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.Add("main", 10, 5)
	a.RegisterChild("main", "coder", "child1")
	if got := a.ChildFor("main", "coder"); got != "child1" { t.Fatalf("ChildFor = %q", got) }
	a.Add("child1", 100, 50)
	agg := a.PopChild("main", "coder", "child1")
	if agg.Tokens != (TokenUsage{100, 50, 150}) { t.Fatalf("PopChild tokens = %+v", agg.Tokens) }
	if got := a.ChildFor("main", "coder"); got != "" { t.Fatalf("ChildFor after pop = %q", got) }
	main, round := a.PopRound("main")
	if main.Tokens != (TokenUsage{10, 5, 15}) { t.Errorf("main = %+v", main.Tokens) }
	if round != (TokenUsage{110, 55, 165}) { t.Errorf("round = %+v", round) }
	if _, round2 := a.PopRound("main"); round2 != (TokenUsage{}) { t.Errorf("second PopRound should be zero") }
}

func TestTokenAccumulators_PopRound_SumsRetiredAndLive(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.Add("main", 1, 1)
	a.RegisterChild("main", "coder", "c1"); a.Add("c1", 10, 10)
	a.PopChild("main", "coder", "c1")                      // 第一次调用已归还
	a.RegisterChild("main", "coder", "c2"); a.Add("c2", 20, 20) // 第二次调用仍在飞
	a.RegisterChild("main", "db", "c3"); a.Add("c3", 5, 5)
	_, round := a.PopRound("main")
	if round != (TokenUsage{36, 36, 72}) { t.Fatalf("round = %+v", round) }
	// 未归还子聚合也已被删除
	if a.PopAndDelete("c2").Tokens != (TokenUsage{}) || a.PopAndDelete("c3").Tokens != (TokenUsage{}) {
		t.Fatal("live children should be removed by PopRound")
	}
}

func TestTokenAccumulators_ClosedRoundDropsLateWrites(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.RegisterChild("main", "coder", "c1"); a.Add("c1", 10, 10)
	a.PopRound("main")
	a.RegisterChild("main", "coder", "c9")     // 轮次已关闭：丢弃
	if a.ChildFor("main", "coder") != "" { t.Fatal("late RegisterChild must be dropped") }
	agg := a.PopChild("main", "coder", "c1")  // 已被 PopRound 删除：零值，不重建
	if agg.Tokens != (TokenUsage{}) { t.Fatalf("late PopChild = %+v", agg.Tokens) }
	a.mu.Lock(); nRounds, nM := len(a.rounds), len(a.m); a.mu.Unlock()
	if nRounds != 0 || nM != 0 { t.Fatalf("leak: rounds=%d m=%d", nRounds, nM) }
}

func TestTokenAccumulators_RegisterChildWithoutRoundIgnored(t *testing.T) {
	a := NewTokenAccumulators()
	a.RegisterChild("main", "coder", "c1")
	if a.ChildFor("main", "coder") != "" { t.Fatal("RegisterChild without OpenRound must be ignored") }
}

func TestTokenAccumulators_PopChildOutsideRoundStillDeletes(t *testing.T) {
	a := NewTokenAccumulators()
	a.Add("c1", 3, 4)
	agg := a.PopChild("nope", "x", "c1")
	if agg.Tokens != (TokenUsage{3, 4, 7}) { t.Fatalf("got %+v", agg.Tokens) }
	if a.PopAndDelete("c1").Tokens != (TokenUsage{}) { t.Fatal("c1 should be deleted") }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run 'TestTokenAccumulators_(RoundBasic|PopRound_SumsRetiredAndLive|ClosedRoundDropsLateWrites|RegisterChildWithoutRoundIgnored|PopChildOutsideRoundStillDeletes)' -v`
Expected: 编译失败，`OpenRound` 等未定义。

- [ ] **Step 3: 实现五个方法与 `roundState`**

要点：`OpenRound` 幂等；`RegisterChild` 轮次不存在直接 return；`PopChild` 无论轮次是否存在都 `delete(a.m, childChatID)`，轮次存在时把 token 加进 `retired` 并 `delete(children, agentName)`（仅当登记值等于 `childChatID`）；`PopRound` 取主聚合、遍历 `children` 取出并删除各子聚合求和、加 `retired`、`delete(a.rounds, mainChatID)`。`roundTokens.Total = Prompt + Completion`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/agent/ -run TestTokenAccumulators -v`
Expected: 全部 PASS（含旧用例）。

- [ ] **Step 5: 快照**

```bash
git add -A && TREE=$(git write-tree) && git commit-tree $TREE -p HEAD -m "snapshot: task1 token accumulators round api" && git reset -q
```
把输出的 commit hash 记入 progress.md。

---

### Task 2: SSEWriter 顶层 usage 字段与 WriteUsage

**Files:**
- Modify: `internal/agent/sse.go`
- Test: `internal/agent/sse_test.go`

**Interfaces:**
- Consumes: `TokenUsage`（Task 1 之前已存在）。
- Produces:
  ```go
  type chunkUsage struct { PromptTokens, CompletionTokens, TotalTokens int } // json: prompt_tokens / completion_tokens / total_tokens
  // chatChunk 增加 Usage *chunkUsage `json:"usage"`
  func (s *SSEWriter) WriteUsage(u TokenUsage) error
  ```

- [ ] **Step 1: 写失败测试**

```go
func TestSSEWriter_OrdinaryChunkHasNullUsage(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteMessage("", "hi"); err != nil { t.Fatal(err) }
	chunk := decodeChunk(t, buf.String())
	u, ok := chunk["usage"]
	if !ok || u != nil { t.Fatalf("usage should be present and null, got %v (present=%v)", u, ok) }
}

func TestSSEWriter_WriteUsage(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteUsage(TokenUsage{Prompt: 120, Completion: 45, Total: 165}); err != nil { t.Fatal(err) }
	raw := buf.String()
	if !strings.Contains(raw, `"choices":[]`) { t.Fatalf("choices must serialize as []: %s", raw) }
	chunk := decodeChunk(t, raw)
	if chunk["id"] != "chat" || chunk["object"] != "chat.completion.chunk" || chunk["created"] != float64(1760947200) || chunk["model"] != "qwen-max" {
		t.Errorf("envelope = %v", chunk)
	}
	u := chunk["usage"].(map[string]any)
	if u["prompt_tokens"] != float64(120) || u["completion_tokens"] != float64(45) || u["total_tokens"] != float64(165) {
		t.Errorf("usage = %v", u)
	}
	if len(u) != 3 { t.Errorf("usage must have exactly 3 keys: %v", u) }
}

func TestSSEWriter_WriteUsageModelFollowsSetModel(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 0)
	w.SetAgentModel("coder", "deepseek-r1")
	w.SetModel("solo-model")
	if err := w.WriteUsage(TokenUsage{1, 1, 2}); err != nil { t.Fatal(err) }
	if got := decodeChunk(t, buf.String())["model"]; got != "solo-model" { t.Errorf("model = %v, want solo-model", got) }
}

func TestSSEWriter_ErrorHasNoUsage(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "m", 0)
	if err := w.WriteError("", "boom"); err != nil { t.Fatal(err) }
	chunk := decodeChunk(t, buf.String())
	if _, ok := chunk["usage"]; ok { t.Errorf("error event must not carry usage: %v", chunk) }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run 'TestSSEWriter_(OrdinaryChunkHasNullUsage|WriteUsage|WriteUsageModelFollowsSetModel|ErrorHasNoUsage)' -v`
Expected: 编译失败（`WriteUsage` 未定义）或 `usage` 字段缺失。

- [ ] **Step 3: 实现**

`chatChunk` 加 `Usage *chunkUsage \`json:"usage"\``；`writeChunk` 不设 Usage（nil → null）；`WriteUsage` 构造 `Choices: []chunkChoice{}`、`Model: s.modelFor("")`、`Usage: &chunkUsage{...}` 后 `WriteData`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/agent/ -run TestSSEWriter -v`
Expected: 全部 PASS。

- [ ] **Step 5: 快照**（同 Task 1 命令，message `snapshot: task2 sse usage chunk`）

---

### Task 3: 引擎归属、轮次开启/收尾与 WriteUsage 回调

**Files:**
- Modify: `internal/agent/engine.go`（`ProgressCallback`、`Run` 入口与四处 `return nil, err`、两处 `WriteDone` 前、`resolveChatIDForAgent`、删除 `popMainAggregateTokens`）
- Modify: `internal/agent/executor.go:282-355`（`WithMainChatID` 后无需改；`ProgressCallback` 字面量加 `WriteUsage`）
- Test: `internal/agent/engine_test.go`

**Interfaces:**
- Consumes: Task 1 五个方法；Task 2 `SSEWriter.WriteUsage`。
- Produces: `ProgressCallback.WriteUsage func(usage TokenUsage) error`；`Engine.resolveChatIDForAgent` 新解析顺序；`Engine.finishRound(ctx) (main ChatAggregate, round TokenUsage)` 私有收尾辅助（替代 `popMainAggregateTokens`）。

- [ ] **Step 1: 写失败测试**

```go
func TestEngine_AccumulateUsage_ChildViaRegistry(t *testing.T) {
	acc := NewTokenAccumulators()
	acc.OpenRound("main_x")
	acc.RegisterChild("main_x", "db-agent", "child_reg")
	e := &Engine{agentName: MainAgentName, tokenAccumulators: acc}
	ctx := WithMainChatID(context.Background(), "main_x") // 不注入 childChatID：模拟生产路径
	e.accumulateUsage(ctx, "db-agent", &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 7, CompletionTokens: 3}})
	if got := acc.PopAndDelete("child_reg").Tokens; got != (TokenUsage{7, 3, 10}) { t.Fatalf("got %+v", got) }
}

func TestEngine_AccumulateUsage_RegistryBeatsCtx(t *testing.T) {
	acc := NewTokenAccumulators()
	acc.OpenRound("main_x")
	acc.RegisterChild("main_x", "db-agent", "child_reg")
	e := &Engine{agentName: MainAgentName, tokenAccumulators: acc}
	ctx := WithChildChatID(WithMainChatID(context.Background(), "main_x"), "child_ctx")
	e.accumulateUsage(ctx, "db-agent", &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 1}})
	if acc.PopAndDelete("child_reg").Tokens != (TokenUsage{1, 1, 2}) { t.Fatal("registry must win") }
	if acc.PopAndDelete("child_ctx").Tokens != (TokenUsage{}) { t.Fatal("ctx child must be untouched") }
}

func TestEngine_FinishRound_SumsAndCleans(t *testing.T) {
	acc := NewTokenAccumulators()
	e := &Engine{agentName: MainAgentName, tokenAccumulators: acc}
	ctx := WithMainChatID(context.Background(), "main_x")
	acc.OpenRound("main_x")
	acc.Add("main_x", 10, 5)
	acc.RegisterChild("main_x", "coder", "c1"); acc.Add("c1", 20, 10)
	main, round := e.finishRound(ctx)
	if main.Tokens != (TokenUsage{10, 5, 15}) { t.Errorf("main = %+v", main.Tokens) }
	if round != (TokenUsage{30, 15, 45}) { t.Errorf("round = %+v", round) }
}

func TestEngine_FinishRound_NilAccumulators(t *testing.T) {
	e := &Engine{agentName: MainAgentName}
	main, round := e.finishRound(WithMainChatID(context.Background(), "m"))
	if main.Tokens != (TokenUsage{}) || round != (TokenUsage{}) { t.Fatal("expected zero values") }
}
```

现有 `TestEngine_AccumulateUsage` 的子用例 `child event with full info accumulated to childChatID` 继续通过（无登记时回退 ctx）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run 'TestEngine_(AccumulateUsage_ChildViaRegistry|AccumulateUsage_RegistryBeatsCtx|FinishRound_SumsAndCleans|FinishRound_NilAccumulators)' -v`
Expected: 编译失败（`finishRound` 未定义）/ `ChildViaRegistry` 断言失败。

- [ ] **Step 3: 实现**

1. `ProgressCallback` 加 `WriteUsage func(usage TokenUsage) error`，注释说明仅在全轮 `Total > 0` 时调用，位于 `WriteDone` 之前。
2. `Run` 入口：取得 `mainID := mainChatIDFromContext(ctx)` 后，若 `e.tokenAccumulators != nil && mainID != ""` 调 `OpenRound(mainID)`，紧接现有 `SetModel`。放在 `e.models.GetByName` **之前**，保证四条错误返回路径都在轮次开启之后。
3. 四处 `return nil, fmt.Errorf(...)`（模型查找、ChatModel 创建、Agent 创建、`runErrFatalConn`）前调用 `e.finishRound(ctx)` 丢弃返回值。实现上用 `defer`-free 直写即可，或抽 `fail := func(err error) (*RunResult, error) { e.finishRound(ctx); return nil, err }`。
4. 取消路径与正常路径：`main, round := e.finishRound(ctx)`；`if round.Total > 0 && cb.WriteUsage != nil { cb.WriteUsage(round) }`；然后 `cb.WriteDone()`；`RunResult.Tokens = main.Tokens`。
5. `finishRound`：`tokenAccumulators == nil` 或 `mainID == ""` 返回零值，否则 `return e.tokenAccumulators.PopRound(mainID)`。删除 `popMainAggregateTokens`。
6. `resolveChatIDForAgent` 子 Agent 分支：`if id := e.tokenAccumulators.ChildFor(mainChatIDFromContext(ctx), eventAgentName); id != "" { return id }`，否则 `return childChatIDFromContext(ctx)`。
7. `executor.go` `ProgressCallback` 字面量加 `WriteUsage: func(u TokenUsage) error { return sse.WriteUsage(u) }`（不做 ctx 检查，与 `WriteFinish` 同类）。

- [ ] **Step 4: 运行确认通过并编译**

Run: `go test ./internal/agent/... -v && go build -o dist/groot ./cmd && gofmt -l internal/`
Expected: 全部 PASS；编译成功；gofmt 无输出。

- [ ] **Step 5: 快照**（message `snapshot: task3 engine round finish and usage callback`）

---

### Task 4: CallAgentTool 登记与归还

**Files:**
- Modify: `internal/agent/call_agent.go:134-185`
- Test: `internal/agent/call_agent_test.go`

**Interfaces:**
- Consumes: Task 1 `RegisterChild` / `PopChild`。
- Produces: 无新接口；行为：`InvokableRun` 生成 `childChatID` 后 `RegisterChild(t.parentChatID, input.AgentName, childChatID)`；结束时 `aggregate = PopChild(t.parentChatID, input.AgentName, childChatID)` 取代 `PopAndDelete(childChatID)`。

- [ ] **Step 1: 写失败测试**

```go
// accProbeTool 在运行中往累加器写入 token，并把登记的 childChatID 暴露出来。
type accProbeTool struct{ acc *TokenAccumulators; seen string }
func (p *accProbeTool) Info(_ context.Context) (*schema.ToolInfo, error) { return &schema.ToolInfo{Name: "p", Desc: "p"}, nil }
func (p *accProbeTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	p.seen = p.acc.ChildFor("chat_p", "coder")
	if p.seen != "" { p.acc.Add(p.seen, 30, 12) }
	return "done", nil
}

func TestCallAgentTool_RegistersChildAndRetiresTokens(t *testing.T) {
	acc := NewTokenAccumulators()
	acc.OpenRound("chat_p")
	probe := &accProbeTool{acc: acc}
	reg := NewRegistryForTest(2)
	entry := &SubAgentEntry{Name: "coder"}
	entry.SetToolForTest(probe)
	reg.SetEntryForTest("coder", entry)
	ct := newTestCallAgentTool(reg, 0, 0)
	ct.tokenAccumulators = acc
	if _, err := ct.InvokableRun(context.Background(), `{"agent_name":"coder","task":"t"}`); err != nil { t.Fatal(err) }
	if probe.seen == "" { t.Fatal("child chatID must be registered before sub-agent runs") }
	if acc.ChildFor("chat_p", "coder") != "" { t.Fatal("registration must be removed after PopChild") }
	_, round := acc.PopRound("chat_p")
	if round != (TokenUsage{30, 12, 42}) { t.Fatalf("round = %+v, child tokens must be retired into round", round) }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run TestCallAgentTool_RegistersChildAndRetiresTokens -v`
Expected: FAIL，`probe.seen == ""` 或 round 为零。

- [ ] **Step 3: 实现**

在 `execCtx = context.WithValue(..., childChatIDKey{}, childChatID)` 之后加 `if t.tokenAccumulators != nil { t.tokenAccumulators.RegisterChild(t.parentChatID, input.AgentName, childChatID) }`；结尾 `aggregate = t.tokenAccumulators.PopChild(t.parentChatID, input.AgentName, childChatID)`。更新该处注释（"累加器无条件 pop"保留，补充"同时归还到本轮合计"）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/agent/... -v`
Expected: 全部 PASS。

- [ ] **Step 5: 快照**（message `snapshot: task4 call_agent register and retire`）

---

### Task 5: README 与 TEST_CASES 文档

**Files:**
- Modify: `README.md`（约 1376-1660 行的 `/chat` SSE 章节）
- Modify: `tests/TEST_CASES.md`（2.16 SSE 流测试节）

- [ ] **Step 1: README 外层字段表**

`choices` 行改为：`普通事件恒为单元素数组，choices[0].index 恒为 0；usage 事件为空数组 []`。新增一行：`| usage | object \| null | 普通事件为 null；usage 事件为 {prompt_tokens, completion_tokens, total_tokens}，为本次对话全部模型调用（主 Agent + 子 Agent）的累计 |`。

- [ ] **Step 2: README 事件识别规则**

在 `存在顶层 error` 之后、`d = choices[0].delta` 之前加一行：`choices 为空数组且 usage 非 null       → usage 事件（位于 [DONE] 之前）`。

- [ ] **Step 3: README 事件类型表**

在 `error` 行之前插入：`| usage | —（choices 为 []，顶层 usage 对象） | — | 可见（OpenAI include_usage 标准形状） | 本次对话 token 用量；一次对话最多一条，紧邻 [DONE] 之前；对话无任何模型调用时不出现 |`。

- [ ] **Step 4: README 示例流**

在首个完整示例的 `data: [DONE]` 之前加 usage chunk 示例行（与 Global Constraints 形状一致，数值 120/45/165），并在 JS 客户端伪代码的 `[DONE]` 判断后加 `if (Array.isArray(c.choices) && c.choices.length === 0 && c.usage) { /* usage 事件 */ return; }`。

- [ ] **Step 5: TEST_CASES.md**

2.16 节表格：`TestSSEClientFlatten` 行末追加 `/ usage chunk 摊平为 {"event":"usage","usage":{...}}（TC-SSE-FLAT-009）`；`TestSSEEventOrder` 行补 `TC-SSE-001/010 末尾事件为 usage，其前一个为 finish`；新增一行 `| TestSSEUsageEvent | test_sse_events.py | TC-SSE-040 usage 事件：choices 为 []、usage 三字段均为非负 int 且 total == prompt + completion、位于 [DONE] 前且在最后一个 finish 之后 |`。

- [ ] **Step 6: 快照**（message `snapshot: task5 docs`）

---

### Task 6: Python 示例客户端与系统测试框架

**Files:**
- Modify: `examples/python/groot_client.py:209-252`
- Modify: `tests/python/conftest.py:230-276`、`tests/python/conftest.py:286-295`（`valid_events` 加 `"usage"`）
- Modify: `tests/python/test_sse_events.py:79-83`（TC-SSE-FLAT-008 保留）、`:126`、`:164`
- Test: `tests/examples/python/test_groot_client.py`、`tests/python/test_sse_events.py`

**Interfaces:**
- Produces: 两处 `flatten_chunk` 在 `choices` 为空且 `raw.get("usage")` 为 dict 时返回 `{"event": "usage", "usage": raw["usage"]}`；`_classify_event` / `_infer_event_type` 在 `data.get("event") == "usage"` 时返回 `"usage"`。

- [ ] **Step 1: 写失败测试（示例客户端）**

在 `tests/examples/python/test_groot_client.py` 中现有 `test_flatten_chunk_message` 所在的 unittest 测试类里追加（沿用 `self.assertEqual` 风格）：

```python
def test_flatten_usage_chunk(self):
    raw = {"id": "c", "object": "chat.completion.chunk", "created": 1, "model": "m",
           "choices": [], "usage": {"prompt_tokens": 120, "completion_tokens": 45, "total_tokens": 165}}
    flat = GrootClient._flatten_chunk(raw)
    self.assertEqual(flat, {"event": "usage", "usage": raw["usage"]})
    self.assertEqual(GrootClient._classify_event(flat), "usage")

def test_flatten_empty_choices_without_usage_passthrough(self):
    raw = {"choices": [], "usage": None}
    self.assertIs(GrootClient._flatten_chunk(raw), raw)
```

在 `tests/python/test_sse_events.py` 的 `TestSSEClientFlatten` 追加：

```python
def test_flatten_usage_chunk(self):
    """TC-SSE-FLAT-009: choices 为空且 usage 非空 → {"event":"usage","usage":{...}}"""
    usage = {"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3}
    raw = {"id": "c", "object": "chat.completion.chunk", "choices": [], "usage": usage}
    flat = SSEClient.flatten_chunk(raw)
    assert flat == {"event": "usage", "usage": usage}
    assert SSEClient._infer_event_type(flat) == "usage"
```

并把 `test_parse_events_keeps_raw` 的假响应在 finish 后插入一条 usage chunk，期望 `get_event_order() == ["message", "finish", "usage"]`。

- [ ] **Step 2: 运行确认失败**

Run: `cd tests/examples/python && pytest test_groot_client.py -k flatten -v; cd ../../python && pytest test_sse_events.py::TestSSEClientFlatten -v`
Expected: 新用例 FAIL（返回原样 dict 而非 usage 事件 / 分类为 message）。

- [ ] **Step 3: 实现两处 flatten 与分类**

- [ ] **Step 4: 调整系统测试末尾断言**

`test_sse_events.py:126`：`assert order[-1] == "usage" and order[-2] == "finish"`；`:164`：`assert events[-1]["event"] == "usage" and events[-2]["event"] == "finish"`。新增 `TestSSEUsageEvent::test_usage_event_fields`（TC-SSE-040）：取 `get_events_by_type("usage")` 恰一条，`raw["choices"] == []`，三字段为 `int` 且 `>= 0`，`total == prompt + completion`，其索引大于最后一个 `finish` 的索引。这些依赖服务器的用例由用户自行运行。

- [ ] **Step 5: 运行离线用例确认通过**

Run: `cd tests/examples/python && pytest test_groot_client.py -v; cd ../../python && pytest test_sse_events.py::TestSSEClientFlatten -v`
Expected: PASS。

- [ ] **Step 6: 快照**（message `snapshot: task6 python clients`）

---

### Task 7: Java 示例客户端与 Web 前端摊平

**Files:**
- Modify: `examples/java/src/main/java/com/groot/client/GrootClient.java:320-370`（`flattenChunk` 与事件分类）
- Test: `tests/examples/java/com/groot/client/GrootClientTest.java`
- Modify: `web/src/api/sseChunk.ts`（`SseEvent` 加 `usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number }`；`flattenChunk` 在 `choices` 为空数组且 `isRecord(raw.usage)` 时返回 `{ event: 'usage', usage }`）
- Modify: `web/src/api/sse.ts:126` 注释（空 choices 不再一概忽略）

**Interfaces:**
- Produces: Java `flattenChunk` 返回 `{"event":"usage","usage":{...}}` ObjectNode，`classifyEvent` 返回 `"usage"`；TS `flattenChunk` 同形。`web/src/stores/chat.ts` 的 `applyEvent` 对未知事件已经安全落空，无需修改。

- [ ] **Step 1: 写失败 Java 测试**

```java
@Test
void flattenUsageChunk() throws Exception {
    JsonNode raw = MAPPER.readTree("{\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[],"
        + "\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":45,\"total_tokens\":165}}");
    JsonNode flat = GrootClient.flattenChunk(raw);
    assertEquals("usage", flat.get("event").asText());
    assertEquals(165, flat.get("usage").get("total_tokens").asInt());
    assertEquals("usage", GrootClient.classifyEvent(flat));
}
```
（`flattenChunk` / `classifyEvent` 均为包级 static 方法，测试类与其同包，现有测试已这样直接调用。）

- [ ] **Step 2: 运行确认失败**

Run: `cd examples/java && mvn -q test -Dtest=GrootClientTest`
Expected: `flattenUsageChunk` FAIL。

- [ ] **Step 3: 实现 Java 与 TS**

- [ ] **Step 4: 验证**

Run: `cd examples/java && mvn -q test -Dtest=GrootClientTest`，Expected: PASS。
Run: `cd web && npx vue-tsc --noEmit -p tsconfig.json`，Expected: 无类型错误（依赖未安装时先 `npm ci`）。

- [ ] **Step 5: 快照**（message `snapshot: task7 java and web clients`）

---

### Task 8: 整体验证

- [ ] `go test ./internal/... -v` 全部 PASS。
- [ ] `go build -o dist/groot ./cmd` 成功；`gofmt -l internal/ cmd/ pkg/` 无输出。
- [ ] `go vet ./internal/agent/...` 无告警。
- [ ] 人工对照 spec 1.2 能力清单 1～8 逐条确认有对应实现与测试。
- [ ] 删除 `.superpowers/sdd/sse-usage-chunk/` 由整体评审通过后执行。
