package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/zfd81/groot/internal/config"
)

// keyLimiter tracks rate limits for a single key (API key or client IP)
type keyLimiter struct {
	qps      *rate.Limiter
	sem      chan struct{}
	lastUsed time.Time
}

// RateLimiter manages per-key and global rate limits
type RateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*keyLimiter
	global   *keyLimiter
	cfg      config.RateLimitConfig
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// New creates a new RateLimiter from config
func New(cfg config.RateLimitConfig) (*RateLimiter, error) {
	cleanupIntervalStr := cfg.CleanupInterval
	if cleanupIntervalStr == "" {
		cleanupIntervalStr = "5m"
	}
	cleanupInterval, err := time.ParseDuration(cleanupIntervalStr)
	if err != nil {
		cleanupInterval = 5 * time.Minute
	}

	rl := &RateLimiter{
		limiters: make(map[string]*keyLimiter),
		cfg:      cfg,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	if cfg.GlobalQPS > 0 || cfg.GlobalConcurrency > 0 {
		rl.global = newKeyLimiter(cfg.GlobalQPS, cfg.GlobalConcurrency)
	}

	go rl.cleanupLoop(cleanupInterval)

	return rl, nil
}

// Allow checks if a request is allowed for the given key (QPS only).
// Returns true if allowed, false if rate limited.
func (rl *RateLimiter) Allow(key string) bool {
	if !rl.cfg.Enabled {
		return true
	}

	// Global QPS check
	if rl.global != nil && rl.global.qps != nil && !rl.global.qps.Allow() {
		return false
	}

	// Per-key QPS check
	kl := rl.getOrCreateLimiter(key)
	return kl.qps == nil || kl.qps.Allow()
}

// Acquire checks QPS and acquires a concurrency slot for the given key.
// Returns true if both checks pass, false if rate limited.
// Caller must call Release when done.
func (rl *RateLimiter) Acquire(key string) bool {
	if !rl.cfg.Enabled {
		return true
	}

	// Global QPS check
	if rl.global != nil && rl.global.qps != nil && !rl.global.qps.Allow() {
		return false
	}

	// Global concurrency check
	if rl.global != nil && rl.global.sem != nil {
		select {
		case rl.global.sem <- struct{}{}:
		default:
			return false
		}
	}

	// Per-key QPS + concurrency check
	kl := rl.getOrCreateLimiter(key)
	if kl.qps != nil && !kl.qps.Allow() {
		// Release global sem if we acquired it
		if rl.global != nil && rl.global.sem != nil {
			<-rl.global.sem
		}
		return false
	}
	if kl.sem != nil {
		select {
		case kl.sem <- struct{}{}:
		default:
			// Release global sem if we acquired it
			if rl.global != nil && rl.global.sem != nil {
				<-rl.global.sem
			}
			return false
		}
	}

	kl.lastUsed = time.Now()
	return true
}

// Release releases a concurrency slot for the given key.
// Must be called after Acquire returns true.
func (rl *RateLimiter) Release(key string) {
	if !rl.cfg.Enabled {
		return
	}

	// Release per-key sem
	if kl := rl.getLimiter(key); kl != nil && kl.sem != nil {
		select {
		case <-kl.sem:
		default:
		}
	}

	// Release global sem
	if rl.global != nil && rl.global.sem != nil {
		select {
		case <-rl.global.sem:
		default:
		}
	}
}

// Stop stops the background cleanup goroutine
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
	<-rl.doneCh
}

// getOrCreateLimiter returns an existing limiter for the key or creates a new one
func (rl *RateLimiter) getOrCreateLimiter(key string) *keyLimiter {
	rl.mu.RLock()
	kl, ok := rl.limiters[key]
	rl.mu.RUnlock()
	if ok {
		kl.lastUsed = time.Now()
		return kl
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	kl, ok = rl.limiters[key]
	if ok {
		kl.lastUsed = time.Now()
		return kl
	}

	kl = newKeyLimiter(rl.cfg.DefaultQPS, rl.cfg.DefaultConcurrency)
	rl.limiters[key] = kl
	return kl
}

// getLimiter returns an existing limiter without creating one
func (rl *RateLimiter) getLimiter(key string) *keyLimiter {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.limiters[key]
}

// cleanupLoop periodically removes idle limiters to prevent memory leaks
func (rl *RateLimiter) cleanupLoop(interval time.Duration) {
	defer close(rl.doneCh)
	for {
		select {
		case <-rl.stopCh:
			return
		case <-time.After(interval):
			rl.cleanup(interval * 2)
		}
	}
}

// cleanup removes limiters that haven't been used for longer than maxAge
func (rl *RateLimiter) cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for key, kl := range rl.limiters {
		if now.Sub(kl.lastUsed) > maxAge {
			delete(rl.limiters, key)
		}
	}
}

// newKeyLimiter creates a keyLimiter from QPS and concurrency values
func newKeyLimiter(qps float64, concurrency int) *keyLimiter {
	kl := &keyLimiter{lastUsed: time.Now()}

	if qps > 0 {
		burst := int(qps)
		if burst < 1 {
			burst = 1
		}
		kl.qps = rate.NewLimiter(rate.Limit(qps), burst)
	}

	if concurrency > 0 {
		kl.sem = make(chan struct{}, concurrency)
	}

	return kl
}
