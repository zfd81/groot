package api

import (
	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/zfd81/groot/internal/api/handler"
	"github.com/zfd81/groot/internal/api/middleware"
)

// RegisterRoutes registers all API routes
func RegisterRoutes(h *server.Hertz,
	authMW *middleware.AuthMiddleware,
	rateLimitMW *middleware.RateLimitMiddleware,
	recoveryMW *middleware.RecoveryMiddleware,
	executeH *handler.ExecuteHandler,
	cancelH *handler.CancelHandler,
	statusH *handler.StatusHandler,
	historyH *handler.HistoryHandler,
	detailH *handler.DetailHandler,
	healthH *handler.HealthHandler,
	skillsH *handler.SkillsHandler,
	toolsH *handler.ToolsHandler,
) {
	// Global middleware
	h.Use(recoveryMW.Serve())

	// Health check (no auth required)
	h.GET("/health", healthH.Serve)

	// API group with auth and rate limit
	apiGroup := h.Group("/")
	apiGroup.Use(authMW.Serve())
	apiGroup.Use(rateLimitMW.Serve())

	// Task endpoints
	apiGroup.POST("/task/execute", executeH.Serve)
	apiGroup.DELETE("/task/:task_id", cancelH.Serve)
	apiGroup.GET("/task/status/:task_id", statusH.Serve)
	apiGroup.GET("/task/history", historyH.Serve)
	apiGroup.GET("/task/:task_id", detailH.Serve)

	// Info endpoints
	apiGroup.GET("/skills", skillsH.Serve)
	apiGroup.GET("/tools", toolsH.Serve)
}
