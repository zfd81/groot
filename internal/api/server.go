package api

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app/server"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/handler"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/lifecycle"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/plugin"
	"github.com/zfd81/groot/internal/ratelimit"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/schedule"
	"github.com/zfd81/groot/internal/setting"
)

// Server represents the API server
type Server struct {
	hertz  *server.Hertz
	config config.Config
	logger *logger.Logger
}

// NewServer creates a new API server.
func NewServer(
	cfg config.Config,
	homeDir string,
	log *logger.Logger,
	mem *memory.Manager,
	runtime *agent.RuntimeState,
	skillBackend einoskill.Backend,
	skillMiddleware adk.ChatModelAgentMiddleware,
	mcpMgr *mcp.Manager,
	exec *agent.Executor,
	subAgentReg *agent.SubAgentRegistry,
	scheduleMgr **schedule.Manager,
	users repo.UserRepo,
	models *llm.ModelService,
	apiKeys repo.APIKeyRepo,
	members repo.MemberRepo,
	syncResources repo.ResourceRepo, // 配置同步的远端仓储；SQLite 单机模式下为 nil（同步禁用）
	settings *setting.Settings, // 配置对象：YAML 与配置表的统一读取入口
	clusterInst *cluster.Cluster, // 集群实例：提供本机 reg_id 与消息发送能力
	role lifecycle.Role, // 进程角色：决定健康检查的 process_mode 与是否支持重启
	pluginMgr *plugin.Manager, // Python 插件管理器；为 nil 时不代理插件、健康检查不含 plugins 项
) *Server {
	// 放宽框架自身的请求体上限，200MB 硬上限由下面的 BodyLimit 中间件统一执行并以
	// file_too_large 错误信封拒绝（框架默认行为是纯文本 413）。取 200MB 以容纳
	// Base64 编码的大附件。已知长度在读取前拒绝；流式请求体在读取中拒绝，代理路由
	// 映射为 413 信封，非代理路由可能在 handler 读取时退化为 400。
	maxBodySize := 200 * 1024 * 1024 // 200MB

	// Create Hertz server with custom body size limit.
	// 不开启 HandleMethodNotAllowed：方法不匹配统一走 NoRoute 返回 404 信封。
	// 405 分支的处理链只含全局中间件，不含 apiGroup 上的鉴权中间件，会让未携带
	// 凭证的请求直接拿到 405，等同于无鉴权的路由探测。
	h := server.Default(
		server.WithHostPorts(fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
		server.WithMaxRequestBodySize(maxBodySize),
		// 请求体以流方式读取，代理转发大文件时不整体读入内存。
		// 关闭 multipart 预解析，保证转发到 Python 插件的请求体原样透传；
		// 需要表单的 handler（FormFile/PostForm）在访问时按需解析。
		server.WithStreamBody(true),
		server.WithDisablePreParseMultipartForm(true),
		// 客户端断开时取消请求 context，代理据此取消上游请求。
		server.WithSenseClientDisconnection(true),
	)

	// 请求体硬上限：开启流式读取后 Hertz 不再因 ErrBodyTooLarge 拒绝请求，
	// 由中间件在读取前（已知长度）或读取中（未知长度）强制 200MB 上限。
	h.Use(middleware.BodyLimit(int64(maxBodySize)))

	// Web 登录会话存储：认证始终启用，会话固定 1 小时且活跃时滑动续期。
	webStore := websession.NewStore(time.Hour)

	// Create middleware
	authMW := middleware.NewAuthMiddleware(cfg.Security, webStore, apiKeys, log)

	// Create rate limiter (best-effort, errors use default config)
	rateLimiter, err := ratelimit.New(cfg.Security.RateLimit)
	if err != nil {
		log.Info("速率限制器初始化失败，已禁用限流", zap.Error(err))
		cfg.Security.RateLimit.Enabled = false
		rateLimiter, _ = ratelimit.New(cfg.Security.RateLimit)
	}
	rateLimitMW := middleware.NewRateLimitMiddleware(rateLimiter)

	// Create handlers
	chatH := handler.NewChatHandler(mem, runtime, exec, mcpMgr, subAgentReg, models, settings, log)
	statusH := handler.NewStatusHandler(runtime, mem)
	detailH := handler.NewDetailHandler(mem)
	// 集群消息服务未挂接时 sender 必须是 nil 接口，而不是包着 nil 指针的非空接口
	var cancelSender agent.MessageSender
	if ms := clusterInst.MessageService(); ms != nil {
		cancelSender = ms
	}
	canceller := agent.NewClusterCanceller(runtime, cancelSender, othersAliveProbe(members, clusterInst.RegID, memberAliveTimeout))
	stopH := handler.NewStopHandler(canceller, mem, log)
	sessionH := handler.NewSessionHandler(mem, runtime, canceller)
	healthH := handler.NewHealthHandler(cfg, homeDir, skillBackend, mcpMgr, mem, runtime, models, log, role.ProcessMode())

	// Python 插件集成：权限解析器供鉴权中间件使用，状态来源供健康检查使用。
	// manager 为 nil 时保持默认权限、健康检查不含 plugins 项。
	if pluginMgr != nil {
		authMW.SetPluginPermissionResolver(pluginMgr)
		healthH.SetPluginStatusProvider(pluginMgr)
	}
	skillsH := handler.NewSkillsHandler(skillBackend, subAgentReg, log)
	agentsH := handler.NewAgentsHandler(subAgentReg, skillBackend, homeDir, log)
	toolsH := handler.NewToolsHandler(mcpMgr, subAgentReg, log)
	modelsH := handler.NewModelsHandler(models, settings, log)
	modelsPublicH := handler.NewModelsPublicHandler(models, log)
	scheduleH := handler.NewScheduleHandler(scheduleMgr, settings, log)
	webAuthH := handler.NewWebAuthHandler(users, webStore, log)
	apiKeysH := handler.NewAPIKeysHandler(apiKeys, cfg.Security, log)
	// 集群消息服务未挂接时 sender 必须是 nil 接口，而不是包着 nil 指针的非空接口
	var restartSender handler.RestartSender
	if ms := clusterInst.MessageService(); ms != nil {
		restartSender = ms
	}
	clusterH := handler.NewClusterHandler(members, clusterInst.RegID, restartSender, role.RestartSupported(), log)
	logsH := handler.NewLogsHandler(cfg.Logging)

	// 文件面板：home 目录解析失败时禁用该功能（不影响其他路由）
	filesH, err := handler.NewFilesHandler(homeDir)
	if err != nil {
		log.Info("文件面板初始化失败，已禁用", zap.Error(err))
		filesH = nil
	}

	// 配置同步：syncResources 为 nil 时端点统一返回 409 sync_disabled
	syncH := handler.NewSyncHandler(homeDir, syncResources)

	transcriptionH := handler.NewTranscriptionHandler(settings, models, log)
	speechH := handler.NewSpeechHandler(models, log)
	imageH := handler.NewImageHandler(settings, models, log)
	settingH := handler.NewSettingHandler(handler.SettingHandlerDeps{
		Settings: settings,
		Models:   models,
		Registry: subAgentReg,
		Limiter:  rateLimiter,
		Log:      log,
	})

	// Register routes
	RegisterRoutes(h, authMW, rateLimitMW, webStore,
		chatH, statusH, detailH, stopH, sessionH,
		healthH, skillsH, agentsH, toolsH, modelsH, modelsPublicH, scheduleH, webAuthH, apiKeysH, clusterH, logsH, filesH, syncH, transcriptionH, speechH, imageH, settingH, pluginMgr)

	return &Server{
		hertz:  h,
		config: cfg,
		logger: log,
	}
}

// Start starts the server with graceful error handling
func (s *Server) Start() (err error) {
	s.logger.Info("Starting API server",
		zap.String("host", s.config.Server.Host),
		zap.Int("port", s.config.Server.Port),
	)

	// Recover from panic (e.g., port already in use) and surface it as an error:
	// 静默吞掉会让工作进程以退出码 0 结束，监督进程随之停止看护。
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Server startup failed", zap.Any("error", r))
			err = fmt.Errorf("server panic: %v", r)
		}
	}()

	return s.hertz.Run()
}

// Stop stops the server gracefully
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping API server")
	return s.hertz.Shutdown(ctx)
}
