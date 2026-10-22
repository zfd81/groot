package handler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"

	"github.com/zfd81/groot/internal/memory"
)

// newDetailContext 构造 GET /chat/{sid}/{cid} 的 RequestContext；cid 为空时只注入 sid。
func newDetailContext(sid, cid string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	uri := "/chat/" + sid
	if cid != "" {
		uri += "/" + cid
	}
	rc.Request.SetRequestURI(uri)
	rc.Params = append(rc.Params, param.Param{Key: "sid", Value: sid})
	if cid != "" {
		rc.Params = append(rc.Params, param.Param{Key: "cid", Value: cid})
	}
	return rc
}

// seedChat 创建归属 (tenant,user) 的会话 sid，并在其下写入一条对话记录 cid。
func seedChat(t *testing.T, mem *memory.Manager, sid, tenant, user, cid string) {
	t.Helper()
	if err := mem.CreateSession(sid, tenant, user, ""); err != nil {
		t.Fatalf("CreateSession(%s): %v", sid, err)
	}
	err := mem.SaveChatRecord(sid, &memory.ChatRecord{
		ChatID:      cid,
		Instruction: "hello",
		Result:      "world",
		Status:      "completed",
		StartedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord(%s/%s): %v", sid, cid, err)
	}
}

func detailSessionNotFoundBody(sid string) string {
	return fmt.Sprintf(`{"status":"session_not_found","session_id":"%s","message":"会话不存在"}`, sid)
}

func TestDetail_ForeignSessionIs404(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	h := NewDetailHandler(mem)

	rc := newDetailContext("s1", "c1")
	setIdentity(rc, "svc", "erp", "u1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 404 {
		t.Fatalf("status = %d, want 404; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := string(rc.Response.Body()); got != detailSessionNotFoundBody("s1") {
		t.Fatalf("body = %s, want %s", got, detailSessionNotFoundBody("s1"))
	}
}

func TestDetail_ForeignUserIs404(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	h := NewDetailHandler(mem)

	rc := newDetailContext("s1", "c1")
	setIdentity(rc, "svc", "crm", "u2")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 404 {
		t.Fatalf("status = %d, want 404; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := string(rc.Response.Body()); got != detailSessionNotFoundBody("s1") {
		t.Fatalf("body = %s, want %s", got, detailSessionNotFoundBody("s1"))
	}
}

func TestDetail_ForeignChatIs404(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	seedChat(t, mem, "s2", "crm", "u1", "c2")
	h := NewDetailHandler(mem)

	// s2 归属正确，但 c1 属于 s1 → chat_not_found
	rc := newDetailContext("s2", "c1")
	setIdentity(rc, "svc", "crm", "u1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 404 {
		t.Fatalf("status = %d, want 404; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	want := `{"status":"chat_not_found","session_id":"s2","chat_id":"c1","message":"对话记录不存在"}`
	if got := string(rc.Response.Body()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestDetail_OwnerOK(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	h := NewDetailHandler(mem)

	rc := newDetailContext("s1", "c1")
	setIdentity(rc, "svc", "crm", "u1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestDetail_MissingTenantIs403(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	h := NewDetailHandler(mem)

	rc := newDetailContext("s1", "c1")
	setIdentity(rc, "svc", "", "u1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 403 {
		t.Fatalf("status = %d, want 403; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestGetLatest_ForeignSessionIs404(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	h := NewDetailHandler(mem)

	rc := newDetailContext("s1", "")
	setIdentity(rc, "svc", "erp", "u1")
	h.GetLatest(context.Background(), rc)

	if rc.Response.StatusCode() != 404 {
		t.Fatalf("status = %d, want 404; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := string(rc.Response.Body()); got != detailSessionNotFoundBody("s1") {
		t.Fatalf("body = %s, want %s", got, detailSessionNotFoundBody("s1"))
	}
}

func TestGetLatest_OwnerOK(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	h := NewDetailHandler(mem)

	rc := newDetailContext("s1", "")
	setIdentity(rc, "svc", "crm", "u1")
	h.GetLatest(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestGetLatest_WebAdminOK(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	h := NewDetailHandler(mem)

	rc := newDetailContext("s1", "")
	setIdentity(rc, "web", "", "")
	h.GetLatest(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestGetLatest_MissingUserIs400(t *testing.T) {
	_, mem, _ := newSearchTestHandler(t)
	seedChat(t, mem, "s1", "crm", "u1", "c1")
	h := NewDetailHandler(mem)

	rc := newDetailContext("s1", "")
	setIdentity(rc, "svc", "crm", "")
	h.GetLatest(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status = %d, want 400; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}
