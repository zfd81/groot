package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/memorydb"
	"github.com/zfd81/groot/internal/repo/modeldb"
	"github.com/zfd81/groot/internal/repo/settingdb"
	"github.com/zfd81/groot/internal/setting"
)

// newModelServiceForTest 构造基于临时 sqlite 库的 ModelService，并预置一个默认模型，
// 使 handler 的模型解析步骤通过，从而能覆盖其后的校验与执行路径。
func newModelServiceForTest(t *testing.T) *llm.ModelService {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = sqlxDB.Close() })
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if err := models.Create(context.Background(), &repo.Model{
		Name:        "test-model",
		BaseURL:     "https://example.com/v1",
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Temperature: 0.7,
		TopP:        1.0,
		Enabled:     true,
	}); err != nil {
		t.Fatalf("models.Create failed: %v", err)
	}
	return models
}

// newBrokenModelServiceForTest 构造一个底层库已关闭的 ModelService：
// 任何 Resolve 都会因数据库故障返回非哨兵错误，用于覆盖 500 internal_error 分支。
func newBrokenModelServiceForTest(t *testing.T) *llm.ModelService {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open failed: %v", err)
	}
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if err := sqlxDB.Close(); err != nil {
		t.Fatalf("sqlxDB.Close failed: %v", err)
	}
	return models
}

// decodeErrorEnvelope 解析 OpenAI 错误信封响应体，失败即终止测试。
func decodeErrorEnvelope(t *testing.T, rc *app.RequestContext) errbody.Envelope {
	t.Helper()
	var envl errbody.Envelope
	if err := json.Unmarshal(rc.Response.Body(), &envl); err != nil {
		t.Fatalf("响应体不是合法错误信封: %v, body=%s", err, rc.Response.Body())
	}
	return envl
}

// fakeChatExecutor 是 taskExecutor 的测试替身：按预设脚本向事件写出器推送事件，
// 并记录 handler 传入的 sessionID 与 task，用于断言 handler 的组装结果。
type fakeChatExecutor struct {
	mu sync.Mutex

	calls     int
	sessionID string
	task      *agent.Task
	messages  []string
	finish    string
	usage     *agent.TokenUsage
	errMsg    string
	status    agent.TaskStatus
}

func (f *fakeChatExecutor) Execute(_ context.Context, sessionID string, task *agent.Task, events agent.EventWriter) {
	f.mu.Lock()
	f.calls++
	f.sessionID = sessionID
	f.task = task
	messages, finish, usage, errMsg, status := f.messages, f.finish, f.usage, f.errMsg, f.status
	f.mu.Unlock()

	for _, s := range messages {
		_ = events.WriteMessage("", s)
	}
	if finish != "" {
		_ = events.WriteFinish("", finish)
	}
	if usage != nil {
		_ = events.WriteUsage(*usage)
	}
	if errMsg != "" {
		_ = events.WriteError("", errMsg)
	}
	if status != "" {
		task.Status = status
	}
	_ = events.WriteDone()
}

func (f *fakeChatExecutor) snapshot() (int, string, *agent.Task) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.sessionID, f.task
}

// testChatEnv 汇总一个可执行的 handler 环境：真实 memory / runtime / settings 加替身执行器。
type testChatEnv struct {
	h    *ChatHandler
	mem  *memory.Manager
	rt   *agent.RuntimeState
	exec *fakeChatExecutor
}

// newChatEnv 构造完整 ChatHandler 环境。closeMemDB 为 true 时关闭 memory 底层库，
// 使 CreateSession 必然失败，用于覆盖注册成功后的失败清理分支。
func newChatEnv(t *testing.T, reg *agent.SubAgentRegistry, closeMemDB bool) *testChatEnv {
	t.Helper()

	memDB, memDialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open(memory) failed: %v", err)
	}
	mem := memory.NewManager(logger.NewNop(), memorydb.New(memDB, memDialect))
	if closeMemDB {
		if err := memDB.Close(); err != nil {
			t.Fatalf("memDB.Close failed: %v", err)
		}
	} else {
		t.Cleanup(func() { _ = memDB.Close() })
	}

	setDB, setDialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open(settings) failed: %v", err)
	}
	t.Cleanup(func() { _ = setDB.Close() })

	rt := agent.NewRuntimeState()
	exec := &fakeChatExecutor{status: agent.StatusCompleted}
	h := &ChatHandler{
		memory:           mem,
		runtimeState:     rt,
		agentExecutor:    exec,
		subAgentRegistry: reg,
		models:           newModelServiceForTest(t),
		settings:         setting.New(runtimeBoot(), settingdb.New(setDB, setDialect)),
		log:              logger.NewNop(),
	}
	return &testChatEnv{h: h, mem: mem, rt: rt, exec: exec}
}

// newChatPostContext 构造一个 POST application/json 请求上下文，body 为给定 JSON；
// 不设置任何身份键，由各测试按需 rc.Set("caller"/"tenant_id"/"user_id")。
func newChatPostContext(body string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	rc.Request.SetBody([]byte(body))
	return rc
}

// simpleChatBody 组装一条最小合法请求体的 JSON。
func simpleChatBody(extra string) string {
	base := `{"messages":[{"role":"user","content":"你好"}]`
	if extra != "" {
		base += "," + extra
	}
	return base + "}"
}

// drainSSE 读取并关闭 SSE 流，返回原始字节。
func drainSSE(t *testing.T, rc *app.RequestContext) string {
	t.Helper()
	data, err := io.ReadAll(rc.Response.BodyStream())
	if err != nil {
		t.Fatalf("读取 SSE 流失败: %v", err)
	}
	return string(data)
}

// TestChatHandler_NonStreamCompletionShape 验证 stream=false 时返回完整的
// chat.completion 结构：object、id、choices[0].message.content、usage 齐备，
// 且响应体不是 chunk 形式的 SSE。
func TestChatHandler_NonStreamCompletionShape(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.messages = []string{"你好呀"}
	env.exec.finish = "stop"
	env.exec.usage = &agent.TokenUsage{Prompt: 11, Completion: 7, Total: 18}

	rc := newChatPostContext(simpleChatBody(""))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("期望 200，实际 %d body=%s", got, rc.Response.Body())
	}
	body := string(rc.Response.Body())
	if strings.Contains(body, "data: ") {
		t.Fatalf("非流式响应不应包含 SSE 数据行: %s", body)
	}

	var resp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Session string `json:"session_id"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Error            *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v, body=%s", err, body)
	}

	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, 期望 chat.completion", resp.Object)
	}
	if resp.ID == "" {
		t.Errorf("id 为空")
	}
	if resp.Model != "test-model" {
		t.Errorf("model = %q, 期望 test-model", resp.Model)
	}
	if resp.Session == "" {
		t.Errorf("session_id 为空")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices 长度 = %d, 期望 1", len(resp.Choices))
	}
	c := resp.Choices[0]
	if c.Index != 0 {
		t.Errorf("choices[0].index = %d, 期望 0", c.Index)
	}
	if c.Message.Role != "assistant" {
		t.Errorf("message.role = %q, 期望 assistant", c.Message.Role)
	}
	if c.Message.Content != "你好呀" {
		t.Errorf("message.content = %q, 期望 你好呀", c.Message.Content)
	}
	if c.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, 期望 stop", c.FinishReason)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 18 {
		t.Errorf("usage = %+v, 期望 11/7/18", resp.Usage)
	}

	// 执行器的入参也要正确：task 应携带模型名、轮次与会话标识
	calls, sid, task := env.exec.snapshot()
	if calls != 1 {
		t.Fatalf("执行器调用次数 = %d, 期望 1", calls)
	}
	if sid != resp.Session {
		t.Errorf("执行器 sessionID = %q, 响应 session_id = %q", sid, resp.Session)
	}
	if task == nil || task.ModelName != "test-model" || task.Round != 1 {
		t.Errorf("task 组装不符合预期: %+v", task)
	}
}

// TestChatHandler_StreamChunksHaveSessionID 验证流式响应的每个 chunk 都带 session_id，
// 且以 [DONE] 结尾；同时确认不再通过响应头下发会话标识。
func TestChatHandler_StreamChunksHaveSessionID(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.messages = []string{"流式", "回复"}
	env.exec.finish = "stop"

	rc := newChatPostContext(simpleChatBody(`"stream":true`))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("期望 200，实际 %d", got)
	}
	if got := rc.Response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, 期望 text/event-stream", got)
	}
	if got := string(rc.Response.Header.Get("X-Session-ID")); got != "" {
		t.Errorf("不应再设置 X-Session-ID 响应头，实际 %q", got)
	}
	if got := string(rc.Response.Header.Get("X-Chat-ID")); got != "" {
		t.Errorf("不应再设置 X-Chat-ID 响应头，实际 %q", got)
	}

	stream := drainSSE(t, rc)
	lines := strings.Split(strings.TrimSpace(stream), "\n\n")
	var chunkCount int
	for _, line := range lines {
		switch {
		case line == "data: [DONE]":
			continue
		case strings.HasPrefix(line, "data: "):
			chunkCount++
			if !strings.Contains(line, `"session_id"`) {
				t.Errorf("chunk 缺少 session_id: %s", line)
			}
		}
	}
	if chunkCount < 2 {
		t.Fatalf("期望至少 2 个数据 chunk，实际 %d，流内容: %s", chunkCount, stream)
	}
	if !strings.Contains(stream, "data: [DONE]") {
		t.Errorf("流缺少 [DONE] 终止标记: %s", stream)
	}
}

// TestChatHandler_StreamErrorAfterHeader 验证执行中报错时，响应状态仍为 200，
// 但流内出现 OpenAI 信封的 error 事件与 [DONE]，客户端据此识别失败。
func TestChatHandler_StreamErrorAfterHeader(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.errMsg = "模型调用失败"
	env.exec.status = agent.StatusFailed

	rc := newChatPostContext(simpleChatBody(`"stream":true`))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("流已开始后期望 200，实际 %d", got)
	}

	stream := drainSSE(t, rc)
	if !strings.Contains(stream, `data: {"error":{"message":"模型调用失败"`) {
		t.Fatalf("流中缺少 error 事件信封: %s", stream)
	}
	if !strings.Contains(stream, "data: [DONE]") {
		t.Fatalf("error 事件之后缺少 [DONE]: %s", stream)
	}
}

// TestChatHandler_SessionNotOwnedCreatesNew 验证 session_id 指向他人会话时静默新建，
// 返回的 session_id 与传入值不同，且不会污染他人会话。
func TestChatHandler_SessionNotOwnedCreatesNew(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.messages = []string{"ok"}

	const foreignSID = "sess_foreign_001"
	if err := env.mem.CreateSession(foreignSID, "other-tenant", "other-user", "别人的会话"); err != nil {
		t.Fatalf("创建他人会话失败: %v", err)
	}

	rc := newChatPostContext(simpleChatBody(`"session_id":"` + foreignSID + `"`))
	setIdentity(rc, "svc", "crm", "u1")

	env.h.Handle(context.Background(), rc)
	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("期望 200，实际 %d body=%s", got, rc.Response.Body())
	}

	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v", err)
	}
	if resp.SessionID == "" || resp.SessionID == foreignSID {
		t.Fatalf("期望新建会话（session_id 不为传入值），实际 %q", resp.SessionID)
	}
	if env.rt.IsRunning(foreignSID) {
		t.Errorf("他人会话不应被注册为活跃状态")
	}
}

// TestChatHandler_ImageOnlyMessage 验证末条 user 只有图片、文本为空时仍能建会话并执行。
func TestChatHandler_ImageOnlyMessage(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.messages = []string{"这是一只猫"}

	body := `{"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}]}`
	rc := newChatPostContext(body)
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)
	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("期望 200，实际 %d body=%s", got, rc.Response.Body())
	}

	var resp struct {
		SessionID string `json:"session_id"`
		Choices   []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "这是一只猫" {
		t.Fatalf("内容不符合预期: %s", rc.Response.Body())
	}

	_, sid, task := env.exec.snapshot()
	if sid != resp.SessionID {
		t.Errorf("执行器 sessionID = %q, 响应 = %q", sid, resp.SessionID)
	}
	if task == nil || task.Instruction != "" {
		t.Errorf("图片消息的 instruction 应为空串，实际 %+v", task)
	}
	if task == nil || len(task.MultiModalContents) != 1 || task.MultiModalContents[0].Type != "image" {
		t.Fatalf("附件未传入 task: %+v", task)
	}

	// 会话必须真实落库，标题回落到「新会话」
	sessions, _, err := env.mem.ListSessions(nil, 10, 0)
	if err != nil {
		t.Fatalf("ListSessions 失败: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != resp.SessionID {
		t.Fatalf("会话未创建: %+v", sessions)
	}
	if sessions[0].Title != "新会话" {
		t.Errorf("标题 = %q, 期望 新会话", sessions[0].Title)
	}
}

// TestChatHandler_IncludeUsageOff 验证未开启 stream_options.include_usage 时，
// 即使执行器产出了 token 用量，流里也不出现 usage chunk。
func TestChatHandler_IncludeUsageOff(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.messages = []string{"hi"}
	env.exec.finish = "stop"
	env.exec.usage = &agent.TokenUsage{Prompt: 3, Completion: 2, Total: 5}

	rc := newChatPostContext(simpleChatBody(`"stream":true`))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)
	stream := drainSSE(t, rc)

	if strings.Contains(stream, `"usage":{"prompt_tokens"`) {
		t.Fatalf("未开启 include_usage 时不应下发 usage chunk: %s", stream)
	}
}

// TestChatHandler_NonStreamCancelledFinishesWithError 验证非流式执行被取消时，
// 响应仍为 200，但 finish_reason 为 error（取消不产生 error 事件，只能靠结束原因表达）。
func TestChatHandler_NonStreamCancelledFinishesWithError(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.status = agent.StatusCancelled

	rc := newChatPostContext(simpleChatBody(""))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("期望 200，实际 %d body=%s", got, rc.Response.Body())
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].FinishReason != "error" {
		t.Fatalf("取消时 finish_reason 应为 error，实际 %s", rc.Response.Body())
	}
	if got := env.rt.RunningCount(); got != 0 {
		t.Errorf("执行结束后 RunningCount 应为 0，实际 %d", got)
	}
}

// TestChatHandler_BusySession 验证会话已有对话在跑时返回 409 chat_limit_exceeded，
// 且响应体不含 session_id。
func TestChatHandler_BusySession(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)

	const sid = "sess_busy_001"
	if err := env.mem.CreateSession(sid, "", "", "忙碌会话"); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if _, err := env.rt.Register(context.Background(), sid, "chat_running"); err != nil {
		t.Fatalf("注册活跃对话失败: %v", err)
	}
	defer env.rt.Delete(sid)

	rc := newChatPostContext(simpleChatBody(`"session_id":"` + sid + `"`))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 409 {
		t.Fatalf("期望 409，实际 %d body=%s", got, rc.Response.Body())
	}
	body := string(rc.Response.Body())
	if !strings.Contains(body, "chat_limit_exceeded") {
		t.Fatalf("期望 code=chat_limit_exceeded，实际 %s", body)
	}
	if strings.Contains(body, "session_id") {
		t.Errorf("409 响应体不应包含 session_id 字段: %s", body)
	}
	if calls, _, _ := env.exec.snapshot(); calls != 0 {
		t.Errorf("忙碌会话不应执行任务，实际调用 %d 次", calls)
	}
}

// TestChatHandler_UnknownAgent 验证 agent_name 指向未注册子 Agent 时 400 unknown_agent。
func TestChatHandler_UnknownAgent(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)

	rc := newChatPostContext(simpleChatBody(`"agent_name":"nope"`))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 {
		t.Fatalf("期望 400，实际 %d body=%s", got, rc.Response.Body())
	}
	body := string(rc.Response.Body())
	if !strings.Contains(body, "unknown_agent") || !strings.Contains(body, "Unknown agent") {
		t.Fatalf("期望 unknown_agent 信封，实际 %s", body)
	}
}

// TestChatHandler_NilRegistryReturns400 验证 subAgentRegistry == nil 时同样 400 unknown_agent。
func TestChatHandler_NilRegistryReturns400(t *testing.T) {
	env := newChatEnv(t, nil, false)

	rc := newChatPostContext(simpleChatBody(`"agent_name":"any"`))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 || !strings.Contains(string(rc.Response.Body()), "unknown_agent") {
		t.Fatalf("期望 400 unknown_agent，实际 %d body=%s", got, rc.Response.Body())
	}
}

// TestChatHandler_MainAgentNameTreatedAsEmpty 验证 agent_name=groot 与不传等价：
// 校验通过并正常执行，不被判为 unknown_agent。
func TestChatHandler_MainAgentNameTreatedAsEmpty(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.messages = []string{"ok"}

	rc := newChatPostContext(simpleChatBody(`"agent_name":"` + agent.MainAgentName + `"`))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("期望 200，实际 %d body=%s", got, rc.Response.Body())
	}
	if strings.Contains(string(rc.Response.Body()), "unknown_agent") {
		t.Fatalf("agent_name=groot 不应触发 unknown_agent: %s", rc.Response.Body())
	}
	_, _, task := env.exec.snapshot()
	if task == nil || task.AgentName != "" {
		t.Fatalf("agent_name=groot 应归一为空串，实际 %+v", task)
	}
}

// TestChatHandler_MissingUserIDReturns400 验证 API Key 调用者有租户但请求体与请求头都缺 user 时
// 返回 OpenAI 错误信封：400 invalid_request_error，param 为 user。
func TestChatHandler_MissingUserIDReturns400(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)

	rc := newChatPostContext(simpleChatBody(""))
	rc.Set("caller", "svc")
	rc.Set("tenant_id", "crm")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 {
		t.Fatalf("期望 400，实际 %d body=%s", got, rc.Response.Body())
	}
	envl := decodeErrorEnvelope(t, rc)
	if envl.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, 期望 invalid_request_error", envl.Error.Type)
	}
	if envl.Error.Param == nil || *envl.Error.Param != "user" {
		t.Errorf("param = %v, 期望 user", envl.Error.Param)
	}
}

// TestChatHandler_MissingUserWithoutHeaderReturns400 验证 API Key 调用方请求体与请求头都没有 user 时，
// 错误文案明确指向缺失的 user 字段。
func TestChatHandler_MissingUserWithoutHeaderReturns400(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)

	rc := newChatPostContext(simpleChatBody(""))
	setIdentity(rc, "svc", "crm", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 {
		t.Fatalf("期望 400，实际 %d body=%s", got, rc.Response.Body())
	}
	envl := decodeErrorEnvelope(t, rc)
	if !strings.Contains(envl.Error.Message, "缺少或非法的 user") {
		t.Fatalf("message = %q, 期望包含「缺少或非法的 user」", envl.Error.Message)
	}
}

// TestChatHandler_EmptyTenantReturns403 验证 API Key 未绑定租户时返回 OpenAI 权限错误信封：
// 403 permission_error，code 为 tenant_not_bound。
func TestChatHandler_EmptyTenantReturns403(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)

	rc := newChatPostContext(simpleChatBody(""))
	rc.Set("caller", "svc")
	rc.Set("tenant_id", "")
	rc.Set("user_id", "u1")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 403 {
		t.Fatalf("期望 403，实际 %d body=%s", got, rc.Response.Body())
	}
	envl := decodeErrorEnvelope(t, rc)
	if envl.Error.Type != "permission_error" {
		t.Errorf("type = %q, 期望 permission_error", envl.Error.Type)
	}
	if envl.Error.Code == nil || *envl.Error.Code != "tenant_not_bound" {
		t.Errorf("code = %v, 期望 tenant_not_bound", envl.Error.Code)
	}
}

// TestChatHandler_APIKeyBodyUserSetsSessionOwner 验证 API Key 调用方不带 user 请求头、
// 只在请求体里传 user 时仍可正常对话，且新建会话的归属 user 取自请求体。
func TestChatHandler_APIKeyBodyUserSetsSessionOwner(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.messages = []string{"ok"}

	rc := newChatPostContext(simpleChatBody(`"user":"body_user"`))
	// 只注入租户，不注入 user_id：模拟 OpenAI 客户端只在请求体传 user 的标准做法
	setIdentity(rc, "svc", "crm", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("期望 200，实际 %d body=%s", got, rc.Response.Body())
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v", err)
	}
	_, sid, _ := env.exec.snapshot()
	if sid == "" || sid != resp.SessionID {
		t.Fatalf("执行器 sessionID = %q, 响应 = %q", sid, resp.SessionID)
	}
	if !env.mem.OwnsSession(resp.SessionID, "crm", "body_user") {
		t.Fatalf("会话归属应为 crm/body_user，实际不属于该用户")
	}
}

// TestChatHandler_InvalidBodyUserReturns400 验证请求体 user 非法（含空格）时 400，
// 信封 param 为 user。
func TestChatHandler_InvalidBodyUserReturns400(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)

	rc := newChatPostContext(simpleChatBody(`"user":"bad user"`))
	setIdentity(rc, "svc", "crm", "u1")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 {
		t.Fatalf("期望 400，实际 %d body=%s", got, rc.Response.Body())
	}
	envl := decodeErrorEnvelope(t, rc)
	if envl.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, 期望 invalid_request_error", envl.Error.Type)
	}
	if envl.Error.Param == nil || *envl.Error.Param != "user" {
		t.Errorf("param = %v, 期望 user", envl.Error.Param)
	}
	if calls, _, _ := env.exec.snapshot(); calls != 0 {
		t.Errorf("非法 user 不应执行任务，实际调用 %d 次", calls)
	}
}

// TestChatHandler_ModelResolveFailureReturns500 验证未指定 model 且模型解析因数据库故障失败时
// 返回 500 internal_error，而不是把它误判为 400 model_not_found，且不向调用方泄露原始错误串。
func TestChatHandler_ModelResolveFailureReturns500(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.h.models = newBrokenModelServiceForTest(t)

	rc := newChatPostContext(simpleChatBody(""))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("期望 500，实际 %d body=%s", got, rc.Response.Body())
	}
	envl := decodeErrorEnvelope(t, rc)
	if envl.Error.Type != "api_error" {
		t.Errorf("type = %q, 期望 api_error", envl.Error.Type)
	}
	if envl.Error.Code == nil || *envl.Error.Code != "internal_error" {
		t.Errorf("code = %v, 期望 internal_error", envl.Error.Code)
	}
	for _, leaked := range []string{"database is closed", "sql:"} {
		if strings.Contains(string(rc.Response.Body()), leaked) {
			t.Fatalf("响应体不应包含原始错误串 %q: %s", leaked, rc.Response.Body())
		}
	}
	if calls, _, _ := env.exec.snapshot(); calls != 0 {
		t.Errorf("模型解析失败不应执行任务，实际调用 %d 次", calls)
	}
}

// TestChatHandler_RegisterStateCleanedOnCreateSessionFailure 验证 Register 成功后
// CreateSession 失败时返回 500 且活跃状态被清理，否则该 session_id 会一直表现为「正在运行」。
func TestChatHandler_RegisterStateCleanedOnCreateSessionFailure(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), true)

	rc := newChatPostContext(simpleChatBody(""))
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("期望 500，实际 %d body=%s", got, rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "创建会话失败") {
		t.Fatalf("期望包含「创建会话失败」，实际 %s", rc.Response.Body())
	}
	if got := env.rt.RunningCount(); got != 0 {
		t.Fatalf("CreateSession 失败后 RunningCount 应为 0，实际 %d", got)
	}
	if calls, _, _ := env.exec.snapshot(); calls != 0 {
		t.Errorf("CreateSession 失败后不应执行任务，实际调用 %d 次", calls)
	}
}

// TestChatHandler_AttachmentCountExceeded 验证附件数量超限时返回 400，
// 且对外 code 是映射后的 invalid_request，不再泄漏附件模块的私有码。
func TestChatHandler_AttachmentCountExceeded(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)

	// 默认上限为 10 个，这里给 11 个文本类 file 片段
	parts := make([]string, 0, 11)
	for i := 0; i < 11; i++ {
		parts = append(parts, `{"type":"file","file":{"filename":"file`+
			strconv.Itoa(i)+`.txt","file_data":"data:text/plain;base64,`+
			base64.StdEncoding.EncodeToString([]byte("hello"))+`"}}`)
	}
	body := `{"messages":[{"role":"user","content":[{"type":"text","text":"分析这些文件"},` +
		strings.Join(parts, ",") + `]}]}`

	rc := newChatPostContext(body)
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 {
		t.Fatalf("期望 400，实际 %d body=%s", got, rc.Response.Body())
	}
	envl := decodeErrorEnvelope(t, rc)
	if envl.Error.Code == nil || *envl.Error.Code != errbody.CodeInvalidRequest {
		t.Errorf("code = %v, 期望 invalid_request", envl.Error.Code)
	}
	if envl.Error.Param == nil || *envl.Error.Param != "messages" {
		t.Errorf("param = %v, 期望 messages", envl.Error.Param)
	}
	if calls, _, _ := env.exec.snapshot(); calls != 0 {
		t.Errorf("附件校验失败不应执行任务，实际调用 %d 次", calls)
	}
}

// waitUntil 以 10ms 步长轮询 cond，直到为真或超时；返回最终是否满足。
func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestChatHandler_NewSessionGetsTruncatedTitle 验证新建会话的标题取自末条 user 文本，
// 按 rune 截断到 MaxTitleRunes（多字节字符不被截坏），且响应体回传的 session_id 与落库一致。
func TestChatHandler_NewSessionGetsTruncatedTitle(t *testing.T) {
	env := newChatEnv(t, agent.NewRegistryForTest(1), false)
	env.exec.messages = []string{"ok"}

	head := strings.Repeat("标", 50) + strings.Repeat("题", 50) // 恰好 100 rune
	instruction := head + "尾"                                 // 101 rune
	if got := utf8.RuneCountInString(instruction); got != memory.MaxTitleRunes+1 {
		t.Fatalf("测试数据错误：期望 %d rune，实际 %d", memory.MaxTitleRunes+1, got)
	}

	body := `{"messages":[{"role":"user","content":"` + instruction + `"}]}`
	rc := newChatPostContext(body)
	setIdentity(rc, "web", "", "")

	env.h.Handle(context.Background(), rc)
	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("期望 200，实际 %d body=%s", got, rc.Response.Body())
	}

	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v", err)
	}

	sessions, _, err := env.mem.ListSessions(nil, 10, 0)
	if err != nil {
		t.Fatalf("ListSessions 失败: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("期望 1 个会话，实际 %d", len(sessions))
	}
	sess := sessions[0]
	if sess.SessionID != resp.SessionID {
		t.Fatalf("session id 不一致: db=%q 响应=%q", sess.SessionID, resp.SessionID)
	}
	if sess.Title != head {
		t.Fatalf("标题应为文本前 %d rune，实际 %q", memory.MaxTitleRunes, sess.Title)
	}
	if got := utf8.RuneCountInString(sess.Title); got != memory.MaxTitleRunes {
		t.Fatalf("标题 rune 数 = %d，期望 %d", got, memory.MaxTitleRunes)
	}
}

// TestChatHandler_NoHistorySkipsOnlyServerHistory 验证 no_history 只关闭服务端历史：
// 传了 session_id 与 no_history=true 时，本轮仍归入该会话、轮次照常累加、
// 调用方自带的 messages 历史照常生效，唯独 HistoryMessages 为空。
func TestChatHandler_NoHistorySkipsOnlyServerHistory(t *testing.T) {
	cases := []struct {
		name        string
		noHistory   bool
		wantHistory int
	}{
		{"未开启", false, 2},
		{"开启", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newChatEnv(t, agent.NewRegistryForTest(1), false)
			env.exec.messages = []string{"本轮回答"}

			const sid = "sess_seed_001"
			if err := env.mem.CreateSession(sid, "crm", "u1", "归档会话"); err != nil {
				t.Fatalf("创建会话失败: %v", err)
			}
			for i := 1; i <= 2; i++ {
				rec := &memory.ChatRecord{
					ChatID:      "seed_chat_" + strconv.Itoa(i),
					SessionID:   sid,
					Instruction: "旧问题" + strconv.Itoa(i),
					Result:      "旧回答" + strconv.Itoa(i),
					Status:      "completed",
					StartedAt:   time.Now(),
					EndedAt:     time.Now(),
				}
				if err := env.mem.SaveChatRecord(sid, rec); err != nil {
					t.Fatalf("预置历史失败: %v", err)
				}
			}

			body := `{"messages":[` +
				`{"role":"user","content":"调用方历史问题"},` +
				`{"role":"assistant","content":"调用方历史回答"},` +
				`{"role":"user","content":"本轮问题"}],` +
				`"session_id":"` + sid + `",` +
				`"no_history":` + strconv.FormatBool(tc.noHistory) + `}`
			rc := newChatPostContext(body)
			setIdentity(rc, "svc", "crm", "u1")

			env.h.Handle(context.Background(), rc)
			if got := rc.Response.StatusCode(); got != 200 {
				t.Fatalf("期望 200，实际 %d body=%s", got, rc.Response.Body())
			}

			var resp struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
				t.Fatalf("响应体不是合法 JSON: %v", err)
			}
			if resp.SessionID != sid {
				t.Errorf("session_id = %q, 期望归入 %q", resp.SessionID, sid)
			}

			_, gotSID, task := env.exec.snapshot()
			if gotSID != sid {
				t.Errorf("执行器 sessionID = %q, 期望 %q", gotSID, sid)
			}
			if task == nil {
				t.Fatal("执行器未收到 task")
			}
			if task.Round != 3 {
				t.Errorf("round = %d, 期望 3（轮次照常累加）", task.Round)
			}
			if len(task.HistoryMessages) != tc.wantHistory {
				t.Errorf("HistoryMessages 长度 = %d, 期望 %d: %+v",
					len(task.HistoryMessages), tc.wantHistory, task.HistoryMessages)
			}
			if len(task.CallerHistory) != 2 {
				t.Errorf("CallerHistory 长度 = %d, 期望 2（调用方历史不受 no_history 影响）", len(task.CallerHistory))
			}
			if !env.mem.ExistsSession(sid) {
				t.Error("会话应保留（留档）")
			}
		})
	}
}
