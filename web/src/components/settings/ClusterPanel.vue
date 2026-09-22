<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { Refresh, RefreshRight } from '@element-plus/icons-vue'
// ElMessageBox / ElLoading 不在 unplugin 自动导入范围内，需显式引入。
// message-box 样式须手动引入；loading 样式已由本文件的 v-loading 指令按需引入，无需重复。
import { ElMessageBox, ElLoading } from 'element-plus'
import 'element-plus/es/components/message-box/style/css'
import { api, ApiError } from '../../api/client'
import type { ClusterMemberInfo, ClusterResp, RestartResp } from '../../api/types'

const { t } = useI18n()

// 后端集群角色常量（internal/cluster/election.go）
const ROLE_LEADER = 'leader'

const members = ref<ClusterMemberInfo[]>([])
// 当前为浏览器服务的实例的 reg_id（GET /web/cluster 的 self 字段）
const selfId = ref('')
const loading = ref(false)

// 心跳每 3s 写库，面板按 5s 轮询保持展示新鲜；仅在面板挂载期间运行。
const REFRESH_INTERVAL = 5000
let timer: ReturnType<typeof setInterval> | null = null
// 组件卸载后不再恢复轮询定时器（waitForSelfRestart 可能跨越卸载时刻）
let unmounted = false

// 重启中状态以地址（IP:PORT）为键：实例重启后 reg_id 会变而地址不变。
// 记录点击时的旧 reg_id：同地址出现了不同 reg_id 的成员即视为已恢复（不依赖浏览器与服务器的时钟）；
// since 仅用于超时判定。
const RESTART_TIMEOUT = 60_000
const restarting = ref<Map<string, { since: number; oldRegId: string }>>(new Map())
// 正在提交重启请求的实例地址，用于防止重复点击
const submitting = ref('')

// 重启本机时轮询 /web/health 的参数
const SELF_POLL_INTERVAL = 1000
const SELF_POLL_TIMEOUT = 90_000
const SELF_MIN_DOWN_WAIT = 10_000

function fmtTime(ms: number): string {
  return new Date(ms).toLocaleString()
}

function isRestarting(m: ClusterMemberInfo): boolean {
  return restarting.value.has(m.address)
}

// 每次刷新后核对重启中集合：同地址出现了不同 reg_id 的成员即视为已恢复；超时则放弃并提示。
function reconcileRestarting() {
  const now = Date.now()
  for (const [address, st] of restarting.value) {
    const back = members.value.some((m) => m.address === address && m.reg_id !== st.oldRegId)
    if (back) {
      restarting.value.delete(address)
    } else if (now - st.since > RESTART_TIMEOUT) {
      restarting.value.delete(address)
      ElNotification.warning({ title: t('cluster.restartTitle'), message: t('cluster.restartTimeout', { address }) })
    }
  }
}

// silent: 轮询刷新不显示 loading 遮罩，也不弹错误提示，避免打断阅读
async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const resp = await api.get<ClusterResp>('/web/cluster')
    members.value = resp.members || []
    selfId.value = resp.self || ''
    reconcileRestarting()
  } catch (e) {
    if (!silent) {
      const message = e instanceof ApiError || e instanceof Error ? e.message : String(e)
      ElNotification.error({ title: t('cluster.title'), message })
    }
  } finally {
    if (!silent) loading.value = false
  }
}

async function confirmRestart(m: ClusterMemberInfo) {
  const self = m.reg_id === selfId.value
  const text = self
    ? t('cluster.restartConfirmSelf', { address: m.address })
    : t('cluster.restartConfirm', { address: m.address })
  try {
    await ElMessageBox.confirm(text, t('cluster.restartTitle'), {
      confirmButtonText: t('cluster.restart'),
      cancelButtonText: t('common.cancel'),
      type: 'warning',
    })
  } catch {
    return // 取消
  }
  await doRestart(m)
}

async function doRestart(m: ClusterMemberInfo) {
  submitting.value = m.address
  try {
    const resp = await api.post<RestartResp>(`/web/cluster/${encodeURIComponent(m.reg_id)}/restart`)
    if (resp.self) {
      await waitForSelfRestart()
      return
    }
    restarting.value.set(m.address, { since: Date.now(), oldRegId: m.reg_id })
    ElNotification.success({ title: t('cluster.restartTitle'), message: t('cluster.restartAccepted', { address: m.address }) })
  } catch (e) {
    if (e instanceof ApiError && e.code === 'member_not_found') {
      ElNotification.warning({ title: t('cluster.restartTitle'), message: t('cluster.restartMemberGone') })
      void load(true)
      return
    }
    if (e instanceof ApiError && e.code === 'restart_unsupported') {
      ElNotification.warning({ title: t('cluster.restartTitle'), message: t('cluster.restartUnsupported') })
      return
    }
    const message = e instanceof ApiError || e instanceof Error ? e.message : String(e)
    ElNotification.error({ title: t('cluster.restartTitle'), message })
  } finally {
    submitting.value = ''
  }
}

// 免登录探测健康端点：只关心能否拿到 200，不走 api 封装（避免 401 拦截）。
async function healthOK(): Promise<boolean> {
  try {
    const r = await fetch('/web/health', { cache: 'no-store', credentials: 'same-origin' })
    return r.ok
  } catch {
    return false
  }
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms))
}

// 重启本机：全屏遮罩 → 等旧进程退出（至少一次失败或 10 秒）→ 等新进程连续两次健康 → 整页跳转登录。
// 登录会话在工作进程内存中，重启后必然失效；整页跳转而非路由跳转，顺带重置前端状态。
async function waitForSelfRestart() {
  const mask = ElLoading.service({ lock: true, text: t('cluster.restartSelfWaiting'), background: 'rgba(0, 0, 0, 0.6)' })
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
  const started = Date.now()
  try {
    let sawDown = false
    let okStreak = 0
    while (Date.now() - started < SELF_POLL_TIMEOUT) {
      await sleep(SELF_POLL_INTERVAL)
      const ok = await healthOK()
      if (!ok) {
        sawDown = true
        okStreak = 0
        continue
      }
      // 尚未观察到停机且未过最短等待期：可能仍是旧进程在响应，继续等
      if (!sawDown && Date.now() - started < SELF_MIN_DOWN_WAIT) continue
      okStreak++
      if (okStreak >= 2) {
        window.location.href = `${import.meta.env.BASE_URL}login`
        return
      }
    }
    ElNotification.error({ title: t('cluster.restartTitle'), message: t('cluster.restartSelfTimeout') })
  } finally {
    mask.close()
    if (timer === null && !unmounted) timer = setInterval(() => void load(true), REFRESH_INTERVAL)
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => void load(true), REFRESH_INTERVAL)
})

onUnmounted(() => {
  unmounted = true
  if (timer !== null) clearInterval(timer)
})
</script>

<template>
  <div v-loading="loading">
    <div class="label-desc panel-desc">{{ t('cluster.desc') }}</div>
    <div class="panel-toolbar">
      <el-button size="small" text :icon="Refresh" @click="load()">{{ t('cluster.refresh') }}</el-button>
    </div>

    <div v-for="m in members" :key="m.reg_id" class="list-item">
      <div class="item-header">
        <span class="member-addr mono">{{ m.address }}</span>
        <el-tag v-if="m.role === ROLE_LEADER" size="small" type="success" effect="light" class="role-tag">
          {{ t('cluster.leader') }}
        </el-tag>
        <el-tag v-else size="small" effect="plain" round class="role-tag">
          {{ t('cluster.follower') }}
        </el-tag>
        <el-tag v-if="m.reg_id === selfId" size="small" type="info" effect="plain" round class="role-tag">
          {{ t('cluster.self') }}
        </el-tag>
        <span class="header-spacer"></span>
        <el-tag v-if="isRestarting(m)" size="small" type="warning" effect="light" round class="role-tag">
          {{ t('cluster.restarting') }}
        </el-tag>
        <el-tooltip v-else :content="t('cluster.restart')" :show-after="200" placement="top">
          <el-button
            size="small"
            text
            :icon="RefreshRight"
            class="restart-btn"
            :aria-label="t('cluster.restart')"
            :disabled="submitting === m.address"
            @click="confirmRestart(m)"
          />
        </el-tooltip>
      </div>
      <div class="item-meta">
        <span>{{ t('cluster.joinedAt') }}: {{ fmtTime(m.created_at) }}</span>
        <span>{{ t('cluster.heartbeatAt') }}: {{ fmtTime(m.heartbeat_at) }}</span>
        <span>PID: {{ m.pid }}</span>
      </div>
    </div>

    <el-empty v-if="!loading && !members.length" :description="t('cluster.empty')" :image-size="60" />
  </div>
</template>

<style scoped>
.panel-desc {
  margin-bottom: 8px;
}

.panel-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}

.label-desc {
  font-size: 0.82em;
  opacity: 0.6;
}

.list-item {
  padding: 16px 20px;
  border: 1px solid var(--el-border-color-light, rgba(127, 127, 127, 0.2));
  border-radius: 12px;
  margin-bottom: 12px;
}

.item-header {
  display: flex;
  align-items: center;
}

/* 实例地址 IP:PORT：等宽字体便于对齐比对，字号与卡片标题同级 */
.member-addr {
  font-weight: 600;
  font-size: 1em;
  opacity: 1;
}

.role-tag {
  margin-left: 8px;
  flex-shrink: 0;
}

/* 把重启按钮/重启中标签推到卡片标题行最右侧 */
.header-spacer {
  flex: 1;
}

.restart-btn {
  padding: 4px 6px;
}

.item-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 16px;
  margin-top: 6px;
  font-size: 0.85em;
  opacity: 0.65;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
</style>
