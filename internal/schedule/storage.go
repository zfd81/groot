package schedule

import (
	"context"
	"errors"
	"fmt"

	"github.com/zfd81/groot/internal/logger"
)

// Storage wraps ScheduleRepo to preserve the internal API surface used by
// manager.go, engine.go, runner.go, and sync.go.
type Storage struct {
	repo ScheduleRepo
	log  *logger.Logger
}

// NewStorage creates a new Storage backed by a ScheduleRepo.
func NewStorage(r ScheduleRepo, log *logger.Logger) *Storage {
	return &Storage{repo: r, log: log}
}

// SaveTask creates or updates a task.
func (s *Storage) SaveTask(task *Task) error {
	return s.repo.SaveTask(context.Background(), task)
}

// LoadTask loads a task by ID.
func (s *Storage) LoadTask(taskID string) (*Task, error) {
	t, err := s.repo.LoadTask(context.Background(), taskID)
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("任务 %s 不存在", taskID)
	}
	return t, err
}

// ListActiveTasks returns all tasks with status "active".
func (s *Storage) ListActiveTasks() ([]*Task, error) {
	return s.repo.ListByStatus(context.Background(), TaskStatusActive)
}

// ListAllTasks returns tasks across all statuses.
func (s *Storage) ListAllTasks() ([]*Task, error) {
	var all []*Task
	for _, status := range []string{
		TaskStatusActive,
		TaskStatusDisabled,
		TaskStatusArchive,
	} {
		tasks, _ := s.listTasksIn(status)
		all = append(all, tasks...)
	}
	return all, nil
}

// listTasksIn lists tasks in a specific status.
// Used by manager.go (via List) and engine.go.
func (s *Storage) listTasksIn(status string) ([]*Task, error) {
	return s.repo.ListByStatus(context.Background(), status)
}

// MoveTask changes a task's status.
// The `from` parameter is accepted for API compatibility; the task must currently
// be in that status or an error is returned.
func (s *Storage) MoveTask(taskID, from, to string) error {
	task, err := s.repo.LoadTask(context.Background(), taskID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("任务 %s 不在 %s 中", taskID, from)
		}
		return err
	}
	if task.Status != from {
		return fmt.Errorf("任务 %s 不在 %s 中", taskID, from)
	}
	return s.repo.MoveStatus(context.Background(), taskID, to, task.Version)
}

// DeleteTask deletes a task by ID.
func (s *Storage) DeleteTask(taskID string) error {
	_, err := s.repo.LoadTask(context.Background(), taskID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("任务 %s 不存在", taskID)
		}
		return err
	}
	return s.repo.DeleteTask(context.Background(), taskID)
}

// GetTaskStatus returns the current status of a task, or empty string if not found.
func (s *Storage) GetTaskStatus(taskID string) string {
	task, err := s.repo.LoadTask(context.Background(), taskID)
	if err != nil {
		return ""
	}
	return task.Status
}

// SaveExecution saves an execution record.
// taskID is accepted for API compatibility; rec.TaskID is set to taskID.
func (s *Storage) SaveExecution(taskID string, rec *ExecutionRecord) error {
	_, err := s.repo.LoadTask(context.Background(), taskID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("任务 %s 不存在", taskID)
		}
		return err
	}
	if rec.ExecutionID == "" {
		rec.ExecutionID = generateExecutionID(taskID, rec)
	}
	rec.TaskID = taskID
	return s.repo.SaveExecution(context.Background(), rec)
}

// LoadExecutions loads recent execution records for a task (up to 50).
func (s *Storage) LoadExecutions(taskID string) ([]ExecutionRecord, error) {
	ptrs, err := s.repo.ListExecutions(context.Background(), taskID, 50)
	if err != nil {
		return nil, err
	}
	if len(ptrs) == 0 {
		return nil, nil
	}
	recs := make([]ExecutionRecord, len(ptrs))
	for i, p := range ptrs {
		recs[i] = *p
	}
	return recs, nil
}

// EnsureDirs is a no-op in DB mode.
func (s *Storage) EnsureDirs() error {
	return nil
}
