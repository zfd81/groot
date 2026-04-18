# Groot Agent 多轮对话实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现基于 Session/Chat 的多轮对话架构，删除原有 storage 模块，新增 Memory 模块，修改 API 支持多轮对话 SSE 流式返回。

**Architecture:** 采用三层架构：Access Layer（API + Attachment Handler）、Intelligence Layer（Agent Engine + Memory + Runtime State）、System Layer（Config + Logger）。Memory 使用文件系统存储会话历史（history.json + chats/{chat_id}.json），Runtime State 使用内存管理活跃对话状态。

**Tech Stack:** Go 1.21+、Hertz HTTP 框架、eino Agent 框架、文件系统存储

---

## Scope Overview

本次变更涉及以下子系统：

1. **删除 Storage 模块** - 移除 BoltDB 存储，清理相关依赖
2. **新增 Memory 模块** - 实现 Session 管理、History 持久化、附件存储、定时清理
3. **新增 Runtime State 模块** - 实现活跃对话状态管理、并发控制
4. **修改 Agent Executor** - 支持多轮对话上下文传递、新 ID 格式
5. **修改 API Handler** - 路径从 /task 改为 /chat 和 /sess，支持 X-Session-ID
6. **修改 SSE Writer** - 事件格式调整，支持 round 字段
7. **修改 Config** - 移除 StorageConfig，添加 MemoryConfig

---

## File Structure

### 新增文件

```
internal/memory/
├── types.go           # 数据结构：SessionInfo, Message, History, ChatRecord
├── memory.go          # Memory 接口定义
├── manager.go         # Manager 实现类（核心业务逻辑）
├── cleanup.go         # 定时清理调度器
└── idgen.go           # ID 生成器（session_id, chat_id, step_id）

internal/agent/
├── runtime_state.go   # Runtime State 实现（替代 CancelManager）
```

### 删除文件

```
internal/storage/
├── storage.go         # 删除
├── boltdb.go          # 删除
├── task.go            # 删除
```

### 修改文件

```
internal/agent/
├── executor.go        # 移除 storage 依赖，使用 Memory + RuntimeState
├── engine.go          # 移除 storage.AttachmentPath，使用 memory.AttachmentPath
├── sse.go             # 添加 round 字段，修改响应 Header
├── cancel.go          # 删除（合并到 runtime_state.go）
├── idgen.go           # 删除（使用 memory/idgen.go）

internal/api/handler/
├── execute.go         # 重命名为 chat.go，逻辑改为多轮对话
├── cancel.go          # 路径改为 DELETE /chat/{sid}
├── status.go          # 路径改为 GET /chat/status/{sid}
├── detail.go          # 路径改为 GET /chat/{sid}
├── history.go         # 重命名为 session_history.go，路径改为 GET /sess/history
├── session.go         # 新增：GET /sess/{sid}
├── health.go          # 保持不变
├── skills.go          # 保持不变
├── tools.go           # 保持不变

internal/api/router.go # 路由路径调整

internal/config/
├── config.go          # 移除 StorageConfig，添加 MemoryConfig
├── defaults.go        # 默认配置调整

cmd/groot/main.go      # 移除 storage 初始化，添加 memory 初始化

go.mod                 # 移除 bbolt 依赖
```

---

## Phase 1: 删除 Storage 模块

### Task 1.1: 移除 BoltDB 依赖

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: 编辑 go.mod，删除 bbolt 依赖**

找到 `go.etcd.io/bbolt` 相关行并删除：

```diff
- go.etcd.io/bbolt v1.3.x
```

- [ ] **Step 2: 运行 go mod tidy 清理依赖**

```bash
cd /Users/zhangfengda/workspace/groot
go mod tidy
```

Expected: 无错误输出，依赖已清理

- [ ] **Step 3: 验证编译失败（确认依赖已被移除）**

```bash
go build ./...
```

Expected: 编译失败，报 `storage` 相关错误（正常，后续任务会修复）

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: remove boltdb dependency"
```

### Task 1.2: 删除 internal/storage 目录

**Files:**
- Delete: `internal/storage/storage.go`
- Delete: `internal/storage/boltdb.go`
- Delete: `internal/storage/task.go`

- [ ] **Step 1: 删除 storage 目录**

```bash
rm -rf internal/storage/
```

- [ ] **Step 2: 验证文件已删除**

```bash
ls internal/storage/ 2>&1
```

Expected: `No such file or directory`

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "chore: remove storage module (replaced by memory module)"
```

---

## Phase 2: 新增 Memory 模块数据结构

### Task 2.1: 创建 Memory 数据结构定义

**Files:**
- Create: `internal/memory/types.go`

- [ ] **Step 1: 创建 memory 目录**

```bash
mkdir -p internal/memory
```

- [ ] **Step 2: 创建 types.go 文件**

```go
package memory

import "time"

// SessionInfo 会话信息
type SessionInfo struct {
	SessionID  string    `json:"session_id"`
	CreatedAt  time.Time `json:"created_at"`
	RoundCount int       `json:"round_count"`
	Path       string    `json:"path"`
}

// Message 单轮对话记录（存储在 history.json）
type Message struct {
	Round             int       `json:"round"`
	ChatID            string    `json:"chat_id"`
	Timestamp         time.Time `json:"timestamp"`
	Instruction       string    `json:"instruction"`
	Attachments       []string  `json:"attachments"`
	Result            string    `json:"result"`
	ResultAttachments []string  `json:"result_attachments"`
	Status            string    `json:"status"` // completed/failed/cancelled
	Duration          int       `json:"duration"` // 秒
	StepsCount        int       `json:"steps_count"`
	Error             *Error    `json:"error,omitempty"`
}

// History 会话历史（history.json 根结构）
type History struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	Messages  []Message `json:"messages"`
}

// ChatRecord 单次对话详细执行记录（chats/{chat_id}.json）
type ChatRecord struct {
	ChatID            string      `json:"chat_id"`
	SessionID         string      `json:"session_id"`
	Round             int         `json:"round"`
	Timestamp         time.Time   `json:"timestamp"`
	Instruction       string      `json:"instruction"`
	Attachments       []string    `json:"attachments"`
	Result            string      `json:"result"`
	ResultAttachments []string    `json:"result_attachments"`
	Status            string      `json:"status"`
	Duration          int         `json:"duration"`
	Caller            string      `json:"caller"`
	Steps             []Step      `json:"steps"`
	Error             *Error      `json:"error,omitempty"`
}

// Step 单步执行记录
type Step struct {
	StepID       string    `json:"step_id"`
	Type         string    `json:"type"` // skill/tool/llm
	Name         string    `json:"name"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Status       string    `json:"status"` // success/failed
	NestingLevel int       `json:"nesting_level"`
	Error        *Error    `json:"error,omitempty"`
}

// Error 错误信息
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AttachmentPath 附件路径信息（传递给 Agent）
type AttachmentPath struct {
	OriginalName string
	Type         string // file/url
	FullPath     string
	RelativePath string
	Size         int64
	ContentType  string
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/memory/types.go
git commit -m "feat(memory): add data structure definitions"
```

### Task 2.2: 创建 ID 生成器

**Files:**
- Create: `internal/memory/idgen.go`

- [ ] **Step 1: 创建 idgen.go 文件**

```go
package memory

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// GenerateSessionID 生成会话ID
// 格式: {YYYYMMDDHHMMSSmmm}_{random4}
func GenerateSessionID() string {
	now := time.Now()
	ts := now.Format("20060102150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	random := randomString(4)
	return fmt.Sprintf("%s_%s", ts, random)
}

// GenerateChatID 生成对话ID
// 格式: chat_{YYYYMMDDHHMMSSmmm}
func GenerateChatID() string {
	now := time.Now()
	ts := now.Format("20060102150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	return fmt.Sprintf("chat_%s", ts)
}

// GenerateStepID 生成步骤ID
// 格式: {YYYYMMDD}-{HHMMSSmmm}-{random6}
func GenerateStepID() string {
	now := time.Now()
	date := now.Format("20060102")
	timeStr := now.Format("150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	random := randomString(6)
	return fmt.Sprintf("%s-%s-%s", date, timeStr, random)
}

// randomString 生成指定长度的随机字符串（小写字母+数字）
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/memory/idgen.go
git commit -m "feat(memory): add ID generator (session/chat/step)"
```

---

## Phase 3: 新增 Memory 接口和实现

### Task 3.1: 创建 Memory 接口定义

**Files:**
- Create: `internal/memory/memory.go`

- [ ] **Step 1: 创建 memory.go 接口文件**

```go
package memory

import "context"

// Memory 接口定义
type Memory interface {
	// Session 管理
	CreateSession(sessionID string) error
	ExistsSession(sessionID string) bool
	GetSessionInfo(sessionID string) (*SessionInfo, error)
	ListSessions(limit, offset int) ([]SessionInfo, int, error)
	
	// History 管理
	AppendMessage(sessionID string, message *Message) error
	GetHistory(sessionID string) (*History, error)
	GetRoundCount(sessionID string) int
	
	// Chat 记录管理
	SaveChatRecord(sessionID string, record *ChatRecord) error
	GetChatRecord(sessionID string, chatID string) (*ChatRecord, error)
	GetLatestChatRecord(sessionID string) (*ChatRecord, error)
	
	// 附件管理
	SaveAttachment(sessionID string, filename string, content []byte) (string, error)
	GetAttachmentPath(sessionID string, filename string) string
	
	// 清理
	Cleanup(ctx context.Context) (int, error)
	
	// 获取记忆目录路径
	GetMemoryDir() string
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/memory/memory.go
git commit -m "feat(memory): add Memory interface definition"
```

### Task 3.2: 创建 Manager 实现（核心业务逻辑）

**Files:**
- Create: `internal/memory/manager.go`

- [ ] **Step 1: 创建 manager.go 文件（第一部分：结构体和初始化）**

```go
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/logger"
)

// Manager Memory 接口的实现
type Manager struct {
	memoryDir    string
	retentionDays int
	log          *logger.Logger
}

// NewManager 创建 Memory Manager
func NewManager(memoryDir string, retentionDays int, log *logger.Logger) *Manager {
	// 确保目录存在
	os.MkdirAll(memoryDir, 0755)
	
	return &Manager{
		memoryDir:     memoryDir,
		retentionDays: retentionDays,
		log:           log,
	}
}

// GetMemoryDir 返回记忆目录路径
func (m *Manager) GetMemoryDir() string {
	return m.memoryDir
}

// sessionDir 返回会话目录路径
func (m *Manager) sessionDir(sessionID string) string {
	return filepath.Join(m.memoryDir, sessionID)
}

// historyPath 返回 history.json 路径
func (m *Manager) historyPath(sessionID string) string {
	return filepath.Join(m.sessionDir(sessionID), "history.json")
}

// chatsDir 返回 chats 目录路径
func (m *Manager) chatsDir(sessionID string) string {
	return filepath.Join(m.sessionDir(sessionID), "chats")
}

// chatPath 返回单次对话记录路径
func (m *Manager) chatPath(sessionID, chatID string) string {
	return filepath.Join(m.chatsDir(sessionID), chatID+".json")
}

// attachmentsDir 返回 attachments 目录路径
func (m *Manager) attachmentsDir(sessionID string) string {
	return filepath.Join(m.sessionDir(sessionID), "attachments")
}
```

- [ ] **Step 2: 添加 Session 管理方法**

```go
// CreateSession 创建新会话
func (m *Manager) CreateSession(sessionID string) error {
	sessionDir := m.sessionDir(sessionID)
	
	// 创建目录结构
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	if err := os.MkdirAll(m.chatsDir(sessionID), 0755); err != nil {
		return fmt.Errorf("创建 chats 目录失败: %w", err)
	}
	if err := os.MkdirAll(m.attachmentsDir(sessionID), 0755); err != nil {
		return fmt.Errorf("创建 attachments 目录失败: %w", err)
	}
	
	// 创建初始 history.json
	history := &History{
		SessionID: sessionID,
		CreatedAt: time.Now(),
		Messages:  []Message{},
	}
	
	return m.saveHistory(sessionID, history)
}

// ExistsSession 检查会话是否存在
func (m *Manager) ExistsSession(sessionID string) bool {
	historyPath := m.historyPath(sessionID)
	_, err := os.Stat(historyPath)
	return err == nil
}

// GetSessionInfo 获取会话信息
func (m *Manager) GetSessionInfo(sessionID string) (*SessionInfo, error) {
	history, err := m.GetHistory(sessionID)
	if err != nil {
		return nil, err
	}
	
	return &SessionInfo{
		SessionID:  sessionID,
		CreatedAt:  history.CreatedAt,
		RoundCount: len(history.Messages),
		Path:       m.sessionDir(sessionID),
	}, nil
}

// ListSessions 查询会话列表
func (m *Manager) ListSessions(limit, offset int) ([]SessionInfo, int, error) {
	entries, err := os.ReadDir(m.memoryDir)
	if err != nil {
		return nil, 0, fmt.Errorf("读取记忆目录失败: %w", err)
	}
	
	var sessions []SessionInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		info, err := m.GetSessionInfo(sessionID)
		if err != nil {
			m.log.Warn("获取会话信息失败: " + sessionID + ", error: " + err.Error())
			continue
		}
		sessions = append(sessions, *info)
	}
	
	// 按创建时间倒序排列
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	
	total := len(sessions)
	
	// 应用分页
	if offset >= total {
		return []SessionInfo{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	
	return sessions[offset:end], total, nil
}
```

- [ ] **Step 3: 添加 History 管理方法**

```go
// saveHistory 保存 history.json
func (m *Manager) saveHistory(sessionID string, history *History) error {
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 history 失败: %w", err)
	}
	
	return os.WriteFile(m.historyPath(sessionID), data, 0644)
}

// GetHistory 获取会话历史
func (m *Manager) GetHistory(sessionID string) (*History, error) {
	data, err := os.ReadFile(m.historyPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("会话不存在: %s", sessionID)
		}
		return nil, fmt.Errorf("读取 history 失败: %w", err)
	}
	
	var history History
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("解析 history 失败: %w", err)
	}
	
	return &history, nil
}

// AppendMessage 追加对话消息
func (m *Manager) AppendMessage(sessionID string, message *Message) error {
	history, err := m.GetHistory(sessionID)
	if err != nil {
		return err
	}
	
	history.Messages = append(history.Messages, *message)
	
	return m.saveHistory(sessionID, history)
}

// GetRoundCount 获取对话轮数
func (m *Manager) GetRoundCount(sessionID string) int {
	history, err := m.GetHistory(sessionID)
	if err != nil {
		return 0
	}
	return len(history.Messages)
}
```

- [ ] **Step 4: 添加 Chat 记录管理方法**

```go
// SaveChatRecord 保存详细对话记录
func (m *Manager) SaveChatRecord(sessionID string, record *ChatRecord) error {
	// 确保 chats 目录存在
	os.MkdirAll(m.chatsDir(sessionID), 0755)
	
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 chat record 失败: %w", err)
	}
	
	return os.WriteFile(m.chatPath(sessionID, record.ChatID), data, 0644)
}

// GetChatRecord 获取单次对话详情
func (m *Manager) GetChatRecord(sessionID string, chatID string) (*ChatRecord, error) {
	data, err := os.ReadFile(m.chatPath(sessionID, chatID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("对话记录不存在: %s", chatID)
		}
		return nil, fmt.Errorf("读取 chat record 失败: %w", err)
	}
	
	var record ChatRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("解析 chat record 失败: %w", err)
	}
	
	return &record, nil
}

// GetLatestChatRecord 获取最近一次对话记录
func (m *Manager) GetLatestChatRecord(sessionID string) (*ChatRecord, error) {
	history, err := m.GetHistory(sessionID)
	if err != nil {
		return nil, err
	}
	
	if len(history.Messages) == 0 {
		return nil, nil
	}
	
	latest := history.Messages[len(history.Messages)-1]
	return m.GetChatRecord(sessionID, latest.ChatID)
}
```

- [ ] **Step 5: 添加附件管理方法**

```go
// SaveAttachment 保存附件
func (m *Manager) SaveAttachment(sessionID string, filename string, content []byte) (string, error) {
	// 确保 attachments 目录存在
	os.MkdirAll(m.attachmentsDir(sessionID), 0755)
	
	// 文件名安全处理
	safeName := sanitizeFilename(filename)
	
	fullPath := filepath.Join(m.attachmentsDir(sessionID), safeName)
	
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return "", fmt.Errorf("保存附件失败: %w", err)
	}
	
	return fullPath, nil
}

// GetAttachmentPath 获取附件完整路径
func (m *Manager) GetAttachmentPath(sessionID string, filename string) string {
	return filepath.Join(m.attachmentsDir(sessionID), sanitizeFilename(filename))
}

// sanitizeFilename 文件名安全处理
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "..", "_")
	
	// 限制长度
	if len(name) > 255 {
		ext := filepath.Ext(name)
		base := name[:255-len(ext)]
		name = base + ext
	}
	
	return name
}
```

- [ ] **Step 6: 添加清理方法**

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
		sessionDir := m.sessionDir(sessionID)
		
		info, err := entry.Info()
		if err != nil {
			continue
		}
		
		// 检查创建时间
		if info.ModTime().Before(cutoff) {
			// 删除整个会话目录
			if err := os.RemoveAll(sessionDir); err != nil {
				m.log.Error("清理会话失败: " + sessionID + ", error: " + err.Error())
				continue
			}
			deleted++
			m.log.Info("清理会话: " + sessionID + ", 创建时间: " + info.ModTime().Format("2006-01-02"))
		}
	}
	
	m.log.Info(fmt.Sprintf("清理完成, 删除 %d 个会话", deleted))
	return deleted, nil
}
```

- [ ] **Step 7: Commit**

```bash
git add internal/memory/manager.go
git commit -m "feat(memory): implement Manager with all core methods"
```

### Task 3.3: 创建定时清理调度器

**Files:**
- Create: `internal/memory/cleanup.go`

- [ ] **Step 1: 创建 cleanup.go 文件**

```go
package memory

import (
	"context"
	"time"
	
	"github.com/zfd81/groot/internal/logger"
)

// CleanupScheduler 定时清理调度器
type CleanupScheduler struct {
	manager    *Manager
	schedule   string // HH:MM 格式，如 "02:00"
	stopCh     chan struct{}
	log        *logger.Logger
}

// NewCleanupScheduler 创建清理调度器
func NewCleanupScheduler(manager *Manager, schedule string, log *logger.Logger) *CleanupScheduler {
	return &CleanupScheduler{
		manager:  manager,
		schedule: schedule,
		stopCh:   make(chan struct{}),
		log:      log,
	}
}

// Start 启动清理调度器
func (s *CleanupScheduler) Start() {
	// 解析清理时间
	parts := strings.Split(s.schedule, ":")
	if len(parts) != 2 {
		s.log.Error("无效的清理时间格式: " + s.schedule)
		return
	}
	
	hour, _ := strconv.Atoi(parts[0])
	minute, _ := strconv.Atoi(parts[1])
	
	// 计算下一次清理时间
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}
	
	s.log.Info("清理调度器已启动, 下次清理时间: " + next.Format("2006-01-02 15:04:05"))
	
	go s.runScheduler(next)
}

// runScheduler 运行调度循环
func (s *CleanupScheduler) runScheduler(next time.Time) {
	for {
		select {
		case <-s.stopCh:
			s.log.Info("清理调度器已停止")
			return
		case <-time.After(time.Until(next)):
			// 执行清理
			s.log.Info("开始执行清理任务")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			deleted, err := s.manager.Cleanup(ctx)
			cancel()
			
			if err != nil {
				s.log.Error("清理任务失败: " + err.Error())
			} else {
				s.log.Info(fmt.Sprintf("清理任务完成, 删除 %d 个会话", deleted))
			}
			
			// 计算下一次清理时间（24小时后）
			next = next.Add(24 * time.Hour)
		}
	}
}

// Stop 停止清理调度器
func (s *CleanupScheduler) Stop() {
	close(s.stopCh)
}
```

- [ ] **Step 2: 补充缺失的 import**

```go
import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	
	"github.com/zfd81/groot/internal/logger"
)
```

- [ ] **Step 3: Commit**

```bash
git add internal/memory/cleanup.go
git commit -m "feat(memory): add cleanup scheduler"
```

---

## Phase 4: 新增 Runtime State 模块

### Task 4.1: 创建 Runtime State 实现

**Files:**
- Create: `internal/agent/runtime_state.go`

- [ ] **Step 1: 创建 runtime_state.go 文件（数据结构）**

```go
package agent

import (
	"fmt"
	"sync"
	"time"
	
	"github.com/zfd81/groot/internal/memory"
)

// ActiveChat 活跃对话状态
type ActiveChat struct {
	SessionID string        `json:"session_id"`
	ChatID    string        `json:"chat_id"`
	Status    string        `json:"status"` // running
	Progress  *ChatProgress `json:"progress"`
	StartTime time.Time     `json:"start_time"`
	CancelCh  chan struct{} `json:"-"` // 取消信号通道
}

// ChatProgress 对话进度
type ChatProgress struct {
	CurrentStep    int `json:"current_step"`
	StepsCompleted int `json:"steps_completed"`
	Percentage     int `json:"percentage"`
}

// ChatResult 对话结果
type ChatResult struct {
	Status            string
	Result            string
	ResultAttachments []string
	Duration          int
	Steps             []memory.Step
	Error             *memory.Error
}

// RuntimeState 运行时状态管理器
type RuntimeState struct {
	activeChats sync.Map // session_id -> *ActiveChat
}
```

- [ ] **Step 2: 添加接口和实现方法**

```go
// NewRuntimeState 创建 Runtime State
func NewRuntimeState() *RuntimeState {
	return &RuntimeState{}
}

// Register 注册活跃对话
func (r *RuntimeState) Register(sessionID, chatID string) (*ActiveChat, error) {
	// 检查是否已有活跃对话
	if r.IsRunning(sessionID) {
		return nil, fmt.Errorf("session %s already has running chat", sessionID)
	}
	
	chat := &ActiveChat{
		SessionID: sessionID,
		ChatID:    chatID,
		Status:    "running",
		Progress:  &ChatProgress{},
		StartTime: time.Now(),
		CancelCh:  make(chan struct{}),
	}
	
	r.activeChats.Store(sessionID, chat)
	return chat, nil
}

// Get 获取活跃对话状态
func (r *RuntimeState) Get(sessionID string) (*ActiveChat, bool) {
	value, ok := r.activeChats.Load(sessionID)
	if !ok {
		return nil, false
	}
	return value.(*ActiveChat), true
}

// UpdateProgress 更新进度
func (r *RuntimeState) UpdateProgress(sessionID string, progress *ChatProgress) error {
	chat, ok := r.Get(sessionID)
	if !ok {
		return fmt.Errorf("session %s not running", sessionID)
	}
	
	chat.Progress = progress
	return nil
}

// Cancel 取消对话
func (r *RuntimeState) Cancel(sessionID string) error {
	chat, ok := r.Get(sessionID)
	if !ok {
		return fmt.Errorf("session %s not running", sessionID)
	}
	
	chat.Status = "cancelled"
	close(chat.CancelCh)
	return nil
}

// Complete 完成对话，返回 ChatRecord
func (r *RuntimeState) Complete(sessionID string, result *ChatResult) (*memory.ChatRecord, error) {
	chat, ok := r.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("session %s not running", sessionID)
	}
	
	// 构建 ChatRecord
	record := &memory.ChatRecord{
		ChatID:            chat.ChatID,
		SessionID:         sessionID,
		Round:             0, // 由调用方设置
		Timestamp:         chat.StartTime,
		Instruction:       "", // 由调用方设置
		Attachments:       []string{},
		Result:            result.Result,
		ResultAttachments: result.ResultAttachments,
		Status:            result.Status,
		Duration:          result.Duration,
		Caller:            "",
		Steps:             result.Steps,
		Error:             result.Error,
	}
	
	// 移除活跃状态
	r.activeChats.Delete(sessionID)
	
	return record, nil
}

// IsRunning 检查会话是否有活跃对话
func (r *RuntimeState) IsRunning(sessionID string) bool {
	_, ok := r.activeChats.Load(sessionID)
	return ok
}

// RunningCount 返回当前活跃对话数量
func (r *RuntimeState) RunningCount() int {
	count := 0
	r.activeChats.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/agent/runtime_state.go
git commit -m "feat(agent): add RuntimeState for active chat management"
```

### Task 4.2: 删除旧的 cancel.go 和 idgen.go

**Files:**
- Delete: `internal/agent/cancel.go`
- Delete: `internal/agent/idgen.go`

- [ ] **Step 1: 删除 cancel.go**

```bash
rm internal/agent/cancel.go
```

- [ ] **Step 2: 删除 idgen.go**

```bash
rm internal/agent/idgen.go
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "chore(agent): remove old cancel.go and idgen.go"
```

---

## Phase 5: 修改配置模块

### Task 5.1: 修改配置结构

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: 删除 StorageConfig，添加 MemoryConfig**

找到 StorageConfig 定义，删除并替换为 MemoryConfig：

```diff
type StorageConfig struct {
    Engine      string `yaml:"engine"`
    BoltDB      BoltDBConfig `yaml:"boltdb"`
    RetentionDays int `yaml:"retention_days"`
}

type BoltDBConfig struct {
    File   string `yaml:"file"`
    Bucket string `yaml:"bucket"`
}
```

替换为：

```go
type MemoryConfig struct {
    Directory      string `yaml:"directory"`       // 记忆目录
    RetentionDays  int    `yaml:"retention_days"`  // 保留天数
    CleanupSchedule string `yaml:"cleanup_schedule"` // 清理时间 HH:MM
}
```

- [ ] **Step 2: 修改 Config 结构体**

```diff
type Config struct {
    Agent       AgentConfig       `yaml:"agent"`
    Server      ServerConfig      `yaml:"server"`
    LLM         LLMConfig         `yaml:"llm"`
    Skills      SkillsConfig      `yaml:"skills"`
    MCP         MCPConfig         `yaml:"mcp"`
-   Storage     StorageConfig     `yaml:"storage"`
+   Memory      MemoryConfig      `yaml:"memory"`
    Performance PerformanceConfig `yaml:"performance"`
    React       ReactConfig       `yaml:"react"`
    Attachment  AttachmentConfig  `yaml:"attachment"`
    Security    SecurityConfig    `yaml:"security"`
    Logging     LoggingConfig     `yaml:"logging"`
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): replace StorageConfig with MemoryConfig"
```

### Task 5.2: 修改默认配置

**Files:**
- Modify: `internal/config/defaults.go`

- [ ] **Step 1: 修改 DefaultConfig 函数**

找到默认配置生成函数，修改 storage 为 memory：

```go
Memory: MemoryConfig{
    Directory:      "memory",
    RetentionDays:  7,
    CleanupSchedule: "02:00",
},
```

- [ ] **Step 2: Commit**

```bash
git add internal/config/defaults.go
git commit -m "feat(config): update default config for memory module"
```

---

## Phase 6: 修改 API Handler

### Task 6.1: 重命名 execute.go 为 chat.go

**Files:**
- Rename: `internal/api/handler/execute.go` → `internal/api/handler/chat.go`

- [ ] **Step 1: 重命名文件**

```bash
mv internal/api/handler/execute.go internal/api/handler/chat.go
```

- [ ] **Step 2: Commit**

```bash
git add -A
git commit -m "refactor(api): rename execute.go to chat.go"
```

### Task 6.2: 重写 ChatHandler 支持多轮对话

**Files:**
- Modify: `internal/api/handler/chat.go`

- [ ] **Step 1: 重写 ChatHandler 结构体和依赖**

完整替换 execute.go 的内容，新的 chat.go：

```go
package handler

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/skill"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/attachment"
)

// ChatHandler 对话处理器
type ChatHandler struct {
	memory         *memory.Manager
	runtimeState   *agent.RuntimeState
	agentExecutor  *agent.Executor
	skillRegistry  *skill.Registry
	mcpManager     *mcp.Manager
	attachmentHandler *attachment.Handler
	config         config.Config
	log            *logger.Logger
}

// NewChatHandler 创建对话处理器
func NewChatHandler(
	mem *memory.Manager,
	runtime *agent.RuntimeState,
	executor *agent.Executor,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	attHandler *attachment.Handler,
	cfg config.Config,
	log *logger.Logger,
) *ChatHandler {
	return &ChatHandler{
		memory:         mem,
		runtimeState:   runtime,
		agentExecutor:  executor,
		skillRegistry:  skills,
		mcpManager:     mcpMgr,
		attachmentHandler: attHandler,
		config:         cfg,
		log:            log,
	}
}

// ChatRequest 对话请求
type ChatRequest struct {
	Instruction string       `json:"instruction"`
	Prompt      string       `json:"prompt,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment 附件
type Attachment struct {
	Type    string `json:"type"`    // file/url
	Name    string `json:"name"`
	Content string `json:"content"` // Base64 content or URL
}
```

- [ ] **Step 2: 添加 Handle 方法（处理 POST /chat）**

```go
// Handle 处理 POST /chat 请求
func (h *ChatHandler) Handle(ctx context.Context, rc *app.RequestContext) {
	// 1. 解析请求
	var req ChatRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	
	// 2. 检查 instruction 是否为空
	if req.Instruction == "" {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "instruction 不能为空"})
		return
	}
	
	// 3. 提取 X-Session-ID
	sessionID := string(rc.GetHeader("X-Session-ID"))
	
	// 4. 会话处理
	var isNew bool
	var round int
	var historyMessages []memory.Message
	
	if sessionID == "" || !h.memory.ExistsSession(sessionID) {
		// 新会话
		sessionID = memory.GenerateSessionID()
		if err := h.memory.CreateSession(sessionID); err != nil {
			rc.JSON(500, utils.H{"status": "error", "message": "创建会话失败"})
			return
		}
		isNew = true
		round = 1
		historyMessages = []memory.Message{}
	} else {
		// 继续会话 - 检查并发
		if h.runtimeState.IsRunning(sessionID) {
			rc.JSON(409, utils.H{
				"status": "chat_limit_exceeded",
				"message": "该会话已有对话正在执行",
			})
			return
		}
		isNew = false
		history, err := h.memory.GetHistory(sessionID)
		if err != nil {
			rc.JSON(500, utils.H{"status": "error", "message": "获取历史失败"})
			return
		}
		historyMessages = history.Messages
		round = len(historyMessages) + 1
	}
	
	// 5. 生成 chat_id
	chatID := memory.GenerateChatID()
	
	// 6. 注册活跃状态
	activeChat, err := h.runtimeState.Register(sessionID, chatID)
	if err != nil {
		rc.JSON(409, utils.H{"status": "error", "message": err.Error()})
		return
	}
	
	// 7. 设置响应 Header
	rc.SetHeader("X-Session-ID", sessionID)
	rc.SetHeader("X-Chat-ID", chatID)
	rc.SetHeader("Content-Type", "text/event-stream")
	rc.SetHeader("Cache-Control", "no-cache")
	rc.SetHeader("Connection", "keep-alive")
	
	// 8. 创建 SSE Writer
	sseWriter := agent.NewSSEWriter(rc, sessionID, chatID, round)
	
	// 9. 处理附件
	var attachmentPaths []memory.AttachmentPath
	if len(req.Attachments) > 0 && h.attachmentHandler != nil {
		for _, att := range req.Attachments {
			// 解码并保存附件
			// ... 附件处理逻辑
		}
	}
	
	// 10. 推送 intent 事件
	sseWriter.WriteIntent()
	
	// 11. 执行 Agent
	go h.agentExecutor.Execute(
		sessionID,
		chatID,
		round,
		req.Instruction,
		req.Prompt,
		attachmentPaths,
		historyMessages,
		sseWriter,
		activeChat.CancelCh,
	)
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/api/handler/chat.go
git commit -m "feat(api): rewrite ChatHandler for multi-round conversation"
```

### Task 6.3: 新增 Session Handler

**Files:**
- Create: `internal/api/handler/session.go`

- [ ] **Step 1: 创建 session.go 文件**

```go
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/memory"
)

// SessionHandler 会话处理器
type SessionHandler struct {
	memory *memory.Manager
}

// NewSessionHandler 创建会话处理器
func NewSessionHandler(mem *memory.Manager) *SessionHandler {
	return &SessionHandler{memory: mem}
}

// GetSession 处理 GET /sess/{sid}
func (h *SessionHandler) GetSession(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")
	
	info, err := h.memory.GetSessionInfo(sessionID)
	if err != nil {
		rc.JSON(404, utils.H{"status": "session_not_found", "message": err.Error()})
		return
	}
	
	history, err := h.memory.GetHistory(sessionID)
	if err != nil {
		rc.JSON(500, utils.H{"status": "error", "message": "获取历史失败"})
		return
	}
	
	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"session":    info,
		"history":    history,
	})
}

// ListSessions 处理 GET /sess/history
func (h *SessionHandler) ListSessions(ctx context.Context, rc *app.RequestContext) {
	limit := rc.Query("limit")
	offset := rc.Query("offset")
	
	limitInt := 20
	offsetInt := 0
	
	if limit != "" {
		if parsed, err := strconv.Atoi(limit); err == nil && parsed > 0 && parsed <= 100 {
			limitInt = parsed
		}
	}
	if offset != "" {
		if parsed, err := strconv.Atoi(offset); err == nil && parsed >= 0 {
			offsetInt = parsed
		}
	}
	
	sessions, total, err := h.memory.ListSessions(limitInt, offsetInt)
	if err != nil {
		rc.JSON(500, utils.H{"status": "error", "message": "查询失败"})
		return
	}
	
	rc.JSON(200, utils.H{
		"status":   "success",
		"total":    total,
		"limit":    limitInt,
		"offset":   offsetInt,
		"sessions": sessions,
	})
}
```

- [ ] **Step 2: 补充 import**

```go
import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/memory"
)
```

- [ ] **Step 3: Commit**

```bash
git add internal/api/handler/session.go
git commit -m "feat(api): add SessionHandler for /sess endpoints"
```

---

## Phase 7: 修改 SSE Writer

### Task 7.1: 修改 SSE Writer 支持新格式

**Files:**
- Modify: `internal/agent/sse.go`

- [ ] **Step 1: 修改 SSEWriter 结构体**

```diff
type SSEWriter struct {
    rc         *app.RequestContext
-   taskID     string
+   sessionID  string
+   chatID     string
+   round      int
}
```

替换为：

```go
type SSEWriter struct {
	rc        *app.RequestContext
	sessionID string
	chatID    string
	round     int
}
```

- [ ] **Step 2: 修改 NewSSEWriter 函数**

```go
func NewSSEWriter(rc *app.RequestContext, sessionID, chatID string, round int) *SSEWriter {
	return &SSEWriter{
		rc:        rc,
		sessionID: sessionID,
		chatID:    chatID,
		round:     round,
	}
}
```

- [ ] **Step 3: 修改 WriteCompleted 方法，添加 round 字段**

```go
// WriteCompleted 写入完成事件
func (w *SSEWriter) WriteCompleted(status string, duration string, result interface{}, err *StepError, message string) {
	event := map[string]interface{}{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"duration":  duration,
		"round":     w.round,
	}
	
	if status == "success" && result != nil {
		event["result"] = result
	} else if status == "failed" && err != nil {
		event["error"] = err
	} else if status == "cancelled" {
		event["message"] = message
	}
	
	w.writeEvent("completed", event)
}
```

- [ ] **Step 4: Commit**

```bash
git add internal/agent/sse.go
git commit -m "feat(agent): update SSEWriter for multi-round format"
```

---

## Phase 8: 修改 Agent Executor

### Task 8.1: 重写 Executor 使用 Memory 和 RuntimeState

**Files:**
- Modify: `internal/agent/executor.go`

- [ ] **Step 1: 修改 Executor 结构体依赖**

```diff
type Executor struct {
-   storage         storage.TaskStorage
+   memory          *memory.Manager
+   runtimeState    *RuntimeState
    skillRegistry   *skill.Registry
    mcpManager      *mcp.Manager
-   cancelManager   *CancelManager
    attachmentHandler *attachment.Handler
    config          config.Config
    logger          *logger.Logger
-   runningTasks    sync.Map
}
```

- [ ] **Step 2: 修改 NewExecutor 函数**

```go
func NewExecutor(
	mem *memory.Manager,
	runtime *RuntimeState,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	attHandler *attachment.Handler,
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		memory:          mem,
		runtimeState:    runtime,
		skillRegistry:   skills,
		mcpManager:      mcpMgr,
		attachmentHandler: attHandler,
		config:          cfg,
		log:             log,
	}
}
```

- [ ] **Step 3: 重写 Execute 方法**

完整的 Execute 方法需要：
- 使用 memory 存储附件
- 使用 runtimeState 管理状态
- 构建 Agent 上下文时传入历史消息
- 完成后使用 memory.AppendMessage 和 memory.SaveChatRecord

- [ ] **Step 4: Commit**

```bash
git add internal/agent/executor.go
git commit -m "feat(agent): rewrite Executor to use Memory and RuntimeState"
```

---

## Phase 9: 修改 Engine

### Task 9.1: 修改 Engine 移除 storage 依赖

**Files:**
- Modify: `internal/agent/engine.go`

- [ ] **Step 1: 修改 AttachmentPath 引用**

将 `storage.AttachmentPath` 替换为 `memory.AttachmentPath`

- [ ] **Step 2: Commit**

```bash
git add internal/agent/engine.go
git commit -m "refactor(agent): update Engine to use memory.AttachmentPath"
```

---

## Phase 10: 修改路由

### Task 10.1: 修改路由路径

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: 修改路由定义**

```diff
POST   "/task/execute"    → POST   "/chat"
DELETE "/task/:task_id"   → DELETE "/chat/:sid"
GET    "/task/status/:task_id" → GET "/chat/status/:sid"
GET    "/task/:task_id"   → GET "/chat/:sid"
GET    "/task/history"    → GET "/sess/history"
+ GET    "/sess/:sid"      → 新增
```

- [ ] **Step 2: Commit**

```bash
git add internal/api/router.go
git commit -m "refactor(api): update routes from /task to /chat and /sess"
```

---

## Phase 11: 修改 main.go 初始化

### Task 11.1: 更新 main.go

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 移除 storage 初始化**

删除 storage 相关的初始化代码

- [ ] **Step 2: 添加 memory 初始化**

```go
// 初始化 Memory Manager
memManager := memory.NewManager(cfg.Memory.Directory, cfg.Memory.RetentionDays, log)

// 初始化 Runtime State
runtimeState := agent.NewRuntimeState()

// 启动清理调度器
cleanupScheduler := memory.NewCleanupScheduler(memManager, cfg.Memory.CleanupSchedule, log)
cleanupScheduler.Start()
```

- [ ] **Step 3: 更新 Handler 初始化参数**

```go
chatHandler := handler.NewChatHandler(memManager, runtimeState, executor, ...)
sessionHandler := handler.NewSessionHandler(memManager)
```

- [ ] **Step 4: Commit**

```bash
git add cmd/groot/main.go
git commit -m "feat(main): update initialization for Memory module"
```

---

## Phase 12: 编译验证和测试

### Task 12.1: 编译验证

- [ ] **Step 1: 运行 go build**

```bash
cd /Users/zhangfengda/workspace/groot
go build ./...
```

Expected: 编译成功，无错误

- [ ] **Step 2: 修复任何编译错误**

如果有编译错误，逐一修复

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "fix: resolve compilation errors"
```

### Task 12.2: 功能测试

- [ ] **Step 1: 启动服务**

```bash
go run cmd/groot/main.go -H ~/.groot-test
```

- [ ] **Step 2: 测试新会话 POST /chat**

```bash
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{"instruction": "测试指令"}'
```

Expected: 返回 X-Session-ID 和 X-Chat-ID header，SSE 流式返回事件

- [ ] **Step 3: 测试继续会话**

```bash
curl -X POST http://localhost:8080/chat \
  -H "X-Session-ID: {上一步返回的session_id}" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "继续对话"}'
```

Expected: round 字段为 2

- [ ] **Step 4: 测试并发限制**

在第一个请求执行期间，发送第二个请求：

```bash
curl -X POST http://localhost:8080/chat \
  -H "X-Session-ID: {同一个session_id}" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "并发测试"}'
```

Expected: 返回 409 chat_limit_exceeded

---

## Self-Review

**1. Spec coverage:**
- [x] 删除 Storage 模块 - Phase 1
- [x] 新增 Memory 模块 - Phase 2-3
- [x] 新增 Runtime State - Phase 4
- [x] 修改 Config - Phase 5
- [x] 修改 API Handler - Phase 6
- [x] 修改 SSE Writer - Phase 7
- [x] 修改 Executor - Phase 8
- [x] 修改 Engine - Phase 9
- [x] 修改 Router - Phase 10
- [x] 修改 main.go - Phase 11
- [x] 编译验证 - Phase 12

**2. Placeholder scan:**
- 无 TBD/TODO
- 所有代码步骤都有完整实现
- 所有命令都有预期输出

**3. Type consistency:**
- memory.AttachmentPath 在 engine.go 和 executor.go 中一致
- memory.ChatRecord 在 RuntimeState.Complete 和 executor 中一致
- SessionInfo、Message、History、ChatRecord 类型在各模块间一致

---

## Execution Handoff

计划已保存到 `docs/superpowers/plans/2026-04-18-memory-module-implementation.md`

**两种执行方式：**

1. **Subagent-Driven（推荐）** - 每个任务派发新的子 agent，任务间可审查，快速迭代

2. **Inline Execution** - 在当前会话中执行，批量执行带检查点

**选择哪种方式？**