package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/memory"
)

// DetailHandler handles GET /chat/{sid}/{cid}
type DetailHandler struct {
	memory *memory.Manager
}

// NewDetailHandler creates a new detail handler
func NewDetailHandler(mem *memory.Manager) *DetailHandler {
	return &DetailHandler{
		memory: mem,
	}
}

// Serve handles the detail request (GET /chat/:sid/:cid)
func (h *DetailHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")
	chatID := rc.Param("cid")

	if sessionID == "" {
		writeMissingSessionID(rc)
		return
	}

	if chatID == "" {
		writeMissingChatID(rc)
		return
	}

	owner, ok := requireOwner(rc)
	if !ok {
		return
	}

	// 检查会话是否存在且归属于调用者（归属不符与不存在同样返回 404）
	if !ownsOrAdmin(h.memory, owner, sessionID) {
		writeSessionNotFound(rc)
		return
	}

	// 获取对话记录
	record, err := h.memory.GetChatRecord(sessionID, chatID)
	if err != nil {
		errbody.Write(rc, 404, "对话记录不存在",
			errbody.TypeInvalidRequest, "", errbody.CodeTaskNotFound)
		return
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"chat":       record,
	})
}

// GetLatest handles GET /chat/:sid - returns latest chat for session
func (h *DetailHandler) GetLatest(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")

	if sessionID == "" {
		writeMissingSessionID(rc)
		return
	}

	owner, ok := requireOwner(rc)
	if !ok {
		return
	}

	// 检查会话是否存在且归属于调用者（归属不符与不存在同样返回 404）
	if !ownsOrAdmin(h.memory, owner, sessionID) {
		writeSessionNotFound(rc)
		return
	}

	// 获取最近一次对话记录
	record, err := h.memory.GetLatestChatRecord(sessionID)
	if err != nil {
		errbody.Write(rc, 500, "获取对话记录失败", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	if record == nil {
		rc.JSON(200, utils.H{
			"status":     "success",
			"session_id": sessionID,
			"chat":       nil,
		})
		return
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"chat":       record,
	})
}
