package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/memory"
)

// setIdentity 模拟 auth/identity 中间件向 RequestContext 注入的调用者身份。
// caller 为 "web" 时视为管理员；其余值视为 API Key 调用者，需携带 tenant 与 user。
func setIdentity(rc *app.RequestContext, caller, tenant, user string) {
	rc.Set("caller", caller)
	rc.Set("tenant_id", tenant)
	rc.Set("user_id", user)
}

// newSidContext 构造 GET /sess/{sid} 的 RequestContext，sid 通过 path param 注入。
func newSidContext(sid string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/sess/" + sid)
	rc.Params = append(rc.Params, param.Param{Key: "sid", Value: sid})
	return rc
}

// newListContext 构造 GET /sess/history 的 RequestContext；rawQuery 为原始 query 串。
func newListContext(rawQuery string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/sess/history?" + rawQuery)
	return rc
}

const sessionNotFoundBody = `{"status":"session_not_found","message":"会话不存在"}`

func TestGetSession_ForeignTenantIs404(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newSidContext("s1")
	setIdentity(rc, "svc", "erp", "u1")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Fatalf("expected 404, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	// 他租户的会话与不存在的会话必须不可区分：响应体与缺失时完全一致
	if got := string(rc.Response.Body()); got != sessionNotFoundBody {
		t.Errorf("unexpected body: %s", got)
	}
}

func TestGetSession_ForeignUserIs404(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newSidContext("s1")
	setIdentity(rc, "svc", "crm", "u2")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Fatalf("expected 404, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := string(rc.Response.Body()); got != sessionNotFoundBody {
		t.Errorf("unexpected body: %s", got)
	}
}

func TestGetSession_MissingIs404(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	rc := newSidContext("nope")
	setIdentity(rc, "svc", "crm", "u1")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Fatalf("expected 404, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := string(rc.Response.Body()); got != sessionNotFoundBody {
		t.Errorf("unexpected body: %s", got)
	}
}

func TestGetSession_OwnerOK(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newSidContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if resp.Status != "success" || resp.SessionID != "s1" {
		t.Errorf("unexpected resp: %+v", resp)
	}
}

func TestGetSession_WebAdminBypasses(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newSidContext("s1")
	setIdentity(rc, "web", "", "")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestGetSession_WebAdminMissingIs404(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	rc := newSidContext("nope")
	setIdentity(rc, "web", "", "")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Fatalf("expected 404, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestGetSession_MissingUserID400(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newSidContext("s1")
	setIdentity(rc, "svc", "crm", "")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestGetSession_MissingTenant403(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newSidContext("s1")
	setIdentity(rc, "svc", "", "u1")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 403 {
		t.Fatalf("expected 403, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// listResp 是 /sess/history 响应中本测试关心的字段。
type listResp struct {
	Total    int `json:"total"`
	Sessions []struct {
		SessionID string `json:"session_id"`
	} `json:"sessions"`
}

func TestListSessions_FilteredByOwner(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	for _, s := range []struct{ sid, tenant, user string }{
		{"a", "crm", "u1"}, {"b", "crm", "u2"}, {"c", "erp", "u1"},
	} {
		if err := mem.CreateSession(s.sid, s.tenant, s.user, ""); err != nil {
			t.Fatalf("CreateSession(%s): %v", s.sid, err)
		}
	}
	rc := newListContext("limit=10")
	setIdentity(rc, "svc", "crm", "u1")
	h.ListSessions(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp listResp
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if resp.Total != 1 || len(resp.Sessions) != 1 || resp.Sessions[0].SessionID != "a" {
		t.Errorf("unexpected resp: %+v", resp)
	}
}

func TestListSessions_WebAdminSeesAll(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	for _, s := range []struct{ sid, tenant, user string }{
		{"a", "crm", "u1"}, {"b", "crm", "u2"}, {"c", "erp", "u1"},
	} {
		if err := mem.CreateSession(s.sid, s.tenant, s.user, ""); err != nil {
			t.Fatalf("CreateSession(%s): %v", s.sid, err)
		}
	}
	rc := newListContext("limit=10")
	setIdentity(rc, "web", "", "")
	h.ListSessions(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp listResp
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if resp.Total != 3 || len(resp.Sessions) != 3 {
		t.Errorf("unexpected resp: %+v", resp)
	}
}

func TestListSessions_MissingUserID400(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	rc := newListContext("limit=10")
	setIdentity(rc, "svc", "crm", "")
	h.ListSessions(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// ---- DELETE /sess/{sid} ----

// newDeleteContext 构造 DELETE /sess/{sid} 的 RequestContext；sid 为空时不注入 path param。
func newDeleteContext(sid string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodDelete)
	rc.Request.SetRequestURI("/sess/" + sid)
	if sid != "" {
		rc.Params = append(rc.Params, param.Param{Key: "sid", Value: sid})
	}
	return rc
}

// shortenDeleteTimers 缩短删除流程里的等待时间，测试结束后恢复。
func shortenDeleteTimers(t *testing.T, wait, grace time.Duration) {
	t.Helper()
	oldWait, oldGrace := deleteWaitTimeout, deleteBroadcastGrace
	deleteWaitTimeout, deleteBroadcastGrace = wait, grace
	t.Cleanup(func() { deleteWaitTimeout, deleteBroadcastGrace = oldWait, oldGrace })
}

func createSessionWithChat(t *testing.T, mem *memory.Manager, sid, tenant, user string) {
	t.Helper()
	if err := mem.CreateSession(sid, tenant, user, ""); err != nil {
		t.Fatalf("CreateSession(%s): %v", sid, err)
	}
	err := mem.SaveChatRecord(sid, &memory.ChatRecord{
		ChatID: sid + "-c1", Instruction: "hi", Result: "ok", Status: "completed", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord(%s): %v", sid, err)
	}
}

func TestDeleteSession_OwnerOK(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")

	rc := newDeleteContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	body := string(rc.Response.Body())
	for _, want := range []string{`"status":"success"`, `"session_id":"s1"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want contains %s", body, want)
		}
	}
	if mem.ExistsSession("s1") {
		t.Fatal("session s1 should be deleted")
	}
	if hist, err := mem.GetHistory("s1"); err == nil && hist != nil && len(hist.Messages) != 0 {
		t.Fatalf("history should be gone, got %d messages", len(hist.Messages))
	}
}

func TestDeleteSession_ForeignIs404(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")

	for _, id := range [][2]string{{"erp", "u1"}, {"crm", "u2"}} {
		rc := newDeleteContext("s1")
		setIdentity(rc, "svc", id[0], id[1])
		h.DeleteSession(context.Background(), rc)

		if rc.Response.StatusCode() != 404 {
			t.Fatalf("(%v) expected 404, got %d body=%s", id, rc.Response.StatusCode(), rc.Response.Body())
		}
		if got := string(rc.Response.Body()); got != sessionNotFoundBody {
			t.Fatalf("(%v) body = %s, want %s", id, got, sessionNotFoundBody)
		}
	}
	if !mem.ExistsSession("s1") {
		t.Fatal("session s1 must still exist")
	}
}

func TestDeleteSession_MissingIs404(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	rc := newDeleteContext("nope")
	setIdentity(rc, "web", "", "")
	h.DeleteSession(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Fatalf("expected 404, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := string(rc.Response.Body()); got != sessionNotFoundBody {
		t.Fatalf("body = %s, want %s", got, sessionNotFoundBody)
	}
}

func TestDeleteSession_WebAdminDeletesScheduledSession(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	// 定时任务产生的会话无租户/用户归属
	createSessionWithChat(t, mem, "sched-1", "", "")

	rc := newDeleteContext("sched-1")
	setIdentity(rc, "web", "", "")
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if mem.ExistsSession("sched-1") {
		t.Fatal("session sched-1 should be deleted")
	}
}

func TestDeleteSession_RunningCancelledThenDeleted(t *testing.T) {
	h, mem, rt := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")

	chat, err := rt.Register(context.Background(), "s1", "c1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// 模拟执行协程：感知取消后稍作清理再释放状态
	go func() {
		<-chat.Context().Done()
		time.Sleep(10 * time.Millisecond)
		rt.Delete("s1")
	}()

	rc := newDeleteContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	select {
	case <-chat.Context().Done():
	default:
		t.Fatal("active chat ctx should be cancelled")
	}
	if mem.ExistsSession("s1") {
		t.Fatal("session s1 should be deleted")
	}
	if _, ok := rt.Get("s1"); ok {
		t.Fatal("placeholder should be released after delete")
	}
}

func TestDeleteSession_RunningWaitTimeout409(t *testing.T) {
	shortenDeleteTimers(t, 30*time.Millisecond, deleteBroadcastGrace)
	h, mem, rt := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")

	if _, err := rt.Register(context.Background(), "s1", "c1"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// 执行协程不退出（不调用 Delete）

	rc := newDeleteContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 409 {
		t.Fatalf("expected 409, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	body := string(rc.Response.Body())
	for _, want := range []string{`"status":"chat_still_running"`, `"session_id":"s1"`, "对话仍在执行"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want contains %s", body, want)
		}
	}
	if !mem.ExistsSession("s1") {
		t.Fatal("session s1 must still exist")
	}
	if _, ok := rt.Get("s1"); !ok {
		t.Fatal("active chat must still be registered")
	}
}

func TestDeleteSession_ConcurrentChatBlockedByPlaceholder(t *testing.T) {
	rt := agent.NewRuntimeState()
	ph, err := rt.Register(context.Background(), "s1", "delete")
	if err != nil {
		t.Fatalf("Register placeholder: %v", err)
	}
	defer rt.Delete("s1")
	if ph.ChatID != "delete" {
		t.Fatalf("placeholder chat id = %q", ph.ChatID)
	}
	if _, err := rt.Register(context.Background(), "s1", "c2"); err == nil {
		t.Fatal("Register during delete placeholder should fail")
	}
}

// racingCanceller 在 Cancel 广播时抢先注册一条新对话，模拟续聊请求先于占位到达。
type racingCanceller struct {
	fakeCanceller
	rt *agent.RuntimeState
}

func (r *racingCanceller) Cancel(ctx context.Context, sid, cid string) (agent.CancelResult, error) {
	if _, err := r.rt.Register(context.Background(), sid, "c9"); err != nil {
		return agent.CancelNotRunning, err
	}
	return r.fakeCanceller.Cancel(ctx, sid, cid)
}

func TestDeleteSession_PlaceholderRaceReturns409(t *testing.T) {
	shortenDeleteTimers(t, deleteWaitTimeout, time.Millisecond)
	_, mem, rt := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")
	t.Cleanup(func() { rt.Delete("s1") })

	fc := &racingCanceller{fakeCanceller: fakeCanceller{res: agent.CancelBroadcast, alive: true}, rt: rt}
	h := NewSessionHandler(mem, rt, fc)

	rc := newDeleteContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 409 {
		t.Fatalf("expected 409, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), `"status":"chat_still_running"`) {
		t.Fatalf("body = %s, want chat_still_running", rc.Response.Body())
	}
	if !mem.ExistsSession("s1") {
		t.Fatal("session s1 must still exist")
	}
	assertCalls(t, &fc.fakeCanceller, [][2]string{{"s1", ""}})
	// 抢先注册的对话不能被删除流程误清理
	if c, ok := rt.Get("s1"); !ok || c.ChatID != "c9" {
		t.Fatalf("racing chat should remain registered, got %+v ok=%v", c, ok)
	}
}

func TestDeleteSession_OthersAliveBroadcastsWhenIdle(t *testing.T) {
	shortenDeleteTimers(t, deleteWaitTimeout, time.Millisecond)
	_, mem, rt := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")

	fc := &fakeCanceller{res: agent.CancelBroadcast, alive: true}
	h := NewSessionHandler(mem, rt, fc)

	rc := newDeleteContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	assertCalls(t, fc, [][2]string{{"s1", ""}})
	if mem.ExistsSession("s1") {
		t.Fatal("session s1 should be deleted")
	}
}

func TestDeleteSession_NoOthersAliveDoesNotBroadcast(t *testing.T) {
	// 宽限时间保持默认 5 秒：若误走了广播分支，耗时断言会失败
	_, mem, rt := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")

	fc := &fakeCanceller{res: agent.CancelNotRunning, alive: false}
	h := NewSessionHandler(mem, rt, fc)

	rc := newDeleteContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	start := time.Now()
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	assertCalls(t, fc, nil)
	if elapsed := time.Since(start); elapsed >= deleteBroadcastGrace {
		t.Fatalf("delete took %v, must not wait the broadcast grace %v", elapsed, deleteBroadcastGrace)
	}
	if mem.ExistsSession("s1") {
		t.Fatal("session s1 should be deleted")
	}
}

func TestDeleteSession_MissingUser400(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")

	rc := newDeleteContext("s1")
	setIdentity(rc, "svc", "crm", "")
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), `"status":"invalid_request"`) {
		t.Fatalf("body = %s, want invalid_request", rc.Response.Body())
	}
	if !mem.ExistsSession("s1") {
		t.Fatal("session s1 must still exist")
	}
}

func TestDeleteSession_MissingSid400(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	rc := newDeleteContext("")
	setIdentity(rc, "web", "", "")
	h.DeleteSession(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), `"status":"invalid_request"`) {
		t.Fatalf("body = %s, want invalid_request", rc.Response.Body())
	}
}

// ---- PATCH /sess/{sid} ----

// newPatchCtx 构造 PATCH /sess/{sid} 的 RequestContext：JSON content-type、sid path param、请求体。
// sid 为空时不注入 path param。
func newPatchCtx(sid, body string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPatch)
	rc.Request.SetRequestURI("/sess/" + sid)
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	if sid != "" {
		rc.Params = append(rc.Params, param.Param{Key: "sid", Value: sid})
	}
	rc.Request.SetBody([]byte(body))
	return rc
}

// sessionTitle 读取会话当前标题，供断言标题是否被修改。
func sessionTitle(t *testing.T, mem *memory.Manager, sid string) string {
	t.Helper()
	info, err := mem.GetSessionInfo(sid)
	if err != nil {
		t.Fatalf("GetSessionInfo(%s): %v", sid, err)
	}
	return info.Title
}

func TestRenameSession_OK(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	createSessionWithChat(t, mem, "s1", "crm", "u1")

	rc := newPatchCtx("s1", `{"title":"  新标题  "}`)
	setIdentity(rc, "svc", "crm", "u1")
	h.RenameSession(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if resp.Status != "success" || resp.SessionID != "s1" || resp.Title != "新标题" {
		t.Errorf("unexpected resp: %+v", resp)
	}
	if got := sessionTitle(t, mem, "s1"); got != "新标题" {
		t.Errorf("title = %q, want %q (trimmed)", got, "新标题")
	}
}

func TestRenameSession_RoundZeroSession(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	rc := newPatchCtx("s1", `{"title":"空会话"}`)
	setIdentity(rc, "svc", "crm", "u1")
	h.RenameSession(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	lc := newListContext("limit=10")
	setIdentity(lc, "svc", "crm", "u1")
	h.ListSessions(context.Background(), lc)
	var resp struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
			Title     string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(lc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, lc.Response.Body())
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].SessionID != "s1" || resp.Sessions[0].Title != "空会话" {
		t.Errorf("unexpected list: %+v", resp)
	}
}

func TestRenameSession_Validation400(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", "原标题"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	tooLong := strings.Repeat("中", memory.MaxTitleRunes+1)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty", `{"title":""}`, "标题不能为空"},
		{"blank", `{"title":"   "}`, "标题不能为空"},
		{"too_long", `{"title":"` + tooLong + `"}`, "标题不能为空"},
		{"number", `{"title":123}`, "请求参数错误"},
		{"not_json", `not json`, "请求参数错误"},
		{"no_title", `{}`, "标题不能为空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := newPatchCtx("s1", tc.body)
			setIdentity(rc, "svc", "crm", "u1")
			h.RenameSession(context.Background(), rc)
			if rc.Response.StatusCode() != 400 {
				t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
			}
			body := string(rc.Response.Body())
			if !strings.Contains(body, `"status":"invalid_request"`) || !strings.Contains(body, tc.want) {
				t.Fatalf("body = %s, want invalid_request containing %q", body, tc.want)
			}
			if got := sessionTitle(t, mem, "s1"); got != "原标题" {
				t.Fatalf("title changed to %q", got)
			}
		})
	}
}

func TestRenameSession_MaxLengthOK(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	exact := strings.Repeat("中", memory.MaxTitleRunes)
	rc := newPatchCtx("s1", `{"title":"`+exact+`"}`)
	setIdentity(rc, "svc", "crm", "u1")
	h.RenameSession(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := sessionTitle(t, mem, "s1"); got != exact {
		t.Fatalf("title not stored at max length")
	}
}

func TestRenameSession_ForeignIs404(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", "原标题"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, id := range [][2]string{{"erp", "u1"}, {"crm", "u2"}} {
		rc := newPatchCtx("s1", `{"title":"改"}`)
		setIdentity(rc, "svc", id[0], id[1])
		h.RenameSession(context.Background(), rc)
		if rc.Response.StatusCode() != 404 {
			t.Fatalf("(%v) expected 404, got %d body=%s", id, rc.Response.StatusCode(), rc.Response.Body())
		}
		if got := string(rc.Response.Body()); got != sessionNotFoundBody {
			t.Fatalf("(%v) body = %s, want %s", id, got, sessionNotFoundBody)
		}
	}
	if got := sessionTitle(t, mem, "s1"); got != "原标题" {
		t.Fatalf("title changed to %q", got)
	}
}

func TestRenameSession_MissingIs404(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	for _, id := range [][3]string{{"svc", "crm", "u1"}, {"web", "", ""}} {
		rc := newPatchCtx("nope", `{"title":"改"}`)
		setIdentity(rc, id[0], id[1], id[2])
		h.RenameSession(context.Background(), rc)
		if rc.Response.StatusCode() != 404 {
			t.Fatalf("(%v) expected 404, got %d body=%s", id, rc.Response.StatusCode(), rc.Response.Body())
		}
		if got := string(rc.Response.Body()); got != sessionNotFoundBody {
			t.Fatalf("(%v) body = %s, want %s", id, got, sessionNotFoundBody)
		}
	}
}

func TestRenameSession_WebAdminOK(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newPatchCtx("s1", `{"title":"管理员改名"}`)
	setIdentity(rc, "web", "", "")
	h.RenameSession(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := sessionTitle(t, mem, "s1"); got != "管理员改名" {
		t.Fatalf("title = %q", got)
	}
}

func TestRenameSession_MissingUser400(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", "原标题"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newPatchCtx("s1", `{"title":"改"}`)
	setIdentity(rc, "svc", "crm", "")
	h.RenameSession(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), `"status":"invalid_request"`) {
		t.Fatalf("body = %s, want invalid_request", rc.Response.Body())
	}
	if got := sessionTitle(t, mem, "s1"); got != "原标题" {
		t.Fatalf("title changed to %q", got)
	}
}

func TestRenameSession_MissingSid400(t *testing.T) {
	h, _, _ := newSearchTestHandler(t)
	rc := newPatchCtx("", `{"title":"改"}`)
	setIdentity(rc, "web", "", "")
	h.RenameSession(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "session_id 参数缺失") {
		t.Fatalf("body = %s, want session_id 参数缺失", rc.Response.Body())
	}
}

func TestRenameSession_DoesNotTouchUpdatedAt(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	// a 早于 b（updated_at 为毫秒精度，隔开足够的时间）
	if err := mem.CreateSession("a", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession(a): %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := mem.CreateSession("b", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession(b): %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	rc := newPatchCtx("a", `{"title":"改 a"}`)
	setIdentity(rc, "svc", "crm", "u1")
	h.RenameSession(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	lc := newListContext("limit=10")
	setIdentity(lc, "svc", "crm", "u1")
	h.ListSessions(context.Background(), lc)
	var resp listResp
	if err := json.Unmarshal(lc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, lc.Response.Body())
	}
	if len(resp.Sessions) != 2 || resp.Sessions[0].SessionID != "b" || resp.Sessions[1].SessionID != "a" {
		t.Fatalf("order after rename must stay b, a; got %+v", resp.Sessions)
	}
}

func TestGetSession_IncludesTitle(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t)
	if err := mem.CreateSession("s1", "crm", "u1", "我的标题"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rc := newSidContext("s1")
	setIdentity(rc, "svc", "crm", "u1")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		Session struct {
			Title string `json:"title"`
		} `json:"session"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if resp.Session.Title != "我的标题" {
		t.Fatalf("session.title = %q, want %q", resp.Session.Title, "我的标题")
	}
}
