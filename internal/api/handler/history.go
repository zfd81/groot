package handler

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/storage"
	"github.com/zfd81/groot/pkg/utils"
)

// HistoryHandler handles GET /task/history
type HistoryHandler struct {
	storage storage.TaskStorage
}

// NewHistoryHandler creates a new history handler
func NewHistoryHandler(store storage.TaskStorage) *HistoryHandler {
	return &HistoryHandler{storage: store}
}

// Serve handles the history request
func (h *HistoryHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	query := storage.TaskQuery{
		Limit:  20,
		Offset: 0,
	}

	// Parse status filter
	statuses := rc.Query("status")
	if statuses != "" {
		query.Status = []storage.TaskStatus{storage.TaskStatus(statuses)}
	}

	// Parse time range
	startTime := rc.Query("start_time")
	if startTime != "" {
		t, err := utils.ParseTime(startTime)
		if err == nil {
			query.StartTime = &t
		}
	}

	endTime := rc.Query("end_time")
	if endTime != "" {
		t, err := utils.ParseTime(endTime)
		if err == nil {
			query.EndTime = &t
		}
	}

	// Parse pagination
	limit := rc.Query("limit")
	if limit != "" {
		l, err := strconv.Atoi(limit)
		if err == nil && l > 0 && l <= 100 {
			query.Limit = l
		}
	}

	offset := rc.Query("offset")
	if offset != "" {
		o, err := strconv.Atoi(offset)
		if err == nil && o >= 0 {
			query.Offset = o
		}
	}

	// Query tasks
	tasks, total, err := h.storage.List(&query)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"storage_error","message":"查询失败"}`))
		return
	}

	// Build response
	summaries := make([]api.TaskSummary, len(tasks))
	for i, task := range tasks {
		summaries[i] = api.TaskSummary{
			ID:          task.ID,
			Instruction: task.Instruction,
			Status:      string(task.Status),
			StartTime:   task.StartTime,
			EndTime:     task.EndTime,
			Duration:    task.Duration,
			Caller:      task.Caller,
		}
	}

	resp := api.HistoryResponse{
		Status: "success",
		Total:  total,
		Limit:  query.Limit,
		Offset: query.Offset,
		Tasks:  summaries,
	}

	rc.JSON(200, resp)
}
