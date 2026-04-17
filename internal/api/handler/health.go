package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/skill"
)

// HealthHandler handles GET /health
type HealthHandler struct {
	config        config.Config
	skillRegistry *skill.Registry
	mcpManager    *mcp.Manager
	executor      *agent.Executor
	startTime     time.Time
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(
	cfg config.Config,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	exec *agent.Executor,
) *HealthHandler {
	return &HealthHandler{
		config:        cfg,
		skillRegistry: skills,
		mcpManager:    mcpMgr,
		executor:      exec,
		startTime:     time.Now(),
	}
}

// Serve handles the health request
func (h *HealthHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	uptime := time.Since(h.startTime)
	uptimeStr := formatUptime(uptime)

	resp := api.HealthResponse{
		Status:  "healthy",
		Version: h.config.Agent.Version,
		Uptime:  uptimeStr,
		Checks: map[string]api.CheckInfo{
			"llm": {
				Status: "healthy",
				Info:   map[string]string{"model": h.config.LLM.ActiveModel},
			},
			"mcp_servers": {
				Status: "healthy",
				Info:   h.mcpManager.List(),
			},
			"skills": {
				Status: "healthy",
				Info:   map[string]int{"count": h.skillRegistry.Count()},
			},
		},
		Metrics: map[string]interface{}{
			"tasks_running": h.executor.RunningCount(),
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
