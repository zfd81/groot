<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { api, ApiError } from '../../api/client'
import type { ClusterMemberInfo, ClusterResp } from '../../api/types'

const { t } = useI18n()

// 后端集群角色常量（internal/cluster/election.go）
const ROLE_LEADER = 'leader'

const members = ref<ClusterMemberInfo[]>([])
const loading = ref(false)

// 心跳每 3s 写库，面板按 5s 轮询保持展示新鲜；仅在面板挂载期间运行。
const REFRESH_INTERVAL = 5000
let timer: ReturnType<typeof setInterval> | null = null

function fmtTime(ms: number): string {
  return new Date(ms).toLocaleString()
}

// silent: 轮询刷新不显示 loading 遮罩，也不弹错误提示，避免打断阅读
async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const resp = await api.get<ClusterResp>('/web/cluster')
    members.value = resp.members || []
  } catch (e) {
    if (!silent) {
      const message = e instanceof ApiError || e instanceof Error ? e.message : String(e)
      ElNotification.error({ title: t('cluster.title'), message })
    }
  } finally {
    if (!silent) loading.value = false
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => void load(true), REFRESH_INTERVAL)
})

onUnmounted(() => {
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
      </div>
      <div class="item-meta">
        <span>{{ t('cluster.joinedAt') }}: {{ fmtTime(m.created_at) }}</span>
        <span>{{ t('cluster.heartbeatAt') }}: {{ fmtTime(m.heartbeat_at) }}</span>
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
