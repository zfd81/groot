package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/storage"
)

func newTestScheduleStorage(t *testing.T) *Storage {
	t.Helper()
	store := storage.NewLocal()
	baseDir := t.TempDir()
	return NewStorage(baseDir, store, logger.NewNop())
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

func TestLoadExecutions_NoExecutionsDir(t *testing.T) {
	s := newTestScheduleStorage(t)
	task := &Task{
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

// TestSaveExecution_TaskNotExist 验证任务不存在时 SaveExecution 返回业务话术。
func TestSaveExecution_TaskNotExist(t *testing.T) {
	s := newTestScheduleStorage(t)
	rec := &ExecutionRecord{
		TaskID:     "task-nope",
		ExecTime:   time.Now(),
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

// TestMoveTask_DstAlreadyExists 验证目标目录已存在(且非空)时,MoveTask 返回错误
// 而非静默覆盖。底层 os.Rename 对非空目录返回 ENOTEMPTY/EEXIST,Storage 透传该错误,
// 上层依赖此契约避免数据丢失。
func TestMoveTask_DstAlreadyExists(t *testing.T) {
	s := newTestScheduleStorage(t)
	// 先准备 active/task-x
	taskA := &Task{
		ID:        "task-x",
		Name:      "源任务",
		Schedule:  "0 * * * *",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.SaveTask(taskA); err != nil {
		t.Fatalf("SaveTask A: %v", err)
	}
	// 先 Move 到 disabled
	if err := s.MoveTask("task-x", "active", "disabled"); err != nil {
		t.Fatalf("MoveTask 1: %v", err)
	}
	// 再 SaveTask 把同 ID 写到 active(产生 dst 残留情形:active/task-x 又出现了)
	taskB := &Task{
		ID:        "task-x",
		Name:      "重新创建的同名任务",
		Schedule:  "0 * * * *",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.SaveTask(taskB); err != nil {
		t.Fatalf("SaveTask B: %v", err)
	}
	// disabled 中仍有 task-x,active 中也有 task-x;再次 Move disabled → active 应当失败:
	// os.Rename 不允许将目录重命名到一个已存在的非空目录。
	err := s.MoveTask("task-x", "disabled", "active")
	if err == nil {
		t.Fatal("expected MoveTask to fail when dst dir already exists and is non-empty")
	}
	// 失败后两侧都应保留(原子性):disabled/task-x 应仍存在;active/task-x 仍是 taskB。
	loaded, err := s.LoadTask("task-x")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if loaded.Name != "重新创建的同名任务" {
		t.Errorf("active side should be untouched, name=%q", loaded.Name)
	}
}
