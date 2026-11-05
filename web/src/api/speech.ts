// web/src/api/speech.ts
// 语音朗读相关接口：文本合成语音与朗读参数配置读写。
// 合成接口的响应是音频二进制，不能走 api.post（它按 JSON 解析响应），
// 因此直接用 fetch，并复用 client 的 401 处理与错误结构。
import { ApiError, api, notifyUnauthorized } from './client'
import i18n from '../i18n'

const t = i18n.global.t

// voice 是合成接口的必填项；instructions 为空时不放进请求体，
// 由上游模型取其默认行为。speed 暂不设界面控件，故不下发。
export interface SpeechSettings {
  // 音色
  voice: string
  // 风格指令（OpenAI 规范字段 instructions）
  instructions: string
}

export const speechApi = {
  // synthesize 把文本合成为 mp3 音频。voice 为空时不发请求，直接以错误结束，
  // 让调用方（朗读队列）走统一失败提示；instructions 非空才放进请求体；
  // signal 用于用户中途停止朗读时取消请求。
  async synthesize(input: string, params: SpeechSettings, signal?: AbortSignal): Promise<Blob> {
    const voice = (params.voice || '').trim()
    if (!voice) {
      // code 供调用方识别「未配置音色」，提示文案由调用方决定
      throw new ApiError(400, t('settings.speechVoiceRequired'), 'voice_required')
    }
    const body: Record<string, string> = { input, response_format: 'mp3', voice }
    if (params.instructions) body.instructions = params.instructions

    const resp = await fetch('/web/audio/speech', {
      method: 'POST',
      body: JSON.stringify(body),
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      signal,
    })
    if (resp.status === 401) {
      notifyUnauthorized()
      throw new ApiError(401, t('error.unauthorized'))
    }
    if (!resp.ok) {
      const data = await resp.json().catch(() => null)
      const err = data && typeof data === 'object' ? data.error : null
      const message =
        (err && typeof err.message === 'string' && err.message) ||
        t('error.requestFailed', { status: resp.status })
      const code =
        err && typeof err === 'object' ? (err.code ?? err.type ?? undefined) : undefined
      throw new ApiError(resp.status, message, code)
    }
    return resp.blob()
  },

  getSettings: () => api.get<SpeechSettings>('/web/settings/speech'),

  saveSettings: (s: SpeechSettings) => api.put<{ status: string }>('/web/settings/speech', s),
}
