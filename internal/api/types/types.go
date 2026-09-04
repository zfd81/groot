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

// ToolsGroup represents a group of tools from a single MCP.
// Type/Description 来自 MCP 定义（config 中的 type 与 description 字段）；
// 合成分组（如 _builtin）二者为空。
type ToolsGroup struct {
	Type        string     `json:"type,omitempty"`
	Description string     `json:"description,omitempty"`
	Tools       []ToolInfo `json:"tools"`
	Total       int        `json:"total"`
}

// ModelsResponse represents models list response
type ModelsResponse struct {
	Models  []ModelInfo `json:"models"`
	Default string      `json:"default"`
	Total   int         `json:"total"`
}

// ModelInfo represents model information（api_key 为脱敏后的展示值）
type ModelInfo struct {
	Name                string   `json:"name"`
	Model               string   `json:"model"`
	BaseURL             string   `json:"base_url"`
	APIKey              string   `json:"api_key"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	MaxContextTokens    int      `json:"max_context_tokens"`
	Temperature         float64  `json:"temperature"`
	TopP                float64  `json:"top_p"`
	FrequencyPenalty    float64  `json:"frequency_penalty"`
	PresencePenalty     float64  `json:"presence_penalty"`
	Seed                int      `json:"seed"`
	Stop                []string `json:"stop"`
	Thinking            bool     `json:"thinking"`
	IsDefault           bool     `json:"is_default"`
	Enabled             bool     `json:"enabled"`
}

// ErrorResponse represents error response
type ErrorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// AgentInfo 列出 Agent 接口的单条信息（GET /agents 响应元素）。
// 每个 Agent 携带其 skills 列表摘要；skills 仅包含 name/description，详情走 GET /skills。
type AgentInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Skills      []SkillInfo `json:"skills"`
}

// AgentsResponse 是 GET /agents 的完整响应体。
// 主 Agent（"groot"）始终位于 Agents[0]，其余按字典序排列。
type AgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// AgentDefinitionResponse 是 GET /web/agents/:name/definition 的响应体。
// Content 为定义文件原文（含 frontmatter）；File 为文件名：
// 主 Agent 是 GROOT.md，子 Agent 是 agent.md。
type AgentDefinitionResponse struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Content string `json:"content"`
}

// ClusterMemberInfo 列出集群成员接口的单条信息（GET /web/cluster 响应元素）。
type ClusterMemberInfo struct {
	RegID       string `json:"reg_id"`
	Role        string `json:"role"`
	Address     string `json:"address"`
	Pid         int    `json:"pid"`
	HeartbeatAt int64  `json:"heartbeat_at"`
	CreatedAt   int64  `json:"created_at"`
}

// ClusterResponse 是 GET /web/cluster 的完整响应体。
type ClusterResponse struct {
	Members []ClusterMemberInfo `json:"members"`
}
