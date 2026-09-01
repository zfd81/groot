<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { storeToRefs } from 'pinia'
import { Sunny, Moon, Monitor } from '@element-plus/icons-vue'
import { useThemeStore, type ThemeMode } from '../../stores/theme'
import { useLanguageStore, type Lang } from '../../stores/language'
import { useMetaStore } from '../../stores/meta'
import { api } from '../../api/client'
import type { SkillsResp, ToolsResp, AgentsResp, AgentInfo, HealthResp } from '../../api/types'

const { t } = useI18n()
const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [v: boolean] }>()

const theme = useThemeStore()
const { mode } = storeToRefs(theme)
const langStore = useLanguageStore()
const { locale } = storeToRefs(langStore)
const meta = useMetaStore()
const { models, defaultModel } = storeToRefs(meta)

const section = ref<string>('general')
const menuOptions = computed(() => [
  { label: t('settings.menuGeneral'), key: 'general' },
  { label: t('settings.menuModels'), key: 'models' },
  { label: t('settings.menuAgents'), key: 'agents' },
  { label: t('settings.menuSkills'), key: 'skills' },
  { label: t('settings.menuTools'), key: 'tools' },
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

const skills = ref<SkillsResp | null>(null)
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

// Skills / MCP 工具按 Agent 维度查看：'groot' 表示主 Agent。
const filterAgent = ref(MAIN_AGENT)
const filterLoading = ref(false)
const agentFilterOptions = computed(() => [
  { label: MAIN_AGENT, value: MAIN_AGENT, isDefault: true },
  ...agents.value
    .filter((a) => a.name !== MAIN_AGENT)
    .map((a) => ({ label: a.name, value: a.name, isDefault: false })),
])

const themeMode = computed<ThemeMode>({
  get: () => mode.value,
  set: (v) => theme.setMode(v),
})

const language = computed<Lang>({
  get: () => locale.value,
  set: (v) => langStore.setLocale(v),
})

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

const toolGroups = computed(() =>
  tools.value
    ? Object.entries(tools.value).map(([name, g]) => ({ name, ...g }))
    : []
)

// 打开时加载 Agent 列表（用于筛选下拉），并首次拉取当前筛选 Agent 的 skills/tools。
async function ensureLoaded() {
  await meta.load()
  if (loadedOnce.value) return
  loading.value = true
  try {
    const [a, h] = await Promise.all([
      api.get<AgentsResp>('/agents').catch(() => null),
      api.get<HealthResp>('/health').catch(() => null),
    ])
    agents.value = a?.agents || []
    envInfo.value = h?.checks?.environment?.info || null
    await loadAgentScoped()
    loadedOnce.value = true
  } finally {
    loading.value = false
  }
}

// 按当前 filterAgent 拉取该 Agent 的 skills 与 MCP 工具。
// 'groot' = 主 Agent（等价于不传 header）；非主 Agent 则通过 X-Agent-Name 指定子 Agent。
async function loadAgentScoped() {
  filterLoading.value = true
  const headers =
    filterAgent.value && filterAgent.value !== MAIN_AGENT
      ? { 'X-Agent-Name': filterAgent.value }
      : undefined
  try {
    const [s, t] = await Promise.all([
      api.get<SkillsResp>('/skills', headers).catch(() => null),
      api.get<ToolsResp>('/tools', headers).catch(() => null),
    ])
    skills.value = s
    tools.value = t
  } finally {
    filterLoading.value = false
  }
}

watch(filterAgent, () => {
  if (loadedOnce.value) void loadAgentScoped()
})

watch(
  () => props.show,
  (v) => {
    if (v) void ensureLoaded()
  }
)
</script>

<template>
  <el-dialog :model-value="show" :title="t('settings.title')" width="750px" align-center
    class="settings-dialog" @update:model-value="emit('update:show', $event)">
    <div class="settings-body">
      <el-menu :default-active="section" class="settings-menu" @select="(k: string) => (section = k)">
        <el-menu-item v-for="o in menuOptions" :key="o.key" :index="o.key">
          {{ o.label }}
        </el-menu-item>
      </el-menu>
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

        <!-- 模型 -->
        <div v-else-if="section === 'models'">
          <div v-for="m in models" :key="m.name" class="list-item">
            <div class="item-header">
              <span class="model-name">{{ m.name }}</span>
              <el-tag v-if="m.name === defaultModel" size="small" type="primary" effect="light"
                style="margin-left: 8px">
                {{ t('settings.default') }}
              </el-tag>
            </div>
            <div class="model-field">
              <span class="field-label">{{ t('settings.apiUrl') }}</span>
              <span class="mono">{{ m.base_url }}</span>
            </div>
            <div class="model-field">
              <span class="field-label">{{ t('settings.modelName') }}</span>
              <span class="mono">{{ m.model }}</span>
            </div>
          </div>
          <el-empty v-if="!models.length" :description="t('settings.noModels')" :image-size="60" />
        </div>

        <!-- Skills -->
        <div v-else-if="section === 'skills'">
          <div class="filter-bar">
            <span class="filter-label">{{ t('settings.agentLabel') }}</span>
            <el-select v-model="filterAgent" size="small" style="width: 180px">
              <el-option v-for="o in agentFilterOptions" :key="o.value" :label="o.label" :value="o.value">
                <span class="opt-label">{{ o.label }}</span>
                <el-tag v-if="o.isDefault" size="small" type="primary" effect="light" class="opt-tag">
                  {{ t('settings.default') }}
                </el-tag>
              </el-option>
            </el-select>
          </div>
          <div v-loading="loading || filterLoading">
            <div v-for="s in skills?.skills || []" :key="s.name" class="list-item">
              <div class="item-title">{{ s.name }}</div>
              <div class="item-desc">{{ s.description }}</div>
            </div>
            <el-empty v-if="!loading && !filterLoading && !(skills?.skills?.length)"
              :description="t('settings.noSkills')" :image-size="60" />
          </div>
        </div>

        <!-- MCP 工具 -->
        <div v-else-if="section === 'tools'">
          <div class="filter-bar">
            <span class="filter-label">{{ t('settings.agentLabel') }}</span>
            <el-select v-model="filterAgent" size="small" style="width: 180px">
              <el-option v-for="o in agentFilterOptions" :key="o.value" :label="o.label" :value="o.value">
                <span class="opt-label">{{ o.label }}</span>
                <el-tag v-if="o.isDefault" size="small" type="primary" effect="light" class="opt-tag">
                  {{ t('settings.default') }}
                </el-tag>
              </el-option>
            </el-select>
          </div>
          <div v-loading="loading || filterLoading">
            <div v-for="g in toolGroups" :key="g.name" class="tool-group">
              <div class="group-title">{{ g.name }} ({{ g.total }})</div>
              <div v-for="tl in g.tools" :key="tl.name" class="list-item">
                <div class="item-title">{{ tl.name }}</div>
                <div class="item-desc">{{ tl.description }}</div>
              </div>
            </div>
            <el-empty v-if="!loading && !filterLoading && !toolGroups.length" :description="t('settings.noTools')"
              :image-size="60" />
          </div>
        </div>

        <!-- Agents -->
        <div v-else-if="section === 'agents'">
          <div v-loading="loading">
            <div v-for="a in agents" :key="a.name" class="list-item">
              <div class="item-title">{{ a.name }}</div>
              <div class="item-desc">{{ a.description }}</div>
              <div v-if="a.skills?.length" class="item-footer">
                <el-tag v-for="sk in a.skills" :key="sk.name" size="small" effect="light" style="margin-right: 6px">
                  {{ sk.name }}
                </el-tag>
              </div>
            </div>
            <el-empty v-if="!loading && !agents.length" :description="t('settings.noAgents')" :image-size="60" />
          </div>
        </div>
      </div>
    </div>
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
  width: 140px;
  flex-shrink: 0;
  border-right: 1px solid var(--el-border-color);
  overflow-y: auto;
}

.settings-content {
  flex: 1;
  min-width: 0;
  padding: 16px;
  overflow-y: auto;
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

.list-item {
  padding: 10px 0;
  border-bottom: 1px solid rgba(127, 127, 127, 0.12);
}

.item-header {
  display: flex;
  align-items: center;
}

.item-title {
  font-weight: 500;
}

.item-desc {
  font-size: 0.85em;
  opacity: 0.65;
  margin-top: 2px;
}

.item-footer {
  margin-top: 6px;
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

.model-name {
  font-weight: 600;
}

.model-field {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-top: 2px;
}

.field-label {
  font-size: 0.78em;
  opacity: 0.75;
  font-weight: 700;
  flex-shrink: 0;
  width: 4em;
}

.tool-group {
  margin-bottom: 16px;
}

.filter-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.filter-label {
  font-size: 0.85em;
  font-weight: 600;
  opacity: 0.7;
}

.group-title {
  font-weight: 600;
  font-size: 0.9em;
  opacity: 0.7;
  margin-bottom: 4px;
}

/* 下拉项：名称占满、默认标签靠右（展开时可见，选中后只显示名称） */
.opt-label {
  margin-right: 8px;
}

.opt-tag {
  float: right;
  margin-top: 6px;
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
</style>
