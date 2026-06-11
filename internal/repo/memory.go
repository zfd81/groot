// internal/repo/memory.go
package repo

import (
	"context"
	"time"
)

// Session holds metadata for a chat session.
type Session struct {
	SessionID string
	UserID    string
	Prompt    string
	Round     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChatRecord is the canonical detailed record for a single chat turn.
// Defined here (in repo) so that memorydb and memory can both use it
// without an import cycle.
type ChatRecord struct {
	ChatID      string    `json:"chat_id"`
	SessionID   string    `json:"session_id"`
	Round       int       `json:"round"`
	Prompt      string    `json:"prompt"`
	Timestamp   time.Time `json:"timestamp"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	Instruction string    `json:"instruction"`
	Result      string    `json:"result"`
	Status      string    `json:"status"`
	// Deprecated: use DurationMs. Duration = DurationMs/1000 for API backward compat.
	Duration         int     `json:"duration"`
	DurationMs       int64   `json:"duration_ms"`
	Caller           string  `json:"caller"`
	Steps            []Step  `json:"steps"`
	AgentName        string  `json:"agent_name,omitempty"`
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
	Error            *Error  `json:"error"`
}

// Step is a single execution step within a chat turn.
type Step struct {
	StepID       string    `json:"step_id"`
	Type         string    `json:"type"` // skill/tool/llm/thinking
	Name         string    `json:"name"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Status       string    `json:"status"` // success/failed
	NestingLevel int       `json:"nesting_level"`
	Error        *Error    `json:"error"`
}

// Error carries error code and message for a failed step or chat.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type MemoryRepo interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	ExistsSession(ctx context.Context, sessionID string) (bool, error)
	ListSessions(ctx context.Context) ([]*Session, error)
	SaveChat(ctx context.Context, rec *ChatRecord) error
	GetChat(ctx context.Context, chatID string) (*ChatRecord, error)
	LoadHistory(ctx context.Context, sessionID string) ([]*ChatRecord, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteExpiredSessions(ctx context.Context, expiredBefore time.Time) (int, error)
}
