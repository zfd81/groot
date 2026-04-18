package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/memory"
)

// StatusHandler handles GET /chat/status/{sid}
type StatusHandler struct {
	runtimeState *agent.RuntimeState
	memory       *memory.Manager
}

// NewStatusHandler creates a new status handler
func NewStatusHandler(
	runtime *agent.RuntimeState,
	mem *memory.Manager,
) *StatusHandler {
	return &StatusHandler{
		runtimeState: runtime,
		memory:       mem,
	}
}

// Serve handles the status request
func (h *StatusHandler) Serve(ctx context.Context, rc *app.RequestContext) {
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

	// 获取活跃对话状态
	activeChat, ok := h.runtimeState.Get(sessionID)
	if !ok {
		// 没有活跃对话，返回历史状态
		history, err := h.memory.GetHistory(sessionID)
		if err != nil {
			rc.SetContentType("application/json")
			rc.SetStatusCode(500)
			rc.Write([]byte(`{"status":"error","message":"获取历史失败"}`))
			return
		}

		rc.JSON(200, utils.H{
			"status":       "idle",
			"session_id":   sessionID,
			"round_count":  len(history.Messages),
			"last_message": getLastMessage(history),
		})
		return
	}

	// 返回活跃对话状态
	duration := formatElapsed(time.Since(activeChat.StartTime))

	rc.JSON(200, utils.H{
		"status":       "running",
		"session_id":   sessionID,
		"chat_id":      activeChat.ChatID,
		"start_time":   activeChat.StartTime.Format(time.RFC3339),
		"duration":     duration,
		"progress":     activeChat.Progress,
	})
}

// getLastMessage 获取最后一条消息摘要
func getLastMessage(history *memory.History) map[string]interface{} {
	if len(history.Messages) == 0 {
		return nil
	}
	last := history.Messages[len(history.Messages)-1]
	return map[string]interface{}{
		"round":    last.Round,
		"chat_id":  last.ChatID,
		"status":   last.Status,
		"duration": last.Duration,
	}
}

// formatElapsed formats elapsed time
func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) - minutes*60
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}