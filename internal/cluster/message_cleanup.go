// internal/cluster/message_cleanup.go
package cluster

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// NewMessageCleanupTask 返回可交给 gocron 的清理函数：删除保留期（30 天）之前的
// 集群消息及其孤立消费记录。只应注册在 Leader 的调度器上。
func NewMessageCleanupTask(ms *MessageService, log *logger.Logger) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		dm, dc, err := ms.Cleanup(ctx)
		if err != nil {
			log.Error("集群消息清理失败", zap.Error(err))
			return
		}
		log.Info("集群消息清理完成",
			zap.Int("deleted_messages", dm),
			zap.Int("deleted_consumers", dc))
	}
}
