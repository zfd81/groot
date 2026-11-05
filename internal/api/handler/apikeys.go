package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// APIKeysHandler API Key 管理（/web/apikeys 系列端点，WebSession 保护）
type APIKeysHandler struct {
	keys   repo.APIKeyRepo
	secret string
	log    *logger.Logger
}

func NewAPIKeysHandler(keys repo.APIKeyRepo, cfg config.SecurityConfig, log *logger.Logger) *APIKeysHandler {
	return &APIKeysHandler{keys: keys, secret: cfg.Auth.Secret, log: log}
}

// apiKeyNow 可注入时钟，便于测试主键同秒冲突重试
var apiKeyNow = time.Now

// expiresInOptions 过期时间枚举 → 距创建时刻的日历偏移
var expiresInOptions = map[string]func(t time.Time) time.Time{
	"1d":  func(t time.Time) time.Time { return t.AddDate(0, 0, 1) },
	"7d":  func(t time.Time) time.Time { return t.AddDate(0, 0, 7) },
	"1mo": func(t time.Time) time.Time { return t.AddDate(0, 1, 0) },
	"6mo": func(t time.Time) time.Time { return t.AddDate(0, 6, 0) },
	"1y":  func(t time.Time) time.Time { return t.AddDate(1, 0, 0) },
	"10y": func(t time.Time) time.Time { return t.AddDate(10, 0, 0) },
}

type apiKeyCreateRequest struct {
	Name        string   `json:"name"`
	TenantID    string   `json:"tenant_id"`
	ExpiresIn   string   `json:"expires_in"`
	Permissions []string `json:"permissions"`
}

type apiKeyInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	TenantID    string   `json:"tenant_id"`
	Permissions []string `json:"permissions"`
	ExpiresAt   int64    `json:"expires_at"`
	CreatedAt   int64    `json:"created_at"`
	Expired     bool     `json:"expired"`
}

// apiKeyCreateResponse 创建成功响应：元数据 + 完整 token（仅创建时返回一次）
type apiKeyCreateResponse struct {
	apiKeyInfo
	Token string `json:"token"`
}

func toAPIKeyInfo(k *repo.APIKey, now time.Time) apiKeyInfo {
	return apiKeyInfo{
		ID:          k.ID,
		Name:        k.Name,
		TenantID:    k.TenantID,
		Permissions: k.Permissions,
		ExpiresAt:   k.ExpiresAt.UnixMilli(),
		CreatedAt:   k.CreatedAt.UnixMilli(),
		Expired:     now.After(k.ExpiresAt),
	}
}

// validatePermissions 校验权限点非空且都在合法全集内。
func validatePermissions(perms []string) bool {
	if len(perms) == 0 {
		return false
	}
	valid := make(map[string]bool, len(repo.ValidPermissions))
	for _, p := range repo.ValidPermissions {
		valid[p] = true
	}
	for _, p := range perms {
		if !valid[p] {
			return false
		}
	}
	return true
}

// List 处理 GET /web/apikeys
func (h *APIKeysHandler) List(ctx context.Context, rc *app.RequestContext) {
	list, err := h.keys.List(ctx)
	if err != nil {
		h.log.Error("API Key 列表查询失败: " + err.Error())
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	now := apiKeyNow()
	infos := make([]apiKeyInfo, 0, len(list))
	for _, k := range list {
		infos = append(infos, toAPIKeyInfo(k, now))
	}
	rc.JSON(200, utils.H{"keys": infos, "total": len(infos)})
}

// Create 处理 POST /web/apikeys：校验 → 名称查重 → 写入（主键同秒冲突 +1 秒重试）→ 签发 token
func (h *APIKeysHandler) Create(ctx context.Context, rc *app.RequestContext) {
	var req apiKeyCreateRequest
	if err := rc.BindJSON(&req); err != nil {
		errbody.Write(rc, 400, "请求参数错误", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		errbody.Write(rc, 400, "名称不能为空", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	// 数据库列为 VARCHAR(64)，超长会落到底层驱动错误（500）；按字节数校验与列宽语义一致
	if len(req.Name) > 64 {
		errbody.Write(rc, 400, "名称长度不能超过 64 个字符", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	// Cookie 通道的 caller 固定为 "web"，同名 API Key 会在限流/日志维度与 Web 会话混同
	if req.Name == "web" {
		errbody.Write(rc, 400, "web 为系统保留名称", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	// 租户是会话隔离的边界，由 API Key 记录绑定、不可通过请求头指定；
	// 标识规则与 user 请求头相同（middleware.ValidIdentifier）
	tenant, ok := middleware.ValidIdentifier(req.TenantID)
	if !ok {
		errbody.Write(rc, 400, "租户标识不能为空，且只能包含字母、数字、下划线、短横线，长度不超过 64",
			errbody.TypeInvalidRequest, "", "invalid_tenant")
		return
	}
	expFn, ok := expiresInOptions[req.ExpiresIn]
	if !ok {
		errbody.Write(rc, 400, "过期时间只支持 1d/7d/1mo/6mo/1y/10y",
			errbody.TypeInvalidRequest, "", "invalid_expires_in")
		return
	}
	if !validatePermissions(req.Permissions) {
		errbody.Write(rc, 400, "权限范围为空或包含非法权限点",
			errbody.TypeInvalidRequest, "", "invalid_permissions")
		return
	}

	if _, err := h.keys.GetByName(ctx, req.Name); err == nil {
		errbody.Write(rc, 409, "名称已存在", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	} else if !errors.Is(err, repo.ErrNotFound) {
		h.log.Error("API Key 名称查重失败: " + err.Error())
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	// 主键为秒级时间戳（yyyyMMddHHmmss）：同秒创建冲突时 +1 秒重试，最多 3 次
	now := apiKeyNow()
	var k *repo.APIKey
	var createErr error
	for i := 0; i < 3; i++ {
		created := now.Add(time.Duration(i) * time.Second).Truncate(time.Second)
		k = &repo.APIKey{
			ID:          created.Format("20060102150405"),
			Name:        req.Name,
			TenantID:    tenant,
			Permissions: req.Permissions,
			ExpiresAt:   expFn(created),
			CreatedAt:   created,
		}
		if createErr = h.keys.Create(ctx, k); createErr == nil {
			break
		}
	}
	if createErr != nil {
		// 并发重名兜底：查重与写入非原子，两请求同时通过 GetByName 后，
		// 后写入者会撞名称唯一约束——此时应返回 409 而非 500。
		if _, err := h.keys.GetByName(ctx, req.Name); err == nil {
			errbody.Write(rc, 409, "名称已存在", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
			return
		}
		h.log.Error("API Key 创建失败: " + createErr.Error())
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	token, err := auth.Sign(k, h.secret)
	if err != nil {
		h.log.Error("API Key 签发失败: " + err.Error())
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	info := toAPIKeyInfo(k, apiKeyNow())
	rc.JSON(200, apiKeyCreateResponse{apiKeyInfo: info, Token: token})
}

// Token 处理 GET /web/apikeys/:id/token：按需用 secret + 元数据确定性还原完整 JWT
func (h *APIKeysHandler) Token(ctx context.Context, rc *app.RequestContext) {
	k, err := h.keys.GetByID(ctx, rc.Param("id"))
	if errors.Is(err, repo.ErrNotFound) {
		errbody.Write(rc, 404, "API Key 不存在", errbody.TypeInvalidRequest, "", "not_found")
		return
	}
	if err != nil {
		h.log.Error("API Key 查询失败: " + err.Error())
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	token, err := auth.Sign(k, h.secret)
	if err != nil {
		h.log.Error("API Key 还原失败: " + err.Error())
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	rc.JSON(200, utils.H{"token": token})
}

// Delete 处理 DELETE /web/apikeys/:id：删除即吊销
func (h *APIKeysHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	err := h.keys.DeleteByID(ctx, rc.Param("id"))
	if errors.Is(err, repo.ErrNotFound) {
		errbody.Write(rc, 404, "API Key 不存在", errbody.TypeInvalidRequest, "", "not_found")
		return
	}
	if err != nil {
		h.log.Error("API Key 删除失败: " + err.Error())
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}
