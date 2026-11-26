package handler

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/pysvc"
)

// ServiceStatusProvider 提供 Python 服务的整体状态快照（由 pysvc.Manager 实现）。
type ServiceStatusProvider interface {
	Status() pysvc.Status
}

// HealthHandler handles GET /health
type HealthHandler struct {
	config        config.Config
	homeDir       string
	skillBackend  skill.Backend
	mcpManager    *mcp.Manager
	memoryManager *memory.Manager
	runtimeState  *agent.RuntimeState
	models        *llm.ModelService
	processMode   string
	startTime     time.Time
	logger        *logger.Logger
	serviceStatus ServiceStatusProvider
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(
	cfg config.Config,
	homeDir string,
	skillBackend skill.Backend,
	mcpMgr *mcp.Manager,
	memMgr *memory.Manager,
	runtime *agent.RuntimeState,
	models *llm.ModelService,
	log *logger.Logger,
	processMode string,
) *HealthHandler {
	return &HealthHandler{
		config:        cfg,
		homeDir:       homeDir,
		skillBackend:  skillBackend,
		mcpManager:    mcpMgr,
		memoryManager: memMgr,
		runtimeState:  runtime,
		models:        models,
		processMode:   processMode,
		startTime:     time.Now(),
		logger:        log,
	}
}

// SetServiceStatusProvider 注入 Python 服务状态来源；传 nil 时健康检查不包含 services 项。
func (h *HealthHandler) SetServiceStatusProvider(p ServiceStatusProvider) {
	h.serviceStatus = p
}

// servicesCheck 返回 Python 服务的健康检查项；未设置 provider 时 ok 为 false。
// 状态取 Status.HealthStatus()，Info 为 pysvc.Status 值本身。
func (h *HealthHandler) servicesCheck() (types.CheckInfo, bool) {
	if h.serviceStatus == nil {
		return types.CheckInfo{}, false
	}
	status := h.serviceStatus.Status()
	return types.CheckInfo{Status: status.HealthStatus(), Info: status}, true
}

// addServicesCheck 在 checks 中写入 services 项；未设置 provider 时不做任何事。
func (h *HealthHandler) addServicesCheck(checks map[string]types.CheckInfo) {
	if check, ok := h.servicesCheck(); ok {
		checks["services"] = check
	}
}

// databaseType 返回数据库类型标识（sqlite/mysql/postgres）。
// Database 配置缺省时按 db.Open 的默认行为视为 sqlite。
func databaseType(cfg *config.DatabaseConfig) string {
	if cfg == nil || cfg.Driver == "" {
		return "sqlite"
	}
	switch db.DialectFrom(cfg.Driver) {
	case db.DialectMySQL:
		return "mysql"
	case db.DialectPostgres:
		return "postgres"
	default:
		return "sqlite"
	}
}

// environmentInfo 返回运行环境信息（供设置界面与 groot status 展示）：
// 工作目录、数据库类型、日志目录、进程 PID、进程模式。
// 日志目录在 main.go 中已解析为绝对路径后才构建 handler。
// pid 是本进程 PID；受监督模式下即工作进程 PID，与集群成员表中登记的一致。
func (h *HealthHandler) environmentInfo() map[string]string {
	return map[string]string{
		"home_dir":     h.homeDir,
		"database":     databaseType(h.config.Database),
		"log_dir":      h.config.Logging.File.Directory,
		"pid":          strconv.Itoa(os.Getpid()),
		"process_mode": h.processMode,
	}
}

// Serve handles the health request
func (h *HealthHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	h.logger.Info("API request: /health")

	uptime := time.Since(h.startTime)
	uptimeStr := formatUptime(uptime)

	// Get memory stats
	var sessionCount int
	if h.memoryManager != nil {
		sessions, total, _ := h.memoryManager.ListSessions(nil, 1, 0)
		sessionCount = total
		_ = sessions
	}

	// Get skills count
	skillsCount := 0
	if h.skillBackend != nil {
		matters, err := h.skillBackend.List(context.Background())
		if err == nil {
			skillsCount = len(matters)
		}
	}

	// Build MCP info with tool count
	mcpInfos := make([]map[string]interface{}, 0)
	for _, info := range h.mcpManager.ListWithToolCount() {
		mcpInfo := map[string]interface{}{
			"name":        info.Name,
			"type":        info.Type,
			"description": info.Description,
			"isActive":    info.IsActive,
			"tools_count": info.ToolCount,
		}
		if info.Error != "" {
			mcpInfo["error"] = info.Error
			h.logger.Error("MCP has discovery error",
				zap.String("name", info.Name),
				zap.String("error", info.Error))
		}
		mcpInfos = append(mcpInfos, mcpInfo)
	}

	// Check LLM connection（无默认模型时报告 unconfigured 而非失败）
	llmStatus, llmError, llmModelName := "unconfigured", "尚未配置模型", ""
	if m, err := h.models.GetByName(ctx, ""); err == nil {
		llmModelName = m.Name
		llmStatus, llmError = llm.CheckConnection(m)
	}
	llmInfo := map[string]string{"model": llmModelName}
	if llmError != "" {
		llmInfo["error"] = llmError
	}

	resp := types.HealthResponse{
		Status:  "healthy",
		Version: h.config.Agent.Version,
		Uptime:  uptimeStr,
		Checks: map[string]types.CheckInfo{
			"llm": {
				Status: llmStatus,
				Info:   llmInfo,
			},
			"mcp_servers": {
				Status: "healthy",
				Info:   mcpInfos,
			},
			"skills": {
				Status: "healthy",
				Info:   map[string]int{"count": skillsCount},
			},
			"memory": {
				Status: "healthy",
				Info:   map[string]int{"sessions": sessionCount},
			},
			"environment": {
				Status: "healthy",
				Info:   h.environmentInfo(),
			},
		},
		Metrics: map[string]interface{}{
			"chats_running": h.runtimeState.RunningCount(),
		},
	}

	// Python 子服务状态：仅在设置了状态来源时加入 services 项。
	h.addServicesCheck(resp.Checks)

	rc.JSON(200, resp)
}

// formatUptime formats uptime duration
func formatUptime(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) - hours*60

	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%dm", hours, minutes)
}
