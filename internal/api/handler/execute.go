package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	// "github.com/zfd81/groot/internal/storage" // removed - will be re-added in Phase 4
)

// Attachment represents attachment data (temporary definition)
type Attachment struct {
	Type    string
	Name    string
	Content string
}

// ExecuteHandler handles POST /task/execute
// NOTE: temporarily disabled until memory module implemented
type ExecuteHandler struct {
	// storage       storage.TaskStorage // removed
	executor      *agent.Executor
	cancelManager *agent.CancelManager
}

// NewExecuteHandler creates a new execute handler
// NOTE: temporarily disabled - will be re-enabled in Phase 4
func NewExecuteHandler(
	// store storage.TaskStorage, // removed
	exec *agent.Executor,
	cancelMgr *agent.CancelManager,
) *ExecuteHandler {
	return &ExecuteHandler{
		// storage:       store,
		executor:      exec,
		cancelManager: cancelMgr,
	}
}

// Serve handles the execute request
// NOTE: temporarily returns error until memory module implemented
func (h *ExecuteHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	var req types.ExecuteRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"无法解析请求体"}`))
		return
	}

	// Validate instruction
	if req.Instruction == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"instruction 字段不能为空"}`))
		return
	}

	// Temporary placeholder response
	rc.SetContentType("application/json")
	rc.SetStatusCode(503)
	rc.Write([]byte(`{"status":"service_unavailable","message":"任务执行功能暂时不可用，正在升级存储模块"}`))
}

// convertAttachments converts API attachments to storage attachments
// NOTE: temporarily disabled - will be re-enabled in Phase 4
func convertAttachments(att []types.Attachment) []Attachment {
	if att == nil {
		return nil
	}
	result := make([]Attachment, len(att))
	for i, a := range att {
		result[i] = Attachment{
			Type:    a.Type,
			Name:    a.Name,
			Content: a.Content,
		}
	}
	return result
}