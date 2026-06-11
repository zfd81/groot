// internal/repo/resource.go
package repo

import (
	"context"
	"time"
)

type Resource struct {
	Path        string
	Content     []byte
	ContentType string
	Size        int64
	ContentHash string
	UpdatedAt   time.Time
}

type ResourceEntry struct {
	Path        string
	Size        int64
	ContentHash string
	UpdatedAt   time.Time
}

type ResourceRepo interface {
	Put(ctx context.Context, r *Resource) error
	Get(ctx context.Context, path string) (*Resource, error)
	Stat(ctx context.Context, path string) (*ResourceEntry, error)
	List(ctx context.Context, prefix string) ([]*ResourceEntry, error)
	Delete(ctx context.Context, path string) error
}
