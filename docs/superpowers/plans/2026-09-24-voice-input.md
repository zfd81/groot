# 语音输入与配置表 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让使用者在 Web 聊天页用语音下达指令，同时把语音配置存进数据库的通用配置表，并建立统一的配置读取对象。

**Architecture:** 新增 `settings` 配置表（三方言 DDL）与 `internal/repo/settingdb` 仓库实现；新增 `internal/setting` 包，`Settings` 对象按分类暴露 `Xxx(ctx) (T, error)` 方法，屏蔽 YAML 与数据库的来源差异；`internal/llm/transcription.go` 以 multipart 流式请求上游 `/audio/transcriptions`；两条转录路由共用一个 handler，分别走 API Key 与 Web 会话鉴权；前端在聊天输入框加话筒按钮用 `MediaRecorder` 录音，设置面板加语音分区。

**Tech Stack:** Go、sqlx、Hertz、SQLite/MySQL/Postgres 三方言、Vue 3 + TypeScript + Element Plus、浏览器 MediaRecorder API

**对应设计文档:** `docs/superpowers/specs/2026-09-24-voice-input-design.md`

---

## 文件结构

新建文件及其职责：

| 文件 | 职责 |
|---|---|
| `internal/repo/setting.go` | `Setting` 结构体、`Scope` 常量、`SettingRepo` 接口 |
| `internal/repo/settingdb/setting.go` | `SettingRepo` 的 sqlx 实现，三方言共用一份代码 |
| `internal/repo/settingdb/setting_test.go` | 仓库单元测试 |
| `internal/setting/settings.go` | `Settings` 对象与全部分类方法 |
| `internal/setting/voice.go` | `VoiceSettings` 结构、键名常量、与配置表的编解码 |
| `internal/setting/defaults.go` | 各分类默认值 |
| `internal/setting/settings_test.go` | 配置对象单元测试 |
| `internal/llm/transcription.go` | 转录客户端 |
| `internal/llm/transcription_test.go` | 转录客户端单元测试 |
| `internal/api/handler/transcription.go` | 转录 handler |
| `internal/api/handler/transcription_test.go` | 转录 handler 单元测试 |
| `internal/api/handler/setting.go` | 语音配置读写 handler |
| `internal/api/handler/setting_test.go` | 设置 handler 单元测试 |
| `web/src/api/voice.ts` | 前端转录与语音配置的 API 封装 |

修改文件：

| 文件 | 改动 |
|---|---|
| `internal/db/migrate.go` | 三处 DDL 段各加 `settings` 建表语句 |
| `internal/repo/repofactory/factory.go` | `Repos` 加 `Setting` 字段并装配 |
| `internal/api/router.go` | 注册四条新路由 |
| `internal/api/server.go` | 装配两个新 handler |
| `internal/api/types/types.go` | 语音配置的请求与响应类型 |
| `web/src/components/settings/SettingsModal.vue` | 语音分区 |
| `web/src/components/chat/ChatInput.vue` | 话筒按钮与录音逻辑 |
| `web/src/i18n/messages/*` | 语音相关文案 |
| `README.md` | 对外转录接口说明 |
| `examples/python/`、`examples/java/` | 转录调用示例 |
| `tests/python/` | 转录接口系统测试 |
| `tests/TEST_CASES.md` | 补充用例点 |

**注意：** `web/src/components/settings/SettingsModal.vue` 当前有未提交的本地修改，本计划的改动会叠加在其之上。开始前先 `git diff` 确认那部分改动是否需要先提交。

---

## Task 1: 配置表的领域模型与接口

**Files:**
- Create: `internal/repo/setting.go`

- [ ] **Step 1: 写领域模型与接口**

参照 `internal/repo/model.go` 的注释风格，每个接口方法都写明未找到时的行为。

```go
// internal/repo/setting.go
package repo

import "context"

// Scope 配置作用域类型。作用域与实体标识分为两个字段，
// 使「作用域类型」与「实体标识」各自占据独立值域：
// 实体标识不会与 ScopeGlobal 这类保留值冲突。
type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeUser   Scope = "user"
)

// scopePriority 定义取值时「由具体到通用」的回落顺序，数值越小越优先。
// 优先级由此表决定，不依赖数据库对 scope 字符串的排序结果
// （字母序与优先级无关：'agent' 会排在 'global' 之前）。
var scopePriority = map[Scope]int{
	ScopeUser:   0,
	ScopeGlobal: 1,
}

// ScopePriority 返回作用域的优先级，未登记的作用域返回 (0, false)。
func ScopePriority(s Scope) (int, bool) {
	p, ok := scopePriority[s]
	return p, ok
}

// Setting 配置表中的一行。
type Setting struct {
	Scope   Scope
	ScopeID string
	// Name 配置键，点号分层，镜像 YAML 的层级路径，如 voice.model
	Name string
	// Value 统一以文本保存，类型转换由配置对象负责
	Value     string
	UpdatedAt int64 // Unix 毫秒
}

// SettingRepo 配置表仓储。表内只保存使用者明确修改过的值，
// 缺失的键由代码默认值提供，因此查询未命中不是错误。
type SettingRepo interface {
	// Get 查询单个键；未找到返回 ErrNotFound
	Get(ctx context.Context, scope Scope, scopeID, name string) (*Setting, error)
	// ListByScope 返回某个作用域实体下的全部配置，按 name 升序；
	// 无数据返回空切片而非错误
	ListByScope(ctx context.Context, scope Scope, scopeID string) ([]*Setting, error)
	// Upsert 按 (scope, scope_id, name) 写入或覆盖，并刷新 updated_at。
	// scope 为 ScopeGlobal 时 scopeID 必须为空串，否则返回错误
	Upsert(ctx context.Context, items ...*Setting) error
	// Delete 删除单个键，等价于「恢复默认」；未找到返回 nil 而非 ErrNotFound
	Delete(ctx context.Context, scope Scope, scopeID, name string) error
}
```

- [ ] **Step 2: 确认编译通过**

Run: `go build ./internal/repo/...`
Expected: 无输出（编译通过）

- [ ] **Step 3: 提交**

```bash
git add internal/repo/setting.go
git commit -m "feat(repo): 新增配置表领域模型与 SettingRepo 接口"
```

---

## Task 2: settings 表建表语句

**Files:**
- Modify: `internal/db/migrate.go`（三处 DDL 段，分别在 SQLite / MySQL / Postgres 分支内）

- [ ] **Step 1: 先写迁移测试**

`internal/db/migrate_test.go` 已有遍历三方言执行 DDL 的测试（见该文件 142 行附近）。追加一个断言 `settings` 表可读写的测试：

```go
// internal/db/migrate_test.go 末尾追加
func TestMigrate_SettingsTable(t *testing.T) {
	sqlxDB, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlxDB.Close()
	if err := Migrate(sqlxDB, dialect); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ins := fmt.Sprintf(
		"INSERT INTO settings (scope, scope_id, name, value, updated_at) VALUES (%s)",
		dialect.Placeholders(5))
	if _, err := sqlxDB.Exec(ins, "global", "", "voice.model", "whisper-1", int64(1)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got string
	q := fmt.Sprintf(
		"SELECT value FROM settings WHERE scope=%s AND scope_id=%s AND name=%s",
		dialect.Placeholder(1), dialect.Placeholder(2), dialect.Placeholder(3))
	if err := sqlxDB.Get(&got, q, "global", "", "voice.model"); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "whisper-1" {
		t.Errorf("value = %q, want %q", got, "whisper-1")
	}
}
```

若该文件尚未导入 `fmt`，补上导入。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/db/ -run TestMigrate_SettingsTable -v`
Expected: FAIL，报错含 `no such table: settings`

- [ ] **Step 3: 在 SQLite DDL 段加建表语句**

在 `internal/db/migrate.go` 的 SQLite 分支内，紧跟 `models` 表及其索引之后（253-273 行附近）插入：

```go
		`CREATE TABLE IF NOT EXISTS settings (
			scope      TEXT    NOT NULL DEFAULT 'global',
			scope_id   TEXT    NOT NULL DEFAULT '',
			name       TEXT    NOT NULL,
			value      TEXT    NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (scope, scope_id, name)
		)`,
```

- [ ] **Step 4: 在 MySQL DDL 段加建表语句**

在 MySQL 分支内 `models` 表之后（399-418 行附近）插入。注意 MySQL 的主键列不能是无长度的 `TEXT`，必须用 `VARCHAR`：

```go
		`CREATE TABLE IF NOT EXISTS settings (
			scope      VARCHAR(32)  NOT NULL DEFAULT 'global',
			scope_id   VARCHAR(128) NOT NULL DEFAULT '',
			name       VARCHAR(128) NOT NULL,
			value      TEXT         NOT NULL,
			updated_at BIGINT       NOT NULL,
			PRIMARY KEY (scope, scope_id, name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
```

`value` 为 `TEXT` 时 MySQL 不允许带 DEFAULT，故此处不写默认值，由写入层保证非空。

- [ ] **Step 5: 在 Postgres DDL 段加建表语句**

在 Postgres 分支内 `models` 表之后（545-565 行附近）插入：

```go
		`CREATE TABLE IF NOT EXISTS settings (
			scope      VARCHAR(32)  NOT NULL DEFAULT 'global',
			scope_id   VARCHAR(128) NOT NULL DEFAULT '',
			name       VARCHAR(128) NOT NULL,
			value      TEXT         NOT NULL DEFAULT '',
			updated_at BIGINT       NOT NULL,
			PRIMARY KEY (scope, scope_id, name)
		)`,
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/db/ -v`
Expected: PASS，含 `TestMigrate_SettingsTable`

- [ ] **Step 7: 提交**

```bash
git add internal/db/migrate.go internal/db/migrate_test.go
git commit -m "feat(db): 新增 settings 配置表三方言建表语句"
```

---

## Task 3: 配置表仓库实现

**Files:**
- Create: `internal/repo/settingdb/setting.go`
- Test: `internal/repo/settingdb/setting_test.go`

- [ ] **Step 1: 写失败测试**

参照 `internal/repo/modeldb/model_test.go` 的 `newTestRepo` 写法，用 `db.Open(nil, t.TempDir())` 建临时 SQLite 库。

注意 `db.Open` 只建连接不建表，必须显式调 `db.Migrate`。

```go
// internal/repo/settingdb/setting_test.go
package settingdb

import (
	"context"
	"errors"
	"testing"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.SettingRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(sqlxDB, dialect); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func TestSettingRepo_UpsertAndGet(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	err := r.Upsert(ctx, &repo.Setting{
		Scope: repo.ScopeGlobal, Name: "voice.model", Value: "whisper-1",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := r.Get(ctx, repo.ScopeGlobal, "", "voice.model")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != "whisper-1" {
		t.Errorf("Value = %q, want %q", got.Value, "whisper-1")
	}
	if got.UpdatedAt == 0 {
		t.Error("UpdatedAt 未写入")
	}
}

func TestSettingRepo_UpsertOverwrites(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	must(r.Upsert(ctx, &repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.model", Value: "a"}))
	must(r.Upsert(ctx, &repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.model", Value: "b"}))

	got, err := r.Get(ctx, repo.ScopeGlobal, "", "voice.model")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != "b" {
		t.Errorf("Value = %q, want %q（主键冲突应覆盖而非报错）", got.Value, "b")
	}
}

func TestSettingRepo_GetNotFound(t *testing.T) {
	r := newTestRepo(t)
	_, err := r.Get(context.Background(), repo.ScopeGlobal, "", "voice.model")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSettingRepo_GlobalRejectsScopeID(t *testing.T) {
	r := newTestRepo(t)
	err := r.Upsert(context.Background(), &repo.Setting{
		Scope: repo.ScopeGlobal, ScopeID: "u1", Name: "voice.model", Value: "x",
	})
	if err == nil {
		t.Error("global 作用域带 scope_id 应被拒绝")
	}
}

func TestSettingRepo_ListByScope(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	err := r.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.model", Value: "w"},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.enabled", Value: "true"},
		&repo.Setting{Scope: repo.ScopeUser, ScopeID: "u1", Name: "voice.model", Value: "x"},
	)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	items, err := r.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2（不应含 user 作用域的行）", len(items))
	}
	// 按 name 升序：voice.enabled 在 voice.model 之前
	if items[0].Name != "voice.enabled" {
		t.Errorf("items[0].Name = %q, want voice.enabled", items[0].Name)
	}
}

func TestSettingRepo_ListByScopeEmpty(t *testing.T) {
	r := newTestRepo(t)
	items, err := r.ListByScope(context.Background(), repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len = %d, want 0", len(items))
	}
}

func TestSettingRepo_Delete(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if err := r.Upsert(ctx, &repo.Setting{
		Scope: repo.ScopeGlobal, Name: "voice.model", Value: "w",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := r.Delete(ctx, repo.ScopeGlobal, "", "voice.model"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Get(ctx, repo.ScopeGlobal, "", "voice.model"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("删除后 Get err = %v, want ErrNotFound", err)
	}
	// 删除不存在的键不报错
	if err := r.Delete(ctx, repo.ScopeGlobal, "", "nope"); err != nil {
		t.Errorf("删除不存在的键应返回 nil，得到 %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/repo/settingdb/ -v`
Expected: FAIL，报错 `undefined: New`

- [ ] **Step 3: 写实现**

`Upsert` 用 `dialect.UpsertSuffix` 处理三方言差异，冲突列是复合主键，传 `"scope, scope_id, name"`。

```go
// internal/repo/settingdb/setting.go
// Package settingdb 是配置表的 sqlx 实现。
// 表内只保存使用者明确修改过的值，查询未命中交由上层用默认值填充。
package settingdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type settingRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.SettingRepo {
	return &settingRepo{db: sqlxDB, dialect: dialect}
}

type settingRow struct {
	Scope     string `db:"scope"`
	ScopeID   string `db:"scope_id"`
	Name      string `db:"name"`
	Value     string `db:"value"`
	UpdatedAt int64  `db:"updated_at"`
}

const settingColumns = `scope, scope_id, name, value, updated_at`

func rowToSetting(r settingRow) *repo.Setting {
	return &repo.Setting{
		Scope:     repo.Scope(r.Scope),
		ScopeID:   r.ScopeID,
		Name:      r.Name,
		Value:     r.Value,
		UpdatedAt: r.UpdatedAt,
	}
}

// validate 校验作用域与实体标识的搭配。global 作用域要求 scope_id 为空串，
// 该约束放在写入层而非 SQL CHECK：三种方言对 CHECK 的支持与行为不一致。
func validate(s *repo.Setting) error {
	if s.Name == "" {
		return fmt.Errorf("setting: name 不能为空")
	}
	if _, ok := repo.ScopePriority(s.Scope); !ok {
		return fmt.Errorf("setting: 未知作用域 %q", s.Scope)
	}
	if s.Scope == repo.ScopeGlobal && s.ScopeID != "" {
		return fmt.Errorf("setting: global 作用域的 scope_id 必须为空串，得到 %q", s.ScopeID)
	}
	if s.Scope != repo.ScopeGlobal && s.ScopeID == "" {
		return fmt.Errorf("setting: %s 作用域必须带 scope_id", s.Scope)
	}
	return nil
}

func (r *settingRepo) Get(ctx context.Context, scope repo.Scope, scopeID, name string) (*repo.Setting, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM settings WHERE scope=%s AND scope_id=%s AND name=%s`,
		settingColumns,
		r.dialect.Placeholder(1), r.dialect.Placeholder(2), r.dialect.Placeholder(3))
	var row settingRow
	if err := r.db.GetContext(ctx, &row, q, string(scope), scopeID, name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("setting get: %w", err)
	}
	return rowToSetting(row), nil
}

func (r *settingRepo) ListByScope(ctx context.Context, scope repo.Scope, scopeID string) ([]*repo.Setting, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM settings WHERE scope=%s AND scope_id=%s ORDER BY name ASC`,
		settingColumns, r.dialect.Placeholder(1), r.dialect.Placeholder(2))
	var rows []settingRow
	if err := r.db.SelectContext(ctx, &rows, q, string(scope), scopeID); err != nil {
		return nil, fmt.Errorf("setting list: %w", err)
	}
	out := make([]*repo.Setting, 0, len(rows))
	for _, row := range rows {
		out = append(out, rowToSetting(row))
	}
	return out, nil
}

// Upsert 在一个事务内写入多个键，避免设置面板整组保存时出现部分成功。
func (r *settingRepo) Upsert(ctx context.Context, items ...*repo.Setting) error {
	if len(items) == 0 {
		return nil
	}
	for _, s := range items {
		if err := validate(s); err != nil {
			return err
		}
	}

	q := fmt.Sprintf(
		`INSERT INTO settings (%s) VALUES (%s) %s`,
		settingColumns,
		r.dialect.Placeholders(5),
		r.dialect.UpsertSuffix("scope, scope_id, name", "value", "updated_at"))

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("setting upsert begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UnixMilli()
	for _, s := range items {
		if _, err := tx.ExecContext(ctx, q,
			string(s.Scope), s.ScopeID, s.Name, s.Value, now); err != nil {
			return fmt.Errorf("setting upsert %s: %w", s.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("setting upsert commit: %w", err)
	}
	return nil
}

// Delete 删除一个键，等价于把该项恢复为代码默认值。
// 键本就不存在时返回 nil：调用方的意图（该键最终不在表内）已经达成。
func (r *settingRepo) Delete(ctx context.Context, scope repo.Scope, scopeID, name string) error {
	q := fmt.Sprintf(
		`DELETE FROM settings WHERE scope=%s AND scope_id=%s AND name=%s`,
		r.dialect.Placeholder(1), r.dialect.Placeholder(2), r.dialect.Placeholder(3))
	if _, err := r.db.ExecContext(ctx, q, string(scope), scopeID, name); err != nil {
		return fmt.Errorf("setting delete: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/repo/settingdb/ -v`
Expected: PASS，7 个测试全通过

- [ ] **Step 5: 格式化并提交**

```bash
gofmt -w internal/repo/settingdb/
git add internal/repo/settingdb/
git commit -m "feat(repo): 新增配置表仓库实现与单元测试"
```

---

## Task 4: 仓库工厂装配

**Files:**
- Modify: `internal/repo/repofactory/factory.go`

- [ ] **Step 1: 写失败测试**

在 `internal/repo/repofactory/factory_test.go` 末尾追加：

```go
func TestNewRepos_SettingNotNil(t *testing.T) {
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlxDB.Close()

	repos := NewRepos(sqlxDB, dialect, t.TempDir())
	if repos.Setting == nil {
		t.Error("Repos.Setting 未装配")
	}
}
```

若该文件已有等价的建库辅助函数，复用它而不是重复写 `db.Open`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/repo/repofactory/ -run TestNewRepos_SettingNotNil -v`
Expected: FAIL，报错 `repos.Setting undefined`

- [ ] **Step 3: 加字段与装配**

在 `internal/repo/repofactory/factory.go` 的 import 块加：

```go
	"github.com/zfd81/groot/internal/repo/settingdb"
```

在 `Repos` 结构体的 `Message` 字段之后加：

```go
	Setting  repo.SettingRepo
```

在 `NewRepos` 返回的字面量中 `Message:` 之后加：

```go
		Setting:  settingdb.New(sqlxDB, dialect),
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/repo/repofactory/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/repo/repofactory/
git add internal/repo/repofactory/
git commit -m "feat(repo): 工厂装配配置表仓库"
```

---

## Task 5: 语音配置类型与默认值

**Files:**
- Create: `internal/setting/voice.go`
- Create: `internal/setting/defaults.go`

本任务只建类型与默认值，`Settings` 对象在 Task 6 建立。这样拆分是因为 Task 6 的测试需要本任务的类型已存在。

- [ ] **Step 1: 写语音配置类型与键名常量**

```go
// internal/setting/voice.go
package setting

// 语音配置在配置表中的键名。点号分层，镜像 YAML 的层级路径。
const (
	KeyVoiceEnabled  = "voice.enabled"
	KeyVoiceModel    = "voice.model"
	KeyVoiceAutoSend = "voice.auto_send"
)

// VoiceSettings 语音输入配置。
type VoiceSettings struct {
	// Enabled 聊天页是否显示话筒按钮。只影响界面，不影响对外转录接口的可用性
	Enabled bool
	// Model 用于转录的模型名，空串表示尚未配置
	Model string
	// AutoSend 转录完成后是否自动发送
	AutoSend bool
}
```

- [ ] **Step 2: 写默认值**

```go
// internal/setting/defaults.go
// 配置默认值集中定义在此，一个分类一组。
// 配置表中缺失的键由此处填充，因此表在创建后为空即可工作，
// 新增配置项也无需为已有数据库补写初始数据。
package setting

// defaultVoice 返回语音配置的默认值。
// 默认关闭：话筒按钮需要使用者先指定转录模型才有意义。
func defaultVoice() VoiceSettings {
	return VoiceSettings{
		Enabled:  false,
		Model:    "",
		AutoSend: false,
	}
}
```

- [ ] **Step 3: 确认编译通过**

Run: `go build ./internal/setting/...`
Expected: 无输出

- [ ] **Step 4: 提交**

```bash
gofmt -w internal/setting/
git add internal/setting/
git commit -m "feat(setting): 新增语音配置类型与默认值"
```

---

## Task 6: Settings 配置对象

**Files:**
- Create: `internal/setting/settings.go`
- Test: `internal/setting/settings_test.go`

全部分类方法统一为 `Xxx(ctx) (T, error)`。来自 YAML 的分类实现中 ctx 不使用、error 恒为 nil。统一签名的目的是：某一分类日后从 YAML 迁到配置表时，调用方无需改动。

- [ ] **Step 1: 写失败测试**

测试用一个内存假实现替代仓库，避免单元测试依赖数据库。

```go
// internal/setting/settings_test.go
package setting

import (
	"context"
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// fakeSettingRepo 内存实现，键为 scope|scope_id|name
type fakeSettingRepo struct {
	data map[string]string
	err  error
}

func newFakeRepo() *fakeSettingRepo {
	return &fakeSettingRepo{data: map[string]string{}}
}

func key(scope repo.Scope, scopeID, name string) string {
	return string(scope) + "|" + scopeID + "|" + name
}

func (f *fakeSettingRepo) Get(ctx context.Context, scope repo.Scope, scopeID, name string) (*repo.Setting, error) {
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.data[key(scope, scopeID, name)]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return &repo.Setting{Scope: scope, ScopeID: scopeID, Name: name, Value: v}, nil
}

func (f *fakeSettingRepo) ListByScope(ctx context.Context, scope repo.Scope, scopeID string) ([]*repo.Setting, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []*repo.Setting
	prefix := key(scope, scopeID, "")
	for k, v := range f.data {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, &repo.Setting{
				Scope: scope, ScopeID: scopeID, Name: k[len(prefix):], Value: v,
			})
		}
	}
	return out, nil
}

func (f *fakeSettingRepo) Upsert(ctx context.Context, items ...*repo.Setting) error {
	if f.err != nil {
		return f.err
	}
	for _, s := range items {
		if s.Scope == repo.ScopeGlobal && s.ScopeID != "" {
			return repo.ErrNotFound // 占位错误，测试只关心「报错」
		}
		f.data[key(s.Scope, s.ScopeID, s.Name)] = s.Value
	}
	return nil
}

func (f *fakeSettingRepo) Delete(ctx context.Context, scope repo.Scope, scopeID, name string) error {
	delete(f.data, key(scope, scopeID, name))
	return nil
}

func TestSettings_VoiceDefaults(t *testing.T) {
	s := New(config.Config{}, newFakeRepo())

	v, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	want := defaultVoice()
	if v != want {
		t.Errorf("Voice = %+v, want %+v（表为空时应返回默认值）", v, want)
	}
}

func TestSettings_VoicePartialOverride(t *testing.T) {
	f := newFakeRepo()
	f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = "whisper-1"

	s := New(config.Config{}, f)
	v, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v.Model != "whisper-1" {
		t.Errorf("Model = %q, want whisper-1", v.Model)
	}
	if v.Enabled != false {
		t.Error("Enabled 未在表中，应回落到默认值 false")
	}
}

func TestSettings_VoiceBoolParsing(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
		{"", false},
		{"garbage", false}, // 无法解析时按默认值处理，不让脏数据导致取值失败
	}
	for _, c := range cases {
		f := newFakeRepo()
		f.data[key(repo.ScopeGlobal, "", KeyVoiceEnabled)] = c.raw
		s := New(config.Config{}, f)
		v, err := s.Voice(context.Background())
		if err != nil {
			t.Fatalf("raw=%q Voice: %v", c.raw, err)
		}
		if v.Enabled != c.want {
			t.Errorf("raw=%q Enabled = %v, want %v", c.raw, v.Enabled, c.want)
		}
	}
}

func TestSettings_SetVoiceRoundTrip(t *testing.T) {
	s := New(config.Config{}, newFakeRepo())
	ctx := context.Background()

	in := VoiceSettings{Enabled: true, Model: "whisper-1", AutoSend: true}
	if err := s.SetVoice(ctx, in); err != nil {
		t.Fatalf("SetVoice: %v", err)
	}
	out, err := s.Voice(ctx)
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if out != in {
		t.Errorf("回读 = %+v, want %+v", out, in)
	}
}

func TestSettings_YAMLCategories(t *testing.T) {
	cfg := config.Config{}
	cfg.Server.Port = 8080
	cfg.Attachment.MaxSize = 1024

	s := New(cfg, newFakeRepo())
	ctx := context.Background()

	srv, err := s.Server(ctx)
	if err != nil {
		t.Fatalf("Server: %v", err)
	}
	if srv.Port != 8080 {
		t.Errorf("Port = %d, want 8080", srv.Port)
	}

	att, err := s.Attachment(ctx)
	if err != nil {
		t.Fatalf("Attachment: %v", err)
	}
	if att.MaxSize != 1024 {
		t.Errorf("MaxSize = %d, want 1024", att.MaxSize)
	}
}

func TestSettings_NilRepoUsesDefaults(t *testing.T) {
	// 配置表不可用时（如仓库未装配），来自表的分类回落到默认值而非 panic
	s := New(config.Config{}, nil)
	v, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v != defaultVoice() {
		t.Errorf("Voice = %+v, want 默认值", v)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/setting/ -v`
Expected: FAIL，报错 `undefined: New`

- [ ] **Step 3: 写 Settings 实现**

```go
// Package setting 是程序读取配置的唯一入口。
// Settings 私有地持有 YAML 解析结果与配置表仓库，对外只按分类暴露方法，
// 调用方从方法返回的结构体上取属性，不关心某一项配置来自 YAML 还是数据库。
//
// 本包独立于 internal/config 而非并入其中：config 处于依赖链底层，
// 而配置表仓库需要引用它完成环境变量展开，配置对象置于两者之上可避免循环导入。
package setting

import (
	"context"
	"strconv"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// Settings 配置对象。
//
// 全部分类方法统一签名 Xxx(ctx) (T, error)：来自 YAML 的分类不使用 ctx 且
// error 恒为 nil，但保持签名一致，使某一分类日后迁移到配置表时调用方无需改动。
//
// 来自配置表的分类每次调用查询数据库，不做内存缓存：设置面板保存后当次请求
// 即可读到新值，多节点共享同一数据库时各节点取值一致。语音配置的读取时机
// （打开聊天页、点击话筒、转录接口被调用）均为单次主键查询，不在热路径。
type Settings struct {
	static config.Config
	repo   repo.SettingRepo
}

// New 构造配置对象。settingRepo 为 nil 时，来自配置表的分类一律返回默认值，
// 便于在尚未接入数据库的场景（如部分单元测试）中使用。
func New(static config.Config, settingRepo repo.SettingRepo) *Settings {
	return &Settings{static: static, repo: settingRepo}
}

// ---- 来自 YAML 的分类 ----

func (s *Settings) Agent(ctx context.Context) (config.AgentConfig, error) {
	return s.static.Agent, nil
}

func (s *Settings) Server(ctx context.Context) (config.ServerConfig, error) {
	return s.static.Server, nil
}

func (s *Settings) Memory(ctx context.Context) (config.MemoryConfig, error) {
	return s.static.Memory, nil
}

func (s *Settings) React(ctx context.Context) (config.ReactConfig, error) {
	return s.static.React, nil
}

func (s *Settings) Attachment(ctx context.Context) (config.AttachmentConfig, error) {
	return s.static.Attachment, nil
}

func (s *Settings) Schedule(ctx context.Context) (config.ScheduleConfig, error) {
	return s.static.Schedule, nil
}

func (s *Settings) Message(ctx context.Context) (config.MessageConfig, error) {
	return s.static.Message, nil
}

func (s *Settings) SubAgent(ctx context.Context) (config.SubAgentConfig, error) {
	return s.static.SubAgent, nil
}

func (s *Settings) Security(ctx context.Context) (config.SecurityConfig, error) {
	return s.static.Security, nil
}

func (s *Settings) Logging(ctx context.Context) (config.LoggingConfig, error) {
	return s.static.Logging, nil
}

func (s *Settings) Database(ctx context.Context) (*config.DatabaseConfig, error) {
	return s.static.Database, nil
}

// ---- 来自配置表的分类 ----

// Voice 读取语音配置。表内缺失的字段由代码默认值填充。
func (s *Settings) Voice(ctx context.Context) (VoiceSettings, error) {
	v := defaultVoice()
	if s.repo == nil {
		return v, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return VoiceSettings{}, err
	}
	if raw, ok := vals[KeyVoiceEnabled]; ok {
		v.Enabled = parseBool(raw, v.Enabled)
	}
	if raw, ok := vals[KeyVoiceModel]; ok {
		v.Model = raw
	}
	if raw, ok := vals[KeyVoiceAutoSend]; ok {
		v.AutoSend = parseBool(raw, v.AutoSend)
	}
	return v, nil
}

// SetVoice 整体保存语音配置的三个字段。
func (s *Settings) SetVoice(ctx context.Context, v VoiceSettings) error {
	if s.repo == nil {
		return ErrNoSettingStore
	}
	return s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceEnabled, Value: strconv.FormatBool(v.Enabled)},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceModel, Value: v.Model},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceAutoSend, Value: strconv.FormatBool(v.AutoSend)},
	)
}

// globalValues 一次取出全局作用域的全部配置，避免一个分类内逐键查询。
func (s *Settings) globalValues(ctx context.Context) (map[string]string, error) {
	items, err := s.repo.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(items))
	for _, it := range items {
		out[it.Name] = it.Value
	}
	return out, nil
}

// parseBool 解析布尔值；无法解析时返回 fallback。
// 表中的脏数据不应导致整次取值失败，退回默认值即可。
func parseBool(raw string, fallback bool) bool {
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return b
}
```

- [ ] **Step 4: 加错误定义**

在 `internal/setting/voice.go` 顶部 package 声明之后加：

```go
import "errors"

// ErrNoSettingStore 配置表仓库未装配，无法写入配置。
var ErrNoSettingStore = errors.New("setting: 配置存储不可用")
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/setting/ -v`
Expected: PASS，6 个测试全通过

- [ ] **Step 6: 提交**

```bash
gofmt -w internal/setting/
git add internal/setting/
git commit -m "feat(setting): 新增 Settings 配置对象与单元测试"
```

---

## Task 7: 转录客户端

**Files:**
- Create: `internal/llm/transcription.go`
- Test: `internal/llm/transcription_test.go`

- [ ] **Step 1: 写失败测试**

用 `httptest` 模拟上游，验证 multipart 组装、`/v1` 补齐与错误映射。

```go
// internal/llm/transcription_test.go
package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/repo"
)

func TestTranscribe_Success(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotLang, gotFilename, gotFileBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotModel = r.FormValue("model")
		gotLang = r.FormValue("language")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
		} else {
			defer f.Close()
			gotFilename = hdr.Filename
			b, _ := io.ReadAll(f)
			gotFileBody = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"帮我看一下登录接口的日志"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "sk-test", Model: "whisper-1"}
	text, err := Transcribe(context.Background(), m,
		strings.NewReader("FAKE-AUDIO"), "rec.webm", "zh")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if text != "帮我看一下登录接口的日志" {
		t.Errorf("text = %q", text)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Errorf("path = %q, want /v1/audio/transcriptions（base_url 缺 /v1 时应补齐）", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotModel != "whisper-1" {
		t.Errorf("model = %q, want whisper-1", gotModel)
	}
	if gotLang != "zh" {
		t.Errorf("language = %q, want zh", gotLang)
	}
	if gotFilename != "rec.webm" {
		t.Errorf("filename = %q, want rec.webm", gotFilename)
	}
	if gotFileBody != "FAKE-AUDIO" {
		t.Errorf("file body = %q, want FAKE-AUDIO", gotFileBody)
	}
}

func TestTranscribe_BaseURLAlreadyHasV1(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "whisper-1"}
	if _, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", ""); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Errorf("path = %q, want /v1/audio/transcriptions（不应重复补 /v1）", gotPath)
	}
}

func TestTranscribe_LanguageOmittedWhenEmpty(t *testing.T) {
	var hasLang bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		_, hasLang = r.MultipartForm.Value["language"]
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	if _, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", ""); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if hasLang {
		t.Error("language 为空时不应下发该字段")
	}
}

func TestTranscribe_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "bad", Model: "whisper-1"}
	_, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", "")
	if err == nil {
		t.Fatal("上游 401 应返回错误")
	}
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("err 应包装 ErrUpstream，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Errorf("错误信息应透传上游原文，得到 %q", err.Error())
	}
}

func TestTranscribe_EmptyTextIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"text":""}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	_, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", "")
	if !errors.Is(err, ErrEmptyTranscript) {
		t.Errorf("err = %v, want ErrEmptyTranscript", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/llm/ -run TestTranscribe -v`
Expected: FAIL，报错 `undefined: Transcribe`

- [ ] **Step 3: 写实现**

文件部分用 `io.Pipe` 流式写入，不在内存中完整展开音频。

```go
// internal/llm/transcription.go
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

var (
	// ErrUpstream 上游转录服务返回了错误，错误信息中透传上游原文
	ErrUpstream = errors.New("上游转录服务返回错误")
	// ErrEmptyTranscript 上游返回了空文本，通常是音频过短或无有效语音
	ErrEmptyTranscript = errors.New("未识别到有效语音")
)

// transcribeTimeout 转录请求的整体超时。音频转录比对话补全慢，
// 留足时间，但不能无上限，否则挂起的请求会一直占用连接。
const transcribeTimeout = 120 * time.Second

// Transcribe 把音频转成文字。
//
// 请求 {base_url}/audio/transcriptions，以 multipart 表单提交，
// 与 OpenAI 的转录规范一致，鉴权沿用 repo.Model 中的 APIKey。
// base_url 缺少 /v1 后缀时自动补齐，与 CheckConnection 的处理一致。
//
// file 以流式写入上游请求体，不在内存中完整展开音频。
// language 为空时不下发该字段，交由上游自行判断语种。
func Transcribe(ctx context.Context, m *repo.Model, file io.Reader,
	filename, language string) (string, error) {

	endpoint := transcriptionURL(m.BaseURL)

	// io.Pipe 让 multipart 的写入与 HTTP 请求体的读取并发进行，
	// 音频不必先在内存里拼成完整的 body。
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		// 写入侧出错时用 CloseWithError 关闭管道，
		// 读取侧（http.Client）随即拿到同一个错误，不会静默发出残缺请求。
		var err error
		defer func() { pw.CloseWithError(err) }()

		var part io.Writer
		if part, err = mw.CreateFormFile("file", filename); err != nil {
			return
		}
		if _, err = io.Copy(part, file); err != nil {
			return
		}
		if err = mw.WriteField("model", m.Model); err != nil {
			return
		}
		if language != "" {
			if err = mw.WriteField("language", language); err != nil {
				return
			}
		}
		err = mw.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		return "", fmt.Errorf("构造转录请求失败: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+m.APIKey)

	client := &http.Client{Timeout: transcribeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: 读取响应失败: %v", ErrUpstream, err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%w: %s", ErrUpstream, upstreamMessage(body, resp.StatusCode))
	}

	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("%w: 响应不是合法 JSON: %s", ErrUpstream, truncate(string(body), 200))
	}
	if strings.TrimSpace(out.Text) == "" {
		return "", ErrEmptyTranscript
	}
	return out.Text, nil
}

// transcriptionURL 由 base_url 推出转录端点，缺少 /v1 后缀时补齐。
func transcriptionURL(baseURL string) string {
	b := strings.TrimSuffix(baseURL, "/")
	if !strings.HasSuffix(b, "/v1") {
		b += "/v1"
	}
	return b + "/audio/transcriptions"
}

// upstreamMessage 从上游错误响应中提取可读信息。
// 优先取 OpenAI 规范的 error.message，取不到则退回响应原文。
func upstreamMessage(body []byte, status int) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	if len(body) == 0 {
		return fmt.Sprintf("HTTP %d", status)
	}
	return truncate(string(body), 200)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/llm/ -run TestTranscribe -v`
Expected: PASS，5 个测试全通过

- [ ] **Step 5: 跑整包回归**

Run: `go test ./internal/llm/ -v`
Expected: PASS，含既有模型服务测试

- [ ] **Step 6: 提交**

```bash
gofmt -w internal/llm/
git add internal/llm/transcription.go internal/llm/transcription_test.go
git commit -m "feat(llm): 新增转录客户端"
```

---

## Task 8: 转录 handler

**Files:**
- Create: `internal/api/handler/transcription.go`
- Test: `internal/api/handler/transcription_test.go`

两条路由（对外与 Web）共用同一个 `Serve` 方法，鉴权差异在路由层用中间件区分。

- [ ] **Step 1: 写失败测试**

`app.RequestContext` 取 multipart 用 `rc.FormFile` 与 `rc.PostForm`，测试侧构造参照 `internal/api/handler/files_test.go:155` 的写法。

```go
// internal/api/handler/transcription_test.go
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
	"github.com/zfd81/groot/internal/repo/settingdb"
	"github.com/zfd81/groot/internal/setting"
)

// newTranscriptionHandlerForTest 建一套真实的仓库与配置对象，
// upstreamURL 作为模型的 base_url 指向 httptest 假上游。
func newTranscriptionHandlerForTest(t *testing.T, upstreamURL, voiceModel string) *TranscriptionHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(sqlxDB, dialect); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if upstreamURL != "" {
		err := models.Create(context.Background(), &repo.Model{
			Name: "whisper-1", Model: "whisper-1",
			BaseURL: upstreamURL, APIKey: "sk-test-1234abcd",
			Enabled: true, Stop: []string{},
		})
		if err != nil {
			t.Fatalf("创建测试模型: %v", err)
		}
	}

	settings := setting.New(config.Config{}, settingdb.New(sqlxDB, dialect))
	if voiceModel != "" {
		err := settings.SetVoice(context.Background(), setting.VoiceSettings{
			Enabled: true, Model: voiceModel,
		})
		if err != nil {
			t.Fatalf("SetVoice: %v", err)
		}
	}
	return NewTranscriptionHandler(settings, models, logger.NewNop())
}

// audioCtx 构造一个 multipart 请求上下文。model 为空时不带该字段。
func audioCtx(t *testing.T, withFile bool, filename, model string) *app.RequestContext {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if withFile {
		fw, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte("FAKE-AUDIO-BYTES")); err != nil {
			t.Fatal(err)
		}
	}
	if model != "" {
		if err := w.WriteField("model", model); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte(w.FormDataContentType()))
	rc.Request.Header.SetContentLength(buf.Len())
	rc.Request.SetBody(buf.Bytes())
	return rc
}

// fakeUpstream 返回一个总是成功转录的假上游。
func fakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"打开登录日志"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func bodyStatus(t *testing.T, rc *app.RequestContext) string {
	t.Helper()
	var out struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %s", rc.Response.Body())
	}
	return out.Status
}

func TestTranscriptionHandler_Success(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, up.URL, "whisper-1")

	rc := audioCtx(t, true, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out struct {
		Text  string `json:"text"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.Text != "打开登录日志" {
		t.Errorf("text = %q", out.Text)
	}
	if out.Model != "whisper-1" {
		t.Errorf("model = %q, want whisper-1（应回传实际使用的模型）", out.Model)
	}
}

func TestTranscriptionHandler_MissingFile(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, up.URL, "whisper-1")

	rc := audioCtx(t, false, "", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
}

func TestTranscriptionHandler_NoVoiceModelConfigured(t *testing.T) {
	up := fakeUpstream(t)
	// 建了模型但没配 voice.model，且请求不带 model 字段
	h := newTranscriptionHandlerForTest(t, up.URL, "")

	rc := audioCtx(t, true, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
}

func TestTranscriptionHandler_ModelNotFound(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, up.URL, "whisper-1")

	rc := audioCtx(t, true, "rec.webm", "nonexistent-model")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
	if !bytes.Contains(rc.Response.Body(), []byte("nonexistent-model")) {
		t.Errorf("错误信息应含模型名: %s", rc.Response.Body())
	}
}

func TestTranscriptionHandler_FormModelWinsOverSetting(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, up.URL, "whisper-1")

	// 再建一个模型，请求显式指定它，应优先于 voice.model
	rc := audioCtx(t, true, "rec.webm", "whisper-1")
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestTranscriptionHandler_HeaderModelUsed(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, up.URL, "")

	rc := audioCtx(t, true, "rec.webm", "")
	rc.Request.Header.Set("X-Model-Name", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s（请求头应可指定模型）", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestTranscriptionHandler_UnsupportedExtension(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, up.URL, "whisper-1")

	rc := audioCtx(t, true, "notes.txt", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "unsupported_type" {
		t.Errorf("status = %q, want unsupported_type", s)
	}
}

func TestTranscriptionHandler_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, srv.URL, "whisper-1")
	rc := audioCtx(t, true, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 502 {
		t.Fatalf("status=%d, want 502", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "upstream_error" {
		t.Errorf("status = %q, want upstream_error", s)
	}
	if !bytes.Contains(rc.Response.Body(), []byte("Incorrect API key")) {
		t.Errorf("应透传上游错误原文: %s", rc.Response.Body())
	}
}

// TestTranscriptionHandler_FileTooLarge 单独构造 handler，把附件上限设为 1MB，
// 再上传 2MB 的音频，验证按 MB 换算后的超限判断。
func TestTranscriptionHandler_FileTooLarge(t *testing.T) {
	up := fakeUpstream(t)
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(sqlxDB, dialect); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if err := models.Create(context.Background(), &repo.Model{
		Name: "whisper-1", Model: "whisper-1", BaseURL: up.URL,
		APIKey: "sk-test-1234abcd", Enabled: true, Stop: []string{},
	}); err != nil {
		t.Fatalf("创建测试模型: %v", err)
	}
	cfg := config.Config{}
	cfg.Attachment.MaxSize = 1 // MB
	h := NewTranscriptionHandler(setting.New(cfg, settingdb.New(sqlxDB, dialect)), models, logger.NewNop())

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "big.webm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte{0}, 2*1024*1024)); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("model", "whisper-1"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte(w.FormDataContentType()))
	rc.Request.Header.SetContentLength(buf.Len())
	rc.Request.SetBody(buf.Bytes())

	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "file_too_large" {
		t.Errorf("status = %q, want file_too_large", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handler/ -run TestTranscriptionHandler -v`
Expected: FAIL，报错 `undefined: NewTranscriptionHandler`

- [ ] **Step 3: 写实现**

```go
// internal/api/handler/transcription.go
package handler

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/setting"
)

// audioExtensions 受支持的音频扩展名。
// 浏览器录音产出 webm，其余为对外接口常见的上传格式。
var audioExtensions = map[string]bool{
	".webm": true, ".mp3": true, ".mp4": true, ".mpeg": true,
	".mpga": true, ".m4a": true, ".wav": true, ".ogg": true, ".flac": true,
}

func supportedAudioExtensions() string {
	exts := make([]string, 0, len(audioExtensions))
	for e := range audioExtensions {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

// TranscriptionHandler 处理音频转录请求。
// 对外路由与 Web 路由共用同一个 Serve 方法，鉴权差异由路由层的中间件承担。
type TranscriptionHandler struct {
	settings *setting.Settings
	models   *llm.ModelService
	log      *logger.Logger
}

func NewTranscriptionHandler(settings *setting.Settings, models *llm.ModelService,
	log *logger.Logger) *TranscriptionHandler {
	return &TranscriptionHandler{settings: settings, models: models, log: log}
}

// Serve 处理 POST /audio/transcriptions 与 POST /web/audio/transcriptions。
func (h *TranscriptionHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	fh, err := rc.FormFile("file")
	if err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "缺少 file 字段"})
		return
	}

	ext := strings.ToLower(path.Ext(fh.Filename))
	if !audioExtensions[ext] {
		rc.JSON(400, utils.H{
			"status":  "unsupported_type",
			"message": fmt.Sprintf("不支持的音频格式 %q，受支持的扩展名: %s", ext, supportedAudioExtensions()),
		})
		return
	}

	// 附件配置的 MaxSize 以 MB 计（见 internal/attachment/handler.go 的换算），
	// 这里同样换算成字节后再与 fh.Size 比较，两处口径保持一致。
	att, err := h.settings.Attachment(ctx)
	if err == nil && att.MaxSize > 0 {
		limit := int64(att.MaxSize) * 1024 * 1024
		if fh.Size > limit {
			rc.JSON(400, utils.H{
				"status":  "file_too_large",
				"message": fmt.Sprintf("音频大小 %d 字节超过上限 %d MB", fh.Size, att.MaxSize),
			})
			return
		}
	}

	modelName, err := h.resolveModelName(ctx, rc)
	if err != nil {
		rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
		return
	}

	// GetByName 自身已校验 enabled：模型不存在返回 ErrModelNotFound，
	// 已禁用返回 ErrModelDisabled，两者对调用方都是「模型不可用」，
	// 统一映射为 invalid_model，错误原文已含模型名。
	m, err := h.models.GetByName(ctx, modelName)
	if err != nil {
		if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) {
			rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
			return
		}
		h.log.Error("查询语音模型失败", zap.String("model", modelName), zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}

	f, err := fh.Open()
	if err != nil {
		h.log.Error("打开上传音频失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	defer f.Close()

	text, err := llm.Transcribe(ctx, m, f, fh.Filename, rc.PostForm("language"))
	if err != nil {
		if errors.Is(err, llm.ErrEmptyTranscript) {
			rc.JSON(400, utils.H{"status": "invalid_request", "message": err.Error()})
			return
		}
		h.log.Warn("转录失败", zap.String("model", modelName), zap.Error(err))
		rc.JSON(502, utils.H{"status": "upstream_error", "message": err.Error()})
		return
	}

	rc.JSON(200, utils.H{"text": text, "model": modelName})
}

// resolveModelName 按「表单 model → 请求头 X-Model-Name → 配置表 voice.model」
// 的顺序取模型名。请求头形式与 /chat 的既有约定一致。
func (h *TranscriptionHandler) resolveModelName(ctx context.Context, rc *app.RequestContext) (string, error) {
	if v := strings.TrimSpace(rc.PostForm("model")); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(string(rc.GetHeader("X-Model-Name"))); v != "" {
		return v, nil
	}
	voice, err := h.settings.Voice(ctx)
	if err != nil {
		h.log.Error("读取语音配置失败", zap.Error(err))
		return "", errors.New("读取语音配置失败")
	}
	if strings.TrimSpace(voice.Model) == "" {
		return "", errors.New("未配置语音模型，请前往设置页指定用于转录的模型")
	}
	return voice.Model, nil
}
```


- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/handler/ -run TestTranscriptionHandler -v`
Expected: PASS，9 个测试全通过

`llm.ErrModelNotFound` 与 `llm.ErrModelDisabled` 定义在 `internal/llm/service.go`，`GetByName` 会返回包装了它们的错误。测试中的 `TestTranscriptionHandler_ModelNotFound` 断言响应体含模型名，正是依赖这两个错误的原文格式 `模型不存在: <name>`。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/api/handler/
git add internal/api/handler/transcription.go internal/api/handler/transcription_test.go
git commit -m "feat(api): 新增转录 handler"
```

---

## Task 9: 语音配置读写 handler

**Files:**
- Create: `internal/api/handler/setting.go`
- Test: `internal/api/handler/setting_test.go`
- Modify: `internal/api/types/types.go`

- [ ] **Step 1: 加请求响应类型**

在 `internal/api/types/types.go` 末尾追加：

```go
// VoiceSettingsRequest 是 PUT /web/settings/voice 的请求体。
// 三个字段整体保存，不支持部分更新：设置面板一次提交整个分区。
type VoiceSettingsRequest struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model"`
	AutoSend bool   `json:"auto_send"`
}

// VoiceSettingsResponse 是 GET /web/settings/voice 的响应体。
type VoiceSettingsResponse struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model"`
	AutoSend bool   `json:"auto_send"`
}
```

- [ ] **Step 2: 写失败测试**

```go
// internal/api/handler/setting_test.go
package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
	"github.com/zfd81/groot/internal/repo/settingdb"
	"github.com/zfd81/groot/internal/setting"
)

// newSettingHandlerForTest 建 handler，并按需预置一个启用的模型。
func newSettingHandlerForTest(t *testing.T, withModel string, enabled bool) *SettingHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(sqlxDB, dialect); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if withModel != "" {
		err := models.Create(context.Background(), &repo.Model{
			Name: withModel, Model: withModel,
			BaseURL: "https://api.openai.com/v1", APIKey: "sk-test-1234abcd",
			Enabled: enabled, Stop: []string{},
		})
		if err != nil {
			t.Fatalf("创建模型: %v", err)
		}
	}
	settings := setting.New(config.Config{}, settingdb.New(sqlxDB, dialect))
	return NewSettingHandler(settings, models, logger.NewNop())
}

func TestSettingHandler_GetVoiceDefaults(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.GetVoice, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out struct {
		Enabled  bool   `json:"enabled"`
		Model    string `json:"model"`
		AutoSend bool   `json:"auto_send"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.Enabled || out.Model != "" || out.AutoSend {
		t.Errorf("表为空时应返回默认值，得到 %+v", out)
	}
}

func TestSettingHandler_PutVoiceRoundTrip(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	body := `{"enabled":true,"model":"whisper-1","auto_send":true}`
	rc := callJSON(h.PutVoice, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PutVoice status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc = callJSON(h.GetVoice, consts.MethodGet, "", nil)
	var out struct {
		Enabled  bool   `json:"enabled"`
		Model    string `json:"model"`
		AutoSend bool   `json:"auto_send"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if !out.Enabled || out.Model != "whisper-1" || !out.AutoSend {
		t.Errorf("回读 = %+v, want 全部生效", out)
	}
}

func TestSettingHandler_PutVoiceEmptyModelAllowed(t *testing.T) {
	// model 为空串表示「尚未指定」，允许保存：使用者可以先开开关再选模型
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestSettingHandler_PutVoiceUnknownModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"nope","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
}

func TestSettingHandler_PutVoiceDisabledModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"whisper-1","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400（已禁用的模型不应被选为语音模型）", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
}

func TestSettingHandler_PutVoiceBadJSON(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{not json`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
}
```

`callJSON` 与 `bodyStatus` 复用同包已有的辅助函数（分别在 `models_test.go` 与 `transcription_test.go` 中定义）。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/api/handler/ -run TestSettingHandler -v`
Expected: FAIL，报错 `undefined: NewSettingHandler`

- [ ] **Step 4: 写实现**

```go
// internal/api/handler/setting.go
package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/setting"
)

// SettingHandler 处理配置的读写。接口按分类而非按单键暴露，
// 与设置面板的分区一一对应。
type SettingHandler struct {
	settings *setting.Settings
	models   *llm.ModelService
	log      *logger.Logger
}

func NewSettingHandler(settings *setting.Settings, models *llm.ModelService,
	log *logger.Logger) *SettingHandler {
	return &SettingHandler{settings: settings, models: models, log: log}
}

// GetVoice 处理 GET /web/settings/voice。
func (h *SettingHandler) GetVoice(ctx context.Context, rc *app.RequestContext) {
	v, err := h.settings.Voice(ctx)
	if err != nil {
		h.log.Error("读取语音配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	rc.JSON(200, types.VoiceSettingsResponse{
		Enabled:  v.Enabled,
		Model:    v.Model,
		AutoSend: v.AutoSend,
	})
}

// PutVoice 处理 PUT /web/settings/voice，整体保存三个字段。
func (h *SettingHandler) PutVoice(ctx context.Context, rc *app.RequestContext) {
	var req types.VoiceSettingsRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}

	// model 为空串表示尚未指定，允许保存：使用者可以先开开关再选模型。
	// 非空时必须是已存在且启用的模型，否则话筒按钮会一直失败。
	name := strings.TrimSpace(req.Model)
	if name != "" {
		if _, err := h.models.GetByName(ctx, name); err != nil {
			if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) {
				rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
				return
			}
			h.log.Error("校验语音模型失败", zap.String("model", name), zap.Error(err))
			rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
			return
		}
	}

	err := h.settings.SetVoice(ctx, setting.VoiceSettings{
		Enabled:  req.Enabled,
		Model:    name,
		AutoSend: req.AutoSend,
	})
	if err != nil {
		h.log.Error("保存语音配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/api/handler/ -run TestSettingHandler -v`
Expected: PASS，6 个测试全通过

- [ ] **Step 6: 提交**

```bash
gofmt -w internal/api/handler/ internal/api/types/
git add internal/api/handler/setting.go internal/api/handler/setting_test.go internal/api/types/types.go
git commit -m "feat(api): 新增语音配置读写 handler"
```

---

## Task 10: 路由注册与服务装配

**Files:**
- Modify: `internal/api/router.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: 给 RegisterRoutes 加参数**

在 `internal/api/router.go` 的 `RegisterRoutes` 参数列表末尾（`syncH *handler.SyncHandler` 之后）加：

```go
	transcriptionH *handler.TranscriptionHandler,
	settingH *handler.SettingHandler,
```

- [ ] **Step 2: 注册四条路由**

先看清文件内既有的分组写法：对外路由带 `authMW` 与 `rateLimitMW`，Web 路由带 Web 会话中间件。按同样的分组位置插入。

对外转录路由，与 `/chat` 放在同一组（API Key 鉴权 + 限流）：

```go
	// 对外转录接口：与 /chat 同组，API Key 鉴权并计入限流
	apiGroup.POST("/audio/transcriptions", transcriptionH.Serve)
```

Web 路由，与其他 `/web/*` 放在同一组（Web 会话鉴权）：

```go
	// 聊天页调用的转录接口，与对外接口共用同一 handler
	webGroup.POST("/audio/transcriptions", transcriptionH.Serve)
	// 语音配置读写
	webGroup.GET("/settings/voice", settingH.GetVoice)
	webGroup.PUT("/settings/voice", settingH.PutVoice)
```

`apiGroup` 与 `webGroup` 是 `internal/api/router.go` 中已有的分组变量名，分别在 87 行与 44 行附近定义。

- [ ] **Step 3: 在 server.go 装配**

`NewServer` 定义在 `internal/api/server.go:38`，第一个参数 `cfg config.Config` 是值类型。在参数列表中 `syncResources repo.ResourceRepo,` 之后加一行：

```go
	settingRepo repo.SettingRepo, // 配置表仓储：语音等无需重启即可生效的配置
```

在 import 块加：

```go
	"github.com/zfd81/groot/internal/setting"
```

在 handler 装配段（`syncH := handler.NewSyncHandler(...)` 之后）加：

```go
	// 配置对象：YAML 配置与配置表的统一入口
	settings := setting.New(cfg, settingRepo)
	transcriptionH := handler.NewTranscriptionHandler(settings, models, log)
	settingH := handler.NewSettingHandler(settings, models, log)
```

把两个新 handler 加到 `RegisterRoutes` 调用的末尾：

```go
	RegisterRoutes(h, authMW, rateLimitMW, webStore,
		chatH, statusH, detailH, sessionH,
		healthH, skillsH, agentsH, toolsH, modelsH, scheduleH, webAuthH, apiKeysH, clusterH, logsH, filesH, syncH,
		transcriptionH, settingH)
```

- [ ] **Step 4: 更新 NewServer 的调用方**

唯一调用方在 `cmd/groot/main.go:459`。把 `repos.Setting` 插到 `repos.SyncResource` 之后：

```go
	srv := api.NewServer(*cfg, homeDir, log, memMgr, runtimeState, skillBackend, skillMiddleware, mcpMgr, exec, subAgentReg, &scheduleMgr, repos.User, modelService, repos.APIKey, repos.Member, repos.SyncResource, repos.Setting, clusterInst, role)
```

- [ ] **Step 5: 确认全项目编译通过**

Run: `go build ./...`
Expected: 无输出

- [ ] **Step 6: 跑全量后端测试**

Run: `go test ./internal/... 2>&1 | tail -30`
Expected: 全部 ok，无 FAIL

- [ ] **Step 7: 提交**

```bash
gofmt -w internal/api/
git add internal/api/ cmd/
git commit -m "feat(api): 注册转录与语音配置路由"
```

---

## Task 11: 前端 API 封装

**Files:**
- Create: `web/src/api/voice.ts`

转录走 multipart，不能用 `api.post`（它会把 body 当 JSON 序列化）。参照 `web/src/api/files.ts:31` 的 `upload` 写法直接用 `fetch`，并复用同一套 401 处理。

- [ ] **Step 1: 写封装**

```ts
// web/src/api/voice.ts
// 语音相关接口：音频转录与语音配置读写。
// 转录是 multipart 请求，不能走 api.post（它按 JSON 序列化 body），
// 因此直接用 fetch，并复用 client 的 401 处理与错误结构。
import { ApiError, api, notifyUnauthorized } from './client'
import i18n from '../i18n'

const t = i18n.global.t

export interface VoiceSettings {
  enabled: boolean
  model: string
  auto_send: boolean
}

export interface TranscriptionResult {
  text: string
  model: string
}

export const voiceApi = {
  // transcribe 上传音频并返回识别文本。model 省略时由后端按配置表取。
  async transcribe(blob: Blob, filename: string, model?: string): Promise<TranscriptionResult> {
    const fd = new FormData()
    fd.append('file', blob, filename)
    if (model) fd.append('model', model)

    const resp = await fetch('/web/audio/transcriptions', {
      method: 'POST',
      body: fd,
      credentials: 'same-origin',
    })
    if (resp.status === 401) {
      notifyUnauthorized()
      throw new ApiError(401, t('error.unauthorized'))
    }
    const data = await resp.json().catch(() => null)
    if (!resp.ok) {
      const message =
        (data && (data.message || data.error)) ||
        t('error.requestFailed', { status: resp.status })
      throw new ApiError(resp.status, message, data && data.status)
    }
    return data as TranscriptionResult
  },

  getSettings: () => api.get<VoiceSettings>('/web/settings/voice'),

  saveSettings: (s: VoiceSettings) => api.put<{ status: string }>('/web/settings/voice', s),
}
```

- [ ] **Step 2: 确认类型检查通过**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head -20`
Expected: 无与 `voice.ts` 相关的报错

- [ ] **Step 3: 提交**

```bash
git add web/src/api/voice.ts
git commit -m "feat(web): 新增语音接口封装"
```

---

## Task 12: 录音组合式函数

**Files:**
- Create: `web/src/composables/useRecorder.ts`

把 `MediaRecorder` 的生命周期封起来，让组件只关心「开始、停止、拿到 Blob」。放 composables 目录是因为这段逻辑与具体组件无关，设置面板的试听按钮日后也能复用。

- [ ] **Step 1: 确认目录存在**

Run: `ls web/src/composables/ 2>/dev/null || echo "需新建目录"`

若不存在则 `mkdir -p web/src/composables`。

- [ ] **Step 2: 写实现**

```ts
// web/src/composables/useRecorder.ts
// 麦克风录音的生命周期封装。组件只需调 start/stop，
// 停止后通过 stop() 的返回值拿到音频 Blob。
import { ref, onBeforeUnmount } from 'vue'

// 浏览器对录音容器的支持不一致：Chrome/Edge/Firefox 支持 webm/opus，
// Safari 只给 mp4。按优先级探测，取第一个受支持的。
const CANDIDATE_TYPES = [
  { mime: 'audio/webm;codecs=opus', ext: 'webm' },
  { mime: 'audio/webm', ext: 'webm' },
  { mime: 'audio/mp4', ext: 'mp4' },
  { mime: 'audio/ogg;codecs=opus', ext: 'ogg' },
]

function pickMimeType(): { mime: string; ext: string } | null {
  if (typeof MediaRecorder === 'undefined') return null
  for (const c of CANDIDATE_TYPES) {
    if (MediaRecorder.isTypeSupported(c.mime)) return c
  }
  return null
}

// isSupported 在组件挂载时用于决定话筒按钮是否可用。
export function isRecordingSupported(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    !!navigator.mediaDevices?.getUserMedia &&
    pickMimeType() !== null
  )
}

export type RecorderError = 'permission_denied' | 'no_device' | 'unsupported' | 'failed'

export function useRecorder() {
  const recording = ref(false)
  // 录音时长（秒），用于在输入框上显示计时
  const duration = ref(0)

  let recorder: MediaRecorder | null = null
  let stream: MediaStream | null = null
  let chunks: Blob[] = []
  let timer: number | null = null
  let ext = 'webm'

  function cleanup() {
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
    // 必须显式停掉轨道，否则浏览器标签页上的录音指示灯不会熄灭
    stream?.getTracks().forEach((tr) => tr.stop())
    stream = null
    recorder = null
    recording.value = false
  }

  // start 申请麦克风并开始录音。失败时抛出 RecorderError 字符串，
  // 由调用方映射为面向使用者的提示文案。
  async function start(): Promise<void> {
    const picked = pickMimeType()
    if (!picked) throw 'unsupported' as RecorderError

    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch (e: any) {
      // NotAllowedError：使用者拒绝或浏览器策略阻止
      // NotFoundError：没有可用的输入设备
      if (e?.name === 'NotAllowedError' || e?.name === 'SecurityError') {
        throw 'permission_denied' as RecorderError
      }
      if (e?.name === 'NotFoundError' || e?.name === 'DevicesNotFoundError') {
        throw 'no_device' as RecorderError
      }
      throw 'failed' as RecorderError
    }

    ext = picked.ext
    chunks = []
    try {
      recorder = new MediaRecorder(stream, { mimeType: picked.mime })
    } catch {
      cleanup()
      throw 'unsupported' as RecorderError
    }
    recorder.ondataavailable = (ev) => {
      if (ev.data.size > 0) chunks.push(ev.data)
    }
    recorder.start()
    recording.value = true
    duration.value = 0
    timer = window.setInterval(() => {
      duration.value += 1
    }, 1000)
  }

  // stop 停止录音并返回音频数据。未在录音中时返回 null。
  function stop(): Promise<{ blob: Blob; filename: string } | null> {
    return new Promise((resolve) => {
      if (!recorder || recorder.state === 'inactive') {
        cleanup()
        resolve(null)
        return
      }
      const mime = recorder.mimeType
      recorder.onstop = () => {
        const blob = new Blob(chunks, { type: mime })
        chunks = []
        cleanup()
        // 空 Blob 意味着点击过快、没采到样本，按「无录音」处理
        resolve(blob.size > 0 ? { blob, filename: `recording.${ext}` } : null)
      }
      recorder.stop()
    })
  }

  // cancel 丢弃本次录音，不触发转录。
  function cancel() {
    if (recorder && recorder.state !== 'inactive') {
      recorder.onstop = () => {
        chunks = []
        cleanup()
      }
      recorder.stop()
    } else {
      chunks = []
      cleanup()
    }
  }

  // 组件卸载时兜底释放麦克风，避免路由切换后指示灯常亮
  onBeforeUnmount(() => cancel())

  return { recording, duration, start, stop, cancel }
}
```

- [ ] **Step 3: 确认类型检查通过**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head -20`
Expected: 无与 `useRecorder.ts` 相关的报错

- [ ] **Step 4: 提交**

```bash
git add web/src/composables/useRecorder.ts
git commit -m "feat(web): 新增录音组合式函数"
```

---

## Task 13: 补齐两语言文案

**Files:**
- Modify: `web/src/i18n/messages/zh-cn.ts`
- Modify: `web/src/i18n/messages/en.ts`

先加文案，后面两个组件任务直接引用，避免中途出现 key 缺失的红字。

- [ ] **Step 1: 加中文文案**

在 `web/src/i18n/messages/zh-cn.ts` 的 `chat` 段内（`copyFailed` 之后、闭合括号之前）插入：

```ts
    recordStart: '语音输入',
    recordStop: '停止录音',
    recordCancel: '取消录音',
    recording: '录音中 {v}',
    transcribing: '识别中…',
    recordEmpty: '没有录到声音，请重试',
    recordNoPermission: '麦克风权限被拒绝，请在浏览器设置中允许后重试',
    recordNoDevice: '未检测到麦克风设备',
    recordUnsupported: '当前浏览器不支持录音',
    recordFailed: '录音失败，请重试',
    transcribeFailed: '语音识别失败：{msg}',
    recordNoModel: '尚未配置识别模型，请前往「设置 → 语音」选择模型',
```

在同文件 `settings` 段内插入（放在 `menuAccount` 之后，与菜单项相邻便于维护）：

```ts
    menuVoice: '语音',
    voiceEnabled: '启用语音输入',
    voiceEnabledDesc: '在输入框显示话筒按钮，录音后转为文字',
    voiceModel: '识别模型',
    voiceModelDesc: '用于音频转录的模型，需支持转录接口',
    voiceModelPlaceholder: '请选择模型',
    voiceAutoSend: '识别后自动发送',
    voiceAutoSendDesc: '开启后识别完成即发送，无需再点发送按钮',
    voiceSaved: '语音设置已保存',
    voiceSaveFailed: '保存失败：{msg}',
    voiceModelRequired: '启用语音输入前请先选择识别模型',
```

- [ ] **Step 2: 加英文文案**

在 `web/src/i18n/messages/en.ts` 的 `chat` 段对应位置插入：

```ts
    recordStart: 'Voice input',
    recordStop: 'Stop recording',
    recordCancel: 'Cancel recording',
    recording: 'Recording {v}',
    transcribing: 'Transcribing…',
    recordEmpty: 'No audio captured, please try again',
    recordNoPermission: 'Microphone access denied. Allow it in your browser settings and retry',
    recordNoDevice: 'No microphone detected',
    recordUnsupported: 'Recording is not supported in this browser',
    recordFailed: 'Recording failed, please try again',
    transcribeFailed: 'Transcription failed: {msg}',
    recordNoModel: 'No transcription model configured. Pick one under Settings → Voice',
```

`settings` 段对应位置插入：

```ts
    menuVoice: 'Voice',
    voiceEnabled: 'Enable voice input',
    voiceEnabledDesc: 'Show a microphone button in the composer and transcribe recordings',
    voiceModel: 'Transcription model',
    voiceModelDesc: 'Model used to transcribe audio; it must support the transcription endpoint',
    voiceModelPlaceholder: 'Select a model',
    voiceAutoSend: 'Send after transcription',
    voiceAutoSendDesc: 'Send the message as soon as transcription finishes',
    voiceSaved: 'Voice settings saved',
    voiceSaveFailed: 'Save failed: {msg}',
    voiceModelRequired: 'Select a transcription model before enabling voice input',
```

- [ ] **Step 3: 核对两语言 key 一致**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head -20`
Expected: 无报错

- [ ] **Step 4: 提交**

```bash
git add web/src/i18n/messages/
git commit -m "feat(web): 补齐语音输入文案"
```

---

## Task 14: 输入框话筒按钮

**Files:**
- Modify: `web/src/components/chat/ChatInput.vue`

话筒按钮放在模型下拉与发送按钮之间，按钮尺寸与形状复用既有的 `.action-btn` 样式。

- [ ] **Step 1: 加脚本逻辑**

在 `web/src/components/chat/ChatInput.vue` 的 import 段加：

```ts
import { Microphone, VideoPlay } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useRecorder, isRecordingSupported, type RecorderError } from '../../composables/useRecorder'
import { voiceApi, type VoiceSettings } from '../../api/voice'
```

在 `const selectedModel = ref('')` 之后加：

```ts
// 语音输入状态。配置在组件挂载时读一次；未启用则不渲染话筒按钮。
const voice = ref<VoiceSettings>({ enabled: false, model: '', auto_send: false })
const transcribing = ref(false)
const { recording, duration, start, stop, cancel } = useRecorder()
const recordSupported = isRecordingSupported()

// 话筒按钮的显示条件：后端启用 + 浏览器支持。
// 两者缺一就不渲染，而不是渲染成禁用态，避免留下一个永远点不动的按钮。
const showMic = computed(() => voice.value.enabled && recordSupported)

// 开关已开但没选模型：话筒显示警告态，点击只提示、不录音。
// 这种状态只有管理员改了设置才会出现，提示里直接指向设置页。
const micWarn = computed(() => voice.value.enabled && !voice.value.model)

// 麦克风权限被拒后置灰。浏览器会记住拒绝结果，再点也只会立刻失败，
// 置灰比反复弹同一条提示更明确。
const micDenied = ref(false)

// 录音计时的 mm:ss 展示
const durationText = computed(() => {
  const m = Math.floor(duration.value / 60)
  const s = duration.value % 60
  return `${m}:${String(s).padStart(2, '0')}`
})

const recorderErrorKey: Record<RecorderError, string> = {
  permission_denied: 'chat.recordNoPermission',
  no_device: 'chat.recordNoDevice',
  unsupported: 'chat.recordUnsupported',
  failed: 'chat.recordFailed',
}

async function startRecording() {
  if (micWarn.value) {
    ElMessage.warning(t('chat.recordNoModel'))
    return
  }
  try {
    await start()
  } catch (e) {
    const err = e as RecorderError
    if (err === 'permission_denied') micDenied.value = true
    ElMessage.warning(t(recorderErrorKey[err] || 'chat.recordFailed'))
  }
}

// stopRecording 停止录音并转录。识别结果追加到已有文本尾部，
// 不覆盖使用者先前手打的内容。
async function stopRecording() {
  const rec = await stop()
  if (!rec) {
    ElMessage.info(t('chat.recordEmpty'))
    return
  }
  transcribing.value = true
  try {
    // 不传 model，由服务端回落到当前配置的语音模型，避免页面缓存的旧模型名失效
    const res = await voiceApi.transcribe(rec.blob, rec.filename)
    const piece = res.text.trim()
    if (!piece) {
      ElMessage.info(t('chat.recordEmpty'))
      return
    }
    text.value = text.value ? `${text.value} ${piece}` : piece
    await syncScrollable()
    if (voice.value.auto_send && !props.sending) {
      handleSend()
    }
  } catch (e: any) {
    ElMessage.error(t('chat.transcribeFailed', { msg: e?.message || '' }))
  } finally {
    transcribing.value = false
  }
}

function cancelRecording() {
  cancel()
}
```

在既有的 `onMounted` 回调内末尾追加配置读取。读失败不打扰使用者，仅保持话筒隐藏：

```ts
  try {
    voice.value = await voiceApi.getSettings()
  } catch {
    // 配置读取失败按未启用处理，不弹错误：语音是增强功能，不该阻塞输入框
  }
```

- [ ] **Step 2: 加模板**

在模型下拉的 `</el-select>` 之后、停止/发送按钮之前插入。录音中给出停止与取消两个按钮，非录音态只给一个话筒：

```vue
          <!-- 录音中：左侧计时 + 取消，右侧停止并识别 -->
          <span v-if="recording" class="rec-timer">{{ t('chat.recording', { v: durationText }) }}</span>
          <el-button
            v-if="recording"
            circle
            class="action-btn"
            :title="t('chat.recordCancel')"
            :aria-label="t('chat.recordCancel')"
            @click="cancelRecording"
          >
            <el-icon :size="16"><Close /></el-icon>
          </el-button>
          <el-button
            v-if="recording"
            type="danger"
            circle
            class="action-btn"
            :title="t('chat.recordStop')"
            :aria-label="t('chat.recordStop')"
            @click="stopRecording"
          >
            <el-icon :size="16"><VideoPlay /></el-icon>
          </el-button>
          <el-button
            v-else-if="showMic"
            circle
            class="action-btn"
            :class="{ 'mic-warn': micWarn }"
            :loading="transcribing"
            :disabled="transcribing || props.sending || micDenied"
            :title="micWarn ? t('chat.recordNoModel') : transcribing ? t('chat.transcribing') : t('chat.recordStart')"
            :aria-label="micWarn ? t('chat.recordNoModel') : transcribing ? t('chat.transcribing') : t('chat.recordStart')"
            @click="startRecording"
          >
            <el-icon :size="16"><Microphone /></el-icon>
          </el-button>
```

同时给文本框加禁用：在 `<el-input>` 的 `@keydown.enter.exact.prevent="handleSend"` 属性旁加 `:disabled="transcribing"`。转录期间禁止编辑，避免识别文字追加到末尾时与手动输入交错。

- [ ] **Step 3: 加计时样式**

在 `<style scoped>` 内追加：

```css
.rec-timer {
  font-size: 12px;
  color: var(--el-color-danger);
  font-variant-numeric: tabular-nums;
  margin-right: 4px;
}
/* 开关已开但未选模型：话筒按警告色显示，提醒去设置页补配置 */
.mic-warn {
  color: var(--el-color-warning);
  border-color: var(--el-color-warning);
}
```

`font-variant-numeric: tabular-nums` 让秒数跳动时宽度不变，避免旁边的按钮随之抖动。

- [ ] **Step 4: 确认类型检查与构建通过**

Run: `cd web && npx vue-tsc --noEmit && npm run build 2>&1 | tail -5`
Expected: 类型检查无报错，构建成功

- [ ] **Step 5: 提交**

```bash
git add web/src/components/chat/ChatInput.vue
git commit -m "feat(web): 输入框新增语音录制按钮"
```

---

## Task 15: 设置面板语音分区

**Files:**
- Modify: `web/src/components/settings/SettingsModal.vue`

分区写法照 `general` 分区：`section` 字符串切换，行布局用既有的 `.row` / `.row-label` / `.label-title` / `.label-desc`。

- [ ] **Step 1: 注册菜单项**

在 `menuOptions` 的 `menuAccount` 之前插入（语音属功能配置，排在账户之前）：

```ts
  { label: t('settings.menuVoice'), key: 'voice' },
```

- [ ] **Step 2: 加脚本逻辑**

在 import 段加两行。`ElMessage` 该文件尚未引入；`models` 也未从 meta store 解构，需一并补上：

```ts
import { ElMessage } from 'element-plus'
import { voiceApi, type VoiceSettings } from '../../api/voice'
```

在 `const meta = useMetaStore()` 之后加：

```ts
const { models } = storeToRefs(meta)
```

在 `const section = ref<string>('general')` 附近加：

```ts
// 语音配置。面板打开时读一次，每次改动即时保存，与外观、语言的行为一致。
const voice = ref<VoiceSettings>({ enabled: false, model: '', auto_send: false })
const voiceSaving = ref(false)

// 可选的识别模型来自模型列表；语音接口要求模型已启用
const voiceModelOptions = computed(() =>
  (models.value || []).filter((m) => m.enabled).map((m) => ({ label: m.name, value: m.name }))
)

async function loadVoice() {
  try {
    voice.value = await voiceApi.getSettings()
  } catch {
    // 读不到按默认值处理，不弹错误
  }
}

// saveVoice 保存整个分区。开关打开但未选模型时拒绝保存并回滚开关，
// 否则会存下一个话筒一按就报错的状态。
async function saveVoice() {
  if (voice.value.enabled && !voice.value.model) {
    ElMessage.warning(t('settings.voiceModelRequired'))
    voice.value.enabled = false
    return
  }
  voiceSaving.value = true
  try {
    await voiceApi.saveSettings(voice.value)
    ElMessage.success(t('settings.voiceSaved'))
  } catch (e: any) {
    ElMessage.error(t('settings.voiceSaveFailed', { msg: e?.message || '' }))
    await loadVoice()
  } finally {
    voiceSaving.value = false
  }
}
```

面板打开时读配置。文件里已有 `watch(() => props.show, ...)` 在弹窗可见时调 `ensureLoaded()`，把它改成同时读语音配置：

```ts
watch(
  () => props.show,
  (v) => {
    if (v) {
      void ensureLoaded()
      void loadVoice()
    }
  }
)
```

- [ ] **Step 3: 加模板分区**

在 `section === 'account'` 分支之前插入：

```vue
        <!-- 语音：开关、识别模型、识别后自动发送 -->
        <div v-else-if="section === 'voice'" class="voice-panel">
          <div class="row">
            <div class="row-label">
              <div class="label-title">{{ t('settings.voiceEnabled') }}</div>
              <div class="label-desc">{{ t('settings.voiceEnabledDesc') }}</div>
            </div>
            <el-switch v-model="voice.enabled" :loading="voiceSaving" @change="saveVoice" />
          </div>
          <div class="row">
            <div class="row-label">
              <div class="label-title">{{ t('settings.voiceModel') }}</div>
              <div class="label-desc">{{ t('settings.voiceModelDesc') }}</div>
            </div>
            <el-select
              v-model="voice.model"
              style="width: 220px"
              clearable
              :placeholder="t('settings.voiceModelPlaceholder')"
              @change="saveVoice"
            >
              <el-option
                v-for="o in voiceModelOptions"
                :key="o.value"
                :label="o.label"
                :value="o.value"
              />
            </el-select>
          </div>
          <div class="row">
            <div class="row-label">
              <div class="label-title">{{ t('settings.voiceAutoSend') }}</div>
              <div class="label-desc">{{ t('settings.voiceAutoSendDesc') }}</div>
            </div>
            <el-switch v-model="voice.auto_send" :loading="voiceSaving" @change="saveVoice" />
          </div>
        </div>
```

- [ ] **Step 4: 确认类型检查与构建通过**

Run: `cd web && npx vue-tsc --noEmit && npm run build 2>&1 | tail -5`
Expected: 类型检查无报错，构建成功

- [ ] **Step 5: 提交**

```bash
git add web/src/components/settings/SettingsModal.vue
git commit -m "feat(web): 设置面板新增语音分区"
```

---

## Task 16: 更新使用手册

**Files:**
- Modify: `README.md`

按项目记忆的约定，手册只写对外 API，Web 自用端点不写。四条新路由里只有 `POST /audio/transcriptions` 是对外接口，另外三条 `/web/*` 不进手册。

- [ ] **Step 1: 找到接口章节位置**

Run: `grep -n "POST /chat\|## API\|### " README.md | head -20`

- [ ] **Step 2: 加转录接口说明**

在 `/chat` 接口之后插入：

````markdown
### 音频转录

`POST /audio/transcriptions`

把音频转成文字。请求为 `multipart/form-data`：

| 字段 | 必填 | 说明 |
|------|------|------|
| `file` | 是 | 音频文件，支持 webm、mp3、mp4、mpeg、mpga、m4a、wav、ogg、flac |
| `model` | 否 | 转录模型名，省略时用设置中配置的语音模型 |
| `language` | 否 | 音频语种的 ISO-639-1 代码，如 `zh`、`en`，可提升准确率 |

```bash
curl -X POST http://localhost:8080/audio/transcriptions \
  -H "Authorization: Bearer $GROOT_API_KEY" \
  -F "file=@recording.webm" \
  -F "model=whisper-1"
```

响应：

```json
{
  "text": "打开登录日志",
  "model": "whisper-1"
}
```

模型也可用请求头 `X-Model-Name` 指定，与 `/chat` 的约定一致。表单字段优先于请求头。
````

- [ ] **Step 3: 提交**

```bash
git add README.md
git commit -m "docs: 手册补充音频转录接口"
```

---

## Task 17: Python 示例客户端

**Files:**
- Modify: `examples/python/groot_client.py`
- Test: `tests/examples/python/test_groot_client.py`

按项目规范，示例的测试放在 `tests/examples/python/`，不放在 `examples/` 下。

- [ ] **Step 1: 在 mock 服务里加转录端点**

在 `tests/examples/python/test_groot_client.py` 的 `MockGrootHandler.do_POST` 中，`if self.path == "/chat":` 分支之后加：

```python
        elif self.path == "/audio/transcriptions":
            self._handle_transcription()
```

在 `_handle_chat` 方法之后加：

```python
    def _handle_transcription(self):
        """模拟 POST /audio/transcriptions：校验 multipart，回显 model 字段。"""
        ctype = self.headers.get("Content-Type", "")
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length) if length > 0 else b""
        if not ctype.startswith("multipart/form-data"):
            self._json_response(400, {"status": "invalid_request", "message": "expect multipart"})
            return
        if b'name="file"' not in body:
            self._json_response(400, {"status": "invalid_request", "message": "缺少 file 字段"})
            return
        # 从表单里取 model；客户端没传时模拟服务端回落到配置表里的值
        model = "whisper-1"
        marker = b'name="model"\r\n\r\n'
        if marker in body:
            start = body.index(marker) + len(marker)
            end = body.index(b"\r\n", start)
            model = body[start:end].decode()
        self._json_response(200, {"text": "你好，世界", "model": model})
```

- [ ] **Step 2: 写失败测试**

在文件顶部 import 区加 `import tempfile`。在 `TestGrootClient` 类末尾追加：

```python
    def _tmp_audio(self):
        f = tempfile.NamedTemporaryFile(suffix=".webm", delete=False)
        f.write(b"FAKE-AUDIO")
        f.close()
        self.addCleanup(os.unlink, f.name)
        return f.name

    def test_transcribe_default_model(self):
        result = self.client.transcribe(self._tmp_audio())
        self.assertEqual(result["text"], "你好，世界")
        # 未指定 model 时由服务端回落到配置的语音模型
        self.assertEqual(result["model"], "whisper-1")

    def test_transcribe_explicit_model(self):
        result = self.client.transcribe(self._tmp_audio(), model="my-asr", language="zh")
        self.assertEqual(result["model"], "my-asr")
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd tests/examples/python && python3 -m unittest test_groot_client -v 2>&1 | tail -15`
Expected: 两个 `test_transcribe_*` FAIL，报错 `AttributeError: 'GrootClient' object has no attribute 'transcribe'`

- [ ] **Step 4: 写客户端方法**

在 `examples/python/groot_client.py` 顶部 import 区加 `import os`（已有则跳过，重复导入亦无害）。在 `list_tools` 方法之后加：

```python
    def transcribe(self, audio_path: str, model: Optional[str] = None,
                   language: Optional[str] = None) -> dict:
        """音频转文字。返回 {"text": 识别文本, "model": 实际使用的模型}。

        model 省略时由服务端使用设置中配置的语音模型。
        """
        # 该接口是 multipart 表单。session 默认带 application/json 的 Content-Type，
        # 这里显式置 None 让 requests 按 files 自动生成 multipart 边界。
        data = {}
        if model:
            data["model"] = model
        if language:
            data["language"] = language
        with open(audio_path, "rb") as f:
            resp = self.session.post(
                f"{self.base_url}/audio/transcriptions",
                headers={"Content-Type": None},
                data=data,
                files={"file": (os.path.basename(audio_path), f)},
            )
        resp.raise_for_status()
        return resp.json()
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd tests/examples/python && python3 -m unittest test_groot_client -v 2>&1 | tail -5`
Expected: `OK`，含两个 `test_transcribe_*`

- [ ] **Step 6: 提交**

```bash
git add examples/python/groot_client.py tests/examples/python/test_groot_client.py
git commit -m "feat(examples): Python 客户端新增转录方法"
```

---

## Task 18: Java 示例客户端

**Files:**
- Modify: `examples/java/src/main/java/com/groot/client/GrootClient.java`
- Test: `tests/examples/java/com/groot/client/GrootClientTest.java`

`pom.xml` 已把 `testSourceDirectory` 指到 `tests/examples/java/`，测试直接写在那里。OkHttp 版本为 4.12.0，`RequestBody.create` 的参数顺序是 `(File, MediaType)`。

- [ ] **Step 1: 写失败测试**

在 `GrootClientTest.java` 的 import 区加：

```java
import okhttp3.mockwebserver.RecordedRequest;
import java.io.File;
import java.nio.file.Files;
```

在类末尾追加：

```java
    @Test
    void testTranscribe() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"text\":\"你好，世界\",\"model\":\"my-asr\"}")
                .addHeader("Content-Type", "application/json"));

        File audio = Files.createTempFile("rec", ".webm").toFile();
        audio.deleteOnExit();
        Files.write(audio.toPath(), "FAKE-AUDIO".getBytes());

        JsonNode resp = client.transcribe(audio, "my-asr", "zh");
        assertEquals("你好，世界", resp.get("text").asText());
        assertEquals("my-asr", resp.get("model").asText());

        RecordedRequest req = server.takeRequest();
        assertEquals("/audio/transcriptions", req.getPath());
        assertTrue(req.getHeader("Content-Type").startsWith("multipart/form-data"));
        String body = req.getBody().readUtf8();
        assertTrue(body.contains("name=\"file\""));
        assertTrue(body.contains("name=\"model\""));
        assertTrue(body.contains("name=\"language\""));
    }

    @Test
    void testTranscribeWithoutModel() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"text\":\"ok\",\"model\":\"whisper-1\"}")
                .addHeader("Content-Type", "application/json"));

        File audio = Files.createTempFile("rec", ".webm").toFile();
        audio.deleteOnExit();
        Files.write(audio.toPath(), "x".getBytes());

        client.transcribe(audio, null, null);

        // 不传 model / language 时表单里不应出现这两个字段，交由服务端回落
        String body = server.takeRequest().getBody().readUtf8();
        assertFalse(body.contains("name=\"model\""));
        assertFalse(body.contains("name=\"language\""));
    }
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd examples/java && mvn -q test -Dtest=GrootClientTest 2>&1 | grep -E "ERROR|cannot find symbol" | head -5`
Expected: 编译错误 `cannot find symbol ... method transcribe`

- [ ] **Step 3: 写客户端方法**

在 `GrootClient.java` 中 `listSessions` 方法之后加：

```java
    /**
     * 音频转文字。
     *
     * @param audioFile 音频文件（webm / mp3 / wav 等）
     * @param model     转录模型名；null 或空串表示使用服务端设置中配置的语音模型
     * @param language  语种提示（如 "zh"）；null 或空串表示不指定
     * @return 含 text（识别文本）与 model（实际使用的模型）的 JSON
     */
    public JsonNode transcribe(java.io.File audioFile, String model, String language) throws IOException {
        MultipartBody.Builder form = new MultipartBody.Builder()
                .setType(MultipartBody.FORM)
                .addFormDataPart("file", audioFile.getName(),
                        RequestBody.create(audioFile, MediaType.get("application/octet-stream")));
        if (model != null && !model.isEmpty()) {
            form.addFormDataPart("model", model);
        }
        if (language != null && !language.isEmpty()) {
            form.addFormDataPart("language", language);
        }
        Request req = new Request.Builder()
                .url(baseUrl + "/audio/transcriptions")
                .post(form.build())
                .build();
        return executeJson(req);
    }
```

`okhttp3.*` 已整体导入，`MultipartBody`、`RequestBody`、`MediaType` 无需再加 import。`executeJson` 是文件内已有的私有辅助方法，非 2xx 时抛 `IOException` 并带响应体。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd examples/java && mvn -q test 2>&1 | tail -8`
Expected: `BUILD SUCCESS`，`Tests run` 中无 failures

- [ ] **Step 5: 提交**

```bash
git add examples/java/src/main/java/com/groot/client/GrootClient.java tests/examples/java/com/groot/client/GrootClientTest.java
git commit -m "feat(examples): Java 客户端新增转录方法"
```

---

## Task 19: 对外转录接口系统测试

**Files:**
- Create: `tests/python/test_transcription.py`

系统测试由使用者自行运行。错误路径不依赖真实语音模型，可直接对着标准 `server` fixture 跑；成功路径需要一个真能转录的模型，用环境变量 `GROOT_VOICE_MODEL` 指定，未设置时跳过。

`conftest.py` 的 `api_headers` 固定带 `Content-Type: application/json`，multipart 请求不能用它，这里直接用 `api_key` fixture 自行拼请求头。

- [ ] **Step 1: 写测试**

```python
"""对外音频转录接口系统测试（POST /audio/transcriptions）。

运行前提：groot 服务已启动（见 conftest 的 server fixture）。
环境变量：
  GROOT_VOICE_MODEL  一个已在设置中创建且能转录的模型名；不设置时跳过成功用例。

用例点：
- 缺少 file 字段（400 invalid_request）
- 不支持的扩展名（400 unsupported_type）
- 指定不存在的模型（400 invalid_model）
- 超过附件单文件上限（400 file_too_large）
- 未携带 API Key（401）
- 正常转录（需 GROOT_VOICE_MODEL）
"""
import os

import pytest
import requests

from conftest import BASE_URL

URL = f"{BASE_URL}/audio/transcriptions"
VOICE_MODEL = os.environ.get("GROOT_VOICE_MODEL", "")


@pytest.fixture
def key_headers(api_key):
    """只带 API Key，不带 Content-Type：multipart 边界由 requests 自动生成"""
    return {"X-API-Key": api_key}


def _audio(name="rec.webm", size=16):
    return {"file": (name, b"\x00" * size, "audio/webm")}


class TestTranscriptionErrors:
    """错误路径，不依赖真实语音模型"""

    def test_missing_file(self, server, key_headers):
        """TC-ASR-001: 缺少 file 字段"""
        resp = requests.post(URL, headers=key_headers, data={"model": "x"}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_unsupported_extension(self, server, key_headers):
        """TC-ASR-002: 扩展名不在音频白名单"""
        resp = requests.post(URL, headers=key_headers, files=_audio("notes.txt"), timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "unsupported_type"

    def test_unknown_model(self, server, key_headers):
        """TC-ASR-003: 指定不存在的模型"""
        resp = requests.post(
            URL, headers=key_headers,
            files=_audio(), data={"model": "__no_such_model__"}, timeout=10,
        )
        assert resp.status_code == 400
        body = resp.json()
        assert body["status"] == "invalid_model"
        assert "__no_such_model__" in body["message"]

    def test_file_too_large(self, server, key_headers):
        """TC-ASR-004: 超过附件单文件上限（默认 50MB）"""
        big = {"file": ("big.webm", b"\x00" * (51 * 1024 * 1024), "audio/webm")}
        resp = requests.post(
            URL, headers=key_headers,
            files=big, data={"model": "__no_such_model__"}, timeout=60,
        )
        # 大小校验先于模型校验，因此这里应命中 file_too_large 而非 invalid_model
        assert resp.status_code == 400
        assert resp.json()["status"] == "file_too_large"

    def test_no_auth(self, server):
        """TC-ASR-005: 未携带 API Key"""
        resp = requests.post(URL, files=_audio(), timeout=10)
        assert resp.status_code in (401, 403)


@pytest.mark.skipif(not VOICE_MODEL, reason="未设置 GROOT_VOICE_MODEL")
class TestTranscriptionSuccess:
    """成功路径，需要真实的语音模型"""

    def test_transcribe_wav(self, server, key_headers):
        """TC-ASR-006: 用指定模型转录一段极短的静音 wav"""
        # 44 字节的 PCM WAV 头 + 少量静音采样，足以让接口走通
        header = (
            b"RIFF" + (36 + 320).to_bytes(4, "little") + b"WAVE"
            b"fmt " + (16).to_bytes(4, "little") + (1).to_bytes(2, "little")
            + (1).to_bytes(2, "little") + (16000).to_bytes(4, "little")
            + (32000).to_bytes(4, "little") + (2).to_bytes(2, "little")
            + (16).to_bytes(2, "little")
            + b"data" + (320).to_bytes(4, "little")
        )
        wav = header + b"\x00" * 320
        resp = requests.post(
            URL, headers=key_headers,
            files={"file": ("silence.wav", wav, "audio/wav")},
            data={"model": VOICE_MODEL},
            timeout=120,
        )
        # 静音可能被上游判为无内容（400）或返回空串/噪声文本（200），两者都算接口打通
        assert resp.status_code in (200, 400), resp.text
        if resp.status_code == 200:
            body = resp.json()
            assert "text" in body
            assert body["model"] == VOICE_MODEL
```

- [ ] **Step 2: 在启动的服务上跑一遍错误路径**

Run: `cd tests/python && pytest test_transcription.py -v -k Errors 2>&1 | tail -12`
Expected: 5 个用例 PASS

- [ ] **Step 3: 提交**

```bash
git add tests/python/test_transcription.py
git commit -m "test: 新增对外转录接口系统测试"
```

---

## Task 20: 更新测试用例汇总

**Files:**
- Modify: `tests/TEST_CASES.md`

- [ ] **Step 1: 加 Go 单元测试段落**

在 `### 1.4 文件面板测试` 段落末尾的 `---` 之后插入：

```markdown
### 1.5 语音输入与配置表测试

位于 `internal/repo/settingdb/setting_test.go`、`internal/setting/settings_test.go`、
`internal/llm/transcription_test.go`、`internal/api/handler/transcription_test.go`
与 `internal/api/handler/setting_test.go`。

覆盖点：

- 配置表仓库：写入回读、主键冲突覆盖、未找到返回 ErrNotFound、`global` 作用域拒绝 scope_id、按作用域批量查询与排序、删除幂等
- 配置对象：表为空回落代码默认值、部分键覆盖、布尔解析（true/false/1/0/脏数据）、`SetVoice` 回写、YAML 分类透传、仓库为 nil 时用默认值
- 转录客户端：multipart 字段与文件名、`/v1` 补齐与不重复补齐、language 为空不下发、上游错误透传原文、空文本判错
- 转录 handler：成功响应含 text 与 model、缺 file、模型未配置、模型不存在、表单 model 优先、请求头 X-Model-Name、扩展名白名单、上游 502 透传、按 MB 换算的大小上限
- 设置 handler：默认值读取、整组保存回读、model 为空允许、模型不存在或已禁用拒绝、非法 JSON

---
```

- [ ] **Step 2: 加 Python 系统测试段落**

Run: `grep -n "^### 2.13" tests/TEST_CASES.md`

在 2.13 节的表格之后、下一个标题之前插入：

```markdown
### 2.14 语音转录测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestTranscriptionErrors | test_transcription.py | 缺文件、扩展名、模型不存在、超大文件、未鉴权 |
| TestTranscriptionSuccess | test_transcription.py | 真实模型转录（需 GROOT_VOICE_MODEL） |
```

- [ ] **Step 3: 提交**

```bash
git add tests/TEST_CASES.md
git commit -m "docs: 测试用例汇总补充语音输入"
```

---

## 收尾验证

- [ ] **Step 1: 全量后端测试**

Run: `go test ./internal/... 2>&1 | grep -v "^ok" | head -20`
Expected: 无 FAIL 行

- [ ] **Step 2: 前端类型检查与构建**

Run: `cd web && npx vue-tsc --noEmit && npm run build`
Expected: 均成功

- [ ] **Step 3: 编译产物落到 dist/**

Run: `go build -o dist/groot ./cmd && ls -lh dist/groot`
Expected: 二进制生成成功

- [ ] **Step 4: 手工验证一遍真实链路**

启动服务，在设置面板开启语音输入并选一个转录模型，回到聊天页点话筒说一句话，确认：

1. 话筒按钮出现，点击后变为计时 + 取消 + 停止三个控件
2. 停止后按钮转 loading，识别文本追加到输入框
3. 开启「识别后自动发送」后，识别完成即发出消息
4. 浏览器标签页的录音指示灯在停止或取消后熄灭
5. 拒绝麦克风权限时给出明确提示，而不是静默失败






