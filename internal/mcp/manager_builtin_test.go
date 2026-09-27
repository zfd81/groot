// Package mcp 内置工具门控测试。
package mcp

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// fakeTool 只实现 Info 与 InvokableRun，供注册与取用测试使用。
type fakeTool struct{ name string }

func (f *fakeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name, Desc: "测试工具"}, nil
}

func (f *fakeTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", nil
}

// newGateTestManager 建一个只输出 error 级别的 Manager。
// 名字带 Gate 前缀：既有 manager_test.go 已定义了无参的 newTestManager。
func newGateTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(logger.New(config.LoggingConfig{
		Level: "error", Format: "console", Output: []string{"stdout"},
	}))
}

// countBuiltin 数 GetTools 结果里名为 name 的工具数量。
func countBuiltin(tools []tool.BaseTool, name string) int {
	n := 0
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err == nil && info != nil && info.Name == name {
			n++
		}
	}
	return n
}

// TestGetTools_GateBlocksBuiltin 验证门控关闭时内置工具不出现在 GetTools 结果里，
// 但仍留在注册表中：开关重新打开后无需再注册即可恢复。
func TestGetTools_GateBlocksBuiltin(t *testing.T) {
	m := newGateTestManager(t)
	allow := false
	m.SetBuiltinGate(BuiltinGroupSchedule, func() bool { return allow })
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"schedule_create": &fakeTool{name: "schedule_create"},
	})

	if n := countBuiltin(m.GetTools(), "schedule_create"); n != 0 {
		t.Fatalf("门控关闭时 GetTools 返回了 %d 个 schedule_create，want 0", n)
	}

	allow = true
	if n := countBuiltin(m.GetTools(), "schedule_create"); n != 1 {
		t.Fatalf("门控打开后 GetTools 返回了 %d 个 schedule_create，want 1", n)
	}
}

// TestGetTools_NoGateAllowsBuiltin 验证未设门控的内置工具一律可见，
// 保证既有内置工具不受本次改动影响。
func TestGetTools_NoGateAllowsBuiltin(t *testing.T) {
	m := newGateTestManager(t)
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"other_tool": &fakeTool{name: "other_tool"},
	})
	if n := countBuiltin(m.GetTools(), "other_tool"); n != 1 {
		t.Fatalf("无门控工具返回 %d 个，want 1", n)
	}
}

// TestListTools_GateBlocksBuiltin 验证门控关闭时工具清单接口也不列出该工具，
// 使界面看到的与模型看到的一致。
func TestListTools_GateBlocksBuiltin(t *testing.T) {
	m := newGateTestManager(t)
	m.SetBuiltinGate(BuiltinGroupSchedule, func() bool { return false })
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"schedule_create": &fakeTool{name: "schedule_create"},
	})
	for _, info := range m.ListTools() {
		if info.Name == "schedule_create" {
			t.Fatal("门控关闭时 ListTools 仍列出了 schedule_create")
		}
	}
	if got := m.ToolCount(); got != 0 {
		t.Errorf("ToolCount() = %d, want 0", got)
	}
}

// TestGetTool_GateBlocksBuiltin 验证单个工具查询也走门控：
// 关闭时按「不存在」返回，打开后无需重新注册即可查到。
func TestGetTool_GateBlocksBuiltin(t *testing.T) {
	m := newGateTestManager(t)
	allow := false
	m.SetBuiltinGate(BuiltinGroupSchedule, func() bool { return allow })
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"schedule_create": &fakeTool{name: "schedule_create"},
	})

	if info, ok := m.GetTool("schedule_create"); ok || info != nil {
		t.Fatalf("门控关闭时 GetTool 返回 (%v, %v)，want (nil, false)", info, ok)
	}

	allow = true
	if info, ok := m.GetTool("schedule_create"); !ok || info == nil {
		t.Fatalf("门控打开后 GetTool 返回 (%v, %v)，want (非 nil, true)", info, ok)
	}
}

// TestGate_DoesNotAffectMCPTools 验证门控只作用于内置工具：
// 即便某个 MCP 的名字恰好与内置组名相同，它的工具也不受门控影响；
// 同组的内置工具则照常被挡住，把「作用域判断」与「门控本身」一起锁死。
func TestGate_DoesNotAffectMCPTools(t *testing.T) {
	m := newGateTestManager(t)
	m.Register(&MCPConfig{Name: BuiltinGroupSchedule, Type: MCPTypeStdio, IsActive: true},
		[]ToolDefinition{{Name: "x", Description: "MCP 工具"}}, "")
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"schedule_create": &fakeTool{name: "schedule_create"},
	})
	m.SetBuiltinGate(BuiltinGroupSchedule, func() bool { return false })

	foundMCP, foundBuiltin := false, false
	for _, info := range m.ListTools() {
		switch info.Name {
		case "x":
			foundMCP = true
		case "schedule_create":
			foundBuiltin = true
		}
	}
	if !foundMCP {
		t.Fatal("与组名同名的 MCP 工具 x 被门控挡住了，want 仍可见")
	}
	if foundBuiltin {
		t.Fatal("门控关闭时 ListTools 仍列出了同组内置工具 schedule_create")
	}
	if _, ok := m.GetTool("x"); !ok {
		t.Fatal("GetTool(\"x\") 返回 false，want true")
	}
	if _, ok := m.GetTool("schedule_create"); ok {
		t.Fatal("GetTool(\"schedule_create\") 返回 true，want false")
	}
	if got := m.ToolCount(); got != 1 {
		t.Errorf("ToolCount() = %d, want 1", got)
	}
}

// TestGetTools_GateEvaluatedOncePerCall 验证每次取用只对每个门控求值一次，
// 而不是按工具逐个求值：门控将来要读配置表，求值次数直接决定库读次数。
func TestGetTools_GateEvaluatedOncePerCall(t *testing.T) {
	m := newGateTestManager(t)
	calls := 0
	m.SetBuiltinGate(BuiltinGroupSchedule, func() bool { calls++; return true })
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"schedule_create": &fakeTool{name: "schedule_create"},
		"schedule_list":   &fakeTool{name: "schedule_list"},
		"schedule_delete": &fakeTool{name: "schedule_delete"},
	})

	m.GetTools()
	if calls != 1 {
		t.Fatalf("GetTools 后门控求值 %d 次，want 1", calls)
	}
	m.ListTools()
	if calls != 2 {
		t.Fatalf("GetTools+ListTools 后门控求值 %d 次，want 2", calls)
	}
}

// TestHiddenGroups_SkipsGateWhenNoBuiltinTools 验证没有内置工具时不求值门控：
// Leader 降级后注销了内置工具，门控仍挂着，此时每次取工具都不该再读配置表。
func TestHiddenGroups_SkipsGateWhenNoBuiltinTools(t *testing.T) {
	m := newGateTestManager(t)
	calls := 0
	m.SetBuiltinGate(BuiltinGroupSchedule, func() bool { calls++; return true })
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"schedule_create": &fakeTool{name: "schedule_create"},
	})

	m.GetTools()
	if calls != 1 {
		t.Fatalf("有内置工具时 GetTools 后门控求值 %d 次，want 1", calls)
	}

	m.UnregisterBuiltinTools()
	m.GetTools()
	m.ListTools()
	if calls != 1 {
		t.Fatalf("注销内置工具后仍求值了门控，calls = %d，want 1", calls)
	}
}
