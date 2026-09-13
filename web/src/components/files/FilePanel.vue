<!-- 右侧抽屉容器：工具栏（scaffold ×3 / 全屏 / 收起）、路径栏、树/预览切换、宽度拖动。 -->
<script setup lang="ts">
import { onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  MagicStick, Connection, Avatar, FullScreen, Refresh,
} from '@element-plus/icons-vue'
import PanelIcon from './PanelIcon.vue'
import FileTree from './FileTree.vue'
import FilePreview from './FilePreview.vue'
import FileEditorModal from './FileEditorModal.vue'
import { filesApi } from '../../api/files'
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
const SCAFFOLD_NAME_RE = /^[\p{L}\p{N}][\p{L}\p{N}_-]{0,63}$/u

// scaffold 类型 → 顶层目录（成功后先展开该目录再刷新树，便于定位新条目）
const SCAFFOLD_DIR: Record<'skill' | 'mcp' | 'agent', string> = {
  skill: 'skills',
  mcp: 'mcp',
  agent: 'subagents',
}

async function scaffold(kind: 'skill' | 'mcp' | 'agent', title: string) {
  let name: string
  try {
    const { value } = await ElMessageBox.prompt(t('files.namePrompt'), title, {
      inputPattern: SCAFFOLD_NAME_RE,
      inputErrorMessage: t('files.nameInvalid'),
    })
    name = value.trim()
  } catch {
    return // 取消
  }
  try {
    const resp = await filesApi.scaffold(kind, name)
    const dir = SCAFFOLD_DIR[kind]
    if (!files.expandedKeys.includes(dir)) files.expandedKeys.push(dir)
    files.refresh()
    files.previewPath = ''
    files.editorPath = resp.path // 直接进编辑
  } catch (e: any) {
    ElMessage.error(e?.message || t('files.opFailed'))
  }
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
      <button class="head-icon" type="button" :title="t('files.newSkill')"
        @click="scaffold('skill', t('files.newSkill'))">
        <el-icon :size="16"><MagicStick /></el-icon>
      </button>
      <button class="head-icon" type="button" :title="t('files.newMcp')"
        @click="scaffold('mcp', t('files.newMcp'))">
        <el-icon :size="16"><Connection /></el-icon>
      </button>
      <button class="head-icon" type="button" :title="t('files.newAgent')"
        @click="scaffold('agent', t('files.newAgent'))">
        <el-icon :size="16"><Avatar /></el-icon>
      </button>
      <span class="head-gap"></span>
      <button class="head-icon" type="button"
        :title="files.fullscreen ? t('files.exitFullscreen') : t('files.fullscreen')"
        @click="files.fullscreen = !files.fullscreen">
        <el-icon :size="15"><FullScreen /></el-icon>
      </button>
      <button class="head-icon" type="button" :title="t('files.collapse')" @click="files.toggle()">
        <el-icon :size="16"><PanelIcon /></el-icon>
      </button>
    </header>

    <div class="path-bar">
      <span class="path-text" :title="files.homePath">{{ files.homePath || '~/.groot' }}</span>
      <button class="head-icon" type="button" :title="t('files.refresh')" @click="files.refresh()">
        <el-icon :size="14"><Refresh /></el-icon>
      </button>
    </div>

    <FileTree v-show="!files.previewPath" />
    <FilePreview v-if="files.previewPath" :path="files.previewPath" />
    <FileEditorModal />
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
