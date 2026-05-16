package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/schedule"
)

// ScheduleHandler handles schedule task REST endpoints
type ScheduleHandler struct {
	mgr **schedule.Manager
	log *logger.Logger
}

// NewScheduleHandler creates a new schedule handler
func NewScheduleHandler(mgr **schedule.Manager, log *logger.Logger) *ScheduleHandler {
	return &ScheduleHandler{mgr: mgr, log: log}
}

// List handles GET /schedule
func (h *ScheduleHandler) List(ctx context.Context, rc *app.RequestContext) {
	mgr := *h.mgr
	if mgr == nil {
		rc.JSON(503, utils.H{"status": "schedule_unavailable", "message": "调度服务不可用"})
		return
	}

	status := rc.Query("status")
	if status == "" {
		status = "all"
	}

	tasks, err := mgr.List(status)
	if err != nil {
		h.log.Error("Failed to list schedule tasks", zap.Error(err))
		rc.JSON(500, utils.H{"status": "schedule_error", "message": err.Error()})
		return
	}

	if tasks == nil {
		tasks = []*schedule.Task{}
	}
	rc.JSON(200, tasks)
}

// Get handles GET /schedule/:id
func (h *ScheduleHandler) Get(ctx context.Context, rc *app.RequestContext) {
	mgr := *h.mgr
	if mgr == nil {
		rc.JSON(503, utils.H{"status": "schedule_unavailable", "message": "调度服务不可用"})
		return
	}

	id := rc.Param("id")

	task, err := mgr.Get(id)
	if err != nil {
		h.log.Error("Failed to get schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(404, utils.H{"status": "task_not_found", "message": "任务不存在"})
		return
	}

	rc.JSON(200, task)
}

// Delete handles DELETE /schedule/:id
func (h *ScheduleHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	mgr := *h.mgr
	if mgr == nil {
		rc.JSON(503, utils.H{"status": "schedule_unavailable", "message": "调度服务不可用"})
		return
	}

	id := rc.Param("id")

	if err := mgr.Delete(id); err != nil {
		h.log.Error("Failed to delete schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(500, utils.H{"status": "schedule_error", "message": err.Error()})
		return
	}

	rc.JSON(200, map[string]string{"status": "deleted", "id": id})
}

// Disable handles POST /schedule/:id/disable
func (h *ScheduleHandler) Disable(ctx context.Context, rc *app.RequestContext) {
	mgr := *h.mgr
	if mgr == nil {
		rc.JSON(503, utils.H{"status": "schedule_unavailable", "message": "调度服务不可用"})
		return
	}

	id := rc.Param("id")

	if err := mgr.Disable(id); err != nil {
		h.log.Error("Failed to disable schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(500, utils.H{"status": "schedule_error", "message": err.Error()})
		return
	}

	rc.JSON(200, map[string]string{"status": "disabled", "id": id})
}

// Enable handles POST /schedule/:id/enable
func (h *ScheduleHandler) Enable(ctx context.Context, rc *app.RequestContext) {
	mgr := *h.mgr
	if mgr == nil {
		rc.JSON(503, utils.H{"status": "schedule_unavailable", "message": "调度服务不可用"})
		return
	}

	id := rc.Param("id")

	if err := mgr.Enable(id); err != nil {
		h.log.Error("Failed to enable schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(500, utils.H{"status": "schedule_error", "message": err.Error()})
		return
	}

	rc.JSON(200, map[string]string{"status": "enabled", "id": id})
}

// Archive handles POST /schedule/:id/archive
func (h *ScheduleHandler) Archive(ctx context.Context, rc *app.RequestContext) {
	mgr := *h.mgr
	if mgr == nil {
		rc.JSON(503, utils.H{"status": "schedule_unavailable", "message": "调度服务不可用"})
		return
	}

	id := rc.Param("id")

	if err := mgr.Archive(id); err != nil {
		h.log.Error("Failed to archive schedule task", zap.String("id", id), zap.Error(err))
		rc.JSON(500, utils.H{"status": "schedule_error", "message": err.Error()})
		return
	}

	rc.JSON(200, map[string]string{"status": "archived", "id": id})
}

// History handles GET /schedule/:id/history
func (h *ScheduleHandler) History(ctx context.Context, rc *app.RequestContext) {
	mgr := *h.mgr
	if mgr == nil {
		rc.JSON(503, utils.H{"status": "schedule_unavailable", "message": "调度服务不可用"})
		return
	}

	id := rc.Param("id")

	records, err := mgr.GetHistory(id)
	if err != nil {
		h.log.Error("Failed to get schedule history", zap.String("id", id), zap.Error(err))
		rc.JSON(500, utils.H{"status": "schedule_error", "message": err.Error()})
		return
	}

	if records == nil {
		records = []schedule.ExecutionRecord{}
	}
	rc.JSON(200, records)
}
