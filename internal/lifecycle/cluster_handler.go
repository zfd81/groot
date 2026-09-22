package lifecycle

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/logger"
)

const (
	// ModuleName 是本处理器在集群消息服务中注册的模块名（TargetModule）。
	ModuleName = "lifecycle"
	// MessageTypeRestart 是重启指令的消息类型。
	MessageTypeRestart = "restart"
	// defaultRestartDelay 处理器返回成功后到真正触发重启的间隔：留给消费记录落库。
	defaultRestartDelay = time.Second
)

// ClusterHandler 处理集群消息模块 lifecycle 的指令。
// 它实现 cluster.MessageHandler。
type ClusterHandler struct {
	ctrl      *Controller
	startedAt time.Time
	delay     time.Duration
	log       *logger.Logger
}

// NewClusterHandler 创建处理器。startedAt 是本进程启动时间，早于它的消息被忽略。
func NewClusterHandler(ctrl *Controller, startedAt time.Time, log *logger.Logger) *ClusterHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &ClusterHandler{ctrl: ctrl, startedAt: startedAt, delay: defaultRestartDelay, log: log}
}

// Handle 实现 cluster.MessageHandler。
// 成功返回 nil 的含义是"已受理"：重启在 delay 之后异步触发，让调用方先把消费记录写入数据库。
func (h *ClusterHandler) Handle(ctx context.Context, msg cluster.Message) error {
	if msg.Type != MessageTypeRestart {
		return fmt.Errorf("lifecycle: 不支持的消息类型 %q", msg.Type)
	}
	if msg.CreatedAt.Before(h.startedAt) {
		// 至少一次语义下的重放保护（仅作兜底）：早于本进程启动的指令视为已由上一代进程处理。
		// CreatedAt 来自发送方时钟，跨主机偏差可能把一条新指令误判为过期，因此用 Warn 留痕。
		// 点对点重启的主要防重放依赖 reg_id 在重启后变化，见设计文档 1.5.4 节。
		h.log.Warn("忽略早于本进程启动的重启指令",
			zap.Int64("msg_id", msg.ID),
			zap.Time("created_at", msg.CreatedAt),
			zap.Time("started_at", h.startedAt))
		return nil
	}
	if !h.ctrl.RestartSupported() {
		return ErrRestartUnsupported
	}
	h.log.Info("收到重启指令",
		zap.Int64("msg_id", msg.ID),
		zap.String("source", msg.SourceInstance),
		zap.Any("payload", msg.Payload),
		zap.Duration("delay", h.delay))
	time.AfterFunc(h.delay, func() {
		if err := h.ctrl.RequestRestart(); err != nil {
			h.log.Error("触发重启失败", zap.Error(err))
		}
	})
	return nil
}
