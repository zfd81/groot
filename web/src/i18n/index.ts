import { createI18n } from 'vue-i18n'
import { detectInitialLang } from '../stores/language'
import zhCn from './messages/zh-cn'
import en from './messages/en'

// Composition API 模式（legacy:false）：切换语言用 i18n.global.locale.value = x。
// 初值直接读 localStorage / 浏览器语言，不依赖 pinia，保证首屏语言正确。
const i18n = createI18n({
  legacy: false,
  locale: detectInitialLang(),
  fallbackLocale: 'zh-cn',
  messages: {
    'zh-cn': zhCn,
    en,
  },
})

export default i18n
