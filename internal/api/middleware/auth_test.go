package middleware

import (
	"context"
	"strings"
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
		Auth: config.AuthConfig{Secret: testSecret},
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
		TenantID:    "crm",
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
		rc.Request.Header.Set("Authorization", "Bearer "+token)
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
		rc.Request.Header.Set("Authorization", "Bearer legacy-random-key")
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
		rc.Request.Header.Set("Authorization", "Bearer "+token)
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
		rc.Request.Header.Set("Authorization", "Bearer "+token)
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
		rc.Request.Header.Set("Authorization", "Bearer "+token)
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
		rc.Request.Header.Set("Authorization", "Bearer "+token)
	})
	if rc.Response.StatusCode() != 403 {
		t.Fatalf("empty permissions should 403, got %d", rc.Response.StatusCode())
	}
}

// TestAuthMiddleware_BearerForms Authorization 头必须是 Bearer <token> 形式，
// 前缀大小写不敏感，token 本体原样保留；旧 X-API-Key 头不再被读取。
func TestAuthMiddleware_BearerForms(t *testing.T) {
	m, _, _, validToken := newTestMiddleware(t)
	cases := []struct {
		name   string
		header string
		value  string
		want   int
	}{
		{"missing", "", "", 401},
		{"bearer-only", "Authorization", "Bearer", 401},
		{"bearer-spaces", "Authorization", "Bearer   ", 401},
		{"prefix-without-separator", "Authorization", "Bearer" + validToken, 401},
		{"lowercase-prefix", "Authorization", "bearer " + validToken, 200},
		{"canonical-prefix", "Authorization", "Bearer " + validToken, 200},
		{"wrong-scheme", "Authorization", "Token " + validToken, 401},
		{"legacy-header", "X-API-Key", validToken, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
				if tc.header != "" {
					rc.Request.Header.Set(tc.header, tc.value)
				}
			})
			if got := rc.Response.StatusCode(); got != tc.want {
				t.Fatalf("header %q = %q: status %d, want %d", tc.header, tc.value, got, tc.want)
			}
		})
	}
}

// TestAuthMiddleware_UnauthorizedEnvelope 401 响应体为 OpenAI 错误信封。
func TestAuthMiddleware_UnauthorizedEnvelope(t *testing.T) {
	m, _, _, _ := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {})
	if got := rc.Response.StatusCode(); got != 401 {
		t.Fatalf("expected 401, got %d", got)
	}
	if ct := string(rc.Response.Header.ContentType()); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	body := string(rc.Response.Body())
	for _, want := range []string{
		`"type":"authentication_error"`,
		`"code":"invalid_api_key"`,
		`"message":"API Key 无效或缺失"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

// TestAuthMiddleware_PermissionDeniedEnvelope Key 权限只有 status，请求 POST /chat/completions 返回 403 信封。
func TestAuthMiddleware_PermissionDeniedEnvelope(t *testing.T) {
	m, keys, _, _ := newTestMiddleware(t)
	created := time.Now().Truncate(time.Second)
	k := &repo.APIKey{
		ID: "20260902120003", Name: "status-only", Permissions: []string{"status"},
		ExpiresAt: created.AddDate(0, 0, 7), CreatedAt: created,
	}
	if err := keys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := auth.Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rc := serveAuth(m, consts.MethodPost, "/chat/completions", func(rc *app.RequestContext) {
		rc.Request.Header.Set("Authorization", "Bearer "+token)
	})
	if got := rc.Response.StatusCode(); got != 403 {
		t.Fatalf("expected 403, got %d", got)
	}
	body := string(rc.Response.Body())
	for _, want := range []string{
		`"type":"permission_error"`,
		`"code":"insufficient_permissions"`,
		`"message":"权限不足: 需要 chat 权限"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

// TestAuthMiddleware_PermissionsForNewRoutes 持 chat 权限的 Key 可访问 /chat/completions 与 /models；
// 只持 session 权限的 Key 访问 /chat/completions 返回 403。
func TestAuthMiddleware_PermissionsForNewRoutes(t *testing.T) {
	m, keys, _, chatToken := newTestMiddleware(t)
	created := time.Now().Truncate(time.Second)
	k := &repo.APIKey{
		ID: "20260902120004", Name: "session-only", Permissions: []string{"session"},
		ExpiresAt: created.AddDate(0, 0, 7), CreatedAt: created,
	}
	if err := keys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	sessionToken, err := auth.Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	cases := []struct {
		name   string
		method string
		uri    string
		token  string
		want   int
	}{
		{"chat-completions-with-chat", consts.MethodPost, "/chat/completions", chatToken, 200},
		{"models-with-chat", consts.MethodGet, "/models", chatToken, 200},
		{"chat-completions-with-session", consts.MethodPost, "/chat/completions", sessionToken, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := serveAuth(m, tc.method, tc.uri, func(rc *app.RequestContext) {
				rc.Request.Header.Set("Authorization", "Bearer "+tc.token)
			})
			if got := rc.Response.StatusCode(); got != tc.want {
				t.Fatalf("%s %s: status %d, want %d", tc.method, tc.uri, got, tc.want)
			}
		})
	}
}

// TestBearerToken 直接验证 Authorization 头解析：前缀大小写不敏感，token 原样保留。
func TestBearerToken(t *testing.T) {
	cases := []struct{ name, header, want string }{
		{"no-header", "", ""},
		{"bearer-only", "Bearer", ""},
		{"space-only", "Bearer   ", ""},
		{"canonical", "Bearer tok", "tok"},
		{"lowercase", "bearer tok", "tok"},
		{"mixed-case", "bEaReR tok", "tok"},
		{"token-whitespace-trimmed", "Bearer  tok  ", "tok"},
		{"token-case-preserved", "Bearer ToKeN", "ToKeN"},
		{"no-separator", "Bearertok", ""},
		{"wrong-scheme", "Token tok", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := app.NewContext(0)
			if tc.header != "" {
				rc.Request.Header.Set("Authorization", tc.header)
			}
			if got := bearerToken(rc); got != tc.want {
				t.Errorf("bearerToken(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// TestGetRequiredPermission 路径与方法到权限点的映射表；/audio/transcriptions、/audio/speech、/image/understanding 与 /chat 共用 chat 权限。
func TestGetRequiredPermission(t *testing.T) {
	tests := []struct {
		path   string
		method string
		want   string
	}{
		{"/chat/completions", consts.MethodPost, "chat"},
		{"/models", consts.MethodGet, "chat"},
		{"/models", consts.MethodPost, "all"},
		{"/audio/transcriptions", consts.MethodPost, "chat"},
		{"/audio/transcriptions", consts.MethodGet, "all"},
		{"/audio/speech", consts.MethodPost, "chat"},
		{"/audio/speech", consts.MethodGet, "all"},
		{"/image/understanding", consts.MethodPost, "chat"},
		{"/image/understanding", consts.MethodGet, "all"},
		{"/chat/status/x", consts.MethodGet, "status"},
		{"/chat/x", consts.MethodGet, "detail"},
		{"/chat/x/y", consts.MethodGet, "detail"},
		{"/sess/history", consts.MethodGet, "history"},
		{"/sess/x", consts.MethodGet, "session"},
		{"/schedule/1", consts.MethodDelete, "schedule"},
		{"/unknown", consts.MethodGet, "all"},
	}
	for _, tt := range tests {
		if got := getRequiredPermission(tt.path, tt.method); got != tt.want {
			t.Errorf("getRequiredPermission(%q, %q) = %q, want %q", tt.path, tt.method, got, tt.want)
		}
	}
}

// TestAuth_InjectsTenantAndUserID JWT 分支注入 Key 所属租户与 trim 后的 X-User-ID。
func TestAuth_InjectsTenantAndUserID(t *testing.T) {
	m, _, _, token := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodPost, "/chat/completions", func(rc *app.RequestContext) {
		rc.Request.Header.Set("Authorization", "Bearer "+token)
		rc.Request.Header.Set(UserIDHeader, " u1 ")
	})
	if GetTenant(rc) != "crm" || GetUserID(rc) != "u1" {
		t.Errorf("tenant=%q user=%q", GetTenant(rc), GetUserID(rc))
	}
}

// TestAuth_InvalidUserIDBecomesEmpty 非法 X-User-ID 归一为空串，中间件不因此拒绝请求。
func TestAuth_InvalidUserIDBecomesEmpty(t *testing.T) {
	m, _, _, token := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodPost, "/chat/completions", func(rc *app.RequestContext) {
		rc.Request.Header.Set("Authorization", "Bearer "+token)
		rc.Request.Header.Set(UserIDHeader, "bad id")
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("must still pass auth")
	}
	if GetUserID(rc) != "" {
		t.Errorf("user=%q want empty", GetUserID(rc))
	}
}

// TestAuth_WebCookieHasNoTenant Cookie 分支为 web 调用方，即使带 X-User-ID 也不设置 tenant 与 user_id。
func TestAuth_WebCookieHasNoTenant(t *testing.T) {
	m, _, _, _ := newTestMiddleware(t)
	token := m.webStore.Create("u1")
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("Cookie", websession.CookieName+"="+token)
		rc.Request.Header.Set(UserIDHeader, "u1")
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("valid cookie should pass auth")
	}
	if !IsWebCaller(rc) {
		t.Errorf("caller should be web, got %q", GetCaller(rc))
	}
	if GetTenant(rc) != "" || GetUserID(rc) != "" {
		t.Errorf("web caller must not have tenant/user_id, got tenant=%q user=%q", GetTenant(rc), GetUserID(rc))
	}
}

// TestGetRequiredPermission_StopAndDelete 中止对话与删除会话的权限映射，
// 且不影响既有的 session / status 映射。
func TestGetRequiredPermission_StopAndDelete(t *testing.T) {
	tests := []struct {
		path   string
		method string
		want   string
	}{
		{"/chat/s/c/stop", consts.MethodPost, "chat"},
		{"/sess/s", consts.MethodDelete, "delete"},
		{"/sess/s", consts.MethodGet, "session"},
		{"/sess/s", consts.MethodPatch, "session"},
		{"/chat/status/s", consts.MethodGet, "status"},
	}
	for _, tt := range tests {
		if got := getRequiredPermission(tt.path, tt.method); got != tt.want {
			t.Errorf("getRequiredPermission(%q, %q) = %q, want %q", tt.path, tt.method, got, tt.want)
		}
	}
}
