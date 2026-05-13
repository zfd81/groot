package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	llmConfig   config.LLMConfig
	middlewares []adk.ChatModelAgentMiddleware
	mcpManager  *mcp.Manager
	reactConfig config.ReactConfig
	log         *logger.Logger
}

// NewEngine creates a new Agent Engine
func NewEngine(
	cfg config.LLMConfig,
	middlewares []adk.ChatModelAgentMiddleware,
	mcpMgr *mcp.Manager,
	reactCfg config.ReactConfig,
	log *logger.Logger,
) *Engine {
	return &Engine{
		llmConfig:   cfg,
		middlewares: middlewares,
		mcpManager:  mcpMgr,
		reactConfig: reactCfg,
		log:         log,
	}
}

// Run executes a task using eino's ChatModelAgent
func (e *Engine) Run(
	ctx context.Context,
	instruction string,
	prompt string,
	sessionMdContent string,
	historyMessages []memory.Message,
	modelName string,
	attachments []MultimodalContent,
	cb *ProgressCallback,
) (*RunResult, error) {
	// 1. Create ChatModel with per-call timeout
	stepTimeout := time.Duration(e.reactConfig.StepTimeout) * time.Second
	chatModel, err := llm.NewChatModel(ctx, e.llmConfig, modelName, stepTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}

	// 2. Build tools from MCP Manager
	tools := e.buildTools()

	// 3. Build system instruction
	systemInstruction := e.buildSystemInstruction(prompt, sessionMdContent)

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

	// Configure retry for transient LLM failures (network jitter, 5xx, timeouts)
	if e.reactConfig.ErrorRetry > 0 {
		agentConfig.ModelRetryConfig = &adk.ModelRetryConfig{
			MaxRetries: e.reactConfig.ErrorRetry,
		}
	}

	// Inject middlewares (skill middleware, etc.)
	if len(e.middlewares) > 0 {
		agentConfig.Handlers = e.middlewares
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
	msgs := e.buildMessageList(instruction, historyMessages, attachments)

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
		defer func() {
			if r := recover(); r != nil {
				e.log.Error(fmt.Sprintf("Agent event reader panic: %v", r))
			}
		}()
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
				if strings.Contains(event.Err.Error(), "NodeRunError") ||
					strings.Contains(event.Err.Error(), "connection refused") ||
					strings.Contains(event.Err.Error(), "dial tcp") ||
					strings.Contains(event.Err.Error(), "no such host") ||
					strings.Contains(event.Err.Error(), "timeout") {
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

						// Send tool_calls (skip streaming artifacts with empty name and no ID)
						if len(msg.ToolCalls) > 0 {
							toolCalls := convertToolCalls(msg.ToolCalls)
							if len(toolCalls) > 0 && cb.WriteToolCalls != nil {
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

				// Handle non-streaming response (only when no streaming was done)
				} else if msgOutput.Message != nil {
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
						if len(toolCalls) > 0 && cb.WriteToolCalls != nil {
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

	// Handle cancellation: send cancelled event + [DONE], then return
	if agentCancelled || ctx.Err() == context.Canceled {
		if cb.WriteFinish != nil {
			cb.WriteFinish("cancel")
		}
		if cb.WriteDone != nil {
			cb.WriteDone()
		}
		return &RunResult{Content: "", Steps: steps, Cancelled: true}, nil
	}

	// Send [DONE] for normal completion
	if cb.WriteDone != nil {
		cb.WriteDone()
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

// convertToolCalls converts eino ToolCalls to SSE ToolCalls format,
// filtering out streaming artifacts that carry no useful content.
func convertToolCalls(tcs []schema.ToolCall) []ToolCall {
	result := make([]ToolCall, 0, len(tcs))
	for _, tc := range tcs {
		if tc.Function.Name == "" && tc.ID == "" && tc.Function.Arguments == "" {
			continue
		}
		result = append(result, ToolCall{
			Index:    tc.Index,
			ID:       tc.ID,
			Type:     tc.Type,
			Function: FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return result
}

// buildTools returns all eino tools from the MCP manager (via eino-ext)
func (e *Engine) buildTools() []tool.BaseTool {
	return e.mcpManager.GetTools()
}

// buildSystemInstruction builds the system prompt
func (e *Engine) buildSystemInstruction(prompt string, sessionMdContent string) string {
	sb := &strings.Builder{}

	// 1. GROOT.md（从全局缓存读取，放在最前面）
	grootMd := grootmd.GetContent()
	if grootMd != "" {
		sb.WriteString(grootMd)
		sb.WriteString("\n\n")
	}

	// 2. SESSION.md（会话文件目录提示）
	if sessionMdContent != "" {
		sb.WriteString(sessionMdContent)
		sb.WriteString("\n\n")
	}

	// 3. prompt（用户传入）
	if prompt != "" {
		sb.WriteString(prompt)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// buildMessageList builds message list with history context
// For multimodal attachments (image/audio/video), constructs UserInputMultiContent messages
func (e *Engine) buildMessageList(instruction string, historyMessages []memory.Message, attachments []MultimodalContent) []adk.Message {
	msgs := []adk.Message{}

	for _, hMsg := range historyMessages {
		if hMsg.Instruction != "" {
			msgs = append(msgs, schema.UserMessage(hMsg.Instruction))
		}
		if hMsg.Result != "" {
			msgs = append(msgs, schema.AssistantMessage(hMsg.Result, nil))
		}
	}

	msgs = append(msgs, e.buildUserMessage(instruction, attachments))

	return msgs
}

// buildUserMessage builds a user message.
// When attachments exist, all are sent as Base64 data URLs to the LLM via UserInputMultiContent.
func (e *Engine) buildUserMessage(instruction string, attachments []MultimodalContent) *schema.Message {
	// If no attachments, return plain text message
	if len(attachments) == 0 {
		return schema.UserMessage(instruction)
	}

	// Separate attachments into multimodal (image/audio/video) and text-based (file)
	multimodalAtts := make([]MultimodalContent, 0, len(attachments))
	fileAtts := make([]MultimodalContent, 0, len(attachments))
	for _, att := range attachments {
		switch att.Type {
		case "image", "audio", "video":
			multimodalAtts = append(multimodalAtts, att)
		default:
			fileAtts = append(fileAtts, att)
		}
	}

	// If no multimodal attachments, return plain text with file contents appended
	if len(multimodalAtts) == 0 {
		text := instruction
		for _, att := range fileAtts {
			text += "\n\n" + att.Name + " 的文件内容如下：\n" + att.DecodedContent
		}
		return schema.UserMessage(text)
	}

	// Build multimodal message with UserInputMultiContent
	parts := make([]schema.MessageInputPart, 0, len(multimodalAtts)+len(fileAtts)+1)

	// Add instruction text first, with any file contents appended
	instructionText := instruction
	for _, att := range fileAtts {
		instructionText += "\n\n" + att.Name + " 的文件内容如下：\n" + att.DecodedContent
	}
	parts = append(parts, schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeText,
		Text: instructionText,
	})

	// Add each multimodal attachment as Base64 data URL
	for _, att := range multimodalAtts {
		mimeType := att.MIMEType
		if mimeType == "" {
			mimeType = defaultMIMEType(att.Type)
		}
		dataURL := "data:" + mimeType + ";base64," + att.Base64Data

		switch att.Type {
		case "image":
			parts = append(parts, schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{
						URL: toPtr(dataURL),
					},
					Detail: schema.ImageURLDetailAuto,
				},
			})
		case "audio":
			parts = append(parts, schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeAudioURL,
				Audio: &schema.MessageInputAudio{
					MessagePartCommon: schema.MessagePartCommon{
						URL: toPtr(dataURL),
					},
				},
			})
		case "video":
			parts = append(parts, schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeVideoURL,
				Video: &schema.MessageInputVideo{
					MessagePartCommon: schema.MessagePartCommon{
						URL: toPtr(dataURL),
					},
				},
			})
		}
	}

	return &schema.Message{
		Role:                  schema.User,
		UserInputMultiContent: parts,
	}
}

func defaultMIMEType(attType string) string {
	switch attType {
	case "image":
		return "image/png"
	case "audio":
		return "audio/wav"
	case "video":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

func toPtr(s string) *string {
	return &s
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
