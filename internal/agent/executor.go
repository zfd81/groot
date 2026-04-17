package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/skill"
	"github.com/zfd81/groot/internal/storage"
)

// Executor executes tasks with ReAct mode
type Executor struct {
	storage       storage.TaskStorage
	skillRegistry *skill.Registry
	mcpManager    *mcp.Manager
	cancelManager *CancelManager
	config        config.Config
	logger        *logger.Logger
	runningTasks  sync.Map
}

// NewExecutor creates a new task executor
func NewExecutor(
	store storage.TaskStorage,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	cancelMgr *CancelManager,
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		storage:       store,
		skillRegistry: skills,
		mcpManager:    mcpMgr,
		cancelManager: cancelMgr,
		config:        cfg,
		logger:        log,
	}
}

// Execute starts task execution
func (e *Executor) Execute(task *storage.Task, sse *SSEWriter, cancelCh chan struct{}) {
	e.runningTasks.Store(task.ID, true)
	defer e.runningTasks.Delete(task.ID)

	// Write intent event
	sse.WriteIntent()

	// Create context for tracking
	ctx := &ExecutionContext{
		Task:      task,
		SSE:       sse,
		CancelCh:  cancelCh,
		StepCount: 0,
		StartTime: time.Now(),
		Logger:    e.logger,
	}

	// Execute in ReAct loop
	result, err := e.reactLoop(ctx)

	// Calculate duration
	duration := time.Since(ctx.StartTime)
	durationStr := formatDuration(duration)

	// Update task in storage
	updates := map[string]interface{}{
		"status":   result.Status,
		"end_time": time.Now(),
		"duration": int(duration.Seconds()),
		"result":   result.Result,
		"steps":    ctx.Steps,
	}
	if err != nil {
		updates["error"] = &storage.TaskError{
			Code:    "execution_error",
			Message: err.Error(),
		}
	}
	e.storage.Update(task.ID, updates)

	// Write completed event
	var stepErr *StepError
	if err != nil {
		stepErr = &StepError{Code: "execution_error", Message: err.Error()}
	}
	sse.WriteCompleted(string(result.Status), durationStr, result.Result, stepErr, result.Message)

	// Unregister from cancel manager
	e.cancelManager.Unregister(task.ID)
}

// reactLoop implements ReAct execution
func (e *Executor) reactLoop(ctx *ExecutionContext) (*ExecutionResult, error) {
	maxIterations := e.config.React.MaxIterations

	for i := 0; i < maxIterations; i++ {
		// Check for cancellation
		select {
		case <-ctx.CancelCh:
			return &ExecutionResult{
				Status:  storage.StatusCancelled,
				Message: "用户主动取消",
			}, nil
		default:
		}

		// Step 1: Reasoning (LLM decides next action)
		stepID := GenerateStepID()
		ctx.StepCount++

		// For MVP: Simple execution - just run LLM to process task
		ctx.SSE.WriteStepStart(stepID, "llm", "reasoning", 0, nil)

		// Simulate LLM processing (placeholder for actual eino integration)
		// In production, this would call eino agent
		progressCh := make(chan string, 10)
		go func() {
			progressCh <- "正在分析任务..."
			time.Sleep(500 * time.Millisecond)
			progressCh <- "正在生成回答..."
			close(progressCh)
		}()

		for msg := range progressCh {
			select {
			case <-ctx.CancelCh:
				ctx.SSE.WriteStepEnd(stepID, "cancelled", nil)
				return &ExecutionResult{Status: storage.StatusCancelled, Message: "用户主动取消"}, nil
			default:
				ctx.SSE.WriteProgress(stepID, msg)
			}
		}

		// Simulate completion
		result := map[string]interface{}{
			"analysis": "任务已完成",
			"output":   fmt.Sprintf("处理指令: %s", ctx.Task.Instruction),
		}

		ctx.SSE.WriteStepEnd(stepID, "success", nil)

		// Record step
		ctx.Steps = append(ctx.Steps, storage.StepRecord{
			StepID:       stepID,
			Type:         "llm",
			Name:         "reasoning",
			StartTime:    time.Now().Add(-2 * time.Second),
			EndTime:      time.Now(),
			Status:       storage.StatusCompleted,
			NestingLevel: 0,
		})

		// For MVP: Complete after first iteration
		return &ExecutionResult{
			Status: storage.StatusCompleted,
			Result: result,
		}, nil
	}

	// Max iterations reached
	return &ExecutionResult{
		Status:  storage.StatusFailed,
		Message: "达到最大循环次数",
	}, fmt.Errorf("max iterations reached")
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

// ExecutionContext holds execution context
type ExecutionContext struct {
	Task      *storage.Task
	SSE       *SSEWriter
	CancelCh  chan struct{}
	StepCount int
	StartTime time.Time
	Steps     []storage.StepRecord
	Logger    *logger.Logger
}

// ExecutionResult holds execution result
type ExecutionResult struct {
	Status  storage.TaskStatus
	Result  interface{}
	Message string
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
