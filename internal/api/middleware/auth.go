package middleware

import (
	"context"
	"fmt"
	"strings"

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
			rc.Set("caller", "anonymous")
			rc.Next(ctx)
			return
		}

		// Get API Key from header
		headerName := m.config.Auth.APIKey.HeaderName
		if headerName == "" {
			headerName = "X-API-Key"
		}
		apiKey := string(rc.GetHeader(headerName))

		if apiKey == "" {
			rc.SetContentType("application/json")
			rc.SetStatusCode(401)
			rc.Write([]byte(`{"status":"unauthorized","message":"API Key 无效或缺失"}`))
			rc.Abort()
			return
		}

		// Validate API Key and check permissions
		for _, keyInfo := range m.config.Auth.APIKey.Keys {
			if keyInfo.Key == apiKey {
				// Check permission for this path
				path := string(rc.URI().Path())
				method := string(rc.Method())
				requiredPerm := getRequiredPermission(path, method)

				if requiredPerm != "" && !m.hasPermission(keyInfo.Permissions, requiredPerm) {
					rc.SetContentType("application/json")
					rc.SetStatusCode(403)
					rc.Write([]byte(fmt.Sprintf(`{"status":"forbidden","message":"权限不足: 需要 %s 权限"}`, requiredPerm)))
					rc.Abort()
					return
				}

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

// hasPermission checks if key has required permission
func (m *AuthMiddleware) hasPermission(perms []string, required string) bool {
	if len(perms) == 0 {
		return true // No permissions defined = all access
	}

	for _, perm := range perms {
		perm = strings.TrimSpace(perm)
		if perm == "all" || perm == required {
			return true
		}
	}
	return false
}

// getRequiredPermission maps path to required permission
func getRequiredPermission(path, method string) string {
	// Health check - no permission required
	if path == "/health" {
		return ""
	}

	// Chat endpoint (POST /chat)
	if path == "/chat" && method == "POST" {
		return "chat"
	}

	// Cancel endpoint (DELETE /chat/:sid)
	if strings.HasPrefix(path, "/chat/") && method == "DELETE" {
		return "cancel"
	}

	// Status endpoint (GET /chat/status/:sid)
	if strings.HasPrefix(path, "/chat/status/") {
		return "status"
	}

	// Chat detail endpoint (GET /chat/:sid/:cid or GET /chat/:sid)
	if strings.HasPrefix(path, "/chat/") && !strings.Contains(path, "/status/") && method == "GET" {
		return "detail"
	}

	// Session history endpoint (must come before /sess/ prefix check)
	if path == "/sess/history" {
		return "history"
	}

	// Session endpoints
	if strings.HasPrefix(path, "/sess/") {
		return "session"
	}

	// Skills endpoint
	if path == "/skills" {
		return "skills"
	}

	// Tools endpoint
	if path == "/tools" {
		return "tools"
	}

	// Schedule endpoints
	if strings.HasPrefix(path, "/schedule") {
		return "schedule"
	}

	// Default: require all
	return "all"
}

// GetCaller extracts caller name from context
func GetCaller(rc *app.RequestContext) string {
	caller, _ := rc.Get("caller")
	if caller == nil {
		return "anonymous"
	}
	return caller.(string)
}
