<script setup lang="ts">
import { computed } from 'vue'
import type { ChatRecord } from '../../api/types'

const { t } = useI18n()
const props = defineProps<{ record: ChatRecord | null; round: number }>()

const tokens = computed(() => {
  if (!props.record) return null
  return {
    prompt: props.record.prompt_tokens || 0,
    completion: props.record.completion_tokens || 0,
    total: props.record.total_tokens || 0,
  }
})

const duration = computed(() => {
  if (!props.record) return ''
  if (props.record.duration_ms) {
    const s = props.record.duration_ms / 1000
    return `${s.toFixed(1)}s`
  }
  return props.record.duration || ''
})
</script>

<template>
  <div class="stats-bar">
    <span class="stat">{{ t('chat.round', { n: round }) }}</span>
    <template v-if="record">
      <span class="sep">·</span>
      <span class="stat">{{ t('chat.duration', { v: duration }) }}</span>
      <template v-if="tokens">
        <span class="sep">·</span>
        <span class="stat">
          Token {{ tokens.total }}
          <span class="sub">({{ tokens.prompt }}+{{ tokens.completion }})</span>
        </span>
      </template>
      <template v-if="record.model">
        <span class="sep">·</span>
        <span class="stat">{{ record.model }}</span>
      </template>
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
.sub {
  opacity: 0.7;
}
</style>
