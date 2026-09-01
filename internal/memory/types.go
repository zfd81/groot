package memory

import (
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// Type aliases for backward compatibility — callers can continue using
// memory.ChatRecord, memory.Step, memory.Error without any change.
type ChatRecord = repo.ChatRecord
type Step = repo.Step
type Error = repo.Error

// SessionInfo 会话信息
type SessionInfo struct {
	SessionID    string    `json:"session_id"`
	CreatedAt    time.Time `json:"created_at"`
	RoundCount   int       `json:"round_count"`
	LastActiveAt string    `json:"last_active_at"` // 最后活跃时间
	Title        string    `json:"title"`          // 首轮用户指令，供列表展示；无对话时为空
	Path         string    `json:"path"`
}

// Message 单轮对话记录（用于 History 消息列表）
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

// History 会话历史（从 DB 重建，保留结构兼容性）
type History struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	Messages  []Message `json:"messages"`
}
