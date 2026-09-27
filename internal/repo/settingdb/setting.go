// Package settingdb 是配置表的 sqlx 实现。
// 表内只保存使用者明确修改过的值，查询未命中交由上层用默认值填充。
package settingdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type settingRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.SettingRepo {
	return &settingRepo{db: sqlxDB, dialect: dialect}
}

type settingRow struct {
	Scope     string `db:"scope"`
	ScopeID   string `db:"scope_id"`
	Name      string `db:"name"`
	Value     string `db:"value"`
	UpdatedAt int64  `db:"updated_at"`
}

const settingColumns = `scope, scope_id, name, value, updated_at`

func rowToSetting(r settingRow) *repo.Setting {
	return &repo.Setting{
		Scope:     repo.Scope(r.Scope),
		ScopeID:   r.ScopeID,
		Name:      r.Name,
		Value:     r.Value,
		UpdatedAt: r.UpdatedAt,
	}
}

// validate 校验一行配置是否可写入：name 非空、作用域已登记、
// 以及作用域与实体标识的搭配（global 要求 scope_id 为空串，其余作用域必须带 scope_id）。
// 这些约束放在写入层而非 SQL CHECK：三种方言对 CHECK 的支持与行为不一致。
func validate(s *repo.Setting) error {
	if s.Name == "" {
		return fmt.Errorf("setting: name 不能为空")
	}
	if _, ok := repo.ScopePriority(s.Scope); !ok {
		return fmt.Errorf("setting: 未知作用域 %q", s.Scope)
	}
	if s.Scope == repo.ScopeGlobal && s.ScopeID != "" {
		return fmt.Errorf("setting: global 作用域的 scope_id 必须为空串，得到 %q", s.ScopeID)
	}
	if s.Scope != repo.ScopeGlobal && s.ScopeID == "" {
		return fmt.Errorf("setting: %s 作用域必须带 scope_id", s.Scope)
	}
	return nil
}

func (r *settingRepo) Get(ctx context.Context, scope repo.Scope, scopeID, name string) (*repo.Setting, error) {
	q := r.db.Rebind(`SELECT ` + settingColumns + ` FROM settings WHERE scope=? AND scope_id=? AND name=?`)
	var row settingRow
	if err := r.db.GetContext(ctx, &row, q, string(scope), scopeID, name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("setting get: %w", err)
	}
	return rowToSetting(row), nil
}

func (r *settingRepo) ListByScope(ctx context.Context, scope repo.Scope, scopeID string) ([]*repo.Setting, error) {
	q := r.db.Rebind(`SELECT ` + settingColumns + ` FROM settings WHERE scope=? AND scope_id=? ORDER BY name ASC`)
	var rows []settingRow
	if err := r.db.SelectContext(ctx, &rows, q, string(scope), scopeID); err != nil {
		return nil, fmt.Errorf("setting list: %w", err)
	}
	out := make([]*repo.Setting, 0, len(rows))
	for _, row := range rows {
		out = append(out, rowToSetting(row))
	}
	return out, nil
}

// Upsert 在一个事务内写入多个键，避免设置面板整组保存时出现部分成功。
func (r *settingRepo) Upsert(ctx context.Context, items ...*repo.Setting) error {
	if len(items) == 0 {
		return nil
	}
	for _, s := range items {
		if err := validate(s); err != nil {
			return err
		}
	}

	q := r.db.Rebind(`INSERT INTO settings (` + settingColumns + `) VALUES (?, ?, ?, ?, ?) ` +
		r.dialect.UpsertSuffix("scope, scope_id, name", "value", "updated_at"))

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("setting upsert begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UnixMilli()
	for _, s := range items {
		if _, err := tx.ExecContext(ctx, q,
			string(s.Scope), s.ScopeID, s.Name, s.Value, now); err != nil {
			return fmt.Errorf("setting upsert %s: %w", s.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("setting upsert commit: %w", err)
	}
	return nil
}

// Delete 删除一个键，等价于把该项恢复为代码默认值。
// 键本就不存在时返回 nil：调用方的意图（该键最终不在表内）已经达成。
func (r *settingRepo) Delete(ctx context.Context, scope repo.Scope, scopeID, name string) error {
	q := r.db.Rebind(`DELETE FROM settings WHERE scope=? AND scope_id=? AND name=?`)
	if _, err := r.db.ExecContext(ctx, q, string(scope), scopeID, name); err != nil {
		return fmt.Errorf("setting delete: %w", err)
	}
	return nil
}
