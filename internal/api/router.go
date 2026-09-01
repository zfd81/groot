package api

import (
	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/zfd81/groot/internal/api/handler"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/api/websession"
)

// RegisterRoutes registers all API routes
func RegisterRoutes(h *server.Hertz,
	authMW *middleware.AuthMiddleware,
	rateLimitMW *middleware.RateLimitMiddleware,
	webStore *websession.Store,
	chatH *handler.ChatHandler,
	statusH *handler.StatusHandler,
	detailH *handler.DetailHandler,
	sessionH *handler.SessionHandler,
	healthH *handler.HealthHandler,
	skillsH *handler.SkillsHandler,
	agentsH *handler.AgentsHandler,
	toolsH *handler.ToolsHandler,
	modelsH *handler.ModelsHandler,
	scheduleH *handler.ScheduleHandler,
	webAuthH *handler.WebAuthHandler,
) {
	// Web UI 免登录端点：认证入口与健康检查（groot status 也走 /web/health）
	h.GET("/web/health", healthH.Serve)
	h.POST("/web/login", webAuthH.Login)
	h.POST("/web/logout", webAuthH.Logout)
	h.GET("/web/me", webAuthH.Me)
	h.POST("/web/setup", webAuthH.Setup)

	// Web UI 静态资源托管（/ui/*）
	RegisterWebUI(h)

	// Web UI 专用端点：需要有效登录会话
	webGroup := h.Group("/web")
	webGroup.Use(middleware.WebSession(webStore), rateLimitMW.Serve())
	webGroup.POST("/password", webAuthH.ChangePassword)
	webGroup.GET("/agents", agentsH.Serve)
	webGroup.GET("/skills", skillsH.Serve)
	webGroup.GET("/tools", toolsH.Serve)
	webGroup.GET("/models", modelsH.Serve)

	// API group with auth + rate limit
	apiGroup := h.Group("/")
	apiGroup.Use(authMW.Serve(), rateLimitMW.Serve())

	// Chat endpoints - 多轮对话
	apiGroup.POST("/chat", chatH.Serve)
	apiGroup.GET("/chat/status/:sid", statusH.Serve)
	apiGroup.GET("/chat/:sid", detailH.GetLatest)  // 获取最近一次对话详情
	apiGroup.GET("/chat/:sid/:cid", detailH.Serve) // 获取指定对话详情

	// Session endpoints - 会话管理
	apiGroup.GET("/sess/:sid", sessionH.GetSession)
	apiGroup.GET("/sess/history", sessionH.ListSessions)

	// Schedule endpoints
	if scheduleH != nil {
		scheduleGroup := apiGroup.Group("/schedule")
		scheduleGroup.GET("/", scheduleH.List)
		scheduleGroup.GET("/:id", scheduleH.Get)
		scheduleGroup.DELETE("/:id", scheduleH.Delete)
		scheduleGroup.POST("/:id/disable", scheduleH.Disable)
		scheduleGroup.POST("/:id/enable", scheduleH.Enable)
		scheduleGroup.POST("/:id/archive", scheduleH.Archive)
		scheduleGroup.GET("/:id/history", scheduleH.History)
	}
}
