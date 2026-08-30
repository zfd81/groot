import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api } from '../api/client'
import { streamChat, ChatStreamError, type SseEvent, type ChatAttachment } from '../api/sse'
import i18n from '../i18n'
import type {
  SessionSummary,
  SessionHistoryResp,
  SessionDetailResp,
  ChatRecord,
} from '../api/types'

// 一次工具调用（含结果回填）。
export interface ToolInvocation {
  id: string
  name: string
  arguments: string
  // OpenAI 流式协议的 tool_call index，用于在缺少 id 时区分并发的多次调用。
  toolIndex?: number
  result?: string
  isError?: boolean
  agentName?: string
  // 计时：调用开始时间戳（ms）与结果回填后计算出的耗时（ms）。
  startedAt?: number
  durationMs?: number
}

// 转录流中的一步：思考片段或一次工具调用。
// 按到达顺序记录，使思考与工具调用能像终端转录那样交错呈现。
// 思考步同样记录起止时间：startedAt 在新建时打点，durationMs 在该步结束
//（有后续步骤开始、正文开始输出或整条消息流结束）时计算。
export type ChatStep =
  | { kind: 'reasoning'; text: string; startedAt?: number; durationMs?: number }
  | { kind: 'tool'; tool: ToolInvocation }

// 一条消息：用户输入或一次助手回复。
export interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  reasoning: string
  tools: ToolInvocation[]
  // 有序时间线：流式展示思考/工具用它，历史消息为空。
  steps: ChatStep[]
  streaming: boolean
  error?: string
}

const PAGE_SIZE = 20

// 单调时钟：优先用 performance.now()，退回 Date.now()，用于步骤计时。
function now(): number {
  return typeof performance !== 'undefined' && performance.now
    ? performance.now()
    : Date.now()
}

// 步骤计时的「上一步结束时刻」。每个步骤的耗时从这里算起，而非从它第一个数据
// chunk 到达时算起——这样「等待 LLM 首个 token」等步骤间的间隙会被正确归入下一步，
// 使各子步骤耗时之和与父级「调用 Agent」时长一致，而不是被严重低估。
// send 是串行的（sending 锁），单条流独占，用模块级变量即可。
let stepClockAt = 0

// 结算末尾未完成的思考步：正文开始、工具调用开始或出错时调用，
// 让思考步也能显示自己的耗时，并把时钟推进到当前时刻。
function finalizeReasoning(msg: ChatMessage) {
  const last = msg.steps[msg.steps.length - 1]
  if (
    last &&
    last.kind === 'reasoning' &&
    last.startedAt !== undefined &&
    last.durationMs === undefined
  ) {
    last.durationMs = now() - last.startedAt
    stepClockAt = now()
  }
}

// 在当前助手消息里查找一次工具调用增量归属的已存在调用（对齐 TUI 的匹配顺序）：
// 1) 按 id 精确匹配（最可靠）；有 id 但没找到，说明是新调用，直接返回，
//    不再退回 index/名称匹配，避免误并入 index 相同的另一次调用。
// 2) 按 index 匹配（需名称一致或增量未带名称），区分共享 index 的不同调用。
// 3) 兜底按名称匹配最后一次同名调用。
function findToolDelta(
  msg: ChatMessage,
  id: string,
  name: string | undefined,
  index: number | undefined
): ToolInvocation | undefined {
  if (id) {
    return msg.tools.find((t) => t.id === id)
  }
  if (index !== undefined) {
    for (let i = msg.tools.length - 1; i >= 0; i--) {
      const t = msg.tools[i]
      if (t.toolIndex === index && (!name || t.name === name)) return t
    }
  }
  if (name) {
    for (let i = msg.tools.length - 1; i >= 0; i--) {
      if (msg.tools[i].name === name) return msg.tools[i]
    }
  }
  return undefined
}

export const useChatStore = defineStore('chat', () => {
  const sessionId = ref('')
  const messages = ref<ChatMessage[]>([])
  const sending = ref(false)

  const sessions = ref<SessionSummary[]>([])
  const sessionsTotal = ref(0)
  const sessionsOffset = ref(0)
  const loadingSessions = ref(false)

  // 统计条数据（来自最近一次对话详情）
  const lastRecord = ref<ChatRecord | null>(null)

  let abortCtrl: AbortController | null = null

  const canLoadMore = computed(() => sessions.value.length < sessionsTotal.value)

  function newSession() {
    stop()
    sessionId.value = ''
    messages.value = []
    lastRecord.value = null
  }

  // 加载会话列表（分页；reset=true 从头加载）。
  async function loadSessions(reset = true) {
    if (loadingSessions.value) return
    loadingSessions.value = true
    try {
      if (reset) {
        sessionsOffset.value = 0
        sessions.value = []
      }
      const resp = await api.get<SessionHistoryResp>(
        `/sess/history?limit=${PAGE_SIZE}&offset=${sessionsOffset.value}`
      )
      sessions.value.push(...(resp.sessions || []))
      sessionsTotal.value = resp.total
      sessionsOffset.value += resp.sessions?.length || 0
    } finally {
      loadingSessions.value = false
    }
  }

  // 打开历史会话，加载其消息记录。
  async function openSession(sid: string) {
    stop()
    sessionId.value = sid
    messages.value = []
    lastRecord.value = null
    const detail = await api.get<SessionDetailResp>(`/sess/${sid}`)
    const msgs = detail.history?.messages || []
    for (const m of msgs) {
      messages.value.push({
        role: 'user',
        content: m.instruction,
        reasoning: '',
        tools: [],
        steps: [],
        streaming: false,
      })
      messages.value.push({
        role: 'assistant',
        content: m.result,
        reasoning: '',
        tools: [],
        steps: [],
        streaming: false,
        error: m.error?.message || undefined,
      })
    }
  }

  // 发送消息，流式接收回复。
  async function send(
    instruction: string,
    modelName?: string,
    agentName?: string,
    attachments?: ChatAttachment[]
  ) {
    if (sending.value || !instruction.trim()) return

    messages.value.push({
      role: 'user',
      content: instruction,
      reasoning: '',
      tools: [],
      steps: [],
      streaming: false,
    })
    const assistant: ChatMessage = {
      role: 'assistant',
      content: '',
      reasoning: '',
      tools: [],
      steps: [],
      streaming: true,
    }
    messages.value.push(assistant)
    // Vue 3 响应式：push 进 reactive 数组后，视图渲染的是数组元素的代理，
    // 而闭包里的 assistant 仍是原始对象。直接改原始对象不经过代理 setter，
    // 不会触发视图更新。必须取回数组中的代理元素，后续流式修改才能刷新界面。
    const live = messages.value[messages.value.length - 1]

    sending.value = true
    abortCtrl = new AbortController()
    // 步骤计时基准：从本次发送时刻开始，首个步骤的耗时含「等待 LLM 首个 token」的间隙。
    stepClockAt = now()

    try {
      await streamChat({
        instruction,
        sessionId: sessionId.value || undefined,
        modelName,
        agentName,
        attachments,
        signal: abortCtrl.signal,
        onHeaders: (sid) => {
          if (sid) sessionId.value = sid
        },
        onEvent: (ev) => applyEvent(live, ev),
      })
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') {
        live.content += '\n\n' + i18n.global.t('chat.stopped')
      } else if (e instanceof ChatStreamError) {
        live.error = e.message
      } else {
        live.error = e instanceof Error ? e.message : i18n.global.t('error.connectionLost')
      }
    } finally {
      finalizeReasoning(live)
      live.streaming = false
      sending.value = false
      abortCtrl = null
      // 刷新统计与会话列表
      void fetchStats()
      void loadSessions(true)
    }
  }

  // 应用单条 SSE 事件到当前助手消息。
  function applyEvent(msg: ChatMessage, ev: SseEvent) {
    // 注意：不再用 ev.agent_name 覆盖 msg.agentName。顶部徽章只表示用户本次选定的
    // Agent（在 send 时确定）；编排过程中委派的子 Agent 由内联「调用 Agent」步骤呈现，
    // 否则子 Agent 的流式 chunk 会把顶部徽章改成子 Agent，且徽章渲染在时间线顶端，
    // 看起来像「调用发生在思考之前」，与真实顺序矛盾。

    if (ev.event === 'error') {
      finalizeReasoning(msg)
      msg.error = ev.message || i18n.global.t('error.generic')
      return
    }
    if (ev.reasoning_content) {
      msg.reasoning += ev.reasoning_content
      // 连续的思考片段合并到同一步；工具调用之后的思考另起一步，
      // 使转录流能像终端那样按到达顺序交错呈现 Think / 工具行。
      const last = msg.steps[msg.steps.length - 1]
      if (last && last.kind === 'reasoning') {
        last.text += ev.reasoning_content
      } else {
        msg.steps.push({
          kind: 'reasoning',
          text: ev.reasoning_content,
          startedAt: stepClockAt,
        })
      }
      return
    }
    if (ev.tool_calls && ev.tool_calls.length > 0) {
      // OpenAI 流式协议里一次工具调用会拆成多个增量事件（按 index/id 累加 arguments），
      // 因此每个事件要先尝试匹配已存在的调用做累加，匹配不到才新建一条，
      // 保证「一次工具调用 = 一条记录」，与 TUI 的 UpdateToolCall 行为一致。
      for (const tc of ev.tool_calls) {
        const existing = findToolDelta(msg, tc.id, tc.function?.name, tc.index)
        if (existing) {
          existing.arguments += tc.function?.arguments || ''
        } else {
          // 首个增量到达即视为该步开始：立刻入列并打点计时，
          // 子 Agent（call_agent）调用因此能在流式过程中第一时间显示。
          finalizeReasoning(msg)
          const inv: ToolInvocation = {
            id: tc.id,
            name: tc.function?.name || '',
            arguments: tc.function?.arguments || '',
            toolIndex: tc.index,
            agentName: ev.agent_name,
            startedAt: stepClockAt,
          }
          msg.tools.push(inv)
          msg.steps.push({ kind: 'tool', tool: inv })
        }
      }
      return
    }
    // 工具结果：role=tool 且带 tool_call_id，回填到对应调用并结算耗时。
    if (ev.role === 'tool' && ev.tool_call_id) {
      const inv = msg.tools.find((t) => t.id === ev.tool_call_id)
      if (inv) {
        inv.result = ev.content || ''
        inv.isError = ev.error
        if (inv.startedAt !== undefined) inv.durationMs = now() - inv.startedAt
        // 工具步结束：推进时钟，让工具结果回填后到下一步开始的间隙归入下一步。
        stepClockAt = now()
      } else {
        const created: ToolInvocation = {
          id: ev.tool_call_id,
          name: ev.tool_name || '',
          arguments: '',
          result: ev.content || '',
          isError: ev.error,
          agentName: ev.agent_name,
        }
        msg.tools.push(created)
        msg.steps.push({ kind: 'tool', tool: created })
      }
      return
    }
    if (ev.content) {
      // 正文开始输出意味着思考阶段结束，结算其耗时。
      finalizeReasoning(msg)
      msg.content += ev.content
    }
  }

  // 停止生成（断开 SSE 连接）。
  function stop() {
    if (abortCtrl) {
      abortCtrl.abort()
      abortCtrl = null
    }
  }

  // 拉取当前会话最近一次对话详情，用于统计条。
  async function fetchStats() {
    if (!sessionId.value) return
    try {
      // /chat/:sid 返回包装体 { status, session_id, chat }，无记录时 chat 为 null。
      const resp = await api.get<{ chat: ChatRecord | null }>(
        `/chat/${sessionId.value}`
      )
      lastRecord.value = resp.chat
    } catch {
      // 统计非关键，失败忽略
    }
  }

  return {
    sessionId,
    messages,
    sending,
    sessions,
    sessionsTotal,
    loadingSessions,
    canLoadMore,
    lastRecord,
    newSession,
    loadSessions,
    openSession,
    send,
    stop,
    fetchStats,
  }
})
