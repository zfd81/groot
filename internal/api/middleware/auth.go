package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// AuthMiddleware 提供 API Key（JWT）/ Web Cookie 认证，始终开启。
type AuthMiddleware struct {
	config   config.SecurityConfig
	webStore *websession.Store
	apiKeys  repo.APIKeyRepo
	log      *logger.Logger
}

// NewAuthMiddleware creates a new auth middleware.
// webStore 为 Web 登录会话存储；传 nil 表示不启用 Cookie 凭证。
func NewAuthMiddleware(cfg config.SecurityConfig, webStore *websession.Store, apiKeys repo.APIKeyRepo, log *logger.Logger) *AuthMiddleware {
	return &AuthMiddleware{config: cfg, webStore: webStore, apiKeys: apiKeys, log: log}
}

// Serve returns a Hertz middleware handler
func (m *AuthMiddleware) Serve() app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		// Web 会话 Cookie 凭证：有效则等同 all 权限放行（Validate 顺带滑动续期）
		if m.webStore != nil {
			if token := string(rc.Cookie(websession.CookieName)); token != "" {
				if userID, ok := m.webStore.Validate(token); ok {
					rc.Set("caller", "web")
					rc.Set("web_user_id", userID)
					rc.Next(ctx)
					return
				}
			}
		}

		headerName := m.config.Auth.HeaderName
		if headerName == "" {
			headerName = "X-API-Key"
		}
		tokenStr := string(rc.GetHeader(headerName))
		if tokenStr == "" {
			writeUnauthorized(rc)
			return
		}

		// 1) 验签 + 过期检查；2) 以 jti 反查数据库确认未被删除（删除即吊销）。
		// 三类失败统一 401 不区分原因，避免向攻击者泄露信息。
		jti, err := auth.Verify(tokenStr, m.config.Auth.Secret)
		if err != nil {
			writeUnauthorized(rc)
			return
		}
		key, err := m.apiKeys.GetByID(ctx, jti)
		if err != nil {
			// 未找到属预期路径（删除即吊销），不打日志；库故障等其他错误记 Error 供运维区分
			if !errors.Is(err, repo.ErrNotFound) {
				m.log.Error("API Key 认证查询失败: " + err.Error())
			}
			writeUnauthorized(rc)
			return
		}

		// 权限检查以数据库行为准
		path := string(rc.URI().Path())
		method := string(rc.Method())
		requiredPerm := getRequiredPermission(path, method)
		if requiredPerm != "" && !hasPermission(key.Permissions, requiredPerm) {
			rc.SetContentType("application/json")
			rc.SetStatusCode(403)
			rc.Write([]byte(fmt.Sprintf(`{"status":"forbidden","message":"权限不足: 需要 %s 权限"}`, requiredPerm)))
			rc.Abort()
			return
		}

		rc.Set("caller", key.Name)
		rc.Next(ctx)
	}
}

func writeUnauthorized(rc *app.RequestContext) {
	rc.SetContentType("application/json")
	rc.SetStatusCode(401)
	rc.Write([]byte(`{"status":"unauthorized","message":"API Key 无效或缺失"}`))
	rc.Abort()
}

// hasPermission 判断权限集合是否满足所需权限点。
// 空集合一律拒绝：创建流程保证至少一个权限点，空集只可能来自脏数据。
func hasPermission(perms []string, required string) bool {
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
	// Chat endpoint (POST /chat)
	if path == "/chat" && method == "POST" {
		return "chat"
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
