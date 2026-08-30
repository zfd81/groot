<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { ElMessage, type UploadRequestOptions } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import { useMetaStore } from '../../stores/meta'
import { storeToRefs } from 'pinia'
import type { ChatAttachment } from '../../api/sse'

const props = defineProps<{ sending: boolean }>()
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
const selectedAgent = ref(MAIN_AGENT)
const attachments = ref<ChatAttachment[]>([])

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
  emit('send', val, selectedModel.value || defaultModel.value, selectedAgent.value, [
    ...attachments.value,
  ])
  text.value = ''
  attachments.value = []
}

// 读取上传文件为 base64，加入附件数组。el-upload 的 http-request 钩子接管默认上传。
// 钩子要求返回 Promise，这里在读取完成后 resolve，避免 el-upload 走默认 XHR 上传。
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
        type: isImage ? 'image' : 'file',
        name: file.name,
        content: base64,
      })
      ElMessage.success(t('chat.attachmentAdded', { name: file.name }))
      resolve()
    }
    reader.onerror = () => resolve()
    reader.readAsDataURL(file)
  })
}
</script>

<template>
  <div class="chat-input">
    <div class="composer">
      <el-input
        v-model="text"
        type="textarea"
        :placeholder="t('chat.inputPlaceholder')"
        :autosize="{ minRows: 1, maxRows: 6 }"
        resize="none"
        class="composer-input"
        @keydown.enter.exact.prevent="handleSend"
      />
      <div class="toolbar">
        <!-- 左下角：＋ 附件 + Agent 选择 -->
        <div class="toolbar-left">
          <el-upload
            :show-file-list="false"
            :http-request="onUpload"
          >
            <el-badge
              :value="attachments.length"
              :hidden="attachments.length === 0"
            >
              <el-button circle text :title="t('chat.addAttachment')">
                <el-icon :size="18"><Plus /></el-icon>
              </el-button>
            </el-badge>
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
          <el-button v-if="props.sending" type="danger" @click="emit('stop')">
            {{ t('chat.stop') }}
          </el-button>
          <el-button v-else type="primary" @click="handleSend"> {{ t('chat.send') }} </el-button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.chat-input {
  padding: 0;
}
.composer {
  max-width: 760px;
  margin: 0 auto;
  border: 1px solid rgba(127, 127, 127, 0.25);
  border-radius: 16px;
  padding: 8px 12px;
  background: transparent;
  transition: border-color 0.15s;
}
.composer:focus-within {
  border-color: var(--el-color-primary);
}
/* 让文本框融入外层容器：去掉 Element Plus textarea 的边框与聚焦阴影，
   透明背景避免“框中框” */
.composer-input :deep(.el-textarea__inner) {
  box-shadow: none;
  background: transparent;
  padding: 4px;
  resize: none;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 6px;
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
