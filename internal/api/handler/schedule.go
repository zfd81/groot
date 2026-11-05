package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/schedule"
	"github.com/zfd81/groot/internal/setting"
)

// ScheduleHandler handles schedule task REST endpoints
type ScheduleHandler struct {
	mgr      **schedule.Manager
	settings *setting.Settings
	log      *logger.Logger
}

// NewScheduleHandler creates a new schedule handler；settings 不能为 nil，生产装配总是传入。
func NewScheduleHandler(mgr **schedule.Manager, settings *setting.Settings, log *logger.Logger) *ScheduleHandler {
	return &ScheduleHandler{mgr: mgr, settings: settings, log: log}
}

// manager 返回当前可用的调度管理器；不可用时已写好 503 响应并返回 false。
//
// 不可用有两种情况：本节点不是 Leader（管理器为 nil），或 schedule.enabled 为关。
// 开关每次请求读一次配置表，设置面板保存后即刻生效；配置表读不到时按静态默认值判定，
// 一次查询失败不该让整组接口变成 503。
func (h *ScheduleHandler) manager(ctx context.Context, rc *app.RequestContext) (*schedule.Manager, bool) {
	mgr := *h.mgr
	if mgr == nil {
		errbody.Write(rc, 503, "调度服务不可用", errbody.TypeAPI, "", errbody.CodeScheduleUnavailable)
		return nil, false
	}
	enabled := h.settings.RuntimeStatic().Schedule.Enabled
	if sc, err := h.settings.Schedule(ctx); err != nil {
		h.log.Warn("读取调度开关失败,按静态配置处理", zap.Error(err))
	} else {
		enabled = sc.Enabled
	}
	if !enabled {
		errbody.Write(rc, 503, "调度服务未启用", errbody.TypeAPI, "", errbody.CodeScheduleUnavailable)
		return nil, false
	}
	return mgr, true
}

// List handles GET /schedule
func (h *ScheduleHandler) List(ctx context.Context, rc *app.RequestContext) {
	mgr, ok := h.manager(ctx, rc)
	if !ok {
		return
	}

	status := rc.Query("status")
	if status == "" {
		status = "all"
	}

	tasks, err := mgr.List(status)
	if err != nil {
		h.log.Error("Failed to list schedule tasks", zap.Error(err))
		errbody.Write(rc, 500, err.Error(), errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	if tasks == nil {
		tasks = []*schedule.Task{}
	}
	rc.JSON(200, tasks)
}

// Get handles GET /schedule/:id
func (h *ScheduleHandler) Get(ctx context.Context, rc *app.RequestContext) {
	mgr, ok := h.manager(ctx, rc)
	if !ok {
		return
	}

	id := rc.Param("id")

	task, err := mgr.Get(id)
	if err != nil {
		h.log.Error("Failed to get schedule task", zap.String("id", id), zap.Error(err))
		errbody.Write(rc, 404, "任务不存在", errbody.TypeInvalidRequest, "id", errbody.CodeTaskNotFound)
		return
	}

	rc.JSON(200, task)
}

// Delete handles DELETE /schedule/:id
func (h *ScheduleHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	mgr, ok := h.manager(ctx, rc)
	if !ok {
		return
	}

	id := rc.Param("id")

	if err := mgr.Delete(id); err != nil {
		h.log.Error("Failed to delete schedule task", zap.String("id", id), zap.Error(err))
		errbody.Write(rc, 500, err.Error(), errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	rc.JSON(200, map[string]string{"status": "deleted", "id": id})
}

// Disable handles POST /schedule/:id/disable
func (h *ScheduleHandler) Disable(ctx context.Context, rc *app.RequestContext) {
	mgr, ok := h.manager(ctx, rc)
	if !ok {
		return
	}

	id := rc.Param("id")

	if err := mgr.Disable(id); err != nil {
		h.log.Error("Failed to disable schedule task", zap.String("id", id), zap.Error(err))
		errbody.Write(rc, 500, err.Error(), errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	rc.JSON(200, map[string]string{"status": "disabled", "id": id})
}

// Enable handles POST /schedule/:id/enable
func (h *ScheduleHandler) Enable(ctx context.Context, rc *app.RequestContext) {
	mgr, ok := h.manager(ctx, rc)
	if !ok {
		return
	}

	id := rc.Param("id")

	if err := mgr.Enable(id); err != nil {
		h.log.Error("Failed to enable schedule task", zap.String("id", id), zap.Error(err))
		errbody.Write(rc, 500, err.Error(), errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	rc.JSON(200, map[string]string{"status": "enabled", "id": id})
}

// Archive handles POST /schedule/:id/archive
func (h *ScheduleHandler) Archive(ctx context.Context, rc *app.RequestContext) {
	mgr, ok := h.manager(ctx, rc)
	if !ok {
		return
	}

	id := rc.Param("id")

	if err := mgr.Archive(id); err != nil {
		h.log.Error("Failed to archive schedule task", zap.String("id", id), zap.Error(err))
		errbody.Write(rc, 500, err.Error(), errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	rc.JSON(200, map[string]string{"status": "archived", "id": id})
}

// History handles GET /schedule/:id/history
func (h *ScheduleHandler) History(ctx context.Context, rc *app.RequestContext) {
	mgr, ok := h.manager(ctx, rc)
	if !ok {
		return
	}

	id := rc.Param("id")

	records, err := mgr.GetHistory(id)
	if err != nil {
		h.log.Error("Failed to get schedule history", zap.String("id", id), zap.Error(err))
		errbody.Write(rc, 500, err.Error(), errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	if records == nil {
		records = []schedule.ExecutionRecord{}
	}
	rc.JSON(200, records)
}
