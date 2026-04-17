package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/mcp"
)

// ToolsHandler handles GET /tools
type ToolsHandler struct {
	mcpManager *mcp.Manager
}

// NewToolsHandler creates a new tools handler
func NewToolsHandler(mcpMgr *mcp.Manager) *ToolsHandler {
	return &ToolsHandler{mcpManager: mcpMgr}
}

// Serve handles the tools request
func (h *ToolsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	tools := h.mcpManager.ListTools()

	toolInfos := make([]api.ToolInfo, len(tools))
	for i, t := range tools {
		toolInfos[i] = api.ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			MCP:         t.MCP,
		}
	}

	resp := api.ToolsResponse{
		Tools: toolInfos,
		Total: len(toolInfos),
	}

	rc.JSON(200, resp)
}
