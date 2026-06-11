package schedule

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by ScheduleRepo when a record does not exist.
var ErrNotFound = errors.New("schedule: not found")

// ErrConflict is returned by ScheduleRepo on an optimistic-lock version conflict.
var ErrConflict = errors.New("schedule: version conflict")

// TaskStatus is the lifecycle state of a scheduled task.
type TaskStatus = string

const (
	TaskStatusActive   TaskStatus = "active"
	TaskStatusDisabled TaskStatus = "disabled"
	TaskStatusArchive  TaskStatus = "archive"
)

// ScheduleRepo is the persistence interface for scheduled tasks and executions.
type ScheduleRepo interface {
	SaveTask(ctx context.Context, task *Task) error
	LoadTask(ctx context.Context, taskID string) (*Task, error)
	ListByStatus(ctx context.Context, status TaskStatus) ([]*Task, error)
	DueTasks(ctx context.Context, now time.Time) ([]*Task, error)
	UpdateNextRun(ctx context.Context, taskID string, nextRunAt, lastRunAt time.Time, version int64) error
	MoveStatus(ctx context.Context, taskID string, newStatus TaskStatus, version int64) error
	DeleteTask(ctx context.Context, taskID string) error
	SaveExecution(ctx context.Context, rec *ExecutionRecord) error
	CompleteExecution(ctx context.Context, rec *ExecutionRecord, nextRunAt, lastRunAt time.Time, version int64) error
	ListExecutions(ctx context.Context, taskID string, limit int) ([]*ExecutionRecord, error)
}
