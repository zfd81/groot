// web/src/api/authSettings.ts
// 认证配置：JWT 签名密钥与 API Key 请求头名，均为重启后生效。
//
// 密钥只以脱敏形式（secret_masked）进入前端，明文不出服务端；
// 三个接口的成功响应同构，界面拿到响应即可整体刷新认证分组。
import { api } from './client'

export interface AuthSettings {
  header_name: string
  header_name_default: string
  secret_masked: string
  secret_set: boolean
}

export function fetchAuthSettings() {
  return api.get<AuthSettings>('/web/settings/auth')
}

// saveAuthHeaderName 保存 API Key 请求头名。空串表示恢复默认；
// 非法值服务端返回 400，消息即校验说明，界面直接展示。
export function saveAuthHeaderName(name: string) {
  return api.put<AuthSettings>('/web/settings/auth', { header_name: name })
}

// regenerateAuthSecret 重新生成签名密钥。调用前界面须让使用者确认后果：
// 全部已签发的 API Key 立即失效，且需重启服务才生效。
export function regenerateAuthSecret() {
  return api.post<AuthSettings>('/web/settings/auth/secret')
}
