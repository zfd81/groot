// internal/cluster/message_poller.go
package cluster

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/repo"
)

// Poll 拉取本实例尚未消费的消息并逐条处理。同步执行；调用方负责并发控制。
func (s *MessageService) Poll(ctx context.Context) {
	instanceID := s.selfID()
	if instanceID == "" {
		s.log.Debug("实例尚未完成集群注册,跳过集群消息轮询")
		return
	}
	msgs, err := s.repo.ListPending(ctx, instanceID, time.Now(), pollBatchSize)
	if err != nil {
		s.log.Warn("集群消息轮询失败", zap.Error(err))
		return
	}
	if len(msgs) == 0 {
		return
	}
	s.log.Debug("集群消息轮询", zap.Int("pending_count", len(msgs)))
	for _, rm := range msgs {
		if ctx.Err() != nil {
			return
		}
		s.processMessage(ctx, instanceID, rm)
	}
}

// processMessage 路由并执行一条消息，然后把结果写入消费记录表。
// 处理失败不重试：记录 failed 状态与错误信息后继续下一条。
func (s *MessageService) processMessage(ctx context.Context, instanceID string, rm *repo.ClusterMessage) {
	msg, err := fromRepoMessage(rm)
	if err != nil {
		s.recordResult(ctx, instanceID, rm, err)
		return
	}
	h := s.handler(msg.TargetModule)
	if h == nil {
		s.recordResult(ctx, instanceID, rm, fmt.Errorf("模块 %q 未注册处理器", msg.TargetModule))
		return
	}
	hctx, cancel := context.WithTimeout(ctx, handlerTimeout)
	err = safeHandle(hctx, h, msg)
	cancel()
	s.recordResult(ctx, instanceID, rm, err)
}

// safeHandle 调用处理器并把 panic 转为 error，保证轮询循环不会被单个处理器击穿。
func safeHandle(ctx context.Context, h MessageHandler, msg Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("处理器 panic: %v", r)
		}
	}()
	return h.Handle(ctx, msg)
}

// recordResult 写入消费记录并打日志。写记录失败只打日志，消息会在下一轮再次被处理（至少一次语义）。
func (s *MessageService) recordResult(ctx context.Context, instanceID string, rm *repo.ClusterMessage, handleErr error) {
	c := &repo.MessageConsumer{
		MessageID:  rm.ID,
		InstanceID: instanceID,
		ConsumedAt: time.Now(),
		Status:     repo.ConsumeStatusSuccess,
	}
	if handleErr != nil {
		c.Status = repo.ConsumeStatusFailed
		c.ErrorMessage = handleErr.Error()
		s.log.Error("集群消息处理失败",
			zap.Int64("msg_id", rm.ID),
			zap.String("type", rm.Type),
			zap.String("module", rm.TargetModule),
			zap.Error(handleErr))
	} else {
		s.log.Info("集群消息处理成功",
			zap.Int64("msg_id", rm.ID),
			zap.String("type", rm.Type),
			zap.String("module", rm.TargetModule))
	}
	if err := s.repo.RecordConsumption(ctx, c); err != nil {
		s.log.Error("写入集群消息消费记录失败",
			zap.Int64("msg_id", rm.ID), zap.Error(err))
	}
}

// SetMessageService 把消息服务挂接到集群心跳循环。必须在 Join 之前调用。
func (c *Cluster) SetMessageService(ms *MessageService) {
	c.msg = ms
}

// MessageService 返回挂接的消息服务；未挂接时返回 nil。
func (c *Cluster) MessageService() *MessageService {
	return c.msg
}

// triggerPoll 在心跳 tick 中异步触发一轮消息轮询。
// 原子标志保证同一时刻只有一轮在跑：上一轮未结束时本轮直接跳过，
// 处理器耗时再长也不会阻塞心跳、不会并发重复处理。
func (c *Cluster) triggerPoll() {
	if c.msg == nil {
		return
	}
	if !atomic.CompareAndSwapInt32(&c.polling, 0, 1) {
		c.log.Debug("上一轮集群消息处理尚未结束,跳过本轮轮询")
		return
	}
	go func() {
		defer atomic.StoreInt32(&c.polling, 0)
		c.msg.Poll(c.ctx)
	}()
}
