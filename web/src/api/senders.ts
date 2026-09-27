// web/src/api/senders.ts
// 消息发送器配置：webhook 与 email 两个渠道的参数，保存即生效。
//
// 密码字段的读写方向不同：服务端回读的是脱敏值（形如 ****cret），
// 提交时空串表示「保持不变」。界面上的可编辑副本要先把密码清空，
// 见 editableSenders。
import { api } from './client'

export type SenderName = 'webhook' | 'email'

export interface SenderSettings {
  enabled: boolean
  url: string // webhook 专用
  smtp_host: string // 以下为 email 专用
  smtp_port: number
  username: string
  password: string
  from: string
}

export interface SendersSettings {
  senders: Record<SenderName, SenderSettings>
}

// 取值边界，与 internal/setting/message.go 的 MinSMTPPort / MaxSMTPPort 一致
export const senderLimits = {
  smtpPort: { min: 1, max: 65535 },
} as const

function emptySender(): SenderSettings {
  return {
    enabled: false,
    url: '',
    smtp_host: '',
    smtp_port: 587,
    username: '',
    password: '',
    from: '',
  }
}

// defaultSendersSettings 仅用于首屏占位，真实值由接口返回后覆盖。
export function defaultSendersSettings(): SendersSettings {
  return { senders: { webhook: emptySender(), email: emptySender() } }
}

// cloneSendersSettings 按字段深拷贝。不用 structuredClone：
// store 里的值是 Vue 响应式 Proxy，结构化克隆会抛 DataCloneError。
export function cloneSendersSettings(s: SendersSettings): SendersSettings {
  const copy = (x: SenderSettings): SenderSettings => ({
    enabled: x.enabled,
    url: x.url,
    smtp_host: x.smtp_host,
    smtp_port: x.smtp_port,
    username: x.username,
    password: x.password,
    from: x.from,
  })
  return { senders: { webhook: copy(s.senders.webhook), email: copy(s.senders.email) } }
}

// editableSenders 生成给界面编辑的副本：密码清空。
// 服务端回读的密码是脱敏串，原样提交会被存成真密码；
// 清空后不改密码直接保存即提交空串，服务端据此保留原值。
export function editableSenders(s: SendersSettings): SendersSettings {
  const out = cloneSendersSettings(s)
  out.senders.webhook.password = ''
  out.senders.email.password = ''
  return out
}

export const sendersApi = {
  getSettings: () => api.get<SendersSettings>('/web/settings/senders'),

  // 保存成功后服务端回传生效值（密码已脱敏），直接用它覆盖本地状态
  saveSettings: (s: SendersSettings) => api.put<SendersSettings>('/web/settings/senders', s),
}
