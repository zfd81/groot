<script setup lang="ts">
import { ref } from 'vue'

const { t } = useI18n()
defineProps<{ content: string }>()
const expanded = ref(false)
</script>

<template>
  <div class="thinking" v-if="content">
    <div class="thinking-head" @click="expanded = !expanded">
      <span class="icon">{{ expanded ? '▼' : '▶' }}</span>
      <span class="label">{{ t('chat.thinking') }}</span>
    </div>
    <el-collapse-transition>
      <pre v-show="expanded" class="thinking-body">{{ content }}</pre>
    </el-collapse-transition>
  </div>
</template>

<style scoped>
.thinking {
  margin-bottom: 8px;
  border-left: 2px solid rgba(127, 127, 127, 0.3);
  padding-left: 8px;
}
.thinking-head {
  cursor: pointer;
  font-size: 0.85em;
  opacity: 0.7;
  user-select: none;
}
.thinking-head .icon {
  margin-right: 4px;
}
.thinking-body {
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 0.85em;
  opacity: 0.8;
  margin: 4px 0;
}
</style>
