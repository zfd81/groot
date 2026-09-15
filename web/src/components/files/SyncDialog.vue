<!-- 配置同步差异对话框：以树形勾选表格展示本地与数据库的差异，支持推送/拉取。 -->
<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { syncApi } from '../../api/sync'
import { ApiError } from '../../api/client'
import type { SyncDiffEntry } from '../../api/types'
import { buildSyncTree, type SyncTree, type SyncTreeNode } from './syncTree'
import { iconForName } from './fileIcon'

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
  // 后端返回 sync_disabled（非 MySQL/PostgreSQL 模式）时通知父组件隐藏同步入口
  disabled: []
}>()

const { t } = useI18n()

const loading = ref(false)
const applying = ref(false)
const inSync = ref(false)
const needsRestart = ref(false)
const disabled = ref(false)
const tree = ref<SyncTree>({ nodes: [], terminals: new Map() })
// 已勾选的终端行路径集合。默认全选在 loadDiff 里初始化。
const checked = ref(new Set<string>())

const totalTerminals = computed(() => tree.value.terminals.size)
const allChecked = computed(
  () => totalTerminals.value > 0 && checked.value.size === totalTerminals.value,
)

// 提交范围：全选时退化为原有的整体语义（scope 或全量），部分勾选时提交勾中的终端路径。
// 终端路径就是 ValidateSyncPath 的合法取值，无需第二套参数（设计文档 §1.6）。
const submitPaths = computed(() => {
  if (allChecked.value) return props.scope ? [props.scope] : []
  return Array.from(checked.value).sort()
})

// 各方向会导致删除的条目数，只统计勾中的终端：推送删数据库的 D，拉取删本地的 A
const deleteCount = computed(() => {
  let push = 0
  let pull = 0
  for (const p of checked.value) {
    for (const e of tree.value.terminals.get(p) ?? []) {
      if (e.status === 'D') push++
      if (e.status === 'A') pull++
    }
  }
  return { push, pull }
})

// —— 三态勾选 ——

type CheckState = 'all' | 'partial' | 'none'

function stateOf(paths: string[]): CheckState {
  let n = 0
  for (const p of paths) if (checked.value.has(p)) n++
  if (n === 0) return 'none'
  return n === paths.length ? 'all' : 'partial'
}

// el-table 作用域插槽把 row 类型标成宽松的 DefaultRow，模板里收窄回树节点
const asNode = (row: unknown) => row as SyncTreeNode

const nodeState = (node: SyncTreeNode) => stateOf(node.terminalPaths)
const headerState = computed<CheckState>(() => stateOf(Array.from(tree.value.terminals.keys())))

// 点聚合行：非全选 → 全勾；全选 → 全去。终端行只有二态。
// 注意对 Set 重新赋值而非原地改，保证 computed 依赖可靠触发。
function toggle(paths: string[]) {
  const next = new Set(checked.value)
  const anyMissing = paths.some((p) => !next.has(p))
  for (const p of paths) {
    if (anyMissing) next.add(p)
    else next.delete(p)
  }
  checked.value = next
}

const toggleNode = (node: SyncTreeNode) => toggle(node.terminalPaths)
const toggleAll = () => toggle(Array.from(tree.value.terminals.keys()))

// —— 展示辅助 ——

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
    const resp = await syncApi.diff(props.scope ? [props.scope] : [])
    tree.value = buildSyncTree(resp.entries ?? [])
    checked.value = new Set(tree.value.terminals.keys()) // 默认全选
    inSync.value = resp.inSync
    needsRestart.value = resp.needsRestart
  } catch (e) {
    // ApiError.status 是 HTTP 数字码，业务 status 值在 code 字段（见 api/client.ts）
    if (e instanceof ApiError && e.code === 'sync_disabled') {
      disabled.value = true
      tree.value = { nodes: [], terminals: new Map() }
      checked.value = new Set()
      emit('disabled') // 提示 alert 保留在对话框内，由父组件隐藏后续入口
    } else {
      ElMessage.error(e instanceof ApiError ? e.message : t('files.syncFailed'))
      close()
    }
  } finally {
    loading.value = false
  }
}

async function apply(direction: 'push' | 'pull') {
  // 仅当操作会导致删除时二次确认（推送含 D / 拉取含 A），数量按勾选集合统计；
  // 纯 M 或纯单向新增直接执行
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
      await syncApi.push(submitPaths.value)
      ElMessage.success(t('files.syncPushDone'))
      emit('pushed')
    } else {
      await syncApi.pull(submitPaths.value)
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
      <el-table
        v-else-if="tree.nodes.length"
        :data="tree.nodes"
        row-key="key"
        :tree-props="{ children: 'children' }"
        max-height="380"
        size="small"
        class="sync-table"
      >
        <!-- 复选框列：树形缩进与展开箭头由 el-table 渲染在第一个普通列，
             即本列——这是 Element Plus 的固定行为，复选框随层级缩进。 -->
        <el-table-column min-width="150">
          <template #header>
            <el-checkbox
              :model-value="headerState === 'all'"
              :indeterminate="headerState === 'partial'"
              :disabled="!totalTerminals"
              @change="toggleAll"
            />
          </template>
          <template #default="{ row }">
            <el-checkbox
              v-if="row.role !== 'detail'"
              :model-value="nodeState(asNode(row)) === 'all'"
              :indeterminate="nodeState(asNode(row)) === 'partial'"
              @change="toggleNode(asNode(row))"
            />
          </template>
        </el-table-column>
        <el-table-column :label="t('files.syncColPath')">
          <template #default="{ row }">
            <el-icon :size="14" class="sync-icon">
              <component :is="iconForName(row.name, row.isDir)" />
            </el-icon>
            <span class="sync-path">{{ row.name }}</span>
            <span v-if="row.remoteUpdatedAt" class="sync-time">
              {{ formatTime(row.remoteUpdatedAt) }}
            </span>
            <el-tag v-if="row.remoteDeleted" type="danger" size="small" effect="plain">
              {{ t('files.syncRemoteDeleted') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('files.syncColStatus')" width="150">
          <template #default="{ row }">
            <el-tag
              v-if="row.status"
              :type="statusType(row.status)"
              size="small"
              disable-transitions
            >
              {{ statusLabel(row.status) }}
            </el-tag>
            <span v-else class="sync-count">
              {{ t('files.syncFileCount', { count: row.fileCount }) }}
            </span>
            <el-tag
              v-if="row.needsRestart && row.role !== 'aggregate'"
              type="warning"
              size="small"
              effect="plain"
            >
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
        :disabled="disabled || inSync || !checked.size"
        :loading="applying"
        @click="apply('push')"
      >
        {{ t('files.syncPush') }}
      </el-button>
      <el-button
        type="warning"
        :disabled="disabled || inSync || !checked.size"
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
.sync-icon {
  vertical-align: -2px;
  margin-right: 5px;
  color: var(--el-text-color-secondary);
}
.sync-count {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-right: 6px;
}
</style>

