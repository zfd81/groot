package schedule

import (
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// Manager handles task lifecycle management
type Manager struct {
	storage *Storage
	engine  *Engine
	runner  *Runner
	log     *logger.Logger
}

// NewManager creates a new task manager
func NewManager(storage *Storage, engine *Engine, runner *Runner, log *logger.Logger) *Manager {
	return &Manager{
		storage: storage,
		engine:  engine,
		runner:  runner,
		log:     log,
	}
}

// Create creates a new task, saves it, and registers it
func (m *Manager) Create(task *Task) error {
	if task.ID == "" {
		task.ID = generateTaskID(task.Name)
	}
	if task.MissedPolicy == "" {
		task.MissedPolicy = "run_once"
	}

	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now

	if err := m.storage.SaveTask(task); err != nil {
		m.log.Error("保存任务失败", zap.String("task_id", task.ID), zap.Error(err))
		return err
	}

	if err := m.engine.Register(task); err != nil {
		m.log.Error("注册任务到调度器失败", zap.String("task_id", task.ID), zap.Error(err))
		return err
	}

	m.log.Info("任务创建成功",
		zap.String("task_id", task.ID),
		zap.String("name", task.Name),
		zap.String("schedule", task.Schedule),
	)
	return nil
}

// List returns tasks filtered by status (active/disabled/archive/all)
func (m *Manager) List(status string) ([]*Task, error) {
	if status == "all" || status == "" {
		return m.storage.ListAllTasks()
	}
	return m.storage.listTasksIn(status)
}

// Get returns a task by ID
func (m *Manager) Get(taskID string) (*Task, error) {
	return m.storage.LoadTask(taskID)
}

// Delete permanently deletes a task
func (m *Manager) Delete(taskID string) error {
	m.engine.Unregister(taskID)
	if err := m.storage.DeleteTask(taskID); err != nil {
		return err
	}
	m.log.Info("任务已删除", zap.String("task_id", taskID))
	return nil
}

// Disable moves a task from active to disabled
func (m *Manager) Disable(taskID string) error {
	m.engine.Unregister(taskID)
	if err := m.storage.MoveTask(taskID, "active", "disabled"); err != nil {
		return err
	}
	m.log.Info("任务已禁用", zap.String("task_id", taskID))
	return nil
}

// Enable moves a task from disabled to active and registers it
func (m *Manager) Enable(taskID string) error {
	if err := m.storage.MoveTask(taskID, "disabled", "active"); err != nil {
		return err
	}

	task, err := m.storage.LoadTask(taskID)
	if err != nil {
		return err
	}

	if err := m.engine.Register(task); err != nil {
		return err
	}

	m.log.Info("任务已启用", zap.String("task_id", taskID))
	return nil
}

// Archive moves a task to the archive directory
func (m *Manager) Archive(taskID string) error {
	status := m.storage.GetTaskStatus(taskID)
	if status == "active" {
		m.engine.Unregister(taskID)
	}
	if err := m.storage.MoveTask(taskID, status, "archive"); err != nil {
		return err
	}
	m.log.Info("任务已归档", zap.String("task_id", taskID))
	return nil
}

// GetHistory returns execution history for a task
func (m *Manager) GetHistory(taskID string) ([]ExecutionRecord, error) {
	return m.storage.LoadExecutions(taskID)
}

// Rerun executes a task immediately
func (m *Manager) Rerun(taskID string) error {
	task, err := m.storage.LoadTask(taskID)
	if err != nil {
		return err
	}
	return m.runner.RunImmediate(task)
}

// generateTaskID generates a kebab-case ID from the task name
func generateTaskID(name string) string {
	id := strings.ToLower(name)
	id = strings.ReplaceAll(id, " ", "-")
	id = strings.ReplaceAll(id, "_", "-")
	// Remove non-alphanumeric except dashes
	var cleaned strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			cleaned.WriteRune(r)
		}
	}
	result := cleaned.String()
	// Remove consecutive dashes
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	result = strings.Trim(result, "-")
	if result == "" {
		result = fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return "task-" + result
}
