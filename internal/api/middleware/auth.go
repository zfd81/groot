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

// Serve implements middleware handler
func (m *AuthMiddleware) Serve(ctx context.Context, rc *app.RequestContext, next app.HandlerFunc) {
	if !m.config.Auth.Enabled {
		next(ctx, rc)
		return
	}

	// Get API Key from header
	headerName := m.config.Auth.APIKey.HeaderName
	apiKey := string(rc.GetHeader(headerName))

	if apiKey == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(401)
		rc.Write([]byte(`{"status":"unauthorized","message":"API Key 无效或缺失"}`))
		return
	}

	// Validate API Key
	for _, keyInfo := range m.config.Auth.APIKey.Keys {
		if keyInfo.Key == apiKey {
			// Store caller info in context
			rc.Set("caller", keyInfo.Name)
			next(ctx, rc)
			return
		}
	}

	// Key not found
	rc.SetContentType("application/json")
	rc.SetStatusCode(401)
	rc.Write([]byte(`{"status":"unauthorized","message":"API Key 无效或缺失"}`))
}

// CheckPermission checks if key has required permission
func (m *AuthMiddleware) CheckPermission(permissions []string, required string) bool {
	for _, p := range permissions {
		if p == "all" || p == required {
			return true
		}
	}
	return false
}

// GetPermissions extracts permissions from config
func (m *AuthMiddleware) GetPermissions(apiKey string) []string {
	for _, keyInfo := range m.config.Auth.APIKey.Keys {
		if keyInfo.Key == apiKey {
			return keyInfo.Permissions
		}
	}
	return nil
}

// GetCaller extracts caller name from context
func GetCaller(rc *app.RequestContext) string {
	caller, _ := rc.Get("caller")
	if caller == nil {
		return "unknown"
	}
	return caller.(string)
}
