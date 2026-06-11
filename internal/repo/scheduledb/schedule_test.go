package scheduledb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/schedule"
)

func newRepo(t *testing.T) schedule.ScheduleRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func TestSaveAndLoadTask(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	task := &schedule.Task{ID: "task-001", Name: "test", Schedule: "0 * * * *"}
	if err := r.SaveTask(ctx, task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	got, err := r.LoadTask(ctx, "task-001")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if got.Name != "test" {
		t.Errorf("expected name=test, got %s", got.Name)
	}
}

func TestLoadTask_NotFound(t *testing.T) {
	r := newRepo(t)
	_, err := r.LoadTask(context.Background(), "nonexistent")
	if !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMoveStatus_Conflict(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.SaveTask(ctx, &schedule.Task{ID: "t1", Name: "x", Schedule: "0 * * * *"})
	err := r.MoveStatus(ctx, "t1", schedule.TaskStatusDisabled, 99) // wrong version
	if !errors.Is(err, schedule.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestListByStatus(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.SaveTask(ctx, &schedule.Task{ID: "t2", Name: "a", Schedule: "* * * * *"})
	r.SaveTask(ctx, &schedule.Task{ID: "t3", Name: "b", Schedule: "* * * * *"})
	tasks, err := r.ListByStatus(ctx, schedule.TaskStatusActive)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestCompleteExecution(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.SaveTask(ctx, &schedule.Task{ID: "t4", Name: "z", Schedule: "* * * * *"})
	now := time.Now()
	fin := now.Add(time.Second)
	rec := &schedule.ExecutionRecord{
		ExecutionID: "exec-001", TaskID: "t4",
		StartedAt: now, FinishedAt: &fin, Status: "success",
	}
	if err := r.SaveExecution(ctx, rec); err != nil {
		t.Fatalf("SaveExecution: %v", err)
	}
	if err := r.CompleteExecution(ctx, rec, now.Add(time.Minute), now, 0); err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	execs, err := r.ListExecutions(ctx, "t4", 10)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(execs) != 1 {
		t.Errorf("expected 1 execution, got %d", len(execs))
	}
}
