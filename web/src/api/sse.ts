// SSE 流式对话解析：fetch + ReadableStream 逐行解析（EventSource 不支持 POST）。
// 事件由 JSON 字段区分类型，与后端 internal/agent/sse.go 输出一致。

import { notifyUnauthorized } from './client'
import i18n from '../i18n'

const t = i18n.global.t

export interface SseToolCall {
  index?: number
  id: string
  type: string
  function: { name: string; arguments: string }
}

// 解析后的单条 SSE 事件（对齐后端 payload 字段）。
export interface SseEvent {
  role?: string
  content?: string
  reasoning_content?: string
  tool_calls?: SseToolCall[]
  tool_call_id?: string
  tool_name?: string
  finish_reason?: string
  event?: string
  message?: string
  error?: boolean
  agent_name?: string
}

export interface ChatAttachment {
  type: string
  name: string
  content: string // base64
}

export interface ChatStreamOptions {
  instruction: string
  sessionId?: string
  modelName?: string
  agentName?: string
  attachments?: ChatAttachment[]
  signal?: AbortSignal
  onEvent: (ev: SseEvent) => void
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

// 发起流式对话。逐行读取 `data: {json}` 与 `data: [DONE]`，
// 从响应头读取 X-Session-ID / X-Chat-ID。
export async function streamChat(opts: ChatStreamOptions): Promise<void> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (opts.sessionId) headers['X-Session-ID'] = opts.sessionId
  if (opts.modelName) headers['X-Model-Name'] = opts.modelName
  if (opts.agentName) headers['X-Agent-Name'] = opts.agentName

  const resp = await fetch('/chat', {
    method: 'POST',
    headers,
    credentials: 'same-origin',
    signal: opts.signal,
    body: JSON.stringify({
      instruction: opts.instruction,
      attachments: opts.attachments,
    }),
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
    try {
      const data = await resp.json()
      if (data && (data.message || data.status)) {
        message = data.message || message
      }
    } catch {
      // 忽略解析失败，用默认消息
    }
    throw new ChatStreamError(resp.status, message)
  }

  const sid = resp.headers.get('X-Session-ID') || opts.sessionId || ''
  const cid = resp.headers.get('X-Chat-ID') || ''
  if (opts.onHeaders) opts.onHeaders(sid, cid)

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

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
      handleChunk(chunk, opts.onEvent)
    }
  }
  // 处理结尾残留（无尾随空行的情况）
  if (buffer.trim()) handleChunk(buffer, opts.onEvent)
}

function handleChunk(chunk: string, onEvent: (ev: SseEvent) => void) {
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
      const ev = JSON.parse(payload) as SseEvent
      onEvent(ev)
    } catch {
      // 忽略无法解析的行
    }
  }
}
