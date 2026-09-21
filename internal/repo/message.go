// internal/repo/message.go
package repo

import (
	"context"
	"time"
)

// 消费记录状态。
const (
	ConsumeStatusSuccess = "success"
	ConsumeStatusFailed  = "failed"
)

// ClusterMessage 是集群实例间的一条指令消息（cluster_messages 表的一行）。
// TargetInstance 为空串表示广播：所有实例都要处理；非空表示只有该 reg_id 的实例处理。
// Payload 是 JSON 文本，由 cluster 包负责与 map[string]any 互转。
type ClusterMessage struct {
	ID             int64
	Type           string
	Payload        string
	TargetInstance string
	TargetModule   string
	Priority       int
	CreatedAt      time.Time
	ExpiresAt      time.Time
	SourceInstance string
}

// MessageConsumer 记录某个实例对某条消息的处理结果（cluster_message_consumers 表的一行）。
type MessageConsumer struct {
	MessageID    int64
	InstanceID   string
	ConsumedAt   time.Time
	Status       string
	ErrorMessage string
}

// MessageRepo 是集群消息的持久化接口。
type MessageRepo interface {
	// Insert 写入一条消息。ID 由数据库自增生成，调用方不依赖回填。
	Insert(ctx context.Context, m *ClusterMessage) error

	// ListPending 返回 instanceID 尚未消费、未过期、且目标为广播或该实例的消息，
	// 按 priority 升序、created_at 升序、id 升序排列，最多 limit 条。limit 非正数时返回空结果。
	ListPending(ctx context.Context, instanceID string, now time.Time, limit int) ([]*ClusterMessage, error)

	// RecordConsumption 写入一条消费记录。同一 (message_id, instance_id) 重复写入返回错误。
	RecordConsumption(ctx context.Context, c *MessageConsumer) error

	// ListConsumers 返回某条消息的全部消费记录，按 instance_id 升序。
	ListConsumers(ctx context.Context, messageID int64) ([]*MessageConsumer, error)

	// DeleteBefore 删除 created_at 早于 before 的消息，并清理已无对应消息的孤立消费记录。
	// 返回删除的消息数与消费记录数。
	DeleteBefore(ctx context.Context, before time.Time) (deletedMessages int, deletedConsumers int, err error)
}
