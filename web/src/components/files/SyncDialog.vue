<!-- 配置同步差异对话框：展示本地与数据库的差异清单，支持推送/拉取。 -->
<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { syncApi } from '../../api/sync'
import { ApiError } from '../../api/client'
import type { SyncDiffEntry } from '../../api/types'

const props = defineProps<{
  modelValue: boolean
  // 同步范围：undefined 表示全部配置，否则为单个资源相对路径
  scope?: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  // pull 成功后通知父组件刷新文件树并关闭已打开的编辑器
  pulled: []
  pushed: []
}>()

const { t } = useI18n()

const loading = ref(false)
const applying = ref(false)
const inSync = ref(false)
const needsRestart = ref(false)
const disabled = ref(false)
const entries = ref<SyncDiffEntry[]>([])

const paths = computed(() => (props.scope ? [props.scope] : []))

// 各方向会导致删除的条目数：推送会从数据库删除 D，拉取会删除本地的 A
const deleteCount = computed(() => ({
  push: entries.value.filter((e) => e.status === 'D').length,
  pull: entries.value.filter((e) => e.status === 'A').length,
}))

const statusLabel = (s: SyncDiffEntry['status']) =>
  s === 'A' ? t('files.syncStatusA') : s === 'M' ? t('files.syncStatusM') : t('files.syncStatusD')

// 版本控制惯例配色：A 绿、M 橙、D 红
const statusType = (s: SyncDiffEntry['status']) =>
  s === 'A' ? 'success' : s === 'M' ? 'warning' : 'danger'

// 远端更新时间（毫秒时间戳）；本地新建（远端无记录）时后端省略该字段，不显示
const formatTime = (ms?: number) => (ms ? new Date(ms).toLocaleString() : '')

async function loadDiff() {
  loading.value = true
  disabled.value = false
  try {
    const resp = await syncApi.diff(paths.value)
    entries.value = resp.entries ?? []
    inSync.value = resp.inSync
    needsRestart.value = resp.needsRestart
  } catch (e) {
    // ApiError.status 是 HTTP 数字码，业务 status 值在 code 字段（见 api/client.ts）
    if (e instanceof ApiError && e.code === 'sync_disabled') {
      disabled.value = true
      entries.value = []
    } else {
      ElMessage.error(e instanceof ApiError ? e.message : t('files.syncFailed'))
      close()
    }
  } finally {
    loading.value = false
  }
}

async function apply(direction: 'push' | 'pull') {
  // 仅当操作会导致删除时二次确认（推送含 D / 拉取含 A）；纯 M 或纯单向新增直接执行
  const delCount = deleteCount.value[direction]
  if (delCount > 0) {
    const hint = direction === 'push'
      ? t('files.syncPushDeleteHint', { count: delCount })
      : t('files.syncPullDeleteHint', { count: delCount })
    try {
      await ElMessageBox.confirm(hint, t('files.syncTitle'), { type: 'warning' })
    } catch {
      return // 用户取消
    }
  }

  applying.value = true
  try {
    if (direction === 'push') {
      await syncApi.push(paths.value)
      ElMessage.success(t('files.syncPushDone'))
      emit('pushed')
    } else {
      await syncApi.pull(paths.value)
      ElMessage.success(t('files.syncPullDone'))
      emit('pulled')
      // 重启提示只对拉取有意义：push 只写数据库，不影响本地运行时
      if (needsRestart.value) {
        ElMessage.warning(t('files.syncRestartHint'))
      }
    }
    close()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : t('files.syncFailed'))
    // 失败后重新比较，让用户看到当前真实状态
    await loadDiff()
  } finally {
    applying.value = false
  }
}

function close() {
  emit('update:modelValue', false)
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) loadDiff()
  },
)
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    :title="t('files.syncTitle')"
    width="640px"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div v-loading="loading">
      <p class="sync-scope">
        {{ scope ? scope : t('files.syncScopeAll') }}
      </p>

      <el-alert v-if="disabled" type="info" :closable="false" :title="t('files.syncDisabled')" />
      <el-alert
        v-else-if="inSync && !loading"
        type="success"
        :closable="false"
        :title="t('files.syncInSync')"
      />
      <el-table v-else-if="entries.length" :data="entries" max-height="380" size="small">
        <el-table-column :label="t('files.syncColStatus')" width="120">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status)" size="small" disable-transitions>
              {{ statusLabel(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('files.syncColPath')">
          <template #default="{ row }">
            <span class="sync-path">{{ row.path }}</span>
            <span v-if="row.remoteUpdatedAt" class="sync-time">
              {{ formatTime(row.remoteUpdatedAt) }}
            </span>
            <el-tag v-if="row.remoteDeleted" type="danger" size="small" effect="plain">
              {{ t('files.syncRemoteDeleted') }}
            </el-tag>
            <el-tag v-if="row.needsRestart" type="warning" size="small" effect="plain">
              {{ t('files.syncNeedsRestart') }}
            </el-tag>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <template #footer>
      <el-button @click="close">{{ t('common.cancel') }}</el-button>
      <el-button
        type="primary"
        :disabled="disabled || inSync || !entries.length"
        :loading="applying"
        @click="apply('push')"
      >
        {{ t('files.syncPush') }}
      </el-button>
      <el-button
        type="warning"
        :disabled="disabled || inSync || !entries.length"
        :loading="applying"
        @click="apply('pull')"
      >
        {{ t('files.syncPull') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.sync-scope {
  margin: 0 0 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.sync-path {
  margin-right: 6px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.sync-time {
  margin-right: 6px;
  font-size: 11px;
  color: var(--el-text-color-secondary);
}
</style>
