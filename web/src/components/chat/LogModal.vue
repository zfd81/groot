<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { Loading, Close, Refresh } from '@element-plus/icons-vue'
import { api } from '../../api/client'
import type { SessionLogEntry, SessionLogsResp } from '../../api/types'

const { t } = useI18n()

const props = defineProps<{ show: boolean; sessionId: string }>()
const emit = defineEmits<{ 'update:show': [v: boolean] }>()

const LEVELS = ['all', 'error', 'warn', 'info', 'debug'] as const
type Level = (typeof LEVELS)[number]

const level = ref<Level>('all')
const loading = ref(false)
const errorMsg = ref('')
const logs = ref<SessionLogEntry[]>([])
const truncated = ref(false)

// 请求序号：关闭弹窗或重新加载时使在途响应失效
let requestSeq = 0

// 打开时重置筛选并加载；关闭时丢弃在途响应
watch(
  () => props.show,
  (v) => {
    if (!v) {
      requestSeq++
      return
    }
    level.value = 'all'
    void load()
  }
)

async function load() {
  const seq = ++requestSeq
  loading.value = true
  errorMsg.value = ''
  try {
    const resp = await api.get<SessionLogsResp>(
      `/web/logs/${encodeURIComponent(props.sessionId)}`
    )
    if (seq !== requestSeq) return
    logs.value = resp.logs || []
    truncated.value = resp.truncated
  } catch {
    if (seq !== requestSeq) return
    errorMsg.value = t('logs.failed')
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

// 级别筛选：纯前端本地过滤
const filtered = computed(() =>
  level.value === 'all' ? logs.value : logs.value.filter((l) => l.level === level.value)
)

// 附加字段拼成 "k=v  k=v"；非字符串值走 JSON 以免出现 [object Object]
function fmtFields(f?: Record<string, unknown>): string {
  if (!f) return ''
  return Object.entries(f)
    .map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
    .join('  ')
}

// 预先算出每行的展示字段，避免模板里重复调用格式化函数
const rows = computed(() =>
  filtered.value.map((e) => ({
    time: fmtTime(e.timestamp),
    level: e.level,
    levelText: (e.level || '').toUpperCase(),
    message: e.message,
    caller: e.caller || '',
    fields: fmtFields(e.fields),
  }))
)

function levelLabel(lv: Level): string {
  return lv === 'all' ? t('logs.all') : lv
}

// ISO 时间戳 → HH:mm:ss；zap 的时区偏移不带冒号（+0300），Safari 解析会失败，
// 此时用正则直接截取时间段，最后才回退原文
function fmtTime(ts: string): string {
  const d = new Date(ts)
  if (isNaN(d.getTime())) {
    const m = ts.match(/T(\d{2}:\d{2}:\d{2})/)
    return m ? m[1] : ts
  }
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
</script>

<template>
  <el-dialog
    :model-value="props.show"
    :show-close="false"
    width="720px"
    top="80px"
    class="logs-dialog"
    @update:model-value="(v: boolean) => emit('update:show', v)"
  >
    <!-- 顶部行：标题 + 级别筛选 + 刷新 + 关闭 -->
    <div class="logs-head">
      <span class="head-title">{{ t('logs.title') }}</span>
      <div class="level-tabs">
        <button
          v-for="lv in LEVELS"
          :key="lv"
          class="level-tab"
          :class="{ active: level === lv, ['lv-' + lv]: true }"
          type="button"
          @click="level = lv"
        >
          {{ levelLabel(lv) }}
        </button>
      </div>
      <button
        class="head-btn"
        type="button"
        :title="t('logs.refresh')"
        :disabled="loading"
        @click="load()"
      >
        <el-icon :size="15"><Refresh /></el-icon>
      </button>
      <div class="head-divider" />
      <button class="head-btn" type="button" @click="emit('update:show', false)">
        <el-icon :size="16"><Close /></el-icon>
      </button>
    </div>

    <!-- 日志列表区 -->
    <div class="log-area">
      <div v-if="loading" class="state-line">
        <el-icon class="is-loading"><Loading /></el-icon>
      </div>
      <div v-else-if="errorMsg" class="state-line">
        {{ errorMsg }}
        <button class="retry-btn" type="button" @click="load()">
          {{ t('logs.retry') }}
        </button>
      </div>
      <div v-else-if="!logs.length" class="state-line">{{ t('logs.empty') }}</div>
      <div v-else-if="!filtered.length" class="state-line">{{ t('logs.emptyLevel') }}</div>
      <template v-else>
        <div
          v-for="(row, i) in rows"
          :key="i"
          class="log-row"
          :class="{ 'is-error': row.level === 'error' }"
        >
          <span class="log-time">{{ row.time }}</span>
          <span class="log-level" :class="'lv-' + row.level">{{ row.levelText }}</span>
          <div class="log-main">
            <div class="log-msg">{{ row.message }}</div>
            <div v-if="row.caller || row.fields" class="log-meta">
              <span v-if="row.caller" class="meta-caller">{{ row.caller }}</span>
              <span v-if="row.fields" class="meta-fields">{{ row.fields }}</span>
            </div>
          </div>
        </div>
      </template>
    </div>

    <!-- 底部：条数统计与截断提示。容器常驻以保持弹窗高度稳定，
         加载中与失败时不显示上一次查询留下的陈旧计数 -->
    <div class="logs-foot">
      <template v-if="!loading && !errorMsg">
        <span>{{ t('logs.count', { n: filtered.length }) }}</span>
        <span v-if="truncated" class="foot-truncated">{{ t('logs.truncated') }}</span>
      </template>
    </div>
  </el-dialog>
</template>

<style scoped>
.logs-head {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 56px;
  padding: 0 16px 0 20px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}
.head-title {
  font-weight: 600;
  flex-shrink: 0;
}
.level-tabs {
  flex: 1;
  display: flex;
  gap: 4px;
  justify-content: flex-end;
  min-width: 0;
}
.level-tab {
  padding: 3px 10px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font-size: 0.82em;
  font-family: inherit;
  opacity: 0.6;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
}
.level-tab:hover {
  opacity: 0.85;
}
.level-tab.active {
  background: rgba(127, 127, 127, 0.15);
  opacity: 1;
  font-weight: 500;
}
.level-tab.active.lv-error {
  color: var(--el-color-error);
}
.level-tab.active.lv-warn {
  color: var(--el-color-warning);
}
.head-btn {
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
.head-btn:hover:not(:disabled) {
  background: rgba(127, 127, 127, 0.12);
  opacity: 0.9;
}
.head-btn:disabled {
  opacity: 0.3;
  cursor: default;
}
.head-divider {
  flex-shrink: 0;
  width: 1px;
  height: 22px;
  background: rgba(127, 127, 127, 0.3);
}
/* 列表区：固定高度，使弹窗整体高度与 SearchModal 打开时一致
   （视口上下各留 80px），并扣掉顶部行 56px 与底部统计行 32px；
   固定而非 max-height，避免高度随日志条数跳动 */
.log-area {
  height: calc(100vh - 248px);
  overflow-y: auto;
  padding: 8px 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.82em;
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker, rgba(127, 127, 127, 0.35)) transparent;
}
.log-area::-webkit-scrollbar {
  width: 6px;
}
.log-area::-webkit-scrollbar-track {
  background: transparent;
}
.log-area::-webkit-scrollbar-thumb {
  background: var(--el-border-color-darker, rgba(127, 127, 127, 0.35));
  border-radius: 3px;
}
/* 多行消息时时间/级别需与消息首行顶端对齐 */
.log-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 4px 8px;
  border-radius: 4px;
}
.log-time,
.log-level {
  line-height: 1.5;
}
.log-row.is-error {
  background: var(--el-color-error-light-9, rgba(245, 108, 108, 0.1));
}
.log-time {
  flex-shrink: 0;
  opacity: 0.55;
}
.log-level {
  flex-shrink: 0;
  width: 46px;
  font-weight: 600;
}
.log-level.lv-error {
  color: var(--el-color-error);
}
.log-level.lv-warn {
  color: var(--el-color-warning);
}
.log-level.lv-info {
  color: var(--el-color-info, inherit);
  opacity: 0.8;
}
.log-level.lv-debug {
  opacity: 0.5;
}
.log-main {
  flex: 1;
  min-width: 0;
}
/* 消息全文展示：保留原始换行，长行按字符断行不溢出 */
.log-msg {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.5;
}
/* 附加信息行：调用位置与结构化字段 */
.log-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 2px;
  font-size: 0.92em;
  opacity: 0.5;
  overflow-wrap: anywhere;
}
.meta-caller {
  flex-shrink: 0;
}
.state-line {
  text-align: center;
  padding: 24px 0;
  opacity: 0.6;
  font-size: 0.9em;
}
.retry-btn {
  margin-left: 8px;
  padding: 2px 8px;
  border: 1px solid rgba(127, 127, 127, 0.3);
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font-family: inherit;
  cursor: pointer;
}
/* 高度固定 32px：与 .log-area 的高度计算相呼应，
   内容为空（加载中/失败）时也不改变弹窗整体高度 */
.logs-foot {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 32px;
  padding: 0 20px;
  border-top: 1px solid rgba(127, 127, 127, 0.15);
  font-size: 0.78em;
  opacity: 0.6;
}
.foot-truncated {
  color: var(--el-color-warning);
}
</style>

<!-- 与 SearchModal 一致：去默认标题栏与内边距，16px 圆角 -->
<style>
.logs-dialog {
  padding: 0;
  border-radius: 16px;
  overflow: hidden;
}
.logs-dialog .el-dialog__header {
  display: none;
}
.logs-dialog .el-dialog__body {
  padding: 0;
}
</style>
