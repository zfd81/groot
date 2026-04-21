package handler

import (
	"context"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

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
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"session_id 参数缺失"}`))
		return
	}

	if chatID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"chat_id 参数缺失"}`))
		return
	}

	// 检查会话是否存在
	if !h.memory.ExistsSession(sessionID) {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(fmt.Sprintf(`{"status":"session_not_found","session_id":"%s","message":"会话不存在"}`, sessionID)))
		return
	}

	// 获取对话记录
	record, err := h.memory.GetChatRecord(sessionID, chatID)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(fmt.Sprintf(`{"status":"chat_not_found","session_id":"%s","chat_id":"%s","message":"对话记录不存在"}`, sessionID, chatID)))
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

	// 获取最近一次对话记录
	record, err := h.memory.GetLatestChatRecord(sessionID)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"获取对话记录失败"}`))
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