package middleware

import (
	"regexp"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
)

// UserIDHeader 携带终端用户标识的请求头，由 API Key 调用方在转发请求时填入。
const UserIDHeader = "X-User-ID"

// identifierPattern 租户 / 用户标识允许的字符集：字母、数字、下划线、连字符。
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidIdentifier 校验标识：去除首尾空白后长度须为 1..64 且匹配 identifierPattern。
// 合法返回 (trimmed, true)，否则返回 ("", false)。
func ValidIdentifier(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) == 0 || len(s) > 64 || !identifierPattern.MatchString(s) {
		return "", false
	}
	return s, true
}

// GetTenant 读取中间件注入的租户标识，缺失返回空串。
func GetTenant(rc *app.RequestContext) string {
	return rc.GetString("tenant_id")
}

// GetUserID 读取中间件注入的用户标识，缺失或原始值非法时为空串。
func GetUserID(rc *app.RequestContext) string {
	return rc.GetString("user_id")
}

// IsWebCaller 判断当前请求是否来自 Web 会话 Cookie 凭证。
func IsWebCaller(rc *app.RequestContext) bool {
	return GetCaller(rc) == "web"
}
