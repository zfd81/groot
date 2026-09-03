<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { storeToRefs } from 'pinia'
import { Sunny, Moon, Monitor, Document, Collection, Tools } from '@element-plus/icons-vue'
import { useThemeStore, type ThemeMode } from '../../stores/theme'
import { useLanguageStore, type Lang } from '../../stores/language'
import { useMetaStore } from '../../stores/meta'
import { useAuthStore } from '../../stores/auth'
import { api, ApiError } from '../../api/client'
import type { ToolsResp, AgentsResp, AgentInfo, AgentDefinitionResp, HealthResp } from '../../api/types'
import ModelsPanel from './ModelsPanel.vue'
import ApiKeysPanel from './ApiKeysPanel.vue'

const { t } = useI18n()
const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [v: boolean] }>()

const theme = useThemeStore()
const { mode } = storeToRefs(theme)
const langStore = useLanguageStore()
const { locale } = storeToRefs(langStore)
const meta = useMetaStore()

const section = ref<string>('general')
const menuOptions = computed(() => [
  { label: t('settings.menuGeneral'), key: 'general' },
  { label: t('settings.menuModels'), key: 'models' },
  { label: t('settings.menuAgents'), key: 'agents' },
  { label: t('settings.menuApiKeys'), key: 'apikeys' },
  { label: t('settings.menuAccount'), key: 'account' },
])

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
    if (v) void ensureLoaded()
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
  <el-dialog :model-value="show" :title="t('settings.title')" width="750px" align-center
    class="settings-dialog" @update:model-value="emit('update:show', $event)">
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
          <!-- 运行环境：工作目录 / 数据库类型 / 日志目录（只读展示） -->
          <div v-for="r in envRows" :key="r.key" class="row env-row">
            <div class="row-label">
              <div class="label-title">{{ t(r.titleKey) }}</div>
              <div class="label-desc">{{ t(r.descKey) }}</div>
            </div>
            <span class="mono env-value">{{ r.value }}</span>
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
              <el-input v-model="oldPassword" type="password" show-password
                :placeholder="t('password.oldPassword')" />
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
                  <button type="button" class="agent-icon-btn" :title="t('settings.viewAgentDef')"
                    @click="openAgentDef(a)">
                    <el-icon>
                      <Document />
                    </el-icon>
                  </button>
                  <button type="button" class="agent-icon-btn" :title="t('settings.viewAgentSkills')"
                    @click="openAgentSkills(a)">
                    <el-icon>
                      <Collection />
                    </el-icon>
                  </button>
                  <button type="button" class="agent-icon-btn" :title="t('settings.viewAgentTools')"
                    @click="openAgentTools(a)">
                    <el-icon>
                      <Tools />
                    </el-icon>
                  </button>
                </div>
              </div>
            </div>
            <el-empty v-if="!loading && !agents.length" :description="t('settings.noAgents')" :image-size="60" />
          </div>
        </div>
      </div>
    </div>

    <!-- Agent 定义查看弹窗（嵌套于设置弹窗之上） -->
    <el-dialog v-model="defShow" :title="t('settings.viewAgentTitle', { name: defName })" width="720px"
      align-center append-to-body class="agent-def-dialog">
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
    <el-dialog v-model="toolsShow" :title="t('settings.agentToolsTitle', { name: toolsAgentName })"
      width="720px" align-center append-to-body class="agent-def-dialog">
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

/* 通用面板：每行之间加分割线，末行不带 */
.general-panel>div {
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}

.general-panel>div:last-child {
  border-bottom: none;
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
