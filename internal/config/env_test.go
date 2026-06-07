package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadEnvFile_Missing 验证 env.yaml 不存在时不报错，且 cfg.Storage.Minio 为 nil。
func TestLoadEnvFile_Missing(t *testing.T) {
	homeDir := t.TempDir()
	cfg := &Config{Storage: StorageConfig{Minio: &MinioConfig{Endpoint: "should-be-cleared"}}}

	if err := loadEnvFile(cfg, homeDir); err != nil {
		t.Fatalf("loadEnvFile 不应返回错误: %v", err)
	}
	if cfg.Storage.Minio != nil {
		t.Error("env.yaml 不存在时，loadEnvFile 必须把 cfg.Storage.Minio 强制置 nil")
	}
}

// TestLoadEnvFile_AllCommented 验证 env.yaml 存在但 minio 节缺失/为空时
// cfg.Storage.Minio 仍为 nil（local 模式）。
func TestLoadEnvFile_AllCommented(t *testing.T) {
	homeDir := t.TempDir()
	envContent := `# 全注释的 env.yaml
#minio:
#  endpoint: localhost:9000
`
	if err := os.WriteFile(filepath.Join(homeDir, EnvFileName), []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	if err := loadEnvFile(cfg, homeDir); err != nil {
		t.Fatalf("loadEnvFile 不应返回错误: %v", err)
	}
	if cfg.Storage.Minio != nil {
		t.Error("minio 节全注释时 cfg.Storage.Minio 应为 nil")
	}
}

// TestLoadEnvFile_MinioConfigured 验证 env.yaml 中 minio 节有效时
// 字段被正确注入到 cfg.Storage.Minio。
func TestLoadEnvFile_MinioConfigured(t *testing.T) {
	homeDir := t.TempDir()
	envContent := `minio:
  endpoint: localhost:9000
  access_key: ak
  secret_key: sk
  bucket: groot
  use_ssl: true
`
	if err := os.WriteFile(filepath.Join(homeDir, EnvFileName), []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	if err := loadEnvFile(cfg, homeDir); err != nil {
		t.Fatalf("loadEnvFile 不应返回错误: %v", err)
	}
	if cfg.Storage.Minio == nil {
		t.Fatal("minio 节有效时 cfg.Storage.Minio 应被赋值")
	}
	if cfg.Storage.Minio.Endpoint != "localhost:9000" {
		t.Errorf("Endpoint = %q", cfg.Storage.Minio.Endpoint)
	}
	if cfg.Storage.Minio.Bucket != "groot" {
		t.Errorf("Bucket = %q", cfg.Storage.Minio.Bucket)
	}
	if !cfg.Storage.Minio.UseSSL {
		t.Error("UseSSL 应为 true")
	}
}

// TestLoadEnvFile_OverridesConfigYaml 验证关键约定：即便 cfg 已经从
// config.yaml 解析出 storage.minio（旧用户残留），loadEnvFile 也会先
// 强制清空，再从 env.yaml 注入。这是 v2 版本"剥离基础设施凭据"的核心。
func TestLoadEnvFile_OverridesConfigYaml(t *testing.T) {
	homeDir := t.TempDir()
	// env.yaml 不存在
	cfg := &Config{
		Storage: StorageConfig{
			Minio: &MinioConfig{
				Endpoint:  "stale-from-config-yaml:9000",
				AccessKey: "stale-ak",
			},
		},
	}

	if err := loadEnvFile(cfg, homeDir); err != nil {
		t.Fatalf("loadEnvFile 不应返回错误: %v", err)
	}
	if cfg.Storage.Minio != nil {
		t.Errorf("config.yaml 残留的 storage.minio 必须被 loadEnvFile 清掉，实际仍有 %+v", cfg.Storage.Minio)
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

// TestLoad_EnvYamlOverridesConfigYaml 端到端验证：config.yaml 里写了 storage.minio
// 也不会生效；只有 env.yaml 才决定 storage 后端。
func TestLoad_EnvYamlOverridesConfigYaml(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key-123")

	homeDir := t.TempDir()
	// config.yaml 里塞一个 storage.minio（按 v2 约定应被忽略）
	configYaml := `
llm:
  default_model: gpt-4
  models:
    gpt-4:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4
storage:
  minio:
    endpoint: should-be-ignored:9000
    access_key: ignored-ak
    secret_key: ignored-sk
    bucket: ignored
`
	writeTestConfig(t, homeDir, configYaml)

	cfg, err := Load(homeDir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Storage.Minio != nil {
		t.Errorf("env.yaml 不存在时，config.yaml 里的 storage.minio 必须被忽略，实际 %+v", cfg.Storage.Minio)
	}
}

// TestLoad_EnvYamlEnablesMinio 端到端验证：env.yaml 里写了 minio 节后，
// Load 返回的 cfg.Storage.Minio 包含正确字段，且环境变量已展开。
func TestLoad_EnvYamlEnablesMinio(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key-123")
	t.Setenv("MINIO_AK_E2E", "real-ak")
	t.Setenv("MINIO_SK_E2E", "real-sk")

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

	envYaml := `minio:
  endpoint: minio.local:9000
  access_key: ${MINIO_AK_E2E}
  secret_key: ${MINIO_SK_E2E}
  bucket: groot
  use_ssl: false
`
	if err := os.WriteFile(filepath.Join(homeDir, EnvFileName), []byte(envYaml), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(homeDir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Storage.Minio == nil {
		t.Fatal("env.yaml 已配置 minio，cfg.Storage.Minio 不应为 nil")
	}
	if cfg.Storage.Minio.Endpoint != "minio.local:9000" {
		t.Errorf("Endpoint = %q", cfg.Storage.Minio.Endpoint)
	}
	if cfg.Storage.Minio.AccessKey != "real-ak" {
		t.Errorf("AccessKey = %q (env var expansion 失效?)", cfg.Storage.Minio.AccessKey)
	}
	if cfg.Storage.Minio.SecretKey != "real-sk" {
		t.Errorf("SecretKey = %q (env var expansion 失效?)", cfg.Storage.Minio.SecretKey)
	}
}
