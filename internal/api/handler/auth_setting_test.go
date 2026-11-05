// internal/api/handler/auth_setting_test.go
// 认证配置接口的单元测试：脱敏展示与密钥重新生成。
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/errbody"
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

// decodeAuthPayload 解析认证配置响应体并核对字段集合恰为 secret_masked 与 secret_set。
func decodeAuthPayload(t *testing.T, body []byte) types.AuthSettingsPayload {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("响应字段应恰为两个, got %v（body=%s）", raw, body)
	}
	for _, k := range []string{"secret_masked", "secret_set"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("响应缺少字段 %q: %s", k, body)
		}
	}
	var out types.AuthSettingsPayload
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	return out
}

// getAuthPayload 调 GET 并解析响应体。
func getAuthPayload(t *testing.T, h *SettingHandler) types.AuthSettingsPayload {
	t.Helper()
	rc := callJSON(h.GetAuthSettings, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("GetAuthSettings status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	return decodeAuthPayload(t, rc.Response.Body())
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
	out := decodeAuthPayload(t, rc.Response.Body())
	if out.SecretMasked != "****1234" {
		t.Errorf("secret_masked = %q, want ****1234", out.SecretMasked)
	}
	if !out.SecretSet {
		t.Errorf("secret_set = false, want true")
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
	out := decodeAuthPayload(t, rc.Response.Body())
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

// failingSettingRepo 让 ListByScope 恒失败，覆盖 500 错误信封分支。
type failingSettingRepo struct {
	repo.SettingRepo
}

func (failingSettingRepo) Get(context.Context, repo.Scope, string, string) (*repo.Setting, error) {
	return nil, errors.New("db down")
}

func (failingSettingRepo) ListByScope(context.Context, repo.Scope, string) ([]*repo.Setting, error) {
	return nil, errors.New("db down")
}

func (failingSettingRepo) Upsert(context.Context, ...*repo.Setting) error {
	return errors.New("db down")
}

func (failingSettingRepo) Delete(context.Context, repo.Scope, string, string) error {
	return errors.New("db down")
}

// TestAuthSettings_RepoErrorEnvelope 配置表读失败时返回 500 信封，且不泄漏底层错误。
func TestAuthSettings_RepoErrorEnvelope(t *testing.T) {
	h := NewSettingHandler(SettingHandlerDeps{
		Settings: setting.New(config.Bootstrap{}, failingSettingRepo{}),
		Log:      logger.NewNop(),
	})

	assertEnvelopeError(t, callJSON(h.GetAuthSettings, consts.MethodGet, "", nil),
		500, errbody.TypeAPI, errbody.CodeInternalError)
	assertEnvelopeError(t, callJSON(h.RegenerateAuthSecret, consts.MethodPost, "", nil),
		500, errbody.TypeAPI, errbody.CodeInternalError)
}
