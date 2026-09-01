package middleware

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/websession"
)

// WebSession 返回 Web 会话保护中间件：校验会话 Cookie，无效则 401。
// 校验通过时向请求上下文注入 caller=web 与 web_user_id（Validate 顺带滑动续期）。
func WebSession(store *websession.Store) app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		token := string(rc.Cookie(websession.CookieName))
		if token == "" {
			abortUnauthorized(rc)
			return
		}
		userID, ok := store.Validate(token)
		if !ok {
			abortUnauthorized(rc)
			return
		}
		rc.Set("caller", "web")
		rc.Set("web_user_id", userID)
		rc.Next(ctx)
	}
}

func abortUnauthorized(rc *app.RequestContext) {
	rc.SetContentType("application/json")
	rc.SetStatusCode(401)
	rc.Write([]byte(`{"status":"unauthorized","message":"未登录或会话已过期"}`))
	rc.Abort()
}
