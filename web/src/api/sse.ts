// SSE 流式对话解析：fetch + ReadableStream 逐行解析（EventSource 不支持 POST）。
// 后端输出 OpenAI `chat.completion.chunk` 格式（见
// docs/superpowers/specs/2026-11-02-openai-compatible-rest-api-design.md），
// 本层通过 flattenChunk 将其摊平为 SseEvent（= choices[0].delta + finish_reason）后交给调用方。
// 会话标识不再来自响应头：`session_id` 在每个 chunk 顶层，`chat_id` 即 chunk 顶层 `id`。

import { notifyUnauthorized } from './client'
import i18n from '../i18n'
import { flattenChunk, type SseEvent, type SseToolCall } from './sseChunk'
import type { ChatContentPart } from './types'

export type { SseEvent, SseToolCall }
export { flattenChunk }

const t = i18n.global.t

// 前端附件：content 为纯 Base64（不带 data: 前缀），mime 为浏览器给出的文件类型。
export interface ChatAttachment {
  type: string
  name: string
  content: string // base64
  mime?: string
}

export interface ChatStreamOptions {
  instruction: string
  sessionId?: string
  // 模型逻辑名，对应请求体 model（空时服务端取默认对话模型）
  modelName?: string
  // 子 Agent 名，对应请求体扩展字段 agent_name（'groot' 等价于不指定）
  agentName?: string
  attachments?: ChatAttachment[]
  signal?: AbortSignal
  onEvent: (ev: SseEvent) => void
  // 首个 chunk 到达时回调一次会话标识（参数名沿用历史命名）。
  // sessionId 取自 chunk 顶层 session_id，chatId 取自 chunk 顶层 id；两者都缺失时回调空串。
  onHeaders?: (sessionId: string, chatId: string) => void
}

export class ChatStreamError extends Error {
  status: number
  code?: string
  constructor(status: number, message: string, code?: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

// 请求体 content 的内容片段：文本、图片（data URL）、文本类文件（data URL）。
type ContentPart = ChatContentPart

// 附件转内容片段：图片走 image_url（data URL），其余文本类文件走 file 片段。
// MIME 缺失时按类型给保守默认值，服务端仍会按 data URL 的 MIME 做校验。
function attachmentToPart(att: ChatAttachment): ContentPart | null {
  if (att.type === 'image') {
    const mime = att.mime || 'image/png'
    return {
      type: 'image_url',
      image_url: { url: `data:${mime};base64,${att.content}` },
    }
  }
  if (att.type === 'file') {
    const mime = att.mime || 'text/plain'
    return {
      type: 'file',
      file: {
        filename: att.name,
        file_data: `data:${mime};base64,${att.content}`,
      },
    }
  }
  return null
}

// 组装 POST /chat/completions 的请求体：末条 user 的文本片段在前，附件片段按原顺序在后。
function buildRequestBody(opts: ChatStreamOptions): Record<string, unknown> {
  const parts: ContentPart[] = [{ type: 'text', text: opts.instruction }]
  for (const att of opts.attachments || []) {
    const part = attachmentToPart(att)
    if (part) parts.push(part)
  }

  const body: Record<string, unknown> = {
    model: opts.modelName || '',
    messages: [{ role: 'user', content: parts }],
    stream: true,
    stream_options: { include_usage: true },
  }
  if (opts.sessionId) body.session_id = opts.sessionId
  if (opts.agentName) body.agent_name = opts.agentName
  return body
}

// 发起流式对话。逐行读取 `data: {json}` 与 `data: [DONE]`，
// 会话标识取自首个 chunk 的顶层 session_id 与 id。
export async function streamChat(opts: ChatStreamOptions): Promise<void> {
  const resp = await fetch('/chat/completions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    signal: opts.signal,
    body: JSON.stringify(buildRequestBody(opts)),
  })

  if (resp.status === 401) {
    // 会话中途过期：与普通 REST 请求一致地触发跳登录页
    notifyUnauthorized()
    throw new ChatStreamError(401, t('error.unauthorized'))
  }
  if (resp.status === 409) {
    throw new ChatStreamError(
      409,
      t('error.sessionBusy'),
      'chat_limit_exceeded'
    )
  }
  if (!resp.ok || !resp.body) {
    let message = t('error.requestFailed', { status: resp.status })
    let code: string | undefined
    try {
      const data = await resp.json()
      const err = data && data.error
      if (err && typeof err === 'object') {
        if (typeof err.message === 'string' && err.message) message = err.message
        if (typeof err.code === 'string' && err.code) code = err.code
        else if (typeof err.type === 'string' && err.type) code = err.type
      }
    } catch {
      // 忽略解析失败，用默认消息
    }
    throw new ChatStreamError(resp.status, message, code)
  }

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  // 会话标识只回调一次：首个 chunk 即携带顶层 session_id 与 id。
  let notified = false
  const notifySession = (ev: SseEvent | null) => {
    if (notified || !opts.onHeaders) return
    if (!ev || (!ev.session_id && !ev.chat_id)) return
    notified = true
    opts.onHeaders(ev.session_id || '', ev.chat_id || '')
  }

  // eslint-disable-next-line no-constant-condition
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    // 归一化 CRLF，兼容会重写换行的中间代理（后端本身输出 \n\n）
    buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, '\n')

    let idx: number
    // SSE 事件以空行分隔；逐个提取完整事件块。
    while ((idx = buffer.indexOf('\n\n')) !== -1) {
      const chunk = buffer.slice(0, idx)
      buffer = buffer.slice(idx + 2)
      handleChunk(chunk, opts.onEvent, notifySession)
    }
  }
  // 处理结尾残留（无尾随空行的情况）
  if (buffer.trim()) handleChunk(buffer, opts.onEvent, notifySession)
}

function handleChunk(
  chunk: string,
  onEvent: (ev: SseEvent) => void,
  notifySession: (ev: SseEvent | null) => void
) {
  for (const line of chunk.split('\n')) {
    const trimmed = line.trimStart()
    if (!trimmed.startsWith('data:')) continue
    const payload = trimmed.slice(5).trim()
    if (!payload) continue
    if (payload === '[DONE]') {
      onEvent({ finish_reason: 'stop', event: '__done__' })
      continue
    }
    try {
      // 摊平 OpenAI chunk；空 choices 仅在带顶层 usage 时产出 usage 事件，其余无法识别的 chunk 直接忽略
      const ev = flattenChunk(JSON.parse(payload))
      if (ev) {
        notifySession(ev)
        onEvent(ev)
      }
    } catch {
      // 忽略无法解析的行
    }
  }
}
