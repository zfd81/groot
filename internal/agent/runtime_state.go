package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/zfd81/groot/internal/memory"
)

// ActiveChat 活跃对话状态
type ActiveChat struct {
	SessionID  string        `json:"session_id"`
	ChatID     string        `json:"chat_id"`
	Status     string        `json:"status"` // running, cancelled, completed
	Progress   *ChatProgress `json:"progress"`
	StartTime  time.Time     `json:"start_time"`
	CancelCh   chan struct{} `json:"-"`     // 取消信号通道
	cancelOnce sync.Once      `json:"-"`     // 确保 channel 只 close 一次
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
		CancelCh:  make(chan struct{}),
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
	// 使用 sync.Once 确保 channel 只被 close 一次，防止重复调用导致 panic
	chat.cancelOnce.Do(func() {
		close(chat.CancelCh)
	})
	return nil
}

// Delete removes active chat state for a session
func (r *RuntimeState) Delete(sessionID string) {
	r.activeChats.Delete(sessionID)
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
