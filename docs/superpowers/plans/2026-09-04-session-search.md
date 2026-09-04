# 会话搜索功能实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Groot Web 添加历史会话全文搜索：侧栏搜索入口 + 搜索弹窗 + 后端 `GET /sess/search` 端点 + 点击结果跳转并定位到匹配轮次。

**Architecture:** 后端沿现有三层结构（repo → memory.Manager → api handler）新增搜索链路，SQL 用通用 LIKE + `ESCAPE '!'` 兼容 SQLite/MySQL/Postgres；前端新增 `SearchModal.vue` 弹窗组件，由 `ChatView.vue` 挂载，侧栏 `SessionSidebar.vue` 提供入口，定位复用现有 `openSession` 流程加 `data-round` 锚点滚动。

**Tech Stack:** Go + Hertz + sqlx（后端）；Vue 3.5 + TypeScript + Element Plus + vue-i18n（前端）。

**规格文档:** `docs/superpowers/specs/2026-09-04-session-search-design.md`

**⚠️ Git 提交规范（覆盖本计划所有 commit 步骤）:** 按项目 CLAUDE.md，**所有 git commit 必须由用户明确请求**。执行本计划时跳过每个任务末尾的 Commit 步骤，全部任务完成后询问用户是否提交；仅当用户明确说"提交"时才执行这些 commit 命令。

---

## 前置知识（给零上下文的工程师）

- **数据模型**：一个会话（`memory_sessions`）含多轮对话（`memory_chats`）。每轮的 `instruction` 是用户指令、`result` 是 AI 回复。`agent_name=''` 表示主 Agent 轮次；非空是子 Agent 内部记录，不对用户展示。会话标题 = 首轮主 Agent 的 `instruction`（动态计算，见 `internal/repo/memorydb/memory.go` 的 `ListSessions`）。
- **测试基建**：`db.Open(nil, t.TempDir())` 打开一个临时 SQLite 库并自动建表（见现有 `internal/repo/memorydb/memory_test.go:14-22`）。`SaveChat` 会自动把主 Agent 轮次的 `round` 从 1 开始递增。
- **Hertz 路由**：静态路由优先级高于命名参数路由，`/sess/search` 与 `/sess/:sid` 可共存（现有 `/sess/history` 就是先例）。
- **前端约定**：Element Plus 组件由 unplugin 自动按需引入（模板里直接写 `<el-dialog>`，无需 import）；`useI18n` 也是自动导入。图标需要显式 `import { X } from '@element-plus/icons-vue'`。前端无单测框架，验证方式是 `npm run build`（含 vue-tsc 类型检查）。
- **运行 Go 测试**：`go test ./internal/xxx/... -v`（在项目根目录）。

---

### Task 1: repo 层 SearchChats（接口 + SQL 实现 + 单测）

**Files:**
- Modify: `internal/repo/memory.go`（加 `SearchHit` 类型与接口方法）
- Modify: `internal/repo/memorydb/memory.go`（加 `escapeLike` 与 `SearchChats` 实现）
- Test: `internal/repo/memorydb/search_test.go`（新建）

- [ ] **Step 1: 在 `internal/repo/memory.go` 定义 SearchHit 并扩展接口**

在 `Error` 结构体之后、`MemoryRepo` 接口之前加入：

```go
// SearchHit 为一条搜索命中的原始数据：命中的轮次及其全文字段。
// 摘要（snippet）截取由上层 memory.Manager 完成，repo 只返回原文。
type SearchHit struct {
	SessionID   string
	ChatID      string
	Round       int
	Title       string // 所属会话标题（首轮主 Agent 指令），无对话记录时为空串
	Instruction string
	Result      string
	StartedAt   time.Time
}
```

在 `MemoryRepo` 接口的 `DeleteSession` 之后加入方法声明：

```go
	// SearchChats 在主 Agent 的已完成轮次（agent_name='' 且 status='completed'）的
	// instruction/result 中模糊匹配 keyword（大小写行为随数据库 collation）。
	// userID 非空时只搜该用户的会话；为空时不按用户过滤（与 ListSessions 行为一致）。
	// 结果按轮次开始时间倒序，最多 limit 条。keyword 原样传入，LIKE 转义由实现负责。
	SearchChats(ctx context.Context, userID, keyword string, limit int) ([]*SearchHit, error)
```

- [ ] **Step 2: 写失败的单测 `internal/repo/memorydb/search_test.go`**

新建文件，完整内容：

```go
package memorydb

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

// newSearchRepo 与 memory_test.go 的 newMemRepo 相同，独立命名避免耦合。
func newSearchRepo(t *testing.T) repo.MemoryRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

// seedSession 建会话；userID 可为空串。
func seedSession(t *testing.T, r repo.MemoryRepo, sid, userID string) {
	t.Helper()
	err := r.CreateSession(context.Background(), &repo.Session{
		SessionID: sid, UserID: userID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateSession %s: %v", sid, err)
	}
}

// seedChat 存一条主 Agent 轮次（round 由 SaveChat 自动递增）。
func seedChat(t *testing.T, r repo.MemoryRepo, sid, chatID, instruction, result string, startedAt time.Time) {
	t.Helper()
	err := r.SaveChat(context.Background(), &repo.ChatRecord{
		ChatID: chatID, SessionID: sid,
		Instruction: instruction, Result: result,
		Status: "completed", StartedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("SaveChat %s: %v", chatID, err)
	}
}

func TestSearchChats_MatchInstructionAndResult(t *testing.T) {
	r := newSearchRepo(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	seedSession(t, r, "s1", "")
	seedChat(t, r, "s1", "c1", "怎么写快速排序", "快排代码如下……", base)
	seedChat(t, r, "s1", "c2", "换个话题", "冒泡排序更简单", base.Add(time.Minute))
	seedChat(t, r, "s1", "c3", "今天天气如何", "晴天", base.Add(2*time.Minute))

	hits, err := r.SearchChats(ctx, "", "排序", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	// c1 命中 instruction，c2 命中 result；c3 不命中。按 started_at 倒序：c2 在前。
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].ChatID != "c2" || hits[1].ChatID != "c1" {
		t.Errorf("expected order [c2 c1], got [%s %s]", hits[0].ChatID, hits[1].ChatID)
	}
	if hits[0].Title != "怎么写快速排序" {
		t.Errorf("expected title from first main chat, got %q", hits[0].Title)
	}
	if hits[1].Round != 1 || hits[0].Round != 2 {
		t.Errorf("unexpected rounds: c1=%d c2=%d", hits[1].Round, hits[0].Round)
	}
}

func TestSearchChats_ExcludesSubAgentAndUncompleted(t *testing.T) {
	r := newSearchRepo(t)
	ctx := context.Background()
	seedSession(t, r, "s2", "")
	// 主 Agent 轮次（占 round 1），不含关键词
	seedChat(t, r, "s2", "m1", "无关内容", "无关回复", time.Now())
	// 子 Agent 记录：含关键词但不应命中
	if err := r.SaveChat(ctx, &repo.ChatRecord{
		ChatID: "sub1", SessionID: "s2", AgentName: "weather", Round: 1,
		Instruction: "特殊关键词xyz", Result: "", Status: "completed", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveChat sub: %v", err)
	}
	// 失败状态的主 Agent 轮次：含关键词但不应命中
	if err := r.SaveChat(ctx, &repo.ChatRecord{
		ChatID: "f1", SessionID: "s2",
		Instruction: "特殊关键词xyz", Result: "", Status: "failed", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveChat failed-status: %v", err)
	}

	hits, err := r.SearchChats(ctx, "", "特殊关键词xyz", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits, got %d", len(hits))
	}
}

func TestSearchChats_EscapesLikeSpecials(t *testing.T) {
	r := newSearchRepo(t)
	ctx := context.Background()
	seedSession(t, r, "s3", "")
	seedChat(t, r, "s3", "e1", "进度是100%完成", "", time.Now())
	seedChat(t, r, "s3", "e2", "进度是100X完成", "", time.Now().Add(time.Second))
	seedChat(t, r, "s3", "e3", "变量 a_b 的含义", "", time.Now().Add(2*time.Second))
	seedChat(t, r, "s3", "e4", "变量 aXb 的含义", "", time.Now().Add(3*time.Second))

	// "100%" 若不转义，% 会通配匹配到 e2
	hits, err := r.SearchChats(ctx, "", "100%", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 1 || hits[0].ChatID != "e1" {
		t.Fatalf("expected only e1, got %d hits", len(hits))
	}
	// "a_b" 若不转义，_ 会通配匹配到 e4
	hits, err = r.SearchChats(ctx, "", "a_b", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 1 || hits[0].ChatID != "e3" {
		t.Fatalf("expected only e3, got %d hits", len(hits))
	}
	// 转义符本身：搜 "!" 不应报错、不应误匹配
	seedChat(t, r, "s3", "e5", "感叹号!在这里", "", time.Now().Add(4*time.Second))
	hits, err = r.SearchChats(ctx, "", "号!在", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 1 || hits[0].ChatID != "e5" {
		t.Fatalf("expected only e5, got %d hits", len(hits))
	}
}

func TestSearchChats_UserFilter(t *testing.T) {
	r := newSearchRepo(t)
	ctx := context.Background()
	seedSession(t, r, "u1s", "u1")
	seedSession(t, r, "u2s", "u2")
	seedChat(t, r, "u1s", "uc1", "共同关键词abc", "", time.Now())
	seedChat(t, r, "u2s", "uc2", "共同关键词abc", "", time.Now())

	// userID 非空：只搜该用户
	hits, err := r.SearchChats(ctx, "u1", "共同关键词abc", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "u1s" {
		t.Fatalf("expected only u1s, got %d hits", len(hits))
	}
	// userID 空串：不过滤
	hits, err = r.SearchChats(ctx, "", "共同关键词abc", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits without user filter, got %d", len(hits))
	}
}

func TestSearchChats_Limit(t *testing.T) {
	r := newSearchRepo(t)
	ctx := context.Background()
	seedSession(t, r, "s4", "")
	base := time.Now()
	seedChat(t, r, "s4", "l1", "限流测试词", "", base)
	seedChat(t, r, "s4", "l2", "限流测试词", "", base.Add(time.Second))
	seedChat(t, r, "s4", "l3", "限流测试词", "", base.Add(2*time.Second))

	hits, err := r.SearchChats(ctx, "", "限流测试词", 2)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (limit), got %d", len(hits))
	}
	// 倒序：最新的 l3 在前
	if hits[0].ChatID != "l3" {
		t.Errorf("expected l3 first, got %s", hits[0].ChatID)
	}
}
```

- [ ] **Step 3: 运行单测确认编译失败（方法未实现）**

Run: `go test ./internal/repo/memorydb/... -run TestSearchChats -v`
Expected: 编译错误 —— `*memoryRepo` 未实现 `repo.MemoryRepo`（缺 `SearchChats`）。

- [ ] **Step 4: 在 `internal/repo/memorydb/memory.go` 实现 SearchChats**

import 块加入 `"strings"`。在文件末尾（`DeleteSession` 之后）加入：

```go
// --- Search ---

// escapeLike 转义 LIKE 模式中的特殊字符。
// 用 '!' 作转义符（配合 SQL 的 ESCAPE '!'）：'!' 在 SQLite/MySQL/Postgres 的
// 字符串字面量中都无特殊含义，规避 '\' 在 MySQL 字面量解析中的兼容问题。
func escapeLike(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}

func (r *memoryRepo) SearchChats(ctx context.Context, userID, keyword string, limit int) ([]*repo.SearchHit, error) {
	pattern := "%" + escapeLike(keyword) + "%"
	var rows []struct {
		SessionID   string `db:"session_id"`
		ChatID      string `db:"chat_id"`
		Round       int    `db:"round"`
		Title       string `db:"title"`
		Instruction string `db:"instruction"`
		Result      string `db:"result"`
		StartedAt   int64  `db:"started_at"`
	}
	// title 子查询与 ListSessions 的口径一致：首轮主 Agent 的 instruction。
	// (? = '' OR s.user_id = ?)：userID 为空串时不按用户过滤，与 ListSessions 行为一致。
	q := r.db.Rebind(`SELECT c.session_id, c.chat_id, c.round, c.instruction, c.result, c.started_at,
			COALESCE((SELECT c2.instruction FROM memory_chats c2
				WHERE c2.session_id = c.session_id AND c2.agent_name = ''
				ORDER BY c2.round ASC LIMIT 1), '') AS title
		 FROM memory_chats c
		 JOIN memory_sessions s ON s.session_id = c.session_id
		 WHERE c.agent_name = '' AND c.status = 'completed'
		   AND (? = '' OR s.user_id = ?)
		   AND (c.instruction LIKE ? ESCAPE '!' OR c.result LIKE ? ESCAPE '!')
		 ORDER BY c.started_at DESC
		 LIMIT ?`)
	if err := r.db.SelectContext(ctx, &rows, q, userID, userID, pattern, pattern, limit); err != nil {
		return nil, err
	}
	hits := make([]*repo.SearchHit, len(rows))
	for i, row := range rows {
		hits[i] = &repo.SearchHit{
			SessionID:   row.SessionID,
			ChatID:      row.ChatID,
			Round:       row.Round,
			Title:       row.Title,
			Instruction: row.Instruction,
			Result:      row.Result,
			StartedAt:   time.UnixMilli(row.StartedAt),
		}
	}
	return hits, nil
}
```

- [ ] **Step 5: 运行单测确认通过**

Run: `go test ./internal/repo/memorydb/... -v`
Expected: 全部 PASS（含既有测试）。

- [ ] **Step 6: Commit（⚠️ 仅在用户明确要求提交时执行，否则跳过）**

```bash
git add internal/repo/memory.go internal/repo/memorydb/memory.go internal/repo/memorydb/search_test.go
git commit -m "feat(repo): 增加 SearchChats 会话对话模糊搜索"
```

---

### Task 2: 业务层 Manager.Search（参数校验 + snippet 生成 + 单测）

**Files:**
- Create: `internal/memory/search.go`
- Test: `internal/memory/search_test.go`（新建）

- [ ] **Step 1: 写失败的单测 `internal/memory/search_test.go`**

新建文件，完整内容（`newTestManager` 定义在同包 `memory_test.go:20`，直接复用）：

```go
package memory

import (
	"strings"
	"testing"
	"time"
)

// seedManagerChat 通过 Manager 落一条主 Agent 轮次。
func seedManagerChat(t *testing.T, m *Manager, sid, chatID, instruction, result string) {
	t.Helper()
	if !m.ExistsSession(sid) {
		if err := m.CreateSession(sid, ""); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	err := m.SaveChatRecord(sid, &ChatRecord{
		ChatID: chatID, Instruction: instruction, Result: result,
		Status: "completed", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord: %v", err)
	}
}

func TestSearch_EmptyKeyword(t *testing.T) {
	m := newTestManager(t)
	for _, kw := range []string{"", "   ", "\t\n"} {
		results, err := m.Search("", kw, 20)
		if err != nil {
			t.Fatalf("Search(%q): %v", kw, err)
		}
		if len(results) != 0 {
			t.Errorf("Search(%q): expected empty results, got %d", kw, len(results))
		}
	}
}

func TestSearch_ResultFields(t *testing.T) {
	m := newTestManager(t)
	seedManagerChat(t, m, "sr-1", "c1", "如何部署 groot 服务", "使用 systemd 部署即可")

	results, err := m.Search("", "部署", 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.SessionID != "sr-1" || r.ChatID != "c1" || r.Round != 1 {
		t.Errorf("unexpected identity fields: %+v", r)
	}
	if r.Title != "如何部署 groot 服务" {
		t.Errorf("unexpected title: %q", r.Title)
	}
	// instruction 与 result 都含关键词时优先 instruction
	if r.MatchedField != "instruction" {
		t.Errorf("expected matched_field=instruction, got %q", r.MatchedField)
	}
	if !strings.Contains(r.Snippet, "部署") {
		t.Errorf("snippet should contain keyword, got %q", r.Snippet)
	}
	if r.Timestamp <= 0 {
		t.Errorf("expected positive timestamp, got %d", r.Timestamp)
	}
}

func TestSearch_MatchedFieldResult(t *testing.T) {
	m := newTestManager(t)
	seedManagerChat(t, m, "sr-2", "c2", "随便聊聊", "冒泡排序是最基础的排序算法")

	results, err := m.Search("", "冒泡", 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].MatchedField != "result" {
		t.Errorf("expected matched_field=result, got %q", results[0].MatchedField)
	}
}

func TestSearch_LimitFallback(t *testing.T) {
	m := newTestManager(t)
	seedManagerChat(t, m, "sr-3", "c3", "限值回退测试", "")

	// limit<=0 与超上限都不报错，正常返回
	for _, limit := range []int{0, -1, 999} {
		results, err := m.Search("", "限值回退", limit)
		if err != nil {
			t.Fatalf("Search(limit=%d): %v", limit, err)
		}
		if len(results) != 1 {
			t.Errorf("Search(limit=%d): expected 1 result, got %d", limit, len(results))
		}
	}
}

func TestMakeSnippet(t *testing.T) {
	// 中文长文本：关键词前 20、后 60 rune，两端补省略号
	prefix := strings.Repeat("前", 30)
	suffix := strings.Repeat("后", 80)
	text := prefix + "目标词" + suffix
	s, ok := makeSnippet(text, "目标词")
	if !ok {
		t.Fatal("expected found")
	}
	if !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") {
		t.Errorf("expected ellipsis on both ends: %q", s)
	}
	if !strings.Contains(s, "目标词") {
		t.Errorf("snippet must contain keyword: %q", s)
	}
	// rune 计数：省略号2 + 前20 + 关键词3 + 后60 = 85
	if n := len([]rune(s)); n != 85 {
		t.Errorf("expected 85 runes, got %d (%q)", n, s)
	}

	// 关键词在开头：无前置省略号
	s, ok = makeSnippet("目标词开头的短句", "目标词")
	if !ok {
		t.Fatal("expected found")
	}
	if strings.HasPrefix(s, "…") {
		t.Errorf("no leading ellipsis expected: %q", s)
	}
	if strings.HasSuffix(s, "…") {
		t.Errorf("no trailing ellipsis for short text: %q", s)
	}

	// 大小写不敏感
	if _, ok = makeSnippet("Hello World", "world"); !ok {
		t.Error("expected case-insensitive match")
	}

	// 找不到
	if _, ok = makeSnippet("abc", "xyz"); ok {
		t.Error("expected not found")
	}
}
```

- [ ] **Step 2: 运行单测确认失败**

Run: `go test ./internal/memory/... -run 'TestSearch|TestMakeSnippet' -v`
Expected: 编译错误 —— `m.Search`、`makeSnippet` 未定义。

- [ ] **Step 3: 实现 `internal/memory/search.go`**

新建文件，完整内容：

```go
package memory

import (
	"context"
	"fmt"
	"strings"
)

// SearchResult 一条搜索结果（轮次级），字段直接对应 /sess/search 响应。
type SearchResult struct {
	SessionID    string `json:"session_id"`
	ChatID       string `json:"chat_id"`
	Round        int    `json:"round"`
	Title        string `json:"title"`
	Snippet      string `json:"snippet"`
	MatchedField string `json:"matched_field"` // instruction | result
	Timestamp    int64  `json:"timestamp"`     // 轮次开始时间（毫秒）
}

const (
	searchDefaultLimit = 20
	searchMaxLimit     = 50
	snippetBefore      = 20 // 关键词前保留的 rune 数
	snippetAfter       = 60 // 关键词后保留的 rune 数
)

// Search 在历史对话（主 Agent 已完成轮次）中模糊搜索 keyword。
// userID 非空时只搜该用户的会话；keyword 去除首尾空白后为空返回空结果；
// limit 非正数回退默认值，超上限时封顶。
func (m *Manager) Search(userID, keyword string, limit int) ([]SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []SearchResult{}, nil
	}
	if limit <= 0 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}
	hits, err := m.repo.SearchChats(context.Background(), userID, keyword, limit)
	if err != nil {
		return nil, fmt.Errorf("搜索对话失败: %w", err)
	}
	results := make([]SearchResult, 0, len(hits))
	for _, h := range hits {
		snippet, field := pickSnippet(h.Instruction, h.Result, keyword)
		results = append(results, SearchResult{
			SessionID:    h.SessionID,
			ChatID:       h.ChatID,
			Round:        h.Round,
			Title:        h.Title,
			Snippet:      snippet,
			MatchedField: field,
			Timestamp:    h.StartedAt.UnixMilli(),
		})
	}
	return results, nil
}

// pickSnippet 依次尝试 instruction、result，返回首个能定位到 keyword 的摘要。
// 两者都定位不到（数据库 LIKE 与 Go 大小写折叠规则不一致的罕见情形）时，
// 回退为 instruction 开头截取。
func pickSnippet(instruction, result, keyword string) (snippet, field string) {
	if s, ok := makeSnippet(instruction, keyword); ok {
		return s, "instruction"
	}
	if s, ok := makeSnippet(result, keyword); ok {
		return s, "result"
	}
	runes := []rune(instruction)
	end := snippetBefore + snippetAfter
	if end > len(runes) {
		end = len(runes)
	}
	s := string(runes[:end])
	if end < len(runes) {
		s += "…"
	}
	return s, "instruction"
}

// makeSnippet 在 text 中大小写不敏感地定位 keyword 首次出现的位置，
// 截取其前约 snippetBefore、后约 snippetAfter 个字符（按 rune，UTF-8 安全），
// 两端被截断时补省略号。定位不到时 ok=false。
func makeSnippet(text, keyword string) (snippet string, ok bool) {
	byteIdx := strings.Index(strings.ToLower(text), strings.ToLower(keyword))
	if byteIdx < 0 {
		return "", false
	}
	// ASCII 与 CJK 的小写折叠不改变字节长度，byteIdx 可直接用于原文；
	// 个别字符折叠后长度变化会导致轻微偏移，snippet 场景可接受。
	if byteIdx > len(text) {
		byteIdx = len(text)
	}
	runeIdx := len([]rune(text[:byteIdx]))
	kwLen := len([]rune(keyword))
	runes := []rune(text)
	start := runeIdx - snippetBefore
	if start < 0 {
		start = 0
	}
	end := runeIdx + kwLen + snippetAfter
	if end > len(runes) {
		end = len(runes)
	}
	if start > end { // 防御折叠偏移导致的越界
		start = 0
	}
	s := string(runes[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(runes) {
		s += "…"
	}
	return s, true
}
```

- [ ] **Step 4: 运行单测确认通过**

Run: `go test ./internal/memory/... -v`
Expected: 全部 PASS（含既有测试）。

- [ ] **Step 5: Commit（⚠️ 仅在用户明确要求提交时执行，否则跳过）**

```bash
git add internal/memory/search.go internal/memory/search_test.go
git commit -m "feat(memory): Manager.Search 搜索业务逻辑与摘要截取"
```

---

### Task 3: API 层 SearchSessions handler + 路由注册

**Files:**
- Create: `internal/api/handler/search.go`（`SessionHandler` 的新方法，不新增 handler 结构体，无需改 `server.go` 装配）
- Modify: `internal/api/router.go:71-72`（注册 `/sess/search`）
- Test: `internal/api/handler/search_test.go`（新建）

- [ ] **Step 1: 写失败的单测 `internal/api/handler/search_test.go`**

新建文件，完整内容（参考 `status_test.go` 的 RequestContext 构造方式）：

```go
package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo/memorydb"
)

// newSearchTestHandler 构造挂了临时 SQLite 库的 SessionHandler。
func newSearchTestHandler(t *testing.T) (*SessionHandler, *memory.Manager) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	mem := memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
	return NewSessionHandler(mem), mem
}

// newSearchTestContext 构造 GET /sess/search 的 RequestContext（q 已 URL 编码由 hertz 处理，
// 这里直接设置原始 query 串即可）。
func newSearchTestContext(rawQuery string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/sess/search?" + rawQuery)
	return rc
}

func TestSearchSessions_OK(t *testing.T) {
	h, mem := newSearchTestHandler(t)
	if err := mem.CreateSession("hs-1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err := mem.SaveChatRecord("hs-1", &memory.ChatRecord{
		ChatID: "hc-1", Instruction: "查询订单状态", Result: "订单已发货",
		Status: "completed", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord: %v", err)
	}

	rc := newSearchTestContext("q=%E8%AE%A2%E5%8D%95") // q=订单
	h.SearchSessions(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var body struct {
		Status  string                `json:"status"`
		Results []memory.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if body.Status != "success" {
		t.Errorf("expected status=success, got %q", body.Status)
	}
	if len(body.Results) != 1 || body.Results[0].ChatID != "hc-1" {
		t.Fatalf("unexpected results: %+v", body.Results)
	}
}

func TestSearchSessions_EmptyQuery(t *testing.T) {
	h, _ := newSearchTestHandler(t)
	for _, raw := range []string{"q=", "q=%20%20", ""} {
		rc := newSearchTestContext(raw)
		h.SearchSessions(context.Background(), rc)
		if rc.Response.StatusCode() != 200 {
			t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
		}
		var body struct {
			Status  string                `json:"status"`
			Results []memory.SearchResult `json:"results"`
		}
		if err := json.Unmarshal(rc.Response.Body(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body.Status != "success" || len(body.Results) != 0 {
			t.Errorf("raw=%q: expected empty success, got %s", raw, rc.Response.Body())
		}
	}
}
```

- [ ] **Step 2: 运行单测确认失败**

Run: `go test ./internal/api/handler/... -run TestSearchSessions -v`
Expected: 编译错误 —— `h.SearchSessions` 未定义。

- [ ] **Step 3: 实现 `internal/api/handler/search.go`**

新建文件，完整内容：

```go
package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/memory"
)

// SearchSessions 处理 GET /sess/search?q=<关键词>&limit=20
// 在历史对话（主 Agent 已完成轮次）的 instruction/result 中模糊搜索，返回轮次级结果。
func (h *SessionHandler) SearchSessions(ctx context.Context, rc *app.RequestContext) {
	q := strings.TrimSpace(rc.Query("q"))

	limit := 20
	if l := rc.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	// q 为空直接返回空结果（不视为错误）
	if q == "" {
		rc.JSON(200, utils.H{"status": "success", "results": []memory.SearchResult{}})
		return
	}

	// 与 /chat 端点一致：调用方可用 X-User-ID 标识用户；为空时不按用户过滤
	userID := string(rc.GetHeader("X-User-ID"))

	results, err := h.memory.Search(userID, q, limit)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"搜索失败"}`))
		return
	}
	rc.JSON(200, utils.H{"status": "success", "results": results})
}
```

- [ ] **Step 4: 注册路由 `internal/api/router.go`**

把：

```go
	// Session endpoints - 会话管理
	apiGroup.GET("/sess/:sid", sessionH.GetSession)
	apiGroup.GET("/sess/history", sessionH.ListSessions)
```

改为：

```go
	// Session endpoints - 会话管理
	// 静态路由（/sess/history、/sess/search）优先级高于命名参数路由（/sess/:sid），可共存
	apiGroup.GET("/sess/:sid", sessionH.GetSession)
	apiGroup.GET("/sess/history", sessionH.ListSessions)
	apiGroup.GET("/sess/search", sessionH.SearchSessions)
```

- [ ] **Step 5: 运行单测确认通过**

Run: `go test ./internal/api/... -v`
Expected: 全部 PASS（含既有 handler 测试）。

- [ ] **Step 6: Commit（⚠️ 仅在用户明确要求提交时执行，否则跳过）**

```bash
git add internal/api/handler/search.go internal/api/handler/search_test.go internal/api/router.go
git commit -m "feat(api): GET /sess/search 会话搜索端点"
```

---

### Task 4: 前端类型定义与 i18n 文案

**Files:**
- Modify: `web/src/api/types.ts`（文件末尾追加）
- Modify: `web/src/i18n/messages/zh-cn.ts`（`sidebar` 加一键；新增 `search` 命名空间）
- Modify: `web/src/i18n/messages/en.ts`（同上，key 一一对应）

- [ ] **Step 1: `web/src/api/types.ts` 末尾追加**

```ts
// 会话搜索（/sess/search）：轮次级命中结果
export interface SearchResultItem {
  session_id: string
  chat_id: string
  round: number
  title: string
  snippet: string
  matched_field: 'instruction' | 'result'
  timestamp: number // 轮次开始时间（毫秒）
}

export interface SearchResp {
  status: string
  results: SearchResultItem[]
}
```

- [ ] **Step 2: `zh-cn.ts` 修改**

`sidebar` 对象内（`settings: '设置',` 之后）加：

```ts
    search: '搜索会话',
```

顶层（`sidebar` 对象之后）加新命名空间：

```ts
  search: {
    placeholder: '搜索…',
    recent: '最近会话',
    noResults: '未找到相关话题',
    failed: '搜索失败，请重试',
    matchInstruction: '我的提问',
    matchResult: 'AI 回复',
  },
```

- [ ] **Step 3: `en.ts` 对应修改**

`sidebar` 对象内加：

```ts
    search: 'Search chats',
```

顶层加：

```ts
  search: {
    placeholder: 'Search…',
    recent: 'Recent chats',
    noResults: 'No matching topics',
    failed: 'Search failed, please retry',
    matchInstruction: 'My question',
    matchResult: 'AI reply',
  },
```

- [ ] **Step 4: 类型检查验证**

Run: `cd web && npm run build`
Expected: 构建成功，无类型错误。

- [ ] **Step 5: Commit（⚠️ 仅在用户明确要求提交时执行，否则跳过）**

```bash
git add web/src/api/types.ts web/src/i18n/messages/zh-cn.ts web/src/i18n/messages/en.ts
git commit -m "feat(web): 搜索功能的类型定义与 i18n 文案"
```

---

### Task 5: 侧栏搜索入口（SessionSidebar.vue）

**Files:**
- Modify: `web/src/components/chat/SessionSidebar.vue`

入口位置（已确认的设计）：
- **展开态**：品牌区内、折叠按钮左侧。
- **收起态**：窄栏自上而下依次为 logo → 展开 → **搜索** → 新建会话 →（底部）设置。

- [ ] **Step 1: script 部分**

图标 import 行改为：

```ts
import { CirclePlus, Setting, Loading, Search } from '@element-plus/icons-vue'
```

`defineEmits` 增加一个事件（`collapse: []` 之后）：

```ts
  openSearch: []
```

- [ ] **Step 2: 收起态模板 —— 在展开按钮与新建会话按钮之间插入搜索按钮**

在 rail 中 `t('sidebar.expand')` 按钮的 `</button>` 与 `t('sidebar.newChat')` 按钮之间插入：

```html
    <button class="rail-btn" type="button" :title="t('sidebar.search')" @click="emit('openSearch')">
      <el-icon :size="20"><Search /></el-icon>
    </button>
```

- [ ] **Step 3: 展开态模板 —— 品牌区折叠按钮之前插入搜索按钮**

在 `.brand` 内 `<span class="brand-badge">AGENT</span>` 与 `class="collapse-btn"` 按钮之间插入：

```html
      <button
        class="search-btn"
        type="button"
        :title="t('sidebar.search')"
        @click="emit('openSearch')"
      >
        <el-icon :size="20"><Search /></el-icon>
      </button>
```

- [ ] **Step 4: 样式调整**

`.collapse-btn` 规则中**删除** `margin-left: auto;`（右推职责移交给左侧的搜索按钮，否则两个 auto margin 会平分空隙）。在 `.collapse-btn` 规则之前新增：

```css
.search-btn {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  color: inherit;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
}
.search-btn:hover {
  background: rgba(127, 127, 127, 0.1);
}
```

- [ ] **Step 5: 类型检查验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 6: Commit（⚠️ 仅在用户明确要求提交时执行，否则跳过）**

```bash
git add web/src/components/chat/SessionSidebar.vue
git commit -m "feat(web): 侧栏展开/收起态增加搜索入口"
```

---

### Task 6: 搜索弹窗组件 SearchModal.vue

**Files:**
- Create: `web/src/components/chat/SearchModal.vue`

- [ ] **Step 1: 新建组件，完整内容**

```vue
<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { Search, Loading } from '@element-plus/icons-vue'
import { api } from '../../api/client'
import type {
  SearchResp,
  SearchResultItem,
  SessionHistoryResp,
  SessionSummary,
} from '../../api/types'

const { t } = useI18n()

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  'update:show': [v: boolean]
  // 选中一条结果：round 缺省表示从「最近会话」进入，不做轮次定位
  select: [sid: string, round?: number]
}>()

const keyword = ref('')
const loading = ref(false)
const errorMsg = ref('')
const results = ref<SearchResultItem[]>([])
const recent = ref<SessionSummary[]>([])
const activeIndex = ref(0)
const inputRef = ref<{ focus: () => void } | null>(null)

let debounceTimer: ReturnType<typeof setTimeout> | null = null
// 请求序号：响应回来时序号已变说明输入又变了，丢弃过期响应
let requestSeq = 0

// 打开时重置状态、拉最近会话、聚焦输入框
watch(
  () => props.show,
  async (v) => {
    if (!v) return
    keyword.value = ''
    results.value = []
    errorMsg.value = ''
    activeIndex.value = 0
    void loadRecent()
    await nextTick()
    inputRef.value?.focus()
  }
)

// 输入 300ms 防抖触发搜索；清空则回到最近会话视图
watch(keyword, (kw) => {
  if (debounceTimer) clearTimeout(debounceTimer)
  errorMsg.value = ''
  activeIndex.value = 0
  const q = kw.trim()
  if (!q) {
    results.value = []
    loading.value = false
    return
  }
  loading.value = true
  debounceTimer = setTimeout(() => void doSearch(q), 300)
})

async function loadRecent() {
  const seq = ++requestSeq
  loading.value = true
  try {
    const resp = await api.get<SessionHistoryResp>('/sess/history?limit=20')
    if (seq !== requestSeq) return
    recent.value = resp.sessions || []
  } catch {
    if (seq !== requestSeq) return
    errorMsg.value = t('search.failed')
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

async function doSearch(q: string) {
  const seq = ++requestSeq
  try {
    const resp = await api.get<SearchResp>(
      `/sess/search?q=${encodeURIComponent(q)}&limit=20`
    )
    if (seq !== requestSeq) return
    results.value = resp.results || []
  } catch {
    if (seq !== requestSeq) return
    errorMsg.value = t('search.failed')
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

// 当前列表项总数（空输入=最近会话，非空=搜索结果）
function itemCount(): number {
  return keyword.value.trim() ? results.value.length : recent.value.length
}

function pickRecent(s: SessionSummary) {
  emit('update:show', false)
  emit('select', s.session_id)
}

function pickResult(r: SearchResultItem) {
  emit('update:show', false)
  emit('select', r.session_id, r.round)
}

function onKeydown(e: KeyboardEvent) {
  const n = itemCount()
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    if (n) activeIndex.value = (activeIndex.value + 1) % n
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    if (n) activeIndex.value = (activeIndex.value - 1 + n) % n
  } else if (e.key === 'Enter') {
    e.preventDefault()
    if (!n) return
    if (keyword.value.trim()) pickResult(results.value[activeIndex.value])
    else pickRecent(recent.value[activeIndex.value])
  }
}

// 关键词高亮：按 keyword（大小写不敏感）把 snippet 切成命中/未命中分段，
// 用文本节点渲染，不用 v-html，避免 XSS。
function segments(text: string): { text: string; hit: boolean }[] {
  const kw = keyword.value.trim()
  if (!kw) return [{ text, hit: false }]
  const out: { text: string; hit: boolean }[] = []
  const lower = text.toLowerCase()
  const k = kw.toLowerCase()
  let pos = 0
  for (;;) {
    const idx = lower.indexOf(k, pos)
    if (idx < 0) {
      if (pos < text.length) out.push({ text: text.slice(pos), hit: false })
      break
    }
    if (idx > pos) out.push({ text: text.slice(pos, idx), hit: false })
    out.push({ text: text.slice(idx, idx + kw.length), hit: true })
    pos = idx + kw.length
  }
  return out
}

function fmtDate(ms: number): string {
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

function recentTime(s: SessionSummary): string {
  const iso = s.last_active_at || s.created_at
  const ts = new Date(iso).getTime()
  return isNaN(ts) ? '' : fmtDate(ts)
}

function recentTitle(s: SessionSummary): string {
  return s.title?.trim() || s.session_id.slice(0, 8)
}
</script>

<template>
  <el-dialog
    :model-value="props.show"
    :show-close="false"
    width="560px"
    top="12vh"
    @update:model-value="(v: boolean) => emit('update:show', v)"
  >
    <div @keydown="onKeydown">
      <el-input
        ref="inputRef"
        v-model="keyword"
        :placeholder="t('search.placeholder')"
        :prefix-icon="Search"
        clearable
        size="large"
      />
      <div class="result-area">
        <div v-if="errorMsg" class="state-line">{{ errorMsg }}</div>
        <div v-else-if="loading" class="state-line">
          <el-icon class="is-loading"><Loading /></el-icon>
        </div>
        <!-- 空输入：最近会话列表 -->
        <template v-else-if="!keyword.trim()">
          <div class="group-label">{{ t('search.recent') }}</div>
          <div
            v-for="(s, i) in recent"
            :key="s.session_id"
            class="item"
            :class="{ active: i === activeIndex }"
            @mouseenter="activeIndex = i"
            @click="pickRecent(s)"
          >
            <div class="item-title">{{ recentTitle(s) }}</div>
            <div class="item-time">{{ recentTime(s) }}</div>
          </div>
          <div v-if="!recent.length" class="state-line">{{ t('sidebar.empty') }}</div>
        </template>
        <!-- 有输入：轮次级搜索结果 -->
        <template v-else>
          <div
            v-for="(r, i) in results"
            :key="r.chat_id"
            class="item"
            :class="{ active: i === activeIndex }"
            @mouseenter="activeIndex = i"
            @click="pickResult(r)"
          >
            <div class="item-title">{{ r.title?.trim() || r.session_id.slice(0, 8) }}</div>
            <div class="item-snippet">
              <span class="match-tag">{{
                r.matched_field === 'result'
                  ? t('search.matchResult')
                  : t('search.matchInstruction')
              }}</span>
              <span
                v-for="(seg, si) in segments(r.snippet)"
                :key="si"
                :class="{ hl: seg.hit }"
                >{{ seg.text }}</span
              >
            </div>
            <div class="item-time">{{ fmtDate(r.timestamp) }}</div>
          </div>
          <div v-if="!results.length" class="state-line">{{ t('search.noResults') }}</div>
        </template>
      </div>
    </div>
  </el-dialog>
</template>

<style scoped>
.result-area {
  margin-top: 12px;
  max-height: 50vh;
  overflow-y: auto;
}
.group-label {
  font-size: 0.78em;
  opacity: 0.6;
  padding: 4px 8px;
}
.item {
  padding: 8px 10px;
  border-radius: 8px;
  cursor: pointer;
}
.item.active {
  background: rgba(127, 127, 127, 0.12);
}
.item-title {
  font-size: 0.92em;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.item-snippet {
  font-size: 0.82em;
  opacity: 0.75;
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.item-snippet .hl {
  color: var(--el-color-primary);
  font-weight: 600;
}
.match-tag {
  display: inline-block;
  font-size: 0.9em;
  padding: 0 4px;
  margin-right: 6px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.15);
}
.item-time {
  font-size: 0.72em;
  opacity: 0.5;
  margin-top: 2px;
}
.state-line {
  text-align: center;
  padding: 16px 0;
  opacity: 0.6;
  font-size: 0.9em;
}
</style>
```

说明：`Esc` 关闭无需代码 —— `el-dialog` 默认 `close-on-press-escape`。

- [ ] **Step 2: 类型检查验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 3: Commit（⚠️ 仅在用户明确要求提交时执行，否则跳过）**

```bash
git add web/src/components/chat/SearchModal.vue
git commit -m "feat(web): 搜索弹窗组件 SearchModal"
```

---

### Task 7: ChatView 接线 + 快捷键 + 跳转定位（MessageList 锚点与高亮）

**Files:**
- Modify: `web/src/views/ChatView.vue`
- Modify: `web/src/components/chat/MessageList.vue`

- [ ] **Step 1: MessageList.vue 加 data-round 锚点与高亮样式**

script 中 `defineProps` 行改为（需要变量名以便计算轮次）：

```ts
const props = defineProps<{ messages: ChatMessage[] }>()

// 第 index 条消息所属轮次 = 到该条为止的用户消息数（消息按 user/assistant 成对排列）
function roundOf(index: number): number {
  let r = 0
  for (let j = 0; j <= index; j++) {
    if (props.messages[j].role === 'user') r++
  }
  return r
}
```

模板中 msg-row 的 div 加 `data-round` 属性（仅用户消息行携带，供搜索跳转定位）：

```html
    <div
      v-for="(m, i) in messages"
      :key="i"
      class="msg-row"
      :class="m.role"
      :data-round="m.role === 'user' ? roundOf(i) : undefined"
    >
```

style 末尾追加（类由 ChatView 通过 DOM 添加；元素属于本组件，scoped 选择器可命中）：

```css
/* 搜索定位高亮：定位到目标轮次后短暂闪烁提示 */
.msg-row.locate-highlight .user-bubble {
  animation: locate-flash 1.5s ease-out;
}
@keyframes locate-flash {
  0% {
    box-shadow: 0 0 0 3px var(--el-color-primary-light-5);
  }
  100% {
    box-shadow: 0 0 0 3px transparent;
  }
}
```

- [ ] **Step 2: ChatView.vue script 修改**

vue import 行加 `onUnmounted`：

```ts
import { ref, onMounted, onUnmounted, watch, nextTick, computed } from 'vue'
```

组件 import 区（`SettingsModal` 之后）加：

```ts
import SearchModal from '../components/chat/SearchModal.vue'
```

`const showSettings = ref(false)` 之后加：

```ts
const showSearch = ref(false)
```

将现有 `handleSelect`（`ChatView.vue:65-70`）替换为：

```ts
async function handleSelect(sid: string, round?: number) {
  if (sid === sessionId.value) {
    // 已在目标会话：只做轮次定位（若有）
    if (round) await locateRound(round)
    return
  }
  await chat.openSession(sid)
  router.replace({ name: 'chat-session', params: { sid } })
  if (round) await locateRound(round)
  else scrollToBottom()
}

// 滚动定位到指定轮次的用户消息并短暂高亮；
// 目标轮次不存在（会话在搜索后被删改）时静默回落到底部。
async function locateRound(round: number) {
  await nextTick()
  const el = scrollArea.value?.querySelector<HTMLElement>(`[data-round="${round}"]`)
  if (!el) {
    scrollToBottom()
    return
  }
  el.scrollIntoView({ block: 'start' })
  el.classList.add('locate-highlight')
  setTimeout(() => el.classList.remove('locate-highlight'), 1600)
}
```

`handleNew` 之后加全局快捷键：

```ts
// Cmd/Ctrl + K 打开搜索弹窗
function onGlobalKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    showSearch.value = true
  }
}
onMounted(() => window.addEventListener('keydown', onGlobalKeydown))
onUnmounted(() => window.removeEventListener('keydown', onGlobalKeydown))
```

（Vue 允许多个 `onMounted`，与现有的 `onMounted(async () => {...})` 并存即可。）

- [ ] **Step 3: ChatView.vue 模板修改**

`<SessionSidebar>` 的事件绑定加一行（`@collapse` 之后）：

```html
        @open-search="showSearch = true"
```

`<SettingsModal v-model:show="showSettings" />` 之后加：

```html
  <SearchModal v-model:show="showSearch" @select="handleSelect" />
```

（`@select` 直接绑 `handleSelect`：事件签名 `[sid: string, round?: number]` 与函数签名一致。）

- [ ] **Step 4: 类型检查验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 5: Commit（⚠️ 仅在用户明确要求提交时执行，否则跳过）**

```bash
git add web/src/views/ChatView.vue web/src/components/chat/MessageList.vue
git commit -m "feat(web): 搜索弹窗接线、Cmd/Ctrl+K 快捷键与轮次定位"
```

---

### Task 8: 整体验证

**Files:** 无新增修改（只运行验证命令）。

- [ ] **Step 1: Go 格式化检查**

Run: `gofmt -l internal/`
Expected: 无输出（所有文件已格式化；有输出则对列出的文件执行 `gofmt -w <file>`）。

- [ ] **Step 2: 运行全部 Go 单元测试**

Run: `go test ./internal/... 2>&1 | tail -30`
Expected: 全部 ok，无 FAIL。

- [ ] **Step 3: 编译后端**

Run: `go build -o bin/groot ./cmd`
Expected: 编译成功，产物在 `bin/groot`。

- [ ] **Step 4: 前端构建**

Run: `cd web && npm run build`
Expected: vue-tsc 类型检查通过 + vite 构建成功。

- [ ] **Step 5: 汇报用户**

告知用户：功能已完成、单测全部通过；系统测试（Python，`tests/python/`）按项目分工由用户自行编写运行；询问是否需要提交代码（此时才可执行各任务中被跳过的 commit 步骤）。

---

## 自检记录

- **规格覆盖**：后端端点/参数/转义/用户过滤/snippet（Task 1-3）；侧栏双入口与位置（Task 5）；弹窗布局/防抖/高亮/键盘/过期响应丢弃（Task 6）；快捷键/跳转定位/高亮/静默降级（Task 7）；i18n（Task 4）；测试要求（Task 1-3, 8）。规格 1.4 排除项（分页/时间分组/FTS5）均未引入。
- **类型一致性**：`SearchHit`（repo）→ `SearchResult`（memory）→ `SearchResultItem`（前端 TS）字段一一对应；`emit('select', sid, round?)` 与 `handleSelect(sid, round?)` 签名一致；i18n key 在 zh/en 与组件用法间一致。
- **无占位符**：所有代码步骤均为完整可用代码。
