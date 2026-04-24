package apitool

import (
	"os"
	"testing"
)

func TestExtractEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		config   *APIToolConfig
		expected []string
	}{
		{
			name: "从URL提取环境变量",
			config: &APIToolConfig{
				URL: "https://api.example.com/$${API_KEY}/data",
			},
			expected: []string{"API_KEY"},
		},
		{
			name: "从Headers提取环境变量",
			config: &APIToolConfig{
				URL: "https://api.example.com/data",
				Headers: map[string]string{
					"Authorization": "Bearer $${TOKEN}",
					"X-API-Key":     "$${API_KEY}",
				},
			},
			expected: []string{"TOKEN", "API_KEY"},
		},
		{
			name: "从Query提取环境变量",
			config: &APIToolConfig{
				URL: "https://api.example.com/data",
				Query: map[string]string{
					"api_key": "$${API_KEY}",
				},
			},
			expected: []string{"API_KEY"},
		},
		{
			name: "从Body提取环境变量",
			config: &APIToolConfig{
				URL: "https://api.example.com/data",
				Body: map[string]interface{}{
					"apiKey": "$${API_KEY}",
					"nested": map[string]interface{}{
						"token": "$${NESTED_TOKEN}",
					},
				},
			},
			expected: []string{"API_KEY", "NESTED_TOKEN"},
		},
		{
			name: "从Auth提取环境变量",
			config: &APIToolConfig{
				URL: "https://api.example.com/data",
				Auth: &AuthConfig{
					Type:  AuthTypeBearer,
					Token: "$${AUTH_TOKEN}",
				},
			},
			expected: []string{"AUTH_TOKEN"},
		},
		{
			name: "无环境变量",
			config: &APIToolConfig{
				URL: "https://api.example.com/data",
			},
			expected: []string{},
		},
		{
			name: "多个相同环境变量去重",
			config: &APIToolConfig{
				URL: "https://api.example.com/$${API_KEY}/$${API_KEY}",
				Headers: map[string]string{
					"X-Key": "$${API_KEY}",
				},
			},
			expected: []string{"API_KEY"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractEnvVars(tt.config)
			if len(result) != len(tt.expected) {
				t.Errorf("ExtractEnvVars() returned %d vars, want %d", len(result), len(tt.expected))
				return
			}
			for _, envVar := range tt.expected {
				found := false
				for _, r := range result {
					if r == envVar {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ExtractEnvVars() missing expected env var %s", envVar)
				}
			}
		})
	}
}

func TestExtractEnvVarsFromBodyWithArray(t *testing.T) {
	config := &APIToolConfig{
		URL: "https://api.example.com/data",
		Body: map[string]interface{}{
			"items": []interface{}{
				"$${ITEM_TOKEN}",
				map[string]interface{}{
					"nested": "$${NESTED_IN_ARRAY}",
				},
			},
		},
	}

	result := ExtractEnvVars(config)
	expected := []string{"ITEM_TOKEN", "NESTED_IN_ARRAY"}

	if len(result) != len(expected) {
		t.Errorf("ExtractEnvVars() returned %d vars, want %d", len(result), len(expected))
	}
}

func TestValidateEnvVars(t *testing.T) {
	// 设置测试环境变量
	os.Setenv("TEST_VAR_1", "value1")
	os.Setenv("TEST_VAR_2", "value2")
	defer os.Unsetenv("TEST_VAR_1")
	defer os.Unsetenv("TEST_VAR_2")

	t.Run("所有环境变量已设置", func(t *testing.T) {
		config := &APIToolConfig{
			Name: "test_tool",
			URL:  "https://api.example.com/$${TEST_VAR_1}",
			Headers: map[string]string{
				"X-Token": "$${TEST_VAR_2}",
			},
		}
		if err := ValidateEnvVars(config); err != nil {
			t.Errorf("ValidateEnvVars() should not error when vars are set: %v", err)
		}
	})

	t.Run("环境变量未设置", func(t *testing.T) {
		config := &APIToolConfig{
			Name: "test_tool",
			URL:  "https://api.example.com/$${UNSET_VAR}",
		}
		err := ValidateEnvVars(config)
		if err == nil {
			t.Error("ValidateEnvVars() should error when var is not set")
		}
		expectedMsg := "环境变量 UNSET_VAR 未设置"
		if err != nil && !contains(err.Error(), expectedMsg) {
			t.Errorf("Error message should contain '%s', got: %v", expectedMsg, err.Error())
		}
	})
}

func TestCheckToolNameConflict(t *testing.T) {
	tests := []struct {
		name              string
		apiTools          []*APIToolConfig
		existingToolNames []string
		expectError       bool
	}{
		{
			name: "无冲突",
			apiTools: []*APIToolConfig{
				{Name: "api_tool_1"},
				{Name: "api_tool_2"},
			},
			existingToolNames: []string{"mcp_tool_1", "mcp_tool_2"},
			expectError:       false,
		},
		{
			name: "有冲突",
			apiTools: []*APIToolConfig{
				{Name: "conflicting_tool"},
			},
			existingToolNames: []string{"conflicting_tool"},
			expectError:       true,
		},
		{
			name:              "空列表无冲突",
			apiTools:          []*APIToolConfig{},
			existingToolNames: []string{"mcp_tool"},
			expectError:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckToolNameConflict(tt.apiTools, tt.existingToolNames)
			if tt.expectError && err == nil {
				t.Error("CheckToolNameConflict() should return error for conflict")
			}
			if !tt.expectError && err != nil {
				t.Errorf("CheckToolNameConflict() should not return error: %v", err)
			}
		})
	}
}

func TestValidateAllEnvVars(t *testing.T) {
	os.Setenv("TEST_VAR_ALL", "value")
	defer os.Unsetenv("TEST_VAR_ALL")

	t.Run("所有配置的环境变量都已设置", func(t *testing.T) {
		configs := []*APIToolConfig{
			{Name: "tool1", URL: "https://api.example.com/$${TEST_VAR_ALL}"},
			{Name: "tool2", URL: "https://api.example.com/$${TEST_VAR_ALL}"},
		}
		if err := ValidateAllEnvVars(configs); err != nil {
			t.Errorf("ValidateAllEnvVars() should not error: %v", err)
		}
	})

	t.Run("某个配置的环境变量未设置", func(t *testing.T) {
		configs := []*APIToolConfig{
			{Name: "tool1", URL: "https://api.example.com/$${TEST_VAR_ALL}"},
			{Name: "tool2", URL: "https://api.example.com/$${UNSET_VAR_ALL}"},
		}
		err := ValidateAllEnvVars(configs)
		if err == nil {
			t.Error("ValidateAllEnvVars() should error when one config has unset var")
		}
	})
}

func TestUniqueStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "空列表",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "无重复",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "有重复",
			input:    []string{"a", "b", "a", "c", "b"},
			expected: []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := uniqueStrings(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("uniqueStrings() returned %d items, want %d", len(result), len(tt.expected))
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}