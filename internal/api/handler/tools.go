package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
)

// ToolsHandler handles GET /tools
type ToolsHandler struct {
	mcpManager *mcp.Manager
	logger     *logger.Logger
}

// NewToolsHandler creates a new tools handler
func NewToolsHandler(mcpMgr *mcp.Manager, log *logger.Logger) *ToolsHandler {
	return &ToolsHandler{
		mcpManager: mcpMgr,
		logger:     log,
	}
}

// Serve handles the tools request
func (h *ToolsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	h.logger.Info("API request: /tools")

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

	// 确保所有 MCP 都显示（包括工具数为 0 的）
	mcpInfos := h.mcpManager.ListWithToolCount()
	for _, info := range mcpInfos {
		if _, exists := grouped[info.Name]; !exists {
			grouped[info.Name] = types.ToolsGroup{
				Tools: []types.ToolInfo{},
				Total: 0,
			}
		}
		// Log MCPs with errors
		if info.Error != "" {
			h.logger.Error("MCP has discovery error",
				zap.String("name", info.Name),
				zap.String("error", info.Error))
		}
	}

	rc.JSON(200, grouped)
}