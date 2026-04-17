package handler

import (
	"context"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/storage"
)

// DetailHandler handles GET /task/{task_id}
type DetailHandler struct {
	storage storage.TaskStorage
}

// NewDetailHandler creates a new detail handler
func NewDetailHandler(store storage.TaskStorage) *DetailHandler {
	return &DetailHandler{storage: store}
}

// Serve handles the detail request
func (h *DetailHandler) Serve(ctx context.Context, rc *app.RequestContext) {
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

	// Build task detail
	detail := api.TaskDetail{
		ID:          task.ID,
		Instruction: task.Instruction,
		Prompt:      task.Prompt,
		Status:      string(task.Status),
		StartTime:   task.StartTime,
		EndTime:     task.EndTime,
		Duration:    task.Duration,
		Caller:      task.Caller,
		Result:      task.Result,
	}

	if task.Error != nil {
		detail.Error = &api.ErrorInfo{
			Code:    task.Error.Code,
			Message: task.Error.Message,
		}
	}

	// Convert steps
	if task.Steps != nil {
		steps := make([]api.StepDetail, len(task.Steps))
		for i, s := range task.Steps {
			steps[i] = api.StepDetail{
				StepID:       s.StepID,
				Type:         s.Type,
				Name:         s.Name,
				StartTime:    s.StartTime,
				EndTime:      s.EndTime,
				Status:       string(s.Status),
				NestingLevel: s.NestingLevel,
			}
			if s.Error != nil {
				steps[i].Error = &api.ErrorInfo{
					Code:    s.Error.Code,
					Message: s.Error.Message,
				}
			}
		}
		detail.Steps = steps
	}

	resp := api.DetailResponse{
		Status: "success",
		Task:   &detail,
	}

	rc.JSON(200, resp)
}
