// internal/api/handler/auth_setting_test.go
// 认证配置接口的单元测试：脱敏展示、请求头名读写与校验、密钥重新生成。
package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/settingdb"
	"github.com/zfd81/groot/internal/setting"
)

// newAuthSettingHandlerForTest 建 handler，同时返回配置表仓库，
// 供测试预置密钥与直接查表核对。
func newAuthSettingHandlerForTest(t *testing.T) (*SettingHandler, repo.SettingRepo) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	settingRepo := settingdb.New(sqlxDB, dialect)
	settings := setting.New(config.Bootstrap{}, settingRepo)
	h := NewSettingHandler(SettingHandlerDeps{
		Settings: settings,
		Log:      logger.NewNop(),
	})
	return h, settingRepo
}

// getAuthPayload 调 GET 并解析响应体。
func getAuthPayload(t *testing.T, h *SettingHandler) types.AuthSettingsPayload {
	t.Helper()
	rc := callJSON(h.GetAuthSettings, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("GetAuthSettings status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out types.AuthSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	return out
}

func TestAuthSettings_GetMasked(t *testing.T) {
	h, r := newAuthSettingHandlerForTest(t)
	const secret = "deadbeefcafe1234"
	err := r.Upsert(context.Background(),
		&repo.Setting{Scope: repo.ScopeGlobal, Name: setting.KeyAuthSecret, Value: secret})
	if err != nil {
		t.Fatalf("预置密钥: %v", err)
	}

	rc := callJSON(h.GetAuthSettings, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if strings.Contains(string(rc.Response.Body()), secret) {
		t.Errorf("响应体不应含完整密钥: %s", rc.Response.Body())
	}
	var out types.AuthSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.SecretMasked != "****1234" {
		t.Errorf("secret_masked = %q, want ****1234", out.SecretMasked)
	}
	if !out.SecretSet {
		t.Errorf("secret_set = false, want true")
	}
	if out.HeaderName != "X-API-Key" || out.HeaderNameDefault != "X-API-Key" {
		t.Errorf("header_name = %q / default = %q, want 均为 X-API-Key",
			out.HeaderName, out.HeaderNameDefault)
	}
}

func TestAuthSettings_GetNoSecret(t *testing.T) {
	h, _ := newAuthSettingHandlerForTest(t)

	out := getAuthPayload(t, h)
	if out.SecretSet {
		t.Errorf("secret_set = true, want false（未设置密钥）")
	}
	if out.SecretMasked != "" {
		t.Errorf("secret_masked = %q, want 空串", out.SecretMasked)
	}
}

func TestAuthSettings_PutHeaderName(t *testing.T) {
	h, _ := newAuthSettingHandlerForTest(t)

	rc := callJSON(h.PutAuthSettings, consts.MethodPut, `{"header_name":"X-Groot-Key"}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out types.AuthSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.HeaderName != "X-Groot-Key" {
		t.Errorf("PUT 响应 header_name = %q, want X-Groot-Key", out.HeaderName)
	}
	if got := getAuthPayload(t, h); got.HeaderName != "X-Groot-Key" {
		t.Errorf("GET 回读 header_name = %q, want X-Groot-Key", got.HeaderName)
	}
}

func TestAuthSettings_PutHeaderNameInvalid(t *testing.T) {
	h, _ := newAuthSettingHandlerForTest(t)

	rc := callJSON(h.PutAuthSettings, consts.MethodPut, `{"header_name":"bad name"}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
	// 非法值不改表：回读仍为默认
	if got := getAuthPayload(t, h); got.HeaderName != "X-API-Key" {
		t.Errorf("非法值后回读 header_name = %q, want X-API-Key", got.HeaderName)
	}
}

func TestAuthSettings_PutHeaderNameEmptyRestoresDefault(t *testing.T) {
	h, _ := newAuthSettingHandlerForTest(t)

	rc := callJSON(h.PutAuthSettings, consts.MethodPut, `{"header_name":"X-Groot-Key"}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("预设 header_name status=%d", rc.Response.StatusCode())
	}
	rc = callJSON(h.PutAuthSettings, consts.MethodPut, `{"header_name":""}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := getAuthPayload(t, h); got.HeaderName != "X-API-Key" {
		t.Errorf("空串后回读 header_name = %q, want X-API-Key", got.HeaderName)
	}
}

func TestAuthSettings_RegenerateSecret(t *testing.T) {
	h, r := newAuthSettingHandlerForTest(t)
	ctx := context.Background()
	err := r.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: setting.KeyAuthSecret, Value: "deadbeefcafe1234"})
	if err != nil {
		t.Fatalf("预置密钥: %v", err)
	}
	before := getAuthPayload(t, h)

	rc := callJSON(h.RegenerateAuthSecret, consts.MethodPost, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out types.AuthSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if !out.SecretSet {
		t.Errorf("secret_set = false, want true")
	}
	if out.SecretMasked == before.SecretMasked {
		t.Errorf("secret_masked 未变化: %q（密钥应已被替换）", out.SecretMasked)
	}

	// 从表里取出新密钥，断言明文不出现在响应体中
	row, err := r.Get(ctx, repo.ScopeGlobal, "", setting.KeyAuthSecret)
	if err != nil {
		t.Fatalf("查表取新密钥: %v", err)
	}
	if row.Value == "deadbeefcafe1234" {
		t.Errorf("表中密钥未被替换")
	}
	if strings.Contains(string(rc.Response.Body()), row.Value) {
		t.Errorf("响应体不应含新密钥明文: %s", rc.Response.Body())
	}
}
