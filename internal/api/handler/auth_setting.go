// internal/api/handler/auth_setting.go
// 认证配置接口：JWT 签名密钥的读取与重新生成。
//
// 密钥由认证中间件在启动时读取一次，这里的改动写入配置表后
// 需重启服务才生效；密钥只以脱敏形式进入响应，明文不出接口。
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/llm"
)

// GetAuthSettings 处理 GET /web/settings/auth。
// 密钥以脱敏形式返回：界面需要知道密钥已设置，但不该拿到原文。
func (h *SettingHandler) GetAuthSettings(ctx context.Context, rc *app.RequestContext) {
	h.respondAuthSettings(ctx, rc)
}

// RegenerateAuthSecret 处理 POST /web/settings/auth/secret，重新生成 JWT 签名密钥。
// 无条件替换：界面在调用前已让使用者确认过后果——
// 全部已签发的 API Key 立即失效，且需重启服务才生效。
func (h *SettingHandler) RegenerateAuthSecret(ctx context.Context, rc *app.RequestContext) {
	if _, err := h.settings.RegenerateAuthSecret(ctx); err != nil {
		h.log.Error("重新生成认证密钥失败", zap.Error(err))
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	h.respondAuthSettings(ctx, rc)
}

// respondAuthSettings 从配置表回读认证配置并以统一结构返回。
// 两个接口的成功响应同构，界面拿到响应即可整体刷新认证分组。
func (h *SettingHandler) respondAuthSettings(ctx context.Context, rc *app.RequestContext) {
	a, err := h.settings.Auth(ctx)
	if err != nil {
		h.log.Error("读取认证配置失败", zap.Error(err))
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	rc.JSON(200, types.AuthSettingsPayload{
		SecretMasked: llm.MaskAPIKey(a.Secret),
		SecretSet:    a.Secret != "",
	})
}
