package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
)

// TestToolsHandler_UnknownAgent 验证 X-Agent-Name 指向未注册子 Agent 时返 400。
func TestToolsHandler_UnknownAgent(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	h := NewToolsHandler(nil, reg, logger.NewNop())
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.Header.Set("X-Agent-Name", "nope")
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "Unknown agent") {
		t.Errorf("body should contain 'Unknown agent', got %s", rc.Response.Body())
	}
}

// TestToolsHandler_NilRegistryReturns400 验证 registry == nil 时同样 400。
// 这是配置异常，但用户视角仍为 unknown_agent；运维通过日志区分。
func TestToolsHandler_NilRegistryReturns400(t *testing.T) {
	h := NewToolsHandler(nil, nil, logger.NewNop())
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.Header.Set("X-Agent-Name", "any")
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d", rc.Response.StatusCode())
	}
}

// TestToolsHandler_MainAgentExposesCallAgent 验证主 Agent 路径下，
// /tools 响应包含合成 group "_builtin"，其内含 call_agent 工具——
// 与 Executor.Execute 的 ExtraTools 注入一致。
//
// 即使主 Agent 没声明 MCP（mcpManager==nil），call_agent 也应可见。
func TestToolsHandler_MainAgentExposesCallAgent(t *testing.T) {
	h := NewToolsHandler(nil, agent.NewRegistryForTest(1), logger.NewNop())
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
	}
	var grouped map[string]types.ToolsGroup
	if err := json.Unmarshal(rc.Response.Body(), &grouped); err != nil {
		t.Fatalf("响应应是 group map JSON，解析失败：%v body=%s", err, rc.Response.Body())
	}
	builtin, ok := grouped["_builtin"]
	if !ok {
		t.Fatalf("主 Agent 响应应含 _builtin group，实际 %+v", grouped)
	}
	if len(builtin.Tools) != 1 || builtin.Tools[0].Name != "call_agent" {
		t.Errorf("_builtin group 应只含 call_agent，实际 %+v", builtin.Tools)
	}
}

// TestToolsHandler_GrootHeaderEquivalentToOmit 验证 X-Agent-Name=groot 与不传 header 等价。
// 主 Agent 路径下 call_agent 同样可见。
func TestToolsHandler_GrootHeaderEquivalentToOmit(t *testing.T) {
	h := NewToolsHandler(nil, agent.NewRegistryForTest(1), logger.NewNop())
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.Header.Set("X-Agent-Name", agent.MainAgentName)
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
	}
	var grouped map[string]types.ToolsGroup
	if err := json.Unmarshal(rc.Response.Body(), &grouped); err != nil {
		t.Fatalf("解析失败：%v body=%s", err, rc.Response.Body())
	}
	if _, ok := grouped["_builtin"]; !ok {
		t.Errorf("X-Agent-Name=groot 应等价于不传，响应应含 _builtin group，实际 %+v", grouped)
	}
}

// TestToolsHandler_SubAgentDoesNotExposeCallAgent 验证 X-Agent-Name 指向已注册子 Agent，
// 但该子 Agent 的 MCPManager 为 nil（未声明 MCP 配置）时：返回 200 + 空 group map。
// 关键：Solo 模式下不挂载 call_agent（与 executor.go 的「Solo 模式不挂 call_agent」一致）。
//
// 这是 Solo 模式子 Agent 无 MCP 时的常态——子 Agent 可能只用 LLM + skills，
// 不依赖任何 MCP 工具，且子 Agent 不能再级联调度其它子 Agent。
func TestToolsHandler_SubAgentDoesNotExposeCallAgent(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	reg.SetEntryForTest("llm-only-agent", &agent.SubAgentEntry{
		Name:       "llm-only-agent",
		MCPManager: nil, // 显式声明无 MCP
	})
	h := NewToolsHandler(nil, reg, logger.NewNop())

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.Header.Set("X-Agent-Name", "llm-only-agent")
	h.Serve(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200, got %d body=%s", got, rc.Response.Body())
	}
	var grouped map[string]types.ToolsGroup
	if err := json.Unmarshal(rc.Response.Body(), &grouped); err != nil {
		t.Fatalf("解析失败：%v body=%s", err, rc.Response.Body())
	}
	if _, ok := grouped["_builtin"]; ok {
		t.Errorf("Solo 模式 /tools 不应含 _builtin (call_agent)，实际 %+v", grouped)
	}
}

// TestToolsHandler_GroupCarriesMCPTypeAndDescription 验证响应分组回填 MCP 定义中的
// type 与 description（前端在分组标题处展示类型标签与描述）；合成分组 _builtin 二者为空。
func TestToolsHandler_GroupCarriesMCPTypeAndDescription(t *testing.T) {
	mgr := mcp.NewManager(logger.NewNop())
	mgr.Register(
		&mcp.MCPConfig{Name: "api-proxy", Type: mcp.MCPTypeSSE, Description: "HTTP API 代理", IsActive: true},
		[]mcp.ToolDefinition{{Name: "call_api", Description: "Call predefined HTTP APIs"}},
		"",
	)
	h := NewToolsHandler(mgr, agent.NewRegistryForTest(1), logger.NewNop())

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Serve(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200, got %d body=%s", got, rc.Response.Body())
	}
	var grouped map[string]types.ToolsGroup
	if err := json.Unmarshal(rc.Response.Body(), &grouped); err != nil {
		t.Fatalf("解析失败：%v body=%s", err, rc.Response.Body())
	}

	g, ok := grouped["api-proxy"]
	if !ok {
		t.Fatalf("响应应含 api-proxy group，实际 %+v", grouped)
	}
	if g.Type != "sse" {
		t.Errorf("api-proxy group Type 应为 sse，实际 %q", g.Type)
	}
	if g.Description != "HTTP API 代理" {
		t.Errorf("api-proxy group Description 应为 MCP 定义中的描述，实际 %q", g.Description)
	}
	if g.Total != 1 || len(g.Tools) != 1 || g.Tools[0].Name != "call_api" {
		t.Errorf("api-proxy group 工具列表不符，实际 %+v", g)
	}

	builtin, ok := grouped["_builtin"]
	if !ok {
		t.Fatalf("主 Agent 响应应含 _builtin group，实际 %+v", grouped)
	}
	if builtin.Type != "" || builtin.Description != "" {
		t.Errorf("_builtin 合成分组不应携带 type/description，实际 type=%q desc=%q", builtin.Type, builtin.Description)
	}
}
