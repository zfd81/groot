package userdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type userRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.UserRepo {
	return &userRepo{db: sqlxDB, dialect: dialect}
}

type userRow struct {
	ID          string        `db:"id"`
	Username    string        `db:"username"`
	Password    string        `db:"password"`
	CreatedAt   int64         `db:"created_at"`
	UpdatedAt   int64         `db:"updated_at"`
	LastLoginAt sql.NullInt64 `db:"last_login_at"`
}

func rowToUser(row userRow) *repo.User {
	u := &repo.User{
		ID:           row.ID,
		Username:     row.Username,
		PasswordHash: row.Password,
		CreatedAt:    time.UnixMilli(row.CreatedAt),
		UpdatedAt:    time.UnixMilli(row.UpdatedAt),
	}
	if row.LastLoginAt.Valid {
		t := time.UnixMilli(row.LastLoginAt.Int64)
		u.LastLoginAt = &t
	}
	return u
}

func (r *userRepo) Create(ctx context.Context, u *repo.User) error {
	q := r.db.Rebind(`INSERT INTO users (id, username, password, created_at, updated_at, last_login_at)
		 VALUES (?, ?, ?, ?, ?, NULL)`)
	_, err := r.db.ExecContext(ctx, q,
		u.ID, u.Username, u.PasswordHash,
		u.CreatedAt.UnixMilli(), u.UpdatedAt.UnixMilli(),
	)
	return err
}

func (r *userRepo) GetByUsername(ctx context.Context, username string) (*repo.User, error) {
	var row userRow
	q := r.db.Rebind(`SELECT id, username, password, created_at, updated_at, last_login_at FROM users WHERE username=?`)
	err := r.db.GetContext(ctx, &row, q, username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToUser(row), nil
}

func (r *userRepo) GetByID(ctx context.Context, id string) (*repo.User, error) {
	var row userRow
	q := r.db.Rebind(`SELECT id, username, password, created_at, updated_at, last_login_at FROM users WHERE id=?`)
	err := r.db.GetContext(ctx, &row, q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToUser(row), nil
}

func (r *userRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM users`); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *userRepo) UpdatePassword(ctx context.Context, id, passwordHash string) error {
	q := r.db.Rebind(`UPDATE users SET password=?, updated_at=? WHERE id=?`)
	res, err := r.db.ExecContext(ctx, q, passwordHash, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (r *userRepo) UpdateLastLogin(ctx context.Context, id string, at time.Time) error {
	q := r.db.Rebind(`UPDATE users SET last_login_at=? WHERE id=?`)
	res, err := r.db.ExecContext(ctx, q, at.UnixMilli(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (r *userRepo) DeleteAll(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM users`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
