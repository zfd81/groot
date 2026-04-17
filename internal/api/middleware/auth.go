package middleware

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/config"
)

// AuthMiddleware provides API Key authentication
type AuthMiddleware struct {
	config config.SecurityConfig
}

// NewAuthMiddleware creates a new auth middleware
func NewAuthMiddleware(cfg config.SecurityConfig) *AuthMiddleware {
	return &AuthMiddleware{config: cfg}
}

// Serve returns a Hertz middleware handler
func (m *AuthMiddleware) Serve() app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		if !m.config.Auth.Enabled {
			rc.Next(ctx)
			return
		}

		// Get API Key from header
		headerName := m.config.Auth.APIKey.HeaderName
		apiKey := string(rc.GetHeader(headerName))

		if apiKey == "" {
			rc.SetContentType("application/json")
			rc.SetStatusCode(401)
			rc.Write([]byte(`{"status":"unauthorized","message":"API Key 无效或缺失"}`))
			rc.Abort()
			return
		}

		// Validate API Key
		for _, keyInfo := range m.config.Auth.APIKey.Keys {
			if keyInfo.Key == apiKey {
				// Store caller info in context
				rc.Set("caller", keyInfo.Name)
				rc.Next(ctx)
				return
			}
		}

		// Key not found
		rc.SetContentType("application/json")
		rc.SetStatusCode(401)
		rc.Write([]byte(`{"status":"unauthorized","message":"API Key 无效或缺失"}`))
		rc.Abort()
	}
}

// GetCaller extracts caller name from context
func GetCaller(rc *app.RequestContext) string {
	caller, _ := rc.Get("caller")
	if caller == nil {
		return "unknown"
	}
	return caller.(string)
}
