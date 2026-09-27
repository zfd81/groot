package setting

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/config"
)

// TestAuth_Defaults 空表时返回默认请求头名与空密钥
func TestAuth_Defaults(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())

	a, err := s.Auth(context.Background())
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.HeaderName != "X-API-Key" {
		t.Errorf("HeaderName = %q, want X-API-Key", a.HeaderName)
	}
	if a.Secret != "" {
		t.Errorf("空表时 Secret 应为空, got %q", a.Secret)
	}
}

// TestEnsureAuthSecret_Generates 密钥缺失时生成并写表，返回值为 64 位 hex
func TestEnsureAuthSecret_Generates(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	secret, err := s.EnsureAuthSecret(ctx)
	if err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	if len(secret) != 64 {
		t.Fatalf("密钥长度 = %d, want 64", len(secret))
	}
	if _, err := hex.DecodeString(secret); err != nil {
		t.Errorf("密钥应为 hex: %v", err)
	}

	// 已落表：再次读取拿到同一个值
	a, err := s.Auth(ctx)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.Secret != secret {
		t.Errorf("表中密钥 = %q, want %q", a.Secret, secret)
	}
}

// TestEnsureAuthSecret_Idempotent 已有密钥时原样返回，不改表
func TestEnsureAuthSecret_Idempotent(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	first, err := s.EnsureAuthSecret(ctx)
	if err != nil {
		t.Fatalf("首次 EnsureAuthSecret: %v", err)
	}
	second, err := s.EnsureAuthSecret(ctx)
	if err != nil {
		t.Fatalf("再次 EnsureAuthSecret: %v", err)
	}
	if first != second {
		t.Errorf("密钥被重新生成: %q → %q（会使已签发的 API Key 全部失效）", first, second)
	}
}

// TestEnsureAuthSecret_NoRepo 无配置表仓库时明确报错，不静默放过空密钥
func TestEnsureAuthSecret_NoRepo(t *testing.T) {
	s := New(config.Bootstrap{}, nil)
	if _, err := s.EnsureAuthSecret(context.Background()); !errors.Is(err, ErrNoSettingStore) {
		t.Errorf("err = %v, want ErrNoSettingStore", err)
	}
}

// TestAuth_HeaderNameFromTable 表中的请求头名覆盖默认值
func TestAuth_HeaderNameFromTable(t *testing.T) {
	r := newFakeRepo()
	s := New(config.Bootstrap{}, r)
	ctx := context.Background()

	if err := s.SetAuthHeaderName(ctx, "X-Groot-Key"); err != nil {
		t.Fatalf("SetAuthHeaderName: %v", err)
	}
	a, err := s.Auth(ctx)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.HeaderName != "X-Groot-Key" {
		t.Errorf("HeaderName = %q, want X-Groot-Key", a.HeaderName)
	}
}

// TestRegenerateAuthSecret 每次生成不同密钥且落表可回读
func TestRegenerateAuthSecret(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	first, err := s.RegenerateAuthSecret(ctx)
	if err != nil {
		t.Fatalf("首次 RegenerateAuthSecret: %v", err)
	}
	a, err := s.Auth(ctx)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.Secret != first {
		t.Errorf("表中密钥 = %q, want %q（应已落表）", a.Secret, first)
	}

	second, err := s.RegenerateAuthSecret(ctx)
	if err != nil {
		t.Fatalf("再次 RegenerateAuthSecret: %v", err)
	}
	if second == first {
		t.Errorf("两次生成得到相同密钥 %q，应无条件替换", second)
	}
	a, err = s.Auth(ctx)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.Secret != second {
		t.Errorf("表中密钥 = %q, want %q（第二次也应落表）", a.Secret, second)
	}
}

// TestRegenerateAuthSecret_NoRepo 无配置表仓库时明确报错
func TestRegenerateAuthSecret_NoRepo(t *testing.T) {
	s := New(config.Bootstrap{}, nil)
	if _, err := s.RegenerateAuthSecret(context.Background()); !errors.Is(err, ErrNoSettingStore) {
		t.Errorf("err = %v, want ErrNoSettingStore", err)
	}
}

// TestSetAuthHeaderName_EmptyRestoresDefault 空串删除表行，回读为默认 X-API-Key
func TestSetAuthHeaderName_EmptyRestoresDefault(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	if err := s.SetAuthHeaderName(ctx, "X-Groot-Key"); err != nil {
		t.Fatalf("SetAuthHeaderName(X-Groot-Key): %v", err)
	}
	if err := s.SetAuthHeaderName(ctx, ""); err != nil {
		t.Fatalf("SetAuthHeaderName(空串): %v", err)
	}
	a, err := s.Auth(ctx)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.HeaderName != "X-API-Key" {
		t.Errorf("HeaderName = %q, want X-API-Key（空串应删行恢复默认）", a.HeaderName)
	}
}

// TestSetAuthHeaderName_Invalid 非法请求头名整体拒绝，可被 errors.Is 识别
func TestSetAuthHeaderName_Invalid(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	cases := []struct {
		desc string
		name string
	}{
		{"含空格", "bad name"},
		{"含下划线", "X_API_Key"},
		{"超 64 长度", strings.Repeat("A", 65)},
		{"含中文", "X-密钥"},
	}
	for _, c := range cases {
		if err := s.SetAuthHeaderName(ctx, c.name); !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("%s：err = %v, want ErrInvalidSetting", c.desc, err)
		}
	}

	// 非法值不应污染表：回读仍为默认
	a, err := s.Auth(ctx)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.HeaderName != "X-API-Key" {
		t.Errorf("HeaderName = %q, want X-API-Key（非法值不应写入）", a.HeaderName)
	}
}
