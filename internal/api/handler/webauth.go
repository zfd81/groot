package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// minPasswordLen 密码最少长度（创建用户与修改密码共用）
const minPasswordLen = 8

// fakeBcryptHash 用户不存在时用于拉平耗时的假哈希（"placeholder" 的 bcrypt）
var fakeBcryptHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// WebAuthHandler 处理 Web 界面登录相关端点
type WebAuthHandler struct {
	users  repo.UserRepo
	store  *websession.Store
	logger *logger.Logger
}

// NewWebAuthHandler 创建 Web 登录处理器
// log 为 nil 时使用 logger.NewNop() 兜底（不应在生产路径出现）。
func NewWebAuthHandler(users repo.UserRepo, store *websession.Store, log *logger.Logger) *WebAuthHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &WebAuthHandler{users: users, store: store, logger: log}
}

type webLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type webChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// validatePassword 校验密码规则，非法时返回给用户的提示信息
func validatePassword(pwd string) (string, bool) {
	if len(pwd) < minPasswordLen {
		return "密码长度不能少于 8 位", false
	}
	return "", true
}

// isSecureRequest 判断请求是否经由 https 到达（直连 TLS 或反代注入的
// X-Forwarded-Proto），据此决定会话 Cookie 是否置 Secure。
func isSecureRequest(rc *app.RequestContext) bool {
	if strings.EqualFold(string(rc.GetHeader("X-Forwarded-Proto")), "https") {
		return true
	}
	return strings.EqualFold(string(rc.URI().Scheme()), "https")
}

// setSessionCookie 下发会话 Cookie。maxAge 为 0（浏览器会话 Cookie）：
// 有效期完全由服务端滑动续期控制，固定 maxAge 会让浏览器到点删 Cookie 使续期失效。
func (h *WebAuthHandler) setSessionCookie(rc *app.RequestContext, token string) {
	rc.SetCookie(websession.CookieName, token, 0, "/", "",
		protocol.CookieSameSiteStrictMode, isSecureRequest(rc), true)
}

// Setup 处理 POST /web/setup，创建首个用户（仅用户表为空时允许）
func (h *WebAuthHandler) Setup(ctx context.Context, rc *app.RequestContext) {
	n, err := h.users.Count(ctx)
	if err != nil {
		h.logger.Error("查询用户数量失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "服务器内部错误"})
		return
	}
	if n > 0 {
		rc.JSON(409, utils.H{"status": "already_initialized", "message": "用户已存在"})
		return
	}
	var req webLoginRequest
	if err := json.Unmarshal(rc.Request.Body(), &req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求格式错误"})
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "用户名不能为空"})
		return
	}
	if msg, ok := validatePassword(req.Password); !ok {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": msg})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("密码加密失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "服务器内部错误"})
		return
	}
	now := time.Now()
	u := &repo.User{
		ID:           now.Format("20060102150405"), // 系统编号：yyyyMMddHHmmss
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.users.Create(ctx, u); err != nil {
		h.logger.Error("创建用户失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "创建用户失败"})
		return
	}
	h.logger.Info("Web 用户已创建", zap.String("username", username))
	rc.JSON(200, utils.H{"status": "ok"})
}

// Login 处理 POST /web/login
func (h *WebAuthHandler) Login(ctx context.Context, rc *app.RequestContext) {
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
	user, err := h.users.GetByUsername(ctx, strings.TrimSpace(req.Username))
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		h.logger.Error("查询用户失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "服务器内部错误"})
		return
	}
	// 用户不存在时仍对假哈希做一次 bcrypt 比较，拉平耗时，
	// 避免由响应时间区分"用户名错"与"密码错"。
	hash := fakeBcryptHash
	if user != nil {
		hash = []byte(user.PasswordHash)
	}
	compareErr := bcrypt.CompareHashAndPassword(hash, []byte(req.Password))
	if user == nil || compareErr != nil {
		h.store.RecordFailure(key)
		// 仅记录来源，不记录用户名与密码，避免凭证进入日志
		h.logger.Warn("Web 登录失败", zap.String("source", key))
		rc.JSON(401, utils.H{"status": "unauthorized", "message": "用户名或密码错误"})
		return
	}
	h.store.ClearFailures(key)
	if err := h.users.UpdateLastLogin(ctx, user.ID, time.Now()); err != nil {
		// 最后登录时间只是附加信息，更新失败不阻断登录
		h.logger.Warn("更新最后登录时间失败", zap.Error(err))
	}
	token := h.store.Create(user.ID)
	h.setSessionCookie(rc, token)
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

// Logout 处理 POST /web/logout（幂等）
func (h *WebAuthHandler) Logout(ctx context.Context, rc *app.RequestContext) {
	if token := string(rc.Cookie(websession.CookieName)); token != "" {
		h.store.Delete(token)
	}
	// maxAge=-1 让浏览器立即删除 Cookie
	rc.SetCookie(websession.CookieName, "", -1, "/", "",
		protocol.CookieSameSiteStrictMode, isSecureRequest(rc), true)
	rc.JSON(200, utils.H{"status": "ok"})
}

// Me 处理 GET /web/me，前端据此决定进入创建用户页、登录页还是主界面
func (h *WebAuthHandler) Me(ctx context.Context, rc *app.RequestContext) {
	n, err := h.users.Count(ctx)
	if err != nil {
		h.logger.Error("查询用户数量失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "服务器内部错误"})
		return
	}
	resp := utils.H{
		"authenticated": false,
		"auth_required": true,
		"needs_setup":   n == 0,
	}
	if token := string(rc.Cookie(websession.CookieName)); token != "" {
		if userID, ok := h.store.Validate(token); ok {
			resp["authenticated"] = true
			if user, err := h.users.GetByID(ctx, userID); err == nil {
				resp["username"] = user.Username
			}
		}
	}
	rc.JSON(200, resp)
}

// ChangePassword 处理 POST /web/password（需有效会话）。
// 修改成功后踢掉该用户的其他会话，当前会话保留。
func (h *WebAuthHandler) ChangePassword(ctx context.Context, rc *app.RequestContext) {
	token := string(rc.Cookie(websession.CookieName))
	userID, ok := "", false
	if token != "" {
		userID, ok = h.store.Validate(token)
	}
	if !ok {
		rc.JSON(401, utils.H{"status": "unauthorized", "message": "未登录或会话已过期"})
		return
	}
	var req webChangePasswordRequest
	if err := json.Unmarshal(rc.Request.Body(), &req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求格式错误"})
		return
	}
	user, err := h.users.GetByID(ctx, userID)
	if err != nil {
		h.logger.Error("查询用户失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "服务器内部错误"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)) != nil {
		rc.JSON(401, utils.H{"status": "wrong_password", "message": "原始密码错误"})
		return
	}
	if msg, ok := validatePassword(req.NewPassword); !ok {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": msg})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("密码加密失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "服务器内部错误"})
		return
	}
	if err := h.users.UpdatePassword(ctx, user.ID, string(hash)); err != nil {
		h.logger.Error("更新密码失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "更新密码失败"})
		return
	}
	kicked := h.store.DeleteOtherByUser(user.ID, token)
	h.logger.Info("Web 用户已修改密码",
		zap.String("username", user.Username), zap.Int("kicked_sessions", kicked))
	rc.JSON(200, utils.H{"status": "ok"})
}
