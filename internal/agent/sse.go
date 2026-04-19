package agent

import (
	"encoding/json"
	"fmt"
	"time"

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

// WriteEvent writes an SSE event
func (s *SSEWriter) WriteEvent(event string, data interface{}) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}

	// SSE format: event: <event>\ndata: <json>\n\n
	line := fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(dataBytes))
	_, err = s.rc.Write([]byte(line))
	return err
}

// timestamp returns current UTC timestamp in ISO 8601 format
func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// WriteStarted writes started event (整体开始信号，必须发送)
func (s *SSEWriter) WriteStarted() error {
	return s.WriteEvent("started", map[string]string{
		"session_id": s.sessionID,
		"chat_id":    s.chatID,
		"timestamp":  timestamp(),
	})
}

// WriteThinkingStart writes thinking_start event (思考阶段开始)
func (s *SSEWriter) WriteThinkingStart(stepID string) error {
	return s.WriteEvent("thinking_start", map[string]string{
		"step_id":   stepID,
		"timestamp": timestamp(),
	})
}

// WriteThinking writes thinking event (思考内容流式输出)
func (s *SSEWriter) WriteThinking(content string) error {
	return s.WriteEvent("thinking", map[string]string{
		"content":   content,
		"timestamp": timestamp(),
	})
}

// WriteThinkingEnd writes thinking_end event (思考阶段结束)
func (s *SSEWriter) WriteThinkingEnd(stepID, status string) error {
	return s.WriteEvent("thinking_end", map[string]string{
		"step_id":   stepID,
		"status":    status,
		"timestamp": timestamp(),
	})
}

// WriteToolCall writes tool_call event (工具调用请求)
func (s *SSEWriter) WriteToolCall(stepID, name string, arguments map[string]interface{}) error {
	data := map[string]interface{}{
		"step_id":   stepID,
		"name":      name,
		"arguments": arguments,
		"timestamp": timestamp(),
	}
	return s.WriteEvent("tool_call", data)
}

// WriteToolResult writes tool_result event (工具执行结果)
func (s *SSEWriter) WriteToolResult(stepID, output string, errStr string) error {
	data := map[string]interface{}{
		"step_id":   stepID,
		"timestamp": timestamp(),
	}
	if output != "" {
		data["output"] = output
	}
	if errStr != "" {
		data["error"] = errStr
	}
	return s.WriteEvent("tool_result", data)
}

// WriteMessageStart writes message_start event (最终输出开始)
func (s *SSEWriter) WriteMessageStart() error {
	return s.WriteEvent("message_start", map[string]string{
		"timestamp": timestamp(),
	})
}

// WriteMessage writes message event (最终回答流式输出)
func (s *SSEWriter) WriteMessage(content string) error {
	return s.WriteEvent("message", map[string]string{
		"content":   content,
		"timestamp": timestamp(),
	})
}

// WriteMessageEnd writes message_end event (最终输出结束)
func (s *SSEWriter) WriteMessageEnd() error {
	return s.WriteEvent("message_end", map[string]string{
		"timestamp": timestamp(),
	})
}

// WriteCompleted writes completed event (对话完成，整体结束信号)
func (s *SSEWriter) WriteCompleted(status, duration string, result interface{}, errInfo *StepError, message string) error {
	data := map[string]interface{}{
		"status":    status,
		"timestamp": timestamp(),
		"duration":  duration,
		"round":     s.round,
		"chat_id":   s.chatID,
	}
	if result != nil {
		data["result"] = result
	}
	if errInfo != nil {
		data["error"] = errInfo
	}
	if message != "" {
		data["message"] = message
	}
	return s.WriteEvent("completed", data)
}

// StepError represents step error info
type StepError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}