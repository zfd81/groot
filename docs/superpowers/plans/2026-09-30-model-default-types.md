# 模型默认类型 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `models.is_default` 布尔标记替换为 `default_flags` 位掩码，支持默认对话 / 语音 / 视觉三种默认类型，转录接口缺省使用默认语音模型，并同步更新 Web 界面、手册与测试。

**Architecture:** 自底向上改造：`repo` 定义 `DefaultFlag` 类型与接口 → `modeldb` 用位运算 SQL 实现（清位用减法兼容 MySQL）→ `llm.ModelService` 提供 `Resolve(ctx, name, flag)` 与默认保护规则 → handler 层按 `type` 查询参数设置/取消默认、转录接口改用 `Resolve(DefaultVoice)` → 前端按 `default_types` / `defaults` 新结构展示与操作。

**Tech Stack:** Go（hertz、sqlx，SQLite/MySQL/PG 三方言）、Vue 3 + Pinia + Element Plus + vue-i18n、pytest。

**设计文档:** `docs/superpowers/specs/2026-09-30-model-default-types-design.md`

**全局约束:**

- 所有 commit 步骤 **需用户明确确认后执行**，禁止自动提交。
- Go 代码改完执行 `gofmt -w <文件>`。
- 编译命令：`go build -o dist/groot ./cmd/groot`（`cmd/` 下只有 `groot/` 子目录，入口包为 `./cmd/groot`，与 Makefile 一致）。
- 开发阶段直接改建表语句，不做数据迁移、不写 DROP COLUMN。本地已有的旧库需删除 `{GROOT_HOME}/groot.db` 后重建。
- Task 2、3 完成后 `go build ./...` 会因上层包尚未适配而失败，属预期；每个 Task 只跑本包测试。Task 4 结束后整体编译恢复。

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/repo/model.go` | 修改 | `DefaultFlag` 类型、`Model.DefaultFlags` 字段、`ModelRepo` 接口签名 |
| `internal/repo/model_test.go` | 新建 | `DefaultFlag` 字符串互转与 `Has` 测试 |
| `internal/db/migrate.go` | 修改 | 三方言 `models` 建表语句改列 |
| `internal/db/migrate_test.go` | 修改 | DDL 字符串检查 |
| `internal/repo/modeldb/model.go` | 修改 | 位运算 SQL：`GetDefault` / `SetDefault` / `ClearDefault` |
| `internal/repo/modeldb/model_test.go` | 修改 | 仓库层默认类型测试 |
| `internal/llm/service.go` | 修改 | `Resolve`、`ClearDefault`、错误定义、默认保护 |
| `internal/llm/service_test.go` | 修改 | 业务层规则测试 |
| `internal/api/types/types.go` | 修改 | `ModelInfo.DefaultTypes`、`ModelsResponse.Defaults` |
| `internal/api/handler/models.go` | 修改 | `type` 参数、`ClearDefault` handler、错误映射 |
| `internal/api/router.go` | 修改 | 注册 `DELETE /web/models/:name/default` |
| `internal/api/handler/models_test.go` | 修改 | 模型接口测试 |
| `internal/api/handler/transcription.go` | 修改 | 模型解析改为 `Resolve(DefaultVoice)` |
| `internal/api/handler/transcription_test.go` | 修改 | 转录模型解析顺序测试 |
| `internal/api/handler/setting.go` | 修改 | `PutVoice` 空模型校验默认语音模型 |
| `internal/api/handler/setting_test.go` | 修改 | 语音设置校验测试 |
| `web/src/api/types.ts` | 修改 | `DefaultType`、`DefaultModels` 类型 |
| `web/src/stores/meta.ts` | 修改 | 保存 `defaults`，`defaultModel` 改为计算属性 |
| `web/src/api/voice.ts` | 修改 | 转录经 `X-Model-Name` 传模型 |
| `web/src/components/chat/ChatInput.vue` | 修改 | 传 `voice.model`、话筒警告态条件 |
| `web/src/components/settings/ModelsPanel.vue` | 修改 | 三种默认标签与菜单项 |
| `web/src/components/settings/SettingsModal.vue` | 修改 | 「跟随默认语音模型」选项 |
| `web/src/i18n/messages/zh-cn.ts`、`en.ts` | 修改 | 文案 |
| `README.md` | 修改 | 默认模型规则、转录模型取用顺序 |
| `tests/TEST_CASES.md` | 修改 | 测试用例点 |
| `tests/python/conftest.py`、`test_models_api.py`、`test_multi_agent_real_llm.py` | 修改 | 适配新响应结构，新增默认类型系统测试 |

---

### Task 1: repo 层 DefaultFlag 类型

**Files:**
- Modify: `internal/repo/model.go`
- Create: `internal/repo/model_test.go`

本 Task 只新增类型，不删除 `IsDefault`，全仓编译保持通过。

- [ ] **Step 1: 写失败测试**

新建 `internal/repo/model_test.go`：

```go
package repo

import "testing"

func TestDefaultFlag_StringAndParse(t *testing.T) {
	cases := []struct {
		flag DefaultFlag
		str  string
		val  int
	}{
		{DefaultChat, "chat", 1},
		{DefaultVoice, "voice", 2},
		{DefaultVision, "vision", 4},
	}
	for _, c := range cases {
		if int(c.flag) != c.val {
			t.Errorf("%s 位值 = %d, want %d", c.str, int(c.flag), c.val)
		}
		if c.flag.String() != c.str {
			t.Errorf("String() = %q, want %q", c.flag.String(), c.str)
		}
		got, ok := ParseDefaultFlag(c.str)
		if !ok || got != c.flag {
			t.Errorf("ParseDefaultFlag(%q) = %v, %v", c.str, got, ok)
		}
	}
	for _, bad := range []string{"", "CHAT", "audio", "chat,voice"} {
		if _, ok := ParseDefaultFlag(bad); ok {
			t.Errorf("ParseDefaultFlag(%q) 应返回 false", bad)
		}
	}
	if len(AllDefaultFlags) != 3 || AllDefaultFlags[0] != DefaultChat ||
		AllDefaultFlags[1] != DefaultVoice || AllDefaultFlags[2] != DefaultVision {
		t.Errorf("AllDefaultFlags 顺序应为 chat、voice、vision: %v", AllDefaultFlags)
	}
}

func TestModel_Has(t *testing.T) {
	m := &Model{DefaultFlags: DefaultChat | DefaultVision}
	if !m.Has(DefaultChat) || !m.Has(DefaultVision) {
		t.Error("应持有 chat 与 vision")
	}
	if m.Has(DefaultVoice) {
		t.Error("不应持有 voice")
	}
	if (&Model{}).Has(DefaultChat) {
		t.Error("零值模型不应持有任何默认类型")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/repo/ -run 'TestDefaultFlag|TestModel_Has' -v`
Expected: 编译失败，`undefined: DefaultFlag`

- [ ] **Step 3: 实现类型**

在 `internal/repo/model.go` 的 `Model` 结构体定义之前插入：

```go
// DefaultFlag 模型默认类型，按位组合存于 models.default_flags。
// 新增默认类型时分配新的位，表结构不变。
type DefaultFlag int

const (
	DefaultChat   DefaultFlag = 1 << iota // 1 默认对话模型
	DefaultVoice                          // 2 默认语音模型
	DefaultVision                         // 4 默认视觉模型
)

// AllDefaultFlags 全部默认类型，顺序即对外展示顺序
var AllDefaultFlags = []DefaultFlag{DefaultChat, DefaultVoice, DefaultVision}

// String 返回默认类型的字符串标识（chat / voice / vision），供 HTTP 层使用
func (f DefaultFlag) String() string {
	switch f {
	case DefaultChat:
		return "chat"
	case DefaultVoice:
		return "voice"
	case DefaultVision:
		return "vision"
	}
	return ""
}

// ParseDefaultFlag 把字符串标识解析为单个默认类型；未知标识返回 false
func ParseDefaultFlag(s string) (DefaultFlag, bool) {
	for _, f := range AllDefaultFlags {
		if f.String() == s {
			return f, true
		}
	}
	return 0, false
}
```

在 `Model` 结构体中，`IsDefault` 行下方加字段（`IsDefault` 在 Task 2 删除）：

```go
	DefaultFlags        DefaultFlag // 持有的默认类型，每种类型全表至多一条持有
```

在 `Model` 结构体定义之后加方法：

```go
// Has 判断模型是否持有某一默认类型
func (m *Model) Has(f DefaultFlag) bool { return m.DefaultFlags&f != 0 }
```

- [ ] **Step 4: 运行测试确认通过**

Run: `gofmt -w internal/repo/model.go internal/repo/model_test.go && go test ./internal/repo/ -run 'TestDefaultFlag|TestModel_Has' -v`
Expected: PASS

- [ ] **Step 5: Commit（需用户明确确认后执行）**

```bash
git add internal/repo/model.go internal/repo/model_test.go
git commit -m "feat(repo): 新增模型默认类型 DefaultFlag"
```

---

### Task 2: 建表语句与 modeldb 位运算实现

**Files:**
- Modify: `internal/db/migrate.go:268,422,576`
- Modify: `internal/db/migrate_test.go`（文件末尾追加）
- Modify: `internal/repo/model.go`（删除 `IsDefault`、改接口）
- Modify: `internal/repo/modeldb/model.go`
- Test: `internal/repo/modeldb/model_test.go`

- [ ] **Step 1: 写 DDL 失败测试**

在 `internal/db/migrate_test.go` 末尾追加：

```go
// TestDDLStatements_ModelsHasDefaultFlags 三方言 models 建表语句统一使用整数位掩码列
// default_flags，且不再包含 is_default。
func TestDDLStatements_ModelsHasDefaultFlags(t *testing.T) {
	for _, d := range []Dialect{DialectSQLite, DialectMySQL, DialectPostgres} {
		var createStmt string
		for _, stmt := range ddlStatements(d) {
			if strings.Contains(stmt, "CREATE TABLE IF NOT EXISTS models") {
				createStmt = stmt
				break
			}
		}
		if createStmt == "" {
			t.Errorf("dialect %v: 找不到 models 建表语句", d)
			continue
		}
		if !strings.Contains(createStmt, "default_flags         INTEGER NOT NULL DEFAULT 0") {
			t.Errorf("dialect %v: models 应含 default_flags INTEGER 列:\n%s", d, createStmt)
		}
		if strings.Contains(createStmt, "is_default") {
			t.Errorf("dialect %v: models 不应再含 is_default 列:\n%s", d, createStmt)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/db/ -run TestDDLStatements_ModelsHasDefaultFlags -v`
Expected: FAIL，三个方言均报「应含 default_flags INTEGER 列」

- [ ] **Step 3: 修改建表语句**

`internal/db/migrate.go` 三处替换（保持原有缩进与列对齐）：

- 第 268 行（SQLite）`is_default            INTEGER NOT NULL DEFAULT 0,`
- 第 422 行（MySQL）`is_default            TINYINT(1) NOT NULL DEFAULT 0,`
- 第 576 行（PG）`is_default            BOOLEAN NOT NULL DEFAULT FALSE,`

均替换为：

```sql
			default_flags         INTEGER NOT NULL DEFAULT 0,
```

Run: `go test ./internal/db/ -v`
Expected: PASS

- [ ] **Step 4: 修改 repo 接口与字段**

`internal/repo/model.go`：删除 `Model` 中的 `IsDefault bool // 全表至多一条为真` 一行；把 `ModelRepo` 接口整体替换为：

```go
// ModelRepo 模型配置存储接口
type ModelRepo interface {
	// Create 按 m.DefaultFlags 原样写入，每类型默认唯一性由调用方（业务层）保证；不回填 m.ID
	Create(ctx context.Context, m *Model) error
	// GetByName 按名称查询，未找到返回 ErrNotFound
	GetByName(ctx context.Context, name string) (*Model, error)
	// GetDefault 查询持有 flag 类型默认的模型，无则返回 ErrNotFound
	GetDefault(ctx context.Context, flag DefaultFlag) (*Model, error)
	// List 返回全部模型，按 name 升序
	List(ctx context.Context) ([]*Model, error)
	// Update 按原名称 name 更新除 default_flags、created_at 外的全部字段（含重命名为 m.Name）；
	// 未找到返回 ErrNotFound。默认类型仅由 SetDefault / ClearDefault 变更
	Update(ctx context.Context, name string, m *Model) error
	// Delete 按名称删除；未找到返回 ErrNotFound
	Delete(ctx context.Context, name string) error
	// SetDefault 事务内先清除全表 flag 位再为目标行置位；目标不存在返回 ErrNotFound
	SetDefault(ctx context.Context, name string, flag DefaultFlag) error
	// ClearDefault 清除目标行的 flag 位（未持有时无操作）；目标不存在返回 ErrNotFound
	ClearDefault(ctx context.Context, name string, flag DefaultFlag) error
	Count(ctx context.Context) (int64, error)
}
```

- [ ] **Step 5: 写 modeldb 失败测试**

`internal/repo/modeldb/model_test.go`：

(a) 第 61-62 行替换为：

```go
	if !got.Enabled || got.DefaultFlags != 0 {
		t.Errorf("字段错误: enabled=%v default_flags=%v", got.Enabled, got.DefaultFlags)
	}
```

(b) 把 `TestModelRepo_SetDefault`（第 147-190 行）整体替换为下面三个函数：

```go
// flagsOf 读回指定模型的默认类型位掩码
func flagsOf(t *testing.T, r repo.ModelRepo, name string) repo.DefaultFlag {
	t.Helper()
	m, err := r.GetByName(context.Background(), name)
	if err != nil {
		t.Fatalf("GetByName %s: %v", name, err)
	}
	return m.DefaultFlags
}

func TestModelRepo_SetDefault(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for _, n := range []string{"m1", "m2"} {
		if err := r.Create(ctx, newModel(n)); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}

	for _, f := range repo.AllDefaultFlags {
		if _, err := r.GetDefault(ctx, f); !errors.Is(err, repo.ErrNotFound) {
			t.Errorf("无 %s 默认时 GetDefault 应返回 ErrNotFound, got %v", f, err)
		}
	}

	if err := r.SetDefault(ctx, "m1", repo.DefaultChat); err != nil {
		t.Fatalf("SetDefault m1 chat: %v", err)
	}
	if d, err := r.GetDefault(ctx, repo.DefaultChat); err != nil || d.Name != "m1" {
		t.Fatalf("GetDefault chat: %v, %+v", err, d)
	}

	// 转移对话默认：全表仍只有一个模型持有 chat
	if err := r.SetDefault(ctx, "m2", repo.DefaultChat); err != nil {
		t.Fatalf("SetDefault m2 chat: %v", err)
	}
	list, _ := r.List(ctx)
	count := 0
	for _, m := range list {
		if m.Has(repo.DefaultChat) {
			count++
			if m.Name != "m2" {
				t.Errorf("默认对话模型应为 m2, got %s", m.Name)
			}
		}
	}
	if count != 1 {
		t.Errorf("默认对话模型应有且只有 1 个, got %d", count)
	}

	// 一模型多默认：m2 同时持有 chat 与 vision
	if err := r.SetDefault(ctx, "m2", repo.DefaultVision); err != nil {
		t.Fatalf("SetDefault m2 vision: %v", err)
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultChat|repo.DefaultVision {
		t.Errorf("m2 default_flags = %d, want 5", got)
	}

	// 类型互不影响：m1 设为 voice 不动 m2 的 chat、vision
	if err := r.SetDefault(ctx, "m1", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault m1 voice: %v", err)
	}
	if got := flagsOf(t, r, "m1"); got != repo.DefaultVoice {
		t.Errorf("m1 default_flags = %d, want 2", got)
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultChat|repo.DefaultVision {
		t.Errorf("m2 default_flags = %d, want 5", got)
	}
	if d, err := r.GetDefault(ctx, repo.DefaultVoice); err != nil || d.Name != "m1" {
		t.Errorf("GetDefault voice: %v, %+v", err, d)
	}

	if err := r.SetDefault(ctx, "ghost", repo.DefaultChat); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("SetDefault 不存在模型应返回 ErrNotFound, got %v", err)
	}
	// 目标不存在时事务回滚，原持有者不受影响
	if d, err := r.GetDefault(ctx, repo.DefaultChat); err != nil || d.Name != "m2" {
		t.Errorf("SetDefault 失败后对话默认应仍为 m2: %v, %+v", err, d)
	}
}

func TestModelRepo_ClearDefault(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for _, n := range []string{"m1", "m2"} {
		if err := r.Create(ctx, newModel(n)); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	_ = r.SetDefault(ctx, "m1", repo.DefaultChat)
	_ = r.SetDefault(ctx, "m1", repo.DefaultVision)
	_ = r.SetDefault(ctx, "m2", repo.DefaultVoice)

	// 只清目标位
	if err := r.ClearDefault(ctx, "m1", repo.DefaultVision); err != nil {
		t.Fatalf("ClearDefault m1 vision: %v", err)
	}
	if got := flagsOf(t, r, "m1"); got != repo.DefaultChat {
		t.Errorf("m1 default_flags = %d, want 1", got)
	}
	if _, err := r.GetDefault(ctx, repo.DefaultVision); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("清除后 vision 应无默认, got %v", err)
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultVoice {
		t.Errorf("m2 不应受影响, default_flags = %d", got)
	}

	// 清除未持有的位：无操作、不报错
	if err := r.ClearDefault(ctx, "m1", repo.DefaultVoice); err != nil {
		t.Errorf("清除未持有的位应成功, got %v", err)
	}
	if got := flagsOf(t, r, "m1"); got != repo.DefaultChat {
		t.Errorf("m1 default_flags = %d, want 1", got)
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultVoice {
		t.Errorf("清除 m1 未持有的 voice 不应影响 m2, got %d", got)
	}

	if err := r.ClearDefault(ctx, "ghost", repo.DefaultVoice); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("ClearDefault 不存在模型应返回 ErrNotFound, got %v", err)
	}
}

// TestModelRepo_CreateAndUpdateDefaultFlags Create 原样写入 default_flags；Update 不改动它。
func TestModelRepo_CreateAndUpdateDefaultFlags(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	m := newModel("m1")
	m.DefaultFlags = repo.DefaultChat | repo.DefaultVoice
	if err := r.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := flagsOf(t, r, "m1"); got != 3 {
		t.Errorf("Create 后 default_flags = %d, want 3", got)
	}

	upd := newModel("m1")
	upd.DefaultFlags = 0
	upd.Model = "gpt-4o-mini"
	if err := r.Update(ctx, "m1", upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := r.GetByName(ctx, "m1")
	if got.Model != "gpt-4o-mini" || got.DefaultFlags != 3 {
		t.Errorf("Update 应改 model 且不改 default_flags: %+v", got)
	}
}
```

- [ ] **Step 6: 运行确认失败**

Run: `go test ./internal/repo/modeldb/ -v`
Expected: 编译失败（`row.IsDefault`、`GetDefault` 参数个数不符、`ClearDefault` 未实现等）

- [ ] **Step 7: 实现 modeldb**

`internal/repo/modeldb/model.go`：

(a) `modelRow` 中 ``IsDefault bool `db:"is_default"` `` 替换为：

```go
	DefaultFlags        int     `db:"default_flags"`
```

(b) `modelColumns` 中的 `is_default` 替换为 `default_flags`：

```go
const modelColumns = `id, name, base_url, api_key, model, max_completion_tokens, max_context_tokens,
	temperature, top_p, frequency_penalty, presence_penalty, seed, stop, thinking,
	default_flags, enabled, created_at, updated_at`
```

(c) `rowToModel` 中 `IsDefault: row.IsDefault,` 替换为：

```go
		DefaultFlags:        repo.DefaultFlag(row.DefaultFlags),
```

(d) `Create` 中列名 `is_default` 替换为 `default_flags`，参数 `m.IsDefault` 替换为 `int(m.DefaultFlags)`（占位符数量不变，仍为 17 个）：

```go
		default_flags, enabled, created_at, updated_at)
```
```go
		int(m.DefaultFlags), m.Enabled, m.CreatedAt.UnixMilli(), m.UpdatedAt.UnixMilli(),
```

(e) `GetDefault` 整体替换为：

```go
func (r *modelRepo) GetDefault(ctx context.Context, flag repo.DefaultFlag) (*repo.Model, error) {
	var row modelRow
	// ORDER BY id LIMIT 1 兜底：异常数据出现多条持有同一位时取最早一条，避免 Get 报错
	q := r.db.Rebind(`SELECT ` + modelColumns + ` FROM models WHERE (default_flags & ?) <> 0 ORDER BY id LIMIT 1`)
	err := r.db.GetContext(ctx, &row, q, int(flag))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToModel(row), nil
}
```

(f) `SetDefault` 整体替换为下面两个函数：

```go
// SetDefault 清位使用减法而非 default_flags & ~flag：MySQL 的 ~ 结果为无符号 64 位整数，
// 与 SQLite、PG 行为不一致；WHERE 已保证该位为 1，减去位值与清位等价。
// 先清全表（含目标行）再为目标行加位，目标不存在时整个事务回滚。
func (r *modelRepo) SetDefault(ctx context.Context, name string, flag repo.DefaultFlag) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	f := int(flag)
	if _, err := tx.ExecContext(ctx,
		tx.Rebind(`UPDATE models SET default_flags = default_flags - ? WHERE (default_flags & ?) <> 0`),
		f, f); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		tx.Rebind(`UPDATE models SET default_flags = default_flags | ?, updated_at=? WHERE name=?`),
		f, time.Now().UnixMilli(), name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return tx.Commit()
}

// ClearDefault 清除目标行的 flag 位。先确认模型存在，再按「该位为 1」条件做减法，
// 未持有该位时 UPDATE 不命中任何行，视为成功。
func (r *modelRepo) ClearDefault(ctx context.Context, name string, flag repo.DefaultFlag) error {
	if _, err := r.GetByName(ctx, name); err != nil {
		return err
	}
	f := int(flag)
	q := r.db.Rebind(`UPDATE models SET default_flags = default_flags - ?, updated_at=? WHERE name=? AND (default_flags & ?) <> 0`)
	_, err := r.db.ExecContext(ctx, q, f, time.Now().UnixMilli(), name, f)
	return err
}
```

- [ ] **Step 8: 运行确认通过**

Run: `gofmt -w internal/repo/model.go internal/repo/modeldb/model.go internal/repo/modeldb/model_test.go internal/db/migrate_test.go && go test ./internal/repo/... ./internal/db/... -v`
Expected: PASS

- [ ] **Step 9: Commit（需用户明确确认后执行）**

```bash
git add internal/db/migrate.go internal/db/migrate_test.go internal/repo/model.go internal/repo/modeldb/
git commit -m "feat(modeldb): models.is_default 改为 default_flags 位掩码"
```

---

### Task 3: llm.ModelService 按类型解析与默认规则

**Files:**
- Modify: `internal/llm/service.go`
- Test: `internal/llm/service_test.go`

- [ ] **Step 1: 更新已有测试调用并写失败测试**

`internal/llm/service_test.go`：

(a) 第 205、230、233 行的 `s.SetDefault(ctx, "m2")`、`s.SetDefault(ctx, "m2")`、`s.SetDefault(ctx, "ghost")` 分别改为带类型参数：

```go
	if err := s.SetDefault(ctx, "m2", repo.DefaultChat); err != nil {
```
```go
	if err := s.SetDefault(ctx, "m2", repo.DefaultChat); !errors.Is(err, ErrModelDisabled) {
```
```go
	if err := s.SetDefault(ctx, "ghost", repo.DefaultChat); !errors.Is(err, ErrModelNotFound) {
```

(b) import 中加入 `"strings"`（若未导入）。

(c) 文件末尾追加：

```go
func TestModelService_ResolveByFlag(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	for _, n := range []string{"m1", "m2"} {
		if err := s.Create(ctx, validModel(n)); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}

	// 名称优先，与类型无关
	if m, err := s.Resolve(ctx, "m2", repo.DefaultVoice); err != nil || m.Name != "m2" {
		t.Errorf("按名称解析: %v, %+v", err, m)
	}

	// 无该类型默认：可被识别为 ErrNoDefaultModel，信息区分类型
	_, err := s.Resolve(ctx, "", repo.DefaultVoice)
	if !errors.Is(err, ErrNoDefaultModel) || !strings.Contains(err.Error(), "默认语音模型") {
		t.Errorf("无语音默认应返回语音类错误, got %v", err)
	}
	_, err = s.Resolve(ctx, "", repo.DefaultVision)
	if !errors.Is(err, ErrNoDefaultModel) || !strings.Contains(err.Error(), "默认视觉模型") {
		t.Errorf("无视觉默认应返回视觉类错误, got %v", err)
	}

	// 按类型回落
	if err := s.SetDefault(ctx, "m2", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault m2 voice: %v", err)
	}
	if m, err := s.Resolve(ctx, "", repo.DefaultVoice); err != nil || m.Name != "m2" {
		t.Errorf("默认语音模型应为 m2: %v, %+v", err, m)
	}
	if m, err := s.Resolve(ctx, "", repo.DefaultChat); err != nil || m.Name != "m1" {
		t.Errorf("默认对话模型应仍为 m1: %v, %+v", err, m)
	}

	// 一模型多默认
	if err := s.SetDefault(ctx, "m1", repo.DefaultVision); err != nil {
		t.Fatalf("SetDefault m1 vision: %v", err)
	}
	if m, err := s.Resolve(ctx, "", repo.DefaultVision); err != nil || m.Name != "m1" {
		t.Errorf("默认视觉模型应为 m1: %v, %+v", err, m)
	}
}

// TestModelService_NoChatDefaultMessage 空库时 GetByName("") 的错误信息保持对话类文案。
func TestModelService_NoChatDefaultMessage(t *testing.T) {
	s := newTestService(t)
	_, err := s.GetByName(context.Background(), "")
	if !errors.Is(err, ErrNoDefaultModel) || err.Error() != ErrNoDefaultModel.Error() {
		t.Errorf("want %q, got %v", ErrNoDefaultModel, err)
	}
}

// TestModelService_CreateIgnoresDefaultFlags 非首个模型创建时忽略调用方传入的默认类型。
func TestModelService_CreateIgnoresDefaultFlags(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	m2 := validModel("m2")
	m2.DefaultFlags = repo.DefaultChat | repo.DefaultVoice
	if err := s.Create(ctx, m2); err != nil {
		t.Fatalf("Create m2: %v", err)
	}
	got, _ := s.GetStored(ctx, "m2")
	if got.DefaultFlags != 0 {
		t.Errorf("非首个模型 default_flags 应为 0, got %d", got.DefaultFlags)
	}
	first, _ := s.GetStored(ctx, "m1")
	if first.DefaultFlags != repo.DefaultChat {
		t.Errorf("首个模型应只持有 chat, got %d", first.DefaultFlags)
	}
}

func TestModelService_VoiceDefaultProtection(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	for _, n := range []string{"m1", "m2"} {
		if err := s.Create(ctx, validModel(n)); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	if err := s.SetDefault(ctx, "m2", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault m2 voice: %v", err)
	}

	// 持有语音默认同样受保护
	if err := s.Delete(ctx, "m2"); !errors.Is(err, ErrDefaultProtected) {
		t.Errorf("删除默认语音模型应被拒绝, got %v", err)
	}
	upd := validModel("m2")
	upd.APIKey = ""
	upd.Enabled = false
	if err := s.Update(ctx, "m2", upd); !errors.Is(err, ErrDefaultProtected) {
		t.Errorf("禁用默认语音模型应被拒绝, got %v", err)
	}

	// 取消语音默认后可删除
	if err := s.ClearDefault(ctx, "m2", repo.DefaultVoice); err != nil {
		t.Fatalf("ClearDefault m2 voice: %v", err)
	}
	if err := s.Delete(ctx, "m2"); err != nil {
		t.Errorf("取消默认后应可删除: %v", err)
	}
}

func TestModelService_ClearDefault(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}

	if err := s.ClearDefault(ctx, "m1", repo.DefaultChat); !errors.Is(err, ErrChatDefaultRequired) {
		t.Errorf("取消对话默认应被拒绝, got %v", err)
	}
	if m, err := s.GetByName(ctx, ""); err != nil || m.Name != "m1" {
		t.Errorf("拒绝后对话默认应仍为 m1: %v, %+v", err, m)
	}
	// 未持有该类型：直接成功
	if err := s.ClearDefault(ctx, "m1", repo.DefaultVoice); err != nil {
		t.Errorf("取消未持有的默认应成功, got %v", err)
	}
	if err := s.ClearDefault(ctx, "ghost", repo.DefaultVoice); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/llm/ -run 'TestModelService' -v`
Expected: 编译失败（`s.Resolve` 未定义、`SetDefault` 参数个数不符、`ErrChatDefaultRequired` 未定义等）

- [ ] **Step 3: 实现 service**

`internal/llm/service.go`：

(a) 错误变量块整体替换为：

```go
var (
	ErrModelNotFound       = errors.New("模型不存在")
	ErrModelDisabled       = errors.New("模型已禁用")
	ErrNoDefaultModel      = errors.New("尚未配置模型，请在设置中创建模型")
	ErrNameExists          = errors.New("模型名称已存在")
	ErrDefaultProtected    = errors.New("默认模型不允许删除或禁用，请先取消其默认标记")
	ErrChatDefaultRequired = errors.New("默认对话模型不能取消，请将其他模型设为默认对话模型")
	ErrInvalidModel        = errors.New("模型配置无效")
)

// noDefaultError 某一类型尚无默认模型。错误信息按类型区分；
// errors.Is(err, ErrNoDefaultModel) 对所有类型成立，调用方无需逐类判断。
type noDefaultError struct{ flag repo.DefaultFlag }

func (e noDefaultError) Error() string {
	switch e.flag {
	case repo.DefaultVoice:
		return "未配置默认语音模型，请在模型管理中设置"
	case repo.DefaultVision:
		return "未配置默认视觉模型，请在模型管理中设置"
	}
	return ErrNoDefaultModel.Error()
}

func (e noDefaultError) Is(target error) bool { return target == ErrNoDefaultModel }
```

(b) `GetByName` 整体替换为：

```go
// Resolve 按用途解析可用模型：name 非空按名称取，为空取 flag 类型的默认模型。
// 模型不存在返回 ErrModelNotFound，禁用返回 ErrModelDisabled，
// 该类型无默认返回可被 errors.Is 识别为 ErrNoDefaultModel 的错误（信息区分类型）。
// APIKey 中的 ${ENV_VAR} 引用会被展开。
func (s *ModelService) Resolve(ctx context.Context, name string, flag repo.DefaultFlag) (*repo.Model, error) {
	var m *repo.Model
	var err error
	if name == "" {
		m, err = s.repo.GetDefault(ctx, flag)
		if errors.Is(err, repo.ErrNotFound) {
			return nil, noDefaultError{flag}
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

// GetByName 按名称获取可用模型；name 为空时返回默认对话模型。供对话与子 Agent 使用。
func (s *ModelService) GetByName(ctx context.Context, name string) (*repo.Model, error) {
	return s.Resolve(ctx, name, repo.DefaultChat)
}
```

(c) `Create`：注释首句改为「库中没有任何模型时，新模型自动成为默认对话模型并强制启用。」；把

```go
	rec := *m
	if n == 0 {
		rec.IsDefault = true
		rec.Enabled = true
	}
```

替换为：

```go
	rec := *m
	// 默认类型只经 SetDefault / ClearDefault 变更，调用方传入的值一律忽略
	rec.DefaultFlags = 0
	if n == 0 {
		rec.DefaultFlags = repo.DefaultChat
		rec.Enabled = true
	}
```

(d) `Update`：注释第三行改为「持有任一默认类型的模型不允许禁用（default_flags 本身不通过 Update 修改）。」；`upd.IsDefault = existing.IsDefault` 改为 `upd.DefaultFlags = existing.DefaultFlags`；`if existing.IsDefault && !upd.Enabled {` 改为 `if existing.DefaultFlags != 0 && !upd.Enabled {`。

(e) `Delete`：注释改为「Delete 删除模型；持有任一默认类型的模型返回 ErrDefaultProtected。」；`if existing.IsDefault {` 改为 `if existing.DefaultFlags != 0 {`。

(f) `SetDefault` 整体替换为下面两个函数：

```go
// SetDefault 把指定模型设为 flag 类型的默认，原持有者自动失去该类型；
// 禁用的模型返回 ErrModelDisabled。
func (s *ModelService) SetDefault(ctx context.Context, name string, flag repo.DefaultFlag) error {
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
	return s.repo.SetDefault(ctx, name, flag)
}

// ClearDefault 取消指定模型的 flag 类型默认。对话默认必须始终存在，
// 只能通过把其他模型设为默认来转移，返回 ErrChatDefaultRequired；
// 模型未持有该类型时直接成功。
func (s *ModelService) ClearDefault(ctx context.Context, name string, flag repo.DefaultFlag) error {
	if flag == repo.DefaultChat {
		return ErrChatDefaultRequired
	}
	m, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return err
	}
	if !m.Has(flag) {
		return nil
	}
	return s.repo.ClearDefault(ctx, name, flag)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `gofmt -w internal/llm/service.go internal/llm/service_test.go && go test ./internal/llm/... -v`
Expected: PASS（含既有 `TestModelService_*` 用例）

- [ ] **Step 5: Commit（需用户明确确认后执行）**

```bash
git add internal/llm/service.go internal/llm/service_test.go
git commit -m "feat(llm): ModelService 支持按默认类型解析与取消默认"
```

---

### Task 4: 模型管理接口（types、handler、router）

**Files:**
- Modify: `internal/api/types/types.go:195-219`
- Modify: `internal/api/handler/models.go`
- Modify: `internal/api/router.go:57`
- Test: `internal/api/handler/models_test.go`

- [ ] **Step 1: 更新已有测试并写失败测试**

`internal/api/handler/models_test.go`：

(a) import 中加入 `"github.com/zfd81/groot/internal/repo"`。

(b) 第 57-63 行替换为：

```go
	// Create 响应应回读库中实际状态：首个模型自动成为默认对话模型且启用
	var created types.ModelInfo
	if err := json.Unmarshal(rc.Response.Body(), &created); err != nil {
		t.Fatalf("unmarshal create resp: %v", err)
	}
	if len(created.DefaultTypes) != 1 || created.DefaultTypes[0] != "chat" || !created.Enabled {
		t.Errorf("Create 响应首个模型应 default_types=[chat] enabled=true, got %+v", created)
	}
```

(c) 第 71 行 `resp.Default != "gpt-4o"` 改为 `resp.Defaults.Chat != "gpt-4o"`；第 78-80 行替换为：

```go
	if len(resp.Models[0].DefaultTypes) != 1 || resp.Models[0].DefaultTypes[0] != "chat" {
		t.Errorf("首个模型应为默认对话模型, got %v", resp.Models[0].DefaultTypes)
	}
	if resp.Defaults.Voice != "" || resp.Defaults.Vision != "" {
		t.Errorf("未设置的类型应为空串, got %+v", resp.Defaults)
	}
```

(d) 文件末尾追加：

```go
// callJSONQuery 与 callJSON 相同，但带完整请求 URI（含查询串），供读取 type 参数的接口使用。
func callJSONQuery(h func(context.Context, *app.RequestContext), method, uri string, params map[string]string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	rc.Request.SetRequestURI(uri)
	for k, v := range params {
		rc.Params = append(rc.Params, param.Param{Key: k, Value: v})
	}
	h(context.Background(), rc)
	return rc
}

// newModelsHandlerWithTwo 建 gpt-4o（默认对话）与 backup 两个模型。
func newModelsHandlerWithTwo(t *testing.T) *ModelsHandler {
	t.Helper()
	h := newModelsHandlerForTest(t)
	callJSON(h.Create, consts.MethodPost, createBody, nil)
	second := strings.Replace(createBody, `"gpt-4o"`, `"backup"`, 1)
	callJSON(h.Create, consts.MethodPost, second, nil)
	return h
}

func listModels(t *testing.T, h *ModelsHandler) types.ModelsResponse {
	t.Helper()
	rc := callJSON(h.List, consts.MethodGet, "", nil)
	var resp types.ModelsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestModelsHandler_SetDefaultByType(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	name := map[string]string{"name": "backup"}

	rc := callJSONQuery(h.SetDefault, consts.MethodPut, "/web/models/backup/default?type=voice", name)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("SetDefault voice: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	rc = callJSONQuery(h.SetDefault, consts.MethodPut, "/web/models/gpt-4o/default?type=vision",
		map[string]string{"name": "gpt-4o"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("SetDefault vision: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}

	resp := listModels(t, h)
	want := types.DefaultModels{Chat: "gpt-4o", Voice: "backup", Vision: "gpt-4o"}
	if resp.Defaults != want {
		t.Errorf("defaults = %+v, want %+v", resp.Defaults, want)
	}
	for _, m := range resp.Models {
		got := strings.Join(m.DefaultTypes, ",")
		switch m.Name {
		case "gpt-4o":
			if got != "chat,vision" {
				t.Errorf("gpt-4o default_types = %q, want chat,vision", got)
			}
		case "backup":
			if got != "voice" {
				t.Errorf("backup default_types = %q, want voice", got)
			}
		}
	}
}

// TestModelsHandler_SetDefaultOmittedType type 省略时按 chat 处理。
func TestModelsHandler_SetDefaultOmittedType(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	rc := callJSONQuery(h.SetDefault, consts.MethodPut, "/web/models/backup/default",
		map[string]string{"name": "backup"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("SetDefault: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if d := listModels(t, h).Defaults.Chat; d != "backup" {
		t.Errorf("defaults.chat = %q, want backup", d)
	}
}

func TestModelsHandler_DefaultInvalidType(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	name := map[string]string{"name": "backup"}
	for _, fn := range []func(context.Context, *app.RequestContext){h.SetDefault, h.ClearDefault} {
		rc := callJSONQuery(fn, consts.MethodPut, "/web/models/backup/default?type=audio", name)
		if rc.Response.StatusCode() != 400 || !strings.Contains(string(rc.Response.Body()), "invalid_request") {
			t.Errorf("非法 type 应 400 invalid_request, got %d %s", rc.Response.StatusCode(), rc.Response.Body())
		}
	}
}

func TestModelsHandler_ClearDefault(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	name := map[string]string{"name": "backup"}
	callJSONQuery(h.SetDefault, consts.MethodPut, "/web/models/backup/default?type=voice", name)

	// 持有语音默认的模型不可删除
	rc := callJSON(h.Delete, consts.MethodDelete, "", name)
	if rc.Response.StatusCode() != 409 {
		t.Errorf("删除默认语音模型应 409, got %d", rc.Response.StatusCode())
	}

	rc = callJSONQuery(h.ClearDefault, consts.MethodDelete, "/web/models/backup/default?type=voice", name)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("ClearDefault voice: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if d := listModels(t, h).Defaults.Voice; d != "" {
		t.Errorf("取消后 defaults.voice 应为空, got %q", d)
	}
	for _, m := range listModels(t, h).Models {
		if m.Name == "backup" && (m.DefaultTypes == nil || len(m.DefaultTypes) != 0) {
			t.Errorf("非默认模型 default_types 应为空数组, got %#v", m.DefaultTypes)
		}
	}

	rc = callJSON(h.Delete, consts.MethodDelete, "", name)
	if rc.Response.StatusCode() != 200 {
		t.Errorf("取消默认后删除应成功, got %d", rc.Response.StatusCode())
	}
}

// TestModelsHandler_EmptyDefaultTypesSerialized 非默认模型序列化为 "default_types":[] 而不是 null。
func TestModelsHandler_EmptyDefaultTypesSerialized(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	rc := callJSON(h.List, consts.MethodGet, "", nil)
	if !strings.Contains(string(rc.Response.Body()), `"default_types":[]`) {
		t.Errorf("响应应含 \"default_types\":[]: %s", rc.Response.Body())
	}
}

func TestModelsHandler_ClearChatDefaultRejected(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	for _, uri := range []string{"/web/models/gpt-4o/default?type=chat", "/web/models/gpt-4o/default"} {
		rc := callJSONQuery(h.ClearDefault, consts.MethodDelete, uri, map[string]string{"name": "gpt-4o"})
		if rc.Response.StatusCode() != 400 || !strings.Contains(string(rc.Response.Body()), "default_chat_required") {
			t.Errorf("%s: 取消对话默认应 400 default_chat_required, got %d %s",
				uri, rc.Response.StatusCode(), rc.Response.Body())
		}
	}
}

// TestParseDefaultType 仅校验 repo 标识与 handler 解析的一致性。
func TestParseDefaultType(t *testing.T) {
	for _, f := range repo.AllDefaultFlags {
		rc := app.NewContext(0)
		rc.Request.SetRequestURI("/x?type=" + f.String())
		got, ok := parseDefaultType(rc)
		if !ok || got != f {
			t.Errorf("parseDefaultType(%s) = %v, %v", f, got, ok)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestModelsHandler|TestParseDefaultType' -v`
Expected: 编译失败（`DefaultTypes`、`Defaults`、`ClearDefault`、`parseDefaultType` 未定义；`transcription.go` / `setting.go` 暂不受影响，`models.go` 中 `m.IsDefault` 报错）

- [ ] **Step 3: 修改响应类型**

`internal/api/types/types.go`：`ModelsResponse` 整体替换为：

```go
// ModelsResponse represents models list response
type ModelsResponse struct {
	Models   []ModelInfo   `json:"models"`
	Defaults DefaultModels `json:"defaults"`
	Total    int           `json:"total"`
}

// DefaultModels 各默认类型当前持有者的模型名，未设置时为空串
type DefaultModels struct {
	Chat   string `json:"chat"`
	Voice  string `json:"voice"`
	Vision string `json:"vision"`
}
```

`ModelInfo` 中 ``IsDefault bool `json:"is_default"` `` 替换为：

```go
	DefaultTypes        []string `json:"default_types"` // 持有的默认类型，按 chat、voice、vision 排列
```

- [ ] **Step 4: 修改 handler**

`internal/api/handler/models.go`：

(a) `toModelInfo` 中 `IsDefault: m.IsDefault,` 替换为 `DefaultTypes: defaultTypes(m),`，并在 `toModelInfo` 之后新增：

```go
// defaultTypes 返回模型持有的默认类型标识，未持有时返回空切片（序列化为 []）
func defaultTypes(m *repo.Model) []string {
	out := []string{}
	for _, f := range repo.AllDefaultFlags {
		if m.Has(f) {
			out = append(out, f.String())
		}
	}
	return out
}

// parseDefaultType 解析查询参数 type；省略时为 chat，非法取值返回 false
func parseDefaultType(rc *app.RequestContext) (repo.DefaultFlag, bool) {
	s := strings.TrimSpace(rc.Query("type"))
	if s == "" {
		return repo.DefaultChat, true
	}
	return repo.ParseDefaultFlag(s)
}

func writeInvalidDefaultType(rc *app.RequestContext) {
	rc.JSON(400, utils.H{"status": "invalid_request", "message": "type 取值无效，可选 chat、voice、vision"})
}
```

若 `models.go` 尚未导入 `strings`，在 import 中补上。

(b) `writeModelError` 的 switch 中，在 `ErrDefaultProtected` 分支之后加：

```go
	case errors.Is(err, llm.ErrChatDefaultRequired):
		status, code = 400, "default_chat_required"
```

(c) `List` 整体替换为：

```go
// List 处理 GET /web/models
func (h *ModelsHandler) List(ctx context.Context, rc *app.RequestContext) {
	list, err := h.models.List(ctx)
	if err != nil {
		h.writeModelError(rc, err)
		return
	}
	models := make([]types.ModelInfo, 0, len(list))
	var defaults types.DefaultModels
	for _, m := range list {
		if m.Has(repo.DefaultChat) {
			defaults.Chat = m.Name
		}
		if m.Has(repo.DefaultVoice) {
			defaults.Voice = m.Name
		}
		if m.Has(repo.DefaultVision) {
			defaults.Vision = m.Name
		}
		models = append(models, toModelInfo(m))
	}
	rc.JSON(200, types.ModelsResponse{Models: models, Defaults: defaults, Total: len(models)})
}
```

(d) `SetDefault` 整体替换为下面两个函数：

```go
// SetDefault 处理 PUT /web/models/:name/default?type=chat|voice|vision（type 省略为 chat）
func (h *ModelsHandler) SetDefault(ctx context.Context, rc *app.RequestContext) {
	flag, ok := parseDefaultType(rc)
	if !ok {
		writeInvalidDefaultType(rc)
		return
	}
	if err := h.models.SetDefault(ctx, rc.Param("name"), flag); err != nil {
		h.writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// ClearDefault 处理 DELETE /web/models/:name/default?type=voice|vision。
// type 为 chat（或省略）时由业务层返回 default_chat_required。
func (h *ModelsHandler) ClearDefault(ctx context.Context, rc *app.RequestContext) {
	flag, ok := parseDefaultType(rc)
	if !ok {
		writeInvalidDefaultType(rc)
		return
	}
	if err := h.models.ClearDefault(ctx, rc.Param("name"), flag); err != nil {
		h.writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}
```

- [ ] **Step 5: 注册路由**

`internal/api/router.go` 第 57 行之后加：

```go
	webGroup.DELETE("/models/:name/default", modelsH.ClearDefault)
```

- [ ] **Step 6: 运行确认通过**

Run: `gofmt -w internal/api/types/types.go internal/api/handler/models.go internal/api/handler/models_test.go internal/api/router.go && go test ./internal/api/handler/ -run 'TestModelsHandler|TestParseDefaultType' -v`
Expected: PASS

Run: `go build ./...`
Expected: 成功（`transcription.go` 仍调用 `GetByName`，签名未变，可编译）

- [ ] **Step 7: Commit（需用户明确确认后执行）**

```bash
git add internal/api/types/types.go internal/api/handler/models.go internal/api/handler/models_test.go internal/api/router.go
git commit -m "feat(api): 模型管理接口支持按类型设置与取消默认"
```

---

### Task 5: 转录接口缺省使用默认语音模型

**Files:**
- Modify: `internal/api/handler/transcription.go:83-143`
- Test: `internal/api/handler/transcription_test.go`

- [ ] **Step 1: 改测试辅助函数与用例**

`internal/api/handler/transcription_test.go`：

(a) `newTranscriptionHandlerForTest` 整体替换为：

```go
// newTranscriptionHandlerForTest 建一套真实的仓库与配置对象，
// cfg 作为静态配置传入 setting.New；upstreamURL 非空时创建名为 whisper-1 的模型
// （库中首个模型，自动成为默认对话模型），其 base_url 指向 httptest 假上游；
// defaultVoice 为 true 时把 whisper-1 设为默认语音模型。
func newTranscriptionHandlerForTest(t *testing.T, cfg config.Bootstrap, upstreamURL string, defaultVoice bool) *TranscriptionHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	ctx := context.Background()
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if upstreamURL != "" {
		err := models.Create(ctx, &repo.Model{
			Name: "whisper-1", Model: "whisper-1",
			BaseURL: upstreamURL, APIKey: "sk-test-1234abcd",
			Enabled: true, Stop: []string{},
		})
		if err != nil {
			t.Fatalf("创建测试模型: %v", err)
		}
		if defaultVoice {
			if err := models.SetDefault(ctx, "whisper-1", repo.DefaultVoice); err != nil {
				t.Fatalf("设置默认语音模型: %v", err)
			}
		}
	}

	settings := setting.New(cfg, settingdb.New(sqlxDB, dialect))
	return NewTranscriptionHandler(settings, models, logger.NewNop())
}
```

(b) 其余调用处把最后一个字符串参数改为布尔值：

| 测试 | 原实参 | 新实参 |
|---|---|---|
| `TestTranscriptionHandler_Success` | `"whisper-1"` | `true` |
| `TestTranscriptionHandler_MissingFile` | `"whisper-1"` | `true` |
| `TestTranscriptionHandler_NoVoiceModelConfigured` | `""` | `false` |
| `TestTranscriptionHandler_ModelNotFound` | `"whisper-1"` | `true` |
| `TestTranscriptionHandler_FormWinsOverHeader` | `""` | `false` |
| `TestTranscriptionHandler_HeaderModelUsed` | `""` | `false` |
| `TestTranscriptionHandler_UnsupportedExtension` | `"whisper-1"` | `true` |
| `TestTranscriptionHandler_NoExtension` | `"whisper-1"` | `true` |
| `TestTranscriptionHandler_UpstreamError` | `"whisper-1"` | `true` |
| `TestTranscriptionHandler_EmptyTranscript` | `"whisper-1"` | `true` |
| `TestTranscriptionHandler_FileTooLarge` | `"whisper-1"` | `true` |

`TestTranscriptionHandler_NoVoiceModelConfigured` 的注释改为「建了模型但未设默认语音模型，且请求不带 model 字段」，并在断言末尾追加：

```go
	if !bytes.Contains(rc.Response.Body(), []byte("未配置默认语音模型")) {
		t.Errorf("错误信息应提示未配置默认语音模型: %s", rc.Response.Body())
	}
```

(c) 删除 `TestTranscriptionHandler_FormModelWinsOverSetting` 与 `TestTranscriptionHandler_HeaderModelWinsOverSetting`（含注释），在原位置写入：

```go
// TestTranscriptionHandler_DefaultVoiceUsed 请求不指定模型时使用默认语音模型。
func TestTranscriptionHandler_DefaultVoiceUsed(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if m := bodyModel(t, rc); m != "whisper-1" {
		t.Errorf("model = %q, want whisper-1（默认语音模型）", m)
	}
}

// TestTranscriptionHandler_FormModelWithoutDefault 未设默认语音模型，表单显式指定时照常转录。
func TestTranscriptionHandler_FormModelWithoutDefault(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, false)

	rc := audioCtx(t, fakeAudio, "rec.webm", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if m := bodyModel(t, rc); m != "whisper-1" {
		t.Errorf("model = %q, want whisper-1", m)
	}
}

// TestTranscriptionHandler_HeaderModelWithoutDefault 未设默认语音模型，请求头显式指定时照常转录。
func TestTranscriptionHandler_HeaderModelWithoutDefault(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, false)

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	rc.Request.Header.Set("X-Model-Name", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if m := bodyModel(t, rc); m != "whisper-1" {
		t.Errorf("model = %q, want whisper-1", m)
	}
}

// TestTranscriptionHandler_ExplicitUnknownNoFallback 显式指定的模型不存在时报错，不回落到默认语音模型。
func TestTranscriptionHandler_ExplicitUnknownNoFallback(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	rc.Request.Header.Set("X-Model-Name", "no-such-model")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400（不应回落到默认语音模型）", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
}

// TestTranscriptionHandler_IgnoresVoiceSetting 服务端不读取配置表 voice.model：
// 配置表选了 whisper-1 但无默认语音模型、请求也不指定时，仍报未配置。
func TestTranscriptionHandler_IgnoresVoiceSetting(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, false)
	err := h.settings.SetVoice(context.Background(), setting.VoiceSettings{Enabled: true, Model: "whisper-1"})
	if err != nil {
		t.Fatalf("SetVoice: %v", err)
	}

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400（不应读取配置表 voice.model）", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestTranscriptionHandler -v`
Expected: `DefaultVoiceUsed`、`NoVoiceModelConfigured`（错误信息断言）、`IgnoresVoiceSetting` FAIL（当前实现仍读取配置表）

- [ ] **Step 3: 实现**

`internal/api/handler/transcription.go`：

(a) import 中加入 `"github.com/zfd81/groot/internal/repo"`。

(b) 把从 `modelName, err := h.resolveModelName(ctx, rc)` 到 `GetByName` 错误处理块结束的代码整体替换为：

```go
	// Resolve 按「显式模型名 → 默认语音模型」解析并校验 enabled：
	// 显式指定的模型不存在或禁用时直接报错，不回落到默认语音模型；
	// 三类错误对调用方都是「模型不可用」，统一映射为 invalid_model，错误原文已含模型名或类型。
	m, err := h.models.Resolve(ctx, explicitModelName(rc), repo.DefaultVoice)
	if err != nil {
		if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) ||
			errors.Is(err, llm.ErrNoDefaultModel) {
			rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
			return
		}
		h.log.Error("查询语音模型失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
```

(c) 转录失败日志 `zap.String("model", modelName)` 改为 `zap.String("model", m.Name)`；成功响应改为：

```go
	rc.JSON(200, utils.H{"text": text, "model": m.Name})
```

(d) 删除 `resolveModelName` 函数（含注释），在原位置写入：

```go
// explicitModelName 按「表单 model → 请求头 X-Model-Name」取调用方显式指定的模型名，
// 均未给出返回空串。请求头形式与 /chat 的既有约定一致。
func explicitModelName(rc *app.RequestContext) string {
	if v := strings.TrimSpace(rc.PostForm("model")); v != "" {
		return v
	}
	return strings.TrimSpace(string(rc.GetHeader("X-Model-Name")))
}
```

`h.settings` 仍用于读取附件大小上限，结构体字段保持不变。

- [ ] **Step 4: 运行确认通过**

Run: `gofmt -w internal/api/handler/transcription.go internal/api/handler/transcription_test.go && go vet ./internal/api/handler/ && go test ./internal/api/handler/ -run TestTranscriptionHandler -v`
Expected: PASS；`go vet` 无「imported and not used」报错

- [ ] **Step 5: Commit（需用户明确确认后执行）**

```bash
git add internal/api/handler/transcription.go internal/api/handler/transcription_test.go
git commit -m "feat(api): 转录接口缺省使用默认语音模型"
```

---

### Task 6: 语音设置校验默认语音模型

**Files:**
- Modify: `internal/api/handler/setting.go:69-103`
- Test: `internal/api/handler/setting_test.go:101-109`

- [ ] **Step 1: 写失败测试**

`internal/api/handler/setting_test.go`：删除 `TestSettingHandler_PutVoiceEmptyModelAllowed`，在原位置写入：

```go
// TestSettingHandler_PutVoiceEmptyModelRequiresDefaultVoice 开关打开、model 为空（跟随默认）
// 且系统无默认语音模型时拒绝保存。
func TestSettingHandler_PutVoiceEmptyModelRequiresDefaultVoice(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	body := string(rc.Response.Body())
	if !strings.Contains(body, "invalid_model") || !strings.Contains(body, "未配置默认语音模型") {
		t.Errorf("应返回 invalid_model 并提示未配置默认语音模型: %s", body)
	}
}

func TestSettingHandler_PutVoiceEmptyModelWithDefaultVoice(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)
	if err := h.models.SetDefault(context.Background(), "whisper-1", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// TestSettingHandler_PutVoiceEmptyModelDisabledSwitch 开关关闭时不校验。
func TestSettingHandler_PutVoiceEmptyModelDisabledSwitch(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":false,"model":"","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestSettingHandler_PutVoice -v`
Expected: `PutVoiceEmptyModelRequiresDefaultVoice` FAIL（当前返回 200）

- [ ] **Step 3: 实现**

`internal/api/handler/setting.go`：

(a) import 中加入 `"github.com/zfd81/groot/internal/repo"`。

(b) `PutVoice` 中从「仅在开关打开且 model 非空时校验」注释到该 `if` 块结束，整体替换为：

```go
	// 仅在开关打开时校验，否则话筒一按就报错：model 非空时该模型必须存在且启用；
	// model 为空表示跟随默认语音模型，此时系统必须已有可用的默认语音模型。
	// 关闭开关时不校验，这样所选模型被删除或禁用后仍能关闭语音输入。
	name := strings.TrimSpace(req.Model)
	if req.Enabled && name != "" {
		if _, err := h.models.GetByName(ctx, name); err != nil {
			if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) {
				rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
				return
			}
			h.log.Error("校验语音模型失败", zap.String("model", name), zap.Error(err))
			rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
			return
		}
	} else if req.Enabled {
		if _, err := h.models.Resolve(ctx, "", repo.DefaultVoice); err != nil {
			if errors.Is(err, llm.ErrNoDefaultModel) {
				rc.JSON(400, utils.H{"status": "invalid_model", "message": "未选择识别模型，且未配置默认语音模型"})
				return
			}
			if errors.Is(err, llm.ErrModelDisabled) {
				rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
				return
			}
			h.log.Error("校验默认语音模型失败", zap.Error(err))
			rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
			return
		}
	}
```

- [ ] **Step 4: 运行确认通过**

Run: `gofmt -w internal/api/handler/setting.go internal/api/handler/setting_test.go && go test ./internal/api/handler/... -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit（需用户明确确认后执行）**

```bash
git add internal/api/handler/setting.go internal/api/handler/setting_test.go
git commit -m "feat(api): 语音设置跟随默认语音模型时校验其存在"
```

---

### Task 7: 前端界面

**Files:**
- Modify: `web/src/api/types.ts:98,125-129`
- Modify: `web/src/stores/meta.ts`
- Modify: `web/src/api/voice.ts:22-31`
- Modify: `web/src/components/chat/ChatInput.vue:21,44-46,101-102`
- Modify: `web/src/components/settings/ModelsPanel.vue`
- Modify: `web/src/components/settings/SettingsModal.vue:43,63-66,216-224,455-470`
- Modify: `web/src/i18n/messages/zh-cn.ts`、`web/src/i18n/messages/en.ts`

前端无单元测试框架（项目禁止引入新测试工具），以 `npm run build`（含 `vue-tsc -b` 类型检查）验证。

- [ ] **Step 1: 类型定义**

`web/src/api/types.ts`：`ModelInfo` 中 `is_default: boolean` 改为：

```ts
  default_types: DefaultType[] // 持有的默认类型，按 chat、voice、vision 排列
```

`ModelsResp` 整体替换为（类型定义放在 `ModelInfo` 之前）：

```ts
// 模型默认类型：对话 / 语音 / 视觉
export type DefaultType = 'chat' | 'voice' | 'vision'

// 各默认类型当前持有者的模型名，未设置时为空串
export interface DefaultModels {
  chat: string
  voice: string
  vision: string
}
```

```ts
export interface ModelsResp {
  models: ModelInfo[]
  defaults: DefaultModels
  total: number
}
```

- [ ] **Step 2: meta store**

`web/src/stores/meta.ts` 整体替换为：

```ts
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api } from '../api/client'
import type { ModelsResp, ModelInfo, DefaultModels } from '../api/types'

// 元数据：模型列表、各类型默认模型、子 Agent 列表，供聊天输入区的切换控件与设置面板使用。
export const useMetaStore = defineStore('meta', () => {
  const models = ref<ModelInfo[]>([])
  const defaults = ref<DefaultModels>({ chat: '', voice: '', vision: '' })
  // 默认对话模型，聊天输入区的模型下拉框以它标注「默认」
  const defaultModel = computed(() => defaults.value.chat)
  const agents = ref<string[]>([])
  const loaded = ref(false)

  async function load() {
    if (loaded.value) return
    try {
      const resp = await api.get<ModelsResp>('/web/models')
      models.value = resp.models || []
      defaults.value = { chat: '', voice: '', vision: '', ...(resp.defaults || {}) }
    } catch {
      // 模型列表拉取失败不阻断聊天
    }
    try {
      const resp = await api.get<{ agents?: Array<{ name: string }> }>('/web/agents')
      agents.value = (resp.agents || []).map((a) => a.name).filter(Boolean)
    } catch {
      // 子 Agent 列表可选
    }
    loaded.value = true
  }

  // 模型管理界面增删改后调用，强制重新拉取模型列表
  async function reload() {
    loaded.value = false
    await load()
  }

  return { models, defaults, defaultModel, agents, loaded, load, reload }
})
```

- [ ] **Step 3: 转录请求经请求头传模型**

`web/src/api/voice.ts` 中 `transcribe` 的注释与前半段替换为：

```ts
  // transcribe 上传音频并返回识别文本。model 非空时经请求头 X-Model-Name 传递；
  // 省略时由后端使用默认语音模型。
  async transcribe(blob: Blob, filename: string, model?: string): Promise<TranscriptionResult> {
    const fd = new FormData()
    fd.append('file', blob, filename)

    const resp = await fetch('/web/audio/transcriptions', {
      method: 'POST',
      body: fd,
      headers: model ? { 'X-Model-Name': model } : undefined,
      credentials: 'same-origin',
    })
```

（不设置 `Content-Type`，multipart 边界由浏览器自动生成。）

- [ ] **Step 4: 聊天输入区**

`web/src/components/chat/ChatInput.vue`：

第 21 行：

```ts
const { models, defaults, defaultModel, agents } = storeToRefs(meta)
```

第 44-46 行替换为：

```ts
// 开关已开、未选具体模型且系统也没有默认语音模型：话筒显示警告态，点击只提示、不录音。
// 这种状态只有管理员改了设置或取消了默认语音模型才会出现，提示里直接指向设置页。
const micWarn = computed(() => voice.value.enabled && !voice.value.model && !defaults.value.voice)
```

第 101-102 行替换为：

```ts
    // 设置中选了具体模型时经 X-Model-Name 传递；未选时不传，由服务端使用默认语音模型
    const res = await voiceApi.transcribe(rec.blob, rec.filename, voice.value.model || undefined)
```

- [ ] **Step 5: 模型管理面板**

`web/src/components/settings/ModelsPanel.vue`：

(a) 第 9 行 import 加入 `DefaultType`：

```ts
import type { ModelInfo, ModelsResp, ModelForm, ModelTestResp, DefaultType } from '../../api/types'
```

(b) `handleSetDefault` 整体替换为：

```ts
// 某一默认类型是否由该模型持有
function isDef(m: ModelInfo, type: DefaultType) {
  return m.default_types?.includes(type) ?? false
}

// 持有任一默认类型的模型不可删除、不可禁用
function hasAnyDefault(m: ModelInfo) {
  return (m.default_types?.length ?? 0) > 0
}

async function handleSetDefault(m: ModelInfo, type: DefaultType) {
  try {
    await api.put(`/web/models/${encodeURIComponent(m.name)}/default?type=${type}`)
    ElNotification.success({ title: t('settings.menuModels'), message: t('settings.defaultChanged') })
    await refreshAll()
  } catch (e) {
    notifyError(e)
  }
}

// 只有语音、视觉默认可取消；对话默认只能通过把其他模型设为默认来转移
async function handleClearDefault(m: ModelInfo, type: DefaultType) {
  try {
    await api.delete(`/web/models/${encodeURIComponent(m.name)}/default?type=${type}`)
    ElNotification.success({ title: t('settings.menuModels'), message: t('settings.defaultCleared') })
    await refreshAll()
  } catch (e) {
    notifyError(e)
  }
}
```

(c) `handleMenuCommand` 中 `case 'setDefault':` 分支替换为：

```ts
    case 'setDefaultChat':
      void handleSetDefault(m, 'chat')
      break
    case 'setDefaultVoice':
      void handleSetDefault(m, 'voice')
      break
    case 'setDefaultVision':
      void handleSetDefault(m, 'vision')
      break
    case 'clearDefaultVoice':
      void handleClearDefault(m, 'voice')
      break
    case 'clearDefaultVision':
      void handleClearDefault(m, 'vision')
      break
```

(d) 模板中第 251-253 行的单个默认标签替换为：

```html
          <el-tag v-if="isDef(c.m, 'chat')" size="small" type="primary" effect="light" style="margin-left: 4px">
            {{ t('settings.defaultChat') }}
          </el-tag>
          <el-tag v-if="isDef(c.m, 'voice')" size="small" type="success" effect="light" style="margin-left: 4px">
            {{ t('settings.defaultVoice') }}
          </el-tag>
          <el-tag v-if="isDef(c.m, 'vision')" size="small" type="warning" effect="light" style="margin-left: 4px">
            {{ t('settings.defaultVision') }}
          </el-tag>
```

(e) 下拉菜单中从 `command="setDefault"` 项到 `command="delete"` 项替换为：

```html
                  <el-dropdown-item command="setDefaultChat" :disabled="isDef(c.m, 'chat') || !c.m.enabled">
                    {{ t('settings.setDefaultChat') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="setDefaultVoice" :disabled="isDef(c.m, 'voice') || !c.m.enabled">
                    {{ t('settings.setDefaultVoice') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="setDefaultVision" :disabled="isDef(c.m, 'vision') || !c.m.enabled">
                    {{ t('settings.setDefaultVision') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="clearDefaultVoice" :disabled="!isDef(c.m, 'voice')">
                    {{ t('settings.clearDefaultVoice') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="clearDefaultVision" :disabled="!isDef(c.m, 'vision')">
                    {{ t('settings.clearDefaultVision') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="edit" divided>{{ t('common.edit') }}</el-dropdown-item>
                  <el-dropdown-item command="toggle" :disabled="hasAnyDefault(c.m) && c.m.enabled">
                    {{ c.m.enabled ? t('settings.disable') : t('settings.enable') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="delete" divided :disabled="hasAnyDefault(c.m)" class="menu-danger">
                    {{ t('common.delete') }}
                  </el-dropdown-item>
```

- [ ] **Step 6: 语音设置分区**

`web/src/components/settings/SettingsModal.vue`：

第 43 行：

```ts
const { models, defaults } = storeToRefs(meta)
```

第 63-66 行 `voiceModelOptions` 替换为：

```ts
// 首项为空串，表示跟随默认语音模型；其余为全部启用的模型（语音接口要求模型已启用）
const voiceModelOptions = computed(() => [
  {
    label: defaults.value.voice
      ? t('settings.voiceModelFollowDefault', { name: defaults.value.voice })
      : t('settings.voiceModelFollowDefaultUnset'),
    value: '',
  },
  ...(models.value || []).filter((m) => m.enabled).map((m) => ({ label: m.name, value: m.name })),
])
```

`saveVoice` 的注释与首个判断替换为：

```ts
// saveVoice 保存整个分区。开关打开、跟随默认且系统无默认语音模型时拒绝保存，并从 store 回滚，
// 避免本地状态与服务端脱节（否则会存下一个话筒一按就报错的状态）。
// 保存成功不弹提示，与通用面板的语言、外观行为一致。
async function saveVoice() {
  if (voice.value.enabled && !voice.value.model && !defaults.value.voice) {
    ElMessage.warning(t('settings.voiceModelRequired'))
    voice.value = { ...voiceStore.settings }
    return
  }
```

模板第 455-457 行注释最后一句改为：

```html
               model 仅供 Web 界面使用，空串表示跟随默认语音模型。 -->
```

`el-select` 开标签替换为（去掉 `clearable` 与 `placeholder`；`empty-values` 让空串成为可选中的值而非「未选中」）：

```html
              <el-select
                v-model="voice.model"
                style="width: 220px"
                :empty-values="[null, undefined]"
                :value-on-clear="''"
                @change="saveVoice"
              >
```

- [ ] **Step 7: i18n 文案**

`web/src/i18n/messages/zh-cn.ts`：

- `recordNoModel` 改为 `'未配置识别模型，请在设置中选择识别模型或设置默认语音模型'`
- 删除 `voiceModelPlaceholder` 行
- `voiceModelRequired` 改为 `'请先选择识别模型，或在模型管理中设置默认语音模型'`
- `setDefault: '设为默认',` 行替换为：

```ts
    setDefaultChat: '设为默认对话',
    setDefaultVoice: '设为默认语音',
    setDefaultVision: '设为默认视觉',
    clearDefaultVoice: '取消默认语音',
    clearDefaultVision: '取消默认视觉',
    defaultChat: '默认对话',
    defaultVoice: '默认语音',
    defaultVision: '默认视觉',
    defaultCleared: '默认标记已取消',
    voiceModelFollowDefault: '跟随默认语音模型（{name}）',
    voiceModelFollowDefaultUnset: '跟随默认语音模型（未配置）',
```

`web/src/i18n/messages/en.ts`：

- `recordNoModel` 改为 `'No transcription model. Pick one in Settings or set a default voice model'`
- 删除 `voiceModelPlaceholder` 行
- `voiceModelRequired` 改为 `'Select a transcription model or set a default voice model first'`
- `setDefault: 'Set as default',` 行替换为：

```ts
    setDefaultChat: 'Set as default chat',
    setDefaultVoice: 'Set as default voice',
    setDefaultVision: 'Set as default vision',
    clearDefaultVoice: 'Unset default voice',
    clearDefaultVision: 'Unset default vision',
    defaultChat: 'Default chat',
    defaultVoice: 'Default voice',
    defaultVision: 'Default vision',
    defaultCleared: 'Default unset',
    voiceModelFollowDefault: 'Follow default voice model ({name})',
    voiceModelFollowDefaultUnset: 'Follow default voice model (not set)',
```

`default`（聊天下拉框「默认」标注）与 `defaultChanged` 保留。

- [ ] **Step 8: 残留检查与构建**

Run: `grep -rn "is_default\|resp\.default\b\|voiceModelPlaceholder\|'setDefault'\|settings\.setDefault'" web/src`
Expected: 无输出

Run: `cd web && npm run build`
Expected: `vue-tsc` 无类型错误，`vite build` 成功

- [ ] **Step 9: 手工验证（浏览器）**

启动 `dist/groot` 后在 Web 界面验证：

1. 设置 → 模型：首个模型显示「默认对话」标签；「···」菜单含三项设为默认、两项取消默认。
2. 把某模型设为默认语音 → 显示「默认语音」标签，「删除」与「禁用」置灰；取消默认语音后恢复可用。
3. 设置 → 通用 → 语音输入：下拉首项显示「跟随默认语音模型（模型名）」；无默认语音模型时显示「（未配置）」，此时打开开关提示并回滚。
4. 聊天页：跟随默认时录音转录成功；设置中选择具体模型后，浏览器开发者工具可见请求头 `X-Model-Name`。

- [ ] **Step 10: Commit（需用户明确确认后执行）**

```bash
git add web/src/api/types.ts web/src/stores/meta.ts web/src/api/voice.ts \
  web/src/components/chat/ChatInput.vue web/src/components/settings/ModelsPanel.vue \
  web/src/components/settings/SettingsModal.vue web/src/i18n/messages/zh-cn.ts web/src/i18n/messages/en.ts
git commit -m "feat(web): 模型管理支持默认对话/语音/视觉，语音设置可跟随默认语音模型"
```

---

### Task 8: 手册、测试用例文档与 Python 系统测试

**Files:**
- Modify: `README.md:351,353,462,482-486,577,1286,1541,1565,1568`
- Modify: `tests/TEST_CASES.md:340-341,387,539-544,761-780`
- Modify: `tests/python/conftest.py:107-108,127-132`
- Modify: `tests/python/test_multi_agent_real_llm.py:157`
- Modify: `tests/python/test_models_api.py`

- [ ] **Step 1: README**

| 行 | 修改为 |
|---|---|
| 351 | 句末「首个创建的模型自动成为默认模型。」改为「首个创建的模型自动成为默认对话模型；默认语音模型、默认视觉模型在模型卡片的「···」菜单中设置。」 |
| 353 | `- 可创建、编辑、删除模型，设置默认对话 / 语音 / 视觉模型，启用/禁用模型并测试连接` |
| 462 | 「切换默认模型」改为「设置默认对话 / 语音 / 视觉模型」 |
| 1286 | 「为空则使用 Web UI 中设置的默认模型」改为「为空则使用默认对话模型」 |
| 1541 | `` | `model` | 否 | 转录模型名，省略时使用默认语音模型 | `` |
| 1565 | `` - `400 invalid_model`：模型不存在、已禁用或未配置默认语音模型 `` |
| 1568 | `` 模型也可用请求头 `X-Model-Name` 指定，与 `/chat` 的约定一致。模型名取用顺序为表单 `model` → 请求头 `X-Model-Name` → 默认语音模型（在 设置 → 模型 中设置）。显式指定的模型不存在或已禁用时直接返回 400，不回落到默认语音模型。 `` |

第 577 行替换为：

```markdown
> - 语音输入（识别模型、话筒开关等）在 **设置 → 通用** 的「语音输入」分组中维护，同样存放于配置表；其中识别模型只作用于 Web 界面的语音输入，选择「跟随默认语音模型」时使用 设置 → 模型 中的默认语音模型
```

第 482-486 行「默认模型规则」整段替换为：

```markdown
默认模型规则：

- 模型有三种默认类型：默认对话模型、默认语音模型、默认视觉模型；每种类型至多一个模型持有，一个模型可同时持有多种
- 调用方不指定模型时按用途取对应默认：对话取默认对话模型，音频转录取默认语音模型
- 首个创建的模型自动成为默认对话模型；默认语音、默认视觉模型需手动设置
- 持有任一默认类型的模型不允许删除或禁用，需先取消其默认标记
- 默认语音、默认视觉可直接取消；默认对话不能取消，只能通过把其他模型设为默认对话模型来转移
- 禁用的模型不可设为默认，也不会出现在聊天下拉框中
```

- [ ] **Step 2: TEST_CASES.md**

第 340 行（转录 handler）替换为：

```markdown
- 转录 handler：成功响应含 text 与 model、缺 file、未配置默认语音模型、模型不存在、模型解析顺序（表单 model → 请求头 X-Model-Name → 默认语音模型）、显式指定失效模型不回落、不读取配置表 voice.model、扩展名白名单与无扩展名、上游 502 透传、空文本 400、按 MB 换算的大小上限
```

第 341 行（设置 handler）替换为：

```markdown
- 设置 handler：默认值读取、整组保存回读、开关打开且 model 为空时要求存在默认语音模型（无则 400 invalid_model）、模型不存在或已禁用拒绝且消息含模型名、非法 JSON、model 裁剪空白、关闭开关时不校验
```

在 `## 二、系统测试（Python）` 之前（1.8 节的 `---` 之后）插入：

```markdown
### 1.9 模型默认类型测试

位于 `internal/repo/model_test.go`、`internal/db/migrate_test.go`、`internal/repo/modeldb/model_test.go`、`internal/llm/service_test.go`、`internal/api/handler/models_test.go`。

覆盖点：

- 类型定义：chat=1、voice=2、vision=4；字符串标识互转，非法标识解析失败；`Has` 按位判断
- 建表语句：三方言 models 表含 `default_flags INTEGER NOT NULL DEFAULT 0`，不含 `is_default`
- 仓库层：无默认时 `GetDefault` 按类型返回 ErrNotFound；`SetDefault` 置位并清除原持有者（每类型唯一）；一模型多默认（chat+vision=5）；三种类型互不影响；`ClearDefault` 只清目标位、未持有时无操作；目标不存在返回 ErrNotFound 且事务回滚；`Create` 原样写入、`Update` 不改 default_flags
- 业务层：首个模型只获得对话默认；非首个模型忽略传入的默认类型；`Resolve` 名称优先、按类型回落、无默认错误按类型区分信息且可识别为 ErrNoDefaultModel；持有语音默认的模型不可删除、不可禁用；禁用模型不可设为默认；取消对话默认返回 ErrChatDefaultRequired；取消未持有的默认直接成功；不存在模型返回 ErrModelNotFound
- 模型接口：`type` 省略按 chat；非法 `type` 返回 400 invalid_request；列表 `default_types` 按 chat、voice、vision 排序、非默认模型为 `[]`；`defaults` 三字段未设置为空串；取消对话默认返回 400 default_chat_required；取消语音默认后可删除

---

```

2.14 节 `TestTranscriptionErrors` 行测试内容改为：

```markdown
| TestTranscriptionErrors | test_transcription.py | 缺文件、扩展名、显式指定不存在的模型（400 invalid_model，不回落默认语音模型）、超大文件、未鉴权 |
```

2.23 节表格修改：

- 「首个模型自动默认」行测试内容改为：`空库创建首个模型后自动成为默认对话模型（default_types 含 chat，defaults.chat 为其名称）`
- 「默认模型删除保护」行测试内容改为：`删除持有任一默认类型的模型返回 409 default_model_protected`
- 「默认模型禁用保护」行测试内容改为：`更新持有任一默认类型的模型为 enabled: false 返回 409`
- 「设默认」行测试内容改为：`` `PUT /web/models/{name}/default?type=chat|voice|vision` 设置对应类型默认，每类型有且只有一个持有者；type 省略按 chat `` 
- 在「设默认」行之后追加：

```markdown
| 取消默认 | test_models_api.py | `DELETE /web/models/{name}/default?type=voice|vision` 返回 200 且 `defaults` 对应字段清空，取消后可删除；`type=chat` 返回 400 `default_chat_required` |
| 非法默认类型 | test_models_api.py | `type` 取值非 chat / voice / vision 时返回 400 `invalid_request` |
```

- [ ] **Step 3: Python conftest 与多 Agent 测试适配**

`tests/python/conftest.py`：

第 107-108 行替换为：

```python
    default_chat = (data.get("defaults") or {}).get("chat")
    if default_chat:
        return default_chat
```

第 126-132 行（「若仍无默认」起）替换为：

```python
    # 若仍无默认对话模型（如已有模型但默认为空），显式设为默认对话模型
    data = session.get(f"{base_url}/web/models", timeout=10).json()
    default_chat = (data.get("defaults") or {}).get("chat")
    if not default_chat:
        r = session.put(f"{base_url}/web/models/{model_name}/default?type=chat", timeout=10)
        if r.status_code != 200:
            raise RuntimeError(f"设置默认模型失败 ({r.status_code}): {r.text}")
        return model_name
    return default_chat
```

`tests/python/test_multi_agent_real_llm.py` 第 157 行：

```python
    if not (data.get("defaults") or {}).get("chat"):
```

同一块中的 `session.put(f"{base_url}/web/models/real-llm/default", ...)` 改为 `.../real-llm/default?type=chat`。

- [ ] **Step 4: test_models_api.py**

(a) 模块 docstring 用例点列表替换为：

```python
用例点：
- 模型 CRUD（创建/列表/更新/删除）
- 首个模型自动成为默认对话模型
- 默认模型删除/禁用保护（持有任一默认类型，409）
- 重名冲突（409）
- api_key 脱敏与留空不改
- 按类型设默认（chat / voice / vision）/ 禁用模型不可设默认
- 取消默认（voice / vision 可取消，chat 不可取消）/ 非法 type（400）
- 连接测试（不可达地址返回 unhealthy）
- chat 引用不存在/禁用模型报错（400）
```

(b) `test_first_model_becomes_default` 整体替换为：

```python
    def test_first_model_becomes_default(self, web, model_name):
        """首个模型自动成为默认对话模型：库中已有模型时验证 defaults.chat 非空且指向存在的模型"""
        web.post(f"{BASE_URL}/web/models", json=model_body(model_name))
        data = web.get(f"{BASE_URL}/web/models").json()
        default_chat = data["defaults"]["chat"]
        assert default_chat, "存在模型时必须有默认对话模型"
        names = [m["name"] for m in data["models"]]
        assert default_chat in names
        default_model = next(m for m in data["models"] if m["name"] == default_chat)
        assert "chat" in default_model["default_types"]
        for m in data["models"]:
            assert isinstance(m["default_types"], list)
```

(c) `test_set_default_and_protection` 中：

- `orig_default = web.get(f"{BASE_URL}/web/models").json().get("default", "")` 改为 `orig_default = web.get(f"{BASE_URL}/web/models").json().get("defaults", {}).get("chat", "")`
- `assert web.get(f"{BASE_URL}/web/models").json()["default"] == a` 改为 `assert web.get(f"{BASE_URL}/web/models").json()["defaults"]["chat"] == a`

(d) `TestChatModelErrors.test_chat_with_disabled_model` 中 `orig_default` 同样改为 `.get("defaults", {}).get("chat", "")`。

(e) 在 `TestDefaultModel` 类末尾追加：

```python
    def test_voice_default_set_clear_and_protection(self, web, model_name):
        """默认语音模型：设置后受删除保护，取消后可删除；取消不影响默认对话模型"""
        before = web.get(f"{BASE_URL}/web/models").json()["defaults"]
        orig_voice = before.get("voice", "")
        assert web.post(f"{BASE_URL}/web/models", json=model_body(model_name)).status_code == 200
        try:
            resp = web.put(f"{BASE_URL}/web/models/{model_name}/default?type=voice")
            assert resp.status_code == 200, resp.text
            data = web.get(f"{BASE_URL}/web/models").json()
            assert data["defaults"]["voice"] == model_name
            assert data["defaults"]["chat"] == before["chat"], "设置语音默认不应改变对话默认"
            m = next(x for x in data["models"] if x["name"] == model_name)
            assert "voice" in m["default_types"]

            # 持有语音默认同样不可删除 / 禁用
            assert web.delete(f"{BASE_URL}/web/models/{model_name}").status_code == 409
            assert web.put(f"{BASE_URL}/web/models/{model_name}",
                           json=model_body(model_name, api_key="", enabled=False)).status_code == 409

            resp = web.delete(f"{BASE_URL}/web/models/{model_name}/default?type=voice")
            assert resp.status_code == 200, resp.text
            data = web.get(f"{BASE_URL}/web/models").json()
            assert data["defaults"]["voice"] == ""
            m = next(x for x in data["models"] if x["name"] == model_name)
            assert "voice" not in m["default_types"]

            assert web.delete(f"{BASE_URL}/web/models/{model_name}").status_code == 200
        finally:
            if orig_voice:
                web.put(f"{BASE_URL}/web/models/{orig_voice}/default?type=voice")
            else:
                web.delete(f"{BASE_URL}/web/models/{model_name}/default?type=voice")

    def test_invalid_default_type(self, web, model_name):
        web.post(f"{BASE_URL}/web/models", json=model_body(model_name))
        for method in (web.put, web.delete):
            resp = method(f"{BASE_URL}/web/models/{model_name}/default?type=audio")
            assert resp.status_code == 400, resp.text
            assert resp.json()["status"] == "invalid_request"

    def test_clear_chat_default_rejected(self, web):
        default_chat = web.get(f"{BASE_URL}/web/models").json()["defaults"]["chat"]
        if not default_chat:
            pytest.skip("库中无默认对话模型")
        resp = web.delete(f"{BASE_URL}/web/models/{default_chat}/default?type=chat")
        assert resp.status_code == 400, resp.text
        assert resp.json()["status"] == "default_chat_required"
        assert web.get(f"{BASE_URL}/web/models").json()["defaults"]["chat"] == default_chat
```

(f) 语法检查（不运行系统测试，系统测试由用户执行）：

Run: `cd tests/python && python -m py_compile conftest.py test_models_api.py test_multi_agent_real_llm.py`
Expected: 无输出

- [ ] **Step 5: Commit（需用户明确确认后执行）**

```bash
git add README.md tests/TEST_CASES.md tests/python/conftest.py tests/python/test_models_api.py tests/python/test_multi_agent_real_llm.py
git commit -m "docs: 手册与测试适配模型默认类型"
```

---

### Task 9: 全量验证

- [ ] **Step 1: 残留引用检查**

Run: `grep -rn "IsDefault\|is_default\|resolveModelName" internal cmd pkg web/src tests/python README.md`
Expected: 无输出（`logger.SetDefault` 不在匹配范围内）

- [ ] **Step 2: 格式与静态检查**

Run: `gofmt -l internal cmd pkg`
Expected: 无输出

Run: `go vet ./...`
Expected: 无输出

- [ ] **Step 3: 全部 Go 单元测试**

Run: `go test ./internal/... 2>&1 | tail -40`
Expected: 全部 `ok`，无 `FAIL`

- [ ] **Step 4: 编译**

Run: `go build -o dist/groot ./cmd/groot && cd web && npm run build`
Expected: 均成功

- [ ] **Step 5: 本地旧库提示**

开发阶段不做迁移，本地已有的 `{GROOT_HOME}/groot.db`（默认 `~/.groot/groot.db`）仍是旧 `is_default` 列，启动后查询会报 `no such column: default_flags`。需删除旧库后重启，重新创建用户与模型。**删除前需用户确认。**

- [ ] **Step 6: 交付**

向用户报告：改动文件清单、Go 单元测试结果、构建结果；提示用户自行运行 `cd tests/python && pytest -v test_models_api.py test_transcription.py`。等待用户明确要求后再提交。

