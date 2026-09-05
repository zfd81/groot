# 会话日志查看功能实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 会话执行链路日志携带 session_id，后端提供按会话查询日志的 Web 端点，前端对话窗口右上角按钮弹窗展示当前会话日志。

**Architecture:** logger 包新增 `With`/`NewContext`/`FromContext` 与包级默认 logger；`Executor.Execute` 入口派生会话级 logger 并传给 Engine/CallAgent；`internal/logger/reader.go` 扫描最近 7 天日志文件过滤 session_id；`GET /web/logs/:sid` 端点返回结构化日志；前端 `LogModal.vue` 弹窗（仿 `SearchModal.vue`）+ ChatView 顶栏按钮。

**Tech Stack:** Go（zap、hertz）、Vue 3 + Element Plus + vue-i18n

**依据设计文档:** `docs/superpowers/specs/2026-09-05-session-log-viewer-design.md`

**注意事项:**
1. **禁止自动 git commit**（项目规范）：每个任务完成后只运行测试并汇报，提交由用户明确指令后统一进行。所以本计划的任务不含 commit 步骤。
2. 设计文档中"client.ts 提供 getSessionLogs 方法"一处，按代码库现有惯例调整为组件内直接调用 `api.get`（`SearchModal.vue` 即此模式，client.ts 只放通用请求封装）。
3. i18n：`useI18n` 在 .vue 中是自动导入的（参见 ChatView.vue 未 import 却直接用），不要手动 import。

---

### Task 1: logger 包 — With / 默认 logger / context 传递

**Files:**
- Modify: `internal/logger/logger.go`
- Create: `internal/logger/context.go`
- Test: `internal/logger/context_test.go`
- Modify: `cmd/groot/main.go:257` 附近（注册默认 logger）

- [ ] **Step 1: 写失败的测试**

创建 `internal/logger/context_test.go`：

```go
package logger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/config"
)

// newFileLogger 创建写入 tmpDir 的 JSON 文件 logger，返回 logger 和日志文件路径
func newFileLogger(t *testing.T, dir string) (*Logger, string) {
	t.Helper()
	cfg := config.LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: []string{"file"},
		File: config.LogFileConfig{
			Directory:       dir,
			FilenamePattern: "test-{date}.log",
		},
	}
	l := New(cfg)
	path := filepath.Join(dir, "test-"+time.Now().Format("2006-01-02")+".log")
	return l, path
}

func TestWith_SessionIDInJSONOutput(t *testing.T) {
	dir := t.TempDir()
	l, path := newFileLogger(t, dir)

	sessionLog := l.With(zap.String("session_id", "sess_test_123"))
	sessionLog.Info("hello with session")
	l.Info("hello without session")
	_ = l.Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("期望 2 行日志，实际 %d 行", len(lines))
	}
	if !strings.Contains(lines[0], `"session_id":"sess_test_123"`) {
		t.Errorf("With 派生的日志应包含 session_id 字段: %s", lines[0])
	}
	if strings.Contains(lines[1], "session_id") {
		t.Errorf("原 logger 的日志不应包含 session_id 字段: %s", lines[1])
	}
}

func TestFromContext_ReturnsStoredLogger(t *testing.T) {
	l := NewNop()
	ctx := NewContext(context.Background(), l)
	if got := FromContext(ctx); got != l {
		t.Errorf("FromContext 应返回 NewContext 放入的 logger")
	}
}

func TestFromContext_FallbackToDefault(t *testing.T) {
	l := NewNop()
	SetDefault(l)
	defer SetDefault(NewNop())
	if got := FromContext(context.Background()); got != l {
		t.Errorf("ctx 中无 logger 时应回退到 SetDefault 设置的默认 logger")
	}
}

func TestFromContext_NeverNil(t *testing.T) {
	if got := FromContext(context.Background()); got == nil {
		t.Fatal("FromContext 任何情况下都不应返回 nil")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/logger/... -run "TestWith_|TestFromContext_" -v`
Expected: 编译失败（`With`、`NewContext`、`FromContext`、`SetDefault` 未定义）

- [ ] **Step 3: 实现**

在 `internal/logger/logger.go` 末尾（`Sync` 方法之后）追加：

```go
// With 派生携带固定字段的子 logger（如 session_id）
func (l *Logger) With(fields ...zap.Field) *Logger {
	return &Logger{
		zap:    l.zap.With(fields...),
		config: l.config,
	}
}
```

创建 `internal/logger/context.go`：

```go
package logger

import (
	"context"
	"sync/atomic"
)

// ctxKey 是 context 中存放 Logger 的私有 key 类型
type ctxKey struct{}

// defaultLogger 包级默认 logger，FromContext 取不到时的回退，
// 未 SetDefault 时为 no-op logger，保证永不返回 nil。
var defaultLogger atomic.Pointer[Logger]

func init() {
	defaultLogger.Store(NewNop())
}

// SetDefault 设置包级默认 logger（应在进程启动创建 logger 后调用一次）
func SetDefault(l *Logger) {
	if l != nil {
		defaultLogger.Store(l)
	}
}

// NewContext 把 logger 放入 context，供执行链路下游取用
func NewContext(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext 从 context 取出 logger；不存在时回退到默认 logger，永不返回 nil
func FromContext(ctx context.Context) *Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*Logger); ok && l != nil {
			return l
		}
	}
	return defaultLogger.Load()
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/logger/... -v`
Expected: 全部 PASS（含原有 logger_test.go）

- [ ] **Step 5: main.go 注册默认 logger**

打开 `cmd/groot/main.go`，找到第 257 行附近的 `log := logger.New(cfg.Logging)`，紧随其后加一行：

```go
	logger.SetDefault(log)
```

- [ ] **Step 6: 编译验证**

Run: `go build ./...`
Expected: 无错误

---

### Task 2: agent 执行链路注入 session_id

**Files:**
- Modify: `internal/agent/executor.go`（仅 `Execute` 方法，约 126-431 行）

说明：`Engine` 和 `CallAgentTool` 都是在 `Execute` 内**按次创建**并通过 config 接收 logger 的，所以只需把会话级 logger 传进去，`engine.go`/`call_agent.go` 无需改动。

- [ ] **Step 1: Execute 入口派生会话级 logger**

在 `internal/agent/executor.go` 的 `Execute` 方法开头（`// Read SESSION.md content` 注释之前）插入：

```go
	// 派生携带 session_id 的会话级 logger，本次执行链路统一使用；
	// 同时放入 ctx，供未直接接收 logger 的下游代码取用。
	sessionLog := e.logger.With(zap.String("session_id", sessionID))
	parentCtx = logger.NewContext(parentCtx, sessionLog)
```

- [ ] **Step 2: 替换 Execute 内的 logger 引用**

把 `Execute` 方法体内所有 `e.logger` 替换为 `sessionLog`，共 9 处：

1. 第 171 行 `e.logger.Error("子 Agent skill 中间件创建失败", ...)`
2. 第 198 行 `CallAgentToolConfig` 的 `Log: e.logger`
3. 第 226 行 `e.logger.Error("保存对话记录失败: " + saveErr.Error())`（soloErr 分支）
4. 第 239 行 `e.logger.Error("追加历史消息失败: " + appendErr.Error())`（soloErr 分支）
5. 第 254 行 `EngineConfig` 的 `Log: e.logger`
6. 第 332 行 `e.logger.Error("Agent execution failed: " + err.Error())`
7. 第 336 行 `e.logger.Error("Failed to write SSE error: " + writeErr.Error())`
8. 第 339 行 `e.logger.Error("Failed to write SSE done: " + writeErr.Error())`
9. 第 402 行与第 425 行的保存/追加失败 Error（正常收尾分支）

注意：`Execute` 之外（如 `NewExecutor`）的 `e.logger` 保持不变。`internal/agent` 包已 import `logger` 与 `zap`，无需新增 import。

- [ ] **Step 3: 编译与既有测试**

Run: `go build ./... && go test ./internal/agent/... ./internal/logger/... ./internal/schedule/...`
Expected: 编译通过，测试全部 PASS

---

### Task 3: 日志读取组件 reader

**Files:**
- Create: `internal/logger/reader.go`
- Test: `internal/logger/reader_test.go`

- [ ] **Step 1: 写失败的测试**

创建 `internal/logger/reader_test.go`：

```go
package logger

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
)

// writeLogFile 在 dir 下按 pattern 写一个指定日期的日志文件
func writeLogFile(t *testing.T, dir, date, content string) {
	t.Helper()
	name := "groot-" + date + ".log"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("写测试日志文件失败: %v", err)
	}
}

func testFileCfg(dir string) config.LogFileConfig {
	return config.LogFileConfig{Directory: dir, FilenamePattern: "groot-{date}.log"}
}

func TestReadSessionLogs_MatchAndSkip(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	writeLogFile(t, dir, today,
		`{"timestamp":"t1","level":"info","message":"m1","caller":"a.go:1","session_id":"sess_a"}`+"\n"+
			`{"timestamp":"t2","level":"error","message":"m2","caller":"a.go:2","session_id":"sess_b"}`+"\n"+
			`不是JSON的坏行`+"\n"+
			`{"timestamp":"t3","level":"warn","message":"m3","caller":"a.go:3","session_id":"sess_a","tool":"web_search"}`+"\n"+
			`{"timestamp":"t4","level":"info","message":"无会话日志"}`+"\n")

	logs, truncated := ReadSessionLogs(testFileCfg(dir), "sess_a", 7, 1000)
	if truncated {
		t.Error("不应截断")
	}
	if len(logs) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条", len(logs))
	}
	if logs[0].Message != "m1" || logs[1].Message != "m3" {
		t.Errorf("应按文件顺序返回 m1、m3，实际 %q、%q", logs[0].Message, logs[1].Message)
	}
	if logs[1].Level != "warn" || logs[1].Caller != "a.go:3" {
		t.Errorf("字段解析错误: %+v", logs[1])
	}
	if v, ok := logs[1].Fields["tool"]; !ok || v != "web_search" {
		t.Errorf("非标准字段应进入 Fields: %+v", logs[1].Fields)
	}
	if logs[0].Fields != nil {
		t.Errorf("无额外字段时 Fields 应为 nil: %+v", logs[0].Fields)
	}
}

func TestReadSessionLogs_MultiDayOrderAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	// 前天的文件缺失：应被跳过而不报错
	writeLogFile(t, dir, yesterday,
		`{"timestamp":"t1","level":"info","message":"昨天","session_id":"s1"}`+"\n")
	writeLogFile(t, dir, today,
		`{"timestamp":"t2","level":"info","message":"今天","session_id":"s1"}`+"\n")

	logs, _ := ReadSessionLogs(testFileCfg(dir), "s1", 7, 1000)
	if len(logs) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条", len(logs))
	}
	if logs[0].Message != "昨天" || logs[1].Message != "今天" {
		t.Errorf("应按日期从旧到新排列: %q, %q", logs[0].Message, logs[1].Message)
	}
}

func TestReadSessionLogs_Truncate(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	content := ""
	for _, m := range []string{"m1", "m2", "m3", "m4", "m5"} {
		content += `{"timestamp":"t","level":"info","message":"` + m + `","session_id":"s1"}` + "\n"
	}
	writeLogFile(t, dir, today, content)

	logs, truncated := ReadSessionLogs(testFileCfg(dir), "s1", 7, 3)
	if !truncated {
		t.Error("超过 limit 应标记 truncated")
	}
	if len(logs) != 3 {
		t.Fatalf("期望 3 条，实际 %d 条", len(logs))
	}
	if logs[0].Message != "m3" || logs[2].Message != "m5" {
		t.Errorf("应保留最新的 3 条 m3..m5，实际 %q..%q", logs[0].Message, logs[2].Message)
	}
}

func TestReadSessionLogs_EmptyCases(t *testing.T) {
	// 目录为空、会话不存在、sessionID 为空，均返回空且不报错
	dir := t.TempDir()
	if logs, _ := ReadSessionLogs(testFileCfg(dir), "nope", 7, 1000); len(logs) != 0 {
		t.Errorf("空目录应返回空列表")
	}
	if logs, _ := ReadSessionLogs(testFileCfg(dir), "", 7, 1000); len(logs) != 0 {
		t.Errorf("空 sessionID 应返回空列表")
	}
	if logs, _ := ReadSessionLogs(config.LogFileConfig{}, "s1", 7, 1000); len(logs) != 0 {
		t.Errorf("空目录配置应返回空列表")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/logger/... -run TestReadSessionLogs -v`
Expected: 编译失败（`ReadSessionLogs`、`LogEntry` 未定义）

- [ ] **Step 3: 实现 reader**

创建 `internal/logger/reader.go`：

```go
package logger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/config"
)

// LogEntry 结构化的会话日志条目
type LogEntry struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Caller    string         `json:"caller"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// standardLogKeys 标准字段集合，其余字段归入 LogEntry.Fields
var standardLogKeys = map[string]bool{
	"timestamp":  true,
	"level":      true,
	"message":    true,
	"caller":     true,
	"session_id": true,
	"logger":     true,
	"stacktrace": true,
}

// ReadSessionLogs 扫描最近 days 天的日志文件，返回指定会话最新的至多 limit 条日志。
// 返回值：日志列表（时间正序）、是否因超过 limit 被截断。
// 文件缺失与 JSON 解析失败的行一律跳过，不视为错误。
func ReadSessionLogs(cfg config.LogFileConfig, sessionID string, days, limit int) ([]LogEntry, bool) {
	if sessionID == "" || cfg.Directory == "" || cfg.FilenamePattern == "" {
		return nil, false
	}

	var entries []LogEntry
	now := time.Now()
	// 从最旧的一天扫到今天，天然保证时间正序
	for i := days - 1; i >= 0; i-- {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		filename := strings.Replace(cfg.FilenamePattern, "{date}", date, 1)
		entries = appendSessionLogsFromFile(entries, filepath.Join(cfg.Directory, filename), sessionID)
	}

	truncated := false
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
		truncated = true
	}
	return entries, truncated
}

// appendSessionLogsFromFile 逐行读取单个日志文件，追加匹配 sessionID 的条目
func appendSessionLogsFromFile(entries []LogEntry, path, sessionID string) []LogEntry {
	f, err := os.Open(path)
	if err != nil {
		return entries // 文件不存在等：跳过
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// 单条日志可能较长（含错误堆栈），放宽单行上限到 1MB
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var raw map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}
		if sid, _ := raw["session_id"].(string); sid != sessionID {
			continue
		}
		entry := LogEntry{}
		entry.Timestamp, _ = raw["timestamp"].(string)
		entry.Level, _ = raw["level"].(string)
		entry.Message, _ = raw["message"].(string)
		entry.Caller, _ = raw["caller"].(string)
		for k, v := range raw {
			if !standardLogKeys[k] {
				if entry.Fields == nil {
					entry.Fields = map[string]any{}
				}
				entry.Fields[k] = v
			}
		}
		entries = append(entries, entry)
	}
	return entries
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/logger/... -v`
Expected: 全部 PASS

---

### Task 4: 查询端点 /web/logs/:sid

**Files:**
- Create: `internal/api/handler/logs.go`
- Modify: `internal/api/server.go`（约 84-102 行，创建 handler 并传参）
- Modify: `internal/api/router.go`（函数签名 + webGroup 注册）

说明：handler 层只有参数校验与响应组装，核心逻辑已由 Task 3 的 reader 单测覆盖；端点行为由 `tests/python/` 系统测试覆盖（用户自行运行），遵循本包既有测试惯例（只测纯函数）。

- [ ] **Step 1: 创建 handler**

创建 `internal/api/handler/logs.go`：

```go
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

const (
	// sessionLogDays 会话日志扫描的天数范围
	sessionLogDays = 7
	// sessionLogLimit 单次返回的日志条数上限
	sessionLogLimit = 1000
)

// LogsHandler 会话日志查询处理器
type LogsHandler struct {
	fileCfg config.LogFileConfig
}

// NewLogsHandler 创建会话日志查询处理器
func NewLogsHandler(cfg config.LoggingConfig) *LogsHandler {
	return &LogsHandler{fileCfg: cfg.File}
}

// Serve 处理 GET /web/logs/:sid
func (h *LogsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")
	if sessionID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"session_id 参数缺失"}`))
		return
	}

	logs, truncated := logger.ReadSessionLogs(h.fileCfg, sessionID, sessionLogDays, sessionLogLimit)
	if logs == nil {
		logs = []logger.LogEntry{} // 会话无日志时返回空数组而非 null
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"count":      len(logs),
		"truncated":  truncated,
		"logs":       logs,
	})
}
```

- [ ] **Step 2: server.go 创建并传入 handler**

在 `internal/api/server.go` 的 handler 创建区（`clusterH := handler.NewClusterHandler(members, log)` 之后）加：

```go
	logsH := handler.NewLogsHandler(cfg.Logging)
```

并把 `RegisterRoutes` 调用改为在末尾多传一个参数：

```go
	RegisterRoutes(h, authMW, rateLimitMW, webStore,
		chatH, statusH, detailH, sessionH,
		healthH, skillsH, agentsH, toolsH, modelsH, scheduleH, webAuthH, apiKeysH, clusterH, logsH)
```

- [ ] **Step 3: router.go 注册路由**

`internal/api/router.go` 的 `RegisterRoutes` 签名末尾加参数：

```go
	clusterH *handler.ClusterHandler,
	logsH *handler.LogsHandler,
) {
```

在 webGroup 注册区（`webGroup.DELETE("/apikeys/:id", apiKeysH.Delete)` 之后）加：

```go
	webGroup.GET("/logs/:sid", logsH.Serve)
```

- [ ] **Step 4: 编译与测试**

Run: `go build ./... && go test ./internal/api/... ./internal/logger/...`
Expected: 编译通过，测试全部 PASS

---

### Task 5: 前端 — 类型、i18n、LogModal 组件

**Files:**
- Modify: `web/src/api/types.ts`（末尾追加）
- Modify: `web/src/i18n/messages/zh-cn.ts`（`search` 段之后追加 `logs` 段）
- Modify: `web/src/i18n/messages/en.ts`（同位置追加）
- Create: `web/src/components/chat/LogModal.vue`

- [ ] **Step 1: types.ts 追加类型**

在 `web/src/api/types.ts` 末尾追加：

```ts
// 会话日志条目（GET /web/logs/:sid）
export interface SessionLogEntry {
  timestamp: string
  level: string
  message: string
  caller: string
  fields?: Record<string, unknown>
}

export interface SessionLogsResp {
  status: string
  session_id: string
  count: number
  truncated: boolean
  logs: SessionLogEntry[]
}
```

- [ ] **Step 2: i18n 文案**

`web/src/i18n/messages/zh-cn.ts`，在 `search: { ... },` 段之后追加：

```ts
  logs: {
    title: '会话日志',
    viewLogs: '查看日志',
    refresh: '刷新',
    all: '全部',
    empty: '暂无日志',
    emptyLevel: '该级别暂无日志',
    failed: '日志加载失败',
    retry: '重试',
    count: '共 {n} 条',
    truncated: '仅展示最新 1000 条',
  },
```

`web/src/i18n/messages/en.ts`，同位置追加：

```ts
  logs: {
    title: 'Session Logs',
    viewLogs: 'View logs',
    refresh: 'Refresh',
    all: 'All',
    empty: 'No logs',
    emptyLevel: 'No logs at this level',
    failed: 'Failed to load logs',
    retry: 'Retry',
    count: '{n} entries',
    truncated: 'Only the latest 1000 entries are shown',
  },
```

- [ ] **Step 3: 创建 LogModal.vue**

创建 `web/src/components/chat/LogModal.vue`（弹窗结构、`requestSeq` 防过期响应模式与 `SearchModal.vue` 一致）：

```vue
<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { Loading, Close, Refresh } from '@element-plus/icons-vue'
import { api } from '../../api/client'
import type { SessionLogEntry, SessionLogsResp } from '../../api/types'

const { t } = useI18n()

const props = defineProps<{ show: boolean; sessionId: string }>()
const emit = defineEmits<{ 'update:show': [v: boolean] }>()

const LEVELS = ['all', 'error', 'warn', 'info', 'debug'] as const
type Level = (typeof LEVELS)[number]

const level = ref<Level>('all')
const loading = ref(false)
const errorMsg = ref('')
const logs = ref<SessionLogEntry[]>([])
const truncated = ref(false)

// 请求序号：关闭弹窗或重新加载时使在途响应失效
let requestSeq = 0

// 打开时重置筛选并加载；关闭时丢弃在途响应
watch(
  () => props.show,
  (v) => {
    if (!v) {
      requestSeq++
      return
    }
    level.value = 'all'
    void load()
  }
)

async function load() {
  const seq = ++requestSeq
  loading.value = true
  errorMsg.value = ''
  try {
    const resp = await api.get<SessionLogsResp>(
      `/web/logs/${encodeURIComponent(props.sessionId)}`
    )
    if (seq !== requestSeq) return
    logs.value = resp.logs || []
    truncated.value = resp.truncated
  } catch {
    if (seq !== requestSeq) return
    errorMsg.value = t('logs.failed')
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

// 级别筛选：纯前端本地过滤
const filtered = computed(() =>
  level.value === 'all' ? logs.value : logs.value.filter((l) => l.level === level.value)
)

function levelLabel(lv: Level): string {
  return lv === 'all' ? t('logs.all') : lv
}

// ISO 时间戳 → HH:mm:ss；解析失败时原样展示
function fmtTime(ts: string): string {
  const d = new Date(ts)
  if (isNaN(d.getTime())) return ts
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
</script>

<template>
  <el-dialog
    :model-value="props.show"
    :show-close="false"
    width="720px"
    top="80px"
    class="logs-dialog"
    @update:model-value="(v: boolean) => emit('update:show', v)"
  >
    <!-- 顶部行：标题 + 级别筛选 + 刷新 + 关闭 -->
    <div class="logs-head">
      <span class="head-title">{{ t('logs.title') }}</span>
      <div class="level-tabs">
        <button
          v-for="lv in LEVELS"
          :key="lv"
          class="level-tab"
          :class="{ active: level === lv, ['lv-' + lv]: true }"
          type="button"
          @click="level = lv"
        >
          {{ levelLabel(lv) }}
        </button>
      </div>
      <button
        class="head-btn"
        type="button"
        :title="t('logs.refresh')"
        :disabled="loading"
        @click="load()"
      >
        <el-icon :size="15"><Refresh /></el-icon>
      </button>
      <div class="head-divider" />
      <button class="head-btn" type="button" @click="emit('update:show', false)">
        <el-icon :size="16"><Close /></el-icon>
      </button>
    </div>

    <!-- 日志列表区 -->
    <div class="log-area">
      <div v-if="loading" class="state-line">
        <el-icon class="is-loading"><Loading /></el-icon>
      </div>
      <div v-else-if="errorMsg" class="state-line">
        {{ errorMsg }}
        <button class="retry-btn" type="button" @click="load()">
          {{ t('logs.retry') }}
        </button>
      </div>
      <div v-else-if="!logs.length" class="state-line">{{ t('logs.empty') }}</div>
      <div v-else-if="!filtered.length" class="state-line">{{ t('logs.emptyLevel') }}</div>
      <template v-else>
        <div
          v-for="(entry, i) in filtered"
          :key="i"
          class="log-row"
          :class="{ 'is-error': entry.level === 'error' }"
        >
          <span class="log-time">{{ fmtTime(entry.timestamp) }}</span>
          <span class="log-level" :class="'lv-' + entry.level">{{
            entry.level.toUpperCase()
          }}</span>
          <span class="log-msg" :title="entry.message">{{ entry.message }}</span>
        </div>
      </template>
    </div>

    <!-- 底部：条数统计与截断提示 -->
    <div class="logs-foot">
      <span>{{ t('logs.count', { n: filtered.length }) }}</span>
      <span v-if="truncated" class="foot-truncated">{{ t('logs.truncated') }}</span>
    </div>
  </el-dialog>
</template>

<style scoped>
.logs-head {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 56px;
  padding: 0 16px 0 20px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}
.head-title {
  font-weight: 600;
  flex-shrink: 0;
}
.level-tabs {
  flex: 1;
  display: flex;
  gap: 4px;
  justify-content: flex-end;
  min-width: 0;
}
.level-tab {
  padding: 3px 10px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font-size: 0.82em;
  font-family: inherit;
  opacity: 0.6;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
}
.level-tab:hover {
  opacity: 0.85;
}
.level-tab.active {
  background: rgba(127, 127, 127, 0.15);
  opacity: 1;
  font-weight: 500;
}
.level-tab.active.lv-error {
  color: var(--el-color-error);
}
.level-tab.active.lv-warn {
  color: var(--el-color-warning);
}
.head-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  opacity: 0.6;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
}
.head-btn:hover:not(:disabled) {
  background: rgba(127, 127, 127, 0.12);
  opacity: 0.9;
}
.head-btn:disabled {
  opacity: 0.3;
  cursor: default;
}
.head-divider {
  flex-shrink: 0;
  width: 1px;
  height: 22px;
  background: rgba(127, 127, 127, 0.3);
}
/* 列表区：与 SearchModal 相同的滚动样式 */
.log-area {
  max-height: calc(100vh - 260px);
  min-height: 120px;
  overflow-y: auto;
  padding: 8px 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.82em;
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker, rgba(127, 127, 127, 0.35)) transparent;
}
.log-area::-webkit-scrollbar {
  width: 6px;
}
.log-area::-webkit-scrollbar-track {
  background: transparent;
}
.log-area::-webkit-scrollbar-thumb {
  background: var(--el-border-color-darker, rgba(127, 127, 127, 0.35));
  border-radius: 3px;
}
.log-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 3px 8px;
  border-radius: 4px;
}
.log-row.is-error {
  background: var(--el-color-error-light-9, rgba(245, 108, 108, 0.1));
}
.log-time {
  flex-shrink: 0;
  opacity: 0.55;
}
.log-level {
  flex-shrink: 0;
  width: 46px;
  font-weight: 600;
}
.log-level.lv-error {
  color: var(--el-color-error);
}
.log-level.lv-warn {
  color: var(--el-color-warning);
}
.log-level.lv-info {
  color: var(--el-color-info, inherit);
  opacity: 0.8;
}
.log-level.lv-debug {
  opacity: 0.5;
}
/* 消息单行省略，hover 由 title 属性展示全文 */
.log-msg {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.state-line {
  text-align: center;
  padding: 24px 0;
  opacity: 0.6;
  font-size: 0.9em;
}
.retry-btn {
  margin-left: 8px;
  padding: 2px 8px;
  border: 1px solid rgba(127, 127, 127, 0.3);
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font-family: inherit;
  cursor: pointer;
}
.logs-foot {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 20px;
  border-top: 1px solid rgba(127, 127, 127, 0.15);
  font-size: 0.78em;
  opacity: 0.6;
}
.foot-truncated {
  color: var(--el-color-warning);
}
</style>

<!-- 与 SearchModal 一致：去默认标题栏与内边距，16px 圆角 -->
<style>
.logs-dialog {
  padding: 0;
  border-radius: 16px;
  overflow: hidden;
}
.logs-dialog .el-dialog__header {
  display: none;
}
.logs-dialog .el-dialog__body {
  padding: 0;
}
</style>
```

- [ ] **Step 4: 前端类型检查**

Run: `cd web && npx vue-tsc --noEmit`（若项目无 vue-tsc 则运行 `npm run build`）
Expected: 无类型错误

---

### Task 6: ChatView 顶栏按钮接入

**Files:**
- Modify: `web/src/views/ChatView.vue`

- [ ] **Step 1: script 部分**

在 `import SearchModal from '../components/chat/SearchModal.vue'`（第 13 行）之后加：

```ts
import LogModal from '../components/chat/LogModal.vue'
import { Document } from '@element-plus/icons-vue'
```

在 `const showSearch = ref(false)`（第 31 行）之后加：

```ts
const showLogs = ref(false)
```

- [ ] **Step 2: template 部分**

把顶部栏（第 146-149 行）改为：

```html
      <!-- 顶部栏：会话标题 + 日志按钮 -->
      <header class="topbar">
        <h1 class="topbar-title">{{ headerTitle }}</h1>
        <button
          class="topbar-logs"
          type="button"
          :disabled="!sessionId"
          :title="t('logs.viewLogs')"
          @click="showLogs = true"
        >
          <el-icon :size="17"><Document /></el-icon>
        </button>
      </header>
```

在 `<SearchModal v-model:show="showSearch" @select="handleSelect" />`（第 175 行）之后加：

```html
  <LogModal v-model:show="showLogs" :session-id="sessionId" />
```

- [ ] **Step 3: style 部分**

在 `.topbar-title` 规则之后追加：

```css
/* 顶栏日志按钮：无会话（未发送消息）时禁用置灰 */
.topbar-logs {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: inherit;
  opacity: 0.65;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
}
.topbar-logs:hover:not(:disabled) {
  background: rgba(127, 127, 127, 0.12);
  opacity: 0.95;
}
.topbar-logs:disabled {
  opacity: 0.25;
  cursor: default;
}
```

- [ ] **Step 4: 前端构建验证**

Run: `cd web && npm run build`
Expected: 构建成功，无类型错误

---

### Task 7: 收尾 — 测试汇总、全量验证

**Files:**
- Modify: `tests/TEST_CASES.md`（追加本功能的测试用例点）

- [ ] **Step 1: 更新测试用例汇总**

在 `tests/TEST_CASES.md` 中按现有格式追加"会话日志查看"小节，列出：

- logger.With 派生日志包含 session_id / 原 logger 不含
- FromContext 取回 / 回退默认 / 永不为 nil
- reader：session 匹配与跳过、坏行跳过、跨天正序、文件缺失跳过、超限截断、空参数返回空
- 端点 `GET /web/logs/:sid`：正常返回、空会话返回 count 0、未登录 401（Python 系统测试覆盖点，用户运行）

- [ ] **Step 2: 全量单元测试**

Run: `go test ./internal/... `
Expected: 全部 PASS

- [ ] **Step 3: 后端编译产物**

Run: `go build -o bin/groot ./cmd/groot`
Expected: 编译成功，产物在 bin/ 目录

- [ ] **Step 4: 汇报**

向用户汇报完成情况（测试结果、改动文件清单），提醒：Python 系统测试（`tests/python/`）建议补充 `/web/logs/:sid` 的端点用例并由用户自行运行；git 提交等待用户明确指令。
