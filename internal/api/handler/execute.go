package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/storage"
)

// ExecuteHandler handles POST /task/execute
type ExecuteHandler struct {
	storage       storage.TaskStorage
	executor      *agent.Executor
	cancelManager *agent.CancelManager
}

// NewExecuteHandler creates a new execute handler
func NewExecuteHandler(
	store storage.TaskStorage,
	exec *agent.Executor,
	cancelMgr *agent.CancelManager,
) *ExecuteHandler {
	return &ExecuteHandler{
		storage:       store,
		executor:      exec,
		cancelManager: cancelMgr,
	}
}

// Serve handles the execute request
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

	// Generate task ID
	taskID := agent.GenerateTaskID()

	// Create task record
	task := &storage.Task{
		ID:          taskID,
		Instruction: req.Instruction,
		Prompt:      req.Prompt,
		Attachments: convertAttachments(req.Attachments),
		Status:      storage.StatusRunning,
		StartTime:   time.Now(),
		Caller:      middleware.GetCaller(rc),
	}

	// Save to storage
	if err := h.storage.Create(task); err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(fmt.Sprintf(`{"status":"storage_error","message":"%s"}`, err)))
		return
	}

	// Set SSE headers
	rc.SetContentType("text/event-stream")
	rc.Header("X-Task-ID", taskID)
	rc.Header("Cache-Control", "no-cache")
	rc.Header("Connection", "keep-alive")

	// Register for cancellation
	cancelCh := h.cancelManager.Register(taskID)

	// Create SSE writer
	sse := agent.NewSSEWriter(rc)

	// Execute task
	h.executor.Execute(task, sse, cancelCh)
}

// convertAttachments converts API attachments to storage attachments
func convertAttachments(att []types.Attachment) []storage.Attachment {
	if att == nil {
		return nil
	}
	result := make([]storage.Attachment, len(att))
	for i, a := range att {
		result[i] = storage.Attachment{
			Type:    a.Type,
			Name:    a.Name,
			Content: a.Content,
		}
	}
	return result
}
