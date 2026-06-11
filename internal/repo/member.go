// internal/repo/member.go
package repo

import (
	"context"
	"time"
)

type Member struct {
	RegID       string
	Role        string
	Host        string
	Port        int
	Pid         int
	HeartbeatAt time.Time
	CreatedAt   time.Time
}

type MemberRepo interface {
	Register(ctx context.Context, m *Member) error
	Heartbeat(ctx context.Context, regID string) error
	UpdateRole(ctx context.Context, regID, role string) error
	Get(ctx context.Context, regID string) (*Member, error)
	ListAll(ctx context.Context) ([]*Member, error)
	Remove(ctx context.Context, regID string) error
	RemoveExpired(ctx context.Context, expiredBefore time.Time) (int, error)
}
