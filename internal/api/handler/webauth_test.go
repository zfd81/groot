package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

func newWebAuthForTest(enabled bool) *WebAuthHandler {
	cfg := config.WebConfig{Enabled: enabled, Username: "admin", Password: "secret", SessionTTL: "1h"}
	return NewWebAuthHandler(cfg, websession.NewStore(time.Hour), logger.NewNop())
}

func postJSON(h func(context.Context, *app.RequestContext), body string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.SetBody([]byte(body))
	h(context.Background(), rc)
	return rc
}

// TestWebLogin_Success 正确凭证返回 200 并下发 HttpOnly Cookie。
func TestWebLogin_Success(t *testing.T) {
	h := newWebAuthForTest(true)
	rc := postJSON(h.Login, `{"username":"admin","password":"secret"}`)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	setCookie := rc.Response.Header.Get("Set-Cookie")
	if !strings.Contains(setCookie, websession.CookieName+"=") || !strings.Contains(setCookie, "HttpOnly") {
		t.Errorf("Set-Cookie should contain session cookie with HttpOnly, got %q", setCookie)
	}
}

// TestWebLogin_WrongPassword 错误密码返回 401。
func TestWebLogin_WrongPassword(t *testing.T) {
	h := newWebAuthForTest(true)
	rc := postJSON(h.Login, `{"username":"admin","password":"wrong"}`)
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestWebLogin_EmptyConfiguredPassword 配置密码为空时一律拒绝（防止空密码直通）。
func TestWebLogin_EmptyConfiguredPassword(t *testing.T) {
	cfg := config.WebConfig{Enabled: true, Username: "admin", Password: ""}
	h := NewWebAuthHandler(cfg, websession.NewStore(time.Hour), logger.NewNop())
	rc := postJSON(h.Login, `{"username":"admin","password":""}`)
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401 for empty configured password, got %d", rc.Response.StatusCode())
	}
}

// TestWebLogin_Lockout 连续 5 次失败后第 6 次返回 429。
func TestWebLogin_Lockout(t *testing.T) {
	h := newWebAuthForTest(true)
	for i := 0; i < 5; i++ {
		postJSON(h.Login, `{"username":"admin","password":"wrong"}`)
	}
	rc := postJSON(h.Login, `{"username":"admin","password":"secret"}`)
	if rc.Response.StatusCode() != 429 {
		t.Fatalf("expected 429 after lockout, got %d", rc.Response.StatusCode())
	}
}

// TestWebMe 登录前 authenticated=false；携带有效 Cookie 后为 true。
func TestWebMe(t *testing.T) {
	h := newWebAuthForTest(true)
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Me(context.Background(), rc)
	body := string(rc.Response.Body())
	if !strings.Contains(body, `"authenticated":false`) || !strings.Contains(body, `"auth_required":true`) {
		t.Errorf("unexpected me body: %s", body)
	}

	token := h.store.Create()
	rc2 := app.NewContext(0)
	rc2.Request.Header.SetMethod(consts.MethodGet)
	rc2.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	h.Me(context.Background(), rc2)
	if !strings.Contains(string(rc2.Response.Body()), `"authenticated":true`) {
		t.Errorf("me with valid cookie should be authenticated, got %s", rc2.Response.Body())
	}
}

// TestWebMe_AuthDisabled 认证关闭时 authenticated=true、auth_required=false。
func TestWebMe_AuthDisabled(t *testing.T) {
	h := newWebAuthForTest(false)
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Me(context.Background(), rc)
	body := string(rc.Response.Body())
	if !strings.Contains(body, `"authenticated":true`) || !strings.Contains(body, `"auth_required":false`) {
		t.Errorf("unexpected me body: %s", body)
	}
}

// TestWebLogout 注销后原令牌失效。
func TestWebLogout(t *testing.T) {
	h := newWebAuthForTest(true)
	token := h.store.Create()
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	h.Logout(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
	}
	if h.store.Validate(token) {
		t.Error("token should be invalid after logout")
	}
}

// TestWebAuth_NilStore 认证关闭时 store 为 nil（server 的真实装配），
// 各端点均不得空指针 panic。
func TestWebAuth_NilStore(t *testing.T) {
	cfg := config.WebConfig{Enabled: false}
	h := NewWebAuthHandler(cfg, nil, logger.NewNop())

	// Logout 带 Cookie 也不得触碰 nil store
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.Set("Cookie", websession.CookieName+"=whatever")
	h.Logout(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Errorf("logout: expected 200, got %d", rc.Response.StatusCode())
	}

	// Login 应在触碰 store 前短路
	rc = app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.SetBody([]byte(`{"username":"admin","password":"x"}`))
	h.Login(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Errorf("login: expected 200, got %d", rc.Response.StatusCode())
	}

	// Me 应在触碰 store 前短路
	rc = app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Me(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Errorf("me: expected 200, got %d", rc.Response.StatusCode())
	}
}
