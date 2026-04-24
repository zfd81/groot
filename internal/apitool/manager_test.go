package apitool

import (
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

func newTestLogger() *logger.Logger {
	return logger.New(config.LoggingConfig{
		Level:  "info",
		Format: "console",
		Output: []string{"stdout"},
	})
}

func TestNewManager(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)
	if manager == nil {
		t.Error("NewManager() should return non-nil Manager")
	}
	if manager.tools == nil {
		t.Error("Manager.tools should be initialized")
	}
	if manager.executor == nil {
		t.Error("Manager.executor should be initialized")
	}
}

func TestManager_Register(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)

	config := &APIToolConfig{
		Name:        "test_tool",
		Description: "测试工具",
		URL:         "https://api.example.com/test",
		Method:      "GET",
	}

	manager.Register(config)

	if manager.Count() != 1 {
		t.Errorf("Manager.Count() = %d, want 1", manager.Count())
	}

	retrieved, ok := manager.Get("test_tool")
	if !ok {
		t.Error("Manager.Get() should find registered tool")
	}
	if retrieved.Name != "test_tool" {
		t.Errorf("Retrieved tool name = %v, want test_tool", retrieved.Name)
	}
}

func TestManager_RegisterMultiple(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)

	configs := []*APIToolConfig{
		{Name: "tool1", Description: "工具1", URL: "https://api.example.com/1", Method: "GET"},
		{Name: "tool2", Description: "工具2", URL: "https://api.example.com/2", Method: "POST"},
		{Name: "tool3", Description: "工具3", URL: "https://api.example.com/3", Method: "DELETE"},
	}

	for _, cfg := range configs {
		manager.Register(cfg)
	}

	if manager.Count() != 3 {
		t.Errorf("Manager.Count() = %d, want 3", manager.Count())
	}
}

func TestManager_Get(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)

	t.Run("获取已注册的工具", func(t *testing.T) {
		manager.Register(&APIToolConfig{Name: "existing_tool", Description: "存在的工具", URL: "https://api.example.com", Method: "GET"})
		config, ok := manager.Get("existing_tool")
		if !ok {
			t.Error("Get() should return true for existing tool")
		}
		if config == nil {
			t.Error("Get() should return non-nil config")
		}
	})

	t.Run("获取未注册的工具", func(t *testing.T) {
		config, ok := manager.Get("non_existing_tool")
		if ok {
			t.Error("Get() should return false for non-existing tool")
		}
		if config != nil {
			t.Error("Get() should return nil for non-existing tool")
		}
	})
}

func TestManager_List(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)

	// 空管理器
	if len(manager.List()) != 0 {
		t.Error("Empty manager List() should return empty slice")
	}

	// 注册多个工具
	manager.Register(&APIToolConfig{Name: "tool1", Description: "工具1", URL: "https://api.example.com/1", Method: "GET"})
	manager.Register(&APIToolConfig{Name: "tool2", Description: "工具2", URL: "https://api.example.com/2", Method: "GET"})

	list := manager.List()
	if len(list) != 2 {
		t.Errorf("List() returned %d items, want 2", len(list))
	}
}

func TestManager_ListToolNames(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)

	manager.Register(&APIToolConfig{Name: "tool_a", Description: "A", URL: "https://api.example.com/a", Method: "GET"})
	manager.Register(&APIToolConfig{Name: "tool_b", Description: "B", URL: "https://api.example.com/b", Method: "GET"})

	names := manager.ListToolNames()
	if len(names) != 2 {
		t.Errorf("ListToolNames() returned %d items, want 2", len(names))
	}

	// 检查名称是否包含
	foundA := false
	foundB := false
	for _, name := range names {
		if name == "tool_a" {
			foundA = true
		}
		if name == "tool_b" {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Error("ListToolNames() should contain tool_a and tool_b")
	}
}

func TestManager_Count(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)

	if manager.Count() != 0 {
		t.Error("Empty manager Count() should return 0")
	}

	manager.Register(&APIToolConfig{Name: "tool", Description: "工具", URL: "https://api.example.com", Method: "GET"})
	if manager.Count() != 1 {
		t.Errorf("Count() = %d, want 1", manager.Count())
	}

	manager.Register(&APIToolConfig{Name: "tool2", Description: "工具2", URL: "https://api.example.com", Method: "GET"})
	if manager.Count() != 2 {
		t.Errorf("Count() = %d, want 2", manager.Count())
	}
}

func TestManager_GetExecutor(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)

	executor := manager.GetExecutor()
	if executor == nil {
		t.Error("GetExecutor() should return non-nil Executor")
	}
}

func TestManager_SameNameOverride(t *testing.T) {
	log := newTestLogger()
	manager := NewManager(log)

	// 注册同名工具会覆盖
	manager.Register(&APIToolConfig{Name: "tool", Description: "原始描述", URL: "https://api.example.com/1", Method: "GET"})
	manager.Register(&APIToolConfig{Name: "tool", Description: "新描述", URL: "https://api.example.com/2", Method: "POST"})

	if manager.Count() != 1 {
		t.Errorf("Same name should not increase count, got %d", manager.Count())
	}

	config, _ := manager.Get("tool")
	if config.Description != "新描述" {
		t.Errorf("Tool should be overridden, description = %v, want '新描述'", config.Description)
	}
}