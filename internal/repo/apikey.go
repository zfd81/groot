package repo

import (
	"context"
	"time"
)

// APIKey 对外 API 访问凭证的元数据。完整 JWT 不落库：
// 由 config 中的 secret + 本结构按需确定性还原（见 internal/auth）。
type APIKey struct {
	ID          string   // 主键，yyyyMMddHHmmss 格式（如 "20260902153045"），同时作为 JWT 的 jti
	Name        string   // 全局唯一
	Permissions []string // 权限点集合，创建时校验非空
	ExpiresAt   time.Time
	CreatedAt   time.Time // 秒级精度（签发 JWT 时取 Unix 秒）
}

// ValidPermissions API Key 可用的权限点全集，
// 与 middleware.getRequiredPermission 的路径映射保持一致。
var ValidPermissions = []string{"chat", "status", "detail", "history", "session", "schedule", "all"}

// APIKeyRepo API Key 元数据存储接口
type APIKeyRepo interface {
	// Create 按 k.ID 原样写入；主键或名称唯一冲突时返回底层驱动错误
	Create(ctx context.Context, k *APIKey) error
	// GetByID 按 ID 查询，未找到返回 ErrNotFound
	GetByID(ctx context.Context, id string) (*APIKey, error)
	// GetByName 按名称查询，未找到返回 ErrNotFound
	GetByName(ctx context.Context, name string) (*APIKey, error)
	// List 返回全部 Key，按 created_at 降序
	List(ctx context.Context) ([]*APIKey, error)
	// DeleteByID 按 ID 删除；未找到返回 ErrNotFound
	DeleteByID(ctx context.Context, id string) error
}
