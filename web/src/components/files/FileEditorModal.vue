<!-- 大弹窗编辑器：CodeMirror 6 动态加载；md 文件左右分屏实时预览（可收起）。 -->
<script setup lang="ts">
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { View, Hide, Loading } from '@element-plus/icons-vue'
import MarkdownView from '../chat/MarkdownView.vue'
import { filesApi } from '../../api/files'
import { useFilesStore } from '../../stores/files'

const { t } = useI18n()
const files = useFilesStore()

const visible = computed(() => !!files.editorPath)
const isMd = computed(() => files.editorPath.toLowerCase().endsWith('.md'))
const fileName = computed(() => files.editorPath.split('/').pop() || '')

const draft = ref('')
const dirty = ref(false)
const loading = ref(false)
const saving = ref(false)
const showPreview = ref(true)
const cmHost = ref<HTMLElement | null>(null)

// CodeMirror EditorView 实例（动态加载，避免类型依赖入主包）
let view: { destroy: () => void } | null = null

watch(
  () => files.editorPath,
  async (p) => {
    destroyEditor()
    if (!p) return
    loading.value = true
    try {
      const resp = await filesApi.content(p)
      draft.value = resp.content
      dirty.value = false
      loading.value = false // 先退出 loading 分支，让 cmHost 渲染出来
      await nextTick() // 等 el-dialog 渲染出 cmHost
      await initEditor(resp.content, p)
    } catch {
      ElMessage.error(t('files.loadFailed'))
      files.editorPath = ''
    } finally {
      loading.value = false
    }
  },
  { immediate: true } // 组件带非空 editorPath 重挂载时也要加载
)

async function initEditor(doc: string, path: string) {
  const [{ EditorView, basicSetup }, langExt] = await Promise.all([
    import('codemirror'),
    languageFor(path),
  ])
  if (!cmHost.value) return
  view = new EditorView({
    doc,
    parent: cmHost.value,
    extensions: [
      basicSetup,
      ...(langExt ? [langExt] : []),
      EditorView.lineWrapping,
      EditorView.updateListener.of((u) => {
        if (u.docChanged) {
          draft.value = u.state.doc.toString()
          dirty.value = true
        }
      }),
    ],
  })
}

// 按扩展名动态加载语言包；未覆盖的扩展名无高亮
async function languageFor(path: string) {
  const ext = path.split('.').pop()?.toLowerCase()
  if (ext === 'md') return (await import('@codemirror/lang-markdown')).markdown()
  if (ext === 'yaml' || ext === 'yml') return (await import('@codemirror/lang-yaml')).yaml()
  if (ext === 'json') return (await import('@codemirror/lang-json')).json()
  return null
}

function destroyEditor() {
  view?.destroy()
  view = null
}
onBeforeUnmount(destroyEditor)

async function save() {
  saving.value = true
  try {
    await filesApi.save(files.editorPath, draft.value)
    dirty.value = false
    ElMessage.success(t('files.saved'))
    files.refresh() // 树上的 size/mtime 变了
  } catch (e: any) {
    ElMessage.error(e?.message || t('files.opFailed'))
  } finally {
    saving.value = false
  }
}

// el-dialog 的 before-close：脏状态需确认；closing 防止确认框弹出期间重复触发
let closing = false
async function handleBeforeClose(done: () => void) {
  if (closing) return
  if (dirty.value) {
    closing = true
    try {
      await ElMessageBox.confirm(t('files.unsavedConfirm'), fileName.value, { type: 'warning' })
    } catch {
      return // 用户取消关闭
    } finally {
      closing = false
    }
  }
  done()
}

function onClosed() {
  destroyEditor()
  files.editorPath = ''
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    width="82%"
    top="4vh"
    align-center
    append-to-body
    :before-close="handleBeforeClose"
    class="file-editor-dialog"
    @closed="onClosed"
  >
    <template #header>
      <div class="editor-head">
        <span class="editor-title" :title="files.editorPath">{{ fileName }}</span>
        <button
          v-if="isMd"
          class="head-btn"
          type="button"
          :title="t('files.preview')"
          @click="showPreview = !showPreview"
        >
          <el-icon :size="15"><View v-if="!showPreview" /><Hide v-else /></el-icon>
        </button>
        <el-button size="small" type="primary" :loading="saving" :disabled="!dirty" @click="save">
          {{ t('files.save') }}
        </el-button>
      </div>
    </template>

    <div v-if="loading" class="editor-loading">
      <el-icon class="is-loading" :size="22"><Loading /></el-icon>
    </div>
    <div v-else class="editor-body" :class="{ split: isMd && showPreview }">
      <div ref="cmHost" class="cm-host"></div>
      <div v-if="isMd && showPreview" class="md-preview">
        <MarkdownView :content="draft" />
      </div>
    </div>
  </el-dialog>
</template>

<style scoped>
.editor-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-right: 24px;
}
.editor-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}
.head-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  opacity: 0.7;
  cursor: pointer;
}
.head-btn:hover {
  background: rgba(127, 127, 127, 0.12);
  opacity: 1;
}
.editor-loading {
  height: 70vh;
  display: flex;
  align-items: center;
  justify-content: center;
}
.editor-body {
  height: 78vh;
  display: flex;
  gap: 0;
}
.cm-host {
  flex: 1;
  min-width: 0;
  overflow: auto;
  font-size: 13px;
}
/* CodeMirror 撑满容器 */
.cm-host :deep(.cm-editor) {
  height: 100%;
}
.editor-body.split .md-preview {
  flex: 1;
  min-width: 0;
  overflow: auto;
  border-left: 1px solid rgba(127, 127, 127, 0.2);
  padding: 0 16px;
}
</style>

<style>
/* 弹窗 body 去 padding，让编辑器贴边 */
.file-editor-dialog .el-dialog__body {
  padding: 0;
}
</style>
