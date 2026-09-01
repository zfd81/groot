package websession

import (
	"fmt"
	"testing"
	"time"
)

func newTestStore(ttl time.Duration) (*Store, *time.Time) {
	now := time.Now()
	s := NewStore(ttl)
	s.now = func() time.Time { return now }
	return s, &now
}

// TestStore_CreateValidate 令牌创建后可校验并返回所属用户，且两次创建的令牌不同。
func TestStore_CreateValidate(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	tok := s.Create("u1")
	if tok == "" || len(tok) != 64 {
		t.Fatalf("token should be 64 hex chars, got %q", tok)
	}
	uid, ok := s.Validate(tok)
	if !ok || uid != "u1" {
		t.Errorf("fresh token should validate with userID u1, got (%q, %v)", uid, ok)
	}
	if _, ok := s.Validate("nonexistent"); ok {
		t.Error("unknown token should not validate")
	}
	if s.Create("u1") == tok {
		t.Error("tokens should be unique")
	}
}

// TestStore_Expiry 过期令牌校验失败。
func TestStore_Expiry(t *testing.T) {
	s, now := newTestStore(time.Hour)
	tok := s.Create("u1")
	*now = now.Add(2 * time.Hour)
	if _, ok := s.Validate(tok); ok {
		t.Error("expired token should not validate")
	}
}

// TestStore_SlidingRenewal 活跃访问滑动续期：每次 Validate 刷新过期时间；
// 持续不访问超过 TTL 则失效。
func TestStore_SlidingRenewal(t *testing.T) {
	s, now := newTestStore(time.Hour)
	tok := s.Create("u1")

	// 40 分钟后访问一次（续期），再过 40 分钟仍应有效（距上次访问未超 1h）
	*now = now.Add(40 * time.Minute)
	if _, ok := s.Validate(tok); !ok {
		t.Fatal("token should be valid at 40min")
	}
	*now = now.Add(40 * time.Minute)
	if _, ok := s.Validate(tok); !ok {
		t.Error("token should be renewed by previous Validate")
	}

	// 距上次访问 61 分钟不再访问，应失效
	*now = now.Add(61 * time.Minute)
	if _, ok := s.Validate(tok); ok {
		t.Error("token should expire after 61min of inactivity")
	}
}

// TestStore_Delete 删除后令牌失效。
func TestStore_Delete(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	tok := s.Create("u1")
	s.Delete(tok)
	if _, ok := s.Validate(tok); ok {
		t.Error("deleted token should not validate")
	}
}

// TestStore_DeleteOtherByUser 删除该用户除保留令牌外的所有会话，不影响其他用户。
func TestStore_DeleteOtherByUser(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	keep := s.Create("u1")
	other1 := s.Create("u1")
	other2 := s.Create("u1")
	alien := s.Create("u2")

	n := s.DeleteOtherByUser("u1", keep)
	if n != 2 {
		t.Errorf("DeleteOtherByUser removed %d, want 2", n)
	}
	if _, ok := s.Validate(keep); !ok {
		t.Error("keepToken should survive")
	}
	if _, ok := s.Validate(other1); ok {
		t.Error("other1 should be deleted")
	}
	if _, ok := s.Validate(other2); ok {
		t.Error("other2 should be deleted")
	}
	if uid, ok := s.Validate(alien); !ok || uid != "u2" {
		t.Error("other user's session should not be affected")
	}
}

// TestStore_FailureLockout 10 分钟内 5 次失败后锁定；窗口滑过后解锁；成功清零。
func TestStore_FailureLockout(t *testing.T) {
	s, now := newTestStore(time.Hour)
	ip := "1.2.3.4"
	for i := 0; i < 4; i++ {
		s.RecordFailure(ip)
	}
	if s.IsLocked(ip) {
		t.Error("4 failures should not lock")
	}
	s.RecordFailure(ip)
	if !s.IsLocked(ip) {
		t.Error("5 failures should lock")
	}
	if s.IsLocked("5.6.7.8") {
		t.Error("other ip should not be locked")
	}
	*now = now.Add(11 * time.Minute)
	if s.IsLocked(ip) {
		t.Error("lock should expire after window")
	}
	s.RecordFailure(ip)
	s.ClearFailures(ip)
	if s.IsLocked(ip) {
		t.Error("clear should reset failures")
	}
}

// TestStore_TTL 返回构造时的 TTL。
func TestStore_TTL(t *testing.T) {
	s, _ := newTestStore(30 * time.Minute)
	if s.TTL() != 30*time.Minute {
		t.Errorf("TTL() = %v", s.TTL())
	}
}

// TestStore_IsLockedPrunesEmptyKey 失败记录滑出窗口后，IsLocked 不应把空 key
// 残留在 failures map 中（否则伪造 IP 可撑爆内存）。
func TestStore_IsLockedPrunesEmptyKey(t *testing.T) {
	s, now := newTestStore(time.Hour)
	ip := "9.9.9.9"
	s.RecordFailure(ip)
	*now = now.Add(11 * time.Minute) // 记录滑出窗口
	if s.IsLocked(ip) {
		t.Error("stale failure should not lock")
	}
	if _, ok := s.failures[ip]; ok {
		t.Error("IsLocked should delete key when no recent failures remain")
	}
	// 从未失败过的 IP 查询也不应新增 key。
	s.IsLocked("never-failed")
	if _, ok := s.failures["never-failed"]; ok {
		t.Error("IsLocked should not create a key for an unseen ip")
	}
}

// TestStore_CreateSweepsExpired Create 时惰性清除已过期会话，防止内存泄漏。
func TestStore_CreateSweepsExpired(t *testing.T) {
	s, now := newTestStore(time.Hour)
	old := s.Create("u1")
	*now = now.Add(2 * time.Hour) // old 已过期
	s.Create("u1")                // 触发清扫
	if _, ok := s.sessions[old]; ok {
		t.Error("expired session should be swept on Create")
	}
	if len(s.sessions) != 1 {
		t.Errorf("only the fresh session should remain, got %d", len(s.sessions))
	}
}

// TestStore_GlobalBackstop 攻击者用大量不同来源键规避单来源限制时，
// 全局兜底计数超过阈值后一律锁定；窗口滑过后解锁。
func TestStore_GlobalBackstop(t *testing.T) {
	s, now := newTestStore(time.Hour)
	// 每个来源仅失败 1 次（远低于单来源上限 5），但合计触发全局兜底。
	for i := 0; i < globalMax; i++ {
		s.RecordFailure(fmt.Sprintf("src-%d", i))
	}
	if !s.IsLocked("brand-new-source") {
		t.Error("global backstop should lock even an unseen source")
	}
	*now = now.Add(11 * time.Minute) // 全局记录滑出窗口
	if s.IsLocked("brand-new-source") {
		t.Error("global backstop should release after window")
	}
}
