<script setup lang="ts">
import { CirclePlus, Setting, Loading, Search } from '@element-plus/icons-vue'
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
  openSearch: []
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
  <!-- 收起态窄栏与展开态侧栏始终挂载、叠放，靠透明度交叉淡入淡出切换，
       使收起与展开两个方向的观感一致，且宽度过渡期间不发生 DOM 挂载卸载。 -->
  <div class="rail" :class="props.collapsed ? 'panel-shown' : 'panel-hidden'">
    <img class="rail-logo" src="../../assets/groot-icon.png" alt="Groot" />
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
    <button class="rail-btn" type="button" :title="t('sidebar.search')" @click="emit('openSearch')">
      <el-icon :size="20"><Search /></el-icon>
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

  <div class="sidebar" :class="props.collapsed ? 'panel-hidden' : 'panel-shown'">
    <!-- 品牌标识（图标 + 文字），高度与主区顶部栏对齐 -->
    <div class="brand">
      <img class="brand-logo" src="../../assets/groot-icon.png" alt="Groot" />
      <span class="brand-name">Groot</span>
      <span class="brand-badge">AGENT</span>
      <button
        class="search-btn"
        type="button"
        :title="t('sidebar.search')"
        @click="emit('openSearch')"
      >
        <el-icon :size="20"><Search /></el-icon>
      </button>
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
/* 两个面板叠放在 .sider（定位上下文）左上角，各自写死最终宽度，
   宽度过渡期间只被 .sider 裁切，内部不重排、图标不横向漂移。 */
.rail,
.sidebar {
  position: absolute;
  top: 0;
  left: 0;
}
/* 淡出走过渡前半段，淡入走后半段，收起与展开两个方向对称。
   visibility 在淡出结束后才置 hidden，同时把隐藏面板移出 Tab 键序与无障碍树。 */
.panel-shown {
  opacity: 1;
  visibility: visible;
  transition:
    opacity 0.1s ease 0.1s,
    visibility 0s;
}
.panel-hidden {
  opacity: 0;
  visibility: hidden;
  /* 淡出期间该面板仍压在上层，须立即停止接收点击 */
  pointer-events: none;
  transition:
    opacity 0.1s ease,
    visibility 0s linear 0.1s;
}
/* 收起态固定 52px，与 .sider 收起后的宽度一致 */
.rail {
  width: 52px;
  height: 100%;
  box-sizing: border-box;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding-top: 12px;
}
.rail-logo {
  width: 26px;
  height: 26px;
  object-fit: contain;
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
/* 展开态固定 260px：与 .sider 展开后的宽度一致，宽度过渡期间内部不被挤压重排 */
.sidebar {
  width: 260px;
  height: 100%;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
}
.brand {
  height: 56px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
  flex-shrink: 0;
}
.brand-logo {
  width: 26px;
  height: 26px;
  object-fit: contain;
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
.search-btn {
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
.search-btn:hover {
  background: rgba(127, 127, 127, 0.1);
}
.collapse-btn {
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
/* 图标按钮只在鼠标悬浮时有视觉变化：去掉浏览器默认焦点环
   （弹窗关闭后焦点回到按钮时会出现），且不加任何焦点态底色 */
.rail-btn:focus,
.rail-btn:focus-visible,
.search-btn:focus,
.search-btn:focus-visible,
.collapse-btn:focus,
.collapse-btn:focus-visible {
  outline: none;
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
