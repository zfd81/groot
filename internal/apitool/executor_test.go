package apitool

import (
	"testing"
)

func TestExecutor_validateParameters(t *testing.T) {
	executor := NewExecutor(nil)

	tests := []struct {
		name        string
		config      *APIToolConfig
		args        map[string]interface{}
		expectError bool
	}{
		{
			name: "必填参数已提供",
			config: &APIToolConfig{
				Parameters: []Parameter{
					{Name: "required_param", Type: "string", Required: true, Description: "必填参数"},
				},
			},
			args:        map[string]interface{}{"required_param": "value"},
			expectError: false,
		},
		{
			name: "必填参数缺失",
			config: &APIToolConfig{
				Parameters: []Parameter{
					{Name: "required_param", Type: "string", Required: true, Description: "必填参数"},
				},
			},
			args:        map[string]interface{}{},
			expectError: true,
		},
		{
			name: "必填参数有默认值",
			config: &APIToolConfig{
				Parameters: []Parameter{
					{Name: "param_with_default", Type: "string", Required: true, Default: "default_value", Description: "带默认值的必填参数"},
				},
			},
			args:        map[string]interface{}{},
			expectError: false,
		},
		{
			name: "非必填参数缺失",
			config: &APIToolConfig{
				Parameters: []Parameter{
					{Name: "optional_param", Type: "string", Required: false, Description: "可选参数"},
				},
			},
			args:        map[string]interface{}{},
			expectError: false,
		},
		{
			name:        "无参数定义",
			config:      &APIToolConfig{},
			args:        map[string]interface{}{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := executor.validateParameters(tt.config, tt.args)
			if tt.expectError && err == nil {
				t.Error("validateParameters() should return error")
			}
			if !tt.expectError && err != nil {
				t.Errorf("validateParameters() should not return error: %v", err)
			}
		})
	}
}

func TestExecutor_mergeParameters(t *testing.T) {
	executor := NewExecutor(nil)

	t.Run("合并传入参数和默认值", func(t *testing.T) {
		config := &APIToolConfig{
			Parameters: []Parameter{
				{Name: "param1", Type: "string", Required: false, Default: "default1", Description: "参数1"},
				{Name: "param2", Type: "string", Required: false, Default: "default2", Description: "参数2"},
			},
		}
		args := map[string]interface{}{
			"param1": "user_value",
		}

		result := executor.mergeParameters(config, args)

		if result["param1"] != "user_value" {
			t.Errorf("param1 should be 'user_value', got: %v", result["param1"])
		}
		if result["param2"] != "default2" {
			t.Errorf("param2 should be 'default2', got: %v", result["param2"])
		}
	})

	t.Run("传入值覆盖默认值", func(t *testing.T) {
		config := &APIToolConfig{
			Parameters: []Parameter{
				{Name: "param", Type: "string", Required: true, Default: "default", Description: "参数"},
			},
		}
		args := map[string]interface{}{
			"param": "override",
		}

		result := executor.mergeParameters(config, args)

		if result["param"] != "override" {
			t.Errorf("param should be 'override', got: %v", result["param"])
		}
	})

	t.Run("无参数时返回空map", func(t *testing.T) {
		config := &APIToolConfig{}
		args := map[string]interface{}{}

		result := executor.mergeParameters(config, args)

		if len(result) != 0 {
			t.Errorf("result should be empty, got %d items", len(result))
		}
	})
}

func TestExecutor_replaceVariables(t *testing.T) {
	executor := NewExecutor(nil)

	t.Run("替换参数变量", func(t *testing.T) {
		params := map[string]interface{}{
			"city": "北京",
			"unit": "celsius",
		}
		result := executor.replaceVariables("https://api.example.com/weather/${city}?unit=${unit}", params)
		expected := "https://api.example.com/weather/北京?unit=celsius"
		if result != expected {
			t.Errorf("replaceVariables() = %v, want %v", result, expected)
		}
	})

	t.Run("未找到的参数保留原样", func(t *testing.T) {
		params := map[string]interface{}{
			"city": "北京",
		}
		result := executor.replaceVariables("https://api.example.com/${city}/${unknown}", params)
		// unknown 变量保留原样
		if !contains(result, "${unknown}") {
			t.Errorf("unknown param should remain as ${unknown}, got: %v", result)
		}
	})

	t.Run("空参数返回原字符串", func(t *testing.T) {
		params := map[string]interface{}{}
		input := "https://api.example.com/static"
		result := executor.replaceVariables(input, params)
		if result != input {
			t.Errorf("replaceVariables() should return original string when no params")
		}
	})
}

func TestExecutor_replaceVariablesInMap(t *testing.T) {
	executor := NewExecutor(nil)

	params := map[string]interface{}{
		"token": "abc123",
		"key":   "xyz789",
	}

	input := map[string]string{
		"Authorization": "Bearer ${token}",
		"X-API-Key":     "${key}",
	}

	result := executor.replaceVariablesInMap(input, params)

	if result["Authorization"] != "Bearer abc123" {
		t.Errorf("Authorization should be 'Bearer abc123', got: %v", result["Authorization"])
	}
	if result["X-API-Key"] != "xyz789" {
		t.Errorf("X-API-Key should be 'xyz789', got: %v", result["X-API-Key"])
	}
}

func TestExecutor_replaceVariablesInBody(t *testing.T) {
	executor := NewExecutor(nil)

	t.Run("替换嵌套body中的变量", func(t *testing.T) {
		params := map[string]interface{}{
			"name":  "张三",
			"email": "test@example.com",
		}

		body := map[string]interface{}{
			"user": map[string]interface{}{
				"name":  "${name}",
				"email": "${email}",
			},
			"action": "create",
		}

		result := executor.replaceVariablesInBody(body, params)

		userMap := result["user"].(map[string]interface{})
		if userMap["name"] != "张三" {
			t.Errorf("user.name should be '张三', got: %v", userMap["name"])
		}
		if userMap["email"] != "test@example.com" {
			t.Errorf("user.email should be 'test@example.com', got: %v", userMap["email"])
		}
		if result["action"] != "create" {
			t.Errorf("action should remain 'create', got: %v", result["action"])
		}
	})

	t.Run("nil body返回nil", func(t *testing.T) {
		result := executor.replaceVariablesInBody(nil, map[string]interface{}{})
		if result != nil {
			t.Errorf("replaceVariablesInBody(nil) should return nil, got: %v", result)
		}
	})
}

func TestExecutor_replaceInArrayRecursive(t *testing.T) {
	executor := NewExecutor(nil)

	params := map[string]interface{}{
		"item1": "value1",
		"item2": "value2",
	}

	arr := []interface{}{
		"${item1}",
		map[string]interface{}{
			"nested": "${item2}",
		},
	}

	result := executor.replaceInArrayRecursive(arr, params)

	if result[0] != "value1" {
		t.Errorf("array[0] should be 'value1', got: %v", result[0])
	}

	nested := result[1].(map[string]interface{})
	if nested["nested"] != "value2" {
		t.Errorf("nested.nested should be 'value2', got: %v", nested["nested"])
	}
}

func TestExecutor_buildQueryString(t *testing.T) {
	executor := NewExecutor(nil)

	query := map[string]string{
		"city": "北京",
		"unit": "celsius",
	}

	result := executor.buildQueryString(query)

	// URL编码后检查
	if !contains(result, "city") || !contains(result, "unit") {
		t.Errorf("buildQueryString() should contain city and unit, got: %v", result)
	}
}

func TestExecutor_buildBody(t *testing.T) {
	executor := NewExecutor(nil)

	t.Run("JSON body", func(t *testing.T) {
		body := map[string]interface{}{
			"name":  "test",
			"value": 123,
		}
		result := executor.buildBody(body, "json")
		// 结果应该是 JSON 格式的 Reader
		if result == nil {
			t.Error("buildBody() should return non-nil Reader for json")
		}
	})

	t.Run("Form body", func(t *testing.T) {
		body := map[string]interface{}{
			"name":  "test",
			"value": "123",
		}
		result := executor.buildBody(body, "form")
		if result == nil {
			t.Error("buildBody() should return non-nil Reader for form")
		}
	})

	t.Run("默认使用JSON", func(t *testing.T) {
		body := map[string]interface{}{
			"name": "test",
		}
		result := executor.buildBody(body, "")
		if result == nil {
			t.Error("buildBody() should default to json format")
		}
	})
}