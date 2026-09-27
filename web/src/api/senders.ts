// web/src/api/senders.ts
// 消息发送器配置：webhook 渠道的参数，保存即生效。
import { api } from './client'

export type SenderName = 'webhook'

export interface SenderSettings {
  enabled: boolean
  url: string
}

export interface SendersSettings {
  senders: Record<SenderName, SenderSettings>
}

function emptySender(): SenderSettings {
  return {
    enabled: false,
    url: '',
  }
}

// defaultSendersSettings 仅用于首屏占位，真实值由接口返回后覆盖。
export function defaultSendersSettings(): SendersSettings {
  return { senders: { webhook: emptySender() } }
}

// cloneSendersSettings 按字段深拷贝。不用 structuredClone：
// store 里的值是 Vue 响应式 Proxy，结构化克隆会抛 DataCloneError。
export function cloneSendersSettings(s: SendersSettings): SendersSettings {
  const copy = (x: SenderSettings): SenderSettings => ({
    enabled: x.enabled,
    url: x.url,
  })
  return { senders: { webhook: copy(s.senders.webhook) } }
}

// editableSenders 生成给界面编辑的副本。
export function editableSenders(s: SendersSettings): SendersSettings {
  return cloneSendersSettings(s)
}

export const sendersApi = {
  getSettings: () => api.get<SendersSettings>('/web/settings/senders'),

  // 保存成功后服务端回传生效值，直接用它覆盖本地状态
  saveSettings: (s: SendersSettings) => api.put<SendersSettings>('/web/settings/senders', s),
}
