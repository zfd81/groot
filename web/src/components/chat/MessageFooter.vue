<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { Loading, CopyDocument, Clock } from '@element-plus/icons-vue'
import type { ChatMessage } from '../../stores/chat'

const { t } = useI18n()
const props = defineProps<{ message: ChatMessage }>()

// 实时计时：仅在流式进行中每秒刷新一次墙钟，驱动「深度思考中 X秒」跳动。
const nowMs = ref(Date.now())
let timer: number | null = null
watch(
  () => props.message.streaming,
  (streaming) => {
    if (streaming && timer === null) {
      nowMs.value = Date.now()
      timer = window.setInterval(() => (nowMs.value = Date.now()), 1000)
    } else if (!streaming && timer !== null) {
      clearInterval(timer)
      timer = null
    }
  },
  { immediate: true }
)
onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})

// 「45秒」/「1分20秒」式的时长文案。
function fmtDuration(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  const m = Math.floor(total / 60)
  const s = total % 60
  return m > 0
    ? t('chat.durationMinSec', { m, s })
    : t('chat.durationSec', { s })
}

// 流式进行中的已耗时（每秒跳动）。
const liveElapsed = computed(() => {
  if (props.message.startedAt === undefined) return ''
  return fmtDuration(nowMs.value - props.message.startedAt)
})

// 完成后的总用时。
const totalDuration = computed(() => {
  if (props.message.durationMs === undefined) return ''
  return fmtDuration(props.message.durationMs)
})

// 完成时刻「19:01」。
const finishTime = computed(() => {
  if (props.message.finishedAt === undefined) return ''
  const d = new Date(props.message.finishedAt)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${hh}:${mm}`
})

// 完成态是否有内容可展示（复制按钮 / 用时 / 完成时刻任一存在即渲染）。
const showDone = computed(
  () =>
    !props.message.streaming &&
    (!!props.message.content || !!totalDuration.value || !!finishTime.value)
)

// 复制整条回复正文；剪贴板 API 不可用（如非 HTTPS）时退回 execCommand。
async function copyContent() {
  const text = props.message.content
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
    } catch {
      document.body.removeChild(ta)
      ElMessage.error(t('chat.copyFailed'))
      return
    }
    document.body.removeChild(ta)
  }
  ElMessage.success(t('chat.copied'))
}
</script>

<template>
  <!-- 流式进行中：实时跳动的「深度思考中 X秒」 -->
  <div v-if="message.streaming" class="footer live">
    <el-icon class="is-loading spin"><Loading /></el-icon>
    <span>{{ t('chat.thinkingLive') }} {{ liveElapsed }}</span>
  </div>
  <!-- 完成后：复制按钮 + 总用时 + 完成时刻 -->
  <div v-else-if="showDone" class="footer done">
    <button
      v-if="message.content"
      class="copy-btn"
      type="button"
      :title="t('chat.copy')"
      @click="copyContent"
    >
      <el-icon :size="15"><CopyDocument /></el-icon>
    </button>
    <span v-if="totalDuration" class="stat">
      <el-icon :size="13"><Clock /></el-icon>
      {{ t('chat.timeUsed', { v: totalDuration }) }}
    </span>
    <span v-if="finishTime" class="stat time">{{ finishTime }}</span>
  </div>
</template>

<style scoped>
.footer {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
  font-size: 0.82em;
  color: rgba(127, 127, 127, 0.95);
}
.footer.live {
  gap: 6px;
}
.spin {
  color: var(--el-color-primary);
  font-size: 14px;
}
.copy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  cursor: pointer;
  transition: background 0.15s;
}
.copy-btn:hover {
  background: rgba(127, 127, 127, 0.12);
}
.stat {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  /* 等宽数字避免计时跳动时抖动 */
  font-variant-numeric: tabular-nums;
}
.stat.time {
  opacity: 0.8;
}
</style>
