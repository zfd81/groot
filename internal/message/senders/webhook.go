package senders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/zfd81/groot/internal/message"
)

// WebhookSender sends events via HTTP POST JSON
type WebhookSender struct {
	url    string
	client *http.Client
}

// NewWebhook creates a new webhook sender
func NewWebhook(url string) *WebhookSender {
	return &WebhookSender{
		url: url,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Name returns the sender name
func (s *WebhookSender) Name() string {
	return "webhook"
}

// Send sends the event as JSON via HTTP POST
func (s *WebhookSender) Send(ctx context.Context, event message.Event) message.SendResult {
	body, err := json.Marshal(event)
	if err != nil {
		return message.SendResult{
			Channel:   "webhook",
			Success:   false,
			Message:   fmt.Sprintf("序列化失败: %v", err),
			Timestamp: time.Now(),
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return message.SendResult{
			Channel:   "webhook",
			Success:   false,
			Message:   fmt.Sprintf("创建请求失败: %v", err),
			Timestamp: time.Now(),
		}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return message.SendResult{
			Channel:   "webhook",
			Success:   false,
			Message:   fmt.Sprintf("发送失败: %v", err),
			Timestamp: time.Now(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return message.SendResult{
			Channel:   "webhook",
			Success:   true,
			Message:   fmt.Sprintf("HTTP %d", resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return message.SendResult{
		Channel:   "webhook",
		Success:   false,
		Message:   fmt.Sprintf("HTTP %d", resp.StatusCode),
		Timestamp: time.Now(),
	}
}
