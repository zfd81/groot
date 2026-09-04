package memorydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type memoryRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.MemoryRepo {
	return &memoryRepo{db: sqlxDB, dialect: dialect}
}

// --- Chat helpers ---

type chatRow struct {
	ChatID           string `db:"chat_id"`
	SessionID        string `db:"session_id"`
	Round            int    `db:"round"`
	AgentName        string `db:"agent_name"`
	Caller           string `db:"caller"`
	Prompt           string `db:"prompt"`
	Instruction      string `db:"instruction"`
	Result           string `db:"result"`
	Steps            string `db:"steps"`
	Status           string `db:"status"`
	Error            string `db:"error"`
	Model            string `db:"model"`
	PromptTokens     int    `db:"prompt_tokens"`
	CompletionTokens int    `db:"completion_tokens"`
	TotalTokens      int    `db:"total_tokens"`
	DurationMs       int64  `db:"duration_ms"`
	StartedAt        int64  `db:"started_at"`
	FinishedAt       *int64 `db:"finished_at"`
}

func rowToChatRecord(row chatRow) *repo.ChatRecord {
	rec := &repo.ChatRecord{
		ChatID:           row.ChatID,
		SessionID:        row.SessionID,
		Round:            row.Round,
		AgentName:        row.AgentName,
		Caller:           row.Caller,
		Prompt:           row.Prompt,
		Instruction:      row.Instruction,
		Result:           row.Result,
		Status:           row.Status,
		Model:            row.Model,
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		TotalTokens:      row.TotalTokens,
		DurationMs:       row.DurationMs,
		Duration:         int(row.DurationMs / 1000),
		StartedAt:        time.UnixMilli(row.StartedAt),
	}
	if row.FinishedAt != nil {
		rec.EndedAt = time.UnixMilli(*row.FinishedAt)
	}
	var steps []repo.Step
	if err := json.Unmarshal([]byte(row.Steps), &steps); err == nil {
		rec.Steps = steps
	}
	if row.Error != "" {
		var e repo.Error
		if err := json.Unmarshal([]byte(row.Error), &e); err == nil {
			rec.Error = &e
		}
	}
	return rec
}

const chatSelectCols = `chat_id, session_id, round, agent_name, caller, prompt, instruction, result, steps,
	status, error, model, prompt_tokens, completion_tokens, total_tokens,
	duration_ms, started_at, finished_at`

// --- Session methods ---

func (r *memoryRepo) CreateSession(ctx context.Context, s *repo.Session) error {
	q := r.db.Rebind(`INSERT INTO memory_sessions (session_id, user_id, prompt, round, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		s.SessionID, s.UserID, s.Prompt,
		s.CreatedAt.UnixMilli(), s.UpdatedAt.UnixMilli(),
	)
	if err != nil {
		return repo.ErrConflict
	}
	return nil
}

func (r *memoryRepo) GetSession(ctx context.Context, sessionID string) (*repo.Session, error) {
	var row struct {
		SessionID string `db:"session_id"`
		UserID    string `db:"user_id"`
		Prompt    string `db:"prompt"`
		Round     int    `db:"round"`
		CreatedAt int64  `db:"created_at"`
		UpdatedAt int64  `db:"updated_at"`
	}
	q := r.db.Rebind(`SELECT session_id, user_id, prompt, round, created_at, updated_at FROM memory_sessions WHERE session_id=?`)
	err := r.db.GetContext(ctx, &row, q, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &repo.Session{
		SessionID: row.SessionID,
		UserID:    row.UserID,
		Prompt:    row.Prompt,
		Round:     row.Round,
		CreatedAt: time.UnixMilli(row.CreatedAt),
		UpdatedAt: time.UnixMilli(row.UpdatedAt),
	}, nil
}

func (r *memoryRepo) ExistsSession(ctx context.Context, sessionID string) (bool, error) {
	var n int
	q := r.db.Rebind(`SELECT COUNT(*) FROM memory_sessions WHERE session_id=?`)
	err := r.db.QueryRowContext(ctx, q, sessionID).Scan(&n)
	return n > 0, err
}

func (r *memoryRepo) ListSessions(ctx context.Context) ([]*repo.Session, error) {
	var rows []struct {
		SessionID string `db:"session_id"`
		UserID    string `db:"user_id"`
		Prompt    string `db:"prompt"`
		Round     int    `db:"round"`
		Title     string `db:"title"`
		CreatedAt int64  `db:"created_at"`
		UpdatedAt int64  `db:"updated_at"`
	}
	// title 取会话首轮主 Agent 对话（agent_name='' 排除子 Agent 记录）的用户指令，
	// 供列表界面直接展示，避免前端为每个会话再拉一次详情。
	if err := r.db.SelectContext(ctx, &rows,
		`SELECT s.session_id, s.user_id, s.prompt, s.round, s.created_at, s.updated_at,
			COALESCE((SELECT c.instruction FROM memory_chats c
				WHERE c.session_id = s.session_id AND c.agent_name = ''
				ORDER BY c.round ASC LIMIT 1), '') AS title
		 FROM memory_sessions s ORDER BY s.updated_at DESC`); err != nil {
		return nil, err
	}
	sessions := make([]*repo.Session, len(rows))
	for i, row := range rows {
		sessions[i] = &repo.Session{
			SessionID: row.SessionID,
			UserID:    row.UserID,
			Prompt:    row.Prompt,
			Round:     row.Round,
			Title:     row.Title,
			CreatedAt: time.UnixMilli(row.CreatedAt),
			UpdatedAt: time.UnixMilli(row.UpdatedAt),
		}
	}
	return sessions, nil
}

// --- Chat methods ---

func (r *memoryRepo) SaveChat(ctx context.Context, rec *repo.ChatRecord) error {
	stepsJSON, _ := json.Marshal(rec.Steps)
	stepsStr := string(stepsJSON)
	if stepsStr == "null" {
		stepsStr = "[]"
	}
	var errJSON string
	if rec.Error != nil {
		b, _ := json.Marshal(rec.Error)
		errJSON = string(b)
	}
	var finishedAtMs interface{}
	if !rec.EndedAt.IsZero() {
		finishedAtMs = rec.EndedAt.UnixMilli()
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 子 Agent 记录（agent_name 非空）使用 rec.Round（父轮次），不推进 session.round。
	// 主 Agent 记录使用 session.round + 1，并在事务内 CAS 推进 session.round。
	isChild := rec.AgentName != ""

	var roundToInsert int
	var curRound int
	if isChild {
		// 校验 session 存在但不读 round
		var exists int
		q0 := r.db.Rebind(`SELECT COUNT(*) FROM memory_sessions WHERE session_id=?`)
		if err = tx.QueryRowContext(ctx, q0, rec.SessionID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return repo.ErrNotFound
		}
		roundToInsert = rec.Round
	} else {
		q1 := r.db.Rebind(`SELECT round FROM memory_sessions WHERE session_id=?`)
		err = tx.QueryRowContext(ctx, q1, rec.SessionID).Scan(&curRound)
		if errors.Is(err, sql.ErrNoRows) {
			return repo.ErrNotFound
		}
		if err != nil {
			return err
		}
		roundToInsert = curRound + 1
	}

	q2 := r.db.Rebind(`INSERT INTO memory_chats
		   (chat_id, session_id, round, agent_name, caller, prompt, instruction, result, steps,
		    status, error, model, prompt_tokens, completion_tokens, total_tokens,
		    duration_ms, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err = tx.ExecContext(ctx, q2,
		rec.ChatID, rec.SessionID, roundToInsert, rec.AgentName, rec.Caller, rec.Prompt,
		rec.Instruction, rec.Result, stepsStr,
		rec.Status, errJSON, rec.Model,
		rec.PromptTokens, rec.CompletionTokens, rec.TotalTokens,
		rec.DurationMs, rec.StartedAt.UnixMilli(), finishedAtMs,
	)
	if err != nil {
		return repo.ErrConflict
	}

	if !isChild {
		q3 := r.db.Rebind(`UPDATE memory_sessions SET round=?, updated_at=? WHERE session_id=? AND round=?`)
		res, err := tx.ExecContext(ctx, q3,
			roundToInsert, time.Now().UnixMilli(), rec.SessionID, curRound,
		)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return repo.ErrConflict
		}
	} else {
		q3 := r.db.Rebind(`UPDATE memory_sessions SET updated_at=? WHERE session_id=?`)
		if _, err = tx.ExecContext(ctx, q3, time.Now().UnixMilli(), rec.SessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *memoryRepo) GetChat(ctx context.Context, chatID string) (*repo.ChatRecord, error) {
	var row chatRow
	q := r.db.Rebind(`SELECT ` + chatSelectCols + ` FROM memory_chats WHERE chat_id=?`)
	err := r.db.GetContext(ctx, &row, q, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToChatRecord(row), nil
}

func (r *memoryRepo) LoadHistory(ctx context.Context, sessionID string) ([]*repo.ChatRecord, error) {
	var rows []chatRow
	q := r.db.Rebind(`SELECT ` + chatSelectCols + ` FROM memory_chats
		 WHERE session_id=? AND status='completed' AND agent_name=''
		 ORDER BY round ASC`)
	if err := r.db.SelectContext(ctx, &rows, q, sessionID); err != nil {
		return nil, err
	}
	chats := make([]*repo.ChatRecord, len(rows))
	for i, row := range rows {
		chats[i] = rowToChatRecord(row)
	}
	return chats, nil
}

func (r *memoryRepo) DeleteSession(ctx context.Context, sessionID string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q1 := r.db.Rebind(`DELETE FROM memory_chats WHERE session_id=?`)
	if _, err := tx.ExecContext(ctx, q1, sessionID); err != nil {
		return err
	}
	q2 := r.db.Rebind(`DELETE FROM memory_sessions WHERE session_id=?`)
	if _, err := tx.ExecContext(ctx, q2, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Search ---

// escapeLike 转义 LIKE 模式中的特殊字符。
// 用 '!' 作转义符（配合 SQL 的 ESCAPE '!'）：'!' 在 SQLite/MySQL/Postgres 的
// 字符串字面量中都无特殊含义，规避 '\' 在 MySQL 字面量解析中的兼容问题。
func escapeLike(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}

func (r *memoryRepo) SearchChats(ctx context.Context, userID, keyword string, limit int) ([]*repo.SearchHit, error) {
	// 三方言对 LIMIT 0/负数行为不一致，非正数直接返回空结果。
	if limit <= 0 {
		return []*repo.SearchHit{}, nil
	}
	pattern := "%" + escapeLike(keyword) + "%"
	var rows []struct {
		SessionID   string `db:"session_id"`
		ChatID      string `db:"chat_id"`
		Round       int    `db:"round"`
		Title       string `db:"title"`
		Instruction string `db:"instruction"`
		Result      string `db:"result"`
		StartedAt   int64  `db:"started_at"`
	}
	// title 子查询与 ListSessions 的口径一致：首轮主 Agent 的 instruction。
	// (? = '' OR s.user_id = ?)：userID 为空串时不按用户过滤，与 ListSessions 行为一致。
	q := r.db.Rebind(`SELECT c.session_id, c.chat_id, c.round, c.instruction, c.result, c.started_at,
			COALESCE((SELECT c2.instruction FROM memory_chats c2
				WHERE c2.session_id = c.session_id AND c2.agent_name = ''
				ORDER BY c2.round ASC LIMIT 1), '') AS title
		 FROM memory_chats c
		 JOIN memory_sessions s ON s.session_id = c.session_id
		 WHERE c.agent_name = '' AND c.status = 'completed'
		   AND (? = '' OR s.user_id = ?)
		   AND (c.instruction LIKE ? ESCAPE '!' OR c.result LIKE ? ESCAPE '!')
		 ORDER BY c.started_at DESC, c.round DESC
		 LIMIT ?`)
	if err := r.db.SelectContext(ctx, &rows, q, userID, userID, pattern, pattern, limit); err != nil {
		return nil, err
	}
	hits := make([]*repo.SearchHit, len(rows))
	for i, row := range rows {
		hits[i] = &repo.SearchHit{
			SessionID:   row.SessionID,
			ChatID:      row.ChatID,
			Round:       row.Round,
			Title:       row.Title,
			Instruction: row.Instruction,
			Result:      row.Result,
			StartedAt:   time.UnixMilli(row.StartedAt),
		}
	}
	return hits, nil
}
