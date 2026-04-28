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
	chatH *handler.ChatHandler,
	cancelH *handler.CancelHandler,
	statusH *handler.StatusHandler,
	detailH *handler.DetailHandler,
	sessionH *handler.SessionHandler,
	healthH *handler.HealthHandler,
	skillsH *handler.SkillsHandler,
	toolsH *handler.ToolsHandler,
) {
	// Health check (no auth required)
	h.GET("/health", healthH.Serve)

	// API group with auth + rate limit
	apiGroup := h.Group("/")
	apiGroup.Use(authMW.Serve(), rateLimitMW.Serve())

	// Chat endpoints - 多轮对话
	apiGroup.POST("/chat", chatH.Serve)
	apiGroup.DELETE("/chat/:sid", cancelH.Serve)
	apiGroup.GET("/chat/status/:sid", statusH.Serve)
	apiGroup.GET("/chat/:sid", detailH.GetLatest)       // 获取最近一次对话详情
	apiGroup.GET("/chat/:sid/:cid", detailH.Serve)      // 获取指定对话详情

	// Session endpoints - 会话管理
	apiGroup.GET("/sess/:sid", sessionH.GetSession)
	apiGroup.GET("/sess/history", sessionH.ListSessions)

	// Info endpoints
	apiGroup.GET("/skills", skillsH.Serve)
	apiGroup.GET("/tools", toolsH.Serve)
}