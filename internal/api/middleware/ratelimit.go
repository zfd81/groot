package middleware

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/ratelimit"
)

// RateLimitMiddleware provides rate limiting for API endpoints
type RateLimitMiddleware struct {
	limiter *ratelimit.RateLimiter
}

// NewRateLimitMiddleware creates a new rate limit middleware
func NewRateLimitMiddleware(limiter *ratelimit.RateLimiter) *RateLimitMiddleware {
	return &RateLimitMiddleware{limiter: limiter}
}

// Serve returns a Hertz middleware handler that:
// - Tracks concurrency (acquire/release) for long-lived endpoints (POST /chat)
// - Only checks QPS for short-lived endpoints (GET, DELETE etc.)
func (m *RateLimitMiddleware) Serve() app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		key := m.resolveKey(rc)
		path := string(rc.URI().Path())
		method := string(rc.Method())

		// POST /chat is a long-lived SSE connection — needs concurrency tracking
		if path == "/chat" && method == "POST" {
			if !m.limiter.Acquire(key) {
				writeRateLimitResponse(rc)
				rc.Abort()
				return
			}
			rc.Next(ctx)
			m.limiter.Release(key)
		} else {
			if !m.limiter.Allow(key) {
				writeRateLimitResponse(rc)
				rc.Abort()
				return
			}
			rc.Next(ctx)
		}
	}
}

// writeRateLimitResponse writes a 429 Too Many Requests response
func writeRateLimitResponse(rc *app.RequestContext) {
	rc.SetContentType("application/json; charset=utf-8")
	rc.SetStatusCode(429)
	rc.Write([]byte(fmt.Sprintf(
		`{"status":"rate_limited","message":"请求过于频繁，请稍后重试"}`)))
}

// resolveKey determines the rate limiting key from the request context
func (m *RateLimitMiddleware) resolveKey(rc *app.RequestContext) string {
	caller := GetCaller(rc)

	// For authenticated requests, use caller name
	if caller != "" && caller != "anonymous" {
		return "key:" + caller
	}

	// For anonymous requests, use client IP
	ip := rc.ClientIP()
	if ip == "" {
		return "anonymous"
	}
	// Strip port if present (IPv4: "ip:port", IPv6: "[::1]:port")
	if idx := strings.LastIndex(ip, ":"); idx > 0 {
		ip = ip[:idx]
	}
	// Clean IPv6 brackets
	ip = strings.Trim(ip, "[]")
	return "ip:" + ip
}
