import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { ModelsResp, ModelInfo } from '../api/types'

// 元数据：模型列表、子 Agent 列表，供聊天输入区的切换控件使用。
export const useMetaStore = defineStore('meta', () => {
  const models = ref<ModelInfo[]>([])
  const defaultModel = ref('')
  const agents = ref<string[]>([])
  const loaded = ref(false)

  async function load() {
    if (loaded.value) return
    try {
      const resp = await api.get<ModelsResp>('/web/models')
      models.value = resp.models || []
      defaultModel.value = resp.default || ''
    } catch {
      // 模型列表拉取失败不阻断聊天
    }
    try {
      const resp = await api.get<{ agents?: Array<{ name: string }> }>('/web/agents')
      agents.value = (resp.agents || []).map((a) => a.name).filter(Boolean)
    } catch {
      // 子 Agent 列表可选
    }
    loaded.value = true
  }

  // 模型管理界面增删改后调用，强制重新拉取模型列表
  async function reload() {
    loaded.value = false
    await load()
  }

  return { models, defaultModel, agents, loaded, load, reload }
})
