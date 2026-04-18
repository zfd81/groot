package handler

import (
	"context"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
	// "github.com/zfd81/groot/internal/storage" // removed - will be re-added in Phase 4
)

// CancelHandler handles DELETE /task/{task_id}
// NOTE: temporarily disabled until memory module implemented
type CancelHandler struct {
	// storage       storage.TaskStorage // removed
	cancelManager *agent.CancelManager
	executor      *agent.Executor
}

// NewCancelHandler creates a new cancel handler
// NOTE: temporarily disabled - will be re-enabled in Phase 4
func NewCancelHandler(
	// store storage.TaskStorage, // removed
	cancelMgr *agent.CancelManager,
	exec *agent.Executor,
) *CancelHandler {
	return &CancelHandler{
		// storage:       store,
		cancelManager: cancelMgr,
		executor:      exec,
	}
}

// Serve handles the cancel request
// NOTE: temporarily returns error until memory module implemented
func (h *CancelHandler) Serve(ctx context.Context, rc *app.RequestContext) {
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
	rc.Write([]byte(fmt.Sprintf(`{"status":"service_unavailable","task_id":"%s","message":"任务取消功能暂时不可用，正在升级存储模块"}`, taskID)))
}