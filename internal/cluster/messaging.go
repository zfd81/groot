// internal/cluster/messaging.go
package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

const (
	defaultPriority   = 5                   // 未指定时的优先级
	defaultMessageTTL = time.Hour           // 未指定 ExpiresAt 时的存活时长
	maxPayloadBytes   = 64 * 1024           // payload JSON 序列化后的最大字节数
	pollBatchSize     = 10                  // 单次轮询最多处理的消息数
	handlerTimeout    = 30 * time.Second    // 单条消息处理器的超时
	messageRetention  = 30 * 24 * time.Hour // 消息保留期，超过即被 Leader 清理
)

var (
	ErrMessageTypeRequired  = errors.New("集群消息: message type 不能为空")
	ErrTargetModuleRequired = errors.New("集群消息: target module 不能为空")
	ErrExpiresInPast        = errors.New("集群消息: expires_at 必须晚于当前时间")
	ErrPayloadTooLarge      = fmt.Errorf("集群消息: payload 超过 %d 字节", maxPayloadBytes)
)

// MessageService 负责集群消息的发送、处理器注册、轮询处理和清理。
// 它是纯内部 API：只由 Groot 代码调用，不暴露 HTTP 端点。
type MessageService struct {
	repo   repo.MessageRepo
	log    *logger.Logger
	selfID func() string // 返回当前实例的 reg_id；未注册时返回空串

	mu       sync.RWMutex
	handlers map[string]MessageHandler
}

// NewMessageService 创建消息服务。selfID 通常传 Cluster.RegID。
func NewMessageService(msgRepo repo.MessageRepo, log *logger.Logger, selfID func() string) *MessageService {
	return &MessageService{
		repo:     msgRepo,
		log:      log,
		selfID:   selfID,
		handlers: make(map[string]MessageHandler),
	}
}

// RegisterHandler 注册模块处理器；同名模块后注册者覆盖先注册者。
func (s *MessageService) RegisterHandler(module string, h MessageHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[module] = h
}

func (s *MessageService) handler(module string) MessageHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handlers[module]
}

// SendMessage 校验并写入一条消息。数据库写入失败时立即重试一次，
// 第二次仍失败则返回错误给调用方，由调用方决定如何处理。
func (s *MessageService) SendMessage(ctx context.Context, msg Message) error {
	now := time.Now()
	if msg.Type == "" {
		return ErrMessageTypeRequired
	}
	if msg.TargetModule == "" {
		return ErrTargetModuleRequired
	}
	if msg.Priority == 0 {
		msg.Priority = defaultPriority
	}
	if msg.ExpiresAt.IsZero() {
		msg.ExpiresAt = now.Add(defaultMessageTTL)
	}
	if !msg.ExpiresAt.After(now) {
		return ErrExpiresInPast
	}
	msg.CreatedAt = now
	msg.SourceInstance = s.selfID()

	rm, err := toRepoMessage(msg)
	if err != nil {
		return err
	}
	if len(rm.Payload) > maxPayloadBytes {
		return ErrPayloadTooLarge
	}

	if err := s.repo.Insert(ctx, rm); err != nil {
		s.log.Warn("集群消息写入失败,立即重试",
			zap.String("type", msg.Type), zap.Error(err))
		if err = s.repo.Insert(ctx, rm); err != nil {
			return fmt.Errorf("集群消息发送失败: %w", err)
		}
	}

	s.log.Info("集群消息已发送",
		zap.String("type", msg.Type),
		zap.String("target", msg.TargetInstance),
		zap.String("module", msg.TargetModule),
		zap.Int("priority", msg.Priority),
	)
	return nil
}

// Cleanup 删除超过保留期（30 天）的消息及其孤立消费记录。只应由 Leader 调用。
func (s *MessageService) Cleanup(ctx context.Context) (deletedMessages, deletedConsumers int, err error) {
	return s.repo.DeleteBefore(ctx, time.Now().Add(-messageRetention))
}
