<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { Search, Loading, Close } from '@element-plus/icons-vue'
import { api } from '../../api/client'
import type {
  SearchResp,
  SearchResultItem,
  SessionHistoryResp,
  SessionSummary,
} from '../../api/types'

const { t } = useI18n()

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  'update:show': [v: boolean]
  // 选中一条结果：round 缺省表示从「最近会话」进入，不做轮次定位
  select: [sid: string, round?: number]
}>()

const keyword = ref('')
const loading = ref(false)
const errorMsg = ref('')
const results = ref<SearchResultItem[]>([])
const recent = ref<SessionSummary[]>([])
const activeIndex = ref(0)
const inputRef = ref<HTMLInputElement | null>(null)

let debounceTimer: ReturnType<typeof setTimeout> | null = null
// 请求序号：响应回来时序号已变说明输入又变了，丢弃过期响应
let requestSeq = 0

// 打开时重置状态、拉最近会话、聚焦输入框
watch(
  () => props.show,
  async (v) => {
    if (!v) {
      // 关闭时取消挂起的防抖与在途请求
      if (debounceTimer) clearTimeout(debounceTimer)
      requestSeq++
      return
    }
    keyword.value = ''
    results.value = []
    recent.value = []
    errorMsg.value = ''
    activeIndex.value = 0
    // 若上次关闭时输入框非空，重置 keyword 会触发下方侦听器（其中 requestSeq++）。
    // 必须等它先执行完再发最近会话请求，否则该请求会被误判为过期而丢弃。
    await nextTick()
    void loadRecent()
  }
)

// 输入 300ms 防抖触发搜索；清空则回到最近会话视图
watch(keyword, (kw) => {
  if (debounceTimer) clearTimeout(debounceTimer)
  errorMsg.value = ''
  activeIndex.value = 0
  const q = kw.trim()
  if (!q) {
    // 使在途搜索请求失效，避免旧响应污染最近会话视图
    requestSeq++
    results.value = []
    loading.value = false
    return
  }
  loading.value = true
  debounceTimer = setTimeout(() => void doSearch(q), 300)
})

async function loadRecent() {
  const seq = ++requestSeq
  loading.value = true
  try {
    const resp = await api.get<SessionHistoryResp>('/sess/history?limit=20')
    if (seq !== requestSeq) return
    recent.value = resp.sessions || []
  } catch {
    if (seq !== requestSeq) return
    errorMsg.value = t('search.failed')
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

async function doSearch(q: string) {
  const seq = ++requestSeq
  try {
    const resp = await api.get<SearchResp>(
      `/sess/search?q=${encodeURIComponent(q)}&limit=20`
    )
    if (seq !== requestSeq) return
    results.value = resp.results || []
  } catch {
    if (seq !== requestSeq) return
    errorMsg.value = t('search.failed')
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

// 当前列表项总数（空输入=最近会话，非空=搜索结果）
// 清除关键词并把焦点还给输入框，回到最近会话视图
function clearKeyword() {
  keyword.value = ''
  inputRef.value?.focus()
}

function itemCount(): number {
  return keyword.value.trim() ? results.value.length : recent.value.length
}

function pickRecent(s: SessionSummary) {
  emit('update:show', false)
  emit('select', s.session_id)
}

function pickResult(r: SearchResultItem) {
  emit('update:show', false)
  emit('select', r.session_id, r.round)
}

function onKeydown(e: KeyboardEvent) {
  const n = itemCount()
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    if (n) activeIndex.value = (activeIndex.value + 1) % n
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    if (n) activeIndex.value = (activeIndex.value - 1 + n) % n
  } else if (e.key === 'Enter') {
    e.preventDefault()
    if (loading.value) return
    // 空输入时处于最近会话视图，回车不做任何事，避免刚打开弹窗就误进入会话；
    // 最近会话通过点击进入
    if (!keyword.value.trim()) return
    if (!n) return
    pickResult(results.value[activeIndex.value])
  }
}

// 关键词高亮：按 keyword（大小写不敏感）把 snippet 切成命中/未命中分段，
// 用文本节点渲染，不用 v-html，避免 XSS。
function segments(text: string): { text: string; hit: boolean }[] {
  const kw = keyword.value.trim()
  if (!kw) return [{ text, hit: false }]
  const out: { text: string; hit: boolean }[] = []
  const lower = text.toLowerCase()
  const k = kw.toLowerCase()
  let pos = 0
  for (;;) {
    const idx = lower.indexOf(k, pos)
    if (idx < 0) {
      if (pos < text.length) out.push({ text: text.slice(pos), hit: false })
      break
    }
    if (idx > pos) out.push({ text: text.slice(pos, idx), hit: false })
    out.push({ text: text.slice(idx, idx + kw.length), hit: true })
    pos = idx + kw.length
  }
  return out
}

function fmtDate(ms: number): string {
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

function recentTime(s: SessionSummary): string {
  const iso = s.last_active_at || s.created_at
  const ts = new Date(iso).getTime()
  return isNaN(ts) ? '' : fmtDate(ts)
}

function recentTitle(s: SessionSummary): string {
  return s.title?.trim() || s.session_id.slice(0, 8)
}
</script>

<template>
  <el-dialog
    :model-value="props.show"
    :show-close="false"
    width="660px"
    top="80px"
    class="search-dialog"
    @opened="inputRef?.focus()"
    @update:model-value="(v: boolean) => emit('update:show', v)"
  >
    <div @keydown="onKeydown">
      <!-- 顶部搜索行：贴弹窗顶部，无边框输入 + 分隔线 + 关闭按钮 -->
      <div class="search-head">
        <el-icon class="head-icon" :size="18"><Search /></el-icon>
        <input
          ref="inputRef"
          v-model="keyword"
          class="head-input"
          type="text"
          :placeholder="t('search.placeholder')"
        />
        <button
          v-if="keyword"
          class="head-clear"
          type="button"
          @click="clearKeyword"
        >
          {{ t('search.clear') }}
        </button>
        <div class="head-divider" />
        <button class="head-close" type="button" @click="emit('update:show', false)">
          <el-icon :size="16"><Close /></el-icon>
        </button>
      </div>
      <div class="result-area">
        <div v-if="errorMsg" class="state-line">{{ errorMsg }}</div>
        <div v-else-if="loading" class="state-line">
          <el-icon class="is-loading"><Loading /></el-icon>
        </div>
        <!-- 空输入：最近会话列表 -->
        <template v-else-if="!keyword.trim()">
          <div class="group-label">{{ t('search.recent') }}</div>
          <div
            v-for="(s, i) in recent"
            :key="s.session_id"
            class="item"
            :class="{ active: i === activeIndex }"
            @mouseenter="activeIndex = i"
            @click="pickRecent(s)"
          >
            <div class="item-title">{{ recentTitle(s) }}</div>
            <div class="item-time">{{ recentTime(s) }}</div>
          </div>
          <div v-if="!recent.length" class="state-line">{{ t('sidebar.empty') }}</div>
        </template>
        <!-- 有输入：轮次级搜索结果 -->
        <template v-else>
          <div
            v-for="(r, i) in results"
            :key="r.chat_id"
            class="item"
            :class="{ active: i === activeIndex }"
            @mouseenter="activeIndex = i"
            @click="pickResult(r)"
          >
            <div class="item-title">{{ r.title?.trim() || r.session_id.slice(0, 8) }}</div>
            <div class="item-snippet">
              <span class="match-tag">{{
                r.matched_field === 'result'
                  ? t('search.matchResult')
                  : t('search.matchInstruction')
              }}</span>
              <span
                v-for="(seg, si) in segments(r.snippet)"
                :key="si"
                :class="{ hl: seg.hit }"
                >{{ seg.text }}</span
              >
            </div>
            <div class="item-time">{{ fmtDate(r.timestamp) }}</div>
          </div>
          <div v-if="!results.length" class="state-line">{{ t('search.noResults') }}</div>
        </template>
      </div>
    </div>
  </el-dialog>
</template>

<style scoped>
/* 顶部搜索行：贴弹窗顶部，底部分隔线 */
.search-head {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 60px;
  padding: 0 16px 0 20px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}
.head-icon {
  flex-shrink: 0;
  opacity: 0.55;
}
.head-input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  background: transparent;
  color: inherit;
  font-size: 1.05em;
  font-family: inherit;
}
.head-input::placeholder {
  color: inherit;
  opacity: 0.4;
}
/* 清除按钮：仅在输入框有内容时出现 */
.head-clear {
  flex-shrink: 0;
  padding: 2px 4px;
  border: none;
  background: transparent;
  color: inherit;
  font-size: 1em;
  font-family: inherit;
  opacity: 0.45;
  cursor: pointer;
  transition: opacity 0.15s;
}
.head-clear:hover {
  opacity: 0.75;
}
.head-divider {
  flex-shrink: 0;
  width: 1px;
  height: 22px;
  background: rgba(127, 127, 127, 0.3);
}
.head-close {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  opacity: 0.6;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
}
.head-close:hover {
  background: rgba(127, 127, 127, 0.12);
  opacity: 0.9;
}
/* 结果区：占满弹窗宽度，滚动条贴弹窗右缘。
   上下各留 80px 空隙：视口高 - 80*2 - 搜索行 60px */
.result-area {
  max-height: calc(100vh - 220px);
  overflow-y: auto;
  padding: 8px;
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker, rgba(127, 127, 127, 0.35)) transparent;
}
.result-area::-webkit-scrollbar {
  width: 6px;
}
.result-area::-webkit-scrollbar-track {
  background: transparent;
}
.result-area::-webkit-scrollbar-thumb {
  background: var(--el-border-color-darker, rgba(127, 127, 127, 0.35));
  border-radius: 3px;
}
.group-label {
  font-size: 0.78em;
  opacity: 0.6;
  padding: 6px 12px 2px;
}
.item {
  padding: 10px 12px;
  border-radius: 8px;
  cursor: pointer;
}
.item.active {
  background: rgba(127, 127, 127, 0.12);
}
.item-title {
  font-size: 0.92em;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.item-snippet {
  font-size: 0.82em;
  opacity: 0.75;
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.item-snippet .hl {
  color: var(--el-color-primary);
  font-weight: 600;
}
.match-tag {
  display: inline-block;
  font-size: 0.9em;
  padding: 0 4px;
  margin-right: 6px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.15);
}
.item-time {
  font-size: 0.72em;
  opacity: 0.5;
  margin-top: 2px;
}
.state-line {
  text-align: center;
  padding: 16px 0;
  opacity: 0.6;
  font-size: 0.9em;
}
</style>

<!-- 弹窗根元素在 scoped 作用域外：圆角外框与设置弹窗一致（16px），
     去掉默认标题栏与内边距，使搜索行贴顶、滚动条贴弹窗右缘 -->
<style>
.search-dialog {
  padding: 0;
  border-radius: 16px;
  overflow: hidden;
}
.search-dialog .el-dialog__header {
  display: none;
}
.search-dialog .el-dialog__body {
  padding: 0;
}
</style>
