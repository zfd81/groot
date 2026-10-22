package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
)

// StopHandler handles POST /chat/{sid}/{cid}/stop：中止正在生成的对话。
type StopHandler struct {
	canceller agent.ChatCanceller
	memory    *memory.Manager
	log       *logger.Logger
}

// NewStopHandler creates a new stop handler
func NewStopHandler(canceller agent.ChatCanceller, mem *memory.Manager, log *logger.Logger) *StopHandler {
	return &StopHandler{
		canceller: canceller,
		memory:    mem,
		log:       log,
	}
}

// Serve handles the stop request (POST /chat/:sid/:cid/stop)。
// 处理顺序：路径参数校验 → 调用者归属前置检查 → 会话归属校验 → 取消。
// 取消结果映射：本地命中 200；已广播集群 202；未在执行 409。
func (h *StopHandler) Serve(ctx context.Context, rc *app.RequestContext) {
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

	owner, ok := requireOwner(rc)
	if !ok {
		return
	}

	// 归属不符与不存在同样返回 404，与 GET /sess/{sid} 保持一致
	if !ownsOrAdmin(h.memory, owner, sessionID) {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(`{"status":"session_not_found","message":"会话不存在"}`))
		return
	}

	res, err := h.canceller.Cancel(ctx, sessionID, chatID)
	switch res {
	case agent.CancelLocal:
		rc.JSON(200, utils.H{
			"status":     "success",
			"session_id": sessionID,
			"chat_id":    chatID,
			"message":    "对话已请求中止",
		})
	case agent.CancelBroadcast:
		rc.JSON(202, utils.H{
			"status":     "accepted",
			"session_id": sessionID,
			"chat_id":    chatID,
			"message":    "中止请求已广播到集群",
		})
	default:
		if err != nil {
			h.log.Warn("广播中止请求失败",
				zap.String("session_id", sessionID), zap.String("chat_id", chatID), zap.Error(err))
		}
		rc.JSON(409, utils.H{
			"status":     "chat_not_running",
			"session_id": sessionID,
			"chat_id":    chatID,
			"message":    "该对话未在执行",
		})
	}
}
