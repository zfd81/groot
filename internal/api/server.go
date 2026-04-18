package api

import (
	"context"
	"fmt"

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
	"github.com/zfd81/groot/internal/skill"
)

// Server represents the API server
type Server struct {
	hertz  *server.Hertz
	config config.Config
	logger *logger.Logger
}

// NewServer creates a new API server
func NewServer(
	cfg config.Config,
	homeDir string,
	log *logger.Logger,
	mem *memory.Manager,
	runtime *agent.RuntimeState,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	cancelMgr *agent.CancelManager,
) *Server {
	// Create Hertz server
	h := server.Default(
		server.WithHostPorts(fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
	)

	// Create attachment handler
	attHandler := attachment.NewHandler(cfg.Attachment, homeDir)

	// Create executor
	exec := agent.NewExecutor(skills, mcpMgr, cancelMgr, attHandler, cfg, log)

	// Create middleware
	authMW := middleware.NewAuthMiddleware(cfg.Security)
	rateLimitMW := middleware.NewRateLimitMiddleware(cfg.Performance.RateLimit)
	// rateLimitMW.SetExecutor(exec) // disabled for now
	recoveryMW := middleware.NewRecoveryMiddleware(log)

	// Create handlers
	chatH := handler.NewChatHandler(mem, runtime, exec, skills, mcpMgr, attHandler, cfg, log)
	cancelH := handler.NewCancelHandler(runtime, mem)
	statusH := handler.NewStatusHandler(runtime, mem)
	detailH := handler.NewDetailHandler(mem)
	sessionH := handler.NewSessionHandler(mem)
	healthH := handler.NewHealthHandler(cfg, skills, mcpMgr, exec)
	skillsH := handler.NewSkillsHandler(skills)
	toolsH := handler.NewToolsHandler(mcpMgr)

	// Register routes
	RegisterRoutes(h, authMW, rateLimitMW, recoveryMW,
		chatH, cancelH, statusH, detailH, sessionH,
		healthH, skillsH, toolsH)

	return &Server{
		hertz:  h,
		config: cfg,
		logger: log,
	}
}

// Start starts the server
func (s *Server) Start() error {
	s.logger.Info("Starting API server",
		zap.String("host", s.config.Server.Host),
		zap.Int("port", s.config.Server.Port),
	)
	return s.hertz.Run()
}

// Stop stops the server gracefully
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping API server")
	return s.hertz.Shutdown(ctx)
}