package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/grootmd"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/skill"
)

// ProgressCallback handles SSE event callbacks with structured data
type ProgressCallback struct {
	WriteThinking   func(content string) error
	WriteMessage    func(content string) error
	WriteToolCalls  func(toolCalls []ToolCall) error
	WriteFinish     func(reason string) error
	WriteToolResult func(toolCallID, toolName, content string) error
	WriteDone       func() error
}

// Engine wraps eino's ChatModelAgent for task execution
type Engine struct {
	llmConfig      config.LLMConfig
	skillsRegistry *skill.Registry
	mcpManager     *mcp.Manager
	reactConfig    config.ReactConfig
	log            *logger.Logger
}

// NewEngine creates a new Agent Engine
func NewEngine(
	cfg config.LLMConfig,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	reactCfg config.ReactConfig,
	log *logger.Logger,
) *Engine {
	return &Engine{
		llmConfig:      cfg,
		skillsRegistry: skills,
		mcpManager:     mcpMgr,
		reactConfig:    reactCfg,
		log:            log,
	}
}

// Run executes a task using eino's ChatModelAgent
func (e *Engine) Run(
	ctx context.Context,
	instruction string,
	prompt string,
	attachmentPaths []AttachmentPath,
	historyMessages []memory.Message,
	modelName string,
	cb *ProgressCallback,
) (*RunResult, error) {
	// 1. Create ChatModel
	chatModel, err := llm.NewChatModel(ctx, e.llmConfig, modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}

	// 2. Build tools from MCP Manager
	tools := e.buildTools()

	// 3. Build system instruction with Skills
	systemInstruction := e.buildSystemInstruction(prompt)

	// 4. Create ChatModelAgent config
	maxIter := e.reactConfig.MaxIterations
	if maxIter <= 0 {
		maxIter = 20 // default
	}

	agentConfig := &adk.ChatModelAgentConfig{
		Name:          "GrootAgent",
		Description:   "Groot AI Task Execution Agent",
		Instruction:   systemInstruction,
		Model:         chatModel,
		MaxIterations: maxIter,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
		},
	}

	// 5. Create Agent
	agent, err := adk.NewChatModelAgent(ctx, agentConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	// 6. Create Runner with streaming enabled
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	// 7. Build message list with history context
	msgs := e.buildMessageList(instruction, attachmentPaths, historyMessages)

	// 8. Run agent and collect events
	iter := runner.Run(ctx, msgs)

	var finalResult string
	var steps []StepRecord
	var agentCancelled bool

	// Process events with cancellation support
	// Use a goroutine to read events asynchronously to avoid blocking
	eventCh := make(chan *adk.AgentEvent, 100)

	go func() {
		defer close(eventCh)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				event, ok := iter.Next()
				if !ok {
					return
				}
				select {
				case eventCh <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

eventLoop:
	for {
		select {
		case <-ctx.Done():
			agentCancelled = true
			break eventLoop
		case event, ok := <-eventCh:
			if !ok {
				// eventCh closed, iterator finished
				break eventLoop
			}

			// Process event
			if event.Err != nil {
				e.log.Error("Agent event error: " + event.Err.Error())
				// 检查是否是严重错误（如连接失败），应该返回给调用者
				// NodeRunError 表示节点执行失败，通常是 LLM 连接问题
				if strings.Contains(event.Err.Error(), "NodeRunError") ||
					strings.Contains(event.Err.Error(), "connection refused") ||
					strings.Contains(event.Err.Error(), "dial tcp") ||
					strings.Contains(event.Err.Error(), "no such host") ||
					strings.Contains(event.Err.Error(), "timeout") {
					// 直接返回错误，让 executor 统一处理 SSE 错误发送
					return nil, fmt.Errorf("LLM 服务连接失败: %w", event.Err)
				}
				continue
			}

			if event.Output == nil || event.Output.MessageOutput == nil {
				continue
			}

			msgOutput := event.Output.MessageOutput

			// Handle Tool role (tool result from MCP execution)
			if msgOutput.Role == schema.Tool {
				e.processToolEvent(event, cb, &steps)
				continue
			}

			// Handle Assistant role (LLM output)
			if msgOutput.Role == schema.Assistant {
				// Handle streaming response
				if msgOutput.IsStreaming && msgOutput.MessageStream != nil {
					stream := msgOutput.MessageStream
					for {
						msg, err := stream.Recv()
						if err != nil {
							break
						}
						if msg == nil {
							continue
						}

						// Send reasoning_content (thinking)
						if msg.ReasoningContent != "" {
							if cb.WriteThinking != nil {
								cb.WriteThinking(msg.ReasoningContent)
							}
						}

						// Send content (message)
						if msg.Content != "" && msg.ReasoningContent == "" {
							if cb.WriteMessage != nil {
								cb.WriteMessage(msg.Content)
							}
							finalResult += msg.Content
						}

						// Send tool_calls
						if len(msg.ToolCalls) > 0 {
							toolCalls := convertToolCalls(msg.ToolCalls)
							if cb.WriteToolCalls != nil {
								cb.WriteToolCalls(toolCalls)
							}
							for _, tc := range msg.ToolCalls {
								steps = append(steps, StepRecord{
									StepID:       tc.ID,
									Type:         "tool",
									Name:         tc.Function.Name,
									Status:       StatusRunning,
									NestingLevel: 0,
								})
							}
						}

						// Send finish_reason
						if msg.ResponseMeta != nil && msg.ResponseMeta.FinishReason != "" {
							if cb.WriteFinish != nil {
								cb.WriteFinish(msg.ResponseMeta.FinishReason)
							}
						}
					}
					stream.Close()
				}

				// Handle non-streaming response
				if msgOutput.Message != nil {
					msg := msgOutput.Message

					if msg.ReasoningContent != "" {
						if cb.WriteThinking != nil {
							cb.WriteThinking(msg.ReasoningContent)
						}
					}

					if msg.Content != "" && msg.ReasoningContent == "" {
						if cb.WriteMessage != nil {
							cb.WriteMessage(msg.Content)
						}
						finalResult = msg.Content
					}

					if len(msg.ToolCalls) > 0 {
						toolCalls := convertToolCalls(msg.ToolCalls)
						if cb.WriteToolCalls != nil {
							cb.WriteToolCalls(toolCalls)
						}
						for _, tc := range msg.ToolCalls {
							steps = append(steps, StepRecord{
								StepID:       tc.ID,
								Type:         "tool",
								Name:         tc.Function.Name,
								Status:       StatusRunning,
								NestingLevel: 0,
							})
						}
					}

					if msg.ResponseMeta != nil && msg.ResponseMeta.FinishReason != "" {
						if cb.WriteFinish != nil {
							cb.WriteFinish(msg.ResponseMeta.FinishReason)
						}
					}
				}
			}
		}
	}

	// Send [DONE] at the end
	if cb.WriteDone != nil {
		cb.WriteDone()
	}

	// Handle cancellation
	if agentCancelled || ctx.Err() == context.Canceled {
		return &RunResult{Content: "", Steps: steps, Cancelled: true}, nil
	}

	if finalResult == "" {
		finalResult = "任务执行完成，但未获得明确结果"
	}

	return &RunResult{Content: finalResult, Steps: steps}, nil
}

// processToolEvent processes Tool role events
func (e *Engine) processToolEvent(event *adk.AgentEvent, cb *ProgressCallback, steps *[]StepRecord) {
	msgOutput := event.Output.MessageOutput

	var toolCallID string
	var output string
	var toolName string

	if msgOutput.Message != nil {
		toolCallID = msgOutput.Message.ToolCallID
		output = msgOutput.Message.Content
	}
	if msgOutput.ToolName != "" {
		toolName = msgOutput.ToolName
	}

	e.log.Info(fmt.Sprintf("Tool result: toolName=%s, toolCallID=%s, outputLen=%d", toolName, toolCallID, len(output)))

	// Send tool_result event
	if cb.WriteToolResult != nil {
		if err := cb.WriteToolResult(toolCallID, toolName, output); err != nil {
			e.log.Error("SSE write tool_result failed: " + err.Error())
		}
	}

	// Update step status to completed
	for i := range *steps {
		if (*steps)[i].StepID == toolCallID {
			(*steps)[i].Status = StatusCompleted
		}
	}
}

// convertToolCalls converts eino ToolCalls to SSE ToolCalls format
func convertToolCalls(tcs []schema.ToolCall) []ToolCall {
	result := make([]ToolCall, len(tcs))
	for i, tc := range tcs {
		result[i] = ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		}
	}
	return result
}

// buildTools creates eino tools from MCP tools
func (e *Engine) buildTools() []tool.BaseTool {
	tools := []tool.BaseTool{}

	for _, toolInfo := range e.mcpManager.ListTools() {
		t := NewMCPToolAdapter(toolInfo, e.mcpManager, e.log)
		tools = append(tools, t)
	}

	return tools
}

// buildSystemInstruction builds the system prompt with Skills
func (e *Engine) buildSystemInstruction(prompt string) string {
	sb := &strings.Builder{}

	// 1. GROOT.md（从全局缓存读取，放在最前面）
	grootMd := grootmd.GetContent()
	if grootMd != "" {
		sb.WriteString(grootMd)
		sb.WriteString("\n\n")
	}

	// 2. prompt（用户传入）
	if prompt != "" {
		sb.WriteString(prompt)
		sb.WriteString("\n\n")
	}

	// 3. Skills 指令
	if e.skillsRegistry.Count() > 0 {
		sb.WriteString("可用技能 (专用任务模板):\n")
		for _, skill := range e.skillsRegistry.List() {
			sb.WriteString(fmt.Sprintf("\n## %s\n", skill.Name))
			sb.WriteString(fmt.Sprintf("描述: %s\n", skill.Description))
			sb.WriteString(fmt.Sprintf("\n指令:\n%s\n", skill.Instructions))
		}
		sb.WriteString("\n")
	}

	// 4. 执行规则
	sb.WriteString("执行规则:\n")
	sb.WriteString("1. 分析用户请求，判断需要使用哪个技能或工具\n")
	sb.WriteString("2. 如果有匹配的技能，按照技能指令执行\n")
	sb.WriteString("3. 使用工具时，按工具定义的参数格式传入参数\n")
	sb.WriteString("4. 观察执行结果，必要时继续调用工具\n")
	sb.WriteString("5. 完成任务后给出最终答案\n")

	return sb.String()
}

// buildUserMessage builds user message with attachment paths
func (e *Engine) buildUserMessage(instruction string, attachmentPaths []AttachmentPath) string {
	msg := instruction
	if len(attachmentPaths) > 0 {
		msg += "\n\n附件:\n"
		for _, att := range attachmentPaths {
			if att.FullPath != "" {
				msg += fmt.Sprintf("- %s (%s)\n  路径: %s\n  类型: %s\n  大小: %d bytes\n",
					att.OriginalName, att.Type, att.FullPath, att.ContentType, att.Size)
			} else if att.Type == "url" {
				msg += fmt.Sprintf("- %s (url)\n  URL: %s\n", att.OriginalName, att.RelativePath)
			} else {
				msg += fmt.Sprintf("- %s (%s)\n", att.OriginalName, att.Type)
			}
		}
	}
	return msg
}

// buildMessageList builds message list with history context
func (e *Engine) buildMessageList(instruction string, attachmentPaths []AttachmentPath, historyMessages []memory.Message) []adk.Message {
	msgs := []adk.Message{}

	for _, hMsg := range historyMessages {
		if hMsg.Instruction != "" {
			msgs = append(msgs, schema.UserMessage(hMsg.Instruction))
		}
		if hMsg.Result != "" {
			msgs = append(msgs, schema.AssistantMessage(hMsg.Result, nil))
		}
	}

	userMessage := e.buildUserMessage(instruction, attachmentPaths)
	msgs = append(msgs, schema.UserMessage(userMessage))

	return msgs
}

// truncate truncates a string to max length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// RunResult holds the result of agent run
type RunResult struct {
	Content   string
	Steps     []StepRecord
	Cancelled bool
}