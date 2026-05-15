package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteRegistrationFile(t *testing.T) {
	dir := t.TempDir()
	err := WriteRegistration(dir, "20260515143022123", "leader", "127.0.0.1", 8080, 12345)
	if err != nil {
		t.Fatalf("WriteRegistration failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "20260515143022123"))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	expected := "leader|127.0.0.1:8080|12345"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

func TestListMembers(t *testing.T) {
	dir := t.TempDir()
	WriteRegistration(dir, "20260515143021123", "leader", "127.0.0.1", 8080, 11111)
	WriteRegistration(dir, "20260515143022123", "follower", "127.0.0.1", 8081, 22222)
	WriteRegistration(dir, "20260515143023123", "follower", "127.0.0.1", 8082, 33333)

	members, err := ListMembers(dir)
	if err != nil {
		t.Fatalf("ListMembers failed: %v", err)
	}
	if len(members) != 3 {
		t.Errorf("expected 3 members, got %d", len(members))
	}
	if members[0].ID != "20260515143021123" {
		t.Errorf("expected smallest ID first, got %s", members[0].ID)
	}
}

func TestListMembers_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	members, err := ListMembers(dir)
	if err != nil {
		t.Fatalf("ListMembers failed: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members, got %d", len(members))
	}
}

func TestRemoveStaleFile(t *testing.T) {
	dir := t.TempDir()
	WriteRegistration(dir, "stale", "follower", "127.0.0.1", 8080, 11111)
	err := RemoveFile(dir, "stale")
	if err != nil {
		t.Fatalf("RemoveFile failed: %v", err)
	}
	_, err = os.Stat(filepath.Join(dir, "stale"))
	if !os.IsNotExist(err) {
		t.Error("expected file to be removed")
	}
}

func TestEnsureMembersDir(t *testing.T) {
	homeDir := t.TempDir()
	membersDir, err := EnsureMembersDir(homeDir)
	if err != nil {
		t.Fatalf("EnsureMembersDir failed: %v", err)
	}
	info, err := os.Stat(membersDir)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected a directory")
	}
}

func TestGenerateRegID(t *testing.T) {
	id1 := GenerateRegID()

	if len(id1) != 17 {
		t.Errorf("expected length 17, got %d: %s", len(id1), id1)
	}

	for i, c := range id1 {
		if c < '0' || c > '9' {
			t.Errorf("expected all digits, got non-digit %c at position %d: %s", c, i, id1)
			break
		}
	}

	id2 := GenerateRegID()
	if id2 < id1 {
		t.Errorf("expected non-decreasing, got id1=%s, id2=%s", id1, id2)
	}
}

func TestFileMtimeUpdates(t *testing.T) {
	dir := t.TempDir()
	WriteRegistration(dir, "test", "leader", "127.0.0.1", 8080, 12345)
	info1, _ := os.Stat(filepath.Join(dir, "test"))
	mtime1 := info1.ModTime()

	time.Sleep(10 * time.Millisecond)
	WriteRegistration(dir, "test", "leader", "127.0.0.1", 8080, 12345)
	info2, _ := os.Stat(filepath.Join(dir, "test"))
	mtime2 := info2.ModTime()

	if !mtime2.After(mtime1) {
		t.Error("expected mtime to be updated after overwrite")
	}
}
