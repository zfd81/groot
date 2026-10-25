import { defineStore } from 'pinia'
import { ref } from 'vue'
import { speechApi } from '../api/speech'
import { useSpeechSettingsStore } from './speechSettings'
import { useMetaStore } from './meta'
import { extractSentences } from '../utils/speechText'
import i18n from '../i18n'

// 总开关持久化键：值 "0" 表示关闭，其他或缺失表示开启。
export const SPEECH_AUTOPLAY_KEY = 'groot.speech.autoplay'

// 预取上限：正在播放的任务之后，最多有这么多任务处于 fetching/ready。
const PREFETCH_LIMIT = 2

// 一段 8kHz 单声道 8bit、仅 1 个采样的静音 WAV，用于在用户手势中解锁自动播放。
const SILENT_WAV =
  'data:audio/wav;base64,UklGRiUAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQEAAACA'

type TaskStatus = 'pending' | 'fetching' | 'ready' | 'playing' | 'done' | 'failed'

interface Task {
  text: string
  status: TaskStatus
  url?: string
  // 每个任务独立的取消控制器：stop() 时中止仍在进行的合成请求。
  abort?: AbortController
}

// 读取总开关初始值。存储不可用时按开启处理。
function readAutoplay(): boolean {
  try {
    return localStorage.getItem(SPEECH_AUTOPLAY_KEY) !== '0'
  } catch {
    return true
  }
}

// 全局朗读播放器：切句、合成、队列与播放。同一时刻只服务一个来源
//（自动朗读中的流式消息，或点播的某条消息），来源切换时先 stop()。
export const useSpeechStore = defineStore('speech', () => {
  const autoplay = ref(readAutoplay())
  // 正在朗读的消息 id，null 表示空闲。
  const playingId = ref<string | null>(null)

  const settingsStore = useSpeechSettingsStore()

  let queue: Task[] = []
  const audio = new Audio()
  // 流式切句已消费到的字符偏移。
  let offset = 0
  // 正在自动朗读的流式消息 id；流结束（done）后置空。
  let streamingId: string | null = null
  // 被打断而放弃自动朗读的流式消息 id，后续对它的 feedStreaming 一律忽略，
  // 直到下一条流式消息开始。
  let abandonedId: string | null = null
  // 本轮播放会话是否已弹过失败提示，stop() 重置。
  let warned = false
  // 每次 stop() 递增；异步合成结果回来时若代数已变化则丢弃。
  let generation = 0
  // 因总开关关闭或未配置默认 TTS 模型而被门禁挡住、仍在流式中的消息 id；流结束置空。
  // 只用于判断「打开总开关时是否有回复正在流式」。
  let gatedStreamId: string | null = null
  // 总开关从关到开的瞬间若有回复正在流式，下一条被接管的流式消息只朗读此后的内容，
  // 不从头读。首次接管时消费并清空；流在被接管前就结束了也清空。stop() 不清它。
  let resumeFromCurrent = false

  // 释放任务持有的 object URL。
  function releaseUrl(task: Task) {
    if (task.url) {
      URL.revokeObjectURL(task.url)
      task.url = undefined
    }
  }

  // 清空播放状态：暂停音频、中止合成请求、释放 URL、清空队列、重置偏移。
  // 若此时有流式消息正在自动朗读，它即告放弃：任何打断（关闭总开关、点播其他消息、
  // 停止生成、切换会话、取消默认 TTS 模型）之后都不再续接，流结束时也不补播。
  // streamingId 本身不清，由流结束的 feedStreaming(done) 置空。
  function stop() {
    generation++
    if (streamingId !== null) abandonedId = streamingId
    audio.pause()
    audio.onended = null
    audio.onerror = null
    audio.removeAttribute('src')
    for (const task of queue) {
      task.abort?.abort()
      releaseUrl(task)
    }
    queue = []
    offset = 0
    warned = false
    playingId.value = null
  }

  // 启动一个 pending 任务的合成。
  function fetchTask(task: Task, gen: number) {
    task.status = 'fetching'
    const ctrl = new AbortController()
    task.abort = ctrl
    void settingsStore
      .load()
      .then(() => speechApi.synthesize(task.text, settingsStore.settings, ctrl.signal))
      .then(
        (blob) => {
          // stop() 之后的过期结果直接丢弃，不进入队列也不创建 URL。
          if (gen !== generation) return
          task.url = URL.createObjectURL(blob)
          task.status = 'ready'
          pump()
        },
        (err: unknown) => {
          if (gen !== generation) return
          // 用户中止不是失败，保持静默。
          if (err instanceof DOMException && err.name === 'AbortError') return
          task.status = 'failed'
          console.warn('speech synthesize failed:', err)
          if (!warned) {
            warned = true
            ElMessage.warning(i18n.global.t('chat.speechFailed'))
          }
          pump()
        },
      )
  }

  // 播放一个 ready 任务；结束或出错后进入下一个。
  function playTask(task: Task, gen: number) {
    task.status = 'playing'
    const finish = (failed: boolean) => {
      if (gen !== generation) return
      audio.onended = null
      audio.onerror = null
      releaseUrl(task)
      task.status = failed ? 'failed' : 'done'
      pump()
    }
    audio.onended = () => finish(false)
    audio.onerror = () => {
      console.warn('speech audio error:', audio.error)
      finish(true)
    }
    audio.src = task.url!
    audio.play().catch((err: unknown) => {
      if (gen !== generation) return
      console.warn('speech play failed:', err)
      finish(true)
    })
  }

  // 推进队列：补足预取、播放队首 ready 任务、队列耗尽时回到空闲。
  function pump() {
    const gen = generation
    const playing = queue.some((t) => t.status === 'playing')
    let inflight = queue.filter((t) => t.status === 'fetching' || t.status === 'ready').length
    for (const task of queue) {
      if (inflight >= PREFETCH_LIMIT) break
      if (task.status === 'pending') {
        fetchTask(task, gen)
        inflight++
      }
    }

    if (!playing) {
      // 严格按顺序播放：跳过已结束的任务，队首若仍在合成则等待其完成后再被 pump。
      const head = queue.find((t) => t.status !== 'done' && t.status !== 'failed')
      if (head && head.status === 'ready') {
        playTask(head, gen)
        return
      }
      if (!head && streamingId !== playingId.value) {
        // 队列全部结束且当前来源不再有新文本（点播来源或流式已 done）：回到空闲。
        // 流式来源仍在进行时保持 playingId，等待后续句子。
        for (const task of queue) releaseUrl(task)
        queue = []
        playingId.value = null
      }
    }
  }

  // 单句长度上限：后端 /audio/speech 对 input 限制 4096 字符（超出返回 400），留出余量。
  const MAX_SENTENCE = 4000

  // 把超长句子切成不超过 MAX_SENTENCE 的片段；优先在上限前最后 200 字符内的
  // 空白或逗号处断开，找不到就按长度硬切。
  function splitLong(text: string): string[] {
    const parts: string[] = []
    let rest = text
    while (rest.length > MAX_SENTENCE) {
      let cut = -1
      for (let i = MAX_SENTENCE; i > MAX_SENTENCE - 200; i--) {
        const ch = rest[i - 1]
        if (ch === ' ' || ch === '\t' || ch === '，' || ch === ',') {
          cut = i
          break
        }
      }
      if (cut < 0) cut = MAX_SENTENCE
      parts.push(rest.slice(0, cut))
      rest = rest.slice(cut)
    }
    if (rest) parts.push(rest)
    return parts
  }

  function enqueue(sentences: string[]) {
    for (const text of sentences) {
      for (const part of text.length > MAX_SENTENCE ? splitLong(text) : [text]) {
        const trimmed = part.trim()
        if (trimmed) queue.push({ text: trimmed, status: 'pending' })
      }
    }
  }

  // 总开关。关闭时放弃正在自动朗读的流式消息并停止播放；
  // 关→开时若有回复正在流式，则只朗读打开之后到达的内容。
  function setAutoplay(v: boolean) {
    const was = autoplay.value
    autoplay.value = v
    try {
      localStorage.setItem(SPEECH_AUTOPLAY_KEY, v ? '1' : '0')
    } catch {
      // 存储不可用时只在内存中生效
    }
    if (!v) stop()
    else if (!was) resumeFromCurrent = gatedStreamId !== null
  }

  // 数据层门禁：未配置默认 TTS 模型时合成接口必然 400，不发请求、不入队。
  function ttsAvailable(): boolean {
    return !!useMetaStore().defaults.tts
  }

  // 流式增量输入：content 为该消息当前完整正文，done 表示流已结束。
  function feedStreaming(id: string, content: string, done: boolean) {
    if (id === abandonedId) {
      // 被放弃的消息仍要在流结束时解除「正在流式」标记，否则点播其他消息后
      // 队列耗尽时会误以为来源还在流式而不回到空闲。
      if (done && streamingId === id) streamingId = null
      return
    }
    if (!autoplay.value || !ttsAvailable()) {
      // 记住被挡住的流，供打开总开关时判断是否要从当前位置接续。
      gatedStreamId = done ? null : id
      if (done) resumeFromCurrent = false
      return
    }
    if (playingId.value !== id) {
      // 新的流式消息首次送入。stop() 会把仍在流式中的消息记为放弃，
      // 因此走到这里的一定是尚未被播放器接管过的新消息。
      // 总开关在本条回复流式中途刚被打开：跳过已到达的内容，只读之后的。
      const from = resumeFromCurrent ? content.length : 0
      resumeFromCurrent = false
      gatedStreamId = null
      const { sentences, next } = extractSentences(content, from, done)
      // 流已结束且没有可读内容（空回复或只有代码块）：不接管播放器，免得打断当前点播。
      if (done && sentences.length === 0) return
      if (playingId.value !== null) stop()
      // 新流式消息开始：上一条被放弃的记录随之失效。
      abandonedId = null
      playingId.value = id
      streamingId = id
      offset = next
      enqueue(sentences)
    } else {
      const { sentences, next } = extractSentences(content, offset, done)
      offset = next
      enqueue(sentences)
    }
    if (done) streamingId = null
    pump()
  }

  // 点播整条消息。若此时有流式消息正在自动朗读，stop() 会把它记为放弃。
  function playMessage(id: string, content: string) {
    if (!ttsAvailable()) return
    stop()
    // 被总开关挡住、随后「接续」的流不得在用户显式点播另一条消息后接管播放。
    if (gatedStreamId !== null) {
      abandonedId = gatedStreamId
      gatedStreamId = null
      resumeFromCurrent = false
    }
    playingId.value = id
    const { sentences } = extractSentences(content, 0, true)
    enqueue(sentences)
    pump()
  }

  // 在用户手势（发送消息）中播放一次静音的极短音频以解锁浏览器自动播放限制，失败忽略。
  // Safari 的许可按元素记录，因此必须在同一个 Audio 上播放一次真实的音频源。
  function unlock() {
    if (playingId.value !== null) return
    try {
      audio.muted = true
      audio.src = SILENT_WAV
      const p = audio.play()
      if (p) p.catch(() => {})
      audio.pause()
      audio.muted = false
    } catch {
      // 解锁失败等待下一次用户手势
    }
  }

  return { autoplay, playingId, setAutoplay, feedStreaming, playMessage, stop, unlock }
})
