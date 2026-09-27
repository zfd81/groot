// web/src/composables/useRecorder.ts
// 麦克风录音的生命周期封装。组件只需调 start/stop，
// 停止后通过 stop() 的返回值拿到音频 Blob。
//
// 采集走 Web Audio 取原始 PCM，在浏览器内封成 WAV，而非用 MediaRecorder
// 产出 webm/opus：webm、m4a 这类压缩容器需要转录服务侧装有 ffmpeg 才能解码，
// 而 WAV 是未压缩的 PCM，任何转录服务都能直接读，不依赖外部解码器。
import { ref, readonly, onBeforeUnmount } from 'vue'

// 目标采样率。16 kHz 单声道是语音识别的通行输入规格，
// 既够用又能把未压缩音频的体积压到每秒约 32 KB。
const TARGET_SAMPLE_RATE = 16000

// 不足半秒基本是误触，没有可识别内容，直接按「无录音」处理
const MIN_DURATION_MS = 500

// 静音判定阈值。取采样绝对值的峰值：低于此值说明采集链路虽然在跑，
// 但输入端没有实际声音（选错输入设备、系统层面静音、虚拟声卡等）。
// 16 位 PCM 的一个量化步长约为 1/32768 ≈ 0.00003，
// 0.005 约合 -46 dBFS，比环境底噪低而远高于全零静音。
const SILENCE_PEAK = 0.005

// 每块采集的帧数。2048 帧在 16 kHz 下约 128 毫秒，
// 兼顾回调频率与单块大小。
const FRAME_SIZE = 2048

type AudioContextCtor = typeof AudioContext

function audioContextCtor(): AudioContextCtor | null {
  if (typeof window === 'undefined') return null
  const w = window as unknown as {
    AudioContext?: AudioContextCtor
    webkitAudioContext?: AudioContextCtor
  }
  return w.AudioContext ?? w.webkitAudioContext ?? null
}

// isRecordingSupported 在组件挂载时用于决定话筒按钮是否可用。
export function isRecordingSupported(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    !!navigator.mediaDevices?.getUserMedia &&
    audioContextCtor() !== null
  )
}

function writeAscii(view: DataView, offset: number, text: string): void {
  for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i))
}

// peakAmplitude 返回采样绝对值的峰值，用于判断是否采到了实际声音。
function peakAmplitude(samples: Float32Array): number {
  let peak = 0
  for (let i = 0; i < samples.length; i++) {
    const v = samples[i] < 0 ? -samples[i] : samples[i]
    if (v > peak) peak = v
  }
  return peak
}

// encodeWav 把 [-1, 1] 的浮点采样封成 16 位单声道 WAV。
// 采样率写入头部，故无需把音频重采样到某个固定值。
function encodeWav(samples: Float32Array, sampleRate: number): Blob {
  const bytes = samples.length * 2
  const view = new DataView(new ArrayBuffer(44 + bytes))
  writeAscii(view, 0, 'RIFF')
  view.setUint32(4, 36 + bytes, true)
  writeAscii(view, 8, 'WAVE')
  writeAscii(view, 12, 'fmt ')
  view.setUint32(16, 16, true) // fmt 块长度
  view.setUint16(20, 1, true) // 1 = 未压缩 PCM
  view.setUint16(22, 1, true) // 单声道
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, sampleRate * 2, true) // 每秒字节数
  view.setUint16(32, 2, true) // 每帧字节数
  view.setUint16(34, 16, true) // 位深
  writeAscii(view, 36, 'data')
  view.setUint32(40, bytes, true)
  let off = 44
  for (let i = 0; i < samples.length; i++, off += 2) {
    const s = Math.max(-1, Math.min(1, samples[i]))
    view.setInt16(off, s < 0 ? s * 0x8000 : s * 0x7fff, true)
  }
  return new Blob([view.buffer], { type: 'audio/wav' })
}

export type RecorderError = 'permission_denied' | 'no_device' | 'unsupported' | 'failed'

// RecordResult 是 stop() 的返回值。
// ok 携带音频；empty 表示点击过快或时长不足；silent 表示采集到的
// 全是接近零的采样，即麦克风没有拾到声音。三者的提示文案各不相同，
// 故由录音器给出判定，而非让调用方只看到一个 null。
export type RecordResult =
  | { kind: 'ok'; blob: Blob; filename: string }
  | { kind: 'empty' }
  | { kind: 'silent' }

export function useRecorder() {
  const recording = ref(false)
  // 录音时长（秒），用于在输入框上显示计时
  const duration = ref(0)

  let ctx: AudioContext | null = null
  let source: MediaStreamAudioSourceNode | null = null
  let processor: ScriptProcessorNode | null = null
  let stream: MediaStream | null = null
  let chunks: Float32Array[] = []
  let sampleRate = TARGET_SAMPLE_RATE
  let timer: number | null = null
  // starting：start() 正在等待权限弹窗期间的重入保护
  let starting = false
  // gen：代次计数。权限弹窗期间被 cancel() 时，start() 拿到的流应直接丢弃
  let gen = 0
  // startedAt：本次录音开始的时间戳，用于过滤过短的误触录音
  let startedAt = 0

  function cleanup() {
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
    // 依次断开采集链路，否则 AudioContext 会持续占用麦克风
    if (processor) {
      processor.onaudioprocess = null
      processor.disconnect()
      processor = null
    }
    source?.disconnect()
    source = null
    // close() 返回 Promise，失败无补救手段，故忽略
    ctx?.close().catch(() => {})
    ctx = null
    // 必须显式停掉轨道，否则浏览器标签页上的录音指示灯不会熄灭
    stream?.getTracks().forEach((tr) => tr.stop())
    stream = null
    starting = false
    startedAt = 0
    recording.value = false
  }

  // takeSamples 把分块的采集结果拼成一段连续采样，并清空累积缓冲。
  function takeSamples(): Float32Array {
    let total = 0
    for (const c of chunks) total += c.length
    const out = new Float32Array(total)
    let off = 0
    for (const c of chunks) {
      out.set(c, off)
      off += c.length
    }
    chunks = []
    return out
  }

  // start 申请麦克风并开始录音。失败时抛出 RecorderError 字符串，
  // 由调用方映射为面向使用者的提示文案。
  async function start(): Promise<void> {
    if (ctx || starting) return

    const Ctor = audioContextCtor()
    if (!Ctor) throw 'unsupported' as RecorderError

    starting = true
    const my = ++gen
    let s: MediaStream
    try {
      // 指定单声道并开启浏览器自带的降噪与回声抑制
      s = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
      })
    } catch (e: unknown) {
      starting = false
      const name = e instanceof DOMException ? e.name : ''
      // NotAllowedError：使用者拒绝或浏览器策略阻止
      // NotFoundError：没有可用的输入设备
      if (name === 'NotAllowedError' || name === 'SecurityError') {
        throw 'permission_denied' as RecorderError
      }
      if (name === 'NotFoundError' || name === 'DevicesNotFoundError') {
        throw 'no_device' as RecorderError
      }
      throw 'failed' as RecorderError
    }
    // 等待期间已被 cancel()：释放刚拿到的流，不进入录音
    if (my !== gen) {
      s.getTracks().forEach((tr) => tr.stop())
      starting = false
      return
    }
    stream = s
    chunks = []

    try {
      // sampleRate 是建议值：部分浏览器会忽略它而沿用设备原生采样率，
      // 故实际值从 ctx 读回，再写进 WAV 头部
      ctx = new Ctor({ sampleRate: TARGET_SAMPLE_RATE })
      sampleRate = ctx.sampleRate
      source = ctx.createMediaStreamSource(stream)
      // ScriptProcessor 已被标记废弃，但仍是各浏览器一致可用的同步采集方式；
      // 替代品 AudioWorklet 需要独立的模块文件，且 Safari 支持较晚
      processor = ctx.createScriptProcessor(FRAME_SIZE, 1, 1)
      processor.onaudioprocess = (ev) => {
        // 通道数据由浏览器复用，必须复制后再存
        chunks.push(new Float32Array(ev.inputBuffer.getChannelData(0)))
      }
      source.connect(processor)
      // 接到 destination 才会驱动 onaudioprocess 回调。增益置零避免自己听到回放
      const mute = ctx.createGain()
      mute.gain.value = 0
      processor.connect(mute)
      mute.connect(ctx.destination)
      startedAt = Date.now()
    } catch {
      cleanup()
      throw 'unsupported' as RecorderError
    }
    starting = false
    recording.value = true
    duration.value = 0
    timer = window.setInterval(() => {
      duration.value += 1
    }, 1000)
  }

  // stop 停止录音并返回判定结果。
  function stop(): Promise<RecordResult> {
    return new Promise((resolve) => {
      if (!ctx) {
        // 权限弹窗期间点停止：让等待中的 start() 作废
        gen++
        cleanup()
        resolve({ kind: 'empty' })
        return
      }
      const tooShort = Date.now() - startedAt < MIN_DURATION_MS
      const rate = sampleRate
      const samples = takeSamples()
      cleanup()
      // 空采样意味着点击过快、没采到样本；时长不足 MIN_DURATION_MS 的
      // 也视为误触。两种情况都按「无录音」处理
      if (tooShort || samples.length === 0) {
        resolve({ kind: 'empty' })
        return
      }
      // 采集链路在跑但全程接近零：上游会返回空文字，
      // 与其发一次无意义的请求，不如就近指出麦克风没拾到声音
      if (peakAmplitude(samples) < SILENCE_PEAK) {
        resolve({ kind: 'silent' })
        return
      }
      resolve({ kind: 'ok', blob: encodeWav(samples, rate), filename: 'recording.wav' })
    })
  }

  // cancel 丢弃本次录音，不触发转录。
  function cancel() {
    gen++
    chunks = []
    cleanup()
  }

  onBeforeUnmount(cancel)

  return {
    recording: readonly(recording),
    duration: readonly(duration),
    start,
    stop,
    cancel,
  }
}
