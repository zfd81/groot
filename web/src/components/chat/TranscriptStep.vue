<script setup lang="ts">
import { ref, computed } from 'vue'
import type { ChatStep } from '../../stores/chat'

const { t } = useI18n()
const props = defineProps<{ step: ChatStep }>()
const expanded = ref(false)

// 从 call_agent / skill 工具的 JSON 入参里取出子 Agent 名或技能名，
// 与 TUI 的 extractSubAgentName / extractSkillName 行为一致；流式增量
// 未解析完成时 JSON.parse 会失败，此时退回原始参数片段作为反馈。
function extractArgName(raw: string, key: string): string {
  if (!raw) return ''
  try {
    const args = JSON.parse(raw)
    if (args && typeof args[key] === 'string') return args[key]
  } catch {
    // 参数尚未流完，忽略
  }
  return ''
}

// 是否为子 Agent 调用步（call_agent 工具）。
const isSubAgent = computed(
  () => props.step.kind === 'tool' && props.step.tool.name === 'call_agent'
)
// 是否为技能调用步（skill 工具）。
const isSkill = computed(
  () => props.step.kind === 'tool' && props.step.tool.name === 'skill'
)

// 行首标签：思考步固定为「思考」；子 Agent / 技能步给出更友好的中文标签；
// 其余工具步取工具名。
const label = computed(() => {
  if (props.step.kind === 'reasoning') return t('transcript.thinking')
  if (isSubAgent.value) return t('transcript.callAgent')
  if (isSkill.value) return t('transcript.callSkill')
  return props.step.tool.name || t('transcript.tool')
})

// 子 Agent / 技能步在标签后追加解析出的名称，便于在流式过程中第一时间识别。
const subject = computed(() => {
  if (props.step.kind !== 'tool') return ''
  if (isSubAgent.value) return extractArgName(props.step.tool.arguments, 'agent_name')
  if (isSkill.value) return extractArgName(props.step.tool.arguments, 'skill')
  return ''
})

// 计时展示：耗时已结算时格式化为「1.2s」或「820ms」。
const duration = computed(() => {
  const ms =
    props.step.kind === 'reasoning'
      ? props.step.durationMs
      : props.step.tool.durationMs
  if (ms === undefined) return ''
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.round(ms)}ms`
})

// 状态：思考步无状态；工具步分 执行中/完成/错误。
const state = computed<'thinking' | 'running' | 'done' | 'error'>(() => {
  if (props.step.kind === 'reasoning') return 'thinking'
  const tool = props.step.tool
  if (tool.isError) return 'error'
  if (tool.result !== undefined) return 'done'
  return 'running'
})

// 行首图标：思考💭、子 Agent🤖、技能⚡，普通工具🔧。
const icon = computed(() => {
  if (props.step.kind === 'reasoning') return '💭'
  if (isSubAgent.value) return '🤖'
  if (isSkill.value) return '⚡'
  return '🔧'
})

// 折叠成一行的摘要文本。
function oneLine(s: string): string {
  return s.replace(/\s+/g, ' ').trim()
}

const summary = computed(() => {
  if (props.step.kind === 'reasoning') return oneLine(props.step.text)
  const t = props.step.tool
  if (t.isError && t.result) return oneLine(t.result)
  if (t.arguments) return oneLine(t.arguments)
  return ''
})

// 是否有可展开的详情。
const hasDetail = computed(() => {
  if (props.step.kind === 'reasoning') return !!props.step.text
  const t = props.step.tool
  return !!(t.arguments || t.result !== undefined)
})

// 递归展开嵌套的 JSON：工具结果里常把子 JSON 当字符串塞进字段（如
// {"text":"{\"resultcode\":...}"}），逐层把「看起来像 JSON 的字符串值」
// 再解析一层，让最终缩进输出里不再残留被转义的 JSON。
function deepParse(value: unknown): unknown {
  if (typeof value === 'string') {
    const t = value.trim()
    if (t.startsWith('{') || t.startsWith('[')) {
      try {
        return deepParse(JSON.parse(t))
      } catch {
        return value
      }
    }
    return value
  }
  if (Array.isArray(value)) {
    return value.map(deepParse)
  }
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value)) out[k] = deepParse(v)
    return out
  }
  return value
}

function pretty(s: string): string {
  if (!s) return ''
  try {
    return JSON.stringify(deepParse(JSON.parse(s)), null, 2)
  } catch {
    return s
  }
}

function toggle() {
  if (hasDetail.value) expanded.value = !expanded.value
}
</script>

<template>
  <div class="step" :class="{ clickable: hasDetail }">
    <div class="step-head" @click="toggle">
      <span class="icon">{{ icon }}</span>
      <span class="label">{{ label }}</span>
      <span v-if="state === 'running'" class="dot-loading">·</span>
      <span v-else class="sep">·</span>
      <!-- 子 Agent / 技能步优先展示解析出的名称，其余展示单行摘要 -->
      <span v-if="subject" class="subject">{{ subject }}</span>
      <span v-else class="summary" :class="{ error: state === 'error' }">{{ summary }}</span>
      <!-- 该步结束后显示耗时 -->
      <span v-if="duration" class="duration">{{ duration }}</span>
    </div>
    <el-collapse-transition v-if="hasDetail">
      <div v-show="expanded" class="step-body">
        <!-- 思考详情 -->
        <template v-if="step.kind === 'reasoning'">
          <pre class="code">{{ step.text }}</pre>
        </template>
        <!-- 工具详情 -->
        <template v-else>
          <div class="section-label">{{ t('transcript.arguments') }}</div>
          <pre class="code">{{ pretty(step.tool.arguments) }}</pre>
          <template v-if="step.tool.result !== undefined">
            <div class="section-label">{{ t('transcript.result') }}</div>
            <pre class="code" :class="{ error: step.tool.isError }">{{ pretty(step.tool.result) }}</pre>
          </template>
        </template>
      </div>
    </el-collapse-transition>
  </div>
</template>

<style scoped>
.step {
  font-size: 0.9em;
}
.step-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 4px;
  border-radius: 6px;
  color: rgba(127, 127, 127, 0.95);
  user-select: none;
}
.step.clickable .step-head {
  cursor: pointer;
}
.step.clickable .step-head:hover {
  background: rgba(127, 127, 127, 0.08);
}
.icon {
  flex-shrink: 0;
  opacity: 0.8;
  font-size: 0.9em;
}
.label {
  flex-shrink: 0;
  font-weight: 500;
  opacity: 0.85;
}
.sep {
  flex-shrink: 0;
  opacity: 0.4;
}
/* 执行中的分隔点做一个轻微呼吸动画，暗示进行中 */
.dot-loading {
  flex-shrink: 0;
  animation: blink 1s ease-in-out infinite;
}
@keyframes blink {
  0%,
  100% {
    opacity: 0.2;
  }
  50% {
    opacity: 0.9;
  }
}
.summary {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  opacity: 0.7;
}
.summary.error {
  color: #d03050;
  opacity: 1;
}
/* 子 Agent / 技能名：略强调，占据主区域 */
.subject {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-weight: 500;
  opacity: 0.85;
}
/* 耗时：靠右、弱化，等宽字体避免跳动 */
.duration {
  flex-shrink: 0;
  margin-left: 8px;
  font-size: 0.85em;
  font-variant-numeric: tabular-nums;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  opacity: 0.5;
}
.step-body {
  padding: 4px 4px 8px 30px;
}
.section-label {
  font-size: 0.8em;
  opacity: 0.6;
  margin: 4px 0;
}
.code {
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.82em;
  background: rgba(127, 127, 127, 0.08);
  padding: 8px;
  border-radius: 4px;
  margin: 0;
  overflow-x: auto;
}
.code.error {
  color: #d03050;
}
</style>
