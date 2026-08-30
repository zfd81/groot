import { defineStore } from 'pinia'
import { ref } from 'vue'

export type Lang = 'zh-cn' | 'en'

const STORAGE_KEY = 'groot-language'

// 首次访问（无本地设置）时跟随浏览器语言：以 zh 开头视为中文，否则英文。
export function detectInitialLang(): Lang {
  const saved = localStorage.getItem(STORAGE_KEY) as Lang | null
  if (saved === 'zh-cn' || saved === 'en') return saved
  const nav = navigator.language?.toLowerCase() || ''
  return nav.startsWith('zh') ? 'zh-cn' : 'en'
}

// 语言设置：中文 / English，持久化到 localStorage。
// 以本 store 为单一数据源，App.vue 负责把 locale 同步到 vue-i18n 与 el-config-provider。
export const useLanguageStore = defineStore('language', () => {
  const locale = ref<Lang>(detectInitialLang())

  function setLocale(next: Lang) {
    locale.value = next
    localStorage.setItem(STORAGE_KEY, next)
  }

  return { locale, setLocale }
})
