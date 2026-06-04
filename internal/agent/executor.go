package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"go.uber.org/zap"

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

// MultimodalContent carries attachment data for multimodal LLM processing
type MultimodalContent struct {
	Type           string // image, audio, video, file
	Name           string
	MIMEType       string // e.g. "image/png", "audio/wav"
	Base64Data     string // Base64 encoded binary data (for image/audio/video)
	DecodedContent string // decoded text content (for file type, already base64-decoded)
}

// Task represents a task (temporary definition until memory module)
type Task struct {
	ID                  string
	Instruction         string
	Prompt              string
	Status              TaskStatus
	StartTime           time.Time
	EndTime             *time.Time
	Duration            int
	Result              string
	Error               *TaskError
	Steps               []StepRecord
	Attachments         []string
	MultiModalContents  []MultimodalContent // carried for multimodal LLM processing
	Caller              string
	Progress            *ProgressInfo
	Round               int
	HistoryMessages     []memory.Message
	ModelName           string
	AgentName           string // Solo 模式时为子 Agent 名；编排模式空字符串
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
	homeDir           string // GROOT_HOME 目录
	memoryManager     *memory.Manager
	middlewares       []adk.ChatModelAgentMiddleware
	mcpManager        *mcp.Manager
	subAgentRegistry  *SubAgentRegistry
	tokenAccumulators *TokenAccumulators
	runtimeState      *RuntimeState
	config            config.Config
	logger            *logger.Logger
}

// NewExecutor creates a new task executor
func NewExecutor(
	homeDir string,
	memMgr *memory.Manager,
	middlewares []adk.ChatModelAgentMiddleware,
	mcpMgr *mcp.Manager,
	subAgentReg *SubAgentRegistry,
	runtime *RuntimeState,
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		homeDir:           homeDir,
		memoryManager:     memMgr,
		middlewares:       middlewares,
		mcpManager:        mcpMgr,
		subAgentRegistry:  subAgentReg,
		tokenAccumulators: NewTokenAccumulators(),
		runtimeState:      runtime,
		config:            cfg,
		logger:            log,
	}
}

// Execute starts task execution
func (e *Executor) Execute(parentCtx context.Context, sessionID string, task *Task, sse *SSEWriter) {
	// Read SESSION.md content
	sessionMdContent := ""
	if e.memoryManager != nil {
		content, err := e.memoryManager.GetSessionMdContent(sessionID)
		if err == nil {
			sessionMdContent = content
		}
	}

	// 区分 Solo / 编排模式：
	// - Solo：task.AgentName 指向已注册子 Agent，使用其 Instruction/MCP/Skill 直接执行
	// - 编排（默认/主 Agent）：挂载 call_agent 工具 + 打开 EmitInternalEvents
	var (
		agentName      = MainAgentName
		agentMdContent = ""
		mcpMgr         = e.mcpManager
		middlewares    = e.middlewares
		extraTools     []tool.BaseTool
		emitInternal   = false
		soloErr        error
	)

	if task.AgentName != "" && task.AgentName != MainAgentName {
		// Solo 模式
		agentName = task.AgentName
		if e.subAgentRegistry == nil {
			soloErr = fmt.Errorf("子 Agent 注册表未初始化")
		} else if entry, ok := e.subAgentRegistry.Get(task.AgentName); !ok {
			soloErr = fmt.Errorf("子 Agent 不存在: %s", task.AgentName)
		} else {
			agentMdContent = entry.Instruction
			mcpMgr = entry.MCPManager
			// agent.md 显式声明的 model 在 Solo 模式下覆盖 task.ModelName，
			// 与 BuildAgentTool 中的 AgentMdModel 优先级语义一致。
			if entry.AgentMdModel != "" {
				task.ModelName = entry.AgentMdModel
			}
			if entry.SkillBK != nil {
				mw, err := einoskill.NewMiddleware(parentCtx, &einoskill.Config{Backend: entry.SkillBK})
				if err == nil {
					middlewares = []adk.ChatModelAgentMiddleware{mw}
				} else {
					// skill 中间件构建失败时降级为「无 skill」继续执行子 Agent，
					// 用户能感知到 skill 未生效，所以必须明确 log。
					e.logger.Error("子 Agent skill 中间件创建失败",
						zap.String("agent", task.AgentName),
						zap.Error(err))
					middlewares = nil
				}
			} else {
				middlewares = nil
			}
			// Solo 模式不挂 call_agent；emitInternal 保持 false
		}
	} else {
		// 编排模式 - 主 Agent；当 registry 不为 nil 时才挂 call_agent
		if e.subAgentRegistry != nil {
			execTimeout, _ := time.ParseDuration(e.config.SubAgent.ExecTimeout)
			if execTimeout <= 0 {
				execTimeout = 5 * time.Minute
			}
			callAgent := NewCallAgentTool(CallAgentToolConfig{
				Registry:          e.subAgentRegistry,
				ParentChatID:      task.ID,
				SessionID:         sessionID,
				MaxTaskLen:        e.config.SubAgent.MaxTaskLength,
				MaxResultLen:      e.config.SubAgent.MaxResultLength,
				ExecTimeout:       execTimeout,
				Memory:            e.memoryManager,
				RuntimeState:      e.runtimeState,
				TokenAccumulators: e.tokenAccumulators,
				Log:               e.logger,
				ParentRound:       task.Round,
			})
			extraTools = []tool.BaseTool{callAgent}
			emitInternal = true
		}
	}

	// Solo 校验失败时直接走错误持久化路径，不创建 engine
	if soloErr != nil {
		sse.WriteError(agentName, soloErr.Error())
		sse.WriteDone()
		if e.memoryManager != nil {
			endTime := time.Now()
			record := &memory.ChatRecord{
				ChatID:      task.ID,
				SessionID:   sessionID,
				Round:       task.Round,
				Timestamp:   endTime,
				StartedAt:   task.StartTime,
				EndedAt:     endTime,
				Instruction: task.Instruction,
				Duration:    int(endTime.Sub(task.StartTime).Seconds()),
				Attachments: task.Attachments,
				Status:      "failed",
				Error:       &memory.Error{Code: "subagent_unavailable", Message: soloErr.Error()},
				AgentName:   task.AgentName,
			}
			if saveErr := e.memoryManager.SaveChatRecord(sessionID, record); saveErr != nil {
				e.logger.Error("保存对话记录失败: " + saveErr.Error())
			}
			msg := &memory.Message{
				ChatID:      task.ID,
				Round:       task.Round,
				Timestamp:   endTime,
				Instruction: task.Instruction,
				Attachments: task.Attachments,
				Duration:    int(endTime.Sub(task.StartTime).Seconds()),
				Status:      "failed",
				AgentName:   task.AgentName,
				Error:       &memory.Error{Code: "subagent_unavailable", Message: soloErr.Error()},
			}
			if appendErr := e.memoryManager.AppendMessage(sessionID, msg); appendErr != nil {
				e.logger.Error("追加历史消息失败: " + appendErr.Error())
			}
		}
		task.Status = StatusFailed
		return
	}

	// Create engine using eino
	engine := NewEngine(EngineConfig{
		LLM:                e.config.LLM,
		HomeDir:            e.homeDir,
		Middlewares:        middlewares,
		MCP:                mcpMgr,
		ExtraTools:         extraTools,
		React:              e.config.React,
		Log:                e.logger,
		AgentName:          agentName,
		EmitInternalEvents: emitInternal,
		TokenAccumulators:  e.tokenAccumulators,
	})

	// Create context derived from parent (SSE disconnect will cancel parentCtx)
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// Run engine with simplified progress callback
	result, err := engine.Run(
		ctx,
		task.Instruction,
		task.Prompt,
		sessionMdContent,
		task.HistoryMessages,
		task.ModelName,
		task.MultiModalContents,
		&ProgressCallback{
			WriteThinking: func(agentName, content string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteThinking(agentName, content)
				}
			},
			WriteMessage: func(agentName, content string) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteMessage(agentName, content)
				}
			},
			WriteToolCalls: func(agentName string, toolCalls []ToolCall) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteToolCalls(agentName, toolCalls)
				}
			},
			WriteFinish: func(agentName, reason string) error {
				return sse.WriteFinish(agentName, reason)
			},
			WriteToolResult: func(agentName, toolCallID, toolName, content string, isError bool) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return sse.WriteToolResult(agentName, toolCallID, toolName, content, isError)
				}
			},
			WriteError: func(agentName, message string) error {
				return sse.WriteError(agentName, message)
			},
			WriteDone: func() error {
				return sse.WriteDone()
			},
		},
		agentMdContent,
	)

	// Calculate duration
	startTime := task.StartTime
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	// Determine final status
	ctxCancelled := ctx.Err() == context.Canceled

	// If execution failed (not cancelled), close SSE stream
	if err != nil && !ctxCancelled {
		e.logger.Error("Agent execution failed: " + err.Error())
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
			AgentName:   task.AgentName,
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
			AgentName:   task.AgentName,
			Error:       chatError,
		}

		if appendErr := e.memoryManager.AppendMessage(sessionID, msg); appendErr != nil {
			e.logger.Error("追加历史消息失败: " + appendErr.Error())
		}

		// 回写最终状态到 task，供调用方（chat handler）查询
		task.Status = TaskStatus(chatStatus)
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