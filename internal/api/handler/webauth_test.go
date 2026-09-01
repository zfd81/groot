package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"golang.org/x/crypto/bcrypt"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/userdb"
)

func newWebAuthForTest(t *testing.T) *WebAuthHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	users := userdb.New(sqlxDB, dialect)
	return NewWebAuthHandler(users, websession.NewStore(time.Hour), logger.NewNop())
}

// seedUser 直接向用户表写入一个 bcrypt 密码的用户，返回其 ID
func seedUser(t *testing.T, h *WebAuthHandler, username, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	now := time.Now()
	u := &repo.User{
		ID:           now.Format("20060102150405"),
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.users.Create(context.Background(), u); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func postJSON(h func(context.Context, *app.RequestContext), body string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.SetBody([]byte(body))
	h(context.Background(), rc)
	return rc
}

func postJSONWithCookie(h func(context.Context, *app.RequestContext), body, token string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	rc.Request.SetBody([]byte(body))
	h(context.Background(), rc)
	return rc
}

// TestWebSetup_Success 空表时创建用户成功：ID 为 14 位时间编号，密码为可校验的 bcrypt。
func TestWebSetup_Success(t *testing.T) {
	h := newWebAuthForTest(t)
	rc := postJSON(h.Setup, `{"username":"admin","password":"secret123"}`)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	u, err := h.users.GetByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("user should exist: %v", err)
	}
	if len(u.ID) != 14 {
		t.Errorf("ID should be 14-digit yyyyMMddHHmmss, got %q", u.ID)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("secret123")) != nil {
		t.Error("stored hash should verify the original password")
	}
	if u.LastLoginAt != nil {
		t.Error("LastLoginAt should be nil before first login")
	}
}

// TestWebSetup_AlreadyInitialized 表非空时创建返回 409。
func TestWebSetup_AlreadyInitialized(t *testing.T) {
	h := newWebAuthForTest(t)
	seedUser(t, h, "admin", "secret123")
	rc := postJSON(h.Setup, `{"username":"other","password":"secret123"}`)
	if rc.Response.StatusCode() != 409 {
		t.Fatalf("expected 409, got %d", rc.Response.StatusCode())
	}
}

// TestWebSetup_InvalidInput 密码过短或用户名为空返回 400。
func TestWebSetup_InvalidInput(t *testing.T) {
	h := newWebAuthForTest(t)
	if rc := postJSON(h.Setup, `{"username":"admin","password":"short"}`); rc.Response.StatusCode() != 400 {
		t.Errorf("short password: expected 400, got %d", rc.Response.StatusCode())
	}
	if rc := postJSON(h.Setup, `{"username":"  ","password":"secret123"}`); rc.Response.StatusCode() != 400 {
		t.Errorf("blank username: expected 400, got %d", rc.Response.StatusCode())
	}
	if n, _ := h.users.Count(context.Background()); n != 0 {
		t.Errorf("no user should be created, count=%d", n)
	}
}

// TestWebLogin_Success 正确凭证返回 200、下发 HttpOnly Cookie 并更新最后登录时间。
func TestWebLogin_Success(t *testing.T) {
	h := newWebAuthForTest(t)
	id := seedUser(t, h, "admin", "secret123")
	rc := postJSON(h.Login, `{"username":"admin","password":"secret123"}`)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	setCookie := rc.Response.Header.Get("Set-Cookie")
	if !strings.Contains(setCookie, websession.CookieName+"=") || !strings.Contains(setCookie, "HttpOnly") {
		t.Errorf("Set-Cookie should contain session cookie with HttpOnly, got %q", setCookie)
	}
	u, err := h.users.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if u.LastLoginAt == nil {
		t.Error("LastLoginAt should be updated after login")
	}
}

// TestWebLogin_WrongPassword 错误密码与不存在的用户名均返回 401。
func TestWebLogin_WrongPassword(t *testing.T) {
	h := newWebAuthForTest(t)
	seedUser(t, h, "admin", "secret123")
	if rc := postJSON(h.Login, `{"username":"admin","password":"wrong-pass"}`); rc.Response.StatusCode() != 401 {
		t.Errorf("wrong password: expected 401, got %d", rc.Response.StatusCode())
	}
	if rc := postJSON(h.Login, `{"username":"nobody","password":"secret123"}`); rc.Response.StatusCode() != 401 {
		t.Errorf("unknown user: expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestWebLogin_Lockout 连续 5 次失败后第 6 次返回 429。
func TestWebLogin_Lockout(t *testing.T) {
	h := newWebAuthForTest(t)
	seedUser(t, h, "admin", "secret123")
	for i := 0; i < 5; i++ {
		postJSON(h.Login, `{"username":"admin","password":"wrong-pass"}`)
	}
	rc := postJSON(h.Login, `{"username":"admin","password":"secret123"}`)
	if rc.Response.StatusCode() != 429 {
		t.Fatalf("expected 429 after lockout, got %d", rc.Response.StatusCode())
	}
}

// TestWebLogin_SecureCookie 经 https 到达（X-Forwarded-Proto）时 Cookie 置 Secure，否则不置。
func TestWebLogin_SecureCookie(t *testing.T) {
	h := newWebAuthForTest(t)
	seedUser(t, h, "admin", "secret123")

	rc := postJSON(h.Login, `{"username":"admin","password":"secret123"}`)
	if strings.Contains(rc.Response.Header.Get("Set-Cookie"), "secure") ||
		strings.Contains(rc.Response.Header.Get("Set-Cookie"), "Secure") {
		t.Errorf("plain http should not set Secure cookie, got %q", rc.Response.Header.Get("Set-Cookie"))
	}

	rc2 := app.NewContext(0)
	rc2.Request.Header.SetMethod(consts.MethodPost)
	rc2.Request.Header.Set("X-Forwarded-Proto", "https")
	rc2.Request.SetBody([]byte(`{"username":"admin","password":"secret123"}`))
	h.Login(context.Background(), rc2)
	if !strings.Contains(strings.ToLower(rc2.Response.Header.Get("Set-Cookie")), "secure") {
		t.Errorf("https via proxy should set Secure cookie, got %q", rc2.Response.Header.Get("Set-Cookie"))
	}
}

// TestWebMe 三态：空表 needs_setup=true；有用户未登录 authenticated=false；
// 携带有效 Cookie 后 authenticated=true 且返回 username。
func TestWebMe(t *testing.T) {
	h := newWebAuthForTest(t)

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Me(context.Background(), rc)
	body := string(rc.Response.Body())
	if !strings.Contains(body, `"needs_setup":true`) || !strings.Contains(body, `"authenticated":false`) {
		t.Errorf("empty table me body: %s", body)
	}

	id := seedUser(t, h, "admin", "secret123")
	rc2 := app.NewContext(0)
	rc2.Request.Header.SetMethod(consts.MethodGet)
	h.Me(context.Background(), rc2)
	body = string(rc2.Response.Body())
	if !strings.Contains(body, `"needs_setup":false`) || !strings.Contains(body, `"authenticated":false`) ||
		!strings.Contains(body, `"auth_required":true`) {
		t.Errorf("unauthenticated me body: %s", body)
	}

	token := h.store.Create(id)
	rc3 := app.NewContext(0)
	rc3.Request.Header.SetMethod(consts.MethodGet)
	rc3.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	h.Me(context.Background(), rc3)
	body = string(rc3.Response.Body())
	if !strings.Contains(body, `"authenticated":true`) || !strings.Contains(body, `"username":"admin"`) {
		t.Errorf("authenticated me body: %s", body)
	}
}

// TestWebLogout 注销后原令牌失效。
func TestWebLogout(t *testing.T) {
	h := newWebAuthForTest(t)
	token := h.store.Create("u1")
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	h.Logout(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
	}
	if _, ok := h.store.Validate(token); ok {
		t.Error("token should be invalid after logout")
	}
}

// TestWebChangePassword 全分支：无会话 401、原密码错 401、新密码短 400、
// 成功后新密码可登录、旧密码失效、其他会话被踢而当前会话保留。
func TestWebChangePassword(t *testing.T) {
	h := newWebAuthForTest(t)
	id := seedUser(t, h, "admin", "secret123")

	// 无会话
	if rc := postJSON(h.ChangePassword, `{"old_password":"secret123","new_password":"newsecret1"}`); rc.Response.StatusCode() != 401 {
		t.Errorf("no session: expected 401, got %d", rc.Response.StatusCode())
	}

	token := h.store.Create(id)
	other := h.store.Create(id)

	// 原密码错误
	if rc := postJSONWithCookie(h.ChangePassword, `{"old_password":"wrong-old","new_password":"newsecret1"}`, token); rc.Response.StatusCode() != 401 {
		t.Errorf("wrong old password: expected 401, got %d", rc.Response.StatusCode())
	}

	// 新密码过短
	if rc := postJSONWithCookie(h.ChangePassword, `{"old_password":"secret123","new_password":"short"}`, token); rc.Response.StatusCode() != 400 {
		t.Errorf("short new password: expected 400, got %d", rc.Response.StatusCode())
	}

	// 成功
	if rc := postJSONWithCookie(h.ChangePassword, `{"old_password":"secret123","new_password":"newsecret1"}`, token); rc.Response.StatusCode() != 200 {
		t.Fatalf("change password: expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	// 其他会话被踢，当前会话保留
	if _, ok := h.store.Validate(other); ok {
		t.Error("other session should be kicked after password change")
	}
	if _, ok := h.store.Validate(token); !ok {
		t.Error("current session should survive password change")
	}

	// 旧密码登录失败，新密码登录成功
	if rc := postJSON(h.Login, `{"username":"admin","password":"secret123"}`); rc.Response.StatusCode() != 401 {
		t.Errorf("old password should no longer work, got %d", rc.Response.StatusCode())
	}
	if rc := postJSON(h.Login, `{"username":"admin","password":"newsecret1"}`); rc.Response.StatusCode() != 200 {
		t.Errorf("new password should work, got %d", rc.Response.StatusCode())
	}
}
