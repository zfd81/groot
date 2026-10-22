package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// probeMemberRepo 是 repo.MemberRepo 的最小实现：othersAliveProbe 只消费 ListAll。
type probeMemberRepo struct {
	members []*repo.Member
	err     error
}

var errProbeNotImpl = errors.New("not implemented")

func (f *probeMemberRepo) ListAll(context.Context) ([]*repo.Member, error) { return f.members, f.err }
func (f *probeMemberRepo) Register(context.Context, *repo.Member) error    { return errProbeNotImpl }
func (f *probeMemberRepo) Heartbeat(context.Context, string) error         { return errProbeNotImpl }
func (f *probeMemberRepo) UpdateRole(context.Context, string, string) error {
	return errProbeNotImpl
}
func (f *probeMemberRepo) Get(context.Context, string) (*repo.Member, error) {
	return nil, errProbeNotImpl
}
func (f *probeMemberRepo) Remove(context.Context, string) error { return errProbeNotImpl }
func (f *probeMemberRepo) RemoveExpired(context.Context, time.Time) (int, error) {
	return 0, errProbeNotImpl
}

func TestOthersAliveProbe(t *testing.T) {
	now := time.Now()
	self := func() string { return "n-self" }
	cases := []struct {
		name string
		repo *probeMemberRepo
		want bool
	}{
		{"self only", &probeMemberRepo{members: []*repo.Member{{RegID: "n-self", HeartbeatAt: now}}}, false},
		{"self and fresh other", &probeMemberRepo{members: []*repo.Member{
			{RegID: "n-self", HeartbeatAt: now},
			{RegID: "n-other", HeartbeatAt: now.Add(-time.Second)},
		}}, true},
		{"self and stale other", &probeMemberRepo{members: []*repo.Member{
			{RegID: "n-self", HeartbeatAt: now},
			{RegID: "n-other", HeartbeatAt: now.Add(-10 * time.Second)},
		}}, false},
		{"empty list", &probeMemberRepo{}, false},
		{"list error", &probeMemberRepo{err: errors.New("db down")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := othersAliveProbe(tc.repo, self, memberAliveTimeout)
			if got := probe(context.Background()); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
