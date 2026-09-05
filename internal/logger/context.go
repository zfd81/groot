package logger

import (
	"context"
	"sync/atomic"
)

// ctxKey 是 context 中存放 Logger 的私有 key 类型
type ctxKey struct{}

// defaultLogger 包级默认 logger，FromContext 取不到时的回退，
// 未 SetDefault 时为 no-op logger，保证永不返回 nil。
var defaultLogger atomic.Pointer[Logger]

func init() {
	defaultLogger.Store(NewNop())
}

// SetDefault 设置包级默认 logger（应在进程启动创建 logger 后调用一次）；
// 传入 nil 时忽略，保证默认 logger 始终有效
func SetDefault(l *Logger) {
	if l != nil {
		defaultLogger.Store(l)
	}
}

// NewContext 把 logger 放入 context，供执行链路下游取用
func NewContext(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext 从 context 取出 logger；不存在时回退到默认 logger，永不返回 nil
func FromContext(ctx context.Context) *Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*Logger); ok && l != nil {
			return l
		}
	}
	return defaultLogger.Load()
}
