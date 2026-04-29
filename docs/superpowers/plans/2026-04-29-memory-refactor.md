# Memory 模块改造实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 Memory 模块三项核心改造（历史窗口截断、原子写入、清理逻辑修正）并清理代码腐化（重复类型、死代码、双重状态管理）。

**Architecture:** 改动集中在三个层——Memory 模块（新增 GetContextMessages、原子写入、crypto/rand）、Agent 模块（删除重复类型和死代码、统一状态管理到 RuntimeState）、API 层（ChatHandler 使用截断上下文、HealthHandler 使用 RuntimeState）。

**Tech Stack:** Go 1.21+、文件系统 JSON 存储

---

## File Structure

### 修改文件

```
internal/config/
├── config.go           # 修改：MemoryConfig 新增 HistoryWindow
├── defaults.go         # 修改：默认值 HistoryWindow: 20

internal/memory/
├── memory.go           # 修改：Memory 接口新增 GetContextMessages
├── manager.go          # 修改：实现 GetContextMessages、原子写入、清理用 created_at
├── idgen.go            # 修改：math/rand → crypto/rand

internal/agent/
├── runtime_state.go    # 修改：删除 Complete()、CancelManager、GenerateTaskID、GenerateStepID
├── executor.go         # 修改：删除 AttachmentPath 类型、runningTasks、IsRunning、RunningCount、重构状态映射
├── engine.go           # 修改：agent.AttachmentPath → memory.AttachmentPath

internal/api/handler/
├── chat.go             # 修改：使用 GetContextMessages 替代 GetHistory 构建上下文
├── health.go           # 修改：Executor.RunningCount() → RuntimeState.RunningCount()

internal/api/
├── server.go           # 修改：HealthHandler 注入 RuntimeState
```

### 删除内容（在同一文件中删除，不新建文件）

- `agent/executor.go`: 删除 `AttachmentPath` 类型、`runningTasks`、`IsRunning()`、`RunningCount()`
- `agent/runtime_state.go`: 删除 `Complete()`、整个 `CancelManager` 类型及方法、`GenerateTaskID()`、`GenerateStepID()`
- `agent/engine.go`: 无删除，仅修改类型引用

---

### Task 1: Config — 新增 HistoryWindow 配置项

**Files:**
- Modify: `internal/config/config.go:60-65`
- Modify: `internal/config/defaults.go:32-36`

- [ ] **Step 1: MemoryConfig 新增 HistoryWindow 字段**

编辑 `internal/config/config.go`，在 MemoryConfig 结构体中新增 `HistoryWindow`：

```go
// MemoryConfig 记忆模块配置
type MemoryConfig struct {
	Directory       string `yaml:"directory"`        // 记忆目录
	RetentionDays   int    `yaml:"retention_days"`   // 保留天数
	CleanupSchedule string `yaml:"cleanup_schedule"` // 清理时间 HH:MM
	HistoryWindow   int    `yaml:"history_window"`   // LLM 上下文窗口（轮次），-1 不限制
}
```

- [ ] **Step 2: DefaultConfig 新增默认值**

编辑 `internal/config/defaults.go`，Memory 配置块加入 `HistoryWindow: 20`：

```go
Memory: MemoryConfig{
	Directory:       "memory",
	RetentionDays:   7,
	CleanupSchedule: "02:00",
	HistoryWindow:   20,
},
```

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go internal/config/defaults.go
git commit -m "feat(config): add history_window to MemoryConfig with default 20"
```

---

### Task 2: Memory 接口 — 新增 GetContextMessages 方法

**Files:**
- Modify: `internal/memory/memory.go:5-32`

- [ ] **Step 1: 接口新增方法**

在 `History 管理` 区块，`GetRoundCount` 之后新增：

```go
// History 管理
AppendMessage(sessionID string, message *Message) error
GetHistory(sessionID string) (*History, error)
GetRoundCount(sessionID string) int
GetContextMessages(sessionID string, windowSize int) ([]Message, error) // 新增
```

- [ ] **Step 2: Commit**

```bash
git add internal/memory/memory.go
git commit -m "feat(memory): add GetContextMessages to Memory interface"
```

---

### Task 3: Manager — 实现核心改造

**Files:**
- Modify: `internal/memory/manager.go`（多步修改）
- Modify: `internal/memory/idgen.go:36-43`

- [ ] **Step 1: idgen.go — math/rand 改为 crypto/rand**

修改 `randomString` 函数，将 `math/rand` 替换为 `crypto/rand`：

```go
import (
	"crypto/rand"
	"fmt"
	"time"
)

// randomString 生成指定长度的随机字符串（小写字母+数字）
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	randBytes := make([]byte, n)
	_, _ = rand.Read(randBytes)
	for i := range b {
		b[i] = letters[int(randBytes[i])%len(letters)]
	}
	return string(b)
}
```

删除文件顶部的 `"math/rand"` import。

- [ ] **Step 2: manager.go — 新增 GetContextMessages 方法**

在 `GetRoundCount` 方法之后新增：

```go
// GetContextMessages 返回用于 LLM 上下文构建的历史消息（截断后）
// windowSize: 保留最近 N 轮，<= 0 表示不限制
func (m *Manager) GetContextMessages(sessionID string, windowSize int) ([]Message, error) {
	history, err := m.GetHistory(sessionID)
	if err != nil {
		return nil, err
	}

	if windowSize <= 0 || len(history.Messages) <= windowSize {
		return history.Messages, nil
	}

	return history.Messages[len(history.Messages)-windowSize:], nil
}
```

- [ ] **Step 3: manager.go — saveHistory 改为原子写入**

修改 `saveHistory` 方法：

```go
// saveHistory 保存 history.json（原子写入：tmp + rename）
func (m *Manager) saveHistory(sessionID string, history *History) error {
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 history 失败: %w", err)
	}

	tmpPath := m.historyPath(sessionID) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}

	return os.Rename(tmpPath, m.historyPath(sessionID))
}
```

- [ ] **Step 4: manager.go — SaveChatRecord 改为原子写入**

修改 `SaveChatRecord` 方法：

```go
// SaveChatRecord 保存详细对话记录（原子写入：tmp + rename）
func (m *Manager) SaveChatRecord(sessionID string, record *ChatRecord) error {
	// 确保 chats 目录存在
	os.MkdirAll(m.chatsDir(sessionID), 0755)

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 chat record 失败: %w", err)
	}

	tmpPath := m.chatPath(sessionID, record.ChatID) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}

	return os.Rename(tmpPath, m.chatPath(sessionID, record.ChatID))
}
```

- [ ] **Step 5: manager.go — Cleanup 改用 history.created_at**

修改 `Cleanup` 方法中的过期判断逻辑：

```go
// Cleanup 清理过期会话
func (m *Manager) Cleanup(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(m.memoryDir)
	if err != nil {
		return 0, fmt.Errorf("读取记忆目录失败: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -m.retentionDays)
	deleted := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()

		// 读取 history.json 获取真实创建时间
		history, err := m.GetHistory(sessionID)
		if err != nil {
			m.log.Info("跳过会话（无法读取 history）: " + sessionID + ", error: " + err.Error())
			continue
		}

		if history.CreatedAt.Before(cutoff) {
			sessionDir := m.sessionDir(sessionID)
			if err := os.RemoveAll(sessionDir); err != nil {
				m.log.Error("清理会话失败: " + sessionID + ", error: " + err.Error())
				continue
			}
			deleted++
			m.log.Info("清理会话: " + sessionID + ", 创建时间: " + history.CreatedAt.Format("2006-01-02") + ", 轮数: " + fmt.Sprintf("%d", len(history.Messages)))
		}
	}

	m.log.Info(fmt.Sprintf("清理完成, 删除 %d 个会话", deleted))
	return deleted, nil
}
```

- [ ] **Step 6: Commit**

```bash
git add internal/memory/idgen.go internal/memory/manager.go
git commit -m "feat(memory): implement GetContextMessages, atomic writes, cleanup by created_at, crypto/rand"
```

---

### Task 4: Memory 单元测试

**Files:**
- Modify: `internal/memory/memory_test.go`

- [ ] **Step 1: 新增 GetContextMessages 测试**

在文件末尾追加：

```go
func TestManager_GetContextMessages(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log)

	sessionID := "test_session_context"
	mgr.CreateSession(sessionID)

	// 追加 5 条消息
	for i := 1; i <= 5; i++ {
		mgr.AppendMessage(sessionID, &Message{
			Round:       i,
			ChatID:      fmt.Sprintf("chat_%03d", i),
			Instruction: fmt.Sprintf("指令 %d", i),
			Result:      fmt.Sprintf("结果 %d", i),
			Status:      "completed",
		})
	}

	t.Run("窗口内全部返回", func(t *testing.T) {
		msgs, err := mgr.GetContextMessages(sessionID, 10)
		if err != nil {
			t.Fatalf("GetContextMessages() 失败: %v", err)
		}
		if len(msgs) != 5 {
			t.Errorf("windowSize=10 时应返回全部 5 条, got %d", len(msgs))
		}
	})

	t.Run("窗口截断", func(t *testing.T) {
		msgs, err := mgr.GetContextMessages(sessionID, 3)
		if err != nil {
			t.Fatalf("GetContextMessages() 失败: %v", err)
		}
		if len(msgs) != 3 {
			t.Errorf("windowSize=3 时应返回 3 条, got %d", len(msgs))
		}
		if msgs[0].Round != 3 {
			t.Errorf("截断后第一条应为 round=3, got %d", msgs[0].Round)
		}
		if msgs[2].Round != 5 {
			t.Errorf("截断后最后一条应为 round=5, got %d", msgs[2].Round)
		}
	})

	t.Run("windowSize=0 不限制", func(t *testing.T) {
		msgs, err := mgr.GetContextMessages(sessionID, 0)
		if err != nil {
			t.Fatalf("GetContextMessages() 失败: %v", err)
		}
		if len(msgs) != 5 {
			t.Errorf("windowSize=0 时应返回全部, got %d", len(msgs))
		}
	})

	t.Run("windowSize=-1 不限制", func(t *testing.T) {
		msgs, err := mgr.GetContextMessages(sessionID, -1)
		if err != nil {
			t.Fatalf("GetContextMessages() 失败: %v", err)
		}
		if len(msgs) != 5 {
			t.Errorf("windowSize=-1 时应返回全部, got %d", len(msgs))
		}
	})

	t.Run("会话不存在", func(t *testing.T) {
		_, err := mgr.GetContextMessages("nonexistent", 10)
		if err == nil {
			t.Error("GetContextMessages() 应对不存在的会话返回错误")
		}
	})
}
```

加入缺失的 import `"fmt"`。

- [ ] **Step 2: 新增原子写入验证测试**

```go
func TestManager_SaveHistory_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log)

	sessionID := "test_session_atomic"
	mgr.CreateSession(sessionID)

	// 追加消息（触发 saveHistory）
	msg := &Message{Round: 1, Status: "completed"}
	mgr.AppendMessage(sessionID, msg)

	// 验证 .tmp 文件不存在（rename 后应清理）
	tmpPath := filepath.Join(tmpDir, sessionID, "history.json.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("原子写入后 .tmp 文件应不存在")
	}

	// 验证正式文件存在且内容正确
	history, _ := mgr.GetHistory(sessionID)
	if len(history.Messages) != 1 {
		t.Errorf("原子写入后消息数应为 1, got %d", len(history.Messages))
	}
}

func TestManager_SaveChatRecord_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log)

	sessionID := "test_session_chat_atomic"
	mgr.CreateSession(sessionID)

	record := &ChatRecord{
		ChatID:    "chat_atomic_001",
		SessionID: sessionID,
		Status:    "completed",
	}
	mgr.SaveChatRecord(sessionID, record)

	// 验证 .tmp 文件不存在
	tmpPath := filepath.Join(tmpDir, sessionID, "chats", "chat_atomic_001.json.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("原子写入后 .tmp 文件应不存在")
	}

	// 验证内容正确
	got, _ := mgr.GetChatRecord(sessionID, "chat_atomic_001")
	if got.Status != "completed" {
		t.Errorf("原子写入后 status 应为 completed, got %s", got.Status)
	}
}
```

- [ ] **Step 3: 运行测试**

```bash
go test ./internal/memory/... -v
```

Expected: 全部 PASS（包括新增和已有测试）。

- [ ] **Step 4: Commit**

```bash
git add internal/memory/memory_test.go
git commit -m "test(memory): add tests for GetContextMessages and atomic writes"
```

---

### Task 5: Agent 包 — 删除重复类型和死代码

**Files:**
- Modify: `internal/agent/runtime_state.go`
- Modify: `internal/agent/executor.go`
- Modify: `internal/agent/engine.go`

- [ ] **Step 1: runtime_state.go — 删除 Complete() 方法**

删除 `runtime_state.go` 中的 `Complete` 方法（约第 110-138 行）：

```go
// 删除以下整个方法：
// func (r *RuntimeState) Complete(sessionID string, result *ChatResult) (*memory.ChatRecord, error) { ... }
```

- [ ] **Step 2: runtime_state.go — 删除 CancelManager**

删除 `runtime_state.go` 末尾的整个 `CancelManager` 类型及其所有方法（约第 194-254 行）：

```go
// 删除以下全部：
// type CancelManager struct { ... }
// func NewCancelManager() *CancelManager { ... }
// func (c *CancelManager) Register(...) { ... }
// func (c *CancelManager) Cancel(...) { ... }
// func (c *CancelManager) Unregister(...) { ... }
// func (c *CancelManager) IsCancelled(...) { ... }
// func (c *CancelManager) Count() { ... }
```

- [ ] **Step 3: runtime_state.go — 删除重复 ID 生成函数**

删除 `GenerateTaskID` 和 `GenerateStepID`（约第 161-185 行）：

```go
// 删除以下两个函数：
// func GenerateTaskID() string { ... }
// func GenerateStepID() string { ... }
```

同时删除不再需要的 import `"crypto/rand"` 和 `"encoding/hex"`（如果它们不再被其他地方使用——检查：`CancelManager` 不用，`Register` 不用，删除后这两个 import 也不用了）。

- [ ] **Step 4: runtime_state.go — 清理 import**

删除不再使用的 import。检查剩余代码中是否还有对 `"crypto/rand"`、`"encoding/hex"` 的引用。`ActiveChat` 和 `RuntimeState` 都不使用这两个包，因此删除。

- [ ] **Step 5: executor.go — 删除冗余的 AttachmentPath 类型**

删除 `executor.go` 中的 `AttachmentPath` 类型定义（约第 59-67 行）：

```go
// 删除：
// type AttachmentPath struct {
//     OriginalName string
//     Type         string
//     FullPath     string
//     RelativePath string
//     Size         int64
//     ContentType  string
// }
```

- [ ] **Step 6: executor.go — Execute 方法改用 memory.AttachmentPath**

修改 `Execute` 方法中的附件构建逻辑（约第 121-131 行）：

```go
// 改造前
var attachmentPaths []AttachmentPath
for _, att := range task.Attachments {
    attachmentPaths = append(attachmentPaths, AttachmentPath{...})
}

// 改造后
var attachmentPaths []memory.AttachmentPath
for _, att := range task.Attachments {
    attachmentPaths = append(attachmentPaths, memory.AttachmentPath{
        OriginalName: att.Name,
        Type:         att.Type,
        FullPath:     att.Content,
        RelativePath: att.Content,
        Size:         0,
        ContentType:  getContentTypeFromType(att.Type),
    })
}
```

- [ ] **Step 7: executor.go — 删除 runningTasks 及相关方法**

删除 `Executor` 结构体中的 `runningTasks sync.Map` 字段（第 95 行）：

```go
// 删除：
// runningTasks  sync.Map
```

删除 `Execute` 方法中的 runningTasks 操作（第 117-118 行）：

```go
// 删除：
// e.runningTasks.Store(task.ID, true)
// defer e.runningTasks.Delete(task.ID)
```

删除 `IsRunning` 和 `RunningCount` 方法（第 346-360 行）：

```go
// 删除：
// func (e *Executor) IsRunning(taskID string) bool { ... }
// func (e *Executor) RunningCount() int { ... }
```

删除 `sync` import（如果不再使用——检查：`Executor` 结构体不再有 `sync.Map`，删除后 `"sync"` 不再需要）。

- [ ] **Step 8: executor.go — 简化状态映射逻辑**

当前 status 判断逻辑在 ChatRecord 构建和 Message 构建中**重复了两遍**。重构为只判断一次：

```go
// 在 SaveChatRecord 之前统一判断 status
attachments := []string{}
for _, att := range task.Attachments {
    attachments = append(attachments, att.Name)
}

// 统一的状态判断（只判断一次）
var chatStatus string
var chatResult string
var chatSteps []memory.Step
var chatError *memory.Error

if ctxCancelled {
    chatStatus = "cancelled"
} else if err != nil {
    chatStatus = "failed"
    chatError = &memory.Error{Code: "execution_error", Message: err.Error()}
} else if result != nil && result.Cancelled {
    chatStatus = "cancelled"
} else if result != nil {
    chatStatus = "completed"
    chatResult = result.Content
    chatSteps = convertSteps(result.Steps)
} else {
    chatStatus = "failed"
    chatError = &memory.Error{Code: "unknown_error", Message: "执行完成但无结果"}
}

// 构建 ChatRecord
record := &memory.ChatRecord{
    ChatID:      task.ID,
    SessionID:   sessionID,
    Round:       task.Round,
    Timestamp:   endTime,
    StartedAt:   startTime,
    EndedAt:     endTime,
    Instruction: task.Instruction,
    Duration:    int(duration.Seconds()),
    Attachments: attachments,
    Status:      chatStatus,
    Result:      chatResult,
    Steps:       chatSteps,
    Error:       chatError,
}

if saveErr := e.memoryManager.SaveChatRecord(sessionID, record); saveErr != nil {
    e.logger.Error("保存对话记录失败: " + saveErr.Error())
}

// 构建 Message（status 等字段直接从 record 拷贝）
var stepsCount int
if result != nil {
    stepsCount = len(result.Steps)
}

msg := &memory.Message{
    ChatID:      task.ID,
    Round:       task.Round,
    Timestamp:   endTime,
    Instruction: task.Instruction,
    Attachments: attachments,
    Duration:    int(duration.Seconds()),
    StepsCount:  stepsCount,
    Status:      chatStatus,
    Result:      chatResult,
    Error:       chatError,
}

if appendErr := e.memoryManager.AppendMessage(sessionID, msg); appendErr != nil {
    e.logger.Error("追加历史消息失败: " + appendErr.Error())
}
```

- [ ] **Step 9: engine.go — 改用 memory.AttachmentPath**

修改 `engine.go` 中所有 `AttachmentPath` 引用为 `memory.AttachmentPath`：

修改 `Run` 方法签名（第 60-68 行）：

```go
func (e *Engine) Run(
    ctx context.Context,
    instruction string,
    prompt string,
    attachmentPaths []memory.AttachmentPath, // agent.AttachmentPath → memory.AttachmentPath
    historyMessages []memory.Message,
    modelName string,
    cb *ProgressCallback,
) (*RunResult, error) {
```

修改 `buildUserMessage` 方法签名（第 412 行）：

```go
func (e *Engine) buildUserMessage(instruction string, attachmentPaths []memory.AttachmentPath) string {
```

修改 `buildMessageList` 方法签名（第 431 行）：

```go
func (e *Engine) buildMessageList(instruction string, attachmentPaths []memory.AttachmentPath, historyMessages []memory.Message) []adk.Message {
```

- [ ] **Step 10: Commit**

```bash
git add internal/agent/runtime_state.go internal/agent/executor.go internal/agent/engine.go
git commit -m "refactor(agent): remove duplicate types, dead code, consolidate to RuntimeState"
```

---

### Task 6: Health Handler — 使用 RuntimeState 替代 Executor.RunningCount

**Files:**
- Modify: `internal/api/handler/health.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: health.go — 注入 RuntimeState**

修改 `HealthHandler` 结构体和构造函数：

```go
type HealthHandler struct {
	config        config.Config
	skillRegistry *skill.Registry
	mcpManager    *mcp.Manager
	memoryManager *memory.Manager
	runtimeState  *agent.RuntimeState // 替换 executor *agent.Executor
	startTime     time.Time
	logger        *logger.Logger
}

func NewHealthHandler(
	cfg config.Config,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	memMgr *memory.Manager,
	runtime *agent.RuntimeState, // 替换 exec *agent.Executor
	log *logger.Logger,
) *HealthHandler {
	return &HealthHandler{
		config:        cfg,
		skillRegistry: skills,
		mcpManager:    mcpMgr,
		memoryManager: memMgr,
		runtimeState:  runtime,
		startTime:     time.Now(),
		logger:        log,
	}
}
```

- [ ] **Step 2: health.go — 修改 Metrics 引用**

将 `h.executor.RunningCount()` 替换为 `h.runtimeState.RunningCount()`：

```go
Metrics: map[string]interface{}{
	"chats_running": h.runtimeState.RunningCount(),
},
```

- [ ] **Step 3: server.go — 更新 NewHealthHandler 调用**

将传入的参数从 `exec` 改为 `runtime`：

```go
healthH := handler.NewHealthHandler(cfg, skills, mcpMgr, mem, runtime, log)
```

- [ ] **Step 4: Commit**

```bash
git add internal/api/handler/health.go internal/api/server.go
git commit -m "refactor(api): health check uses RuntimeState instead of Executor"
```

---

### Task 7: Chat Handler — 使用 GetContextMessages

**Files:**
- Modify: `internal/api/handler/chat.go:140-155`

- [ ] **Step 1: chat.go — 上下文构建改用 GetContextMessages**

修改 `Handle` 方法中获取历史消息的逻辑：

```go
// 改造前
historyMessages = history.Messages
round = len(historyMessages) + 1

// 改造后
round = h.memory.GetRoundCount(sessionID) + 1
historyMessages, err = h.memory.GetContextMessages(sessionID, h.config.Memory.HistoryWindow)
if err != nil {
	rc.JSON(500, utils.H{"status": "error", "message": "获取上下文失败"})
	return
}
```

同时删除不再需要的 `history` 变量（原 `GetHistory` 调用的返回值）。

完整上下文逻辑变为：

```go
if sessionID == "" || !h.memory.ExistsSession(sessionID) {
	// 新会话
	sessionID = memory.GenerateSessionID()
	isNew = true
	round = 1
	historyMessages = []memory.Message{}
} else {
	// 继续会话
	isNew = false
	round = h.memory.GetRoundCount(sessionID) + 1
	historyMessages, err = h.memory.GetContextMessages(sessionID, h.config.Memory.HistoryWindow)
	if err != nil {
		rc.JSON(500, utils.H{"status": "error", "message": "获取上下文失败"})
		return
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/api/handler/chat.go
git commit -m "feat(api): use GetContextMessages for LLM context with history_window"
```

---

### Task 8: 编译验证

**Files:**
- 无新建文件

- [ ] **Step 1: 编译项目**

```bash
cd /Users/zhangfengda/workspace/groot
go build ./...
```

Expected: 编译成功，无错误。

- [ ] **Step 2: 运行所有单元测试**

```bash
go test ./internal/memory/... -v
go test ./internal/agent/... -v
go test ./internal/config/... -v
```

Expected: 全部 PASS。

- [ ] **Step 3: 修复所有编译/测试错误**

如果上述步骤有错误，逐一修复。

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore: build verification after memory refactor"
```

---

## Self-Review

**1. Spec coverage:**
- [x] history_window 配置项 — Task 1
- [x] GetContextMessages 方法 — Task 2 + Task 3 Step 2
- [x] 原子写入 — Task 3 Step 3-4 + Task 4 Step 2
- [x] 清理用 created_at — Task 3 Step 5
- [x] crypto/rand — Task 3 Step 1
- [x] 删除 agent.AttachmentPath — Task 5 Step 5
- [x] 删除重复 ID 生成函数 — Task 5 Step 3
- [x] 删除 Complete() 死代码 — Task 5 Step 1
- [x] 删除 CancelManager — Task 5 Step 2
- [x] 删除 Executor.runningTasks — Task 5 Step 7
- [x] 状态映射逻辑只保留一份 — Task 5 Step 8
- [x] engine.go 改用 memory.AttachmentPath — Task 5 Step 9
- [x] HealthHandler 用 RuntimeState — Task 6
- [x] ChatHandler 用 GetContextMessages — Task 7
- [x] 单元测试 — Task 4

**2. Placeholder scan:**
- 无 TBD/TODO
- 所有步骤都有完整代码
- 所有命令有预期输出

**3. Type consistency:**
- `memory.AttachmentPath` 在 executor.go 和 engine.go 中一致
- `GetContextMessages(sessionID, windowSize)` 签名在 memory.go 接口、manager.go 实现、chat.go 调用中一致
- `RuntimeState.RunningCount()` 在 runtime_state.go 定义和 health.go 使用中一致
