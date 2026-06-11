package schedule_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo/scheduledb"
	"github.com/zfd81/groot/internal/schedule"
)

// newTestScheduleStorage creates a Storage backed by an in-memory SQLite DB.
func newTestScheduleStorage(t *testing.T) *schedule.Storage {
	t.Helper()
	sqlxDB, err := sqlx.Open("sqlite3", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	if err := db.Migrate(sqlxDB, db.DialectSQLite); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return schedule.NewStorage(scheduledb.New(sqlxDB, db.DialectSQLite), logger.NewNop())
}

func sampleTask(id string) *schedule.Task {
	return &schedule.Task{
		ID:        id,
		Name:      "test task " + id,
		Schedule:  "0 0 * * *",
		TaskDef:   schedule.TaskDef{Instruction: "echo hello"},
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

	// Moving from wrong source status should fail.
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
	rec1 := &schedule.ExecutionRecord{
		TaskID:     "task-exec",
		StartedAt:  now.Add(-2 * time.Hour),
		Status:     "completed",
		DurationMs: 100,
	}
	rec2 := &schedule.ExecutionRecord{
		TaskID:     "task-exec",
		StartedAt:  now.Add(-1 * time.Hour),
		Status:     "failed",
		DurationMs: 200,
	}
	rec3 := &schedule.ExecutionRecord{
		TaskID:     "task-exec",
		StartedAt:  now,
		Status:     "completed",
		DurationMs: 300,
	}

	for _, rec := range []*schedule.ExecutionRecord{rec1, rec2, rec3} {
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

	// Records should be sorted by StartedAt DESC (newest first).
	if !records[0].StartedAt.Equal(rec3.StartedAt) {
		t.Errorf("records[0].StartedAt = %v, want %v", records[0].StartedAt, rec3.StartedAt)
	}
	if !records[1].StartedAt.Equal(rec2.StartedAt) {
		t.Errorf("records[1].StartedAt = %v, want %v", records[1].StartedAt, rec2.StartedAt)
	}
	if !records[2].StartedAt.Equal(rec1.StartedAt) {
		t.Errorf("records[2].StartedAt = %v, want %v", records[2].StartedAt, rec1.StartedAt)
	}
}

func TestListAllTasks(t *testing.T) {
	s := newTestScheduleStorage(t)

	t1 := sampleTask("task-A")
	t2 := sampleTask("task-B")
	t3 := sampleTask("task-C")

	for _, task := range []*schedule.Task{t1, t2, t3} {
		if err := s.SaveTask(task); err != nil {
			t.Fatalf("SaveTask %s: %v", task.ID, err)
		}
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

func TestLoadExecutions_NoExecutions(t *testing.T) {
	s := newTestScheduleStorage(t)
	task := &schedule.Task{
		ID:        "task-empty-exec",
		Name:      "尚无执行的任务",
		Schedule:  "0 * * * *",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	records, err := s.LoadExecutions("task-empty-exec")
	if err != nil {
		t.Fatalf("LoadExecutions: %v", err)
	}
	if records != nil {
		t.Errorf("expected nil records, got %v", records)
	}
}

func TestSaveExecution_TaskNotExist(t *testing.T) {
	s := newTestScheduleStorage(t)
	rec := &schedule.ExecutionRecord{
		TaskID:     "task-nope",
		StartedAt:  time.Now(),
		Status:     "completed",
		DurationMs: 100,
	}
	err := s.SaveExecution("task-nope", rec)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
	if !strings.Contains(err.Error(), "任务 task-nope 不存在") {
		t.Errorf("expected error to contain '任务 task-nope 不存在', got: %v", err)
	}
}
