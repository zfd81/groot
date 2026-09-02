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
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/ratelimit"
	"github.com/zfd81/groot/internal/repo"
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
	users repo.UserRepo,
	models *llm.ModelService,
	apiKeys repo.APIKeyRepo,
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
	chatH := handler.NewChatHandler(mem, runtime, exec, mcpMgr, subAgentReg, attHandler, models, cfg, log)
	statusH := handler.NewStatusHandler(runtime, mem)
	detailH := handler.NewDetailHandler(mem)
	sessionH := handler.NewSessionHandler(mem)
	healthH := handler.NewHealthHandler(cfg, homeDir, skillBackend, mcpMgr, mem, runtime, models, log)
	skillsH := handler.NewSkillsHandler(skillBackend, subAgentReg, log)
	agentsH := handler.NewAgentsHandler(subAgentReg, skillBackend, log)
	toolsH := handler.NewToolsHandler(mcpMgr, subAgentReg, log)
	modelsH := handler.NewModelsHandler(models, log)
	scheduleH := handler.NewScheduleHandler(scheduleMgr, log)
	webAuthH := handler.NewWebAuthHandler(users, webStore, log)
	apiKeysH := handler.NewAPIKeysHandler(apiKeys, cfg.Security, log)

	// Register routes
	RegisterRoutes(h, authMW, rateLimitMW, webStore,
		chatH, statusH, detailH, sessionH,
		healthH, skillsH, agentsH, toolsH, modelsH, scheduleH, webAuthH, apiKeysH)

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
