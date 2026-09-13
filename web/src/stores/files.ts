// 文件面板状态：开关/宽度持久化，树版本号驱动刷新，预览与编辑路径。
import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const OPEN_KEY = 'groot-files-open'
const WIDTH_KEY = 'groot-files-width'

export function clampWidth(w: number): number {
  return Math.min(600, Math.max(280, w))
}

export const useFilesStore = defineStore('files', () => {
  const open = ref(localStorage.getItem(OPEN_KEY) === '1')
  const width = ref(clampWidth(Number(localStorage.getItem(WIDTH_KEY)) || 390))
  const fullscreen = ref(false)
  const homePath = ref('') // 由首次 list('') 响应回填，供路径栏展示
  const previewPath = ref('') // 非空 = 面板显示预览而非树
  const editorPath = ref('') // 非空 = 编辑弹窗打开
  const treeVersion = ref(0) // +1 触发树重建（保持已展开节点）
  const expandedKeys = ref<string[]>([])

  watch(open, (v) => localStorage.setItem(OPEN_KEY, v ? '1' : '0'))
  watch(width, (v) => localStorage.setItem(WIDTH_KEY, String(v)))

  function toggle() {
    open.value = !open.value
    if (!open.value) fullscreen.value = false
  }

  function refresh() {
    treeVersion.value++
  }

  return {
    open, width, fullscreen, homePath, previewPath, editorPath,
    treeVersion, expandedKeys, toggle, refresh,
  }
})
