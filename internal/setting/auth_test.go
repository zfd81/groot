package setting

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/zfd81/groot/internal/config"
)

// TestAuth_Defaults 空表时返回空密钥
func TestAuth_Defaults(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())

	a, err := s.Auth(context.Background())
	if err != nil {
		t.Fatalf("Auth: %v", err)
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
