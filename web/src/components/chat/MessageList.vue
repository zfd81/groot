<script setup lang="ts">
import MarkdownView from './MarkdownView.vue'
import ThinkingBlock from './ThinkingBlock.vue'
import ToolCallPanel from './ToolCallPanel.vue'
import TranscriptStep from './TranscriptStep.vue'
import MessageFooter from './MessageFooter.vue'
import type { ChatMessage } from '../../stores/chat'

defineProps<{ messages: ChatMessage[] }>()
</script>

<template>
  <div class="message-list">
    <div
      v-for="(m, i) in messages"
      :key="i"
      class="msg-row"
      :class="m.role"
      :data-round="m.role === 'user' ? m.round : undefined"
    >
      <div v-if="m.role === 'user'" class="user-bubble">
        {{ m.content }}
      </div>
      <div v-else class="assistant-block">
        <!-- 有序转录流：思考与工具调用按到达顺序逐行呈现 -->
        <div v-if="m.steps.length" class="transcript">
          <TranscriptStep
            v-for="(s, si) in m.steps"
            :key="si"
            :step="s"
          />
        </div>
        <!-- 历史消息无 steps 时间线，回退到旧的块状渲染 -->
        <template v-else>
          <ThinkingBlock v-if="m.reasoning" :content="m.reasoning" />
          <ToolCallPanel v-for="t in m.tools" :key="t.id" :tool="t" />
        </template>
        <MarkdownView v-if="m.content" :content="m.content" />
        <div v-if="m.error" class="msg-error">⚠️ {{ m.error }}</div>
        <!-- 底部状态栏：流式中显示实时计时，完成后显示 复制/总用时/完成时刻 -->
        <MessageFooter :message="m" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.message-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px 0;
  max-width: 760px;
  margin: 0 auto;
}
.msg-row {
  display: flex;
}
.msg-row.user {
  justify-content: flex-end;
}
.user-bubble {
  background: var(--el-color-primary);
  color: #fff;
  padding: 8px 14px;
  border-radius: 12px 12px 2px 12px;
  max-width: 80%;
  white-space: pre-wrap;
  word-break: break-word;
}
.assistant-block {
  width: 100%;
}
.transcript {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-bottom: 10px;
}
.msg-error {
  color: #d03050;
  margin-top: 8px;
  font-size: 0.9em;
}
/* 搜索定位高亮：定位到目标轮次后短暂闪烁提示 */
.msg-row.locate-highlight .user-bubble {
  animation: locate-flash 1.5s ease-out;
}
@keyframes locate-flash {
  0% {
    box-shadow: 0 0 0 3px var(--el-color-primary-light-5);
  }
  100% {
    box-shadow: 0 0 0 3px transparent;
  }
}
</style>
