// internal/cluster/message_handler.go
package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// Message 是集群消息在 Go 侧的表示。
// TargetInstance 为空表示广播；非空表示只投递给该 reg_id 的实例。
// TargetModule 决定消息被路由到哪个已注册的 MessageHandler。
type Message struct {
	ID             int64
	Type           string
	TargetInstance string
	TargetModule   string
	Payload        map[string]any
	Priority       int
	CreatedAt      time.Time
	ExpiresAt      time.Time
	SourceInstance string
}

// MessageHandler 是模块处理集群消息的抽象接口。
// 返回非 nil error 表示处理失败：结果记录到消费记录表后该消息不再投递给本实例。
// 消息保证至少送达一次：仅当处理成功但消费记录写入失败时，下一轮会重复投递，
// 因此处理逻辑必须是幂等的。
type MessageHandler interface {
	Handle(ctx context.Context, msg Message) error
}

// HandlerFunc 让普通函数满足 MessageHandler 接口。
type HandlerFunc func(ctx context.Context, msg Message) error

// Handle 实现 MessageHandler。
func (f HandlerFunc) Handle(ctx context.Context, msg Message) error { return f(ctx, msg) }

// toRepoMessage 把 Message 转为持久化结构；Payload 序列化为 JSON，nil 视为 {}。
func toRepoMessage(m Message) (*repo.ClusterMessage, error) {
	payload := m.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化 payload 失败: %w", err)
	}
	return &repo.ClusterMessage{
		ID:             m.ID,
		Type:           m.Type,
		Payload:        string(raw),
		TargetInstance: m.TargetInstance,
		TargetModule:   m.TargetModule,
		Priority:       m.Priority,
		CreatedAt:      m.CreatedAt,
		ExpiresAt:      m.ExpiresAt,
		SourceInstance: m.SourceInstance,
	}, nil
}

// fromRepoMessage 把持久化结构转回 Message；payload 非法 JSON 时返回错误。
func fromRepoMessage(rm *repo.ClusterMessage) (Message, error) {
	var payload map[string]any
	if rm.Payload != "" {
		if err := json.Unmarshal([]byte(rm.Payload), &payload); err != nil {
			return Message{}, fmt.Errorf("解析 payload 失败: %w", err)
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return Message{
		ID:             rm.ID,
		Type:           rm.Type,
		TargetInstance: rm.TargetInstance,
		TargetModule:   rm.TargetModule,
		Payload:        payload,
		Priority:       rm.Priority,
		CreatedAt:      rm.CreatedAt,
		ExpiresAt:      rm.ExpiresAt,
		SourceInstance: rm.SourceInstance,
	}, nil
}
