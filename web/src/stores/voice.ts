import { defineStore } from 'pinia'
import { ref } from 'vue'
import { voiceApi, type VoiceSettings } from '../api/voice'

// 语音配置：设置面板负责写，聊天输入框负责读。
// 两处共享同一份状态，保存后话筒按钮立即跟随变化，无需刷新页面。
export const useVoiceStore = defineStore('voice', () => {
  const settings = ref<VoiceSettings>({ enabled: false, model: '', auto_send: false })
  const loaded = ref(false)

  // load 首次拉取。已加载过则直接复用，避免每次挂载输入框都打一次接口。
  async function load() {
    if (loaded.value) return
    await reload()
  }

  // reload 强制回源。设置保存失败后用它把界面恢复成服务端的真实值。
  async function reload() {
    try {
      settings.value = await voiceApi.getSettings()
      loaded.value = true
    } catch {
      // 配置读取失败按未启用处理，不弹错误：语音是增强功能，不该阻塞输入框
    }
  }

  // save 写入服务端并同步本地状态，成功后所有读取方立即看到新值。
  async function save(next: VoiceSettings) {
    await voiceApi.saveSettings(next)
    settings.value = { ...next }
    loaded.value = true
  }

  return { settings, loaded, load, reload, save }
})
