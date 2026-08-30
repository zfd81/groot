import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { MeResp } from '../api/types'

// 登录态管理：启动时查询 /web/me，据 auth_required 决定是否需要登录。
export const useAuthStore = defineStore('auth', () => {
  const authenticated = ref(false)
  const authRequired = ref(false)
  const checked = ref(false)

  // 查询当前登录态。返回是否已认证。
  async function fetchMe(): Promise<boolean> {
    const me = await api.get<MeResp>('/web/me')
    authRequired.value = me.auth_required
    authenticated.value = me.authenticated
    checked.value = true
    return me.authenticated
  }

  async function login(username: string, password: string): Promise<void> {
    await api.post('/web/login', { username, password })
    authenticated.value = true
  }

  async function logout(): Promise<void> {
    try {
      await api.post('/web/logout')
    } finally {
      authenticated.value = false
    }
  }

  return { authenticated, authRequired, checked, fetchMe, login, logout }
})
