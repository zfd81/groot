package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
)

// taskExecutor runs an agent task within a session. *agent.Executor satisfies it.
type taskExecutor interface {
	Execute(ctx context.Context, sessionID string, task *agent.Task, sse *agent.SSEWriter)
}

// Runner executes scheduled tasks
type Runner struct {
	executor  taskExecutor
	memoryMgr *memory.Manager
	storage   *Storage
	log       *logger.Logger
}

// NewRunner creates a new task runner
func NewRunner(
	exec *agent.Executor,
	mem *memory.Manager,
	storage *Storage,
	log *logger.Logger,
) *Runner {
	return &Runner{
		executor:  exec,
		memoryMgr: mem,
		storage:   storage,
		log:       log,
	}
}

// Run executes a task. Implements gocron task signature.
func (r *Runner) Run(taskID string) func() {
	return func() {
		r.log.Info("开始执行定时任务", zap.String("task_id", taskID))

		task, err := r.storage.LoadTask(taskID)
		if err != nil {
			r.log.Error("加载任务失败", zap.String("task_id", taskID), zap.Error(err))
			return
		}

		startTime := time.Now()
		sessionID := fmt.Sprintf("%s-%s-sched", task.ID, startTime.Format("20060102T150405"))

		// Create session
		if err := r.memoryMgr.CreateSession(sessionID, ""); err != nil {
			r.log.Error("创建 session 失败", zap.String("session_id", sessionID), zap.Error(err))
			return
		}

		triggerType := r.detectTriggerType(task.Schedule)
		r.log.Info("执行定时任务",
			zap.String("task_id", task.ID),
			zap.String("name", task.Name),
			zap.String("session_id", sessionID),
			zap.String("trigger", triggerType),
		)

		// Build agent task
		agentTask := &agent.Task{
			ID:          task.ID,
			Instruction: task.TaskDef.Instruction,
			Prompt:      task.TaskDef.SystemPrompt,
			ModelName:   task.TaskDef.Model,
			Caller:      "schedule",
		}

		// Execute
		r.executor.Execute(context.Background(), sessionID, agentTask, nil)

		durationMs := time.Since(startTime).Milliseconds()

		// Determine status
		status := string(agentTask.Status)
		if status == "" {
			status = "completed"
		}
		stepCount := len(agentTask.Steps)

		// Build execution record
		record := &ExecutionRecord{
			TaskID:      task.ID,
			StartedAt:   startTime,
			TriggerType: triggerType,
			SessionID:   sessionID,
			ChatID:      startTime.Format("20060102150405"),
			Status:      status,
			DurationMs:  durationMs,
			StepCount:   stepCount,
		}

		if agentTask.Error != nil {
			record.Error = agentTask.Error.Message
		}

		// Save execution record
		if err := r.storage.SaveExecution(task.ID, record); err != nil {
			r.log.Error("保存执行记录失败", zap.String("task_id", task.ID), zap.Error(err))
		}

		// Notification round
		r.notify(task, sessionID, status, agentTask.Result, record.Error, startTime, time.Duration(durationMs)*time.Millisecond)

		// One-shot task auto-archive
		if ParseScheduleType(task.Schedule) == ScheduleTypeOnce && status == "completed" {
			if err := r.storage.MoveTask(task.ID, "active", "archive"); err != nil {
				r.log.Error("归档一次性任务失败", zap.String("task_id", task.ID), zap.Error(err))
			} else {
				r.log.Info("一次性任务已归档", zap.String("task_id", task.ID), zap.String("name", task.Name))
			}
		}

		r.log.Info("定时任务执行完成",
			zap.String("task_id", task.ID),
			zap.String("status", status),
			zap.Int64("duration_ms", durationMs),
			zap.Int("steps", stepCount),
		)
	}
}

// RunImmediate executes a task immediately (for rerun)
func (r *Runner) RunImmediate(task *Task) error {
	startTime := time.Now()
	sessionID := fmt.Sprintf("%s-%s-sched", task.ID, startTime.Format("20060102T150405"))

	if err := r.memoryMgr.CreateSession(sessionID, ""); err != nil {
		return err
	}

	agentTask := &agent.Task{
		ID:          task.ID,
		Instruction: task.TaskDef.Instruction,
		Prompt:      task.TaskDef.SystemPrompt,
		ModelName:   task.TaskDef.Model,
		Caller:      "schedule",
	}

	r.executor.Execute(context.Background(), sessionID, agentTask, nil)

	durationMs := time.Since(startTime).Milliseconds()
	status := string(agentTask.Status)
	if status == "" {
		status = "completed"
	}

	record := &ExecutionRecord{
		TaskID:      task.ID,
		StartedAt:   startTime,
		TriggerType: "manual",
		SessionID:   sessionID,
		Status:      status,
		DurationMs:  durationMs,
		StepCount:   len(agentTask.Steps),
	}

	if agentTask.Error != nil {
		record.Error = agentTask.Error.Message
	}

	if err := r.storage.SaveExecution(task.ID, record); err != nil {
		r.log.Error("保存执行记录失败", zap.String("task_id", task.ID), zap.Error(err))
	}

	r.notify(task, sessionID, status, agentTask.Result, record.Error, startTime, time.Duration(durationMs)*time.Millisecond)
	return nil
}

// NewTask creates a gocron.Task wrapper for this task
func (r *Runner) NewTask(taskID string) gocron.Task {
	return gocron.NewTask(r.Run(taskID))
}

func (r *Runner) detectTriggerType(schedule string) string {
	switch ParseScheduleType(schedule) {
	case ScheduleTypeCron:
		return "cron"
	case ScheduleTypeOnce:
		return "once"
	case ScheduleTypeInterval:
		return "interval"
	default:
		return "cron"
	}
}
