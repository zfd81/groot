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
// NOTE: storage parameter removed - will be re-added in Phase 4 (memory module)
func NewServer(
	cfg config.Config,
	homeDir string,
	log *logger.Logger,
	// store storage.TaskStorage, // removed
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	cancelMgr *agent.CancelManager,
) *Server {
	// Create Hertz server
	h := server.Default(
		server.WithHostPorts(fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
	)

	// Create attachment handler (reserved for future use)
	attHandler := attachment.NewHandler(cfg.Attachment, homeDir)
	_ = attHandler // reserved for executor when memory module implemented

	// Create executor - temporarily disabled until memory module implemented
	// exec := agent.NewExecutor(store, skills, mcpMgr, cancelMgr, attHandler, cfg, log)

	// Create middleware
	authMW := middleware.NewAuthMiddleware(cfg.Security)
	rateLimitMW := middleware.NewRateLimitMiddleware(cfg.Performance.RateLimit)
	// rateLimitMW.SetExecutor(exec) // disabled
	recoveryMW := middleware.NewRecoveryMiddleware(log)

	// Create handlers - temporarily disabled until memory module implemented
	// executeH := handler.NewExecuteHandler(store, exec, cancelMgr)
	// cancelH := handler.NewCancelHandler(store, cancelMgr, exec)
	// statusH := handler.NewStatusHandler(store)
	// historyH := handler.NewHistoryHandler(store)
	// detailH := handler.NewDetailHandler(store)
	healthH := handler.NewHealthHandler(cfg, skills, mcpMgr, nil) // executor nil for now
	skillsH := handler.NewSkillsHandler(skills)
	toolsH := handler.NewToolsHandler(mcpMgr)

	// Register routes with placeholder handlers
	RegisterRoutes(h, authMW, rateLimitMW, recoveryMW,
		nil, nil, nil, nil, nil, // execute, cancel, status, history, detail disabled
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