package resourcedb

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type resourceRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.ResourceRepo {
	return &resourceRepo{db: sqlxDB, dialect: dialect}
}

// SHA1Hex computes SHA-1 hex of content. Exported for use by sync module.
func SHA1Hex(content []byte) string {
	h := sha1.Sum(content)
	return fmt.Sprintf("%x", h)
}

func (r *resourceRepo) Put(ctx context.Context, res *repo.Resource) error {
	upsert := r.dialect.UpsertSuffix("path",
		"content", "content_type", "size", "content_hash", "updated_at")
	q := r.db.Rebind(`INSERT INTO shared_resources (path, content, content_type, size, content_hash, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?) ` + upsert)
	_, err := r.db.ExecContext(ctx, q,
		res.Path, res.Content, res.ContentType, res.Size, res.ContentHash, res.UpdatedAt.UnixMilli(),
	)
	return err
}

func (r *resourceRepo) Get(ctx context.Context, path string) (*repo.Resource, error) {
	var row struct {
		Path        string `db:"path"`
		Content     []byte `db:"content"`
		ContentType string `db:"content_type"`
		Size        int64  `db:"size"`
		ContentHash string `db:"content_hash"`
		UpdatedAt   int64  `db:"updated_at"`
	}
	q := r.db.Rebind(`SELECT path, content, content_type, size, content_hash, updated_at FROM shared_resources WHERE path=?`)
	err := r.db.GetContext(ctx, &row, q, path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &repo.Resource{
		Path: row.Path, Content: row.Content, ContentType: row.ContentType,
		Size: row.Size, ContentHash: row.ContentHash, UpdatedAt: time.UnixMilli(row.UpdatedAt),
	}, nil
}

func (r *resourceRepo) Stat(ctx context.Context, path string) (*repo.ResourceEntry, error) {
	var row struct {
		Path        string `db:"path"`
		Size        int64  `db:"size"`
		ContentHash string `db:"content_hash"`
		UpdatedAt   int64  `db:"updated_at"`
	}
	q := r.db.Rebind(`SELECT path, size, content_hash, updated_at FROM shared_resources WHERE path=?`)
	err := r.db.GetContext(ctx, &row, q, path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &repo.ResourceEntry{
		Path: row.Path, Size: row.Size,
		ContentHash: row.ContentHash, UpdatedAt: time.UnixMilli(row.UpdatedAt),
	}, nil
}

func (r *resourceRepo) List(ctx context.Context, prefix string) ([]*repo.ResourceEntry, error) {
	var rows []struct {
		Path        string `db:"path"`
		Size        int64  `db:"size"`
		ContentHash string `db:"content_hash"`
		UpdatedAt   int64  `db:"updated_at"`
	}
	var err error
	if prefix == "" {
		err = r.db.SelectContext(ctx, &rows,
			`SELECT path, size, content_hash, updated_at FROM shared_resources ORDER BY path ASC`)
	} else {
		q := r.db.Rebind(`SELECT path, size, content_hash, updated_at FROM shared_resources WHERE path LIKE ? ORDER BY path ASC`)
		err = r.db.SelectContext(ctx, &rows, q, prefix+"%")
	}
	if err != nil {
		return nil, err
	}
	entries := make([]*repo.ResourceEntry, len(rows))
	for i, row := range rows {
		entries[i] = &repo.ResourceEntry{
			Path: row.Path, Size: row.Size,
			ContentHash: row.ContentHash, UpdatedAt: time.UnixMilli(row.UpdatedAt),
		}
	}
	return entries, nil
}

func (r *resourceRepo) Delete(ctx context.Context, path string) error {
	q := r.db.Rebind(`DELETE FROM shared_resources WHERE path=?`)
	_, err := r.db.ExecContext(ctx, q, path)
	return err
}
