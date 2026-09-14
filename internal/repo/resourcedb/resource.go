package resourcedb

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	// status 必须在更新列内：重新推送一个已删除路径时要把状态复位为 active，
	// 否则会写入新内容却仍标记为已删除。
	upsert := r.dialect.UpsertSuffix("path",
		"content", "content_type", "size", "content_hash", "updated_at", "status")
	q := r.db.Rebind(`INSERT INTO shared_resources (path, content, content_type, size, content_hash, updated_at, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?) ` + upsert)
	_, err := r.db.ExecContext(ctx, q,
		res.Path, res.Content, res.ContentType, res.Size, res.ContentHash,
		res.UpdatedAt.UnixMilli(), repo.ResourceStatusActive,
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
		Status      string `db:"status"`
	}
	q := r.db.Rebind(`SELECT path, content, content_type, size, content_hash, updated_at, status
		FROM shared_resources WHERE path=? AND status='active'`)
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
		Status: row.Status,
	}, nil
}

func (r *resourceRepo) Stat(ctx context.Context, path string) (*repo.ResourceEntry, error) {
	var row struct {
		Path        string `db:"path"`
		Size        int64  `db:"size"`
		ContentHash string `db:"content_hash"`
		UpdatedAt   int64  `db:"updated_at"`
		Status      string `db:"status"`
	}
	q := r.db.Rebind(`SELECT path, size, content_hash, updated_at, status
		FROM shared_resources WHERE path=? AND status='active'`)
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
		Status: row.Status,
	}, nil
}

func (r *resourceRepo) List(ctx context.Context, prefix string) ([]*repo.ResourceEntry, error) {
	return r.list(ctx, prefix, true)
}

// ListWithDeleted 返回含已删除记录的列表，供同步差异比较区分
// 「远端曾有此文件但已删除」与「远端从无记录」。
func (r *resourceRepo) ListWithDeleted(ctx context.Context, prefix string) ([]*repo.ResourceEntry, error) {
	return r.list(ctx, prefix, false)
}

func (r *resourceRepo) list(ctx context.Context, prefix string, activeOnly bool) ([]*repo.ResourceEntry, error) {
	var rows []struct {
		Path        string `db:"path"`
		Size        int64  `db:"size"`
		ContentHash string `db:"content_hash"`
		UpdatedAt   int64  `db:"updated_at"`
		Status      string `db:"status"`
	}
	const base = `SELECT path, size, content_hash, updated_at, status FROM shared_resources`
	var conds []string
	var args []interface{}
	if prefix != "" {
		conds = append(conds, "path LIKE ?")
		args = append(args, prefix+"%")
	}
	if activeOnly {
		conds = append(conds, "status='active'")
	}
	q := base
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY path ASC"
	if err := r.db.SelectContext(ctx, &rows, r.db.Rebind(q), args...); err != nil {
		return nil, err
	}
	entries := make([]*repo.ResourceEntry, len(rows))
	for i, row := range rows {
		entries[i] = &repo.ResourceEntry{
			Path: row.Path, Size: row.Size, ContentHash: row.ContentHash,
			UpdatedAt: time.UnixMilli(row.UpdatedAt), Status: row.Status,
		}
	}
	return entries, nil
}

// Delete 将记录标记为已删除并清空内容，保留行以便同步时区分
// 「他人删除了这个文件」与「远端从无此文件」。
// 路径不存在时 UPDATE 影响 0 行，不报错（与物理删除的行为一致）。
// 已删除的路径同样影响 0 行：updated_at 是墓碑除「存在」之外唯一承载的信息，
// 同步比较要靠它判断本地文件是否该删，所以重复删除必须保住首次删除的时刻，
// 不能刷成当次时间把墓碑显得比实际更新。
func (r *resourceRepo) Delete(ctx context.Context, path string) error {
	// content 绑参数而 size/content_hash 用字面量，是因为空 BLOB 的字面量各方言
	// 写法不同（SQLite X''、MySQL ''、Postgres '\x'::bytea），绑参数是唯一可移植的写法。
	q := r.db.Rebind(`UPDATE shared_resources
		SET status='deleted', content=?, size=0, content_hash='', updated_at=?
		WHERE path=? AND status='active'`)
	_, err := r.db.ExecContext(ctx, q, []byte{}, time.Now().UnixMilli(), path)
	return err
}
