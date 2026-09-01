package repo

import (
	"context"
	"time"
)

// User Web 登录用户
type User struct {
	ID           string     // 系统编号，格式 yyyyMMddHHmmss
	Username     string     // 用户名，唯一
	PasswordHash string     // bcrypt 哈希
	CreatedAt    time.Time  // 创建时间
	UpdatedAt    time.Time  // 修改时间
	LastLoginAt  *time.Time // 最后登录时间，nil 表示从未登录
}

// UserRepo Web 用户存储接口
type UserRepo interface {
	Create(ctx context.Context, u *User) error
	// GetByUsername 按用户名查询，未找到返回 ErrNotFound
	GetByUsername(ctx context.Context, username string) (*User, error)
	// GetByID 按系统编号查询，未找到返回 ErrNotFound
	GetByID(ctx context.Context, id string) (*User, error)
	Count(ctx context.Context) (int64, error)
	// UpdatePassword 更新密码哈希，同时刷新 updated_at；未找到返回 ErrNotFound
	UpdatePassword(ctx context.Context, id, passwordHash string) error
	// UpdateLastLogin 更新最后登录时间；未找到返回 ErrNotFound
	UpdateLastLogin(ctx context.Context, id string, at time.Time) error
	// DeleteAll 删除全部用户，返回删除条数
	DeleteAll(ctx context.Context) (int64, error)
}
