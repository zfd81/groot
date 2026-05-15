package cluster

import (
	"testing"
	"time"
)

func TestDetermineRole_NoAliveMembers(t *testing.T) {
	role := DetermineRole("20260515143022123", nil, 7*time.Second)
	if role != "leader" {
		t.Errorf("expected leader, got %s", role)
	}
}

func TestDetermineRole_SelfIsSmallest(t *testing.T) {
	members := []MemberInfo{
		{ID: "20260515143022123", Mtime: time.Now()},
		{ID: "20260515143023123", Mtime: time.Now()},
		{ID: "20260515143024123", Mtime: time.Now()},
	}
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != "leader" {
		t.Errorf("expected leader, got %s", role)
	}
}

func TestDetermineRole_SelfIsNotSmallest(t *testing.T) {
	members := []MemberInfo{
		{ID: "20260515143021123", Mtime: time.Now()},
		{ID: "20260515143022123", Mtime: time.Now()},
		{ID: "20260515143023123", Mtime: time.Now()},
	}
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != "follower" {
		t.Errorf("expected follower, got %s", role)
	}
}

func TestDetermineRole_StaleMembersExcluded(t *testing.T) {
	members := []MemberInfo{
		{ID: "20260515143021123", Mtime: time.Now().Add(-10 * time.Second)}, // stale
		{ID: "20260515143022123", Mtime: time.Now()},
	}
	// stale member excluded, self becomes leader among survivors
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != "leader" {
		t.Errorf("expected leader after excluding stale, got %s", role)
	}
}

func TestDetermineRole_AllStale(t *testing.T) {
	members := []MemberInfo{
		{ID: "20260515143021123", Mtime: time.Now().Add(-10 * time.Second)},
		{ID: "20260515143022123", Mtime: time.Now().Add(-10 * time.Second)},
	}
	role := DetermineRole("20260515143025123", members, 7*time.Second)
	if role != "leader" {
		t.Errorf("expected leader when all stale, got %s", role)
	}
}
