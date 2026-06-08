package schedule

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/storage"
)

func newTestScheduleStorage(t *testing.T) *Storage {
	t.Helper()
	store := storage.NewLocal()
	baseDir := t.TempDir()
	s := NewStorage(baseDir, store, logger.NewNop())
	if err := s.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return s
}

func sampleTask(id string) *Task {
	return &Task{
		ID:        id,
		Name:      "test task " + id,
		Schedule:  "0 0 * * *",
		TaskDef:   TaskDef{Instruction: "echo hello"},
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestSaveAndLoadTask(t *testing.T) {
	s := newTestScheduleStorage(t)

	task := sampleTask("task-001")
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	got, err := s.LoadTask("task-001")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if got.ID != task.ID {
		t.Errorf("ID = %q, want %q", got.ID, task.ID)
	}
	if got.Name != task.Name {
		t.Errorf("Name = %q, want %q", got.Name, task.Name)
	}
	if got.Schedule != task.Schedule {
		t.Errorf("Schedule = %q, want %q", got.Schedule, task.Schedule)
	}

	if status := s.GetTaskStatus("task-001"); status != "active" {
		t.Errorf("GetTaskStatus = %q, want %q", status, "active")
	}
}

func TestMoveTask(t *testing.T) {
	s := newTestScheduleStorage(t)

	task := sampleTask("task-move")
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	if err := s.MoveTask("task-move", "active", "disabled"); err != nil {
		t.Fatalf("MoveTask active->disabled: %v", err)
	}
	if status := s.GetTaskStatus("task-move"); status != "disabled" {
		t.Errorf("after move, GetTaskStatus = %q, want %q", status, "disabled")
	}

	got, err := s.LoadTask("task-move")
	if err != nil {
		t.Fatalf("LoadTask after move: %v", err)
	}
	if got.ID != "task-move" {
		t.Errorf("ID = %q, want %q", got.ID, "task-move")
	}

	if err := s.MoveTask("task-move", "disabled", "archive"); err != nil {
		t.Fatalf("MoveTask disabled->archive: %v", err)
	}
	if status := s.GetTaskStatus("task-move"); status != "archive" {
		t.Errorf("after move, GetTaskStatus = %q, want %q", status, "archive")
	}

	if err := s.MoveTask("task-move", "active", "archive"); err == nil {
		t.Errorf("MoveTask from non-existent source should fail")
	}
}

func TestDeleteTask(t *testing.T) {
	s := newTestScheduleStorage(t)

	task := sampleTask("task-del")
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	if err := s.DeleteTask("task-del"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	if _, err := s.LoadTask("task-del"); err == nil {
		t.Errorf("LoadTask after delete should fail")
	}
	if status := s.GetTaskStatus("task-del"); status != "" {
		t.Errorf("after delete, GetTaskStatus = %q, want empty", status)
	}
}

func TestSaveAndLoadExecutions(t *testing.T) {
	s := newTestScheduleStorage(t)

	task := sampleTask("task-exec")
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	rec1 := &ExecutionRecord{
		TaskID:     "task-exec",
		ExecTime:   now.Add(-2 * time.Hour),
		Status:     "completed",
		DurationMs: 100,
	}
	rec2 := &ExecutionRecord{
		TaskID:     "task-exec",
		ExecTime:   now.Add(-1 * time.Hour),
		Status:     "failed",
		DurationMs: 200,
	}
	rec3 := &ExecutionRecord{
		TaskID:     "task-exec",
		ExecTime:   now,
		Status:     "completed",
		DurationMs: 300,
	}

	for _, rec := range []*ExecutionRecord{rec1, rec2, rec3} {
		if err := s.SaveExecution("task-exec", rec); err != nil {
			t.Fatalf("SaveExecution: %v", err)
		}
	}

	records, err := s.LoadExecutions("task-exec")
	if err != nil {
		t.Fatalf("LoadExecutions: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("len(records) = %d, want 3", len(records))
	}

	// Records should be sorted by ExecTime DESC (newest first)
	if !records[0].ExecTime.Equal(rec3.ExecTime) {
		t.Errorf("records[0].ExecTime = %v, want %v", records[0].ExecTime, rec3.ExecTime)
	}
	if !records[1].ExecTime.Equal(rec2.ExecTime) {
		t.Errorf("records[1].ExecTime = %v, want %v", records[1].ExecTime, rec2.ExecTime)
	}
	if !records[2].ExecTime.Equal(rec1.ExecTime) {
		t.Errorf("records[2].ExecTime = %v, want %v", records[2].ExecTime, rec1.ExecTime)
	}
}

func TestListAllTasks(t *testing.T) {
	s := newTestScheduleStorage(t)

	t1 := sampleTask("task-A")
	t2 := sampleTask("task-B")
	t3 := sampleTask("task-C")

	if err := s.SaveTask(t1); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	if err := s.SaveTask(t2); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	if err := s.SaveTask(t3); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	if err := s.MoveTask("task-B", "active", "disabled"); err != nil {
		t.Fatalf("MoveTask: %v", err)
	}
	if err := s.MoveTask("task-C", "active", "archive"); err != nil {
		t.Fatalf("MoveTask: %v", err)
	}

	all, err := s.ListAllTasks()
	if err != nil {
		t.Fatalf("ListAllTasks: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("len(all) = %d, want 3", len(all))
	}

	active, err := s.ListActiveTasks()
	if err != nil {
		t.Fatalf("ListActiveTasks: %v", err)
	}
	if len(active) != 1 {
		t.Errorf("len(active) = %d, want 1", len(active))
	}
	if len(active) > 0 && active[0].ID != "task-A" {
		t.Errorf("active[0].ID = %q, want task-A", active[0].ID)
	}
}

func TestDeleteNonExistentTask(t *testing.T) {
	s := newTestScheduleStorage(t)

	if err := s.DeleteTask("does-not-exist"); err == nil {
		t.Errorf("DeleteTask on missing id should fail")
	}
	if _, err := s.LoadTask("does-not-exist"); err == nil {
		t.Errorf("LoadTask on missing id should fail")
	}
	if status := s.GetTaskStatus("does-not-exist"); status != "" {
		t.Errorf("GetTaskStatus on missing id = %q, want empty", status)
	}
}

func TestLoadTask_ContextCancel(t *testing.T) {
	// LoadTask 当前不接受 context, 但底层 storage.Read 接受。
	// 此用例验证: 即使在 context 被外部取消的环境下,
	// LoadTask 内部使用的 context.Background() 不受影响,仍能正常读取。
	s := newTestScheduleStorage(t)

	task := sampleTask("task-ctx")
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	// 模拟一个已取消的外部 context (LoadTask 不会传播,验证其使用独立 ctx)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx

	got, err := s.LoadTask("task-ctx")
	if err != nil {
		t.Fatalf("LoadTask should not be affected by cancelled external ctx: %v", err)
	}
	if got.ID != "task-ctx" {
		t.Errorf("ID = %q, want task-ctx", got.ID)
	}

	// 进一步确认: 如果直接对 store 传入已取消的 ctx, 不一定会 fail
	// (local 实现不感知 ctx),但至少不能阻塞。
	store := storage.NewLocal()
	path := filepath.Join(t.TempDir(), "x.json")
	cancelledCtx, c2 := context.WithCancel(context.Background())
	c2()
	_, _ = store.Read(cancelledCtx, path) // expect ErrNotFound, not hang
}
