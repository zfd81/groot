package memberdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.MemberRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func TestRegisterAndGet(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	m := &repo.Member{
		RegID: "20260610143022123", Role: "follower",
		Host: "127.0.0.1", Port: 8080, Pid: 1234,
		HeartbeatAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := r.Register(ctx, m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Get(ctx, m.RegID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Role != "follower" {
		t.Errorf("expected follower, got %s", got.Role)
	}
}

func TestRegister_Idempotent(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	m := &repo.Member{
		RegID: "20260610143022123", Role: "follower",
		Host: "127.0.0.1", Port: 8080, Pid: 1,
		HeartbeatAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := r.Register(ctx, m); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	m.Role = "leader"
	if err := r.Register(ctx, m); err != nil {
		t.Fatalf("second Register (upsert): %v", err)
	}
	got, _ := r.Get(ctx, m.RegID)
	if got.Role != "leader" {
		t.Errorf("expected role updated to leader, got %s", got.Role)
	}
}

func TestHeartbeat_NotFound(t *testing.T) {
	r := newTestRepo(t)
	err := r.Heartbeat(context.Background(), "nonexistent")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateRole(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	m := &repo.Member{
		RegID: "20260610000000001", Role: "follower",
		Host: "127.0.0.1", Port: 8080, Pid: 1,
		HeartbeatAt: time.Now(), CreatedAt: time.Now(),
	}
	r.Register(ctx, m)
	if err := r.UpdateRole(ctx, m.RegID, "leader"); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	got, _ := r.Get(ctx, m.RegID)
	if got.Role != "leader" {
		t.Errorf("expected leader, got %s", got.Role)
	}
}

func TestRemoveExpired(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	old := &repo.Member{
		RegID: "20260101000000000", Role: "follower",
		Host: "127.0.0.1", Port: 8080, Pid: 1,
		HeartbeatAt: time.Now().Add(-1 * time.Hour), CreatedAt: time.Now(),
	}
	r.Register(ctx, old)
	n, err := r.RemoveExpired(ctx, time.Now().Add(-30*time.Second))
	if err != nil {
		t.Fatalf("RemoveExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 removed, got %d", n)
	}
}

func TestListAll(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for i, id := range []string{"20260610000000001", "20260610000000002"} {
		r.Register(ctx, &repo.Member{
			RegID: id, Role: "follower",
			Host: "127.0.0.1", Port: 8080 + i, Pid: i + 1,
			HeartbeatAt: time.Now(), CreatedAt: time.Now(),
		})
	}
	members, err := r.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("expected 2 members, got %d", len(members))
	}
}
