import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { MeResp } from '../api/types'

// 登录态管理：启动时查询 /web/me；needs_setup 为 true 时进入创建用户流程。
export const useAuthStore = defineStore('auth', () => {
  const authenticated = ref(false)
  const needsSetup = ref(false)
  const username = ref('')
  const checked = ref(false)

  // 查询当前登录态。返回是否已认证。
  async function fetchMe(): Promise<boolean> {
    const me = await api.get<MeResp>('/web/me')
    authenticated.value = me.authenticated
    needsSetup.value = me.needs_setup
    username.value = me.username || ''
    checked.value = true
    return me.authenticated
  }

  // 首次初始化：创建用户（成功后不自动登录，由页面跳转到登录页）
  async function setup(name: string, password: string): Promise<void> {
    await api.post('/web/setup', { username: name, password })
    needsSetup.value = false
  }

  async function login(name: string, password: string): Promise<void> {
    await api.post('/web/login', { username: name, password })
    authenticated.value = true
    username.value = name
  }

  async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
    await api.post('/web/password', { old_password: oldPassword, new_password: newPassword })
  }

  async function logout(): Promise<void> {
    try {
      await api.post('/web/logout')
    } finally {
      authenticated.value = false
      username.value = ''
    }
  }

  return { authenticated, needsSetup, username, checked, fetchMe, setup, login, changePassword, logout }
})
