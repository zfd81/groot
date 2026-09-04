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
	// Title 为该会话首轮主 Agent 对话的用户指令，仅由 ListSessions 填充，
	// 供列表界面展示；无对话记录时为空串。
	Title     string
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
	Duration         int    `json:"duration"`
	DurationMs       int64  `json:"duration_ms"`
	Caller           string `json:"caller"`
	Steps            []Step `json:"steps"`
	AgentName        string `json:"agent_name,omitempty"`
	Model            string `json:"model,omitempty"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
	Error            *Error `json:"error"`
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

// SearchHit 为一条搜索命中的原始数据：命中的轮次及其全文字段。
// 摘要（snippet）截取由上层 memory.Manager 完成，repo 只返回原文。
type SearchHit struct {
	SessionID   string
	ChatID      string
	Round       int
	Title       string // 所属会话标题（首轮主 Agent 指令），无对话记录时为空串
	Instruction string
	Result      string
	StartedAt   time.Time
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
	// SearchChats 在主 Agent 的已完成轮次（agent_name='' 且 status='completed'）的
	// instruction/result 中模糊匹配 keyword（大小写行为随数据库 collation）。
	// userID 非空时只搜该用户的会话；为空时不按用户过滤（与 ListSessions 行为一致）。
	// 结果按轮次开始时间倒序，最多 limit 条。keyword 原样传入，LIKE 转义由实现负责。
	// limit 须为正数，非正数返回空结果。
	SearchChats(ctx context.Context, userID, keyword string, limit int) ([]*SearchHit, error)
}
