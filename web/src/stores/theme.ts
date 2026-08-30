import { defineStore } from 'pinia'
import { ref, computed, watchEffect } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'auto'

const STORAGE_KEY = 'groot-theme'

// 主题设置：浅色 / 深色 / 跟随系统，持久化到 localStorage。
// Element Plus 通过 <html> 上的 `dark` 类切换暗色主题（配合 dark/css-vars.css），
// 因此这里根据当前明暗状态在 documentElement 上增删 `dark` 类。
export const useThemeStore = defineStore('theme', () => {
  const saved = (localStorage.getItem(STORAGE_KEY) as ThemeMode | null) || 'auto'
  const mode = ref<ThemeMode>(saved)

  // 系统偏好（跟随系统模式下据此决定明暗）
  const prefersDark = ref(
    window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches
  )
  if (window.matchMedia) {
    window
      .matchMedia('(prefers-color-scheme: dark)')
      .addEventListener('change', (e) => {
        prefersDark.value = e.matches
      })
  }

  const isDark = computed(() => {
    if (mode.value === 'auto') return prefersDark.value
    return mode.value === 'dark'
  })

  // 将明暗状态同步到 <html class="dark">，供 Element Plus 暗色变量生效。
  watchEffect(() => {
    const el = document.documentElement
    if (isDark.value) el.classList.add('dark')
    else el.classList.remove('dark')
  })

  function setMode(next: ThemeMode) {
    mode.value = next
    localStorage.setItem(STORAGE_KEY, next)
  }

  return { mode, isDark, setMode }
})
