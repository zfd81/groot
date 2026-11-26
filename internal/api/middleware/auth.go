package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// ServicePermissionResolver 解析子服务声明的权限点；名字不存在或子服务无效时 found 为 false。
type ServicePermissionResolver interface {
	ServicePermission(name string) (string, bool)
}

// AuthMiddleware 提供 API Key（JWT）/ Web Cookie 认证，始终开启。
type AuthMiddleware struct {
	config      config.SecurityConfig
	webStore    *websession.Store
	apiKeys     repo.APIKeyRepo
	log         *logger.Logger
	servicePerm ServicePermissionResolver
}

// NewAuthMiddleware creates a new auth middleware.
// webStore 为 Web 登录会话存储；传 nil 表示不启用 Cookie 凭证。
func NewAuthMiddleware(cfg config.SecurityConfig, webStore *websession.Store, apiKeys repo.APIKeyRepo, log *logger.Logger) *AuthMiddleware {
	return &AuthMiddleware{config: cfg, webStore: webStore, apiKeys: apiKeys, log: log}
}

// SetServicePermissionResolver 注入子服务权限解析器；传 nil 时服务路径沿用默认权限。
func (m *AuthMiddleware) SetServicePermissionResolver(r ServicePermissionResolver) {
	m.servicePerm = r
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

		tokenStr := bearerToken(rc)
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

		// 权限检查以数据库行为准；命中 /services/<单段> 时以子服务声明的权限点覆盖默认映射
		path := string(rc.URI().Path())
		method := string(rc.Method())
		requiredPerm := getRequiredPermission(path, method)
		if name, ok := serviceNameFromPath(path); ok && m.servicePerm != nil {
			// 空权限点一律不接受：resolver 返回空串时沿用静态默认（all，最严格），避免绕过检查
			if perm, found := m.servicePerm.ServicePermission(name); found && perm != "" {
				requiredPerm = perm
			}
		}
		if requiredPerm != "" && !hasPermission(key.Permissions, requiredPerm) {
			errbody.Write(rc, 403, "权限不足: 需要 "+requiredPerm+" 权限",
				errbody.TypePermission, "", errbody.CodeInsufficientPermissions)
			return
		}

		// 注入调用方身份：租户来自 Key 所属租户；用户标识来自请求头，非法或缺失归一为空串，
		// 中间件不因此拒绝请求，是否必须提供由各 handler 自行决定
		rc.Set("caller", key.Name)
		rc.Set("tenant_id", key.TenantID)
		userID, _ := ValidIdentifier(string(rc.GetHeader(UserIDHeader)))
		rc.Set("user_id", userID)
		rc.Next(ctx)
	}
}

// bearerPrefix 是 Authorization 头要求的 scheme 前缀。
const bearerPrefix = "Bearer "

// bearerToken 从 Authorization 头解析 Bearer token。
// 前缀 "Bearer " 大小写不敏感，token 本体原样保留（仅 TrimSpace）；不合法返回空串。
func bearerToken(rc *app.RequestContext) string {
	value := string(rc.GetHeader("Authorization"))
	if len(value) < len(bearerPrefix) || !strings.EqualFold(value[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(value[len(bearerPrefix):])
}

func writeUnauthorized(rc *app.RequestContext) {
	errbody.Write(rc, 401, "API Key 无效或缺失", errbody.TypeAuthentication, "", errbody.CodeInvalidAPIKey)
}

// serviceNameFromPath 从 /services/<单段> 路径提取子服务名；
// /services/、多段路径及非服务路径一律不命中。
func serviceNameFromPath(path string) (string, bool) {
	const prefix = "/services/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name := path[len(prefix):]
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
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
	// OpenAI 兼容对话端点 (POST /chat/completions)：chat 权限
	if path == "/chat/completions" && method == "POST" {
		return "chat"
	}

	// 模型列表端点 (GET /models)：只暴露模型清单供客户端选择，归入 chat 权限
	if path == "/models" && method == "GET" {
		return "chat"
	}

	// Transcription endpoint (POST /audio/transcriptions)：语音输入是聊天的一种输入方式，
	// 与 /chat/completions 共用 chat 权限，不另设权限点
	if path == "/audio/transcriptions" && method == "POST" {
		return "chat"
	}

	// Speech endpoint (POST /audio/speech)：语音输出是聊天回复的一种呈现方式，同样共用 chat 权限
	if path == "/audio/speech" && method == "POST" {
		return "chat"
	}

	// Image understanding endpoint (POST /image/understanding)：图片解析是聊天输入的一种形式，
	// 与 /chat/completions 共用 chat 权限
	if path == "/image/understanding" && method == "POST" {
		return "chat"
	}

	// Stop endpoint (POST /chat/:sid/:cid/stop)：中止对话与发起对话同属 chat 权限
	if strings.HasPrefix(path, "/chat/") && strings.HasSuffix(path, "/stop") && method == "POST" {
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

	// Session delete endpoint (DELETE /sess/:sid) 单独的 delete 权限
	if strings.HasPrefix(path, "/sess/") && method == "DELETE" {
		return "delete"
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
