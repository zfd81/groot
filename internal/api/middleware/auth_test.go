package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/config"
)

func enabledAuthConfig() config.SecurityConfig {
	return config.SecurityConfig{
		Auth: config.AuthConfig{
			Enabled: true,
			Type:    "api_key",
			APIKey: config.APIKeyConfig{
				HeaderName: "X-API-Key",
				Keys:       []config.KeyInfo{{Name: "k1", Key: "valid-key", Permissions: []string{"all"}}},
			},
		},
	}
}

func serveAuth(m *AuthMiddleware, setup func(rc *app.RequestContext)) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/sess/history")
	setup(rc)
	m.Serve()(context.Background(), rc)
	return rc
}

// TestAuth_ValidCookie 有效 Web 会话 Cookie 应通过认证，caller 为 web。
func TestAuth_ValidCookie(t *testing.T) {
	store := websession.NewStore(time.Hour)
	token := store.Create()
	m := NewAuthMiddleware(enabledAuthConfig(), store)
	rc := serveAuth(m, func(rc *app.RequestContext) {
		rc.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("valid cookie should pass auth")
	}
	if GetCaller(rc) != "web" {
		t.Errorf("caller should be web, got %q", GetCaller(rc))
	}
}

// TestAuth_InvalidCookieFallsBack 无效 Cookie 且无 API Key 返回 401。
func TestAuth_InvalidCookieFallsBack(t *testing.T) {
	m := NewAuthMiddleware(enabledAuthConfig(), websession.NewStore(time.Hour))
	rc := serveAuth(m, func(rc *app.RequestContext) {
		rc.Request.Header.Set("Cookie", websession.CookieName+"=bogus")
	})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_APIKeyStillWorks 原有 API Key 路径行为不变。
func TestAuth_APIKeyStillWorks(t *testing.T) {
	m := NewAuthMiddleware(enabledAuthConfig(), websession.NewStore(time.Hour))
	rc := serveAuth(m, func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", "valid-key")
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("valid api key should pass")
	}
	if GetCaller(rc) != "k1" {
		t.Errorf("caller should be k1, got %q", GetCaller(rc))
	}
}

// TestAuth_DisabledPassesAnonymous 认证关闭时匿名放行（回归保护）。
func TestAuth_DisabledPassesAnonymous(t *testing.T) {
	m := NewAuthMiddleware(config.SecurityConfig{}, nil)
	rc := serveAuth(m, func(rc *app.RequestContext) {})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("disabled auth should pass anonymously")
	}
	if GetCaller(rc) != "anonymous" {
		t.Errorf("caller should be anonymous, got %q", GetCaller(rc))
	}
}
