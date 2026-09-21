# 集群消息指令系统 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Groot 多实例集群提供基于数据库表 + 心跳轮询的单向指令通道：任意实例内部代码调用 `SendMessage` 写入 `cluster_messages`，所有实例在 3 秒心跳 tick 中轮询自己未消费的广播/点对点消息，路由到已注册的模块处理器执行，并把处理结果写入 `cluster_message_consumers`。

**Architecture:** 仓储层沿用项目惯例——接口在 `internal/repo/message.go`，三方言 SQL 实现在 `internal/repo/messagedb/`，由 `repofactory` 装配。`internal/cluster/` 新增 `MessageService`（发送、处理器注册、轮询、清理），`Cluster` 通过 `SetMessageService` 挂接，在心跳 tick 中触发一次异步轮询（原子标志保证同一时刻只有一轮在跑，不阻塞心跳、不在心跳写锁内运行处理器）。Leader 通过现有 gocron 调度器每日 03:00 清理 30 天前的消息。纯内部 API，不新增 HTTP 端点。

**Tech Stack:** Go 1.26、sqlx（SQLite/MySQL/PostgreSQL）、go.uber.org/zap、gocron v2。测试用 Go 标准 `testing`，数据库测试用 `db.Open(nil, t.TempDir())` 建临时 SQLite。

**Spec:** `docs/superpowers/specs/2026-09-18-cluster-messaging-design.md`

**与设计文档的对齐说明（实现时以本计划为准）：**

| 设计文档 | 本计划 | 原因 |
|---|---|---|
| `internal/cluster/message_repo.go` 放 SQL | 接口 `internal/repo/message.go` + 实现 `internal/repo/messagedb/message.go` | 项目惯例：cluster 包只依赖 repo 接口，SQL 全部在 `internal/repo/*db/` |
| 时间列 `TIMESTAMP` | `BIGINT` 毫秒时间戳 | 项目所有表统一用 Unix 毫秒，避免三方言时区差异 |
| `target_instance NULL` 表示广播 | `target_instance = ''` 表示广播，列 `NOT NULL DEFAULT ''` | 三方言下空串比 NULL 判断更简单，与 Go 侧 `TargetInstance: ""` 一致 |
| `payload JSON` | `TEXT` / `LONGTEXT` 存 JSON 文本 | SQLite 无 JSON 类型；Go 侧仍用 `map[string]any` |
| 轮询"在 heartbeat() 内调用" | 同一 tick 内，`heartbeat()` 之后由 `triggerPoll()` 启一个受原子标志保护的 goroutine | `heartbeat()` 全程持有 `c.mu` 写锁，处理器若在锁内运行会与 `IsLeader()` 死锁并拖慢心跳 |
| "复用 memory cleanup 调度器" | 在 `startLeaderTasks` 中用 `sched.AddDaily(3, 0, ...)` 新注册每日清理任务 | 当前代码中已不存在 memory cleanup 任务 |

**Git 规则：** 项目规范要求所有 commit 必须由用户明确请求。本计划的任务**不包含提交步骤**，全部任务完成并通过测试后，向用户报告并等待"提交"指令。

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/repo/message.go` | 新建：`ClusterMessage`、`MessageConsumer` 领域类型，`MessageRepo` 接口，消费状态常量 |
| `internal/repo/messagedb/message.go` | 新建：`MessageRepo` 的 sqlx 实现（三方言） |
| `internal/repo/messagedb/message_test.go` | 新建：仓储层单元测试 |
| `internal/db/migrate.go` | 修改：三方言 DDL 增 `cluster_messages`、`cluster_message_consumers` |
| `internal/db/migrate_test.go` | 修改：新增建表与幂等测试 |
| `internal/repo/repofactory/factory.go` | 修改：`Repos` 增 `Message` 字段并装配 |
| `internal/repo/repofactory/factory_test.go` | 修改：断言 `Message` 非 nil |
| `internal/cluster/message_handler.go` | 新建：`Message` 结构、`MessageHandler` 接口、`HandlerFunc` 适配器、与 repo 类型的互转 |
| `internal/cluster/messaging.go` | 新建：`MessageService`（构造、处理器注册、`SendMessage` 校验与重试、`Cleanup`） |
| `internal/cluster/message_poller.go` | 新建：`MessageService.Poll` / `processMessage`；`Cluster.SetMessageService` / `triggerPoll` |
| `internal/cluster/message_cleanup.go` | 新建：`NewMessageCleanupTask` gocron 任务包装 |
| `internal/cluster/cluster.go` | 修改：`Cluster` 增 `msg`、`polling` 字段；`run()` tick 中调用 `triggerPoll()` |
| `internal/cluster/message_test.go` | 新建：MessageService 单元测试（发送、轮询、处理、清理、Cluster 集成） |
| `cmd/groot/main.go` | 修改：构造 `MessageService`、挂接到 Cluster、Leader 注册每日清理任务 |
| `docs/superpowers/specs/2026-09-18-cluster-messaging-design.md` | 修改：第六章代码组织与本计划对齐 |
| `tests/TEST_CASES.md` | 修改：登记新增单元测试 |

---

## Task 1: 仓储层类型与接口

**Files:**
- Create: `internal/repo/message.go`

- [ ] **Step 1: 写接口文件**

```go
// internal/repo/message.go
package repo

import (
	"context"
	"time"
)

// 消费记录状态。
const (
	ConsumeStatusSuccess = "success"
	ConsumeStatusFailed  = "failed"
)

// ClusterMessage 是集群实例间的一条指令消息（cluster_messages 表的一行）。
// TargetInstance 为空串表示广播：所有实例都要处理；非空表示只有该 reg_id 的实例处理。
// Payload 是 JSON 文本，由 cluster 包负责与 map[string]any 互转。
type ClusterMessage struct {
	ID             int64
	Type           string
	Payload        string
	TargetInstance string
	TargetModule   string
	Priority       int
	CreatedAt      time.Time
	ExpiresAt      time.Time
	SourceInstance string
}

// MessageConsumer 记录某个实例对某条消息的处理结果（cluster_message_consumers 表的一行）。
type MessageConsumer struct {
	MessageID    int64
	InstanceID   string
	ConsumedAt   time.Time
	Status       string
	ErrorMessage string
}

// MessageRepo 是集群消息的持久化接口。
type MessageRepo interface {
	// Insert 写入一条消息。ID 由数据库自增生成，调用方不依赖回填。
	Insert(ctx context.Context, m *ClusterMessage) error

	// ListPending 返回 instanceID 尚未消费、未过期、且目标为广播或该实例的消息，
	// 按 priority 升序、created_at 升序、id 升序排列，最多 limit 条。
	ListPending(ctx context.Context, instanceID string, now time.Time, limit int) ([]*ClusterMessage, error)

	// RecordConsumption 写入一条消费记录。同一 (message_id, instance_id) 重复写入返回错误。
	RecordConsumption(ctx context.Context, c *MessageConsumer) error

	// ListConsumers 返回某条消息的全部消费记录，按 instance_id 升序。
	ListConsumers(ctx context.Context, messageID int64) ([]*MessageConsumer, error)

	// DeleteBefore 删除 created_at 早于 before 的消息，并清理已无对应消息的孤立消费记录。
	// 返回删除的消息数与消费记录数。
	DeleteBefore(ctx context.Context, before time.Time) (deletedMessages int, deletedConsumers int, err error)
}
```

- [ ] **Step 2: 编译确认**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./internal/repo/`
Expected: 无输出（编译通过）

---

## Task 2: 数据库 schema 与迁移

**Files:**
- Modify: `internal/db/migrate.go`（`sqliteDDL` / `mysqlDDL` / `postgresDDL` 三个函数末尾）
- Modify: `internal/db/migrate_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/db/migrate_test.go` 末尾追加：

```go
// TestMigrate_CreatesClusterMessageTables 新库执行 Migrate 后应存在集群消息两张表
// 及其关键列；重复执行不报错（幂等）。
func TestMigrate_CreatesClusterMessageTables(t *testing.T) {
	sqlxDB := openLegacyDB(t)

	if err := Migrate(sqlxDB, DialectSQLite); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := Migrate(sqlxDB, DialectSQLite); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	checks := []struct{ table, column string }{
		{"cluster_messages", "id"},
		{"cluster_messages", "message_type"},
		{"cluster_messages", "payload"},
		{"cluster_messages", "target_instance"},
		{"cluster_messages", "target_module"},
		{"cluster_messages", "priority"},
		{"cluster_messages", "created_at"},
		{"cluster_messages", "expires_at"},
		{"cluster_messages", "source_instance"},
		{"cluster_message_consumers", "message_id"},
		{"cluster_message_consumers", "instance_id"},
		{"cluster_message_consumers", "consumed_at"},
		{"cluster_message_consumers", "status"},
		{"cluster_message_consumers", "error_message"},
	}
	for _, c := range checks {
		exists, err := columnExists(sqlxDB, DialectSQLite, c.table, c.column)
		if err != nil {
			t.Fatalf("columnExists(%s.%s): %v", c.table, c.column, err)
		}
		if !exists {
			t.Errorf("column %s.%s missing after Migrate", c.table, c.column)
		}
	}

	for _, idx := range []struct{ table, name string }{
		{"cluster_messages", "idx_cm_target_expires"},
		{"cluster_messages", "idx_cm_priority_created"},
		{"cluster_message_consumers", "idx_cmc_instance_consumed"},
	} {
		exists, err := indexExists(sqlxDB, DialectSQLite, idx.table, idx.name)
		if err != nil {
			t.Fatalf("indexExists(%s): %v", idx.name, err)
		}
		if !exists {
			t.Errorf("index %s missing after Migrate", idx.name)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/db/ -run TestMigrate_CreatesClusterMessageTables -v`
Expected: FAIL，输出多行 `column cluster_messages.xxx missing after Migrate`

- [ ] **Step 3: 在 sqliteDDL 末尾追加**

在 `sqliteDDL()` 返回的切片中，`uk_api_keys_name` 那一行之后追加：

```go
		`CREATE TABLE IF NOT EXISTS cluster_messages (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			message_type    TEXT    NOT NULL,
			payload         TEXT    NOT NULL,
			target_instance TEXT    NOT NULL DEFAULT '',
			target_module   TEXT    NOT NULL DEFAULT '',
			priority        INTEGER NOT NULL DEFAULT 5,
			created_at      INTEGER NOT NULL,
			expires_at      INTEGER NOT NULL,
			source_instance TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cm_target_expires ON cluster_messages(target_instance, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cm_priority_created ON cluster_messages(priority, created_at)`,
		`CREATE TABLE IF NOT EXISTS cluster_message_consumers (
			message_id    INTEGER NOT NULL,
			instance_id   TEXT    NOT NULL,
			consumed_at   INTEGER NOT NULL,
			status        TEXT    NOT NULL,
			error_message TEXT    NOT NULL DEFAULT '',
			PRIMARY KEY (message_id, instance_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cmc_instance_consumed ON cluster_message_consumers(instance_id, consumed_at)`,
```

- [ ] **Step 4: 在 mysqlDDL 末尾追加**

在 `mysqlDDL()` 返回的切片中，`api_keys` 建表语句之后追加：

```go
		`CREATE TABLE IF NOT EXISTS cluster_messages (
			id              BIGINT       PRIMARY KEY AUTO_INCREMENT,
			message_type    VARCHAR(64)  NOT NULL,
			payload         LONGTEXT     NOT NULL,
			target_instance VARCHAR(32)  NOT NULL DEFAULT '',
			target_module   VARCHAR(64)  NOT NULL DEFAULT '',
			priority        INT          NOT NULL DEFAULT 5,
			created_at      BIGINT       NOT NULL,
			expires_at      BIGINT       NOT NULL,
			source_instance VARCHAR(32)  NOT NULL DEFAULT '',
			KEY idx_cm_target_expires (target_instance, expires_at),
			KEY idx_cm_priority_created (priority, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS cluster_message_consumers (
			message_id    BIGINT       NOT NULL,
			instance_id   VARCHAR(32)  NOT NULL,
			consumed_at   BIGINT       NOT NULL,
			status        VARCHAR(16)  NOT NULL,
			error_message TEXT         NOT NULL,
			PRIMARY KEY (message_id, instance_id),
			KEY idx_cmc_instance_consumed (instance_id, consumed_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
```

注意：MySQL 的 `TEXT` 列不允许 `DEFAULT ''`，因此 `error_message` 只写 `NOT NULL`，由仓储层保证总是写入非 NULL 值（空串）。

- [ ] **Step 5: 在 postgresDDL 末尾追加**

在 `postgresDDL()` 返回的切片中，`uk_api_keys_name` 那一行之后追加：

```go
		`CREATE TABLE IF NOT EXISTS cluster_messages (
			id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			message_type    VARCHAR(64)  NOT NULL,
			payload         TEXT         NOT NULL,
			target_instance VARCHAR(32)  NOT NULL DEFAULT '',
			target_module   VARCHAR(64)  NOT NULL DEFAULT '',
			priority        INTEGER      NOT NULL DEFAULT 5,
			created_at      BIGINT       NOT NULL,
			expires_at      BIGINT       NOT NULL,
			source_instance VARCHAR(32)  NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cm_target_expires ON cluster_messages(target_instance, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cm_priority_created ON cluster_messages(priority, created_at)`,
		`CREATE TABLE IF NOT EXISTS cluster_message_consumers (
			message_id    BIGINT       NOT NULL,
			instance_id   VARCHAR(32)  NOT NULL,
			consumed_at   BIGINT       NOT NULL,
			status        VARCHAR(16)  NOT NULL,
			error_message TEXT         NOT NULL DEFAULT '',
			PRIMARY KEY (message_id, instance_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cmc_instance_consumed ON cluster_message_consumers(instance_id, consumed_at)`,
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/db/ -v -count=1`
Expected: 全部 PASS（含 `TestMigrate_CreatesClusterMessageTables`）

---

## Task 3: messagedb 仓储实现

**Files:**
- Create: `internal/repo/messagedb/message.go`
- Create: `internal/repo/messagedb/message_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/repo/messagedb/message_test.go
package messagedb

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.MessageRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func newMsg(typ, target string, priority int, ttl time.Duration) *repo.ClusterMessage {
	now := time.Now()
	return &repo.ClusterMessage{
		Type:           typ,
		Payload:        `{"k":"v"}`,
		TargetInstance: target,
		TargetModule:   "mod",
		Priority:       priority,
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
		SourceInstance: "sender",
	}
}

func mustInsert(t *testing.T, r repo.MessageRepo, m *repo.ClusterMessage) {
	t.Helper()
	if err := r.Insert(context.Background(), m); err != nil {
		t.Fatalf("Insert: %v", err)
	}
}

func TestInsertAndListPending_Broadcast(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))

	for _, inst := range []string{"inst-A", "inst-B"} {
		got, err := r.ListPending(ctx, inst, time.Now(), 10)
		if err != nil {
			t.Fatalf("ListPending(%s): %v", inst, err)
		}
		if len(got) != 1 {
			t.Fatalf("ListPending(%s) len = %d, want 1", inst, len(got))
		}
		m := got[0]
		if m.ID == 0 {
			t.Error("ID should be populated from db")
		}
		if m.Type != "sync" || m.Payload != `{"k":"v"}` || m.TargetModule != "mod" ||
			m.Priority != 5 || m.SourceInstance != "sender" || m.TargetInstance != "" {
			t.Errorf("round-trip mismatch: %+v", m)
		}
		if m.ExpiresAt.Sub(m.CreatedAt) < 59*time.Minute {
			t.Errorf("timestamps not preserved: created=%v expires=%v", m.CreatedAt, m.ExpiresAt)
		}
	}
}

func TestListPending_PointToPointOnlyTarget(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("restart", "inst-B", 5, time.Hour))

	a, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	b, _ := r.ListPending(ctx, "inst-B", time.Now(), 10)
	if len(a) != 0 {
		t.Errorf("inst-A should not see point-to-point message for inst-B, got %d", len(a))
	}
	if len(b) != 1 {
		t.Errorf("inst-B should see its message, got %d", len(b))
	}
}

func TestListPending_ExcludesExpired(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("old", "", 5, -time.Minute)) // 已过期
	mustInsert(t, r, newMsg("fresh", "", 5, time.Hour))

	got, err := r.ListPending(ctx, "inst-A", time.Now(), 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 1 || got[0].Type != "fresh" {
		t.Errorf("expected only fresh message, got %+v", got)
	}
}

func TestListPending_ExcludesConsumedBySelfOnly(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	first, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	if len(first) != 1 {
		t.Fatalf("setup: expected 1 pending, got %d", len(first))
	}

	err := r.RecordConsumption(ctx, &repo.MessageConsumer{
		MessageID: first[0].ID, InstanceID: "inst-A",
		ConsumedAt: time.Now(), Status: repo.ConsumeStatusSuccess,
	})
	if err != nil {
		t.Fatalf("RecordConsumption: %v", err)
	}

	a, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	b, _ := r.ListPending(ctx, "inst-B", time.Now(), 10)
	if len(a) != 0 {
		t.Errorf("inst-A already consumed, got %d pending", len(a))
	}
	if len(b) != 1 {
		t.Errorf("inst-B has not consumed, got %d pending", len(b))
	}
}

func TestListPending_OrderByPriorityThenCreated(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	base := time.Now()
	insertAt := func(typ string, prio int, at time.Time) {
		m := newMsg(typ, "", prio, time.Hour)
		m.CreatedAt = at
		mustInsert(t, r, m)
	}
	insertAt("low-late", 9, base.Add(2*time.Second))
	insertAt("high-late", 1, base.Add(3*time.Second))
	insertAt("low-early", 9, base.Add(1*time.Second))
	insertAt("high-early", 1, base)

	got, err := r.ListPending(ctx, "inst-A", time.Now(), 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	want := []string{"high-early", "high-late", "low-early", "low-late"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Type != w {
			t.Errorf("pos %d = %s, want %s", i, got[i].Type, w)
		}
	}
}

func TestListPending_Limit(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	}
	got, err := r.ListPending(ctx, "inst-A", time.Now(), 3)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestRecordConsumption_DuplicateFails(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	pending, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)

	c := &repo.MessageConsumer{MessageID: pending[0].ID, InstanceID: "inst-A",
		ConsumedAt: time.Now(), Status: repo.ConsumeStatusSuccess}
	if err := r.RecordConsumption(ctx, c); err != nil {
		t.Fatalf("first RecordConsumption: %v", err)
	}
	if err := r.RecordConsumption(ctx, c); err == nil {
		t.Error("second RecordConsumption with same (message_id, instance_id) should fail")
	}
}

func TestListConsumers(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	pending, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	id := pending[0].ID

	r.RecordConsumption(ctx, &repo.MessageConsumer{MessageID: id, InstanceID: "inst-B",
		ConsumedAt: time.Now(), Status: repo.ConsumeStatusFailed, ErrorMessage: "boom"})
	r.RecordConsumption(ctx, &repo.MessageConsumer{MessageID: id, InstanceID: "inst-A",
		ConsumedAt: time.Now(), Status: repo.ConsumeStatusSuccess})

	got, err := r.ListConsumers(ctx, id)
	if err != nil {
		t.Fatalf("ListConsumers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].InstanceID != "inst-A" || got[0].Status != repo.ConsumeStatusSuccess || got[0].ErrorMessage != "" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].InstanceID != "inst-B" || got[1].Status != repo.ConsumeStatusFailed || got[1].ErrorMessage != "boom" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestDeleteBefore(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	old := newMsg("old", "", 5, time.Hour)
	old.CreatedAt = time.Now().Add(-40 * 24 * time.Hour)
	mustInsert(t, r, old)
	mustInsert(t, r, newMsg("fresh", "", 5, time.Hour))

	all, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	if len(all) != 2 {
		t.Fatalf("setup: expected 2 messages, got %d", len(all))
	}
	for _, m := range all {
		r.RecordConsumption(ctx, &repo.MessageConsumer{MessageID: m.ID, InstanceID: "inst-A",
			ConsumedAt: time.Now(), Status: repo.ConsumeStatusSuccess})
	}

	dm, dc, err := r.DeleteBefore(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if dm != 1 || dc != 1 {
		t.Errorf("deleted messages=%d consumers=%d, want 1/1", dm, dc)
	}

	remaining, _ := r.ListPending(ctx, "inst-B", time.Now(), 10)
	if len(remaining) != 1 || remaining[0].Type != "fresh" {
		t.Errorf("expected only fresh remaining, got %+v", remaining)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/repo/messagedb/ -v`
Expected: 编译失败，`undefined: New`

- [ ] **Step 3: 写实现**

```go
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
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/repo/messagedb/ -v -count=1`
Expected: 全部 PASS（9 个测试）

---

## Task 4: repofactory 装配

**Files:**
- Modify: `internal/repo/repofactory/factory.go`
- Modify: `internal/repo/repofactory/factory_test.go`

- [ ] **Step 1: 写失败测试**

在 `factory_test.go` 末尾追加：

```go
func TestNewRepos_MessageRepoWired(t *testing.T) {
	homeDir := t.TempDir()
	sqlxDB, dialect, err := db.Open(nil, homeDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlxDB.Close()

	repos := NewRepos(sqlxDB, dialect, homeDir)
	if repos.Message == nil {
		t.Fatal("Message repo 不应为 nil")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/repo/repofactory/ -run TestNewRepos_MessageRepoWired -v`
Expected: 编译失败，`repos.Message undefined`

- [ ] **Step 3: 修改 factory.go**

import 块增加：

```go
	"github.com/zfd81/groot/internal/repo/messagedb"
```

`Repos` 结构体在 `APIKey   repo.APIKeyRepo` 之后增加：

```go
	Message  repo.MessageRepo
```

`NewRepos` 返回值中 `APIKey:   apikeydb.New(sqlxDB, dialect),` 之后增加：

```go
		Message:  messagedb.New(sqlxDB, dialect),
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/repo/repofactory/ -v -count=1`
Expected: 全部 PASS

---

## Task 5: cluster 消息类型、处理器接口与 SendMessage

**Files:**
- Create: `internal/cluster/message_handler.go`
- Create: `internal/cluster/messaging.go`
- Create: `internal/cluster/message_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/cluster/message_test.go
package cluster

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/messagedb"
)

// newTestDB 建一个临时 SQLite 库。多个 MessageService 共用同一个 *sqlx.DB
// 即可模拟多实例共享数据库。
func newTestDB(t *testing.T) (*sqlx.DB, db.Dialect) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return sqlxDB, dialect
}

func newTestMessageService(t *testing.T, sqlxDB *sqlx.DB, dialect db.Dialect, instanceID string) *MessageService {
	t.Helper()
	return NewMessageService(messagedb.New(sqlxDB, dialect), newTestLogger(),
		func() string { return instanceID })
}

// flakyMessageRepo 让前 failures 次 Insert 返回错误，用于验证 SendMessage 的重试。
type flakyMessageRepo struct {
	repo.MessageRepo
	failures int32
	calls    int32
}

func (r *flakyMessageRepo) Insert(ctx context.Context, m *repo.ClusterMessage) error {
	atomic.AddInt32(&r.calls, 1)
	if atomic.AddInt32(&r.failures, -1) >= 0 {
		return errors.New("db down")
	}
	return r.MessageRepo.Insert(ctx, m)
}

func validMessage() Message {
	return Message{
		Type:         "sync_resource",
		TargetModule: "resource_sync",
		Payload:      map[string]any{"version": "v1.2.3"},
	}
}

// ---------- SendMessage ----------

func TestSendMessage_PersistsWithDefaults(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	before := time.Now()

	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	pending, err := ms.repo.ListPending(context.Background(), "inst-B", time.Now(), 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("len = %d, want 1", len(pending))
	}
	m := pending[0]
	if m.Type != "sync_resource" || m.TargetModule != "resource_sync" || m.TargetInstance != "" {
		t.Errorf("routing fields mismatch: %+v", m)
	}
	if m.SourceInstance != "inst-A" {
		t.Errorf("source = %q, want inst-A", m.SourceInstance)
	}
	if m.Priority != defaultPriority {
		t.Errorf("priority = %d, want default %d", m.Priority, defaultPriority)
	}
	if m.Payload != `{"version":"v1.2.3"}` {
		t.Errorf("payload = %s", m.Payload)
	}
	ttl := m.ExpiresAt.Sub(before)
	if ttl < defaultMessageTTL-time.Minute || ttl > defaultMessageTTL+time.Minute {
		t.Errorf("default TTL not applied: expires in %v", ttl)
	}
}

func TestSendMessage_NilPayloadStoredAsEmptyObject(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	msg := validMessage()
	msg.Payload = nil
	if err := ms.SendMessage(context.Background(), msg); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	pending, _ := ms.repo.ListPending(context.Background(), "inst-B", time.Now(), 10)
	if len(pending) != 1 || pending[0].Payload != "{}" {
		t.Errorf("payload = %q, want {}", pending[0].Payload)
	}
}

func TestSendMessage_Validation(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	ctx := context.Background()

	noType := validMessage()
	noType.Type = ""
	if err := ms.SendMessage(ctx, noType); !errors.Is(err, ErrMessageTypeRequired) {
		t.Errorf("empty type: err = %v, want ErrMessageTypeRequired", err)
	}

	noModule := validMessage()
	noModule.TargetModule = ""
	if err := ms.SendMessage(ctx, noModule); !errors.Is(err, ErrTargetModuleRequired) {
		t.Errorf("empty module: err = %v, want ErrTargetModuleRequired", err)
	}

	past := validMessage()
	past.ExpiresAt = time.Now().Add(-time.Second)
	if err := ms.SendMessage(ctx, past); !errors.Is(err, ErrExpiresInPast) {
		t.Errorf("past expires: err = %v, want ErrExpiresInPast", err)
	}

	big := validMessage()
	big.Payload = map[string]any{"blob": strings.Repeat("x", maxPayloadBytes+1)}
	if err := ms.SendMessage(ctx, big); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("oversized payload: err = %v, want ErrPayloadTooLarge", err)
	}

	pending, _ := ms.repo.ListPending(ctx, "inst-B", time.Now(), 10)
	if len(pending) != 0 {
		t.Errorf("invalid messages must not be persisted, got %d", len(pending))
	}
}

func TestSendMessage_RetriesOnceThenSucceeds(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	flaky := &flakyMessageRepo{MessageRepo: messagedb.New(sqlxDB, dialect), failures: 1}
	ms := NewMessageService(flaky, newTestLogger(), func() string { return "inst-A" })

	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("expected success after one retry, got %v", err)
	}
	if atomic.LoadInt32(&flaky.calls) != 2 {
		t.Errorf("Insert calls = %d, want 2", flaky.calls)
	}
}

func TestSendMessage_FailsAfterSecondError(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	flaky := &flakyMessageRepo{MessageRepo: messagedb.New(sqlxDB, dialect), failures: 2}
	ms := NewMessageService(flaky, newTestLogger(), func() string { return "inst-A" })

	err := ms.SendMessage(context.Background(), validMessage())
	if err == nil {
		t.Fatal("expected error after two failed inserts")
	}
	if !strings.Contains(err.Error(), "db down") {
		t.Errorf("error should wrap underlying cause, got %v", err)
	}
	if atomic.LoadInt32(&flaky.calls) != 2 {
		t.Errorf("Insert calls = %d, want exactly 2 (no third attempt)", flaky.calls)
	}
}

// ---------- RegisterHandler ----------

func TestRegisterHandler_LookupAndOverwrite(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")

	if ms.handler("mod") != nil {
		t.Fatal("unregistered module should return nil handler")
	}
	first := HandlerFunc(func(ctx context.Context, msg Message) error { return errors.New("first") })
	second := HandlerFunc(func(ctx context.Context, msg Message) error { return errors.New("second") })
	ms.RegisterHandler("mod", first)
	ms.RegisterHandler("mod", second)

	err := ms.handler("mod").Handle(context.Background(), Message{})
	if err == nil || err.Error() != "second" {
		t.Errorf("later registration should win, got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/cluster/ -run 'TestSendMessage|TestRegisterHandler' -v`
Expected: 编译失败，`undefined: NewMessageService` 等

- [ ] **Step 3: 写 message_handler.go**

```go
// internal/cluster/message_handler.go
package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// Message 是集群消息在 Go 侧的表示。
// TargetInstance 为空表示广播；非空表示只投递给该 reg_id 的实例。
// TargetModule 决定消息被路由到哪个已注册的 MessageHandler。
type Message struct {
	ID             int64
	Type           string
	TargetInstance string
	TargetModule   string
	Payload        map[string]any
	Priority       int
	CreatedAt      time.Time
	ExpiresAt      time.Time
	SourceInstance string
}

// MessageHandler 是模块处理集群消息的抽象接口。
// 返回非 nil error 表示处理失败，结果会记录到消费记录表且不会重试。
// 处理逻辑必须是幂等的：消息保证至少送达一次，极端情况下可能重复。
type MessageHandler interface {
	Handle(ctx context.Context, msg Message) error
}

// HandlerFunc 让普通函数满足 MessageHandler 接口。
type HandlerFunc func(ctx context.Context, msg Message) error

// Handle 实现 MessageHandler。
func (f HandlerFunc) Handle(ctx context.Context, msg Message) error { return f(ctx, msg) }

// toRepoMessage 把 Message 转为持久化结构；Payload 序列化为 JSON，nil 视为 {}。
func toRepoMessage(m Message) (*repo.ClusterMessage, error) {
	payload := m.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化 payload 失败: %w", err)
	}
	return &repo.ClusterMessage{
		ID:             m.ID,
		Type:           m.Type,
		Payload:        string(raw),
		TargetInstance: m.TargetInstance,
		TargetModule:   m.TargetModule,
		Priority:       m.Priority,
		CreatedAt:      m.CreatedAt,
		ExpiresAt:      m.ExpiresAt,
		SourceInstance: m.SourceInstance,
	}, nil
}

// fromRepoMessage 把持久化结构转回 Message；payload 非法 JSON 时返回错误。
func fromRepoMessage(rm *repo.ClusterMessage) (Message, error) {
	var payload map[string]any
	if rm.Payload != "" {
		if err := json.Unmarshal([]byte(rm.Payload), &payload); err != nil {
			return Message{}, fmt.Errorf("解析 payload 失败: %w", err)
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return Message{
		ID:             rm.ID,
		Type:           rm.Type,
		TargetInstance: rm.TargetInstance,
		TargetModule:   rm.TargetModule,
		Payload:        payload,
		Priority:       rm.Priority,
		CreatedAt:      rm.CreatedAt,
		ExpiresAt:      rm.ExpiresAt,
		SourceInstance: rm.SourceInstance,
	}, nil
}
```

- [ ] **Step 4: 写 messaging.go**

```go
// internal/cluster/messaging.go
package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

const (
	defaultPriority   = 5                   // 未指定时的优先级
	defaultMessageTTL = time.Hour           // 未指定 ExpiresAt 时的存活时长
	maxPayloadBytes   = 64 * 1024           // payload JSON 序列化后的最大字节数
	pollBatchSize     = 10                  // 单次轮询最多处理的消息数
	handlerTimeout    = 30 * time.Second    // 单条消息处理器的超时
	messageRetention  = 30 * 24 * time.Hour // 消息保留期，超过即被 Leader 清理
)

var (
	ErrMessageTypeRequired  = errors.New("集群消息: message type 不能为空")
	ErrTargetModuleRequired = errors.New("集群消息: target module 不能为空")
	ErrExpiresInPast        = errors.New("集群消息: expires_at 必须晚于当前时间")
	ErrPayloadTooLarge      = fmt.Errorf("集群消息: payload 超过 %d 字节", maxPayloadBytes)
)

// MessageService 负责集群消息的发送、处理器注册、轮询处理和清理。
// 它是纯内部 API：只由 Groot 代码调用，不暴露 HTTP 端点。
type MessageService struct {
	repo   repo.MessageRepo
	log    *logger.Logger
	selfID func() string // 返回当前实例的 reg_id；未注册时返回空串

	mu       sync.RWMutex
	handlers map[string]MessageHandler
}

// NewMessageService 创建消息服务。selfID 通常传 Cluster.RegID。
func NewMessageService(msgRepo repo.MessageRepo, log *logger.Logger, selfID func() string) *MessageService {
	return &MessageService{
		repo:     msgRepo,
		log:      log,
		selfID:   selfID,
		handlers: make(map[string]MessageHandler),
	}
}

// RegisterHandler 注册模块处理器；同名模块后注册者覆盖先注册者。
func (s *MessageService) RegisterHandler(module string, h MessageHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[module] = h
}

func (s *MessageService) handler(module string) MessageHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handlers[module]
}

// SendMessage 校验并写入一条消息。数据库写入失败时立即重试一次，
// 第二次仍失败则返回错误给调用方，由调用方决定如何处理。
func (s *MessageService) SendMessage(ctx context.Context, msg Message) error {
	now := time.Now()
	if msg.Type == "" {
		return ErrMessageTypeRequired
	}
	if msg.TargetModule == "" {
		return ErrTargetModuleRequired
	}
	if msg.Priority == 0 {
		msg.Priority = defaultPriority
	}
	if msg.ExpiresAt.IsZero() {
		msg.ExpiresAt = now.Add(defaultMessageTTL)
	}
	if !msg.ExpiresAt.After(now) {
		return ErrExpiresInPast
	}
	msg.CreatedAt = now
	msg.SourceInstance = s.selfID()

	rm, err := toRepoMessage(msg)
	if err != nil {
		return err
	}
	if len(rm.Payload) > maxPayloadBytes {
		return ErrPayloadTooLarge
	}

	if err := s.repo.Insert(ctx, rm); err != nil {
		s.log.Warn("集群消息写入失败,立即重试",
			zap.String("type", msg.Type), zap.Error(err))
		if err = s.repo.Insert(ctx, rm); err != nil {
			return fmt.Errorf("集群消息发送失败: %w", err)
		}
	}

	s.log.Info("集群消息已发送",
		zap.String("type", msg.Type),
		zap.String("target", msg.TargetInstance),
		zap.String("module", msg.TargetModule),
		zap.Int("priority", msg.Priority),
	)
	return nil
}

// Cleanup 删除超过保留期（30 天）的消息及其孤立消费记录。只应由 Leader 调用。
func (s *MessageService) Cleanup(ctx context.Context) (deletedMessages, deletedConsumers int, err error) {
	return s.repo.DeleteBefore(ctx, time.Now().Add(-messageRetention))
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/cluster/ -run 'TestSendMessage|TestRegisterHandler' -v -count=1`
Expected: 全部 PASS（6 个测试）

---

## Task 6: 轮询与处理

**Files:**
- Create: `internal/cluster/message_poller.go`
- Modify: `internal/cluster/message_test.go`（追加测试）

- [ ] **Step 1: 写失败测试**

在 `internal/cluster/message_test.go` 末尾追加：

```go
// ---------- Poll / processMessage ----------

// recorder 记录处理器收到的消息。
type recorder struct {
	mu   sync.Mutex
	got  []Message
	fail error
}

func (r *recorder) Handle(ctx context.Context, msg Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, msg)
	return r.fail
}

func (r *recorder) messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.got...)
}

func consumersOf(t *testing.T, ms *MessageService, instanceID string) []*repo.MessageConsumer {
	t.Helper()
	// 找到库里所有消息（用一个从未消费过的假实例视角）
	all, err := ms.repo.ListPending(context.Background(), "__probe__", time.Now(), 100)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	var out []*repo.MessageConsumer
	for _, m := range all {
		cs, err := ms.repo.ListConsumers(context.Background(), m.ID)
		if err != nil {
			t.Fatalf("ListConsumers: %v", err)
		}
		for _, c := range cs {
			if c.InstanceID == instanceID {
				out = append(out, c)
			}
		}
	}
	return out
}

func TestPoll_DeliversToHandlerAndRecordsSuccess(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	rec := &recorder{}
	ms.RegisterHandler("resource_sync", rec)

	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	ms.Poll(context.Background())

	got := rec.messages()
	if len(got) != 1 {
		t.Fatalf("handler called %d times, want 1", len(got))
	}
	if got[0].Type != "sync_resource" || got[0].Payload["version"] != "v1.2.3" || got[0].ID == 0 {
		t.Errorf("handler received %+v", got[0])
	}
	cs := consumersOf(t, ms, "inst-A")
	if len(cs) != 1 || cs[0].Status != repo.ConsumeStatusSuccess || cs[0].ErrorMessage != "" {
		t.Errorf("consumer records = %+v", cs)
	}
}

func TestPoll_DoesNotRedeliverConsumedMessage(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	rec := &recorder{}
	ms.RegisterHandler("resource_sync", rec)
	ms.SendMessage(context.Background(), validMessage())

	ms.Poll(context.Background())
	ms.Poll(context.Background())
	ms.Poll(context.Background())

	if n := len(rec.messages()); n != 1 {
		t.Errorf("handler called %d times across 3 polls, want 1", n)
	}
}

func TestPoll_HandlerErrorRecordedAsFailedNoRetry(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	rec := &recorder{fail: errors.New("sync exploded")}
	ms.RegisterHandler("resource_sync", rec)
	ms.SendMessage(context.Background(), validMessage())

	ms.Poll(context.Background())
	ms.Poll(context.Background())

	if n := len(rec.messages()); n != 1 {
		t.Errorf("failed message must not be retried, handler called %d times", n)
	}
	cs := consumersOf(t, ms, "inst-A")
	if len(cs) != 1 || cs[0].Status != repo.ConsumeStatusFailed || cs[0].ErrorMessage != "sync exploded" {
		t.Errorf("consumer records = %+v", cs)
	}
}

func TestPoll_HandlerPanicRecordedAsFailed(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	ms.RegisterHandler("resource_sync", HandlerFunc(func(ctx context.Context, msg Message) error {
		panic("boom")
	}))
	ms.SendMessage(context.Background(), validMessage())

	ms.Poll(context.Background()) // 不能让 panic 逃出

	cs := consumersOf(t, ms, "inst-A")
	if len(cs) != 1 || cs[0].Status != repo.ConsumeStatusFailed || !strings.Contains(cs[0].ErrorMessage, "panic") {
		t.Errorf("consumer records = %+v", cs)
	}
}

func TestPoll_UnknownModuleRecordedAsFailed(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	// 不注册任何处理器
	ms.SendMessage(context.Background(), validMessage())

	ms.Poll(context.Background())

	cs := consumersOf(t, ms, "inst-A")
	if len(cs) != 1 || cs[0].Status != repo.ConsumeStatusFailed || !strings.Contains(cs[0].ErrorMessage, "resource_sync") {
		t.Errorf("consumer records = %+v", cs)
	}
}

func TestPoll_BroadcastReachesEveryInstance(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	a := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	b := newTestMessageService(t, sqlxDB, dialect, "inst-B")
	recA, recB := &recorder{}, &recorder{}
	a.RegisterHandler("resource_sync", recA)
	b.RegisterHandler("resource_sync", recB)

	a.SendMessage(context.Background(), validMessage()) // 广播
	a.Poll(context.Background())
	b.Poll(context.Background())

	if len(recA.messages()) != 1 || len(recB.messages()) != 1 {
		t.Errorf("broadcast: A got %d, B got %d, want 1/1", len(recA.messages()), len(recB.messages()))
	}
	if recB.messages()[0].SourceInstance != "inst-A" {
		t.Errorf("source = %q, want inst-A", recB.messages()[0].SourceInstance)
	}
}

func TestPoll_PointToPointReachesOnlyTarget(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	a := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	b := newTestMessageService(t, sqlxDB, dialect, "inst-B")
	recA, recB := &recorder{}, &recorder{}
	a.RegisterHandler("restart", recA)
	b.RegisterHandler("restart", recB)

	msg := Message{Type: "restart", TargetModule: "restart", TargetInstance: "inst-B", Priority: 1}
	a.SendMessage(context.Background(), msg)
	a.Poll(context.Background())
	b.Poll(context.Background())

	if len(recA.messages()) != 0 || len(recB.messages()) != 1 {
		t.Errorf("point-to-point: A got %d, B got %d, want 0/1", len(recA.messages()), len(recB.messages()))
	}
}

func TestPoll_SkipsWhenInstanceIDEmpty(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	sender := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	sender.SendMessage(context.Background(), validMessage())

	unregistered := newTestMessageService(t, sqlxDB, dialect, "")
	rec := &recorder{}
	unregistered.RegisterHandler("resource_sync", rec)
	unregistered.Poll(context.Background())

	if len(rec.messages()) != 0 {
		t.Errorf("instance without reg_id must not consume, got %d", len(rec.messages()))
	}
}

func TestPoll_RespectsPriorityOrder(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	rec := &recorder{}
	ms.RegisterHandler("m", rec)

	ms.SendMessage(context.Background(), Message{Type: "low", TargetModule: "m", Priority: 9})
	ms.SendMessage(context.Background(), Message{Type: "high", TargetModule: "m", Priority: 1})
	ms.Poll(context.Background())

	got := rec.messages()
	if len(got) != 2 || got[0].Type != "high" || got[1].Type != "low" {
		t.Errorf("order = %v", []string{got[0].Type, got[1].Type})
	}
}

// ---------- Cleanup ----------

func TestCleanup_RemovesMessagesOlderThan30Days(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	ctx := context.Background()

	old := &repo.ClusterMessage{Type: "old", Payload: "{}", TargetModule: "m", Priority: 5,
		CreatedAt: time.Now().Add(-31 * 24 * time.Hour), ExpiresAt: time.Now().Add(time.Hour)}
	if err := ms.repo.Insert(ctx, old); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	ms.SendMessage(ctx, Message{Type: "fresh", TargetModule: "m"})

	dm, dc, err := ms.Cleanup(ctx)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if dm != 1 || dc != 0 {
		t.Errorf("deleted messages=%d consumers=%d, want 1/0", dm, dc)
	}
	remaining, _ := ms.repo.ListPending(ctx, "__probe__", time.Now(), 10)
	if len(remaining) != 1 || remaining[0].Type != "fresh" {
		t.Errorf("remaining = %+v", remaining)
	}
}
```

同时在文件顶部 import 块增加 `"sync"`（`recorder` 用到）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/cluster/ -run 'TestPoll|TestCleanup' -v`
Expected: 编译失败，`ms.Poll undefined`

- [ ] **Step 3: 写 message_poller.go**

```go
// internal/cluster/message_poller.go
package cluster

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/repo"
)

// Poll 拉取本实例尚未消费的消息并逐条处理。同步执行；调用方负责并发控制。
func (s *MessageService) Poll(ctx context.Context) {
	instanceID := s.selfID()
	if instanceID == "" {
		s.log.Debug("实例尚未完成集群注册,跳过集群消息轮询")
		return
	}
	msgs, err := s.repo.ListPending(ctx, instanceID, time.Now(), pollBatchSize)
	if err != nil {
		s.log.Warn("集群消息轮询失败", zap.Error(err))
		return
	}
	if len(msgs) == 0 {
		return
	}
	s.log.Debug("集群消息轮询", zap.Int("pending_count", len(msgs)))
	for _, rm := range msgs {
		if ctx.Err() != nil {
			return
		}
		s.processMessage(ctx, instanceID, rm)
	}
}

// processMessage 路由并执行一条消息，然后把结果写入消费记录表。
// 处理失败不重试：记录 failed 状态与错误信息后继续下一条。
func (s *MessageService) processMessage(ctx context.Context, instanceID string, rm *repo.ClusterMessage) {
	msg, err := fromRepoMessage(rm)
	if err != nil {
		s.recordResult(ctx, instanceID, rm, err)
		return
	}
	h := s.handler(msg.TargetModule)
	if h == nil {
		s.recordResult(ctx, instanceID, rm, fmt.Errorf("模块 %q 未注册处理器", msg.TargetModule))
		return
	}
	hctx, cancel := context.WithTimeout(ctx, handlerTimeout)
	err = safeHandle(hctx, h, msg)
	cancel()
	s.recordResult(ctx, instanceID, rm, err)
}

// safeHandle 调用处理器并把 panic 转为 error，保证轮询循环不会被单个处理器击穿。
func safeHandle(ctx context.Context, h MessageHandler, msg Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("处理器 panic: %v", r)
		}
	}()
	return h.Handle(ctx, msg)
}

// recordResult 写入消费记录并打日志。写记录失败只打日志，消息会在下一轮再次被处理（至少一次语义）。
func (s *MessageService) recordResult(ctx context.Context, instanceID string, rm *repo.ClusterMessage, handleErr error) {
	c := &repo.MessageConsumer{
		MessageID:  rm.ID,
		InstanceID: instanceID,
		ConsumedAt: time.Now(),
		Status:     repo.ConsumeStatusSuccess,
	}
	if handleErr != nil {
		c.Status = repo.ConsumeStatusFailed
		c.ErrorMessage = handleErr.Error()
		s.log.Error("集群消息处理失败",
			zap.Int64("msg_id", rm.ID),
			zap.String("type", rm.Type),
			zap.String("module", rm.TargetModule),
			zap.Error(handleErr))
	} else {
		s.log.Info("集群消息处理成功",
			zap.Int64("msg_id", rm.ID),
			zap.String("type", rm.Type),
			zap.String("module", rm.TargetModule))
	}
	if err := s.repo.RecordConsumption(ctx, c); err != nil {
		s.log.Error("写入集群消息消费记录失败",
			zap.Int64("msg_id", rm.ID), zap.Error(err))
	}
}

// SetMessageService 把消息服务挂接到集群心跳循环。必须在 Join 之前调用。
func (c *Cluster) SetMessageService(ms *MessageService) {
	c.msg = ms
}

// MessageService 返回挂接的消息服务；未挂接时返回 nil。
func (c *Cluster) MessageService() *MessageService {
	return c.msg
}

// triggerPoll 在心跳 tick 中异步触发一轮消息轮询。
// 原子标志保证同一时刻只有一轮在跑：上一轮未结束时本轮直接跳过，
// 处理器耗时再长也不会阻塞心跳、不会并发重复处理。
func (c *Cluster) triggerPoll() {
	if c.msg == nil {
		return
	}
	if !atomic.CompareAndSwapInt32(&c.polling, 0, 1) {
		c.log.Debug("上一轮集群消息处理尚未结束,跳过本轮轮询")
		return
	}
	go func() {
		defer atomic.StoreInt32(&c.polling, 0)
		c.msg.Poll(c.ctx)
	}()
}
```

- [ ] **Step 4: 在 cluster.go 增加字段**

`Cluster` 结构体中，`onLoseLeader   func()` 之后增加：

```go
	msg     *MessageService // 挂接的集群消息服务，可为 nil
	polling int32           // 1 表示有一轮消息轮询正在进行
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/cluster/ -run 'TestPoll|TestCleanup' -v -count=1`
Expected: 全部 PASS（10 个测试）

---

## Task 7: 清理任务包装

**Files:**
- Create: `internal/cluster/message_cleanup.go`
- Modify: `internal/cluster/message_test.go`（追加测试）

- [ ] **Step 1: 写失败测试**

在 `internal/cluster/message_test.go` 末尾追加：

```go
// ---------- NewMessageCleanupTask ----------

func TestNewMessageCleanupTask_RunsCleanup(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	ctx := context.Background()

	old := &repo.ClusterMessage{Type: "old", Payload: "{}", TargetModule: "m", Priority: 5,
		CreatedAt: time.Now().Add(-40 * 24 * time.Hour), ExpiresAt: time.Now().Add(time.Hour)}
	if err := ms.repo.Insert(ctx, old); err != nil {
		t.Fatalf("insert old: %v", err)
	}

	task := NewMessageCleanupTask(ms, newTestLogger())
	task()

	remaining, _ := ms.repo.ListPending(ctx, "__probe__", time.Now(), 10)
	if len(remaining) != 0 {
		t.Errorf("cleanup task should have removed old message, %d remain", len(remaining))
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/cluster/ -run TestNewMessageCleanupTask -v`
Expected: 编译失败，`undefined: NewMessageCleanupTask`

- [ ] **Step 3: 写实现**

```go
// internal/cluster/message_cleanup.go
package cluster

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// NewMessageCleanupTask 返回可交给 gocron 的清理函数：删除保留期（30 天）之前的
// 集群消息及其孤立消费记录。只应注册在 Leader 的调度器上。
func NewMessageCleanupTask(ms *MessageService, log *logger.Logger) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		dm, dc, err := ms.Cleanup(ctx)
		if err != nil {
			log.Error("集群消息清理失败", zap.Error(err))
			return
		}
		log.Info("集群消息清理完成",
			zap.Int("deleted_messages", dm),
			zap.Int("deleted_consumers", dc))
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/cluster/ -run TestNewMessageCleanupTask -v -count=1`
Expected: PASS

---

## Task 8: Cluster 心跳 tick 集成

**Files:**
- Modify: `internal/cluster/cluster.go`（`run()` 方法）
- Modify: `internal/cluster/message_test.go`（追加测试）

- [ ] **Step 1: 写失败测试**

在 `internal/cluster/message_test.go` 末尾追加：

```go
// ---------- Cluster 集成 ----------

func TestCluster_TriggerPoll_DeliversMessage(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	memberRepo := memberdb.New(sqlxDB, dialect)

	c := New("127.0.0.1", 8080, newTestLogger(), memberRepo)
	ms := NewMessageService(messagedb.New(sqlxDB, dialect), newTestLogger(), c.RegID)
	c.SetMessageService(ms)
	c.ctx, c.cancel = context.WithCancel(context.Background())
	t.Cleanup(c.Leave)
	c.register()

	done := make(chan Message, 1)
	ms.RegisterHandler("resource_sync", HandlerFunc(func(ctx context.Context, msg Message) error {
		done <- msg
		return nil
	}))
	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	c.triggerPoll()

	select {
	case msg := <-done:
		if msg.SourceInstance != c.RegID() {
			t.Errorf("source = %q, want own reg_id %q", msg.SourceInstance, c.RegID())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler not invoked after triggerPoll")
	}
}

func TestCluster_TriggerPoll_SkipsWhilePreviousRoundInProgress(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	memberRepo := memberdb.New(sqlxDB, dialect)

	c := New("127.0.0.1", 8080, newTestLogger(), memberRepo)
	ms := NewMessageService(messagedb.New(sqlxDB, dialect), newTestLogger(), c.RegID)
	c.SetMessageService(ms)
	c.ctx, c.cancel = context.WithCancel(context.Background())
	t.Cleanup(c.Leave)
	c.register()

	release := make(chan struct{})
	var calls int32
	ms.RegisterHandler("resource_sync", HandlerFunc(func(ctx context.Context, msg Message) error {
		atomic.AddInt32(&calls, 1)
		<-release
		return nil
	}))
	ms.SendMessage(context.Background(), validMessage())

	c.triggerPoll() // 第一轮：处理器阻塞在 release 上
	time.Sleep(100 * time.Millisecond)
	c.triggerPoll() // 第二轮：应因 polling=1 被跳过
	time.Sleep(100 * time.Millisecond)
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("handler called %d times while first round in progress, want 1", n)
	}
	close(release)
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&c.polling) != 0 {
		t.Error("polling flag should reset after round completes")
	}
}

func TestCluster_TriggerPoll_NoServiceIsNoop(t *testing.T) {
	c := newManualCluster(t, 8080, newTestRepo(t))
	c.triggerPoll() // 不应 panic
	if atomic.LoadInt32(&c.polling) != 0 {
		t.Error("polling flag must stay 0 without message service")
	}
}
```

同时在 import 块增加 `"github.com/zfd81/groot/internal/repo/memberdb"`。

- [ ] **Step 2: 运行测试确认通过（此时已可通过，验证不依赖 run()）**

Run: `go test ./internal/cluster/ -run TestCluster_TriggerPoll -v -count=1`
Expected: 全部 PASS（3 个测试）。这些测试直接调用 `triggerPoll`，用于锁定其行为；下一步把它接入 tick。

- [ ] **Step 3: 修改 cluster.go 的 run()**

把

```go
		case <-ticker.C:
			c.heartbeat()
```

改为

```go
		case <-ticker.C:
			c.heartbeat()
			// 与心跳同一 tick 触发消息轮询，避免多一个定时器增加数据库并发。
			// 放在 heartbeat() 之后而非其内部：heartbeat 全程持有 c.mu 写锁，
			// 处理器若在锁内运行会与 IsLeader() 等读锁死锁并拖慢心跳。
			c.triggerPoll()
```

- [ ] **Step 4: 运行 cluster 包全部测试**

Run: `go test ./internal/cluster/ -v -count=1`
Expected: 全部 PASS（含原有心跳/选举测试，约 10 秒）

---

## Task 9: main.go 接线

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 在 startLeaderTasks 定义之前声明消息服务变量**

找到注释 `// Declare schedule module variables (used by leader callbacks and API server)` 那一段，在 `var scheduleRunner *schedule.Runner` 之后增加：

```go
	// 集群消息服务：在 clusterInst 创建后赋值，startLeaderTasks 闭包中引用
	var clusterMsg *cluster.MessageService
```

- [ ] **Step 2: 在 startLeaderTasks 中注册每日清理任务**

在 `startLeaderTasks` 闭包内，`// Register sync task` 段落之后、`// Register schedule tools if enabled` 之前插入：

```go
		// Register cluster message cleanup (daily 03:00, retention 30 days, leader only)
		if clusterMsg != nil {
			sched.AddDaily(3, 0, gocron.NewTask(cluster.NewMessageCleanupTask(clusterMsg, log)),
				"system-cluster-message-cleanup", "cleanup")
		}
```

- [ ] **Step 3: 创建消息服务并挂接到 Cluster**

把

```go
	clusterInst := cluster.New(cluster.ResolveAdvertiseHost(cfg.Server.Host), cfg.Server.Port, log, repos.Member)
	clusterInst.SetCallbacks(startLeaderTasks, stopLeaderTasks)

	if err := clusterInst.Join(context.Background()); err != nil {
```

改为

```go
	clusterInst := cluster.New(cluster.ResolveAdvertiseHost(cfg.Server.Host), cfg.Server.Port, log, repos.Member)
	clusterInst.SetCallbacks(startLeaderTasks, stopLeaderTasks)

	// 集群消息服务必须在 Join 之前挂接：Join 会同步触发 register → onBecomeLeader，
	// 且心跳 goroutine 启动后不再允许修改 Cluster 的挂接字段。
	clusterMsg = cluster.NewMessageService(repos.Message, log, clusterInst.RegID)
	clusterInst.SetMessageService(clusterMsg)
	// 具体业务处理器在有场景时通过 clusterMsg.RegisterHandler(module, handler) 注册。

	if err := clusterInst.Join(context.Background()); err != nil {
```

- [ ] **Step 4: 编译**

Run: `cd /Users/zhangfengda/workspace/groot && go build -o dist/groot ./cmd/groot && echo BUILD_OK`
Expected: 输出 `BUILD_OK`（若出现 `error obtaining VCS status` 提示，加 `-buildvcs=false` 重跑）

- [ ] **Step 5: 冒烟启动验证日志**

Run:

```bash
cd /Users/zhangfengda/workspace/groot
export GROOT_HOME=$(mktemp -d)
./dist/groot -p 18080 > "$GROOT_HOME/smoke.log" 2>&1 &
PID=$!
sleep 6
kill $PID
grep -E "集群注册完成|统一调度器已启动|无法初始化数据库|panic" "$GROOT_HOME/smoke.log"
rm -rf "$GROOT_HOME"
```

Expected: 输出包含 `集群注册完成` 与 `统一调度器已启动`；不包含 `panic` 或 `无法初始化数据库`。如果实例因缺少 LLM 配置而提前退出，只要 `集群注册完成` 出现即可视为通过（说明建表与挂接成功）。

---

## Task 10: 文档更新

**Files:**
- Modify: `docs/superpowers/specs/2026-09-18-cluster-messaging-design.md`
- Modify: `tests/TEST_CASES.md`

- [ ] **Step 1: 更新设计文档"六、代码组织"章节**

把第六章整段替换为：

```markdown
## 六、代码组织

仓储层沿用项目惯例（接口在 `internal/repo/`，实现在 `internal/repo/*db/`），集群包只依赖接口：

```
internal/repo/
├── message.go                # ClusterMessage / MessageConsumer 类型、MessageRepo 接口
└── messagedb/
    ├── message.go            # MessageRepo 的 sqlx 实现（SQLite/MySQL/PostgreSQL）
    └── message_test.go

internal/cluster/
├── cluster.go                # 现有集群管理；心跳 tick 中调用 triggerPoll()
├── election.go               # 现有选举逻辑
├── addr.go                   # 现有地址工具
├── message_handler.go        # Message 结构、MessageHandler 接口、HandlerFunc、与 repo 类型互转
├── messaging.go              # MessageService：构造、RegisterHandler、SendMessage（重试一次）、Cleanup
├── message_poller.go         # MessageService.Poll / processMessage；Cluster.SetMessageService / triggerPoll
├── message_cleanup.go        # NewMessageCleanupTask（gocron 每日任务包装）
└── message_test.go           # 单元测试
```

**心跳整合方式：** `Cluster.run()` 在每个 3 秒 tick 中先执行 `heartbeat()`，再调用 `triggerPoll()`。`triggerPoll` 用原子标志保证同一时刻只有一轮 `Poll` 在独立 goroutine 中运行；处理器不在心跳写锁内执行，耗时处理器不会阻塞心跳，上一轮未结束时本轮跳过。

**存储约定：** 时间列统一为 Unix 毫秒 `BIGINT`；`target_instance = ''` 表示广播；`payload` 以 JSON 文本存于 `TEXT`/`LONGTEXT` 列。
```

同时把第二章 2.1 / 2.2 的 DDL 替换为 Task 2 中的 SQLite 版本，并在字段说明中把"`NULL` 表示广播"改为"空串表示广播"。第 5.4 节"实现位置"改为：`internal/cluster/message_cleanup.go` 提供任务函数，`cmd/groot/main.go` 的 `startLeaderTasks` 中以 `sched.AddDaily(3, 0, ...)` 注册。

在文末"十四、迭代说明"下追加：

```markdown
### 2026-09-21 实现对齐

- 调整：仓储层拆分为 `internal/repo/message.go` + `internal/repo/messagedb/`，与项目其他表一致
- 调整：时间列改为 Unix 毫秒 `BIGINT`；广播以 `target_instance = ''` 表示
- 调整：轮询在心跳 tick 中由 `triggerPoll()` 异步触发（原子标志防并发），不在 `heartbeat()` 写锁内执行
- 调整：清理任务由 Leader 的 gocron 调度器每日 03:00 执行（`AddDaily`），不复用不存在的 memory cleanup
- 新增：处理器 panic 被捕获并记录为 failed；单条处理器 30 秒超时
```

- [ ] **Step 2: 在 tests/TEST_CASES.md 的 1.1 集群管理测试小节末尾追加**

```markdown
**集群消息存储** (`internal/repo/messagedb/message_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestInsertAndListPending_Broadcast | 广播消息所有实例可见，字段往返一致 |
| TestListPending_PointToPointOnlyTarget | 点对点消息只有目标实例可见 |
| TestListPending_ExcludesExpired | 过期消息不返回 |
| TestListPending_ExcludesConsumedBySelfOnly | 已消费的只对本实例隐藏，其他实例仍可见 |
| TestListPending_OrderByPriorityThenCreated | 按优先级、创建时间排序 |
| TestListPending_Limit | 单次返回条数受 limit 限制 |
| TestRecordConsumption_DuplicateFails | 同一实例重复记录同一消息报错 |
| TestListConsumers | 列出某条消息的全部消费记录 |
| TestDeleteBefore | 删除过期消息及孤立消费记录并返回计数 |

**集群消息服务** (`internal/cluster/message_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestSendMessage_PersistsWithDefaults | 发送落库，默认优先级 5 / TTL 1h / 来源实例 |
| TestSendMessage_NilPayloadStoredAsEmptyObject | nil payload 存为 `{}` |
| TestSendMessage_Validation | type/module 必填、expires 不能为过去、payload ≤ 64KB |
| TestSendMessage_RetriesOnceThenSucceeds | 首次写入失败立即重试一次成功 |
| TestSendMessage_FailsAfterSecondError | 两次失败后返回错误，不做第三次 |
| TestRegisterHandler_LookupAndOverwrite | 处理器注册与同名覆盖 |
| TestPoll_DeliversToHandlerAndRecordsSuccess | 轮询路由到处理器并记录 success |
| TestPoll_DoesNotRedeliverConsumedMessage | 已消费消息不重复投递 |
| TestPoll_HandlerErrorRecordedAsFailedNoRetry | 处理失败记录 failed 与错误信息，不重试 |
| TestPoll_HandlerPanicRecordedAsFailed | 处理器 panic 被捕获并记录 failed |
| TestPoll_UnknownModuleRecordedAsFailed | 未注册模块记录 failed |
| TestPoll_BroadcastReachesEveryInstance | 广播消息每个实例各处理一次 |
| TestPoll_PointToPointReachesOnlyTarget | 点对点消息只有目标实例处理 |
| TestPoll_SkipsWhenInstanceIDEmpty | 未注册集群的实例不消费 |
| TestPoll_RespectsPriorityOrder | 高优先级先处理 |
| TestCleanup_RemovesMessagesOlderThan30Days | 清理 30 天前消息 |
| TestNewMessageCleanupTask_RunsCleanup | gocron 任务包装可执行清理 |
| TestCluster_TriggerPoll_DeliversMessage | Cluster 挂接后 triggerPoll 投递消息 |
| TestCluster_TriggerPoll_SkipsWhilePreviousRoundInProgress | 上一轮未结束时跳过本轮 |
| TestCluster_TriggerPoll_NoServiceIsNoop | 未挂接消息服务时不报错 |

**数据库迁移** (`internal/db/migrate_test.go`) 追加：

| 测试函数 | 测试内容 |
|---------|---------|
| TestMigrate_CreatesClusterMessageTables | 建 cluster_messages / cluster_message_consumers 两表及索引，重复执行幂等 |
```

README 不需要改动：本功能是纯内部 API，没有面向用户的配置项、命令或 HTTP 端点。

---

## Task 11: 全量验证

- [ ] **Step 1: gofmt**

Run: `cd /Users/zhangfengda/workspace/groot && gofmt -l internal/ cmd/`
Expected: 无输出。若列出文件，执行 `gofmt -w <文件>` 后重跑。

- [ ] **Step 2: go vet**

Run: `go vet ./internal/cluster/ ./internal/repo/... ./internal/db/ ./cmd/...`
Expected: 无输出

- [ ] **Step 3: 相关包全部测试**

Run: `go test ./internal/cluster/ ./internal/repo/... ./internal/db/ -count=1`
Expected: 每个包 `ok`

- [ ] **Step 4: 全量测试**

Run: `go test ./... -count=1 2>&1 | grep -v '^ok\|no test files'`
Expected: 无 FAIL 行

- [ ] **Step 5: 编译到 dist**

Run: `go build -o dist/groot ./cmd/groot && echo BUILD_OK`
Expected: `BUILD_OK`

- [ ] **Step 6: 向用户报告，等待提交指令**

列出改动文件清单和测试结果。**不要执行 git commit**，等用户明确说"提交"。

---

## 附：验收检查

- [ ] 三方言 DDL 都已加入，SQLite 下 Migrate 幂等测试通过
- [ ] `SendMessage` 校验四类错误并返回哨兵 error；写入失败重试恰好一次
- [ ] 广播消息 N 个实例各消费一次；点对点只有目标实例消费
- [ ] 处理失败 / panic / 未注册模块都记录 `failed` 并不重试
- [ ] `triggerPoll` 在心跳 tick 中触发，不在 `heartbeat()` 写锁内运行处理器
- [ ] Leader 的调度器注册了 `system-cluster-message-cleanup` 每日任务，保留期 30 天硬编码
- [ ] 无新增 HTTP 端点、无新增配置项
- [ ] 设计文档第六章与实际文件布局一致；`tests/TEST_CASES.md` 已登记
