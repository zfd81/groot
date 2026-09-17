<!-- 语义化创建对话框：名称输入；skill / mcp 额外提供「所属 Agent」选择，
     默认主 Agent groot，可选任一已存在的子 Agent（subagents/ 下的一级目录）。 -->
<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { filesApi } from '../../api/files'

type Kind = 'skill' | 'mcp' | 'agent'

const props = defineProps<{
  modelValue: boolean
  kind: Kind
  title: string
  defaultAgent?: string // 打开时预选的子 Agent（如当前浏览位置位于某子 Agent 内）
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
  (e: 'created', path: string, agent: string): void
}>()

const { t } = useI18n()

// 与后端 scaffoldNameRe 一致：字母开头（不能以数字开头），之后仅字母、数字、下划线、连字符，≤64 字符
const NAME_RE = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/

const name = ref('')
// 主 Agent 选项值。不能用空字符串：el-select 把 '' 视作未选择，既不回显也无法选中。
const MAIN_AGENT = 'groot'
const agent = ref(MAIN_AGENT)
const agents = ref<string[]>([])
const submitting = ref(false)
const inputRef = ref<{ focus: () => void } | null>(null)

const hasAgentField = computed(() => props.kind !== 'agent') // 子 Agent 不能嵌套
const nameValid = computed(() => NAME_RE.test(name.value.trim()))
const nameError = computed(() => (name.value && !nameValid.value ? t('files.nameInvalid') : ''))

// subagents/ 下的一级目录即候选子 Agent；目录不存在（尚无子 Agent）按空列表处理。
async function loadAgents() {
  try {
    const resp = await filesApi.list('subagents')
    // 与主 Agent 同名的目录会被加载器跳过，这里同样过滤，避免与主 Agent 选项重复
    agents.value = resp.entries
      .filter((e) => e.type === 'dir' && e.name !== MAIN_AGENT)
      .map((e) => e.name)
  } catch {
    agents.value = []
  }
}

watch(
  () => props.modelValue,
  async (open) => {
    if (!open) return
    name.value = ''
    agent.value = props.defaultAgent || MAIN_AGENT
    if (hasAgentField.value) await loadAgents()
    if (agent.value !== MAIN_AGENT && !agents.value.includes(agent.value)) agent.value = MAIN_AGENT
    await nextTick()
    inputRef.value?.focus()
  }
)

function close() {
  emit('update:modelValue', false)
}

async function submit() {
  if (!nameValid.value || submitting.value) return
  submitting.value = true
  try {
    // 主 Agent 不传 agent 字段，后端按 home 根目录处理
    const a = hasAgentField.value && agent.value !== MAIN_AGENT ? agent.value : ''
    const resp = await filesApi.scaffold(props.kind, name.value.trim(), a || undefined)
    emit('created', resp.path, a)
    close()
  } catch (e: any) {
    ElMessage.error(e?.message || t('files.opFailed'))
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    :title="title"
    width="380px"
    align-center
    append-to-body
    @update:model-value="close"
  >
    <el-form label-position="top" @submit.prevent="submit">
      <el-form-item v-if="hasAgentField" :label="t('files.scaffoldAgent')">
        <el-select v-model="agent" style="width: 100%">
          <el-option :label="t('files.mainAgent')" :value="MAIN_AGENT" />
          <el-option v-for="a in agents" :key="a" :label="a" :value="a" />
        </el-select>
      </el-form-item>
      <el-form-item :label="t('files.namePrompt')" :error="nameError">
        <el-input ref="inputRef" v-model="name" @keyup.enter="submit" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="close">{{ t('files.cancel') }}</el-button>
      <el-button type="primary" :disabled="!nameValid" :loading="submitting" @click="submit">
        {{ t('files.create') }}
      </el-button>
    </template>
  </el-dialog>
</template>
