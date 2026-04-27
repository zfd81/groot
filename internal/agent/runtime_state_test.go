package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/memory"
)

func TestNewRuntimeState(t *testing.T) {
	state := NewRuntimeState()
	if state == nil {
		t.Fatal("NewRuntimeState() returned nil")
	}
}

func TestRuntimeState_Register(t *testing.T) {
	state := NewRuntimeState()

	sessionID := "session_001"
	chatID := "chat_001"

	chat, err := state.Register(sessionID, chatID)
	if err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	if chat.SessionID != sessionID {
		t.Errorf("Register().SessionID = %s, want %s", chat.SessionID, sessionID)
	}

	if chat.ChatID != chatID {
		t.Errorf("Register().ChatID = %s, want %s", chat.ChatID, chatID)
	}

	if chat.Status != "running" {
		t.Errorf("Register().Status = %s, want running", chat.Status)
	}

	if chat.CancelCh == nil {
		t.Error("Register().CancelCh should not be nil")
	}
}

func TestRuntimeState_Register_Duplicate(t *testing.T) {
	state := NewRuntimeState()

	sessionID := "session_001"

	// 第一次注册
	_, err := state.Register(sessionID, "chat_001")
	if err != nil {
		t.Fatalf("First Register() failed: %v", err)
	}

	// 第二次注册同一 session 应失败
	_, err = state.Register(sessionID, "chat_002")
	if err == nil {
		t.Error("Second Register() should fail for duplicate session")
	}
}

func TestRuntimeState_Get(t *testing.T) {
	state := NewRuntimeState()

	// 未注册时获取
	_, ok := state.Get("nonexistent")
	if ok {
		t.Error("Get() should return false for nonexistent session")
	}

	// 注册后获取
	sessionID := "session_001"
	state.Register(sessionID, "chat_001")

	chat, ok := state.Get(sessionID)
	if !ok {
		t.Error("Get() should return true for registered session")
	}

	if chat.SessionID != sessionID {
		t.Errorf("Get().SessionID = %s, want %s", chat.SessionID, sessionID)
	}
}

func TestRuntimeState_UpdateProgress(t *testing.T) {
	state := NewRuntimeState()

	sessionID := "session_001"
	state.Register(sessionID, "chat_001")

	progress := &ChatProgress{
		CurrentStep:    5,
		StepsCompleted: 4,
		Percentage:     80,
	}

	err := state.UpdateProgress(sessionID, progress)
	if err != nil {
		t.Fatalf("UpdateProgress() failed: %v", err)
	}

	chat, _ := state.Get(sessionID)
	if chat.Progress.Percentage != 80 {
		t.Errorf("UpdateProgress().Percentage = %d, want 80", chat.Progress.Percentage)
	}
}

func TestRuntimeState_UpdateProgress_Nonexistent(t *testing.T) {
	state := NewRuntimeState()

	err := state.UpdateProgress("nonexistent", &ChatProgress{})
	if err == nil {
		t.Error("UpdateProgress() should fail for nonexistent session")
	}
}

func TestRuntimeState_Cancel(t *testing.T) {
	state := NewRuntimeState()

	sessionID := "session_001"
	state.Register(sessionID, "chat_001")

	err := state.Cancel(sessionID)
	if err != nil {
		t.Fatalf("Cancel() failed: %v", err)
	}

	chat, _ := state.Get(sessionID)
	if chat.Status != "cancelled" {
		t.Errorf("Cancel().Status = %s, want cancelled", chat.Status)
	}

	// 验证 CancelCh 已关闭
	select {
	case <-chat.CancelCh:
		// 正常，channel 已关闭
	default:
		t.Error("CancelCh should be closed after Cancel()")
	}
}

func TestRuntimeState_Cancel_MultipleCalls(t *testing.T) {
	state := NewRuntimeState()

	sessionID := "session_001"
	state.Register(sessionID, "chat_001")

	// 第一次取消
	err := state.Cancel(sessionID)
	if err != nil {
		t.Fatalf("First Cancel() failed: %v", err)
	}

	// 第二次取消同一 session（使用 sync.Once，不应 panic）
	err = state.Cancel(sessionID)
	if err != nil {
		t.Fatalf("Second Cancel() failed: %v", err)
	}

	chat, _ := state.Get(sessionID)
	if chat.Status != "cancelled" {
		t.Errorf("Status after multiple Cancel() = %s, want cancelled", chat.Status)
	}
}

func TestRuntimeState_Cancel_Nonexistent(t *testing.T) {
	state := NewRuntimeState()

	err := state.Cancel("nonexistent")
	if err == nil {
		t.Error("Cancel() should fail for nonexistent session")
	}
}

func TestRuntimeState_Complete(t *testing.T) {
	state := NewRuntimeState()

	sessionID := "session_001"
	state.Register(sessionID, "chat_001")

	result := &ChatResult{
		Status:            "completed",
		Result:            "test result",
		ResultAttachments: []string{"file1.txt"},
		Duration:          1000,
		Steps:             []memory.Step{},
	}

	record, err := state.Complete(sessionID, result)
	if err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}

	if record.ChatID != "chat_001" {
		t.Errorf("Complete().ChatID = %s, want chat_001", record.ChatID)
	}

	if record.Result != "test result" {
		t.Errorf("Complete().Result = %s, want test result", record.Result)
	}

	// 验证会话已从活跃列表删除
	_, ok := state.Get(sessionID)
	if ok {
		t.Error("Complete() should remove session from activeChats")
	}
}

func TestRuntimeState_Complete_Nonexistent(t *testing.T) {
	state := NewRuntimeState()

	result := &ChatResult{Status: "completed"}

	_, err := state.Complete("nonexistent", result)
	if err == nil {
		t.Error("Complete() should fail for nonexistent session")
	}
}

func TestRuntimeState_Delete(t *testing.T) {
	state := NewRuntimeState()

	sessionID := "session_001"
	state.Register(sessionID, "chat_001")

	state.Delete(sessionID)

	_, ok := state.Get(sessionID)
	if ok {
		t.Error("Delete() should remove session from activeChats")
	}
}

func TestRuntimeState_Delete_Nonexistent(t *testing.T) {
	state := NewRuntimeState()

	// 删除不存在 session 不应报错
	state.Delete("nonexistent")
}

func TestRuntimeState_IsRunning(t *testing.T) {
	state := NewRuntimeState()

	// 未注册时不应 running
	if state.IsRunning("nonexistent") {
		t.Error("IsRunning() should return false for nonexistent session")
	}

	// 注册后应 running
	sessionID := "session_001"
	state.Register(sessionID, "chat_001")

	if !state.IsRunning(sessionID) {
		t.Error("IsRunning() should return true for registered session")
	}

	// Complete 后不应 running
	state.Complete(sessionID, &ChatResult{Status: "completed"})

	if state.IsRunning(sessionID) {
		t.Error("IsRunning() should return false after Complete()")
	}
}

func TestRuntimeState_RunningCount(t *testing.T) {
	state := NewRuntimeState()

	// 初始为 0
	if state.RunningCount() != 0 {
		t.Errorf("RunningCount() initial = %d, want 0", state.RunningCount())
	}

	// 注册多个
	for i := 1; i <= 5; i++ {
		sessionID := "session_" + string(rune('0'+i))
		state.Register(sessionID, "chat_"+string(rune('0'+i)))
	}

	if state.RunningCount() != 5 {
		t.Errorf("RunningCount() after 5 registers = %d, want 5", state.RunningCount())
	}

	// Complete 一个
	state.Complete("session_1", &ChatResult{Status: "completed"})

	if state.RunningCount() != 4 {
		t.Errorf("RunningCount() after 1 complete = %d, want 4", state.RunningCount())
	}
}

func TestGenerateTaskID(t *testing.T) {
	id := GenerateTaskID()

	// 验证格式: task-{YYYYMMDD}-{HHMMSSmmm}-{random4}
	if !strings.HasPrefix(id, "task-") {
		t.Errorf("GenerateTaskID() = %s, should start with 'task-'", id)
	}

	parts := strings.Split(id, "-")
	if len(parts) != 4 {
		t.Errorf("GenerateTaskID() format error: should have 4 parts, got %d", len(parts))
	}

	// 日期部分 8 位
	if len(parts[1]) != 8 {
		t.Errorf("GenerateTaskID() date part length = %d, want 8", len(parts[1]))
	}

	// 时间部分 9 位 (HHMMSSmmm)
	if len(parts[2]) != 9 {
		t.Errorf("GenerateTaskID() time part length = %d, want 9", len(parts[2]))
	}

	// 随机部分 4 位
	if len(parts[3]) != 4 {
		t.Errorf("GenerateTaskID() random part length = %d, want 4", len(parts[3]))
	}
}

func TestGenerateTaskID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateTaskID()
		if ids[id] {
			t.Errorf("GenerateTaskID() generated duplicate ID: %s", id)
		}
		ids[id] = true
		time.Sleep(1 * time.Millisecond) // 确保时间戳不同
	}
}

func TestGenerateStepID(t *testing.T) {
	id := GenerateStepID()

	// 验证格式: {YYYYMMDD}-{HHMMSSmmm}-{random6}
	parts := strings.Split(id, "-")
	if len(parts) != 3 {
		t.Errorf("GenerateStepID() format error: should have 3 parts, got %d", len(parts))
	}

	// 日期部分 8 位
	if len(parts[0]) != 8 {
		t.Errorf("GenerateStepID() date part length = %d, want 8", len(parts[0]))
	}

	// 时间部分 9 位
	if len(parts[1]) != 9 {
		t.Errorf("GenerateStepID() time part length = %d, want 9", len(parts[1]))
	}

	// 随机部分 6 位
	if len(parts[2]) != 6 {
		t.Errorf("GenerateStepID() random part length = %d, want 6", len(parts[2]))
	}
}

func TestGenerateStepID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateStepID()
		if ids[id] {
			t.Errorf("GenerateStepID() generated duplicate ID: %s", id)
		}
		ids[id] = true
		time.Sleep(1 * time.Millisecond) // 确保时间戳不同
	}
}

func TestCancelManager_Register(t *testing.T) {
	mgr := NewCancelManager()

	taskID := "task_001"
	ch := mgr.Register(taskID)

	if ch == nil {
		t.Error("Register() returned nil channel")
	}

	if mgr.Count() != 1 {
		t.Errorf("Count() after register = %d, want 1", mgr.Count())
	}
}

func TestCancelManager_Cancel(t *testing.T) {
	mgr := NewCancelManager()

	taskID := "task_001"
	ch := mgr.Register(taskID)

	// 取消任务
	result := mgr.Cancel(taskID)
	if !result {
		t.Error("Cancel() should return true for registered task")
	}

	// 验证 channel 已关闭
	select {
	case <-ch:
		// 正常
	default:
		t.Error("Channel should be closed after Cancel()")
	}

	// 任务应从 map 中删除
	if mgr.Count() != 0 {
		t.Errorf("Count() after cancel = %d, want 0", mgr.Count())
	}
}

func TestCancelManager_Cancel_Nonexistent(t *testing.T) {
	mgr := NewCancelManager()

	result := mgr.Cancel("nonexistent")
	if result {
		t.Error("Cancel() should return false for nonexistent task")
	}
}

func TestCancelManager_Unregister(t *testing.T) {
	mgr := NewCancelManager()

	taskID := "task_001"
	ch := mgr.Register(taskID)

	// Unregister 不关闭 channel
	mgr.Unregister(taskID)

	// Channel 仍开放
	select {
	case <-ch:
		t.Error("Channel should still be open after Unregister()")
	default:
		// 正常
	}

	// 任务已删除
	if mgr.Count() != 0 {
		t.Errorf("Count() after unregister = %d, want 0", mgr.Count())
	}
}

func TestCancelManager_IsCancelled(t *testing.T) {
	mgr := NewCancelManager()

	taskID := "task_001"
	mgr.Register(taskID)

	// 注册后，任务在 map 中，IsCancelled 返回 false
	if mgr.IsCancelled(taskID) {
		t.Error("IsCancelled() should return false for registered task")
	}

	// 取消后，任务不在 map 中，IsCancelled 返回 true
	mgr.Cancel(taskID)
	if !mgr.IsCancelled(taskID) {
		t.Error("IsCancelled() should return true after Cancel()")
	}
}

func TestCancelManager_Count(t *testing.T) {
	mgr := NewCancelManager()

	// 初始为 0
	if mgr.Count() != 0 {
		t.Errorf("Count() initial = %d, want 0", mgr.Count())
	}

	// 注册多个
	for i := 1; i <= 5; i++ {
		mgr.Register("task_" + string(rune('0'+i)))
	}

	if mgr.Count() != 5 {
		t.Errorf("Count() after 5 registers = %d, want 5", mgr.Count())
	}

	// 取消一个
	mgr.Cancel("task_1")

	if mgr.Count() != 4 {
		t.Errorf("Count() after 1 cancel = %d, want 4", mgr.Count())
	}

	// Unregister 一个
	mgr.Unregister("task_2")

	if mgr.Count() != 3 {
		t.Errorf("Count() after 1 unregister = %d, want 3", mgr.Count())
	}
}

func TestCancelManager_Concurrent(t *testing.T) {
	mgr := NewCancelManager()

	var wg sync.WaitGroup

	// 并发注册 10 个任务
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			taskID := "task_" + string(rune('0'+idx))
			mgr.Register(taskID)
		}(i)
	}

	wg.Wait()

	if mgr.Count() != 10 {
		t.Errorf("Count() after concurrent registers = %d, want 10", mgr.Count())
	}

	// 并发取消 5 个
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			taskID := "task_" + string(rune('0'+idx))
			mgr.Cancel(taskID)
		}(i)
	}

	wg.Wait()

	if mgr.Count() != 5 {
		t.Errorf("Count() after concurrent cancels = %d, want 5", mgr.Count())
	}
}

func TestActiveChat_Progress(t *testing.T) {
	chat := &ActiveChat{
		SessionID: "session_001",
		ChatID:    "chat_001",
		Status:    "running",
		Progress:  &ChatProgress{},
		StartTime: time.Now(),
	}

	// 更新进度
	chat.Progress.CurrentStep = 3
	chat.Progress.StepsCompleted = 2
	chat.Progress.Percentage = 66

	if chat.Progress.Percentage != 66 {
		t.Errorf("Progress.Percentage = %d, want 66", chat.Progress.Percentage)
	}
}

func TestChatResult(t *testing.T) {
	result := &ChatResult{
		Status:            "completed",
		Result:            "test result content",
		ResultAttachments: []string{"file1.txt", "file2.txt"},
		Duration:          5000,
		Steps: []memory.Step{
			{StepID: "step_001", Type: "tool", Name: "test_tool", Status: "success"},
		},
	}

	if result.Status != "completed" {
		t.Errorf("ChatResult.Status = %s, want completed", result.Status)
	}

	if len(result.ResultAttachments) != 2 {
		t.Errorf("ChatResult.ResultAttachments length = %d, want 2", len(result.ResultAttachments))
	}

	if len(result.Steps) != 1 {
		t.Errorf("ChatResult.Steps length = %d, want 1", len(result.Steps))
	}
}