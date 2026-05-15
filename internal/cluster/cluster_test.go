package cluster

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/logger"
)

func TestCluster_JoinAsLeader_NoExistingMembers(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()
	c := New(homeDir, "127.0.0.1", 8080, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Join(ctx)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	defer c.Leave()

	if !c.IsLeader() {
		t.Error("expected leader when no other members")
	}
	if c.RegID() == "" {
		t.Error("expected non-empty registration ID")
	}

	// verify file was created
	membersDir := filepath.Join(homeDir, "cluster", "members")
	files, _ := os.ReadDir(membersDir)
	if len(files) != 1 {
		t.Fatalf("expected 1 registration file, got %d", len(files))
	}
	content, _ := os.ReadFile(filepath.Join(membersDir, files[0].Name()))
	expectedPrefix := "leader|127.0.0.1:8080|"
	if string(content)[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("unexpected file content: %s", string(content))
	}
}

func TestCluster_JoinAsFollower_ExistingLeader(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()

	// start first instance (leader)
	leader := New(homeDir, "127.0.0.1", 8080, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := leader.Join(ctx)
	if err != nil {
		t.Fatalf("leader Join failed: %v", err)
	}
	defer leader.Leave()

	if !leader.IsLeader() {
		t.Fatal("first instance should be leader")
	}

	// start second instance (follower)
	// Small delay to ensure different regID (same-millisecond startup is not
	// a real-world concern per design doc).
	time.Sleep(time.Millisecond)
	follower := New(homeDir, "127.0.0.1", 8081, log)
	err = follower.Join(ctx)
	if err != nil {
		t.Fatalf("follower Join failed: %v", err)
	}
	defer follower.Leave()

	if follower.IsLeader() {
		t.Error("second instance should be follower")
	}
}

func TestCluster_Heartbeat_FileLost(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()

	c := New(homeDir, "127.0.0.1", 8080, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Join(ctx)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	defer c.Leave()

	oldRegID := c.RegID()

	// simulate file deletion
	membersDir := filepath.Join(homeDir, "cluster", "members")
	RemoveFile(membersDir, oldRegID)

	// wait for heartbeat to re-register
	time.Sleep(3500 * time.Millisecond)

	newRegID := c.RegID()
	if newRegID == oldRegID {
		t.Error("expected new registration ID after file loss")
	}
	if newRegID == "" {
		t.Error("expected non-empty registration ID after re-registration")
	}
}

func TestCluster_Heartbeat_LeaderCleanupStale(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()

	leader := New(homeDir, "127.0.0.1", 8080, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := leader.Join(ctx)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	defer leader.Leave()

	// write a stale file manually (simulating dead instance)
	membersDir := filepath.Join(homeDir, "cluster", "members")
	WriteRegistration(membersDir, "20200101000000001", "follower", "127.0.0.1", 9000, 99999)

	// set its mtime to old
	oldTime := time.Now().Add(-10 * time.Second)
	os.Chtimes(filepath.Join(membersDir, "20200101000000001"), oldTime, oldTime)

	// wait for leader heartbeat to clean up
	time.Sleep(3500 * time.Millisecond)

	_, err = os.Stat(filepath.Join(membersDir, "20200101000000001"))
	if !os.IsNotExist(err) {
		t.Error("expected stale file to be cleaned up by leader")
	}
}

func TestCluster_Leave(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()

	c := New(homeDir, "127.0.0.1", 8080, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Join(ctx)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	c.Leave()

	membersDir := filepath.Join(homeDir, "cluster", "members")
	files, _ := os.ReadDir(membersDir)
	if len(files) != 0 {
		t.Errorf("expected 0 files after Leave, got %d", len(files))
	}
}
