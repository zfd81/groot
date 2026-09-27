package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/zfd81/groot/internal/config"
)

// keyLimiter tracks rate limits for a single key (API key or client IP)
type keyLimiter struct {
	qps *rate.Limiter
	sem chan struct{}

	// mu 只保护 lastUsed。qps 与 sem 自身并发安全，建桶后不再改写。
	mu       sync.Mutex
	lastUsed time.Time
}

// touch 记录本次使用时间，供空闲回收判定。
func (kl *keyLimiter) touch() {
	kl.mu.Lock()
	kl.lastUsed = time.Now()
	kl.mu.Unlock()
}

// idleFor 返回距上次使用经过的时长。
func (kl *keyLimiter) idleFor(now time.Time) time.Duration {
	kl.mu.Lock()
	defer kl.mu.Unlock()
	return now.Sub(kl.lastUsed)
}

// release 非阻塞地归还一个并发名额。通道为空时直接返回，
// 不会因为桶被 Reconfigure 换过、持有者多于令牌而挂起调用方。
func (kl *keyLimiter) release() {
	if kl.sem == nil {
		return
	}
	select {
	case <-kl.sem:
	default:
	}
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
	cfg, global := rl.snapshot()
	if !cfg.Enabled {
		return true
	}

	// Global QPS check
	if global != nil && global.qps != nil && !global.qps.Allow() {
		return false
	}

	// Per-key QPS check
	kl := rl.getOrCreateLimiter(key, cfg)
	return kl.qps == nil || kl.qps.Allow()
}

// Acquire checks QPS and acquires a concurrency slot for the given key.
// Returns true if both checks pass, false if rate limited.
// Caller must call Release when done.
func (rl *RateLimiter) Acquire(key string) bool {
	cfg, global := rl.snapshot()
	if !cfg.Enabled {
		return true
	}

	// Global QPS check
	if global != nil && global.qps != nil && !global.qps.Allow() {
		return false
	}

	// Global concurrency check
	if global != nil && global.sem != nil {
		select {
		case global.sem <- struct{}{}:
		default:
			return false
		}
	}

	// Per-key QPS + concurrency check
	kl := rl.getOrCreateLimiter(key, cfg)
	if kl.qps != nil && !kl.qps.Allow() {
		// Release global sem if we acquired it
		if global != nil {
			global.release()
		}
		return false
	}
	if kl.sem != nil {
		select {
		case kl.sem <- struct{}{}:
		default:
			// Release global sem if we acquired it
			if global != nil {
				global.release()
			}
			return false
		}
	}

	kl.touch()
	return true
}

// Release releases a concurrency slot for the given key.
// Must be called after Acquire returns true.
//
// 不看 Enabled 开关：一个在开启期间取到名额的请求，可能在关停之后才归还，
// 此时若早退，per-key 桶的槽位就永远还不回去（per-key 桶在重建时保留）。
// 关停期间 Acquire 不占槽位，对空通道做非阻塞归还是 no-op，无副作用。
//
// 归还时重新取快照而非沿用获取时的：全局桶重建后旧实例被丢弃，
// 不再有人读它的计数，少还一次不影响新桶的容量。偏差说明见 Reconfigure。
func (rl *RateLimiter) Release(key string) {
	_, global := rl.snapshot()

	// Release per-key sem
	if kl := rl.getLimiter(key); kl != nil {
		kl.release()
	}

	// Release global sem
	if global != nil {
		global.release()
	}
}

// Reconfigure 用新配置替换限流参数。
//
// 已建桶的 key 保留旧容量，随后首次出现的 key 按新配置建桶。正在进行的
// 请求不被打断——中途抽走名额会让一个已放行的请求在归还时对不上账。
// 旧桶在空闲超过回收窗口后由 cleanup 清掉，此后该 key 再来即按新配置建桶。
//
// per-key 桶不清空：Release 是按 key 查当前桶来归还的，清空后一个
// 重建前取到名额的请求会把名额还进新桶，凭空放宽新上限。
//
// 全局桶只在全局参数变化时才重建，未变则保留桶及其令牌状态——
// 界面整包提交只改 per-key 参数时，不应丢掉全局 QPS 桶的计数。
// 重建瞬间正在占用全局名额的请求，其归还会落到新桶上，新桶出现
// 「持有者多于令牌」的欠账。欠账在新桶的通道下一次排空前一直存在，
// 排空时多余的 no-op 归还把偏差吸收掉；归还路径均为非阻塞，欠账不会
// 挂住调用方，且全局桶的容量通常远大于瞬时并发，这点偏差不影响限流意图。
func (rl *RateLimiter) Reconfigure(cfg config.RateLimitConfig) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if cfg.GlobalQPS != rl.cfg.GlobalQPS || cfg.GlobalConcurrency != rl.cfg.GlobalConcurrency {
		rl.global = nil
		if cfg.GlobalQPS > 0 || cfg.GlobalConcurrency > 0 {
			rl.global = newKeyLimiter(cfg.GlobalQPS, cfg.GlobalConcurrency)
		}
	}
	rl.cfg = cfg
}

// Config 返回当前生效的限流配置，供接口回读与启动日志使用。
func (rl *RateLimiter) Config() config.RateLimitConfig {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.cfg
}

// snapshot 一次取出配置与全局桶，避免调用方在一次判定内多次加锁，
// 也避免中途被 Reconfigure 换掉造成前后不一致。
func (rl *RateLimiter) snapshot() (config.RateLimitConfig, *keyLimiter) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.cfg, rl.global
}

// Stop stops the background cleanup goroutine
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
	<-rl.doneCh
}

// getOrCreateLimiter returns an existing limiter for the key or creates a new one
func (rl *RateLimiter) getOrCreateLimiter(key string, cfg config.RateLimitConfig) *keyLimiter {
	rl.mu.RLock()
	kl, ok := rl.limiters[key]
	rl.mu.RUnlock()
	if ok {
		kl.touch()
		return kl
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	kl, ok = rl.limiters[key]
	if ok {
		kl.touch()
		return kl
	}

	kl = newKeyLimiter(cfg.DefaultQPS, cfg.DefaultConcurrency)
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
		if kl.idleFor(now) > maxAge {
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
