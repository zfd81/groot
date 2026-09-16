# 同步对话框树形勾选表格 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 SyncDialog 的扁平差异清单改造为树形勾选表格:三态复选框、类型图标、默认折叠、弹窗尺寸对齐设置界面、按钮改名为推送/拉取。

**Architecture:** 纯前端改造,后端零改动。前端把后端返回的扁平 entries 聚合成嵌套树(聚合逻辑独立成 `syncTree.ts` 模块),树的可勾选终端行与后端 `ValidateSyncPath` 的合法取值一一对应,勾选结果直接作为 `paths` 提交。复选框列自绘(`el-checkbox` + `indeterminate`),不用 `el-table` 内置选择列(它与树层级无关且无三态)。文件图标从 FileTree 抽成共享模块,两处复用同一套规则。

**Tech Stack:** Vue 3 `<script setup>` + TypeScript + Element Plus 2.14(el-table 树形模式)。项目无前端测试框架,验证门槛为 `npx vue-tsc -b` 与 `npm run build`(设计文档 1.10 节明确不引入新测试工具)。

**Spec:** `docs/superpowers/specs/2026-09-14-web-sync-design.md` §1.8

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `web/src/components/files/fileIcon.ts` | 新建:按名称与目录性选图标,FileTree 与 SyncDialog 共用 |
| `web/src/components/files/FileTree.vue` | 修改:`iconFor` 改用共享模块 |
| `web/src/components/files/syncTree.ts` | 新建:扁平 entries → 树结构聚合,原子单元判定 |
| `web/src/components/files/SyncDialog.vue` | 修改:树形表格、三态勾选、提交逻辑、弹窗尺寸 |
| `web/src/i18n/messages/{zh-cn,en}.ts` | 修改:按钮改名,新增文件数 key |

**任务顺序:** Task 1 抽图标(独立、最小),Task 2 树聚合模块(纯 TS、无 UI),Task 3 重写对话框(依赖 1、2),Task 4 尺寸与文案收尾。每个 Task 结束时 `vue-tsc` 与 `build` 通过。

## 关键背景(实现者必读)

1. **后端契约不变。** `POST /web/sync/diff` 返回 `entries: SyncDiffEntry[]`(见 `web/src/api/types.ts:308`),每项是文件级路径,已按路径升序(后端 `webview.go` 排过)。push/pull 接受 `paths`(资源级路径数组),空数组表示全量。
2. **原子单元规则**(镜像后端 `internal/sync/resource.go` 的 `isDirectSkillFile`/`parentSkillDir`):`skills/{name}/...` 下的文件归并到 `skills/{name}`;`subagents/{sa}/skills/{skill}/...` 下的文件归并到 `subagents/{sa}/skills/{skill}`;其余文件自身即原子单元。原子单元路径都是 `ValidateSyncPath` 认可的合法 `paths` 取值。
3. **Element Plus 树形表格的缩进位置是定死的:第一个普通列。** 用户截图里「选择列不缩进、箭头在第二列」是 `type="selection"` 的特权,自绘列拿不到。**决策:复选框列放第一列,接受缩进和展开箭头渲染在该列内**(经典树形复选框布局,勾选框随层级缩进)。列宽给足(设 `min-width: 140`),最深层级是 `subagents/{sa}/skills/{skill}` 的明细行(4 层缩进 ≈ 64px + 箭头 + 复选框)。**不要**尝试用 `type="selection"`、`type="index"` 或 CSS 移动缩进,都是死路。
4. **弹窗尺寸的既有模式**在 `SettingsModal.vue:797` 起的**非 scoped** `<style>` 块:el-dialog 经 teleport 渲染在组件作用域外,scoped 样式够不到它,必须用非 scoped 块 + 专属 class 限定。照抄这个模式。

---

## Task 1: 抽共享文件图标模块

**Files:**
- Create: `web/src/components/files/fileIcon.ts`
- Modify: `web/src/components/files/FileTree.vue:5,33-41`

- [ ] **Step 1: 新建共享模块**

新建 `web/src/components/files/fileIcon.ts`:

```ts
// 文件/目录图标选取规则,FileTree 与 SyncDialog 共用,保证两处视觉语言一致。
import { Folder, Document, Picture, Memo, Tickets } from '@element-plus/icons-vue'
import type { Component } from 'vue'

const IMG_EXTS = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico']

// 按目录性与文件名选图标。isDir 由调用方判定:同步树中 skills/{name}
// 是可勾选终端行但仍是目录,不能用「有无子节点」推断目录性。
export function iconForName(name: string, isDir: boolean): Component {
  if (isDir) return Folder
  const ext = name.includes('.') ? name.split('.').pop()!.toLowerCase() : ''
  if (IMG_EXTS.includes(ext)) return Picture
  if (ext === 'md') return Memo
  if (ext === 'json' || ext === 'yaml' || ext === 'yml') return Tickets
  return Document
}
```

- [ ] **Step 2: FileTree 改用共享模块**

`web/src/components/files/FileTree.vue`:
- 第 5 行 import 中删去 `Folder, Document, Picture, Memo, Tickets`(保留 `Lock, MoreFilled` 等仍在用的),加一行 `import { iconForName } from './fileIcon'`
- 第 33-41 行的 `IMG_EXTS` 常量与 `iconFor` 函数体替换为:

```ts
function iconFor(data: TreeItem) {
  return iconForName(data.name, !data.leaf)
}
```

模板中 `iconFor(data)` 的调用点(约 228 行)不变。

- [ ] **Step 3: 验证**

Run: `cd web && npx vue-tsc -b && npm run build`
Expected: 零错误,构建通过。肉眼确认 `grep -n "IMG_EXTS" src/components/files/FileTree.vue` 无残留。

- [ ] **Step 4: 提交**

```bash
git add web/src/components/files/fileIcon.ts web/src/components/files/FileTree.vue
git commit -m "refactor(web): 文件图标规则抽成共享模块"
```

---

## Task 2: 差异树聚合模块

**Files:**
- Create: `web/src/components/files/syncTree.ts`

- [ ] **Step 1: 新建聚合模块**

新建 `web/src/components/files/syncTree.ts`,完整内容:

```ts
// 把 /web/sync/diff 返回的扁平差异条目聚合成树形表格数据。
// 树的「终端行」是可勾选的最小单位,其路径集合与后端 ValidateSyncPath
// 的合法取值一一对应,因此勾选结果可直接作为 paths 提交(设计文档 §1.8)。
import type { SyncDiffEntry, SyncEntryStatus } from '../../api/types'

// aggregate: 纯目录行,复选框三态,由名下终端推导
// terminal:  可勾选行(文件,或作为原子单元的 skill 目录)
// detail:    skill 目录内部的变更明细,只读展示,无复选框
export type SyncNodeRole = 'aggregate' | 'terminal' | 'detail'

export interface SyncTreeNode {
  path: string // 完整相对路径,兼�� el-table 的 row-key(全树唯一)
  name: string // 当前层级段名
  isDir: boolean
  role: SyncNodeRole
  status?: SyncEntryStatus // 文件行(terminal 文件 / detail)才有
  fileCount: number // 名下(含自身)变更文件数;目录行在状态列展示它
  needsRestart: boolean
  remoteDeleted: boolean
  remoteUpdatedAt?: number
  // 名下全部终端行路径。terminal 行为 [自身];detail 行为 []。
  // 聚合行的三态与批量勾选都基于它,预计算避免每次渲染递归。
  terminalPaths: string[]
  children?: SyncTreeNode[]
}

export interface SyncTree {
  nodes: SyncTreeNode[]
  // 终端路径 → 其名下差异条目(skill 目录终端对应多条,文件终端对应一条)。
  // 删除确认的计数按勾选的终端集合从这里汇总。
  terminals: Map<string, SyncDiffEntry[]>
}

// 镜像后端 internal/sync/resource.go 的 parentSkillDir:
// skill 目录是原子单元,其内部文件归并到 skill 目录路径。
export function atomicUnitOf(path: string): string {
  const parts = path.split('/')
  if (parts.length >= 3 && parts[0] === 'skills') return parts.slice(0, 2).join('/')
  if (parts.length >= 5 && parts[0] === 'subagents' && parts[2] === 'skills') {
    return parts.slice(0, 4).join('/')
  }
  return path
}

export function buildSyncTree(entries: SyncDiffEntry[]): SyncTree {
  const terminals = new Map<string, SyncDiffEntry[]>()
  for (const e of entries) {
    const unit = atomicUnitOf(e.path)
    const list = terminals.get(unit)
    if (list) list.push(e)
    else terminals.set(unit, [e])
  }

  const roots: SyncTreeNode[] = []
  // path → 已创建的聚合节点,保证同一目录只建一次
  const dirNodes = new Map<string, SyncTreeNode>()

  const childrenOf = (parent: SyncTreeNode | null): SyncTreeNode[] => {
    if (parent === null) return roots
    if (!parent.children) parent.children = []
    return parent.children
  }

  // 自上而下为 unit 建出祖先聚合链,返回直接父节点(根层为 null)
  const ensureAncestors = (unit: string): SyncTreeNode | null => {
    const segs = unit.split('/')
    let parent: SyncTreeNode | null = null
    for (let i = 0; i < segs.length - 1; i++) {
      const dirPath = segs.slice(0, i + 1).join('/')
      let node = dirNodes.get(dirPath)
      if (!node) {
        node = {
          path: dirPath,
          name: segs[i],
          isDir: true,
          role: 'aggregate',
          fileCount: 0,
          needsRestart: false,
          remoteDeleted: false,
          terminalPaths: [],
        }
        dirNodes.set(dirPath, node)
        childrenOf(parent).push(node)
      }
      parent = node
    }
    return parent
  }

  // terminals 沿用 entries 的路径升序(后端已排),树内兄弟自然有序
  for (const [unit, unitEntries] of terminals) {
    const parent = ensureAncestors(unit)
    const name = unit.split('/').pop()!
    const isSkillDir = unitEntries.length > 1 || unitEntries[0].path !== unit
    const node: SyncTreeNode = {
      path: unit,
      name,
      isDir: isSkillDir,
      role: 'terminal',
      fileCount: unitEntries.length,
      needsRestart: unitEntries.some((e) => e.needsRestart),
      remoteDeleted: !isSkillDir && unitEntries[0].remoteDeleted,
      remoteUpdatedAt: isSkillDir ? undefined : unitEntries[0].remoteUpdatedAt,
      terminalPaths: [unit],
      children: isSkillDir
        ? unitEntries.map((e) => ({
            // skill 内部明细:路径显示相对于 skill 目录的剩余段,只读无复选框
            path: e.path,
            name: e.path.slice(unit.length + 1),
            isDir: false,
            role: 'detail' as const,
            status: e.status,
            fileCount: 1,
            needsRestart: e.needsRestart,
            remoteDeleted: e.remoteDeleted,
            remoteUpdatedAt: e.remoteUpdatedAt,
            terminalPaths: [],
          }))
        : undefined,
    }
    if (!isSkillDir) node.status = unitEntries[0].status
    childrenOf(parent).push(node)
  }

  // 自底向上回填聚合行的 fileCount / needsRestart / terminalPaths
  const fill = (node: SyncTreeNode): void => {
    if (node.role !== 'aggregate') return
    for (const c of node.children ?? []) {
      fill(c)
      node.fileCount += c.fileCount
      node.needsRestart = node.needsRestart || c.needsRestart
      node.terminalPaths.push(...c.terminalPaths)
    }
  }
  for (const r of roots) fill(r)

  return { nodes: roots, terminals }
}
```

**实现注意:**
- `isSkillDir` 的判定是「条目数大于 1,或唯一条目的路径不等于单元路径」。后者覆盖 skill 目录下只有一个文件变更的情况(如 `skills/weather` 只变了 `SKILL.md`):unit 是 `skills/weather` 而 entry.path 是 `skills/weather/SKILL.md`,两者不等 → 是 skill 目录。
- detail 行的 `name` 取相对 skill 目录的剩余段(`scripts/server.cjs`),不再拆层——skill 内部结构不需要树形,一层明细足够。
- 不要给这个模块引入 Vue 依赖,它是纯函数,保持可独立类型检查。

- [ ] **Step 2: 类型检查**

Run: `cd web && npx vue-tsc -b`
Expected: 零错误(模块此时尚无使用方,只验证自身类型)。

- [ ] **Step 3: 提交**

```bash
git add web/src/components/files/syncTree.ts
git commit -m "feat(web): 同步差异树聚合模块"
```

---

## Task 3: SyncDialog 重写为树形表格

**Files:**
- Modify: `web/src/components/files/SyncDialog.vue`(整体重写 script 与 template 的表格部分)

现有文件 206 行,保留的部分:props/emits 定义、`loadDiff` 的错误处理(`sync_disabled` → emit disabled)、`apply` 的方向确认与成功后流程、`close`、`watch`。重写的部分:数据从 `entries` 直用改为经 `buildSyncTree` 聚合、勾选状态、表格模板、按钮禁用条件。

- [ ] **Step 1: script 部分改造**

`<script setup lang="ts">` 整体替换为:

```ts
import { ref, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { syncApi } from '../../api/sync'
import { ApiError } from '../../api/client'
import type { SyncDiffEntry } from '../../api/types'
import { buildSyncTree, type SyncTree, type SyncTreeNode } from './syncTree'
import { iconForName } from './fileIcon'

const props = defineProps<{
  modelValue: boolean
  // 同步范围:undefined 表示全部配置,否则为单个资源相对路径
  scope?: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  // pull 成功后通知父组件刷新文件树并关闭已打开的编辑器
  pulled: []
  pushed: []
  // 后端返回 sync_disabled(非 MySQL/PostgreSQL 模式)时通知父组件隐藏同步入口
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

// 提交范围:全选时退化为原有的整体语义(scope 或全量),部分勾选时提交勾中的终端路径。
// 终端路径就是 ValidateSyncPath 的合法取值,无需第二套参数(设计文档 §1.6)。
const submitPaths = computed(() => {
  if (allChecked.value) return props.scope ? [props.scope] : []
  return Array.from(checked.value).sort()
})

// 各方向会导致删除的条目数,只统计勾中的终端:推送删数据库的 D,拉取删本地的 A
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

const nodeState = (node: SyncTreeNode) => stateOf(node.terminalPaths)
const headerState = computed<CheckState>(() =>
  stateOf(Array.from(tree.value.terminals.keys())),
)

// 点聚合行:非全选 → 全勾;全选 → 全去。终端行只有二态。
// 注意对 Set 重新赋值而非原地改,保证 computed 依赖可靠触发。
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

// 版本控制惯例配色:A 绿、M 橙、D 红
const statusType = (s: SyncDiffEntry['status']) =>
  s === 'A' ? 'success' : s === 'M' ? 'warning' : 'danger'

// 远端更新时间(毫秒时间戳);本地新建(远端无记录)时后端省略该字段,不显示
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
    // ApiError.status 是 HTTP 数字码,业务 status 值在 code 字段(见 api/client.ts)
    if (e instanceof ApiError && e.code === 'sync_disabled') {
      disabled.value = true
      tree.value = { nodes: [], terminals: new Map() }
      checked.value = new Set()
      emit('disabled') // 提示 alert 保留在对话框内,由父组件隐藏后续入口
    } else {
      ElMessage.error(e instanceof ApiError ? e.message : t('files.syncFailed'))
      close()
    }
  } finally {
    loading.value = false
  }
}

async function apply(direction: 'push' | 'pull') {
  // 仅当操作会导致删除时二次确认(推送含 D / 拉取含 A),数量按勾选集合统计;
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
      // 重启提示只对拉取有意义:push 只写数据库,不影响本地运行时
      if (needsRestart.value) {
        ElMessage.warning(t('files.syncRestartHint'))
      }
    }
    close()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : t('files.syncFailed'))
    // 失败后重新比较,让用户看到当前真实状态
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
```

**部分勾选的语义提醒**(写在 `submitPaths` 注释里已体现,实现时不要"优化"掉):部分勾选后提交的是勾中终端的显式列表,push/pull 在后端会对这些路径重算 diff 再执行,未勾中的资源完全不参与——包括镜像删除。这正是设计意图。

- [ ] **Step 2: template 表格部分改造**

`<el-table>` 块整体替换为(外层 `v-loading`、alert 两行保持,只把 `entries.length` 条件换成 `tree.nodes.length`):

```vue
      <el-table
        v-else-if="tree.nodes.length"
        :data="tree.nodes"
        row-key="path"
        :tree-props="{ children: 'children' }"
        max-height="380"
        size="small"
        class="sync-table"
      >
        <!-- 复选框列:树形缩进与展开箭头由 el-table 渲染在第一个普通列,
             即本列——这是 Element Plus 的固定行为,复选框随层级缩进。 -->
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
              :model-value="nodeState(row) === 'all'"
              :indeterminate="nodeState(row) === 'partial'"
              @change="toggleNode(row)"
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
            <span v-else class="sync-count">{{ t('files.syncFileCount', { count: row.fileCount }) }}</span>
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
```

按钮部分:两个操作按钮的 `:disabled` 条件改为 `disabled || inSync || !checked.size`(勾选为空时禁用,替代原来的 `!entries.length`——树非空但全部勾除时也应禁用)。按钮文案 key 不变(`files.syncPush`/`files.syncPull`),文案本身在 Task 4 改。

**展示规则说明**(与设计文档 §1.8 对齐):
- 状态列:文件行(terminal 文件/detail)显示状态标签;目录行(aggregate 与 skill 终端目录)显示 `{count} 个文件变更`。判定用 `row.status` 有无,不用 role——terminal 文件行有 status,目录行没有。
- 重启标注:只标在非聚合行(terminal/detail)上,聚合行不标,避免一列全是重复标签。
- 远端时间/已删除标注:只有携带这些字段的行(terminal 文件行、detail 行)会渲染,skill 目录终端行两个字段都是 undefined,自然不显示。
- 默认折叠:el-table 树形默认不展开(`default-expand-all` 缺省为 false),**不需要**任何代码,不要加 `default-expand-all`。

- [ ] **Step 3: 补 i18n key(本任务模板已引用)**

模板用了 `t('files.syncFileCount', { count })`,key 现在就要加上,否则界面显示裸 key。

`web/src/i18n/messages/zh-cn.ts` 的 files 段 sync 系列 key 中追加:

```ts
    syncFileCount: '{count} 个文件变更',
```

`web/src/i18n/messages/en.ts` 同位置追加:

```ts
    syncFileCount: '{count} file(s) changed',
```

- [ ] **Step 4: style 部分追加**

`<style scoped>` 内追加(保留现有 `.sync-scope`/`.sync-path`/`.sync-time`):

```css
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
```

- [ ] **Step 5: 验证**

Run: `cd web && npx vue-tsc -b && npm run build`
Expected: 零错误。

手工验证清单(启动 `npm run dev` + 后端,或留给用户系统测试;至少走查代码确认逻辑):
1. 打开对话框:顶层折叠、全部勾选、表头勾选框为勾中态
2. 展开 skills → 勾除一个 skill:该行未勾,`skills` 行与表头变半选
3. skill 目录行可展开出明细行,明细行无复选框
4. 全部勾除:推送/拉取按钮禁用
5. 部分勾选下推送:请求体 `paths` 为勾中的终端路径列表

- [ ] **Step 6: 提交**

```bash
git add web/src/components/files/SyncDialog.vue web/src/i18n/messages/
git commit -m "feat(web): 同步对话框改为树形勾选表格"
```

---

## Task 4: 弹窗尺寸、按钮文案与 i18n

**Files:**
- Modify: `web/src/components/files/SyncDialog.vue`(el-dialog 属性与非 scoped 样式)
- Modify: `web/src/i18n/messages/zh-cn.ts`(files 段)
- Modify: `web/src/i18n/messages/en.ts`(files 段)

- [ ] **Step 1: 弹窗尺寸与圆角**

`SyncDialog.vue` 的 `<el-dialog>` 改为:

```vue
  <el-dialog
    :model-value="modelValue"
    :title="t('files.syncTitle')"
    width="750px"
    align-center
    class="sync-dialog"
    @update:model-value="emit('update:modelValue', $event)"
  >
```

文件末尾追加**非 scoped** `<style>` 块(与 `<style scoped>` 并列;el-dialog 经 teleport 渲染在组件作用域外,scoped 规则够不到,照抄 `SettingsModal.vue:797` 的既有模式):

```vue
<!-- 弹窗根元素在 scoped 作用域外,用非 scoped 规则控制尺寸与圆角,
     模式与 SettingsModal 的 settings-dialog 一致。 -->
<style>
.sync-dialog {
  /* 高度恒为视口高度减去上下各 50px,随窗口尺寸实时变化 */
  height: calc(100vh - 100px);
  margin-top: 0;
  margin-bottom: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  /* 圆角外框;overflow: hidden 保证内部内容不溢出直角 */
  border-radius: 16px;
}

.sync-dialog .el-dialog__body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
</style>
```

body 变成 flex 容器后,表格要占满剩余高度并在内部滚动。`<div v-loading="loading">` 加 class 与样式(scoped 块):

```css
.sync-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
```

`el-table` 上删掉 Task 3 之前遗留的 `max-height="380"`(若还在),改为 `height="100%"`,并给它的直接父级保证高度传递:模板结构调整为

```vue
    <div v-loading="loading" class="sync-body">
      <p class="sync-scope">...</p>
      <el-alert ... />
      <div class="sync-table-wrap">
        <el-table ... height="100%">...</el-table>
      </div>
    </div>
```

scoped 样式加:

```css
.sync-table-wrap {
  flex: 1;
  min-height: 0;
}
```

- [ ] **Step 2: i18n 改名与新增**

`web/src/i18n/messages/zh-cn.ts` files 段(279-280 行附近):

```ts
    syncPush: '推送',
    syncPull: '拉取',
```

`web/src/i18n/messages/en.ts` 同位置:

```ts
    syncPush: 'Push',
    syncPull: 'Pull',
```

(`syncFileCount` 已在 Task 3 加过,本任务只改这两个按钮文案。)

改完 diff 两个文件的 sync key 序列,必须逐行一致:

```bash
diff <(grep -oE "^\s+sync[A-Za-z]*:" web/src/i18n/messages/zh-cn.ts) \
     <(grep -oE "^\s+sync[A-Za-z]*:" web/src/i18n/messages/en.ts)
```

Expected: 无输出。

**注意:** `syncPushDone`('已推送到数据库')/`syncPullDone`/`syncPushDeleteHint`/`syncPullDeleteHint` 等完整句保持不变——按钮要短,提示语要完整,两者职责不同。

- [ ] **Step 3: 验证**

Run: `cd web && npx vue-tsc -b && npm run build`
Expected: 零错误。

Run: `cd .. && go build ./... && go test ./internal/... 2>&1 | tail -3`
Expected: 后端全绿(本计划不动后端,这是回归确认)。

- [ ] **Step 4: 提交**

```bash
git add web/src/components/files/SyncDialog.vue web/src/i18n/messages/
git commit -m "feat(web): 同步弹窗尺寸对齐设置界面,按钮改名推送/拉取"
```

---

## 附:验收检查

```bash
cd web && npx vue-tsc -b && npm run build
cd .. && go build ./... && go test ./internal/...
```

手工验证(MySQL/PostgreSQL 模式启动,面板打开同步对话框):
1. 弹窗 750px 宽、接近满屏高、四角圆角,与设置界面观感一致
2. 打开即整体概览:顶层行 + 全选,直接点「推送」等于整体推送
3. 展开 `skills` → skill 目录行可再展开出内部明细(无复选框)
4. 勾除部分资源后推送,数据库只收到勾中的;未勾中资源在数据库保持原样
5. 目录行图标为文件夹,`.md` 文件为 Memo 图标,与左侧文件树一致
6. 拉取含本地删除时确认框数量与勾选集合一致
