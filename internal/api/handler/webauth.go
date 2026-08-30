package handler

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// WebAuthHandler 处理 Web 界面登录相关端点
type WebAuthHandler struct {
	enabled  bool
	username string
	password string // 明文密码（${ENV} 引用已在 config loader 中展开）
	secure   bool   // 会话 Cookie 是否置 Secure
	store    *websession.Store
	logger   *logger.Logger
}

// NewWebAuthHandler 创建 Web 登录处理器
// log 为 nil 时使用 logger.NewNop() 兜底（不应在生产路径出现）。
func NewWebAuthHandler(cfg config.WebConfig, store *websession.Store, log *logger.Logger) *WebAuthHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &WebAuthHandler{
		enabled:  cfg.Enabled,
		username: cfg.Username,
		password: cfg.Password,
		secure:   cfg.Secure,
		store:    store,
		logger:   log,
	}
}

type webLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// constEqual 常量时间比较两个凭证。先各自 SHA-256 再比较定长摘要，
// 避免 subtle.ConstantTimeCompare 在长度不等时立即返回 0 而泄漏凭证长度。
func constEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// Login 处理 POST /web/login
func (h *WebAuthHandler) Login(ctx context.Context, rc *app.RequestContext) {
	if !h.enabled {
		rc.JSON(200, utils.H{"status": "ok", "auth_required": false})
		return
	}
	// 限速键用真实 TCP 对端地址，而非 ClientIP()——后者采信可伪造的
	// X-Forwarded-For，攻击者每次换一个头即可绕过锁定。
	key := lockKey(rc)
	if h.store.IsLocked(key) {
		rc.JSON(429, utils.H{"status": "locked", "message": "登录失败次数过多，请稍后再试"})
		return
	}
	var req webLoginRequest
	if err := json.Unmarshal(rc.Request.Body(), &req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求格式错误"})
		return
	}
	// 两项均无条件求值，不做短路，避免由耗时差异区分"用户名错"与"密码错"
	userOK := constEqual(req.Username, h.username)
	passOK := constEqual(req.Password, h.password)
	// 配置密码为空视为配置错误，一律拒绝，防止空密码直通
	if h.password == "" || !(userOK && passOK) {
		h.store.RecordFailure(key)
		// 仅记录来源，不记录用户名与密码，避免凭证进入日志
		h.logger.Warn("Web 登录失败", zap.String("source", key))
		rc.JSON(401, utils.H{"status": "unauthorized", "message": "用户名或密码错误"})
		return
	}
	h.store.ClearFailures(key)
	token := h.store.Create()
	// secure 由配置决定：内网/本机 http 部署置 false，经 https 反代部署应置 true
	rc.SetCookie(websession.CookieName, token, int(h.store.TTL().Seconds()), "/", "",
		protocol.CookieSameSiteStrictMode, h.secure, true)
	rc.JSON(200, utils.H{"status": "ok"})
}

// lockKey 返回登录限速使用的来源标识：优先真实 TCP 对端 IP（不可被请求头伪造），
// 取不到时回退 ClientIP()。
func lockKey(rc *app.RequestContext) string {
	if addr := rc.RemoteAddr(); addr != nil {
		if host, _, err := net.SplitHostPort(addr.String()); err == nil {
			return host
		}
		return addr.String()
	}
	return rc.ClientIP()
}

// Logout 处理 POST /web/logout
func (h *WebAuthHandler) Logout(ctx context.Context, rc *app.RequestContext) {
	// 认证关闭时 store 为 nil，直接返回，避免空指针
	if !h.enabled {
		rc.JSON(200, utils.H{"status": "ok", "auth_required": false})
		return
	}
	if token := string(rc.Cookie(websession.CookieName)); token != "" {
		h.store.Delete(token)
	}
	// maxAge=-1 让浏览器立即删除 Cookie
	rc.SetCookie(websession.CookieName, "", -1, "/", "",
		protocol.CookieSameSiteStrictMode, h.secure, true)
	rc.JSON(200, utils.H{"status": "ok"})
}

// Me 处理 GET /web/me，前端据此判断是否需要跳登录页
func (h *WebAuthHandler) Me(ctx context.Context, rc *app.RequestContext) {
	authenticated := true
	if h.enabled {
		token := string(rc.Cookie(websession.CookieName))
		authenticated = token != "" && h.store.Validate(token)
	}
	rc.JSON(200, utils.H{
		"authenticated": authenticated,
		"auth_required": h.enabled,
	})
}
