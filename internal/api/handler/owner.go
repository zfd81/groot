package handler

import (
	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
)

// requireOwner 会话类接口的公共归属前置检查。
// Web 调用者（会话 Cookie）视为管理员，返回 (nil, true)，不做租户/用户限制；
// API Key 调用者必须同时携带租户与用户标识，缺失时直接写出错误响应并返回 (nil, false)，
// 调用方据此 return；检查通过返回归属信息与 true。
func requireOwner(rc *app.RequestContext) (owner *repo.SessionOwner, ok bool) {
	if middleware.IsWebCaller(rc) {
		return nil, true
	}
	tenant := middleware.GetTenant(rc)
	if tenant == "" {
		errbody.Write(rc, 403, "API Key 未绑定租户",
			errbody.TypePermission, "", errbody.CodeTenantNotBound)
		return nil, false
	}
	userID := middleware.GetUserID(rc)
	if userID == "" {
		errbody.Write(rc, 400, "缺少或非法的 user",
			errbody.TypeInvalidRequest, "user", errbody.CodeInvalidRequest)
		return nil, false
	}
	return &repo.SessionOwner{TenantID: tenant, UserID: userID}, true
}

// ownsOrAdmin 判断调用者是否可访问指定会话：
// owner 为 nil（管理员）时只要求会话存在，否则要求会话归属于该租户与用户。
func ownsOrAdmin(mem *memory.Manager, owner *repo.SessionOwner, sid string) bool {
	if owner == nil {
		return mem.ExistsSession(sid)
	}
	return mem.OwnsSession(sid, owner.TenantID, owner.UserID)
}

// writeMissingSessionID 写出 400 session_id 缺失响应。
func writeMissingSessionID(rc *app.RequestContext) {
	errbody.Write(rc, 400, "session_id 参数缺失",
		errbody.TypeInvalidRequest, "session_id", errbody.CodeInvalidRequest)
}

// writeMissingChatID 写出 400 chat_id 缺失响应。
func writeMissingChatID(rc *app.RequestContext) {
	errbody.Write(rc, 400, "chat_id 参数缺失",
		errbody.TypeInvalidRequest, "chat_id", errbody.CodeInvalidRequest)
}

// writeSessionNotFound 写出 404 会话不存在响应；归属不符与会话确实不存在共用此响应。
func writeSessionNotFound(rc *app.RequestContext) {
	errbody.Write(rc, 404, "会话不存在",
		errbody.TypeInvalidRequest, "", errbody.CodeSessionNotFound)
}
