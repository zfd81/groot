package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/storage"
)

// StatusHandler handles GET /task/status/{task_id}
type StatusHandler struct {
	storage storage.TaskStorage
}

// NewStatusHandler creates a new status handler
func NewStatusHandler(store storage.TaskStorage) *StatusHandler {
	return &StatusHandler{storage: store}
}

// Serve handles the status request
func (h *StatusHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	taskID := rc.Param("task_id")

	if taskID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"task_id 参数缺失"}`))
		return
	}

	task, err := h.storage.Get(taskID)
	if err != nil {
		rc.SetContentType("application/json")
		rc.Write([]byte(fmt.Sprintf(`{"status":"task_not_found","task_id":"%s","message":"任务不存在"}`, taskID)))
		return
	}

	// Calculate elapsed time
	elapsed := time.Since(task.StartTime)
	elapsedStr := formatElapsed(elapsed)

	resp := api.StatusResponse{
		Status:      "success",
		TaskID:      taskID,
		TaskStatus:  string(task.Status),
		StartedAt:   task.StartTime.Format(time.RFC3339),
		ElapsedTime: elapsedStr,
	}

	if task.Status == storage.StatusRunning && task.Progress != nil {
		resp.Progress = &api.ProgressInfo{
			CurrentStep:    task.Progress.CurrentStep,
			StepsCompleted: task.Progress.StepsCompleted,
			Percentage:     task.Progress.Percentage,
		}
	}

	rc.JSON(200, resp)
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
