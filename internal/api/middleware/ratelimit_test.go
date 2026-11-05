package middleware

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/ratelimit"
)

// newTestRateLimitMiddleware 用给定配置构造限流中间件，供同一测试内的多次请求共享限流状态。
func newTestRateLimitMiddleware(t *testing.T, cfg config.RateLimitConfig) *RateLimitMiddleware {
	t.Helper()
	limiter, err := ratelimit.New(cfg)
	if err != nil {
		t.Fatalf("ratelimit.New: %v", err)
	}
	t.Cleanup(limiter.Stop)
	return NewRateLimitMiddleware(limiter)
}

// serveRateLimit 用指定中间件执行一次请求。
func serveRateLimit(m *RateLimitMiddleware, method, uri string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	rc.Request.SetRequestURI(uri)
	m.Serve()(context.Background(), rc)
	return rc
}

// TestRateLimitMiddleware_ChatCompletionsUsesConcurrencyBranch POST /chat/completions 走并发分支：
// 并发上限为 1 时，第一个请求在长连接 handler 中阻塞并持有名额，第二个请求立即 429。
func TestRateLimitMiddleware_ChatCompletionsUsesConcurrencyBranch(t *testing.T) {
	m := newTestRateLimitMiddleware(t, config.RateLimitConfig{Enabled: true, DefaultConcurrency: 1})

	// 第一个请求挂在一个阻塞的下游 handler 上，模拟长连接仍占用并发名额
	started := make(chan struct{})
	unblock := make(chan struct{})
	first := app.NewContext(0)
	first.Request.Header.SetMethod(consts.MethodPost)
	first.Request.SetRequestURI("/chat/completions")
	first.SetHandlers(app.HandlersChain{func(context.Context, *app.RequestContext) {
		close(started)
		<-unblock
	}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Serve()(context.Background(), first)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not reach downstream handler")
	}
	if got := first.Response.StatusCode(); got == 429 {
		t.Fatalf("first request should pass, got %d", got)
	}

	second := serveRateLimit(m, consts.MethodPost, "/chat/completions")
	if got := second.Response.StatusCode(); got != 429 {
		t.Fatalf("second concurrent request should 429, got %d", got)
	}

	close(unblock)
	<-done
}

// TestRateLimitMiddleware_Envelope 429 响应体为 OpenAI 错误信封。
func TestRateLimitMiddleware_Envelope(t *testing.T) {
	// QPS 极低且 burst 为 1：第一个请求消耗令牌，第二个立即被限流
	m := newTestRateLimitMiddleware(t, config.RateLimitConfig{Enabled: true, DefaultQPS: 0.001})

	serveRateLimit(m, consts.MethodPost, "/chat/completions")
	rc := serveRateLimit(m, consts.MethodPost, "/chat/completions")

	if got := rc.Response.StatusCode(); got != 429 {
		t.Fatalf("expected 429, got %d", got)
	}
	if ct := string(rc.Response.Header.ContentType()); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	body := string(rc.Response.Body())
	for _, want := range []string{
		`"type":"rate_limit_error"`,
		`"code":"rate_limit_exceeded"`,
		`"message":"请求过于频繁，请稍后重试"`,
		`"param":null`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}
