package cluster

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// countingRepo 包装真实 MemberRepo，统计 UpdateRole 的调用次数，
// 用于验证 leader 心跳在角色未变化时不会发出多余的 UPDATE。
type countingRepo struct {
	repo.MemberRepo
	updateRoleCalls int32
}

func (r *countingRepo) UpdateRole(ctx context.Context, regID, role string) error {
	atomic.AddInt32(&r.updateRoleCalls, 1)
	return r.MemberRepo.UpdateRole(ctx, regID, role)
}

func (r *countingRepo) calls() int32 { return atomic.LoadInt32(&r.updateRoleCalls) }

// newManualCluster 创建一个不启动 ticker 的 Cluster，测试里手动调用 heartbeat()，
// 避免依赖 3 秒定时器带来的时序不确定性。
func newManualCluster(t *testing.T, port int, r repo.MemberRepo) *Cluster {
	t.Helper()
	c := New("127.0.0.1", port, newTestLogger(), r)
	c.ctx, c.cancel = context.WithCancel(context.Background())
	t.Cleanup(c.Leave)
	c.register()
	return c
}

func getMember(t *testing.T, r repo.MemberRepo, regID string) *repo.Member {
	t.Helper()
	m, err := r.Get(context.Background(), regID)
	if err != nil {
		t.Fatalf("Get(%s): %v", regID, err)
	}
	return m
}

// 表中角色已是 leader 时，多轮心跳不应调用 UpdateRole，但心跳时间必须持续刷新。
func TestLeaderHeartbeat_SkipsUpdateRoleWhenUnchanged(t *testing.T) {
	cr := &countingRepo{MemberRepo: newTestRepo(t)}
	c := newManualCluster(t, 8080, cr)
	if !c.IsLeader() {
		t.Fatal("single instance should be leader")
	}
	if cr.calls() != 0 {
		t.Fatalf("register should not call UpdateRole, got %d", cr.calls())
	}
	before := getMember(t, cr, c.RegID()).HeartbeatAt

	time.Sleep(2 * time.Millisecond)
	for i := 0; i < 3; i++ {
		c.heartbeat()
	}

	if cr.calls() != 0 {
		t.Errorf("expected 0 UpdateRole calls when role unchanged, got %d", cr.calls())
	}
	m := getMember(t, cr, c.RegID())
	if m.Role != RoleLeader {
		t.Errorf("db role = %s, want leader", m.Role)
	}
	if !m.HeartbeatAt.After(before) {
		t.Errorf("heartbeat_at not refreshed: before=%v after=%v", before, m.HeartbeatAt)
	}
	if !c.IsLeader() {
		t.Error("should remain leader")
	}
}

// 表中角色与内存不一致（模拟提升时写角色失败）时，leader 心跳应自动写回。
func TestLeaderHeartbeat_RepairsRoleWhenDrifted(t *testing.T) {
	base := newTestRepo(t)
	cr := &countingRepo{MemberRepo: base}
	c := newManualCluster(t, 8080, cr)

	// 直接篡改表中角色，绕过计数器
	if err := base.UpdateRole(context.Background(), c.RegID(), RoleFollower); err != nil {
		t.Fatalf("seed drift: %v", err)
	}

	c.heartbeat()
	if cr.calls() != 1 {
		t.Errorf("expected exactly 1 UpdateRole call to repair drift, got %d", cr.calls())
	}
	if got := getMember(t, cr, c.RegID()).Role; got != RoleLeader {
		t.Errorf("db role = %s, want leader after repair", got)
	}

	// 修复之后再心跳，不应再写
	c.heartbeat()
	if cr.calls() != 1 {
		t.Errorf("expected no further UpdateRole calls, got %d", cr.calls())
	}
}

// 自身记录丢失时走重注册路径：Register 直接带角色写入，不应调用 UpdateRole，
// 且单实例重注册后仍为 leader。
func TestHeartbeat_RecordLost_ReregistersWithoutUpdateRole(t *testing.T) {
	cr := &countingRepo{MemberRepo: newTestRepo(t)}
	lost := 0
	c := newManualCluster(t, 8080, cr)
	c.SetCallbacks(nil, func() { lost++ })
	oldID := c.RegID()

	if err := cr.Remove(context.Background(), oldID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	c.heartbeat()

	if c.RegID() == oldID || c.RegID() == "" {
		t.Errorf("expected new reg_id after record loss, got %q", c.RegID())
	}
	if lost != 1 {
		t.Errorf("onLoseLeader calls = %d, want 1", lost)
	}
	if !c.IsLeader() {
		t.Error("single instance should be re-elected leader")
	}
	if cr.calls() != 0 {
		t.Errorf("re-register path should not call UpdateRole, got %d", cr.calls())
	}
	if got := getMember(t, cr, c.RegID()).Role; got != RoleLeader {
		t.Errorf("db role = %s, want leader", got)
	}
}

// follower 提升为 leader 的选主路径不受影响：提升那一轮写一次角色，之后不再写。
func TestFollowerPromotion_WritesRoleOnce(t *testing.T) {
	base := newTestRepo(t)
	leader := newManualCluster(t, 8080, &countingRepo{MemberRepo: base})
	if !leader.IsLeader() {
		t.Fatal("first instance should be leader")
	}

	time.Sleep(2 * time.Millisecond)
	fr := &countingRepo{MemberRepo: base}
	promoted := 0
	follower := newManualCluster(t, 8081, fr)
	follower.SetCallbacks(func() { promoted++ }, nil)
	if follower.IsLeader() {
		t.Fatal("second instance should be follower")
	}

	// follower 常规心跳：不写角色
	follower.heartbeat()
	if fr.calls() != 0 {
		t.Fatalf("follower heartbeat should not call UpdateRole, got %d", fr.calls())
	}

	leader.Leave()
	follower.heartbeat()

	if !follower.IsLeader() {
		t.Fatal("follower should be promoted after leader leaves")
	}
	if promoted != 1 {
		t.Errorf("onBecomeLeader calls = %d, want 1", promoted)
	}
	if fr.calls() != 1 {
		t.Errorf("promotion should call UpdateRole exactly once, got %d", fr.calls())
	}
	if got := getMember(t, fr, follower.RegID()).Role; got != RoleLeader {
		t.Errorf("db role = %s, want leader", got)
	}

	// 提升后作为 leader 继续心跳：角色已一致，不再写
	follower.heartbeat()
	follower.heartbeat()
	if fr.calls() != 1 {
		t.Errorf("post-promotion heartbeats should not call UpdateRole, got %d", fr.calls())
	}
}
