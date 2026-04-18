package agent

import (
	"crypto/rand"
	"encoding/hex"
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

// GenerateTaskID generates a unique task ID
// Format: task-{YYYYMMDD}-{HHMMSSmmm}-{random4}
func GenerateTaskID() string {
	now := time.Now()
	datePart := now.Format("20060102")
	timePart := now.Format("150405.000")
	timePart = timePart[:9] // HHMMSSmmm (remove the dot)

	random := generateRandomHex(4)

	return fmt.Sprintf("task-%s-%s-%s", datePart, timePart, random)
}

// GenerateStepID generates a unique step ID
// Format: {YYYYMMDD}-{HHMMSSmmm}-{random6}
func GenerateStepID() string {
	now := time.Now()
	datePart := now.Format("20060102")
	timePart := now.Format("150405.000")
	timePart = timePart[:9] // HHMMSSmmm (remove the dot)

	random := generateRandomHex(6)

	return fmt.Sprintf("%s-%s-%s", datePart, timePart, random)
}

// generateRandomHex creates random hex string of given length
func generateRandomHex(length int) string {
	bytes := make([]byte, length/2+1)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)[:length]
}

// CancelManager manages task cancellation (retained for backward compatibility)
type CancelManager struct {
	cancellations map[string]chan struct{}
	mu            sync.RWMutex
}

// NewCancelManager creates a new cancel manager
func NewCancelManager() *CancelManager {
	return &CancelManager{
		cancellations: make(map[string]chan struct{}),
	}
}

// Register registers a task for cancellation tracking
func (c *CancelManager) Register(taskID string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch := make(chan struct{})
	c.cancellations[taskID] = ch
	return ch
}

// Cancel cancels a task
func (c *CancelManager) Cancel(taskID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ch, ok := c.cancellations[taskID]; ok {
		close(ch)
		delete(c.cancellations, taskID)
		return true
	}
	return false
}

// Unregister removes task from cancellation tracking
func (c *CancelManager) Unregister(taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.cancellations[taskID]; ok {
		// Don't close the channel - let the task finish naturally
		delete(c.cancellations, taskID)
	}
}

// IsCancelled checks if a task is cancelled
func (c *CancelManager) IsCancelled(taskID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.cancellations[taskID]
	return !ok // If not in map, it was either cancelled or finished
}

// Count returns number of tracked tasks
func (c *CancelManager) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cancellations)
}