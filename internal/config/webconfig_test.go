package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestWebConfig_ParseYAML 验证 security.web 段可被正确解析。
func TestWebConfig_ParseYAML(t *testing.T) {
	src := `
security:
  web:
    enabled: true
    username: admin
    password: ${GROOT_WEB_PASS}
    session_ttl: 12h
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	w := cfg.Security.Web
	if !w.Enabled || w.Username != "admin" || w.Password != "${GROOT_WEB_PASS}" || w.SessionTTL != "12h" {
		t.Errorf("unexpected web config: %+v", w)
	}
}

// TestWebConfig_Defaults 验证默认值：关闭、用户名 admin、密码为空、TTL 24h。
func TestWebConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	w := cfg.Security.Web
	if w.Enabled {
		t.Error("web auth should be disabled by default")
	}
	if w.Username != "admin" {
		t.Errorf("default username should be admin, got %q", w.Username)
	}
	if w.Password != "" {
		t.Errorf("default password should be empty, got %q", w.Password)
	}
	if w.SessionTTL != "24h" {
		t.Errorf("default session_ttl should be 24h, got %q", w.SessionTTL)
	}
}

// TestWebConfig_PasswordEnvExpansion 验证 web.password 中的 ${ENV_VAR} 会被展开。
func TestWebConfig_PasswordEnvExpansion(t *testing.T) {
	t.Setenv("GROOT_WEB_PASS", "s3cret")
	cfg := DefaultConfig()
	cfg.Security.Web.Password = "${GROOT_WEB_PASS}"
	expandConfigEnvVars(cfg)
	if cfg.Security.Web.Password != "s3cret" {
		t.Errorf("password should be expanded to s3cret, got %q", cfg.Security.Web.Password)
	}
}
