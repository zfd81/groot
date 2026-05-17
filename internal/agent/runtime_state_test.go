package agent

import (
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

func TestRuntimeState_Get_Nonexistent(t *testing.T) {
	state := NewRuntimeState()

	_, ok := state.Get("nonexistent")
	if ok {
		t.Error("Get() should return false for nonexistent session")
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

	// Delete 后不应 running
	state.Delete(sessionID)

	if state.IsRunning(sessionID) {
		t.Error("IsRunning() should return false after Delete()")
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

	// Delete 一个
	state.Delete("session_1")

	if state.RunningCount() != 4 {
		t.Errorf("RunningCount() after 1 delete = %d, want 4", state.RunningCount())
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
