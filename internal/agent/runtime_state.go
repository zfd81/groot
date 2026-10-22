package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zfd81/groot/internal/memory"
)

// ActiveChat 活跃对话状态
//
// 字段并发模型：
//   - SessionID / ChatID / StartTime：Register 时一次性写入，之后只读，无需保护
//   - Status：Register 时写入 "running"，Cancel 时在 mu 保护下翻为 "cancelled"；
//     读取请走 StatusSnapshot()（带读锁），不要直读字段，否则与 Cancel 并发会 race。
//   - ctx / cancel / done：Register 时一次性写入，之后只读。ctx 由 parentCtx 派生，
//     cancel 可被多次调用（context 保证幂等）；done 只由 Delete 关闭一次
//     （LoadAndDelete 保证只有一个调用者命中），Wait 通过它感知对话结束。
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

	// ctx 是本次对话的执行上下文（由 Register 的 parentCtx 派生），
	// 执行器全程使用它，外部通过 Cancel 触发取消。
	ctx    context.Context
	cancel context.CancelFunc
	// done 在 Delete 时关闭，表示执行协程已退出、状态已清理。
	done chan struct{}
}

// Context 返回本次对话的可取消上下文，供执行器使用。
func (c *ActiveChat) Context() context.Context {
	return c.ctx
}

// StatusSnapshot 在读锁保护下返回当前 Status，可与 Cancel 并发调用。
func (c *ActiveChat) StatusSnapshot() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Status
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
//
// 会从 parentCtx 派生一个可取消的子上下文挂到 ActiveChat 上：父上下文取消
// （如请求断开）或外部调用 Cancel 都会让执行器收到取消信号。
func (r *RuntimeState) Register(parentCtx context.Context, sessionID, chatID string) (*ActiveChat, error) {
	ctx, cancel := context.WithCancel(parentCtx)
	chat := &ActiveChat{
		SessionID: sessionID,
		ChatID:    chatID,
		Status:    "running",
		Progress:  &ChatProgress{},
		StartTime: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}

	// 使用 LoadOrStore 确保原子性
	// 如果已存在，返回 existing 和 loaded=true
	// 如果不存在，存储新值并返回 stored 和 loaded=false
	existing, loaded := r.activeChats.LoadOrStore(sessionID, chat)
	if loaded {
		// 已有活跃对话：释放刚派生的 ctx，返回错误
		cancel()
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

// Cancel 请求取消 session 当前的活跃对话。
//
// chatID 为空时匹配任意活跃对话；非空时必须与 ActiveChat.ChatID 一致，否则返回 false
// （避免误停下一轮对话）。session 不存在返回 false。
//
// 命中时在 mu 保护下把 Status 翻为 "cancelled" 并取消 ctx，但**不删除**状态：
// 状态由执行协程感知取消后退出时调用 Delete 清理，调用方可通过 Wait 等待该时刻。
func (r *RuntimeState) Cancel(sessionID, chatID string) bool {
	v, ok := r.activeChats.Load(sessionID)
	if !ok {
		return false
	}
	chat := v.(*ActiveChat)
	if chatID != "" && chat.ChatID != chatID {
		return false
	}
	chat.mu.Lock()
	chat.Status = "cancelled"
	chat.mu.Unlock()
	chat.cancel()
	return true
}

// Wait 阻塞直到 session 的活跃对话被 Delete（执行协程退出）或超时。
// 无活跃对话立即返回 true；超时返回 false。
func (r *RuntimeState) Wait(sessionID string, timeout time.Duration) bool {
	v, ok := r.activeChats.Load(sessionID)
	if !ok {
		return true
	}
	chat := v.(*ActiveChat)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-chat.done:
		return true
	case <-timer.C:
		return false
	}
}

// Delete 移除 session 的活跃对话状态，并取消 ctx（释放 context 资源）、关闭 done
// 唤醒所有 Wait 调用者。
//
// 使用 LoadAndDelete 保证同一个 ActiveChat 只会被一个调用者命中，因此重复 Delete
// 不会二次 close(done)；session 不存在时是 no-op。
func (r *RuntimeState) Delete(sessionID string) {
	v, ok := r.activeChats.LoadAndDelete(sessionID)
	if !ok {
		return
	}
	chat := v.(*ActiveChat)
	chat.cancel()
	close(chat.done)
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
