package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	istorage "github.com/zfd81/groot/internal/storage"
)

// Storage handles file-based persistence for scheduled tasks
type Storage struct {
	baseDir string // {GROOT_HOME}/schedules
	store   istorage.Storage
	log     *logger.Logger
}

// NewStorage creates a new storage instance
func NewStorage(baseDir string, store istorage.Storage, log *logger.Logger) *Storage {
	return &Storage{baseDir: baseDir, store: store, log: log}
}

// EnsureDirs 在 local 模式下预建 active/disabled/archive;minio 模式下
// Storage.Write 自动建前缀,本方法 noop。
func (s *Storage) EnsureDirs() error {
	if _, ok := s.store.(*istorage.Local); !ok {
		return nil
	}
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
	task.UpdatedAt = task.CreatedAt
	if task.CreatedAt.IsZero() {
		task.CreatedAt = task.UpdatedAt
	}

	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}

	taskPath := filepath.Join(s.baseDir, "active", task.ID, "task.json")
	return s.store.Write(context.Background(), taskPath, bytes.NewReader(data), int64(len(data)), "application/json")
}

// LoadTask reads a task.json by ID, searching active/disabled/archive
func (s *Storage) LoadTask(taskID string) (*Task, error) {
	for _, status := range []string{"active", "disabled", "archive"} {
		path := filepath.Join(s.baseDir, status, taskID, "task.json")
		rc, err := s.store.Read(context.Background(), path)
		if err != nil {
			if errors.Is(err, istorage.ErrNotFound) {
				continue
			}
			return nil, err
		}
		data, readErr := io.ReadAll(rc)
		rc.Close()
		if readErr != nil {
			return nil, readErr
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
		_, err := s.store.Stat(context.Background(), dir)
		if err != nil {
			if errors.Is(err, istorage.ErrNotFound) {
				continue
			}
			return err
		}
		return s.store.DeleteDir(context.Background(), dir)
	}
	return fmt.Errorf("任务 %s 不存在", taskID)
}

// MoveTask moves a task directory between status dirs
func (s *Storage) MoveTask(taskID, from, to string) error {
	srcDir := filepath.Join(s.baseDir, from, taskID)
	dstDir := filepath.Join(s.baseDir, to, taskID)
	if _, err := s.store.Stat(context.Background(), srcDir); err != nil {
		if errors.Is(err, istorage.ErrNotFound) {
			return fmt.Errorf("任务 %s 不在 %s 中", taskID, from)
		}
		return err
	}
	return s.store.Rename(context.Background(), srcDir, dstDir)
}

// GetTaskStatus returns the current status directory for a task
func (s *Storage) GetTaskStatus(taskID string) string {
	for _, status := range []string{"active", "disabled", "archive"} {
		dir := filepath.Join(s.baseDir, status, taskID)
		if _, err := s.store.Stat(context.Background(), dir); err == nil {
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

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}

	filename := record.ExecTime.Format("2006-01-02-150405") + ".json"
	path := filepath.Join(s.baseDir, status, taskID, "executions", filename)
	return s.store.Write(context.Background(), path, bytes.NewReader(data), int64(len(data)), "application/json")
}

// LoadExecutions loads all execution records for a task
func (s *Storage) LoadExecutions(taskID string) ([]ExecutionRecord, error) {
	status := s.GetTaskStatus(taskID)
	if status == "" {
		return nil, fmt.Errorf("任务 %s 不存在", taskID)
	}

	execDir := filepath.Join(s.baseDir, status, taskID, "executions")
	entries, err := s.store.List(context.Background(), execDir)
	if err != nil {
		if errors.Is(err, istorage.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var records []ExecutionRecord
	for _, entry := range entries {
		if entry.IsDir || filepath.Ext(entry.Path) != ".json" {
			continue
		}
		rc, err := s.store.Read(context.Background(), entry.Path)
		if err != nil {
			s.log.Info("读取执行记录失败: "+filepath.Base(entry.Path), zap.Error(err))
			continue
		}
		data, readErr := io.ReadAll(rc)
		rc.Close()
		if readErr != nil {
			s.log.Info("读取执行记录内容失败: "+filepath.Base(entry.Path), zap.Error(readErr))
			continue
		}
		var record ExecutionRecord
		if err := json.Unmarshal(data, &record); err != nil {
			s.log.Info("解析执行记录失败: "+filepath.Base(entry.Path), zap.Error(err))
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
	entries, err := s.store.List(context.Background(), dir)
	if err != nil {
		if errors.Is(err, istorage.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var tasks []*Task
	for _, entry := range entries {
		if !entry.IsDir {
			continue
		}
		taskPath := filepath.Join(entry.Path, "task.json")
		rc, err := s.store.Read(context.Background(), taskPath)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(rc)
		rc.Close()
		if readErr != nil {
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
