package senders

import (
	"context"
	"fmt"
	"time"

	"github.com/zfd81/groot/internal/message"
)

// StdoutSender writes events to stdout for debugging
type StdoutSender struct{}

// NewStdout creates a new stdout sender
func NewStdout() *StdoutSender {
	return &StdoutSender{}
}

// Name returns the sender name
func (s *StdoutSender) Name() string {
	return "stdout"
}

// Send writes the event to stdout
func (s *StdoutSender) Send(ctx context.Context, event message.Event) message.SendResult {
	fmt.Printf("[消息] %s | %s\n  %s\n", event.Time.Format(time.RFC3339), event.Title, event.Content)
	return message.SendResult{
		Channel:   "stdout",
		Success:   true,
		Message:   "已输出",
		Timestamp: time.Now(),
	}
}
