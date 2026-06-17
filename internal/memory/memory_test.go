package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo/memorydb"
)

func initTestLogger() *logger.Logger {
	return logger.New(config.LoggingConfig{Level: "debug", Format: "json", Output: []string{"stdout"}})
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	memRepo := memorydb.New(sqlxDB, dialect)
	return NewManager(7, initTestLogger(), memRepo)
}

func TestGenerateSessionID(t *testing.T) {
	id := GenerateSessionID()

	// 验证格式: {YYYYMMDDHHMMSSmmm}_{random4}
	if !strings.Contains(id, "_") {
		t.Errorf("GenerateSessionID() 格式错误: 应包含 '_' 分隔符, got %s", id)
	}

	parts := strings.Split(id, "_")
	if len(parts) != 2 {
		t.Errorf("GenerateSessionID() 格式错误: 应有 2 部分, got %d", len(parts))
	}

	// 时间戳部分应为 17 位 (YYYYMMDDHHMMSSmmm)
	if len(parts[0]) != 17 {
		t.Errorf("GenerateSessionID() 时间戳部分长度错误: got %d, want 17", len(parts[0]))
	}

	// 随机部分应为 4 位
	if len(parts[1]) != 4 {
		t.Errorf("GenerateSessionID() 随机部分长度错误: got %d, want 4", len(parts[1]))
	}
}

func TestGenerateSessionID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateSessionID()
		if ids[id] {
			t.Errorf("GenerateSessionID() 生成重复 ID: %s", id)
		}
		ids[id] = true
	}
}

func TestGenerateChatID(t *testing.T) {
	id := GenerateChatID()

	// 验证格式: {YYYYMMDDHHMMSSmmm}（纯时间戳，17位，无前缀）
	if len(id) != 17 {
		t.Errorf("GenerateChatID() 长度错误: got %d, want 17, id=%s", len(id), id)
	}

	// 验证全为数字
	for i, c := range id {
		if c < '0' || c > '9' {
			t.Errorf("GenerateChatID() 第 %d 个字符不是数字: got %c, id=%s", i, c, id)
		}
	}
}

func TestGenerateStepID(t *testing.T) {
	id := GenerateStepID()

	// 验证格式: {YYYYMMDD}-{HHMMSSmmm}-{random6}
	parts := strings.Split(id, "-")
	if len(parts) != 3 {
		t.Errorf("GenerateStepID() 格式错误: 应有 3 部分, got %d", len(parts))
	}

	// 日期部分应为 8 位
	if len(parts[0]) != 8 {
		t.Errorf("GenerateStepID() 日期部分长度错误: got %d, want 8", len(parts[0]))
	}

	// 时间部分应为 9 位
	if len(parts[1]) != 9 {
		t.Errorf("GenerateStepID() 时间部分长度错误: got %d, want 9", len(parts[1]))
	}

	// 随机部分应为 6 位
	if len(parts[2]) != 6 {
		t.Errorf("GenerateStepID() 随机部分长度错误: got %d, want 6", len(parts[2]))
	}
}

func TestGenerateStepID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateStepID()
		if ids[id] {
			t.Errorf("GenerateStepID() 生成重复 ID: %s", id)
		}
		ids[id] = true
	}
}

// Manager 测试

func TestNewManager(t *testing.T) {
	mgr := newTestManager(t)
	// DB 模式下 GetMemoryDir 返回空字符串
	if mgr.GetMemoryDir() != "" {
		t.Errorf("NewManager().GetMemoryDir() should be empty in DB mode, got %s", mgr.GetMemoryDir())
	}
}

func TestNewManager_PanicsOnNilRepo(t *testing.T) {
	log := initTestLogger()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on nil memRepo")
		}
	}()
	_ = NewManager(7, log, nil)
}

func TestManager_CreateSession(t *testing.T) {
	mgr := newTestManager(t)

	t.Run("空 userID", func(t *testing.T) {
		sessionID := "test_session_001"
		if err := mgr.CreateSession(sessionID, ""); err != nil {
			t.Fatalf("CreateSession() 失败: %v", err)
		}
		if !mgr.ExistsSession(sessionID) {
			t.Error("CreateSession() 后 ExistsSession() 应返回 true")
		}
	})

	t.Run("带 userID", func(t *testing.T) {
		sessionID := "test_session_002"
		userID := "user-abc"
		if err := mgr.CreateSession(sessionID, userID); err != nil {
			t.Fatalf("CreateSession() with userID 失败: %v", err)
		}
		if !mgr.ExistsSession(sessionID) {
			t.Error("CreateSession() 后 ExistsSession() 应返回 true")
		}
		// 验证 user_id 已写入
		s, err := mgr.repo.GetSession(context.Background(), sessionID)
		if err != nil {
			t.Fatalf("GetSession() 失败: %v", err)
		}
		if s.UserID != userID {
			t.Errorf("UserID 期望 %q，实际 %q", userID, s.UserID)
		}
	})
}

// TestManager_GetSessionMdContent_ReturnsConstant 验证 GetSessionMdContent
// 返回值与传入的 sessionID 无关(包括不存在的 sessionID)。
func TestManager_GetSessionMdContent_ReturnsConstant(t *testing.T) {
	mgr := newTestManager(t)

	cases := []string{"existing_session", "non_existent_session", ""}
	var first string
	for i, sid := range cases {
		content, err := mgr.GetSessionMdContent(sid)
		if err != nil {
			t.Fatalf("GetSessionMdContent(%q) 返回 err: %v", sid, err)
		}
		if i == 0 {
			first = content
		} else if content != first {
			t.Errorf("GetSessionMdContent 应返回与 sessionID 无关的常量,但 %q 与 %q 内容不同", cases[0], sid)
		}
	}
	// 内容可以为空（session_rules.md 目前为空文件），
	// 此处只验证多次调用返回同一常量即可。
}

func TestManager_ExistsSession(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_002"

	// 未创建时不应存在
	if mgr.ExistsSession(sessionID) {
		t.Error("ExistsSession() 应返回 false 未创建会话")
	}

	// 创建后应存在
	mgr.CreateSession(sessionID, "")
	if !mgr.ExistsSession(sessionID) {
		t.Error("ExistsSession() 应返回 true 已创建会话")
	}
}

func TestManager_GetHistory(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_003"
	mgr.CreateSession(sessionID, "")

	history, err := mgr.GetHistory(sessionID)
	if err != nil {
		t.Fatalf("GetHistory() 失败: %v", err)
	}

	if history.SessionID != sessionID {
		t.Errorf("GetHistory().SessionID = %s, want %s", history.SessionID, sessionID)
	}

	if len(history.Messages) != 0 {
		t.Errorf("GetHistory().Messages 初始应为空, got %d", len(history.Messages))
	}
}

func TestManager_GetHistory_NotExist(t *testing.T) {
	mgr := newTestManager(t)

	_, err := mgr.GetHistory("nonexistent")
	if err == nil {
		t.Error("GetHistory() 应返回错误当会话不存在")
	}
}

func TestManager_AppendMessage(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_004"
	mgr.CreateSession(sessionID, "")

	// AppendMessage 在 DB 模式下是空操作，不影响 GetHistory 结果
	// (轮次数据由 SaveChatRecord 维护)
	msg := &Message{
		Round:       1,
		ChatID:      "chat_001",
		Timestamp:   time.Now(),
		Instruction: "测试指令",
		Result:      "测试结果",
		Status:      "completed",
	}

	err := mgr.AppendMessage(sessionID, msg)
	if err != nil {
		t.Fatalf("AppendMessage() 失败: %v", err)
	}
}

func TestManager_GetRoundCount(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_005"
	mgr.CreateSession(sessionID, "")

	// 初始应为 0
	if mgr.GetRoundCount(sessionID) != 0 {
		t.Errorf("GetRoundCount() 初始应为 0, got %d", mgr.GetRoundCount(sessionID))
	}

	// SaveChatRecord 后 round 递增
	for i := 1; i <= 3; i++ {
		rec := &ChatRecord{
			ChatID:    fmt.Sprintf("chat_%03d", i),
			SessionID: sessionID,
			Status:    "success",
			StartedAt: time.Now(),
		}
		if err := mgr.SaveChatRecord(sessionID, rec); err != nil {
			t.Fatalf("SaveChatRecord round=%d: %v", i, err)
		}
	}

	if mgr.GetRoundCount(sessionID) != 3 {
		t.Errorf("GetRoundCount() 应为 3, got %d", mgr.GetRoundCount(sessionID))
	}
}

func TestManager_SaveChatRecord(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_006"
	mgr.CreateSession(sessionID, "")

	record := &ChatRecord{
		ChatID:      "20260611100000001",
		SessionID:   sessionID,
		Instruction: "测试指令",
		Result:      "测试结果",
		Status:      "success",
		StartedAt:   time.Now(),
		Steps: []Step{
			{StepID: "step_001", Type: "tool", Name: "test_tool", Status: "success"},
		},
	}

	err := mgr.SaveChatRecord(sessionID, record)
	if err != nil {
		t.Fatalf("SaveChatRecord() 失败: %v", err)
	}

	// 验证可以读回
	got, err := mgr.GetChatRecord(sessionID, "20260611100000001")
	if err != nil {
		t.Fatalf("GetChatRecord() 失败: %v", err)
	}
	if got.Instruction != "测试指令" {
		t.Errorf("Instruction mismatch: got %s, want 测试指令", got.Instruction)
	}
}

func TestManager_GetChatRecord(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_007"
	mgr.CreateSession(sessionID, "")

	record := &ChatRecord{
		ChatID:      "20260611100000002",
		SessionID:   sessionID,
		Instruction: "测试指令",
		Result:      "测试结果",
		Status:      "success",
		StartedAt:   time.Now(),
	}
	mgr.SaveChatRecord(sessionID, record)

	got, err := mgr.GetChatRecord(sessionID, "20260611100000002")
	if err != nil {
		t.Fatalf("GetChatRecord() 失败: %v", err)
	}

	if got.Instruction != "测试指令" {
		t.Errorf("GetChatRecord().Instruction = %s, want 测试指令", got.Instruction)
	}
}

func TestManager_GetChatRecord_NotExist(t *testing.T) {
	mgr := newTestManager(t)

	if err := mgr.CreateSession("test-session", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	_, err := mgr.GetChatRecord("test-session", "nonexistent-chat-id")
	if err == nil {
		t.Fatal("expected error for non-existent chat record")
	}
	if !strings.Contains(err.Error(), "对话记录不存在") {
		t.Errorf("expected error to contain '对话记录不存在', got: %v", err)
	}
}

func TestManager_ListSessions(t *testing.T) {
	mgr := newTestManager(t)

	// 创建多个会话
	for i := 1; i <= 5; i++ {
		sessionID := GenerateSessionID()
		mgr.CreateSession(sessionID, "")
		time.Sleep(1 * time.Millisecond) // 确保时间不同
	}

	sessions, total, err := mgr.ListSessions(10, 0)
	if err != nil {
		t.Fatalf("ListSessions() 失败: %v", err)
	}

	if total != 5 {
		t.Errorf("ListSessions() total = %d, want 5", total)
	}

	if len(sessions) != 5 {
		t.Errorf("ListSessions() 返回数量 = %d, want 5", len(sessions))
	}
}

func TestManager_ListSessions_Pagination(t *testing.T) {
	mgr := newTestManager(t)

	// 创建 10 个会话
	for i := 1; i <= 10; i++ {
		sessionID := GenerateSessionID()
		mgr.CreateSession(sessionID, "")
		time.Sleep(1 * time.Millisecond)
	}

	// 第一页
	sessions, _, _ := mgr.ListSessions(3, 0)
	if len(sessions) != 3 {
		t.Errorf("ListSessions(limit=3, offset=0) 应返回 3 条, got %d", len(sessions))
	}

	// 第二页
	sessions2, _, _ := mgr.ListSessions(3, 3)
	if len(sessions2) != 3 {
		t.Errorf("ListSessions(limit=3, offset=3) 应返回 3 条, got %d", len(sessions2))
	}

	// 验证分页不重叠
	if sessions[0].SessionID == sessions2[0].SessionID {
		t.Error("分页应返回不同会话")
	}

	// 超出范围
	sessions3, _, _ := mgr.ListSessions(3, 100)
	if len(sessions3) != 0 {
		t.Errorf("ListSessions(offset=100) 应返回空, got %d", len(sessions3))
	}
}

func TestManager_Cleanup(t *testing.T) {
	// retentionDays=1，需创建一个独立的管理器
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlxDB.Close()
	memRepo := memorydb.New(sqlxDB, dialect)
	shortMgr := NewManager(1, initTestLogger(), memRepo)

	// 创建旧会话（由 DeleteExpiredSessions 按 updated_at 淘汰）
	sessionID := "test_session_old"
	shortMgr.CreateSession(sessionID, "")

	// 使 updated_at 显得是 2 天前：直接插入一条旧 updated_at 的 session
	// 通过先删除再重建（利用 DB 直接操作）
	// 因为无法直接修改 updated_at，改为在 shortMgr 内部用 repo 调用
	// 最简方案：DeleteSession 然后手动操作是不现实的，
	// 所以改为创建一个 updated_at 为 2 天前的会话，通过底层 DB insert
	sqlxDB.ExecContext(context.Background(),
		`UPDATE memory_sessions SET updated_at=? WHERE session_id=?`,
		time.Now().AddDate(0, 0, -2).UnixMilli(), sessionID,
	)

	// 创建新会话（不会被清理）
	newSessionID := "test_session_new"
	shortMgr.CreateSession(newSessionID, "")

	// 执行清理
	deleted, err := shortMgr.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup() 失败: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Cleanup() 应删除 1 个会话, got %d", deleted)
	}

	// 验证旧会话已删除
	if shortMgr.ExistsSession(sessionID) {
		t.Error("Cleanup() 应删除过期会话")
	}

	// 验证新会话保留
	if !shortMgr.ExistsSession(newSessionID) {
		t.Error("Cleanup() 不应删除未过期会话")
	}
}

func TestManager_GetContextMessages(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_context"
	mgr.CreateSession(sessionID, "")

	// 保存 5 条成功的 chat record（只有 completed + agent_name="" 才进 LoadHistory）
	for i := 1; i <= 5; i++ {
		rec := &ChatRecord{
			ChatID:      fmt.Sprintf("2026061110000000%d", i),
			SessionID:   sessionID,
			Instruction: fmt.Sprintf("指令 %d", i),
			Result:      fmt.Sprintf("结果 %d", i),
			Status:      "completed",
			StartedAt:   time.Now(),
		}
		if err := mgr.SaveChatRecord(sessionID, rec); err != nil {
			t.Fatalf("SaveChatRecord i=%d: %v", i, err)
		}
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

func TestManager_GetLatestChatRecord(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_latest"
	mgr.CreateSession(sessionID, "")

	// 无记录时应返回 nil, nil
	rec, err := mgr.GetLatestChatRecord(sessionID)
	if err != nil {
		t.Fatalf("GetLatestChatRecord() (empty) 失败: %v", err)
	}
	if rec != nil {
		t.Errorf("GetLatestChatRecord() 空会话应返回 nil, got %+v", rec)
	}

	// 保存两条记录
	for i := 1; i <= 2; i++ {
		mgr.SaveChatRecord(sessionID, &ChatRecord{
			ChatID:      fmt.Sprintf("2026061110000010%d", i),
			SessionID:   sessionID,
			Instruction: fmt.Sprintf("指令 %d", i),
			Status:      "completed",
			StartedAt:   time.Now(),
		})
	}

	latest, err := mgr.GetLatestChatRecord(sessionID)
	if err != nil {
		t.Fatalf("GetLatestChatRecord() 失败: %v", err)
	}
	if latest == nil {
		t.Fatal("GetLatestChatRecord() 应返回最后一条记录, got nil")
	}
	if latest.Instruction != "指令 2" {
		t.Errorf("GetLatestChatRecord() 应返回最后一条, got instruction=%s", latest.Instruction)
	}
}

func TestManager_DeleteSession(t *testing.T) {
	mgr := newTestManager(t)

	sessionID := "test_session_delete"
	mgr.CreateSession(sessionID, "")

	// 保存一条记录
	mgr.SaveChatRecord(sessionID, &ChatRecord{
		ChatID:    "20260611100000200",
		SessionID: sessionID,
		Status:    "success",
		StartedAt: time.Now(),
	})

	if err := mgr.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession() 失败: %v", err)
	}

	// 删除后会话不应存在
	if mgr.ExistsSession(sessionID) {
		t.Error("DeleteSession() 后 ExistsSession() 应返回 false")
	}

	// GetHistory 应返回错误
	if _, err := mgr.GetHistory(sessionID); err == nil {
		t.Error("DeleteSession() 后 GetHistory() 应返回错误")
	}
}
