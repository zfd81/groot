package cluster

import (
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

func TestDetermineRole_NoAliveMembers(t *testing.T) {
	role := DetermineRole("20260515143022123", nil, 7*time.Second)
	if role != RoleLeader {
		t.Errorf("expected leader, got %s", role)
	}
}

func TestDetermineRole_SelfIsSmallest(t *testing.T) {
	members := []*repo.Member{
		{RegID: "20260515143022123", HeartbeatAt: time.Now()},
		{RegID: "20260515143023123", HeartbeatAt: time.Now()},
		{RegID: "20260515143024123", HeartbeatAt: time.Now()},
	}
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != RoleLeader {
		t.Errorf("expected leader, got %s", role)
	}
}

func TestDetermineRole_SelfIsNotSmallest(t *testing.T) {
	members := []*repo.Member{
		{RegID: "20260515143021123", HeartbeatAt: time.Now()},
		{RegID: "20260515143022123", HeartbeatAt: time.Now()},
		{RegID: "20260515143023123", HeartbeatAt: time.Now()},
	}
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != RoleFollower {
		t.Errorf("expected follower, got %s", role)
	}
}

func TestDetermineRole_StaleMembersExcluded(t *testing.T) {
	members := []*repo.Member{
		{RegID: "20260515143021123", HeartbeatAt: time.Now().Add(-10 * time.Second)}, // stale
		{RegID: "20260515143022123", HeartbeatAt: time.Now()},
	}
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != RoleLeader {
		t.Errorf("expected leader after excluding stale, got %s", role)
	}
}

func TestDetermineRole_AllStale(t *testing.T) {
	members := []*repo.Member{
		{RegID: "20260515143021123", HeartbeatAt: time.Now().Add(-10 * time.Second)},
		{RegID: "20260515143022123", HeartbeatAt: time.Now().Add(-10 * time.Second)},
	}
	role := DetermineRole("20260515143025123", members, 7*time.Second)
	if role != RoleLeader {
		t.Errorf("expected leader when all stale, got %s", role)
	}
}

func TestDetermineRole_SelfStaleOthersAlive(t *testing.T) {
	members := []*repo.Member{
		{RegID: "20260515143022000", HeartbeatAt: time.Now().Add(-10 * time.Second)}, // self, stale
		{RegID: "20260515143023000", HeartbeatAt: time.Now()},                        // alive, larger ID
	}
	role := DetermineRole("20260515143022000", members, 7*time.Second)
	if role != RoleFollower {
		t.Errorf("stale self should not be leader, got %s", role)
	}
}
