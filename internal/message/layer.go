package message

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// registration 把一个渠道的发送器实例与其配置绑在一起，
// 保证两者总是被原子地一起替换或删除。
type registration struct {
	sender Sender
	cfg    config.SenderConf
}

// Layer is the message notification layer
//
// senders 可在运行期被替换（设置面板改完即生效），由 mu 保护。
// 取发送器与投递分成两步：先在锁内拿到实例，
// 再在锁外调用 Send——发送是网络操作，持锁会把整个消息层卡住。
type Layer struct {
	mu        sync.RWMutex
	queue     chan *sendJob
	queueSize int
	senders   map[string]registration
	workers   int
	stopCh    chan struct{}
	wg        sync.WaitGroup
	log       *logger.Logger
}

// NewLayer creates a new message layer
func NewLayer(cfg config.MessageConfig, log *logger.Logger) *Layer {
	return &Layer{
		queue:     make(chan *sendJob, cfg.QueueSize),
		queueSize: cfg.QueueSize,
		senders:   make(map[string]registration),
		workers:   cfg.Workers,
		stopCh:    make(chan struct{}),
		log:       log,
	}
}

// Register registers a sender with its config.
// 启动路径使用；运行期改动走 SetSender。
func (l *Layer) Register(name string, sender Sender, cfg config.SenderConf) {
	l.SetSender(name, sender, cfg)
}

// SetSender 注册或替换一个发送器，随后开始处理的消息即走新实例。
// 正在投递中的消息握着旧实例的指针，按旧配置发完——
// 中途换掉会让一次已开始的网络请求结果无处归属。
func (l *Layer) SetSender(name string, sender Sender, cfg config.SenderConf) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.senders[name] = registration{sender: sender, cfg: cfg}
}

// RemoveSender 注销一个发送器，此后该渠道被视为不可用。
func (l *Layer) RemoveSender(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.senders, name)
}

// resolve 在锁内取出一个可用渠道的发送器实例。
// 第二个返回值为 false 表示未注册或已禁用。
func (l *Layer) resolve(name string) (Sender, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	r, ok := l.senders[name]
	if !ok || !r.cfg.Enabled {
		return nil, false
	}
	return r.sender, true
}

// ChannelEnabled 报告某渠道当前是否可投递：已注册且已启用。
func (l *Layer) ChannelEnabled(name string) bool {
	_, ok := l.resolve(name)
	return ok
}

// Start launches the worker goroutine pool
func (l *Layer) Start() {
	for i := 0; i < l.workers; i++ {
		l.wg.Add(1)
		go l.worker()
	}
	l.log.Info("消息层已启动", zap.Int("workers", l.workers), zap.Int("queue_size", l.queueSize))
}

// Publish publishes an event to specified channels asynchronously.
// Returns a future channel to get send results.
func (l *Layer) Publish(ctx context.Context, event Event, channels []string) (<-chan []SendResult, error) {
	if len(channels) == 0 {
		ch := make(chan []SendResult)
		close(ch)
		return ch, nil
	}

	job := &sendJob{
		ctx:      ctx,
		event:    event,
		channels: channels,
		resultCh: make(chan []SendResult, 1),
	}

	// 优先检查 context 是否已取消，避免 select 随机选择入队成功
	if err := ctx.Err(); err != nil {
		l.log.Info("消息入队失败: context已取消", zap.String("title", event.Title))
		return nil, err
	}

	select {
	case l.queue <- job:
		l.log.Info("消息入队",
			zap.String("title", event.Title),
			zap.Strings("channels", channels),
		)
		return job.resultCh, nil
	case <-ctx.Done():
		l.log.Info("消息入队失败: context已取消", zap.String("title", event.Title))
		return nil, ctx.Err()
	default:
		l.log.Info("消息入队失败: 队列已满",
			zap.String("title", event.Title),
			zap.Int("queue_size", l.queueSize),
		)
		return nil, ErrQueueFull
	}
}

// Stop gracefully stops the layer
func (l *Layer) Stop() {
	close(l.stopCh)
	l.wg.Wait()
	l.log.Info("消息层已停止")
}

// worker is the consumer loop
func (l *Layer) worker() {
	defer l.wg.Done()
	for {
		select {
		case job := <-l.queue:
			l.processJob(job)
		case <-l.stopCh:
			return
		}
	}
}

// processJob filters channels, sends concurrently, and writes results
func (l *Layer) processJob(job *sendJob) {
	defer func() {
		if r := recover(); r != nil {
			l.log.Error("Worker panic", zap.Any("panic", r))
		}
	}()

	var enabledChannels []string
	var targets []Sender
	for _, name := range job.channels {
		if s, ok := l.resolve(name); ok {
			enabledChannels = append(enabledChannels, name)
			targets = append(targets, s)
		}
	}

	if len(enabledChannels) == 0 {
		l.log.Debug("无可用渠道，跳过发送",
			zap.String("title", job.event.Title),
			zap.Strings("requested", job.channels),
		)
		job.resultCh <- []SendResult{}
		return
	}

	ctx, cancel := context.WithTimeout(job.ctx, 10*time.Second)
	defer cancel()

	results := make([]SendResult, len(enabledChannels))
	var wg sync.WaitGroup
	for i, name := range enabledChannels {
		wg.Add(1)
		go func(idx int, channelName string, sender Sender) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[idx] = SendResult{
						Channel: channelName,
						Success: false,
						Message: fmt.Sprintf("panic: %v", r),
					}
					l.log.Error("Sender panic",
						zap.String("channel", channelName),
						zap.String("title", job.event.Title),
						zap.Any("panic", r),
					)
				}
			}()
			results[idx] = sender.Send(ctx, job.event)
		}(i, name, targets[i])
	}
	wg.Wait()

	for _, r := range results {
		if r.Success {
			l.log.Info("消息发送成功",
				zap.String("channel", r.Channel),
				zap.String("title", job.event.Title),
				zap.Time("sent_at", r.Timestamp),
			)
		} else {
			l.log.Error("消息发送失败",
				zap.String("channel", r.Channel),
				zap.String("title", job.event.Title),
				zap.String("reason", r.Message),
			)
		}
	}

	job.resultCh <- results
}
