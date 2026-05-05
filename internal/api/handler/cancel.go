package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/memory"
)

// CancelHandler handles DELETE /chat/{sid}
type CancelHandler struct {
	runtimeState *agent.RuntimeState
	memory       *memory.Manager
}

// NewCancelHandler creates a new cancel handler
func NewCancelHandler(
	runtime *agent.RuntimeState,
	mem *memory.Manager,
) *CancelHandler {
	return &CancelHandler{
		runtimeState: runtime,
		memory:       mem,
	}
}

// Serve handles the cancel request
func (h *CancelHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")

	if sessionID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"session_id 参数缺失"}`))
		return
	}

	// 检查是否有活跃对话
	activeChat, ok := h.runtimeState.Get(sessionID)
	if !ok {
		// 没有活跃对话，返回特定状态（幂等操作）
		rc.JSON(200, utils.H{
			"status":     "no_running_chat",
			"session_id": sessionID,
			"message":    "该会话当前没有正在执行的对话",
		})
		return
	}

	// 执行取消（直接操作 ActiveChat，无 TOCTOU 竞态）
	activeChat.Cancel()

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"chat_id":    activeChat.ChatID,
		"message":    "对话已取消",
	})
}