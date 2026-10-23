package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// flushWriter is the minimal interface for SSE writing: Write + Flush.
// network.Conn satisfies this natively; io.PipeWriter can be adapted with a no-op Flush.
type flushWriter interface {
	io.Writer
	Flush() error
}

// SSEWriter writes Server-Sent Events to a flushable writer.
// 事件以 OpenAI chat.completion.chunk 外层结构输出。
type SSEWriter struct {
	w         flushWriter
	sessionID string
	chatID    string
	round     int
	created   int64 // 对话开始时间（Unix 秒），作为 chunk.created

	mu          sync.RWMutex
	model       string            // 主模型名，作为 chunk.model 的默认值；受 mu 保护（SetModel 可覆盖）
	agentModels map[string]string // 子 Agent 名 → 其实际使用的模型名；由 CallAgentTool 登记，保留至请求结束
}

// NewSSEWriter creates a new SSE writer.
// model 为本轮对话的主模型名，created 为对话开始时间的 Unix 秒。
func NewSSEWriter(w flushWriter, sessionID, chatID string, round int, model string, created int64) *SSEWriter {
	return &SSEWriter{
		w:           w,
		sessionID:   sessionID,
		chatID:      chatID,
		round:       round,
		model:       model,
		created:     created,
		agentModels: make(map[string]string),
	}
}

// SetModel 覆盖主模型名（chunk.model 的默认值）。用于 Solo 模式：agent.md 钉死的模型
// 在 Executor 中覆盖 task.ModelName 后，须同步到 SSEWriter 以保证 chunk.model 反映实际模型。
// model 为空时不做任何修改。
func (s *SSEWriter) SetModel(model string) {
	if model == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model = model
}

// SetAgentModel 登记 agentName 对应的模型名；model 为空时等价于 ClearAgentModel。
func (s *SSEWriter) SetAgentModel(agentName, model string) {
	if model == "" {
		s.ClearAgentModel(agentName)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agentModels == nil {
		s.agentModels = make(map[string]string)
	}
	s.agentModels[agentName] = model
}

// ClearAgentModel 移除 agentName 的模型登记，后续事件回退到主模型名。
func (s *SSEWriter) ClearAgentModel(agentName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agentModels, agentName)
}

// chatChunk 是 OpenAI chat.completion.chunk 的外层结构。
type chatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chunkUsage   `json:"usage"` // 普通 chunk 为 nil（序列化为 null），仅 usage chunk 携带对象
}

// chunkUsage 是 OpenAI chunk.usage 的结构：本次 /chat 请求的 token 用量汇总。
type chunkUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// chunkChoice 是 chunk.choices 的元素；Delta 始终为非 nil map 以保证序列化为 {}，
// FinishReason 为 nil 时序列化为 null。
type chunkChoice struct {
	Index        int            `json:"index"`
	Delta        map[string]any `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

// modelFor 返回 agentName 对应的模型名；agentName 为空、未登记或登记值为空时回退主模型名。
func (s *SSEWriter) modelFor(agentName string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if agentName == "" {
		return s.model
	}
	if m := s.agentModels[agentName]; m != "" {
		return m
	}
	return s.model
}

// writeChunk 以 chat.completion.chunk 结构写出一条事件。
// agentName 非空时写入 delta["agent_name"]；delta 为 nil 时写入空对象 {}。
func (s *SSEWriter) writeChunk(agentName string, delta map[string]any, finishReason *string) error {
	if delta == nil {
		delta = map[string]any{}
	}
	if agentName != "" {
		delta["agent_name"] = agentName
	}
	return s.WriteData(chatChunk{
		ID:      s.chatID,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.modelFor(agentName),
		Choices: []chunkChoice{{
			Index:        0,
			Delta:        delta,
			FinishReason: finishReason,
		}},
	})
}

// WriteData marshals data as JSON and writes it as an SSE data line, then flushes.
func (s *SSEWriter) WriteData(data interface{}) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}
	line := fmt.Sprintf("data: %s\n\n", string(dataBytes))
	if _, err := s.w.Write([]byte(line)); err != nil {
		return err
	}
	return s.w.Flush()
}

// WriteThinking writes thinking chunk (delta.reasoning_content); agentName 非空时注入 delta.agent_name。
func (s *SSEWriter) WriteThinking(agentName, content string) error {
	return s.writeChunk(agentName, map[string]any{
		"role":              "assistant",
		"reasoning_content": content,
	}, nil)
}

// WriteMessage writes message chunk (delta.content); agentName 非空时注入 delta.agent_name。
func (s *SSEWriter) WriteMessage(agentName, content string) error {
	return s.writeChunk(agentName, map[string]any{
		"role":    "assistant",
		"content": content,
	}, nil)
}

// WriteToolCalls writes tool_calls chunk (delta.tool_calls); agentName 非空时注入 delta.agent_name。
func (s *SSEWriter) WriteToolCalls(agentName string, toolCalls []ToolCall) error {
	return s.writeChunk(agentName, map[string]any{
		"role":       "assistant",
		"tool_calls": toolCalls,
	}, nil)
}

// WriteFinish writes finish chunk：finish_reason 位于 choices[0]，delta 为空对象；
// agentName 非空时注入 delta.agent_name。
func (s *SSEWriter) WriteFinish(agentName, reason string) error {
	return s.writeChunk(agentName, nil, &reason)
}

// WriteToolResult writes tool result chunk (delta.role == "tool"). Set isError to true
// for MCP tool errors so the TUI can render them with error styling instead of silently
// dropping them. agentName 非空时注入 delta.agent_name。
func (s *SSEWriter) WriteToolResult(agentName, toolCallID, toolName, content string, isError bool) error {
	return s.writeChunk(agentName, map[string]any{
		"role":         "tool",
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"content":      content,
		"error":        isError,
	}, nil)
}

// WriteUsage writes OpenAI 风格的最终 usage chunk：choices 为空数组 []，顶层 usage 为
// 本次请求的 token 用量汇总；model 取主模型名（受 SetModel 覆盖，与主 Agent 事件一致）。
func (s *SSEWriter) WriteUsage(u TokenUsage) error {
	return s.WriteData(chatChunk{
		ID:      s.chatID,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.modelFor(""),
		Choices: []chunkChoice{},
		Usage: &chunkUsage{
			PromptTokens:     u.Prompt,
			CompletionTokens: u.Completion,
			TotalTokens:      u.Total,
		},
	})
}

// WriteError writes an OpenAI 风格的 error 事件（无 choices）；agentName 非空时注入 error.agent_name。
func (s *SSEWriter) WriteError(agentName, message string) error {
	e := map[string]any{
		"message": message,
		"type":    "server_error",
		"code":    nil,
		"param":   nil,
	}
	if agentName != "" {
		e["agent_name"] = agentName
	}
	return s.WriteData(map[string]any{"error": e})
}

// WriteDone writes [DONE] marker and flushes.
func (s *SSEWriter) WriteDone() error {
	if _, err := s.w.Write([]byte("data: [DONE]\n\n")); err != nil {
		return err
	}
	return s.w.Flush()
}

// ToolCall represents a tool call (OpenAI format).
type ToolCall struct {
	Index    *int           `json:"index,omitempty"`
	ID       string         `json:"id"`
	Type     string         `json:"type"` // "function"
	Function FunctionCall   `json:"function"`
	Extra    map[string]any `json:"extra,omitempty"`
}

// FunctionCall represents function call details.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}
