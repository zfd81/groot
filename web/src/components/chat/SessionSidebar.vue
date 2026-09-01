<script setup lang="ts">
import { CirclePlus, Setting, Loading } from '@element-plus/icons-vue'
import type { SessionSummary } from '../../api/types'

const { t } = useI18n()

const props = defineProps<{
  sessions: SessionSummary[]
  currentId: string
  canLoadMore: boolean
  loading: boolean
  collapsed: boolean
  /** 正在执行对话的会话 ID（无执行中对话时为空串），标题前显示执行动画 */
  sendingId: string
}>()
const emit = defineEmits<{
  select: [sid: string]
  loadMore: []
  newSession: []
  openSettings: []
  expand: []
  collapse: []
}>()

// 相对时间格式化
function relTime(iso: string): string {
  if (!iso) return ''
  const ts = new Date(iso).getTime()
  if (isNaN(ts)) return ''
  const diff = Date.now() - ts
  const m = Math.floor(diff / 60000)
  if (m < 1) return t('sidebar.justNow')
  if (m < 60) return t('sidebar.minutesAgo', { n: m })
  const h = Math.floor(m / 60)
  if (h < 24) return t('sidebar.hoursAgo', { n: h })
  const d = Math.floor(h / 24)
  return t('sidebar.daysAgo', { n: d })
}

function title(s: SessionSummary): string {
  // 优先显示首轮用户指令（与主区顶部标题一致），无对话记录时回退会话 ID 前缀
  return s.title?.trim() || s.session_id.slice(0, 8)
}
</script>

<template>
  <!-- 收起态：窄栏，只放展开 + 新建会话两个图标 -->
  <div v-if="props.collapsed" class="rail">
    <button class="rail-btn" type="button" :title="t('sidebar.expand')" @click="emit('expand')">
      <el-icon :size="20">
        <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
          <rect
            x="3"
            y="3"
            width="18"
            height="18"
            rx="2"
            stroke="currentColor"
            stroke-width="2"
          />
          <path d="M9 3V21" stroke="currentColor" stroke-width="2" />
        </svg>
      </el-icon>
    </button>
    <button class="rail-btn" type="button" :title="t('sidebar.newChat')" @click="emit('newSession')">
      <el-icon :size="22"><CirclePlus /></el-icon>
    </button>
    <!-- 底部：设置图标 -->
    <button
      class="rail-btn rail-settings"
      type="button"
      :title="t('sidebar.settings')"
      @click="emit('openSettings')"
    >
      <el-icon :size="20"><Setting /></el-icon>
    </button>
  </div>

  <div v-else class="sidebar">
    <!-- 品牌标识（纯文字），高度与主区顶部栏对齐 -->
    <div class="brand">
      <span class="brand-name">Groot</span>
      <span class="brand-badge">AGENT</span>
      <button
        class="collapse-btn"
        type="button"
        :title="t('sidebar.collapse')"
        @click="emit('collapse')"
      >
        <el-icon :size="20">
          <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
            <rect
              x="3"
              y="3"
              width="18"
              height="18"
              rx="2"
              stroke="currentColor"
              stroke-width="2"
            />
            <path d="M9 3V21" stroke="currentColor" stroke-width="2" />
          </svg>
        </el-icon>
      </button>
    </div>

    <!-- ② 新会话：整宽按钮 -->
    <div class="new-wrap">
      <el-button class="new-btn" @click="emit('newSession')">
        <el-icon class="btn-icon"><CirclePlus /></el-icon>
        {{ t('sidebar.newChat') }}
      </el-button>
    </div>

    <div class="session-list">
      <el-empty
        v-if="!loading && props.sessions.length === 0"
        :description="t('sidebar.empty')"
        :image-size="60"
        style="margin-top: 24px"
      />
      <div
        v-for="s in props.sessions"
        :key="s.session_id"
        class="session-item"
        :class="{ active: s.session_id === props.currentId }"
        @click="emit('select', s.session_id)"
      >
        <div class="session-title">
          <el-icon
            v-if="s.session_id === props.sendingId"
            class="is-loading session-spinner"
          >
            <Loading />
          </el-icon>
          <span class="session-title-text">{{ title(s) }}</span>
        </div>
        <div class="session-meta">
          {{ relTime(s.last_active_at || s.created_at) }} · {{ t('sidebar.rounds', { n: s.round_count }) }}
        </div>
      </div>
      <div v-if="loading" class="loading">
        <el-icon class="is-loading"><Loading /></el-icon>
      </div>
      <el-button
        v-if="props.canLoadMore && !loading"
        text
        size="small"
        style="width: 100%"
        @click="emit('loadMore')"
      >
        {{ t('sidebar.loadMore') }}
      </el-button>
    </div>

    <!-- ③ 设置：左下角，带齿轮图标 -->
    <div class="bottom">
      <button class="settings-btn" type="button" @click="emit('openSettings')">
        <el-icon :size="20"><Setting /></el-icon>
        <span>{{ t('sidebar.settings') }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.rail {
  height: 100%;
  box-sizing: border-box;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding-top: 12px;
  border-right: 1px solid rgba(127, 127, 127, 0.15);
}
.rail-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  background: transparent;
  color: inherit;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
}
.rail-btn:hover {
  background: rgba(127, 127, 127, 0.12);
}
.rail-settings {
  margin-top: auto;
  margin-bottom: 12px;
}
.sidebar {
  height: 100%;
  display: flex;
  flex-direction: column;
  border-right: 1px solid rgba(127, 127, 127, 0.15);
}
.brand {
  height: 56px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
  flex-shrink: 0;
}
.brand-name {
  font-size: 1.15em;
  font-weight: 700;
  letter-spacing: 0.2px;
}
.brand-badge {
  font-size: 0.62em;
  font-weight: 700;
  letter-spacing: 1px;
  padding: 2px 5px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.15);
  opacity: 0.75;
}
.collapse-btn {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  color: inherit;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
}
.collapse-btn:hover {
  background: rgba(127, 127, 127, 0.1);
}
.new-wrap {
  padding: 4px 12px 12px;
  flex-shrink: 0;
}
.new-btn {
  width: 100%;
  height: 40px;
  border-radius: 10px;
  font-weight: 500;
}
/* 覆盖 Element Plus 默认按钮的蓝色 hover/focus，统一为中性灰，与设置按钮一致 */
.new-btn:hover,
.new-btn:focus {
  background: rgba(127, 127, 127, 0.1);
  border-color: var(--el-border-color);
  color: inherit;
}
.btn-icon {
  margin-right: 4px;
}
.session-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 8px 8px;
  /* 滚动条只保留滑块：轨道透明；默认整体隐藏，悬停侧栏时才显示滑块 */
  scrollbar-width: thin;
  scrollbar-color: transparent transparent;
}
.sidebar:hover .session-list {
  scrollbar-color: rgba(127, 127, 127, 0.35) transparent;
}
/* Chrome/Safari：改走 WebKit 伪元素以精确控制 10px 宽度。
   Chrome 一旦设置 scrollbar-width 就会忽略伪元素规则，故此处先撤销标准属性；
   Firefox 不支持该选择器，继续沿用上面的 thin + scrollbar-color 方案 */
@supports selector(::-webkit-scrollbar) {
  .session-list,
  .sidebar:hover .session-list {
    scrollbar-width: auto;
    scrollbar-color: auto;
  }
  .session-list::-webkit-scrollbar {
    width: 10px;
  }
  .session-list::-webkit-scrollbar-track {
    background: transparent;
  }
  .session-list::-webkit-scrollbar-thumb {
    background: transparent;
    border-radius: 5px;
  }
  .sidebar:hover .session-list::-webkit-scrollbar-thumb {
    background: rgba(127, 127, 127, 0.35);
  }
}
.session-item {
  padding: 8px 10px;
  border-radius: 8px;
  cursor: pointer;
  margin-bottom: 4px;
}
.session-item:hover {
  background: rgba(127, 127, 127, 0.1);
}
.session-item.active {
  background: var(--el-color-primary-light-9);
  color: var(--el-color-primary);
}
.session-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.9em;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
}
.session-title-text {
  overflow: hidden;
  text-overflow: ellipsis;
}
/* 执行中动画：标题前的旋转图标 */
.session-spinner {
  flex-shrink: 0;
  color: var(--el-color-primary);
}
.session-meta {
  font-size: 0.75em;
  opacity: 0.6;
  margin-top: 2px;
}
.loading {
  text-align: center;
  padding: 8px;
}
.bottom {
  padding: 10px 12px;
  flex-shrink: 0;
}
.settings-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 8px;
  border: none;
  background: transparent;
  color: inherit;
  font-size: 0.9em;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
}
.settings-btn:hover {
  background: rgba(127, 127, 127, 0.1);
}
</style>
