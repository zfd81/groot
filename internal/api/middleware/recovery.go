package middleware

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// RecoveryMiddleware provides panic recovery
type RecoveryMiddleware struct {
	logger *logger.Logger
}

// NewRecoveryMiddleware creates a new recovery middleware
func NewRecoveryMiddleware(log *logger.Logger) *RecoveryMiddleware {
	return &RecoveryMiddleware{logger: log}
}

// Serve returns a Hertz middleware handler
func (m *RecoveryMiddleware) Serve() app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()
				m.logger.Error("panic recovered",
					zap.Any("error", err),
					zap.String("stack", string(stack)),
					zap.String("path", string(rc.URI().Path())),
				)

				rc.SetContentType("application/json")
				rc.SetStatusCode(500)
				rc.Write([]byte(fmt.Sprintf(`{"status":"internal_error","message":"%s"}`, err)))
				rc.Abort()
			}
		}()

		rc.Next(ctx)
	}
}
