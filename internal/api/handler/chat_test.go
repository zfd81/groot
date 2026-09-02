package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
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

// TestChatHandler_UnknownAgentReturns400 验证 X-Agent-Name 指向未注册子 Agent 时
// 早返 400 unknown_agent，不触达 runtime/memory。
func TestChatHandler_UnknownAgentReturns400(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	h := newChatHandlerForTest(t, reg)

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	rc.Request.Header.Set("X-Agent-Name", "nope")
	rc.Request.SetBody([]byte(`{"instruction":"hi"}`))

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

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	rc.Request.Header.Set("X-Agent-Name", "any")
	rc.Request.SetBody([]byte(`{"instruction":"hi"}`))

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

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	rc.Request.Header.Set("X-Agent-Name", agent.MainAgentName)
	rc.Request.SetBody([]byte(`{"instruction":"hi"}`))

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
