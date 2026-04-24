package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/apitool"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/mcp"
)

// ToolsHandler handles GET /tools
type ToolsHandler struct {
	mcpManager *mcp.Manager
	apiManager *apitool.Manager
}

// NewToolsHandler creates a new tools handler
func NewToolsHandler(mcpMgr *mcp.Manager, apiMgr *apitool.Manager) *ToolsHandler {
	return &ToolsHandler{
		mcpManager: mcpMgr,
		apiManager: apiMgr,
	}
}

// Serve handles the tools request
func (h *ToolsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	// 获取 MCP 工具
	mcpTools := h.mcpManager.ListTools()

	// 获取 API 工具
	apiTools := h.apiManager.List()

	// 按 MCP/API 分组
	grouped := make(map[string]types.ToolsGroup)

	// 添加 MCP 工具
	for _, t := range mcpTools {
		group := grouped[t.MCP]
		group.Tools = append(group.Tools, types.ToolInfo{
			Name:        t.Name,
			Description: t.Description,
		})
		group.Total++
		grouped[t.MCP] = group
	}

	// 添加 API 工具（分组名为 "api"）
	for _, t := range apiTools {
		group := grouped["api"]
		group.Tools = append(group.Tools, types.ToolInfo{
			Name:        t.Name,
			Description: t.Description,
		})
		group.Total++
		grouped["api"] = group
	}

	rc.JSON(200, grouped)
}
