package memory

import "time"

// SessionInfo 会话信息
type SessionInfo struct {
	SessionID      string    `json:"session_id"`
	CreatedAt      time.Time `json:"created_at"`
	RoundCount     int       `json:"round_count"`
	LastActiveAt   string    `json:"last_active_at"` // 最后活跃时间
	Path           string    `json:"path"`
}

// Message 单轮对话记录（存储在 history.json）
type Message struct {
	Round             int       `json:"round"`
	ChatID            string    `json:"chat_id"`
	Timestamp         time.Time `json:"timestamp"`
	Instruction       string    `json:"instruction"`
	Attachments       []string  `json:"attachments"`
	Result            string    `json:"result"`
	ResultAttachments []string  `json:"result_attachments"`
	Status            string    `json:"status"` // completed/failed/cancelled
	Duration          int       `json:"duration"` // 秒
	StepsCount        int       `json:"steps_count"`
	Error             *Error    `json:"error"` // 移除 omitempty，总是输出
}

// History 会话历史（history.json 根结构）
type History struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	Messages  []Message `json:"messages"`
}

// ChatRecord 单次对话详细执行记录（chats/{chat_id}.json）
type ChatRecord struct {
	ChatID            string      `json:"chat_id"`
	SessionID         string      `json:"session_id"`
	Round             int         `json:"round"`
	Timestamp         time.Time   `json:"timestamp"`
	StartedAt         time.Time   `json:"started_at"` // 开始时间
	EndedAt           time.Time   `json:"ended_at"`   // 结束时间
	Instruction       string      `json:"instruction"`
	Attachments       []string    `json:"attachments"`
	Result            string      `json:"result"`
	ResultAttachments []string    `json:"result_attachments"`
	Status            string      `json:"status"`
	Duration          int         `json:"duration"`
	Caller            string      `json:"caller"`
	Steps             []Step      `json:"steps"`
	Error             *Error      `json:"error"` // 移除 omitempty，总是输出
}

// Step 单步执行记录
type Step struct {
	StepID       string    `json:"step_id"`
	Type         string    `json:"type"` // skill/tool/llm
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

// AttachmentPath 附件路径信息（传递给 Agent）
type AttachmentPath struct {
	OriginalName string
	Type         string // file/url
	FullPath     string
	RelativePath string
	Size         int64
	ContentType  string
}