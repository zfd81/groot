// 把 /web/sync/diff 返回的扁平差异条目聚合成树形表格数据。
// 树的「终端行」是可勾选的最小单位，其路径集合与后端 ValidateSyncPath
// 的合法取值一一对应，因此勾选结果可直接作为 paths 提交（设计文档 §1.8）。
import type { SyncDiffEntry, SyncEntryStatus } from '../../api/types'

// aggregate: 纯目录行，复选框三态，由名下终端推导
// terminal:  可勾选行（文件，或作为原子单元的 skill 目录）
// detail:    skill 目录内部的变更明细，只读展示，无复选框
export type SyncNodeRole = 'aggregate' | 'terminal' | 'detail'

export interface SyncTreeNode {
  // el-table 专用 row-key（`${role}:${path}`）。path 本身不保证全树唯一：
  // 数据库残留场景下（某路径曾是文件、后换成目录），同一 path 可能同时
  // 以 terminal 与 detail（或 aggregate）两种角色入树，撞 key 会污染展开/勾选状态。
  key: string
  path: string // 完整相对路径（提交 paths / terminals 查表用）
  name: string // 当前层级段名
  isDir: boolean
  role: SyncNodeRole
  status?: SyncEntryStatus // 文件行（terminal 文件 / detail）才有
  fileCount: number // 名下（含自身）变更文件数；目录行在状态列展示它
  needsRestart: boolean
  remoteDeleted: boolean
  remoteUpdatedAt?: number
  // 名下全部终端行路径。terminal 行为 [自身]；detail 行为 []。
  // 聚合行的三态与批量勾选都基于它，预计算避免每次渲染递归。
  terminalPaths: string[]
  children?: SyncTreeNode[]
}

export interface SyncTree {
  nodes: SyncTreeNode[]
  // 终端路径 → 其名下差异条目（skill 目录终端对应多条，文件终端对应一条）。
  // 删除确认的计数按勾选的终端集合从这里汇总。
  terminals: Map<string, SyncDiffEntry[]>
}

// 镜像后端 internal/sync/resource.go 的 parentSkillDir：
// skill 目录是原子单元，其内部文件归并到 skill 目录路径。
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
  // path → 已创建的聚合节点，保证同一目录只建一次
  const dirNodes = new Map<string, SyncTreeNode>()

  const childrenOf = (parent: SyncTreeNode | null): SyncTreeNode[] => {
    if (parent === null) return roots
    if (!parent.children) parent.children = []
    return parent.children
  }

  // 自上而下为 unit 建出祖先聚合链，返回直接父节点（根层为 null）
  const ensureAncestors = (unit: string): SyncTreeNode | null => {
    const segs = unit.split('/')
    let parent: SyncTreeNode | null = null
    for (let i = 0; i < segs.length - 1; i++) {
      const dirPath = segs.slice(0, i + 1).join('/')
      let node = dirNodes.get(dirPath)
      if (!node) {
        node = {
          key: `aggregate:${dirPath}`,
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

  // terminals 沿用 entries 的路径升序（后端已排），树内兄弟自然有序
  for (const [unit, unitEntries] of terminals) {
    const parent = ensureAncestors(unit)
    const name = unit.split('/').pop()!
    const isSkillDir = unitEntries.length > 1 || unitEntries[0].path !== unit
    const node: SyncTreeNode = {
      key: `terminal:${unit}`,
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
            // skill 内部明细：路径显示相对于 skill 目录的剩余段，只读无复选框。
            // 数据库残留时 e.path 可能等于 unit 本身（该路径曾是文件），此时取末段兜底
            key: `detail:${e.path}`,
            path: e.path,
            name: e.path === unit ? e.path.split('/').pop()! : e.path.slice(unit.length + 1),
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
