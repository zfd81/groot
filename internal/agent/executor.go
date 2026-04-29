package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
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
	Attachments     []string
	Caller          string
	Progress        *ProgressInfo
	Round           int
	HistoryMessages []memory.Message
	ModelName       string
}

// TaskError represents task error
type TaskError struct {
	Code    string
	Message string
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
	middlewares   []adk.ChatModelAgentMiddleware
	mcpManager    *mcp.Manager
	config        config.Config
	logger        *logger.Logger
}

// NewExecutor creates a new task executor
func NewExecutor(
	memMgr *memory.Manager,
	middlewares []adk.ChatModelAgentMiddleware,
	mcpMgr *mcp.Manager,
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		memoryManager: memMgr,
		middlewares:   middlewares,
		mcpManager:    mcpMgr,
		config:        cfg,
		logger:        log,
	}
}

// Execute starts task execution
func (e *Executor) Execute(sessionID string, task *Task, sse *SSEWriter, cancelCh chan struct{}) {
	// Read SESSION.md content
	sessionMdContent := ""
	if e.memoryManager != nil {
		content, err := e.memoryManager.GetSessionMdContent(sessionID)
		if err == nil {
			sessionMdContent = content
		}
	}

	// Create engine using eino
	engine := NewEngine(
		e.config.LLM,
		e.middlewares,
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

	// Run engine with simplified progress callback
	result, err := engine.Run(
		ctx,
		task.Instruction,
		task.Prompt,
		sessionMdContent,
		task.HistoryMessages,
		task.ModelName,
		&ProgressCallback{
			WriteThinking: func(content string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteThinking(content)
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
			WriteToolCalls: func(toolCalls []ToolCall) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteToolCalls(toolCalls)
				}
			},
			WriteFinish: func(reason string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteFinish(reason)
				}
			},
			WriteToolResult: func(toolCallID, toolName, content string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteToolResult(toolCallID, toolName, content)
				}
			},
			WriteDone: func() error {
				return sse.WriteDone()
			},
		},
	)

	// Calculate duration
	startTime := task.StartTime
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	// Determine final status
	ctxCancelled := ctx.Err() == context.Canceled

	// If execution failed (not cancelled), send error via SSE before saving to memory
	if err != nil && !ctxCancelled {
		e.logger.Error("Agent execution failed: " + err.Error())
		// Send error event to client via SSE
		if writeErr := sse.WriteError("execution_error", err.Error()); writeErr != nil {
			e.logger.Error("Failed to write SSE error: " + writeErr.Error())
		}
		if writeErr := sse.WriteDone(); writeErr != nil {
			e.logger.Error("Failed to write SSE done: " + writeErr.Error())
		}
	}

	// Save chat record to memory
	if e.memoryManager != nil {
		attachments := task.Attachments

		// 统一的状态判断（只判断一次）
		var chatStatus string
		var chatResult string
		var chatSteps []memory.Step
		var chatError *memory.Error

		if ctxCancelled {
			chatStatus = "cancelled"
		} else if err != nil {
			chatStatus = "failed"
			chatError = &memory.Error{Code: "execution_error", Message: err.Error()}
		} else if result != nil && result.Cancelled {
			chatStatus = "cancelled"
		} else if result != nil {
			chatStatus = "completed"
			chatResult = result.Content
			chatSteps = convertSteps(result.Steps)
		} else {
			chatStatus = "failed"
			chatError = &memory.Error{Code: "unknown_error", Message: "执行完成但无结果"}
		}

		// 构建 ChatRecord
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
			Status:      chatStatus,
			Result:      chatResult,
			Steps:       chatSteps,
			Error:       chatError,
		}

		if saveErr := e.memoryManager.SaveChatRecord(sessionID, record); saveErr != nil {
			e.logger.Error("保存对话记录失败: " + saveErr.Error())
		}

		// 构建 Message（status 等字段直接从 record 拷贝）
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
			Status:      chatStatus,
			Result:      chatResult,
			Error:       chatError,
		}

		if appendErr := e.memoryManager.AppendMessage(sessionID, msg); appendErr != nil {
			e.logger.Error("追加历史消息失败: " + appendErr.Error())
		}
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