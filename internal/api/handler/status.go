package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	// "github.com/zfd81/groot/internal/api/types" // removed - not used
	// "github.com/zfd81/groot/internal/storage" // removed - will be re-added in Phase 4
)

// StatusHandler handles GET /task/status/{task_id}
// NOTE: temporarily disabled until memory module implemented
type StatusHandler struct {
	// storage storage.TaskStorage // removed
}

// NewStatusHandler creates a new status handler
// NOTE: temporarily disabled - will be re-enabled in Phase 4
func NewStatusHandler(
	// store storage.TaskStorage, // removed
) *StatusHandler {
	return &StatusHandler{
		// storage: store,
	}
}

// Serve handles the status request
// NOTE: temporarily returns error until memory module implemented
func (h *StatusHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	taskID := rc.Param("task_id")

	if taskID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"task_id 参数缺失"}`))
		return
	}

	// Storage query disabled
	// task, err := h.storage.Get(taskID)
	// if err != nil {
	// 	rc.SetContentType("application/json")
	// 	rc.Write([]byte(fmt.Sprintf(`{"status":"task_not_found","task_id":"%s","message":"任务不存在"}`, taskID)))
	// 	return
	// }

	// Temporary placeholder response
	rc.SetContentType("application/json")
	rc.SetStatusCode(503)
	rc.Write([]byte(fmt.Sprintf(`{"status":"service_unavailable","task_id":"%s","message":"任务查询功能暂时不可用，正在升级存储模块"}`, taskID)))
}

// formatElapsed formats elapsed time
func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) - minutes*60
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}