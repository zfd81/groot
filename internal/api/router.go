package api

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/api/handler"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/plugin"
)

// registerFallbackHandlers 为未匹配的路径与方法注册统一错误信封。
// Hertz 默认返回纯文本的 404/405，与本服务「所有由应用处理的请求都返回
// OpenAI 错误信封」的约定不符，因此在路由注册最前面覆写这两个兜底处理器。
// 它与具体路由的注册顺序无关，也不受分组中间件影响。
func registerFallbackHandlers(h *server.Hertz) {
	h.NoRoute(func(ctx context.Context, rc *app.RequestContext) {
		errbody.Write(rc, 404, "接口不存在："+string(rc.URI().Path()),
			errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
	})
	h.NoMethod(func(ctx context.Context, rc *app.RequestContext) {
		errbody.Write(rc, 405, "该接口不支持 "+string(rc.Method())+" 方法",
			errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
	})
}

// RegisterRoutes registers all API routes
func RegisterRoutes(h *server.Hertz,
	authMW *middleware.AuthMiddleware,
	rateLimitMW *middleware.RateLimitMiddleware,
	webStore *websession.Store,
	chatH *handler.ChatHandler,
	statusH *handler.StatusHandler,
	detailH *handler.DetailHandler,
	stopH *handler.StopHandler,
	sessionH *handler.SessionHandler,
	healthH *handler.HealthHandler,
	skillsH *handler.SkillsHandler,
	agentsH *handler.AgentsHandler,
	toolsH *handler.ToolsHandler,
	modelsH *handler.ModelsHandler,
	modelsPublicH *handler.ModelsPublicHandler,
	scheduleH *handler.ScheduleHandler,
	webAuthH *handler.WebAuthHandler,
	apiKeysH *handler.APIKeysHandler,
	clusterH *handler.ClusterHandler,
	logsH *handler.LogsHandler,
	filesH *handler.FilesHandler,
	syncH *handler.SyncHandler,
	transcriptionH *handler.TranscriptionHandler,
	speechH *handler.SpeechHandler,
	imageH *handler.ImageHandler,
	settingH *handler.SettingHandler,
	pluginMgr *plugin.Manager,
) {
	// 未匹配的路径与方法也返回统一的 OpenAI 错误信封，避免框架默认的纯文本响应。
	registerFallbackHandlers(h)

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
	webGroup.DELETE("/models/:name/default", modelsH.ClearDefault)
	webGroup.DELETE("/models/:name", modelsH.Delete)
	webGroup.GET("/cluster", clusterH.Serve)
	webGroup.POST("/cluster/:reg_id/restart", clusterH.Restart)
	webGroup.GET("/apikeys", apiKeysH.List)
	webGroup.POST("/apikeys", apiKeysH.Create)
	webGroup.GET("/apikeys/:id/token", apiKeysH.Token)
	webGroup.DELETE("/apikeys/:id", apiKeysH.Delete)
	webGroup.GET("/logs/:sid", logsH.Serve)
	// 聊天页调用的转录接口，与对外接口共用同一 handler
	webGroup.POST("/audio/transcriptions", transcriptionH.Serve)
	// 聊天页调用的语音合成接口，与对外接口共用同一 handler
	webGroup.POST("/audio/speech", speechH.Serve)
	// 语音配置读写
	webGroup.GET("/settings/voice", settingH.GetVoice)
	webGroup.PUT("/settings/voice", settingH.PutVoice)
	// 语音合成配置读写（朗读音色）
	webGroup.GET("/settings/speech", settingH.GetSpeech)
	webGroup.PUT("/settings/speech", settingH.PutSpeech)
	webGroup.GET("/settings/runtime", settingH.GetRuntime)
	webGroup.PUT("/settings/runtime", settingH.PutRuntime)
	// 认证配置：密钥读取与重新生成，改动需重启生效
	webGroup.GET("/settings/auth", settingH.GetAuthSettings)
	webGroup.POST("/settings/auth/secret", settingH.RegenerateAuthSecret)

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

	// Python 插件代理：为每个通过校验的插件注册精确路径 /plugins/<name>。
	// 无效插件不注册路由；不注册下级路径，代理 handler 只接受精确匹配。
	if pluginMgr != nil {
		for _, p := range pluginMgr.Plugins() {
			if p.Invalid != "" {
				continue
			}
			apiGroup.Any("/plugins/"+p.Name, pluginMgr.Proxy(p.Name))
		}
	}

	// Chat endpoints - 多轮对话
	apiGroup.POST("/chat/completions", chatH.Serve)
	apiGroup.GET("/chat/status/:sid", statusH.Serve)
	apiGroup.GET("/chat/:sid", detailH.GetLatest)      // 获取最近一次对话详情
	apiGroup.GET("/chat/:sid/:cid", detailH.Serve)     // 获取指定对话详情
	apiGroup.POST("/chat/:sid/:cid/stop", stopH.Serve) // 中止正在生成的对话

	// 对外转录接口：与 /chat 同组，API Key 鉴权并计入限流
	apiGroup.POST("/audio/transcriptions", transcriptionH.Serve)
	// 对外合成接口：与转录同组，API Key 鉴权并计入限流
	apiGroup.POST("/audio/speech", speechH.Serve)
	// 对外图片解析接口：与 /chat 同组，API Key 鉴权并计入限流
	apiGroup.POST("/image/understanding", imageH.Serve)

	// 对外模型列表接口：与上述端点同组，API Key 鉴权并计入限流
	apiGroup.GET("/models", modelsPublicH.Serve)

	// Session endpoints - 会话管理
	// 静态路由（/sess/history、/sess/search）优先级高于命名参数路由（/sess/:sid），可共存
	apiGroup.GET("/sess/:sid", sessionH.GetSession)
	apiGroup.DELETE("/sess/:sid", sessionH.DeleteSession)
	apiGroup.PATCH("/sess/:sid", sessionH.RenameSession)
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
