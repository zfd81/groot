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
		e.log.Error("Agent event error: " + event.Err.Error())
		return ""
	}

	// Info level logging for event processing (visible in production)
	if event.Output != nil && event.Output.MessageOutput != nil {
		msgOutput := event.Output.MessageOutput
		e.log.Info(fmt.Sprintf("Event: Role=%s, IsStreaming=%v, HasMessage=%v, HasStream=%v, ToolCalls=%d",
			string(msgOutput.Role), msgOutput.IsStreaming,
			msgOutput.Message != nil, msgOutput.MessageStream != nil,
			len(msgOutput.Message.ToolCalls)))
	}

	// Process output
	if event.Output != nil {
		msgOutput := event.Output.MessageOutput

		// Handle Tool role (tool result from MCP execution)
		if msgOutput != nil && msgOutput.Role == schema.Tool {
			e.log.Info("Tool role event: processing tool result")

			// Tool result from MCP execution
			var stepID string
			var output string
			var errStr string
			var toolName string

			if msgOutput.Message != nil {
				stepID = msgOutput.Message.ToolCallID
				output = msgOutput.Message.Content
			}
			if msgOutput.ToolName != "" {
				toolName = msgOutput.ToolName
			}

			if stepID == "" {
				stepID = stepIDGen.Next()
			}

			e.log.Info(fmt.Sprintf("Tool result: toolName=%s, stepID=%s, outputLen=%d", toolName, stepID, len(output)))

			// IMPORTANT: First send tool_call event BEFORE tool_result
			// This ensures proper event ordering: thinking_start → thinking → thinking_end → tool_call → tool_result
			if toolName != "" && cb.WriteToolCall != nil {
				e.log.Info(fmt.Sprintf("Sending tool_call event: stepID=%s, toolName=%s", stepID, toolName))
				if err := cb.WriteToolCall(stepID, toolName, nil); err != nil {
					e.log.Error("SSE write tool_call failed: " + err.Error())
				}
				*steps = append(*steps, StepRecord{
					StepID:       stepID,
					Type:         "tool",
					Name:         toolName,
					Status:       StatusRunning,
					NestingLevel: 0,
				})
			}

			// Then send tool_result event
			if cb.WriteToolResult != nil {
				e.log.Info(fmt.Sprintf("Sending tool_result event: stepID=%s", stepID))
				if err := cb.WriteToolResult(stepID, output, errStr); err != nil {
					e.log.Error("SSE write tool_result failed: " + err.Error())
				}
			}

			// Update step status to completed
			for i := range *steps {
				if (*steps)[i].StepID == stepID {
					(*steps)[i].Status = StatusCompleted
				}
			}

			return ""
		}

		// Handle Assistant role (LLM output)
		if msgOutput != nil && msgOutput.Role == schema.Assistant {
			e.log.Info("Assistant role event: processing LLM output")

			// Check for ToolCalls - indicates thinking phase (will call tools)
			hasToolCalls := msgOutput.Message != nil && len(msgOutput.Message.ToolCalls) > 0

			// Handle streaming response - TRUE streaming: send each chunk immediately
			if msgOutput.IsStreaming && msgOutput.MessageStream != nil {
				e.log.Info(fmt.Sprintf("Streaming response: hasToolCalls=%v", hasToolCalls))

				// KEY: Read all content FIRST before deciding event type
				var content string
				stream := msgOutput.MessageStream
				for {
					msg, err := stream.Recv()
					if err != nil {
						break // EOF or error
					}
					if msg != nil && msg.Content != "" {
						content += msg.Content
					}
				}
				stream.Close()

				e.log.Info(fmt.Sprintf("Streaming complete: contentLen=%d, hasToolCalls=%v", len(content), hasToolCalls))

				// KEY LOGIC: Determine event type based on content AND ToolCalls
				// - Empty content → thinking (next event will be tool call)
				// - Non-empty content with ToolCalls → thinking (will call tools)
				// - Non-empty content without ToolCalls → message (final output)
				isThinking := len(content) == 0 || hasToolCalls
				stepID := stepIDGen.Next()

				if isThinking {
					// Thinking phase - send thinking_start/thinking/thinking_end
					e.log.Info(fmt.Sprintf("Sending thinking events: stepID=%s, contentLen=%d", stepID, len(content)))
					if cb.WriteThinkingStart != nil {
						if err := cb.WriteThinkingStart(stepID); err != nil {
							e.log.Error("SSE write thinking_start failed: " + err.Error())
						}
					}
					if content != "" && cb.WriteThinking != nil {
						if err := cb.WriteThinking(content); err != nil {
							e.log.Error("SSE write thinking failed: " + err.Error())
						}
					}
					if cb.WriteThinkingEnd != nil {
						if err := cb.WriteThinkingEnd(stepID, "success"); err != nil {
							e.log.Error("SSE write thinking_end failed: " + err.Error())
						}
					}
					*steps = append(*steps, StepRecord{
						StepID:       stepID,
						Type:         "thinking",
						Name:         "reasoning",
						Status:       StatusCompleted,
						NestingLevel: 0,
					})

					// Send tool_calls if present in this event
					if hasToolCalls {
						for _, tc := range msgOutput.Message.ToolCalls {
							arguments := make(map[string]interface{})
							if tc.Function.Arguments != "" {
								if err := json.Unmarshal([]byte(tc.Function.Arguments), &arguments); err != nil {
									arguments["_raw"] = tc.Function.Arguments
								}
							}
							e.log.Info(fmt.Sprintf("Sending tool_call from streaming: id=%s, name=%s", tc.ID, tc.Function.Name))
							if cb.WriteToolCall != nil {
								if err := cb.WriteToolCall(tc.ID, tc.Function.Name, arguments); err != nil {
									e.log.Error("SSE write tool_call failed: " + err.Error())
								}
							}
							*steps = append(*steps, StepRecord{
								StepID:       tc.ID,
								Type:         "tool",
								Name:         tc.Function.Name,
								Status:       StatusRunning,
								NestingLevel: 0,
							})
						}
					}
					return ""
				} else {
					// Message phase - final output
					e.log.Info("Sending message events")
					if cb.WriteMessageStart != nil {
						if err := cb.WriteMessageStart(); err != nil {
							e.log.Error("SSE write message_start failed: " + err.Error())
						}
					}
					if content != "" && cb.WriteMessage != nil {
						if err := cb.WriteMessage(content); err != nil {
							e.log.Error("SSE write message failed: " + err.Error())
						}
					}
					if cb.WriteMessageEnd != nil {
						if err := cb.WriteMessageEnd(); err != nil {
							e.log.Error("SSE write message_end failed: " + err.Error())
						}
					}
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
				hasToolCalls = len(msg.ToolCalls) > 0

				e.log.Info(fmt.Sprintf("Non-streaming: ContentLen=%d, ToolCalls=%d", len(msg.Content), len(msg.ToolCalls)))

				if msg.Content != "" {
					// Determine if thinking or message
					if hasToolCalls {
						// Thinking phase - content followed by tool calls
						stepID := stepIDGen.Next()
						e.log.Info(fmt.Sprintf("Sending thinking (has ToolCalls): stepID=%s", stepID))
						if cb.WriteThinkingStart != nil {
							if err := cb.WriteThinkingStart(stepID); err != nil {
								e.log.Error("SSE write thinking_start failed: " + err.Error())
							}
						}
						if cb.WriteThinking != nil {
							if err := cb.WriteThinking(msg.Content); err != nil {
								e.log.Error("SSE write thinking failed: " + err.Error())
							}
						}
						if cb.WriteThinkingEnd != nil {
							if err := cb.WriteThinkingEnd(stepID, "success"); err != nil {
								e.log.Error("SSE write thinking_end failed: " + err.Error())
							}
						}
						*steps = append(*steps, StepRecord{
							StepID:       stepID,
							Type:         "thinking",
							Name:         "reasoning",
							Status:       StatusCompleted,
							NestingLevel: 0,
						})

						// Send tool_calls
						for _, tc := range msg.ToolCalls {
							arguments := make(map[string]interface{})
							if tc.Function.Arguments != "" {
								if err := json.Unmarshal([]byte(tc.Function.Arguments), &arguments); err != nil {
									arguments["_raw"] = tc.Function.Arguments
								}
							}
							e.log.Info(fmt.Sprintf("Sending tool_call: id=%s, name=%s", tc.ID, tc.Function.Name))
							if cb.WriteToolCall != nil {
								if err := cb.WriteToolCall(tc.ID, tc.Function.Name, arguments); err != nil {
									e.log.Error("SSE write tool_call failed: " + err.Error())
								}
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

					// No ToolCalls - final message output
					e.log.Info("Sending message (no ToolCalls)")
					if cb.WriteMessageStart != nil {
						if err := cb.WriteMessageStart(); err != nil {
							e.log.Error("SSE write message_start failed: " + err.Error())
						}
					}
					if cb.WriteMessage != nil {
						if err := cb.WriteMessage(msg.Content); err != nil {
							e.log.Error("SSE write message failed: " + err.Error())
						}
					}
					if cb.WriteMessageEnd != nil {
						if err := cb.WriteMessageEnd(); err != nil {
							e.log.Error("SSE write message_end failed: " + err.Error())
						}
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

				// No content but has ToolCalls
				if hasToolCalls {
					e.log.Info("No content but has ToolCalls")
					for _, tc := range msg.ToolCalls {
						arguments := make(map[string]interface{})
						if tc.Function.Arguments != "" {
							if err := json.Unmarshal([]byte(tc.Function.Arguments), &arguments); err != nil {
								arguments["_raw"] = tc.Function.Arguments
							}
						}
						e.log.Info(fmt.Sprintf("Sending tool_call (no content): id=%s, name=%s", tc.ID, tc.Function.Name))
						if cb.WriteToolCall != nil {
							if err := cb.WriteToolCall(tc.ID, tc.Function.Name, arguments); err != nil {
								e.log.Error("SSE write tool_call failed: " + err.Error())
							}
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
			}
		}
	}

	// Process actions
	if event.Action != nil {
		if event.Action.Exit {
			e.log.Info("Agent exit action detected")
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