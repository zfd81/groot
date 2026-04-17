package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/config"
)

// RateLimitMiddleware provides rate limiting
type RateLimitMiddleware struct {
	config        config.RateLimitConfig
	requestCounts map[string]*RequestCounter
	mu            sync.Mutex
	executor      interface{} // Will be TaskExecutor, using interface to avoid import cycle
}

// RequestCounter tracks request counts
type RequestCounter struct {
	MinuteCount int
	HourCount   int
	MinuteStart time.Time
	HourStart   time.Time
}

// NewRateLimitMiddleware creates a new rate limit middleware
func NewRateLimitMiddleware(cfg config.RateLimitConfig) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		config:        cfg,
		requestCounts: make(map[string]*RequestCounter),
	}
}

// SetExecutor sets the executor reference
func (m *RateLimitMiddleware) SetExecutor(exec interface{}) {
	m.executor = exec
}

// Serve implements middleware handler
func (m *RateLimitMiddleware) Serve(ctx context.Context, rc *app.RequestContext, next app.HandlerFunc) {
	// Check concurrent tasks (if executor is set)
	if m.executor != nil {
		// Type assertion would happen here in production
		// For MVP: skip concurrent check
	}

	// Check rate limits per caller
	caller := GetCaller(rc)
	m.mu.Lock()
	counter, ok := m.requestCounts[caller]
	if !ok {
		counter = &RequestCounter{
			MinuteStart: time.Now(),
			HourStart:   time.Now(),
		}
		m.requestCounts[caller] = counter
	}
	m.mu.Unlock()

	// Reset counters if time windows expired
	now := time.Now()
	if now.Sub(counter.MinuteStart) >= time.Minute {
		counter.MinuteCount = 0
		counter.MinuteStart = now
	}
	if now.Sub(counter.HourStart) >= time.Hour {
		counter.HourCount = 0
		counter.HourStart = now
	}

	// Check minute limit
	if counter.MinuteCount >= m.config.MaxRequestsPerMinute {
		rc.SetContentType("application/json")
		rc.SetStatusCode(429)
		rc.Write([]byte(`{"status":"rate_limited","message":"请求频率超限，请稍后重试"}`))
		return
	}

	// Check hour limit
	if counter.HourCount >= m.config.MaxRequestsPerHour {
		rc.SetContentType("application/json")
		rc.SetStatusCode(429)
		rc.Write([]byte(`{"status":"rate_limited","message":"请求频率超限，请稍后重试"}`))
		return
	}

	// Increment counters
	m.mu.Lock()
	counter.MinuteCount++
	counter.HourCount++
	m.mu.Unlock()

	next(ctx, rc)
}
