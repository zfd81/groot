// internal/api/handler/auth_setting.go
// 认证配置接口：JWT 签名密钥与 API Key 请求头名的读写。
//
// 两项配置均由认证中间件在启动时读取一次，这里的改动写入配置表后
// 需重启服务才生效；密钥只以脱敏形式进入响应，明文不出接口。
package handler

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/setting"
)

// GetAuthSettings 处理 GET /web/settings/auth。
// 密钥以脱敏形式返回：界面需要知道密钥已设置，但不该拿到原文。
func (h *SettingHandler) GetAuthSettings(ctx context.Context, rc *app.RequestContext) {
	h.respondAuthSettings(ctx, rc)
}

// PutAuthSettings 处理 PUT /web/settings/auth，保存 API Key 请求头名。
// 空串表示恢复默认；非法值整次拒绝，不改表。改动需重启服务才生效。
func (h *SettingHandler) PutAuthSettings(ctx context.Context, rc *app.RequestContext) {
	var req types.PutAuthSettingsRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	if err := h.settings.SetAuthHeaderName(ctx, req.HeaderName); err != nil {
		if errors.Is(err, setting.ErrInvalidSetting) {
			rc.JSON(400, utils.H{"status": "invalid_request", "message": err.Error()})
			return
		}
		h.log.Error("保存认证请求头名失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	h.respondAuthSettings(ctx, rc)
}

// RegenerateAuthSecret 处理 POST /web/settings/auth/secret，重新生成 JWT 签名密钥。
// 无条件替换：界面在调用前已让使用者确认过后果——
// 全部已签发的 API Key 立即失效，且需重启服务才生效。
func (h *SettingHandler) RegenerateAuthSecret(ctx context.Context, rc *app.RequestContext) {
	if _, err := h.settings.RegenerateAuthSecret(ctx); err != nil {
		h.log.Error("重新生成认证密钥失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	h.respondAuthSettings(ctx, rc)
}

// respondAuthSettings 从配置表回读认证配置并以统一结构返回。
// 三个接口的成功响应同构，界面拿到响应即可整体刷新认证分组。
func (h *SettingHandler) respondAuthSettings(ctx context.Context, rc *app.RequestContext) {
	a, err := h.settings.Auth(ctx)
	if err != nil {
		h.log.Error("读取认证配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	rc.JSON(200, types.AuthSettingsPayload{
		HeaderName:        a.HeaderName,
		HeaderNameDefault: setting.DefaultAuthHeaderName,
		SecretMasked:      llm.MaskAPIKey(a.Secret),
		SecretSet:         a.Secret != "",
	})
}
