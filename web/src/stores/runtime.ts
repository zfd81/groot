import { defineStore } from 'pinia'
import { ref } from 'vue'
import { runtimeApi, defaultRuntimeSettings, type RuntimeSettings } from '../api/runtime'

// 运行时配置：设置面板读写。与语音配置分开建 store，
// 因为语音状态还有聊天输入框这个读取方，而运行时配置只服务于设置面板。
export const useRuntimeStore = defineStore('runtime', () => {
  const settings = ref<RuntimeSettings>(defaultRuntimeSettings())
  const loaded = ref(false)

  // load 首次拉取，已加载过则复用
  async function load() {
    if (loaded.value) return
    await reload()
  }

  // reload 强制回源。保存失败后用它把界面恢复成服务端的真实值。
  async function reload() {
    settings.value = await runtimeApi.getSettings()
    loaded.value = true
  }

  // save 写入服务端，并用服务端回传的生效值覆盖本地状态
  async function save(next: RuntimeSettings) {
    settings.value = await runtimeApi.saveSettings(next)
    loaded.value = true
  }

  return { settings, loaded, load, reload, save }
})
