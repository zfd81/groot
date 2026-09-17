// 文件面板 API 封装。上传走原生 fetch（multipart 与 client.ts 的 JSON 封装不兼容）。
import i18n from '../i18n'
import { api, ApiError, notifyUnauthorized } from './client'
import type { FileListResp, FileContentResp, FileScaffoldResp } from './types'

const t = i18n.global.t

const enc = encodeURIComponent

export const filesApi = {
  list: (path: string) => api.get<FileListResp>(`/web/files/list?path=${enc(path)}`),
  content: (path: string) => api.get<FileContentResp>(`/web/files/content?path=${enc(path)}`),
  save: (path: string, content: string) =>
    api.put<{ status: string }>('/web/files/content', { path, content }),
  rename: (from: string, to: string) =>
    api.post<{ status: string }>('/web/files/rename', { from, to }),
  remove: (path: string) => api.delete<{ status: string }>(`/web/files?path=${enc(path)}`),
  // agent 非空时 skill/mcp 创建到 subagents/<agent>/ 下；缺省或为空创建到主 Agent 目录。
  scaffold: (kind: 'skill' | 'mcp' | 'agent', name: string, agent?: string) =>
    api.post<FileScaffoldResp>('/web/files/scaffold', agent ? { kind, name, agent } : { kind, name }),
  downloadUrl: (path: string) => `/web/files/download?path=${enc(path)}`,

  // 目录上传第一步：在 dir 下创建单层目录 name，用于落任何文件之前探测目标目录是否已存在。
  // 目录已存在时后端返回 409（ApiError.status === 409，code 为 'exists'），
  // 调用方应捕获该状态并终止整批上传，而不是继续 POST 文件。
  uploadPrepare: (dir: string, name: string) =>
    api.post<{ status: string }>('/web/files/upload/prepare', { path: dir, name }),

  // relpath 承载文件在所选目录内的相对路径（如 `A/sub/note.md`），仅目录上传时传入；
  // 普通单文件上传不传，后端只取文件名最后一段。
  async upload(dir: string, file: File, relpath?: string): Promise<void> {
    const fd = new FormData()
    fd.append('path', dir)
    fd.append('file', file)
    if (relpath) fd.append('relpath', relpath)
    const resp = await fetch('/web/files/upload', {
      method: 'POST',
      body: fd,
      credentials: 'same-origin',
    })
    if (resp.status === 401) {
      notifyUnauthorized()
      throw new ApiError(401, t('error.unauthorized'))
    }
    if (!resp.ok) {
      const data = await resp.json().catch(() => null)
      throw new ApiError(
        resp.status,
        data?.message || `upload failed: ${resp.status}`,
        data?.code || data?.status
      )
    }
  },
}
