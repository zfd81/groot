import { createApp } from 'vue'
import { createPinia } from 'pinia'
// Element Plus 暗色主题的 CSS 变量（组件样式本身由 unplugin 按需注入，
// 但暗色变量与基础样式变量需全局显式引入）。
import 'element-plus/theme-chalk/dark/css-vars.css'
import App from './App.vue'
import router from './router'
import i18n from './i18n'
import './styles/global.css'

const app = createApp(App)
app.use(createPinia())
app.use(i18n)
app.use(router)
app.mount('#app')
