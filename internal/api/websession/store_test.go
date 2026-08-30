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

// TestStore_CreateValidate 令牌创建后可校验，且两次创建的令牌不同。
func TestStore_CreateValidate(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	tok := s.Create()
	if tok == "" || len(tok) != 64 {
		t.Fatalf("token should be 64 hex chars, got %q", tok)
	}
	if !s.Validate(tok) {
		t.Error("fresh token should validate")
	}
	if s.Validate("nonexistent") {
		t.Error("unknown token should not validate")
	}
	if s.Create() == tok {
		t.Error("tokens should be unique")
	}
}

// TestStore_Expiry 过期令牌校验失败。
func TestStore_Expiry(t *testing.T) {
	s, now := newTestStore(time.Hour)
	tok := s.Create()
	*now = now.Add(2 * time.Hour)
	if s.Validate(tok) {
		t.Error("expired token should not validate")
	}
}

// TestStore_Delete 删除后令牌失效。
func TestStore_Delete(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	tok := s.Create()
	s.Delete(tok)
	if s.Validate(tok) {
		t.Error("deleted token should not validate")
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
	old := s.Create()
	*now = now.Add(2 * time.Hour) // old 已过期
	s.Create()                    // 触发清扫
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
