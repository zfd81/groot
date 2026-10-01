// web/src/api/voice.ts
// 语音相关接口：音频转录与语音配置读写。
// 转录是 multipart 请求，不能走 api.post（它按 JSON 序列化 body），
// 因此直接用 fetch，并复用 client 的 401 处理与错误结构。
import { ApiError, api, notifyUnauthorized } from './client'
import i18n from '../i18n'

const t = i18n.global.t

export interface VoiceSettings {
  // Web 界面语音输入使用的识别模型，空串表示不启用语音输入
  model: string
  auto_send: boolean
}

export interface TranscriptionResult {
  text: string
  model: string
}

export const voiceApi = {
  // transcribe 上传音频并返回识别文本。识别模型经请求头 X-Model-Name 传递，
  // 由设置中选定的模型决定，不依赖服务端的默认语音模型。
  async transcribe(blob: Blob, filename: string, model: string): Promise<TranscriptionResult> {
    const fd = new FormData()
    fd.append('file', blob, filename)

    const resp = await fetch('/web/audio/transcriptions', {
      method: 'POST',
      body: fd,
      headers: { 'X-Model-Name': model },
      credentials: 'same-origin',
    })
    if (resp.status === 401) {
      notifyUnauthorized()
      throw new ApiError(401, t('error.unauthorized'))
    }
    const data = await resp.json().catch(() => null)
    if (!resp.ok) {
      const message =
        data?.message || data?.error || t('error.requestFailed', { status: resp.status })
      throw new ApiError(resp.status, message, data?.status ?? undefined)
    }
    // 2xx 但响应体不是 JSON：按请求失败处理，不把 null 交给调用方
    if (!data) throw new ApiError(resp.status, t('error.requestFailed', { status: resp.status }))
    return data as TranscriptionResult
  },

  getSettings: () => api.get<VoiceSettings>('/web/settings/voice'),

  saveSettings: (s: VoiceSettings) => api.put<{ status: string }>('/web/settings/voice', s),
}
