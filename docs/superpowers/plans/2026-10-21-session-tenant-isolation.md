# 会话租户与用户隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让每个会话记录归属 (租户, 用户)，并在所有会话类对外接口上按归属隔离；租户来自 API Key 元数据，用户来自 `X-User-ID` 请求头。

**Architecture:** 底层自上而下：`api_keys` 与 `memory_sessions` 两张表加列（只改建表语句）；repo 与 memory 层接口增加归属参数与 `OwnsSession`；鉴权中间件把租户与用户注入请求上下文；各 handler 在业务逻辑前做统一的三步前置检查，归属不符时表现为"会话不存在"。Web 管理员（Cookie 凭证，`caller == "web"`）全程绕过隔离。

**Tech Stack:** Go 1.x、Hertz、sqlx（SQLite/MySQL/Postgres 三方言）、Vue 3 + Element Plus（管理界面）。

**Spec:** `docs/superpowers/specs/2026-10-21-session-tenant-isolation-design.md`

## Global Constraints

- 表结构只改 `CREATE TABLE`，不写 `addColumnIfMissing`、不重建、不追加；开发库删掉重启。
- `memory_sessions.tenant_id` 必须位于 `session_id` 之后、`user_id` 之前；`api_keys.tenant` 必须位于 `name` 之后、`permissions` 之前。
- 列类型：SQLite `TEXT NOT NULL DEFAULT ''`，MySQL/Postgres `VARCHAR(64) NOT NULL DEFAULT ''`。
- 租户与用户标识的合法性：去首尾空白后长度 1..64，匹配 `^[A-Za-z0-9_-]+$`。
- 租户不进入 JWT claims；`auth.Sign` 不改。
- 归属不符对调用方的表现必须与各接口现有的"会话不存在"完全一致；归属判定必须先于 `IsRunning`（/chat）与 `runtimeState.Get`（/chat/status）。
- 请求上下文键名固定：`tenant`、`user_id`（我的决定）。
- 所有 commit 由用户明确要求后才执行；本计划中的 Commit 步骤均为"向用户请求提交"而非自动提交。
- Go 单元测试：`go test ./internal/... -v`，全绿后才算任务完成。

## Review Focus

1. `X-User-ID` 为 ` alice ` 这类带空白的值应被 trim 后接受；为 `alice bob`、`a/b`、65 个字符时按缺失处理返回 400。(Task 4 覆盖)
2. 同一个 user_id 在两个租户下各有会话，租户 A 的调用方用租户 B 的 sid 请求 `/sess/{sid}` 必须 404，而非读到数据。(Task 7 覆盖)
3. 定时任务会话归属 ("", "")；业务 Key 永远拿不到空租户（403 在先），因此业务调用方不可能列出它们。(Task 6 覆盖 ListSessions 对 owner=("", "") 的精确匹配)
4. Web 管理员 Cookie 请求不带 `X-User-ID`，所有会话类接口必须照常返回全量数据而不是 400。(Task 7、8、9 覆盖)
5. `/chat/{sid}/{cid}` 中 cid 属于另一个 sid 时返回 404 `chat_not_found`，即使该 sid 归属匹配。(Task 5、8 覆盖)

---

### Task 1: 表结构：api_keys.tenant 与 memory_sessions.tenant_id

**Files:**
- Modify: `internal/db/migrate.go:200-209`（sqlite memory_sessions）、`:282-289`（sqlite api_keys）、`:354-363`（mysql memory_sessions）、`:436-443`（mysql api_keys）、`:508-517`（pg memory_sessions）、`:590-597`（pg api_keys）
- Test: `internal/db/migrate_test.go`

**Interfaces:**
- Produces: 列 `memory_sessions.tenant_id`、`api_keys.tenant`；索引 `idx_ms_tenant_user`（SQLite/PG 用 `CREATE INDEX IF NOT EXISTS`，MySQL 用 `KEY idx_tenant_user (tenant_id, user_id)`）。

- [ ] **Step 1: 写失败测试**

在 `internal/db/migrate_test.go` 追加：

```go
func TestDDLStatements_TenantColumnsOrder(t *testing.T) {
	for _, d := range []Dialect{DialectSQLite, DialectMySQL, DialectPostgres} {
		ddl := strings.Join(ddlStatements(d), "\n")
		ms := extractCreateTable(ddl, "memory_sessions")
		assertColumnOrder(t, d, ms, "session_id", "tenant_id", "user_id")
		ak := extractCreateTable(ddl, "api_keys")
		assertColumnOrder(t, d, ak, "name", "tenant", "permissions")
		if !strings.Contains(ddl, "idx_ms_tenant_user") && !strings.Contains(ddl, "idx_tenant_user") {
			t.Errorf("%v: missing tenant/user composite index", d)
		}
	}
}

func TestMigrate_FreshDBHasTenantColumns(t *testing.T) {
	sqlxDB, dialect, err := Open(nil, t.TempDir())
	if err != nil { t.Fatal(err) }
	defer sqlxDB.Close()
	for _, c := range []struct{ table, col string }{{"memory_sessions", "tenant_id"}, {"api_keys", "tenant"}} {
		ok, err := columnExists(sqlxDB, dialect, c.table, c.col)
		if err != nil || !ok { t.Errorf("%s.%s missing (err=%v)", c.table, c.col, err) }
	}
}
```

`extractCreateTable(ddl, table string) string` 返回包含 `CREATE TABLE IF NOT EXISTS <table> (` 的那条语句；`assertColumnOrder(t, d, stmt, a, b, c string)` 断言 `strings.Index(stmt, a) < strings.Index(stmt, b) < strings.Index(stmt, c)`，三个都 >= 0。两个辅助函数写在同一测试文件。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/db/ -run 'TestDDLStatements_TenantColumnsOrder|TestMigrate_FreshDBHasTenantColumns' -v`
Expected: FAIL，列不存在 / 顺序断言失败。

- [ ] **Step 3: 修改六处 CREATE TABLE 与索引**

- memory_sessions（三方言）：在 `session_id` 行后插入 `tenant_id` 行（类型见 Global Constraints）；SQLite/PG 在 `idx_ms_user_id` 之后追加 `CREATE INDEX IF NOT EXISTS idx_ms_tenant_user ON memory_sessions(tenant_id, user_id)`；MySQL 在 `KEY idx_user_id` 后追加 `KEY idx_tenant_user (tenant_id, user_id)`。
- api_keys（三方言）：在 `name` 行后插入 `tenant` 行。

- [ ] **Step 4: 运行全部 db 测试**

Run: `go test ./internal/db/... -v`
Expected: PASS（含既有的 shared_resources 补列测试不受影响）。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(db): api_keys 增加 tenant 列，memory_sessions 增加 tenant_id 列`

---

### Task 2: repo 层：APIKey.Tenant 与 Session.TenantID 的读写

**Files:**
- Modify: `internal/repo/apikey.go:10-16`、`internal/repo/apikeydb/apikey.go:27-33,60-66`
- Modify: `internal/repo/memory.go:10-19,79-92`、`internal/repo/memorydb/memory.go:89-171,315-360`
- Test: `internal/repo/apikeydb/apikey_test.go`、`internal/repo/memorydb/memory_test.go`、`internal/repo/memorydb/search_test.go`

**Interfaces:**
- Produces:
  - `repo.APIKey.Tenant string`
  - `repo.Session.TenantID string`
  - `type repo.SessionOwner struct { TenantID, UserID string }`
  - `MemoryRepo.ListSessions(ctx context.Context, owner *SessionOwner) ([]*Session, error)` — owner nil 不过滤，非 nil 对 `tenant_id` 与 `user_id` 同时等值匹配（空串是合法匹配值）。
  - `MemoryRepo.SearchChats(ctx context.Context, owner *SessionOwner, keyword string, limit int) ([]*SearchHit, error)` — 同上。
  - `CreateSession` 写入 `TenantID`；`GetSession` 回填 `TenantID`。

- [ ] **Step 1: 写失败测试（apikeydb）**

在 `internal/repo/apikeydb/apikey_test.go` 追加：

```go
func TestAPIKey_TenantRoundTrip(t *testing.T) {
	r := newTestRepo(t)
	k := &repo.APIKey{ID: "20260902120001", Name: "svc-t", Tenant: "crm", Permissions: []string{"chat"}, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now().Truncate(time.Second)}
	if err := r.Create(context.Background(), k); err != nil { t.Fatal(err) }
	got, err := r.GetByID(context.Background(), k.ID)
	if err != nil { t.Fatal(err) }
	if got.Tenant != "crm" { t.Errorf("Tenant=%q want crm", got.Tenant) }
	list, _ := r.List(context.Background())
	if list[0].Tenant != "crm" { t.Errorf("List Tenant=%q", list[0].Tenant) }
}
```

- [ ] **Step 2: 写失败测试（memorydb）**

在 `internal/repo/memorydb/memory_test.go` 追加：

```go
func TestSession_TenantRoundTrip(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	s := &repo.Session{SessionID: "s-t1", TenantID: "crm", UserID: "u1", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := r.CreateSession(ctx, s); err != nil { t.Fatal(err) }
	got, _ := r.GetSession(ctx, "s-t1")
	if got.TenantID != "crm" || got.UserID != "u1" { t.Errorf("got %+v", got) }
}

func TestListSessions_OwnerFilter(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	mk := func(id, tenant, user string) { _ = r.CreateSession(ctx, &repo.Session{SessionID: id, TenantID: tenant, UserID: user, CreatedAt: time.Now(), UpdatedAt: time.Now()}) }
	mk("a1", "crm", "u1"); mk("a2", "crm", "u2"); mk("b1", "erp", "u1"); mk("sched", "", "")
	all, _ := r.ListSessions(ctx, nil)
	if len(all) != 4 { t.Errorf("nil owner: want 4 got %d", len(all)) }
	crmU1, _ := r.ListSessions(ctx, &repo.SessionOwner{TenantID: "crm", UserID: "u1"})
	if len(crmU1) != 1 || crmU1[0].SessionID != "a1" { t.Errorf("crm/u1: %+v", crmU1) }
	empty, _ := r.ListSessions(ctx, &repo.SessionOwner{})
	if len(empty) != 1 || empty[0].SessionID != "sched" { t.Errorf("empty owner must match only sched: %+v", empty) }
}
```

在 `internal/repo/memorydb/search_test.go` 追加 `TestSearchChats_OwnerFilter`：建 ("crm","u1") 与 ("erp","u1") 两个会话各存一条 completed 主 Agent 记录含关键词 `租户过滤`；`SearchChats(ctx, nil, "租户过滤", 20)` 得 2 条；`SearchChats(ctx, &repo.SessionOwner{"crm","u1"}, ...)` 得 1 条且 SessionID 为 crm 的；`SearchChats(ctx, &repo.SessionOwner{"crm","u2"}, ...)` 得 0 条。

同时把本文件及 `memory_test.go` 内所有既有的 `r.ListSessions(ctx)` 改为 `r.ListSessions(ctx, nil)`，`r.SearchChats(ctx, "", kw, n)` 改为 `r.SearchChats(ctx, nil, kw, n)`。

- [ ] **Step 3: 运行确认编译失败**

Run: `go test ./internal/repo/... 2>&1 | head`
Expected: 编译错误（字段/类型不存在、参数个数不符）。

- [ ] **Step 4: 实现 repo 接口与结构**

- `internal/repo/apikey.go`：`APIKey` 增加 `Tenant string // 所属租户，非唯一`，放在 `Name` 之后。
- `internal/repo/apikeydb/apikey.go`：row 结构与 `apiKeyColumns` 加 `tenant`（置于 name 与 permissions 之间）；`Create` 的 INSERT 列与值同步；行到 `repo.APIKey` 的转换回填 `Tenant`。
- `internal/repo/memory.go`：`Session` 增加 `TenantID string`（放在 `SessionID` 之后）；新增 `SessionOwner`；`ListSessions`、`SearchChats` 改签名并更新注释（owner nil 不过滤，非 nil 等值匹配）。
- `internal/repo/memorydb/memory.go`：`CreateSession` INSERT 加 `tenant_id`；`GetSession`、`ListSessions` 的 row 结构与 SELECT 加 `tenant_id` 并回填；`ListSessions` 与 `SearchChats` 在 owner 非 nil 时追加 `AND s.tenant_id = ? AND s.user_id = ?`（WHERE 子句拼接方式由实现者决定，参数用 Rebind），删除原 `(? = '' OR s.user_id = ?)` 写法。

- [ ] **Step 5: 运行 repo 测试**

Run: `go test ./internal/repo/... -v`
Expected: PASS。

- [ ] **Step 6: 请求用户提交**

建议 message：`feat(repo): API Key 与会话携带租户，列表/搜索支持按归属过滤`

---

### Task 3: memory 层：CreateSession 签名、OwnsSession、归属过滤、GetChatRecord 校验

**Files:**
- Modify: `internal/memory/manager.go:31-40,74-106,246-253`、`internal/memory/search.go:27-41`、`internal/memory/memory.go:6,9`
- Modify: `internal/schedule/runner.go:59,140`（调用方改签名）、`internal/api/handler/health.go:104`（改为 `ListSessions(nil, 1, 0)`）
- Test: `internal/memory/manager_test.go`（新建）、`internal/memory/search_test.go`、`internal/memory/memory_test.go`、`internal/memory/debug2_test.go`

**Interfaces:**
- Consumes: Task 2 的 `repo.SessionOwner`、`ListSessions(ctx, owner)`、`SearchChats(ctx, owner, kw, limit)`。
- Produces:
  - `(*Manager).CreateSession(sessionID, tenantID, userID string) error`
  - `(*Manager).OwnsSession(sessionID, tenantID, userID string) bool` — 会话存在且 `TenantID==tenantID && UserID==userID`。
  - `(*Manager).ListSessions(owner *repo.SessionOwner, limit, offset int) ([]SessionInfo, int, error)`
  - `(*Manager).Search(owner *repo.SessionOwner, keyword string, limit int) ([]SearchResult, error)`
  - `(*Manager).GetChatRecord(sessionID, chatID string)` 在 `rec.SessionID != sessionID` 时返回错误（视为未找到）。
  - `memory.Memory` 接口（`memory.go`）同步为 `CreateSession(sessionID, tenantID, userID string) error` 与 `ListSessions(owner *repo.SessionOwner, limit, offset int) ([]SessionInfo, int, error)`，并新增 `OwnsSession(sessionID, tenantID, userID string) bool`。

- [ ] **Step 1: 写失败测试**

新建 `internal/memory/manager_test.go`（若包内已有同名辅助函数构造 Manager，复用之；否则用 `db.Open(nil, t.TempDir())` + `memorydb.New` 构造）：

```go
func TestOwnsSession(t *testing.T) {
	m := newTestManager(t)
	_ = m.CreateSession("s1", "crm", "u1")
	cases := []struct{ sid, tenant, user string; want bool }{
		{"s1", "crm", "u1", true},
		{"s1", "crm", "u2", false},
		{"s1", "erp", "u1", false},
		{"missing", "crm", "u1", false},
	}
	for _, c := range cases {
		if got := m.OwnsSession(c.sid, c.tenant, c.user); got != c.want {
			t.Errorf("OwnsSession(%q,%q,%q)=%v want %v", c.sid, c.tenant, c.user, got, c.want)
		}
	}
}

func TestListSessions_ByOwner(t *testing.T) {
	m := newTestManager(t)
	_ = m.CreateSession("a", "crm", "u1"); _ = m.CreateSession("b", "erp", "u1")
	got, total, _ := m.ListSessions(&repo.SessionOwner{TenantID: "crm", UserID: "u1"}, 20, 0)
	if total != 1 || len(got) != 1 || got[0].SessionID != "a" { t.Errorf("got %+v total=%d", got, total) }
	all, totalAll, _ := m.ListSessions(nil, 20, 0)
	if totalAll != 2 || len(all) != 2 { t.Errorf("nil owner: %d", totalAll) }
}

func TestGetChatRecord_RejectsForeignChat(t *testing.T) {
	m := newTestManager(t)
	_ = m.CreateSession("s1", "crm", "u1"); _ = m.CreateSession("s2", "crm", "u1")
	_ = m.SaveChatRecord("s1", &ChatRecord{ChatID: "c1", Instruction: "x", Result: "y", Status: "completed", StartedAt: time.Now()})
	if _, err := m.GetChatRecord("s1", "c1"); err != nil { t.Fatalf("own chat: %v", err) }
	if _, err := m.GetChatRecord("s2", "c1"); err == nil { t.Fatal("chat of s1 must not be readable via s2") }
}
```

修改既有测试调用：`search_test.go` 中 `m.Search("", kw, n)` → `m.Search(nil, kw, n)`，`m.Search("other-user", ...)` / `m.Search("some-user", ...)` → `m.Search(&repo.SessionOwner{UserID: "other-user"}, ...)`（租户空串，与测试中 `CreateSession(..., "", "some-user")` 对应）；所有 `CreateSession(id, user)` → `CreateSession(id, "", user)`。

- [ ] **Step 2: 运行确认编译失败**

Run: `go test ./internal/memory/ 2>&1 | head`
Expected: 编译错误。

- [ ] **Step 3: 实现**

- `manager.go`：`CreateSession` 增加 `tenantID` 参数并写入 `repo.Session.TenantID`；新增 `OwnsSession`（调 `repo.GetSession`，err 非 nil 返回 false）；`ListSessions` 透传 owner 到 repo；`GetChatRecord` 取回记录后比对 `SessionID`，不符返回 `fmt.Errorf("对话记录不存在: %s", chatID)`。
- `search.go`：`Search` 第一个参数改为 `owner *repo.SessionOwner` 并透传。
- `memory.go`：接口签名同步。
- `schedule/runner.go` 两处：`CreateSession(sessionID, "", "")`。
- `handler/health.go:104`：`h.memoryManager.ListSessions(nil, 1, 0)`。

- [ ] **Step 4: 运行 memory 与 schedule 测试**

Run: `go build ./... && go test ./internal/memory/... ./internal/schedule/... -v`
Expected: 构建通过（handler 包的 chat/session/search 调用在本任务仍用旧签名会编译失败——因此本任务同时把 `handler/chat.go:229` 临时改为 `CreateSession(sessionID, "", userID)`、`handler/session.go:91` 改为 `ListSessions(nil, limitInt, offsetInt)`、`handler/search.go:35` 改为 `Search(&repo.SessionOwner{UserID: userID}, q, limit)` 以保持可编译，行为由 Task 6、7 重写），memory 与 schedule 测试 PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(memory): 会话归属判定 OwnsSession，列表/搜索按归属过滤，对话记录校验所属会话`

---

### Task 4: 鉴权中间件：注入 tenant 与 user_id，提供标识校验

**Files:**
- Modify: `internal/api/middleware/auth.go:86-87,131-138`
- Create: `internal/api/middleware/identity.go`
- Test: `internal/api/middleware/auth_test.go`、`internal/api/middleware/identity_test.go`（新建）

**Interfaces:**
- Consumes: Task 2 的 `repo.APIKey.Tenant`。
- Produces（包 `middleware`）：
  - `const UserIDHeader = "X-User-ID"`
  - `func ValidIdentifier(s string) (string, bool)` — trim 后长度 1..64 且匹配 `^[A-Za-z0-9_-]+$` 返回 (trimmed, true)，否则 ("", false)。
  - `func GetTenant(rc *app.RequestContext) string`、`func GetUserID(rc *app.RequestContext) string` — 从上下文键 `tenant`、`user_id` 读取，缺失返回空串。
  - `func IsWebCaller(rc *app.RequestContext) bool` — `GetCaller(rc) == "web"`。
  - 中间件 JWT 分支在 `rc.Set("caller", key.Name)` 处同时 `rc.Set("tenant", key.Tenant)` 与 `rc.Set("user_id", <ValidIdentifier(X-User-ID) 的值或空串>)`。Cookie 分支不设这两个键。

- [ ] **Step 1: 写失败测试**

`internal/api/middleware/identity_test.go`：

```go
func TestValidIdentifier(t *testing.T) {
	cases := []struct{ in, want string; ok bool }{
		{"alice", "alice", true}, {" alice ", "alice", true}, {"a_b-9", "a_b-9", true},
		{"", "", false}, {"   ", "", false}, {"alice bob", "", false}, {"a/b", "", false},
		{strings.Repeat("x", 64), strings.Repeat("x", 64), true}, {strings.Repeat("x", 65), "", false},
	}
	for _, c := range cases {
		got, ok := ValidIdentifier(c.in)
		if got != c.want || ok != c.ok { t.Errorf("ValidIdentifier(%q)=(%q,%v) want (%q,%v)", c.in, got, ok, c.want, c.ok) }
	}
}
```

`auth_test.go` 追加（`newTestMiddleware` 中预置 Key 的 `Tenant` 设为 `"crm"`）：

```go
func TestAuth_InjectsTenantAndUserID(t *testing.T) {
	m, _, _, token := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodPost, "/chat", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token); rc.Request.Header.Set("X-User-ID", " u1 ")
	})
	if GetTenant(rc) != "crm" || GetUserID(rc) != "u1" { t.Errorf("tenant=%q user=%q", GetTenant(rc), GetUserID(rc)) }
}

func TestAuth_InvalidUserIDBecomesEmpty(t *testing.T) {
	m, _, _, token := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodPost, "/chat", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token); rc.Request.Header.Set("X-User-ID", "bad id")
	})
	if rc.Response.StatusCode() == 401 { t.Fatal("must still pass auth") }
	if GetUserID(rc) != "" { t.Errorf("user=%q want empty", GetUserID(rc)) }
}

func TestAuth_WebCookieHasNoTenant(t *testing.T) {
	// 复用文件内既有的 Cookie 放行测试构造方式，断言 IsWebCaller(rc) && GetTenant(rc)=="" && GetUserID(rc)==""
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/middleware/ -run 'TestValidIdentifier|TestAuth_Injects|TestAuth_InvalidUserID|TestAuth_WebCookie' -v`
Expected: FAIL（函数未定义）。

- [ ] **Step 3: 实现 identity.go 与 auth.go 改动**

正则用包级 `regexp.MustCompile`。

- [ ] **Step 4: 运行 middleware 测试**

Run: `go test ./internal/api/middleware/... -v`
Expected: PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(auth): 中间件注入租户与用户标识到请求上下文`

---

### Task 5: handler 公共前置检查

**Files:**
- Create: `internal/api/handler/owner.go`
- Test: `internal/api/handler/owner_test.go`（新建）

**Interfaces:**
- Consumes: Task 4 的 `middleware.IsWebCaller`、`GetTenant`、`GetUserID`；Task 2 的 `repo.SessionOwner`。
- Produces（包 `handler`）：
  - `func requireOwner(rc *app.RequestContext) (owner *repo.SessionOwner, ok bool)` — Web 调用者返回 `(nil, true)`；租户为空写 403 `{"status":"forbidden","message":"API Key 未绑定租户"}` 并返回 `(nil,false)`；用户为空写 400 `{"status":"invalid_request","message":"缺少或非法的 X-User-ID"}` 并返回 `(nil,false)`；否则返回 `(&repo.SessionOwner{TenantID, UserID}, true)`。
  - `func ownsOrAdmin(mem *memory.Manager, owner *repo.SessionOwner, sid string) bool` — owner nil 时等于 `mem.ExistsSession(sid)`，否则 `mem.OwnsSession(sid, owner.TenantID, owner.UserID)`。

响应写法沿用各 handler 现有的 `rc.SetContentType("application/json"); rc.SetStatusCode(...); rc.Write(...)` 风格，并 `rc.Abort()` 不需要（handler 内部直接 return）。

- [ ] **Step 1: 写失败测试**

```go
func TestRequireOwner(t *testing.T) {
	mk := func(caller, tenant, user string) *app.RequestContext {
		rc := app.NewContext(0)
		if caller != "" { rc.Set("caller", caller) }
		rc.Set("tenant", tenant); rc.Set("user_id", user)
		return rc
	}
	if o, ok := requireOwner(mk("web", "", "")); !ok || o != nil { t.Error("web must pass with nil owner") }
	if _, ok := requireOwner(mk("svc", "", "u1")); ok { t.Error("empty tenant must fail") } else if rc := mk("svc","","u1"); func() bool { requireOwner(rc); return rc.Response.StatusCode()==403 }() == false { t.Error("want 403") }
	if rc := mk("svc", "crm", ""); func() bool { _, ok := requireOwner(rc); return !ok && rc.Response.StatusCode()==400 }() == false { t.Error("want 400") }
	if o, ok := requireOwner(mk("svc", "crm", "u1")); !ok || o == nil || o.TenantID != "crm" || o.UserID != "u1" { t.Errorf("got %+v %v", o, ok) }
}
```

（实现者可把上面的内联闭包整理成表驱动，断言不变：状态码 403/400、返回值。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestRequireOwner -v`
Expected: FAIL（未定义）。

- [ ] **Step 3: 实现 owner.go**

- [ ] **Step 4: 运行**

Run: `go test ./internal/api/handler/ -run TestRequireOwner -v`
Expected: PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(api): 会话类接口公共归属前置检查`

---

### Task 6: /chat 写入归属并在续聊时校验

**Files:**
- Modify: `internal/api/handler/chat.go:111-112,138-148,187,229,305`
- Test: `internal/api/handler/chat_test.go`

**Interfaces:**
- Consumes: Task 5 `requireOwner`、`ownsOrAdmin`；Task 3 `CreateSession(sid, tenant, user)`。

- [ ] **Step 1: 写失败测试**

现有 `newChatHandlerForTest` 构造的 handler 没有 memory/runtimeState，前置检查在模型解析之后、`X-Session-ID` 处理之前，所以用它即可覆盖 403/400：

```go
func TestChatHandler_MissingUserIDReturns400(t *testing.T) {
	h := newChatHandlerForTest(t, agent.NewRegistryForTest(1))
	rc := newChatPostContext(`{"instruction":"hi"}`) // 抽取现有测试中的 rc 构造为辅助函数
	rc.Set("caller", "svc"); rc.Set("tenant", "crm") // 无 user_id
	h.Handle(context.Background(), rc)
	if rc.Response.StatusCode() != 400 || !strings.Contains(string(rc.Response.Body()), "X-User-ID") { t.Fatalf("%d %s", rc.Response.StatusCode(), rc.Response.Body()) }
}

func TestChatHandler_EmptyTenantReturns403(t *testing.T) {
	// 同上，rc.Set("tenant",""); rc.Set("user_id","u1")；断言 403 且 body 含 "forbidden"
}
```

归属写入与续聊校验需要完整 handler（memory + runtimeState + settings），若 `chat_test.go` 没有现成的完整构造，本任务只以上述两个测试为必需，续聊归属行为由 Task 10 的 Python 系统测试用例点覆盖并在 TEST_CASES.md 标注。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestChatHandler_MissingUserID|TestChatHandler_EmptyTenant' -v`
Expected: FAIL（目前返回非 400/403）。

- [ ] **Step 3: 实现**

- 删除 `userID := string(rc.GetHeader("X-User-ID"))`；在 `X-Agent-Name` 校验之后、`X-Session-ID` 提取之前调用 `owner, ok := requireOwner(rc); if !ok { return }`。
- 续聊判定：把第 4 步 `IsRunning` 前移到归属判定之后，即：`if sessionID != "" && !ownsOrAdmin(h.memory, owner, sessionID) { sessionID = "" }`，然后再做 `IsRunning` 检查与后续 `ExistsSession` 分支（此时 `ExistsSession` 必为真或 sessionID 已清空，可直接改为 `if sessionID == ""`）。
- 新建：`h.memory.CreateSession(sessionID, tenantOf(owner), userOf(owner))`，owner nil（Web）时两者为空串。
- 日志：`sessionLog := h.log.With(zap.String("session_id", sessionID), zap.String("tenant", ...), zap.String("user_id", ...))`。

- [ ] **Step 4: 运行 handler 测试**

Run: `go test ./internal/api/handler/... -v`
Expected: PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(chat): 新建会话写入归属，续聊按归属校验且先于并发检查`

---

### Task 7: /sess/{sid}、/sess/history、/sess/search 归属隔离

**Files:**
- Modify: `internal/api/handler/session.go:24-41,73-105`、`internal/api/handler/search.go:32-35`
- Test: `internal/api/handler/session_test.go`（新建）、`internal/api/handler/search_test.go`

**Interfaces:**
- Consumes: Task 5 `requireOwner`、`ownsOrAdmin`；Task 3 `ListSessions(owner, limit, offset)`、`Search(owner, kw, limit)`。

- [ ] **Step 1: 写失败测试**

`session_test.go`（复用 `newSearchTestHandler` 构造 handler；path param 用 `status_test.go` 的 `param.Param` 写法）：

```go
func setIdentity(rc *app.RequestContext, caller, tenant, user string) { rc.Set("caller", caller); rc.Set("tenant", tenant); rc.Set("user_id", user) }

func TestGetSession_ForeignTenantIs404(t *testing.T) {
	h, mem := newSearchTestHandler(t)
	_ = mem.CreateSession("s1", "crm", "u1")
	rc := newSidContext("s1"); setIdentity(rc, "svc", "erp", "u1")
	h.GetSession(context.Background(), rc)
	if rc.Response.StatusCode() != 404 { t.Fatalf("got %d", rc.Response.StatusCode()) }
}

func TestGetSession_OwnerOK(t *testing.T) { /* ("crm","u1") 读 s1 → 200 */ }
func TestGetSession_WebAdminBypasses(t *testing.T) { /* caller=web 无 tenant/user 读 s1 → 200 */ }
func TestGetSession_MissingUserID400(t *testing.T) { /* caller=svc tenant=crm user="" → 400 */ }

func TestListSessions_FilteredByOwner(t *testing.T) {
	h, mem := newSearchTestHandler(t)
	_ = mem.CreateSession("a", "crm", "u1"); _ = mem.CreateSession("b", "crm", "u2"); _ = mem.CreateSession("c", "erp", "u1")
	rc := newListContext("limit=10"); setIdentity(rc, "svc", "crm", "u1")
	h.ListSessions(context.Background(), rc)
	var resp struct{ Total int `json:"total"`; Sessions []struct{ SessionID string `json:"session_id"` } `json:"sessions"` }
	_ = json.Unmarshal(rc.Response.Body(), &resp)
	if resp.Total != 1 || len(resp.Sessions) != 1 || resp.Sessions[0].SessionID != "a" { t.Errorf("%+v", resp) }
}
func TestListSessions_WebAdminSeesAll(t *testing.T) { /* caller=web → total 3 */ }
```

`search_test.go`：现有测试的 `CreateSession("hs-1", "")` → `CreateSession("hs-1", "crm", "u1")`，并在 rc 上 `setIdentity(rc, "svc", "crm", "u1")`；追加 `TestSearchSessions_ForeignTenantEmpty`（`setIdentity(rc,"svc","erp","u1")` → results 为空）与 `TestSearchSessions_MissingUserID400`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestGetSession_|TestListSessions_|TestSearchSessions_' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- `GetSession`：参数检查后 `owner, ok := requireOwner(rc)`；`if !ownsOrAdmin(h.memory, owner, sessionID)` → 404 `session_not_found`（沿用现有 body）；其余不变。
- `ListSessions`：解析分页后 `requireOwner`，调 `h.memory.ListSessions(owner, limitInt, offsetInt)`。
- `SearchSessions`（search.go）：删除读 `X-User-ID` 的两行，`requireOwner` 后调 `h.memory.Search(owner, q, limit)`。

- [ ] **Step 4: 运行**

Run: `go test ./internal/api/handler/... -v`
Expected: PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(sess): 会话详情/列表/搜索按租户与用户隔离`

---

### Task 8: /chat/{sid}、/chat/{sid}/{cid} 归属隔离

**Files:**
- Modify: `internal/api/handler/detail.go:44-50,80-86`
- Test: `internal/api/handler/detail_test.go`（新建）

**Interfaces:**
- Consumes: Task 5；Task 3 `GetChatRecord` 的 session 校验。

- [ ] **Step 1: 写失败测试**

```go
func TestDetail_ForeignSessionIs404(t *testing.T) {
	// 建 ("crm","u1") 会话 s1 + 记录 c1；setIdentity(rc,"svc","erp","u1")；Serve(sid=s1,cid=c1) → 404 session_not_found
}
func TestDetail_ForeignChatIs404(t *testing.T) {
	// 建 s1、s2 同归属 ("crm","u1")，c1 属于 s1；Serve(sid=s2,cid=c1) → 404 chat_not_found
}
func TestDetail_OwnerOK(t *testing.T) { /* 200 */ }
func TestGetLatest_ForeignSessionIs404(t *testing.T) { /* 同 Foreign 场景走 GetLatest */ }
func TestGetLatest_WebAdminOK(t *testing.T) { /* caller=web → 200 */ }
```

构造 `DetailHandler` 用 `NewDetailHandler(mem)`（按文件内现有构造函数签名）；rc 用 `param.Param{Key:"sid"}`、`{Key:"cid"}`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestDetail_|TestGetLatest_' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

两个方法在参数检查后 `requireOwner`，把 `!h.memory.ExistsSession(sessionID)` 替换为 `!ownsOrAdmin(h.memory, owner, sessionID)`。cid 归属由 Task 3 的 `GetChatRecord` 保证，现有 `chat_not_found` 分支已覆盖。

- [ ] **Step 4: 运行**

Run: `go test ./internal/api/handler/... -v`
Expected: PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(chat): 对话详情接口按归属隔离`

---

### Task 9: /chat/status/{sid} 归属判定先于活跃状态

**Files:**
- Modify: `internal/api/handler/status.go:33-45`
- Test: `internal/api/handler/status_test.go`

**Interfaces:**
- Consumes: Task 5。

- [ ] **Step 1: 写失败测试**

```go
func TestStatus_ForeignRunningSessionLooksIdle(t *testing.T) {
	rt := agent.NewRuntimeState(); _, _ = rt.Register("s1", "c1")
	sqlxDB, dialect, _ := db.Open(nil, t.TempDir()); t.Cleanup(func(){ sqlxDB.Close() })
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	_ = mem.CreateSession("s1", "crm", "u1")
	h := NewStatusHandler(rt, mem)
	rc := newStatusTestContext("s1"); setIdentity(rc, "svc", "erp", "u1")
	h.Serve(context.Background(), rc)
	body := string(rc.Response.Body())
	if rc.Response.StatusCode() != 200 || !strings.Contains(body, `"idle"`) || !strings.Contains(body, `"round_count":0`) || strings.Contains(body, "c1") {
		t.Fatalf("foreign caller must see idle/0 and no chat id: %d %s", rc.Response.StatusCode(), body)
	}
}
func TestStatus_MissingUserID400(t *testing.T) { /* setIdentity(rc,"svc","crm","") → 400 */ }
```

现有 `TestStatusHandler_IncludesSubAgents` 的 rc 需加 `setIdentity(rc, "web", "", "")` 以保持管理员视角通过。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestStatus_' -v`
Expected: FAIL（当前返回活跃状态含 c1）。

- [ ] **Step 3: 实现**

参数检查后 `owner, ok := requireOwner(rc)`；若 `owner != nil && !h.memory.OwnsSession(sessionID, owner.TenantID, owner.UserID)` 则直接写现有的"会话不存在"响应（200 idle、round_count 0）并 return；之后逻辑不变。

- [ ] **Step 4: 运行**

Run: `go test ./internal/api/handler/... -v`
Expected: PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(status): 归属判定先于活跃状态查询`

---

### Task 10: API Key 创建/列表携带租户（后端）

**Files:**
- Modify: `internal/api/handler/apikeys.go:42-72,108-160`
- Test: `internal/api/handler/apikeys_test.go`

**Interfaces:**
- Consumes: Task 4 `middleware.ValidIdentifier`；Task 2 `repo.APIKey.Tenant`。
- Produces: `POST /web/apikeys` 请求体字段 `tenant`（必填）；`apiKeyInfo` 响应字段 `tenant`。

- [ ] **Step 1: 写失败测试**

```go
func TestAPIKeys_CreateRequiresTenant(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	for _, body := range []string{
		`{"name":"a","expires_in":"7d","permissions":["chat"]}`,
		`{"name":"a","tenant":"  ","expires_in":"7d","permissions":["chat"]}`,
		`{"name":"a","tenant":"bad tenant","expires_in":"7d","permissions":["chat"]}`,
	} {
		rc := postJSON(h.Create, body)
		if rc.Response.StatusCode() != 400 || !strings.Contains(string(rc.Response.Body()), "invalid_tenant") { t.Errorf("%s → %d %s", body, rc.Response.StatusCode(), rc.Response.Body()) }
	}
}

func TestAPIKeys_TenantPersistedAndListed(t *testing.T) {
	h, keys := newAPIKeysHandler(t)
	rc := postJSON(h.Create, `{"name":"a","tenant":" crm ","expires_in":"7d","permissions":["chat"]}`)
	if rc.Response.StatusCode() != 200 { t.Fatalf("%d %s", rc.Response.StatusCode(), rc.Response.Body()) }
	k, _ := keys.GetByName(context.Background(), "a")
	if k.Tenant != "crm" { t.Errorf("Tenant=%q", k.Tenant) }
	if !strings.Contains(string(rc.Response.Body()), `"tenant":"crm"`) { t.Errorf("body: %s", rc.Response.Body()) }
}
```

既有测试中所有创建请求体补上 `"tenant":"t1"`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestAPIKeys_' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- `apiKeyCreateRequest` 加 `Tenant string \`json:"tenant"\``；`apiKeyInfo` 加 `Tenant string \`json:"tenant"\``；`toAPIKeyInfo` 回填。
- `Create` 在名称校验之后：`tenant, ok := middleware.ValidIdentifier(req.Tenant); if !ok → 400 {"status":"invalid_tenant","message":"租户标识不能为空，且只能包含字母、数字、下划线、短横线，长度不超过 64"}`；构造 `repo.APIKey` 时设 `Tenant: tenant`。

- [ ] **Step 4: 运行**

Run: `go test ./internal/api/handler/... -v`
Expected: PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(apikeys): 创建 API Key 必须绑定租户`

---

### Task 11: 管理界面：租户输入与展示

**Files:**
- Modify: `web/src/api/types.ts:219-226`、`web/src/components/settings/ApiKeysPanel.vue:19,104,114-127,231,259-272,287-298`、`web/src/i18n/messages/zh-cn.ts:256+`、`web/src/i18n/messages/en.ts`（对应 apikeys 块）

**Interfaces:**
- Consumes: Task 10 的 `tenant` 请求/响应字段。

- [ ] **Step 1: 修改类型与文案**

- `ApiKeyInfo` 加 `tenant: string`。
- i18n 两种语言 `apikeys` 块加 `tenant: '租户' / 'Tenant'`、`tenantPlaceholder: '如 crm，字母数字下划线短横线' / 'e.g. crm (letters, digits, _ -)'`、`tenantRequired: '请输入租户标识' / 'Tenant is required'`。

- [ ] **Step 2: 修改面板**

- `form` 初值与重置处加 `tenant: ''`。
- 提交前校验：`tenant` trim 为空时 `ElNotification.warning(... t('apikeys.tenantRequired'))` 并 return；请求体加 `tenant: form.value.tenant.trim()`。
- 创建表单：在名称项之后加 `<el-form-item :label="t('apikeys.tenant')" required><el-input v-model="form.tenant" :placeholder="t('apikeys.tenantPlaceholder')" maxlength="64" /></el-form-item>`。
- 详情只读表单：名称项之后加只读的租户 `el-input :model-value="k.tenant" disabled`。
- 列表行：`key-name` 之后展示 `k.tenant`（样式复用相邻次要文本的 class，实现者选）。

- [ ] **Step 3: 构建前端**

Run: `cd web && npm run build`
Expected: 构建成功，无 TS 错误。

- [ ] **Step 4: 请求用户提交**

建议 message：`feat(web): API Key 管理页增加租户字段`

---

### Task 12: 文档与测试用例汇总

**Files:**
- Modify: `README.md:620-626`（权限表附近补租户说明）、`:1288-1291`（请求头表：`X-User-ID` 改为会话类接口必填，说明 400/403）、`:1807-1941`（/sess 三接口补归属说明）、API Key 创建小节补 `tenant` 参数
- Modify: `tests/TEST_CASES.md`：新增 `### 1.11 会话租户与用户隔离测试` 小节（格式参照 1.9），并在 `## 二、系统测试` 下补用户待运行的覆盖点：续聊归属不符生成新 sid、归属不符的运行中会话 `/chat` 不返回 409、`/chat/status` 对他人运行中会话返回 idle。
- Modify: `examples/python/*.py`、`tests/python/*.py` 中调用会话类接口处补 `X-User-ID` 请求头（用 grep `"/sess/"`、`"/chat"` 定位）。

- [ ] **Step 1: 更新 README 四处**

措辞遵循 CLAUDE.md"功能设计正面陈述"规范，不写"新增/原来"。

- [ ] **Step 2: 更新 TEST_CASES.md**

列出 Task 1..10 新增的测试名与覆盖点。

- [ ] **Step 3: 更新 Python 示例与系统测试的请求头**

- [ ] **Step 4: 全量验证**

Run: `go build -o dist/groot ./cmd && go test ./internal/... 2>&1 | tail -20`
Expected: 构建成功，所有包 `ok`。

- [ ] **Step 5: 请求用户提交**

建议 message：`docs: 会话租户隔离的使用说明与测试用例汇总`
