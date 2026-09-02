package apikeydb

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

type apiKeyRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.APIKeyRepo {
	return &apiKeyRepo{db: sqlxDB, dialect: dialect}
}

type apiKeyRow struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Permissions string `db:"permissions"`
	ExpiresAt   int64  `db:"expires_at"`
	CreatedAt   int64  `db:"created_at"`
}

const apiKeyColumns = `id, name, permissions, expires_at, created_at`

func rowToAPIKey(row apiKeyRow) *repo.APIKey {
	var perms []string
	// 序列化损坏时按空数组处理（等同无任何权限），不拖垮整个列表
	if err := json.Unmarshal([]byte(row.Permissions), &perms); err != nil || perms == nil {
		perms = []string{}
	}
	return &repo.APIKey{
		ID:          row.ID,
		Name:        row.Name,
		Permissions: perms,
		ExpiresAt:   time.UnixMilli(row.ExpiresAt),
		CreatedAt:   time.UnixMilli(row.CreatedAt),
	}
}

func permsJSON(perms []string) string {
	if perms == nil {
		perms = []string{}
	}
	b, err := json.Marshal(perms)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (r *apiKeyRepo) Create(ctx context.Context, k *repo.APIKey) error {
	q := r.db.Rebind(`INSERT INTO api_keys (id, name, permissions, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		k.ID, k.Name, permsJSON(k.Permissions), k.ExpiresAt.UnixMilli(), k.CreatedAt.UnixMilli())
	return err
}

func (r *apiKeyRepo) GetByID(ctx context.Context, id string) (*repo.APIKey, error) {
	var row apiKeyRow
	q := r.db.Rebind(`SELECT ` + apiKeyColumns + ` FROM api_keys WHERE id=?`)
	err := r.db.GetContext(ctx, &row, q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToAPIKey(row), nil
}

func (r *apiKeyRepo) GetByName(ctx context.Context, name string) (*repo.APIKey, error) {
	var row apiKeyRow
	q := r.db.Rebind(`SELECT ` + apiKeyColumns + ` FROM api_keys WHERE name=?`)
	err := r.db.GetContext(ctx, &row, q, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToAPIKey(row), nil
}

func (r *apiKeyRepo) List(ctx context.Context) ([]*repo.APIKey, error) {
	var rows []apiKeyRow
	q := `SELECT ` + apiKeyColumns + ` FROM api_keys ORDER BY created_at DESC`
	if err := r.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, err
	}
	result := make([]*repo.APIKey, 0, len(rows))
	for _, row := range rows {
		result = append(result, rowToAPIKey(row))
	}
	return result, nil
}

func (r *apiKeyRepo) DeleteByID(ctx context.Context, id string) error {
	q := r.db.Rebind(`DELETE FROM api_keys WHERE id=?`)
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}
