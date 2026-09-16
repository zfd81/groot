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
	apiKeysH *handler.APIKeysHandler,
	clusterH *handler.ClusterHandler,
	logsH *handler.LogsHandler,
	filesH *handler.FilesHandler,
	syncH *handler.SyncHandler,
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
	webGroup.GET("/agents/:name/definition", agentsH.Definition)
	webGroup.GET("/skills", skillsH.Serve)
	webGroup.GET("/tools", toolsH.Serve)
	webGroup.GET("/models", modelsH.List)
	webGroup.POST("/models", modelsH.Create)
	webGroup.POST("/models/test", modelsH.Test)
	webGroup.PUT("/models/:name", modelsH.Update)
	webGroup.PUT("/models/:name/default", modelsH.SetDefault)
	webGroup.DELETE("/models/:name", modelsH.Delete)
	webGroup.GET("/cluster", clusterH.Serve)
	webGroup.GET("/apikeys", apiKeysH.List)
	webGroup.POST("/apikeys", apiKeysH.Create)
	webGroup.GET("/apikeys/:id/token", apiKeysH.Token)
	webGroup.DELETE("/apikeys/:id", apiKeysH.Delete)
	webGroup.GET("/logs/:sid", logsH.Serve)

	// 文件面板端点（filesH 为 nil 表示初始化失败，跳过注册）
	if filesH != nil {
		filesGroup := webGroup.Group("/files")
		filesGroup.GET("/list", filesH.List)
		filesGroup.GET("/content", filesH.Content)
		filesGroup.PUT("/content", filesH.Save)
		filesGroup.POST("/rename", filesH.Rename)
		filesGroup.POST("/upload", filesH.Upload)
		filesGroup.POST("/upload/prepare", filesH.UploadPrepare)
		filesGroup.GET("/download", filesH.Download)
		filesGroup.POST("/scaffold", filesH.Scaffold)
		webGroup.DELETE("/files", filesH.Delete)
	}

	// 配置同步端点：SQLite 模式下 handler 统一返回 409 sync_disabled，
	// 由前端据此隐藏入口，因此这里无条件注册。
	syncGroup := webGroup.Group("/sync")
	syncGroup.POST("/diff", syncH.Diff)
	syncGroup.POST("/push", syncH.Push)
	syncGroup.POST("/pull", syncH.Pull)

	// API group with auth + rate limit
	apiGroup := h.Group("/")
	apiGroup.Use(authMW.Serve(), rateLimitMW.Serve())

	// Chat endpoints - 多轮对话
	apiGroup.POST("/chat", chatH.Serve)
	apiGroup.GET("/chat/status/:sid", statusH.Serve)
	apiGroup.GET("/chat/:sid", detailH.GetLatest)  // 获取最近一次对话详情
	apiGroup.GET("/chat/:sid/:cid", detailH.Serve) // 获取指定对话详情

	// Session endpoints - 会话管理
	// 静态路由（/sess/history、/sess/search）优先级高于命名参数路由（/sess/:sid），可共存
	apiGroup.GET("/sess/:sid", sessionH.GetSession)
	apiGroup.GET("/sess/history", sessionH.ListSessions)
	apiGroup.GET("/sess/search", sessionH.SearchSessions)

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
