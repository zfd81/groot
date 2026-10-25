import { defineStore } from 'pinia'
import { ref } from 'vue'
import { speechApi, type SpeechSettings } from '../api/speech'

// 语音朗读配置：设置面板负责写，消息朗读按钮负责读。
// 两处共享同一份状态，保存后朗读立即使用新参数，无需刷新页面。
export const useSpeechSettingsStore = defineStore('speechSettings', () => {
  const settings = ref<SpeechSettings>({ voice: '', instructions: '' })
  const loaded = ref(false)

  // load 首次拉取。已加载过则直接复用，避免每次点朗读都打一次接口。
  async function load() {
    if (loaded.value) return
    await reload()
  }

  // reload 强制回源。设置保存失败后用它把界面恢复成服务端的真实值。
  async function reload() {
    try {
      settings.value = await speechApi.getSettings()
      loaded.value = true
    } catch {
      // 配置读取失败按默认音色处理，不弹错误：朗读是增强功能，不该阻塞界面
    }
  }

  // save 写入服务端并同步本地状态，成功后所有读取方立即看到新值。
  async function save(next: SpeechSettings) {
    await speechApi.saveSettings(next)
    settings.value = { ...next }
    loaded.value = true
  }

  return { settings, loaded, load, reload, save }
})
