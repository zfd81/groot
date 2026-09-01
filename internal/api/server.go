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
	"github.com/zfd81/groot/internal/attachment"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/ratelimit"
	"github.com/zfd81/groot/internal/schedule"
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
) *Server {
	// Set a large max request body size to allow attachment handler to validate sizes
	// Hertz returns 413 when body exceeds this limit, but we want attachment handler
	// to return 400 with proper error code instead
	// Use 200MB as max to handle large attachments with Base64 encoding
	maxBodySize := 200 * 1024 * 1024 // 200MB

	// Create Hertz server with custom body size limit
	h := server.Default(
		server.WithHostPorts(fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
		server.WithMaxRequestBodySize(maxBodySize),
	)

	// Create attachment handler (temp directory is fixed at {attachmentTempBase}/temp)
	attHandler := attachment.NewHandler(cfg.Attachment)

	// Web 登录会话存储：仅当 Web 认证启用时创建，作为 Cookie 凭证来源。
	// 未启用时为 nil，auth 中间件退化为纯 API Key 判定。
	var webStore *websession.Store
	if cfg.Security.Web.Enabled {
		ttl, err := time.ParseDuration(cfg.Security.Web.SessionTTL)
		if err != nil || ttl <= 0 {
			log.Info("Web 会话有效期配置无效，回退为 24h",
				zap.String("session_ttl", cfg.Security.Web.SessionTTL))
			ttl = 24 * time.Hour
		}
		webStore = websession.NewStore(ttl)
	}

	// API 认证开启但 Web 认证关闭时，浏览器请求无凭证会被拦截，Web 界面不可用。
	if cfg.Security.Auth.Enabled && !cfg.Security.Web.Enabled {
		log.Info("API 认证已开启但 Web 登录认证未开启，浏览器将无法访问 Web 界面；" +
			"如需使用 Web 界面，请在 config.yaml 中设置 security.web.enabled: true")
	}

	// Web 登录开启但 API 认证关闭时：Web 界面弹登录页，但直接访问 REST API 仍匿名放行，
	// 登录仅保护 /ui 入口而非后端，容易造成"服务已受保护"的误判。
	if cfg.Security.Web.Enabled && !cfg.Security.Auth.Enabled {
		log.Warn("Web 登录认证已开启但 API 认证未开启：绕过 Web 界面直接调用 REST API 仍可匿名访问；" +
			"如需保护后端接口，请同时设置 security.auth.enabled: true")
	}

	// Create middleware
	authMW := middleware.NewAuthMiddleware(cfg.Security, webStore)

	// Create rate limiter (best-effort, errors use default config)
	rateLimiter, err := ratelimit.New(cfg.Security.RateLimit)
	if err != nil {
		log.Info("速率限制器初始化失败，已禁用限流", zap.Error(err))
		cfg.Security.RateLimit.Enabled = false
		rateLimiter, _ = ratelimit.New(cfg.Security.RateLimit)
	}
	rateLimitMW := middleware.NewRateLimitMiddleware(rateLimiter)

	// Create handlers
	chatH := handler.NewChatHandler(mem, runtime, exec, mcpMgr, subAgentReg, attHandler, cfg, log)
	statusH := handler.NewStatusHandler(runtime, mem)
	detailH := handler.NewDetailHandler(mem)
	sessionH := handler.NewSessionHandler(mem)
	healthH := handler.NewHealthHandler(cfg, homeDir, skillBackend, mcpMgr, mem, runtime, log)
	skillsH := handler.NewSkillsHandler(skillBackend, subAgentReg, log)
	agentsH := handler.NewAgentsHandler(subAgentReg, skillBackend, log)
	toolsH := handler.NewToolsHandler(mcpMgr, subAgentReg, log)
	modelsH := handler.NewModelsHandler(&cfg)
	scheduleH := handler.NewScheduleHandler(scheduleMgr, log)
	webAuthH := handler.NewWebAuthHandler(cfg.Security.Web, webStore, log)

	// Register routes
	RegisterRoutes(h, authMW, rateLimitMW,
		chatH, statusH, detailH, sessionH,
		healthH, skillsH, agentsH, toolsH, modelsH, scheduleH, webAuthH)

	return &Server{
		hertz:  h,
		config: cfg,
		logger: log,
	}
}

// Start starts the server with graceful error handling
func (s *Server) Start() error {
	s.logger.Info("Starting API server",
		zap.String("host", s.config.Server.Host),
		zap.Int("port", s.config.Server.Port),
	)

	// Recover from panic (e.g., port already in use)
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Server startup failed", zap.Any("error", r))
		}
	}()

	return s.hertz.Run()
}

// Stop stops the server gracefully
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping API server")
	return s.hertz.Shutdown(ctx)
}
