// Package memory 管理会话的元数据与附件存储。
//
// 存储职责划分：
//   - 附件（attachments/<file>）通过 storage.Storage 接口读写，便于将
//     底层后端切换为 MinIO 等对象存储；
//   - 会话元数据（history.json、chats/*.json）继续走本地文件系统 +
//     原子 rename，因为它们小、需要原子写、未来计划迁 PostgreSQL，
//     与对象存储路径不同
//     （详见 docs/superpowers/specs/2026-06-06-storage-interface-design.md 2.1.3 节）。
//   - 会话规则提示由嵌入式常量 defaultSessionRules 提供，不再写物理文件。
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/storage"
)

// Manager Memory 接口的实现
type Manager struct {
	memoryDir     string
	retentionDays int
	log           *logger.Logger
	storage       storage.Storage
}

// NewManager 创建 Memory Manager。
// store 用于附件读写，必须非 nil（启动时通过 storage.New(cfg.Storage) 创建）。
func NewManager(memoryDir string, retentionDays int, log *logger.Logger, store storage.Storage) *Manager {
	if store == nil {
		panic("memory: NewManager: storage must not be nil")
	}

	// 确保目录存在
	os.MkdirAll(memoryDir, 0755)

	return &Manager{
		memoryDir:     memoryDir,
		retentionDays: retentionDays,
		log:           log,
		storage:       store,
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

// AttachmentsDir 返回指定会话的附件目录路径。
// 工具层（如 internal/agent 内置工具）可调用此方法获取统一的附件目录拼接结果，
// 避免在多处硬编码 "<memoryDir>/<sessionID>/attachments" 的拼接规则。
func (m *Manager) AttachmentsDir(sessionID string) string {
	return filepath.Join(m.sessionDir(sessionID), "attachments")
}

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

	// 获取最后活跃时间
	lastActiveAt := ""
	if len(history.Messages) > 0 {
		lastActiveAt = history.Messages[len(history.Messages)-1].Timestamp.Format("2006-01-02T15:04:05Z")
	}

	return &SessionInfo{
		SessionID:    sessionID,
		CreatedAt:    history.CreatedAt,
		RoundCount:   len(history.Messages),
		LastActiveAt: lastActiveAt,
		Path:         m.sessionDir(sessionID),
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
		if !m.ExistsSession(sessionID) {
			continue
		}
		info, err := m.GetSessionInfo(sessionID)
		if err != nil {
			m.log.Info("获取会话信息失败: " + sessionID + ", error: " + err.Error())
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

// saveHistory 保存 history.json（原子写入：tmp + rename）
func (m *Manager) saveHistory(sessionID string, history *History) error {
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 history 失败: %w", err)
	}

	tmpPath := m.historyPath(sessionID) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}

	return os.Rename(tmpPath, m.historyPath(sessionID))
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

// GetContextMessages 返回用于 LLM 上下文构建的历史消息（截断后）
// windowSize: 保留最近 N 轮，<= 0 表示不限制
func (m *Manager) GetContextMessages(sessionID string, windowSize int) ([]Message, error) {
	history, err := m.GetHistory(sessionID)
	if err != nil {
		return nil, err
	}

	if windowSize <= 0 || len(history.Messages) <= windowSize {
		return history.Messages, nil
	}

	return history.Messages[len(history.Messages)-windowSize:], nil
}

// SaveChatRecord 保存详细对话记录（原子写入：tmp + rename）
func (m *Manager) SaveChatRecord(sessionID string, record *ChatRecord) error {
	// 确保 chats 目录存在
	os.MkdirAll(m.chatsDir(sessionID), 0755)

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 chat record 失败: %w", err)
	}

	tmpPath := m.chatPath(sessionID, record.ChatID) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}

	return os.Rename(tmpPath, m.chatPath(sessionID, record.ChatID))
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

// SaveAttachment 保存附件
func (m *Manager) SaveAttachment(sessionID string, filename string, content []byte) (string, error) {
	// 文件名安全处理
	safeName := sanitizeFilename(filename)

	fullPath := filepath.Join(m.AttachmentsDir(sessionID), safeName)

	// TODO: 当 SaveAttachment 升级为接受 ctx 参数后，把这里替换为调用方传入的 ctx，
	// 以支持 minio 模式下的请求级超时与取消。
	if err := m.storage.Write(
		context.Background(),
		fullPath,
		bytes.NewReader(content),
		int64(len(content)),
		"",
	); err != nil {
		return "", fmt.Errorf("保存附件失败: %w", err)
	}

	return fullPath, nil
}

// GetAttachmentPath 获取附件完整路径
func (m *Manager) GetAttachmentPath(sessionID string, filename string) string {
	return filepath.Join(m.AttachmentsDir(sessionID), sanitizeFilename(filename))
}

// GetSessionMdContent 返回会话规则提示内容。
//
// 历史上该方法读取每个 session 目录下的 SESSION.md 物理文件,现在改为直接
// 返回 //go:embed 嵌入的 defaultSessionRules 常量——所有会话共享同一份规则,
// 不再做会话级定制。sessionID 参数仅作签名兼容保留,不参与逻辑。
//
// 返回 err 永远为 nil。签名保持 (string, error) 是为了让接口 Memory 与所有
// 现有调用方(executor.go 等)无需调整。
func (m *Manager) GetSessionMdContent(sessionID string) (string, error) {
	_ = sessionID
	return defaultSessionRules, nil
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

		if !m.ExistsSession(sessionID) {
			continue
		}

		sessionDir := m.sessionDir(sessionID)
		info, err := os.Stat(sessionDir)
		if err != nil {
			m.log.Info("跳过会话（无法获取目录信息）: " + sessionID + ", error: " + err.Error())
			continue
		}

		if info.ModTime().Before(cutoff) {
			// 先删附件（走 storage 抽象，确保 minio 模式下也能清理）。
			// 任何一步失败时 continue 跳过，避免"附件残留 + 元数据被删"的
			// 不一致状态——失败的 session 在下次 Cleanup 时会自动重试。
			attachmentsDir := m.AttachmentsDir(sessionID)
			if err := m.storage.DeleteDir(ctx, attachmentsDir); err != nil {
				m.log.Error("清理附件失败: " + sessionID + ", error: " + err.Error())
				continue
			}
			// 再删元数据（history.json / chats 等本地文件，含旧版残留的 SESSION.md）
			if err := os.RemoveAll(sessionDir); err != nil {
				m.log.Error("清理会话失败: " + sessionID + ", error: " + err.Error())
				continue
			}
			deleted++
			roundCount := m.GetRoundCount(sessionID)
			m.log.Info("清理会话: " + sessionID + ", 最后活跃: " + info.ModTime().Format("2006-01-02") + ", 轮数: " + fmt.Sprintf("%d", roundCount))
		}
	}

	m.log.Info(fmt.Sprintf("清理完成, 删除 %d 个会话", deleted))
	return deleted, nil
}
