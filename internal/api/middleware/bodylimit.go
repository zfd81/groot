package middleware

import (
	"context"
	"io"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/errbody"
)

// BodyLimit 强制请求体大小上限。Hertz 开启 stream body 后不会因超限拒绝请求，
// 而是把完整长度的流交给 handler；本中间件按 Content-Length 在读取前拦截已知超限，
// 并为流式请求体加一层计数读取，读取超过 max 时返回 errbody.ErrBodyTooLarge。
// max <= 0 表示不限制。
func BodyLimit(max int64) app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		if max <= 0 {
			rc.Next(ctx)
			return
		}
		if length := rc.Request.Header.ContentLength(); length > 0 && int64(length) > max {
			writeBodyTooLarge(rc)
			return
		}
		if rc.Request.IsBodyStream() {
			orig := rc.Request.BodyStream()
			limited := &limitedBodyReader{reader: orig, remaining: max}
			// ConstructBodyStream 保留已声明的 Content-Length，不重置请求体与流。
			rc.Request.ConstructBodyStream(rc.Request.BodyBuffer(), limited)
			rc.Next(ctx)
			// Hertz 在请求结束时只会对自己的流类型丢弃连接上未读完的剩余字节；
			// 这里把原始流还原回去，否则残留字节会被当成 keep-alive 连接上的下一个请求。
			if rc.Request.IsBodyStream() {
				rc.Request.ConstructBodyStream(rc.Request.BodyBuffer(), orig)
			}
			if limited.remaining < 0 {
				// 已超限：剩余字节可能非常多，直接关闭连接而不是为丢弃它们阻塞。
				rc.SetConnectionClose()
			}
			return
		}
		if int64(len(rc.Request.Body())) > max {
			writeBodyTooLarge(rc)
			return
		}
		rc.Next(ctx)
	}
}

// writeBodyTooLarge 以 413 file_too_large 结束请求。
func writeBodyTooLarge(rc *app.RequestContext) {
	errbody.Write(rc, http.StatusRequestEntityTooLarge, "请求体超过大小上限",
		errbody.TypeInvalidRequest, "", errbody.CodeFileTooLarge)
}

// limitedBodyReader 在读取时累计字节数，超过上限后返回 errbody.ErrBodyTooLarge；
// 保留底层读取器的 Close 行为，便于 Hertz 在请求结束后释放资源。
type limitedBodyReader struct {
	reader    io.Reader
	remaining int64
}

func (r *limitedBodyReader) Read(p []byte) (int, error) {
	if r.remaining < 0 {
		return 0, errbody.ErrBodyTooLarge
	}
	// 多读 1 字节，用于区分「恰好等于上限」与「超过上限」。
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	if r.remaining < 0 {
		// 丢弃多读的那 1 字节，调用方最多只会拿到 max 字节。
		return n - 1, errbody.ErrBodyTooLarge
	}
	return n, err
}

// Close 转发到底层读取器（若它实现了 io.Closer）。
func (r *limitedBodyReader) Close() error {
	if closer, ok := r.reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
