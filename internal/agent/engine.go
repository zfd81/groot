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
// 所有 Write* 函数（除 WriteUsage、WriteDone）首参数 agentName 非空时表示事件来自子 Agent；
// 主 Agent 自身事件应传 ""，保持向后兼容（不注入 agent_name 字段）。
type ProgressCallback struct {
	WriteThinking   func(agentName, content string) error
	WriteMessage    func(agentName, content string) error
	WriteToolCalls  func(agentName string, toolCalls []ToolCall) error
	WriteFinish     func(agentName, reason string) error
	WriteToolResult func(agentName, toolCallID, toolName, content string, isError bool) error
	WriteError      func(agentName, message string) error
	// WriteUsage 下发本轮（主 Agent + 全部子 Agent）汇总的 token 用量。
	// 仅在全轮 Total > 0 时调用一次，位于 WriteDone 之前；错误路径不调用。
	WriteUsage func(usage TokenUsage) error
	WriteDone  func() error
}

// AgentErrorToolName 是可恢复的节点运行错误以 tool_result 形式下发时所用的工具名。
// OpenAI 流式语义下 error 事件意味着流终结，因此非致命错误不能走 WriteError，
// 而是以 isError=true 的 tool_result 发出，流继续。
const AgentErrorToolName = "agent_error"

// runErrorKind 是事件循环中 event.Err 的分类结果。
type runErrorKind int

const (
	// runErrIgnored 其他未识别错误：仅记日志，不下发任何事件，流继续。
	runErrIgnored runErrorKind = iota
	// runErrMCP MCP 工具执行错误（含 command_not_allowed）：以 mcp_tool 的 tool_result 下发，流继续。
	runErrMCP
	// runErrFatalConn LLM 服务连接类错误：返回 error 终止 Run，由 Executor 写出 error 事件。
	runErrFatalConn
	// runErrRecoverable 其他 NodeRunError：以 AgentErrorToolName 的 tool_result 下发，流继续。
	runErrRecoverable
)

// classifyRunError 按错误文本把 event.Err 归类，纯函数。
// 优先级固定为：mcp/command_not_allowed → 连接类 → NodeRunError → 其他，
// 调用方据此决定下发 tool_result、返回 error 终止，或仅忽略。
// 引擎自身不再写 error 事件：致命错误（runErrFatalConn）以 error 返回值上抛，
// 由 Executor 统一负责写出唯一的 error 事件并紧跟 [DONE]。
func classifyRunError(errStr string) runErrorKind {
	if strings.Contains(errStr, "mcp") || strings.Contains(errStr, "command_not_allowed") {
		return runErrMCP
	}
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "dial tcp") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "timeout") {
		return runErrFatalConn
	}
	if strings.Contains(errStr, "NodeRunError") {
		return runErrRecoverable
	}
	return runErrIgnored
}

// Engine wraps eino's ChatModelAgent for task execution
type Engine struct {
	models             *llm.ModelService
	homeDir            string // GROOT_HOME 目录，用于读取 GROOT.md
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
	Models             *llm.ModelService
	HomeDir            string // GROOT_HOME 目录
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
		models:             cfg.Models,
		homeDir:            cfg.HomeDir,
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
	// 开启本轮执行的轮次状态：后续 call_agent 登记的子 Agent 都挂在这个主 chatID 下，
	// Run 收尾时 finishRound 汇总主 + 子的 token。必须在任何可能提前返回的步骤之前开启，
	// 保证所有错误路径都能通过 finishRound 清理轮次。
	mainID := mainChatIDFromContext(ctx)
	if e.tokenAccumulators != nil && mainID != "" {
		e.tokenAccumulators.OpenRound(mainID)
	}

	// fail 统一处理 Run 的提前返回：先收尾轮次（丢弃结果，错误路径不下发 usage），再返回错误。
	fail := func(err error) (*RunResult, error) {
		e.finishRound(ctx)
		return nil, err
	}

	// 0. 从数据库解析实际生效的模型（modelName 为空时取默认模型）。
	// 每次执行实时读库，WebUI 中的模型变更立即生效。
	// 解析出的 model 名放进 ctx —— call_agent 工具运行时通过它把同一个 model
	// 透传给子 Agent，保证编排模式下子 Agent 跟随主 Agent 当前选定的 model。
	mdl, err := e.models.GetByName(ctx, modelName)
	if err != nil {
		return fail(fmt.Errorf("模型配置不可用: %w", err))
	}
	resolvedModel := mdl.Name
	ctx = WithParentModel(ctx, resolvedModel)

	// 主 Agent 自身的 model 名记入累加器，Run 收尾时取出写入 RunResult.Model。
	if e.tokenAccumulators != nil && mainID != "" {
		e.tokenAccumulators.SetModel(mainID, resolvedModel)
	}

	// 1. Create ChatModel with per-call timeout
	stepTimeout := time.Duration(e.reactConfig.StepTimeout) * time.Second
	chatModel, err := llm.NewChatModel(ctx, mdl, stepTimeout)
	if err != nil {
		return fail(fmt.Errorf("failed to create chat model: %w", err))
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
		return fail(fmt.Errorf("failed to create agent: %w", err))
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

				switch classifyRunError(errStr) {
				case runErrMCP:
					// MCP tool execution errors → send as tool_result so agent can continue
					toolCallID := extractToolCallID(errStr)
					if cb.WriteToolResult != nil {
						cb.WriteToolResult(errEventAgentName, toolCallID, "mcp_tool", "MCP 工具错误: "+formatMCPError(errStr), true)
					}
					continue

				case runErrFatalConn:
					// LLM 连接类错误 → 致命，直接返回 error 终止 Run。
					// 此处不调用 cb.WriteError：Executor 收到返回的 error 后统一写出
					// 唯一的 error 事件并紧跟 [DONE]；若这里也写，流中会出现两条 error。
					return fail(fmt.Errorf("LLM 服务连接失败: %w", event.Err))

				case runErrRecoverable:
					// 其他 NodeRunError → 以 isError=true 的 tool_result 下发后继续。
					// 不能用 WriteError：OpenAI 流式语义下 error 事件是终结事件，
					// 发出后客户端会关闭流；用 tool_result 承载可保持流符合 OpenAI 规范。
					if cb.WriteToolResult != nil {
						cb.WriteToolResult(errEventAgentName, "", AgentErrorToolName, errStr, true)
					}
					continue

				default:
					continue
				}
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
				e.processToolEvent(ctx, eventAgentName, sseAgentName, event, cb, &steps)
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
						// msg == nil 时可能仍携带 usage-only chunk（OpenAI streaming 把
						// token 用量放在最后一个 content=nil 的 chunk 里）。先处理 usage
						// 再决定是否 continue。
						if msg == nil {
							// usage-only chunk 不会到这里（schema.Message 本身 nil），
							// ResponseMeta 挂在 msg 上，msg==nil 时无需处理，直接跳过。
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
								step := StepRecord{
									StepID:       tc.ID,
									Type:         "tool",
									Name:         tc.Function.Name,
									Status:       StatusRunning,
									NestingLevel: 0,
								}
								// 主 Agent 自身的 step 走 RunResult.Steps（Executor 直接读）；
								// 子 Agent 的 step 通过 tokenAccumulators 累积，由 CallAgentTool 收尾时 PopChild 取出。
								if eventAgentName == "" || eventAgentName == e.agentName {
									steps = append(steps, step)
								}
								e.appendStep(ctx, eventAgentName, step)
							}
						}

						// token 用量 + finish_reason
						if msg.ResponseMeta != nil {
							// 先累加 token 再写 finish：子 Agent 的 CallAgentTool 在其流 EOF 后立刻 PopChild，
							// 把累加放在 HTTP 写出之前，尽量让子 ChatRecord 也拿到最后一个 usage chunk。
							// 对每个携带 Usage 的 chunk 都累加（不限于 finish 那一个）。
							e.accumulateUsage(ctx, eventAgentName, msg.ResponseMeta)
							if msg.ResponseMeta.FinishReason != "" {
								if cb.WriteFinish != nil {
									cb.WriteFinish(sseAgentName, msg.ResponseMeta.FinishReason)
								}
							}
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
							step := StepRecord{
								StepID:       tc.ID,
								Type:         "tool",
								Name:         tc.Function.Name,
								Status:       StatusRunning,
								NestingLevel: 0,
							}
							if eventAgentName == "" || eventAgentName == e.agentName {
								steps = append(steps, step)
							}
							e.appendStep(ctx, eventAgentName, step)
						}
					}

					// 非流式响应：一次性处理 token 用量 + finish_reason（先累加再写出，理由同流式分支）
					if msg.ResponseMeta != nil {
						e.accumulateUsage(ctx, eventAgentName, msg.ResponseMeta)
						if msg.ResponseMeta.FinishReason != "" && cb.WriteFinish != nil {
							cb.WriteFinish(sseAgentName, msg.ResponseMeta.FinishReason)
						}
					}
				}
			}
		}
	}

	// 收尾本轮：取出主 Agent 聚合并汇总全轮（主 + 子）token。
	// 全轮有用量时先下发 usage chunk，再发 [DONE]（顺序不可颠倒，usage 必须在流终结之前）。
	// 取消路径同样下发：已消耗的 token 对调用方仍是真实开销。
	main, round := e.finishRound(ctx)
	if round.Total > 0 && cb.WriteUsage != nil {
		cb.WriteUsage(round)
	}

	// Handle cancellation: send [DONE], then return
	if agentCancelled || ctx.Err() == context.Canceled {
		if cb.WriteDone != nil {
			cb.WriteDone()
		}
		return &RunResult{
			Content:   "",
			Steps:     steps,
			Cancelled: true,
			Model:     resolvedModel,
			Tokens:    main.Tokens,
		}, nil
	}

	// Send [DONE] for normal completion
	if cb.WriteDone != nil {
		cb.WriteDone()
	}

	if finalResult == "" {
		finalResult = "任务执行完成，但未获得明确结果"
	}

	return &RunResult{
		Content: finalResult,
		Steps:   steps,
		Model:   resolvedModel,
		Tokens:  main.Tokens,
	}, nil
}

// finishRound 收尾主 Agent 的一轮执行：取出并清理主 Agent 聚合，同时汇总本轮全部子 Agent 的 token。
// main 仅含主 Agent 自身（RunResult.Tokens 语义不变）；round 为主 + 子的全轮合计，供 usage chunk 下发。
// Steps 不通过累加器返回（主 Agent 的 steps 已经在 Run 闭包里直接维护并返回）；
// Model 也不通过这里返回（resolvedModel 已经直接写入 RunResult.Model）。
// accumulators 未初始化或 ctx 缺失 mainChatID 时返回零值。
func (e *Engine) finishRound(ctx context.Context) (main ChatAggregate, round TokenUsage) {
	if e.tokenAccumulators == nil {
		return ChatAggregate{}, TokenUsage{}
	}
	mainID := mainChatIDFromContext(ctx)
	if mainID == "" {
		return ChatAggregate{}, TokenUsage{}
	}
	return e.tokenAccumulators.PopRound(mainID)
}

// processToolEvent processes Tool role events.
// sseAgentName 是已折叠的 SSE 字段（主 Agent → ""），eventAgentName 是事件原始 Agent 名，
// 后者用于把 step 完成状态归属到正确的累加器。
func (e *Engine) processToolEvent(ctx context.Context, eventAgentName, sseAgentName string, event *adk.AgentEvent, cb *ProgressCallback, steps *[]StepRecord) {
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
		if err := cb.WriteToolResult(sseAgentName, toolCallID, toolName, output, false); err != nil {
			e.log.Error("SSE write tool_result failed: " + err.Error())
		}
	}

	// Update step status to completed:
	// - 主 Agent 自身的 step 在 RunResult.Steps 上原地翻状态
	// - 子 Agent 的 step 在累加器里翻状态（call_agent 收尾时取出）
	if eventAgentName == "" || eventAgentName == e.agentName {
		for i := range *steps {
			if (*steps)[i].StepID == toolCallID {
				(*steps)[i].Status = StatusCompleted
			}
		}
	}
	e.completeStep(ctx, eventAgentName, toolCallID)
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
			Index: tc.Index,
			ID:    tc.ID,
			Type:  tc.Type,
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
		// 1. GROOT.md（按需读取，有就加载，没有就跳过）
		grootMd := grootmd.GetContent(e.homeDir)
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
			// strip <think>...</think> 标签：部分模型（如 MiniMax）把推理过程内嵌在 Content 里，
			// 传入上下文会大幅增加 prompt token，且可能干扰模型行为。
			// 数据库中原始内容保持不变，只在构建 LLM 上下文时做 strip。
			msgs = append(msgs, schema.AssistantMessage(stripThinkingTags(hMsg.Result), nil))
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
			// Detail 故意不设。OpenAI 协议中 detail 是可选字段，
			// 不传由后端按自身默认行为处理；部分 OpenAI 兼容后端
			// （如本地 Qwen 推理网关）只接受 low/high，会拒绝 auto。
			parts = append(parts, schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{
						URL: toPtr(dataURL),
					},
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

// WithChildChatID 把子 Agent 的 chatID 注入 ctx；调用方一般是 CallAgentTool。
func WithChildChatID(ctx context.Context, chatID string) context.Context {
	return context.WithValue(ctx, childChatIDKey{}, chatID)
}

// childChatIDFromContext 取出子 Agent chatID；不存在返回空字符串。
func childChatIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(childChatIDKey{}).(string); ok {
		return v
	}
	return ""
}

// mainChatIDKey 在 ctx 中携带主 Agent 的 chatID。Engine 在 Run 入口注入；
// 事件循环用它把主 Agent 的 token / step 累积到 TokenAccumulators 里。
type mainChatIDKey struct{}

// WithMainChatID 由 Executor 调用，在 Run 之前注入主 Agent 的 chatID。
func WithMainChatID(ctx context.Context, chatID string) context.Context {
	return context.WithValue(ctx, mainChatIDKey{}, chatID)
}

// mainChatIDFromContext 取出主 Agent chatID；不存在返回空字符串。
func mainChatIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(mainChatIDKey{}).(string); ok {
		return v
	}
	return ""
}

// resolveChatIDForAgent 在事件循环中按事件源选定累加 chatID：
//   - 事件源是主 Agent（eventAgentName == e.agentName 或为空）→ ctx 中的 mainChatID
//   - 事件源是子 Agent → 优先取轮次登记表中 agentName 对应的 childChatID（由 CallAgentTool
//     RegisterChild 登记；主 Agent 事件循环的 ctx 里没有子 chatID，只能靠登记表归属），
//     未登记时回退 ctx 中的 childChatID（由 CallAgentTool 注入）
//
// 返回空字符串表示该事件不应累加（缺失 ctx 注入或 accumulators 未初始化）。
func (e *Engine) resolveChatIDForAgent(ctx context.Context, eventAgentName string) string {
	if e.tokenAccumulators == nil {
		return ""
	}
	if eventAgentName == "" || eventAgentName == e.agentName {
		return mainChatIDFromContext(ctx)
	}
	if id := e.tokenAccumulators.ChildFor(mainChatIDFromContext(ctx), eventAgentName); id != "" {
		return id
	}
	return childChatIDFromContext(ctx)
}

// accumulateUsage 把一次 LLM 响应的 token 用量按事件源 Agent 归属到累加器。
// nil meta / nil usage / 缺失 chatID / 缺失 accumulators 时静默 no-op。
func (e *Engine) accumulateUsage(ctx context.Context, eventAgentName string, meta *schema.ResponseMeta) {
	if meta == nil || meta.Usage == nil {
		return
	}
	chatID := e.resolveChatIDForAgent(ctx, eventAgentName)
	if chatID == "" {
		return
	}
	e.tokenAccumulators.Add(chatID, meta.Usage.PromptTokens, meta.Usage.CompletionTokens)
}

// appendStep 把 LLM 触发的 tool call 作为一条 running step 加入对应 Agent 的累加器。
func (e *Engine) appendStep(ctx context.Context, eventAgentName string, step StepRecord) {
	chatID := e.resolveChatIDForAgent(ctx, eventAgentName)
	if chatID == "" {
		return
	}
	e.tokenAccumulators.AppendStep(chatID, step)
}

// completeStep 把 tool 返回结果对应的 step（按 stepID 匹配）状态翻为 completed。
func (e *Engine) completeStep(ctx context.Context, eventAgentName, stepID string) {
	if stepID == "" {
		return
	}
	chatID := e.resolveChatIDForAgent(ctx, eventAgentName)
	if chatID == "" {
		return
	}
	e.tokenAccumulators.CompleteStep(chatID, stepID)
}

// truncate truncates a string to max length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// thinkTagRegex 匹配 <think>...</think> 块（含跨行内容）。
// 部分模型（如 MiniMax）把推理过程内嵌在 Content 里而非独立 ReasoningContent 字段，
// 历史上下文传入 LLM 前需要 strip 掉，避免浪费 prompt token 或干扰模型行为。
var thinkTagRegex = regexp.MustCompile(`(?s)<think>.*?</think>`)

// stripThinkingTags 去除字符串中所有 <think>...</think> 标签及其内容，
// 并清理多余的首尾空白。
func stripThinkingTags(s string) string {
	return strings.TrimSpace(thinkTagRegex.ReplaceAllString(s, ""))
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
	// Model 反映本次执行实际选定的 model 名（即 NewChatModel 所用的 modelName）。
	// 如果创建 ChatModel 失败则为空字符串。
	Model string
	// Tokens 主 Agent 自身的 token 用量（不含子 Agent；子 Agent 的 token 由 CallAgentTool 单独从累加器取出）。
	Tokens TokenUsage
}
