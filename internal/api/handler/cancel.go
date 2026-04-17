package handler

import (
	"context"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/storage"
)

// CancelHandler handles DELETE /task/{task_id}
type CancelHandler struct {
	storage       storage.TaskStorage
	cancelManager *agent.CancelManager
	executor      *agent.Executor
}

// NewCancelHandler creates a new cancel handler
func NewCancelHandler(
	store storage.TaskStorage,
	cancelMgr *agent.CancelManager,
	exec *agent.Executor,
) *CancelHandler {
	return &CancelHandler{
		storage:       store,
		cancelManager: cancelMgr,
		executor:      exec,
	}
}

// Serve handles the cancel request
func (h *CancelHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	taskID := rc.Param("task_id")

	if taskID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"task_id 参数缺失"}`))
		return
	}

	// Check if task exists
	task, err := h.storage.Get(taskID)
	if err != nil {
		rc.SetContentType("application/json")
		rc.Write([]byte(fmt.Sprintf(`{"status":"task_not_found","task_id":"%s","message":"任务不存在"}`, taskID)))
		return
	}

	// Check if task is already completed
	if task.Status != storage.StatusRunning {
		statusMsg := ""
		switch task.Status {
		case storage.StatusCompleted:
			statusMsg = "任务已完成，无法取消"
		case storage.StatusFailed:
			statusMsg = "任务已失败，无法取消"
		case storage.StatusCancelled:
			statusMsg = "任务已取消"
		}
		rc.SetContentType("application/json")
		rc.Write([]byte(fmt.Sprintf(`{"status":"%s","task_id":"%s","message":"%s"}`, task.Status, taskID, statusMsg)))
		return
	}

	// Cancel the task
	if h.cancelManager.Cancel(taskID) {
		rc.SetContentType("application/json")
		rc.Write([]byte(fmt.Sprintf(`{"status":"success","task_id":"%s","message":"任务已取消"}`, taskID)))
	} else {
		rc.SetContentType("application/json")
		rc.Write([]byte(fmt.Sprintf(`{"status":"%s","task_id":"%s","message":"任务已完成，无法取消"}`, task.Status, taskID)))
	}
}
