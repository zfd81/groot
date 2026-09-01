import { createRouter, createWebHistory } from 'vue-router'
import { setUnauthorizedHandler } from '../api/client'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory('/ui/'),
  routes: [
    { path: '/', redirect: '/chat' },
    {
      path: '/setup',
      name: 'setup',
      component: () => import('../views/SetupView.vue'),
    },
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

// 路由守卫：首次进入先查登录态。
// 用户表为空 → 创建用户页；未登录 → 登录页；已登录访问 login/setup → 主页面。
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.checked) {
    try {
      await auth.fetchMe()
    } catch {
      // 查询失败不阻断，交由页面内请求的 401 拦截处理
    }
  }
  if (auth.needsSetup) {
    return to.name === 'setup' ? true : { name: 'setup' }
  }
  if (to.name === 'setup') {
    // 已初始化后不停留在创建用户页
    return { name: auth.authenticated ? 'chat' : 'login' }
  }
  if (to.name === 'login') {
    // 已登录时不停留在登录页
    return auth.authenticated ? { name: 'chat' } : true
  }
  if (!auth.authenticated) {
    return { name: 'login' }
  }
  return true
})

export default router
