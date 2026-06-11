# Database Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the local-file/MinIO storage backend with SQLite (single-machine) and MySQL/PostgreSQL (multi-host cluster) using Repository interfaces per business domain.

**Architecture:** Introduce `internal/db/` for connection management and dialect adaptation (sqlite/mysql/postgres), `internal/repo/` for four domain Repository interfaces (MemberRepo, ScheduleRepo, MemoryRepo, ResourceRepo) with a single `db` implementation shared by all three drivers and a `resourcelocal` file-system implementation for SQLite mode. Retire `internal/storage/` entirely. Wire everything together in `cmd/groot/main.go`.

**Tech Stack:** `github.com/jmoiron/sqlx`, `github.com/mattn/go-sqlite3`, `github.com/go-sql-driver/mysql`, `github.com/lib/pq`, `crypto/sha1` (stdlib)

---

## File Map

### New files
- `internal/db/db.go` — DB connection factory, dialect enum, ping+migrate on startup
- `internal/db/migrate.go` — schema migration (idempotent CREATE TABLE IF NOT EXISTS, per-dialect DDL)
- `internal/db/dialect.go` — dialect helpers (placeholder style `?` vs `$1`, UPSERT syntax, type names)
- `internal/repo/member.go` — `Member` struct, `MemberRepo` interface, `ErrNotFound`, `ErrConflict`
- `internal/repo/schedule.go` — `TaskStatus` consts, `ScheduleRepo` interface
- `internal/repo/memory.go` — `Session` struct, `MemoryRepo` interface
- `internal/repo/resource.go` — `Resource`, `ResourceEntry` structs, `ResourceRepo` interface
- `internal/repo/errors.go` — `ErrNotFound`, `ErrConflict` sentinel errors
- `internal/repo/memberdb/member.go` — `MemberRepo` DB implementation
- `internal/repo/scheduledb/schedule.go` — `ScheduleRepo` DB implementation
- `internal/repo/memorydb/memory.go` — `MemoryRepo` DB implementation
- `internal/repo/resourcedb/resource.go` — `ResourceRepo` DB implementation (MySQL/PG)
- `internal/repo/resourcelocal/resource.go` — `ResourceRepo` local-fs implementation (SQLite mode)
- `internal/repo/factory.go` — `NewRepos(db *sqlx.DB, driver, homeDir string)` factory

### Modified files
- `internal/config/env.go` — replace `MinioConfig` with `DatabaseConfig`; update `loadEnvFile`
- `internal/config/config.go` — remove `StorageConfig` / `MinioConfig`; update `Config` struct
- `internal/schedule/types.go` — add `ExecutionID`, rename `ExecTime→StartedAt`, add `FinishedAt`
- `internal/memory/types.go` — add `Prompt string`, add `DurationMs int`, deprecate `Duration`
- `internal/memory/idgen.go` — update `GenerateChatID()` to drop `chat_` prefix
- `internal/cluster/cluster.go` — replace `storage.Storage` with `repo.MemberRepo`
- `internal/cluster/member.go` — delete (logic moves to `memberdb`)
- `internal/cluster/election.go` — keep `DetermineRole`, `MemberInfo`, but use `repo.Member`
- `internal/schedule/storage.go` — replace with thin adapter calling `ScheduleRepo`
- `internal/memory/manager.go` — replace `storage.Storage` with `MemoryRepo`
- `internal/memory/memory.go` — update to use `MemoryRepo` methods
- `internal/sync/sync.go` — replace `storage.Storage` with `ResourceRepo`; update `ErrSyncDisabled` msg
- `internal/sync/diff.go` — replace mtime-based diff with SHA-1; use `ResourceRepo`
- `internal/cmd/push.go` — pass `ResourceRepo` instead of `storage.Storage`
- `internal/cmd/pull.go` — pass `ResourceRepo` instead of `storage.Storage`; remove `os.Chtimes`
- `internal/cmd/diff_cmd.go` — pass `ResourceRepo` instead of `storage.Storage`
- `cmd/groot/main.go` — read `env.yaml` database config, open DB, construct repos, wire modules
- `go.mod` / `go.sum` — add sqlx, go-sqlite3, go-sql-driver/mysql, lib/pq

### Deleted files
- `internal/storage/storage.go`
- `internal/storage/local.go`
- `internal/storage/local_test.go`
- `internal/storage/minio.go`
- `internal/storage/minio_test.go`
- `internal/storage/factory.go`
- `internal/storage/factory_test.go`
- `internal/cluster/member.go` (replaced by memberdb)
- `internal/cluster/member_test.go`

---

## Task 1: Add DB dependencies to go.mod

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add dependencies**

```bash
cd /Users/zhangfengda/workspace/groot
go get github.com/jmoiron/sqlx@v1.3.5
go get github.com/mattn/go-sqlite3@v1.14.22
go get github.com/go-sql-driver/mysql@v1.8.1
go get github.com/lib/pq@v1.10.9
```

- [ ] **Step 2: Verify go.mod updated**

```bash
grep -E "sqlx|go-sqlite3|go-sql-driver|lib/pq" go.mod
```
Expected: four lines present.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add sqlx, sqlite3, mysql, pq dependencies"
```

---

## Task 2: Define repo sentinel errors and interfaces

**Files:**
- Create: `internal/repo/errors.go`
- Create: `internal/repo/member.go`
- Create: `internal/repo/schedule.go`
- Create: `internal/repo/memory.go`
- Create: `internal/repo/resource.go`

- [ ] **Step 1: Create errors.go**

```go
// internal/repo/errors.go
package repo

import "errors"

var ErrNotFound = errors.New("repo: not found")
var ErrConflict = errors.New("repo: version conflict")
```

- [ ] **Step 2: Create member.go**

```go
// internal/repo/member.go
package repo

import (
	"context"
	"time"
)

type Member struct {
	RegID       string
	Role        string
	Host        string
	Port        int
	Pid         int
	HeartbeatAt time.Time
	CreatedAt   time.Time
}

type MemberRepo interface {
	Register(ctx context.Context, m *Member) error
	Heartbeat(ctx context.Context, regID string) error
	UpdateRole(ctx context.Context, regID, role string) error
	Get(ctx context.Context, regID string) (*Member, error)
	ListAll(ctx context.Context) ([]*Member, error)
	Remove(ctx context.Context, regID string) error
	RemoveExpired(ctx context.Context, expiredBefore time.Time) (int, error)
}
```

- [ ] **Step 3: Create schedule.go**

```go
// internal/repo/schedule.go
package repo

import (
	"context"
	"time"

	"github.com/zfd81/groot/internal/schedule"
)

type TaskStatus = string

const (
	TaskStatusActive   TaskStatus = "active"
	TaskStatusDisabled TaskStatus = "disabled"
	TaskStatusArchive  TaskStatus = "archive"
)

type ScheduleRepo interface {
	SaveTask(ctx context.Context, task *schedule.Task) error
	LoadTask(ctx context.Context, taskID string) (*schedule.Task, error)
	ListByStatus(ctx context.Context, status TaskStatus) ([]*schedule.Task, error)
	DueTasks(ctx context.Context, now time.Time) ([]*schedule.Task, error)
	UpdateNextRun(ctx context.Context, taskID string, nextRunAt, lastRunAt time.Time, version int64) error
	MoveStatus(ctx context.Context, taskID string, newStatus TaskStatus, version int64) error
	DeleteTask(ctx context.Context, taskID string) error
	SaveExecution(ctx context.Context, rec *schedule.ExecutionRecord) error
	CompleteExecution(ctx context.Context, rec *schedule.ExecutionRecord, nextRunAt, lastRunAt time.Time, version int64) error
	ListExecutions(ctx context.Context, taskID string, limit int) ([]*schedule.ExecutionRecord, error)
}
```

- [ ] **Step 4: Create memory.go**

```go
// internal/repo/memory.go
package repo

import (
	"context"
	"time"

	"github.com/zfd81/groot/internal/memory"
)

type Session struct {
	SessionID string
	UserID    string
	Prompt    string
	Round     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type MemoryRepo interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	ExistsSession(ctx context.Context, sessionID string) (bool, error)
	ListSessions(ctx context.Context) ([]*Session, error)
	SaveChat(ctx context.Context, rec *memory.ChatRecord) error
	GetChat(ctx context.Context, chatID string) (*memory.ChatRecord, error)
	LoadHistory(ctx context.Context, sessionID string) ([]*memory.ChatRecord, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteExpiredSessions(ctx context.Context, expiredBefore time.Time) (int, error)
}
```

- [ ] **Step 5: Create resource.go**

```go
// internal/repo/resource.go
package repo

import (
	"context"
	"time"
)

type Resource struct {
	Path        string
	Content     []byte
	ContentType string
	Size        int64
	ContentHash string
	UpdatedAt   time.Time
}

type ResourceEntry struct {
	Path        string
	Size        int64
	ContentHash string
	UpdatedAt   time.Time
}

type ResourceRepo interface {
	Put(ctx context.Context, r *Resource) error
	Get(ctx context.Context, path string) (*Resource, error)
	Stat(ctx context.Context, path string) (*ResourceEntry, error)
	List(ctx context.Context, prefix string) ([]*ResourceEntry, error)
	Delete(ctx context.Context, path string) error
}
```

- [ ] **Step 6: Verify compilation**

```bash
go build ./internal/repo/...
```
Expected: no errors (interfaces only, no implementations yet).

- [ ] **Step 7: Commit**

```bash
git add internal/repo/
git commit -m "feat: define repo interfaces and sentinel errors"
```

---

## Task 3: Update data structs (types.go, idgen.go)

**Files:**
- Modify: `internal/schedule/types.go`
- Modify: `internal/memory/types.go`
- Modify: `internal/memory/idgen.go`

- [ ] **Step 1: Update ExecutionRecord in schedule/types.go**

Replace the `ExecutionRecord` struct (keep everything else in the file unchanged):

```go
// ExecutionRecord records a single task execution
type ExecutionRecord struct {
	ExecutionID string             `json:"execution_id"`
	TaskID      string             `json:"task_id"`
	StartedAt   time.Time          `json:"started_at"`   // renamed from ExecTime
	FinishedAt  *time.Time         `json:"finished_at"`  // nil while running
	TriggerType string             `json:"trigger_type"`
	SessionID   string             `json:"session_id"`
	ChatID      string             `json:"chat_id"`
	Status      string             `json:"status"`
	DurationMs  int64              `json:"duration_ms"`
	StepCount   int                `json:"step_count"`
	Error       string             `json:"error"`
	Notifications []NotificationResult `json:"notifications"`
}
```

- [ ] **Step 2: Update ChatRecord in memory/types.go**

Add `Prompt` and `DurationMs` fields, deprecate `Duration`:

```go
type ChatRecord struct {
	ChatID      string    `json:"chat_id"`
	SessionID   string    `json:"session_id"`
	Round       int       `json:"round"`
	Timestamp   time.Time `json:"timestamp"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	Prompt      string    `json:"prompt"`       // system prompt for this chat
	Instruction string    `json:"instruction"`
	Result      string    `json:"result"`
	Status      string    `json:"status"`
	// Deprecated: use DurationMs. Kept for API backward compat (Duration = DurationMs/1000).
	Duration    int       `json:"duration"`
	DurationMs  int64     `json:"duration_ms"`
	Caller      string    `json:"caller"`
	Steps       []Step    `json:"steps"`
	AgentName        string `json:"agent_name,omitempty"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
	Error            *Error `json:"error"`
}
```

- [ ] **Step 3: Update GenerateChatID in memory/idgen.go**

```go
// GenerateChatID 生成对话ID
// 格式: {YYYYMMDDHHMMSSmmm}
func GenerateChatID() string {
	now := time.Now()
	ts := now.Format("20060102150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	return ts
}
```

- [ ] **Step 4: Fix callers of ExecTime and GenerateChatID**

```bash
grep -rn "ExecTime\|\.ExecTime\|chat_" internal/schedule/ internal/memory/ internal/cmd/ cmd/ --include="*.go" | grep -v "_test.go"
```

Update each callsite: `ExecTime` → `StartedAt`, remove `chat_` prefix expectation.

- [ ] **Step 5: Build to catch compile errors**

```bash
go build ./...
```

- [ ] **Step 6: Run existing tests**

```bash
go test ./internal/schedule/... ./internal/memory/... -v 2>&1 | tail -20
```

- [ ] **Step 7: Commit**

```bash
git add internal/schedule/types.go internal/memory/types.go internal/memory/idgen.go
git commit -m "feat: update ExecutionRecord and ChatRecord structs for DB backend"
```

---

## Task 4: Update config — replace MinioConfig with DatabaseConfig

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/env.go`

- [ ] **Step 1: Replace StorageConfig / MinioConfig in config.go**

Remove the `StorageConfig` and `MinioConfig` types. Remove `Storage StorageConfig` from `Config`. Add:

```go
// DatabaseConfig 数据库连接配置（来自 env.yaml）
type DatabaseConfig struct {
	Driver          string `yaml:"driver"`           // "sqlite" | "mysql" | "postgres"
	DSN             string `yaml:"dsn"`              // 连接字符串，支持 ${ENV_VAR}
	MaxOpenConns    int    `yaml:"max_open_conns"`   // 默认 20
	MaxIdleConns    int    `yaml:"max_idle_conns"`   // 默认 5
	ConnMaxLifetime string `yaml:"conn_max_lifetime"` // 默认 "30m"
}
```

- [ ] **Step 2: Update env.go**

Replace the `minio` field in `envFile` with `database`:

```go
type envFile struct {
	Database *DatabaseConfig `yaml:"database"`
}

func loadEnvFile(cfg *Config, homeDir string) error {
	cfg.Database = nil

	envPath := filepath.Join(homeDir, EnvFileName)
	data, err := os.ReadFile(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read env file: %w", err)
	}

	var ef envFile
	if err := yaml.Unmarshal(data, &ef); err != nil {
		return fmt.Errorf("failed to parse env file: %w", err)
	}

	cfg.Database = ef.Database
	return nil
}
```

- [ ] **Step 3: Add `Database *DatabaseConfig` to Config struct**

```go
type Config struct {
	Agent      AgentConfig      `yaml:"agent"`
	Server     ServerConfig     `yaml:"server"`
	LLM        LLMConfig        `yaml:"llm"`
	Memory     MemoryConfig     `yaml:"memory"`
	React      ReactConfig      `yaml:"react"`
	Attachment AttachmentConfig `yaml:"attachment"`
	Schedule   ScheduleConfig   `yaml:"schedule"`
	Message    MessageConfig    `yaml:"message"`
	SubAgent   SubAgentConfig   `yaml:"subagent"`
	Security   SecurityConfig   `yaml:"security"`
	Logging    LoggingConfig    `yaml:"logging"`
	Database   *DatabaseConfig  `yaml:"-"` // loaded from env.yaml, not config.yaml
}
```

- [ ] **Step 4: Fix compile errors from removed StorageConfig**

```bash
go build ./... 2>&1 | grep "StorageConfig\|MinioConfig\|cfg\.Storage"
```

Fix each reference (will be handled in later tasks for main.go and sync).

- [ ] **Step 5: Run config tests**

```bash
go test ./internal/config/... -v
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/
git commit -m "feat: replace MinioConfig with DatabaseConfig in env.yaml"
```

---

## Task 5: Implement internal/db — connection, dialect, migration

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/dialect.go`
- Create: `internal/db/migrate.go`
- Create: `internal/db/db_test.go`

- [ ] **Step 1: Create dialect.go**

```go
// internal/db/dialect.go
package db

type Dialect int

const (
	DialectSQLite   Dialect = iota
	DialectMySQL
	DialectPostgres
)

func DialectFrom(driver string) Dialect {
	switch driver {
	case "mysql":
		return DialectMySQL
	case "postgres":
		return DialectPostgres
	default:
		return DialectSQLite
	}
}

// Placeholder returns the positional placeholder for a given index (1-based).
// SQLite and MySQL use ?, Postgres uses $1, $2, ...
func (d Dialect) Placeholder(n int) string {
	if d == DialectPostgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// Placeholders returns n consecutive placeholders joined by commas.
func (d Dialect) Placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = d.Placeholder(i + 1)
	}
	return strings.Join(parts, ", ")
}
```

Add missing imports: `"fmt"`, `"strings"`.

- [ ] **Step 2: Create migrate.go**

```go
// internal/db/migrate.go
package db

import "github.com/jmoiron/sqlx"

// Migrate creates all tables if they don't exist. Idempotent.
func Migrate(db *sqlx.DB, dialect Dialect) error {
	stmts := ddlStatements(dialect)
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("db migrate: %w", err)
		}
	}
	return nil
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
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_session_round ON memory_chats(session_id, round)`,
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
			prompt     LONGTEXT     NOT NULL DEFAULT '',
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
			prompt            LONGTEXT     NOT NULL DEFAULT '',
			instruction       LONGTEXT     NOT NULL,
			result            LONGTEXT     NOT NULL DEFAULT '',
			steps             LONGTEXT     NOT NULL DEFAULT '',
			status            VARCHAR(16)  NOT NULL,
			error             TEXT         NOT NULL DEFAULT '',
			model             VARCHAR(64)  NOT NULL DEFAULT '',
			prompt_tokens     INT          NOT NULL DEFAULT 0,
			completion_tokens INT          NOT NULL DEFAULT 0,
			total_tokens      INT          NOT NULL DEFAULT 0,
			duration_ms       BIGINT       NOT NULL DEFAULT 0,
			started_at        BIGINT       NOT NULL,
			finished_at       BIGINT,
			UNIQUE KEY uk_session_round (session_id, round),
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
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_session_round ON memory_chats(session_id, round)`,
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
```

Add `"fmt"` to dialect.go imports.

- [ ] **Step 3: Create db.go**

```go
// internal/db/db.go
package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	"github.com/zfd81/groot/internal/config"
)

// Open opens a database connection, runs migrations, and returns the db.
// If cfg is nil, opens SQLite at homeDir/groot.db.
func Open(cfg *config.DatabaseConfig, homeDir string) (*sqlx.DB, Dialect, error) {
	driver, dsn, dialect := resolveDriver(cfg, homeDir)

	db, err := sqlx.Open(driver, dsn)
	if err != nil {
		return nil, dialect, fmt.Errorf("db open: %w", err)
	}

	maxOpen := 20
	maxIdle := 5
	maxLife := 30 * time.Minute

	if cfg != nil {
		if cfg.MaxOpenConns > 0 {
			maxOpen = cfg.MaxOpenConns
		}
		if cfg.MaxIdleConns > 0 {
			maxIdle = cfg.MaxIdleConns
		}
		if cfg.ConnMaxLifetime != "" {
			if d, err := time.ParseDuration(cfg.ConnMaxLifetime); err == nil {
				maxLife = d
			}
		}
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLife)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, dialect, fmt.Errorf("db ping: %w", err)
	}

	if err := Migrate(db, dialect); err != nil {
		db.Close()
		return nil, dialect, fmt.Errorf("db migrate: %w", err)
	}

	return db, dialect, nil
}

func resolveDriver(cfg *config.DatabaseConfig, homeDir string) (driver, dsn string, dialect Dialect) {
	if cfg == nil {
		dbPath := filepath.Join(homeDir, "groot.db")
		return "sqlite3", dbPath + "?_journal_mode=WAL&_busy_timeout=5000", DialectSQLite
	}
	d := cfg.Driver
	dsn = expandEnvVars(cfg.DSN)
	dialect = DialectFrom(d)
	switch dialect {
	case DialectMySQL:
		driver = "mysql"
	case DialectPostgres:
		driver = "postgres"
	default:
		driver = "sqlite3"
	}
	return driver, dsn, dialect
}

func expandEnvVars(s string) string {
	return os.ExpandEnv(s)
}
```

- [ ] **Step 4: Write test for db.go using SQLite in-memory**

```go
// internal/db/db_test.go
package db

import (
	"testing"
)

func TestOpen_SQLite(t *testing.T) {
	db, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if dialect != DialectSQLite {
		t.Errorf("expected SQLite dialect, got %v", dialect)
	}
	// verify tables exist
	tables := []string{
		"cluster_members", "schedule_tasks", "schedule_executions",
		"memory_sessions", "memory_chats", "shared_resources",
	}
	for _, tbl := range tables {
		var n int
		err := db.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n)
		if err != nil {
			t.Errorf("table %s not accessible: %v", tbl, err)
		}
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	db, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// run migrate again — must not error
	if err := Migrate(db, dialect); err != nil {
		t.Errorf("second Migrate: %v", err)
	}
	db.Close()
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/db/... -v
```
Expected: `TestOpen_SQLite PASS`, `TestMigrate_Idempotent PASS`.

- [ ] **Step 6: Commit**

```bash
git add internal/db/
git commit -m "feat: add internal/db with connection factory, dialect, migrations"
```

---

## Task 6: Implement memberdb — MemberRepo DB implementation

**Files:**
- Create: `internal/repo/memberdb/member.go`
- Create: `internal/repo/memberdb/member_test.go`

- [ ] **Step 1: Implement memberdb/member.go**

```go
// internal/repo/memberdb/member.go
package memberdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/repo"
)

type memberRepo struct {
	db *sqlx.DB
}

func New(db *sqlx.DB) repo.MemberRepo {
	return &memberRepo{db: db}
}

func (r *memberRepo) Register(ctx context.Context, m *repo.Member) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO cluster_members (reg_id, role, host, port, pid, heartbeat_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(reg_id) DO UPDATE SET
		   role=excluded.role, host=excluded.host, port=excluded.port,
		   pid=excluded.pid, heartbeat_at=excluded.heartbeat_at`,
		m.RegID, m.Role, m.Host, m.Port, m.Pid,
		m.HeartbeatAt.UnixMilli(), m.CreatedAt.UnixMilli(),
	)
	return err
}

func (r *memberRepo) Heartbeat(ctx context.Context, regID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE cluster_members SET heartbeat_at=? WHERE reg_id=?`,
		time.Now().UnixMilli(), regID,
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

func (r *memberRepo) UpdateRole(ctx context.Context, regID, role string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE cluster_members SET role=? WHERE reg_id=?`, role, regID,
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

func (r *memberRepo) Get(ctx context.Context, regID string) (*repo.Member, error) {
	var row struct {
		RegID       string `db:"reg_id"`
		Role        string `db:"role"`
		Host        string `db:"host"`
		Port        int    `db:"port"`
		Pid         int    `db:"pid"`
		HeartbeatAt int64  `db:"heartbeat_at"`
		CreatedAt   int64  `db:"created_at"`
	}
	err := r.db.GetContext(ctx, &row,
		`SELECT reg_id, role, host, port, pid, heartbeat_at, created_at FROM cluster_members WHERE reg_id=?`,
		regID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &repo.Member{
		RegID:       row.RegID,
		Role:        row.Role,
		Host:        row.Host,
		Port:        row.Port,
		Pid:         row.Pid,
		HeartbeatAt: time.UnixMilli(row.HeartbeatAt),
		CreatedAt:   time.UnixMilli(row.CreatedAt),
	}, nil
}

func (r *memberRepo) ListAll(ctx context.Context) ([]*repo.Member, error) {
	var rows []struct {
		RegID       string `db:"reg_id"`
		Role        string `db:"role"`
		Host        string `db:"host"`
		Port        int    `db:"port"`
		Pid         int    `db:"pid"`
		HeartbeatAt int64  `db:"heartbeat_at"`
		CreatedAt   int64  `db:"created_at"`
	}
	if err := r.db.SelectContext(ctx, &rows,
		`SELECT reg_id, role, host, port, pid, heartbeat_at, created_at FROM cluster_members`); err != nil {
		return nil, err
	}
	members := make([]*repo.Member, len(rows))
	for i, row := range rows {
		members[i] = &repo.Member{
			RegID: row.RegID, Role: row.Role, Host: row.Host,
			Port: row.Port, Pid: row.Pid,
			HeartbeatAt: time.UnixMilli(row.HeartbeatAt),
			CreatedAt:   time.UnixMilli(row.CreatedAt),
		}
	}
	return members, nil
}

func (r *memberRepo) Remove(ctx context.Context, regID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cluster_members WHERE reg_id=?`, regID)
	return err
}

func (r *memberRepo) RemoveExpired(ctx context.Context, expiredBefore time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM cluster_members WHERE heartbeat_at < ?`, expiredBefore.UnixMilli(),
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
```

**Note:** The `ON CONFLICT` syntax above is SQLite/PG style. For MySQL use `INSERT ... ON DUPLICATE KEY UPDATE`. Extract a `upsertMember` helper that switches on dialect, or use a helper from `internal/db`. For the initial implementation use SQLite syntax and add a TODO for MySQL dialect.

- [ ] **Step 2: Write member_test.go**

```go
// internal/repo/memberdb/member_test.go
package memberdb

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestDB(t *testing.T) *memberRepo {
	t.Helper()
	sqlxDB, _, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB).(*memberRepo)
}

func TestRegisterAndGet(t *testing.T) {
	r := newTestDB(t)
	ctx := context.Background()
	m := &repo.Member{
		RegID: "20260610143022123", Role: "follower",
		Host: "127.0.0.1", Port: 8080, Pid: 1234,
		HeartbeatAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := r.Register(ctx, m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Get(ctx, m.RegID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Role != "follower" {
		t.Errorf("expected follower, got %s", got.Role)
	}
}

func TestHeartbeat_NotFound(t *testing.T) {
	r := newTestDB(t)
	err := r.Heartbeat(context.Background(), "nonexistent")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRemoveExpired(t *testing.T) {
	r := newTestDB(t)
	ctx := context.Background()
	old := &repo.Member{
		RegID: "20260101000000000", Role: "follower",
		Host: "127.0.0.1", Port: 8080, Pid: 1,
		HeartbeatAt: time.Now().Add(-1 * time.Hour), CreatedAt: time.Now(),
	}
	r.Register(ctx, old)
	n, err := r.RemoveExpired(ctx, time.Now().Add(-30*time.Second))
	if err != nil {
		t.Fatalf("RemoveExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 removed, got %d", n)
	}
}
```

Add `"errors"` import.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/repo/memberdb/... -v
```
Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/repo/memberdb/
git commit -m "feat: implement MemberRepo DB backend"
```

---

## Task 7: Implement scheduledb — ScheduleRepo DB implementation

**Files:**
- Create: `internal/repo/scheduledb/schedule.go`
- Create: `internal/repo/scheduledb/schedule_test.go`

- [ ] **Step 1: Implement scheduledb/schedule.go**

```go
// internal/repo/scheduledb/schedule.go
package scheduledb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/schedule"
)

type scheduleRepo struct {
	db *sqlx.DB
}

func New(db *sqlx.DB) repo.ScheduleRepo {
	return &scheduleRepo{db: db}
}

func (r *scheduleRepo) SaveTask(ctx context.Context, task *schedule.Task) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	var nextRunAt, lastRunAt interface{}
	// next_run_at and last_run_at stored in payload; columns default to NULL
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO schedule_tasks
		   (task_id, name, schedule_expr, status, payload, next_run_at, last_run_at, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		 ON CONFLICT(task_id) DO UPDATE SET
		   name=excluded.name, schedule_expr=excluded.schedule_expr,
		   status=excluded.status, payload=excluded.payload,
		   next_run_at=excluded.next_run_at, last_run_at=excluded.last_run_at,
		   version=version+1, updated_at=excluded.updated_at`,
		task.ID, task.Name, task.Schedule, "active", string(payload),
		nextRunAt, lastRunAt, now, now,
	)
	return err
}

func (r *scheduleRepo) LoadTask(ctx context.Context, taskID string) (*schedule.Task, error) {
	var payload string
	err := r.db.QueryRowContext(ctx,
		`SELECT payload FROM schedule_tasks WHERE task_id=?`, taskID,
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var task schedule.Task
	if err := json.Unmarshal([]byte(payload), &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *scheduleRepo) ListByStatus(ctx context.Context, status repo.TaskStatus) ([]*schedule.Task, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT payload FROM schedule_tasks WHERE status=? ORDER BY created_at ASC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (r *scheduleRepo) DueTasks(ctx context.Context, now time.Time) ([]*schedule.Task, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT payload FROM schedule_tasks WHERE status='active' AND next_run_at <= ?`,
		now.UnixMilli(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func scanTasks(rows *sql.Rows) ([]*schedule.Task, error) {
	var tasks []*schedule.Task
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var t schedule.Task
		if err := json.Unmarshal([]byte(payload), &t); err != nil {
			return nil, err
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

func (r *scheduleRepo) UpdateNextRun(ctx context.Context, taskID string, nextRunAt, lastRunAt time.Time, version int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE schedule_tasks SET next_run_at=?, last_run_at=?, version=version+1, updated_at=?
		 WHERE task_id=? AND version=?`,
		nextRunAt.UnixMilli(), lastRunAt.UnixMilli(), time.Now().UnixMilli(), taskID, version,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrConflict
	}
	return nil
}

func (r *scheduleRepo) MoveStatus(ctx context.Context, taskID string, newStatus repo.TaskStatus, version int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE schedule_tasks SET status=?, version=version+1, updated_at=? WHERE task_id=? AND version=?`,
		newStatus, time.Now().UnixMilli(), taskID, version,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrConflict
	}
	return nil
}

func (r *scheduleRepo) DeleteTask(ctx context.Context, taskID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM schedule_tasks WHERE task_id=?`, taskID)
	return err
}

func (r *scheduleRepo) SaveExecution(ctx context.Context, rec *schedule.ExecutionRecord) error {
	detail, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var finishedAt interface{}
	if rec.FinishedAt != nil {
		finishedAt = rec.FinishedAt.UnixMilli()
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schedule_executions
		   (execution_id, task_id, started_at, finished_at, status, detail)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		rec.ExecutionID, rec.TaskID, rec.StartedAt.UnixMilli(), finishedAt, rec.Status, string(detail),
	)
	return err
}

func (r *scheduleRepo) CompleteExecution(ctx context.Context, rec *schedule.ExecutionRecord, nextRunAt, lastRunAt time.Time, version int64) error {
	detail, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var finishedAt interface{}
	if rec.FinishedAt != nil {
		finishedAt = rec.FinishedAt.UnixMilli()
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`UPDATE schedule_executions SET finished_at=?, status=?, detail=? WHERE execution_id=?`,
		finishedAt, rec.Status, string(detail), rec.ExecutionID,
	)
	if err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE schedule_tasks SET next_run_at=?, last_run_at=?, version=version+1, updated_at=?
		 WHERE task_id=? AND version=?`,
		nextRunAt.UnixMilli(), lastRunAt.UnixMilli(), time.Now().UnixMilli(), rec.TaskID, version,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrConflict
	}
	return tx.Commit()
}

func (r *scheduleRepo) ListExecutions(ctx context.Context, taskID string, limit int) ([]*schedule.ExecutionRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT detail FROM schedule_executions WHERE task_id=? ORDER BY started_at DESC LIMIT ?`,
		taskID, limit,
	)
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
```

- [ ] **Step 2: Write schedule_test.go**

```go
// internal/repo/scheduledb/schedule_test.go
package scheduledb

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/schedule"
)

func newRepo(t *testing.T) repo.ScheduleRepo {
	t.Helper()
	sqlxDB, _, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB)
}

func TestSaveAndLoadTask(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	task := &schedule.Task{ID: "task-001", Name: "test", Schedule: "0 * * * *"}
	if err := r.SaveTask(ctx, task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	got, err := r.LoadTask(ctx, "task-001")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if got.Name != "test" {
		t.Errorf("expected name=test, got %s", got.Name)
	}
}

func TestMoveStatus_Conflict(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.SaveTask(ctx, &schedule.Task{ID: "t1", Name: "x", Schedule: "0 * * * *"})
	// version=0 after SaveTask
	err := r.MoveStatus(ctx, "t1", repo.TaskStatusDisabled, 99) // wrong version
	if !errors.Is(err, repo.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestCompleteExecution(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.SaveTask(ctx, &schedule.Task{ID: "t2", Name: "y", Schedule: "* * * * *"})
	now := time.Now()
	fin := now.Add(time.Second)
	rec := &schedule.ExecutionRecord{
		ExecutionID: "exec-001", TaskID: "t2",
		StartedAt: now, FinishedAt: &fin, Status: "success",
	}
	r.SaveExecution(ctx, rec)
	err := r.CompleteExecution(ctx, rec, now.Add(time.Minute), now, 0)
	if err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
}
```

Add `"errors"` import.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/repo/scheduledb/... -v
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/repo/scheduledb/
git commit -m "feat: implement ScheduleRepo DB backend"
```

---

## Task 8: Implement memorydb — MemoryRepo DB implementation

**Files:**
- Create: `internal/repo/memorydb/memory.go`
- Create: `internal/repo/memorydb/memory_test.go`

- [ ] **Step 1: Implement memorydb/memory.go**

```go
// internal/repo/memorydb/memory.go
package memorydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
)

type memoryRepo struct {
	db *sqlx.DB
}

func New(db *sqlx.DB) repo.MemoryRepo {
	return &memoryRepo{db: db}
}

func (r *memoryRepo) CreateSession(ctx context.Context, s *repo.Session) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO memory_sessions (session_id, user_id, prompt, round, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, ?)`,
		s.SessionID, s.UserID, s.Prompt, s.CreatedAt.UnixMilli(), s.UpdatedAt.UnixMilli(),
	)
	if err != nil {
		// duplicate primary key → ErrConflict
		return repo.ErrConflict
	}
	return nil
}

func (r *memoryRepo) GetSession(ctx context.Context, sessionID string) (*repo.Session, error) {
	var row struct {
		SessionID string `db:"session_id"`
		UserID    string `db:"user_id"`
		Prompt    string `db:"prompt"`
		Round     int    `db:"round"`
		CreatedAt int64  `db:"created_at"`
		UpdatedAt int64  `db:"updated_at"`
	}
	err := r.db.GetContext(ctx, &row,
		`SELECT session_id, user_id, prompt, round, created_at, updated_at FROM memory_sessions WHERE session_id=?`,
		sessionID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &repo.Session{
		SessionID: row.SessionID, UserID: row.UserID, Prompt: row.Prompt, Round: row.Round,
		CreatedAt: time.UnixMilli(row.CreatedAt), UpdatedAt: time.UnixMilli(row.UpdatedAt),
	}, nil
}

func (r *memoryRepo) ExistsSession(ctx context.Context, sessionID string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_sessions WHERE session_id=?`, sessionID).Scan(&n)
	return n > 0, err
}

func (r *memoryRepo) ListSessions(ctx context.Context) ([]*repo.Session, error) {
	var rows []struct {
		SessionID string `db:"session_id"`
		UserID    string `db:"user_id"`
		Round     int    `db:"round"`
		CreatedAt int64  `db:"created_at"`
		UpdatedAt int64  `db:"updated_at"`
	}
	if err := r.db.SelectContext(ctx, &rows,
		`SELECT session_id, user_id, round, created_at, updated_at FROM memory_sessions ORDER BY updated_at DESC`); err != nil {
		return nil, err
	}
	sessions := make([]*repo.Session, len(rows))
	for i, row := range rows {
		sessions[i] = &repo.Session{
			SessionID: row.SessionID, UserID: row.UserID, Round: row.Round,
			CreatedAt: time.UnixMilli(row.CreatedAt), UpdatedAt: time.UnixMilli(row.UpdatedAt),
		}
	}
	return sessions, nil
}

func (r *memoryRepo) SaveChat(ctx context.Context, rec *memory.ChatRecord) error {
	stepsJSON, _ := json.Marshal(rec.Steps)
	var errJSON string
	if rec.Error != nil {
		b, _ := json.Marshal(rec.Error)
		errJSON = string(b)
	}
	var finishedAt interface{}
	if !rec.EndedAt.IsZero() {
		finishedAt = rec.EndedAt.UnixMilli()
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Get current round
	var curRound int
	err = tx.QueryRowContext(ctx,
		`SELECT round FROM memory_sessions WHERE session_id=?`, rec.SessionID).Scan(&curRound)
	if errors.Is(err, sql.ErrNoRows) {
		return repo.ErrNotFound
	}
	if err != nil {
		return err
	}
	nextRound := curRound + 1

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memory_chats
		   (chat_id, session_id, round, agent_name, caller, prompt, instruction, result, steps,
		    status, error, model, prompt_tokens, completion_tokens, total_tokens,
		    duration_ms, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ChatID, rec.SessionID, nextRound, rec.AgentName, rec.Caller, rec.Prompt,
		rec.Instruction, rec.Result, string(stepsJSON),
		rec.Status, errJSON, "", rec.PromptTokens, rec.CompletionTokens, rec.TotalTokens,
		rec.DurationMs, rec.StartedAt.UnixMilli(), finishedAt,
	)
	if err != nil {
		return repo.ErrConflict
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE memory_sessions SET round=?, updated_at=? WHERE session_id=? AND round=?`,
		nextRound, time.Now().UnixMilli(), rec.SessionID, curRound,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrConflict
	}
	return tx.Commit()
}

func (r *memoryRepo) GetChat(ctx context.Context, chatID string) (*memory.ChatRecord, error) {
	var row struct {
		ChatID    string `db:"chat_id"`
		SessionID string `db:"session_id"`
		Round     int    `db:"round"`
		AgentName string `db:"agent_name"`
		Caller    string `db:"caller"`
		Prompt    string `db:"prompt"`
		Instruction string `db:"instruction"`
		Result    string `db:"result"`
		Steps     string `db:"steps"`
		Status    string `db:"status"`
		Error     string `db:"error"`
		PromptTokens     int   `db:"prompt_tokens"`
		CompletionTokens int   `db:"completion_tokens"`
		TotalTokens      int   `db:"total_tokens"`
		DurationMs       int64 `db:"duration_ms"`
		StartedAt        int64 `db:"started_at"`
		FinishedAt       *int64 `db:"finished_at"`
	}
	err := r.db.GetContext(ctx, &row,
		`SELECT chat_id, session_id, round, agent_name, caller, prompt, instruction, result, steps,
		        status, error, prompt_tokens, completion_tokens, total_tokens,
		        duration_ms, started_at, finished_at
		 FROM memory_chats WHERE chat_id=?`, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToChat(row), nil
}

func (r *memoryRepo) LoadHistory(ctx context.Context, sessionID string) ([]*memory.ChatRecord, error) {
	var rows []struct {
		ChatID    string `db:"chat_id"`
		SessionID string `db:"session_id"`
		Round     int    `db:"round"`
		AgentName string `db:"agent_name"`
		Caller    string `db:"caller"`
		Prompt    string `db:"prompt"`
		Instruction string `db:"instruction"`
		Result    string `db:"result"`
		Steps     string `db:"steps"`
		Status    string `db:"status"`
		Error     string `db:"error"`
		PromptTokens     int    `db:"prompt_tokens"`
		CompletionTokens int    `db:"completion_tokens"`
		TotalTokens      int    `db:"total_tokens"`
		DurationMs       int64  `db:"duration_ms"`
		StartedAt        int64  `db:"started_at"`
		FinishedAt       *int64 `db:"finished_at"`
	}
	if err := r.db.SelectContext(ctx, &rows,
		`SELECT chat_id, session_id, round, agent_name, caller, prompt, instruction, result, steps,
		        status, error, prompt_tokens, completion_tokens, total_tokens,
		        duration_ms, started_at, finished_at
		 FROM memory_chats
		 WHERE session_id=? AND status='success' AND agent_name=''
		 ORDER BY round ASC`, sessionID); err != nil {
		return nil, err
	}
	chats := make([]*memory.ChatRecord, len(rows))
	for i, row := range rows {
		chats[i] = rowToChat(row)
	}
	return chats, nil
}

func rowToChat(row interface{}) *memory.ChatRecord {
	// Use type switch to handle the same struct shape from GetChat and LoadHistory
	type rowType struct {
		ChatID    string `db:"chat_id"`
		SessionID string `db:"session_id"`
		Round     int    `db:"round"`
		AgentName string `db:"agent_name"`
		Caller    string `db:"caller"`
		Prompt    string `db:"prompt"`
		Instruction string `db:"instruction"`
		Result    string `db:"result"`
		Steps     string `db:"steps"`
		Status    string `db:"status"`
		Error     string `db:"error"`
		PromptTokens     int    `db:"prompt_tokens"`
		CompletionTokens int    `db:"completion_tokens"`
		TotalTokens      int    `db:"total_tokens"`
		DurationMs       int64  `db:"duration_ms"`
		StartedAt        int64  `db:"started_at"`
		FinishedAt       *int64 `db:"finished_at"`
	}
	r := row.(rowType)
	rec := &memory.ChatRecord{
		ChatID: r.ChatID, SessionID: r.SessionID, Round: r.Round,
		AgentName: r.AgentName, Caller: r.Caller, Prompt: r.Prompt,
		Instruction: r.Instruction, Result: r.Result,
		Status: r.Status,
		PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens,
		TotalTokens: r.TotalTokens, DurationMs: r.DurationMs,
		Duration: int(r.DurationMs / 1000),
		StartedAt: time.UnixMilli(r.StartedAt),
	}
	if r.FinishedAt != nil {
		rec.EndedAt = time.UnixMilli(*r.FinishedAt)
	}
	var steps []memory.Step
	json.Unmarshal([]byte(r.Steps), &steps)
	rec.Steps = steps
	if r.Error != "" {
		var e memory.Error
		json.Unmarshal([]byte(r.Error), &e)
		rec.Error = &e
	}
	return rec
}

func (r *memoryRepo) DeleteSession(ctx context.Context, sessionID string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_chats WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_sessions WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *memoryRepo) DeleteExpiredSessions(ctx context.Context, expiredBefore time.Time) (int, error) {
	var sessionIDs []string
	if err := r.db.SelectContext(ctx, &sessionIDs,
		`SELECT session_id FROM memory_sessions WHERE updated_at < ?`, expiredBefore.UnixMilli()); err != nil {
		return 0, err
	}
	count := 0
	for _, sid := range sessionIDs {
		if err := r.DeleteSession(ctx, sid); err == nil {
			count++
		}
	}
	return count, nil
}
```

- [ ] **Step 2: Write memory_test.go**

```go
// internal/repo/memorydb/memory_test.go
package memorydb

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
)

func newMemRepo(t *testing.T) repo.MemoryRepo {
	t.Helper()
	sqlxDB, _, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB)
}

func TestCreateAndGetSession(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	s := &repo.Session{SessionID: "sess-001", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := r.CreateSession(ctx, s); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := r.GetSession(ctx, "sess-001")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Round != 0 {
		t.Errorf("expected round=0, got %d", got.Round)
	}
}

func TestSaveChatIncreasesRound(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s1", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	rec := &memory.ChatRecord{
		ChatID: "20260610143022123", SessionID: "s1",
		Instruction: "hello", Status: "success",
		StartedAt: time.Now(),
	}
	if err := r.SaveChat(ctx, rec); err != nil {
		t.Fatalf("SaveChat: %v", err)
	}
	sess, _ := r.GetSession(ctx, "s1")
	if sess.Round != 1 {
		t.Errorf("expected round=1 after SaveChat, got %d", sess.Round)
	}
}

func TestLoadHistory_ExcludesSubAgents(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s2", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	// main agent chat
	r.SaveChat(ctx, &memory.ChatRecord{
		ChatID: "20260610000000001", SessionID: "s2",
		Instruction: "main", Status: "success", StartedAt: time.Now(),
	})
	history, err := r.LoadHistory(ctx, "s2")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(history))
	}
}

func TestDeleteSession(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s3", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	r.SaveChat(ctx, &memory.ChatRecord{
		ChatID: "20260610000000002", SessionID: "s3",
		Instruction: "x", Status: "success", StartedAt: time.Now(),
	})
	if err := r.DeleteSession(ctx, "s3"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	exists, _ := r.ExistsSession(ctx, "s3")
	if exists {
		t.Error("session should be deleted")
	}
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/repo/memorydb/... -v
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/repo/memorydb/
git commit -m "feat: implement MemoryRepo DB backend"
```

---

## Task 9: Implement resourcedb and resourcelocal

**Files:**
- Create: `internal/repo/resourcedb/resource.go`
- Create: `internal/repo/resourcelocal/resource.go`
- Create: `internal/repo/resourcedb/resource_test.go`
- Create: `internal/repo/resourcelocal/resource_test.go`

- [ ] **Step 1: Implement resourcedb/resource.go**

```go
// internal/repo/resourcedb/resource.go
package resourcedb

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/repo"
)

type resourceRepo struct {
	db *sqlx.DB
}

func New(db *sqlx.DB) repo.ResourceRepo {
	return &resourceRepo{db: db}
}

func (r *resourceRepo) Put(ctx context.Context, res *repo.Resource) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO shared_resources (path, content, content_type, size, content_hash, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   content=excluded.content, content_type=excluded.content_type,
		   size=excluded.size, content_hash=excluded.content_hash, updated_at=excluded.updated_at`,
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
	err := r.db.GetContext(ctx, &row,
		`SELECT path, content, content_type, size, content_hash, updated_at FROM shared_resources WHERE path=?`, path)
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
	err := r.db.GetContext(ctx, &row,
		`SELECT path, size, content_hash, updated_at FROM shared_resources WHERE path=?`, path)
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
		err = r.db.SelectContext(ctx, &rows,
			`SELECT path, size, content_hash, updated_at FROM shared_resources WHERE path LIKE ? ORDER BY path ASC`,
			prefix+"%")
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
	_, err := r.db.ExecContext(ctx, `DELETE FROM shared_resources WHERE path=?`, path)
	return err
}

// SHA1Hex computes SHA-1 hex of content. Used by sync module.
func SHA1Hex(content []byte) string {
	h := sha1.Sum(content)
	return fmt.Sprintf("%x", h)
}
```

- [ ] **Step 2: Implement resourcelocal/resource.go**

```go
// internal/repo/resourcelocal/resource.go
package resourcelocal

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

type localRepo struct {
	homeDir string
}

func New(homeDir string) repo.ResourceRepo {
	return &localRepo{homeDir: homeDir}
}

func (r *localRepo) abs(path string) string {
	return filepath.Join(r.homeDir, filepath.FromSlash(path))
}

func (r *localRepo) Put(ctx context.Context, res *repo.Resource) error {
	absPath := r.abs(res.Path)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return err
	}
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, res.Content, 0644); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, absPath)
}

func (r *localRepo) Get(ctx context.Context, path string) (*repo.Resource, error) {
	absPath := r.abs(path)
	content, err := os.ReadFile(absPath)
	if os.IsNotExist(err) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	info, _ := os.Stat(absPath)
	h := sha1.Sum(content)
	return &repo.Resource{
		Path: path, Content: content,
		Size: int64(len(content)),
		ContentHash: fmt.Sprintf("%x", h),
		UpdatedAt: info.ModTime(),
	}, nil
}

func (r *localRepo) Stat(ctx context.Context, path string) (*repo.ResourceEntry, error) {
	absPath := r.abs(path)
	content, err := os.ReadFile(absPath)
	if os.IsNotExist(err) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	info, _ := os.Stat(absPath)
	h := sha1.Sum(content)
	return &repo.ResourceEntry{
		Path: path, Size: int64(len(content)),
		ContentHash: fmt.Sprintf("%x", h),
		UpdatedAt: info.ModTime(),
	}, nil
}

func (r *localRepo) List(ctx context.Context, prefix string) ([]*repo.ResourceEntry, error) {
	base := r.homeDir
	if prefix != "" {
		base = filepath.Join(r.homeDir, filepath.FromSlash(prefix))
	}
	var entries []*repo.ResourceEntry
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		content, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		info, _ := d.Info()
		h := sha1.Sum(content)
		rel, _ := filepath.Rel(r.homeDir, path)
		entries = append(entries, &repo.ResourceEntry{
			Path: filepath.ToSlash(rel),
			Size: int64(len(content)),
			ContentHash: fmt.Sprintf("%x", h),
			UpdatedAt: info.ModTime(),
		})
		return nil
	})
	return entries, err
}

func (r *localRepo) Delete(ctx context.Context, path string) error {
	err := os.Remove(r.abs(path))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
```

- [ ] **Step 3: Write resourcedb_test.go**

```go
// internal/repo/resourcedb/resource_test.go
package resourcedb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newRepo(t *testing.T) repo.ResourceRepo {
	t.Helper()
	sqlxDB, _, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB)
}

func TestPutAndGet(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	content := []byte("# Weather Skill")
	res := &repo.Resource{
		Path: "skills/weather/SKILL.md", Content: content,
		ContentType: "text/markdown", Size: int64(len(content)),
		ContentHash: SHA1Hex(content), UpdatedAt: time.Now(),
	}
	if err := r.Put(ctx, res); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := r.Get(ctx, "skills/weather/SKILL.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Content) != "# Weather Skill" {
		t.Errorf("unexpected content: %s", got.Content)
	}
}

func TestStat_NotFound(t *testing.T) {
	r := newRepo(t)
	_, err := r.Stat(context.Background(), "nonexistent")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestList_Prefix(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	for _, p := range []string{"skills/a/SKILL.md", "skills/b/SKILL.md", "mcp/server.json"} {
		c := []byte("content")
		r.Put(ctx, &repo.Resource{Path: p, Content: c, Size: int64(len(c)), ContentHash: SHA1Hex(c), UpdatedAt: time.Now()})
	}
	entries, err := r.List(ctx, "skills/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries under skills/, got %d", len(entries))
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/repo/resourcedb/... ./internal/repo/resourcelocal/... -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repo/resourcedb/ internal/repo/resourcelocal/
git commit -m "feat: implement ResourceRepo DB and local backends"
```

---

## Task 10: Create repo factory

**Files:**
- Create: `internal/repo/factory.go`

- [ ] **Step 1: Create factory.go**

```go
// internal/repo/factory.go
package repo

import (
	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo/memberdb"
	"github.com/zfd81/groot/internal/repo/memorydb"
	"github.com/zfd81/groot/internal/repo/resourcedb"
	"github.com/zfd81/groot/internal/repo/resourcelocal"
	"github.com/zfd81/groot/internal/repo/scheduledb"
)

// Repos holds all domain repositories constructed from one DB connection.
type Repos struct {
	Member   MemberRepo
	Schedule ScheduleRepo
	Memory   MemoryRepo
	Resource ResourceRepo
}

// NewRepos constructs all Repository implementations.
// For SQLite dialect, Resource uses the local-fs implementation.
// For MySQL/PG, Resource uses the DB implementation.
func NewRepos(sqlxDB *sqlx.DB, dialect db.Dialect, homeDir string) *Repos {
	var resourceRepo ResourceRepo
	if dialect == db.DialectSQLite {
		resourceRepo = resourcelocal.New(homeDir)
	} else {
		resourceRepo = resourcedb.New(sqlxDB)
	}
	return &Repos{
		Member:   memberdb.New(sqlxDB),
		Schedule: scheduledb.New(sqlxDB),
		Memory:   memorydb.New(sqlxDB),
		Resource: resourceRepo,
	}
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./internal/repo/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/repo/factory.go
git commit -m "feat: add repo factory"
```

---

## Task 11: Refactor internal/cluster to use MemberRepo

**Files:**
- Modify: `internal/cluster/cluster.go`
- Delete: `internal/cluster/member.go`
- Delete: `internal/cluster/member_test.go`
- Modify: `internal/cluster/election.go`
- Create: `internal/cluster/cluster_test.go` (rewrite)

- [ ] **Step 1: Delete old member.go and member_test.go**

```bash
rm internal/cluster/member.go internal/cluster/member_test.go
```

- [ ] **Step 2: Update election.go**

Replace `MemberInfo` with `repo.Member` import, update `DetermineRole` signature:

```go
// internal/cluster/election.go
package cluster

import (
	"sort"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

const (
	RoleLeader   = "leader"
	RoleFollower = "follower"
)

// DetermineRole decides whether this instance should be leader.
// members: all known members from MemberRepo.ListAll().
// timeout: heartbeat timeout (7s).
func DetermineRole(selfID string, members []*repo.Member, timeout time.Duration) string {
	now := time.Now()
	var alive []*repo.Member
	for _, m := range members {
		if now.Sub(m.HeartbeatAt) < timeout {
			alive = append(alive, m)
		}
	}
	if len(alive) == 0 {
		return RoleLeader
	}
	sort.Slice(alive, func(i, j int) bool { return alive[i].RegID < alive[j].RegID })
	if alive[0].RegID == selfID {
		return RoleLeader
	}
	return RoleFollower
}
```

- [ ] **Step 3: Rewrite cluster.go**

Replace the `storage.Storage` field with `repo.MemberRepo`:

```go
// internal/cluster/cluster.go
package cluster

import (
	"context"
	"os"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

const (
	heartbeatInterval = 3 * time.Second
	heartbeatTimeout  = 7 * time.Second
)

type Cluster struct {
	host  string
	port  int
	regID string
	role  string
	mu    sync.RWMutex
	log   *logger.Logger
	repo  repo.MemberRepo

	onBecomeLeader func()
	onLoseLeader   func()

	ctx    context.Context
	cancel context.CancelFunc
}

func New(host string, port int, log *logger.Logger, memberRepo repo.MemberRepo) *Cluster {
	return &Cluster{host: host, port: port, log: log, repo: memberRepo}
}

func (c *Cluster) SetCallbacks(onBecomeLeader, onLoseLeader func()) {
	c.onBecomeLeader = onBecomeLeader
	c.onLoseLeader = onLoseLeader
}

func (c *Cluster) Join(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.register()
	go c.run()
	return nil
}

func (c *Cluster) Leave() {
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.regID == "" {
		return
	}
	if err := c.repo.Remove(context.Background(), c.regID); err != nil {
		c.log.Error("删除注册记录失败", zap.Error(err))
	}
	c.regID = ""
}

func (c *Cluster) IsLeader() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.role == RoleLeader
}

func (c *Cluster) RegID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.regID
}

func (c *Cluster) Role() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.role
}

func (c *Cluster) run() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.heartbeat()
		}
	}
}

func (c *Cluster) heartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.repo.Get(c.ctx, c.regID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			if c.role == RoleLeader && c.onLoseLeader != nil {
				c.onLoseLeader()
			}
			c.register()
			return
		}
		c.log.Warn("自检失败,跳过本轮心跳", zap.Error(err))
		return
	}

	if c.role == RoleLeader {
		c.leaderHeartbeat()
	} else {
		c.followerHeartbeat()
	}
}

func (c *Cluster) register() {
	members, err := c.repo.ListAll(c.ctx)
	if err != nil {
		c.log.Error("列出成员失败", zap.Error(err))
		c.role = RoleFollower
		return
	}
	c.regID = GenerateRegID()
	c.role = DetermineRole(c.regID, members, heartbeatTimeout)
	pid := os.Getpid()
	m := &repo.Member{
		RegID: c.regID, Role: c.role,
		Host: c.host, Port: c.port, Pid: pid,
		HeartbeatAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := c.repo.Register(c.ctx, m); err != nil {
		c.log.Error("写入注册记录失败", zap.Error(err))
		return
	}
	c.log.Info("集群注册完成", zap.String("reg_id", c.regID), zap.String("role", c.role), zap.Int("pid", pid))
	if c.role == RoleLeader && c.onBecomeLeader != nil {
		c.onBecomeLeader()
	}
}

func (c *Cluster) leaderHeartbeat() {
	if err := c.repo.Heartbeat(c.ctx, c.regID); err != nil {
		c.log.Error("心跳写入失败", zap.Error(err))
		return
	}
	if err := c.repo.UpdateRole(c.ctx, c.regID, RoleLeader); err != nil {
		c.log.Error("角色更新失败", zap.Error(err))
	}
	n, err := c.repo.RemoveExpired(c.ctx, time.Now().Add(-heartbeatTimeout))
	if err != nil {
		c.log.Warn("清理超时成员失败", zap.Error(err))
	} else if n > 0 {
		c.log.Info("清理超时成员", zap.Int("count", n))
	}
}

func (c *Cluster) followerHeartbeat() {
	members, err := c.repo.ListAll(c.ctx)
	if err != nil {
		c.log.Warn("列出成员失败,跳过本轮心跳", zap.Error(err))
		return
	}
	now := time.Now()
	var alive []*repo.Member
	for _, m := range members {
		if now.Sub(m.HeartbeatAt) < heartbeatTimeout {
			alive = append(alive, m)
		}
	}
	sort.Slice(alive, func(i, j int) bool { return alive[i].RegID < alive[j].RegID })
	if len(alive) > 0 && alive[0].RegID == c.regID {
		c.role = RoleLeader
		c.repo.UpdateRole(c.ctx, c.regID, RoleLeader)
		c.log.Info("提升为 leader", zap.String("reg_id", c.regID))
		c.repo.RemoveExpired(c.ctx, time.Now().Add(-heartbeatTimeout))
		if c.onBecomeLeader != nil {
			c.onBecomeLeader()
		}
	} else {
		c.repo.Heartbeat(c.ctx, c.regID)
	}
}

// GenerateRegID generates a 17-digit millisecond timestamp reg ID.
func GenerateRegID() string {
	s := time.Now().Format("20060102150405.000")
	return strings.Replace(s, ".", "", 1)
}
```

Add imports: `"errors"`, `"strings"`.

- [ ] **Step 4: Build**

```bash
go build ./internal/cluster/...
```

- [ ] **Step 5: Run cluster tests**

```bash
go test ./internal/cluster/... -v
```

- [ ] **Step 6: Commit**

```bash
git add internal/cluster/
git commit -m "feat: refactor cluster to use MemberRepo"
```

---

## Task 12: Refactor internal/schedule to use ScheduleRepo

**Files:**
- Modify: `internal/schedule/storage.go` (replace body)
- Modify: `internal/schedule/manager.go` (update constructor)

- [ ] **Step 1: Replace storage.go with a thin ScheduleRepo adapter**

The existing `internal/schedule/storage.go` wraps file operations. Replace its entire body to delegate to `repo.ScheduleRepo`. Keep the same `Storage` struct name so `manager.go` and `engine.go` don't need changes initially.

```go
// internal/schedule/storage.go
package schedule

import (
	"context"
	"time"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// Storage wraps ScheduleRepo to preserve the existing internal API surface.
type Storage struct {
	repo repo.ScheduleRepo
	log  *logger.Logger
}

func NewStorage(r repo.ScheduleRepo, log *logger.Logger) *Storage {
	return &Storage{repo: r, log: log}
}

func (s *Storage) SaveTask(task *Task) error {
	return s.repo.SaveTask(context.Background(), task)
}

func (s *Storage) LoadTask(taskID string) (*Task, error) {
	t, err := s.repo.LoadTask(context.Background(), taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, fmt.Errorf("任务 %s 不存在", taskID)
	}
	return t, err
}

func (s *Storage) ListTasks(status string) ([]*Task, error) {
	return s.repo.ListByStatus(context.Background(), status)
}

func (s *Storage) MoveTask(taskID, newStatus string) error {
	task, err := s.repo.LoadTask(context.Background(), taskID)
	if err != nil {
		return err
	}
	return s.repo.MoveStatus(context.Background(), taskID, newStatus, task.Version)
}

func (s *Storage) DeleteTask(taskID string) error {
	return s.repo.DeleteTask(context.Background(), taskID)
}

func (s *Storage) SaveExecution(rec *ExecutionRecord) error {
	return s.repo.SaveExecution(context.Background(), rec)
}

func (s *Storage) GetTaskStatus(taskID string) (string, error) {
	task, err := s.repo.LoadTask(context.Background(), taskID)
	if err != nil {
		return "", err
	}
	return task.Status, nil
}
```

Add imports: `"context"`, `"errors"`, `"fmt"`.

Also add `Version int64` to the `Task` struct in `types.go` so `MoveTask` can pass the version:

```go
// in types.go, add to Task struct:
Version int64 `json:"version,omitempty"`
```

- [ ] **Step 2: Build**

```bash
go build ./internal/schedule/...
```

- [ ] **Step 3: Run schedule tests**

```bash
go test ./internal/schedule/... -v 2>&1 | tail -30
```

- [ ] **Step 4: Commit**

```bash
git add internal/schedule/
git commit -m "feat: refactor schedule.Storage to delegate to ScheduleRepo"
```

---

## Task 13: Refactor internal/memory to use MemoryRepo

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/memory/memory.go`

- [ ] **Step 1: Rewrite manager.go constructor and key methods**

Replace `storage.Storage` field with `repo.MemoryRepo`:

```go
// internal/memory/manager.go  — key changes only (show full new constructor and CreateSession)
package memory

import (
	"context"
	"time"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

type Manager struct {
	retentionDays int
	log           *logger.Logger
	repo          repo.MemoryRepo
}

func NewManager(retentionDays int, log *logger.Logger, memRepo repo.MemoryRepo) *Manager {
	if memRepo == nil {
		panic("memory: NewManager: repo must not be nil")
	}
	return &Manager{retentionDays: retentionDays, log: log, repo: memRepo}
}

func (m *Manager) CreateSession(sessionID string) error {
	s := &repo.Session{
		SessionID: sessionID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return m.repo.CreateSession(context.Background(), s)
}

func (m *Manager) ExistsSession(sessionID string) (bool, error) {
	return m.repo.ExistsSession(context.Background(), sessionID)
}

func (m *Manager) ListSessions() ([]*repo.Session, error) {
	return m.repo.ListSessions(context.Background())
}

func (m *Manager) SaveChatRecord(rec *ChatRecord) error {
	return m.repo.SaveChat(context.Background(), rec)
}

func (m *Manager) GetChatRecord(chatID string) (*ChatRecord, error) {
	return m.repo.GetChat(context.Background(), chatID)
}

func (m *Manager) GetHistory(sessionID string) ([]*ChatRecord, error) {
	return m.repo.LoadHistory(context.Background(), sessionID)
}

func (m *Manager) DeleteSession(sessionID string) error {
	return m.repo.DeleteSession(context.Background(), sessionID)
}

func (m *Manager) Cleanup(ctx context.Context) error {
	if m.retentionDays <= 0 {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -m.retentionDays)
	_, err := m.repo.DeleteExpiredSessions(ctx, cutoff)
	return err
}
```

- [ ] **Step 2: Update callers of old GetHistory that expected *History**

```bash
grep -rn "GetHistory\|saveHistory\|historyPath\|ListSessions" internal/ cmd/ --include="*.go" | grep -v "_test.go"
```

Update each call site. `GetHistory` now returns `[]*ChatRecord` not `*History`. Callers that built LLM messages from `History.Messages` should now iterate `[]*ChatRecord` directly.

- [ ] **Step 3: Build**

```bash
go build ./internal/memory/...
```

- [ ] **Step 4: Run memory tests**

```bash
go test ./internal/memory/... -v 2>&1 | tail -30
```

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat: refactor memory.Manager to use MemoryRepo"
```

---

## Task 14: Refactor internal/sync to use ResourceRepo

**Files:**
- Modify: `internal/sync/sync.go`
- Modify: `internal/sync/diff.go`
- Modify: `internal/cmd/push.go`
- Modify: `internal/cmd/pull.go`
- Modify: `internal/cmd/diff_cmd.go`

- [ ] **Step 1: Update ErrSyncDisabled in sync.go**

```go
var ErrSyncDisabled = errors.New("sync: 仅在 MySQL/PostgreSQL 模式下可用 — 请在 env.yaml 中配置 database 节")
```

Replace `localSyncManager.store istorage.Storage` with `localSyncManager.repo repo.ResourceRepo`:

```go
type localSyncManager struct {
	homeDir string
	repo    repo.ResourceRepo
}

func NewSyncManager(homeDir string, r repo.ResourceRepo) SyncManager {
	if r == nil {
		return &disabledSyncManager{}
	}
	return &localSyncManager{homeDir: homeDir, repo: r}
}
```

Remove the `remoteBase string` parameter — ResourceRepo handles paths directly.

- [ ] **Step 2: Update diff.go**

Replace `ComputeDiff(store istorage.Storage, ...)` with `ComputeDiff(r repo.ResourceRepo, ...)`:

```go
func ComputeDiff(r repo.ResourceRepo, localBase string, paths []string) (DiffResult, error) {
	var result DiffResult
	for _, rel := range paths {
		localPath := filepath.Join(localBase, filepath.FromSlash(rel))
		localFiles, err := walkLocalFiles(localPath, localBase)
		if err != nil {
			return result, fmt.Errorf("sync diff: scan local %s: %w", rel, err)
		}
		remoteEntries, err := r.List(context.Background(), rel)
		if err != nil {
			return result, fmt.Errorf("sync diff: list remote %s: %w", rel, err)
		}
		remoteMap := make(map[string]*repo.ResourceEntry, len(remoteEntries))
		for _, e := range remoteEntries {
			remoteMap[e.Path] = e
		}
		// classify local files
		for relPath, localInfo := range localFiles {
			if remote, ok := remoteMap[relPath]; ok {
				if localInfo.size != remote.Size || localInfo.hash != remote.ContentHash {
					result.Modified = append(result.Modified, relPath)
				} else {
					result.Same = append(result.Same, relPath)
				}
				delete(remoteMap, relPath)
			} else {
				result.Added = append(result.Added, relPath)
			}
		}
		// remaining remoteMap entries are remote-only
		for relPath := range remoteMap {
			result.Removed = append(result.Removed, relPath)
		}
	}
	return result, nil
}
```

Update `walkLocalFiles` to return a struct with `size int64` and `hash string` (SHA-1 hex computed on read):

```go
type localFileInfo struct {
	size int64
	hash string
}

func walkLocalFiles(localPath, localBase string) (map[string]localFileInfo, error) {
	files := make(map[string]localFileInfo)
	info, err := os.Stat(localPath)
	if os.IsNotExist(err) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(localBase, localPath)
		h := sha1.Sum(content)
		files[filepath.ToSlash(rel)] = localFileInfo{
			size: int64(len(content)), hash: fmt.Sprintf("%x", h),
		}
		return files, nil
	}
	return files, filepath.WalkDir(localPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		content, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(localBase, path)
		h := sha1.Sum(content)
		files[filepath.ToSlash(rel)] = localFileInfo{
			size: int64(len(content)), hash: fmt.Sprintf("%x", h),
		}
		return nil
	})
}
```

Add imports: `"context"`, `"crypto/sha1"`, `"fmt"`, `"io/fs"`, `"strings"`.

- [ ] **Step 3: Update push/pull — replace os.Chtimes call**

In `internal/cmd/pull.go`, remove the `os.Chtimes` step that anchored local mtime to remote LastModified. With SHA-1 diff, mtime is no longer used for comparison.

In `pushOne`/`pullOne` helpers in `sync.go`, replace `store.Write`/`store.Read` with `repo.Put`/`repo.Get`.

- [ ] **Step 4: Update cmd files to pass ResourceRepo**

In `internal/cmd/push.go`, `pull.go`, `diff_cmd.go`: replace `storage.New(...)` with the `ResourceRepo` from the repos instance. The `RunPush/RunPull/RunDiff` functions must accept `repo.ResourceRepo` instead of `istorage.Storage`.

- [ ] **Step 5: Build**

```bash
go build ./internal/sync/... ./internal/cmd/...
```

- [ ] **Step 6: Run sync tests**

```bash
go test ./internal/sync/... -v
```

- [ ] **Step 7: Commit**

```bash
git add internal/sync/ internal/cmd/
git commit -m "feat: refactor sync to use ResourceRepo, replace mtime diff with SHA-1"
```

---

## Task 15: Wire main.go — open DB, construct repos, inject modules

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: Replace storage.New with db.Open + repo.NewRepos**

In `cmd/groot/main.go`, find the section that creates the storage and cluster/schedule/memory managers.

Replace:
```go
store, err := storage.New(cfg.Storage)
```

With:
```go
sqlxDB, dialect, err := db.Open(cfg.Database, homeDir)
if err != nil {
    log.Fatal("数据库连接失败", zap.Error(err))
}
defer sqlxDB.Close()
repos := repo.NewRepos(sqlxDB, dialect, homeDir)
```

- [ ] **Step 2: Update cluster.New call**

Replace the old signature `cluster.New(membersDir, host, port, log, store)` with:
```go
clusterInstance := cluster.New(cfg.Server.Host, cfg.Server.Port, log, repos.Member)
```

- [ ] **Step 3: Update schedule.NewStorage call**

```go
schedStorage := schedule.NewStorage(repos.Schedule, log)
```

- [ ] **Step 4: Update memory.NewManager call**

```go
memManager := memory.NewManager(cfg.Memory.RetentionDays, log, repos.Memory)
```

- [ ] **Step 5: Update sync.NewSyncManager call**

```go
// MySQL/PG: repos.Resource is resourcedb — push/pull/diff enabled
// SQLite:   repos.Resource is resourcelocal — push/pull/diff disabled (returns ErrSyncDisabled)
syncMgr := sync.NewSyncManager(homeDir, repos.Resource)
```

- [ ] **Step 6: Remove all storage import references**

```bash
grep -n "storage\." cmd/groot/main.go
```
Remove or replace each reference.

- [ ] **Step 7: Build the binary**

```bash
go build -o bin/groot ./cmd/groot
```
Expected: no errors.

- [ ] **Step 8: Smoke test — start with SQLite (no env.yaml)**

```bash
./bin/groot &
sleep 2
curl -s http://localhost:8080/health || echo "no health endpoint"
kill %1
```

- [ ] **Step 9: Commit**

```bash
git add cmd/groot/main.go
git commit -m "feat: wire main.go to DB backend — replace storage with repos"
```

---

## Task 16: Delete internal/storage

**Files:**
- Delete: `internal/storage/`

- [ ] **Step 1: Verify no remaining references**

```bash
grep -rn "internal/storage\|istorage\." --include="*.go" . | grep -v "_test.go" | grep -v "vendor"
```
Expected: zero lines.

- [ ] **Step 2: Delete the package**

```bash
rm -rf internal/storage
```

- [ ] **Step 3: Build and test**

```bash
go build ./...
go test ./... 2>&1 | grep -E "FAIL|ok"
```
Expected: no FAIL.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore: delete internal/storage (replaced by repo layer)"
```

---

## Task 17: Full integration test — all three drivers

**Files:**
- Create: `tests/python/test_db_backend.py` (system test, user-run)
- Create: `internal/repo/integration_test.go`

- [ ] **Step 1: Write Go integration test using SQLite**

```go
// internal/repo/integration_test.go
//go:build integration
package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/memorydb"
	"github.com/zfd81/groot/internal/repo/memberdb"
)

func TestIntegration_MemberLifecycle(t *testing.T) {
	sqlxDB, _, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sqlxDB.Close()

	r := memberdb.New(sqlxDB)
	ctx := context.Background()

	m := &repo.Member{
		RegID: "20260610000000001", Role: "follower",
		Host: "127.0.0.1", Port: 8080, Pid: 1,
		HeartbeatAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := r.Register(ctx, m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.UpdateRole(ctx, m.RegID, "leader"); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	got, _ := r.Get(ctx, m.RegID)
	if got.Role != "leader" {
		t.Errorf("expected leader, got %s", got.Role)
	}
	r.Remove(ctx, m.RegID)
	_, err = r.Get(ctx, m.RegID)
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound after remove, got %v", err)
	}
}

func TestIntegration_SessionChatHistory(t *testing.T) {
	sqlxDB, _, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sqlxDB.Close()

	r := memorydb.New(sqlxDB)
	ctx := context.Background()

	sess := &repo.Session{SessionID: "s-integ", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	r.CreateSession(ctx, sess)

	for i := 0; i < 3; i++ {
		r.SaveChat(ctx, &memory.ChatRecord{
			ChatID:      fmt.Sprintf("chat-%d", i),
			SessionID:   "s-integ",
			Instruction: fmt.Sprintf("turn %d", i),
			Status:      "success",
			StartedAt:   time.Now(),
		})
	}

	history, err := r.LoadHistory(ctx, "s-integ")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("expected 3 history entries, got %d", len(history))
	}
	if history[0].Round != 1 || history[2].Round != 3 {
		t.Errorf("rounds out of order: %v %v", history[0].Round, history[2].Round)
	}
}
```

- [ ] **Step 2: Run integration test**

```bash
go test -tags=integration ./internal/repo/... -v -run TestIntegration
```
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/repo/integration_test.go
git commit -m "test: add integration tests for member and memory repo"
```

---

## Task 18: Cleanup and final build check

- [ ] **Step 1: Remove minio from go.mod**

```bash
go mod tidy
grep "minio" go.mod
```
Expected: minio line gone.

- [ ] **Step 2: Full build**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 3: Full test suite**

```bash
go test ./... 2>&1 | grep -E "FAIL|panic"
```
Expected: no FAILs, no panics.

- [ ] **Step 4: Update README if storage section exists**

```bash
grep -n "minio\|MinIO\|storage" README.md | head -20
```

Remove MinIO references; update deployment section to describe `env.yaml` `database` node.

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "chore: cleanup, remove minio dependency, update README"
```
