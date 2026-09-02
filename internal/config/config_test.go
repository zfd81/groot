package config

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// 验证 Agent 默认值
	if cfg.Agent.Name != "groot" {
		t.Errorf("Agent.Name 默认值错误: got %s, want groot", cfg.Agent.Name)
	}
	if cfg.Agent.Version != "1.0.0" {
		t.Errorf("Agent.Version 默认值错误: got %s, want 1.0.0", cfg.Agent.Version)
	}

	// 验证 Server 默认值
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host 默认值错误: got %s, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port 默认值错误: got %d, want 8080", cfg.Server.Port)
	}

	// 验证 Memory 默认值
	if cfg.Memory.HistoryWindow != 20 {
		t.Errorf("Memory.HistoryWindow 默认值错误: got %d, want 20", cfg.Memory.HistoryWindow)
	}

	// 验证 React 默认值
	if cfg.React.MaxIterations != 20 {
		t.Errorf("React.MaxIterations 默认值错误: got %d, want 20", cfg.React.MaxIterations)
	}
	if cfg.React.StepTimeout != 60 {
		t.Errorf("React.StepTimeout 默认值错误: got %d, want 60", cfg.React.StepTimeout)
	}
	if cfg.React.ErrorRetry != 2 {
		t.Errorf("React.ErrorRetry 默认值错误: got %d, want 2", cfg.React.ErrorRetry)
	}

	// 验证 Attachment 默认值
	if cfg.Attachment.MaxSize != 50 {
		t.Errorf("Attachment.MaxSize 默认值错误: got %d, want 50", cfg.Attachment.MaxSize)
	}
	if cfg.Attachment.MaxCount != 10 {
		t.Errorf("Attachment.MaxCount 默认值错误: got %d, want 10", cfg.Attachment.MaxCount)
	}

	// 验证 Logging 默认值
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level 默认值错误: got %s, want info", cfg.Logging.Level)
	}
}

func TestExpandEnv(t *testing.T) {
	// 设置测试环境变量
	os.Setenv("TEST_API_KEY", "test123")
	defer os.Unsetenv("TEST_API_KEY")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "展开环境变量",
			input:    "${TEST_API_KEY}",
			expected: "test123",
		},
		{
			name:     "环境变量不存在",
			input:    "${NON_EXISTENT_VAR}",
			expected: "",
		},
		{
			name:     "非环境变量格式",
			input:    "plain_text",
			expected: "plain_text",
		},
		{
			name:     "部分匹配不展开",
			input:    "prefix${TEST_API_KEY}",
			expected: "prefix${TEST_API_KEY}",
		},
		{
			name:     "空字符串",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandEnv(tt.input)
			if result != tt.expected {
				t.Errorf("ExpandEnv(%s) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

// TestConfig_SubAgentDefaults 验证子 Agent 调度配置的默认值（设计 4.4 节）
func TestConfig_SubAgentDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.SubAgent.MaxConcurrency != 5 {
		t.Errorf("expected MaxConcurrency=5, got %d", cfg.SubAgent.MaxConcurrency)
	}
	if cfg.SubAgent.ExecTimeout != "5m" {
		t.Errorf("expected ExecTimeout=5m, got %s", cfg.SubAgent.ExecTimeout)
	}
	if cfg.SubAgent.MaxTaskLength != 16000 {
		t.Errorf("expected MaxTaskLength=16000, got %d", cfg.SubAgent.MaxTaskLength)
	}
	if cfg.SubAgent.MaxResultLength != 8000 {
		t.Errorf("expected MaxResultLength=8000, got %d", cfg.SubAgent.MaxResultLength)
	}
}
