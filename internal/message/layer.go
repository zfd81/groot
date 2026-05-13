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

// Layer is the message notification layer
type Layer struct {
	queue         chan *sendJob
	queueSize     int
	senders       map[string]Sender
	senderConfigs map[string]config.SenderConf
	workers       int
	stopCh        chan struct{}
	wg            sync.WaitGroup
	log           *logger.Logger
}

// NewLayer creates a new message layer
func NewLayer(cfg config.MessageConfig, log *logger.Logger) *Layer {
	return &Layer{
		queue:         make(chan *sendJob, cfg.QueueSize),
		queueSize:     cfg.QueueSize,
		senders:       make(map[string]Sender),
		senderConfigs: make(map[string]config.SenderConf),
		workers:       cfg.Workers,
		stopCh:        make(chan struct{}),
		log:           log,
	}
}

// Register registers a sender with its config
func (l *Layer) Register(name string, sender Sender, cfg config.SenderConf) {
	l.senders[name] = sender
	l.senderConfigs[name] = cfg
}

// isSenderEnabled checks if a channel is available: registered and enabled
func (l *Layer) isSenderEnabled(name string) bool {
	cfg, ok := l.senderConfigs[name]
	if !ok {
		return false
	}
	if !cfg.Enabled {
		return false
	}
	_, registered := l.senders[name]
	return registered
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
	for _, name := range job.channels {
		if l.isSenderEnabled(name) {
			enabledChannels = append(enabledChannels, name)
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
		go func(idx int, channelName string) {
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
			results[idx] = l.senders[channelName].Send(ctx, job.event)
		}(i, name)
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
