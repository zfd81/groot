package apitool

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestNewAPIToolAdapter(t *testing.T) {
	config := &APIToolConfig{
		Name:        "test_adapter",
		Description: "测试适配器",
		URL:         "https://api.example.com/test",
		Method:      "GET",
	}
	manager := NewManager(nil)

	adapter := NewAPIToolAdapter(config, manager, nil)
	if adapter == nil {
		t.Error("NewAPIToolAdapter() should return non-nil adapter")
	}
	if adapter.config != config {
		t.Error("Adapter should store config reference")
	}
	if adapter.manager != manager {
		t.Error("Adapter should store manager reference")
	}
}

func TestAPIToolAdapter_convertType(t *testing.T) {
	adapter := NewAPIToolAdapter(nil, nil, nil)

	tests := []struct {
		name     string
		input    string
		expected schema.DataType
	}{
		{"string", "string", schema.String},
		{"int", "int", schema.Number},
		{"integer", "integer", schema.Number},
		{"float", "float", schema.Number},
		{"number", "number", schema.Number},
		{"bool", "bool", schema.Boolean},
		{"boolean", "boolean", schema.Boolean},
		{"array", "array", schema.Array},
		{"object", "object", schema.Object},
		{"unknown defaults to string", "unknown", schema.String},
		{"empty defaults to string", "", schema.String},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.convertType(tt.input)
			if result != tt.expected {
				t.Errorf("convertType(%s) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestAPIToolAdapter_convertParameters(t *testing.T) {
	config := &APIToolConfig{
		Parameters: []Parameter{
			{Name: "param1", Type: "string", Required: true, Description: "参数1"},
			{Name: "param2", Type: "int", Required: false, Description: "参数2"},
			{Name: "param3", Type: "bool", Required: false, Default: true, Description: "参数3"},
		},
	}
	adapter := NewAPIToolAdapter(config, nil, nil)

	result := adapter.convertParameters(config.Parameters)

	if len(result) != 3 {
		t.Errorf("convertParameters() returned %d params, want 3", len(result))
	}

	// 检查 param1
	if result["param1"].Type != schema.String {
		t.Errorf("param1.Type = %v, want String", result["param1"].Type)
	}
	if result["param1"].Required != true {
		t.Error("param1.Required should be true")
	}
	if result["param1"].Desc != "参数1" {
		t.Errorf("param1.Desc = %v, want '参数1'", result["param1"].Desc)
	}

	// 检查 param2
	if result["param2"].Type != schema.Number {
		t.Errorf("param2.Type = %v, want Number", result["param2"].Type)
	}

	// 检查 param3
	if result["param3"].Type != schema.Boolean {
		t.Errorf("param3.Type = %v, want Boolean", result["param3"].Type)
	}
}

func TestAPIToolAdapter_convertParametersEmpty(t *testing.T) {
	adapter := NewAPIToolAdapter(&APIToolConfig{}, nil, nil)

	result := adapter.convertParameters([]Parameter{})

	if len(result) != 0 {
		t.Errorf("convertParameters(empty) should return empty map, got %d items", len(result))
	}
}