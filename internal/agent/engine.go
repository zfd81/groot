package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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

// ProgressCallback handles SSE event callbacks with structured data.
// 所有 Write* 函数（除 WriteDone）首参数 agentName 非空时表示事件来自子 Agent；
// 主 Agent 自身事件应传 ""，保持向后兼容（不注入 agent_name 字段）。
type ProgressCallback struct {
	WriteThinking   func(agentName, content string) error
	WriteMessage    func(agentName, content string) error
	WriteToolCalls  func(agentName string, toolCalls []ToolCall) error
	WriteFinish     func(agentName, reason string) error
	WriteToolResult func(agentName, toolCallID, toolName, content string, isError bool) error
	WriteError      func(agentName, message string) error
	WriteDone       func() error
}

// Engine wraps eino's ChatModelAgent for task execution
type Engine struct {
	llmConfig          config.LLMConfig
	middlewares        []adk.ChatModelAgentMiddleware
	mcpManager         *mcp.Manager
	extraTools         []tool.BaseTool // 追加到 mcpManager.GetTools() 之后，用于 call_agent
	reactConfig        config.ReactConfig
	log                *logger.Logger
	agentName          string // MainAgentName 或子 Agent 名（Solo 模式）
	emitInternalEvents bool   // 主 Agent 路径打开以透传子 Agent 事件
	tokenAccumulators  *TokenAccumulators
}

// EngineConfig 是 NewEngine 的命名参数集合，避免位置参数过多。
type EngineConfig struct {
	LLM                config.LLMConfig
	Middlewares        []adk.ChatModelAgentMiddleware
	MCP                *mcp.Manager
	ExtraTools         []tool.BaseTool
	React              config.ReactConfig
	Log                *logger.Logger
	AgentName          string
	EmitInternalEvents bool
	TokenAccumulators  *TokenAccumulators
}

// NewEngine creates a new Agent Engine.
// 若 cfg.AgentName 为空字符串，自动回退为 MainAgentName，
// 调用方在「主 Agent / 默认场景」下可省略该字段。
func NewEngine(cfg EngineConfig) *Engine {
	name := cfg.AgentName
	if name == "" {
		name = MainAgentName
	}
	return &Engine{
		llmConfig:          cfg.LLM,
		middlewares:        cfg.Middlewares,
		mcpManager:         cfg.MCP,
		extraTools:         cfg.ExtraTools,
		reactConfig:        cfg.React,
		log:                cfg.Log,
		agentName:          name,
		emitInternalEvents: cfg.EmitInternalEvents,
		tokenAccumulators:  cfg.TokenAccumulators,
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
	agentMdContent string,
) (*RunResult, error) {
	// 0. 把父 modelName 放进 ctx —— call_agent 工具运行时通过它把同一个 model
	// 透传给子 Agent，保证编排模式下子 Agent 跟随主 Agent 当前选定的 model。
	ctx = WithParentModel(ctx, modelName)

	// 1. Create ChatModel with per-call timeout
	stepTimeout := time.Duration(e.reactConfig.StepTimeout) * time.Second
	chatModel, err := llm.NewChatModel(ctx, e.llmConfig, modelName, stepTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}

	// 2. Build tools from MCP Manager
	tools := e.buildTools()

	// 3. Build system instruction
	systemInstruction := e.buildSystemInstruction(prompt, sessionMdContent, agentMdContent)

	// 4. Create ChatModelAgent config
	maxIter := e.reactConfig.MaxIterations
	if maxIter <= 0 {
		maxIter = 20 // default
	}

	agentConfig := &adk.ChatModelAgentConfig{
		Name:          e.agentName,
		Description:   "Groot AI Task Execution Agent",
		Instruction:   systemInstruction,
		Model:         chatModel,
		MaxIterations: maxIter,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
			EmitInternalEvents: e.emitInternalEvents,
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
				errStr := event.Err.Error()
				e.log.Error("Agent event error: " + errStr)

				// 在错误分支中也提取 agentName，主 Agent 自身事件折叠为空串保持向后兼容
				errEventAgentName := event.AgentName
				if errEventAgentName == e.agentName {
					errEventAgentName = ""
				}

				// MCP tool execution errors → send as tool_result so agent can continue
				if strings.Contains(errStr, "mcp") || strings.Contains(errStr, "command_not_allowed") {
					toolCallID := extractToolCallID(errStr)
					if cb.WriteToolResult != nil {
						cb.WriteToolResult(errEventAgentName, toolCallID, "mcp_tool", "MCP 工具错误: "+formatMCPError(errStr), true)
					}
					continue
				}

				// LLM connection errors → fatal
				if strings.Contains(errStr, "connection refused") ||
					strings.Contains(errStr, "dial tcp") ||
					strings.Contains(errStr, "no such host") ||
					strings.Contains(errStr, "timeout") {
					if cb.WriteError != nil {
						cb.WriteError(errEventAgentName, "LLM 服务连接失败: "+errStr)
					}
					return nil, fmt.Errorf("LLM 服务连接失败: %w", event.Err)
				}

				// Other NodeRunError → send error event but continue
				if strings.Contains(errStr, "NodeRunError") {
					if cb.WriteError != nil {
						cb.WriteError(errEventAgentName, errStr)
					}
					continue
				}
				continue
			}

			if event.Output == nil || event.Output.MessageOutput == nil {
				continue
			}

			msgOutput := event.Output.MessageOutput
			eventAgentName := event.AgentName
			// 主 Agent 自身事件折叠为空串：SSE 不注入 agent_name，保持向后兼容
			sseAgentName := eventAgentName
			if sseAgentName == e.agentName {
				sseAgentName = ""
			}

			// Handle Tool role (tool result from MCP execution)
			if msgOutput.Role == schema.Tool {
				e.processToolEvent(sseAgentName, event, cb, &steps)
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
								cb.WriteThinking(sseAgentName, msg.ReasoningContent)
							}
						}

						// Send content (message)
						if msg.Content != "" && msg.ReasoningContent == "" {
							if cb.WriteMessage != nil {
								cb.WriteMessage(sseAgentName, msg.Content)
							}
							finalResult += msg.Content
						}

						// Send tool_calls (skip streaming artifacts with empty name and no ID)
						if len(msg.ToolCalls) > 0 {
							toolCalls := convertToolCalls(msg.ToolCalls)
							if len(toolCalls) > 0 && cb.WriteToolCalls != nil {
								cb.WriteToolCalls(sseAgentName, toolCalls)
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
								cb.WriteFinish(sseAgentName, msg.ResponseMeta.FinishReason)
							}
							e.accumulateUsageIfChild(ctx, eventAgentName, msg.ResponseMeta)
						}
					}
					stream.Close()

				// Handle non-streaming response (only when no streaming was done)
				} else if msgOutput.Message != nil {
					msg := msgOutput.Message

					if msg.ReasoningContent != "" {
						if cb.WriteThinking != nil {
							cb.WriteThinking(sseAgentName, msg.ReasoningContent)
						}
					}

					if msg.Content != "" && msg.ReasoningContent == "" {
						if cb.WriteMessage != nil {
							cb.WriteMessage(sseAgentName, msg.Content)
						}
						finalResult = msg.Content
					}

					if len(msg.ToolCalls) > 0 {
						toolCalls := convertToolCalls(msg.ToolCalls)
						if len(toolCalls) > 0 && cb.WriteToolCalls != nil {
							cb.WriteToolCalls(sseAgentName, toolCalls)
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
							cb.WriteFinish(sseAgentName, msg.ResponseMeta.FinishReason)
						}
						e.accumulateUsageIfChild(ctx, eventAgentName, msg.ResponseMeta)
					}
				}
			}
		}
	}

	// Handle cancellation: send [DONE], then return
	if agentCancelled || ctx.Err() == context.Canceled {
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

// processToolEvent processes Tool role events.
// eventAgentName 已由调用方完成「主 Agent 折叠为空串」处理，可直接透传给 SSE。
func (e *Engine) processToolEvent(eventAgentName string, event *adk.AgentEvent, cb *ProgressCallback, steps *[]StepRecord) {
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
		if err := cb.WriteToolResult(eventAgentName, toolCallID, toolName, output, false); err != nil {
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
			Extra: tc.Extra,
		})
	}
	return result
}

// buildTools returns mcp tools followed by extraTools (e.g., call_agent for orchestrated mode).
// 主动拷贝以避免对 mcpManager.GetTools() 内部 slice 的隐式依赖——即使将来 GetTools 改为复用底层数组，
// 这里也不会污染原始 slice。
func (e *Engine) buildTools() []tool.BaseTool {
	base := e.mcpManager.GetTools()
	tools := make([]tool.BaseTool, 0, len(base)+len(e.extraTools))
	tools = append(tools, base...)
	if len(e.extraTools) > 0 {
		tools = append(tools, e.extraTools...)
	}
	return tools
}

// buildSystemInstruction builds the system prompt.
// Solo 模式（agentMdContent 非空）：用 agent.md 替换 GROOT.md；
// 编排/主 Agent 模式：保留原有 GROOT.md 注入。
func (e *Engine) buildSystemInstruction(prompt, sessionMdContent, agentMdContent string) string {
	sb := &strings.Builder{}

	if agentMdContent != "" {
		// Solo 模式：用 agent.md 替换 GROOT.md
		sb.WriteString(agentMdContent)
		sb.WriteString("\n\n")
	} else {
		// 1. GROOT.md（从全局缓存读取，放在最前面）
		grootMd := grootmd.GetContent()
		if grootMd != "" {
			sb.WriteString(grootMd)
			sb.WriteString("\n\n")
		}
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

// childChatIDKey 是 ctx 中存放子 Agent chatID 的 key 类型（unexported 防外部冲突）。
type childChatIDKey struct{}

// WithChildChatID 把子 Agent 的 chatID 注入 ctx；调用方一般是 CallAgentTool（Task 11）。
func WithChildChatID(ctx context.Context, chatID string) context.Context {
	return context.WithValue(ctx, childChatIDKey{}, chatID)
}

// accumulateUsageIfChild 当事件来自子 Agent 且携带 Usage 时累加。
// - eventAgentName 是事件源 Agent 名（adk.AgentEvent.AgentName）
// - 主 Agent 自己的事件不累加（避免误把外层 token 当成子 Agent 输入）
func (e *Engine) accumulateUsageIfChild(ctx context.Context, eventAgentName string, meta *schema.ResponseMeta) {
	if meta == nil || meta.Usage == nil {
		return
	}
	if eventAgentName == "" || eventAgentName == e.agentName {
		return
	}
	if e.tokenAccumulators == nil {
		return
	}
	childChatID, ok := ctx.Value(childChatIDKey{}).(string)
	if !ok || childChatID == "" {
		return
	}
	e.tokenAccumulators.Add(childChatID, meta.Usage.PromptTokens, meta.Usage.CompletionTokens)
}

// truncate truncates a string to max length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

var toolCallIDRegex = regexp.MustCompile(`tool call (\S+)`)

// formatMCPError parses the nested MCP error JSON and returns a readable error message.
// Falls back to extracting the tool_call_id and a short summary if parsing fails.
func formatMCPError(errStr string) string {
	// Find the JSON part: look for {"content": pattern
	jsonStart := strings.Index(errStr, `{"content":`)
	if jsonStart < 0 {
		// Fallback: look for "mcp server return error: " prefix
		if idx := strings.Index(errStr, "mcp server return error: "); idx >= 0 {
			jsonStart = idx + len("mcp server return error: ")
		}
	}
	if jsonStart < 0 {
		return shortMCPError(errStr)
	}

	jsonStr := errStr[jsonStart:]
	// Find matching closing brace
	if idx := strings.LastIndex(jsonStr, "}}"); idx > 0 {
		jsonStr = jsonStr[:idx+2]
	}

	var mcpErr struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &mcpErr); err != nil {
		return shortMCPError(errStr)
	}
	if len(mcpErr.Content) == 0 || mcpErr.Content[0].Text == "" {
		return shortMCPError(errStr)
	}

	var inner struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(mcpErr.Content[0].Text), &inner); err != nil {
		return shortMCPError(mcpErr.Content[0].Text)
	}

	if inner.Error.Message != "" {
		return fmt.Sprintf("%s (code: %s)", inner.Error.Message, inner.Error.Code)
	}
	return shortMCPError(errStr)
}

// shortMCPError extracts the tool_call_id from the error and returns a brief summary.
func shortMCPError(errStr string) string {
	id := extractToolCallID(errStr)
	if strings.Contains(errStr, "command_not_allowed") {
		return fmt.Sprintf("工具 %s 执行被拒绝 (code: command_not_allowed)", id)
	}
	if strings.Contains(errStr, "mcp") {
		return fmt.Sprintf("工具 %s MCP 调用失败", id)
	}
	return fmt.Sprintf("工具 %s 执行失败", id)
}

// extractToolCallID extracts the tool call ID from an error message.
func extractToolCallID(errStr string) string {
	match := toolCallIDRegex.FindStringSubmatch(errStr)
	if len(match) > 1 {
		return match[1]
	}
	return "unknown"
}

// RunResult holds the result of agent run
type RunResult struct {
	Content   string
	Steps     []StepRecord
	Cancelled bool
}
