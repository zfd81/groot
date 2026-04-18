package handler

import (
	"context"
	"fmt"

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

	// 检查会话是否存在
	if !h.memory.ExistsSession(sessionID) {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(fmt.Sprintf(`{"status":"session_not_found","session_id":"%s","message":"会话不存在"}`, sessionID)))
		return
	}

	// 检查是否有活跃对话
	activeChat, ok := h.runtimeState.Get(sessionID)
	if !ok {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(fmt.Sprintf(`{"status":"no_running_chat","session_id":"%s","message":"该会话当前没有正在执行的对话"}`, sessionID)))
		return
	}

	// 执行取消
	if err := h.runtimeState.Cancel(sessionID); err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(fmt.Sprintf(`{"status":"error","session_id":"%s","message":"取消失败: %s"}`, sessionID, err.Error())))
		return
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"chat_id":    activeChat.ChatID,
		"message":    "对话已取消",
	})
}