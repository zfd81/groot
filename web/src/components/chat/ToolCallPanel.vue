<script setup lang="ts">
import { ref } from 'vue'
import type { ToolInvocation } from '../../stores/chat'

const { t } = useI18n()
defineProps<{ tool: ToolInvocation }>()
const expanded = ref(false)

function pretty(s: string): string {
  if (!s) return ''
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}
</script>

<template>
  <div class="tool-call">
    <div class="tool-head" @click="expanded = !expanded">
      <span class="icon">{{ expanded ? '▼' : '▶' }}</span>
      <span class="tool-name">🔧 {{ tool.name }}</span>
      <el-tag v-if="tool.agentName" size="small" type="info" effect="light">
        {{ tool.agentName }}
      </el-tag>
      <el-tag v-if="tool.isError" size="small" type="danger" effect="light">
        {{ t('tool.error') }}
      </el-tag>
      <el-tag
        v-else-if="tool.result !== undefined"
        size="small"
        type="success"
        effect="light"
      >
        {{ t('tool.done') }}
      </el-tag>
      <el-tag v-else size="small" type="info" effect="plain">{{ t('tool.running') }}</el-tag>
    </div>
    <el-collapse-transition>
      <div v-show="expanded" class="tool-body">
        <div class="section-label">{{ t('tool.arguments') }}</div>
        <pre class="code">{{ pretty(tool.arguments) }}</pre>
        <template v-if="tool.result !== undefined">
          <div class="section-label">{{ t('tool.result') }}</div>
          <pre class="code" :class="{ error: tool.isError }">{{ tool.result }}</pre>
        </template>
      </div>
    </el-collapse-transition>
  </div>
</template>

<style scoped>
.tool-call {
  margin: 6px 0;
  border: 1px solid rgba(127, 127, 127, 0.2);
  border-radius: 6px;
  overflow: hidden;
}
.tool-head {
  cursor: pointer;
  padding: 6px 10px;
  display: flex;
  align-items: center;
  gap: 8px;
  background: rgba(127, 127, 127, 0.06);
  font-size: 0.9em;
  user-select: none;
}
.tool-name {
  font-weight: 500;
}
.tool-body {
  padding: 8px 10px;
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
