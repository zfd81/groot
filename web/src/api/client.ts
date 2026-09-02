// HTTP 客户端封装：统一 JSON 请求、错误处理与 401 拦截。
// 同源部署（前端在 /ui/，API 在同一服务），故请求用相对路径，
// Cookie 由浏览器自动携带。
import i18n from '../i18n'

const t = i18n.global.t

export class ApiError extends Error {
  status: number
  code?: string
  constructor(status: number, message: string, code?: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

// 401 处理钩子：由 auth store / router 注入，收到 401 时跳登录页。
let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

// 供非 request() 路径（如 SSE 流）复用同一套 401 跳转逻辑。
export function notifyUnauthorized() {
  if (onUnauthorized) onUnauthorized()
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  extraHeaders?: Record<string, string>
): Promise<T> {
  const headers: Record<string, string> = { ...(extraHeaders || {}) }
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const resp = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: 'same-origin',
  })

  if (resp.status === 401) {
    if (onUnauthorized) onUnauthorized()
    throw new ApiError(401, t('error.unauthorized'))
  }

  const text = await resp.text()
  let data: any = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = text
    }
  }

  if (!resp.ok) {
    const message =
      (data && (data.message || data.error)) ||
      t('error.requestFailed', { status: resp.status })
    const code = data && (data.code || data.status)
    throw new ApiError(resp.status, message, code)
  }

  return data as T
}

export const api = {
  get: <T>(path: string, headers?: Record<string, string>) =>
    request<T>('GET', path, undefined, headers),
  post: <T>(path: string, body?: unknown, headers?: Record<string, string>) =>
    request<T>('POST', path, body, headers),
  put: <T>(path: string, body?: unknown, headers?: Record<string, string>) =>
    request<T>('PUT', path, body, headers),
  delete: <T>(path: string, headers?: Record<string, string>) =>
    request<T>('DELETE', path, undefined, headers),
}
