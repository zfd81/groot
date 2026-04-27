package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/mcp"
)

// ToolsHandler handles GET /tools
type ToolsHandler struct {
	mcpManager *mcp.Manager
}

// NewToolsHandler creates a new tools handler
func NewToolsHandler(mcpMgr *mcp.Manager) *ToolsHandler {
	return &ToolsHandler{
		mcpManager: mcpMgr,
	}
}

// Serve handles the tools request
func (h *ToolsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	// 获取 MCP 工具
	mcpTools := h.mcpManager.ListTools()

	// 按 MCP 分组
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

	rc.JSON(200, grouped)
}