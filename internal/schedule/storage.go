package schedule

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// Storage handles file-based persistence for scheduled tasks
type Storage struct {
	baseDir string // {GROOT_HOME}/schedules
	log     *logger.Logger
}

// NewStorage creates a new storage instance
func NewStorage(baseDir string, log *logger.Logger) *Storage {
	return &Storage{baseDir: baseDir, log: log}
}

// EnsureDirs creates the active/disabled/archive directories if they don't exist
func (s *Storage) EnsureDirs() error {
	for _, dir := range []string{"active", "disabled", "archive"} {
		path := filepath.Join(s.baseDir, dir)
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", path, err)
		}
	}
	return nil
}

// SaveTask atomically writes a task.json to active/{id}/
func (s *Storage) SaveTask(task *Task) error {
	dir := filepath.Join(s.baseDir, "active", task.ID)
	if err := os.MkdirAll(filepath.Join(dir, "executions"), 0755); err != nil {
		return err
	}

	task.UpdatedAt = task.CreatedAt
	if task.CreatedAt.IsZero() {
		task.CreatedAt = task.UpdatedAt
	}

	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}

	taskPath := filepath.Join(dir, "task.json")
	tmpPath := taskPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, taskPath)
}

// LoadTask reads a task.json by ID, searching active/disabled/archive
func (s *Storage) LoadTask(taskID string) (*Task, error) {
	for _, status := range []string{"active", "disabled", "archive"} {
		path := filepath.Join(s.baseDir, status, taskID, "task.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			return nil, err
		}
		return &task, nil
	}
	return nil, fmt.Errorf("任务 %s 不存在", taskID)
}

// DeleteTask removes the entire task directory
func (s *Storage) DeleteTask(taskID string) error {
	for _, status := range []string{"active", "disabled", "archive"} {
		dir := filepath.Join(s.baseDir, status, taskID)
		if _, err := os.Stat(dir); err == nil {
			return os.RemoveAll(dir)
		}
	}
	return fmt.Errorf("任务 %s 不存在", taskID)
}

// MoveTask moves a task directory between status dirs
func (s *Storage) MoveTask(taskID, from, to string) error {
	srcDir := filepath.Join(s.baseDir, from, taskID)
	dstDir := filepath.Join(s.baseDir, to, taskID)
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return fmt.Errorf("任务 %s 不在 %s 中", taskID, from)
	}
	return os.Rename(srcDir, dstDir)
}

// GetTaskStatus returns the current status directory for a task
func (s *Storage) GetTaskStatus(taskID string) string {
	for _, status := range []string{"active", "disabled", "archive"} {
		dir := filepath.Join(s.baseDir, status, taskID)
		if _, err := os.Stat(dir); err == nil {
			return status
		}
	}
	return ""
}

// SaveExecution writes an execution record
func (s *Storage) SaveExecution(taskID string, record *ExecutionRecord) error {
	status := s.GetTaskStatus(taskID)
	if status == "" {
		return fmt.Errorf("任务 %s 不存在", taskID)
	}

	execDir := filepath.Join(s.baseDir, status, taskID, "executions")
	if err := os.MkdirAll(execDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}

	filename := record.ExecTime.Format("2006-01-02-150405") + ".json"
	path := filepath.Join(execDir, filename)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// LoadExecutions loads all execution records for a task
func (s *Storage) LoadExecutions(taskID string) ([]ExecutionRecord, error) {
	status := s.GetTaskStatus(taskID)
	if status == "" {
		return nil, fmt.Errorf("任务 %s 不存在", taskID)
	}

	execDir := filepath.Join(s.baseDir, status, taskID, "executions")
	entries, err := os.ReadDir(execDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var records []ExecutionRecord
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(execDir, entry.Name()))
		if err != nil {
			s.log.Info("读取执行记录失败: "+entry.Name(), zap.Error(err))
			continue
		}
		var record ExecutionRecord
		if err := json.Unmarshal(data, &record); err != nil {
			s.log.Info("解析执行记录失败: "+entry.Name(), zap.Error(err))
			continue
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].ExecTime.After(records[j].ExecTime)
	})

	return records, nil
}

// ListActiveTasks loads all tasks in the active directory
func (s *Storage) ListActiveTasks() ([]*Task, error) {
	return s.listTasksIn("active")
}

// ListAllTasks loads all tasks across all status directories
func (s *Storage) ListAllTasks() ([]*Task, error) {
	var all []*Task
	for _, status := range []string{"active", "disabled", "archive"} {
		tasks, _ := s.listTasksIn(status)
		all = append(all, tasks...)
	}
	return all, nil
}

func (s *Storage) listTasksIn(status string) ([]*Task, error) {
	dir := filepath.Join(s.baseDir, status)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var tasks []*Task
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name(), "task.json"))
		if err != nil {
			continue
		}
		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}
		tasks = append(tasks, &task)
	}
	return tasks, nil
}
