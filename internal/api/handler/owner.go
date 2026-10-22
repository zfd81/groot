package handler

import (
	"github.com/cloudwego/hertz/pkg/app"

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
		rc.SetContentType("application/json")
		rc.SetStatusCode(403)
		rc.Write([]byte(`{"status":"forbidden","message":"API Key 未绑定租户"}`))
		return nil, false
	}
	userID := middleware.GetUserID(rc)
	if userID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"缺少或非法的 X-User-ID"}`))
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

// ownerFields 展开归属信息为 (tenantID, userID)；owner 为 nil（Web 管理员）时两者为空串。
func ownerFields(owner *repo.SessionOwner) (tenantID, userID string) {
	if owner == nil {
		return "", ""
	}
	return owner.TenantID, owner.UserID
}
