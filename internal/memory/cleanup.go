package memory

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// NewCleanupTask creates a cleanup task function compatible with gocron.
// The scheduling lifecycle (start/stop/schedule) is managed by the scheduler module.
func NewCleanupTask(mgr *Manager, log *logger.Logger) func() {
	return func() {
		log.Info("开始执行清理任务")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		deleted, err := mgr.Cleanup(ctx)
		if err != nil {
			log.Error("清理任务失败", zap.Error(err))
		} else {
			log.Info("清理任务完成", zap.Int("deleted", deleted))
		}
	}
}
