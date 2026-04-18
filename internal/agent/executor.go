package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zfd81/groot/internal/attachment"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/skill"
	// "github.com/zfd81/groot/internal/storage" // removed - will be re-added in Phase 4
)

// TaskStatus represents task status (temporary definition until memory module)
type TaskStatus string

const (
	StatusRunning    TaskStatus = "running"
	StatusCompleted  TaskStatus = "completed"
	StatusFailed     TaskStatus = "failed"
	StatusCancelled  TaskStatus = "cancelled"
)

// Task represents a task (temporary definition until memory module)
type Task struct {
	ID          string
	Instruction string
	Prompt      string
	Status      TaskStatus
	StartTime   time.Time
	EndTime     *time.Time
	Duration    int
	Result      string
	Error       *TaskError
	Steps       []StepRecord
	Attachments []Attachment
	Caller      string
	Progress    *ProgressInfo
}

// TaskError represents task error
type TaskError struct {
	Code    string
	Message string
}

// Attachment represents attachment data
type Attachment struct {
	Type    string
	Name    string
	Content string
}

// AttachmentPath represents attachment path info
type AttachmentPath struct {
	OriginalName string
	Type         string
	FullPath     string
	RelativePath string
	Size         int64
	ContentType  string
}

// StepRecord represents execution step
type StepRecord struct {
	StepID       string
	Type         string
	Name         string
	StartTime    *time.Time
	EndTime      *time.Time
	Status       TaskStatus
	NestingLevel int
	Error        *TaskError
}

// ProgressInfo represents task progress
type ProgressInfo struct {
	CurrentStep    string
	StepsCompleted int
	Percentage     int
}

// Executor executes tasks with ReAct mode
type Executor struct {
	// storage         storage.TaskStorage // removed - will be re-added in Phase 4
	skillRegistry     *skill.Registry
	mcpManager        *mcp.Manager
	cancelManager     *CancelManager
	attachmentHandler *attachment.Handler
	config            config.Config
	logger            *logger.Logger
	runningTasks      sync.Map
}

// NewExecutor creates a new task executor
// NOTE: storage parameter removed - will be re-added in Phase 4
func NewExecutor(
	// store storage.TaskStorage, // removed
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	cancelMgr *CancelManager,
	attHandler *attachment.Handler,
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		// storage:         store,
		skillRegistry:     skills,
		mcpManager:        mcpMgr,
		cancelManager:     cancelMgr,
		attachmentHandler: attHandler,
		config:            cfg,
		logger:            log,
	}
}

// Execute starts task execution
func (e *Executor) Execute(task *Task, sse *SSEWriter, cancelCh chan struct{}) {
	e.runningTasks.Store(task.ID, true)
	defer e.runningTasks.Delete(task.ID)

	// Process attachments if any (before intent event)
	var processedAttachments []attachment.ProcessedAttachment
	var attachmentPaths []AttachmentPath
	if len(task.Attachments) > 0 && e.attachmentHandler != nil {
		// Convert attachments
		attInput := make([]attachment.Attachment, len(task.Attachments))
		for i, att := range task.Attachments {
			attInput[i] = attachment.Attachment{
				Type:    att.Type,
				Name:    att.Name,
				Content: att.Content,
			}
		}

		// Validate attachments
		if err := e.attachmentHandler.Validate(attInput); err != nil {
			e.handleFailure(task, sse, err, "attachment_validation_error")
			return
		}

		// Process attachments (decode Base64, save to temp)
		processed, err := e.attachmentHandler.Process(task.ID, attInput)
		if err != nil {
			e.handleFailure(task, sse, err, "attachment_processing_error")
			return
		}
		processedAttachments = processed

		// Build attachment paths for engine
		for _, pa := range processedAttachments {
			attachmentPaths = append(attachmentPaths, AttachmentPath{
				OriginalName: pa.OriginalName,
				Type:         pa.Type,
				FullPath:     pa.FullPath,
				RelativePath: pa.Path,
				Size:         pa.Size,
				ContentType:  pa.ContentType,
			})
		}
	}

	// Cleanup attachments when done
	defer func() {
		if e.attachmentHandler != nil && len(processedAttachments) > 0 {
			e.attachmentHandler.Cleanup(task.ID)
		}
	}()

	// Write intent event (after all preparation is complete)
	sse.WriteIntent()

	// Create engine using eino
	engine := NewEngine(
		e.config.LLM,
		e.skillRegistry,
		e.mcpManager,
		e.config.React,
		e.logger,
	)

	// Create context with cancellation support
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle cancellation in separate goroutine
	go func() {
		select {
		case <-cancelCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	// Run engine with progress callback
	result, err := engine.Run(
		ctx,
		task.Instruction,
		task.Prompt,
		attachmentPaths,
		func(stepID, eventType, message string) {
			select {
			case <-ctx.Done():
				return
			default:
				sse.WriteProgress(stepID, message)
			}
		},
	)

	// Calculate duration
	startTime := task.StartTime
	endTime := time.Now()
	duration := endTime.Sub(startTime)
	durationStr := formatDuration(duration)

	// Update task status (storage update disabled)
	// updates := map[string]interface{}{
	// 	"end_time": endTime,
	// 	"duration": int(duration.Seconds()),
	// 	"steps":    result.Steps,
	// }

	if err != nil {
		// updates["status"] = storage.StatusFailed
		// updates["error"] = &storage.TaskError{...}
		sse.WriteCompleted("failed", durationStr, nil, &StepError{Code: "execution_error", Message: err.Error()}, "")
	} else if ctx.Err() == context.Canceled {
		// updates["status"] = storage.StatusCancelled
		sse.WriteCompleted("cancelled", durationStr, nil, nil, "用户主动取消")
	} else {
		// updates["status"] = storage.StatusCompleted
		// updates["result"] = result.Content
		sse.WriteCompleted("success", durationStr, result.Content, nil, "")
	}

	// e.storage.Update(task.ID, updates) // disabled

	// Unregister from cancel manager
	e.cancelManager.Unregister(task.ID)
}

// handleFailure handles task failure
func (e *Executor) handleFailure(task *Task, sse *SSEWriter, err error, errorCode string) {
	endTime := time.Now()
	duration := endTime.Sub(task.StartTime)
	durationStr := formatDuration(duration)

	// Storage update disabled
	// updates := map[string]interface{}{
	// 	"status":    storage.StatusFailed,
	// 	"end_time":  endTime,
	// 	"duration":  int(duration.Seconds()),
	// 	"error":     &storage.TaskError{Code: errorCode, Message: err.Error()},
	// }
	// e.storage.Update(task.ID, updates)

	sse.WriteCompleted("failed", durationStr, nil, &StepError{Code: errorCode, Message: err.Error()}, "")
	e.cancelManager.Unregister(task.ID)
}

// IsRunning checks if task is currently running
func (e *Executor) IsRunning(taskID string) bool {
	_, ok := e.runningTasks.Load(taskID)
	return ok
}

// RunningCount returns count of running tasks
func (e *Executor) RunningCount() int {
	count := 0
	e.runningTasks.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// formatDuration formats duration for display
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) - minutes*60
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}