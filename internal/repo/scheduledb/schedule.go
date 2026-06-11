package scheduledb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/schedule"
)

type scheduleRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) schedule.ScheduleRepo {
	return &scheduleRepo{db: sqlxDB, dialect: dialect}
}

func (r *scheduleRepo) SaveTask(ctx context.Context, task *schedule.Task) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	upsert := r.dialect.UpsertSuffix("task_id",
		"name", "schedule_expr", "status", "payload", "updated_at")
	q := r.db.Rebind(`INSERT INTO schedule_tasks
		   (task_id, name, schedule_expr, status, payload, next_run_at, last_run_at, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NULL, NULL, 0, ?, ?) ` + upsert)
	_, err = r.db.ExecContext(ctx, q,
		task.ID, task.Name, task.Schedule, "active", string(payload), now, now,
	)
	return err
}

func (r *scheduleRepo) loadTaskRow(ctx context.Context, query string, args ...interface{}) (*schedule.Task, error) {
	var row struct {
		Payload string `db:"payload"`
		Version int64  `db:"version"`
		Status  string `db:"status"`
	}
	err := r.db.GetContext(ctx, &row, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, schedule.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var task schedule.Task
	if err := json.Unmarshal([]byte(row.Payload), &task); err != nil {
		return nil, err
	}
	task.Version = row.Version
	task.Status = row.Status
	return &task, nil
}

func (r *scheduleRepo) LoadTask(ctx context.Context, taskID string) (*schedule.Task, error) {
	q := r.db.Rebind(`SELECT payload, version, status FROM schedule_tasks WHERE task_id=?`)
	return r.loadTaskRow(ctx, q, taskID)
}

func (r *scheduleRepo) ListByStatus(ctx context.Context, status schedule.TaskStatus) ([]*schedule.Task, error) {
	var rows []struct {
		Payload string `db:"payload"`
		Version int64  `db:"version"`
		Status  string `db:"status"`
	}
	q := r.db.Rebind(`SELECT payload, version, status FROM schedule_tasks WHERE status=? ORDER BY created_at ASC`)
	if err := r.db.SelectContext(ctx, &rows, q, status); err != nil {
		return nil, err
	}
	tasks := make([]*schedule.Task, 0, len(rows))
	for _, row := range rows {
		var t schedule.Task
		if err := json.Unmarshal([]byte(row.Payload), &t); err != nil {
			return nil, err
		}
		t.Version = row.Version
		t.Status = row.Status
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

func (r *scheduleRepo) DueTasks(ctx context.Context, now time.Time) ([]*schedule.Task, error) {
	var rows []struct {
		Payload string `db:"payload"`
		Version int64  `db:"version"`
		Status  string `db:"status"`
	}
	q := r.db.Rebind(`SELECT payload, version, status FROM schedule_tasks WHERE status='active' AND next_run_at IS NOT NULL AND next_run_at <= ?`)
	if err := r.db.SelectContext(ctx, &rows, q, now.UnixMilli()); err != nil {
		return nil, err
	}
	tasks := make([]*schedule.Task, 0, len(rows))
	for _, row := range rows {
		var t schedule.Task
		if err := json.Unmarshal([]byte(row.Payload), &t); err != nil {
			return nil, err
		}
		t.Version = row.Version
		t.Status = row.Status
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

func (r *scheduleRepo) UpdateNextRun(ctx context.Context, taskID string, nextRunAt, lastRunAt time.Time, version int64) error {
	q := r.db.Rebind(`UPDATE schedule_tasks SET next_run_at=?, last_run_at=?, version=version+1, updated_at=?
		 WHERE task_id=? AND version=?`)
	res, err := r.db.ExecContext(ctx, q,
		nextRunAt.UnixMilli(), lastRunAt.UnixMilli(), time.Now().UnixMilli(), taskID, version,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return schedule.ErrConflict
	}
	return nil
}

func (r *scheduleRepo) MoveStatus(ctx context.Context, taskID string, newStatus schedule.TaskStatus, version int64) error {
	q := r.db.Rebind(`UPDATE schedule_tasks SET status=?, version=version+1, updated_at=? WHERE task_id=? AND version=?`)
	res, err := r.db.ExecContext(ctx, q,
		newStatus, time.Now().UnixMilli(), taskID, version,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return schedule.ErrConflict
	}
	return nil
}

func (r *scheduleRepo) DeleteTask(ctx context.Context, taskID string) error {
	q := r.db.Rebind(`DELETE FROM schedule_tasks WHERE task_id=?`)
	_, err := r.db.ExecContext(ctx, q, taskID)
	return err
}

func (r *scheduleRepo) SaveExecution(ctx context.Context, rec *schedule.ExecutionRecord) error {
	detail, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var finishedAtMs interface{}
	if rec.FinishedAt != nil {
		finishedAtMs = rec.FinishedAt.UnixMilli()
	}
	prefix := r.dialect.InsertIgnorePrefix()
	suffix := r.dialect.InsertIgnoreSuffix()
	q := r.db.Rebind(prefix + ` schedule_executions
		   (execution_id, task_id, started_at, finished_at, status, detail)
		 VALUES (?, ?, ?, ?, ?, ?) ` + suffix)
	_, err = r.db.ExecContext(ctx, q,
		rec.ExecutionID, rec.TaskID, rec.StartedAt.UnixMilli(), finishedAtMs, rec.Status, string(detail),
	)
	return err
}

func (r *scheduleRepo) CompleteExecution(ctx context.Context, rec *schedule.ExecutionRecord, nextRunAt, lastRunAt time.Time, version int64) error {
	detail, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var finishedAtMs interface{}
	if rec.FinishedAt != nil {
		finishedAtMs = rec.FinishedAt.UnixMilli()
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	q1 := r.db.Rebind(`UPDATE schedule_executions SET finished_at=?, status=?, detail=? WHERE execution_id=?`)
	if _, err = tx.ExecContext(ctx, q1,
		finishedAtMs, rec.Status, string(detail), rec.ExecutionID,
	); err != nil {
		return err
	}

	q2 := r.db.Rebind(`UPDATE schedule_tasks SET next_run_at=?, last_run_at=?, version=version+1, updated_at=?
		 WHERE task_id=? AND version=?`)
	res, err := tx.ExecContext(ctx, q2,
		nextRunAt.UnixMilli(), lastRunAt.UnixMilli(), time.Now().UnixMilli(), rec.TaskID, version,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return schedule.ErrConflict
	}
	return tx.Commit()
}

func (r *scheduleRepo) ListExecutions(ctx context.Context, taskID string, limit int) ([]*schedule.ExecutionRecord, error) {
	q := r.db.Rebind(`SELECT detail FROM schedule_executions WHERE task_id=? ORDER BY started_at DESC LIMIT ?`)
	rows, err := r.db.QueryContext(ctx, q, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var recs []*schedule.ExecutionRecord
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			return nil, err
		}
		var rec schedule.ExecutionRecord
		if err := json.Unmarshal([]byte(detail), &rec); err != nil {
			return nil, err
		}
		recs = append(recs, &rec)
	}
	return recs, rows.Err()
}
