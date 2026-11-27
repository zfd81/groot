package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/logger"
)

// targetFunc 返回可代理的环回端口；进程未就绪时 ok 为 false。
type targetFunc func() (port int, ok bool)

// internalTokenFunc 返回当前 Python 进程的内部令牌；每次请求调用，令牌随重启轮换。
type internalTokenFunc func() string

const (
	// upstreamDialTimeout 是连接上游 Python 插件的超时。
	upstreamDialTimeout = 5 * time.Second

	// grootHeaderPrefix 是 Groot 内部请求头前缀，客户端携带的同类头一律丢弃。
	grootHeaderPrefix = "x-groot-"

	// 设计文档 1.5.4 定义的六个内部请求头。
	headerInternalToken = "X-Groot-Internal-Token"
	headerCaller        = "X-Groot-Caller"
	headerTenantID      = "X-Groot-Tenant-Id"
	headerUserID        = "X-Groot-User-Id"
	headerRequestID     = "X-Groot-Request-Id"
	headerPlugin        = "X-Groot-Plugin"

	// requestIDBytes 是请求 ID 的随机字节数，十六进制编码后为 32 个字符。
	requestIDBytes = 16
)

// hopHeaders 是逐跳头，转发请求与响应时都要移除。
var hopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

// newProxy 返回把 /plugins/<name> 转发到 Python 插件环回端口的 handler。
// target 在每次请求时查询可代理端口；token 返回当前子进程的内部令牌，
// 每次请求读取，随进程重启轮换。
func newProxy(name string, target targetFunc, timeout time.Duration, log *logger.Logger, internalToken internalTokenFunc) app.HandlerFunc {
	if log == nil {
		log = logger.NewNop()
	}
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}

	client := &http.Client{
		Transport: &http.Transport{
			// 上游永远是环回地址，不走环境变量里的代理。
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   upstreamDialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: timeout,
			// 不自动解压：响应体与 Content-Encoding 原样透传。
			DisableCompression:  true,
			MaxIdleConnsPerHost: 16,
			// Python 进程重启会换端口，空闲连接要能过期，不要无限留在池里。
			IdleConnTimeout: 90 * time.Second,
		},
		// 不跟随上游重定向：状态码与 Location 原样透传，避免把注入的
		// X-Groot-* 头再次发往重定向目标。
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 响应体由 Hertz 在写完后关闭（SetBodyStream 对 io.Closer 的约定），
	// 既释放上游连接，也取消客户端断开后的上游请求。
	return func(ctx context.Context, rc *app.RequestContext) {
		port, ok := target()
		if !ok {
			errbody.Write(rc, http.StatusServiceUnavailable,
				fmt.Sprintf("Python 插件未就绪: %s", name),
				errbody.TypeAPI, "", errbody.CodePluginUnavailable)
			return
		}

		requestID, err := newRequestID()
		if err != nil {
			errbody.Write(rc, http.StatusInternalServerError, "生成请求 ID 失败",
				errbody.TypeAPI, "", errbody.CodeInternalError)
			return
		}

		tokenValue := internalToken()

		upstreamReq, err := buildUpstreamRequest(ctx, rc, port, name, tokenValue, requestID)
		if err != nil {
			log.Warn("构建上游请求失败",
				zap.String("plugin", name),
				zap.String("request_id", requestID),
				zap.Error(err))
			errbody.Write(rc, http.StatusBadGateway, "构建上游请求失败",
				errbody.TypeAPI, "", errbody.CodeUpstreamError)
			return
		}

		resp, err := client.Do(upstreamReq) //nolint:bodyclose // 响应体交给 Hertz 流式回写
		if err != nil {
			// 读取客户端请求体时超过硬上限：映射为 413，而不是上游错误。
			if errors.Is(err, errbody.ErrBodyTooLarge) {
				errbody.Write(rc, http.StatusRequestEntityTooLarge, "请求体超过大小上限",
					errbody.TypeInvalidRequest, "", errbody.CodeFileTooLarge)
				return
			}
			// 客户端已断开：不再写响应，直接结束。
			if ctx.Err() != nil {
				rc.Abort()
				return
			}
			status := upstreamErrorStatus(err)
			log.Warn("代理 Python 插件请求失败",
				zap.String("plugin", name),
				zap.String("request_id", requestID),
				zap.Int("status", status),
				zap.Error(err))
			message := "上游插件请求失败"
			if status == http.StatusGatewayTimeout {
				message = "上游插件响应超时"
			}
			errbody.Write(rc, status, message, errbody.TypeAPI, "", errbody.CodeUpstreamError)
			return
		}

		copyResponse(rc, resp)
	}
}

// buildUpstreamRequest 构造发往 Python 插件的上游请求：路径为 /<name>，方法、
// 查询串与请求头原样转发，请求体以流方式透传，不整体读入内存。
func buildUpstreamRequest(ctx context.Context, rc *app.RequestContext, port int, name, internalToken, requestID string) (*http.Request, error) {
	rawURL := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/" + name
	if query := rc.URI().QueryString(); len(query) > 0 {
		rawURL += "?" + string(query)
	}

	body, size := requestBody(rc)
	req, err := http.NewRequestWithContext(ctx, string(rc.Method()), rawURL, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = int64(size)
	copyRequestHeaders(rc, req)

	ident := identityOf(rc)
	req.Header.Set(headerInternalToken, internalToken)
	req.Header.Set(headerCaller, ident.caller)
	req.Header.Set(headerTenantID, ident.tenantID)
	req.Header.Set(headerUserID, ident.userID)
	req.Header.Set(headerRequestID, requestID)
	req.Header.Set(headerPlugin, name)
	return req, nil
}

// requestBody 选择请求体来源：优先 Hertz 的流式请求体，其次已缓冲的请求体。
// 返回体大小，未知大小用 -1（分块传输）。
func requestBody(rc *app.RequestContext) (io.Reader, int) {
	if rc.Request.IsBodyStream() {
		size := rc.Request.Header.ContentLength()
		if size < 0 {
			size = -1
		}
		if size == 0 {
			// net/http 把「非 nil Body + ContentLength 0」当作长度未知并改用 chunked；
			// 用 NoBody 保持 Content-Length: 0 原样转发。
			return http.NoBody, 0
		}
		return rc.Request.BodyStream(), size
	}
	body := rc.Request.Body()
	if len(body) == 0 {
		return nil, 0
	}
	return bytes.NewReader(body), len(body)
}

// copyRequestHeaders 复制客户端请求头，跳过 Host、逐跳头、Content-Length
// 与全部 X-Groot-*；这些头随后由 buildUpstreamRequest 以真实值写入。
func copyRequestHeaders(rc *app.RequestContext, req *http.Request) {
	extraHop := connectionTokens(rc)
	rc.Request.Header.VisitAll(func(key, value []byte) {
		name := string(key)
		lower := strings.ToLower(name)
		if lower == "host" || lower == "content-length" {
			return
		}
		if _, ok := hopHeaders[lower]; ok {
			return
		}
		if _, ok := extraHop[lower]; ok {
			return
		}
		if strings.HasPrefix(lower, grootHeaderPrefix) {
			return
		}
		req.Header.Add(name, string(value))
	})
}

// connectionTokens 返回 Connection 头里列出、必须随连接移除的额外头。
func connectionTokens(rc *app.RequestContext) map[string]struct{} {
	tokens := map[string]struct{}{}
	for _, value := range rc.Request.Header.PeekAll("Connection") {
		for _, token := range strings.Split(string(value), ",") {
			if token = strings.ToLower(strings.TrimSpace(token)); token != "" {
				tokens[token] = struct{}{}
			}
		}
	}
	return tokens
}

// copyResponse 把上游响应的状态码与头复制到 Hertz 响应，响应体以流方式回写。
func copyResponse(rc *app.RequestContext, resp *http.Response) {
	rc.SetStatusCode(resp.StatusCode)
	for key, values := range resp.Header {
		lower := strings.ToLower(key)
		if lower == "content-length" {
			continue
		}
		if _, ok := hopHeaders[lower]; ok {
			continue
		}
		for _, value := range values {
			rc.Response.Header.Add(key, value)
		}
	}
	size := -1
	if resp.ContentLength >= 0 {
		size = int(resp.ContentLength)
	}
	rc.SetBodyStream(resp.Body, size)
}

// requestIdentity 是认证中间件注入的调用方身份。
type requestIdentity struct {
	caller   string
	tenantID string
	userID   string
}

// identityOf 读取认证中间件注入的 caller/tenant_id/user_id；
// user_id 为空时回退到 Web 会话的 web_user_id。
func identityOf(rc *app.RequestContext) requestIdentity {
	userID := rc.GetString("user_id")
	if userID == "" {
		userID = rc.GetString("web_user_id")
	}
	return requestIdentity{
		caller:   rc.GetString("caller"),
		tenantID: rc.GetString("tenant_id"),
		userID:   userID,
	}
}

// newRequestID 生成 16 字节随机数的十六进制编码，用于关联两端日志。
func newRequestID() (string, error) {
	buf := make([]byte, requestIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// upstreamErrorStatus 把上游错误映射为状态码：首字节超时 504，其余 502。
// 连接（dial）超时属于连接失败，仍映射 502；只有等待响应头超时才映射 504。
func upstreamErrorStatus(err error) int {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return http.StatusBadGateway
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}
