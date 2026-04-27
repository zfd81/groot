package config

import (
	"os"
	"strings"
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

	// 验证 LLM 默认值
	if cfg.LLM.DefaultModel != "gpt-4o" {
		t.Errorf("LLM.DefaultModel 默认值错误: got %s, want gpt-4o", cfg.LLM.DefaultModel)
	}
	if len(cfg.LLM.Models) == 0 {
		t.Error("LLM.Models 默认值不能为空")
	}

	// 验证 Skills 默认值
	if !cfg.Skills.HotReload.Enabled {
		t.Error("Skills.HotReload.Enabled 默认应该为 true")
	}
	if cfg.Skills.HotReload.DebounceDelay != 2 {
		t.Errorf("Skills.HotReload.DebounceDelay 默认值错误: got %d, want 2", cfg.Skills.HotReload.DebounceDelay)
	}

	// 验证 Memory 默认值
	if cfg.Memory.Directory != "memory" {
		t.Errorf("Memory.Directory 默认值错误: got %s, want memory", cfg.Memory.Directory)
	}
	if cfg.Memory.RetentionDays != 7 {
		t.Errorf("Memory.RetentionDays 默认值错误: got %d, want 7", cfg.Memory.RetentionDays)
	}

	// 验证 React 默认值
	if cfg.React.MaxIterations != 20 {
		t.Errorf("React.MaxIterations 默认值错误: got %d, want 20", cfg.React.MaxIterations)
	}
	if cfg.React.MaxTokens != 100000 {
		t.Errorf("React.MaxTokens 默认值错误: got %d, want 100000", cfg.React.MaxTokens)
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

func TestGetDefaultModel(t *testing.T) {
	cfg := &LLMConfig{
		DefaultModel: "model1",
		Models: map[string]ModelConfig{
			"model1": {BaseURL: "http://localhost", APIKey: "key1", Model: "m1"},
			"model2": {BaseURL: "http://localhost", APIKey: "key2", Model: "m2"},
		},
	}

	model := cfg.GetDefaultModel()
	if model == nil {
		t.Fatal("GetDefaultModel() 返回 nil")
	}
	if model.Model != "m1" {
		t.Errorf("GetDefaultModel() 返回错误模型: got %s, want m1", model.Model)
	}
}

func TestGetDefaultModel_NotFound(t *testing.T) {
	cfg := &LLMConfig{
		DefaultModel: "nonexistent",
		Models: map[string]ModelConfig{
			"model1": {BaseURL: "http://localhost", APIKey: "key1", Model: "m1"},
		},
	}

	model := cfg.GetDefaultModel()
	if model != nil {
		t.Errorf("GetDefaultModel() 应返回 nil 当 DefaultModel 不存在")
	}
}

func TestGetModelByName(t *testing.T) {
	cfg := &LLMConfig{
		DefaultModel: "model1",
		Models: map[string]ModelConfig{
			"model1": {BaseURL: "http://localhost", APIKey: "key1", Model: "m1"},
			"model2": {BaseURL: "http://localhost", APIKey: "key2", Model: "m2"},
		},
	}

	tests := []struct {
		name         string
		modelName    string
		expectModel  string
		expectNil    bool
	}{
		{
			name:        "空名称返回默认模型",
			modelName:   "",
			expectModel: "m1",
		},
		{
			name:        "指定存在的模型",
			modelName:   "model2",
			expectModel: "m2",
		},
		{
			name:      "指定不存在的模型",
			modelName: "nonexistent",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := cfg.GetModelByName(tt.modelName)
			if tt.expectNil {
				if model != nil {
					t.Errorf("GetModelByName(%s) 应返回 nil", tt.modelName)
				}
				return
			}
			if model == nil {
				t.Fatalf("GetModelByName(%s) 返回 nil", tt.modelName)
			}
			if model.Model != tt.expectModel {
				t.Errorf("GetModelByName(%s) 返回错误模型: got %s, want %s", tt.modelName, model.Model, tt.expectModel)
			}
		})
	}
}

func TestValidateModel(t *testing.T) {
	cfg := &LLMConfig{
		DefaultModel: "model1",
		Models: map[string]ModelConfig{
			"model1": {},
			"model2": {},
		},
	}

	tests := []struct {
		name      string
		modelName string
		expected  bool
	}{
		{
			name:      "空名称有效（使用默认）",
			modelName: "",
			expected:  true,
		},
		{
			name:      "存在的模型",
			modelName: "model1",
			expected:  true,
		},
		{
			name:      "不存在的模型",
			modelName: "nonexistent",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cfg.ValidateModel(tt.modelName)
			if result != tt.expected {
				t.Errorf("ValidateModel(%s) = %v, want %v", tt.modelName, result, tt.expected)
			}
		})
	}
}

func TestValidateLLMConfig(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *LLMConfig
		expectError bool
	}{
		{
			name: "正常配置",
			cfg: &LLMConfig{
				DefaultModel: "model1",
				Models: map[string]ModelConfig{
					"model1": {BaseURL: "https://api.openai.com/v1", APIKey: "test-key"},
				},
			},
			expectError: false,
		},
		{
			name: "空 Models",
			cfg: &LLMConfig{
				DefaultModel: "model1",
				Models:       map[string]ModelConfig{},
			},
			expectError: true,
		},
		{
			name: "DefaultModel 不存在",
			cfg: &LLMConfig{
				DefaultModel: "nonexistent",
				Models: map[string]ModelConfig{
					"model1": {BaseURL: "https://api.openai.com/v1", APIKey: "test-key"},
				},
			},
			expectError: true,
		},
		{
			name: "空 DefaultModel 自动设置",
			cfg: &LLMConfig{
				DefaultModel: "",
				Models: map[string]ModelConfig{
					"model1": {BaseURL: "https://api.openai.com/v1", APIKey: "test-key"},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLLMConfig(tt.cfg)
			if tt.expectError {
				if err == nil {
					t.Error("ValidateLLMConfig 应返回错误")
				}
			} else {
				if err != nil {
					t.Errorf("ValidateLLMConfig 不应返回错误: %v", err)
				}
			}
		})
	}
}

func TestValidateLLMConfig_SetsDefaultModel(t *testing.T) {
	cfg := &LLMConfig{
		DefaultModel: "",
		Models: map[string]ModelConfig{
			"model1": {BaseURL: "https://api.openai.com/v1", APIKey: "test-key"},
			"model2": {BaseURL: "https://api.openai.com/v1", APIKey: "test-key"},
		},
	}

	err := ValidateLLMConfig(cfg)
	if err != nil {
		t.Fatalf("ValidateLLMConfig 返回错误: %v", err)
	}

	if cfg.DefaultModel == "" {
		t.Error("ValidateLLMConfig 应自动设置 DefaultModel")
	}

	// DefaultModel 应设置为第一个模型（map 返回顺序不确定，但至少应该设置一个存在的）
	if !cfg.ValidateModel(cfg.DefaultModel) {
		t.Errorf("DefaultModel '%s' 应存在于 Models 中", cfg.DefaultModel)
	}
}

func TestValidateLLMConfigEmptyModels(t *testing.T) {
	cfg := &LLMConfig{Models: map[string]ModelConfig{}}
	err := ValidateLLMConfig(cfg)
	if err == nil {
		t.Fatal("空 models 应返回错误")
	}
	if !strings.Contains(err.Error(), "models 配置为空") {
		t.Errorf("错误信息不符合预期: %s", err.Error())
	}
}

func TestValidateLLMConfigEmptyAPIKey(t *testing.T) {
	cfg := &LLMConfig{
		DefaultModel: "gpt-4o",
		Models: map[string]ModelConfig{
			"gpt-4o": {BaseURL: "https://api.openai.com/v1", APIKey: ""},
		},
	}
	err := ValidateLLMConfig(cfg)
	if err == nil {
		t.Fatal("空 api_key 应返回错误")
	}
	if !strings.Contains(err.Error(), "api_key 为空") {
		t.Errorf("错误信息不符合预期: %s", err.Error())
	}
}

func TestValidateLLMConfigEnvVarNotSet(t *testing.T) {
	// 确保环境变量未设置
	os.Unsetenv("TEST_API_KEY_FOR_UNIT_TEST")

	cfg := &LLMConfig{
		DefaultModel: "gpt-4o",
		Models: map[string]ModelConfig{
			"gpt-4o": {BaseURL: "https://api.openai.com/v1", APIKey: "${TEST_API_KEY_FOR_UNIT_TEST}"},
		},
	}
	err := ValidateLLMConfig(cfg)
	if err == nil {
		t.Fatal("环境变量未设置应返回错误")
	}
	if !strings.Contains(err.Error(), "TEST_API_KEY_FOR_UNIT_TEST 未设置") {
		t.Errorf("错误信息不符合预期: %s", err.Error())
	}
}

func TestValidateLLMConfigEmptyBaseURL(t *testing.T) {
	cfg := &LLMConfig{
		DefaultModel: "gpt-4o",
		Models: map[string]ModelConfig{
			"gpt-4o": {BaseURL: "", APIKey: "test-key"},
		},
	}
	err := ValidateLLMConfig(cfg)
	if err == nil {
		t.Fatal("空 base_url 应返回错误")
	}
	if !strings.Contains(err.Error(), "base_url 为空") {
		t.Errorf("错误信息不符合预期: %s", err.Error())
	}
}

func TestValidateLLMConfigEnvVarSet(t *testing.T) {
	// 设置环境变量
	os.Setenv("TEST_API_KEY_SET", "test-value")
	defer os.Unsetenv("TEST_API_KEY_SET")

	cfg := &LLMConfig{
		DefaultModel: "gpt-4o",
		Models: map[string]ModelConfig{
			"gpt-4o": {BaseURL: "https://api.openai.com/v1", APIKey: "${TEST_API_KEY_SET}"},
		},
	}
	err := ValidateLLMConfig(cfg)
	if err != nil {
		t.Fatalf("环境变量已设置时不应返回错误: %v", err)
	}
}