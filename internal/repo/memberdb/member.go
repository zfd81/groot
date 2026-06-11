package memberdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type memberRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.MemberRepo {
	return &memberRepo{db: sqlxDB, dialect: dialect}
}

func (r *memberRepo) Register(ctx context.Context, m *repo.Member) error {
	upsert := r.dialect.UpsertSuffix("reg_id",
		"role", "host", "port", "pid", "heartbeat_at")
	q := r.db.Rebind(`INSERT INTO cluster_members (reg_id, role, host, port, pid, heartbeat_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?) ` + upsert)
	_, err := r.db.ExecContext(ctx, q,
		m.RegID, m.Role, m.Host, m.Port, m.Pid,
		m.HeartbeatAt.UnixMilli(), m.CreatedAt.UnixMilli(),
	)
	return err
}

func (r *memberRepo) Heartbeat(ctx context.Context, regID string) error {
	q := r.db.Rebind(`UPDATE cluster_members SET heartbeat_at=? WHERE reg_id=?`)
	res, err := r.db.ExecContext(ctx, q, time.Now().UnixMilli(), regID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (r *memberRepo) UpdateRole(ctx context.Context, regID, role string) error {
	q := r.db.Rebind(`UPDATE cluster_members SET role=? WHERE reg_id=?`)
	res, err := r.db.ExecContext(ctx, q, role, regID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

type memberRow struct {
	RegID       string `db:"reg_id"`
	Role        string `db:"role"`
	Host        string `db:"host"`
	Port        int    `db:"port"`
	Pid         int    `db:"pid"`
	HeartbeatAt int64  `db:"heartbeat_at"`
	CreatedAt   int64  `db:"created_at"`
}

func rowToMember(row memberRow) *repo.Member {
	return &repo.Member{
		RegID:       row.RegID,
		Role:        row.Role,
		Host:        row.Host,
		Port:        row.Port,
		Pid:         row.Pid,
		HeartbeatAt: time.UnixMilli(row.HeartbeatAt),
		CreatedAt:   time.UnixMilli(row.CreatedAt),
	}
}

func (r *memberRepo) Get(ctx context.Context, regID string) (*repo.Member, error) {
	var row memberRow
	q := r.db.Rebind(`SELECT reg_id, role, host, port, pid, heartbeat_at, created_at FROM cluster_members WHERE reg_id=?`)
	err := r.db.GetContext(ctx, &row, q, regID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToMember(row), nil
}

func (r *memberRepo) ListAll(ctx context.Context) ([]*repo.Member, error) {
	var rows []memberRow
	if err := r.db.SelectContext(ctx, &rows,
		`SELECT reg_id, role, host, port, pid, heartbeat_at, created_at FROM cluster_members`); err != nil {
		return nil, err
	}
	members := make([]*repo.Member, len(rows))
	for i, row := range rows {
		members[i] = rowToMember(row)
	}
	return members, nil
}

func (r *memberRepo) Remove(ctx context.Context, regID string) error {
	q := r.db.Rebind(`DELETE FROM cluster_members WHERE reg_id=?`)
	_, err := r.db.ExecContext(ctx, q, regID)
	return err
}

func (r *memberRepo) RemoveExpired(ctx context.Context, expiredBefore time.Time) (int, error) {
	q := r.db.Rebind(`DELETE FROM cluster_members WHERE heartbeat_at < ?`)
	res, err := r.db.ExecContext(ctx, q, expiredBefore.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
