package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// Manager Memory 接口的实现
type Manager struct {
	retentionDays int
	log           *logger.Logger
	repo          repo.MemoryRepo
}

// NewManager 创建 Memory Manager。
// memRepo 用于会话/聊天记录的数据库读写，必须非 nil。
func NewManager(retentionDays int, log *logger.Logger, memRepo repo.MemoryRepo) *Manager {
	if memRepo == nil {
		panic("memory: NewManager: memRepo must not be nil")
	}

	return &Manager{
		retentionDays: retentionDays,
		log:           log,
		repo:          memRepo,
	}
}

// GetMemoryDir 保留兼容签名，返回空字符串（数据库模式下无文件目录）
func (m *Manager) GetMemoryDir() string {
	return ""
}

// CreateSession 创建新会话
func (m *Manager) CreateSession(sessionID string) error {
	now := time.Now()
	return m.repo.CreateSession(context.Background(), &repo.Session{
		SessionID: sessionID,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// ExistsSession 检查会话是否存在
func (m *Manager) ExistsSession(sessionID string) bool {
	exists, err := m.repo.ExistsSession(context.Background(), sessionID)
	if err != nil {
		return false
	}
	return exists
}

// GetSessionInfo 获取会话信息
func (m *Manager) GetSessionInfo(sessionID string) (*SessionInfo, error) {
	s, err := m.repo.GetSession(context.Background(), sessionID)
	if err != nil {
		return nil, fmt.Errorf("会话不存在: %s", sessionID)
	}

	// 获取最后活跃时间（UpdatedAt 即最后一次 SaveChat 的时间）
	lastActiveAt := ""
	if !s.UpdatedAt.IsZero() && s.Round > 0 {
		lastActiveAt = s.UpdatedAt.Format("2006-01-02T15:04:05Z")
	}

	return &SessionInfo{
		SessionID:    sessionID,
		CreatedAt:    s.CreatedAt,
		RoundCount:   s.Round,
		LastActiveAt: lastActiveAt,
		Path:         "",
	}, nil
}

// ListSessions 查询会话列表，支持分页
func (m *Manager) ListSessions(limit, offset int) ([]SessionInfo, int, error) {
	sessions, err := m.repo.ListSessions(context.Background())
	if err != nil {
		return nil, 0, fmt.Errorf("查询会话列表失败: %w", err)
	}

	var infos []SessionInfo
	for _, s := range sessions {
		lastActiveAt := ""
		if !s.UpdatedAt.IsZero() && s.Round > 0 {
			lastActiveAt = s.UpdatedAt.Format("2006-01-02T15:04:05Z")
		}
		infos = append(infos, SessionInfo{
			SessionID:    s.SessionID,
			CreatedAt:    s.CreatedAt,
			RoundCount:   s.Round,
			LastActiveAt: lastActiveAt,
			Path:         "",
		})
	}

	total := len(infos)
	if offset >= total {
		return []SessionInfo{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return infos[offset:end], total, nil
}

// GetHistory 获取会话历史（从 DB 重建 History 结构）
func (m *Manager) GetHistory(sessionID string) (*History, error) {
	s, err := m.repo.GetSession(context.Background(), sessionID)
	if err != nil {
		return nil, fmt.Errorf("会话不存在: %s", sessionID)
	}

	chats, err := m.repo.LoadHistory(context.Background(), sessionID)
	if err != nil {
		return nil, fmt.Errorf("读取对话历史失败: %w", err)
	}

	messages := make([]Message, 0, len(chats))
	for _, c := range chats {
		msg := Message{
			Round:       c.Round,
			ChatID:      c.ChatID,
			Timestamp:   c.EndedAt,
			Instruction: c.Instruction,
			Result:      c.Result,
			Status:      c.Status,
			Duration:    c.Duration,
			StepsCount:  len(c.Steps),
			AgentName:   c.AgentName,
			Error:       c.Error,
		}
		messages = append(messages, msg)
	}

	return &History{
		SessionID: sessionID,
		CreatedAt: s.CreatedAt,
		Messages:  messages,
	}, nil
}

// AppendMessage 在 DB 模式下为空操作：轮次信息已由 SaveChatRecord/SaveChat 自动维护。
// 保留签名保持与调用方（executor.go）兼容。
func (m *Manager) AppendMessage(sessionID string, message *Message) error {
	// SaveChat 已将 round 自增并更新 updated_at，无需额外操作
	return nil
}

// GetRoundCount 获取对话轮数
func (m *Manager) GetRoundCount(sessionID string) int {
	s, err := m.repo.GetSession(context.Background(), sessionID)
	if err != nil {
		return 0
	}
	return s.Round
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

// SaveChatRecord 保存详细对话记录到数据库
func (m *Manager) SaveChatRecord(sessionID string, record *ChatRecord) error {
	// 确保 SessionID 字段已设置
	if record.SessionID == "" {
		record.SessionID = sessionID
	}
	return m.repo.SaveChat(context.Background(), record)
}

// GetChatRecord 获取单次对话详情
func (m *Manager) GetChatRecord(sessionID string, chatID string) (*ChatRecord, error) {
	rec, err := m.repo.GetChat(context.Background(), chatID)
	if err != nil {
		return nil, fmt.Errorf("对话记录不存在: %s", chatID)
	}
	return rec, nil
}

// GetLatestChatRecord 获取最近一次对话记录
func (m *Manager) GetLatestChatRecord(sessionID string) (*ChatRecord, error) {
	chats, err := m.repo.LoadHistory(context.Background(), sessionID)
	if err != nil {
		return nil, fmt.Errorf("读取对话历史失败: %w", err)
	}
	if len(chats) == 0 {
		return nil, nil
	}
	return chats[len(chats)-1], nil
}

// GetSessionMdContent 返回会话规则提示内容。
// sessionID 参数仅作签名兼容保留，不参与逻辑。
// 返回 err 永远为 nil。
func (m *Manager) GetSessionMdContent(sessionID string) (string, error) {
	_ = sessionID
	return defaultSessionRules, nil
}

// DeleteSession 删除会话及其所有对话记录
func (m *Manager) DeleteSession(sessionID string) error {
	return m.repo.DeleteSession(context.Background(), sessionID)
}

// Cleanup 清理过期会话
func (m *Manager) Cleanup(ctx context.Context) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -m.retentionDays)
	deleted, err := m.repo.DeleteExpiredSessions(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("清理过期会话失败: %w", err)
	}
	m.log.Info(fmt.Sprintf("清理完成, 删除 %d 个会话", deleted))
	return deleted, nil
}
