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
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/skill"
	// "github.com/zfd81/groot/internal/storage" // removed - will be re-added in Phase 4
)

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
	progress func(stepID, eventType, message string),
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

	// 7. Build user message with attachment paths
	userMessage := e.buildUserMessage(instruction, attachmentPaths)

	// 8. Run agent and collect events
	// Use adk.Message (alias for *schema.Message)
	msgs := []adk.Message{schema.UserMessage(userMessage)}
	iter := runner.Run(ctx, msgs)

	var finalResult string
	var steps []StepRecord
	stepIDGen := NewStepIDGenerator()

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}

		// Process event and send progress
		stepID := stepIDGen.Next()
		content := e.processEvent(event, stepID, progress, &steps)
		if content != "" {
			finalResult = content
		}
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

// processEvent handles agent events and sends progress
// Returns the message content if it's an assistant response
func (e *Engine) processEvent(event *adk.AgentEvent, stepID string, progress func(string, string, string), steps *[]StepRecord) string {
	// Check for errors
	if event.Err != nil {
		progress(stepID, "error", event.Err.Error())
		return ""
	}

	// Process output
	if event.Output != nil {
		msgOutput := event.Output.MessageOutput

		if msgOutput != nil && msgOutput.Role == schema.Assistant {
			// Handle streaming response
			if msgOutput.IsStreaming && msgOutput.MessageStream != nil {
				// Read from stream using Recv()
				var content string
				stream := msgOutput.MessageStream
				for {
					msg, err := stream.Recv()
					if err != nil {
						break // EOF or error
					}
					if msg != nil && msg.Content != "" {
						content += msg.Content
						progress(stepID, "progress", msg.Content)
					}
				}
				stream.Close()
				if content != "" {
					*steps = append(*steps, StepRecord{
						StepID:       stepID,
						Type:         "llm",
						Name:         "model_response",
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
					progress(stepID, "progress", msg.Content)
					*steps = append(*steps, StepRecord{
						StepID:       stepID,
						Type:         "llm",
						Name:         "model_response",
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
			// Agent exited
			progress(stepID, "exit", "任务完成")
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
	Content string
	Steps   []StepRecord
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