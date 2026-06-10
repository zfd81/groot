package memory

import "time"

// SessionInfo 会话信息
type SessionInfo struct {
	SessionID    string    `json:"session_id"`
	CreatedAt    time.Time `json:"created_at"`
	RoundCount   int       `json:"round_count"`
	LastActiveAt string    `json:"last_active_at"` // 最后活跃时间
	Path         string    `json:"path"`
}

// Message 单轮对话记录（存储在 history.json）
type Message struct {
	Round       int       `json:"round"`
	ChatID      string    `json:"chat_id"`
	Timestamp   time.Time `json:"timestamp"`
	Instruction string    `json:"instruction"`
	Result      string    `json:"result"`
	Status      string    `json:"status"`   // completed/failed/cancelled
	Duration    int       `json:"duration"` // 秒
	StepsCount  int       `json:"steps_count"`
	AgentName   string    `json:"agent_name,omitempty"`
	Error       *Error    `json:"error"`
}

// History 会话历史（history.json 根结构）
type History struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	Messages  []Message `json:"messages"`
}

// ChatRecord 单次对话详细执行记录（chats/{chat_id}.json）。
//
// JSON 字段策略：
//   - Error 不带 omitempty：兼容承诺，消费方可稳定假设此 key 存在（值为 null 或对象）。
//   - 多 Agent 扩展字段（AgentName/PromptTokens/CompletionTokens/TotalTokens）使用 omitempty，
//     保证主 Agent 主路径下的 JSON 输出格式不变。
type ChatRecord struct {
	ChatID      string    `json:"chat_id"`
	SessionID   string    `json:"session_id"`
	Round       int       `json:"round"`
	Timestamp   time.Time `json:"timestamp"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	Instruction string    `json:"instruction"`
	Result      string    `json:"result"`
	Status      string    `json:"status"`
	Duration    int       `json:"duration"`
	Caller      string    `json:"caller"`
	Steps       []Step    `json:"steps"`
	AgentName        string `json:"agent_name,omitempty"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
	Error            *Error `json:"error"`
}

// Step 单步执行记录
type Step struct {
	StepID       string    `json:"step_id"`
	Type         string    `json:"type"` // skill/tool/llm/thinking
	Name         string    `json:"name"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Status       string    `json:"status"` // success/failed
	NestingLevel int       `json:"nesting_level"`
	Error        *Error    `json:"error"` // 移除 omitempty，总是输出
}

// Error 错误信息
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
