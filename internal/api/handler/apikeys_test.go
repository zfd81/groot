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

	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/apikeydb"
)

const testAuthSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newAPIKeysHandler(t *testing.T) (*APIKeysHandler, repo.APIKeyRepo) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	keys := apikeydb.New(sqlxDB, dialect)
	cfg := config.SecurityConfig{Auth: config.AuthConfig{Secret: testAuthSecret}}
	return NewAPIKeysHandler(keys, cfg, logger.NewNop()), keys
}

// setParamID 注入 :id path param，写法与 status_test.go 保持一致。
func setParamID(rc *app.RequestContext, id string) {
	rc.Params = append(rc.Params, param.Param{Key: "id", Value: id})
}

// TestAPIKeys_CreateAndVerify 创建成功返回可验证的 token，且库存元数据能还原出同一 token。
func TestAPIKeys_CreateAndVerify(t *testing.T) {
	h, keys := newAPIKeysHandler(t)
	rc := postJSON(h.Create, `{"name":"svc-a","expires_in":"7d","permissions":["chat","status"]}`)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("Create status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if len(resp.ID) != 14 {
		t.Errorf("id should be yyyyMMddHHmmss, got %q", resp.ID)
	}
	jti, err := auth.Verify(resp.Token, testAuthSecret)
	if err != nil || jti != resp.ID {
		t.Errorf("token not verifiable: %v, jti=%q", err, jti)
	}
	// 还原恒等性：库存元数据 + secret 重签 == 创建时返回的 token
	stored, err := keys.GetByID(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	restored, err := auth.Sign(stored, testAuthSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if restored != resp.Token {
		t.Errorf("restored token differs:\n%s\n%s", restored, resp.Token)
	}
}

// TestAPIKeys_CreateValidation 非法参数各返回 400。
func TestAPIKeys_CreateValidation(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	cases := []string{
		`{"name":"","expires_in":"7d","permissions":["chat"]}`,                                // 空名称
		`{"name":"a","expires_in":"3d","permissions":["chat"]}`,                               // 非法 expires_in
		`{"name":"a","expires_in":"7d","permissions":[]}`,                                     // 空权限
		`{"name":"a","expires_in":"7d","permissions":["superuser"]}`,                          // 非法权限点
		`{"name":"web","expires_in":"7d","permissions":["chat"]}`,                             // 系统保留名称
		`{"name":"` + strings.Repeat("a", 65) + `","expires_in":"7d","permissions":["chat"]}`, // 名称超过 64 字符
	}
	for _, body := range cases {
		if rc := postJSON(h.Create, body); rc.Response.StatusCode() != 400 {
			t.Errorf("body %s: status=%d, want 400", body, rc.Response.StatusCode())
		}
	}
}

// TestAPIKeys_CreateDuplicateName 重名返回 409。
func TestAPIKeys_CreateDuplicateName(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	postJSON(h.Create, `{"name":"svc-a","expires_in":"7d","permissions":["chat"]}`)
	rc := postJSON(h.Create, `{"name":"svc-a","expires_in":"1d","permissions":["all"]}`)
	if rc.Response.StatusCode() != 409 {
		t.Fatalf("duplicate name status=%d, want 409", rc.Response.StatusCode())
	}
}

// TestAPIKeys_CreateSameSecondRetry 同秒创建两把 Key，主键 +1 秒重试后都成功。
func TestAPIKeys_CreateSameSecondRetry(t *testing.T) {
	h, keys := newAPIKeysHandler(t)
	fixed := time.Date(2026, 9, 2, 12, 0, 0, 500e6, time.Local)
	origNow := apiKeyNow
	apiKeyNow = func() time.Time { return fixed }
	t.Cleanup(func() { apiKeyNow = origNow })

	if rc := postJSON(h.Create, `{"name":"a","expires_in":"7d","permissions":["chat"]}`); rc.Response.StatusCode() != 200 {
		t.Fatalf("first create: %d", rc.Response.StatusCode())
	}
	if rc := postJSON(h.Create, `{"name":"b","expires_in":"7d","permissions":["chat"]}`); rc.Response.StatusCode() != 200 {
		t.Fatalf("second create same second: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	list, err := keys.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}
	if list[0].ID == list[1].ID {
		t.Error("ids should differ after retry")
	}
}

// TestAPIKeys_ListAndExpired 列表包含过期状态计算。
func TestAPIKeys_ListAndExpired(t *testing.T) {
	h, keys := newAPIKeysHandler(t)
	created := time.Now().AddDate(0, 0, -8).Truncate(time.Second)
	expired := &repo.APIKey{
		ID: "20260825120000", Name: "old", Permissions: []string{"chat"},
		ExpiresAt: created.AddDate(0, 0, 7), CreatedAt: created,
	}
	if err := keys.Create(context.Background(), expired); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/web/apikeys")
	h.List(context.Background(), rc)
	var resp struct {
		Keys []struct {
			Name    string `json:"name"`
			Expired bool   `json:"expired"`
		} `json:"keys"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("bad response: %v body=%s", err, rc.Response.Body())
	}
	if resp.Total != 1 || !resp.Keys[0].Expired {
		t.Errorf("expected 1 expired key, got %+v", resp)
	}
}

// TestAPIKeys_DeleteNotFound 删除不存在的 id 返回 404。
func TestAPIKeys_DeleteNotFound(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodDelete)
	setParamID(rc, "19700101000000")
	h.Delete(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Errorf("delete missing id = %d, want 404", rc.Response.StatusCode())
	}
}

// TestAPIKeys_CreateTrimsName 名称去除首尾空白后入库，重名判定基于去空白后的名称。
func TestAPIKeys_CreateTrimsName(t *testing.T) {
	h, keys := newAPIKeysHandler(t)
	if rc := postJSON(h.Create, `{"name":"  svc-a  ","expires_in":"7d","permissions":["chat"]}`); rc.Response.StatusCode() != 200 {
		t.Fatalf("create with padded name: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if _, err := keys.GetByName(context.Background(), "svc-a"); err != nil {
		t.Fatalf("GetByName(svc-a) after trimmed create: %v", err)
	}
	if rc := postJSON(h.Create, `{"name":"svc-a","expires_in":"7d","permissions":["chat"]}`); rc.Response.StatusCode() != 409 {
		t.Errorf("create with same trimmed name = %d, want 409", rc.Response.StatusCode())
	}
}

// TestAPIKeys_CreateMalformedJSON 非法 JSON 请求体返回 400。
func TestAPIKeys_CreateMalformedJSON(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	if rc := postJSON(h.Create, `{not json`); rc.Response.StatusCode() != 400 {
		t.Errorf("malformed json = %d, want 400", rc.Response.StatusCode())
	}
}

// TestAPIKeys_TokenAndDelete 查看 token 与删除端点。
func TestAPIKeys_TokenAndDelete(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	rcCreate := postJSON(h.Create, `{"name":"svc-a","expires_in":"7d","permissions":["chat"]}`)
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rcCreate.Response.Body(), &created); err != nil {
		t.Fatalf("bad create response: %v", err)
	}

	// GET /web/apikeys/:id/token 还原出同一 token
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/web/apikeys/" + created.ID + "/token")
	setParamID(rc, created.ID)
	h.Token(context.Background(), rc)
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &tokenResp); err != nil {
		t.Fatalf("bad token response: %v body=%s", err, rc.Response.Body())
	}
	if tokenResp.Token != created.Token {
		t.Error("restored token should equal original")
	}

	// DELETE 后再查 token 返回 404
	rcDel := app.NewContext(0)
	rcDel.Request.Header.SetMethod(consts.MethodDelete)
	setParamID(rcDel, created.ID)
	h.Delete(context.Background(), rcDel)
	if rcDel.Response.StatusCode() != 200 {
		t.Fatalf("Delete: %d", rcDel.Response.StatusCode())
	}
	rcGone := app.NewContext(0)
	setParamID(rcGone, created.ID)
	h.Token(context.Background(), rcGone)
	if rcGone.Response.StatusCode() != 404 {
		t.Errorf("token after delete = %d, want 404", rcGone.Response.StatusCode())
	}
}
