package cluster

import (
	"sort"
	"time"
)

const (
	RoleLeader   = "leader"
	RoleFollower = "follower"
)

// MemberInfo represents a cluster member's metadata from its registration file.
type MemberInfo struct {
	ID    string
	Mtime time.Time
}

// DetermineRole determines whether this instance should be leader or follower.
// It filters out stale members (mtime older than timeout), sorts the survivors
// by ID, and returns "leader" if selfID is the smallest or there are no survivors.
func DetermineRole(selfID string, members []MemberInfo, timeout time.Duration) string {
	now := time.Now()
	var alive []MemberInfo
	for _, m := range members {
		if now.Sub(m.Mtime) < timeout {
			alive = append(alive, m)
		}
	}
	if len(alive) == 0 {
		return RoleLeader
	}
	sort.Slice(alive, func(i, j int) bool {
		return alive[i].ID < alive[j].ID
	})
	if selfID == alive[0].ID {
		return RoleLeader
	}
	return RoleFollower
}
