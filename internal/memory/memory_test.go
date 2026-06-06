package memory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/storage"
)

// spyStorage 包装一个 storage.Storage，记录 Write 调用以验证 SaveAttachment
// 真的通过抽象层而不是直接 os.WriteFile。
type spyStorage struct {
	storage.Storage
	writeCalled bool
	lastPath    string
	lastSize    int64
	lastCT      string
}

func (s *spyStorage) Write(ctx context.Context, path string, r io.Reader, size int64, ct string) error {
	s.writeCalled = true
	s.lastPath = path
	s.lastSize = size
	s.lastCT = ct
	return s.Storage.Write(ctx, path, r, size, ct)
}

func initTestLogger() *logger.Logger {
	return logger.New(config.LoggingConfig{Level: "debug", Format: "json", Output: []string{"stdout"}})
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

	// 验证格式: chat_{YYYYMMDDHHMMSSmmm}
	if !strings.HasPrefix(id, "chat_") {
		t.Errorf("GenerateChatID() 格式错误: 应以 'chat_' 开头, got %s", id)
	}

	parts := strings.Split(id, "_")
	if len(parts) != 2 {
		t.Errorf("GenerateChatID() 格式错误: 应有 2 部分, got %d", len(parts))
	}

	// 时间戳部分应为 17 位
	if len(parts[1]) != 17 {
		t.Errorf("GenerateChatID() 时间戳部分长度错误: got %d, want 17", len(parts[1]))
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

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "正常文件名",
			input:    "test.txt",
			expected: "test.txt",
		},
		{
			name:     "包含斜杠",
			input:    "path/test.txt",
			expected: "path_test.txt",
		},
		{
			name:     "包含反斜杠",
			input:    "path\\test.txt",
			expected: "path_test.txt",
		},
		{
			name:     "包含双点",
			input:    "..test.txt",
			expected: "_test.txt",
		},
		{
			name:     "路径穿越尝试",
			input:    "../secret.txt",
			expected: "__secret.txt",
		},
		{
			name:     "过长文件名",
			input:    strings.Repeat("a", 300) + ".txt",
			expected: strings.Repeat("a", 251) + ".txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename(%s) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

// Manager 测试

func TestNewManager(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	if mgr.GetMemoryDir() != tmpDir {
		t.Errorf("NewManager().GetMemoryDir() = %s, want %s", mgr.GetMemoryDir(), tmpDir)
	}
}

func TestManager_CreateSession(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_001"
	err := mgr.CreateSession(sessionID)
	if err != nil {
		t.Fatalf("CreateSession() 失败: %v", err)
	}

	// 验证目录结构
	sessionDir := filepath.Join(tmpDir, sessionID)
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		t.Error("CreateSession() 未创建会话目录")
	}

	chatsDir := filepath.Join(sessionDir, "chats")
	if _, err := os.Stat(chatsDir); os.IsNotExist(err) {
		t.Error("CreateSession() 未创建 chats 目录")
	}

	// attachments 目录采用懒创建策略：CreateSession 不预先创建，首次 SaveAttachment
	// 时由 storage.Write 自动建目录（local 模式），minio 模式则根本不需要目录概念。
	attachmentsDir := filepath.Join(sessionDir, "attachments")
	if _, err := os.Stat(attachmentsDir); !os.IsNotExist(err) {
		t.Errorf("CreateSession() 不应预先创建 attachments 目录, got err=%v", err)
	}

	// 验证 history.json
	historyPath := filepath.Join(sessionDir, "history.json")
	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		t.Error("CreateSession() 未创建 history.json")
	}
}

func TestManager_CreateSession_WritesSessionMd(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_md"
	err := mgr.CreateSession(sessionID)
	if err != nil {
		t.Fatalf("CreateSession() 失败: %v", err)
	}

	// 验证 SESSION.md 已创建在会话根目录
	sessionMdPath := filepath.Join(tmpDir, sessionID, "SESSION.md")
	data, err := os.ReadFile(sessionMdPath)
	if err != nil {
		t.Fatalf("SESSION.md 未创建: %v", err)
	}

	content := string(data)
	attachmentsDir := filepath.Join(tmpDir, sessionID, "attachments")

	if !strings.Contains(content, "当前会话文件目录") {
		t.Error("SESSION.md 应包含引导文本")
	}
	if !strings.Contains(content, attachmentsDir) {
		t.Errorf("SESSION.md 应包含 attachments 目录路径: %s, 内容: %s", attachmentsDir, content)
	}

	// 验证 GetSessionMdContent 可读取
	mdContent, err := mgr.GetSessionMdContent(sessionID)
	if err != nil {
		t.Fatalf("GetSessionMdContent() 失败: %v", err)
	}
	if mdContent != content {
		t.Error("GetSessionMdContent() 返回内容与文件内容不一致")
	}
}

func TestManager_ExistsSession(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_002"

	// 未创建时不应存在
	if mgr.ExistsSession(sessionID) {
		t.Error("ExistsSession() 应返回 false 未创建会话")
	}

	// 创建后应存在
	mgr.CreateSession(sessionID)
	if !mgr.ExistsSession(sessionID) {
		t.Error("ExistsSession() 应返回 true 已创建会话")
	}
}

func TestManager_GetHistory(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_003"
	mgr.CreateSession(sessionID)

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
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	_, err := mgr.GetHistory("nonexistent")
	if err == nil {
		t.Error("GetHistory() 应返回错误当会话不存在")
	}
}

func TestManager_AppendMessage(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_004"
	mgr.CreateSession(sessionID)

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

	// 验证消息已追加
	history, _ := mgr.GetHistory(sessionID)
	if len(history.Messages) != 1 {
		t.Errorf("AppendMessage() 后 Messages 数量错误: got %d, want 1", len(history.Messages))
	}

	if history.Messages[0].Instruction != "测试指令" {
		t.Errorf("AppendMessage() 内容错误: got %s, want 测试指令", history.Messages[0].Instruction)
	}
}

func TestManager_GetRoundCount(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_005"
	mgr.CreateSession(sessionID)

	// 初始应为 0
	if mgr.GetRoundCount(sessionID) != 0 {
		t.Errorf("GetRoundCount() 初始应为 0, got %d", mgr.GetRoundCount(sessionID))
	}

	// 追加消息后
	mgr.AppendMessage(sessionID, &Message{Round: 1})
	mgr.AppendMessage(sessionID, &Message{Round: 2})
	mgr.AppendMessage(sessionID, &Message{Round: 3})

	if mgr.GetRoundCount(sessionID) != 3 {
		t.Errorf("GetRoundCount() 应为 3, got %d", mgr.GetRoundCount(sessionID))
	}
}

func TestManager_SaveChatRecord(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_006"
	mgr.CreateSession(sessionID)

	record := &ChatRecord{
		ChatID:      "chat_001",
		SessionID:   sessionID,
		Round:       1,
		Instruction: "测试指令",
		Result:      "测试结果",
		Status:      "completed",
		Steps: []Step{
			{StepID: "step_001", Type: "tool", Name: "test_tool", Status: "success"},
		},
	}

	err := mgr.SaveChatRecord(sessionID, record)
	if err != nil {
		t.Fatalf("SaveChatRecord() 失败: %v", err)
	}

	// 验证文件存在
	chatPath := filepath.Join(tmpDir, sessionID, "chats", "chat_001.json")
	if _, err := os.Stat(chatPath); os.IsNotExist(err) {
		t.Error("SaveChatRecord() 未创建文件")
	}
}

func TestManager_GetChatRecord(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_007"
	mgr.CreateSession(sessionID)

	record := &ChatRecord{
		ChatID:      "chat_001",
		SessionID:   sessionID,
		Round:       1,
		Instruction: "测试指令",
		Result:      "测试结果",
		Status:      "completed",
	}
	mgr.SaveChatRecord(sessionID, record)

	got, err := mgr.GetChatRecord(sessionID, "chat_001")
	if err != nil {
		t.Fatalf("GetChatRecord() 失败: %v", err)
	}

	if got.Instruction != "测试指令" {
		t.Errorf("GetChatRecord().Instruction = %s, want 测试指令", got.Instruction)
	}
}

func TestManager_SaveAttachment(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_session_008"
	mgr.CreateSession(sessionID)

	content := []byte("test file content")
	path, err := mgr.SaveAttachment(sessionID, "test.txt", content)
	if err != nil {
		t.Fatalf("SaveAttachment() 失败: %v", err)
	}

	// 验证文件存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("SaveAttachment() 未创建文件")
	}

	// 验证内容
	data, _ := os.ReadFile(path)
	if string(data) != "test file content" {
		t.Errorf("SaveAttachment() 内容错误: got %s, want test file content", string(data))
	}
}

func TestManager_ListSessions(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	// 创建多个会话
	for i := 1; i <= 5; i++ {
		sessionID := GenerateSessionID()
		mgr.CreateSession(sessionID)
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

	// 验证按时间倒序
	for i := 0; i < len(sessions)-1; i++ {
		if sessions[i].CreatedAt.Before(sessions[i+1].CreatedAt) {
			t.Error("ListSessions() 应按创建时间倒序排列")
		}
	}
}

func TestManager_ListSessions_Pagination(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	// 创建 10 个会话
	for i := 1; i <= 10; i++ {
		sessionID := GenerateSessionID()
		mgr.CreateSession(sessionID)
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
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 1, log, storage.NewLocal()) // 保留 1 天

	// 创建旧会话，然后修改目录 ModTime 为 2 天前
	sessionID := "test_session_old"
	mgr.CreateSession(sessionID)
	oldTime := time.Now().AddDate(0, 0, -2)
	sessionDir := mgr.sessionDir(sessionID)
	os.Chtimes(sessionDir, oldTime, oldTime)

	// 创建一个新会话（不会被清理）
	newSessionID := "test_session_new"
	mgr.CreateSession(newSessionID)

	// 执行清理
	deleted, err := mgr.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup() 失败: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Cleanup() 应删除 1 个会话, got %d", deleted)
	}

	// 验证旧会话已删除
	if mgr.ExistsSession(sessionID) {
		t.Error("Cleanup() 应删除过期会话")
	}

	// 验证新会话保留
	if !mgr.ExistsSession(newSessionID) {
		t.Error("Cleanup() 不应删除未过期会话")
	}
}

func TestManager_GetContextMessages(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

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

func TestManager_SaveHistory_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

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
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

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

func TestManager_SaveAttachment_WritesViaStorage(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	spy := &spyStorage{Storage: storage.NewLocal()}
	mgr := NewManager(tmpDir, 7, log, spy)

	sessionID := "test_save_attach_via_storage"
	if err := mgr.CreateSession(sessionID); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	content := []byte("hello attachment")
	path, err := mgr.SaveAttachment(sessionID, "report.pdf", content)
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	// 验证 storage.Write 真的被调用
	if !spy.writeCalled {
		t.Fatal("storage.Write was not called")
	}
	if spy.lastPath != path {
		t.Errorf("storage.Write path = %q, want %q", spy.lastPath, path)
	}
	if spy.lastSize != int64(len(content)) {
		t.Errorf("storage.Write size = %d, want %d", spy.lastSize, len(content))
	}
	if spy.lastCT != "" {
		t.Errorf("storage.Write contentType = %q, want empty", spy.lastCT)
	}

	// 同时验证内容确实落盘（spy 透传到了 NewLocal）
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %q, want %q", got, content)
	}
}

func TestManager_SaveAttachment_AutoCreatesAttachmentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_lazy_attach_dir"
	if err := mgr.CreateSession(sessionID); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 验证 CreateSession 后 attachments 目录不存在（懒创建契约）
	attDir := filepath.Join(tmpDir, sessionID, "attachments")
	if _, err := os.Stat(attDir); !os.IsNotExist(err) {
		t.Fatalf("attachments dir should not exist after CreateSession, stat err: %v", err)
	}

	// SaveAttachment 后应自动创建 attachments 目录并写入文件
	if _, err := mgr.SaveAttachment(sessionID, "x.txt", []byte("data")); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if info, err := os.Stat(attDir); err != nil {
		t.Fatalf("attachments dir should exist after SaveAttachment, stat err: %v", err)
	} else if !info.IsDir() {
		t.Fatal("attachments path should be a directory")
	}
}

func TestNewManager_PanicsOnNilStorage(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on nil storage")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "storage must not be nil") {
			t.Fatalf("expected panic message about nil storage, got: %v", r)
		}
	}()
	_ = NewManager(tmpDir, 7, log, nil)
}
