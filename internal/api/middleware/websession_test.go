package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/websession"
)

func serveWebSession(store *websession.Store, setup func(rc *app.RequestContext)) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/web/agents")
	setup(rc)
	WebSession(store)(context.Background(), rc)
	return rc
}

// TestWebSession_NoCookie 无 Cookie 返回 401。
func TestWebSession_NoCookie(t *testing.T) {
	store := websession.NewStore(time.Hour)
	rc := serveWebSession(store, func(rc *app.RequestContext) {})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestWebSession_InvalidToken 无效令牌返回 401。
func TestWebSession_InvalidToken(t *testing.T) {
	store := websession.NewStore(time.Hour)
	rc := serveWebSession(store, func(rc *app.RequestContext) {
		rc.Request.Header.Set("Cookie", websession.CookieName+"=bogus")
	})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestWebSession_ValidToken 有效令牌放行，注入 caller=web 与 web_user_id。
func TestWebSession_ValidToken(t *testing.T) {
	store := websession.NewStore(time.Hour)
	token := store.Create("u1")
	rc := serveWebSession(store, func(rc *app.RequestContext) {
		rc.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("valid token should pass")
	}
	if GetCaller(rc) != "web" {
		t.Errorf("caller should be web, got %q", GetCaller(rc))
	}
	uid, _ := rc.Get("web_user_id")
	if uid != "u1" {
		t.Errorf("web_user_id should be u1, got %v", uid)
	}
}
