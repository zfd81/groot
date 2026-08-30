import { createRouter, createWebHistory } from 'vue-router'
import { setUnauthorizedHandler } from '../api/client'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory('/ui/'),
  routes: [
    { path: '/', redirect: '/chat' },
    {
      path: '/login',
      name: 'login',
      component: () => import('../views/LoginView.vue'),
    },
    {
      path: '/chat',
      name: 'chat',
      component: () => import('../views/ChatView.vue'),
    },
    {
      path: '/chat/:sid',
      name: 'chat-session',
      component: () => import('../views/ChatView.vue'),
    },
  ],
})

// 401 统一跳登录页
setUnauthorizedHandler(() => {
  if (router.currentRoute.value.name !== 'login') {
    router.push({ name: 'login' })
  }
})

// 路由守卫：首次进入先查登录态；需要登录且未登录则跳登录页。
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.checked) {
    try {
      await auth.fetchMe()
    } catch {
      // 查询失败不阻断，交由页面内请求的 401 拦截处理
    }
  }
  if (to.name === 'login') {
    // 已登录或无需登录时不停留在登录页
    if (!auth.authRequired || auth.authenticated) return { name: 'chat' }
    return true
  }
  if (auth.authRequired && !auth.authenticated) {
    return { name: 'login' }
  }
  return true
})

export default router
