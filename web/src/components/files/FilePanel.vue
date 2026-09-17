<!-- 右侧抽屉容器：工具栏（scaffold ×3，创建对话框见 ScaffoldDialog / 全屏 / 收起）、路径栏、树/预览切换、宽度拖动。 -->
<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { FullScreen, Refresh, Sort } from '@element-plus/icons-vue'
import PanelIcon from './PanelIcon.vue'
import BoltIcon from './BoltIcon.vue'
import WrenchIcon from './WrenchIcon.vue'
import FileTree from './FileTree.vue'
import FilePreview from './FilePreview.vue'
import FileEditorModal from './FileEditorModal.vue'
import SyncDialog from './SyncDialog.vue'
import ScaffoldDialog from './ScaffoldDialog.vue'
import { useFilesStore, clampWidth } from '../../stores/files'

const { t } = useI18n()
const files = useFilesStore()

// —— 宽度拖动 ——
let dragStartX = 0
let dragStartW = 0

function onDragStart(e: MouseEvent) {
  dragStartX = e.clientX
  dragStartW = files.width
  document.addEventListener('mousemove', onDragMove)
  document.addEventListener('mouseup', onDragEnd)
  document.body.style.userSelect = 'none'
}
function onDragMove(e: MouseEvent) {
  files.width = clampWidth(dragStartW + (dragStartX - e.clientX))
}
function onDragEnd() {
  document.removeEventListener('mousemove', onDragMove)
  document.removeEventListener('mouseup', onDragEnd)
  document.body.style.userSelect = ''
}
onBeforeUnmount(onDragEnd)

// —— scaffold 创建 ——
type ScaffoldKind = 'skill' | 'mcp' | 'agent'

// scaffold 类型 → 资源目录名（成功后先展开相关目录再刷新树，便于定位新条目）
const SCAFFOLD_DIR: Record<ScaffoldKind, string> = {
  skill: 'skills',
  mcp: 'mcp',
  agent: 'subagents',
}

const scaffoldOpen = ref(false)
const scaffoldKind = ref<ScaffoldKind>('skill')
const scaffoldTitle = ref('')
const scaffoldDefaultAgent = ref('')

// 路径位于某子 Agent 内（subagents/<name>/...）时返回该子 Agent 名，否则 ''。
function agentOfPath(p: string): string {
  const m = /^subagents\/([^/]+)\//.exec(p)
  return m ? m[1] : ''
}

// 打开创建对话框；skill / mcp 按当前预览或编辑中的文件所在子 Agent 预选「所属 Agent」。
function scaffold(kind: ScaffoldKind, title: string) {
  scaffoldKind.value = kind
  scaffoldTitle.value = title
  scaffoldDefaultAgent.value =
    kind === 'agent' ? '' : agentOfPath(files.previewPath || files.editorPath)
  scaffoldOpen.value = true
}

// 创建成功：展开新条目所在目录链（子 Agent 资源在 subagents/<agent>/<dir> 下），刷新树并直接进编辑。
function onScaffolded(path: string, agent: string) {
  const dir = SCAFFOLD_DIR[scaffoldKind.value]
  const keys = agent ? ['subagents', `subagents/${agent}`, `subagents/${agent}/${dir}`] : [dir]
  for (const k of keys) if (!files.expandedKeys.includes(k)) files.expandedKeys.push(k)
  files.refresh()
  files.previewPath = ''
  files.editorPath = path
}

// —— 配置同步 ——
const syncOpen = ref(false)
const syncScope = ref<string | undefined>(undefined) // undefined = 全量同步
// 同步功能未启用（后端返回 sync_disabled）时隐藏所有同步入口（设计文档 §1.8/§1.9）。
// 会话内记忆即可：服务端模式运行期不变，页面刷新后重新探测。
const syncDisabled = ref(false)

function openSync(scope?: string) {
  syncScope.value = scope
  syncOpen.value = true
}

// pull 会覆盖本地文件：刷新文件树，并关闭已打开的编辑器与预览，
// 避免用户在陈旧内容上保存、把刚拉取的内容覆盖回去（设计文档 §1.7）。
// push 只写数据库、本地文件不变，无需任何刷新，故不监听 @pushed。
function onPulled() {
  files.editorPath = ''
  files.previewPath = ''
  files.refresh()
}
</script>

<template>
  <aside
    class="file-panel"
    :class="{ fullscreen: files.fullscreen }"
    :style="files.fullscreen ? undefined : { width: files.width + 'px' }"
  >
    <div v-if="!files.fullscreen" class="drag-handle" @mousedown="onDragStart"></div>

    <header class="panel-head">
      <span class="panel-title">{{ t('files.title') }}</span>
      <!-- 原生 title 提示的出现延迟由浏览器固定（约 1s），改用 el-tooltip 缩短到 200ms，
           与 SettingsModal 中 Agent 卡片按钮的做法一致。 -->
      <el-tooltip :content="t('files.newSkill')" :show-after="200" placement="bottom">
        <button class="head-icon" type="button" @click="scaffold('skill', t('files.newSkill'))">
          <el-icon :size="17"><BoltIcon /></el-icon>
        </button>
      </el-tooltip>
      <el-tooltip :content="t('files.newMcp')" :show-after="200" placement="bottom">
        <button class="head-icon" type="button" @click="scaffold('mcp', t('files.newMcp'))">
          <el-icon :size="17"><WrenchIcon /></el-icon>
        </button>
      </el-tooltip>
      <el-tooltip :content="t('files.newAgent')" :show-after="200" placement="bottom">
        <button class="head-icon" type="button" @click="scaffold('agent', t('files.newAgent'))">
          <span class="head-emoji" aria-hidden="true">🤖</span>
        </button>
      </el-tooltip>
      <el-tooltip v-if="!syncDisabled" :content="t('files.sync')" :show-after="200" placement="bottom">
        <button class="head-icon" type="button" @click="openSync()">
          <el-icon :size="16"><Sort /></el-icon>
        </button>
      </el-tooltip>
      <span class="head-gap"></span>
      <el-tooltip
        :content="files.fullscreen ? t('files.exitFullscreen') : t('files.fullscreen')"
        :show-after="200"
        placement="bottom"
      >
        <button class="head-icon" type="button" @click="files.fullscreen = !files.fullscreen">
          <el-icon :size="15"><FullScreen /></el-icon>
        </button>
      </el-tooltip>
      <el-tooltip :content="t('files.collapse')" :show-after="200" placement="bottom">
        <button class="head-icon" type="button" @click="files.toggle()">
          <el-icon :size="16"><PanelIcon /></el-icon>
        </button>
      </el-tooltip>
    </header>

    <div class="path-bar">
      <span class="path-text" :title="files.homePath">{{ files.homePath || '~/.groot' }}</span>
      <button class="head-icon" type="button" :title="t('files.refresh')" @click="files.refresh()">
        <el-icon :size="14"><Refresh /></el-icon>
      </button>
    </div>

    <FileTree v-show="!files.previewPath" :sync-disabled="syncDisabled" @sync="openSync" />
    <FilePreview v-if="files.previewPath" :path="files.previewPath" />
    <FileEditorModal />
    <ScaffoldDialog
      v-model="scaffoldOpen"
      :kind="scaffoldKind"
      :title="scaffoldTitle"
      :default-agent="scaffoldDefaultAgent"
      @created="onScaffolded"
    />
    <SyncDialog v-model="syncOpen" :scope="syncScope" @pulled="onPulled"
      @disabled="syncDisabled = true" />
  </aside>
</template>

<style scoped>
.file-panel {
  flex-shrink: 0;
  height: 100vh;
  display: flex;
  flex-direction: column;
  position: relative;
  border-left: 1px solid rgba(127, 127, 127, 0.15);
  background: var(--el-bg-color);
}
.file-panel.fullscreen {
  flex: 1;
  min-width: 0;
}
.drag-handle {
  position: absolute;
  left: -3px;
  top: 0;
  bottom: 0;
  width: 6px;
  cursor: col-resize;
  z-index: 2;
}
.panel-head {
  flex-shrink: 0;
  height: 56px;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0 10px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}
.panel-title {
  font-weight: 600;
  margin-right: 8px;
}
.head-gap {
  flex: 1;
}
.head-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  opacity: 0.65;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
}
/* 技能 / MCP 工具 / Agent 三个新建按钮沿用对话区 TranscriptStep 行首的 ⚡ / 🔧 / 🤖
   语义。前两者 emoji 在系统字体下笔画偏细，改为实心 SVG（BoltIcon / WrenchIcon，17px）；
   🤖 保留 emoji，字号按 28px 按钮位取 15px，与相邻线性图标视觉等大。 */
.head-emoji {
  font-size: 15px;
  line-height: 1;
}
.head-icon:hover {
  background: rgba(127, 127, 127, 0.12);
  opacity: 0.95;
}
.path-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.12);
}
.path-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.78em;
  opacity: 0.6;
  direction: rtl; /* 路径过长时保留结尾（目录名）可见 */
  text-align: left;
}
</style>
