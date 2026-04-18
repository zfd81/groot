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
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/skill"
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
	ID              string
	Instruction     string
	Prompt          string
	Status          TaskStatus
	StartTime       time.Time
	EndTime         *time.Time
	Duration        int
	Result          string
	Error           *TaskError
	Steps           []StepRecord
	Attachments     []Attachment
	Caller          string
	Progress        *ProgressInfo
	Round           int
	HistoryMessages []memory.Message
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
	memoryManager    *memory.Manager
	skillRegistry    *skill.Registry
	mcpManager       *mcp.Manager
	cancelManager    *CancelManager
	attachmentHandler *attachment.Handler
	config           config.Config
	logger           *logger.Logger
	runningTasks     sync.Map
}

// NewExecutor creates a new task executor
func NewExecutor(
	memMgr *memory.Manager,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	cancelMgr *CancelManager,
	attHandler *attachment.Handler,
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		memoryManager:    memMgr,
		skillRegistry:    skills,
		mcpManager:       mcpMgr,
		cancelManager:    cancelMgr,
		attachmentHandler: attHandler,
		config:           cfg,
		logger:           log,
	}
}

// Execute starts task execution
func (e *Executor) Execute(sessionID string, task *Task, sse *SSEWriter, cancelCh chan struct{}) {
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
			e.handleFailure(sessionID, task, sse, err, "attachment_validation_error")
			return
		}

		// Process attachments (decode Base64, save to temp)
		processed, err := e.attachmentHandler.Process(task.ID, attInput)
		if err != nil {
			e.handleFailure(sessionID, task, sse, err, "attachment_processing_error")
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
	sse.WriteIntent(task.Round)

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

	// Run engine with progress callback and history messages
	result, err := engine.Run(
		ctx,
		task.Instruction,
		task.Prompt,
		attachmentPaths,
		task.HistoryMessages,
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

	// Save chat record to memory
	if e.memoryManager != nil {
		record := &memory.ChatRecord{
			ChatID:      task.ID,
			SessionID:   sessionID,
			Round:       task.Round,
			Timestamp:   endTime,
			Instruction: task.Instruction,
			Duration:    int(duration.Seconds()),
		}

		if err != nil {
			record.Status = "failed"
			record.Error = &memory.Error{
				Code:    "execution_error",
				Message: err.Error(),
			}
		} else if ctx.Err() == context.Canceled {
			record.Status = "cancelled"
		} else {
			record.Status = "completed"
			record.Result = result.Content
			record.Steps = convertSteps(result.Steps)
		}

		// Save chat record
		if saveErr := e.memoryManager.SaveChatRecord(sessionID, record); saveErr != nil {
			e.logger.Error("保存对话记录失败: " + saveErr.Error())
		}

		// Append message to history
		if err == nil && ctx.Err() != context.Canceled && result.Content != "" {
			msg := &memory.Message{
				ChatID:      task.ID,
				Round:       task.Round,
				Timestamp:   endTime,
				Instruction: task.Instruction,
				Result:      result.Content,
				Status:      "completed",
				Duration:    int(duration.Seconds()),
				StepsCount:  len(result.Steps),
			}
			if appendErr := e.memoryManager.AppendMessage(sessionID, msg); appendErr != nil {
				e.logger.Error("追加历史消息失败: " + appendErr.Error())
			}
		}
	}

	if err != nil {
		sse.WriteCompleted("failed", durationStr, task.Round, nil, &StepError{Code: "execution_error", Message: err.Error()}, "")
	} else if ctx.Err() == context.Canceled {
		sse.WriteCompleted("cancelled", durationStr, task.Round, nil, nil, "用户主动取消")
	} else {
		sse.WriteCompleted("success", durationStr, task.Round, result.Content, nil, "")
	}

	// Unregister from cancel manager
	e.cancelManager.Unregister(task.ID)
}

// convertSteps converts agent steps to memory steps
func convertSteps(steps []StepRecord) []memory.Step {
	result := make([]memory.Step, len(steps))
	for i, s := range steps {
		result[i] = memory.Step{
			StepID:       s.StepID,
			Type:         s.Type,
			Name:         s.Name,
			Status:       string(s.Status),
			NestingLevel: s.NestingLevel,
		}
		if s.Error != nil {
			result[i].Error = &memory.Error{
				Code:    s.Error.Code,
				Message: s.Error.Message,
			}
		}
		if s.StartTime != nil {
			result[i].StartTime = *s.StartTime
		}
		if s.EndTime != nil {
			result[i].EndTime = *s.EndTime
		}
	}
	return result
}

// handleFailure handles task failure
func (e *Executor) handleFailure(sessionID string, task *Task, sse *SSEWriter, err error, errorCode string) {
	endTime := time.Now()
	duration := endTime.Sub(task.StartTime)
	durationStr := formatDuration(duration)

	// Save failed chat record to memory
	if e.memoryManager != nil {
		record := &memory.ChatRecord{
			ChatID:      task.ID,
			SessionID:   sessionID,
			Round:       task.Round,
			Timestamp:   endTime,
			Instruction: task.Instruction,
			Status:      "failed",
			Duration:    int(duration.Seconds()),
			Error:       &memory.Error{Code: errorCode, Message: err.Error()},
		}
		if saveErr := e.memoryManager.SaveChatRecord(sessionID, record); saveErr != nil {
			e.logger.Error("保存对话记录失败: " + saveErr.Error())
		}
	}

	sse.WriteCompleted("failed", durationStr, task.Round, nil, &StepError{Code: errorCode, Message: err.Error()}, "")
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