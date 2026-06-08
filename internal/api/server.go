package api

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app/server"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/handler"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/attachment"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/mcp"
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
//
// attachmentTempBase 是 attachment handler 的本地暂存基础目录(必须为绝对路径,
// 与 storage 后端无关)。attachment handler 会在 {attachmentTempBase}/temp/{taskID}/
// 下落地 base64 上传的临时文件,这是纯本地文件系统操作,minio 模式下也使用本地
// 暂存(因此不能传 minio 模式下用作 object-key 前缀的相对路径)。
func NewServer(
	cfg config.Config,
	homeDir string,
	attachmentTempBase string,
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
	attHandler := attachment.NewHandler(cfg.Attachment, attachmentTempBase)

	// Create middleware
	authMW := middleware.NewAuthMiddleware(cfg.Security)

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
	healthH := handler.NewHealthHandler(cfg, skillBackend, mcpMgr, mem, runtime, log)
	skillsH := handler.NewSkillsHandler(skillBackend, subAgentReg, log)
	agentsH := handler.NewAgentsHandler(subAgentReg, skillBackend, log)
	toolsH := handler.NewToolsHandler(mcpMgr, subAgentReg, log)
	modelsH := handler.NewModelsHandler(&cfg)
	scheduleH := handler.NewScheduleHandler(scheduleMgr, log)

	// Register routes
	RegisterRoutes(h, authMW, rateLimitMW,
		chatH, statusH, detailH, sessionH,
		healthH, skillsH, agentsH, toolsH, modelsH, scheduleH)

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