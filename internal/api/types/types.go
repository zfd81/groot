package types

import (
	"time"
)

// ExecuteRequest represents task execute request
type ExecuteRequest struct {
	Instruction string       `json:"instruction"`
	Prompt      string       `json:"prompt,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment represents file attachment
type Attachment struct {
	Type    string `json:"type"` // file, image
	Name    string `json:"name"`
	Content string `json:"content"` // Base64
}

// ExecuteResponse represents SSE event response
type ExecuteResponse struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// IntentEvent represents intent SSE event
type IntentEvent struct {
	Timestamp string `json:"timestamp"`
}

// StepStartEvent represents step_start SSE event
type StepStartEvent struct {
	Type         string                 `json:"type"`
	Name         string                 `json:"name"`
	StepID       string                 `json:"step_id"`
	Timestamp    string                 `json:"timestamp"`
	NestingLevel int                    `json:"nesting_level,omitempty"`
	Params       map[string]interface{} `json:"params,omitempty"`
}

// StepEndEvent represents step_end SSE event
type StepEndEvent struct {
	StepID    string     `json:"step_id"`
	Timestamp string     `json:"timestamp"`
	Status    string     `json:"status"`
	Error     *ErrorInfo `json:"error,omitempty"`
}

// ProgressEvent represents progress SSE event
type ProgressEvent struct {
	StepID    string `json:"step_id,omitempty"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// CompletedEvent represents completed SSE event
type CompletedEvent struct {
	Status    string      `json:"status"`
	Timestamp string      `json:"timestamp"`
	Duration  string      `json:"duration"`
	Result    interface{} `json:"result,omitempty"`
	Error     *ErrorInfo  `json:"error,omitempty"`
	Message   string      `json:"message,omitempty"`
}

// ErrorInfo represents error information
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StatusResponse represents status response
type StatusResponse struct {
	Status      string        `json:"status"`
	TaskID      string        `json:"task_id"`
	TaskStatus  string        `json:"task_status,omitempty"`
	Progress    *ProgressInfo `json:"progress,omitempty"`
	StartedAt   string        `json:"started_at,omitempty"`
	ElapsedTime string        `json:"elapsed_time,omitempty"`
	Message     string        `json:"message,omitempty"`
}

// ProgressInfo represents task progress
type ProgressInfo struct {
	CurrentStep    int `json:"current_step"`
	StepsCompleted int `json:"steps_completed"`
	Percentage     int `json:"percentage"`
}

// HistoryResponse represents history response
type HistoryResponse struct {
	Status string        `json:"status"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
	Tasks  []TaskSummary `json:"tasks"`
}

// TaskSummary represents task summary for history
type TaskSummary struct {
	ID          string    `json:"id"`
	Instruction string    `json:"instruction"`
	Status      string    `json:"status"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time,omitempty"`
	Duration    int       `json:"duration"`
	Caller      string    `json:"caller"`
}

// DetailResponse represents task detail response
type DetailResponse struct {
	Status  string      `json:"status"`
	Task    *TaskDetail `json:"task,omitempty"`
	Message string      `json:"message,omitempty"`
}

// TaskDetail represents full task detail
type TaskDetail struct {
	ID          string       `json:"id"`
	Instruction string       `json:"instruction"`
	Prompt      string       `json:"prompt,omitempty"`
	Status      string       `json:"status"`
	StartTime   time.Time    `json:"start_time"`
	EndTime     time.Time    `json:"end_time,omitempty"`
	Duration    int          `json:"duration"`
	Caller      string       `json:"caller"`
	Result      interface{}  `json:"result,omitempty"`
	Error       *ErrorInfo   `json:"error,omitempty"`
	Steps       []StepDetail `json:"steps,omitempty"`
}

// StepDetail represents step detail
type StepDetail struct {
	StepID       string     `json:"step_id"`
	Type         string     `json:"type"`
	Name         string     `json:"name"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      time.Time  `json:"end_time,omitempty"`
	Status       string     `json:"status"`
	NestingLevel int        `json:"nesting_level"`
	Error        *ErrorInfo `json:"error,omitempty"`
}

// HealthResponse represents health check response
type HealthResponse struct {
	Status  string                 `json:"status"`
	Version string                 `json:"version"`
	Uptime  string                 `json:"uptime"`
	Checks  map[string]CheckInfo   `json:"checks"`
	Metrics map[string]interface{} `json:"metrics"`
}

// CheckInfo represents health check info
type CheckInfo struct {
	Status string      `json:"status"`
	Info   interface{} `json:"info,omitempty"`
}

// SkillsResponse represents skills list response
type SkillsResponse struct {
	Skills []SkillInfo `json:"skills"`
	Total  int         `json:"total"`
}

// SkillInfo represents skill information
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolsResponse represents tools list response
type ToolsResponse struct {
	Tools []ToolInfo `json:"tools"`
	Total int        `json:"total"`
}

// ToolInfo represents tool information
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MCP         string `json:"mcp,omitempty"`
}

// ToolsGroup represents a group of tools from a single MCP
type ToolsGroup struct {
	Tools []ToolInfo `json:"tools"`
	Total int        `json:"total"`
}

// ModelsResponse represents models list response
type ModelsResponse struct {
	Models  []ModelInfo `json:"models"`
	Default string      `json:"default"`
	Total   int         `json:"total"`
}

// ModelInfo represents model information
type ModelInfo struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	BaseURL string `json:"base_url"`
}

// ErrorResponse represents error response
type ErrorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}
