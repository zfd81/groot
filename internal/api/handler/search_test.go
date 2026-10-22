package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo/memorydb"
)

// newSearchTestHandler 构造挂了临时 SQLite 库的 SessionHandler（单机模式、fakeCanceller），
// 同时返回其 RuntimeState 以便测试注册活跃对话。
func newSearchTestHandler(t *testing.T) (*SessionHandler, *memory.Manager, *agent.RuntimeState) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	rt := agent.NewRuntimeState()
	return NewSessionHandler(mem, rt, &fakeCanceller{}), mem, rt
}

// newSearchTestContext 构造 GET /sess/search 的 RequestContext；
// rawQuery 传已 URL 编码的原始 query 串，rc.Query 会负责解码。
func newSearchTestContext(rawQuery string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/sess/search?" + rawQuery)
	return rc
}

func TestSearchSessions_OK(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("hs-1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err := mem.SaveChatRecord("hs-1", &memory.ChatRecord{
		ChatID: "hc-1", Instruction: "查询订单状态", Result: "订单已发货",
		Status: "completed", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord: %v", err)
	}

	rc := newSearchTestContext("q=%E8%AE%A2%E5%8D%95") // q=订单
	setIdentity(rc, "svc", "crm", "u1")
	h.SearchSessions(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var body struct {
		Status  string                `json:"status"`
		Results []memory.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if body.Status != "success" {
		t.Errorf("expected status=success, got %q", body.Status)
	}
	if len(body.Results) != 1 || body.Results[0].ChatID != "hc-1" {
		t.Fatalf("unexpected results: %+v", body.Results)
	}
}

func TestSearchSessions_LimitParam(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("hs-lim", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, cid := range []string{"hc-lim-1", "hc-lim-2"} {
		err := mem.SaveChatRecord("hs-lim", &memory.ChatRecord{
			ChatID: cid, Instruction: "limitcase question", Result: "limitcase answer",
			Status: "completed", StartedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("SaveChatRecord(%s): %v", cid, err)
		}
	}

	doSearch := func(rawQuery string) []memory.SearchResult {
		t.Helper()
		rc := newSearchTestContext(rawQuery)
		setIdentity(rc, "svc", "crm", "u1")
		h.SearchSessions(context.Background(), rc)
		if rc.Response.StatusCode() != 200 {
			t.Fatalf("rawQuery=%q: expected 200, got %d body=%s",
				rawQuery, rc.Response.StatusCode(), rc.Response.Body())
		}
		var body struct {
			Status  string                `json:"status"`
			Results []memory.SearchResult `json:"results"`
		}
		if err := json.Unmarshal(rc.Response.Body(), &body); err != nil {
			t.Fatalf("rawQuery=%q: unmarshal: %v body=%s", rawQuery, err, rc.Response.Body())
		}
		if body.Status != "success" {
			t.Fatalf("rawQuery=%q: expected status=success, got %q", rawQuery, body.Status)
		}
		return body.Results
	}

	// limit=1 只返回 1 条
	if got := doSearch("q=limitcase&limit=1"); len(got) != 1 {
		t.Errorf("limit=1: expected 1 result, got %d: %+v", len(got), got)
	}
	// 非法/超限的 limit 回落默认 20，两条都返回
	for _, raw := range []string{"q=limitcase&limit=abc", "q=limitcase&limit=999"} {
		if got := doSearch(raw); len(got) != 2 {
			t.Errorf("rawQuery=%q: expected 2 results (fallback limit), got %d: %+v", raw, len(got), got)
		}
	}
}

func TestSearchSessions_EmptyQuery(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	for _, raw := range []string{"q=", "q=%20%20", ""} {
		rc := newSearchTestContext(raw)
		setIdentity(rc, "svc", "crm", "u1")
		h.SearchSessions(context.Background(), rc)
		if rc.Response.StatusCode() != 200 {
			t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
		}
		var body struct {
			Status  string                `json:"status"`
			Results []memory.SearchResult `json:"results"`
		}
		if err := json.Unmarshal(rc.Response.Body(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body.Status != "success" || len(body.Results) != 0 {
			t.Errorf("raw=%q: expected empty success, got %s", raw, rc.Response.Body())
		}
	}
}

func TestSearchSessions_ForeignTenantEmpty(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("hs-ft", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err := mem.SaveChatRecord("hs-ft", &memory.ChatRecord{
		ChatID: "hc-ft", Instruction: "foreigncase question", Result: "foreigncase answer",
		Status: "completed", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord: %v", err)
	}

	// 同用户 ID、不同租户：不得看到他租户的记录
	rc := newSearchTestContext("q=foreigncase")
	setIdentity(rc, "svc", "erp", "u1")
	h.SearchSessions(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var body struct {
		Status  string                `json:"status"`
		Results []memory.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if body.Status != "success" || len(body.Results) != 0 {
		t.Errorf("expected empty success, got %s", rc.Response.Body())
	}
}

func TestSearchSessions_MissingUserID400(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	rc := newSearchTestContext("q=anything")
	setIdentity(rc, "svc", "crm", "")
	h.SearchSessions(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestSearchSessions_WebAdminSeesAll(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("hs-adm", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err := mem.SaveChatRecord("hs-adm", &memory.ChatRecord{
		ChatID: "hc-adm", Instruction: "admincase question", Result: "admincase answer",
		Status: "completed", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord: %v", err)
	}

	rc := newSearchTestContext("q=admincase")
	setIdentity(rc, "web", "", "")
	h.SearchSessions(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var body struct {
		Results []memory.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if len(body.Results) != 1 || body.Results[0].ChatID != "hc-adm" {
		t.Errorf("unexpected results: %+v", body.Results)
	}
}
