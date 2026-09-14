// internal/repo/resource.go
package repo

import (
	"context"
	"time"
)

// ResourceStatus 是资源记录的状态。已删除记录保留行以区分「他人删除」与「从无记录」。
type ResourceStatus = string

const (
	ResourceStatusActive  ResourceStatus = "active"
	ResourceStatusDeleted ResourceStatus = "deleted"
)

type Resource struct {
	Path        string
	Content     []byte
	ContentType string
	Size        int64
	ContentHash string
	UpdatedAt   time.Time
	Status      ResourceStatus
}

type ResourceEntry struct {
	Path        string
	Size        int64
	ContentHash string
	UpdatedAt   time.Time
	Status      ResourceStatus
}

type ResourceRepo interface {
	Put(ctx context.Context, r *Resource) error
	Get(ctx context.Context, path string) (*Resource, error)
	Stat(ctx context.Context, path string) (*ResourceEntry, error)
	// List 只返回有效记录（status=active）。
	List(ctx context.Context, prefix string) ([]*ResourceEntry, error)
	// ListWithDeleted 返回 prefix 下所有记录，含已标记删除的记录。
	// 仅供同步差异比较使用；常规读取请用 List。
	ListWithDeleted(ctx context.Context, prefix string) ([]*ResourceEntry, error)
	Delete(ctx context.Context, path string) error
}
