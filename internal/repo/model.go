package repo

import (
	"context"
	"time"
)

// DefaultFlag 模型默认类型，按位组合存于 models.default_flags。
// 新增默认类型时分配新的位，表结构不变。
type DefaultFlag int

const (
	DefaultChat   DefaultFlag = 1 << iota // 1 默认对话模型
	DefaultVoice                          // 2 默认语音模型
	DefaultVision                         // 4 默认视觉模型
)

// AllDefaultFlags 全部默认类型，顺序即对外展示顺序
var AllDefaultFlags = []DefaultFlag{DefaultChat, DefaultVoice, DefaultVision}

// String 返回默认类型的字符串标识（chat / voice / vision），供 HTTP 层使用
func (f DefaultFlag) String() string {
	switch f {
	case DefaultChat:
		return "chat"
	case DefaultVoice:
		return "voice"
	case DefaultVision:
		return "vision"
	}
	return ""
}

// ParseDefaultFlag 把字符串标识解析为单个默认类型；未知标识返回 false
func ParseDefaultFlag(s string) (DefaultFlag, bool) {
	for _, f := range AllDefaultFlags {
		if f.String() == s {
			return f, true
		}
	}
	return 0, false
}

// Model LLM 模型配置（唯一存储于数据库 models 表）
type Model struct {
	ID                  int64
	Name                string // 逻辑名称，全局唯一，聊天请求按此引用
	BaseURL             string
	APIKey              string // 明文存储，支持 ${ENV_VAR} 引用
	Model               string // 实际模型 ID
	MaxCompletionTokens int
	MaxContextTokens    int // 输入上下文 token 预算（0 表示不限制）
	Temperature         float64
	TopP                float64
	FrequencyPenalty    float64
	PresencePenalty     float64
	Seed                int
	Stop                []string
	Thinking            bool
	DefaultFlags        DefaultFlag // 持有的默认类型，每种类型全表至多一条持有
	Enabled             bool        // 禁用后不出现在聊天下拉框
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Has 判断模型是否持有某一默认类型
func (m *Model) Has(f DefaultFlag) bool { return m.DefaultFlags&f != 0 }

// ModelRepo 模型配置存储接口
type ModelRepo interface {
	// Create 按 m.DefaultFlags 原样写入，每类型默认唯一性由调用方（业务层）保证；不回填 m.ID
	Create(ctx context.Context, m *Model) error
	// GetByName 按名称查询，未找到返回 ErrNotFound
	GetByName(ctx context.Context, name string) (*Model, error)
	// GetDefault 查询持有 flag 类型默认的模型，无则返回 ErrNotFound
	GetDefault(ctx context.Context, flag DefaultFlag) (*Model, error)
	// List 返回全部模型，按 name 升序
	List(ctx context.Context) ([]*Model, error)
	// Update 按原名称 name 更新除 default_flags、created_at 外的全部字段（含重命名为 m.Name）；
	// 未找到返回 ErrNotFound。默认类型仅由 SetDefault / ClearDefault 变更
	Update(ctx context.Context, name string, m *Model) error
	// Delete 按名称删除；未找到返回 ErrNotFound
	Delete(ctx context.Context, name string) error
	// SetDefault 事务内先清除全表 flag 位再为目标行置位；目标不存在返回 ErrNotFound
	SetDefault(ctx context.Context, name string, flag DefaultFlag) error
	// ClearDefault 清除目标行的 flag 位（未持有时无操作）；目标不存在返回 ErrNotFound
	ClearDefault(ctx context.Context, name string, flag DefaultFlag) error
	Count(ctx context.Context) (int64, error)
}
