import { defineStore } from 'pinia'
import { ref } from 'vue'
import { sendersApi, defaultSendersSettings, type SendersSettings } from '../api/senders'

// 发送器配置：只服务于设置面板，与运行时配置同构地单独建 store。
// settings 里保存的是服务端回传值，其中密码为脱敏串。
export const useSendersStore = defineStore('senders', () => {
  const settings = ref<SendersSettings>(defaultSendersSettings())
  const loaded = ref(false)

  async function load() {
    if (loaded.value) return
    await reload()
  }

  // reload 强制回源。保存失败后用它把界面恢复成服务端的真实值。
  async function reload() {
    settings.value = await sendersApi.getSettings()
    loaded.value = true
  }

  // save 写入服务端，并用服务端回传的生效值覆盖本地状态
  async function save(next: SendersSettings) {
    settings.value = await sendersApi.saveSettings(next)
    loaded.value = true
  }

  return { settings, loaded, load, reload, save }
})
