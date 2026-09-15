<!-- 懒加载文件树：el-tree lazy 模式，行悬停「⋯」菜单，任意目录可上传。 -->
<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Lock, MoreFilled } from '@element-plus/icons-vue'
import { filesApi } from '../../api/files'
import { iconForName } from './fileIcon'
import { useFilesStore } from '../../stores/files'

const { t } = useI18n()
const files = useFilesStore()

// 同步功能未启用时（FilePanel 收到 sync_disabled 后下传）隐藏「同步此项」菜单项
const props = defineProps<{
  syncDisabled?: boolean
}>()

// 行菜单「同步此项」→ 由 FilePanel 打开单资源同步对话框
const emit = defineEmits<{
  sync: [path: string]
}>()

// 树节点数据（el-tree data 项）
interface TreeItem {
  name: string
  path: string
  leaf: boolean
  readonly: boolean
}

const treeProps = { label: 'name', isLeaf: 'leaf' }

// 文件图标按扩展名区分：图片 / Markdown / 配置数据 / 其他
function iconFor(data: TreeItem) {
  return iconForName(data.name, !data.leaf)
}

// el-tree lazy load：level 0 加载 home 根，其余按节点路径加载
async function loadNode(node: any, resolve: (data: TreeItem[]) => void) {
  const dir = node.level === 0 ? '' : (node.data as TreeItem).path
  try {
    const resp = await filesApi.list(dir)
    if (node.level === 0) files.homePath = resp.home
    resolve(
      resp.entries.map((e) => ({
        name: e.name,
        path: dir ? `${dir}/${e.name}` : e.name,
        leaf: e.type === 'file',
        readonly: e.readonly,
      }))
    )
  } catch {
    ElMessage.error(t('files.loadFailed'))
    resolve([])
    // 允许用户再次展开时重试，而不是留下永久的空目录。
    // resolve([]) 会把节点置为 loaded 且 isLeaf=true（箭头消失），
    // 必须回退 loaded 后重算 leaf 态才能恢复展开箭头。
    node.loaded = false
    node.expanded = false
    node.updateLeafState()
  }
}

function onNodeClick(data: TreeItem) {
  if (data.leaf) files.previewPath = data.path
}

function onExpand(data: TreeItem) {
  if (!files.expandedKeys.includes(data.path)) files.expandedKeys.push(data.path)
}
// 必须连同子树的展开记录一起清掉：default-expanded-keys 是响应式绑定，
// 残留的子孙 key 会被 el-tree 以 autoExpandParent 重新展开，
// 把刚收起的父节点又拉开（表现为只能从最深一层逐层收起）。
function onCollapse(data: TreeItem) {
  dropExpandedUnder(data.path)
}

// home 下的一级目录是运行时结构性目录，不允许删除（与后端规则一致）
function isTopDir(data: TreeItem): boolean {
  return !data.leaf && !data.path.includes('/')
}

// 结构性条目：一级目录与 GROOT.md，禁止重命名与删除（与后端规则一致）
function isProtected(data: TreeItem): boolean {
  return isTopDir(data) || (!data.path.includes('/') && data.path.toLowerCase() === 'groot.md')
}

function parentDir(p: string): string {
  const i = p.lastIndexOf('/')
  return i < 0 ? '' : p.slice(0, i)
}

// —— 配置同步入口 ——

// 与后端 sync.SyncableResourceRoots 对应；不在白名单内的路径不显示同步入口。
const SYNCABLE_ROOTS = ['config.yaml', 'skills', 'subagents', 'mcp', 'GROOT.md']

// 与后端 sync.isDirectSkillFile（internal/sync/resource.go）一致：
// skill 目录内的单个文件不允许单独同步，必须操作整个 skill 目录。
//   skills/{skill}/{file}                  → 深度 >= 3 且首段为 skills
//   subagents/{sa}/skills/{skill}/{file}   → 深度 >= 5 且第 3 段为 skills
function isDirectSkillFile(path: string): boolean {
  const parts = path.split('/')
  if (parts.length >= 3 && parts[0] === 'skills') return true
  if (parts.length >= 5 && parts[0] === 'subagents' && parts[2] === 'skills') return true
  return false
}

// 与后端 sync.ValidateSyncPath 保持一致，避免用户点了同步却收到 400
function isSyncable(path: string): boolean {
  const inWhitelist = SYNCABLE_ROOTS.some((root) => path === root || path.startsWith(root + '/'))
  return inWhitelist && !isDirectSkillFile(path)
}

// —— 行操作 ——

// 文件/目录名：以字母数字开头，不含路径分隔符，且不以点/空格结尾（与后端 validName 一致）
const NAME_RE = /^[\p{L}\p{N}](?:[\p{L}\p{N} ()._-]{0,62}[\p{L}\p{N})_-])?$/u

async function promptName(
  title: string,
  message = t('files.namePrompt'),
  defaultValue = ''
): Promise<string | null> {
  try {
    const { value } = await ElMessageBox.prompt(message, title, {
      inputValue: defaultValue,
      inputPattern: NAME_RE,
      inputErrorMessage: t('files.fileNameInvalid'),
    })
    return value?.trim() || null
  } catch {
    return null // 用户取消
  }
}

async function run(op: () => Promise<unknown>): Promise<boolean> {
  try {
    await op()
    files.refresh()
    return true
  } catch (e: any) {
    ElMessage.error(e?.message || t('files.opFailed'))
    return false
  }
}

// 节点路径变更/删除后，清掉它及其子树的展开记录
function dropExpandedUnder(path: string) {
  files.expandedKeys = files.expandedKeys.filter(
    (k) => k !== path && !k.startsWith(path + '/')
  )
}

async function onCommand(cmd: string, data: TreeItem) {
  switch (cmd) {
    case 'rename': {
      const name = await promptName(t('files.rename'), t('files.renamePrompt'), data.name)
      if (!name) return
      const to = parentDir(data.path) ? `${parentDir(data.path)}/${name}` : name
      if (await run(() => filesApi.rename(data.path, to))) dropExpandedUnder(data.path)
      break
    }
    case 'delete': {
      try {
        await ElMessageBox.confirm(t('files.deleteConfirm', { name: data.name }), t('files.del'), {
          type: 'warning',
        })
      } catch {
        return
      }
      if (await run(() => filesApi.remove(data.path))) dropExpandedUnder(data.path)
      break
    }
    case 'download': {
      const a = document.createElement('a')
      a.href = filesApi.downloadUrl(data.path)
      a.download = data.name
      a.click()
      break
    }
    case 'upload': {
      uploadDir.value = data.path
      uploadInput.value?.click()
      break
    }
    case 'sync': {
      emit('sync', data.path)
      break
    }
  }
}

// 上传：隐藏 input，change 后提交
const uploadInput = ref<HTMLInputElement | null>(null)
const uploadDir = ref('')
async function onUploadChange(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 允许连续上传同一个文件
  if (!file) return
  await run(() => filesApi.upload(uploadDir.value, file))
}
</script>

<template>
  <div class="file-tree">
    <el-tree
      :key="files.treeVersion"
      lazy
      :load="loadNode"
      :props="treeProps"
      node-key="path"
      :default-expanded-keys="files.expandedKeys"
      :expand-on-click-node="true"
      @node-click="onNodeClick"
      @node-expand="onExpand"
      @node-collapse="onCollapse"
    >
      <template #default="{ data }">
        <div class="tree-row">
          <el-icon :size="15" class="tree-icon">
            <component :is="iconFor(data)" />
          </el-icon>
          <span class="tree-name">{{ data.name }}</span>
          <el-icon v-if="data.readonly" :size="12" class="tree-lock" :title="t('files.readonlyTag')">
            <Lock />
          </el-icon>
          <el-dropdown
            trigger="click"
            class="tree-more"
            @command="(cmd: string) => onCommand(cmd, data)"
          >
            <el-icon :size="14" @click.stop><MoreFilled /></el-icon>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="rename" :disabled="data.readonly || isProtected(data)">
                  {{ t('files.rename') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="data.leaf" command="download">
                  {{ t('files.download') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="!data.leaf" command="upload">
                  {{ t('files.upload') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="!props.syncDisabled && isSyncable(data.path)" command="sync">
                  {{ t('files.syncScopeOne') }}
                </el-dropdown-item>
                <el-dropdown-item command="delete" :disabled="data.readonly || isProtected(data)" divided>
                  {{ t('files.del') }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </template>
    </el-tree>
    <input ref="uploadInput" type="file" hidden @change="onUploadChange" />
  </div>
</template>

<style scoped>
.file-tree {
  flex: 1;
  overflow: auto;
  padding: 4px 6px;
}
.tree-row {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding-right: 4px;
}
.tree-icon {
  flex-shrink: 0;
  opacity: 0.75;
}
.tree-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tree-lock {
  flex-shrink: 0;
  opacity: 0.5;
}
/* ⋯ 按钮：默认隐藏，悬停行时浮现 */
.tree-more {
  flex-shrink: 0;
  opacity: 0;
  transition: opacity 0.15s;
}
.tree-row:hover .tree-more {
  opacity: 0.7;
}
</style>
