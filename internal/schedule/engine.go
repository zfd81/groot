package schedule

import (
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/scheduler"
)

// Engine manages the scheduler and task registration
type Engine struct {
	scheduler *scheduler.Scheduler
	runner    *Runner
	storage   *Storage
	log       *logger.Logger
}

// NewEngine creates a new schedule engine
func NewEngine(sched *scheduler.Scheduler, runner *Runner, storage *Storage, log *logger.Logger) *Engine {
	return &Engine{
		scheduler: sched,
		runner:    runner,
		storage:   storage,
		log:       log,
	}
}

// Start loads all active tasks and registers them in the scheduler
func (e *Engine) Start() error {
	tasks, err := e.storage.ListActiveTasks()
	if err != nil {
		return err
	}

	for _, task := range tasks {
		if err := e.Register(task); err != nil {
			e.log.Info("注册任务失败，跳过", zap.String("task_id", task.ID), zap.Error(err))
			continue
		}
	}

	e.log.Info("调度引擎已启动", zap.Int("active_tasks", len(tasks)))
	return nil
}

// Register registers a task in the scheduler
func (e *Engine) Register(task *Task) error {
	taskFn := e.runner.NewTask(task.ID)
	tags := []string{"user-task", task.ID}

	switch ParseScheduleType(task.Schedule) {
	case ScheduleTypeCron:
		return e.scheduler.AddCron(task.Schedule, taskFn, tags...)
	case ScheduleTypeOnce:
		t, err := time.Parse(time.RFC3339, task.Schedule)
		if err != nil {
			return err
		}
		return e.scheduler.AddOnce(t, taskFn, tags...)
	case ScheduleTypeInterval:
		d, err := time.ParseDuration(task.Schedule)
		if err != nil {
			return err
		}
		return e.scheduler.AddDuration(d, taskFn, tags...)
	}

	return nil
}

// Unregister removes a task from the scheduler
func (e *Engine) Unregister(taskID string) {
	e.scheduler.RemoveByTag(taskID)
}
