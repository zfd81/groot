// internal/repo/messagedb/message.go
package messagedb

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type messageRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

// New 创建基于 sqlx 的 MessageRepo 实现。
func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.MessageRepo {
	return &messageRepo{db: sqlxDB, dialect: dialect}
}

const messageColumns = `id, message_type, payload, target_instance, target_module,
	priority, created_at, expires_at, source_instance`

type messageRow struct {
	ID             int64  `db:"id"`
	Type           string `db:"message_type"`
	Payload        string `db:"payload"`
	TargetInstance string `db:"target_instance"`
	TargetModule   string `db:"target_module"`
	Priority       int    `db:"priority"`
	CreatedAt      int64  `db:"created_at"`
	ExpiresAt      int64  `db:"expires_at"`
	SourceInstance string `db:"source_instance"`
}

func rowToMessage(row messageRow) *repo.ClusterMessage {
	return &repo.ClusterMessage{
		ID:             row.ID,
		Type:           row.Type,
		Payload:        row.Payload,
		TargetInstance: row.TargetInstance,
		TargetModule:   row.TargetModule,
		Priority:       row.Priority,
		CreatedAt:      time.UnixMilli(row.CreatedAt),
		ExpiresAt:      time.UnixMilli(row.ExpiresAt),
		SourceInstance: row.SourceInstance,
	}
}

type consumerRow struct {
	MessageID    int64  `db:"message_id"`
	InstanceID   string `db:"instance_id"`
	ConsumedAt   int64  `db:"consumed_at"`
	Status       string `db:"status"`
	ErrorMessage string `db:"error_message"`
}

func rowToConsumer(row consumerRow) *repo.MessageConsumer {
	return &repo.MessageConsumer{
		MessageID:    row.MessageID,
		InstanceID:   row.InstanceID,
		ConsumedAt:   time.UnixMilli(row.ConsumedAt),
		Status:       row.Status,
		ErrorMessage: row.ErrorMessage,
	}
}

func (r *messageRepo) Insert(ctx context.Context, m *repo.ClusterMessage) error {
	q := r.db.Rebind(`INSERT INTO cluster_messages
		(message_type, payload, target_instance, target_module, priority, created_at, expires_at, source_instance)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		m.Type, m.Payload, m.TargetInstance, m.TargetModule, m.Priority,
		m.CreatedAt.UnixMilli(), m.ExpiresAt.UnixMilli(), m.SourceInstance,
	)
	return err
}

func (r *messageRepo) ListPending(ctx context.Context, instanceID string, now time.Time, limit int) ([]*repo.ClusterMessage, error) {
	// 三方言对 LIMIT 0/负数行为不一致，非正数直接返回空结果。
	if limit <= 0 {
		return nil, nil
	}
	q := r.db.Rebind(`SELECT ` + messageColumns + ` FROM cluster_messages m
		WHERE (m.target_instance = '' OR m.target_instance = ?)
		  AND m.expires_at > ?
		  AND NOT EXISTS (
		      SELECT 1 FROM cluster_message_consumers c
		      WHERE c.message_id = m.id AND c.instance_id = ?
		  )
		ORDER BY m.priority ASC, m.created_at ASC, m.id ASC
		LIMIT ?`)
	var rows []messageRow
	if err := r.db.SelectContext(ctx, &rows, q, instanceID, now.UnixMilli(), instanceID, limit); err != nil {
		return nil, err
	}
	msgs := make([]*repo.ClusterMessage, len(rows))
	for i, row := range rows {
		msgs[i] = rowToMessage(row)
	}
	return msgs, nil
}

func (r *messageRepo) RecordConsumption(ctx context.Context, c *repo.MessageConsumer) error {
	q := r.db.Rebind(`INSERT INTO cluster_message_consumers
		(message_id, instance_id, consumed_at, status, error_message)
		VALUES (?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		c.MessageID, c.InstanceID, c.ConsumedAt.UnixMilli(), c.Status, c.ErrorMessage)
	return err
}

func (r *messageRepo) ListConsumers(ctx context.Context, messageID int64) ([]*repo.MessageConsumer, error) {
	q := r.db.Rebind(`SELECT message_id, instance_id, consumed_at, status, error_message
		FROM cluster_message_consumers WHERE message_id = ? ORDER BY instance_id ASC`)
	var rows []consumerRow
	if err := r.db.SelectContext(ctx, &rows, q, messageID); err != nil {
		return nil, err
	}
	out := make([]*repo.MessageConsumer, len(rows))
	for i, row := range rows {
		out[i] = rowToConsumer(row)
	}
	return out, nil
}

func (r *messageRepo) DeleteBefore(ctx context.Context, before time.Time) (int, int, error) {
	q := r.db.Rebind(`DELETE FROM cluster_messages WHERE created_at < ?`)
	res, err := r.db.ExecContext(ctx, q, before.UnixMilli())
	if err != nil {
		return 0, 0, err
	}
	dm, _ := res.RowsAffected()

	res, err = r.db.ExecContext(ctx,
		`DELETE FROM cluster_message_consumers
		 WHERE message_id NOT IN (SELECT id FROM cluster_messages)`)
	if err != nil {
		return int(dm), 0, err
	}
	dc, _ := res.RowsAffected()
	return int(dm), int(dc), nil
}
