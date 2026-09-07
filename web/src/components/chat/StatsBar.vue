<script setup lang="ts">
import { computed } from 'vue'
import type { ChatRecord } from '../../api/types'
import { fmtTok } from '../../utils/format'

const { t } = useI18n()

// stats 为会话累计统计（各轮求和）：总耗时与输入/输出 token 累计；
// record（最近一轮记录）仅用于展示当前模型名。
const props = defineProps<{
  record: ChatRecord | null
  round: number
  stats: { durationMs: number; promptTokens: number; completionTokens: number }
}>()

// 会话总耗时：一分钟内保留一位小数（如 15.3s），超过一分钟用「X分Y秒」。
const duration = computed(() => {
  const ms = props.stats.durationMs
  if (!ms) return ''
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  const sec = Math.round(s % 60)
  return t('chat.durationMinSec', { m, s: sec })
})

const hasTokens = computed(
  () => props.stats.promptTokens > 0 || props.stats.completionTokens > 0
)
</script>

<template>
  <div class="stats-bar">
    <span class="stat">{{ t('chat.round', { n: round }) }}</span>
    <template v-if="duration">
      <span class="sep">·</span>
      <span class="stat">{{ t('chat.duration', { v: duration }) }}</span>
    </template>
    <template v-if="hasTokens">
      <span class="sep">·</span>
      <span class="stat">{{ t('chat.tokenInputShort', { n: fmtTok(stats.promptTokens) }) }}</span>
      <span class="sep">·</span>
      <span class="stat">{{ t('chat.tokenOutputShort', { n: fmtTok(stats.completionTokens) }) }}</span>
    </template>
    <template v-if="record?.model">
      <span class="sep">·</span>
      <span class="stat">{{ record.model }}</span>
    </template>
  </div>
</template>

<style scoped>
.stats-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.78em;
  opacity: 0.6;
  padding: 4px 8px;
}
</style>
