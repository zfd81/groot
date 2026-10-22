// internal/repo/memory.go
package repo

import (
	"context"
	"time"
)

// Session holds metadata for a chat session.
type Session struct {
	SessionID string
	TenantID  string
	UserID    string
	Prompt    string
	Round     int
	// Title 为会话标题，持久化于 memory_sessions.title；由 CreateSession 写入，
	// GetSession/ListSessions 回填，UpdateTitle 可单独修改。
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
	Title       string // 所属会话标题（memory_sessions.title）
	Instruction string
	Result      string
	StartedAt   time.Time
}

// SessionOwner 为会话归属过滤条件：tenant_id 与 user_id 同时等值匹配。
// 空串是合法的匹配值（例如调度器创建的会话 tenant_id、user_id 均为空串）。
type SessionOwner struct {
	TenantID string
	UserID   string
}

type MemoryRepo interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	ExistsSession(ctx context.Context, sessionID string) (bool, error)
	// ListSessions 按 updated_at 倒序返回会话。owner 为 nil 时不过滤（全量）；
	// 非 nil 时只返回 tenant_id 与 user_id 同时等于 owner 对应字段的会话。
	ListSessions(ctx context.Context, owner *SessionOwner) ([]*Session, error)
	SaveChat(ctx context.Context, rec *ChatRecord) error
	GetChat(ctx context.Context, chatID string) (*ChatRecord, error)
	LoadHistory(ctx context.Context, sessionID string) ([]*ChatRecord, error)
	// UpdateTitle 只更新 title，不动 updated_at（以免影响 ListSessions 排序与
	// last_active_at）；影响 0 行时再查会话是否存在：存在返回 nil（标题未变，
	// MySQL 只统计变更行），不存在返回 ErrNotFound。
	UpdateTitle(ctx context.Context, sessionID, title string) error
	DeleteSession(ctx context.Context, sessionID string) error
	// SearchChats 在主 Agent 的已完成轮次（agent_name='' 且 status='completed'）的
	// instruction/result 中模糊匹配 keyword（大小写行为随数据库 collation）。
	// owner 为 nil 时不按归属过滤；非 nil 时只搜 tenant_id 与 user_id 同时匹配的会话（与 ListSessions 一致）。
	// 结果按轮次开始时间倒序，最多 limit 条。keyword 原样传入，LIKE 转义由实现负责。
	// limit 须为正数，非正数返回空结果。
	SearchChats(ctx context.Context, owner *SessionOwner, keyword string, limit int) ([]*SearchHit, error)
}
