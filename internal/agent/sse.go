package agent

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"
)

// SSEWriter writes Server-Sent Events
type SSEWriter struct {
	rc        *app.RequestContext
	sessionID string
	chatID    string
	round     int
}

// NewSSEWriter creates a new SSE writer
func NewSSEWriter(rc *app.RequestContext, sessionID, chatID string, round int) *SSEWriter {
	return &SSEWriter{
		rc:        rc,
		sessionID: sessionID,
		chatID:    chatID,
		round:     round,
	}
}

// WriteData writes raw SSE data line
func (s *SSEWriter) WriteData(data interface{}) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}
	line := fmt.Sprintf("data: %s\n\n", string(dataBytes))
	_, err = s.rc.Write([]byte(line))
	return err
}

// WriteThinking writes thinking chunk (reasoning_content)
func (s *SSEWriter) WriteThinking(content string) error {
	return s.WriteData(map[string]string{
		"role":              "assistant",
		"reasoning_content": content,
	})
}

// WriteMessage writes message chunk (content)
func (s *SSEWriter) WriteMessage(content string) error {
	return s.WriteData(map[string]string{
		"role":    "assistant",
		"content": content,
	})
}

// WriteToolCalls writes tool_calls event
func (s *SSEWriter) WriteToolCalls(toolCalls []ToolCall) error {
	return s.WriteData(map[string]interface{}{
		"role":       "assistant",
		"tool_calls": toolCalls,
	})
}

// WriteFinish writes finish_reason event
func (s *SSEWriter) WriteFinish(reason string) error {
	return s.WriteData(map[string]string{
		"role":          "assistant",
		"finish_reason": reason,
	})
}

// WriteToolResult writes tool result event
func (s *SSEWriter) WriteToolResult(toolCallID, toolName, content string) error {
	return s.WriteData(map[string]string{
		"role":         "tool",
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"content":      content,
	})
}

// WriteDone writes [DONE] marker
func (s *SSEWriter) WriteDone() error {
	_, err := s.rc.Write([]byte("data: [DONE]\n\n"))
	return err
}

// ToolCall represents a tool call (OpenAI format)
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents function call details
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}