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
}

// 从错误信封 {"error":{"message","type","param","code"}} 取消息与 code。
function envelopeError(data: any, status: number): ApiError {
  const err = data && typeof data === 'object' ? data.error : null
  const message =
    (err && typeof err.message === 'string' && err.message) ||
    t('error.requestFailed', { status })
  const code =
    err && typeof err === 'object' ? (err.code ?? err.type ?? undefined) : undefined
  return new ApiError(status, message, code)
}

export const voiceApi = {
  // transcribe 上传音频并返回识别文本。识别模型走 multipart 的 model 字段，
  // 由设置中选定的模型决定，不依赖服务端的默认语音模型；
  // response_format 固定 json，界面按 {"text":"..."} 解析。
  async transcribe(blob: Blob, filename: string, model: string): Promise<TranscriptionResult> {
    const fd = new FormData()
    fd.append('file', blob, filename)
    fd.append('model', model)
    fd.append('response_format', 'json')

    const resp = await fetch('/web/audio/transcriptions', {
      method: 'POST',
      body: fd,
      credentials: 'same-origin',
    })
    if (resp.status === 401) {
      notifyUnauthorized()
      throw new ApiError(401, t('error.unauthorized'))
    }
    const data = await resp.json().catch(() => null)
    if (!resp.ok) throw envelopeError(data, resp.status)
    // 2xx 但响应体不是 JSON：按请求失败处理，不把 null 交给调用方
    if (!data) throw new ApiError(resp.status, t('error.requestFailed', { status: resp.status }))
    return data as TranscriptionResult
  },

  getSettings: () => api.get<VoiceSettings>('/web/settings/voice'),

  saveSettings: (s: VoiceSettings) => api.put<{ status: string }>('/web/settings/voice', s),
}
