package handler

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/memory"
)

// SessionHandler 会话处理器
type SessionHandler struct {
	memory *memory.Manager
}

// NewSessionHandler 创建会话处理器
func NewSessionHandler(mem *memory.Manager) *SessionHandler {
	return &SessionHandler{memory: mem}
}

// GetSession 处理 GET /sess/{sid}
func (h *SessionHandler) GetSession(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")

	if sessionID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"session_id 参数缺失"}`))
		return
	}

	info, err := h.memory.GetSessionInfo(sessionID)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(`{"status":"session_not_found","message":"会话不存在"}`))
		return
	}

	history, err := h.memory.GetHistory(sessionID)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"获取历史失败"}`))
		return
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"session": utils.H{
			"session_id":     info.SessionID,
			"created_at":     info.CreatedAt,
			"round_count":    info.RoundCount,
			"path":           info.Path,
			"last_active_at": getLastActiveTime(history),
		},
		"history": history,
	})
}

// getLastActiveTime returns the timestamp of the last message
func getLastActiveTime(history *memory.History) string {
	if len(history.Messages) == 0 {
		return ""
	}
	return history.Messages[len(history.Messages)-1].Timestamp.Format("2006-01-02T15:04:05Z")
}

// ListSessions 处理 GET /sess/history
func (h *SessionHandler) ListSessions(ctx context.Context, rc *app.RequestContext) {
	limit := rc.Query("limit")
	offset := rc.Query("offset")

	limitInt := 20
	offsetInt := 0

	if limit != "" {
		if parsed, err := strconv.Atoi(limit); err == nil && parsed > 0 && parsed <= 100 {
			limitInt = parsed
		}
	}
	if offset != "" {
		if parsed, err := strconv.Atoi(offset); err == nil && parsed >= 0 {
			offsetInt = parsed
		}
	}

	sessions, total, err := h.memory.ListSessions(limitInt, offsetInt)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"查询失败"}`))
		return
	}

	rc.JSON(200, utils.H{
		"status":   "success",
		"total":    total,
		"limit":    limitInt,
		"offset":   offsetInt,
		"sessions": sessions,
	})
}

// Serve 实现 handler 接口 - 默认路由到 ListSessions
func (h *SessionHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	h.ListSessions(ctx, rc)
}