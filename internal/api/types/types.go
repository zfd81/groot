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
// Self 是处理本次请求的实例的 reg_id；未注册集群时为空串。
type ClusterResponse struct {
	Members []ClusterMemberInfo `json:"members"`
	Self    string              `json:"self"`
}

// RestartResponse 是 POST /web/cluster/:reg_id/restart 的 202 响应体。
// Self 为 true 表示被重启的就是处理本次请求的实例，前端据此等待恢复并引导重新登录。
type RestartResponse struct {
	Status string `json:"status"`
	RegID  string `json:"reg_id"`
	Self   bool   `json:"self"`
}

// VoiceSettingsRequest 是 PUT /web/settings/voice 的请求体。
// 三个字段整体保存，不支持部分更新：设置面板一次提交整个分区。
type VoiceSettingsRequest struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model"`
	AutoSend bool   `json:"auto_send"`
}

// VoiceSettingsResponse 是 GET /web/settings/voice 的响应体。
type VoiceSettingsResponse struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model"`
	AutoSend bool   `json:"auto_send"`
}

// RuntimeSettingsPayload 是 /web/settings/runtime 的请求与响应体。
// 读写同构：界面按分区整体提交，回读的字段与提交的字段一一对应，
// 前端无需为两个方向维护两套结构。
//
// 不含 security.rate_limit.cleanup_interval：它是后台回收协程的周期，改动需重启才生效。
// 也不含 schedule.max_concurrent_tasks 与 schedule.sync_interval：它们是调度器的构造参数，
// 改动同样需重启。
//
// 分区字段为指针当且仅当其全零值是合法配置；其余分区的零值会被 Validate 拒绝，
// 缺失即被捕获，无需指针。
type RuntimeSettingsPayload struct {
	Memory     MemorySettings     `json:"memory"`
	React      ReactSettings      `json:"react"`
	SubAgent   SubAgentSettings   `json:"subagent"`
	Attachment AttachmentSettings `json:"attachment"`
	// 指针：全零对该分区是合法值（关闭限流、各维度不限制），
	// 需要区分「未携带」与「显式关闭」，未携带时拒绝而不是静默关掉限流。
	RateLimit *RateLimitSettings `json:"rate_limit"`
	// 指针：{enabled:false} 是合法值，需要区分「未携带」与「显式关闭」
	Schedule *ScheduleSettings `json:"schedule"`
}

// MemorySettings 对话历史相关配置。
type MemorySettings struct {
	HistoryWindow int `json:"history_window"` // LLM 上下文窗口（轮次），-1 不限制
}

// ReactSettings ReAct 循环的执行限额。
type ReactSettings struct {
	MaxIterations int `json:"max_iterations"`
	StepTimeout   int `json:"step_timeout"` // 单步 LLM 调用超时（秒）
	ErrorRetry    int `json:"error_retry"`
}

// SubAgentSettings 子 Agent 的执行限额。
type SubAgentSettings struct {
	MaxConcurrency  int    `json:"max_concurrency"` // 同时运行的子 Agent 数上限
	ExecTimeout     string `json:"exec_timeout"`    // Go duration 字面量，如 "5m"
	MaxTaskLength   int    `json:"max_task_length"`
	MaxResultLength int    `json:"max_result_length"`
}

// AttachmentSettings 附件上传限额。
type AttachmentSettings struct {
	MaxSize      int      `json:"max_size"`       // 单文件上限（MB）
	MaxTotalSize int      `json:"max_total_size"` // 单次合计上限（MB）
	MaxCount     int      `json:"max_count"`
	AllowedTypes []string `json:"allowed_types"` // 空数组表示不限制
}

// RateLimitSettings 限流参数。QPS 与并发上限为 0 表示该维度不限制。
type RateLimitSettings struct {
	Enabled            bool    `json:"enabled"`
	GlobalQPS          float64 `json:"global_qps"`
	GlobalConcurrency  int     `json:"global_concurrency"`
	DefaultQPS         float64 `json:"default_qps"`
	DefaultConcurrency int     `json:"default_concurrency"`
}

// ScheduleSettings 定时任务相关的可配置项。
// 只有开关：并发数与同步间隔是调度器构造参数，改动需重启，不在此列。
type ScheduleSettings struct {
	Enabled bool `json:"enabled"` // 是否允许在对话中创建定时任务
}

// SendersPayload 是 /web/settings/senders 的请求与响应体。
// 读写同构，但 password 字段方向不同：响应中是脱敏值，
// 请求中空串表示不改密码。
// 密码一旦设置无法经接口清空，需换新密码或关闭渠道。
type SendersPayload struct {
	Senders map[string]SenderSettings `json:"senders"`
}

// SenderSettings 单个发送渠道的参数。
// 字段是两个渠道的并集：webhook 只用 url，email 用其余几项。
type SenderSettings struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}

// AuthSettingsPayload 是 /web/settings/auth 系列接口的响应体。
// 密钥只以脱敏形式返回，界面据 secret_set 判断密钥是否已设置。
type AuthSettingsPayload struct {
	HeaderName        string `json:"header_name"`         // 当前生效的 API Key 请求头名
	HeaderNameDefault string `json:"header_name_default"` // 默认请求头名，供界面「恢复默认」提示
	SecretMasked      string `json:"secret_masked"`       // 脱敏后的 JWT 签名密钥；未设置时为空串
	SecretSet         bool   `json:"secret_set"`          // 密钥是否已设置
}

// PutAuthSettingsRequest 是 PUT /web/settings/auth 的请求体。
// header_name 为空串表示恢复默认请求头名。
type PutAuthSettingsRequest struct {
	HeaderName string `json:"header_name"`
}
