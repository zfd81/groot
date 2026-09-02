package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

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
	ExpiresIn   string   `json:"expires_in"`
	Permissions []string `json:"permissions"`
}

type apiKeyInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
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
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
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
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "名称不能为空"})
		return
	}
	// 数据库列为 VARCHAR(64)，超长会落到底层驱动错误（500）；按字节数校验与列宽语义一致
	if len(req.Name) > 64 {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "名称长度不能超过 64 个字符"})
		return
	}
	// Cookie 通道的 caller 固定为 "web"，同名 API Key 会在限流/日志维度与 Web 会话混同
	if req.Name == "web" {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "web 为系统保留名称"})
		return
	}
	expFn, ok := expiresInOptions[req.ExpiresIn]
	if !ok {
		rc.JSON(400, utils.H{"status": "invalid_expires_in", "message": "过期时间只支持 1d/7d/1mo/6mo/1y/10y"})
		return
	}
	if !validatePermissions(req.Permissions) {
		rc.JSON(400, utils.H{"status": "invalid_permissions", "message": "权限范围为空或包含非法权限点"})
		return
	}

	if _, err := h.keys.GetByName(ctx, req.Name); err == nil {
		rc.JSON(409, utils.H{"status": "name_exists", "message": "名称已存在"})
		return
	} else if !errors.Is(err, repo.ErrNotFound) {
		h.log.Error("API Key 名称查重失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
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
			rc.JSON(409, utils.H{"status": "name_exists", "message": "名称已存在"})
			return
		}
		h.log.Error("API Key 创建失败: " + createErr.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}

	token, err := auth.Sign(k, h.secret)
	if err != nil {
		h.log.Error("API Key 签发失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	info := toAPIKeyInfo(k, apiKeyNow())
	rc.JSON(200, apiKeyCreateResponse{apiKeyInfo: info, Token: token})
}

// Token 处理 GET /web/apikeys/:id/token：按需用 secret + 元数据确定性还原完整 JWT
func (h *APIKeysHandler) Token(ctx context.Context, rc *app.RequestContext) {
	k, err := h.keys.GetByID(ctx, rc.Param("id"))
	if errors.Is(err, repo.ErrNotFound) {
		rc.JSON(404, utils.H{"status": "not_found", "message": "API Key 不存在"})
		return
	}
	if err != nil {
		h.log.Error("API Key 查询失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	token, err := auth.Sign(k, h.secret)
	if err != nil {
		h.log.Error("API Key 还原失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	rc.JSON(200, utils.H{"token": token})
}

// Delete 处理 DELETE /web/apikeys/:id：删除即吊销
func (h *APIKeysHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	err := h.keys.DeleteByID(ctx, rc.Param("id"))
	if errors.Is(err, repo.ErrNotFound) {
		rc.JSON(404, utils.H{"status": "not_found", "message": "API Key 不存在"})
		return
	}
	if err != nil {
		h.log.Error("API Key 删除失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}
