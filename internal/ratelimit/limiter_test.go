package ratelimit

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
)

// newTestConfig returns a rate limit config suitable for testing
func newTestConfig() config.RateLimitConfig {
	return config.RateLimitConfig{
		Enabled:            true,
		GlobalQPS:          0, // disabled
		GlobalConcurrency:  0, // disabled
		DefaultQPS:         100,
		DefaultConcurrency: 10,
		CleanupInterval:    "1m",
	}
}

func TestNewRateLimiter_InvalidCleanupInterval(t *testing.T) {
	cfg := newTestConfig()
	cfg.CleanupInterval = "invalid"

	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// Should still be functional with default cleanup interval
	if !rl.Allow("test-key") {
		t.Error("expected Allow to work after fallback default")
	}
}

func TestNewRateLimiter_Disabled(t *testing.T) {
	cfg := config.RateLimitConfig{Enabled: false}
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	if !rl.Allow("test-key") {
		t.Error("expected Allow to return true when disabled")
	}
	if !rl.Acquire("test-key") {
		t.Error("expected Acquire to return true when disabled")
	}
	rl.Release("test-key") // should not panic
}

func TestAllow_QPSLimit(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultQPS = 5
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	key := "test-user"

	// First 5 calls should succeed (burst = QPS)
	for i := 0; i < 5; i++ {
		if !rl.Allow(key) {
			t.Fatalf("call %d should be allowed (within burst)", i+1)
		}
	}

	// The 6th call should fail (no burst left, no time to refill)
	if rl.Allow(key) {
		t.Error("expected call to be rate limited (exceeded QPS)")
	}
}

func TestAllow_ZeroQPS(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultQPS = 0
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// With QPS=0, no rate limiting on QPS
	for i := 0; i < 100; i++ {
		if !rl.Allow("test-user") {
			t.Fatal("expected all calls to be allowed when QPS is 0")
		}
	}
}

func TestAcquire_ConcurrencyLimit(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 3
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	key := "test-user"

	// Acquire 3 slots — should all succeed
	for i := 0; i < 3; i++ {
		if !rl.Acquire(key) {
			t.Fatalf("acquire %d should succeed (within concurrency limit)", i+1)
		}
	}

	// 4th acquire should fail (concurrency limit reached)
	if rl.Acquire(key) {
		t.Error("expected acquire to be rejected (concurrency limit reached)")
	}

	// Release one slot
	rl.Release(key)

	// Now acquire should succeed again
	if !rl.Acquire(key) {
		t.Error("expected acquire to succeed after release")
	}

	// Release remaining slots to avoid goroutine leak (3 acquired - 1 released + 1 re-acquired = 3 acquired)
	rl.Release(key)
	rl.Release(key)
}

func TestAcquire_ZeroConcurrency(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 0
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// With concurrency=0, no concurrency limiting
	for i := 0; i < 100; i++ {
		if !rl.Acquire("test-user") {
			t.Fatal("expected all acquires to succeed when concurrency is 0")
		}
	}
}

func TestRelease_WithoutAcquire_NoPanic(t *testing.T) {
	cfg := newTestConfig()
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// Release should not panic even if called without Acquire
	rl.Release("non-existent-key")
	rl.Release("another-key")
}

func TestKeyIsolation(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 2
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// Key A consumes both slots
	if !rl.Acquire("key-a") {
		t.Fatal("key-a acquire 1 should succeed")
	}
	if !rl.Acquire("key-a") {
		t.Fatal("key-a acquire 2 should succeed")
	}

	// Key B should still be able to acquire (isolated limiters)
	if !rl.Acquire("key-b") {
		t.Fatal("key-b acquire should succeed (keys are isolated)")
	}

	// Key A is now at limit
	if rl.Acquire("key-a") {
		t.Fatal("key-a acquire 3 should be rejected")
	}

	// Cleanup
	rl.Release("key-a")
	rl.Release("key-a")
	rl.Release("key-b")
}

func TestGlobalQPSLimit(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultQPS = 100 // per-key high
	cfg.GlobalQPS = 3    // global low
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// First 3 calls from different keys should succeed
	for i := 0; i < 3; i++ {
		key := "user-" + string(rune('A'+i))
		if !rl.Allow(key) {
			t.Fatalf("global allow %d should succeed (within global QPS)", i+1)
		}
	}

	// 4th call should fail (global QPS exceeded)
	if rl.Allow("another-user") {
		t.Error("expected global QPS limit to be exceeded")
	}
}

func TestGlobalConcurrencyLimit(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 100 // per-key high
	cfg.GlobalConcurrency = 2    // global low
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// Two different keys acquire global slots
	if !rl.Acquire("key-a") {
		t.Fatal("key-a acquire should succeed")
	}
	if !rl.Acquire("key-b") {
		t.Fatal("key-b acquire should succeed")
	}

	// Third key should fail (global concurrency limit)
	if rl.Acquire("key-c") {
		t.Fatal("key-c acquire should be rejected (global concurrency limit)")
	}

	// Release one global slot
	rl.Release("key-a")

	// Now key-c should succeed
	if !rl.Acquire("key-c") {
		t.Fatal("key-c acquire should succeed after release")
	}

	// Cleanup
	rl.Release("key-b")
	rl.Release("key-c")
}

func TestConcurrentAccess(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 5
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	key := "shared-key"
	var wg sync.WaitGroup

	// 20 goroutines competing for 5 slots
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rl.Acquire(key) {
				time.Sleep(10 * time.Millisecond)
				rl.Release(key)
			}
		}()
	}

	wg.Wait()
	// If no panic or deadlock, the test passes
}

func TestCleanup_IdleLimiters(t *testing.T) {
	cfg := newTestConfig()
	cfg.CleanupInterval = "100ms"
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// Create some limiters
	rl.Acquire("temp-user")
	rl.Release("temp-user")

	// Verify limiter exists
	rl.mu.RLock()
	_, exists := rl.limiters["temp-user"]
	rl.mu.RUnlock()
	if !exists {
		t.Fatal("expected temp-user limiter to exist before cleanup")
	}

	// Wait for cleanup (cleanup interval = 100ms, cleanup removes > 2*interval = 200ms idle)
	// Since lastUsed was just updated, we need to wait for it to become idle
	time.Sleep(300 * time.Millisecond)

	// Trigger cleanup
	rl.cleanup(100 * time.Millisecond)

	// Verify limiter was removed
	rl.mu.RLock()
	_, exists = rl.limiters["temp-user"]
	rl.mu.RUnlock()
	if exists {
		t.Error("expected temp-user limiter to be cleaned up")
	}
}

func TestStop_NoLeak(t *testing.T) {
	cfg := newTestConfig()
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		rl.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not complete within timeout")
	}
}

func TestMultipleKeys_TrackLastUsed(t *testing.T) {
	cfg := newTestConfig()
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	rl.Acquire("key-a")
	rl.Release("key-a")

	time.Sleep(10 * time.Millisecond)

	rl.Acquire("key-b")
	rl.Release("key-b")

	rl.mu.RLock()
	klA, existsA := rl.limiters["key-a"]
	klB, existsB := rl.limiters["key-b"]
	rl.mu.RUnlock()

	if !existsA || !existsB {
		t.Fatal("both limiters should exist")
	}

	if !klA.lastUsed.Before(klB.lastUsed) {
		t.Error("expected key-a lastUsed to be before key-b lastUsed")
	}
}

// TestReconfigure_AppliesToNewKeys 验证收紧并发上限对随后首次出现的 key 生效。
func TestReconfigure_AppliesToNewKeys(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 3
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	next := cfg
	next.DefaultConcurrency = 1
	rl.Reconfigure(next)

	if !rl.Acquire("fresh-key") {
		t.Fatal("first acquire on a fresh key should succeed")
	}
	if rl.Acquire("fresh-key") {
		t.Error("second acquire should be blocked by the new limit of 1")
	}
	rl.Release("fresh-key")
}

// TestReconfigure_KeepsExistingBuckets 验证已建桶的 key 保留旧容量。
// 这是「已发起的调用按旧配置走完」的直接体现：取名额与归还名额
// 落在同一个桶上，收紧上限不会让一个已放行的请求在归还时对不上账。
func TestReconfigure_KeepsExistingBuckets(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 2
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	if !rl.Acquire("old-key") {
		t.Fatal("acquire before reconfigure should succeed")
	}

	next := cfg
	next.DefaultConcurrency = 1
	rl.Reconfigure(next)

	// old-key 的桶容量仍是 2，第二个名额应能取到
	if !rl.Acquire("old-key") {
		t.Error("existing key should keep its old capacity of 2")
	}
	// 同时新 key 已按新上限建桶，取第二个应被挡住
	if !rl.Acquire("new-key") {
		t.Fatal("first acquire on new-key should succeed")
	}
	if rl.Acquire("new-key") {
		t.Error("new-key should be capped at the new limit of 1")
	}

	rl.Release("old-key")
	rl.Release("old-key")
	rl.Release("new-key")
}

// TestReconfigure_TogglesEnabled 验证开关可在线关停与恢复。
func TestReconfigure_TogglesEnabled(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultQPS = 1
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// 先把 key 的令牌耗干
	rl.Allow("drained")
	if rl.Allow("drained") {
		t.Fatal("second call should be rate limited")
	}

	off := cfg
	off.Enabled = false
	rl.Reconfigure(off)
	if !rl.Allow("drained") {
		t.Error("disabled limiter must allow everything")
	}

	// 恢复开关后限流重新起作用：换一个 key 观察，
	// drained 的桶尚未补满令牌，用它判定会依赖计时
	rl.Reconfigure(cfg)
	if !rl.Allow("fresh") {
		t.Fatal("first call on a fresh key should pass")
	}
	if rl.Allow("fresh") {
		t.Error("re-enabled limiter should rate limit the second call")
	}
}

// TestReconfigure_RebuildsGlobalLimiter 验证全局桶按新配置重建。
func TestReconfigure_RebuildsGlobalLimiter(t *testing.T) {
	cfg := newTestConfig()
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	next := cfg
	next.GlobalConcurrency = 1
	rl.Reconfigure(next)

	if !rl.Acquire("a") {
		t.Fatal("first acquire should take the only global slot")
	}
	if rl.Acquire("b") {
		t.Error("second acquire should be blocked by global concurrency 1")
	}
	rl.Release("a")

	// (a) 全局参数归零后全局桶应被移除
	none := cfg
	none.GlobalQPS = 0
	none.GlobalConcurrency = 0
	rl.Reconfigure(none)
	rl.mu.RLock()
	global := rl.global
	rl.mu.RUnlock()
	if global != nil {
		t.Error("global limiter should be nil when global params are zero")
	}

	// (b) 全局参数未变时不重建全局桶，保留其令牌状态
	rl.Reconfigure(next)
	rl.mu.RLock()
	first := rl.global
	rl.mu.RUnlock()
	if first == nil {
		t.Fatal("global limiter should exist after reconfigure with GlobalConcurrency=1")
	}
	same := next
	same.DefaultQPS = next.DefaultQPS + 1 // 只改 per-key 参数
	rl.Reconfigure(same)
	rl.mu.RLock()
	second := rl.global
	rl.mu.RUnlock()
	if first != second {
		t.Error("global limiter should be kept when global params are unchanged")
	}
}

// TestReconfigure_DisableMidFlightDoesNotLeak 验证在持有名额期间关停限流，
// 归还仍能落回 per-key 桶，重开后并发上限不会永久减一。
func TestReconfigure_DisableMidFlightDoesNotLeak(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 2
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	if !rl.Acquire("k") {
		t.Fatal("acquire before disable should succeed")
	}

	off := cfg
	off.Enabled = false
	rl.Reconfigure(off)
	rl.Release("k")
	rl.Reconfigure(cfg)

	if !rl.Acquire("k") {
		t.Fatal("first acquire after re-enable should succeed")
	}
	if !rl.Acquire("k") {
		t.Error("second acquire after re-enable should succeed: slot released during disable must not leak")
	}
	if rl.Acquire("k") {
		t.Error("third acquire should be rejected by concurrency limit 2")
	}

	rl.Release("k")
	rl.Release("k")
}

// TestReconfigure_ConcurrentWithTraffic 在 -race 下验证重建与请求并发无竞争。
func TestReconfigure_ConcurrentWithTraffic(t *testing.T) {
	cfg := newTestConfig()
	cfg.GlobalConcurrency = 4
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				key := "k" + strconv.Itoa(n)
				if rl.Acquire(key) {
					rl.Release(key)
				}
				rl.Allow(key)
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			next := cfg
			next.DefaultConcurrency = n + 1
			for j := 0; j < 20; j++ {
				// 第 0 个协程交替切换全局并发，让全局桶换桶路径在 -race 下跑到
				if n == 0 {
					next.GlobalConcurrency = 4 + j%2
				}
				rl.Reconfigure(next)
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			rl.cleanup(time.Hour)
		}
	}()
	wg.Wait()
}
