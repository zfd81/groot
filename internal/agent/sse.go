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

// WriteThinking writes thinking chunk (reasoning_content).
func (s *SSEWriter) WriteThinking(content string) error {
	return s.WriteData(map[string]string{
		"role":              "assistant",
		"reasoning_content": content,
	})
}

// WriteMessage writes message chunk (content).
func (s *SSEWriter) WriteMessage(content string) error {
	return s.WriteData(map[string]string{
		"role":    "assistant",
		"content": content,
	})
}

// WriteToolCalls writes tool_calls event.
func (s *SSEWriter) WriteToolCalls(toolCalls []ToolCall) error {
	return s.WriteData(map[string]interface{}{
		"role":       "assistant",
		"tool_calls": toolCalls,
	})
}

// WriteFinish writes finish_reason event.
func (s *SSEWriter) WriteFinish(reason string) error {
	return s.WriteData(map[string]string{
		"role":          "assistant",
		"finish_reason": reason,
	})
}

// WriteToolResult writes tool result event.
func (s *SSEWriter) WriteToolResult(toolCallID, toolName, content string) error {
	return s.WriteData(map[string]string{
		"role":         "tool",
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"content":      content,
	})
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
