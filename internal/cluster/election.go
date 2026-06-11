package cluster

import (
	"sort"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

const (
	RoleLeader   = "leader"
	RoleFollower = "follower"
)

// DetermineRole decides whether selfID should be leader.
// members: all known members from MemberRepo.ListAll().
// timeout: heartbeat timeout for alive filtering.
func DetermineRole(selfID string, members []*repo.Member, timeout time.Duration) string {
	now := time.Now()
	var alive []*repo.Member
	for _, m := range members {
		if now.Sub(m.HeartbeatAt) < timeout {
			alive = append(alive, m)
		}
	}
	if len(alive) == 0 {
		return RoleLeader
	}
	sort.Slice(alive, func(i, j int) bool { return alive[i].RegID < alive[j].RegID })
	if alive[0].RegID == selfID {
		return RoleLeader
	}
	return RoleFollower
}
