package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/logger"
)

// Manager Memory 接口的实现
type Manager struct {
	memoryDir     string
	retentionDays int
	log           *logger.Logger
}

// NewManager 创建 Memory Manager
func NewManager(memoryDir string, retentionDays int, log *logger.Logger) *Manager {
	// 确保目录存在
	os.MkdirAll(memoryDir, 0755)

	return &Manager{
		memoryDir:     memoryDir,
		retentionDays: retentionDays,
		log:           log,
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

// attachmentsDir 返回 attachments 目录路径
func (m *Manager) attachmentsDir(sessionID string) string {
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
	if err := os.MkdirAll(m.attachmentsDir(sessionID), 0755); err != nil {
		return fmt.Errorf("创建 attachments 目录失败: %w", err)
	}

	// 写入 SESSION.md（告知 LLM 附件目录位置）
	sessionMdPath := filepath.Join(sessionDir, "SESSION.md")
	sessionMdContent := fmt.Sprintf("当前会话文件目录：%s\n", m.attachmentsDir(sessionID)) +
			"用户提到的文件名直接拼接此目录即为完整路径。例如用户说「打开 report.pdf」，路径为 " + m.attachmentsDir(sessionID) + "/report.pdf\n"
	if err := os.WriteFile(sessionMdPath, []byte(sessionMdContent), 0644); err != nil {
		return fmt.Errorf("创建 SESSION.md 失败: %w", err)
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
	// 确保 attachments 目录存在
	os.MkdirAll(m.attachmentsDir(sessionID), 0755)

	// 文件名安全处理
	safeName := sanitizeFilename(filename)

	fullPath := filepath.Join(m.attachmentsDir(sessionID), safeName)

	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return "", fmt.Errorf("保存附件失败: %w", err)
	}

	return fullPath, nil
}

// GetAttachmentPath 获取附件完整路径
func (m *Manager) GetAttachmentPath(sessionID string, filename string) string {
	return filepath.Join(m.attachmentsDir(sessionID), sanitizeFilename(filename))
}

// GetSessionMdContent 获取 SESSION.md 内容
func (m *Manager) GetSessionMdContent(sessionID string) (string, error) {
	path := filepath.Join(m.sessionDir(sessionID), "SESSION.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
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