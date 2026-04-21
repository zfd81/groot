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

	// 获取活跃对话状态
	activeChat, ok := h.runtimeState.Get(sessionID)
	if !ok {
		// 没有活跃对话，返回 idle 状态
		// 如果会话存在，返回历史信息；如果不存在，返回 chat=None
		if h.memory.ExistsSession(sessionID) {
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
				"chat":         nil,
			})
			return
		}

		// 会话不存在，返回 chat=None (不返回 404)
		rc.JSON(200, utils.H{
			"status":       "idle",
			"session_id":   sessionID,
			"round_count":  0,
			"last_message": nil,
			"chat":         nil,
		})
		return
	}

	// 返回活跃对话状态
	duration := formatElapsed(time.Since(activeChat.StartTime))

	// 获取当前轮数
	round := 1
	if h.memory.ExistsSession(sessionID) {
		history, err := h.memory.GetHistory(sessionID)
		if err == nil {
			round = len(history.Messages) + 1
		}
	}

	rc.JSON(200, utils.H{
		"status":       "success",
		"session_id":   sessionID,
		"chat": utils.H{
			"chat_id":      activeChat.ChatID,
			"status":       activeChat.Status,
			"started_at":   activeChat.StartTime.Format(time.RFC3339),
			"elapsed_time": duration,
			"duration":     duration,
			"round":        round,
			"progress":     activeChat.Progress,
		},
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