package message

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// mockSender implements Sender for testing
type mockSender struct {
	name     string
	delay    time.Duration
	shouldFail bool
}

func (m *mockSender) Name() string {
	return m.name
}

func (m *mockSender) Send(ctx context.Context, event Event) SendResult {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return SendResult{
				Channel:   m.name,
				Success:   false,
				Message:   ctx.Err().Error(),
				Timestamp: time.Now(),
			}
		}
	}
	if m.shouldFail {
		return SendResult{
			Channel:   m.name,
			Success:   false,
			Message:   "mock failure",
			Timestamp: time.Now(),
		}
	}
	return SendResult{
		Channel:   m.name,
		Success:   true,
		Message:   "ok",
		Timestamp: time.Now(),
	}
}

func newTestLayer(queueSize, workers int) *Layer {
	log := logger.New(config.LoggingConfig{
		Level:  "info",
		Format: "text",
		Output: []string{"stdout"},
	})
	return &Layer{
		queue:         make(chan *sendJob, queueSize),
		queueSize:     queueSize,
		senders:       make(map[string]Sender),
		senderConfigs: make(map[string]config.SenderConf),
		workers:       workers,
		stopCh:        make(chan struct{}),
		log:           log,
	}
}

func TestPublishSuccess(t *testing.T) {
	l := newTestLayer(10, 1)
	l.Register("test", &mockSender{name: "test"}, config.SenderConf{Enabled: true})
	l.Start()
	defer l.Stop()

	ctx := context.Background()
	event := Event{Type: "test", Title: "test_title", Time: time.Now()}

	resultCh, err := l.Publish(ctx, event, []string{"test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case results := <-resultCh:
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if !results[0].Success {
			t.Fatalf("expected success, got: %s", results[0].Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestPublishEmptyChannels(t *testing.T) {
	l := newTestLayer(10, 1)

	ctx := context.Background()
	event := Event{Type: "test", Title: "test_title", Time: time.Now()}

	resultCh, err := l.Publish(ctx, event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should get empty slice immediately
	select {
	case results := <-resultCh:
		if len(results) != 0 {
			t.Fatalf("expected 0 results, got %d", len(results))
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestPublishQueueFull(t *testing.T) {
	l := newTestLayer(1, 0) // queue size 1, no workers

	ctx := context.Background()
	event := Event{Type: "test", Title: "test_title", Time: time.Now()}

	// Fill the queue
	_, err := l.Publish(ctx, event, []string{"test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second publish should fail
	_, err = l.Publish(ctx, event, []string{"test"})
	if err != ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got: %v", err)
	}
}

func TestChannelFiltering(t *testing.T) {
	l := newTestLayer(10, 1)
	// Register but disabled
	l.Register("disabled", &mockSender{name: "disabled"}, config.SenderConf{Enabled: false})
	// Not registered at all - "unregistered"

	// isSenderEnabled checks
	if l.isSenderEnabled("disabled") {
		t.Fatal("disabled sender should not be enabled")
	}
	if l.isSenderEnabled("unregistered") {
		t.Fatal("unregistered sender should not be enabled")
	}
}

func TestPublishContextCancel(t *testing.T) {
	l := newTestLayer(10, 0) // no workers, so queue is never consumed

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	event := Event{Type: "test", Title: "test_title", Time: time.Now()}
	_, err := l.Publish(ctx, event, []string{"test"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSenderPanicRecovery(t *testing.T) {
	l := newTestLayer(10, 1)
	// Use a sender that panics
	l.Register("panic", &panicSender{name: "panic"}, config.SenderConf{Enabled: true})
	l.Register("ok", &mockSender{name: "ok"}, config.SenderConf{Enabled: true})
	l.Start()
	defer l.Stop()

	ctx := context.Background()
	event := Event{Type: "test", Title: "test_title", Time: time.Now()}

	resultCh, err := l.Publish(ctx, event, []string{"panic", "ok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case results := <-resultCh:
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		// panic sender should fail
		if results[0].Success {
			t.Fatal("panic sender should fail")
		}
		// ok sender should succeed
		if !results[1].Success {
			t.Fatal("ok sender should succeed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

// panicSender panics in Send
type panicSender struct {
	name string
}

func (p *panicSender) Name() string {
	return p.name
}

func (p *panicSender) Send(ctx context.Context, event Event) SendResult {
	panic("intentional panic")
}
