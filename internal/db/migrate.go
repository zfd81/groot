package db

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Migrate creates all tables if they don't exist, then runs cleanup steps for
// historical schema (e.g. dropping deprecated indices). Idempotent.
func Migrate(db *sqlx.DB, dialect Dialect) error {
	stmts := ddlStatements(dialect)
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("db migrate: %w", err)
		}
	}
	if err := dropLegacyIndices(db, dialect); err != nil {
		return fmt.Errorf("db migrate cleanup: %w", err)
	}
	return nil
}

// dropLegacyIndices removes indices that earlier versions created but the
// current schema no longer wants. Each step probes the catalog first so it
// works on every supported dialect/version (no reliance on `DROP INDEX
// IF EXISTS` syntax variants).
func dropLegacyIndices(db *sqlx.DB, dialect Dialect) error {
	// uk_session_round on memory_chats: 早期版本以 UNIQUE (session_id, round)
	// 强约束建表；新方案下子 Agent 沿用父 round 会与主 Agent 同 round 冲突，
	// 所以这个唯一约束必须降级为非唯一索引（已在 DDL 中以 idx_mc_session_round 补回）。
	return dropIndexIfExists(db, dialect, "memory_chats", "uk_session_round")
}

// dropIndexIfExists drops an index by name only if the catalog reports it.
// Returns nil whether or not the index existed; surfaces only real DROP errors.
func dropIndexIfExists(db *sqlx.DB, dialect Dialect, table, indexName string) error {
	exists, err := indexExists(db, dialect, table, indexName)
	if err != nil {
		return fmt.Errorf("probe index %s: %w", indexName, err)
	}
	if !exists {
		return nil
	}
	var stmt string
	switch dialect {
	case DialectMySQL:
		// MySQL 5.7 / 8.0.0–8.0.28 不识别 IF EXISTS；探测过后直接 DROP 即可。
		stmt = fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", table, indexName)
	case DialectPostgres:
		stmt = fmt.Sprintf("DROP INDEX %s", indexName)
	default: // SQLite
		stmt = fmt.Sprintf("DROP INDEX %s", indexName)
	}
	if _, err := db.Exec(stmt); err != nil {
		return fmt.Errorf("drop index %s on %s: %w", indexName, table, err)
	}
	return nil
}

// indexExists queries the dialect-specific catalog for an index by name.
func indexExists(db *sqlx.DB, dialect Dialect, table, indexName string) (bool, error) {
	var q string
	var args []interface{}
	switch dialect {
	case DialectMySQL:
		q = `SELECT COUNT(*) FROM information_schema.statistics
		     WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`
		args = []interface{}{table, indexName}
	case DialectPostgres:
		q = `SELECT COUNT(*) FROM pg_indexes
		     WHERE schemaname = current_schema() AND tablename = $1 AND indexname = $2`
		args = []interface{}{table, indexName}
	default: // SQLite
		q = `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`
		args = []interface{}{indexName}
	}
	var n int
	if err := db.Get(&n, q, args...); err != nil {
		return false, err
	}
	return n > 0, nil
}

func ddlStatements(d Dialect) []string {
	switch d {
	case DialectMySQL:
		return mysqlDDL()
	case DialectPostgres:
		return postgresDDL()
	default:
		return sqliteDDL()
	}
}

func sqliteDDL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS cluster_members (
			reg_id       TEXT NOT NULL PRIMARY KEY,
			role         TEXT NOT NULL,
			host         TEXT NOT NULL,
			port         INTEGER NOT NULL,
			pid          INTEGER NOT NULL,
			heartbeat_at INTEGER NOT NULL,
			created_at   INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_tasks (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id       TEXT NOT NULL,
			name          TEXT NOT NULL,
			schedule_expr TEXT NOT NULL,
			status        TEXT NOT NULL,
			payload       TEXT NOT NULL,
			next_run_at   INTEGER,
			last_run_at   INTEGER,
			version       INTEGER NOT NULL DEFAULT 0,
			created_at    INTEGER NOT NULL,
			updated_at    INTEGER NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_task_id ON schedule_tasks(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_status_next_run ON schedule_tasks(status, next_run_at)`,
		`CREATE INDEX IF NOT EXISTS idx_st_updated_at ON schedule_tasks(updated_at)`,
		`CREATE TABLE IF NOT EXISTS schedule_executions (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			execution_id TEXT NOT NULL,
			task_id      TEXT NOT NULL,
			started_at   INTEGER NOT NULL,
			finished_at  INTEGER,
			status       TEXT NOT NULL,
			detail       TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_execution_id ON schedule_executions(execution_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_started ON schedule_executions(task_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_se_started_at ON schedule_executions(started_at)`,
		`CREATE TABLE IF NOT EXISTS memory_sessions (
			session_id TEXT NOT NULL PRIMARY KEY,
			user_id    TEXT NOT NULL DEFAULT '',
			prompt     TEXT NOT NULL DEFAULT '',
			round      INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ms_user_id ON memory_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ms_updated_at ON memory_sessions(updated_at)`,
		`CREATE TABLE IF NOT EXISTS memory_chats (
			chat_id           TEXT NOT NULL PRIMARY KEY,
			session_id        TEXT NOT NULL,
			round             INTEGER NOT NULL,
			agent_name        TEXT NOT NULL DEFAULT '',
			caller            TEXT NOT NULL DEFAULT '',
			prompt            TEXT NOT NULL DEFAULT '',
			instruction       TEXT NOT NULL,
			result            TEXT NOT NULL DEFAULT '',
			steps             TEXT NOT NULL DEFAULT '',
			status            TEXT NOT NULL,
			error             TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens      INTEGER NOT NULL DEFAULT 0,
			duration_ms       INTEGER NOT NULL DEFAULT 0,
			started_at        INTEGER NOT NULL,
			finished_at       INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mc_session_round ON memory_chats(session_id, round)`,
		`CREATE INDEX IF NOT EXISTS idx_mc_session_started ON memory_chats(session_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mc_started_at ON memory_chats(started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mc_status ON memory_chats(status)`,
		`CREATE TABLE IF NOT EXISTS shared_resources (
			path         TEXT NOT NULL PRIMARY KEY,
			content      BLOB NOT NULL,
			content_type TEXT NOT NULL DEFAULT '',
			size         INTEGER NOT NULL,
			content_hash TEXT NOT NULL DEFAULT '',
			updated_at   INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sr_updated_at ON shared_resources(updated_at)`,
	}
}

func mysqlDDL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS cluster_members (
			reg_id       VARCHAR(32)  NOT NULL PRIMARY KEY,
			role         VARCHAR(16)  NOT NULL,
			host         VARCHAR(64)  NOT NULL,
			port         INT          NOT NULL,
			pid          INT          NOT NULL,
			heartbeat_at BIGINT       NOT NULL,
			created_at   BIGINT       NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS schedule_tasks (
			id            BIGINT       PRIMARY KEY AUTO_INCREMENT,
			task_id       VARCHAR(64)  NOT NULL,
			name          VARCHAR(255) NOT NULL,
			schedule_expr VARCHAR(64)  NOT NULL,
			status        VARCHAR(16)  NOT NULL,
			payload       LONGTEXT     NOT NULL,
			next_run_at   BIGINT,
			last_run_at   BIGINT,
			version       BIGINT       NOT NULL DEFAULT 0,
			created_at    BIGINT       NOT NULL,
			updated_at    BIGINT       NOT NULL,
			UNIQUE KEY uk_task_id (task_id),
			KEY idx_status_next_run (status, next_run_at),
			KEY idx_updated_at (updated_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS schedule_executions (
			id           BIGINT      PRIMARY KEY AUTO_INCREMENT,
			execution_id VARCHAR(64) NOT NULL,
			task_id      VARCHAR(64) NOT NULL,
			started_at   BIGINT      NOT NULL,
			finished_at  BIGINT,
			status       VARCHAR(16) NOT NULL,
			detail       LONGTEXT    NOT NULL,
			UNIQUE KEY uk_execution_id (execution_id),
			KEY idx_task_started (task_id, started_at DESC),
			KEY idx_started_at (started_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS memory_sessions (
			session_id VARCHAR(64)  NOT NULL PRIMARY KEY,
			user_id    VARCHAR(64)  NOT NULL DEFAULT '',
			prompt     LONGTEXT     NOT NULL,
			round      INT          NOT NULL DEFAULT 0,
			created_at BIGINT       NOT NULL,
			updated_at BIGINT       NOT NULL,
			KEY idx_user_id (user_id),
			KEY idx_updated_at (updated_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS memory_chats (
			chat_id           VARCHAR(64)  NOT NULL PRIMARY KEY,
			session_id        VARCHAR(64)  NOT NULL,
			round             INT          NOT NULL,
			agent_name        VARCHAR(64)  NOT NULL DEFAULT '',
			caller            VARCHAR(64)  NOT NULL DEFAULT '',
			prompt            LONGTEXT     NOT NULL,
			instruction       LONGTEXT     NOT NULL,
			result            LONGTEXT     NOT NULL,
			steps             LONGTEXT     NOT NULL,
			status            VARCHAR(16)  NOT NULL,
			error             TEXT         NOT NULL,
			model             VARCHAR(64)  NOT NULL DEFAULT '',
			prompt_tokens     INT          NOT NULL DEFAULT 0,
			completion_tokens INT          NOT NULL DEFAULT 0,
			total_tokens      INT          NOT NULL DEFAULT 0,
			duration_ms       BIGINT       NOT NULL DEFAULT 0,
			started_at        BIGINT       NOT NULL,
			finished_at       BIGINT,
			KEY idx_session_round (session_id, round),
			KEY idx_session_started (session_id, started_at DESC),
			KEY idx_started_at (started_at),
			KEY idx_status (status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS shared_resources (
			path         VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
			content      LONGBLOB     NOT NULL,
			content_type VARCHAR(64)  NOT NULL DEFAULT '',
			size         BIGINT       NOT NULL,
			content_hash CHAR(40)     NOT NULL DEFAULT '',
			updated_at   BIGINT       NOT NULL,
			KEY idx_updated_at (updated_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
}

func postgresDDL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS cluster_members (
			reg_id       VARCHAR(32)  NOT NULL PRIMARY KEY,
			role         VARCHAR(16)  NOT NULL,
			host         VARCHAR(64)  NOT NULL,
			port         INTEGER      NOT NULL,
			pid          INTEGER      NOT NULL,
			heartbeat_at BIGINT       NOT NULL,
			created_at   BIGINT       NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_tasks (
			id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			task_id       VARCHAR(64)  NOT NULL,
			name          VARCHAR(255) NOT NULL,
			schedule_expr VARCHAR(64)  NOT NULL,
			status        VARCHAR(16)  NOT NULL,
			payload       TEXT         NOT NULL,
			next_run_at   BIGINT,
			last_run_at   BIGINT,
			version       BIGINT       NOT NULL DEFAULT 0,
			created_at    BIGINT       NOT NULL,
			updated_at    BIGINT       NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_task_id ON schedule_tasks(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_status_next_run ON schedule_tasks(status, next_run_at)`,
		`CREATE INDEX IF NOT EXISTS idx_st_updated_at ON schedule_tasks(updated_at)`,
		`CREATE TABLE IF NOT EXISTS schedule_executions (
			id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			execution_id VARCHAR(64) NOT NULL,
			task_id      VARCHAR(64) NOT NULL,
			started_at   BIGINT      NOT NULL,
			finished_at  BIGINT,
			status       VARCHAR(16) NOT NULL,
			detail       TEXT        NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_execution_id ON schedule_executions(execution_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_started ON schedule_executions(task_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_se_started_at ON schedule_executions(started_at)`,
		`CREATE TABLE IF NOT EXISTS memory_sessions (
			session_id VARCHAR(64)  NOT NULL PRIMARY KEY,
			user_id    VARCHAR(64)  NOT NULL DEFAULT '',
			prompt     TEXT         NOT NULL DEFAULT '',
			round      INTEGER      NOT NULL DEFAULT 0,
			created_at BIGINT       NOT NULL,
			updated_at BIGINT       NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ms_user_id ON memory_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ms_updated_at ON memory_sessions(updated_at)`,
		`CREATE TABLE IF NOT EXISTS memory_chats (
			chat_id           VARCHAR(64)  NOT NULL PRIMARY KEY,
			session_id        VARCHAR(64)  NOT NULL,
			round             INTEGER      NOT NULL,
			agent_name        VARCHAR(64)  NOT NULL DEFAULT '',
			caller            VARCHAR(64)  NOT NULL DEFAULT '',
			prompt            TEXT         NOT NULL DEFAULT '',
			instruction       TEXT         NOT NULL,
			result            TEXT         NOT NULL DEFAULT '',
			steps             TEXT         NOT NULL DEFAULT '',
			status            VARCHAR(16)  NOT NULL,
			error             TEXT         NOT NULL DEFAULT '',
			model             VARCHAR(64)  NOT NULL DEFAULT '',
			prompt_tokens     INTEGER      NOT NULL DEFAULT 0,
			completion_tokens INTEGER      NOT NULL DEFAULT 0,
			total_tokens      INTEGER      NOT NULL DEFAULT 0,
			duration_ms       BIGINT       NOT NULL DEFAULT 0,
			started_at        BIGINT       NOT NULL,
			finished_at       BIGINT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mc_session_round ON memory_chats(session_id, round)`,
		`CREATE INDEX IF NOT EXISTS idx_mc_session_started ON memory_chats(session_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mc_started_at ON memory_chats(started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mc_status ON memory_chats(status)`,
		`CREATE TABLE IF NOT EXISTS shared_resources (
			path         VARCHAR(512) NOT NULL PRIMARY KEY,
			content      BYTEA        NOT NULL,
			content_type VARCHAR(64)  NOT NULL DEFAULT '',
			size         BIGINT       NOT NULL,
			content_hash CHAR(40)     NOT NULL DEFAULT '',
			updated_at   BIGINT       NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sr_updated_at ON shared_resources(updated_at)`,
	}
}
