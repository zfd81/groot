package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	memoryManager *memory.Manager
	skillRegistry *skill.Registry
	mcpManager    *mcp.Manager
	config        config.Config
	logger        *logger.Logger
	runningTasks  sync.Map
}

// NewExecutor creates a new task executor
func NewExecutor(
	memMgr *memory.Manager,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		memoryManager: memMgr,
		skillRegistry: skills,
		mcpManager:    mcpMgr,
		config:        cfg,
		logger:        log,
	}
}

// Execute starts task execution
func (e *Executor) Execute(sessionID string, task *Task, sse *SSEWriter, cancelCh chan struct{}) {
	e.runningTasks.Store(task.ID, true)
	defer e.runningTasks.Delete(task.ID)

	// Build attachment paths from already-processed attachments
	// Note: Attachments are already processed by chat handler (Base64 decoded and saved)
	var attachmentPaths []AttachmentPath
	for _, att := range task.Attachments {
		attachmentPaths = append(attachmentPaths, AttachmentPath{
			OriginalName: att.Name,
			Type:         att.Type,
			FullPath:     att.Content, // Content is already the saved file path
			RelativePath: att.Content,
			Size:         0, // Size info not available at this stage
			ContentType:  getContentTypeFromType(att.Type),
		})
	}

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
		&ProgressCallback{
			WriteThinkingStart: func(stepID string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteThinkingStart(stepID)
				}
			},
			WriteThinking: func(content string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteThinking(content)
				}
			},
			WriteThinkingEnd: func(stepID, status string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteThinkingEnd(stepID, status)
				}
			},
			WriteToolCall: func(stepID, name string, arguments map[string]interface{}) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteToolCall(stepID, name, arguments)
				}
			},
			WriteToolResult: func(stepID, output, errStr string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteToolResult(stepID, output, errStr)
				}
			},
			WriteMessageStart: func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteMessageStart()
				}
			},
			WriteMessage: func(content string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteMessage(content)
				}
			},
			WriteMessageEnd: func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteMessageEnd()
				}
			},
		},
	)

	// Calculate duration
	startTime := task.StartTime
	endTime := time.Now()
	duration := endTime.Sub(startTime)
	durationStr := formatDuration(duration)

	// Determine final status - check cancellation FIRST before other conditions
	// Context might be cancelled after engine.Run returns but before we check
	ctxCancelled := ctx.Err() == context.Canceled

	// Save chat record to memory
	if e.memoryManager != nil {
		// Build attachments list
		attachments := []string{}
		for _, att := range task.Attachments {
			attachments = append(attachments, att.Name)
		}

		record := &memory.ChatRecord{
			ChatID:      task.ID,
			SessionID:   sessionID,
			Round:       task.Round,
			Timestamp:   endTime,
			StartedAt:   startTime,
			EndedAt:     endTime,
			Instruction: task.Instruction,
			Duration:    int(duration.Seconds()),
			Attachments: attachments,
		}

		// Check cancellation status FIRST (highest priority)
		if ctxCancelled {
			record.Status = "cancelled"
		} else if err != nil {
			record.Status = "failed"
			record.Error = &memory.Error{
				Code:    "execution_error",
				Message: err.Error(),
			}
		} else if result != nil && result.Cancelled {
			record.Status = "cancelled"
		} else if result != nil {
			record.Status = "completed"
			record.Result = result.Content
			record.Steps = convertSteps(result.Steps)
		} else {
			record.Status = "failed"
			record.Error = &memory.Error{
				Code:    "unknown_error",
				Message: "执行完成但无结果",
			}
		}

		// Save chat record
		if saveErr := e.memoryManager.SaveChatRecord(sessionID, record); saveErr != nil {
			e.logger.Error("保存对话记录失败: " + saveErr.Error())
		}

		// Append message to history
		// Always save message regardless of status
		var stepsCount int
		if result != nil {
			stepsCount = len(result.Steps)
		}

		msg := &memory.Message{
			ChatID:      task.ID,
			Round:       task.Round,
			Timestamp:   endTime,
			Instruction: task.Instruction,
			Attachments: attachments,
			Duration:    int(duration.Seconds()),
			StepsCount:  stepsCount,
		}

		// Check cancellation status FIRST (highest priority)
		if ctxCancelled {
			msg.Status = "cancelled"
		} else if err != nil {
			msg.Status = "failed"
			msg.Error = &memory.Error{
				Code:    "execution_error",
				Message: err.Error(),
			}
		} else if result != nil && result.Cancelled {
			msg.Status = "cancelled"
		} else if result != nil {
			msg.Status = "completed"
			msg.Result = result.Content
		} else {
			msg.Status = "failed"
			msg.Error = &memory.Error{
				Code:    "unknown_error",
				Message: "执行完成但无结果",
			}
		}

		if appendErr := e.memoryManager.AppendMessage(sessionID, msg); appendErr != nil {
			e.logger.Error("追加历史消息失败: " + appendErr.Error())
		}
	}

	// Send SSE completed event - check cancellation status FIRST
	if ctxCancelled {
		sse.WriteCompleted("cancelled", durationStr, nil, nil, "用户主动取消")
	} else if err != nil {
		sse.WriteCompleted("failed", durationStr, nil, &StepError{Code: "execution_error", Message: err.Error()}, "")
	} else if result != nil && result.Cancelled {
		sse.WriteCompleted("cancelled", durationStr, nil, nil, "用户主动取消")
	} else if result != nil {
		sse.WriteCompleted("success", durationStr, result.Content, nil, "")
	} else {
		sse.WriteCompleted("failed", durationStr, nil, &StepError{Code: "unknown_error", Message: "执行完成但无结果"}, "")
	}
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

	// Build attachments list
	attachments := []string{}
	for _, att := range task.Attachments {
		attachments = append(attachments, att.Name)
	}

	// Save failed chat record to memory
	if e.memoryManager != nil {
		record := &memory.ChatRecord{
			ChatID:      task.ID,
			SessionID:   sessionID,
			Round:       task.Round,
			Timestamp:   endTime,
			StartedAt:   task.StartTime,
			EndedAt:     endTime,
			Instruction: task.Instruction,
			Attachments: attachments,
			Status:      "failed",
			Duration:    int(duration.Seconds()),
			Error:       &memory.Error{Code: errorCode, Message: err.Error()},
		}
		if saveErr := e.memoryManager.SaveChatRecord(sessionID, record); saveErr != nil {
			e.logger.Error("保存对话记录失败: " + saveErr.Error())
		}
	}

	sse.WriteCompleted("failed", durationStr, nil, &StepError{Code: errorCode, Message: err.Error()}, "")
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

// getContentTypeFromType returns content type based on attachment type
func getContentTypeFromType(attType string) string {
	switch attType {
	case "file":
		return "application/octet-stream"
	case "image":
		return "image/png"
	case "url":
		return "url"
	case "text":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
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