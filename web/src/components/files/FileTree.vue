<!-- 懒加载文件树：el-tree lazy 模式，行悬停「⋯」菜单，任意目录可上传。 -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Lock, MoreFilled } from '@element-plus/icons-vue'
import { ApiError } from '../../api/client'
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

// 结构性目录是运行时按固定名称查找的目录，不允许改名或删除（与后端
// webfiles.isStructuralDir 一致）：home 下的一级目录，以及 subagent 内的
// mcp 与 skills 目录（subagents/{name}/mcp、subagents/{name}/skills）。
function isStructuralDir(data: TreeItem): boolean {
  if (data.leaf) return false
  const segs = data.path.split('/')
  if (segs.length === 1) return true
  return segs.length === 3 && segs[0] === 'subagents' && (segs[2] === 'mcp' || segs[2] === 'skills')
}

// 结构性条目：一级目录与 GROOT.md，禁止重命名与删除（与后端规则一致）
function isProtected(data: TreeItem): boolean {
  return (
    isStructuralDir(data) || (!data.path.includes('/') && data.path.toLowerCase() === 'groot.md')
  )
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
    case 'uploadFile': {
      uploadDir.value = data.path
      uploadInput.value?.click()
      break
    }
    case 'uploadDir': {
      uploadDir.value = data.path
      dirInput.value?.click()
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
  if (uploading.value) return // 目录上传进行中，不混入单文件上传
  await run(() => filesApi.upload(uploadDir.value, file))
}

const dirInput = ref<HTMLInputElement | null>(null)

// 目录上传单批上限（设计文档 §1.4.5）。服务端逐文件受理、看不到整批，
// 这是前端兜底，防止误选巨大目录把浏览器与服务端拖住。
const MAX_DIR_FILES = 500

// 目录上传进度，非空时树区域显示 v-loading 遮罩
const uploading = ref(false)
const progress = ref({ done: 0, total: 0 })
const progressText = computed(() =>
  t('files.uploadDirProgress', { done: progress.value.done, total: progress.value.total }),
)

// 浏览器不支持目录选择（主要是移动端）时隐藏「上传目录」菜单项
const dirUploadSupported = 'webkitdirectory' in HTMLInputElement.prototype

// 目录上传编排：占位建目标根目录（探测冲突）→ 逐文件上传。
// 首个失败立即中断：已传部分保留（不自动清理，删一半比留一半更难解释），
// 401 时若不中断，剩余每个文件都会再触发一次未登录跳转与错误弹窗。
// 不套 run()——它每个文件刷一次树、弹一条错，批量场景由这里统一收尾。
async function onDirChange(e: Event) {
  const input = e.target as HTMLInputElement
  const picked = Array.from(input.files ?? [])
  input.value = '' // 允许连续选择同一目录
  if (!picked.length) return
  // 重入守卫：v-loading 遮罩是树容器内随内容滚动的绝对定位子元素，
  // 树高于视口时向下滚动会让遮罩滚出视口、下方行菜单裸露可点，
  // 不拦的话第二个编排会与进行中的批次互踩 uploading/progress/uploadDir。
  if (uploading.value) return

  // 相对路径任一段以 "." 开头的文件（.DS_Store、.git/ 内容）一律跳过，
  // 与后端列表接口过滤隐藏项的行为一致；后端也会拒绝这类段，前端过滤
  // 是为了不让它们计入进度与失败统计。
  const items = picked.filter((f) => {
    const rel = f.webkitRelativePath
    return rel !== '' && !rel.split('/').some((seg) => seg.startsWith('.'))
  })
  if (!items.length) {
    ElMessage.warning(t('files.uploadDirEmpty'))
    return
  }
  if (items.length > MAX_DIR_FILES) {
    ElMessage.error(t('files.uploadDirTooMany', { count: items.length, limit: MAX_DIR_FILES }))
    return
  }

  const dir = uploadDir.value
  // webkitRelativePath 首段即所选目录名，占位建出它以探测同名冲突
  const rootName = items[0].webkitRelativePath.split('/')[0]
  uploading.value = true
  progress.value = { done: 0, total: items.length }
  try {
    try {
      await filesApi.uploadPrepare(dir, rootName)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        ElMessage.error(t('files.uploadDirExists', { name: rootName }))
      } else {
        ElMessage.error(err instanceof ApiError ? err.message : t('files.opFailed'))
      }
      return
    }
    for (const f of items) {
      try {
        await filesApi.upload(dir, f, f.webkitRelativePath)
      } catch {
        ElMessage.error(
          t('files.uploadDirFailed', {
            path: f.webkitRelativePath,
            done: progress.value.done,
            total: items.length,
          }),
        )
        return
      }
      progress.value.done++
    }
    ElMessage.success(t('files.uploadDirDone', { total: items.length }))
  } finally {
    uploading.value = false
    files.refresh() // 无论成败都刷新一次，让已传部分立即可见
  }
}
</script>

<template>
  <div class="file-tree" v-loading="uploading" :element-loading-text="progressText">
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
                <el-dropdown-item v-if="!data.leaf" command="uploadFile">
                  {{ t('files.uploadFile') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="!data.leaf && dirUploadSupported" command="uploadDir">
                  {{ t('files.uploadDir') }}
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
    <input ref="dirInput" type="file" webkitdirectory multiple hidden @change="onDirChange" />
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
