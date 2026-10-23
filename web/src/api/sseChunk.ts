// SSE chunk 摊平：把后端输出的 OpenAI `chat.completion.chunk` 转为前端消费的平铺 SseEvent。
// 本模块不得引入任何有副作用的依赖（便于独立类型检查与 Node 直接验证）。
// 格式见 docs/superpowers/specs/2026-10-20-openai-sse-format-design.md。

export interface SseToolCall {
  index?: number
  id: string
  type: string
  function: { name: string; arguments: string }
}

// 解析后的单条 SSE 事件：即 `choices[0].delta` 摊平后再合并顶层 `finish_reason`。
// 错误 chunk（`{"error":{...}}`）被映射为 `{ event: 'error', message, agent_name? }`。
// 用量 chunk（`choices` 为 []、顶层 `usage` 为对象）被映射为 `{ event: 'usage', usage }`。
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
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number }
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null
}

// 将一条已 JSON.parse 的 chunk 摊平为 SseEvent：
// - `error` 为对象 → `{ event: 'error', message, agent_name? }`
// - `choices[0]` 存在 → `{ ...delta, finish_reason? }`
// - `choices` 为空数组且 `usage` 为对象 → `{ event: 'usage', usage }`
// - 其他（含非对象输入）→ null，调用方应忽略
export function flattenChunk(raw: unknown): SseEvent | null {
  if (!isRecord(raw)) return null

  const err = raw.error
  if (isRecord(err)) {
    const ev: SseEvent = {
      event: 'error',
      message: typeof err.message === 'string' ? err.message : '',
    }
    if (typeof err.agent_name === 'string') ev.agent_name = err.agent_name
    return ev
  }

  const choices = raw.choices
  if (!Array.isArray(choices)) return null
  if (choices.length === 0) {
    const usage = raw.usage
    if (isRecord(usage)) {
      return { event: 'usage', usage: usage as SseEvent['usage'] }
    }
    return null
  }
  const first: unknown = choices[0]
  if (!isRecord(first)) return null

  const delta = isRecord(first.delta) ? (first.delta as SseEvent) : {}
  const finish = first.finish_reason
  return {
    ...delta,
    ...(finish != null ? { finish_reason: String(finish) } : {}),
  }
}
