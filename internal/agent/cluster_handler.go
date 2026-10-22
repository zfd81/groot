package agent

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/logger"
)

// ClusterHandler 处理集群消息模块 chat 的指令（目前只有 chat.cancel）。
// 它实现 cluster.MessageHandler。
type ClusterHandler struct {
	rt  *RuntimeState
	log *logger.Logger
}

// NewClusterHandler 创建处理器。log 为 nil 时使用空日志。
func NewClusterHandler(rt *RuntimeState, log *logger.Logger) cluster.MessageHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &ClusterHandler{rt: rt, log: log}
}

// Handle 实现 cluster.MessageHandler。
//
// 取消指令是广播给所有节点的，绝大多数节点上并没有目标对话，因此本地未命中
// 不是错误（返回 nil），否则每条指令都会在 N-1 个节点上留下失败记录。
// 只有消息本身不合法（类型不对、缺 session_id）才返回错误。
func (h *ClusterHandler) Handle(ctx context.Context, msg cluster.Message) error {
	if msg.Type != MessageTypeCancel {
		return fmt.Errorf("chat: 不支持的消息类型 %q", msg.Type)
	}
	sessionID, ok := msg.Payload["session_id"].(string)
	if !ok || sessionID == "" {
		return fmt.Errorf("chat: 取消指令缺少有效的 session_id (msg_id=%d)", msg.ID)
	}
	// chat_id 缺失或类型不对按空串处理：匹配该 session 的任意活跃对话。
	chatID, _ := msg.Payload["chat_id"].(string)

	if h.rt.Cancel(sessionID, chatID) {
		h.log.Info("已按集群指令取消本地对话",
			zap.Int64("msg_id", msg.ID),
			zap.String("source", msg.SourceInstance),
			zap.String("session_id", sessionID),
			zap.String("chat_id", chatID))
	} else {
		h.log.Debug("集群取消指令在本节点未命中",
			zap.Int64("msg_id", msg.ID),
			zap.String("session_id", sessionID),
			zap.String("chat_id", chatID))
	}
	return nil
}
