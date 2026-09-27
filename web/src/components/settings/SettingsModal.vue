<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { storeToRefs } from 'pinia'
import { Sunny, Moon, Monitor, Document } from '@element-plus/icons-vue'
import BoltIcon from '../files/BoltIcon.vue'
import WrenchIcon from '../files/WrenchIcon.vue'
import { useThemeStore, type ThemeMode } from '../../stores/theme'
import { useLanguageStore, type Lang } from '../../stores/language'
import { useMetaStore } from '../../stores/meta'
import { useAuthStore } from '../../stores/auth'
import { api, ApiError } from '../../api/client'
import type { ToolsResp, AgentsResp, AgentInfo, AgentDefinitionResp, HealthResp } from '../../api/types'
// ElMessageBox 不在 unplugin 自动导入范围内，需显式引入（含样式）
import { ElMessage, ElMessageBox } from 'element-plus'
import { type VoiceSettings } from '../../api/voice'
import { useVoiceStore } from '../../stores/voice'
import {
  runtimeLimits,
  defaultRuntimeSettings,
  cloneRuntimeSettings,
  type RuntimeSettings,
} from '../../api/runtime'
import { useRuntimeStore } from '../../stores/runtime'
import { senderLimits, editableSenders, type SendersSettings } from '../../api/senders'
import { useSendersStore } from '../../stores/senders'
import {
  fetchAuthSettings,
  saveAuthHeaderName,
  regenerateAuthSecret,
  type AuthSettings,
} from '../../api/authSettings'
import ModelsPanel from './ModelsPanel.vue'
import ApiKeysPanel from './ApiKeysPanel.vue'
import ClusterPanel from './ClusterPanel.vue'

const { t } = useI18n()
const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [v: boolean] }>()

const theme = useThemeStore()
const { mode } = storeToRefs(theme)
const langStore = useLanguageStore()
const { locale } = storeToRefs(langStore)
const meta = useMetaStore()
const { models } = storeToRefs(meta)

const section = ref<string>('general')
const menuOptions = computed(() => [
  { label: t('settings.menuGeneral'), key: 'general' },
  { label: t('settings.menuModels'), key: 'models' },
  { label: t('settings.menuAgents'), key: 'agents' },
  { label: t('settings.menuApiKeys'), key: 'apikeys' },
  { label: t('settings.menuCluster'), key: 'cluster' },
  { label: t('settings.menuConfig'), key: 'config' },
  { label: t('settings.menuAccount'), key: 'account' },
])

// 语音配置。面板打开时读一次，每次改动即时保存，与外观、语言的行为一致。
const voiceStore = useVoiceStore()
// 本地副本：编辑中的值。保存成功后写回 store，失败则从 store 回滚，
// 避免把一个服务端已拒绝的值留在界面上。
const voice = ref<VoiceSettings>({ ...voiceStore.settings })
const voiceSaving = ref(false)

// 可选的识别模型来自模型列表；语音接口要求模型已启用
const voiceModelOptions = computed(() =>
  (models.value || []).filter((m) => m.enabled).map((m) => ({ label: m.name, value: m.name }))
)

async function loadVoice() {
  await voiceStore.reload()
  voice.value = { ...voiceStore.settings }
}

// ---- 运行时配置 ----
const runtimeStore = useRuntimeStore()
// 本地副本：编辑中的值不直接改 store，保存失败时可整体回滚
const runtime = ref<RuntimeSettings>(defaultRuntimeSettings())
const runtimeSaving = ref(false)

async function loadRuntime() {
  try {
    await runtimeStore.reload()
    runtime.value = cloneRuntimeSettings(runtimeStore.settings)
  } catch (e: any) {
    ElMessage.error(t('settings.runtimeLoadFailed', { msg: e?.message || '' }))
  }
}

// saveRuntime 整体保存运行时配置的全部分组。越界由服务端裁决，失败即回源，
// 避免界面上留下一个并未生效的值。
//
// 保存必须串行：输入框 blur 触发的 change 紧接 input-number 的 ▲ 点击会连发两次，
// 若并发两个 PUT，先发后到的响应会用服务端旧值覆盖用户后来的改动。
// 因此保存进行中再触发只记脏标记，由正在跑的那次收尾时再发一轮；
// 每轮都读最新的本地副本，且只在最后一轮成功后才用服务端回传值覆盖本地副本。
let runtimeDirty = false
async function saveRuntime() {
  // 读取失败时本地副本仍是 defaultRuntimeSettings() 的占位值，此时保存会把整包默认值
  // 写到服务端、覆盖真实配置。store 的 loaded 只在 reload 成功后置 true，据此挡掉。
  if (!runtimeStore.loaded) {
    ElMessage.warning(t('settings.saveBlockedNotLoaded'))
    return
  }
  if (runtimeSaving.value) {
    runtimeDirty = true
    return
  }
  runtimeSaving.value = true
  try {
    do {
      runtimeDirty = false
      await runtimeStore.save(runtime.value)
    } while (runtimeDirty)
    runtime.value = cloneRuntimeSettings(runtimeStore.settings)
    ElMessage.success(t('settings.runtimeSaved'))
  } catch (e: any) {
    ElMessage.error(t('settings.runtimeSaveFailed', { msg: e?.message || '' }))
    await loadRuntime()
  } finally {
    runtimeSaving.value = false
  }
}

// 附件扩展名以逗号分隔的文本编辑，比多选标签更贴合「自由填写扩展名」的场景
const allowedTypesText = computed({
  get: () => runtime.value.attachment.allowed_types.join(', '),
  set: (raw: string) => {
    runtime.value.attachment.allowed_types = raw
      .split(',')
      .map((x) => x.trim())
      .filter((x) => x !== '')
  },
})

// ---- 发送器配置 ----
const sendersStore = useSendersStore()
// 本地副本用 editableSenders 生成：密码清空，不动密码直接保存即提交空串
const senders = ref<SendersSettings>(editableSenders(sendersStore.settings))
const sendersSaving = ref(false)
// 服务端回读的密码非空即表示已设置；输入框用它决定提示文案
const smtpPasswordSet = computed(() => sendersStore.settings.senders.email.password !== '')

async function loadSenders() {
  try {
    await sendersStore.reload()
    senders.value = editableSenders(sendersStore.settings)
  } catch (e: any) {
    ElMessage.error(t('settings.sendersLoadFailed', { msg: e?.message || '' }))
  }
}

// saveSenders 整体保存两个渠道。启用了渠道但参数不全由服务端拒绝，
// 失败即回源，避免界面上留下一个并未生效的值。
//
// 保存必须串行，原因同 saveRuntime：并发两个 PUT 时先发后到的响应会覆盖用户后来的改动。
// 保存进行中再触发只记脏标记，由正在跑的那次收尾时再发一轮；
// 每轮都读最新的本地副本，且只在最后一轮成功后才用 editableSenders 覆盖本地副本。
let sendersDirty = false
async function saveSenders() {
  // 同 saveRuntime：读取失败时本地副本是 defaultSendersSettings() 的占位值，
  // 保存会覆盖服务端真实配置；loaded 只在 reload 成功后置 true。
  if (!sendersStore.loaded) {
    ElMessage.warning(t('settings.saveBlockedNotLoaded'))
    return
  }
  if (sendersSaving.value) {
    sendersDirty = true
    return
  }
  sendersSaving.value = true
  try {
    do {
      sendersDirty = false
      await sendersStore.save(senders.value)
    } while (sendersDirty)
    senders.value = editableSenders(sendersStore.settings)
    ElMessage.success(t('settings.sendersSaved'))
  } catch (e: any) {
    ElMessage.error(t('settings.sendersSaveFailed', { msg: e?.message || '' }))
    await loadSenders()
  } finally {
    sendersSaving.value = false
  }
}

// ---- 认证配置 ----
// 重启后生效的一组，无其他页面消费，不进 store，面板内自管理。
// authSettings 保存服务端最近一次回读值，请求头名的输入框绑定独立副本，
// 保存失败时用回读值回滚，避免把服务端已拒绝的值留在界面上。
const authSettings = ref<AuthSettings | null>(null)
const authHeaderName = ref('')
const authSaving = ref(false)
const authRegenerating = ref(false)

async function loadAuthSettings() {
  try {
    const s = await fetchAuthSettings()
    authSettings.value = s
    authHeaderName.value = s.header_name
  } catch (e: any) {
    ElMessage.error(t('settings.authLoadFailed', { msg: e?.message || '' }))
  }
}

// saveAuthHeader 保存 API Key 请求头名。空串即恢复默认，由服务端裁决；
// 保存成功用回传值覆盖本地（恢复默认时回读即默认值）。
//
// 保存必须串行，原因同 saveRuntime：并发两个 PUT 时先发后到的响应会覆盖后来的改动。
// 保存进行中再触发只记脏标记，由正在跑的那次收尾时再发一轮。
let authDirty = false
async function saveAuthHeader() {
  // 读取失败时本地没有可信的服务端状态，先挡掉，避免盲写
  if (!authSettings.value) {
    ElMessage.warning(t('settings.saveBlockedNotLoaded'))
    return
  }
  if (authSaving.value) {
    authDirty = true
    return
  }
  authSaving.value = true
  try {
    let s = authSettings.value
    do {
      authDirty = false
      s = await saveAuthHeaderName(authHeaderName.value)
    } while (authDirty)
    authSettings.value = s
    authHeaderName.value = s.header_name
    ElMessage.success(t('settings.authSaved'))
  } catch (e: any) {
    ElMessage.error(t('settings.authSaveFailed', { msg: e?.message || '' }))
    authHeaderName.value = authSettings.value.header_name // 回滚到服务端生效值
  } finally {
    authSaving.value = false
  }
}

// confirmRegenerateSecret 重新生成签名密钥。后果不可逆（全部已签发的
// API Key 重启后立即失效），必须先经警告型二次确认。
async function confirmRegenerateSecret() {
  try {
    await ElMessageBox.confirm(
      t('settings.authRegenerateConfirmBody'),
      t('settings.authRegenerateConfirmTitle'),
      {
        confirmButtonText: t('settings.authRegenerate'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
  } catch {
    return // 取消
  }
  authRegenerating.value = true
  try {
    const s = await regenerateAuthSecret()
    authSettings.value = s
    authHeaderName.value = s.header_name
    ElMessage.success(t('settings.authRegenerateSuccess'))
  } catch (e: any) {
    ElMessage.error(t('settings.authRegenerateFailed', { msg: e?.message || '' }))
  } finally {
    authRegenerating.value = false
  }
}

// saveVoice 保存整个分区。开关打开但未选模型时拒绝保存，并从 store 回滚，
// 避免本地状态与服务端脱节（否则会存下一个话筒一按就报错的状态）。
// 保存成功不弹提示，与通用面板的语言、外观行为一致。
async function saveVoice() {
  if (voice.value.enabled && !voice.value.model) {
    ElMessage.warning(t('settings.voiceModelRequired'))
    voice.value = { ...voiceStore.settings }
    return
  }
  voiceSaving.value = true
  try {
    // 写 store 而非直接调接口：成功后聊天输入框共享同一份状态，话筒随即显示或隐藏
    await voiceStore.save(voice.value)
  } catch (e: any) {
    ElMessage.error(t('settings.voiceSaveFailed', { msg: e?.message || '' }))
    await loadVoice()
  } finally {
    voiceSaving.value = false
  }
}

// 外观三选一卡片配置。
const themeCards: { value: ThemeMode; icon: typeof Sunny; labelKey: string }[] = [
  { value: 'light', icon: Sunny, labelKey: 'settings.light' },
  { value: 'dark', icon: Moon, labelKey: 'settings.dark' },
  { value: 'auto', icon: Monitor, labelKey: 'settings.system' },
]
// 语言下拉选项：各用母语名，不随界面翻译。
const langOptions: { label: string; value: Lang }[] = [
  { label: '中文', value: 'zh-cn' },
  { label: 'English', value: 'en' },
]

const tools = ref<ToolsResp | null>(null)
const agents = ref<AgentInfo[]>([])
// 运行环境信息（工作目录/数据库类型/日志目录），来自 /health 的 environment 检查项
const envInfo = ref<{ home_dir: string; database: string; log_dir: string } | null>(null)
const loading = ref(false)
const loadedOnce = ref(false)

// 主 Agent 哨兵：与 ChatInput 一致，用非空 'groot' 而非空串。
// el-select 把空串当作“未选中”而回落到 placeholder；用非空值才能正确显示选中态。
// 后端把 X-Agent-Name=groot 视为等价于不传（即主 Agent）。
const MAIN_AGENT = 'groot'

const themeMode = computed<ThemeMode>({
  get: () => mode.value,
  set: (v) => theme.setMode(v),
})

const language = computed<Lang>({
  get: () => locale.value,
  set: (v) => langStore.setLocale(v),
})

// 账户：修改密码表单
const auth = useAuthStore()
const oldPassword = ref('')
const newPassword = ref('')
const confirmNewPassword = ref('')
const changingPassword = ref(false)

async function handleChangePassword() {
  if (!oldPassword.value || !newPassword.value || !confirmNewPassword.value) {
    ElNotification.warning({ title: t('password.notifyTitle'), message: t('password.needAllFields') })
    return
  }
  if (newPassword.value.length < 8) {
    ElNotification.warning({ title: t('password.notifyTitle'), message: t('password.tooShort') })
    return
  }
  if (newPassword.value !== confirmNewPassword.value) {
    ElNotification.warning({ title: t('password.notifyTitle'), message: t('password.mismatch') })
    return
  }
  changingPassword.value = true
  try {
    await auth.changePassword(oldPassword.value, newPassword.value)
    ElNotification.success({ title: t('password.notifyTitle'), message: t('password.success') })
    oldPassword.value = ''
    newPassword.value = ''
    confirmNewPassword.value = ''
  } catch (e) {
    let message: string
    if (e instanceof ApiError && e.status === 401) {
      message = t('password.wrongOldPassword')
    } else {
      message = e instanceof Error ? e.message : t('password.failed')
    }
    ElNotification.error({ title: t('password.notifyTitle'), message })
  } finally {
    changingPassword.value = false
  }
}

// 通用面板的运行环境行：标题/描述 + 右侧值，与语言行同样的 .row 布局
const envRows = computed(() =>
  envInfo.value
    ? [
      { key: 'workDir', titleKey: 'settings.workDir', descKey: 'settings.workDirDesc', value: envInfo.value.home_dir },
      { key: 'dbType', titleKey: 'settings.dbType', descKey: 'settings.dbTypeDesc', value: envInfo.value.database },
      { key: 'logDir', titleKey: 'settings.logDir', descKey: 'settings.logDirDesc', value: envInfo.value.log_dir },
    ]
    : []
)

// 后端主 Agent 路径下会把内置工具（如 call_agent）合成为 "_builtin" 分组，
// 设置页只展示真实配置的 MCP，展示时过滤掉该合成分组。
const BUILTIN_GROUP = '_builtin'

const toolGroups = computed(() =>
  tools.value
    ? Object.entries(tools.value)
      .filter(([name]) => name !== BUILTIN_GROUP)
      .map(([name, g]) => ({ name, ...g }))
    : []
)

// 打开时加载 Agent 列表与运行环境信息。
async function ensureLoaded() {
  await meta.load()
  if (loadedOnce.value) return
  loading.value = true
  try {
    const [a, h] = await Promise.all([
      api.get<AgentsResp>('/web/agents').catch(() => null),
      api.get<HealthResp>('/web/health').catch(() => null),
    ])
    agents.value = a?.agents || []
    envInfo.value = h?.checks?.environment?.info || null
    loadedOnce.value = true
  } finally {
    loading.value = false
  }
}

watch(
  () => props.show,
  (v) => {
    if (v) {
      void ensureLoaded()
      void loadVoice()
      void loadRuntime()
      void loadSenders()
      void loadAuthSettings()
    }
  }
)

// Agent 定义查看弹窗：点击卡片「查看」按钮时实时拉取该 Agent 的 md 文件原文。
const defShow = ref(false)
const defLoading = ref(false)
const defName = ref('')
const defFile = ref('')
const defContent = ref('')
const defError = ref('')

async function openAgentDef(a: AgentInfo) {
  defName.value = a.name
  defFile.value = ''
  defContent.value = ''
  defError.value = ''
  defShow.value = true
  defLoading.value = true
  try {
    const resp = await api.get<AgentDefinitionResp>(
      `/web/agents/${encodeURIComponent(a.name)}/definition`
    )
    defFile.value = resp.file
    defContent.value = resp.content
  } catch (e) {
    defError.value = e instanceof Error ? e.message : t('settings.agentDefLoadFail')
  } finally {
    defLoading.value = false
  }
}

// Agent Skills 查看弹窗：展示该 Agent 的 skills 列表（数据来自 /web/agents，无需再请求）。
const skillsShow = ref(false)
const skillsAgent = ref<AgentInfo | null>(null)

function openAgentSkills(a: AgentInfo) {
  skillsAgent.value = a
  skillsShow.value = true
}

// Agent MCP 工具查看弹窗：打开时按 Agent 实时拉取 /web/tools。
// 主 Agent（groot）不传 header；子 Agent 通过 X-Agent-Name 指定。
const toolsShow = ref(false)
const toolsLoading = ref(false)
const toolsAgentName = ref('')

async function openAgentTools(a: AgentInfo) {
  toolsAgentName.value = a.name
  tools.value = null
  toolsShow.value = true
  toolsLoading.value = true
  const headers = a.name !== MAIN_AGENT ? { 'X-Agent-Name': a.name } : undefined
  try {
    tools.value = await api.get<ToolsResp>('/web/tools', headers).catch(() => null)
  } finally {
    toolsLoading.value = false
  }
}
</script>

<template>
  <el-dialog :model-value="show" :title="t('settings.title')" width="750px" align-center class="settings-dialog"
    @update:model-value="emit('update:show', $event)">
    <div class="settings-body">
      <div class="settings-menu">
        <button v-for="o in menuOptions" :key="o.key" type="button" class="menu-item"
          :class="{ active: section === o.key }" @click="section = o.key">
          {{ o.label }}
        </button>
      </div>
      <div class="settings-content">
        <!-- 通用 -->
        <div v-if="section === 'general'" class="general-panel">
          <div class="row">
            <div class="row-label">
              <div class="label-title">{{ t('settings.language') }}</div>
            </div>
            <el-select v-model="language" style="width: 160px">
              <el-option v-for="o in langOptions" :key="o.value" :label="o.label" :value="o.value" />
            </el-select>
          </div>
          <div class="appearance-block">
            <div class="label-title">{{ t('settings.appearance') }}</div>
            <div class="label-desc">{{ t('settings.appearanceDesc') }}</div>
            <div class="theme-cards">
              <button v-for="c in themeCards" :key="c.value" type="button" class="theme-card"
                :class="{ active: themeMode === c.value }" @click="themeMode = c.value">
                <el-icon class="theme-card-icon">
                  <component :is="c.icon" />
                </el-icon>
                <span>{{ t(c.labelKey) }}</span>
              </button>
            </div>
          </div>
          <!-- 语音输入：影响聊天页话筒按钮的行为，属于界面交互偏好，
               故放在通用而非配置分区（后者承载 Agent 运行参数）。
               其中 model 同时被对外的 /audio/transcriptions 用作缺省模型。 -->
          <div class="config-group">
            <div class="group-title">{{ t('settings.configVoice') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.voiceModel') }}</div>
                <div class="label-desc">{{ t('settings.voiceModelDesc') }}</div>
              </div>
              <el-select
                v-model="voice.model"
                style="width: 220px"
                clearable
                :placeholder="t('settings.voiceModelPlaceholder')"
                @change="saveVoice"
              >
                <el-option
                  v-for="o in voiceModelOptions"
                  :key="o.value"
                  :label="o.label"
                  :value="o.value"
                />
              </el-select>
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.voiceEnabled') }}</div>
                <div class="label-desc">{{ t('settings.voiceEnabledDesc') }}</div>
              </div>
              <el-switch v-model="voice.enabled" :loading="voiceSaving" @change="saveVoice" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.voiceAutoSend') }}</div>
                <div class="label-desc">{{ t('settings.voiceAutoSendDesc') }}</div>
              </div>
              <el-switch v-model="voice.auto_send" :loading="voiceSaving" @change="saveVoice" />
            </div>
          </div>

          <!-- 运行环境：工作目录 / 数据库类型 / 日志目录（只读展示） -->
          <div v-for="r in envRows" :key="r.key" class="row env-row">
            <div class="row-label">
              <div class="label-title">{{ t(r.titleKey) }}</div>
              <div class="label-desc">{{ t(r.descKey) }}</div>
            </div>
            <span class="mono env-value">{{ r.value }}</span>
          </div>
        </div>

        <!-- 配置：Agent 运行参数，按分类分组，后续分类在此追加同构的 config-group -->
        <div v-else-if="section === 'config'" class="config-panel">
          <div class="config-group">
            <div class="group-title">{{ t('settings.configMemory') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.historyWindow') }}</div>
                <div class="label-desc">{{ t('settings.historyWindowDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.memory.history_window"
                :min="runtimeLimits.historyWindow.min"
                :max="runtimeLimits.historyWindow.max"
                :step="1"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configReact') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.maxIterations') }}</div>
                <div class="label-desc">{{ t('settings.maxIterationsDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.react.max_iterations"
                :min="runtimeLimits.maxIterations.min"
                :max="runtimeLimits.maxIterations.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.stepTimeout') }}</div>
                <div class="label-desc">{{ t('settings.stepTimeoutDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.react.step_timeout"
                :min="runtimeLimits.stepTimeout.min"
                :max="runtimeLimits.stepTimeout.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.errorRetry') }}</div>
                <div class="label-desc">{{ t('settings.errorRetryDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.react.error_retry"
                :min="runtimeLimits.errorRetry.min"
                :max="runtimeLimits.errorRetry.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configSubAgent') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.maxConcurrency') }}</div>
                <div class="label-desc">{{ t('settings.maxConcurrencyDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.subagent.max_concurrency"
                :min="runtimeLimits.subAgentConcurrency.min"
                :max="runtimeLimits.subAgentConcurrency.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.execTimeout') }}</div>
                <div class="label-desc">{{ t('settings.execTimeoutDesc') }}</div>
              </div>
              <el-input
                v-model="runtime.subagent.exec_timeout"
                style="width: 140px"
                placeholder="5m"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.maxTaskLength') }}</div>
                <div class="label-desc">{{ t('settings.maxTaskLengthDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.subagent.max_task_length"
                :min="runtimeLimits.subAgentTextLength.min"
                :max="runtimeLimits.subAgentTextLength.max"
                :step="500"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.maxResultLength') }}</div>
                <div class="label-desc">{{ t('settings.maxResultLengthDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.subagent.max_result_length"
                :min="runtimeLimits.subAgentTextLength.min"
                :max="runtimeLimits.subAgentTextLength.max"
                :step="500"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configAttachment') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.attachMaxSize') }}</div>
                <div class="label-desc">{{ t('settings.attachMaxSizeDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.attachment.max_size"
                :min="runtimeLimits.attachmentSize.min"
                :max="runtimeLimits.attachmentSize.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.attachMaxTotalSize') }}</div>
                <div class="label-desc">{{ t('settings.attachMaxTotalSizeDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.attachment.max_total_size"
                :min="runtimeLimits.attachmentSize.min"
                :max="runtimeLimits.attachmentSize.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.attachMaxCount') }}</div>
                <div class="label-desc">{{ t('settings.attachMaxCountDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.attachment.max_count"
                :min="runtimeLimits.attachmentCount.min"
                :max="runtimeLimits.attachmentCount.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.attachAllowedTypes') }}</div>
                <div class="label-desc">{{ t('settings.attachAllowedTypesDesc') }}</div>
              </div>
              <el-input
                v-model="allowedTypesText"
                style="width: 260px"
                :placeholder="t('settings.attachAllowedTypesPlaceholder')"
                @change="saveRuntime"
              />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configRateLimit') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitEnabled') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitEnabledDesc') }}</div>
              </div>
              <el-switch v-model="runtime.rate_limit.enabled" :loading="runtimeSaving" @change="saveRuntime" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitDefaultQps') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitDefaultQpsDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.rate_limit.default_qps"
                :min="runtimeLimits.rateLimitQPS.min"
                :max="runtimeLimits.rateLimitQPS.max"
                :precision="1"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitDefaultConcurrency') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitDefaultConcurrencyDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.rate_limit.default_concurrency"
                :min="runtimeLimits.rateLimitConcurrency.min"
                :max="runtimeLimits.rateLimitConcurrency.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitGlobalQps') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitGlobalQpsDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.rate_limit.global_qps"
                :min="runtimeLimits.rateLimitQPS.min"
                :max="runtimeLimits.rateLimitQPS.max"
                :precision="1"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitGlobalConcurrency') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitGlobalConcurrencyDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.rate_limit.global_concurrency"
                :min="runtimeLimits.rateLimitConcurrency.min"
                :max="runtimeLimits.rateLimitConcurrency.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configAuth') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.authHeaderName') }}</div>
                <div class="label-desc">{{ t('settings.authHeaderNameDesc') }}</div>
              </div>
              <el-input
                v-model="authHeaderName"
                style="width: 220px"
                :placeholder="authSettings?.header_name_default || 'X-API-Key'"
                @change="saveAuthHeader"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.authSecret') }}</div>
                <div class="label-desc">{{ t('settings.authSecretDesc') }}</div>
              </div>
              <div class="auth-secret">
                <span class="mono">
                  {{ authSettings?.secret_set ? authSettings.secret_masked : t('settings.authSecretNotSet') }}
                </span>
                <el-button :loading="authRegenerating" @click="confirmRegenerateSecret">
                  {{ t('settings.authRegenerate') }}
                </el-button>
              </div>
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configSchedule') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.scheduleEnabled') }}</div>
                <div class="label-desc">{{ t('settings.scheduleEnabledDesc') }}</div>
              </div>
              <el-switch v-model="runtime.schedule.enabled" :loading="runtimeSaving" @change="saveRuntime" />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configWebhook') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.webhookUrl') }}</div>
                <div class="label-desc">{{ t('settings.webhookUrlDesc') }}</div>
              </div>
              <el-input
                v-model="senders.senders.webhook.url"
                style="width: 320px"
                placeholder="https://"
                @change="saveSenders"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.webhookEnabled') }}</div>
                <div class="label-desc">{{ t('settings.webhookEnabledDesc') }}</div>
              </div>
              <el-switch v-model="senders.senders.webhook.enabled" :loading="sendersSaving" @change="saveSenders" />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configEmail') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpHost') }}</div>
                <div class="label-desc">{{ t('settings.smtpHostDesc') }}</div>
              </div>
              <el-input v-model="senders.senders.email.smtp_host" style="width: 220px" @change="saveSenders" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpPort') }}</div>
                <div class="label-desc">{{ t('settings.smtpPortDesc') }}</div>
              </div>
              <el-input-number
                v-model="senders.senders.email.smtp_port"
                :min="senderLimits.smtpPort.min"
                :max="senderLimits.smtpPort.max"
                controls-position="right"
                style="width: 140px"
                @change="saveSenders"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpUsername') }}</div>
                <div class="label-desc">{{ t('settings.smtpUsernameDesc') }}</div>
              </div>
              <el-input v-model="senders.senders.email.username" style="width: 220px" @change="saveSenders" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpPassword') }}</div>
                <div class="label-desc">{{ t('settings.smtpPasswordDesc') }}</div>
              </div>
              <el-input
                v-model="senders.senders.email.password"
                type="password"
                show-password
                autocomplete="new-password"
                style="width: 220px"
                :placeholder="smtpPasswordSet ? t('settings.smtpPasswordKeepHint') : t('settings.smtpPasswordUnsetHint')"
                @change="saveSenders"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpFrom') }}</div>
                <div class="label-desc">{{ t('settings.smtpFromDesc') }}</div>
              </div>
              <el-input v-model="senders.senders.email.from" style="width: 220px" @change="saveSenders" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.emailEnabled') }}</div>
                <div class="label-desc">{{ t('settings.emailEnabledDesc') }}</div>
              </div>
              <el-switch v-model="senders.senders.email.enabled" :loading="sendersSaving" @change="saveSenders" />
            </div>
          </div>
        </div>

        <!-- 账户：修改密码 -->
        <div v-else-if="section === 'account'" class="account-panel">
          <div class="account-user">
            <div class="label-title">{{ t('password.currentUser') }}</div>
            <span class="account-username">{{ auth.username || '-' }}</span>
          </div>
          <div class="label-desc password-title">{{ t('password.desc') }}</div>
          <el-form label-position="top" class="password-form" @submit.prevent="handleChangePassword">
            <el-form-item :label="t('password.oldPassword')">
              <el-input v-model="oldPassword" type="password" show-password :placeholder="t('password.oldPassword')" />
            </el-form-item>
            <el-form-item :label="t('password.newPassword')">
              <el-input v-model="newPassword" type="password" show-password
                :placeholder="t('password.newPasswordHint')" />
            </el-form-item>
            <el-form-item :label="t('password.confirmPassword')">
              <el-input v-model="confirmNewPassword" type="password" show-password
                :placeholder="t('password.confirmPassword')" @keyup.enter="handleChangePassword" />
            </el-form-item>
            <el-button type="primary" :loading="changingPassword" @click="handleChangePassword">
              {{ t('password.submit') }}
            </el-button>
          </el-form>
        </div>

        <!-- 模型 -->
        <div v-else-if="section === 'models'">
          <ModelsPanel />
        </div>

        <!-- API Keys -->
        <div v-else-if="section === 'apikeys'">
          <ApiKeysPanel />
        </div>

        <!-- 集群管理 -->
        <div v-else-if="section === 'cluster'">
          <ClusterPanel />
        </div>

        <!-- Agents：卡片网格，每卡三个按钮分别弹窗展示定义 md 原文 / Skills / MCP 工具 -->
        <div v-else-if="section === 'agents'">
          <div v-loading="loading">
            <div class="agent-grid">
              <div v-for="a in agents" :key="a.name" class="agent-card">
                <div class="agent-card-head">
                  <span class="agent-card-title">{{ a.name }}</span>
                  <el-tag v-if="a.name === MAIN_AGENT" size="small" effect="plain" round class="agent-card-tag">
                    {{ t('settings.default') }}
                  </el-tag>
                </div>
                <div class="agent-card-desc">{{ a.description }}</div>
                <div class="agent-card-id mono">{{ a.name }}</div>
                <div class="agent-card-footer">
                  <!-- 原生 title 提示的出现延迟由浏览器固定（约 1s），改用 el-tooltip 缩短到 200ms -->
                  <el-tooltip :content="t('settings.viewAgentDef')" :show-after="200" placement="top">
                    <button type="button" class="agent-icon-btn" @click="openAgentDef(a)">
                      <el-icon>
                        <Document />
                      </el-icon>
                    </button>
                  </el-tooltip>
                  <el-tooltip :content="t('settings.viewAgentSkills')" :show-after="200" placement="top">
                    <button type="button" class="agent-icon-btn" @click="openAgentSkills(a)">
                      <el-icon>
                        <BoltIcon />
                      </el-icon>
                    </button>
                  </el-tooltip>
                  <el-tooltip :content="t('settings.viewAgentTools')" :show-after="200" placement="top">
                    <button type="button" class="agent-icon-btn" @click="openAgentTools(a)">
                      <el-icon>
                        <WrenchIcon />
                      </el-icon>
                    </button>
                  </el-tooltip>
                </div>
              </div>
            </div>
            <el-empty v-if="!loading && !agents.length" :description="t('settings.noAgents')" :image-size="60" />
          </div>
        </div>
      </div>
    </div>

    <!-- Agent 定义查看弹窗（嵌套于设置弹窗之上） -->
    <el-dialog v-model="defShow" :title="t('settings.viewAgentTitle', { name: defName })" width="720px" align-center
      append-to-body class="agent-def-dialog">
      <div class="def-sub">{{ t('settings.agentDefFile', { file: defFile || 'agent.md' }) }}</div>
      <div v-loading="defLoading" class="def-box">
        <div v-if="defError" class="def-error">{{ defError }}</div>
        <pre v-else class="def-content">{{ defContent }}</pre>
      </div>
      <template #footer>
        <el-button round @click="defShow = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>

    <!-- Agent Skills 查看弹窗（嵌套于设置弹窗之上） -->
    <el-dialog v-model="skillsShow" :title="t('settings.agentSkillsTitle', { name: skillsAgent?.name || '' })"
      width="720px" align-center append-to-body class="agent-def-dialog">
      <div class="skills-box">
        <div v-for="s in skillsAgent?.skills || []" :key="s.name" class="skill-entry">
          <div class="skill-entry-name">{{ s.name }}</div>
          <div class="skill-entry-desc">{{ s.description }}</div>
        </div>
        <el-empty v-if="!(skillsAgent?.skills?.length)" :description="t('settings.noSkills')" :image-size="60" />
      </div>
      <template #footer>
        <el-button round @click="skillsShow = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>

    <!-- Agent MCP 工具查看弹窗（嵌套于设置弹窗之上） -->
    <el-dialog v-model="toolsShow" :title="t('settings.agentToolsTitle', { name: toolsAgentName })" width="720px"
      align-center append-to-body class="agent-def-dialog">
      <div v-loading="toolsLoading" class="skills-box tools-box">
        <div v-for="g in toolGroups" :key="g.name" class="tool-group">
          <div class="group-title">
            <span>{{ g.name }} ({{ g.total }})</span>
            <el-tag v-if="g.type" size="small" effect="plain" round class="group-tag">
              {{ g.type }}
            </el-tag>
          </div>
          <div v-if="g.description" class="group-desc">{{ g.description }}</div>
          <div v-for="tl in g.tools" :key="tl.name" class="skill-entry">
            <div class="skill-entry-name">{{ tl.name }}</div>
            <div class="skill-entry-desc">{{ tl.description }}</div>
          </div>
        </div>
        <el-empty v-if="!toolsLoading && !toolGroups.length" :description="t('settings.noTools')" :image-size="60" />
      </div>
      <template #footer>
        <el-button round @click="toolsShow = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>
  </el-dialog>
</template>

<style scoped>
/* 高度自适应：常态 560px；浏览器窗口变矮时按视口收缩，
   预留量 = 上下各 50px 间距 + 弹窗标题栏与内边距（约 120px）。
   内容区自身 overflow-y: auto，收缩后由它出滚动条。 */
/* 撑满弹窗 body 的可用高度；内容超出时由 .settings-content 自身滚动 */
.settings-body {
  display: flex;
  height: 100%;
}

.settings-menu {
  width: 160px;
  flex-shrink: 0;
  overflow-y: auto;
  padding: 4px 12px 4px 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

/* 菜单项：普通按钮，悬浮/选中同为圆角灰底（参照稿风格），无分隔竖线 */
.menu-item {
  display: block;
  width: 100%;
  padding: 10px 14px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--el-text-color-primary);
  font-size: 14px;
  text-align: left;
  cursor: pointer;
  transition: background-color 0.15s;
}

.menu-item:hover,
.menu-item.active {
  background: var(--el-fill-color, rgba(127, 127, 127, 0.12));
}

.settings-content {
  flex: 1;
  min-width: 0;
  padding: 16px;
  overflow-y: auto;
  /* Firefox：细滚动条，轨道透明只留滑块 */
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker, rgba(127, 127, 127, 0.35)) transparent;
}

/* WebKit：轨道透明，只显示圆角滑块 */
.settings-content::-webkit-scrollbar,
.settings-menu::-webkit-scrollbar {
  width: 6px;
}

.settings-content::-webkit-scrollbar-track,
.settings-menu::-webkit-scrollbar-track {
  background: transparent;
}

.settings-content::-webkit-scrollbar-thumb,
.settings-menu::-webkit-scrollbar-thumb {
  background: var(--el-border-color-darker, rgba(127, 127, 127, 0.35));
  border-radius: 3px;
}

.settings-menu {
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker, rgba(127, 127, 127, 0.35)) transparent;
}

.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 0;
}

/* 通用面板与配置分组：每行之间加分割线，末行不带 */
.general-panel>div:not(.config-group),
.config-group>.row {
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}

.general-panel>div:not(.config-group):last-child,
.config-group>.row:last-child {
  border-bottom: none;
}

/* 配置分组以卡片承载：边框把同一分类的若干行圈成一块，
   使「分类」这一层级不再只靠标题的字重区分。
   通用面板内嵌的语音分组共用这套外观，两处分区观感一致。 */
.config-group {
  border: 1px solid var(--el-border-color-lighter, rgba(127, 127, 127, 0.2));
  border-radius: 10px;
  padding: 4px 16px 6px;
  background: var(--el-bg-color-overlay, transparent);
}

.config-group+.config-group {
  margin-top: 16px;
}

/* 卡片内的组标题：撑满卡片宽度并以分割线收尾，与下方各行区隔。
   负的左右外边距让分割线贴到卡片内壁，而非缩在内边距里。 */
.config-group>.group-title {
  margin: 0 -16px 0;
  padding: 12px 16px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
  font-size: 0.95em;
  opacity: 1;
}

/* 通用面板中语音卡片与上方外观块的间距。
   卡片自带边框，靠内边距分隔已不够，改用外边距。 */
.general-panel>.config-group {
  margin-top: 16px;
}

.label-title {
  font-weight: 500;
}

.label-desc {
  font-size: 0.82em;
  opacity: 0.6;
  margin-top: 2px;
}

.appearance-block {
  padding: 16px 0;
}

.account-user {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 0;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}

/* 当前用户名：与左侧标题同级的视觉分量，不做缩小弱化 */
.account-username {
  font-size: 1em;
  font-weight: 600;
}

.password-title {
  margin-top: 16px;
}

.password-form {
  margin-top: 12px;
  max-width: 320px;
}

.theme-cards {
  display: flex;
  gap: 12px;
  margin-top: 12px;
}

.theme-card {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 16px 0;
  border: 1px solid var(--el-border-color);
  border-radius: 8px;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font-size: 0.9em;
  transition: border-color 0.2s, background-color 0.2s;
}

.theme-card:hover {
  border-color: var(--el-color-primary-light-5);
}

.theme-card.active {
  border-color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
  color: var(--el-color-primary);
}

.theme-card-icon {
  font-size: 22px;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.85em;
  opacity: 0.7;
}

/* 运行环境行：描述与值两端对齐，长路径右对齐并允许折行 */
.env-row {
  align-items: flex-start;
  gap: 16px;
}

.env-row .row-label {
  flex-shrink: 0;
}

.env-value {
  max-width: 55%;
  text-align: right;
  word-break: break-all;
  padding-top: 2px;
}

/* 认证分组的密钥行：脱敏值与「重新生成」按钮同排右对齐 */
.auth-secret {
  display: flex;
  align-items: center;
  gap: 12px;
}

.tool-group {
  margin-bottom: 16px;
}

.tool-group:last-of-type {
  margin-bottom: 0;
}

/* 多个 MCP 分组之间用分割线区隔 */
.tool-group+.tool-group {
  border-top: 1px solid var(--el-border-color-lighter, rgba(127, 127, 127, 0.2));
  padding-top: 14px;
}

.group-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  font-size: 0.9em;
  opacity: 0.7;
  margin-bottom: 4px;
}

/* MCP 定义中的描述：分组标题下方一行，弱化显示 */
.group-desc {
  font-size: 0.9em;
  opacity: 0.65;
  margin-bottom: 4px;
}

/* ---- Agents 卡片网格 ---- */
.agent-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 14px;
}

.agent-card {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--el-border-color);
  border-radius: 12px;
  padding: 16px 16px 8px;
  transition: border-color 0.2s;
}

.agent-card:hover {
  border-color: var(--el-border-color-darker, rgba(127, 127, 127, 0.45));
}

.agent-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.agent-card-title {
  font-size: 1.05em;
  font-weight: 600;
}

.agent-card-tag {
  flex-shrink: 0;
}

.agent-card-desc {
  flex: 1;
  font-size: 0.85em;
  opacity: 0.7;
  line-height: 1.6;
  margin-top: 8px;
  /* 描述过长时截断，保持卡片高度整齐 */
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.agent-card-id {
  margin-top: 10px;
  opacity: 0.45;
}

.agent-card-footer {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
  border-top: 1px solid rgba(127, 127, 127, 0.15);
  margin-top: 10px;
  padding-top: 6px;
}

.agent-icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--el-text-color-regular);
  font-size: 16px;
  cursor: pointer;
  transition: background-color 0.15s;
}

.agent-icon-btn:hover {
  background: var(--el-fill-color, rgba(127, 127, 127, 0.12));
}

/* ---- Agent 定义查看弹窗内容 ---- */
.def-sub {
  font-size: 0.95em;
  margin-bottom: 12px;
}

.def-box {
  min-height: 120px;
}

.def-content {
  margin: 0;
  max-height: 52vh;
  overflow: auto;
  padding: 14px 16px;
  border: 1px solid var(--el-border-color);
  border-radius: 10px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--el-text-color-primary);
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker, rgba(127, 127, 127, 0.35)) transparent;
}

.def-content::-webkit-scrollbar {
  width: 6px;
}

.def-content::-webkit-scrollbar-track {
  background: transparent;
}

.def-content::-webkit-scrollbar-thumb {
  background: var(--el-border-color-darker, rgba(127, 127, 127, 0.35));
  border-radius: 3px;
}

.def-error {
  padding: 24px 0;
  text-align: center;
  color: var(--el-color-danger);
  font-size: 0.9em;
}

/* Skills 弹窗列表区：容器与文字样式对齐定义查看弹窗（.def-content）——
   同样的边框圆角容器、等宽字体、主文字颜色，超高时独立滚动 */
.skills-box {
  max-height: 52vh;
  overflow-y: auto;
  padding: 14px 16px;
  border: 1px solid var(--el-border-color);
  border-radius: 10px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  line-height: 1.7;
  color: var(--el-text-color-primary);
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker, rgba(127, 127, 127, 0.35)) transparent;
}

.skill-entry {
  padding: 8px 0;
  border-bottom: 1px solid rgba(127, 127, 127, 0.12);
}

.skill-entry:last-of-type {
  border-bottom: none;
}

.skill-entry-name {
  font-weight: 600;
}

.skill-entry-desc {
  margin-top: 2px;
  white-space: pre-wrap;
  word-break: break-word;
}

/* MCP 工具弹窗：加载中内容为空时保证 loading 遮罩有可视高度 */
.tools-box {
  min-height: 120px;
}

.skills-box::-webkit-scrollbar {
  width: 6px;
}

.skills-box::-webkit-scrollbar-track {
  background: transparent;
}

.skills-box::-webkit-scrollbar-thumb {
  background: var(--el-border-color-darker, rgba(127, 127, 127, 0.35));
  border-radius: 3px;
}
</style>

<!-- 弹窗根元素在 scoped 作用域外，用非 scoped 规则限定其最大高度：
     极矮窗口下也保证上下各留 50px，超出部分由内容区滚动。 -->
<style>
.settings-dialog {
  /* 高度恒为视口高度减去上下各 50px 间距，随窗口尺寸实时变化 */
  height: calc(100vh - 100px);
  margin-top: 0;
  margin-bottom: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  /* 圆角外框；overflow: hidden 已保证内部内容不会溢出直角 */
  border-radius: 16px;
}

.settings-dialog .el-dialog__body {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

/* Agent 定义查看弹窗：圆角外框，与设置弹窗风格一致 */
.agent-def-dialog {
  border-radius: 16px;
}
</style>
