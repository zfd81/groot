package repo

import (
	"context"
	"time"
)

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
	IsDefault           bool // 全表至多一条为真
	Enabled             bool // 禁用后不出现在聊天下拉框
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ModelRepo 模型配置存储接口
type ModelRepo interface {
	// Create 按 m.IsDefault 原样写入，默认唯一性由调用方（业务层）保证；不回填 m.ID
	Create(ctx context.Context, m *Model) error
	// GetByName 按名称查询，未找到返回 ErrNotFound
	GetByName(ctx context.Context, name string) (*Model, error)
	// GetDefault 查询默认模型，无默认返回 ErrNotFound
	GetDefault(ctx context.Context) (*Model, error)
	// List 返回全部模型，按 name 升序
	List(ctx context.Context) ([]*Model, error)
	// Update 按原名称 name 更新除 is_default、created_at 外的全部字段（含重命名为 m.Name）；
	// 未找到返回 ErrNotFound。默认标记仅由 SetDefault 变更
	Update(ctx context.Context, name string, m *Model) error
	// Delete 按名称删除；未找到返回 ErrNotFound
	Delete(ctx context.Context, name string) error
	// SetDefault 事务内先清除全表 is_default 再设置目标行；未找到返回 ErrNotFound
	SetDefault(ctx context.Context, name string) error
	Count(ctx context.Context) (int64, error)
}
