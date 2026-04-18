package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// SSEWriter writes Server-Sent Events
type SSEWriter struct {
	writer io.Writer
}

// NewSSEWriter creates a new SSE writer
func NewSSEWriter(w io.Writer) *SSEWriter {
	return &SSEWriter{writer: w}
}

// WriteEvent writes an SSE event
func (s *SSEWriter) WriteEvent(event string, data interface{}) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}

	// SSE format: event: <event>\ndata: <json>\n\n
	line := fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(dataBytes))
	_, err = s.writer.Write([]byte(line))
	return err
}

// WriteIntent writes intent event
func (s *SSEWriter) WriteIntent(round int) error {
	return s.WriteEvent("intent", map[string]interface{}{
		"round":     round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// WriteStepStart writes step_start event
func (s *SSEWriter) WriteStepStart(stepID, typ, name string, nestingLevel int, params map[string]interface{}) error {
	data := map[string]interface{}{
		"type":          typ,
		"name":          name,
		"step_id":       stepID,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
		"nesting_level": nestingLevel,
	}
	if params != nil {
		data["params"] = params
	}
	return s.WriteEvent("step_start", data)
}

// WriteStepEnd writes step_end event
func (s *SSEWriter) WriteStepEnd(stepID, status string, errInfo *StepError) error {
	data := map[string]interface{}{
		"step_id":   stepID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"status":    status,
	}
	if errInfo != nil {
		data["error"] = errInfo
	}
	return s.WriteEvent("step_end", data)
}

// WriteProgress writes progress event
func (s *SSEWriter) WriteProgress(stepID, message string) error {
	data := map[string]string{
		"message":   message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	if stepID != "" {
		data["step_id"] = stepID
	}
	return s.WriteEvent("progress", data)
}

// WriteCompleted writes completed event
func (s *SSEWriter) WriteCompleted(status, duration string, round int, result interface{}, errInfo *StepError, message string) error {
	data := map[string]interface{}{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"duration":  duration,
		"round":     round,
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
