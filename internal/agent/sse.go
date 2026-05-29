package agent

import (
	"encoding/json"
	"fmt"
	"io"
)

// flushWriter is the minimal interface for SSE writing: Write + Flush.
// network.Conn satisfies this natively; io.PipeWriter can be adapted with a no-op Flush.
type flushWriter interface {
	io.Writer
	Flush() error
}

// SSEWriter writes Server-Sent Events to a flushable writer.
type SSEWriter struct {
	w         flushWriter
	sessionID string
	chatID    string
	round     int
}

// NewSSEWriter creates a new SSE writer.
func NewSSEWriter(w flushWriter, sessionID, chatID string, round int) *SSEWriter {
	return &SSEWriter{
		w:         w,
		sessionID: sessionID,
		chatID:    chatID,
		round:     round,
	}
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

// WriteThinking writes thinking chunk (reasoning_content); agentName 非空时注入 agent_name。
func (s *SSEWriter) WriteThinking(agentName, content string) error {
	payload := map[string]interface{}{
		"role":              "assistant",
		"reasoning_content": content,
	}
	if agentName != "" {
		payload["agent_name"] = agentName
	}
	return s.WriteData(payload)
}

// WriteMessage writes message chunk (content); agentName 非空时注入 agent_name。
func (s *SSEWriter) WriteMessage(agentName, content string) error {
	payload := map[string]interface{}{
		"role":    "assistant",
		"content": content,
	}
	if agentName != "" {
		payload["agent_name"] = agentName
	}
	return s.WriteData(payload)
}

// WriteToolCalls writes tool_calls event; agentName 非空时注入 agent_name。
func (s *SSEWriter) WriteToolCalls(agentName string, toolCalls []ToolCall) error {
	payload := map[string]interface{}{
		"role":       "assistant",
		"tool_calls": toolCalls,
	}
	if agentName != "" {
		payload["agent_name"] = agentName
	}
	return s.WriteData(payload)
}

// WriteFinish writes finish_reason event; agentName 非空时注入 agent_name。
func (s *SSEWriter) WriteFinish(agentName, reason string) error {
	payload := map[string]interface{}{
		"role":          "assistant",
		"finish_reason": reason,
	}
	if agentName != "" {
		payload["agent_name"] = agentName
	}
	return s.WriteData(payload)
}

// WriteToolResult writes tool result event. Set isError to true for MCP tool errors
// so the TUI can render them with error styling instead of silently dropping them.
// agentName 非空时注入 agent_name。
func (s *SSEWriter) WriteToolResult(agentName, toolCallID, toolName, content string, isError bool) error {
	payload := map[string]interface{}{
		"role":         "tool",
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"content":      content,
		"error":        isError,
	}
	if agentName != "" {
		payload["agent_name"] = agentName
	}
	return s.WriteData(payload)
}

// WriteError writes an error SSE event; agentName 非空时注入 agent_name。
func (s *SSEWriter) WriteError(agentName, message string) error {
	payload := map[string]interface{}{
		"event":   "error",
		"message": message,
	}
	if agentName != "" {
		payload["agent_name"] = agentName
	}
	return s.WriteData(payload)
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
