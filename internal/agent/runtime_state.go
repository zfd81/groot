package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/zfd81/groot/internal/memory"
)

// ActiveChat 活跃对话状态
//
// 字段并发模型：
//   - SessionID / ChatID / StartTime：Register 时一次性写入，之后只读，无需保护
//   - Status：当前路径上只在 Register 时写一次（"running"），cancel/complete 走 Delete
//     而非状态翻转，所以 handler 直读不会竞态。**未来若需要在运行中翻转 Status，
//     必须把它纳入 mu 保护范围**，否则会出现 race。
//   - Progress（含 SubAgents slice）：多个 call_agent 子工具并发 Add/RemoveSubAgent
//     与 status handler 序列化并发，必须由 mu 串行化。AddSubAgent 用 copy+append
//     而非原地 append，目的是让 SnapshotProgress 之前快照持有的旧底层数组不被污染。
//
// `json:"-"` 让 mu 不出现在 JSON 输出中（RWMutex 本身没有导出字段，encoding/json
// 默认也会跳过，但显式写明更清晰）。
type ActiveChat struct {
	SessionID string        `json:"session_id"`
	ChatID    string        `json:"chat_id"`
	Status    string        `json:"status"` // running, cancelled, completed
	Progress  *ChatProgress `json:"progress"`
	StartTime time.Time     `json:"start_time"`

	mu sync.RWMutex `json:"-"`
}

// ChatProgress 对话进度
type ChatProgress struct {
	CurrentStep    int                `json:"current_step"`
	StepsCompleted int                `json:"steps_completed"`
	Percentage     int                `json:"percentage"`
	SubAgents      []SubAgentProgress `json:"sub_agents,omitempty"`
}

// SubAgentProgress 单个子 Agent 的运行时状态。
type SubAgentProgress struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ChatResult 对话结果
type ChatResult struct {
	Status   string
	Result   string
	Duration int
	Steps    []memory.Step
	Error    *memory.Error
}

// RuntimeState 运行时状态管理器
type RuntimeState struct {
	activeChats sync.Map // session_id -> *ActiveChat
}

// NewRuntimeState 创建 Runtime State
func NewRuntimeState() *RuntimeState {
	return &RuntimeState{}
}

// Register 注册活跃对话（原子操作，防止并发冲突）
func (r *RuntimeState) Register(sessionID, chatID string) (*ActiveChat, error) {
	chat := &ActiveChat{
		SessionID: sessionID,
		ChatID:    chatID,
		Status:    "running",
		Progress:  &ChatProgress{},
		StartTime: time.Now(),
	}

	// 使用 LoadOrStore 确保原子性
	// 如果已存在，返回 existing 和 loaded=true
	// 如果不存在，存储新值并返回 stored 和 loaded=false
	existing, loaded := r.activeChats.LoadOrStore(sessionID, chat)
	if loaded {
		// 已有活跃对话，返回错误
		return nil, fmt.Errorf("session %s already has running chat", sessionID)
	}

	// 成功注册新对话
	return existing.(*ActiveChat), nil
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

	chat.mu.Lock()
	defer chat.mu.Unlock()
	chat.Progress = progress
	return nil
}

// Delete removes active chat state for a session
func (r *RuntimeState) Delete(sessionID string) {
	r.activeChats.Delete(sessionID)
}

// SnapshotProgress 返回 Progress 的深拷贝；handler 拿到的 SubAgents slice 与
// 内部脱钩，可安全并发遍历/序列化。session 不存在时返回 nil。
func (r *RuntimeState) SnapshotProgress(sessionID string) *ChatProgress {
	v, ok := r.activeChats.Load(sessionID)
	if !ok {
		return nil
	}
	chat := v.(*ActiveChat)
	chat.mu.RLock()
	defer chat.mu.RUnlock()
	if chat.Progress == nil {
		return &ChatProgress{}
	}
	cp := *chat.Progress
	if len(chat.Progress.SubAgents) > 0 {
		cp.SubAgents = make([]SubAgentProgress, len(chat.Progress.SubAgents))
		copy(cp.SubAgents, chat.Progress.SubAgents)
	}
	return &cp
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

// AddSubAgent 标记一个子 Agent 正在运行；不存在 active chat 时是 no-op。
//
// 实现：分配新 slice 后 copy+append，而非原地 append。这样即使 SnapshotProgress
// 已经返回了旧 SubAgents 的引用（持锁后逃逸到 handler 序列化期间），新的写入也
// 不会触碰旧底层数组——配合 mu.Lock() 共同保证读写隔离。
func (r *RuntimeState) AddSubAgent(sessionID, name string) {
	v, ok := r.activeChats.Load(sessionID)
	if !ok {
		return
	}
	chat := v.(*ActiveChat)
	chat.mu.Lock()
	defer chat.mu.Unlock()
	if chat.Progress == nil {
		chat.Progress = &ChatProgress{}
	}
	old := chat.Progress.SubAgents
	next := make([]SubAgentProgress, 0, len(old)+1)
	next = append(next, old...)
	next = append(next, SubAgentProgress{Name: name, Status: "running"})
	chat.Progress.SubAgents = next
}

// RemoveSubAgent 把对应条目从 list 中删除（按 name 全部匹配）。
// 不存在 active chat 或 Progress 为 nil 时是 no-op。
//
// 实现：分配新 slice，不复用底层数组——并发场景下其它 goroutine 可能正在通过
// SnapshotProgress 持有旧 slice 进行序列化，原地覆盖会让它们读到不一致状态。
func (r *RuntimeState) RemoveSubAgent(sessionID, name string) {
	v, ok := r.activeChats.Load(sessionID)
	if !ok {
		return
	}
	chat := v.(*ActiveChat)
	chat.mu.Lock()
	defer chat.mu.Unlock()
	if chat.Progress == nil {
		return
	}
	old := chat.Progress.SubAgents
	filtered := make([]SubAgentProgress, 0, len(old))
	for _, sp := range old {
		if sp.Name != name {
			filtered = append(filtered, sp)
		}
	}
	chat.Progress.SubAgents = filtered
}
