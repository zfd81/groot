package config

import (
	"os"
	"testing"
)

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
