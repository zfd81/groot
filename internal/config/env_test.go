package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadEnvFile_Missing 验证 env.yaml 不存在时不报错，且 cfg.Database 为 nil。
func TestLoadEnvFile_Missing(t *testing.T) {
	homeDir := t.TempDir()
	cfg := &Config{Database: &DatabaseConfig{Driver: "sqlite", DSN: "should-be-cleared"}}

	if err := loadEnvFile(cfg, homeDir); err != nil {
		t.Fatalf("loadEnvFile 不应返回错误: %v", err)
	}
	if cfg.Database != nil {
		t.Error("env.yaml 不存在时，loadEnvFile 必须把 cfg.Database 强制置 nil")
	}
}

// TestLoadEnvFile_AllCommented 验证 env.yaml 存在但 database 节缺失/为空时
// cfg.Database 仍为 nil。
func TestLoadEnvFile_AllCommented(t *testing.T) {
	homeDir := t.TempDir()
	envContent := `# 全注释的 env.yaml
#database:
#  driver: sqlite
#  dsn: /tmp/groot.db
`
	if err := os.WriteFile(filepath.Join(homeDir, EnvFileName), []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	if err := loadEnvFile(cfg, homeDir); err != nil {
		t.Fatalf("loadEnvFile 不应返回错误: %v", err)
	}
	if cfg.Database != nil {
		t.Error("database 节全注释时 cfg.Database 应为 nil")
	}
}

// TestLoadEnvFile_DatabaseConfigured 验证 env.yaml 中 database 节有效时
// 字段被正确注入到 cfg.Database。
func TestLoadEnvFile_DatabaseConfigured(t *testing.T) {
	homeDir := t.TempDir()
	envContent := `database:
  driver: sqlite
  dsn: /tmp/groot_test.db
  max_open_conns: 10
  max_idle_conns: 3
  conn_max_lifetime: 15m
`
	if err := os.WriteFile(filepath.Join(homeDir, EnvFileName), []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	if err := loadEnvFile(cfg, homeDir); err != nil {
		t.Fatalf("loadEnvFile 不应返回错误: %v", err)
	}
	if cfg.Database == nil {
		t.Fatal("database 节有效时 cfg.Database 应被赋值")
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("Driver = %q, want sqlite", cfg.Database.Driver)
	}
	if cfg.Database.DSN != "/tmp/groot_test.db" {
		t.Errorf("DSN = %q", cfg.Database.DSN)
	}
	if cfg.Database.MaxOpenConns != 10 {
		t.Errorf("MaxOpenConns = %d, want 10", cfg.Database.MaxOpenConns)
	}
	if cfg.Database.ConnMaxLifetime != "15m" {
		t.Errorf("ConnMaxLifetime = %q, want 15m", cfg.Database.ConnMaxLifetime)
	}
}

// TestLoadEnvFile_OverridesStaleValue 验证即便 cfg 已有 Database 值，
// loadEnvFile 也会先强制清空，再从 env.yaml 注入。
func TestLoadEnvFile_OverridesStaleValue(t *testing.T) {
	homeDir := t.TempDir()
	// env.yaml 不存在
	cfg := &Config{
		Database: &DatabaseConfig{
			Driver: "mysql",
			DSN:    "stale-dsn",
		},
	}

	if err := loadEnvFile(cfg, homeDir); err != nil {
		t.Fatalf("loadEnvFile 不应返回错误: %v", err)
	}
	if cfg.Database != nil {
		t.Errorf("残留的 database 配置必须被 loadEnvFile 清掉，实际仍有 %+v", cfg.Database)
	}
}

// TestLoadEnvFile_InvalidYAML 验证 env.yaml 解析失败时返回错误。
func TestLoadEnvFile_InvalidYAML(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeDir, EnvFileName), []byte("this is: : not yaml: ["), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	if err := loadEnvFile(cfg, homeDir); err == nil {
		t.Fatal("env.yaml 内容非法时 loadEnvFile 应返回错误")
	}
}

// TestLoad_EnvYamlOverridesConfigYaml 端到端验证：只有 env.yaml 才决定 database 配置，
// config.yaml 中没有 database 字段。
func TestLoad_EnvYamlOverridesConfigYaml(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key-123")

	homeDir := t.TempDir()
	configYaml := `
llm:
  default_model: gpt-4
  models:
    gpt-4:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4
`
	writeTestConfig(t, homeDir, configYaml)

	cfg, err := Load(homeDir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Database != nil {
		t.Errorf("env.yaml 不存在时，cfg.Database 必须为 nil，实际 %+v", cfg.Database)
	}
}

// TestLoad_EnvYamlEnablesDatabase 端到端验证：env.yaml 里写了 database 节后，
// Load 返回的 cfg.Database 包含正确字段，且环境变量已展开。
func TestLoad_EnvYamlEnablesDatabase(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key-123")
	t.Setenv("DB_DSN_E2E", "mysql://root:pass@localhost/groot")

	homeDir := t.TempDir()
	configYaml := `
llm:
  default_model: gpt-4
  models:
    gpt-4:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4
`
	writeTestConfig(t, homeDir, configYaml)

	envYaml := `database:
  driver: mysql
  dsn: ${DB_DSN_E2E}
  max_open_conns: 20
  max_idle_conns: 5
  conn_max_lifetime: 30m
`
	if err := os.WriteFile(filepath.Join(homeDir, EnvFileName), []byte(envYaml), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(homeDir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Database == nil {
		t.Fatal("env.yaml 已配置 database，cfg.Database 不应为 nil")
	}
	if cfg.Database.Driver != "mysql" {
		t.Errorf("Driver = %q, want mysql", cfg.Database.Driver)
	}
	if cfg.Database.DSN != "mysql://root:pass@localhost/groot" {
		t.Errorf("DSN = %q (env var expansion 失效?)", cfg.Database.DSN)
	}
	if cfg.Database.MaxOpenConns != 20 {
		t.Errorf("MaxOpenConns = %d, want 20", cfg.Database.MaxOpenConns)
	}
}
