package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/logger"
)

// fakeCanceller 可控的 ChatCanceller：记录调用参数并返回预设结果。
type fakeCanceller struct {
	res   agent.CancelResult
	err   error
	alive bool
	calls [][2]string
}

func (f *fakeCanceller) OthersAlive(_ context.Context) bool { return f.alive }

func (f *fakeCanceller) Cancel(_ context.Context, sid, cid string) (agent.CancelResult, error) {
	f.calls = append(f.calls, [2]string{sid, cid})
	return f.res, f.err
}

// newStopCtx 构造 POST /chat/{sid}/{cid}/stop 的 RequestContext；空参数不注入对应 path param。
func newStopCtx(sid, cid string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.SetRequestURI("/chat/" + sid + "/" + cid + "/stop")
	if sid != "" {
		rc.Params = append(rc.Params, param.Param{Key: "sid", Value: sid})
	}
	if cid != "" {
		rc.Params = append(rc.Params, param.Param{Key: "cid", Value: cid})
	}
	return rc
}

func newStopHandlerForTest(t *testing.T, fc *fakeCanceller) (*StopHandler, func(sid, tenant, user string)) {
	t.Helper()
	_, mem, _ := newSearchTestHandler(t)
	create := func(sid, tenant, user string) {
		if err := mem.CreateSession(sid, tenant, user, ""); err != nil {
			t.Fatalf("CreateSession(%s): %v", sid, err)
		}
	}
	return NewStopHandler(fc, mem, logger.NewNop()), create
}

func assertCalls(t *testing.T, fc *fakeCanceller, want [][2]string) {
	t.Helper()
	if len(fc.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", fc.calls, want)
	}
	for i := range want {
		if fc.calls[i] != want[i] {
			t.Fatalf("calls[%d] = %v, want %v", i, fc.calls[i], want[i])
		}
	}
}

func TestStop_LocalHit200(t *testing.T) {
	fc := &fakeCanceller{res: agent.CancelLocal}
	h, create := newStopHandlerForTest(t, fc)
	create("s1", "crm", "u1")

	rc := newStopCtx("s1", "c1")
	setIdentity(rc, "svc", "crm", "u1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	body := string(rc.Response.Body())
	for _, want := range []string{`"status":"success"`, `"session_id":"s1"`, `"chat_id":"c1"`, "对话已请求中止"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want contains %s", body, want)
		}
	}
	assertCalls(t, fc, [][2]string{{"s1", "c1"}})
}

func TestStop_MissBroadcast202(t *testing.T) {
	fc := &fakeCanceller{res: agent.CancelBroadcast}
	h, create := newStopHandlerForTest(t, fc)
	create("s1", "crm", "u1")

	rc := newStopCtx("s1", "c1")
	setIdentity(rc, "svc", "crm", "u1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 202 {
		t.Fatalf("status = %d, want 202; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	body := string(rc.Response.Body())
	for _, want := range []string{`"status":"accepted"`, `"session_id":"s1"`, `"chat_id":"c1"`, "中止请求已广播到集群"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want contains %s", body, want)
		}
	}
	assertCalls(t, fc, [][2]string{{"s1", "c1"}})
}

func TestStop_MissNoBroadcast409(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"no error", nil},
		{"with send error", errors.New("send failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeCanceller{res: agent.CancelNotRunning, err: tc.err}
			h, create := newStopHandlerForTest(t, fc)
			create("s1", "crm", "u1")

			rc := newStopCtx("s1", "c1")
			setIdentity(rc, "svc", "crm", "u1")
			h.Serve(context.Background(), rc)

			if rc.Response.StatusCode() != 409 {
				t.Fatalf("status = %d, want 409; body=%s", rc.Response.StatusCode(), rc.Response.Body())
			}
			body := string(rc.Response.Body())
			for _, want := range []string{`"status":"chat_not_running"`, `"session_id":"s1"`, `"chat_id":"c1"`, "该对话未在执行"} {
				if !strings.Contains(body, want) {
					t.Fatalf("body = %s, want contains %s", body, want)
				}
			}
			assertCalls(t, fc, [][2]string{{"s1", "c1"}})
		})
	}
}

func TestStop_ForeignIs404(t *testing.T) {
	fc := &fakeCanceller{res: agent.CancelLocal}
	h, create := newStopHandlerForTest(t, fc)
	create("s1", "crm", "u1")

	for _, id := range [][2]string{{"erp", "u1"}, {"crm", "u2"}} {
		rc := newStopCtx("s1", "c1")
		setIdentity(rc, "svc", id[0], id[1])
		h.Serve(context.Background(), rc)

		if rc.Response.StatusCode() != 404 {
			t.Fatalf("(%v) status = %d, want 404; body=%s", id, rc.Response.StatusCode(), rc.Response.Body())
		}
		want := `{"status":"session_not_found","message":"会话不存在"}`
		if got := string(rc.Response.Body()); got != want {
			t.Fatalf("(%v) body = %s, want %s", id, got, want)
		}
	}
	assertCalls(t, fc, nil)
}

func TestStop_MissingSession404(t *testing.T) {
	fc := &fakeCanceller{res: agent.CancelLocal}
	h, _ := newStopHandlerForTest(t, fc)

	rc := newStopCtx("nope", "c1")
	setIdentity(rc, "web", "", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 404 {
		t.Fatalf("status = %d, want 404; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	assertCalls(t, fc, nil)
}

func TestStop_MissingUser400(t *testing.T) {
	fc := &fakeCanceller{res: agent.CancelLocal}
	h, create := newStopHandlerForTest(t, fc)
	create("s1", "crm", "u1")

	rc := newStopCtx("s1", "c1")
	setIdentity(rc, "svc", "crm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status = %d, want 400; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), `"status":"invalid_request"`) {
		t.Fatalf("body = %s, want invalid_request", rc.Response.Body())
	}
	assertCalls(t, fc, nil)
}

func TestStop_EmptyTenant403(t *testing.T) {
	fc := &fakeCanceller{res: agent.CancelLocal}
	h, create := newStopHandlerForTest(t, fc)
	create("s1", "crm", "u1")

	rc := newStopCtx("s1", "c1")
	setIdentity(rc, "svc", "", "u1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 403 {
		t.Fatalf("status = %d, want 403; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	assertCalls(t, fc, nil)
}

func TestStop_WebAdminBypasses(t *testing.T) {
	fc := &fakeCanceller{res: agent.CancelLocal}
	h, create := newStopHandlerForTest(t, fc)
	create("s1", "crm", "u1")

	rc := newStopCtx("s1", "c1")
	setIdentity(rc, "web", "", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	assertCalls(t, fc, [][2]string{{"s1", "c1"}})
}

func TestStop_MissingParams400(t *testing.T) {
	for _, tc := range [][2]string{{"s1", ""}, {"", "c1"}} {
		fc := &fakeCanceller{res: agent.CancelLocal}
		h, _ := newStopHandlerForTest(t, fc)

		rc := newStopCtx(tc[0], tc[1])
		setIdentity(rc, "web", "", "")
		h.Serve(context.Background(), rc)

		if rc.Response.StatusCode() != 400 {
			t.Fatalf("(%v) status = %d, want 400; body=%s", tc, rc.Response.StatusCode(), rc.Response.Body())
		}
		if !strings.Contains(string(rc.Response.Body()), `"status":"invalid_request"`) {
			t.Fatalf("(%v) body = %s, want invalid_request", tc, rc.Response.Body())
		}
		assertCalls(t, fc, nil)
	}
}
