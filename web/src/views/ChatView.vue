<script setup lang="ts">
import { ref, onMounted, watch, nextTick, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useChatStore } from '../stores/chat'
import { useMetaStore } from '../stores/meta'
import type { ChatAttachment } from '../api/sse'
import SessionSidebar from '../components/chat/SessionSidebar.vue'
import MessageList from '../components/chat/MessageList.vue'
import ChatInput from '../components/chat/ChatInput.vue'
import StatsBar from '../components/chat/StatsBar.vue'
import SettingsModal from '../components/settings/SettingsModal.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const chat = useChatStore()
const meta = useMetaStore()
// 悬浮输入框需要与页面同色的遮罩，取 Element Plus 的背景色变量，随主题明暗切换。
const bodyColor = 'var(--el-bg-color)'
const { messages, sending, sessions, sessionId, canLoadMore, loadingSessions, lastRecord } =
  storeToRefs(chat)

const collapsed = ref(false)
const showSettings = ref(false)
const scrollArea = ref<HTMLElement | null>(null)

const roundCount = computed(
  () => messages.value.filter((m) => m.role === 'user').length
)

// 空会话（尚无任何消息）时输入框居中呈现；发出首条消息后即切换为底部停靠。
const isEmpty = computed(() => messages.value.length === 0)

// 顶部栏标题：取当前会话首条用户消息，未开始则显示占位。
const headerTitle = computed(() => {
  const firstUser = messages.value.find((m) => m.role === 'user')
  if (firstUser?.content) return firstUser.content
  return t('common.newChat')
})

async function scrollToBottom() {
  await nextTick()
  const el = scrollArea.value
  if (el) el.scrollTop = el.scrollHeight
}

watch(
  () =>
    messages.value
      .map((m) => m.content.length + m.reasoning.length + m.steps.length)
      .join(),
  scrollToBottom
)

function handleSend(
  instruction: string,
  model: string,
  agent: string,
  attachments: ChatAttachment[]
) {
  void chat.send(instruction, model, agent, attachments)
}

async function handleSelect(sid: string) {
  if (sid === sessionId.value) return
  await chat.openSession(sid)
  router.replace({ name: 'chat-session', params: { sid } })
  scrollToBottom()
}

function handleNew() {
  chat.newSession()
  router.replace({ name: 'chat' })
}

onMounted(async () => {
  await meta.load()
  await chat.loadSessions(true)
  const sid = route.params.sid as string | undefined
  if (sid) {
    await chat.openSession(sid)
    await chat.fetchStats()
    scrollToBottom()
  }
})
</script>

<template>
  <div class="chat-layout">
    <aside class="sider" :class="{ collapsed }">
      <SessionSidebar
        :sessions="sessions"
        :current-id="sessionId"
        :sending-id="sending ? sessionId : ''"
        :can-load-more="canLoadMore"
        :loading="loadingSessions"
        :collapsed="collapsed"
        @select="handleSelect"
        @load-more="chat.loadSessions(false)"
        @new-session="handleNew"
        @open-settings="showSettings = true"
        @expand="collapsed = false"
        @collapse="collapsed = true"
      />
    </aside>

    <div class="main">
      <!-- 顶部栏：会话标题 -->
      <header class="topbar">
        <h1 class="topbar-title">{{ headerTitle }}</h1>
      </header>

      <div v-show="!isEmpty" ref="scrollArea" class="scroll-area">
        <MessageList :messages="messages" />
        <!-- 底部占位：让最后的内容能滚动到悬浮输入框上方 -->
        <div class="scroll-spacer" />
      </div>
      <!-- 输入区：空会话时垂直居中（.centered），有消息后停靠底部。
           两种形态复用同一个 ChatInput 实例，避免重建丢失已选模型与 Agent。 -->
      <div class="input-area" :class="{ centered: isEmpty }">
        <!-- 居中的不透明输入区：只遮住内容列，不覆盖右侧滚动条 -->
        <div class="input-inner">
          <h2 v-if="isEmpty" class="hero-title">{{ t('chat.heroTitle') }}</h2>
          <ChatInput
            :sending="sending"
            :hero="isEmpty"
            @send="handleSend"
            @stop="chat.stop()"
          />
          <StatsBar v-if="!isEmpty" :record="lastRecord" :round="roundCount" />
        </div>
      </div>
    </div>
  </div>

  <SettingsModal v-model:show="showSettings" />
</template>

<style scoped>
.chat-layout {
  display: flex;
  height: 100vh;
}
/* 侧栏：展开 260px、收起 52px，宽度过渡与右侧边框由此处提供 */
.sider {
  width: 260px;
  flex-shrink: 0;
  height: 100vh;
  overflow: hidden;
  transition: width 0.2s var(--el-transition-function-ease-in-out-bezier, ease);
}
.sider.collapsed {
  width: 52px;
}
.main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  height: 100vh;
  position: relative;
}
.topbar {
  height: 56px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 16px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}
.topbar-title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 1em;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.scroll-area {
  flex: 1;
  overflow-y: auto;
  /* 两侧预留等宽滚动条空槽，使内容列在对称区域内居中，与浮动输入框中心对齐 */
  scrollbar-gutter: stable both-edges;
}
/* 占位高度略大于输入框，保证最后一条消息能滚到输入框上方 */
.scroll-spacer {
  height: 140px;
}
.input-area {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  /* 通栏容器本身透明且不拦截事件，保证右侧滚动条可见、可交互并延伸到底 */
  background: transparent;
  pointer-events: none;
}
/* 空会话形态：撑满主区并垂直居中，输入框位于视觉中心 */
.input-area.centered {
  top: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  /* 略高于几何中心，视觉上更居中（底部无消息列表压迫感） */
  padding-bottom: 6vh;
}
/* 居中态不需要顶部渐隐遮罩（其上方没有滚动内容） */
.input-area.centered .input-inner::before {
  display: none;
}
/* 居中态输入区加宽，与 hero 形态的输入卡片同宽。
   必须显式给 width：flex 列容器里 margin: 0 auto 会取消交叉轴拉伸，
   元素退化为 fit-content 宽度，max-width 便形同虚设。 */
.input-area.centered .input-inner {
  width: 100%;
  max-width: 820px;
  padding-bottom: 0;
  background: transparent;
}
/* 欢迎标题：仅空会话可见 */
.hero-title {
  margin: 0 0 24px;
  font-size: 1.75em;
  font-weight: 600;
  letter-spacing: 0.5px;
  text-align: center;
}
/* 真正的输入区：居中、限定宽度、不透明背景，只遮住内容列 */
.input-inner {
  max-width: 760px;
  margin: 0 auto;
  padding: 0 0 12px;
  background: v-bind(bodyColor);
  pointer-events: auto;
  position: relative;
}
/* 顶部渐隐遮罩：让滚动内容平滑地淡入到输入框下方 */
.input-inner::before {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  top: -24px;
  height: 24px;
  background: linear-gradient(to top, v-bind(bodyColor), transparent);
  pointer-events: none;
}
</style>
