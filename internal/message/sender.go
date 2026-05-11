package message

import (
	"context"
	"fmt"
	"time"
)

// ErrQueueFull is returned when the send queue is full
var ErrQueueFull = fmt.Errorf("消息队列已满")

// Event represents a notification event
type Event struct {
	Type     string         // 事件类型，如 "schedule.completed"、"system.alert"
	Time     time.Time      // 事件发生时间
	Title    string         // 事件标题
	Content  string         // 事件内容
	Metadata map[string]any // 附加元数据
}

// SendResult represents the result of sending to one channel
type SendResult struct {
	Channel   string    // 渠道名
	Success   bool      // 是否发送成功
	Message   string    // 结果描述
	Timestamp time.Time // 发送时间
}

// Sender is the interface for message delivery channels
type Sender interface {
	Name() string
	Send(ctx context.Context, event Event) SendResult
}

// sendJob wraps a publish request for the queue
type sendJob struct {
	ctx      context.Context
	event    Event
	channels []string
	resultCh chan []SendResult // buffer=1，防止 Worker 阻塞
}
