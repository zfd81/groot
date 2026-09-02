package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/apikeydb"
)

const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testSecurityConfig() config.SecurityConfig {
	return config.SecurityConfig{
		Auth: config.AuthConfig{HeaderName: "X-API-Key", Secret: testSecret},
	}
}

// newTestMiddleware 返回中间件、真实 SQLite 仓库、预置的 chat+history 权限 Key 及其 token。
func newTestMiddleware(t *testing.T) (*AuthMiddleware, repo.APIKeyRepo, *repo.APIKey, string) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	keys := apikeydb.New(sqlxDB, dialect)
	created := time.Now().Truncate(time.Second)
	k := &repo.APIKey{
		ID:          "20260902120000",
		Name:        "svc-a",
		Permissions: []string{"chat", "history"},
		ExpiresAt:   created.AddDate(0, 0, 7),
		CreatedAt:   created,
	}
	if err := keys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create key: %v", err)
	}
	token, err := auth.Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	m := NewAuthMiddleware(testSecurityConfig(), websession.NewStore(time.Hour), keys, logger.NewNop())
	return m, keys, k, token
}

func serveAuth(m *AuthMiddleware, method, uri string, setup func(rc *app.RequestContext)) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	rc.Request.SetRequestURI(uri)
	setup(rc)
	m.Serve()(context.Background(), rc)
	return rc
}

// TestAuth_ValidCookie 有效 Web 会话 Cookie 通过认证，caller 为 web。
func TestAuth_ValidCookie(t *testing.T) {
	m, _, _, _ := newTestMiddleware(t)
	token := m.webStore.Create("u1")
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("valid cookie should pass auth")
	}
	if GetCaller(rc) != "web" {
		t.Errorf("caller should be web, got %q", GetCaller(rc))
	}
}

// TestAuth_MissingToken 无凭证返回 401（认证始终开启，不存在匿名放行）。
func TestAuth_MissingToken(t *testing.T) {
	m, _, _, _ := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_InvalidCookieFallsBack 无效 Cookie 且无 token 返回 401。
func TestAuth_InvalidCookieFallsBack(t *testing.T) {
	m, _, _, _ := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("Cookie", websession.CookieName+"=bogus")
	})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_ValidToken 有效 JWT 通过，caller 为 Key 名称。
func TestAuth_ValidToken(t *testing.T) {
	m, _, _, token := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("valid token should pass")
	}
	if GetCaller(rc) != "svc-a" {
		t.Errorf("caller should be svc-a, got %q", GetCaller(rc))
	}
}

// TestAuth_InvalidToken 非法字符串（含旧版随机串 Key）返回 401。
func TestAuth_InvalidToken(t *testing.T) {
	m, _, _, _ := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", "legacy-random-key")
	})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_RevokedToken 删除数据库行后原 token 立即失效（删除即吊销）。
func TestAuth_RevokedToken(t *testing.T) {
	m, keys, key, token := newTestMiddleware(t)
	if err := keys.DeleteByID(context.Background(), key.ID); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("revoked token should 401, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_PermissionDenied Key 无所需权限点返回 403。
func TestAuth_PermissionDenied(t *testing.T) {
	m, _, _, token := newTestMiddleware(t)
	// 预置 Key 只有 chat+history，访问 /schedule 需要 schedule 权限
	rc := serveAuth(m, consts.MethodGet, "/schedule", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if rc.Response.StatusCode() != 403 {
		t.Fatalf("expected 403, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_AllPermission all 权限可访问任意端点。
func TestAuth_AllPermission(t *testing.T) {
	m, keys, _, _ := newTestMiddleware(t)
	created := time.Now().Truncate(time.Second)
	k := &repo.APIKey{
		ID: "20260902120001", Name: "admin", Permissions: []string{"all"},
		ExpiresAt: created.AddDate(0, 0, 7), CreatedAt: created,
	}
	if err := keys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := auth.Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rc := serveAuth(m, consts.MethodGet, "/schedule", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if code := rc.Response.StatusCode(); code == 401 || code == 403 {
		t.Fatalf("all permission should pass, got %d", code)
	}
}

// TestAuth_EmptyPermissionsDenied 空权限集合一律拒绝（脏数据兜底，不再视为全权限）。
func TestAuth_EmptyPermissionsDenied(t *testing.T) {
	m, keys, _, _ := newTestMiddleware(t)
	created := time.Now().Truncate(time.Second)
	k := &repo.APIKey{
		ID: "20260902120002", Name: "empty", Permissions: []string{},
		ExpiresAt: created.AddDate(0, 0, 7), CreatedAt: created,
	}
	if err := keys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := auth.Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if rc.Response.StatusCode() != 403 {
		t.Fatalf("empty permissions should 403, got %d", rc.Response.StatusCode())
	}
}
