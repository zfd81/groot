package memory

import "context"

// Memory 接口定义
type Memory interface {
	// Session 管理
	CreateSession(sessionID string) error
	ExistsSession(sessionID string) bool
	GetSessionInfo(sessionID string) (*SessionInfo, error)
	ListSessions(limit, offset int) ([]SessionInfo, int, error)

	// History 管理
	AppendMessage(sessionID string, message *Message) error
	GetHistory(sessionID string) (*History, error)
	GetRoundCount(sessionID string) int
	GetContextMessages(sessionID string, windowSize int) ([]Message, error)

	// Chat 记录管理
	SaveChatRecord(sessionID string, record *ChatRecord) error
	GetChatRecord(sessionID string, chatID string) (*ChatRecord, error)
	GetLatestChatRecord(sessionID string) (*ChatRecord, error)

	// 会话文件目录提示
	GetSessionMdContent(sessionID string) (string, error)

	// 清理
	Cleanup(ctx context.Context) (int, error)

	// 获取记忆目录路径
	GetMemoryDir() string
}