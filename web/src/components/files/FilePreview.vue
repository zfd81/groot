<!-- 面板内只读预览：md 渲染 / 代码高亮 / 图片 / 二进制提示。 -->
<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { ArrowLeft, EditPen, Download, Loading } from '@element-plus/icons-vue'
import hljs from 'highlight.js/lib/core'
import MarkdownView from '../chat/MarkdownView.vue'
import { filesApi } from '../../api/files'
import { useFilesStore } from '../../stores/files'
import { ApiError } from '../../api/client'

const { t } = useI18n()
const files = useFilesStore()

const props = defineProps<{ path: string }>()

type Mode = 'markdown' | 'code' | 'image' | 'binary' | 'toolarge' | 'error'
const mode = ref<Mode>('code')
const content = ref('')
const readonly = ref(false)
const loading = ref(false)

const IMG_EXTS = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico']
const fileName = computed(() => props.path.split('/').pop() || props.path)
const ext = computed(() => (fileName.value.includes('.') ? fileName.value.split('.').pop()!.toLowerCase() : ''))
// 带上 treeVersion 作为查询参数：刷新后绕过浏览器对同 URL 图片的缓存
const imageUrl = computed(() => `${filesApi.downloadUrl(props.path)}&v=${files.treeVersion}`)
// 可编辑：文本类内容且非只读
const canEdit = computed(
  () => !readonly.value && (mode.value === 'markdown' || mode.value === 'code')
)

// 代码高亮：语言已被 MarkdownView 注册进 hljs 单例（ChatView 必然加载过它），
// 未知语言回退为纯文本。
const highlighted = computed(() => {
  if (mode.value !== 'code') return ''
  const lang = ext.value === 'yml' ? 'yaml' : ext.value
  if (lang && hljs.getLanguage(lang)) {
    return hljs.highlight(content.value, { language: lang }).value
  }
  return escapeHtml(content.value)
})

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// 请求序号：快速切换文件时使旧的在途响应失效
let requestSeq = 0

// 同时监听路径与 treeVersion：编辑保存（内部触发 refresh）和
// 路径栏刷新按钮都会使 treeVersion+1，预览内容随之重新加载。
watch(
  () => [props.path, files.treeVersion] as const,
  async ([p]) => {
    if (!p) return
    const seq = ++requestSeq
    content.value = ''
    if (IMG_EXTS.includes(ext.value)) {
      mode.value = 'image'
      return
    }
    loading.value = true
    try {
      const resp = await filesApi.content(p)
      if (seq !== requestSeq) return
      readonly.value = resp.readonly
      if (resp.binary) {
        mode.value = 'binary'
      } else {
        content.value = resp.content
        mode.value = ext.value === 'md' ? 'markdown' : 'code'
      }
    } catch (e) {
      if (seq !== requestSeq) return
      mode.value = e instanceof ApiError && e.status === 413 ? 'toolarge' : 'error'
    } finally {
      if (seq === requestSeq) loading.value = false
    }
  },
  { immediate: true }
)

function download() {
  const a = document.createElement('a')
  a.href = imageUrl.value
  a.download = fileName.value
  a.click()
}
</script>

<template>
  <div class="file-preview">
    <header class="preview-bar">
      <button class="bar-btn" type="button" :title="t('files.back')" @click="files.previewPath = ''">
        <el-icon :size="15"><ArrowLeft /></el-icon>
      </button>
      <span class="preview-name" :title="path">{{ fileName }}</span>
      <span v-if="readonly" class="preview-ro">{{ t('files.readonlyTag') }}</span>
      <button v-if="canEdit" class="bar-btn" type="button" :title="t('files.edit')"
        @click="files.editorPath = path">
        <el-icon :size="15"><EditPen /></el-icon>
      </button>
      <button class="bar-btn" type="button" :title="t('files.download')" @click="download">
        <el-icon :size="15"><Download /></el-icon>
      </button>
    </header>

    <div class="preview-body">
      <div v-if="loading" class="preview-center">
        <el-icon class="is-loading" :size="20"><Loading /></el-icon>
      </div>
      <MarkdownView v-else-if="mode === 'markdown'" :content="content" />
      <pre v-else-if="mode === 'code'" class="preview-code"><code v-html="highlighted"></code></pre>
      <div v-else-if="mode === 'image'" class="preview-center">
        <img :src="imageUrl" :alt="fileName" class="preview-img" />
      </div>
      <div v-else class="preview-center preview-hint">
        {{ mode === 'binary' ? t('files.binary') : mode === 'toolarge' ? t('files.tooLarge') : t('files.loadFailed') }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.file-preview {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.preview-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}
.bar-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  opacity: 0.7;
  cursor: pointer;
}
.bar-btn:hover {
  background: rgba(127, 127, 127, 0.12);
  opacity: 1;
}
.preview-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9em;
  font-weight: 600;
}
.preview-ro {
  flex-shrink: 0;
  font-size: 0.75em;
  opacity: 0.55;
  border: 1px solid rgba(127, 127, 127, 0.35);
  border-radius: 4px;
  padding: 0 4px;
}
.preview-body {
  flex: 1;
  overflow: auto;
  padding: 10px 12px;
}
.preview-code {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.preview-center {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}
.preview-hint {
  opacity: 0.55;
  font-size: 0.9em;
}
.preview-img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}
</style>
