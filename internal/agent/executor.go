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
	"github.com/zfd81/groot/internal/storage"
)

// Executor executes tasks with ReAct mode
type Executor struct {
	storage         storage.TaskStorage
	skillRegistry   *skill.Registry
	mcpManager      *mcp.Manager
	cancelManager   *CancelManager
	attachmentHandler *attachment.Handler
	config          config.Config
	logger          *logger.Logger
	runningTasks    sync.Map
}

// NewExecutor creates a new task executor
func NewExecutor(
	store storage.TaskStorage,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	cancelMgr *CancelManager,
	attHandler *attachment.Handler,
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		storage:         store,
		skillRegistry:   skills,
		mcpManager:      mcpMgr,
		cancelManager:   cancelMgr,
		attachmentHandler: attHandler,
		config:          cfg,
		logger:          log,
	}
}

// Execute starts task execution
func (e *Executor) Execute(task *storage.Task, sse *SSEWriter, cancelCh chan struct{}) {
	e.runningTasks.Store(task.ID, true)
	defer e.runningTasks.Delete(task.ID)

	// Write intent event
	sse.WriteIntent()

	// Process attachments if any
	var processedAttachments []attachment.ProcessedAttachment
	var attachmentPaths []storage.AttachmentPath
	if len(task.Attachments) > 0 && e.attachmentHandler != nil {
		// Convert storage attachments to attachment.Attachment
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
			attachmentPaths = append(attachmentPaths, storage.AttachmentPath{
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

	// Update task in storage
	updates := map[string]interface{}{
		"end_time": endTime,
		"duration": int(duration.Seconds()),
		"steps":    result.Steps,
	}

	if err != nil {
		updates["status"] = storage.StatusFailed
		updates["error"] = &storage.TaskError{
			Code:    "execution_error",
			Message: err.Error(),
		}
		sse.WriteCompleted("failed", durationStr, nil, &StepError{Code: "execution_error", Message: err.Error()}, "")
	} else if ctx.Err() == context.Canceled {
		updates["status"] = storage.StatusCancelled
		updates["error"] = nil
		sse.WriteCompleted("cancelled", durationStr, nil, nil, "用户主动取消")
	} else {
		updates["status"] = storage.StatusCompleted
		updates["result"] = result.Content
		sse.WriteCompleted("success", durationStr, result.Content, nil, "")
	}

	e.storage.Update(task.ID, updates)

	// Unregister from cancel manager
	e.cancelManager.Unregister(task.ID)
}

// handleFailure handles task failure
func (e *Executor) handleFailure(task *storage.Task, sse *SSEWriter, err error, errorCode string) {
	endTime := time.Now()
	duration := endTime.Sub(task.StartTime)
	durationStr := formatDuration(duration)

	updates := map[string]interface{}{
		"status":    storage.StatusFailed,
		"end_time":  endTime,
		"duration":  int(duration.Seconds()),
		"error":     &storage.TaskError{Code: errorCode, Message: err.Error()},
	}
	e.storage.Update(task.ID, updates)

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
