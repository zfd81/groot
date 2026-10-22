package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo/memorydb"
)

// newStatusTestContext 构造一个带 sid path param 的 RequestContext。
//
// hertz 的 path param 通过 RequestContext.Params 注入；handler 内部用 rc.Param("sid")
// 读取。这里直接 append 一条 param.Param 即可，与 hertz 自身 context_test.go 的写法
// 保持一致（参见 hertz@v0.10.4/pkg/app/context_test.go:271）。
func newStatusTestContext(sid string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Params = append(rc.Params, param.Param{Key: "sid", Value: sid})
	return rc
}

// TestStatusHandler_IncludesSubAgents 验证活跃对话状态响应中包含 sub_agents 数组，
// 且能反映 AddSubAgent 注册的子 Agent 名字。这是 Plan Task 19 的核心验收点。
func TestStatusHandler_IncludesSubAgents(t *testing.T) {
	rt := agent.NewRuntimeState()
	if _, err := rt.Register(context.Background(), "sess", "chat"); err != nil {
		t.Fatal(err)
	}
	rt.AddSubAgent("sess", "db-agent")
	rt.AddSubAgent("sess", "weather-agent")

	// status handler 在 active chat 路径上仍会调 memory.ExistsSession 拿轮数；
	// 给一个真实的 Manager 指向临时目录即可，会话不存在不会报错。
	sqlxDB, dialect, _ := db.Open(nil, t.TempDir())
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	h := NewStatusHandler(rt, mem)

	rc := newStatusTestContext("sess")
	setIdentity(rc, "web", "", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	body := rc.Response.Body()
	if !strings.Contains(string(body), `"sub_agents"`) {
		t.Errorf("expected sub_agents in body: %s", body)
	}
	if !strings.Contains(string(body), `"db-agent"`) {
		t.Errorf("expected db-agent in body: %s", body)
	}
	if !strings.Contains(string(body), `"weather-agent"`) {
		t.Errorf("expected weather-agent in body: %s", body)
	}

	// 进一步解析响应结构，确保 sub_agents 是个数组而非字符串拼接产物。
	var resp struct {
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
		Chat      struct {
			ChatID   string `json:"chat_id"`
			Status   string `json:"status"`
			Progress struct {
				SubAgents []struct {
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"sub_agents"`
			} `json:"progress"`
		} `json:"chat"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if resp.Status != "success" {
		t.Errorf("status = %q, want success", resp.Status)
	}
	if resp.Chat.ChatID != "chat" {
		t.Errorf("chat_id = %q, want chat", resp.Chat.ChatID)
	}
	if got := len(resp.Chat.Progress.SubAgents); got != 2 {
		t.Fatalf("sub_agents count = %d, want 2: %+v", got, resp.Chat.Progress.SubAgents)
	}
	names := []string{resp.Chat.Progress.SubAgents[0].Name, resp.Chat.Progress.SubAgents[1].Name}
	if names[0] != "db-agent" || names[1] != "weather-agent" {
		t.Errorf("sub_agents order = %+v, want [db-agent weather-agent]", names)
	}
	for i, sa := range resp.Chat.Progress.SubAgents {
		if sa.Status != "running" {
			t.Errorf("SubAgents[%d].status = %q, want running", i, sa.Status)
		}
	}
}

// TestStatusHandler_IdleNoActiveChat 验证 sid 没有活跃对话时返回 idle/chat=null，
// 这是 SnapshotProgress 不会被调用的分支——确保没有空指针问题。
func TestStatusHandler_IdleNoActiveChat(t *testing.T) {
	rt := agent.NewRuntimeState()
	sqlxDB, dialect, _ := db.Open(nil, t.TempDir())
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	h := NewStatusHandler(rt, mem)

	rc := newStatusTestContext("missing-sess")
	setIdentity(rc, "web", "", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	body := string(rc.Response.Body())
	if !strings.Contains(body, `"status":"idle"`) {
		t.Errorf("expected idle status, got %s", body)
	}
	if !strings.Contains(body, `"chat":null`) {
		t.Errorf("expected chat=null, got %s", body)
	}
}

// TestStatusHandler_MissingSidReturns400 验证 sid path param 缺失时返 400。
func TestStatusHandler_MissingSidReturns400(t *testing.T) {
	rt := agent.NewRuntimeState()
	sqlxDB, dialect, _ := db.Open(nil, t.TempDir())
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	h := NewStatusHandler(rt, mem)

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	// 不注入 sid param

	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// TestStatus_ForeignRunningSessionLooksIdle 验证归属判定先于活跃状态：
// 其他租户的调用者查询一个正在运行的会话，必须得到与"会话不存在"完全一致的
// idle 响应（200、round_count 0、chat null），绝不能泄露活跃 chat_id。
func TestStatus_ForeignRunningSessionLooksIdle(t *testing.T) {
	rt := agent.NewRuntimeState()
	if _, err := rt.Register(context.Background(), "s1", "c1"); err != nil {
		t.Fatal(err)
	}
	sqlxDB, dialect, _ := db.Open(nil, t.TempDir())
	t.Cleanup(func() { sqlxDB.Close() })
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatal(err)
	}
	h := NewStatusHandler(rt, mem)

	rc := newStatusTestContext("s1")
	setIdentity(rc, "svc", "erp", "u1")
	h.Serve(context.Background(), rc)

	body := string(rc.Response.Body())
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), body)
	}
	if !strings.Contains(body, `"status":"idle"`) || !strings.Contains(body, `"round_count":0`) ||
		!strings.Contains(body, `"chat":null`) || !strings.Contains(body, `"last_message":null`) {
		t.Fatalf("foreign caller must see idle/0/null: %s", body)
	}
	if strings.Contains(body, "c1") {
		t.Fatalf("foreign caller must not see active chat id: %s", body)
	}
}

// TestStatus_OwnerSeesRunningSession 验证归属匹配的 API Key 调用者仍能看到活跃状态。
func TestStatus_OwnerSeesRunningSession(t *testing.T) {
	rt := agent.NewRuntimeState()
	if _, err := rt.Register(context.Background(), "s1", "c1"); err != nil {
		t.Fatal(err)
	}
	sqlxDB, dialect, _ := db.Open(nil, t.TempDir())
	t.Cleanup(func() { sqlxDB.Close() })
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatal(err)
	}
	h := NewStatusHandler(rt, mem)

	rc := newStatusTestContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	h.Serve(context.Background(), rc)

	body := string(rc.Response.Body())
	if rc.Response.StatusCode() != 200 || !strings.Contains(body, `"status":"success"`) || !strings.Contains(body, `"chat_id":"c1"`) {
		t.Fatalf("owner must see active chat: %d %s", rc.Response.StatusCode(), body)
	}
}

// TestStatus_MissingUserID400 验证 API Key 调用者缺少 X-User-ID 时返回 400。
func TestStatus_MissingUserID400(t *testing.T) {
	rt := agent.NewRuntimeState()
	sqlxDB, dialect, _ := db.Open(nil, t.TempDir())
	t.Cleanup(func() { sqlxDB.Close() })
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	h := NewStatusHandler(rt, mem)

	rc := newStatusTestContext("s1")
	setIdentity(rc, "svc", "crm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// TestStatus_MissingTenant403 验证 API Key 未绑定租户时返回 403。
func TestStatus_MissingTenant403(t *testing.T) {
	rt := agent.NewRuntimeState()
	sqlxDB, dialect, _ := db.Open(nil, t.TempDir())
	t.Cleanup(func() { sqlxDB.Close() })
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	h := NewStatusHandler(rt, mem)

	rc := newStatusTestContext("s1")
	setIdentity(rc, "svc", "", "u1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 403 {
		t.Fatalf("expected 403, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}
