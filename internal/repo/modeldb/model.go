package modeldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type modelRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.ModelRepo {
	return &modelRepo{db: sqlxDB, dialect: dialect}
}

type modelRow struct {
	ID                  int64   `db:"id"`
	Name                string  `db:"name"`
	BaseURL             string  `db:"base_url"`
	APIKey              string  `db:"api_key"`
	Model               string  `db:"model"`
	MaxCompletionTokens int     `db:"max_completion_tokens"`
	MaxContextTokens    int     `db:"max_context_tokens"`
	Temperature         float64 `db:"temperature"`
	TopP                float64 `db:"top_p"`
	FrequencyPenalty    float64 `db:"frequency_penalty"`
	PresencePenalty     float64 `db:"presence_penalty"`
	Seed                int     `db:"seed"`
	Stop                string  `db:"stop"`
	Thinking            bool    `db:"thinking"`
	DefaultFlags        int     `db:"default_flags"`
	Enabled             bool    `db:"enabled"`
	CreatedAt           int64   `db:"created_at"`
	UpdatedAt           int64   `db:"updated_at"`
}

const modelColumns = `id, name, base_url, api_key, model, max_completion_tokens, max_context_tokens,
	temperature, top_p, frequency_penalty, presence_penalty, seed, stop, thinking,
	default_flags, enabled, created_at, updated_at`

func rowToModel(row modelRow) *repo.Model {
	var stop []string
	// stop 序列化损坏时按空数组处理，不让单行脏数据拖垮整个列表
	if err := json.Unmarshal([]byte(row.Stop), &stop); err != nil {
		stop = []string{}
	}
	return &repo.Model{
		ID:                  row.ID,
		Name:                row.Name,
		BaseURL:             row.BaseURL,
		APIKey:              row.APIKey,
		Model:               row.Model,
		MaxCompletionTokens: row.MaxCompletionTokens,
		MaxContextTokens:    row.MaxContextTokens,
		Temperature:         row.Temperature,
		TopP:                row.TopP,
		FrequencyPenalty:    row.FrequencyPenalty,
		PresencePenalty:     row.PresencePenalty,
		Seed:                row.Seed,
		Stop:                stop,
		Thinking:            row.Thinking,
		DefaultFlags:        repo.DefaultFlag(row.DefaultFlags),
		Enabled:             row.Enabled,
		CreatedAt:           time.UnixMilli(row.CreatedAt),
		UpdatedAt:           time.UnixMilli(row.UpdatedAt),
	}
}

func stopJSON(stop []string) string {
	if stop == nil {
		stop = []string{}
	}
	b, err := json.Marshal(stop)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (r *modelRepo) Create(ctx context.Context, m *repo.Model) error {
	q := r.db.Rebind(`INSERT INTO models (name, base_url, api_key, model,
		max_completion_tokens, max_context_tokens, temperature, top_p,
		frequency_penalty, presence_penalty, seed, stop, thinking,
		default_flags, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		m.Name, m.BaseURL, m.APIKey, m.Model,
		m.MaxCompletionTokens, m.MaxContextTokens, m.Temperature, m.TopP,
		m.FrequencyPenalty, m.PresencePenalty, m.Seed, stopJSON(m.Stop), m.Thinking,
		int(m.DefaultFlags), m.Enabled, m.CreatedAt.UnixMilli(), m.UpdatedAt.UnixMilli(),
	)
	return err
}

func (r *modelRepo) GetByName(ctx context.Context, name string) (*repo.Model, error) {
	var row modelRow
	q := r.db.Rebind(`SELECT ` + modelColumns + ` FROM models WHERE name=?`)
	err := r.db.GetContext(ctx, &row, q, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToModel(row), nil
}

func (r *modelRepo) GetDefault(ctx context.Context, flag repo.DefaultFlag) (*repo.Model, error) {
	var row modelRow
	// ORDER BY id LIMIT 1 兜底：异常数据出现多条持有同一位时取最早一条，避免 Get 报错
	q := r.db.Rebind(`SELECT ` + modelColumns + ` FROM models WHERE (default_flags & ?) <> 0 ORDER BY id LIMIT 1`)
	err := r.db.GetContext(ctx, &row, q, int(flag))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToModel(row), nil
}

func (r *modelRepo) List(ctx context.Context) ([]*repo.Model, error) {
	var rows []modelRow
	if err := r.db.SelectContext(ctx, &rows, `SELECT `+modelColumns+` FROM models ORDER BY name ASC`); err != nil {
		return nil, err
	}
	result := make([]*repo.Model, 0, len(rows))
	for _, row := range rows {
		result = append(result, rowToModel(row))
	}
	return result, nil
}

func (r *modelRepo) Update(ctx context.Context, name string, m *repo.Model) error {
	q := r.db.Rebind(`UPDATE models SET name=?, base_url=?, api_key=?, model=?,
		max_completion_tokens=?, max_context_tokens=?, temperature=?, top_p=?,
		frequency_penalty=?, presence_penalty=?, seed=?, stop=?, thinking=?,
		enabled=?, updated_at=? WHERE name=?`)
	res, err := r.db.ExecContext(ctx, q,
		m.Name, m.BaseURL, m.APIKey, m.Model,
		m.MaxCompletionTokens, m.MaxContextTokens, m.Temperature, m.TopP,
		m.FrequencyPenalty, m.PresencePenalty, m.Seed, stopJSON(m.Stop), m.Thinking,
		m.Enabled, time.Now().UnixMilli(), name,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (r *modelRepo) Delete(ctx context.Context, name string) error {
	q := r.db.Rebind(`DELETE FROM models WHERE name=?`)
	res, err := r.db.ExecContext(ctx, q, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

// SetDefault 清位使用减法而非 default_flags & ~flag：MySQL 的 ~ 结果为无符号 64 位整数，
// 与 SQLite、PG 行为不一致；WHERE 已保证该位为 1，减去位值与清位等价。
// 先清全表（含目标行）再为目标行置位，目标不存在时整个事务回滚。
// 置位用按位或而非加法：PG READ COMMITTED 下并发 SetDefault 时，后一事务的清位语句
// 可能看不到前一事务刚置的位，加法会把同一位加两次溢出到其他位；按位或重复执行结果不变。
func (r *modelRepo) SetDefault(ctx context.Context, name string, flag repo.DefaultFlag) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	f := int(flag)
	if _, err := tx.ExecContext(ctx,
		tx.Rebind(`UPDATE models SET default_flags = default_flags - ? WHERE (default_flags & ?) <> 0`),
		f, f); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		tx.Rebind(`UPDATE models SET default_flags = default_flags | ?, updated_at=? WHERE name=?`),
		f, time.Now().UnixMilli(), name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return tx.Commit()
}

// ClearDefault 清除目标行的 flag 位。先确认模型存在，再按「该位为 1」条件做减法，
// 未持有该位时 UPDATE 不命中任何行，视为成功。
func (r *modelRepo) ClearDefault(ctx context.Context, name string, flag repo.DefaultFlag) error {
	if _, err := r.GetByName(ctx, name); err != nil {
		return err
	}
	f := int(flag)
	q := r.db.Rebind(`UPDATE models SET default_flags = default_flags - ?, updated_at=? WHERE name=? AND (default_flags & ?) <> 0`)
	_, err := r.db.ExecContext(ctx, q, f, time.Now().UnixMilli(), name, f)
	return err
}

func (r *modelRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM models`); err != nil {
		return 0, err
	}
	return n, nil
}
