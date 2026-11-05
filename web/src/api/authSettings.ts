// web/src/api/authSettings.ts
// 认证配置：只含 JWT 签名密钥的展示与重新生成，改动重启后生效。
//
// 密钥只以脱敏形式（secret_masked）进入前端，明文不出服务端；
// 两个接口的成功响应同构，界面拿到响应即可整体刷新认证分组。
import { api } from './client'

export interface AuthSettings {
  secret_masked: string
  secret_set: boolean
}

export function fetchAuthSettings() {
  return api.get<AuthSettings>('/web/settings/auth')
}

// regenerateAuthSecret 重新生成签名密钥。调用前界面须让使用者确认后果：
// 全部已签发的 API Key 立即失效，且需重启服务才生效。
export function regenerateAuthSecret() {
  return api.post<AuthSettings>('/web/settings/auth/secret')
}
