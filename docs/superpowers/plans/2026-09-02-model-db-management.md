# 模型配置数据库化管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 模型配置以数据库 models 表为唯一存储，WebUI 设置界面提供模型的增删改查、设默认、启用/禁用与连接测试，变更立即生效。

**Architecture:** 新增 `models` 表（三方言 DDL）→ `repo.ModelRepo` 接口 + `modeldb` 实现 → `llm.ModelService` 业务层（校验/默认规则/脱敏/连接测试）→ engine/executor/subagent/handler 全部改为持有 ModelService、每次直查数据库。config.yaml 彻底移除 llm 段，允许零模型启动。

**Tech Stack:** Go + sqlx（sqlite/mysql/postgres）、Hertz、eino/eino-ext、Vue 3 + Element Plus + Pinia。

**Spec:** `docs/superpowers/specs/2026-09-02-model-db-management-design.md`

**⚠️ Git 提交规范（项目 CLAUDE.md 强制）：所有 git commit 必须由用户明确请求，本计划任何步骤都不执行 git commit。每个任务以"编译通过 + 测试通过"作为完成标志，全部完成后由用户决定提交。**

**每个任务结束时必须运行 `go build ./...` 确认全仓编译通过（前端任务除外）。**

---

### Task 1: models 表 DDL（三方言）

**Files:**
- Modify: `internal/db/migrate.go`

- [ ] **Step 1: sqlite DDL 追加**

在 `sqliteDDL()` 返回的切片末尾（`uk_users_username` 之后）追加两个元素：

```go
		`CREATE TABLE IF NOT EXISTS models (
			id                    INTEGER PRIMARY KEY AUTOINCREMENT,
			name                  TEXT NOT NULL,
			base_url              TEXT NOT NULL,
			api_key               TEXT NOT NULL,
			model                 TEXT NOT NULL,
			max_completion_tokens INTEGER NOT NULL DEFAULT 0,
			max_context_tokens    INTEGER NOT NULL DEFAULT 0,
			temperature           REAL NOT NULL DEFAULT 0.7,
			top_p                 REAL NOT NULL DEFAULT 1.0,
			frequency_penalty     REAL NOT NULL DEFAULT 0,
			presence_penalty      REAL NOT NULL DEFAULT 0,
			seed                  INTEGER NOT NULL DEFAULT 0,
			stop                  TEXT NOT NULL DEFAULT '[]',
			thinking              INTEGER NOT NULL DEFAULT 0,
			is_default            INTEGER NOT NULL DEFAULT 0,
			enabled               INTEGER NOT NULL DEFAULT 1,
			created_at            INTEGER NOT NULL,
			updated_at            INTEGER NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_models_name ON models(name)`,
```

- [ ] **Step 2: mysql DDL 追加**

在 `mysqlDDL()` 末尾追加：

```go
		`CREATE TABLE IF NOT EXISTS models (
			id                    BIGINT PRIMARY KEY AUTO_INCREMENT,
			name                  VARCHAR(64)  NOT NULL,
			base_url              VARCHAR(255) NOT NULL,
			api_key               VARCHAR(512) NOT NULL,
			model                 VARCHAR(128) NOT NULL,
			max_completion_tokens INT     NOT NULL DEFAULT 0,
			max_context_tokens    INT     NOT NULL DEFAULT 0,
			temperature           DOUBLE  NOT NULL DEFAULT 0.7,
			top_p                 DOUBLE  NOT NULL DEFAULT 1.0,
			frequency_penalty     DOUBLE  NOT NULL DEFAULT 0,
			presence_penalty      DOUBLE  NOT NULL DEFAULT 0,
			seed                  INT     NOT NULL DEFAULT 0,
			stop                  TEXT    NOT NULL,
			thinking              TINYINT(1) NOT NULL DEFAULT 0,
			is_default            TINYINT(1) NOT NULL DEFAULT 0,
			enabled               TINYINT(1) NOT NULL DEFAULT 1,
			created_at            BIGINT NOT NULL,
			updated_at            BIGINT NOT NULL,
			UNIQUE KEY uk_models_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
```

（MySQL 的 TEXT 列不支持 DEFAULT，stop 由应用层保证写入 `[]`。）

- [ ] **Step 3: postgres DDL 追加**

在 `postgresDDL()` 末尾追加：

```go
		`CREATE TABLE IF NOT EXISTS models (
			id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			name                  VARCHAR(64)  NOT NULL,
			base_url              VARCHAR(255) NOT NULL,
			api_key               VARCHAR(512) NOT NULL,
			model                 VARCHAR(128) NOT NULL,
			max_completion_tokens INTEGER NOT NULL DEFAULT 0,
			max_context_tokens    INTEGER NOT NULL DEFAULT 0,
			temperature           DOUBLE PRECISION NOT NULL DEFAULT 0.7,
			top_p                 DOUBLE PRECISION NOT NULL DEFAULT 1.0,
			frequency_penalty     DOUBLE PRECISION NOT NULL DEFAULT 0,
			presence_penalty      DOUBLE PRECISION NOT NULL DEFAULT 0,
			seed                  INTEGER NOT NULL DEFAULT 0,
			stop                  TEXT NOT NULL DEFAULT '[]',
			thinking              BOOLEAN NOT NULL DEFAULT FALSE,
			is_default            BOOLEAN NOT NULL DEFAULT FALSE,
			enabled               BOOLEAN NOT NULL DEFAULT TRUE,
			created_at            BIGINT NOT NULL,
			updated_at            BIGINT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_models_name ON models(name)`,
```

- [ ] **Step 4: 验证**

Run: `go test ./internal/db/... -v`
Expected: PASS（现有 db_test 会执行 Migrate，新 DDL 语法错误会在此暴露）

---

### Task 2: repo 层 Model 结构与 ModelRepo 接口

**Files:**
- Create: `internal/repo/model.go`

- [ ] **Step 1: 定义结构与接口**

```go
package repo

import (
	"context"
	"time"
)

// Model LLM 模型配置（唯一存储于数据库 models 表）
type Model struct {
	ID                  int64
	Name                string // 逻辑名称，全局唯一，聊天请求按此引用
	BaseURL             string
	APIKey              string // 明文存储，支持 ${ENV_VAR} 引用
	Model               string // 实际模型 ID
	MaxCompletionTokens int
	MaxContextTokens    int // 输入上下文 token 预算（0 表示不限制）
	Temperature         float64
	TopP                float64
	FrequencyPenalty    float64
	PresencePenalty     float64
	Seed                int
	Stop                []string
	Thinking            bool
	IsDefault           bool // 全表至多一条为真
	Enabled             bool // 禁用后不出现在聊天下拉框
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ModelRepo 模型配置存储接口
type ModelRepo interface {
	Create(ctx context.Context, m *Model) error
	// GetByName 按名称查询，未找到返回 ErrNotFound
	GetByName(ctx context.Context, name string) (*Model, error)
	// GetDefault 查询默认模型，无默认返回 ErrNotFound
	GetDefault(ctx context.Context) (*Model, error)
	// List 返回全部模型，按 name 升序
	List(ctx context.Context) ([]*Model, error)
	// Update 按原名称 name 整行更新（含重命名为 m.Name）；未找到返回 ErrNotFound
	Update(ctx context.Context, name string, m *Model) error
	// Delete 按名称删除；未找到返回 ErrNotFound
	Delete(ctx context.Context, name string) error
	// SetDefault 事务内先清除全表 is_default 再设置目标行；未找到返回 ErrNotFound
	SetDefault(ctx context.Context, name string) error
	Count(ctx context.Context) (int64, error)
}
```

- [ ] **Step 2: 验证**

Run: `go build ./...`
Expected: 编译通过

---

### Task 3: modeldb 实现 + 单元测试

**Files:**
- Create: `internal/repo/modeldb/model.go`
- Test: `internal/repo/modeldb/model_test.go`

- [ ] **Step 1: 写失败的测试**

仿照 `internal/repo/userdb/user_test.go` 的模式（`db.Open(nil, t.TempDir())`）：

```go
package modeldb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.ModelRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func newModel(name string) *repo.Model {
	now := time.Now()
	return &repo.Model{
		Name:                name,
		BaseURL:             "https://api.openai.com/v1",
		APIKey:              "sk-test-1234abcd",
		Model:               "gpt-4o",
		MaxCompletionTokens: 4096,
		Temperature:         0.7,
		TopP:                1.0,
		Stop:                []string{},
		Enabled:             true,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func TestModelRepo_CreateAndGet(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	m := newModel("gpt-4o")
	m.Stop = []string{"\n\n", "END"}
	if err := r.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := r.GetByName(ctx, "gpt-4o")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.Name != "gpt-4o" || got.BaseURL != m.BaseURL || got.APIKey != m.APIKey {
		t.Errorf("GetByName mismatch: %+v", got)
	}
	if len(got.Stop) != 2 || got.Stop[0] != "\n\n" {
		t.Errorf("Stop 反序列化错误: %v", got.Stop)
	}
	if !got.Enabled || got.IsDefault {
		t.Errorf("bool 字段错误: enabled=%v is_default=%v", got.Enabled, got.IsDefault)
	}
}

func TestModelRepo_GetByName_NotFound(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.GetByName(context.Background(), "nope"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestModelRepo_UniqueName(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.Create(ctx, newModel("dup")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Create(ctx, newModel("dup")); err == nil {
		t.Error("重名创建应当失败")
	}
}

func TestModelRepo_ListOrdered(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	r.Create(ctx, newModel("b-model"))
	r.Create(ctx, newModel("a-model"))
	list, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Name != "a-model" {
		t.Errorf("List 应按 name 升序: %v", list)
	}
}

func TestModelRepo_UpdateAndRename(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	r.Create(ctx, newModel("old-name"))

	m := newModel("new-name")
	m.Temperature = 1.5
	if err := r.Update(ctx, "old-name", m); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := r.GetByName(ctx, "old-name"); !errors.Is(err, repo.ErrNotFound) {
		t.Error("旧名称应当查不到")
	}
	got, err := r.GetByName(ctx, "new-name")
	if err != nil || got.Temperature != 1.5 {
		t.Errorf("重命名后查询失败: %v, %+v", err, got)
	}

	if err := r.Update(ctx, "ghost", newModel("x")); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("更新不存在的模型应返回 ErrNotFound, got %v", err)
	}
}

func TestModelRepo_Delete(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	r.Create(ctx, newModel("m1"))
	if err := r.Delete(ctx, "m1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.GetByName(ctx, "m1"); !errors.Is(err, repo.ErrNotFound) {
		t.Error("删除后应查不到")
	}
	if err := r.Delete(ctx, "m1"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("删除不存在的模型应返回 ErrNotFound, got %v", err)
	}
}

func TestModelRepo_SetDefault(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	r.Create(ctx, newModel("m1"))
	r.Create(ctx, newModel("m2"))

	if _, err := r.GetDefault(ctx); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("无默认时 GetDefault 应返回 ErrNotFound, got %v", err)
	}

	if err := r.SetDefault(ctx, "m1"); err != nil {
		t.Fatalf("SetDefault m1: %v", err)
	}
	d, err := r.GetDefault(ctx)
	if err != nil || d.Name != "m1" {
		t.Fatalf("GetDefault: %v, %+v", err, d)
	}

	// 切换默认：全表仍只有一个 is_default
	if err := r.SetDefault(ctx, "m2"); err != nil {
		t.Fatalf("SetDefault m2: %v", err)
	}
	list, _ := r.List(ctx)
	count := 0
	for _, m := range list {
		if m.IsDefault {
			count++
			if m.Name != "m2" {
				t.Errorf("默认模型应为 m2, got %s", m.Name)
			}
		}
	}
	if count != 1 {
		t.Errorf("默认模型应有且只有 1 个, got %d", count)
	}

	if err := r.SetDefault(ctx, "ghost"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("SetDefault 不存在模型应返回 ErrNotFound, got %v", err)
	}
}

func TestModelRepo_Count(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	n, _ := r.Count(ctx)
	if n != 0 {
		t.Errorf("初始 Count 应为 0, got %d", n)
	}
	r.Create(ctx, newModel("m1"))
	n, _ = r.Count(ctx)
	if n != 1 {
		t.Errorf("Count 应为 1, got %d", n)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/repo/modeldb/... -v`
Expected: FAIL（`New` 未定义，编译错误）

- [ ] **Step 3: 实现 modeldb**

```go
package modeldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type modelRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.ModelRepo {
	return &modelRepo{db: sqlxDB, dialect: dialect}
}

type modelRow struct {
	ID                  int64   `db:"id"`
	Name                string  `db:"name"`
	BaseURL             string  `db:"base_url"`
	APIKey              string  `db:"api_key"`
	Model               string  `db:"model"`
	MaxCompletionTokens int     `db:"max_completion_tokens"`
	MaxContextTokens    int     `db:"max_context_tokens"`
	Temperature         float64 `db:"temperature"`
	TopP                float64 `db:"top_p"`
	FrequencyPenalty    float64 `db:"frequency_penalty"`
	PresencePenalty     float64 `db:"presence_penalty"`
	Seed                int     `db:"seed"`
	Stop                string  `db:"stop"`
	Thinking            bool    `db:"thinking"`
	IsDefault           bool    `db:"is_default"`
	Enabled             bool    `db:"enabled"`
	CreatedAt           int64   `db:"created_at"`
	UpdatedAt           int64   `db:"updated_at"`
}

const modelColumns = `id, name, base_url, api_key, model, max_completion_tokens, max_context_tokens,
	temperature, top_p, frequency_penalty, presence_penalty, seed, stop, thinking,
	is_default, enabled, created_at, updated_at`

func rowToModel(row modelRow) *repo.Model {
	var stop []string
	// stop 序列化损坏时按空数组处理，不让单行脏数据拖垮整个列表
	if err := json.Unmarshal([]byte(row.Stop), &stop); err != nil {
		stop = []string{}
	}
	return &repo.Model{
		ID:                  row.ID,
		Name:                row.Name,
		BaseURL:             row.BaseURL,
		APIKey:              row.APIKey,
		Model:               row.Model,
		MaxCompletionTokens: row.MaxCompletionTokens,
		MaxContextTokens:    row.MaxContextTokens,
		Temperature:         row.Temperature,
		TopP:                row.TopP,
		FrequencyPenalty:    row.FrequencyPenalty,
		PresencePenalty:     row.PresencePenalty,
		Seed:                row.Seed,
		Stop:                stop,
		Thinking:            row.Thinking,
		IsDefault:           row.IsDefault,
		Enabled:             row.Enabled,
		CreatedAt:           time.UnixMilli(row.CreatedAt),
		UpdatedAt:           time.UnixMilli(row.UpdatedAt),
	}
}

func stopJSON(stop []string) string {
	if stop == nil {
		stop = []string{}
	}
	b, err := json.Marshal(stop)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (r *modelRepo) Create(ctx context.Context, m *repo.Model) error {
	q := r.db.Rebind(`INSERT INTO models (name, base_url, api_key, model,
		max_completion_tokens, max_context_tokens, temperature, top_p,
		frequency_penalty, presence_penalty, seed, stop, thinking,
		is_default, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		m.Name, m.BaseURL, m.APIKey, m.Model,
		m.MaxCompletionTokens, m.MaxContextTokens, m.Temperature, m.TopP,
		m.FrequencyPenalty, m.PresencePenalty, m.Seed, stopJSON(m.Stop), m.Thinking,
		m.IsDefault, m.Enabled, m.CreatedAt.UnixMilli(), m.UpdatedAt.UnixMilli(),
	)
	return err
}

func (r *modelRepo) GetByName(ctx context.Context, name string) (*repo.Model, error) {
	var row modelRow
	q := r.db.Rebind(`SELECT ` + modelColumns + ` FROM models WHERE name=?`)
	err := r.db.GetContext(ctx, &row, q, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToModel(row), nil
}

func (r *modelRepo) GetDefault(ctx context.Context) (*repo.Model, error) {
	var row modelRow
	q := r.db.Rebind(`SELECT ` + modelColumns + ` FROM models WHERE is_default=?`)
	err := r.db.GetContext(ctx, &row, q, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToModel(row), nil
}

func (r *modelRepo) List(ctx context.Context) ([]*repo.Model, error) {
	var rows []modelRow
	if err := r.db.SelectContext(ctx, &rows, `SELECT `+modelColumns+` FROM models ORDER BY name ASC`); err != nil {
		return nil, err
	}
	result := make([]*repo.Model, 0, len(rows))
	for _, row := range rows {
		result = append(result, rowToModel(row))
	}
	return result, nil
}

func (r *modelRepo) Update(ctx context.Context, name string, m *repo.Model) error {
	q := r.db.Rebind(`UPDATE models SET name=?, base_url=?, api_key=?, model=?,
		max_completion_tokens=?, max_context_tokens=?, temperature=?, top_p=?,
		frequency_penalty=?, presence_penalty=?, seed=?, stop=?, thinking=?,
		enabled=?, updated_at=? WHERE name=?`)
	res, err := r.db.ExecContext(ctx, q,
		m.Name, m.BaseURL, m.APIKey, m.Model,
		m.MaxCompletionTokens, m.MaxContextTokens, m.Temperature, m.TopP,
		m.FrequencyPenalty, m.PresencePenalty, m.Seed, stopJSON(m.Stop), m.Thinking,
		m.Enabled, time.Now().UnixMilli(), name,
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

func (r *modelRepo) Delete(ctx context.Context, name string) error {
	q := r.db.Rebind(`DELETE FROM models WHERE name=?`)
	res, err := r.db.ExecContext(ctx, q, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (r *modelRepo) SetDefault(ctx context.Context, name string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE models SET is_default=? WHERE is_default=?`), false, true); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE models SET is_default=?, updated_at=? WHERE name=?`),
		true, time.Now().UnixMilli(), name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return tx.Commit()
}

func (r *modelRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM models`); err != nil {
		return 0, err
	}
	return n, nil
}
```

注意：`Update` 故意不更新 `is_default` 列——默认标记只能通过 `SetDefault` 变更。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/repo/modeldb/... -v`
Expected: 全部 PASS

---

### Task 4: repofactory 注入 Model 仓库

**Files:**
- Modify: `internal/repo/repofactory/factory.go`

- [ ] **Step 1: Repos 增加 Model 字段**

import 增加 `"github.com/zfd81/groot/internal/repo/modeldb"`；`Repos` 结构体增加字段 `Model repo.ModelRepo`；`NewRepos` 返回值中增加 `Model: modeldb.New(sqlxDB, dialect),`。

- [ ] **Step 2: 验证**

Run: `go build ./...`
Expected: 编译通过

---

### Task 5: llm.ModelService 业务层 + 单元测试

**Files:**
- Create: `internal/llm/service.go`
- Test: `internal/llm/service_test.go`

本任务不改动 `chatmodel.go`（签名改造在 Task 6 与消费方一起完成）。

- [ ] **Step 1: 写失败的测试**

```go
package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
)

func newTestService(t *testing.T) *ModelService {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return NewModelService(modeldb.New(sqlxDB, dialect))
}

func validModel(name string) *repo.Model {
	return &repo.Model{
		Name:        name,
		BaseURL:     "https://api.openai.com/v1",
		APIKey:      "sk-test-1234abcd",
		Model:       "gpt-4o",
		Temperature: 0.7,
		TopP:        1.0,
		Enabled:     true,
	}
}

func TestModelService_CreateFirstBecomesDefault(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m, err := s.GetByName(ctx, "")
	if err != nil || m.Name != "m1" {
		t.Fatalf("首个模型应自动成为默认: %v, %+v", err, m)
	}

	// 第二个模型不抢默认
	if err := s.Create(ctx, validModel("m2")); err != nil {
		t.Fatalf("Create m2: %v", err)
	}
	m, _ = s.GetByName(ctx, "")
	if m.Name != "m1" {
		t.Errorf("默认模型应仍为 m1, got %s", m.Name)
	}
}

func TestModelService_CreateValidation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	cases := []struct {
		mutate func(*repo.Model)
		desc   string
	}{
		{func(m *repo.Model) { m.Name = "" }, "空名称"},
		{func(m *repo.Model) { m.BaseURL = "" }, "空 base_url"},
		{func(m *repo.Model) { m.APIKey = "" }, "空 api_key"},
		{func(m *repo.Model) { m.Model = "" }, "空 model"},
		{func(m *repo.Model) { m.Temperature = 2.5 }, "temperature 超界"},
		{func(m *repo.Model) { m.TopP = 1.5 }, "top_p 超界"},
		{func(m *repo.Model) { m.FrequencyPenalty = -3 }, "frequency_penalty 超界"},
		{func(m *repo.Model) { m.PresencePenalty = 3 }, "presence_penalty 超界"},
	}
	for _, c := range cases {
		m := validModel("bad")
		c.mutate(m)
		if err := s.Create(ctx, m); !errors.Is(err, ErrInvalidModel) {
			t.Errorf("%s: want ErrInvalidModel, got %v", c.desc, err)
		}
	}
}

func TestModelService_CreateDuplicateName(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	s.Create(ctx, validModel("dup"))
	if err := s.Create(ctx, validModel("dup")); !errors.Is(err, ErrNameExists) {
		t.Errorf("want ErrNameExists, got %v", err)
	}
}

func TestModelService_GetByName(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	// 空库、无默认
	if _, err := s.GetByName(ctx, ""); !errors.Is(err, ErrNoDefaultModel) {
		t.Errorf("want ErrNoDefaultModel, got %v", err)
	}
	if _, err := s.GetByName(ctx, "nope"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}

	s.Create(ctx, validModel("m1"))
	s.Create(ctx, validModel("m2"))

	// 禁用后按名称获取报 ErrModelDisabled
	m2, _ := s.GetByName(ctx, "m2")
	m2.Enabled = false
	m2.APIKey = "" // 留空 = 不修改
	if err := s.Update(ctx, "m2", m2); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := s.GetByName(ctx, "m2"); !errors.Is(err, ErrModelDisabled) {
		t.Errorf("want ErrModelDisabled, got %v", err)
	}
}

func TestModelService_GetByNameExpandsEnv(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	t.Setenv("GROOT_TEST_KEY", "sk-from-env")

	m := validModel("env-model")
	m.APIKey = "${GROOT_TEST_KEY}"
	s.Create(ctx, m)

	got, err := s.GetByName(ctx, "env-model")
	if err != nil || got.APIKey != "sk-from-env" {
		t.Errorf("APIKey 应展开环境变量: %v, %q", err, got.APIKey)
	}
}

func TestModelService_UpdateKeepsAPIKeyWhenEmpty(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	s.Create(ctx, validModel("m1"))

	upd := validModel("m1")
	upd.APIKey = ""
	upd.Temperature = 1.2
	if err := s.Update(ctx, "m1", upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.GetByName(ctx, "m1")
	if got.APIKey != "sk-test-1234abcd" {
		t.Errorf("api_key 留空应保持原值, got %q", got.APIKey)
	}
	if got.Temperature != 1.2 {
		t.Errorf("temperature 应更新为 1.2, got %v", got.Temperature)
	}
}

func TestModelService_UpdateRenameConflict(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	s.Create(ctx, validModel("m1"))
	s.Create(ctx, validModel("m2"))

	upd := validModel("m2") // 把 m1 改名为已存在的 m2
	if err := s.Update(ctx, "m1", upd); !errors.Is(err, ErrNameExists) {
		t.Errorf("want ErrNameExists, got %v", err)
	}
}

func TestModelService_DefaultProtection(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	s.Create(ctx, validModel("m1")) // 自动默认
	s.Create(ctx, validModel("m2"))

	// 默认模型禁止删除
	if err := s.Delete(ctx, "m1"); !errors.Is(err, ErrDefaultProtected) {
		t.Errorf("删除默认模型应被拒绝, got %v", err)
	}
	// 默认模型禁止禁用
	upd := validModel("m1")
	upd.APIKey = ""
	upd.Enabled = false
	if err := s.Update(ctx, "m1", upd); !errors.Is(err, ErrDefaultProtected) {
		t.Errorf("禁用默认模型应被拒绝, got %v", err)
	}
	// 切换默认后即可删除
	if err := s.SetDefault(ctx, "m2"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if err := s.Delete(ctx, "m1"); err != nil {
		t.Errorf("非默认模型应可删除: %v", err)
	}
}

func TestModelService_SetDefaultRejectsDisabled(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	s.Create(ctx, validModel("m1"))
	s.Create(ctx, validModel("m2"))

	upd := validModel("m2")
	upd.APIKey = ""
	upd.Enabled = false
	s.Update(ctx, "m2", upd)

	if err := s.SetDefault(ctx, "m2"); !errors.Is(err, ErrModelDisabled) {
		t.Errorf("禁用模型不可设为默认, got %v", err)
	}
	if err := s.SetDefault(ctx, "ghost"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}
}

func TestMaskAPIKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sk-abcdefgh1234", "****1234"},
		{"short", "****"},
		{"", ""},
		{"${OPENAI_API_KEY}", "${OPENAI_API_KEY}"}, // 环境变量引用原样展示
	}
	for _, c := range cases {
		if got := MaskAPIKey(c.in); got != c.want {
			t.Errorf("MaskAPIKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/llm/... -v`
Expected: FAIL（`ModelService` 等未定义，编译错误）

- [ ] **Step 3: 实现 service.go**

```go
// Package llm 中的 ModelService 是模型配置的业务层：
// 封装参数校验、默认模型规则、API Key 脱敏与环境变量展开。
// 读路径每次直查数据库（无缓存），保证 WebUI 增删改立即生效，
// 多节点共享 MySQL/PG 时天然一致。
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

var (
	ErrModelNotFound    = errors.New("模型不存在")
	ErrModelDisabled    = errors.New("模型已禁用")
	ErrNoDefaultModel   = errors.New("尚未配置模型，请在设置中创建模型")
	ErrNameExists       = errors.New("模型名称已存在")
	ErrDefaultProtected = errors.New("默认模型不允许删除或禁用，请先将其他模型设为默认")
	ErrInvalidModel     = errors.New("模型配置无效")
)

// ModelService 模型配置业务层
type ModelService struct {
	repo repo.ModelRepo
}

func NewModelService(r repo.ModelRepo) *ModelService {
	return &ModelService{repo: r}
}

// GetByName 按名称获取可用模型；name 为空时返回默认模型。
// APIKey 中的 ${ENV_VAR} 引用会被展开。禁用的模型返回 ErrModelDisabled。
func (s *ModelService) GetByName(ctx context.Context, name string) (*repo.Model, error) {
	var m *repo.Model
	var err error
	if name == "" {
		m, err = s.repo.GetDefault(ctx)
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrNoDefaultModel
		}
	} else {
		m, err = s.repo.GetByName(ctx, name)
		if errors.Is(err, repo.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrModelNotFound, name)
		}
	}
	if err != nil {
		return nil, err
	}
	if !m.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrModelDisabled, m.Name)
	}
	m.APIKey = config.ExpandEnv(m.APIKey)
	return m, nil
}

// GetStored 按名称获取模型（不检查 enabled，APIKey 展开环境变量）。
// 供连接测试等管理场景使用。
func (s *ModelService) GetStored(ctx context.Context, name string) (*repo.Model, error) {
	m, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return nil, err
	}
	m.APIKey = config.ExpandEnv(m.APIKey)
	return m, nil
}

// List 返回全部模型（APIKey 保持库中原文，由调用方决定脱敏方式）。
func (s *ModelService) List(ctx context.Context) ([]*repo.Model, error) {
	return s.repo.List(ctx)
}

// Create 创建模型。库中没有任何模型时，新模型自动成为默认模型。
func (s *ModelService) Create(ctx context.Context, m *repo.Model) error {
	if err := validateModel(m); err != nil {
		return err
	}
	if _, err := s.repo.GetByName(ctx, m.Name); err == nil {
		return fmt.Errorf("%w: %s", ErrNameExists, m.Name)
	} else if !errors.Is(err, repo.ErrNotFound) {
		return err
	}
	n, err := s.repo.Count(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		m.IsDefault = true
		m.Enabled = true
	}
	now := time.Now()
	m.CreatedAt = now
	m.UpdatedAt = now
	return s.repo.Create(ctx, m)
}

// Update 按原名称 name 更新模型。m.APIKey 为空表示保持库中原值；
// 允许重命名（m.Name != name），新名称冲突返回 ErrNameExists；
// 默认模型不允许禁用（is_default 本身不通过 Update 修改）。
func (s *ModelService) Update(ctx context.Context, name string, m *repo.Model) error {
	existing, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return err
	}
	if m.APIKey == "" {
		m.APIKey = existing.APIKey
	}
	if err := validateModel(m); err != nil {
		return err
	}
	if existing.IsDefault && !m.Enabled {
		return ErrDefaultProtected
	}
	if m.Name != name {
		if _, err := s.repo.GetByName(ctx, m.Name); err == nil {
			return fmt.Errorf("%w: %s", ErrNameExists, m.Name)
		} else if !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}
	return s.repo.Update(ctx, name, m)
}

// Delete 删除模型；默认模型返回 ErrDefaultProtected。
func (s *ModelService) Delete(ctx context.Context, name string) error {
	existing, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return err
	}
	if existing.IsDefault {
		return ErrDefaultProtected
	}
	return s.repo.Delete(ctx, name)
}

// SetDefault 把指定模型设为默认；禁用的模型返回 ErrModelDisabled。
func (s *ModelService) SetDefault(ctx context.Context, name string) error {
	m, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return err
	}
	if !m.Enabled {
		return fmt.Errorf("%w: %s", ErrModelDisabled, name)
	}
	return s.repo.SetDefault(ctx, name)
}

// validateModel 校验必填字段与参数范围。
func validateModel(m *repo.Model) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("%w: 名称不能为空", ErrInvalidModel)
	}
	if m.BaseURL == "" {
		return fmt.Errorf("%w: base_url 不能为空", ErrInvalidModel)
	}
	if m.APIKey == "" {
		return fmt.Errorf("%w: api_key 不能为空", ErrInvalidModel)
	}
	if m.Model == "" {
		return fmt.Errorf("%w: model 不能为空", ErrInvalidModel)
	}
	if m.Temperature < 0.0 || m.Temperature > 2.0 {
		return fmt.Errorf("%w: temperature 超出范围 %.1f（有效范围 0.0~2.0）", ErrInvalidModel, m.Temperature)
	}
	if m.TopP < 0.0 || m.TopP > 1.0 {
		return fmt.Errorf("%w: top_p 超出范围 %.1f（有效范围 0.0~1.0）", ErrInvalidModel, m.TopP)
	}
	if m.FrequencyPenalty < -2.0 || m.FrequencyPenalty > 2.0 {
		return fmt.Errorf("%w: frequency_penalty 超出范围 %.1f（有效范围 -2.0~2.0）", ErrInvalidModel, m.FrequencyPenalty)
	}
	if m.PresencePenalty < -2.0 || m.PresencePenalty > 2.0 {
		return fmt.Errorf("%w: presence_penalty 超出范围 %.1f（有效范围 -2.0~2.0）", ErrInvalidModel, m.PresencePenalty)
	}
	return nil
}

// MaskAPIKey 返回脱敏后的 API Key：只保留尾 4 位；
// ${ENV_VAR} 环境变量引用不是机密，原样返回。
func MaskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "${") && strings.HasSuffix(key, "}") {
		return key
	}
	if len(key) <= 8 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/llm/... -v`
Expected: 全部 PASS

---

### Task 6: ChatModel 构建链改造（chatmodel/engine/subagent/executor/main）

这是一个编译连锁改动，必须在一个任务内完成。改完后 `go build ./...` 必须通过。

**Files:**
- Modify: `internal/llm/chatmodel.go`
- Modify: `internal/agent/engine.go`
- Modify: `internal/agent/subagent_registry.go`
- Modify: `internal/agent/call_agent.go`（仅注释）
- Modify: `internal/agent/executor.go`
- Modify: `cmd/groot/main.go`
- Test: `internal/agent/` 下现有测试的编译修复

- [ ] **Step 1: 改造 chatmodel.go**

`NewChatModel` 与 `CheckConnection` 改为接收 `*repo.Model`：

```go
// NewChatModel creates an OpenAI-compatible ChatModel using eino-ext.
// m 为已解析的模型配置（APIKey 已展开环境变量，由 ModelService 保证）。
// timeout: per-call timeout for LLM API requests (0 means no timeout)
func NewChatModel(ctx context.Context, m *repo.Model, timeout time.Duration) (model.BaseChatModel, error) {
	maxTokens := m.MaxCompletionTokens
	temperature := float32(m.Temperature)
	topP := float32(m.TopP)
	frequencyPenalty := float32(m.FrequencyPenalty)
	presencePenalty := float32(m.PresencePenalty)

	chatCfg := &openai.ChatModelConfig{
		Model:               m.Model,
		APIKey:              m.APIKey,
		BaseURL:             m.BaseURL,
		MaxCompletionTokens: &maxTokens,
		Temperature:         &temperature,
		TopP:                &topP,
		FrequencyPenalty:    &frequencyPenalty,
		PresencePenalty:     &presencePenalty,
		Timeout:             timeout,
	}
	if m.Seed > 0 {
		seed := m.Seed
		chatCfg.Seed = &seed
	}
	if len(m.Stop) > 0 {
		chatCfg.Stop = m.Stop
	}
	if m.Thinking {
		chatCfg.ExtraFields = map[string]any{
			"thinking": map[string]any{"type": "enabled"},
		}
	}

	chatModel, err := openai.NewChatModel(ctx, chatCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}
	return chatModel, nil
}
```

`CheckConnection(cfg config.LLMConfig)` 改为 `CheckConnection(m *repo.Model) (status string, errorMsg string)`：删除函数开头的 `modelCfg := cfg.GetDefaultModel()` 与 nil 判断，其余逻辑不变，`modelCfg.BaseURL`/`modelCfg.APIKey` 改为 `m.BaseURL`/`m.APIKey`。

import 调整：删除 `"github.com/zfd81/groot/internal/config"`，增加 `"github.com/zfd81/groot/internal/repo"`。

- [ ] **Step 2: 改造 engine.go**

`Engine` 结构体 `llmConfig config.LLMConfig` → `models *llm.ModelService`；`EngineConfig` 的 `LLM config.LLMConfig` → `Models *llm.ModelService`；`NewEngine` 对应赋值 `models: cfg.Models`。

`Run` 方法开头（原第 99-120 行的"解析 model 名 + 创建 ChatModel"两段）合并改为：

```go
	// 0. 从数据库解析实际生效的模型（modelName 为空时取默认模型）。
	// 每次执行实时读库，WebUI 中的模型变更立即生效。
	// 解析出的 model 名放进 ctx —— call_agent 工具运行时通过它把同一个 model
	// 透传给子 Agent，保证编排模式下子 Agent 跟随主 Agent 当前选定的 model。
	mdl, err := e.models.GetByName(ctx, modelName)
	if err != nil {
		return nil, fmt.Errorf("模型配置不可用: %w", err)
	}
	resolvedModel := mdl.Name
	ctx = WithParentModel(ctx, resolvedModel)

	// 主 Agent 自身的 model 名记入累加器，Run 收尾时取出写入 RunResult.Model。
	if e.tokenAccumulators != nil {
		if mainID := mainChatIDFromContext(ctx); mainID != "" {
			e.tokenAccumulators.SetModel(mainID, resolvedModel)
		}
	}

	// 1. Create ChatModel with per-call timeout
	stepTimeout := time.Duration(e.reactConfig.StepTimeout) * time.Second
	chatModel, err := llm.NewChatModel(ctx, mdl, stepTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}
```

- [ ] **Step 3: 改造 subagent_registry.go**

- `SubAgentEntry.LLMCfg config.LLMConfig` → `Models *llm.ModelService`（注释同步：ChatModel 实例化材料 → 模型配置读取入口）。
- `BuildSubAgentRegistry` 与 `buildSubAgentEntry` 的参数 `llmCfg config.LLMConfig` → `models *llm.ModelService`，构造 entry 时 `Models: models`。
- `BuildAgentTool` 的 model 解析改为：

```go
	modelName := e.AgentMdModel
	if modelName == "" {
		modelName = parentModelName
	}
	// modelName 仍为空时由 ModelService 解析默认模型

	// 单测注入路径：跳过真实 LLM 构造，返回当前算出的 modelName 以便测试断言。
	if e.testTool != nil {
		return e.testTool, modelName, nil
	}

	mdl, err := e.Models.GetByName(ctx, modelName)
	if err != nil {
		return nil, modelName, fmt.Errorf("模型配置不可用: %w", err)
	}

	chatModel, err := llm.NewChatModel(ctx, mdl, e.StepTimeout)
	if err != nil {
		return nil, mdl.Name, fmt.Errorf("chat model: %w", err)
	}
```

后续 `return invokableTool, modelName, nil` 等处的 `modelName` 统一改为 `mdl.Name`。同时更新文件头部与函数上方注释中"LLMCfg.DefaultModel 兜底"的措辞为"ModelService 解析默认模型"。

- [ ] **Step 4: 更新 call_agent.go 注释**

第 35 行附近注释"（由 BuildAgentTool 进一步退到 LLMCfg.DefaultModel）"改为"（由 BuildAgentTool 通过 ModelService 解析默认模型）"。无代码改动。

- [ ] **Step 5: 改造 executor.go**

`Executor` 结构体增加字段 `models *llm.ModelService`（import `"github.com/zfd81/groot/internal/llm"`）；`NewExecutor` 在 `runtime *RuntimeState` 之后增加参数 `models *llm.ModelService` 并赋值；`Execute` 中 `EngineConfig` 装配处 `LLM: e.config.LLM,` 改为 `Models: e.models,`。

- [ ] **Step 6: 改造 main.go**

`startServer` 中，在 `repos := repofactory.NewRepos(...)` 之后增加：

```go
	// 模型配置业务层：模型配置唯一存储于数据库，每次使用实时读取
	modelService := llm.NewModelService(repos.Model)
```

import 增加 `"github.com/zfd81/groot/internal/llm"`。三处调用点更新：
- `agent.BuildSubAgentRegistry(context.Background(), subAgentDir, cfg.React, cfg.SubAgent, modelService, log)`
- `agent.NewExecutor(homeDir, memMgr, []adk.ChatModelAgentMiddleware{skillMiddleware}, mcpMgr, subAgentReg, runtimeState, modelService, *cfg, log)`
- `api.NewServer(...)` 暂不加参数（Task 7 处理），本步先保持原样——**注意此时 server.go 内 handler 仍引用 cfg.LLM，编译仍通过（config.LLM 字段要到 Task 9 才删除）**。

- [ ] **Step 7: 修复 agent 包现有测试的编译**

Run: `go build ./... && go test ./internal/agent/... ./internal/llm/... 2>&1 | head -50`

现有测试中构造 `SubAgentEntry`（通过 `SetEntryForTest`）的地方如果设置了 `LLMCfg` 字段，改为注入 `Models`（用 sqlite 临时库构建：`db.Open(nil, t.TempDir())` + `modeldb.New` + `llm.NewModelService`，并预置一个名为测试期望默认名的模型后 `SetDefault`）；只用 `testTool` 路径的测试无需注入 Models。逐个修到编译与测试全绿。

- [ ] **Step 8: 验证**

Run: `go build ./... && go test ./internal/agent/... ./internal/llm/... -v 2>&1 | tail -20`
Expected: 编译通过，测试 PASS

---

### Task 7: chat / health handler 改造 + server.go 装配

**Files:**
- Modify: `internal/api/handler/chat.go`
- Modify: `internal/api/handler/health.go`
- Modify: `internal/api/server.go`
- Modify: `cmd/groot/main.go`
- Test: `internal/api/handler/chat_test.go`、`internal/api/handler/health_test.go` 编译修复

- [ ] **Step 1: chat.go**

`ChatHandler` 结构体增加字段 `models *llm.ModelService`（import `"github.com/zfd81/groot/internal/llm"`）；`NewChatHandler` 在 `cfg config.Config` 之前增加参数 `models *llm.ModelService`。

原"2.6 验证模型名称"块（`if modelName != "" && !h.config.LLM.ValidateModel(modelName)`）替换为：

```go
	// 2.6. 解析模型配置：校验存在性与启用状态，无默认模型时给出引导性错误。
	// mdl 同时供后文 MaxContextTokens 使用。
	mdl, err := h.models.GetByName(ctx, modelName)
	if err != nil {
		rc.JSON(400, utils.H{
			"status":  "invalid_model",
			"message": err.Error(),
		})
		return
	}
```

原"继续会话"分支中获取模型配置的三行（`modelConfig := h.config.LLM.GetModelByName(...)` 到 nil 兜底）删除，`GetContextMessagesWithTokenLimit` 的第三个参数改为 `mdl.MaxContextTokens`。

注意：该分支内原有 `var err error` 声明与上方新的 `err` 会冲突，按编译器提示调整（内层改用 `historyMessages, err = ...` 复用外层 err 即可）。

- [ ] **Step 2: health.go**

`HealthHandler` 增加字段 `models *llm.ModelService`；`NewHealthHandler` 在 `log` 之前增加参数 `models *llm.ModelService`。

原 LLM 检查两行：

```go
	llmStatus, llmError := llm.CheckConnection(h.config.LLM)
	llmInfo := map[string]string{"model": h.config.LLM.DefaultModel}
```

替换为：

```go
	// Check LLM connection（无默认模型时报告 unconfigured 而非失败）
	llmStatus, llmError, llmModelName := "unconfigured", "尚未配置模型", ""
	if m, err := h.models.GetByName(ctx, ""); err == nil {
		llmModelName = m.Name
		llmStatus, llmError = llm.CheckConnection(m)
	}
	llmInfo := map[string]string{"model": llmModelName}
```

（`Serve` 方法签名里已有 `ctx context.Context`，直接使用。）

- [ ] **Step 3: server.go**

`NewServer` 增加参数 `models *llm.ModelService`（放在 `users repo.UserRepo` 之后），import `"github.com/zfd81/groot/internal/llm"`。handler 构造更新：

```go
	chatH := handler.NewChatHandler(mem, runtime, exec, mcpMgr, subAgentReg, attHandler, models, cfg, log)
	healthH := handler.NewHealthHandler(cfg, homeDir, skillBackend, mcpMgr, memMgr, runtime, models, log)
```

（`modelsH` 的构造在 Task 8 一并改；本步暂保持 `handler.NewModelsHandler(&cfg)` 不动。）

main.go 的 `api.NewServer(..., repos.User)` 改为 `api.NewServer(..., repos.User, modelService)`。

- [ ] **Step 4: 修复 handler 测试编译**

`chat_test.go`、`health_test.go` 中的 `NewChatHandler`/`NewHealthHandler` 调用按新签名补 `models` 参数——用与 Task 6 Step 7 相同的 sqlite 临时库方式构建 ModelService，并预置一个默认模型（除非该测试显式测"无模型"场景）。

- [ ] **Step 5: 验证**

Run: `go build ./... && go test ./internal/api/... -v 2>&1 | tail -20`
Expected: 编译通过，测试 PASS

---

### Task 8: models handler 重写（CRUD API）+ 路由 + 类型 + 单测

**Files:**
- Modify: `internal/api/types/`（`ModelInfo` 所在文件，约 191-203 行）
- Rewrite: `internal/api/handler/models.go`
- Create: `internal/api/handler/models_test.go`
- Modify: `internal/api/router.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: 扩展 types.ModelInfo**

```go
// ModelsResponse represents models list response
type ModelsResponse struct {
	Models  []ModelInfo `json:"models"`
	Default string      `json:"default"`
	Total   int         `json:"total"`
}

// ModelInfo represents model information（api_key 为脱敏后的展示值）
type ModelInfo struct {
	Name                string   `json:"name"`
	Model               string   `json:"model"`
	BaseURL             string   `json:"base_url"`
	APIKey              string   `json:"api_key"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	MaxContextTokens    int      `json:"max_context_tokens"`
	Temperature         float64  `json:"temperature"`
	TopP                float64  `json:"top_p"`
	FrequencyPenalty    float64  `json:"frequency_penalty"`
	PresencePenalty     float64  `json:"presence_penalty"`
	Seed                int      `json:"seed"`
	Stop                []string `json:"stop"`
	Thinking            bool     `json:"thinking"`
	IsDefault           bool     `json:"is_default"`
	Enabled             bool     `json:"enabled"`
}
```

- [ ] **Step 2: 重写 models.go**

```go
package handler

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// ModelsHandler 模型配置管理（/web/models 系列端点）
type ModelsHandler struct {
	models *llm.ModelService
	log    *logger.Logger
}

func NewModelsHandler(models *llm.ModelService, log *logger.Logger) *ModelsHandler {
	return &ModelsHandler{models: models, log: log}
}

// ModelRequest 创建/更新模型的请求体（api_key 为空表示更新时保持原值）
type ModelRequest struct {
	Name                string   `json:"name"`
	Model               string   `json:"model"`
	BaseURL             string   `json:"base_url"`
	APIKey              string   `json:"api_key"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	MaxContextTokens    int      `json:"max_context_tokens"`
	Temperature         float64  `json:"temperature"`
	TopP                float64  `json:"top_p"`
	FrequencyPenalty    float64  `json:"frequency_penalty"`
	PresencePenalty     float64  `json:"presence_penalty"`
	Seed                int      `json:"seed"`
	Stop                []string `json:"stop"`
	Thinking            bool     `json:"thinking"`
	Enabled             bool     `json:"enabled"`
}

func (r *ModelRequest) toModel() *repo.Model {
	return &repo.Model{
		Name:                r.Name,
		Model:               r.Model,
		BaseURL:             r.BaseURL,
		APIKey:              r.APIKey,
		MaxCompletionTokens: r.MaxCompletionTokens,
		MaxContextTokens:    r.MaxContextTokens,
		Temperature:         r.Temperature,
		TopP:                r.TopP,
		FrequencyPenalty:    r.FrequencyPenalty,
		PresencePenalty:     r.PresencePenalty,
		Seed:                r.Seed,
		Stop:                r.Stop,
		Thinking:            r.Thinking,
		Enabled:             r.Enabled,
	}
}

func toModelInfo(m *repo.Model) types.ModelInfo {
	stop := m.Stop
	if stop == nil {
		stop = []string{}
	}
	return types.ModelInfo{
		Name:                m.Name,
		Model:               m.Model,
		BaseURL:             m.BaseURL,
		APIKey:              llm.MaskAPIKey(m.APIKey),
		MaxCompletionTokens: m.MaxCompletionTokens,
		MaxContextTokens:    m.MaxContextTokens,
		Temperature:         m.Temperature,
		TopP:                m.TopP,
		FrequencyPenalty:    m.FrequencyPenalty,
		PresencePenalty:     m.PresencePenalty,
		Seed:                m.Seed,
		Stop:                stop,
		Thinking:            m.Thinking,
		IsDefault:           m.IsDefault,
		Enabled:             m.Enabled,
	}
}

// writeModelError 把 ModelService 错误映射为 HTTP 状态码与错误码。
func writeModelError(rc *app.RequestContext, err error) {
	status, code := 500, "internal_error"
	switch {
	case errors.Is(err, llm.ErrModelNotFound):
		status, code = 404, "model_not_found"
	case errors.Is(err, llm.ErrNameExists):
		status, code = 409, "model_name_exists"
	case errors.Is(err, llm.ErrDefaultProtected):
		status, code = 409, "default_model_protected"
	case errors.Is(err, llm.ErrModelDisabled):
		status, code = 400, "model_disabled"
	case errors.Is(err, llm.ErrNoDefaultModel):
		status, code = 400, "no_default_model"
	case errors.Is(err, llm.ErrInvalidModel):
		status, code = 400, "invalid_model_config"
	}
	rc.JSON(status, utils.H{"status": code, "message": err.Error()})
}

// List 处理 GET /web/models
func (h *ModelsHandler) List(ctx context.Context, rc *app.RequestContext) {
	list, err := h.models.List(ctx)
	if err != nil {
		writeModelError(rc, err)
		return
	}
	models := make([]types.ModelInfo, 0, len(list))
	defaultName := ""
	for _, m := range list {
		if m.IsDefault {
			defaultName = m.Name
		}
		models = append(models, toModelInfo(m))
	}
	rc.JSON(200, types.ModelsResponse{Models: models, Default: defaultName, Total: len(models)})
}

// Create 处理 POST /web/models
func (h *ModelsHandler) Create(ctx context.Context, rc *app.RequestContext) {
	var req ModelRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	m := req.toModel()
	if err := h.models.Create(ctx, m); err != nil {
		writeModelError(rc, err)
		return
	}
	rc.JSON(200, toModelInfo(m))
}

// Update 处理 PUT /web/models/:name
func (h *ModelsHandler) Update(ctx context.Context, rc *app.RequestContext) {
	name := rc.Param("name")
	var req ModelRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	m := req.toModel()
	if err := h.models.Update(ctx, name, m); err != nil {
		writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// Delete 处理 DELETE /web/models/:name
func (h *ModelsHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	if err := h.models.Delete(ctx, rc.Param("name")); err != nil {
		writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// SetDefault 处理 PUT /web/models/:name/default
func (h *ModelsHandler) SetDefault(ctx context.Context, rc *app.RequestContext) {
	if err := h.models.SetDefault(ctx, rc.Param("name")); err != nil {
		writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// ModelTestRequest 连接测试请求体。api_key 为空且 name 非空时取库中已存 key。
type ModelTestRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// Test 处理 POST /web/models/test
func (h *ModelsHandler) Test(ctx context.Context, rc *app.RequestContext) {
	var req ModelTestRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	apiKey := config.ExpandEnv(req.APIKey)
	if apiKey == "" && req.Name != "" {
		stored, err := h.models.GetStored(ctx, req.Name)
		if err != nil {
			writeModelError(rc, err)
			return
		}
		apiKey = stored.APIKey
		if req.BaseURL == "" {
			req.BaseURL = stored.BaseURL
		}
		if req.Model == "" {
			req.Model = stored.Model
		}
	}
	if req.BaseURL == "" || apiKey == "" {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "base_url 和 api_key 不能为空"})
		return
	}
	status, errMsg := llm.CheckConnection(&repo.Model{
		BaseURL: req.BaseURL,
		APIKey:  apiKey,
		Model:   req.Model,
	})
	rc.JSON(200, utils.H{"status": status, "message": errMsg})
}
```

- [ ] **Step 3: 路由与装配**

`router.go` 中 `webGroup.GET("/models", modelsH.Serve)` 替换为：

```go
	webGroup.GET("/models", modelsH.List)
	webGroup.POST("/models", modelsH.Create)
	webGroup.POST("/models/test", modelsH.Test)
	webGroup.PUT("/models/:name", modelsH.Update)
	webGroup.PUT("/models/:name/default", modelsH.SetDefault)
	webGroup.DELETE("/models/:name", modelsH.Delete)
```

`server.go` 中 `handler.NewModelsHandler(&cfg)` 改为 `handler.NewModelsHandler(models, log)`。

- [ ] **Step 4: handler 单测**

创建 `internal/api/handler/models_test.go`，仿照 `webauth_test.go` 的直接调用模式（`app.NewContext(0)` + 设置请求体/参数后调用 handler 函数）：

```go
package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo/modeldb"
)

func newModelsHandlerForTest(t *testing.T) *ModelsHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return NewModelsHandler(llm.NewModelService(modeldb.New(sqlxDB, dialect)), logger.NewNop())
}

func callJSON(h func(context.Context, *app.RequestContext), method, body string, params map[string]string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	if body != "" {
		rc.Request.SetBody([]byte(body))
		rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	}
	for k, v := range params {
		rc.Params = append(rc.Params, param.Param{Key: k, Value: v})
	}
	h(context.Background(), rc)
	return rc
}

const createBody = `{"name":"gpt-4o","model":"gpt-4o","base_url":"https://api.openai.com/v1",
	"api_key":"sk-test-1234abcd","temperature":0.7,"top_p":1.0,"stop":[],"enabled":true}`

func TestModelsHandler_CreateAndList(t *testing.T) {
	h := newModelsHandlerForTest(t)

	rc := callJSON(h.Create, consts.MethodPost, createBody, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("Create status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc = callJSON(h.List, consts.MethodGet, "", nil)
	var resp types.ModelsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 1 || resp.Default != "gpt-4o" {
		t.Errorf("List: %+v", resp)
	}
	// api_key 必须脱敏且不含原文
	if resp.Models[0].APIKey != "****abcd" || strings.Contains(string(rc.Response.Body()), "sk-test") {
		t.Errorf("api_key 未脱敏: %s", resp.Models[0].APIKey)
	}
	if !resp.Models[0].IsDefault {
		t.Error("首个模型应为默认")
	}
}

func TestModelsHandler_CreateDuplicate(t *testing.T) {
	h := newModelsHandlerForTest(t)
	callJSON(h.Create, consts.MethodPost, createBody, nil)
	rc := callJSON(h.Create, consts.MethodPost, createBody, nil)
	if rc.Response.StatusCode() != 409 {
		t.Errorf("重名创建应 409, got %d", rc.Response.StatusCode())
	}
}

func TestModelsHandler_DeleteDefaultRejected(t *testing.T) {
	h := newModelsHandlerForTest(t)
	callJSON(h.Create, consts.MethodPost, createBody, nil)
	rc := callJSON(h.Delete, consts.MethodDelete, "", map[string]string{"name": "gpt-4o"})
	if rc.Response.StatusCode() != 409 {
		t.Errorf("删除默认模型应 409, got %d", rc.Response.StatusCode())
	}
}

func TestModelsHandler_SetDefaultAndDelete(t *testing.T) {
	h := newModelsHandlerForTest(t)
	callJSON(h.Create, consts.MethodPost, createBody, nil)
	second := strings.Replace(createBody, `"gpt-4o"`, `"backup"`, 1)
	callJSON(h.Create, consts.MethodPost, second, nil)

	rc := callJSON(h.SetDefault, consts.MethodPut, "", map[string]string{"name": "backup"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("SetDefault: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	rc = callJSON(h.Delete, consts.MethodDelete, "", map[string]string{"name": "gpt-4o"})
	if rc.Response.StatusCode() != 200 {
		t.Errorf("切换默认后删除应成功, got %d", rc.Response.StatusCode())
	}
}

func TestModelsHandler_UpdateNotFound(t *testing.T) {
	h := newModelsHandlerForTest(t)
	rc := callJSON(h.Update, consts.MethodPut, createBody, map[string]string{"name": "ghost"})
	if rc.Response.StatusCode() != 404 {
		t.Errorf("更新不存在模型应 404, got %d", rc.Response.StatusCode())
	}
}
```

注意：`rc.Params` 的具体注入方式以 hertz 版本 API 为准（`github.com/cloudwego/hertz/pkg/route/param`）；若 `rc.Params` 不可直接 append，用 `rc.Params = param.Params{{Key: "name", Value: "..."}}`。

- [ ] **Step 5: 验证**

Run: `go build ./... && go test ./internal/api/... -v 2>&1 | tail -30`
Expected: 编译通过，测试 PASS

---

### Task 9: config 包清理（移除 llm 段）

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/template.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: config.go**

删除：`Config.LLM` 字段、`LLMConfig`、`ModelConfig`、`GetDefaultModel`、`GetModelByName`、`ValidateModel`、`validateModelParams`、`ValidateLLMConfig`。**保留 `ExpandEnv`**（数据库 DSN、API Key 认证、ModelService 都在用）。

- [ ] **Step 2: loader.go**

删除 `Load` 中的 ValidateLLMConfig 调用块（原 46-49 行）与 `expandConfigEnvVars` 中的 LLM 展开循环（原 152-156 行）。

- [ ] **Step 3: template.go**

删除模板中整个 `llm:` 段（从 `# LLM 配置（必填）` 注释到 `thinking: false` 行），在原位置替换为一行说明：

```
# 模型配置通过 Web UI 管理（登录后进入 设置 → 模型），不在本文件中配置
```

- [ ] **Step 4: config_test.go**

删除所有引用 `LLM`/`LLMConfig`/`ModelConfig` 的断言与测试函数（约第 28-36 行的默认值断言，以及 134/152/166 行附近以 `&LLMConfig{...}` 构造的验证测试——这些校验逻辑已由 `internal/llm/service_test.go` 覆盖）。若被测的 Load 流程依赖模板中的 llm 段，同步修正测试夹具。

- [ ] **Step 5: 验证**

Run: `go build ./... && go test ./internal/... 2>&1 | tail -20`
Expected: 编译通过，全部测试 PASS（此时全仓不应再有 `config.LLMConfig` 的任何引用：`grep -rn "LLMConfig\|ValidateLLMConfig" --include="*.go" internal/ cmd/` 结果为空）

---

### Task 10: 前端基础改造（client / types / meta store / ChatInput）

**Files:**
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/stores/meta.ts`
- Modify: `web/src/components/chat/ChatInput.vue`

- [ ] **Step 1: client.ts 增加 put / delete**

在 `export const api = {` 对象中补充：

```ts
  put: <T>(path: string, body?: unknown, headers?: Record<string, string>) =>
    request<T>('PUT', path, body, headers),
  delete: <T>(path: string, headers?: Record<string, string>) =>
    request<T>('DELETE', path, undefined, headers),
```

- [ ] **Step 2: types.ts 扩展 ModelInfo**

替换现有 `ModelInfo`：

```ts
export interface ModelInfo {
  name: string
  model: string
  base_url: string
  api_key: string // 脱敏后的展示值
  max_completion_tokens: number
  max_context_tokens: number
  temperature: number
  top_p: number
  frequency_penalty: number
  presence_penalty: number
  seed: number
  stop: string[]
  thinking: boolean
  is_default: boolean
  enabled: boolean
}
```

`ModelsResp` 保持不变。新增表单与测试类型：

```ts
// 创建/更新模型的请求体（api_key 为空表示更新时保持原值）
export interface ModelForm {
  name: string
  model: string
  base_url: string
  api_key: string
  max_completion_tokens: number
  max_context_tokens: number
  temperature: number
  top_p: number
  frequency_penalty: number
  presence_penalty: number
  seed: number
  stop: string[]
  thinking: boolean
  enabled: boolean
}

export interface ModelTestResp {
  status: string // healthy | unhealthy
  message: string
}
```

- [ ] **Step 3: meta.ts 增加 reload**

在 `useMetaStore` 中增加并导出：

```ts
  // 模型管理界面增删改后调用，强制重新拉取模型列表
  async function reload() {
    loaded.value = false
    await load()
  }
```

`return { models, defaultModel, agents, loaded, load, reload }`

- [ ] **Step 4: ChatInput.vue 只显示启用模型**

模型下拉选项的 computed（约 79 行 `models.value.map(...)`）改为先过滤：

```ts
  models.value.filter((m) => m.enabled).map((m) => ({ ... }))
```

（`...` 内保持原有映射字段不变。）

- [ ] **Step 5: 验证**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head -20`（或项目已有的类型检查命令，见 web/package.json scripts）
Expected: 无类型错误（SettingsModal 对 ModelInfo 旧字段的使用仍兼容——name/model/base_url 均保留）

---

### Task 11: ModelsPanel 组件 + SettingsModal 接入 + i18n

**Files:**
- Create: `web/src/components/settings/ModelsPanel.vue`
- Modify: `web/src/components/settings/SettingsModal.vue`
- Modify: `web/src/i18n/messages/zh-cn.ts`
- Modify: `web/src/i18n/messages/en.ts`

- [ ] **Step 1: i18n 文案**

`zh-cn.ts` 的 `settings` 段（`noModels` 之后）追加：

```ts
    addModel: '新建模型',
    editModel: '编辑模型',
    deleteModelConfirm: '确定删除模型 “{name}” 吗？此操作不可恢复。',
    setDefault: '设为默认',
    modelEnabled: '启用',
    apiKey: 'API密钥',
    apiKeyKeepHint: '留空表示保持原密钥不变',
    advancedParams: '高级参数',
    testConnection: '测试连接',
    testOk: '连接成功',
    testFail: '连接失败',
    modelSaved: '模型已保存',
    modelDeleted: '模型已删除',
    defaultChanged: '默认模型已切换',
    stopHint: '停止序列：模型输出到这些字符串时停止生成，回车分隔',
    defaultProtectedHint: '默认模型不可删除或禁用',
```

`en.ts` 对应追加：

```ts
    addModel: 'New model',
    editModel: 'Edit model',
    deleteModelConfirm: 'Delete model "{name}"? This cannot be undone.',
    setDefault: 'Set as default',
    modelEnabled: 'Enabled',
    apiKey: 'API key',
    apiKeyKeepHint: 'Leave empty to keep the current key',
    advancedParams: 'Advanced parameters',
    testConnection: 'Test connection',
    testOk: 'Connection OK',
    testFail: 'Connection failed',
    modelSaved: 'Model saved',
    modelDeleted: 'Model deleted',
    defaultChanged: 'Default model changed',
    stopHint: 'Stop sequences: generation stops at these strings, one per line',
    defaultProtectedHint: 'The default model cannot be deleted or disabled',
```

- [ ] **Step 2: 创建 ModelsPanel.vue**

```vue
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api, ApiError } from '../../api/client'
import { useMetaStore } from '../../stores/meta'
import type { ModelInfo, ModelsResp, ModelForm, ModelTestResp } from '../../api/types'

const { t } = useI18n()
const meta = useMetaStore()

const models = ref<ModelInfo[]>([])
const loading = ref(false)

// 表单对话框状态：editingName 非空表示编辑模式（值为原名称）
const showForm = ref(false)
const editingName = ref('')
const saving = ref(false)
const testing = ref(false)

function emptyForm(): ModelForm {
  return {
    name: '', model: '', base_url: '', api_key: '',
    max_completion_tokens: 4096, max_context_tokens: 0,
    temperature: 0.7, top_p: 1.0, frequency_penalty: 0, presence_penalty: 0,
    seed: 0, stop: [], thinking: false, enabled: true,
  }
}
const form = ref<ModelForm>(emptyForm())
// stop 在表单里用多行文本编辑，提交时按行拆分
const stopText = ref('')

async function loadModels() {
  loading.value = true
  try {
    const resp = await api.get<ModelsResp>('/web/models')
    models.value = resp.models || []
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingName.value = ''
  form.value = emptyForm()
  stopText.value = ''
  showForm.value = true
}

function openEdit(m: ModelInfo) {
  editingName.value = m.name
  form.value = {
    name: m.name, model: m.model, base_url: m.base_url,
    api_key: '', // 编辑时留空 = 不修改（后端语义）
    max_completion_tokens: m.max_completion_tokens,
    max_context_tokens: m.max_context_tokens,
    temperature: m.temperature, top_p: m.top_p,
    frequency_penalty: m.frequency_penalty, presence_penalty: m.presence_penalty,
    seed: m.seed, stop: m.stop || [], thinking: m.thinking, enabled: m.enabled,
  }
  stopText.value = (m.stop || []).join('\n')
  showForm.value = true
}

function buildPayload(): ModelForm {
  return {
    ...form.value,
    stop: stopText.value.split('\n').map((s) => s).filter((s) => s.length > 0),
  }
}

async function handleSave() {
  saving.value = true
  try {
    const payload = buildPayload()
    if (editingName.value) {
      await api.put(`/web/models/${encodeURIComponent(editingName.value)}`, payload)
    } else {
      await api.post('/web/models', payload)
    }
    ElNotification.success({ title: t('settings.menuModels'), message: t('settings.modelSaved') })
    showForm.value = false
    await refreshAll()
  } catch (e) {
    notifyError(e)
  } finally {
    saving.value = false
  }
}

async function handleDelete(m: ModelInfo) {
  try {
    await ElMessageBox.confirm(
      t('settings.deleteModelConfirm', { name: m.name }),
      t('settings.menuModels'),
      { type: 'warning' }
    )
  } catch {
    return // 用户取消
  }
  try {
    await api.delete(`/web/models/${encodeURIComponent(m.name)}`)
    ElNotification.success({ title: t('settings.menuModels'), message: t('settings.modelDeleted') })
    await refreshAll()
  } catch (e) {
    notifyError(e)
  }
}

async function handleSetDefault(m: ModelInfo) {
  try {
    await api.put(`/web/models/${encodeURIComponent(m.name)}/default`)
    ElNotification.success({ title: t('settings.menuModels'), message: t('settings.defaultChanged') })
    await refreshAll()
  } catch (e) {
    notifyError(e)
  }
}

async function handleToggleEnabled(m: ModelInfo, enabled: boolean) {
  try {
    // 启用开关复用更新端点：api_key 传空 = 保持原值
    await api.put(`/web/models/${encodeURIComponent(m.name)}`, {
      name: m.name, model: m.model, base_url: m.base_url, api_key: '',
      max_completion_tokens: m.max_completion_tokens,
      max_context_tokens: m.max_context_tokens,
      temperature: m.temperature, top_p: m.top_p,
      frequency_penalty: m.frequency_penalty, presence_penalty: m.presence_penalty,
      seed: m.seed, stop: m.stop || [], thinking: m.thinking, enabled,
    })
    await refreshAll()
  } catch (e) {
    notifyError(e)
    await loadModels() // 失败时回滚开关显示
  }
}

async function handleTest() {
  testing.value = true
  try {
    const payload = buildPayload()
    const resp = await api.post<ModelTestResp>('/web/models/test', {
      // 编辑模式且未填新 key 时带上 name，后端取库中已存 key
      name: editingName.value || undefined,
      base_url: payload.base_url,
      api_key: payload.api_key,
      model: payload.model,
    })
    if (resp.status === 'healthy') {
      ElNotification.success({ title: t('settings.testConnection'), message: t('settings.testOk') })
    } else {
      ElNotification.error({ title: t('settings.testConnection'), message: resp.message || t('settings.testFail') })
    }
  } catch (e) {
    notifyError(e)
  } finally {
    testing.value = false
  }
}

async function refreshAll() {
  await loadModels()
  await meta.reload() // 同步刷新聊天下拉框的模型列表
}

function notifyError(e: unknown) {
  const message = e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e)
  ElNotification.error({ title: t('settings.menuModels'), message })
}

onMounted(() => void loadModels())
</script>

<template>
  <div v-loading="loading">
    <div class="panel-toolbar">
      <el-button type="primary" size="small" @click="openCreate">{{ t('settings.addModel') }}</el-button>
    </div>

    <div v-for="m in models" :key="m.name" class="list-item">
      <div class="item-header">
        <span class="model-name">{{ m.name }}</span>
        <el-tag v-if="m.is_default" size="small" type="primary" effect="light" style="margin-left: 8px">
          {{ t('settings.default') }}
        </el-tag>
        <div class="item-actions">
          <el-tooltip v-if="m.is_default" :content="t('settings.defaultProtectedHint')" placement="top">
            <span>
              <el-switch :model-value="m.enabled" size="small" disabled />
            </span>
          </el-tooltip>
          <el-switch v-else :model-value="m.enabled" size="small"
            @change="(v: boolean) => handleToggleEnabled(m, v)" />
          <el-button v-if="!m.is_default" size="small" text type="primary"
            :disabled="!m.enabled" @click="handleSetDefault(m)">
            {{ t('settings.setDefault') }}
          </el-button>
          <el-button size="small" text @click="openEdit(m)">{{ t('common.edit') }}</el-button>
          <el-button size="small" text type="danger" :disabled="m.is_default" @click="handleDelete(m)">
            {{ t('common.delete') }}
          </el-button>
        </div>
      </div>
      <div class="model-field">
        <span class="field-label">{{ t('settings.apiUrl') }}</span>
        <span class="mono">{{ m.base_url }}</span>
      </div>
      <div class="model-field">
        <span class="field-label">{{ t('settings.modelName') }}</span>
        <span class="mono">{{ m.model }}</span>
      </div>
    </div>
    <el-empty v-if="!loading && !models.length" :description="t('settings.noModels')" :image-size="60" />

    <!-- 创建/编辑表单 -->
    <el-dialog v-model="showForm" :title="editingName ? t('settings.editModel') : t('settings.addModel')"
      width="520px" append-to-body>
      <el-form label-position="top" @submit.prevent>
        <el-form-item :label="t('settings.modelName')" required>
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item :label="t('settings.apiUrl')" required>
          <el-input v-model="form.base_url" placeholder="https://api.openai.com/v1" />
        </el-form-item>
        <el-form-item :label="t('settings.apiKey')" :required="!editingName">
          <el-input v-model="form.api_key" type="password" show-password
            :placeholder="editingName ? t('settings.apiKeyKeepHint') : 'sk-...'" />
        </el-form-item>
        <el-form-item label="Model ID" required>
          <el-input v-model="form.model" placeholder="gpt-4o" />
        </el-form-item>

        <el-collapse>
          <el-collapse-item :title="t('settings.advancedParams')">
            <el-form-item label="temperature (0~2)">
              <el-input-number v-model="form.temperature" :min="0" :max="2" :step="0.1" />
            </el-form-item>
            <el-form-item label="top_p (0~1)">
              <el-input-number v-model="form.top_p" :min="0" :max="1" :step="0.05" />
            </el-form-item>
            <el-form-item label="max_completion_tokens">
              <el-input-number v-model="form.max_completion_tokens" :min="0" :step="256" />
            </el-form-item>
            <el-form-item label="max_context_tokens">
              <el-input-number v-model="form.max_context_tokens" :min="0" :step="1024" />
            </el-form-item>
            <el-form-item label="frequency_penalty (-2~2)">
              <el-input-number v-model="form.frequency_penalty" :min="-2" :max="2" :step="0.1" />
            </el-form-item>
            <el-form-item label="presence_penalty (-2~2)">
              <el-input-number v-model="form.presence_penalty" :min="-2" :max="2" :step="0.1" />
            </el-form-item>
            <el-form-item label="seed">
              <el-input-number v-model="form.seed" :min="0" />
            </el-form-item>
            <el-form-item label="stop">
              <el-input v-model="stopText" type="textarea" :rows="2" :placeholder="t('settings.stopHint')" />
            </el-form-item>
            <el-form-item label="thinking">
              <el-switch v-model="form.thinking" />
            </el-form-item>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button :loading="testing" @click="handleTest">{{ t('settings.testConnection') }}</el-button>
        <el-button @click="showForm = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">{{ t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.panel-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}

.list-item {
  padding: 10px 0;
  border-bottom: 1px solid rgba(127, 127, 127, 0.12);
}

.item-header {
  display: flex;
  align-items: center;
}

.item-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
}

.model-name {
  font-weight: 600;
}

.model-field {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-top: 2px;
}

.field-label {
  font-size: 0.78em;
  opacity: 0.75;
  font-weight: 700;
  flex-shrink: 0;
  width: 4em;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.85em;
  opacity: 0.7;
}
</style>
```

说明：
- `common.edit` / `common.delete` / `common.cancel` / `common.save` 等通用键先检查 i18n 中是否已存在（`grep -n "edit\|delete\|cancel\|save" web/src/i18n/messages/zh-cn.ts` 的 common 段），缺失则补进 common 段（zh：编辑/删除/取消/保存；en：Edit/Delete/Cancel/Save）。
- `ElNotification`/`ElMessageBox`/`useI18n` 若项目通过 unplugin 自动导入（SettingsModal.vue 未显式 import 即在用 `useI18n`/`ElNotification`，说明是自动导入），保持同样写法。

- [ ] **Step 3: SettingsModal.vue 接入**

script 中 import：`import ModelsPanel from './ModelsPanel.vue'`；删除不再使用的 `models, defaultModel` 解构（`const { models, defaultModel } = storeToRefs(meta)` 一行，注意 `meta.load()` 在 ensureLoaded 中保留）。

模板中整个 `<!-- 模型 -->` 段（`v-else-if="section === 'models'"` 的 div，含内部循环与 el-empty）替换为：

```vue
        <!-- 模型 -->
        <div v-else-if="section === 'models'">
          <ModelsPanel />
        </div>
```

style 中不再被引用的 `.model-name`、`.model-field`、`.field-label` 规则可删除（已迁入 ModelsPanel）。

- [ ] **Step 4: 构建验证**

Run: `cd web && npm run build 2>&1 | tail -10`（构建命令以 web/package.json 为准；仓库根若有 `make web` 用 make web）
Expected: 构建成功，无类型错误

- [ ] **Step 5: 手工验证清单（启动 `go run ./cmd` 后浏览器操作）**

1. 空库启动 → 设置-模型页显示空态 + "新建模型"按钮
2. 创建首个模型 → 自动带默认 tag；聊天下拉框出现该模型
3. 编辑模型（api_key 留空保存）→ 保存成功，重新打开表单 base_url 等保留
4. 创建第二个模型 → 设为默认 → tag 切换
5. 禁用非默认模型 → 聊天下拉框消失；默认模型开关置灰
6. 删除非默认模型 → 二次确认 → 列表刷新；删除默认模型按钮置灰
7. 测试连接（正确/错误 key 各一次）→ 成功/失败提示

---

### Task 12: 文档与 Python 系统测试

**Files:**
- Modify: `README.md`
- Modify: `tests/TEST_CASES.md`
- Create: `tests/python/test_models_api.py`

- [ ] **Step 1: README.md（用户手册）更新**

先 `grep -n "llm\|default_model\|api_key" README.md` 定位模型配置相关章节，然后：
- config.yaml 示例/说明中删除 `llm:` 段，替换为一句话："模型配置通过 Web UI 管理：登录后进入 设置 → 模型，可创建、编辑、删除模型，切换默认模型，启用/禁用模型并测试连接。API Key 支持填写 `${ENV_VAR}` 引用环境变量。"
- 如有"快速开始"流程，在启动服务之后补一步"在 Web UI 中创建模型"。
- 升级说明：原 config.yaml 中的 llm 配置不再生效，需在 Web UI 中重新创建模型。

- [ ] **Step 2: tests/TEST_CASES.md 追加用例点**

在文档末尾按现有格式追加"模型管理"小节，列出：模型 CRUD、首个模型自动默认、默认模型删除/禁用保护、重名冲突、api_key 脱敏与留空不改、设默认、启用/禁用、连接测试、chat 引用不存在/禁用模型报错、无默认模型时 chat 报错。

- [ ] **Step 3: Python 系统测试**

先查看 `tests/python/` 现有 conftest.py 与任一测试文件，确认 base_url、Web 登录 fixture 的既有写法；若已有 Web 会话 fixture 则复用。若无，创建 `tests/python/test_models_api.py`：

```python
"""模型管理 API 系统测试（/web/models 系列端点）。

运行前提：groot 服务已启动，且已完成 Web 用户初始化。
环境变量：
  GROOT_BASE_URL   默认 http://127.0.0.1:8080
  GROOT_WEB_USER / GROOT_WEB_PASSWORD  Web 登录凭据
"""
import os
import uuid

import pytest
import requests

BASE = os.environ.get("GROOT_BASE_URL", "http://127.0.0.1:8080")


@pytest.fixture(scope="module")
def web():
    """已登录的 Web 会话（Cookie 认证）"""
    s = requests.Session()
    resp = s.post(f"{BASE}/web/login", json={
        "username": os.environ.get("GROOT_WEB_USER", "admin"),
        "password": os.environ.get("GROOT_WEB_PASSWORD", ""),
    })
    assert resp.status_code == 200, f"登录失败: {resp.text}"
    yield s


@pytest.fixture()
def model_name(web):
    """生成唯一模型名，测试结束后清理（若非默认）"""
    name = f"t-{uuid.uuid4().hex[:8]}"
    yield name
    web.delete(f"{BASE}/web/models/{name}")


def model_body(name, **overrides):
    body = {
        "name": name,
        "model": "gpt-4o",
        "base_url": "https://api.openai.com/v1",
        "api_key": "sk-system-test",
        "temperature": 0.7,
        "top_p": 1.0,
        "stop": [],
        "enabled": True,
    }
    body.update(overrides)
    return body


class TestModelsCRUD:
    def test_create_and_list(self, web, model_name):
        resp = web.post(f"{BASE}/web/models", json=model_body(model_name))
        assert resp.status_code == 200

        resp = web.get(f"{BASE}/web/models")
        assert resp.status_code == 200
        names = [m["name"] for m in resp.json()["models"]]
        assert model_name in names
        created = next(m for m in resp.json()["models"] if m["name"] == model_name)
        assert "sk-system-test" not in created["api_key"], "api_key 应脱敏"

    def test_duplicate_name_conflict(self, web, model_name):
        assert web.post(f"{BASE}/web/models", json=model_body(model_name)).status_code == 200
        assert web.post(f"{BASE}/web/models", json=model_body(model_name)).status_code == 409

    def test_update_keeps_key_when_empty(self, web, model_name):
        web.post(f"{BASE}/web/models", json=model_body(model_name))
        resp = web.put(
            f"{BASE}/web/models/{model_name}",
            json=model_body(model_name, api_key="", temperature=1.2),
        )
        assert resp.status_code == 200
        got = next(m for m in web.get(f"{BASE}/web/models").json()["models"]
                   if m["name"] == model_name)
        assert got["temperature"] == 1.2

    def test_update_not_found(self, web):
        resp = web.put(f"{BASE}/web/models/no-such-model", json=model_body("no-such-model"))
        assert resp.status_code == 404

    def test_delete(self, web, model_name):
        web.post(f"{BASE}/web/models", json=model_body(model_name))
        assert web.delete(f"{BASE}/web/models/{model_name}").status_code == 200
        names = [m["name"] for m in web.get(f"{BASE}/web/models").json()["models"]]
        assert model_name not in names


class TestDefaultModel:
    def test_set_default_and_protection(self, web):
        a = f"t-{uuid.uuid4().hex[:8]}"
        b = f"t-{uuid.uuid4().hex[:8]}"
        orig_default = web.get(f"{BASE}/web/models").json().get("default", "")
        try:
            web.post(f"{BASE}/web/models", json=model_body(a))
            web.post(f"{BASE}/web/models", json=model_body(b))

            assert web.put(f"{BASE}/web/models/{a}/default").status_code == 200
            assert web.get(f"{BASE}/web/models").json()["default"] == a

            # 默认模型禁止删除 / 禁用
            assert web.delete(f"{BASE}/web/models/{a}").status_code == 409
            assert web.put(f"{BASE}/web/models/{a}",
                           json=model_body(a, api_key="", enabled=False)).status_code == 409

            # 禁用的模型不可设为默认
            web.put(f"{BASE}/web/models/{b}", json=model_body(b, api_key="", enabled=False))
            assert web.put(f"{BASE}/web/models/{b}/default").status_code == 400
        finally:
            if orig_default:
                web.put(f"{BASE}/web/models/{orig_default}/default")
            web.delete(f"{BASE}/web/models/{a}")
            web.delete(f"{BASE}/web/models/{b}")


class TestConnection:
    def test_test_endpoint_unreachable(self, web):
        resp = web.post(f"{BASE}/web/models/test", json={
            "base_url": "http://127.0.0.1:9",  # 不可达端口
            "api_key": "sk-x",
            "model": "gpt-4o",
        })
        assert resp.status_code == 200
        assert resp.json()["status"] == "unhealthy"
```

- [ ] **Step 4: 验证**

Python 测试由用户自行运行（`cd tests/python && pytest test_models_api.py -v`），本任务只保证文件就位、与 conftest 不冲突。

---

### Task 13: 全量验证

- [ ] **Step 1: 后端全量测试**

Run: `go build ./... && go test ./... 2>&1 | tail -30`
Expected: 全部 PASS

- [ ] **Step 2: 残留引用检查**

Run: `grep -rn "LLMConfig\|ValidateLLMConfig\|GetModelByName\|GetDefaultModel" --include="*.go" internal/ cmd/`
Expected: 无输出（旧 API 全部清除）

- [ ] **Step 3: 前端构建**

Run: `make web`（或 `cd web && npm run build`）
Expected: 构建成功，dist 更新

- [ ] **Step 4: 编译产物**

Run: `go build -o bin/groot ./cmd`
Expected: 编译成功

- [ ] **Step 5: 冒烟启动**

用临时 GROOT_HOME 启动一次（`GROOT_HOME=$(mktemp -d) bin/groot init && GROOT_HOME=... bin/groot -p 18080`），确认：零模型可启动、`/web/health` 中 LLM 检查为 unconfigured。验证后停止进程并清理临时目录。

- [ ] **Step 6: 提醒用户**

全部任务完成后，向用户报告结果并等待用户明确指示后再执行 git 提交（项目规范禁止自动提交）。
