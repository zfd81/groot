package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/skill"
)

// HealthHandler handles GET /health
type HealthHandler struct {
	config        config.Config
	skillRegistry *skill.Registry
	mcpManager    *mcp.Manager
	memoryManager *memory.Manager
	executor      *agent.Executor
	startTime     time.Time
	logger        *logger.Logger
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(
	cfg config.Config,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	memMgr *memory.Manager,
	exec *agent.Executor,
	log *logger.Logger,
) *HealthHandler {
	return &HealthHandler{
		config:        cfg,
		skillRegistry: skills,
		mcpManager:    mcpMgr,
		memoryManager: memMgr,
		executor:      exec,
		startTime:     time.Now(),
		logger:        log,
	}
}

// Serve handles the health request
func (h *HealthHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	h.logger.Info("API request: /health")

	uptime := time.Since(h.startTime)
	uptimeStr := formatUptime(uptime)

	// Get memory stats
	var sessionCount int
	if h.memoryManager != nil {
		sessions, total, _ := h.memoryManager.ListSessions(1, 0)
		sessionCount = total
		_ = sessions
	}

	// Build MCP info with tool count
	mcpInfos := make([]map[string]interface{}, 0)
	for _, info := range h.mcpManager.ListWithToolCount() {
		mcpInfo := map[string]interface{}{
			"name":        info.Name,
			"type":        info.Type,
			"description": info.Description,
			"isActive":    info.IsActive,
			"tools_count": info.ToolCount,
		}
		if info.Error != "" {
			mcpInfo["error"] = info.Error
			h.logger.Error("MCP has discovery error",
				zap.String("name", info.Name),
				zap.String("error", info.Error))
		}
		mcpInfos = append(mcpInfos, mcpInfo)
	}

	resp := types.HealthResponse{
		Status:  "healthy",
		Version: h.config.Agent.Version,
		Uptime:  uptimeStr,
		Checks: map[string]types.CheckInfo{
			"llm": {
				Status: "healthy",
				Info:   map[string]string{"model": h.config.LLM.DefaultModel},
			},
			"mcp_servers": {
				Status: "healthy",
				Info:   mcpInfos,
			},
			"skills": {
				Status: "healthy",
				Info:   map[string]int{"count": h.skillRegistry.Count()},
			},
			"memory": {
				Status: "healthy",
				Info:   map[string]int{"sessions": sessionCount},
			},
		},
		Metrics: map[string]interface{}{
			"chats_running": h.executor.RunningCount(),
		},
	}

	rc.JSON(200, resp)
}

// formatUptime formats uptime duration
func formatUptime(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) - hours*60

	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%dm", hours, minutes)
}
