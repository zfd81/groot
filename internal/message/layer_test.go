package message

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// mockSender implements Sender for testing
type mockSender struct {
	name       string
	delay      time.Duration
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
		queue:     make(chan *sendJob, queueSize),
		queueSize: queueSize,
		senders:   make(map[string]registration),
		workers:   workers,
		stopCh:    make(chan struct{}),
		log:       log,
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

	// resolve checks
	if _, ok := l.resolve("disabled"); ok {
		t.Fatal("disabled sender should not be enabled")
	}
	if _, ok := l.resolve("unregistered"); ok {
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

// TestLayer_SetSenderReplacesInPlace 验证替换发送器后新消息走新实例。
func TestLayer_SetSenderReplacesInPlace(t *testing.T) {
	l := NewLayer(config.MessageConfig{QueueSize: 4, Workers: 1}, logger.NewNop())
	first := &countingSender{name: "webhook"}
	second := &countingSender{name: "webhook"}
	l.SetSender("webhook", first, config.SenderConf{Enabled: true})
	l.Start()
	defer l.Stop()

	publishAndWait(t, l, "webhook")
	if first.count() != 1 {
		t.Fatalf("first sender count = %d, want 1", first.count())
	}

	l.SetSender("webhook", second, config.SenderConf{Enabled: true})
	publishAndWait(t, l, "webhook")
	if second.count() != 1 {
		t.Errorf("replacement sender count = %d, want 1", second.count())
	}
	if first.count() != 1 {
		t.Errorf("old sender count = %d, 替换后不应再收到消息", first.count())
	}
}

// TestLayer_RemoveSenderStopsDelivery 验证注销后该渠道不再投递。
func TestLayer_RemoveSenderStopsDelivery(t *testing.T) {
	l := NewLayer(config.MessageConfig{QueueSize: 4, Workers: 1}, logger.NewNop())
	s := &countingSender{name: "webhook"}
	l.SetSender("webhook", s, config.SenderConf{Enabled: true})
	l.Start()
	defer l.Stop()

	l.RemoveSender("webhook")
	results := publishCollect(t, l, "webhook")
	if len(results) != 0 {
		t.Errorf("注销后应无可用渠道，得到 %d 条结果", len(results))
	}
	if s.count() != 0 {
		t.Errorf("已注销的发送器仍被调用 %d 次", s.count())
	}
}

// TestLayer_SetSenderDisabledSkipsDelivery 验证 Enabled=false 的渠道不投递，
// 但仍留在注册表里：界面上把它关掉再打开不需要重启。
func TestLayer_SetSenderDisabledSkipsDelivery(t *testing.T) {
	l := NewLayer(config.MessageConfig{QueueSize: 4, Workers: 1}, logger.NewNop())
	s := &countingSender{name: "webhook"}
	l.SetSender("webhook", s, config.SenderConf{Enabled: false})
	l.Start()
	defer l.Stop()

	results := publishCollect(t, l, "webhook")
	if len(results) != 0 {
		t.Errorf("已禁用渠道不应投递，得到 %d 条结果", len(results))
	}

	l.SetSender("webhook", s, config.SenderConf{Enabled: true})
	publishAndWait(t, l, "webhook")
	if s.count() != 1 {
		t.Errorf("重新启用后应投递一次，实际 %d 次", s.count())
	}
}

// TestLayer_SetSenderConcurrentWithDelivery 在投递过程中反复换发送器，
// 由 -race 判定读写是否有竞争。50 条消息一次性发出不等待，
// 每发 5 条换一次发送器，让 worker 的 resolve 与 SetSender 充分重叠。
func TestLayer_SetSenderConcurrentWithDelivery(t *testing.T) {
	l := NewLayer(config.MessageConfig{QueueSize: 64, Workers: 4}, logger.NewNop())
	l.SetSender("webhook", &countingSender{name: "webhook"}, config.SenderConf{Enabled: true})
	l.Start()
	defer l.Stop()

	const total = 50
	pending := make([]<-chan []SendResult, 0, total)
	for i := 0; i < total; i++ {
		if i%5 == 0 {
			l.SetSender("webhook", &countingSender{name: "webhook"},
				config.SenderConf{Enabled: true})
		}
		ch, err := l.Publish(context.Background(), Event{Title: "t"}, []string{"webhook"})
		if err != nil {
			t.Fatalf("publish #%d: %v", i, err)
		}
		pending = append(pending, ch)
	}

	for i, ch := range pending {
		select {
		case results := <-ch:
			if len(results) != 1 || !results[0].Success {
				t.Errorf("消息 #%d 结果异常: %+v", i, results)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("等待消息 #%d 结果超时", i)
		}
	}
}

// countingSender 记录被调用次数的测试发送器。
type countingSender struct {
	name string
	mu   sync.Mutex
	n    int
}

func (s *countingSender) Name() string { return s.name }

func (s *countingSender) Send(_ context.Context, _ Event) SendResult {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return SendResult{Channel: s.name, Success: true, Timestamp: time.Now()}
}

func (s *countingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// publishCollect 发一条消息并取回结果，入队失败或超时即失败。
func publishCollect(t *testing.T, l *Layer, channel string) []SendResult {
	t.Helper()
	ch, err := l.Publish(context.Background(), Event{Title: "t"}, []string{channel})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case results := <-ch:
		return results
	case <-time.After(2 * time.Second):
		t.Fatal("等待发送结果超时")
		return nil
	}
}

// publishAndWait 发一条消息并要求全部渠道成功。
func publishAndWait(t *testing.T, l *Layer, channel string) {
	t.Helper()
	results := publishCollect(t, l, channel)
	if len(results) != 1 {
		t.Fatalf("结果条数 = %d, want 1", len(results))
	}
	if !results[0].Success {
		t.Fatalf("发送失败: %s", results[0].Message)
	}
}
