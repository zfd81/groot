package handler

import (
	"context"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"

	// "github.com/zfd81/groot/internal/api/types" // removed - not used
	// "github.com/zfd81/groot/internal/storage" // removed - will be re-added in Phase 4
)

// DetailHandler handles GET /task/{task_id}
// NOTE: temporarily disabled until memory module implemented
type DetailHandler struct {
	// storage storage.TaskStorage // removed
}

// NewDetailHandler creates a new detail handler
// NOTE: temporarily disabled - will be re-enabled in Phase 4
func NewDetailHandler(
	// store storage.TaskStorage, // removed
) *DetailHandler {
	return &DetailHandler{
		// storage: store,
	}
}

// Serve handles the detail request
// NOTE: temporarily returns error until memory module implemented
func (h *DetailHandler) Serve(ctx context.Context, rc *app.RequestContext) {
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
	rc.Write([]byte(fmt.Sprintf(`{"status":"service_unavailable","task_id":"%s","message":"任务详情查询功能暂时不可用，正在升级存储模块"}`, taskID)))
}