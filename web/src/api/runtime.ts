// web/src/api/runtime.ts
// 运行时配置：保存即生效的配置分类，读写同构。
// 回读的字段与提交的字段一一对应，界面按分区整体提交。
import { api } from './client'

export interface MemorySettings {
  history_window: number // LLM 上下文窗口（轮次），-1 不限制
}

export interface ReactSettings {
  max_iterations: number
  step_timeout: number // 单步 LLM 调用超时（秒）
  error_retry: number
}

export interface SubAgentSettings {
  max_concurrency: number // 同时运行的子 Agent 数上限
  exec_timeout: string // Go duration 字面量，如 "5m"
  max_task_length: number
  max_result_length: number
}

export interface AttachmentSettings {
  max_size: number // 单文件上限（MB）
  max_total_size: number // 单次合计上限（MB）
  max_count: number
  allowed_types: string[] // 空数组表示不限制
}

export interface RateLimitSettings {
  enabled: boolean
  global_qps: number // 0 表示不限制
  global_concurrency: number // 0 表示不限制
  default_qps: number // 每个调用方，0 表示不限制
  default_concurrency: number // 每个调用方，0 表示不限制
}

export interface ScheduleSettings {
  enabled: boolean // 是否允许在对话中创建定时任务
}

export interface RuntimeSettings {
  memory: MemorySettings
  react: ReactSettings
  subagent: SubAgentSettings
  attachment: AttachmentSettings
  rate_limit: RateLimitSettings
  schedule: ScheduleSettings
}

// 取值边界，与后端 internal/setting/runtime.go 中的常量保持一致。
// 前端用它约束输入控件，避免明显越界的值发到服务端才被拒绝。
export const runtimeLimits = {
  historyWindow: { min: -1, max: 500 },
  maxIterations: { min: 1, max: 200 },
  stepTimeout: { min: 1, max: 3600 },
  errorRetry: { min: 0, max: 10 },
  subAgentConcurrency: { min: 1, max: 100 },
  subAgentTextLength: { min: 1, max: 200000 },
  attachmentSize: { min: 1, max: 1024 },
  attachmentCount: { min: 1, max: 100 },
  rateLimitQPS: { min: 0, max: 100000 },
  rateLimitConcurrency: { min: 0, max: 100000 },
} as const

// defaultRuntimeSettings 仅用于首屏渲染的占位，真实值由接口返回后覆盖。
// 数值与 internal/setting/defaults.go 保持一致，避免首屏闪现与服务端不同的值。
export function defaultRuntimeSettings(): RuntimeSettings {
  return {
    memory: { history_window: 20 },
    react: { max_iterations: 20, step_timeout: 60, error_retry: 2 },
    subagent: {
      max_concurrency: 5,
      exec_timeout: '5m',
      max_task_length: 16000,
      max_result_length: 8000,
    },
    attachment: { max_size: 50, max_total_size: 100, max_count: 10, allowed_types: [] },
    rate_limit: {
      enabled: false,
      global_qps: 0,
      global_concurrency: 0,
      default_qps: 10,
      default_concurrency: 5,
    },
    schedule: { enabled: false },
  }
}

// cloneRuntimeSettings 按字段深拷贝一份可自由编辑的副本。
// 不用 structuredClone：store 里的值取出来是 Vue 的响应式 Proxy，
// 结构化克隆算法不接受 Proxy，会抛 DataCloneError。
export function cloneRuntimeSettings(s: RuntimeSettings): RuntimeSettings {
  return {
    memory: { history_window: s.memory.history_window },
    react: {
      max_iterations: s.react.max_iterations,
      step_timeout: s.react.step_timeout,
      error_retry: s.react.error_retry,
    },
    subagent: {
      max_concurrency: s.subagent.max_concurrency,
      exec_timeout: s.subagent.exec_timeout,
      max_task_length: s.subagent.max_task_length,
      max_result_length: s.subagent.max_result_length,
    },
    attachment: {
      max_size: s.attachment.max_size,
      max_total_size: s.attachment.max_total_size,
      max_count: s.attachment.max_count,
      allowed_types: [...s.attachment.allowed_types],
    },
    rate_limit: {
      enabled: s.rate_limit.enabled,
      global_qps: s.rate_limit.global_qps,
      global_concurrency: s.rate_limit.global_concurrency,
      default_qps: s.rate_limit.default_qps,
      default_concurrency: s.rate_limit.default_concurrency,
    },
    schedule: { enabled: s.schedule.enabled },
  }
}

export const runtimeApi = {
  getSettings: () => api.get<RuntimeSettings>('/web/settings/runtime'),

  // 保存成功后服务端回传生效值，直接用它覆盖本地状态
  saveSettings: (s: RuntimeSettings) => api.put<RuntimeSettings>('/web/settings/runtime', s),
}
