package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

const (
	// sessionLogDays 会话日志扫描的天数范围
	sessionLogDays = 7
	// sessionLogLimit 单次返回的日志条数上限
	sessionLogLimit = 1000
)

// LogsHandler 会话日志查询处理器
type LogsHandler struct {
	fileCfg config.LogFileConfig
}

// NewLogsHandler 创建会话日志查询处理器
func NewLogsHandler(cfg config.LoggingConfig) *LogsHandler {
	return &LogsHandler{fileCfg: cfg.File}
}

// Serve 处理 GET /web/logs/:sid
func (h *LogsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")
	if sessionID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"session_id 参数缺失"}`))
		return
	}

	logs, truncated := logger.ReadSessionLogs(h.fileCfg, sessionID, sessionLogDays, sessionLogLimit)
	if logs == nil {
		logs = []logger.LogEntry{} // 会话无日志时返回空数组而非 null
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"count":      len(logs),
		"truncated":  truncated,
		"logs":       logs,
	})
}
