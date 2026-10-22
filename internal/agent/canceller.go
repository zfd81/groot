package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/zfd81/groot/internal/cluster"
)

const (
	// ClusterModuleName 是对话模块在集群消息服务中注册的模块名（TargetModule）。
	ClusterModuleName = "chat"
	// MessageTypeCancel 是取消对话指令的消息类型。
	MessageTypeCancel = "chat.cancel"
	// cancelMessageTTL 取消指令的有效期：对话取消是即时操作，过期的指令没有意义。
	cancelMessageTTL = 30 * time.Second
)

// MessageSender 发送集群消息。由 *cluster.MessageService 实现；
// 传 nil 接口表示没有集群消息服务可用，不做广播。
type MessageSender interface {
	SendMessage(ctx context.Context, msg cluster.Message) error
}

// CancelResult 描述一次取消请求的处理结果。
type CancelResult int

const (
	// CancelLocal 对话在本节点运行，已直接取消。
	CancelLocal CancelResult = iota
	// CancelBroadcast 本节点没有该对话，取消指令已广播给集群其他节点。
	CancelBroadcast
	// CancelNotRunning 本节点没有该对话，且无法广播（集群中没有其他存活节点或发送失败）。
	CancelNotRunning
)

// String 实现 fmt.Stringer，便于日志与测试输出。
func (r CancelResult) String() string {
	switch r {
	case CancelLocal:
		return "local"
	case CancelBroadcast:
		return "broadcast"
	case CancelNotRunning:
		return "not_running"
	default:
		return fmt.Sprintf("CancelResult(%d)", int(r))
	}
}

// ChatCanceller 取消一次对话：先尝试本地，再按需跨节点传播。
type ChatCanceller interface {
	Cancel(ctx context.Context, sessionID, chatID string) (CancelResult, error)
	// OthersAlive 报告集群中是否存在除本节点外、心跳未超时的其他成员。
	// 为 false 时没有广播的对象，调用方可直接按"未在执行"处理。
	OthersAlive(ctx context.Context) bool
}

// ClusterCanceller 是 ChatCanceller 的集群实现：
// 本地 RuntimeState 命中则直接取消；未命中则通过 sender 向 chat 模块广播取消指令，
// 由运行该对话的节点上的 ClusterHandler 执行。
type ClusterCanceller struct {
	rt     *RuntimeState
	sender MessageSender
	// othersAlive 探测集群中是否存在其他存活节点；nil 视为恒为 false。
	othersAlive func(ctx context.Context) bool
}

// NewClusterCanceller 创建集群取消器。
// sender 为 nil 接口表示没有消息服务，不做广播；
// othersAlive 为 nil 视为"集群中没有其他存活节点"，同样不做广播。
func NewClusterCanceller(rt *RuntimeState, sender MessageSender, othersAlive func(ctx context.Context) bool) *ClusterCanceller {
	return &ClusterCanceller{rt: rt, sender: sender, othersAlive: othersAlive}
}

// OthersAlive 实现 ChatCanceller：探测函数缺失时返回 false。
func (c *ClusterCanceller) OthersAlive(ctx context.Context) bool {
	return c.othersAlive != nil && c.othersAlive(ctx)
}

// Cancel 实现 ChatCanceller。
//
//   - 本地命中 → CancelLocal, nil（不发送任何消息）
//   - sender 为 nil 或集群中没有其他存活节点 → CancelNotRunning, nil（不发送任何消息）
//   - 广播成功 → CancelBroadcast, nil
//   - 广播失败 → CancelNotRunning, err
//
// chatID 为空时匹配 session 的任意活跃对话（语义同 RuntimeState.Cancel），
// 广播时原样携带，由接收节点做同样匹配。
func (c *ClusterCanceller) Cancel(ctx context.Context, sessionID, chatID string) (CancelResult, error) {
	if c.rt.Cancel(sessionID, chatID) {
		return CancelLocal, nil
	}
	if c.sender == nil || !c.OthersAlive(ctx) {
		return CancelNotRunning, nil
	}
	msg := cluster.Message{
		Type:         MessageTypeCancel,
		TargetModule: ClusterModuleName,
		Payload: map[string]any{
			"session_id": sessionID,
			"chat_id":    chatID,
		},
		ExpiresAt: time.Now().Add(cancelMessageTTL),
	}
	if err := c.sender.SendMessage(ctx, msg); err != nil {
		return CancelNotRunning, fmt.Errorf("广播取消指令失败: %w", err)
	}
	return CancelBroadcast, nil
}
