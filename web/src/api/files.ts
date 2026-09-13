// 文件面板 API 封装。上传走原生 fetch（multipart 与 client.ts 的 JSON 封装不兼容）。
import { api, ApiError, notifyUnauthorized } from './client'
import type { FileListResp, FileContentResp, FileScaffoldResp } from './types'

const enc = encodeURIComponent

export const filesApi = {
  list: (path: string) => api.get<FileListResp>(`/web/files/list?path=${enc(path)}`),
  content: (path: string) => api.get<FileContentResp>(`/web/files/content?path=${enc(path)}`),
  save: (path: string, content: string) =>
    api.put<{ status: string }>('/web/files/content', { path, content }),
  mkdir: (path: string) => api.post<{ status: string }>('/web/files/mkdir', { path }),
  create: (path: string) => api.post<{ status: string }>('/web/files/create', { path }),
  rename: (from: string, to: string) =>
    api.post<{ status: string }>('/web/files/rename', { from, to }),
  remove: (path: string) => api.delete<{ status: string }>(`/web/files?path=${enc(path)}`),
  scaffold: (kind: 'skill' | 'mcp' | 'agent', name: string) =>
    api.post<FileScaffoldResp>('/web/files/scaffold', { kind, name }),
  downloadUrl: (path: string) => `/web/files/download?path=${enc(path)}`,

  async upload(dir: string, file: File): Promise<void> {
    const fd = new FormData()
    fd.append('path', dir)
    fd.append('file', file)
    const resp = await fetch('/web/files/upload', {
      method: 'POST',
      body: fd,
      credentials: 'same-origin',
    })
    if (resp.status === 401) {
      notifyUnauthorized()
      throw new ApiError(401, 'unauthorized')
    }
    if (!resp.ok) {
      const data = await resp.json().catch(() => null)
      throw new ApiError(resp.status, data?.message || `upload failed: ${resp.status}`, data?.status)
    }
  },
}
