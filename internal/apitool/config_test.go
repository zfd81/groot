package apitool

import (
	"testing"
)

func TestAPIToolConfig_GetTimeout(t *testing.T) {
	tests := []struct {
		name     string
		timeout  int
		expected int
	}{
		{
			name:     "默认超时时间",
			timeout:  0,
			expected: DefaultTimeout,
		},
		{
			name:     "负数超时时间使用默认值",
			timeout:  -1,
			expected: DefaultTimeout,
		},
		{
			name:     "自定义超时时间",
			timeout:  60,
			expected: 60,
		},
		{
			name:     "最小超时时间",
			timeout:  1,
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &APIToolConfig{
				Timeout: tt.timeout,
			}
			if got := config.GetTimeout(); got != tt.expected {
				t.Errorf("GetTimeout() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAuthTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		authType AuthType
		expected string
	}{
		{"none", AuthTypeNone, "none"},
		{"bearer", AuthTypeBearer, "bearer"},
		{"basic", AuthTypeBasic, "basic"},
		{"apikey", AuthTypeAPIKey, "apikey"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.authType) != tt.expected {
				t.Errorf("AuthType %s = %v, want %v", tt.name, tt.authType, tt.expected)
			}
		})
	}
}

func TestParameterDefaults(t *testing.T) {
	param := Parameter{
		Name:        "test_param",
		Type:        "string",
		Required:    false,
		Default:     "default_value",
		Description: "测试参数",
	}

	if param.Name != "test_param" {
		t.Errorf("Parameter.Name = %v, want test_param", param.Name)
	}
	if param.Type != "string" {
		t.Errorf("Parameter.Type = %v, want string", param.Type)
	}
	if param.Required != false {
		t.Errorf("Parameter.Required = %v, want false", param.Required)
	}
	if param.Default != "default_value" {
		t.Errorf("Parameter.Default = %v, want default_value", param.Default)
	}
}