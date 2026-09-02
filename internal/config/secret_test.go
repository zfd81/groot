package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// assertPerm0600 断言文件权限为 0600（config.yaml 承载 JWT 签名密钥）。
func assertPerm0600(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file perm = %o, want 0600", info.Mode().Perm())
	}
}

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestGenerateAuthSecret(t *testing.T) {
	s1, err := GenerateAuthSecret()
	if err != nil {
		t.Fatalf("GenerateAuthSecret: %v", err)
	}
	if len(s1) != 64 {
		t.Errorf("secret length = %d, want 64 hex chars", len(s1))
	}
	s2, _ := GenerateAuthSecret()
	if s1 == s2 {
		t.Error("two secrets should differ")
	}
}

// TestEnsureAuthSecret_AlreadySet 已配置 secret 时不改文件。
func TestEnsureAuthSecret_AlreadySet(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "security:\n  auth:\n    secret: existing\n")
	before, _ := os.ReadFile(p)
	cfg := &Config{}
	cfg.Security.Auth.Secret = "existing"
	if err := EnsureAuthSecret(dir, cfg); err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("file should be untouched when secret already set")
	}
}

// TestEnsureAuthSecret_AppendWhenNoSecurityNode 无活动 security 节（如全注释模板）时
// 追加文本块，且原有注释内容原样保留。
func TestEnsureAuthSecret_AppendWhenNoSecurityNode(t *testing.T) {
	dir := t.TempDir()
	original := "# Groot 配置\n#security:\n#  auth:\n#    secret: xxx\nserver:\n  port: 8080\n"
	p := writeConfig(t, dir, original)
	cfg := &Config{}
	if err := EnsureAuthSecret(dir, cfg); err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	if cfg.Security.Auth.Secret == "" {
		t.Fatal("cfg secret should be set")
	}
	data, _ := os.ReadFile(p)
	content := string(data)
	if !strings.Contains(content, "# Groot 配置") || !strings.Contains(content, "port: 8080") {
		t.Error("original content should be preserved")
	}
	// 回写后的文件必须能被 yaml 解析且读出同一 secret
	var parsed Config
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("patched file not valid yaml: %v\n%s", err, content)
	}
	if parsed.Security.Auth.Secret != cfg.Security.Auth.Secret {
		t.Errorf("file secret %q != cfg secret %q", parsed.Security.Auth.Secret, cfg.Security.Auth.Secret)
	}
	assertPerm0600(t, p)
}

// TestEnsureAuthSecret_NullSecurityNode security 键存在但值为 null（子项全被注释）时，
// 就地升级为映射写入 secret，不损坏文件、其他顶层键保留。
func TestEnsureAuthSecret_NullSecurityNode(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "security:\n# 全部注释\nserver:\n  port: 8080\n")
	cfg := &Config{}
	if err := EnsureAuthSecret(dir, cfg); err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	data, _ := os.ReadFile(p)
	var parsed Config
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("patched file not valid yaml: %v\n%s", err, string(data))
	}
	if parsed.Security.Auth.Secret != cfg.Security.Auth.Secret {
		t.Errorf("secret not written: %q != %q", parsed.Security.Auth.Secret, cfg.Security.Auth.Secret)
	}
	if parsed.Server.Port != 8080 {
		t.Errorf("server.port = %d, want 8080 (other top-level keys should be preserved)", parsed.Server.Port)
	}
}

// TestEnsureAuthSecret_PatchExistingSecurityNode 已有活动 security 节时以节点方式写入，
// 不产生重复键，且已有子节点保留。
func TestEnsureAuthSecret_PatchExistingSecurityNode(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "security:\n  rate_limit: # 限流配置\n    enabled: true\n")
	cfg := &Config{}
	if err := EnsureAuthSecret(dir, cfg); err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	data, _ := os.ReadFile(p)
	var parsed Config
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("patched file not valid yaml: %v\n%s", err, string(data))
	}
	if parsed.Security.Auth.Secret != cfg.Security.Auth.Secret {
		t.Errorf("secret not written: %q", parsed.Security.Auth.Secret)
	}
	if !parsed.Security.RateLimit.Enabled {
		t.Error("existing rate_limit node should be preserved")
	}
	if !strings.Contains(string(data), "# 限流配置") {
		t.Errorf("yaml comment inside security node should be preserved:\n%s", string(data))
	}
	assertPerm0600(t, p)
}
