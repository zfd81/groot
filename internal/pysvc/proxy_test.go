package pysvc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/logger"
)

const (
	testProxyToken = "test-internal-token"
	testCaller     = "alice"
	testTenant     = "t1"
	testUser       = "u1"
)

// identityMiddleware 模拟认证中间件：在代理 handler 之前写入调用方身份。
func identityMiddleware() app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		rc.Set("caller", testCaller)
		rc.Set("tenant_id", testTenant)
		rc.Set("user_id", testUser)
		rc.Next(ctx)
	}
}

// newTestProxyEngine 构建挂有身份中间件与代理 handler 的 Hertz 测试引擎。
func newTestProxyEngine(target targetFunc, timeout time.Duration) *server.Hertz {
	h := server.Default(
		server.WithStreamBody(true),
		server.WithDisablePreParseMultipartForm(true),
	)
	h.Use(identityMiddleware())
	h.Any("/services/asr-router",
		newProxy("asr-router", target, timeout, logger.NewNop(), func() string { return testProxyToken }))
	return h
}

// staticTarget 返回固定结果的 targetFunc。
func staticTarget(port int, ok bool) targetFunc {
	return func() (int, bool) { return port, ok }
}

// startRealProxyServer 启动真实监听的 Hertz 测试服务器，返回访问地址。
// 真实监听会经过 Hertz 的请求体读取路径，用于验证流式与 multipart 预解析行为。
func startRealProxyServer(t *testing.T, target targetFunc, timeout time.Duration, opts ...config.Option) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听测试端口失败: %v", err)
	}
	options := []config.Option{server.WithListener(ln), server.WithStreamBody(true)}
	options = append(options, opts...)

	h := server.Default(options...)
	h.Use(identityMiddleware())
	h.Any("/services/asr-router",
		newProxy("asr-router", target, timeout, logger.NewNop(), func() string { return testProxyToken }))
	go func() { _ = h.Run() }()
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })
	return ln.Addr().String()
}

// upstreamPort 解析 httptest 服务器地址中的端口。
func upstreamPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("解析上游地址失败: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("解析上游端口失败: %v", err)
	}
	return port
}

// capturedRequest 是上游 handler 观察到的一次请求。
type capturedRequest struct {
	method string
	path   string
	query  string
	header http.Header
	body   []byte
}

// captureServer 启动一个把请求写入 channel 的上游。
func captureServer(t *testing.T) (*httptest.Server, <-chan capturedRequest) {
	t.Helper()
	ch := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ch <- capturedRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			header: r.Header.Clone(),
			body:   body,
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// assertEnvelopeCode 断言响应是给定状态码与错误码的 OpenAI 错误信封。
func assertEnvelopeCode(t *testing.T, w *ut.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("状态码 = %d, want %d; body=%s", w.Code, status, w.Body.String())
	}
	var env errbody.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析错误信封失败: %v; body=%s", err, w.Body.String())
	}
	if env.Error.Code == nil || *env.Error.Code != code {
		t.Fatalf("错误码 = %v, want %s; body=%s", env.Error.Code, code, w.Body.String())
	}
}

// TestProxy_ForwardsMethodPathQueryAndBody 上游收到相同的方法、去前缀路径、查询串与请求体。
func TestProxy_ForwardsMethodPathQueryAndBody(t *testing.T) {
	srv, captured := captureServer(t)
	h := newTestProxyEngine(staticTarget(upstreamPort(t, srv), true), 5*time.Second)

	const payload = "hello-body"
	w := ut.PerformRequest(h.Engine, http.MethodPost, "/services/asr-router?x=1&y=2",
		&ut.Body{Body: strings.NewReader(payload), Len: len(payload)})

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	got := <-captured
	if got.method != http.MethodPost {
		t.Errorf("方法 = %q, want POST", got.method)
	}
	if got.path != "/asr-router" {
		t.Errorf("路径 = %q, want /asr-router", got.path)
	}
	if got.query != "x=1&y=2" {
		t.Errorf("查询 = %q, want x=1&y=2", got.query)
	}
	if string(got.body) != payload {
		t.Errorf("请求体 = %q, want %q", got.body, payload)
	}
}

// TestProxy_StripsSpoofedHeaders 客户端伪造的 X-Groot-* 头全部被删除，上游只看到注入值。
func TestProxy_StripsSpoofedHeaders(t *testing.T) {
	srv, captured := captureServer(t)
	h := newTestProxyEngine(staticTarget(upstreamPort(t, srv), true), 5*time.Second)

	w := ut.PerformRequest(h.Engine, http.MethodGet, "/services/asr-router", nil,
		ut.Header{Key: "X-Groot-Caller", Value: "evil"},
		ut.Header{Key: "X-Groot-Internal-Token", Value: "evil"},
		ut.Header{Key: "X-Groot-Tenant-Id", Value: "evil"},
		ut.Header{Key: "X-Groot-User-Id", Value: "evil"},
		ut.Header{Key: "X-Groot-Request-Id", Value: "evil"},
		ut.Header{Key: "X-Groot-Service", Value: "evil"},
	)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	got := <-captured
	want := map[string]string{
		"X-Groot-Internal-Token": testProxyToken,
		"X-Groot-Caller":         testCaller,
		"X-Groot-Tenant-Id":      testTenant,
		"X-Groot-User-Id":        testUser,
		"X-Groot-Service":        "asr-router",
	}
	for key, value := range want {
		if actual := got.header.Get(key); actual != value {
			t.Errorf("%s = %q, want %q", key, actual, value)
		}
	}
	reqID := got.header.Get("X-Groot-Request-Id")
	if len(reqID) != 32 {
		t.Errorf("X-Groot-Request-Id 长度 = %d, want 32: %q", len(reqID), reqID)
	}
	if _, err := hex.DecodeString(reqID); err != nil {
		t.Errorf("X-Groot-Request-Id 不是十六进制: %q (%v)", reqID, err)
	}
	for key := range want {
		if got.header.Get(key) == "evil" {
			t.Errorf("%s 透传了客户端的伪造值", key)
		}
	}
}

// TestProxy_WebUserFallback user_id 为空时回退到 Web 会话的 web_user_id。
func TestProxy_WebUserFallback(t *testing.T) {
	srv, captured := captureServer(t)

	h := server.Default(server.WithStreamBody(true), server.WithDisablePreParseMultipartForm(true))
	h.Use(func(ctx context.Context, rc *app.RequestContext) {
		rc.Set("caller", "web")
		rc.Set("web_user_id", "web-u1")
		rc.Next(ctx)
	})
	h.Any("/services/asr-router",
		newProxy("asr-router", staticTarget(upstreamPort(t, srv), true), 5*time.Second, logger.NewNop(),
			func() string { return testProxyToken }))

	w := ut.PerformRequest(h.Engine, http.MethodGet, "/services/asr-router", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	got := <-captured
	if actual := got.header.Get("X-Groot-User-Id"); actual != "web-u1" {
		t.Errorf("X-Groot-User-Id = %q, want web-u1", actual)
	}
	if actual := got.header.Get("X-Groot-Caller"); actual != "web" {
		t.Errorf("X-Groot-Caller = %q, want web", actual)
	}
}

// TestProxy_DoesNotFollowRedirect 上游 302 原样透传，代理不跟随重定向，
// 上游只收到一次请求（避免把注入的 X-Groot-* 头带到重定向目标）。
func TestProxy_DoesNotFollowRedirect(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Location", "http://127.0.0.1:1/redirected")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	h := newTestProxyEngine(staticTarget(upstreamPort(t, srv), true), 5*time.Second)

	w := ut.PerformRequest(h.Engine, http.MethodGet, "/services/asr-router", nil)

	if w.Code != http.StatusFound {
		t.Fatalf("状态码 = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "http://127.0.0.1:1/redirected" {
		t.Errorf("Location = %q, want 原样透传", loc)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("上游被请求 %d 次, want 1（代理不得跟随重定向）", got)
	}
}

// TestProxy_NotReady503 进程未就绪时返回 503 与 service_unavailable。
func TestProxy_NotReady503(t *testing.T) {
	h := newTestProxyEngine(staticTarget(0, false), 5*time.Second)

	w := ut.PerformRequest(h.Engine, http.MethodGet, "/services/asr-router", nil)
	assertEnvelopeCode(t, w, http.StatusServiceUnavailable, errbody.CodeServiceUnavailable)
}

// TestProxy_UpstreamError502 上游端口已关闭时返回 502 与 upstream_error。
func TestProxy_UpstreamError502(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听临时端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	h := newTestProxyEngine(staticTarget(port, true), 2*time.Second)

	w := ut.PerformRequest(h.Engine, http.MethodGet, "/services/asr-router", nil)
	assertEnvelopeCode(t, w, http.StatusBadGateway, errbody.CodeUpstreamError)
}

// TestProxy_LargeBodyStreams 20MB 请求体完整流式到达上游，不被截断。
func TestProxy_LargeBodyStreams(t *testing.T) {
	const size = 20 << 20
	body := bytes.Repeat([]byte("x"), size)

	countCh := make(chan int64, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			countCh <- -1
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		countCh <- n
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "%d", n)
	}))
	t.Cleanup(srv.Close)

	h := newTestProxyEngine(staticTarget(upstreamPort(t, srv), true), 30*time.Second)

	w := ut.PerformRequest(h.Engine, http.MethodPost, "/services/asr-router",
		&ut.Body{Body: bytes.NewReader(body), Len: len(body)})

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != strconv.Itoa(size) {
		t.Errorf("上游计数 = %s, want %d", got, size)
	}
	if n := <-countCh; n != size {
		t.Errorf("上游读到 %d 字节, want %d", n, size)
	}
}

// TestProxy_MultipartBodyStreams multipart 请求体原样流式转发，包含大文件与相同的 boundary。
func TestProxy_MultipartBodyStreams(t *testing.T) {
	fileData := bytes.Repeat([]byte("multipart-payload-"), 64*1024)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "blob.bin")
	if err != nil {
		t.Fatalf("创建 multipart 文件字段失败: %v", err)
	}
	if _, err := fw.Write(fileData); err != nil {
		t.Fatalf("写入 multipart 文件失败: %v", err)
	}
	if err := mw.WriteField("model", "asr-router"); err != nil {
		t.Fatalf("写入 multipart 文本字段失败: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("结束 multipart 写入失败: %v", err)
	}
	raw := body.Bytes()
	contentType := mw.FormDataContentType()

	srv, captured := captureServer(t)
	// 真实监听，经过 Hertz 的请求体读取路径；关闭预解析后请求体原样透传。
	addr := startRealProxyServer(t, staticTarget(upstreamPort(t, srv), true), 30*time.Second,
		server.WithDisablePreParseMultipartForm(true))

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/services/asr-router", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("构造客户端请求失败: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", resp.StatusCode)
	}

	got := <-captured
	if !bytes.Equal(got.body, raw) {
		t.Fatalf("上游收到的 multipart 请求体与原始字节不一致: 收到 %d 字节, want %d 字节", len(got.body), len(raw))
	}
	if actual := got.header.Get("Content-Type"); actual != contentType {
		t.Errorf("Content-Type = %q, want %q", actual, contentType)
	}
}

// TestProxy_BodyLimit 超限的流式请求体映射为 413，上游不会收到完整请求体。
func TestProxy_BodyLimit(t *testing.T) {
	const limit = 1024
	const total = 4096

	received := make(chan int64, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		received <- n
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	h := server.Default(
		server.WithStreamBody(true),
		server.WithDisablePreParseMultipartForm(true),
	)
	h.Use(identityMiddleware())
	h.Use(middleware.BodyLimit(limit))
	h.Any("/services/asr-router",
		newProxy("asr-router", staticTarget(upstreamPort(t, srv), true), 5*time.Second, logger.NewNop(),
			func() string { return testProxyToken }))

	// Len=-1 构造分块（未知长度）请求体，绕开按 Content-Length 的前置拦截，验证读取中拦截。
	w := ut.PerformRequest(h.Engine, http.MethodPost, "/services/asr-router",
		&ut.Body{Body: strings.NewReader(strings.Repeat("x", total)), Len: -1})

	assertEnvelopeCode(t, w, http.StatusRequestEntityTooLarge, errbody.CodeFileTooLarge)

	select {
	case n := <-received:
		if n >= total {
			t.Fatalf("上游收到完整请求体 %d 字节，超限请求不应完整到达", n)
		}
	case <-time.After(2 * time.Second):
		// 上游未收到请求同样满足「未收到完整请求」。
	}
}

// TestProxy_ClientCancel 客户端取消请求后，上游 handler 在 1 秒内观察到 context 结束。
func TestProxy_ClientCancel(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		<-r.Context().Done()
		close(upstreamDone)
	}))
	t.Cleanup(srv.Close)

	// 真实监听端口：模拟客户端断开需要真实连接，ut.PerformRequest 无法暴露取消。
	addr := startRealProxyServer(t, staticTarget(upstreamPort(t, srv), true), 5*time.Second,
		server.WithSenseClientDisconnection(true),
		server.WithDisablePreParseMultipartForm(true))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+addr+"/services/asr-router", nil)
	if err != nil {
		t.Fatalf("构造客户端请求失败: %v", err)
	}

	clientErr := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		clientErr <- err
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未在 5 秒内收到请求")
	}
	cancel()

	select {
	case <-upstreamDone:
	case <-time.After(time.Second):
		t.Fatal("客户端取消后上游未在 1 秒内观察到 context 结束")
	}
	<-clientErr
}

// TestProxy_ClientCancelWithBody 带（流式）请求体的请求在等待上游响应期间客户端断开，
// 上游同样观察到取消。TestProxy_ClientCancel 只覆盖无正文 GET，而真实场景是音频上传。
// 注意：这依赖 netpoll 传输层（macOS/Linux 默认）在事件循环中感知断连；Windows 使用的
// standard 传输层对带流式请求体的请求不做断连探测，该平台上取消要等 request_timeout 到期。
func TestProxy_ClientCancelWithBody(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(upstreamStarted)
		<-r.Context().Done()
		close(upstreamDone)
	}))
	t.Cleanup(srv.Close)

	addr := startRealProxyServer(t, staticTarget(upstreamPort(t, srv), true), 5*time.Second,
		server.WithSenseClientDisconnection(true),
		server.WithDisablePreParseMultipartForm(true))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := bytes.NewReader(bytes.Repeat([]byte("x"), 64<<10)) // 超过 8KB 才走流式请求体
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/services/asr-router", body)
	if err != nil {
		t.Fatalf("构造客户端请求失败: %v", err)
	}

	clientErr := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		clientErr <- err
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未在 5 秒内收到请求")
	}
	cancel()

	select {
	case <-upstreamDone:
	case <-time.After(time.Second):
		t.Fatal("带正文请求的客户端取消后上游未在 1 秒内观察到 context 结束")
	}
	<-clientErr
}

// TestRequestBody_EmptyStreamUsesNoBody 流式请求体但 Content-Length 为 0 时，上游请求不能带
// 非 nil 的 Body（net/http 会把它当未知长度，发出 chunked 空体），应当是 http.NoBody。
func TestRequestBody_EmptyStreamUsesNoBody(t *testing.T) {
	rc := app.NewContext(0)
	rc.Request.Header.SetContentLength(0)
	rc.Request.ConstructBodyStream(rc.Request.BodyBuffer(), strings.NewReader(""))
	if !rc.Request.IsBodyStream() {
		t.Fatal("测试前置条件：请求应处于流式请求体状态")
	}

	body, size := requestBody(rc)
	if size != 0 {
		t.Errorf("size = %d, want 0", size)
	}
	if body != http.NoBody {
		t.Errorf("body = %T, want http.NoBody", body)
	}
}

// TestProxy_UpstreamTimeout504 上游在 request_timeout 内未返回响应头时返回 504 与 upstream_error。
func TestProxy_UpstreamTimeout504(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	h := newTestProxyEngine(staticTarget(upstreamPort(t, srv), true), 50*time.Millisecond)

	w := ut.PerformRequest(h.Engine, http.MethodGet, "/services/asr-router", nil)
	assertEnvelopeCode(t, w, http.StatusGatewayTimeout, errbody.CodeUpstreamError)
}

// TestProxy_StripsHopByHopHeaders 请求与响应两个方向的逐跳头都不透传，Connection 列出的额外头同样剔除。
func TestProxy_StripsHopByHopHeaders(t *testing.T) {
	captured := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- r.Header.Clone()
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	h := newTestProxyEngine(staticTarget(upstreamPort(t, srv), true), 5*time.Second)

	w := ut.PerformRequest(h.Engine, http.MethodGet, "/services/asr-router", nil,
		ut.Header{Key: "Connection", Value: "X-Hop-Extra"},
		ut.Header{Key: "X-Hop-Extra", Value: "drop-me"},
		ut.Header{Key: "Keep-Alive", Value: "timeout=5"},
		ut.Header{Key: "Proxy-Authorization", Value: "Basic xxx"},
		ut.Header{Key: "X-Keep", Value: "keep-me"},
	)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d; body=%s", w.Code, w.Body.String())
	}

	got := <-captured
	for _, key := range []string{"X-Hop-Extra", "Keep-Alive", "Proxy-Authorization"} {
		if v := got.Get(key); v != "" {
			t.Errorf("逐跳头 %s 被转发到上游: %q", key, v)
		}
	}
	if v := got.Get("X-Keep"); v != "keep-me" {
		t.Errorf("普通头 X-Keep = %q, want keep-me", v)
	}
	if v := w.Header().Get("Keep-Alive"); v != "" {
		t.Errorf("响应逐跳头 Keep-Alive 被透传: %q", v)
	}
	if v := w.Header().Get("X-Upstream"); v != "yes" {
		t.Errorf("响应普通头 X-Upstream = %q, want yes", v)
	}
}

// TestProxy_StreamsUnknownLengthResponse 未知长度（SSE 风格）的响应逐块到达客户端，
// 第一块不必等上游写完整个响应。
func TestProxy_StreamsUnknownLengthResponse(t *testing.T) {
	secondChunk := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-secondChunk
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	// 代理到上游的连接池空闲连接会让 srv.Close 等待；直接 CloseClientConnections。
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })

	addr := startRealProxyServer(t, staticTarget(upstreamPort(t, srv), true), 5*time.Second)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get("http://" + addr + "/services/asr-router")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 64)
	type readResult struct {
		n   int
		err error
	}
	first := make(chan readResult, 1)
	go func() {
		n, err := resp.Body.Read(buf)
		first <- readResult{n, err}
	}()
	select {
	case r := <-first:
		if r.err != nil || string(buf[:r.n]) != "data: first\n\n" {
			t.Fatalf("第一块 = %q, err=%v", buf[:r.n], r.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("上游尚未结束响应时，第一块没有到达客户端（响应未逐块透传）")
	}
	close(secondChunk)
	rest, _ := io.ReadAll(resp.Body)
	if string(rest) != "data: second\n\n" {
		t.Errorf("剩余部分 = %q", rest)
	}
}
