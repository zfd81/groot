package memory

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/logger"
)

// CleanupScheduler 定时清理调度器
type CleanupScheduler struct {
	manager  *Manager
	schedule string // HH:MM 格式，如 "02:00"
	stopCh   chan struct{}
	log      *logger.Logger
}

// NewCleanupScheduler 创建清理调度器
func NewCleanupScheduler(manager *Manager, schedule string, log *logger.Logger) *CleanupScheduler {
	return &CleanupScheduler{
		manager:  manager,
		schedule: schedule,
		stopCh:   make(chan struct{}),
		log:      log,
	}
}

// Start 启动清理调度器
func (s *CleanupScheduler) Start() {
	// 解析清理时间
	parts := strings.Split(s.schedule, ":")
	if len(parts) != 2 {
		s.log.Error("无效的清理时间格式: " + s.schedule)
		return
	}

	hour, _ := strconv.Atoi(parts[0])
	minute, _ := strconv.Atoi(parts[1])

	// 计算下一次清理时间
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}

	s.log.Info("清理调度器已启动, 下次清理时间: " + next.Format("2006-01-02 15:04:05"))

	go s.runScheduler(next)
}

// runScheduler 运行调度循环
func (s *CleanupScheduler) runScheduler(next time.Time) {
	for {
		select {
		case <-s.stopCh:
			s.log.Info("清理调度器已停止")
			return
		case <-time.After(time.Until(next)):
			// 执行清理
			s.log.Info("开始执行清理任务")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			deleted, err := s.manager.Cleanup(ctx)
			cancel()

			if err != nil {
				s.log.Error("清理任务失败: " + err.Error())
			} else {
				s.log.Info(fmt.Sprintf("清理任务完成, 删除 %d 个会话", deleted))
			}

			// 计算下一次清理时间（24小时后）
			next = next.Add(24 * time.Hour)
		}
	}
}

// Stop 停止清理调度器
func (s *CleanupScheduler) Stop() {
	close(s.stopCh)
}