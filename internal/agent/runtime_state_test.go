package agent

import (
	"context"
	"fmt"
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

	chat, err := state.Register(context.Background(), sessionID, chatID)
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
	_, err := state.Register(context.Background(), sessionID, "chat_001")
	if err != nil {
		t.Fatalf("First Register() failed: %v", err)
	}

	// 第二次注册同一 session 应失败
	_, err = state.Register(context.Background(), sessionID, "chat_002")
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
	state.Register(context.Background(), sessionID, "chat_001")

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
	state.Register(context.Background(), sessionID, "chat_001")

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
	state.Register(context.Background(), sessionID, "chat_001")

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
	state.Register(context.Background(), sessionID, "chat_001")

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
		state.Register(context.Background(), sessionID, "chat_"+string(rune('0'+i)))
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

// TestRuntimeState_SubAgentTracking 验证 AddSubAgent/RemoveSubAgent 正确维护
// ChatProgress.SubAgents 列表，且对未注册的 session 是 no-op。
func TestRuntimeState_SubAgentTracking(t *testing.T) {
	r := NewRuntimeState()
	_, _ = r.Register(context.Background(), "sess", "chat")
	r.AddSubAgent("sess", "db-agent")
	r.AddSubAgent("sess", "weather-agent")
	c, _ := r.Get("sess")
	if len(c.Progress.SubAgents) != 2 {
		t.Fatalf("expected 2 sub_agents, got %d", len(c.Progress.SubAgents))
	}
	r.RemoveSubAgent("sess", "db-agent")
	c, _ = r.Get("sess")
	if len(c.Progress.SubAgents) != 1 || c.Progress.SubAgents[0].Name != "weather-agent" {
		t.Fatalf("expected only weather-agent, got %+v", c.Progress.SubAgents)
	}

	// No active chat → no-op (no panic)
	r.AddSubAgent("missing", "x")
	r.RemoveSubAgent("missing", "x")
}

// TestRuntimeState_AddSubAgentConcurrent 验证多 goroutine 并发 AddSubAgent 不丢条目。
//
// 在不加锁的旧实现下，原地 append 共享底层数组会导致写覆盖；race detector 也会
// 直接报错。本测试在 -race 模式下若 AddSubAgent 没有 mu.Lock 必失败。
func TestRuntimeState_AddSubAgentConcurrent(t *testing.T) {
	rt := NewRuntimeState()
	if _, err := rt.Register(context.Background(), "sess", "chat"); err != nil {
		t.Fatal(err)
	}
	const N = 100
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rt.AddSubAgent("sess", fmt.Sprintf("agent-%d", i))
		}(i)
	}
	wg.Wait()

	snap := rt.SnapshotProgress("sess")
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	if len(snap.SubAgents) != N {
		t.Errorf("expected %d sub_agents, got %d", N, len(snap.SubAgents))
	}
}

// TestRuntimeState_SnapshotIsolatesFromMutation 验证 SnapshotProgress 返回的 slice
// 不会被后续 Add/Remove 影响——保证 handler 在序列化期间不会读到中间状态。
func TestRuntimeState_SnapshotIsolatesFromMutation(t *testing.T) {
	rt := NewRuntimeState()
	if _, err := rt.Register(context.Background(), "sess", "chat"); err != nil {
		t.Fatal(err)
	}
	rt.AddSubAgent("sess", "a")
	rt.AddSubAgent("sess", "b")
	snap := rt.SnapshotProgress("sess")
	if len(snap.SubAgents) != 2 {
		t.Fatalf("expected 2, got %d", len(snap.SubAgents))
	}
	rt.AddSubAgent("sess", "c")
	rt.RemoveSubAgent("sess", "a")
	if len(snap.SubAgents) != 2 {
		t.Errorf("snapshot should be isolated; got %d", len(snap.SubAgents))
	}
}

// TestRuntimeState_SnapshotProgress_Nonexistent 验证 session 不存在时返回 nil。
func TestRuntimeState_SnapshotProgress_Nonexistent(t *testing.T) {
	rt := NewRuntimeState()
	if snap := rt.SnapshotProgress("nope"); snap != nil {
		t.Errorf("expected nil snapshot, got %+v", snap)
	}
}

func TestChatResult(t *testing.T) {
	result := &ChatResult{
		Status:   "completed",
		Result:   "test result content",
		Duration: 5000,
		Steps: []memory.Step{
			{StepID: "step_001", Type: "tool", Name: "test_tool", Status: "success"},
		},
	}

	if result.Status != "completed" {
		t.Errorf("ChatResult.Status = %s, want completed", result.Status)
	}

	if len(result.Steps) != 1 {
		t.Errorf("ChatResult.Steps length = %d, want 1", len(result.Steps))
	}
}

// TestRuntimeState_Cancel 验证 Cancel 的匹配语义：chat_id 不匹配 / session 不存在时
// 返回 false 且不触发取消；匹配时取消 ctx、把 Status 翻为 cancelled，但不删除状态
// （状态由执行协程退出时 Delete 清理）。
func TestRuntimeState_Cancel(t *testing.T) {
	rt := NewRuntimeState()
	chat, _ := rt.Register(context.Background(), "s1", "c1")
	if rt.Cancel("s1", "other") {
		t.Fatal("wrong cid must not cancel")
	}
	if rt.Cancel("missing", "c1") {
		t.Fatal("missing session must not cancel")
	}
	if !rt.Cancel("s1", "c1") {
		t.Fatal("matching cid must cancel")
	}
	select {
	case <-chat.Context().Done():
	default:
		t.Fatal("ctx not cancelled")
	}
	if chat.StatusSnapshot() != "cancelled" {
		t.Errorf("status=%q", chat.StatusSnapshot())
	}
	if _, ok := rt.Get("s1"); !ok {
		t.Fatal("Cancel must not delete state")
	}
}

// TestRuntimeState_CancelEmptyChatIDMatchesAny 验证 chat_id 为空时匹配任意活跃对话。
func TestRuntimeState_CancelEmptyChatIDMatchesAny(t *testing.T) {
	rt := NewRuntimeState()
	chat, _ := rt.Register(context.Background(), "s1", "c1")
	if !rt.Cancel("s1", "") {
		t.Fatal("empty cid must match")
	}
	select {
	case <-chat.Context().Done():
	default:
		t.Fatal("ctx not cancelled")
	}
}

// TestRuntimeState_ParentCancelPropagates 验证 Register 派生的子 ctx 跟随父 ctx 取消。
func TestRuntimeState_ParentCancelPropagates(t *testing.T) {
	rt := NewRuntimeState()
	parent, cancel := context.WithCancel(context.Background())
	chat, _ := rt.Register(parent, "s1", "c1")
	cancel()
	select {
	case <-chat.Context().Done():
	default:
		t.Fatal("child ctx must follow parent")
	}
}

// TestRuntimeState_Wait 验证 Wait：无活跃对话立即返回 true；活跃对话超时返回 false；
// Delete 释放等待者；二次 Delete 不 panic。
func TestRuntimeState_Wait(t *testing.T) {
	rt := NewRuntimeState()
	if !rt.Wait("none", 10*time.Millisecond) {
		t.Fatal("no active chat → true immediately")
	}
	rt.Register(context.Background(), "s1", "c1")
	if rt.Wait("s1", 20*time.Millisecond) {
		t.Fatal("active chat must time out")
	}
	go func() { time.Sleep(10 * time.Millisecond); rt.Delete("s1") }()
	if !rt.Wait("s1", time.Second) {
		t.Fatal("Delete must release Wait")
	}
	rt.Delete("s1") // 二次 Delete 不 panic
}

// TestRuntimeState_DeleteCancelsContext 验证 Delete 会取消 ctx（释放 context 资源），
// 避免执行协程异常退出时派生 ctx 泄漏。
func TestRuntimeState_DeleteCancelsContext(t *testing.T) {
	rt := NewRuntimeState()
	chat, _ := rt.Register(context.Background(), "s1", "c1")
	rt.Delete("s1")
	select {
	case <-chat.Context().Done():
	default:
		t.Fatal("Delete must cancel ctx")
	}
}
