package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/network/standard"

	"github.com/zfd81/groot/internal/api/errbody"
)

// assertBodyLimitCode 断言响应是给定错误码的 OpenAI 错误信封。
func assertBodyLimitCode(t *testing.T, w *ut.ResponseRecorder, code string) {
	t.Helper()
	var env errbody.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析错误信封失败: %v; body=%s", err, w.Body.String())
	}
	if env.Error.Code == nil || *env.Error.Code != code {
		t.Fatalf("错误码 = %v, want %s; body=%s", env.Error.Code, code, w.Body.String())
	}
}

// TestBodyLimit_KnownLengthOverLimit 已知长度超限时在读取前返回 413，业务 handler 不执行。
func TestBodyLimit_KnownLengthOverLimit(t *testing.T) {
	called := false
	h := server.Default(server.WithStreamBody(true))
	h.Use(BodyLimit(64))
	h.POST("/upload", func(ctx context.Context, rc *app.RequestContext) {
		called = true
		rc.String(http.StatusOK, "ok")
	})

	w := ut.PerformRequest(h.Engine, http.MethodPost, "/upload",
		&ut.Body{Body: strings.NewReader(strings.Repeat("x", 100)), Len: 100})

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d, want 413; body=%s", w.Code, w.Body.String())
	}
	assertBodyLimitCode(t, w, errbody.CodeFileTooLarge)
	if called {
		t.Error("超限请求不应进入业务 handler")
	}
}

// TestBodyLimit_UnderLimitPasses 未超限的流式请求正常进入 handler，请求体完整可读。
func TestBodyLimit_UnderLimitPasses(t *testing.T) {
	called := false
	h := server.Default(server.WithStreamBody(true))
	h.Use(BodyLimit(1024))
	h.POST("/upload", func(ctx context.Context, rc *app.RequestContext) {
		called = true
		body, err := io.ReadAll(rc.Request.BodyStream())
		if err != nil {
			t.Errorf("读取请求体失败: %v", err)
		}
		rc.String(http.StatusOK, string(body))
	})

	const payload = "hello-body"
	w := ut.PerformRequest(h.Engine, http.MethodPost, "/upload",
		&ut.Body{Body: strings.NewReader(payload), Len: len(payload)})

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("未超限请求应进入业务 handler")
	}
	if got := w.Body.String(); got != payload {
		t.Errorf("响应体 = %q, want %q", got, payload)
	}
}

// TestBodyLimit_ExactLimitPasses 恰好等于上限的请求体可以通过。
func TestBodyLimit_ExactLimitPasses(t *testing.T) {
	h := server.Default(server.WithStreamBody(true))
	h.Use(BodyLimit(64))
	h.POST("/upload", func(ctx context.Context, rc *app.RequestContext) {
		if _, err := io.ReadAll(rc.Request.BodyStream()); err != nil {
			t.Errorf("读取请求体失败: %v", err)
		}
		rc.String(http.StatusOK, "ok")
	})

	w := ut.PerformRequest(h.Engine, http.MethodPost, "/upload",
		&ut.Body{Body: strings.NewReader(strings.Repeat("x", 64)), Len: 64})
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

// TestBodyLimit_StreamedOverLimit 未知长度（分块）请求体在读取过程中以 ErrBodyTooLarge 中止。
func TestBodyLimit_StreamedOverLimit(t *testing.T) {
	var readErr error
	done := make(chan struct{})
	h := server.Default(server.WithStreamBody(true))
	h.Use(BodyLimit(64))
	h.POST("/upload", func(ctx context.Context, rc *app.RequestContext) {
		_, readErr = io.ReadAll(rc.Request.BodyStream())
		close(done)
		rc.String(http.StatusOK, "ok")
	})

	ut.PerformRequest(h.Engine, http.MethodPost, "/upload",
		&ut.Body{Body: strings.NewReader(strings.Repeat("x", 4096)), Len: -1})
	<-done
	if !errors.Is(readErr, errbody.ErrBodyTooLarge) {
		t.Fatalf("读取错误 = %v, want errbody.ErrBodyTooLarge", readErr)
	}
}

// TestBodyLimit_BufferedBodyOverLimit 未启用流式请求体时，已缓冲的超限请求体同样返回 413。
func TestBodyLimit_BufferedBodyOverLimit(t *testing.T) {
	called := false
	h := server.New()
	h.Use(BodyLimit(64))
	h.POST("/upload", func(ctx context.Context, rc *app.RequestContext) {
		called = true
		rc.String(http.StatusOK, "ok")
	})

	w := ut.PerformRequest(h.Engine, http.MethodPost, "/upload",
		&ut.Body{Body: strings.NewReader(strings.Repeat("x", 100)), Len: 100})

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d, want 413; body=%s", w.Code, w.Body.String())
	}
	assertBodyLimitCode(t, w, errbody.CodeFileTooLarge)
	if called {
		t.Error("超限请求不应进入业务 handler")
	}
}

// TestBodyLimit_UnreadBodyDoesNotPolluteKeepAlive handler 未读完流式请求体就返回时，
// Hertz 仍能丢弃连接上的剩余字节，同一 keep-alive 连接上的下一个请求正常路由。
func TestBodyLimit_UnreadBodyDoesNotPolluteKeepAlive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h := server.Default(server.WithStreamBody(true),
		server.WithListener(ln), server.WithTransport(standard.NewTransporter))
	h.Use(BodyLimit(1 << 20))
	h.POST("/upload", func(ctx context.Context, rc *app.RequestContext) {
		rc.String(http.StatusUnauthorized, "no")
	})
	go h.Spin()
	defer h.Shutdown(context.Background()) //nolint:errcheck

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)

	send := func(body string) *http.Response {
		req := fmt.Sprintf("POST /upload HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		if _, err := io.WriteString(conn, req); err != nil {
			t.Fatalf("write: %v", err)
		}
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
		return resp
	}

	if got := send(strings.Repeat("x", 64<<10)).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("第一个请求状态码 = %d, want 401", got)
	}
	if got := send("small").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("第二个请求状态码 = %d, want 401（剩余请求体污染了连接）", got)
	}
}
