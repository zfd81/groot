// 文件/目录图标选取规则，FileTree 与 SyncDialog 共用，保证两处视觉语言一致。
import { Folder, Document, Picture, Memo, Tickets } from '@element-plus/icons-vue'
import type { Component } from 'vue'

const IMG_EXTS = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico']

// 按目录性与文件名选图标。isDir 由调用方判定：同步树中 skills/{name}
// 是可勾选终端行但仍是目录，不能用「有无子节点」推断目录性。
export function iconForName(name: string, isDir: boolean): Component {
  if (isDir) return Folder
  const ext = name.includes('.') ? name.split('.').pop()!.toLowerCase() : ''
  if (IMG_EXTS.includes(ext)) return Picture
  if (ext === 'md') return Memo
  if (ext === 'json' || ext === 'yaml' || ext === 'yml') return Tickets
  return Document
}
