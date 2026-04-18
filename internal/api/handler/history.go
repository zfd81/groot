package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	// "github.com/zfd81/groot/internal/api/types" // removed - not used
	// "github.com/zfd81/groot/internal/storage" // removed - will be re-added in Phase 4
)

// HistoryHandler handles GET /task/history
// NOTE: temporarily disabled until memory module implemented
type HistoryHandler struct {
	// storage storage.TaskStorage // removed
}

// NewHistoryHandler creates a new history handler
// NOTE: temporarily disabled - will be re-enabled in Phase 4
func NewHistoryHandler(
	// store storage.TaskStorage, // removed
) *HistoryHandler {
	return &HistoryHandler{
		// storage: store,
	}
}

// Serve handles the history request
// NOTE: temporarily returns error until memory module implemented
func (h *HistoryHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	// Storage query disabled
	// query := storage.TaskQuery{...}
	// tasks, total, err := h.storage.List(&query)
	// ...

	// Temporary placeholder response
	rc.SetContentType("application/json")
	rc.SetStatusCode(503)
	rc.Write([]byte(`{"status":"service_unavailable","message":"任务历史查询功能暂时不可用，正在升级存储模块"}`))
}