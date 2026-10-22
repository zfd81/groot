# 会话标题 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 会话拥有持久化标题：创建时取首轮指令截取前 100 个字符，`PATCH /sess/{sid}` 可重命名，列表/搜索/详情直接读标题列。

**Architecture:** `memory_sessions` 加 `title` 列（只改建表语句）。repo 层 `CreateSession` 写入、读取路径回填、新增 `UpdateTitle`；`ListSessions`/`SearchChats` 删掉从 `memory_chats` 现算标题的子查询改读 `s.title`。memory 层 `CreateSession` 增加 `title` 参数、新增 `RenameSession` 与 `TruncateTitle`。`/chat` 与定时任务创建会话时传截取后的指令；`SessionHandler.RenameSession` 复用 `requireOwner`/`ownsOrAdmin`。

**Tech Stack:** Go、Hertz、sqlx（SQLite/MySQL/Postgres）。

**Spec:** `docs/superpowers/specs/2026-10-22-session-title-design.md`

## Global Constraints

- `title` 列位于 `memory_sessions` 的 `user_id` 之后、`prompt` 之前；SQLite `TEXT NOT NULL DEFAULT ''`，MySQL/Postgres `VARCHAR(100) NOT NULL DEFAULT ''`。只改 `CREATE TABLE`，不补列、不重建。
- 标题长度按 Unicode 字符（rune）计，上限 100。初始化：`TrimSpace` 后超过 100 个字符截取前 100 个，不加省略号。重命名：`TrimSpace` 后 1..100，否则 400 `{"status":"invalid_request","message":"标题不能为空，且长度不能超过 100 个字符"}`。
- `UpdateTitle` 只更新 `title`，不更新 `updated_at`；影响 0 行返回 `repo.ErrNotFound`。
- `ListSessions` 与 `SearchChats` 的 `Title` 来自 `s.title` 列；原 `COALESCE((SELECT c.instruction ...)) AS title` 子查询删除。
- `PATCH /sess/{sid}` 顺序：sid 空 → 400；请求体解析失败 → 400 "请求参数错误"；标题校验 → 400；`requireOwner`；`ownsOrAdmin` false → 404 `{"status":"session_not_found","message":"会话不存在"}`（字节一致）；`RenameSession` `ErrNotFound` → 同一 404；其他错误 → 500 `{"status":"error","message":"重命名失败"}`；成功 200 `{"status":"success","session_id":sid,"title":title}`。权限归现有 `session`（中间件无需改动）。
- `GET /sess/{sid}` 响应 `session` 对象增加 `title`。
- 定时任务会话标题 = `TruncateTitle(task.TaskDef.Instruction)`，无兜底。
- 所有 Commit 步骤为"向用户请求提交"，不自动提交（CLAUDE.md 默认模式）。
- `go build ./...`、`gofmt -l` 对触碰文件为空、`go test ./internal/...` 全绿。

## Review Focus

1. 指令含多字节字符（中文、emoji）恰为 100 与 101 个字符时的截断按字符而非字节。(Task 2 覆盖)
2. 重命名后 `ListSessions` 顺序与 `last_active_at` 不变（`updated_at` 未动）。(Task 1 覆盖)
3. 重命名一个只有创建、尚无对话的会话（round 0）成功，列表立即显示新标题。(Task 4 覆盖)
4. 请求体 `{"title": 123}`（类型错误）或非 JSON → 400 "请求参数错误"，不是 500。(Task 4 覆盖)
5. 前端 `SessionSidebar` 的 `title?.trim() || sid 前 8 位` 兜底在标题列为空（历史数据或极端情况）时仍成立，无需改前端。(Task 5 核对 README 即可，不改代码)

---

### Task 1: 表结构与 repo 层

**Files:**
- Modify: `internal/db/migrate.go:203-204`（sqlite）、`:360-361`（mysql）、`:517-518`（pg）
- Modify: `internal/repo/memory.go:10-21,88-104`、`internal/repo/memorydb/memory.go:89-97,102-128,136-171,315-360`
- Test: `internal/db/migrate_test.go`、`internal/repo/memorydb/memory_test.go`、`internal/repo/memorydb/search_test.go`

**Interfaces:**
- Produces:
  - 列 `memory_sessions.title`。
  - `repo.Session.Title` 由 `CreateSession` 写入（注释改为"会话标题"），`GetSession`/`ListSessions` 回填。
  - `MemoryRepo.UpdateTitle(ctx context.Context, sessionID, title string) error`。
  - `SearchHit.Title` 来自 `s.title`。

- [ ] **Step 1: 写失败测试**

`migrate_test.go`：在 `TestDDLStatements_TenantColumnsOrder` 旁加 `TestDDLStatements_TitleColumnOrder`，三方言断言 `assertColumnOrder(t, d, ms, "user_id", "title", "prompt")`；`TestMigrate_FreshDBHasTenantColumns` 的列表加 `{"memory_sessions","title"}`。

`memory_test.go`：

```go
func TestSession_TitleRoundTrip(t *testing.T) {
	r := newMemRepo(t); ctx := context.Background()
	s := &repo.Session{SessionID: "s-t", TenantID: "crm", UserID: "u1", Title: "第一轮问题", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := r.CreateSession(ctx, s); err != nil { t.Fatal(err) }
	got, err := r.GetSession(ctx, "s-t"); if err != nil { t.Fatal(err) }
	if got.Title != "第一轮问题" { t.Errorf("Title=%q", got.Title) }
	list, _ := r.ListSessions(ctx, nil)
	if len(list) != 1 || list[0].Title != "第一轮问题" { t.Errorf("list title %+v", list) }
}

func TestUpdateTitle(t *testing.T) {
	r := newMemRepo(t); ctx := context.Background()
	created := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	_ = r.CreateSession(ctx, &repo.Session{SessionID: "s1", Title: "old", CreatedAt: created, UpdatedAt: created})
	if err := r.UpdateTitle(ctx, "s1", "new"); err != nil { t.Fatal(err) }
	got, _ := r.GetSession(ctx, "s1")
	if got.Title != "new" { t.Errorf("Title=%q", got.Title) }
	if !got.UpdatedAt.Equal(created) { t.Errorf("updated_at must not change: %v vs %v", got.UpdatedAt, created) }
	if err := r.UpdateTitle(ctx, "missing", "x"); !errors.Is(err, repo.ErrNotFound) { t.Errorf("want ErrNotFound, got %v", err) }
}
```

改写既有 `TestListSessions_TitleFromFirstMainChat`（memory_test.go:81）为 `TestListSessions_TitleFromColumn`：创建会话时 `Title: "列标题"`，再存一条 instruction 为 "你是谁?" 的对话，断言列表 Title 为 "列标题"（证明不再现算）。`TestListSessions_TitleEmptyWithoutChats` 改为创建时 `Title: ""`，断言为空。`search_test.go:55` 的断言改为会话创建时设置的 Title。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/db/ ./internal/repo/memorydb/ 2>&1 | head`
Expected: 编译错误（UpdateTitle 未定义）/ 列序断言失败。

- [ ] **Step 3: 实现**

- 三方言 CREATE TABLE 在 `user_id` 行后插入 `title` 行。
- `repo.Session.Title` 注释更新；`MemoryRepo` 增加 `UpdateTitle` 并注释"只更新 title，不动 updated_at；0 行返回 ErrNotFound"。
- memorydb：`CreateSession` INSERT 加 `title`；`GetSession`/`ListSessions` 的 row 与 SELECT 加 `title`，删除 `ListSessions` 与 `SearchChats` 中的 COALESCE 子查询，改为 `s.title AS title`（SearchChats 已 JOIN `memory_sessions s`）；新增 `UpdateTitle`：`UPDATE memory_sessions SET title=? WHERE session_id=?`，`RowsAffected()==0` 返回 `repo.ErrNotFound`。

- [ ] **Step 4: 运行**

Run: `gofmt -l internal/db internal/repo && go test ./internal/db/... ./internal/repo/... -v 2>&1 | tail -5`
Expected: gofmt 无输出；PASS（`internal/memory` 依赖方此时仍编译通过，因为 repo 接口只是新增方法）。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(repo): 会话标题列与 UpdateTitle`

---

### Task 2: memory 层：CreateSession 带标题、TruncateTitle、RenameSession

**Files:**
- Modify: `internal/memory/manager.go:31-41,52-72`、`internal/memory/memory.go:8`
- Create: `internal/memory/title.go`
- Modify: 调用方 `internal/api/handler/chat.go:242`、`internal/schedule/runner.go:59,140`
- Modify: 所有测试中的 `CreateSession(` 调用（12 个文件，见下）
- Test: `internal/memory/title_test.go`（新建）、`internal/memory/manager_test.go`

**Interfaces:**
- Consumes: Task 1 的 `repo.Session.Title`、`UpdateTitle`。
- Produces:
  - `const MaxTitleRunes = 100`
  - `func TruncateTitle(s string) string` — `TrimSpace` 后按 rune 截到 `MaxTitleRunes`。
  - `(*Manager).CreateSession(sessionID, tenantID, userID, title string) error`
  - `(*Manager).RenameSession(sessionID, title string) error` — 直接透传 repo.UpdateTitle（校验由 handler 做）。
  - `SessionInfo.Title` 由 `GetSessionInfo` 回填（现只在 ListSessions 回填）。
  - `memory.Memory` 接口同步 `CreateSession` 四参、新增 `RenameSession`。

- [ ] **Step 1: 写失败测试**

`title_test.go`：

```go
func TestTruncateTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  hello  ", "hello"},
		{strings.Repeat("a", 100), strings.Repeat("a", 100)},
		{strings.Repeat("a", 101), strings.Repeat("a", 100)},
		{strings.Repeat("中", 100), strings.Repeat("中", 100)},
		{strings.Repeat("中", 101), strings.Repeat("中", 100)},
		{"", ""},
	}
	for _, c := range cases {
		if got := TruncateTitle(c.in); got != c.want { t.Errorf("TruncateTitle(%q)=%q want %q", c.in, got, c.want) }
	}
}
```

`manager_test.go` 追加：

```go
func TestCreateSession_TitleAndRename(t *testing.T) {
	m := newTestManager(t)
	if err := m.CreateSession("s1", "crm", "u1", "初始标题"); err != nil { t.Fatal(err) }
	info, _ := m.GetSessionInfo("s1")
	if info.Title != "初始标题" { t.Errorf("Title=%q", info.Title) }
	if err := m.RenameSession("s1", "新标题"); err != nil { t.Fatal(err) }
	info, _ = m.GetSessionInfo("s1")
	if info.Title != "新标题" { t.Errorf("Title=%q", info.Title) }
	if err := m.RenameSession("missing", "x"); !errors.Is(err, repo.ErrNotFound) { t.Errorf("want ErrNotFound got %v", err) }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/memory/ 2>&1 | head`
Expected: 编译错误。

- [ ] **Step 3: 实现并修复所有调用方**

- `title.go`：常量 + `TruncateTitle`（`[]rune` 截取）。
- `manager.go`：`CreateSession` 四参写 `Title`；`GetSessionInfo` 回填 `Title: s.Title`；新增 `RenameSession`。
- `memory.go` 接口同步。
- `chat.go:242`：`h.memory.CreateSession(sessionID, tenantID, userID, memory.TruncateTitle(req.Instruction))`。
- `runner.go:59,140`：`CreateSession(sessionID, "", "", memory.TruncateTitle(task.TaskDef.Instruction))`。
- 测试调用方：以下文件中所有 `CreateSession(a, b, c)` 加第四参 `""`（除非该测试关心标题）：`internal/memory/{manager,debug2,search,memory}_test.go`、`internal/api/handler/{detail,owner,search,status,stop,session}_test.go`。用 `grep -rn "CreateSession(" internal --include=*_test.go` 核对无遗漏。

- [ ] **Step 4: 运行**

Run: `go build ./... && gofmt -l internal && go test ./internal/... 2>&1 | grep -v "^ok\|no test files"`
Expected: 构建通过；gofmt 只列出 5 个已知的历史未格式化文件或为空；无非 ok 行。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(memory): 会话创建写入标题，支持重命名`

---

### Task 3: /chat 新建会话标题的端到端断言

**Files:**
- Test: `internal/api/handler/chat_test.go`

**Interfaces:**
- Consumes: Task 2。

本任务只加测试，验证 `/chat` 新建会话时标题落库。复用 `TestChatHandler_RegisterStateCleanedOnCreateSessionFailure` 的完整 handler 构造方式，但 memory 的 DB 保持打开；发送 `{"instruction": "<101 个字符的字符串>"}`，web 身份。由于没有可用的模型，`Execute` 会在 goroutine 内失败，但会话在 goroutine 之前已创建。断言：响应 200（SSE 已开始）且 `mem.ListSessions(nil, 10, 0)` 返回 1 条，`Title` 为前 100 个字符。若 SSE 流式响应在测试中无法同步拿到会话（goroutine 时序），改为在 handler 返回后轮询 `mem.ListSessions` 最多 1 秒。

- [ ] **Step 1: 写测试 `TestChatHandler_NewSessionGetsTruncatedTitle`**
- [ ] **Step 2: 运行**

Run: `go test ./internal/api/handler/ -run TestChatHandler_NewSessionGetsTruncatedTitle -v`
Expected: PASS（实现已在 Task 2 完成；若 FAIL 说明 Task 2 的 chat.go 改动有误，修复后再过）。

- [ ] **Step 3: 请求用户提交**

建议 message：`test(chat): 新建会话标题截取断言`

---

### Task 4: PATCH /sess/{sid} 重命名接口

**Files:**
- Modify: `internal/api/handler/session.go`（新增 `RenameSession` 方法；`GetSession` 响应加 `title`）
- Modify: `internal/api/router.go`（`apiGroup.PATCH("/sess/:sid", sessionH.RenameSession)`，放在 DELETE 路由旁）
- Test: `internal/api/handler/session_test.go`

**Interfaces:**
- Consumes: Task 2 `RenameSession`、`MaxTitleRunes`；`requireOwner`、`ownsOrAdmin`、`setIdentity`。
- Produces: `(*SessionHandler).RenameSession(ctx, rc)`；请求体结构 `renameSessionRequest{ Title string \`json:"title"\` }`。

- [ ] **Step 1: 写失败测试**

```go
func newPatchCtx(sid, body string) *app.RequestContext { /* PATCH, JSON content-type, sid param, body */ }

func TestRenameSession_OK(t *testing.T) {
	h, mem, _ := newSearchTestHandler(t); _ = mem.CreateSession("s1", "crm", "u1", "old")
	rc := newPatchCtx("s1", `{"title":"  新标题  "}`); setIdentity(rc, "svc", "crm", "u1")
	h.RenameSession(context.Background(), rc)
	// 200; body 含 "新标题"; mem.GetSessionInfo("s1").Title == "新标题"（已 trim）
}
func TestRenameSession_RoundZeroSession(t *testing.T) { /* 无对话的会话重命名成功；ListSessions 返回新标题 */ }
func TestRenameSession_Validation400(t *testing.T) {
	// 表驱动：`{"title":""}`、`{"title":"   "}`、`{"title":"<101 个中>"}`、`{"title":123}`、`not json`、`{}` → 全部 400；
	// 前三种 body 含 "标题不能为空"，后三种含 "请求参数错误" 或 "标题不能为空"（{} 视为空标题）
	// 并断言标题未变
}
func TestRenameSession_ForeignIs404(t *testing.T) { /* ("erp","u1") → 404 字节一致；标题未变 */ }
func TestRenameSession_MissingIs404(t *testing.T) {}
func TestRenameSession_WebAdminOK(t *testing.T) {}
func TestRenameSession_MissingUser400(t *testing.T) {}
func TestRenameSession_DoesNotTouchUpdatedAt(t *testing.T) { /* 建两个会话 a(早) b(晚)；重命名 a；ListSessions 顺序仍 b, a */ }
func TestGetSession_IncludesTitle(t *testing.T) { /* GET /sess/{sid} 响应 session.title == 创建时标题 */ }
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestRenameSession_|TestGetSession_IncludesTitle' 2>&1 | head`
Expected: 编译错误。

- [ ] **Step 3: 实现**

`RenameSession` 严格按 Global Constraints 的顺序；`rc.BindJSON(&req)` 失败 → 400 "请求参数错误"；`title := strings.TrimSpace(req.Title)`；`utf8.RuneCountInString(title)` 在 1..`memory.MaxTitleRunes` 之外 → 400。`GetSession` 响应 `session` 加 `"title": info.Title`。路由注册。

- [ ] **Step 4: 运行**

Run: `go build ./... && gofmt -l internal/api && go test ./internal/api/... -v 2>&1 | tail -5`
Expected: 通过。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(sess): PATCH /sess/{sid} 重命名会话，详情返回标题`

---

### Task 5: 文档、测试用例与示例客户端

**Files:**
- Modify: `README.md`：7.1 接口总表加 PATCH 行；7.7 `GET /sess/{sid}` 响应示例加 `title`；在 7.7 之后新增 `### 7.7.1 PATCH /sess/{sid} - 重命名会话`（用途、权限 `session`、请求头、请求体、每种响应、标题规则：创建时取首轮指令前 100 个字符、重命名 1..100 字符、不影响排序与最后活跃时间）；7.8/7.9 的 `title` 字段说明改为"会话标题"；错误码附录如有 `invalid_request` 条目补一句。
- Modify: `tests/TEST_CASES.md`：新增 `### 1.13 会话标题测试`（列 Task 1-4 的 Go 测试名，核实存在）；`## 二` 下新增 `### 2.29 会话标题（待运行）`：新建会话后列表标题为指令前 100 字；PATCH 后列表/详情/搜索均显示新标题；PATCH 空标题 400；跨租户 PATCH 404；PATCH 不改变列表顺序。
- Modify: `examples/python/groot_client.py`：增加 `rename_session(self, session_id: str, title: str) -> dict`（PATCH）。
- Modify: `tests/examples/python/test_groot_client.py`：mock 分支与一个测试。
- Modify: `tests/python/*`：若有断言 `GET /sess/history` 返回 `title` 来自首轮指令的用例，更新为"等于会话标题"。

- [ ] **Step 1: README（正面陈述，禁止对比措辞）**
- [ ] **Step 2: TEST_CASES.md**
- [ ] **Step 3: Python 客户端与测试，`python3 -I -m py_compile` 通过**
- [ ] **Step 4: 全量验证**

Run: `go build -o dist/groot ./cmd/groot && go test ./internal/... 2>&1 | tail -3`
Expected: 全 ok。

- [ ] **Step 5: 请求用户提交**

建议 message：`docs: 会话标题与重命名接口说明`
