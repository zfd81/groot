// 配置同步 API 封装。三个端点都用 POST：路径是数组，放请求体比 query 干净。
// paths 省略或为空数组表示全量同步（后端展开白名单）。
import { api } from './client'
import type { SyncDiffResp } from './types'

export const syncApi = {
  diff: (paths?: string[]) => api.post<SyncDiffResp>('/web/sync/diff', { paths: paths ?? [] }),
  push: (paths?: string[]) => api.post<{ status: string }>('/web/sync/push', { paths: paths ?? [] }),
  pull: (paths?: string[]) => api.post<{ status: string }>('/web/sync/pull', { paths: paths ?? [] }),
}
