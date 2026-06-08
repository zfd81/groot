package cluster

import (
	"testing"
	"time"

	"github.com/zfd81/groot/internal/storage"
)

func newTestStore() *storage.Local {
	return storage.NewLocal()
}

func TestWriteRegistrationFile(t *testing.T) {
	store := newTestStore()
	dir := t.TempDir()
	err := WriteRegistration(store, dir, "20260515143022123", "leader", "127.0.0.1", 8080, 12345)
	if err != nil {
		t.Fatalf("WriteRegistration failed: %v", err)
	}
	members, err := ListMembers(store, dir)
	if err != nil {
		t.Fatalf("ListMembers failed: %v", err)
	}
	if len(members) != 1 || members[0].ID != "20260515143022123" {
		t.Errorf("expected single member 20260515143022123, got %+v", members)
	}
	// 验证文件内容(role|host:port|pid)通过 ReadRegistration
	content, err := ReadRegistration(store, dir, "20260515143022123")
	if err != nil {
		t.Fatalf("ReadRegistration: %v", err)
	}
	if content != "leader|127.0.0.1:8080|12345" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestListMembers(t *testing.T) {
	store := newTestStore()
	dir := t.TempDir()
	if err := WriteRegistration(store, dir, "20260515143021123", "leader", "127.0.0.1", 8080, 11111); err != nil {
		t.Fatalf("WriteRegistration: %v", err)
	}
	if err := WriteRegistration(store, dir, "20260515143022123", "follower", "127.0.0.1", 8081, 22222); err != nil {
		t.Fatalf("WriteRegistration: %v", err)
	}
	if err := WriteRegistration(store, dir, "20260515143023123", "follower", "127.0.0.1", 8082, 33333); err != nil {
		t.Fatalf("WriteRegistration: %v", err)
	}

	members, err := ListMembers(store, dir)
	if err != nil {
		t.Fatalf("ListMembers failed: %v", err)
	}
	if len(members) != 3 {
		t.Errorf("expected 3 members, got %d", len(members))
	}
}

func TestListMembers_EmptyDir(t *testing.T) {
	store := newTestStore()
	dir := t.TempDir()
	members, err := ListMembers(store, dir)
	if err != nil {
		t.Fatalf("ListMembers failed: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members, got %d", len(members))
	}
}

func TestListMembers_NonExistentDir(t *testing.T) {
	store := newTestStore()
	dir := t.TempDir() + "/does-not-exist"
	members, err := ListMembers(store, dir)
	if err != nil {
		t.Fatalf("ListMembers should not error on non-existent dir, got: %v", err)
	}
	if members != nil {
		t.Errorf("expected nil for non-existent dir, got %+v", members)
	}
}

func TestRemoveFile(t *testing.T) {
	store := newTestStore()
	dir := t.TempDir()
	if err := WriteRegistration(store, dir, "stale", "follower", "127.0.0.1", 8080, 11111); err != nil {
		t.Fatalf("WriteRegistration: %v", err)
	}
	err := RemoveFile(store, dir, "stale")
	if err != nil {
		t.Fatalf("RemoveFile failed: %v", err)
	}
	members, _ := ListMembers(store, dir)
	if len(members) != 0 {
		t.Errorf("expected file to be removed, found %d members", len(members))
	}
}

func TestRemoveFile_NonExistent(t *testing.T) {
	store := newTestStore()
	dir := t.TempDir()
	// 不存在视为成功(幂等)
	if err := RemoveFile(store, dir, "non-existent"); err != nil {
		t.Fatalf("RemoveFile should be idempotent, got: %v", err)
	}
}

func TestEnsureMembersDir(t *testing.T) {
	store := newTestStore()
	homeDir := t.TempDir()
	membersDir, err := EnsureMembersDir(homeDir, store)
	if err != nil {
		t.Fatalf("EnsureMembersDir failed: %v", err)
	}
	if membersDir == "" {
		t.Error("expected non-empty membersDir")
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
	store := newTestStore()
	dir := t.TempDir()
	if err := WriteRegistration(store, dir, "test", "leader", "127.0.0.1", 8080, 12345); err != nil {
		t.Fatalf("WriteRegistration 1: %v", err)
	}
	members1, _ := ListMembers(store, dir)
	if len(members1) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members1))
	}
	mtime1 := members1[0].Mtime

	time.Sleep(20 * time.Millisecond)
	if err := WriteRegistration(store, dir, "test", "leader", "127.0.0.1", 8080, 12345); err != nil {
		t.Fatalf("WriteRegistration 2: %v", err)
	}
	members2, _ := ListMembers(store, dir)
	mtime2 := members2[0].Mtime

	if !mtime2.After(mtime1) {
		t.Errorf("expected mtime to advance after re-write: mtime1=%v mtime2=%v", mtime1, mtime2)
	}
}
