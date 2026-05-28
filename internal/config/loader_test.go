package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "no_config")

	_, err := Load(homeDir)
	if err == nil {
		t.Fatal("配置不存在时应返回错误")
	}

	// 检查错误信息包含 groot init 提示
	if !strings.Contains(err.Error(), "groot init") {
		t.Errorf("错误信息应包含 'groot init' 提示，实际: %s", err.Error())
	}
}

// writeTestConfig 在 homeDir 写入 config.yaml
func writeTestConfig(t *testing.T, homeDir, content string) {
	t.Helper()
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	configPath := filepath.Join(homeDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}
}

// TestLoad_SubAgentDefaultsWhenAbsent 验证 yaml 不写 subagent 段时
// applyDefaults 会注入默认值，避免 Task 6/11 在零值上 panic
func TestLoad_SubAgentDefaultsWhenAbsent(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key-123")

	homeDir := t.TempDir()
	yaml := `
llm:
  default_model: gpt-4
  models:
    gpt-4:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4
`
	writeTestConfig(t, homeDir, yaml)

	cfg, err := Load(homeDir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	if cfg.SubAgent.MaxConcurrency != 5 {
		t.Errorf("expected MaxConcurrency=5, got %d", cfg.SubAgent.MaxConcurrency)
	}
	if cfg.SubAgent.ExecTimeout != "5m" {
		t.Errorf("expected ExecTimeout=5m, got %q", cfg.SubAgent.ExecTimeout)
	}
	if cfg.SubAgent.MaxTaskLength != 16000 {
		t.Errorf("expected MaxTaskLength=16000, got %d", cfg.SubAgent.MaxTaskLength)
	}
	if cfg.SubAgent.MaxResultLength != 8000 {
		t.Errorf("expected MaxResultLength=8000, got %d", cfg.SubAgent.MaxResultLength)
	}
}

// TestLoad_SubAgentPartialOverride 验证 yaml 只写部分 subagent 字段时，
// 用户提供的值保留，未提供的字段补默认值
func TestLoad_SubAgentPartialOverride(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key-123")

	homeDir := t.TempDir()
	yaml := `
llm:
  default_model: gpt-4
  models:
    gpt-4:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4
subagent:
  max_concurrency: 10
`
	writeTestConfig(t, homeDir, yaml)

	cfg, err := Load(homeDir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	if cfg.SubAgent.MaxConcurrency != 10 {
		t.Errorf("expected MaxConcurrency=10 (用户覆盖), got %d", cfg.SubAgent.MaxConcurrency)
	}
	if cfg.SubAgent.ExecTimeout != "5m" {
		t.Errorf("expected ExecTimeout=5m (默认), got %q", cfg.SubAgent.ExecTimeout)
	}
	if cfg.SubAgent.MaxTaskLength != 16000 {
		t.Errorf("expected MaxTaskLength=16000 (默认), got %d", cfg.SubAgent.MaxTaskLength)
	}
	if cfg.SubAgent.MaxResultLength != 8000 {
		t.Errorf("expected MaxResultLength=8000 (默认), got %d", cfg.SubAgent.MaxResultLength)
	}
}