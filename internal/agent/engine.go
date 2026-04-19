package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/skill"
)

// ProgressCallback handles SSE event callbacks with structured data
type ProgressCallback struct {
	WriteThinkingStart func(stepID string) error
	WriteThinking      func(content string) error
	WriteThinkingEnd   func(stepID, status string) error
	WriteToolCall      func(stepID, name string, arguments map[string]interface{}) error
	WriteToolResult    func(stepID, output, errStr string) error
	WriteMessageStart  func() error
	WriteMessage       func(content string) error
	WriteMessageEnd    func() error
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
	cb *ProgressCallback,
) (*RunResult, error) {
	// 1. Create ChatModel
	chatModel, err := llm.NewChatModel(ctx, e.llmConfig)
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

	// 8. Initialize step ID generator for reasoning steps
	stepIDGen := NewStepIDGenerator()

	// 9. Run agent and collect events with proper cancellation handling
	iter := runner.Run(ctx, msgs)

	var finalResult string
	var steps []StepRecord
	var agentCancelled bool

	// Use a channel to receive events asynchronously
	eventCh := make(chan *adk.AgentEvent, 100) // buffered channel to avoid blocking
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			// Bug fix: check context cancellation before reading next event
			select {
			case <-ctx.Done():
				close(eventCh)
				return
			default:
				event, ok := iter.Next()
				if !ok {
					close(eventCh)
					return
				}
				// Try to send event, but check cancellation frequently
				select {
				case eventCh <- event:
				case <-ctx.Done():
					close(eventCh)
					return
				}
			}
		}
	}()

	// Process events with cancellation support
eventLoop:
	for {
		select {
		case <-ctx.Done():
			// Context was cancelled, return immediately with cancelled status
			agentCancelled = true
			break eventLoop
		case event, ok := <-eventCh:
			if !ok {
				// Iterator finished, break out of loop
				break eventLoop
			}
			// Process event and send progress
			content := e.processEvent(event, stepIDGen, cb, &steps)
			if content != "" {
				finalResult = content
			}
		case <-done:
			// Iterator goroutine finished
			break eventLoop
		}
	}

	// 10. Handle cancellation
	if agentCancelled || ctx.Err() == context.Canceled {
		return &RunResult{Content: "", Steps: steps, Cancelled: true}, nil
	}

	if finalResult == "" {
		finalResult = "任务执行完成，但未获得明确结果"
	}

	return &RunResult{Content: finalResult, Steps: steps}, nil
}

// buildTools creates eino tools from MCP tools
func (e *Engine) buildTools() []tool.BaseTool {
	tools := []tool.BaseTool{}

	for _, toolInfo := range e.mcpManager.ListTools() {
		// Create eino tool wrapper for each MCP tool
		t := NewMCPToolAdapter(toolInfo, e.mcpManager, e.log)
		tools = append(tools, t)
	}

	return tools
}

// buildSystemInstruction builds the system prompt with Skills
func (e *Engine) buildSystemInstruction(prompt string) string {
	sb := &strings.Builder{}

	// User's custom prompt
	if prompt != "" {
		sb.WriteString(prompt)
		sb.WriteString("\n\n")
	}

	// Skills instructions
	if e.skillsRegistry.Count() > 0 {
		sb.WriteString("可用技能 (专用任务模板):\n")
		for _, skill := range e.skillsRegistry.List() {
			sb.WriteString(fmt.Sprintf("\n## %s\n", skill.Name))
			sb.WriteString(fmt.Sprintf("描述: %s\n", skill.Description))
			sb.WriteString(fmt.Sprintf("\n指令:\n%s\n", skill.Instructions))
		}
		sb.WriteString("\n")
	}

	// Execution rules
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
				// File type - show path for tools to read
				msg += fmt.Sprintf("- %s (%s)\n  路径: %s\n  类型: %s\n  大小: %d bytes\n",
					att.OriginalName, att.Type, att.FullPath, att.ContentType, att.Size)
			} else if att.Type == "url" {
				// URL type
				msg += fmt.Sprintf("- %s (url)\n  URL: %s\n", att.OriginalName, att.RelativePath)
			} else {
				// Other types
				msg += fmt.Sprintf("- %s (%s)\n", att.OriginalName, att.Type)
			}
		}
	}
	return msg
}

// buildMessageList builds message list with history context
func (e *Engine) buildMessageList(instruction string, attachmentPaths []AttachmentPath, historyMessages []memory.Message) []adk.Message {
	msgs := []adk.Message{}

	// Add history messages as context (convert to conversation format)
	for _, hMsg := range historyMessages {
		// Previous user instruction
		if hMsg.Instruction != "" {
			msgs = append(msgs, schema.UserMessage(hMsg.Instruction))
		}
		// Previous assistant response
		if hMsg.Result != "" {
			msgs = append(msgs, schema.AssistantMessage(hMsg.Result, nil))
		}
	}

	// Add current user message
	userMessage := e.buildUserMessage(instruction, attachmentPaths)
	msgs = append(msgs, schema.UserMessage(userMessage))

	return msgs
}

// processEvent handles agent events and sends SSE events via callback
// Returns the message content if it's an assistant response
func (e *Engine) processEvent(event *adk.AgentEvent, stepIDGen *StepIDGenerator, cb *ProgressCallback, steps *[]StepRecord) string {
	// Check for errors
	if event.Err != nil {
		// Error during execution - no specific event, handled by caller
		return ""
	}

	// Process output
	if event.Output != nil {
		msgOutput := event.Output.MessageOutput

		// Handle Tool role (tool result)
		if msgOutput != nil && msgOutput.Role == schema.Tool {
			// Tool result from MCP execution
			// Get step_id from Message.ToolCallID
			var stepID string
			var output string
			var errStr string

			if msgOutput.Message != nil {
				stepID = msgOutput.Message.ToolCallID
				output = msgOutput.Message.Content
			} else if msgOutput.ToolName != "" {
				// Fallback: use ToolName if no ToolCallID
				stepID = msgOutput.ToolName
			}

			if stepID == "" {
				stepID = stepIDGen.Next()
			}

			// Send tool_result event
			if cb.WriteToolResult != nil {
				cb.WriteToolResult(stepID, output, errStr)
			}

			*steps = append(*steps, StepRecord{
				StepID:       stepID,
				Type:         "tool",
				Name:         "tool_result",
				Status:       StatusCompleted,
				NestingLevel: 0,
			})
			return ""
		}

		// Handle Assistant role (LLM output)
		if msgOutput != nil && msgOutput.Role == schema.Assistant {
			// Check for ToolCalls in Message (LLM wants to call tools)
			if msgOutput.Message != nil && len(msgOutput.Message.ToolCalls) > 0 {
				// Send tool_call events for each tool call request
				for _, tc := range msgOutput.Message.ToolCalls {
					// Parse arguments
					arguments := make(map[string]interface{})
					if tc.Function.Arguments != "" {
						// Try to parse as JSON
						if err := json.Unmarshal([]byte(tc.Function.Arguments), &arguments); err != nil {
							// If not valid JSON, store as raw string
							arguments["_raw"] = tc.Function.Arguments
						}
					}

					if cb.WriteToolCall != nil {
						cb.WriteToolCall(tc.ID, tc.Function.Name, arguments)
					}

					*steps = append(*steps, StepRecord{
						StepID:       tc.ID,
						Type:         "tool",
						Name:         tc.Function.Name,
						Status:       StatusRunning,
						NestingLevel: 0,
					})
				}
				return ""
			}

			// Handle streaming response (final message output)
			if msgOutput.IsStreaming && msgOutput.MessageStream != nil {
				// Send message_start before streaming
				if cb.WriteMessageStart != nil {
					cb.WriteMessageStart()
				}

				var content string
				stream := msgOutput.MessageStream
				for {
					msg, err := stream.Recv()
					if err != nil {
						break // EOF or error
					}
					if msg != nil && msg.Content != "" {
						content += msg.Content
						if cb.WriteMessage != nil {
							cb.WriteMessage(msg.Content)
						}
					}
				}
				stream.Close()

				// Send message_end after streaming completes
				if cb.WriteMessageEnd != nil {
					cb.WriteMessageEnd()
				}

				if content != "" {
					*steps = append(*steps, StepRecord{
						StepID:       stepIDGen.Next(),
						Type:         "message",
						Name:         "final_response",
						Status:       StatusCompleted,
						NestingLevel: 0,
					})
					return content
				}
			}

			// Handle non-streaming response
			if msgOutput.Message != nil {
				msg := msgOutput.Message
				if msg.Content != "" {
					// Send message_start -> message -> message_end
					if cb.WriteMessageStart != nil {
						cb.WriteMessageStart()
					}
					if cb.WriteMessage != nil {
						cb.WriteMessage(msg.Content)
					}
					if cb.WriteMessageEnd != nil {
						cb.WriteMessageEnd()
					}

					*steps = append(*steps, StepRecord{
						StepID:       stepIDGen.Next(),
						Type:         "message",
						Name:         "final_response",
						Status:       StatusCompleted,
						NestingLevel: 0,
					})
					return msg.Content
				}
			}
		}
	}

	// Process actions
	if event.Action != nil {
		if event.Action.Exit {
			// Agent exited normally - no specific event needed
		}
	}

	return ""
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

// StepIDGenerator generates step IDs
type StepIDGenerator struct {
	counter int
}

func NewStepIDGenerator() *StepIDGenerator {
	return &StepIDGenerator{counter: 0}
}

func (g *StepIDGenerator) Next() string {
	g.counter++
	return GenerateStepID()
}