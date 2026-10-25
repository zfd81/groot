// web/src/api/speech.ts
// 语音朗读相关接口：文本合成语音与朗读参数配置读写。
// 合成接口的响应是音频二进制，不能走 api.post（它按 JSON 解析响应），
// 因此直接用 fetch，并复用 client 的 401 处理与错误结构。
import { ApiError, api, notifyUnauthorized } from './client'
import i18n from '../i18n'

const t = i18n.global.t

// 两个字段均为空串时不放进合成请求体，由上游模型取其默认行为。
export interface SpeechSettings {
  // 音色
  voice: string
  // 风格指令（OpenAI 规范字段 instructions）
  instructions: string
}

export const speechApi = {
  // synthesize 把文本合成为 mp3 音频。params 中非空的字段才放进请求体，
  // 空字段由上游决定默认行为；signal 用于用户中途停止朗读时取消请求。
  async synthesize(input: string, params: SpeechSettings, signal?: AbortSignal): Promise<Blob> {
    const body: Record<string, string> = { input, response_format: 'mp3' }
    if (params.voice) body.voice = params.voice
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
      const message =
        data?.message || data?.error || t('error.requestFailed', { status: resp.status })
      throw new ApiError(resp.status, message, data?.status ?? undefined)
    }
    return resp.blob()
  },

  getSettings: () => api.get<SpeechSettings>('/web/settings/speech'),

  saveSettings: (s: SpeechSettings) => api.put<{ status: string }>('/web/settings/speech', s),
}
