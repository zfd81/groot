package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/schedule"
)

// ScheduleHandler handles schedule task REST endpoints
type ScheduleHandler struct {
	mgr *schedule.Manager
	log *logger.Logger
}

// NewScheduleHandler creates a new schedule handler
func NewScheduleHandler(mgr *schedule.Manager, log *logger.Logger) *ScheduleHandler {
	return &ScheduleHandler{mgr: mgr, log: log}
}

// List handles GET /schedule
func (h *ScheduleHandler) List(ctx context.Context, rc *app.RequestContext) {
	status := rc.Query("status")
	if status == "" {
		status = "all"
	}

	tasks, err := h.mgr.List(status)
	if err != nil {
		h.log.Error("Failed to list schedule tasks", zap.Error(err))
		rc.JSON(500, map[string]string{"error": err.Error()})
		return
	}

	if tasks == nil {
		tasks = []*schedule.Task{}
	}
	rc.JSON(200, tasks)
}

// Get handles GET /schedule/:id
func (h *ScheduleHandler) Get(ctx context.Context, rc *app.RequestContext) {
	id := rc.Param("id")

	task, err := h.mgr.Get(id)
	if err != nil {
		h.log.Error("Failed to get schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(404, map[string]string{"error": "task not found"})
		return
	}

	rc.JSON(200, task)
}

// Delete handles DELETE /schedule/:id
func (h *ScheduleHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	id := rc.Param("id")

	if err := h.mgr.Delete(id); err != nil {
		h.log.Error("Failed to delete schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(500, map[string]string{"error": err.Error()})
		return
	}

	rc.JSON(200, map[string]string{"status": "deleted", "id": id})
}

// Disable handles POST /schedule/:id/disable
func (h *ScheduleHandler) Disable(ctx context.Context, rc *app.RequestContext) {
	id := rc.Param("id")

	if err := h.mgr.Disable(id); err != nil {
		h.log.Error("Failed to disable schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(500, map[string]string{"error": err.Error()})
		return
	}

	rc.JSON(200, map[string]string{"status": "disabled", "id": id})
}

// Enable handles POST /schedule/:id/enable
func (h *ScheduleHandler) Enable(ctx context.Context, rc *app.RequestContext) {
	id := rc.Param("id")

	if err := h.mgr.Enable(id); err != nil {
		h.log.Error("Failed to enable schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(500, map[string]string{"error": err.Error()})
		return
	}

	rc.JSON(200, map[string]string{"status": "enabled", "id": id})
}

// Archive handles POST /schedule/:id/archive
func (h *ScheduleHandler) Archive(ctx context.Context, rc *app.RequestContext) {
	id := rc.Param("id")

	if err := h.mgr.Archive(id); err != nil {
		h.log.Error("Failed to archive schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(500, map[string]string{"error": err.Error()})
		return
	}

	rc.JSON(200, map[string]string{"status": "archived", "id": id})
}

// History handles GET /schedule/:id/history
func (h *ScheduleHandler) History(ctx context.Context, rc *app.RequestContext) {
	id := rc.Param("id")

	records, err := h.mgr.GetHistory(id)
	if err != nil {
		h.log.Error("Failed to get schedule history", zap.String("id", id), zap.Error(err))
		rc.JSON(500, map[string]string{"error": err.Error()})
		return
	}

	if records == nil {
		records = []schedule.ExecutionRecord{}
	}
	rc.JSON(200, records)
}
