<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onBeforeUnmount } from 'vue'
import type { UploadRequestOptions, InputInstance } from 'element-plus'
import { Plus, Close, Document, Top, VideoPause } from '@element-plus/icons-vue'
import { useMetaStore } from '../../stores/meta'
import { storeToRefs } from 'pinia'
import type { ChatAttachment } from '../../api/sse'

// hero: 空会话居中形态。此形态下文本框默认更高，视觉上更接近一个「起始卡片」。
const props = defineProps<{ sending: boolean; hero?: boolean }>()
const emit = defineEmits<{
  send: [instruction: string, model: string, agent: string, attachments: ChatAttachment[]]
  stop: []
}>()

const meta = useMetaStore()
const { models, defaultModel, agents } = storeToRefs(meta)
const { t } = useI18n()

// 主 Agent 的哨兵值：与后端 agent.MainAgentName 一致。用非空值而非空串，
// 使 el-select 能正确显示选中态（空串会被当作“未选中”而回落到 placeholder）。
// 后端把 X-Agent-Name=groot 视为等价于不传（即默认编排模式）。
const MAIN_AGENT = 'groot'

const text = ref('')
const selectedModel = ref('')

// 文本框滚动条控制：未达到 maxRows 时随内容增高、隐藏滚动条；
// 只有高度被 maxRows 封顶（内容真正溢出一行以上）后才允许滚动。
// 4px 容差吸收 autosize 亚像素取整造成的假性溢出，避免凭空出现滚动条。
const inputRef = ref<InputInstance>()
const scrollable = ref(false)
async function syncScrollable() {
  await nextTick()
  const ta = inputRef.value?.textarea
  scrollable.value = !!ta && ta.scrollHeight > ta.clientHeight + 4
}

let resizeOb: ResizeObserver | null = null
onMounted(async () => {
  await nextTick()
  const ta = inputRef.value?.textarea
  if (ta && typeof ResizeObserver !== 'undefined') {
    // 宽度变化会改变折行数，autosize 重算高度后需同步滚动条状态
    resizeOb = new ResizeObserver(() => void syncScrollable())
    resizeOb.observe(ta)
  }
  void syncScrollable()
})
onBeforeUnmount(() => resizeOb?.disconnect())

// 内容或形态（hero 行数不同）变化后重新判断是否需要滚动条
watch([text, () => props.hero], () => void syncScrollable())
const selectedAgent = ref(MAIN_AGENT)
// 附件本地条目：att 为发送给后端的数据，preview 仅图片有值（完整 data URL，用于缩略图）。
interface AttachmentItem {
  att: ChatAttachment
  preview: string
}
const attachments = ref<AttachmentItem[]>([])

function removeAttachment(index: number) {
  attachments.value.splice(index, 1)
}

// 模型必须始终有选中项：元数据加载后，若尚未选择则回落到默认模型，
// 默认模型缺失时取列表首项，确保选择框不为空。
watch(
  [defaultModel, models],
  ([def, list]) => {
    if (selectedModel.value) return
    if (def) selectedModel.value = def
    else if (list.length) selectedModel.value = list[0].name
  },
  { immediate: true }
)

const modelOptions = computed(() =>
  models.value.map((m) => ({
    label: m.name,
    value: m.name,
    isDefault: m.name === defaultModel.value,
  }))
)
// Agent 选项：主 Agent groot 即默认编排模式（value = MAIN_AGENT，后端等价于不传）。
// 后端 /agents 会把 groot 放在列表首位，这里过滤掉以免与默认项重复。
// isDefault 仅用于下拉展开时标注「默认」；选中后只显示 groot 本身。
const agentOptions = computed(() => [
  { label: MAIN_AGENT, value: MAIN_AGENT, isDefault: true },
  ...agents.value
    .filter((a) => a !== MAIN_AGENT)
    .map((a) => ({ label: a, value: a, isDefault: false })),
])

// 下拉框宽度随选中文本自适应：CJK 字符按 15px、其他按 8px 估算，
// 再加上下拉箭头与内边距的固定余量；设下限避免过窄。
function selectWidth(label: string): number {
  let w = 0
  for (const ch of label) w += /[⺀-￿]/.test(ch) ? 15 : 8
  return Math.max(96, Math.ceil(w) + 52)
}
const agentLabel = computed(
  () => agentOptions.value.find((o) => o.value === selectedAgent.value)?.label ?? ''
)
const agentWidth = computed(() => selectWidth(agentLabel.value))
const modelWidth = computed(() => selectWidth(selectedModel.value || defaultModel.value))

function handleSend() {
  const val = text.value.trim()
  if (!val) return
  emit(
    'send',
    val,
    selectedModel.value || defaultModel.value,
    selectedAgent.value,
    attachments.value.map((a) => a.att)
  )
  text.value = ''
  attachments.value = []
}

// 读取上传文件为 base64，加入附件数组。el-upload 的 http-request 钩子接管默认上传。
// 钩子要求返回 Promise，这里在读取完成后 resolve，避免 el-upload 走默认 XHR 上传。
// 图片保留完整 data URL 作为缩略图预览；添加结果直接以预览区展示，不再弹消息提示。
function onUpload(options: UploadRequestOptions): Promise<void> {
  const file = options.file
  return new Promise<void>((resolve) => {
    if (!file) {
      resolve()
      return
    }
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      const base64 = result.includes(',') ? result.split(',')[1] : result
      const isImage = file.type.startsWith('image/')
      attachments.value.push({
        att: {
          type: isImage ? 'image' : 'file',
          name: file.name,
          content: base64,
        },
        preview: isImage ? result : '',
      })
      resolve()
    }
    reader.onerror = () => resolve()
    reader.readAsDataURL(file)
  })
}
</script>

<template>
  <div class="chat-input">
    <div class="composer" :class="{ hero: props.hero }">
      <!-- 附件预览区：有附件时撑高输入框，悬浮条目显示删除按钮 -->
      <div v-if="attachments.length" class="attachment-row">
        <div
          v-for="(a, i) in attachments"
          :key="i"
          class="attachment-item"
          :title="a.att.name"
        >
          <img v-if="a.att.type === 'image'" :src="a.preview" class="attachment-thumb" />
          <div v-else class="attachment-file">
            <el-icon :size="20"><Document /></el-icon>
            <span class="attachment-name">{{ a.att.name }}</span>
          </div>
          <button
            type="button"
            class="attachment-remove"
            :title="t('chat.removeAttachment')"
            @click="removeAttachment(i)"
          >
            <el-icon :size="12"><Close /></el-icon>
          </button>
        </div>
      </div>
      <el-input
        ref="inputRef"
        v-model="text"
        type="textarea"
        :placeholder="t('chat.inputPlaceholder')"
        :autosize="{ minRows: props.hero ? 2 : 1, maxRows: 6 }"
        resize="none"
        class="composer-input"
        :class="{ scrollable }"
        @keydown.enter.exact.prevent="handleSend"
      />
      <div class="toolbar">
        <!-- 左下角：＋ 附件 + Agent 选择 -->
        <div class="toolbar-left">
          <el-upload
            :show-file-list="false"
            :http-request="onUpload"
          >
            <el-button circle text :title="t('chat.addAttachment')">
              <el-icon :size="18"><Plus /></el-icon>
            </el-button>
          </el-upload>
          <el-select
            v-model="selectedAgent"
            placeholder="Agent"
            class="borderless-select"
            :style="{ width: agentWidth + 'px' }"
          >
            <el-option
              v-for="o in agentOptions"
              :key="o.value"
              :label="o.label"
              :value="o.value"
            >
              <span class="opt-label">{{ o.label }}</span>
              <el-tag v-if="o.isDefault" size="small" type="primary" effect="light" class="opt-tag">
                {{ t('settings.default') }}
              </el-tag>
            </el-option>
          </el-select>
        </div>
        <!-- 右下角：模型选择 + 发送/停止 -->
        <div class="toolbar-right">
          <el-select
            v-model="selectedModel"
            :placeholder="defaultModel || t('common.model')"
            class="borderless-select"
            :style="{ width: modelWidth + 'px' }"
          >
            <el-option
              v-for="o in modelOptions"
              :key="o.value"
              :label="o.label"
              :value="o.value"
            >
              <span class="opt-label">{{ o.label }}</span>
              <el-tag v-if="o.isDefault" size="small" type="primary" effect="light" class="opt-tag">
                {{ t('settings.default') }}
              </el-tag>
            </el-option>
          </el-select>
          <!-- 圆形图标按钮：文字改为 title/aria-label，保证读屏与悬浮提示仍可读 -->
          <el-button
            v-if="props.sending"
            type="danger"
            circle
            class="action-btn"
            :title="t('chat.stop')"
            :aria-label="t('chat.stop')"
            @click="emit('stop')"
          >
            <el-icon :size="16"><VideoPause /></el-icon>
          </el-button>
          <el-button
            v-else
            type="primary"
            circle
            class="action-btn"
            :title="t('chat.send')"
            :aria-label="t('chat.send')"
            @click="handleSend"
          >
            <el-icon :size="16"><Top /></el-icon>
          </el-button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.chat-input {
  padding: 0;
}
/* 输入卡片：大圆角 + 柔和底色 + 轻阴影，作为独立的一块承载区而非细边框输入行 */
.composer {
  width: 100%;
  max-width: 760px;
  margin: 0 auto;
  box-sizing: border-box;
  border: 1px solid rgba(127, 127, 127, 0.18);
  border-radius: 22px;
  padding: 14px 16px 10px;
  background: var(--el-fill-color-lighter);
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.05);
  transition: border-color 0.15s, box-shadow 0.15s;
}
.composer:focus-within {
  border-color: rgba(127, 127, 127, 0.32);
  box-shadow: 0 4px 18px rgba(0, 0, 0, 0.08);
}
/* 居中起始形态：更宽、内边距更松，占位文字更大 */
.composer.hero {
  max-width: 820px;
  padding: 20px 20px 12px;
}
.composer.hero .composer-input :deep(.el-textarea__inner) {
  /* 用整数像素而非 1.05em（14.7px）：小数字号会让行高取整后
     与 autosize 计算的高度差出 1px，凭空出现滚动条 */
  font-size: 15px;
}
/* 附件预览区：横向排列可换行，位于文本框上方 */
.attachment-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 4px 4px 8px;
}
.attachment-item {
  position: relative;
  border-radius: 10px;
}
.attachment-thumb {
  display: block;
  width: 56px;
  height: 56px;
  object-fit: cover;
  border-radius: 10px;
  border: 1px solid rgba(127, 127, 127, 0.25);
}
/* 非图片附件：图标 + 文件名 */
.attachment-file {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 56px;
  max-width: 180px;
  padding: 0 12px;
  border: 1px solid rgba(127, 127, 127, 0.25);
  border-radius: 10px;
  font-size: 0.82em;
}
.attachment-name {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
/* 删除按钮：右上角小圆钮，仅悬浮条目时可见 */
.attachment-remove {
  position: absolute;
  top: -6px;
  right: -6px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.65);
  color: #fff;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s;
}
.attachment-item:hover .attachment-remove {
  opacity: 1;
}
/* 让文本框融入外层容器：去掉 Element Plus textarea 的边框与聚焦阴影，
   透明背景避免“框中框” */
.composer-input :deep(.el-textarea__inner) {
  box-shadow: none;
  background: transparent;
  padding: 4px;
  resize: none;
  /* 默认隐藏滚动条：未达 maxRows 时靠 autosize 增高承载内容 */
  overflow-y: hidden;
}
/* 内容超过 maxRows、高度被封顶后才允许滚动 */
.composer-input.scrollable :deep(.el-textarea__inner) {
  overflow-y: auto;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 10px;
}
/* 发送/停止：圆形图标钮，与参考稿的右下角圆钮一致 */
.action-btn {
  width: 34px;
  height: 34px;
  margin-left: 4px;
}
.toolbar-left,
.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
/* 无边框选择器：平时透明无框，仅悬浮时显示浅底色以示可交互。
   选中后失焦不保留背景（不对 is-focused 着色）。 */
.borderless-select :deep(.el-select__wrapper) {
  box-shadow: none;
  background-color: transparent;
  border-radius: 8px;
  transition: background-color 0.15s;
}
.borderless-select :deep(.el-select__wrapper:hover) {
  background-color: rgba(127, 127, 127, 0.12);
}
/* 模型下拉项：名称占满、默认标签靠右 */
.opt-label {
  margin-right: 8px;
}
.opt-tag {
  float: right;
  margin-top: 6px;
}
</style>
