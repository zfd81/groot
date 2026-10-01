import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api } from '../api/client'
import type { ModelsResp, ModelInfo, DefaultModels } from '../api/types'

// 元数据：模型列表、各类型默认模型、子 Agent 列表，供聊天输入区的切换控件与设置面板使用。
export const useMetaStore = defineStore('meta', () => {
  const models = ref<ModelInfo[]>([])
  const defaults = ref<DefaultModels>({ chat: '', voice: '', vision: '' })
  // 默认对话模型，聊天输入区的模型下拉框以它标注「默认」
  const defaultModel = computed(() => defaults.value.chat)
  const agents = ref<string[]>([])
  const loaded = ref(false)

  async function load() {
    if (loaded.value) return
    try {
      const resp = await api.get<ModelsResp>('/web/models')
      models.value = resp.models || []
      // 按 Partial 合并：响应缺字段时以空串兜底
      const got: Partial<DefaultModels> = resp.defaults || {}
      defaults.value = { chat: '', voice: '', vision: '', ...got }
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

  return { models, defaults, defaultModel, agents, loaded, load, reload }
})
