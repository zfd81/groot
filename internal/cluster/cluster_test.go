package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/memberdb"
)

func newTestRepo(t *testing.T) repo.MemberRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return memberdb.New(sqlxDB, dialect)
}

func newTestLogger() *logger.Logger {
	return logger.NewNop()
}

func TestCluster_SingleInstance_BecomesLeader(t *testing.T) {
	repo := newTestRepo(t)
	log := newTestLogger()

	c := New("127.0.0.1", 8080, log, repo)

	leaderCh := make(chan struct{}, 1)
	c.SetCallbacks(func() { leaderCh <- struct{}{} }, nil)

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer c.Leave()

	select {
	case <-leaderCh:
		// became leader via callback
	case <-time.After(2 * time.Second):
		// single instance should self-elect immediately on register
	}

	if !c.IsLeader() {
		t.Error("single instance should be leader")
	}
	if c.RegID() == "" {
		t.Error("expected non-empty registration ID")
	}
}

func TestCluster_TwoInstances_OneLeaderOneFollower(t *testing.T) {
	memberRepo := newTestRepo(t)
	log := newTestLogger()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leader := New("127.0.0.1", 8080, log, memberRepo)
	if err := leader.Join(ctx); err != nil {
		t.Fatalf("leader Join: %v", err)
	}
	defer leader.Leave()

	if !leader.IsLeader() {
		t.Fatal("first instance should be leader")
	}

	time.Sleep(time.Millisecond)
	follower := New("127.0.0.1", 8081, log, memberRepo)
	if err := follower.Join(ctx); err != nil {
		t.Fatalf("follower Join: %v", err)
	}
	defer follower.Leave()

	if follower.IsLeader() {
		t.Error("second instance should be follower")
	}
}

func TestCluster_Leave_RemovesRecord(t *testing.T) {
	memberRepo := newTestRepo(t)
	log := newTestLogger()

	c := New("127.0.0.1", 8080, log, memberRepo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Join(ctx); err != nil {
		t.Fatalf("Join: %v", err)
	}

	c.Leave()

	members, err := memberRepo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members after Leave, got %d", len(members))
	}
}

func TestCluster_Callbacks_OnBecomeLeader(t *testing.T) {
	memberRepo := newTestRepo(t)
	log := newTestLogger()

	becomeCalled := make(chan struct{}, 1)
	c := New("127.0.0.1", 8080, log, memberRepo)
	c.SetCallbacks(func() { becomeCalled <- struct{}{} }, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Join(ctx); err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer c.Leave()

	select {
	case <-becomeCalled:
		// expected
	case <-time.After(time.Second):
		t.Error("onBecomeLeader was not called")
	}
}

func TestCluster_Callbacks_OnLoseLeader(t *testing.T) {
	memberRepo := newTestRepo(t)
	log := newTestLogger()

	loseCalled := make(chan struct{}, 1)
	c := New("127.0.0.1", 8080, log, memberRepo)
	c.SetCallbacks(nil, func() { loseCalled <- struct{}{} })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Join(ctx); err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer c.Leave()

	if !c.IsLeader() {
		t.Fatal("expected leader")
	}

	// Delete the registration record to simulate crash
	if err := memberRepo.Remove(context.Background(), c.RegID()); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	select {
	case <-loseCalled:
		// expected
	case <-time.After(4 * time.Second):
		t.Error("onLoseLeader was not called after record deletion")
	}
}

func TestCluster_FollowerPromotion_AfterLeaderLeaves(t *testing.T) {
	memberRepo := newTestRepo(t)
	log := newTestLogger()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leader := New("127.0.0.1", 8080, log, memberRepo)
	if err := leader.Join(ctx); err != nil {
		t.Fatalf("leader Join: %v", err)
	}
	if !leader.IsLeader() {
		t.Fatal("first instance should be leader")
	}

	time.Sleep(time.Millisecond)
	becomeCalled := make(chan struct{}, 1)
	follower := New("127.0.0.1", 8081, log, memberRepo)
	follower.SetCallbacks(func() { becomeCalled <- struct{}{} }, nil)
	if err := follower.Join(ctx); err != nil {
		t.Fatalf("follower Join: %v", err)
	}
	defer follower.Leave()

	if follower.IsLeader() {
		t.Fatal("second instance should start as follower")
	}

	// Leader leaves cleanly
	leader.Leave()

	select {
	case <-becomeCalled:
		// expected
	case <-time.After(5 * time.Second):
		t.Error("onBecomeLeader not called when follower promoted")
	}

	if !follower.IsLeader() {
		t.Error("follower should be leader after promotion")
	}
}

func TestCluster_MultipleInstances_SingleLeader(t *testing.T) {
	memberRepo := newTestRepo(t)
	log := newTestLogger()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	instances := []*Cluster{
		New("127.0.0.1", 8080, log, memberRepo),
		New("127.0.0.1", 8081, log, memberRepo),
		New("127.0.0.1", 8082, log, memberRepo),
	}

	for i, inst := range instances {
		if i > 0 {
			time.Sleep(time.Millisecond)
		}
		if err := inst.Join(ctx); err != nil {
			t.Fatalf("instance %d Join: %v", i, err)
		}
		defer inst.Leave()
	}

	leaderCount, followerCount := 0, 0
	for i, inst := range instances {
		if inst.IsLeader() {
			leaderCount++
		} else {
			followerCount++
		}
		t.Logf("instance %d (reg=%s): role=%s", i, inst.RegID(), inst.Role())
	}

	if leaderCount != 1 {
		t.Errorf("expected exactly 1 leader, got %d", leaderCount)
	}
	if followerCount != 2 {
		t.Errorf("expected 2 followers, got %d", followerCount)
	}
}

func TestCluster_Heartbeat_RecordLost_Reregisters(t *testing.T) {
	memberRepo := newTestRepo(t)
	log := newTestLogger()

	c := New("127.0.0.1", 8080, log, memberRepo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Join(ctx); err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer c.Leave()

	oldRegID := c.RegID()

	// Simulate record deletion (crash / external cleanup)
	memberRepo.Remove(context.Background(), oldRegID)

	// Wait for heartbeat to detect and re-register
	time.Sleep(4 * time.Second)

	newRegID := c.RegID()
	if newRegID == oldRegID {
		t.Error("expected new registration ID after record loss")
	}
	if newRegID == "" {
		t.Error("expected non-empty registration ID after re-registration")
	}
}
