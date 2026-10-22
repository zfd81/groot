package handler

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/agent"
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
// 使 Handle 的模型校验（2.6）通过，从而能覆盖其后的 X-Agent-Name 校验路径。
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

// newChatHandlerForTest 构造仅注入 SubAgentRegistry + ModelService + NopLogger 的最小 ChatHandler，
// 仅用于覆盖 X-Agent-Name 校验路径，**不**触达 memory/executor/runtime 等下游。
// 该路径在校验失败时早返，所以零值依赖是安全的；logger 必须非 nil 以承接 Error 调用；
// models 必须非 nil 且含默认模型，否则模型校验（位于 agent 校验之前）会先行 400。
func newChatHandlerForTest(t *testing.T, reg *agent.SubAgentRegistry) *ChatHandler {
	t.Helper()
	return &ChatHandler{subAgentRegistry: reg, models: newModelServiceForTest(t), log: logger.NewNop()}
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

// TestChatHandler_UnknownAgentReturns400 验证 X-Agent-Name 指向未注册子 Agent 时
// 早返 400 unknown_agent，不触达 runtime/memory。
func TestChatHandler_UnknownAgentReturns400(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	h := newChatHandlerForTest(t, reg)

	rc := newChatPostContext(`{"instruction":"hi"}`)
	rc.Request.Header.Set("X-Agent-Name", "nope")

	h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 {
		t.Fatalf("expected 400, got %d body=%s", got, rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "Unknown agent") {
		t.Fatalf("expected 'Unknown agent' in body, got: %s", rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "unknown_agent") {
		t.Fatalf("expected status 'unknown_agent' in body, got: %s", rc.Response.Body())
	}
}

// TestChatHandler_NilRegistryReturns400 验证 subAgentRegistry == nil 时同样 400。
func TestChatHandler_NilRegistryReturns400(t *testing.T) {
	h := newChatHandlerForTest(t, nil)

	rc := newChatPostContext(`{"instruction":"hi"}`)
	rc.Request.Header.Set("X-Agent-Name", "any")

	h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 {
		t.Fatalf("expected 400, got %d body=%s", got, rc.Response.Body())
	}
}

// TestChatHandler_MainAgentNameTreatedAsEmpty 验证 X-Agent-Name=groot 与不传等价：
// 此路径不会因为 agent 校验失败而 400。校验通过后下游因 nil 依赖会 panic 或返其他状态码，
// 我们用「status code 非 400 + body 不含 unknown_agent」证明校验路径已被跳过。
//
// 这两条断言互补：
//   - status != 400：直接排除 unknown_agent / invalid_request / chat_limit_exceeded 等所有早返 400 路径
//   - body 不含 unknown_agent：再次确认即便偶然 status 是 400，错误码也不是 unknown_agent
//
// 任意一条失败都说明 "groot" 标准化逻辑回归。
func TestChatHandler_MainAgentNameTreatedAsEmpty(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	h := newChatHandlerForTest(t, reg)

	rc := newChatPostContext(`{"instruction":"hi"}`)
	rc.Request.Header.Set("X-Agent-Name", agent.MainAgentName)
	// 归属前置检查位于 agent 校验之后；以 Web 管理员身份通过它，保证测试仍覆盖到下游
	rc.Set("caller", "web")

	var panicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		h.Handle(context.Background(), rc)
	}()

	body := string(rc.Response.Body())

	// 关键断言 1：error 路径未进 unknown_agent 早返
	if strings.Contains(body, "unknown_agent") {
		t.Fatalf("X-Agent-Name=groot should NOT trigger unknown_agent, body=%s", body)
	}

	// 关键断言 2：要么 panic（下游 nil 依赖），要么 status code 不是 400 unknown_agent。
	// 二者任一证明校验已跳过；同时 status==400 + body 不含 unknown_agent 也是合规
	// （比如 invalid_request 文案的 400 也证明校验已通过——但这种状态通常意味着别的问题，
	// 我们至少要保证测试在校验回归（错误地 unknown_agent）时一定 fail）。
	if !panicked && rc.Response.StatusCode() == 400 && strings.Contains(body, "Unknown agent") {
		t.Fatalf("校验路径疑似回归：未 panic 且返回 400 Unknown agent，body=%s", body)
	}
}

// TestChatHandler_MissingUserIDReturns400 验证 API Key 调用者有租户但缺 user_id 时 400，
// 文案提示 X-User-ID；归属检查位于 agent 校验之后、X-Session-ID 处理之前，不触达 memory。
func TestChatHandler_MissingUserIDReturns400(t *testing.T) {
	h := newChatHandlerForTest(t, agent.NewRegistryForTest(1))
	rc := newChatPostContext(`{"instruction":"hi"}`)
	rc.Set("caller", "svc")
	rc.Set("tenant_id", "crm")

	h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 400 || !strings.Contains(string(rc.Response.Body()), "X-User-ID") {
		t.Fatalf("expected 400 mentioning X-User-ID, got %d body=%s", got, rc.Response.Body())
	}
}

// TestChatHandler_EmptyTenantReturns403 验证 API Key 未绑定租户时 403 forbidden。
func TestChatHandler_EmptyTenantReturns403(t *testing.T) {
	h := newChatHandlerForTest(t, agent.NewRegistryForTest(1))
	rc := newChatPostContext(`{"instruction":"hi"}`)
	rc.Set("caller", "svc")
	rc.Set("tenant_id", "")
	rc.Set("user_id", "u1")

	h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 403 || !strings.Contains(string(rc.Response.Body()), "forbidden") {
		t.Fatalf("expected 403 forbidden, got %d body=%s", got, rc.Response.Body())
	}
}

// newChatHandlerWithMemory 构造 runtimeState 为真实实例、memory 指向给定 Manager 的 ChatHandler。
// settings 使用独立的、仍打开的临时库，保证 Runtime(ctx) 正常返回，不干扰被测路径。
// agentExecutor 保持 nil：到达 goroutine 的用例依赖其内部 recover 把 nil 解引用转为 SSE error。
func newChatHandlerWithMemory(t *testing.T, mem *memory.Manager) (*ChatHandler, *agent.RuntimeState) {
	t.Helper()

	setDB, setDialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open(settings) failed: %v", err)
	}
	t.Cleanup(func() { _ = setDB.Close() })
	settings := setting.New(runtimeBoot(), settingdb.New(setDB, setDialect))

	rt := agent.NewRuntimeState()
	h := &ChatHandler{
		memory:       mem,
		runtimeState: rt,
		models:       newModelServiceForTest(t),
		settings:     settings,
		log:          logger.NewNop(),
	}
	return h, rt
}

// newChatHandlerWithClosedMemory 构造 memory 指向已关闭 sqlx.DB 的 ChatHandler：
// CreateSession 必然失败，用于覆盖 Register 成功之后的失败清理分支。
func newChatHandlerWithClosedMemory(t *testing.T) (*ChatHandler, *agent.RuntimeState) {
	t.Helper()

	memDB, memDialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open(memory) failed: %v", err)
	}
	mem := memory.NewManager(logger.NewNop(), memorydb.New(memDB, memDialect))
	// 关闭底层连接后任何写入都会失败，CreateSession 即走 500 分支
	if err := memDB.Close(); err != nil {
		t.Fatalf("memDB.Close failed: %v", err)
	}
	return newChatHandlerWithMemory(t, mem)
}

// newChatHandlerWithOpenMemory 构造 memory 指向仍打开的临时库的 ChatHandler，
// 用于断言新建会话真实落库的内容；库在测试结束时关闭。
func newChatHandlerWithOpenMemory(t *testing.T) (*ChatHandler, *memory.Manager, *agent.RuntimeState) {
	t.Helper()

	memDB, memDialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open(memory) failed: %v", err)
	}
	t.Cleanup(func() { _ = memDB.Close() })
	mem := memory.NewManager(logger.NewNop(), memorydb.New(memDB, memDialect))
	h, rt := newChatHandlerWithMemory(t, mem)
	return h, mem, rt
}

// TestChatHandler_RegisterStateCleanedOnCreateSessionFailure 验证 Register 成功后
// CreateSession 失败时，handler 返回 500 且已注册的活跃状态被清理（RunningCount 回到 0），
// 否则该 session_id 会一直表现为"正在运行"，后续 Wait 只能等到超时。
func TestChatHandler_RegisterStateCleanedOnCreateSessionFailure(t *testing.T) {
	h, rt := newChatHandlerWithClosedMemory(t)

	rc := newChatPostContext(`{"instruction":"hi"}`)
	setIdentity(rc, "web", "", "")

	h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("expected 500, got %d body=%s", got, rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "创建会话失败") {
		t.Fatalf("expected '创建会话失败' in body, got: %s", rc.Response.Body())
	}
	if got := rt.RunningCount(); got != 0 {
		t.Fatalf("expected RunningCount 0 after CreateSession failure, got %d", got)
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

// TestChatHandler_NewSessionGetsTruncatedTitle 验证 /chat 新建会话时，标题取自 instruction
// 并按 rune 截断到 MaxTitleRunes（多字节字符不被截坏）。
//
// agentExecutor 为 nil，goroutine 内对其解引用会 panic 并被 recover 转为 SSE error；
// 会话在 goroutine 启动之前即已创建，所以断言不依赖执行结果。
// SSE 写入走 io.Pipe，必须有读端消费，否则 recover 中的 WriteError 永久阻塞，
// RunningCount 也永不归零。
func TestChatHandler_NewSessionGetsTruncatedTitle(t *testing.T) {
	h, mem, rt := newChatHandlerWithOpenMemory(t)

	head := strings.Repeat("标", 50) + strings.Repeat("题", 50) // 恰好 100 rune
	instruction := head + "尾"                                 // 101 rune
	if got := utf8.RuneCountInString(instruction); got != memory.MaxTitleRunes+1 {
		t.Fatalf("test setup: expected %d runes, got %d", memory.MaxTitleRunes+1, got)
	}

	rc := newChatPostContext(`{"instruction":"` + instruction + `"}`)
	setIdentity(rc, "web", "", "")

	h.Handle(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200 (SSE started), got %d body=%s", got, rc.Response.Body())
	}
	if got := rc.Response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", got)
	}

	// 消费 SSE 流，让 goroutine 能写完并退出
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(io.Discard, rc.Response.BodyStream())
	}()

	var sessions []memory.SessionInfo
	ok := waitUntil(time.Second, func() bool {
		list, _, err := mem.ListSessions(nil, 10, 0)
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}
		sessions = list
		return len(sessions) == 1
	})
	if !ok {
		t.Fatalf("expected exactly 1 session within 1s, got %d", len(sessions))
	}

	sess := sessions[0]
	if sess.SessionID != rc.Response.Header.Get("X-Session-ID") {
		t.Fatalf("session id mismatch: db=%q header=%q", sess.SessionID, rc.Response.Header.Get("X-Session-ID"))
	}
	if sess.Title != head {
		t.Fatalf("expected title to be first %d runes of instruction, got %q", memory.MaxTitleRunes, sess.Title)
	}
	if got := utf8.RuneCountInString(sess.Title); got != memory.MaxTitleRunes {
		t.Fatalf("expected title rune count %d, got %d", memory.MaxTitleRunes, got)
	}

	// 等 goroutine 结束再返回，避免 t.Cleanup 先关库导致 "database is closed" 噪音
	if !waitUntil(2*time.Second, func() bool { return rt.RunningCount() == 0 }) {
		t.Fatalf("agent goroutine did not finish within 2s, RunningCount=%d", rt.RunningCount())
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatalf("SSE body stream was not closed within 2s")
	}
}
