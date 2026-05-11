package schedule

import (
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// SyncTask creates a task that syncs the active/ directory with gocron
func SyncTask(engine *Engine, storage *Storage, log *logger.Logger) func() {
	return func() {
		log.Debug("同步检查开始")

		// Get tasks from active/ directory
		activeTasks, err := storage.ListActiveTasks()
		if err != nil {
			log.Error("同步检查失败: 无法读取 active/ 目录", zap.Error(err))
			return
		}

		activeIDs := make(map[string]bool)
		for _, t := range activeTasks {
			activeIDs[t.ID] = true
		}

		// Re-register any active tasks that might have been missed
		// (e.g., manually added files while scheduler was running)
		for _, task := range activeTasks {
			if err := engine.Register(task); err != nil {
				log.Info("同步注册任务失败",
					zap.String("task_id", task.ID),
					zap.Error(err),
				)
			}
		}

		log.Debug("同步检查完成",
			zap.Int("active_tasks", len(activeIDs)),
		)
	}
}

// NewSyncTask creates a gocron-compatible sync task
func NewSyncTask(engine *Engine, storage *Storage, log *logger.Logger) func() {
	return SyncTask(engine, storage, log)
}

// ParseCleanupTime parses HH:MM format and returns hour, minute
func ParseCleanupTime(schedule string) (int, int) {
	hour, minute := 2, 0 // default 02:00
	if len(schedule) == 5 && schedule[2] == ':' {
		h := 0
		m := 0
		for i, c := range schedule {
			if i == 0 {
				h = h*10 + int(c-'0')
			} else if i == 1 {
				h = h*10 + int(c-'0')
			} else if i == 3 {
				m = m*10 + int(c-'0')
			} else if i == 4 {
				m = m*10 + int(c-'0')
			}
		}
		if h >= 0 && h < 24 && m >= 0 && m < 60 {
			return h, m
		}
	}
	return hour, minute
}
