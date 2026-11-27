# 工作空间文件编辑器 Python 与 Markdown 语法高亮实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Web 工作空间的文件编辑弹窗对 `.py` 提供 Python 语法着色，对 `.md` 提供有颜色区分的 Markdown 高亮。

**Architecture:** 沿用现有 CodeMirror 6 编辑器与按扩展名懒加载语言包的方式：`FileEditorModal.languageFor()` 新增 `py` 分支加载 `@codemirror/lang-python`；`md` 分支在原 `markdown()` 之上追加一个只定义颜色的 `HighlightStyle`，补足默认样式对标题/行内代码/链接/引用不着色的问题。

**Tech Stack:** Vue 3 + TypeScript + CodeMirror 6（`@codemirror/lang-python`、`@codemirror/lang-markdown`、`@codemirror/language`、`@lezer/highlight`）+ Vite。

**Spec:** `docs/superpowers/specs/2026-11-27-file-editor-python-md-highlight-design.md`

## Global Constraints

- 前端测试规范：项目禁止引入新的测试框架，本计划不新增 vitest 等工具；验证使用 `npm run build`（含 `vue-tsc` 类型检查）+ 一次性 Node 校验脚本（用完即删，不入库）。
- 语言包必须按扩展名动态 `import()`，不得改为顶层静态导入（避免增大首屏主包）。
- 直接在 `master` 工作区改动，不创建分支或 worktree。
- 不执行 `git commit`；每个任务结束时用临时索引生成悬空快照（命令见各任务 Step），改动保留在工作区。
- 语言选择只覆盖 `py`、`md`，其余扩展名行为不变；不引入完整编辑器主题。
- 构建产物：`make web` → `web/dist`，`make build` → `dist/groot`。

## Review Focus

1. 打开 `.py` 文件时编辑器空白或控制台报错（语言包未安装、动态导入路径错误）：应显示 Python 着色并正常编辑。
2. 打开 `.md` 文件时标题/行内代码仍无颜色（HighlightStyle 未生效、扩展数组被错误嵌套或吞掉）：标题应呈蓝色，行内代码应有独立颜色。
3. 打开 `.json`/`.yaml` 时高亮异常（`languageFor` 改为可返回数组后破坏原有分支）：行为应与改动前一致。
4. 打开 `.txt` 等无语言文件（`languageFor` 返回 `null`）：应为纯文本、编辑器可用、无报错。
5. 保存、未保存关闭确认、Markdown 右侧预览（改动 `initEditor` 的扩展链后）：均应与改动前一致。

---

### Task 1: 接入 Python 语言包

**Files:**
- Modify: `web/package.json`、`web/package-lock.json`（新增依赖）
- Modify: `web/src/components/files/FileEditorModal.vue:70-77`（`languageFor`）

**Interfaces:**
- Consumes: 现有 `languageFor(path: string)`，返回扩展或 `null`。
- Produces: `languageFor` 对 `py` 返回 `@codemirror/lang-python` 的 `python()`；对其余扩展名行为不变。

- [ ] **Step 1: 安装语言包**

Run:
```bash
cd /Users/zhangfengda/workspace/groot/web && npm install @codemirror/lang-python@^6.2.1
```
Expected: `package.json` 的 `dependencies` 出现 `@codemirror/lang-python`，`package-lock.json` 同步更新，命令退出码 0。

- [ ] **Step 2: 在 `languageFor` 中增加 `py` 分支**

在 `web/src/components/files/FileEditorModal.vue` 的 `languageFor` 中，紧跟 `const ext = ...` 之后加入：
```ts
  if (ext === 'py') return (await import('@codemirror/lang-python')).python()
```
其余分支保持不变。

- [ ] **Step 3: 构建验证**

Run:
```bash
cd /Users/zhangfengda/workspace/groot/web && npm run build
```
Expected: `vite build` 与 `vue-tsc -b` 均成功，退出码 0，`web/dist` 更新。

- [ ] **Step 4: 一次性 Node 脚本验证 Python 词法着色**

在 `web/.verify-highlight.mjs` 写入（验证后立即删除，不入库）：
```js
import { EditorState } from '@codemirror/state'
import { defaultHighlightStyle, syntaxTree } from '@codemirror/language'
import { python } from '@codemirror/lang-python'
import { highlightTree } from '@lezer/highlight'

const state = EditorState.create({
  doc: 'def f(x):\n    return "hi"  # note\n',
  extensions: [python()],
})
const spans = []
highlightTree(syntaxTree(state), defaultHighlightStyle, (from, to, cls) => {
  spans.push([state.sliceDoc(from, to), cls])
})
console.log(spans)
const colored = (s) => spans.some(([text, cls]) => cls && text.includes(s))
if (!colored('def') || !colored('"hi"') || !colored('# note')) {
  console.error('FAIL: Python 关键字/字符串/注释未全部着色')
  process.exit(1)
}
console.log('PASS: Python 关键字、字符串、注释均有高亮类')
```
Run:
```bash
cd /Users/zhangfengda/workspace/groot/web && node .verify-highlight.mjs && rm .verify-highlight.mjs
```
Expected: 输出 `PASS: Python 关键字、字符串、注释均有高亮类`，退出码 0。

- [ ] **Step 5: 生成悬空快照（不提交）**

Run:
```bash
cd /Users/zhangfengda/workspace/groot
IDX=$(mktemp -u /tmp/groot-index-XXXXXX)
GIT_INDEX_FILE="$IDX" git read-tree HEAD
GIT_INDEX_FILE="$IDX" git add -A
TREE=$(GIT_INDEX_FILE="$IDX" git write-tree)
SNAP=$(git commit-tree "$TREE" -p HEAD -m "snapshot: task 1 python highlight")
rm -f "$IDX"
echo "$SNAP"
```
Expected: 输出一个 40 位 commit 哈希；`git status` 中的改动仍然保留在工作区。

---

### Task 2: Markdown 高亮样式

**Files:**
- Modify: `web/package.json`、`web/package-lock.json`（显式声明 `@lezer/highlight`）
- Modify: `web/src/components/files/FileEditorModal.vue`（`languageFor` 与模块级缓存）

**Interfaces:**
- Consumes: Task 1 之后的 `languageFor`。
- Produces: `markdownExtensions(): Promise<import('@codemirror/state').Extension[]>`，返回 `[markdown(), syntaxHighlighting(style)]`；`languageFor` 对 `md` 返回该 Promise。

- [ ] **Step 1: 显式声明 `@lezer/highlight` 依赖**

Run:
```bash
cd /Users/zhangfengda/workspace/groot/web && npm install @lezer/highlight@^1.2.3
```
Expected: `package.json` 的 `dependencies` 出现 `@lezer/highlight`，退出码 0。

- [ ] **Step 2: 实现 Markdown 扩展**

在 `web/src/components/files/FileEditorModal.vue` 中，`languageFor` 之前加入：
```ts
// Markdown 高亮样式只定义颜色/字重；默认样式对标题只加粗下划线，这里补足可见颜色。
let mdExtensions: Promise<import('@codemirror/state').Extension[]> | null = null
function markdownExtensions() {
  if (!mdExtensions) {
    mdExtensions = (async () => {
      const [{ markdown }, { HighlightStyle, syntaxHighlighting }, { tags }] = await Promise.all([
        import('@codemirror/lang-markdown'),
        import('@codemirror/language'),
        import('@lezer/highlight'),
      ])
      const style = HighlightStyle.define([
        {
          tag: [tags.heading1, tags.heading2, tags.heading3, tags.heading4, tags.heading5, tags.heading6],
          color: '#0550ae',
          textDecoration: 'none',
        },
        { tag: tags.monospace, color: '#cf222e' },
        { tag: tags.link, color: '#0969da', textDecoration: 'underline' },
        { tag: tags.quote, color: '#57606a' },
        { tag: tags.processingInstruction, color: '#6e7781' },
      ])
      return [markdown(), syntaxHighlighting(style)]
    })()
  }
  return mdExtensions
}
```
并把 `languageFor` 中的 `md` 分支改为：
```ts
  if (ext === 'md') return markdownExtensions()
```

- [ ] **Step 3: 构建验证**

Run:
```bash
cd /Users/zhangfengda/workspace/groot/web && npm run build
```
Expected: 退出码 0，`web/dist` 更新；无 TypeScript 报错。

- [ ] **Step 4: 一次性 Node 脚本验证 Markdown 着色**

在 `web/.verify-md-highlight.mjs` 写入（验证后立即删除，不入库）：
```js
import { EditorState } from '@codemirror/state'
import { HighlightStyle, syntaxHighlighting, syntaxTree } from '@codemirror/language'
import { markdown } from '@codemirror/lang-markdown'
import { highlightTree, tags } from '@lezer/highlight'

const style = HighlightStyle.define([
  { tag: [tags.heading1, tags.heading2, tags.heading3, tags.heading4, tags.heading5, tags.heading6], color: '#0550ae', textDecoration: 'none' },
  { tag: tags.monospace, color: '#cf222e' },
  { tag: tags.link, color: '#0969da', textDecoration: 'underline' },
  { tag: tags.quote, color: '#57606a' },
  { tag: tags.processingInstruction, color: '#6e7781' },
])
const state = EditorState.create({
  doc: '# Title\n\n`code`\n\n[link](https://x)\n\n> quote\n',
  extensions: [markdown(), syntaxHighlighting(style)],
})
const spans = []
highlightTree(syntaxTree(state), style, (from, to, cls) => {
  spans.push([state.sliceDoc(from, to), cls])
})
console.log(spans)
const colored = (s) => spans.some(([text, cls]) => cls && text.includes(s))
if (!colored('Title') || !colored('code') || !colored('link') || !colored('quote')) {
  console.error('FAIL: Markdown 标题/行内代码/链接/引用未全部着色')
  process.exit(1)
}
console.log('PASS: Markdown 标题、行内代码、链接、引用均有高亮类')
```
Run:
```bash
cd /Users/zhangfengda/workspace/groot/web && node .verify-md-highlight.mjs && rm .verify-md-highlight.mjs
```
Expected: 输出 `PASS: Markdown 标题、行内代码、链接、引用均有高亮类`，退出码 0。

- [ ] **Step 5: 生成悬空快照（不提交）**

Run:
```bash
cd /Users/zhangfengda/workspace/groot
IDX=$(mktemp -u /tmp/groot-index-XXXXXX)
GIT_INDEX_FILE="$IDX" git read-tree HEAD
GIT_INDEX_FILE="$IDX" git add -A
TREE=$(GIT_INDEX_FILE="$IDX" git write-tree)
SNAP=$(git commit-tree "$TREE" -p HEAD -m "snapshot: task 2 markdown highlight")
rm -f "$IDX"
echo "$SNAP"
```
Expected: 输出一个 40 位 commit 哈希；工作区改动保留。

---

### Task 3: 端到端构建与验收

**Files:**
- 构建产物：`web/dist/`、`dist/groot`（均被 gitignore，不入库）

**Interfaces:**
- Consumes: Task 1、Task 2 的前端改动与依赖。
- Produces: 可运行的本地二进制，内含新前端。

- [ ] **Step 1: 用 `npm ci` 复现构建**

Run:
```bash
cd /Users/zhangfengda/workspace/groot && make build
```
Expected: `npm ci` 成功（证明 `package-lock.json` 与 `package.json` 一致），`vite build`、`vue-tsc -b` 通过，最后 `go build -o dist/groot ./cmd/groot` 成功，退出码 0。

- [ ] **Step 2: 确认二进制与前端产物存在**

Run:
```bash
cd /Users/zhangfengda/workspace/groot && ls -la dist/groot web/dist/index.html
```
Expected: 两个文件都存在且 `dist/groot` 修改时间晚于本次构建开始时间。

- [ ] **Step 3: 人工视觉验收清单（交由用户执行）**

启动 `dist/groot` 后在 Web 工作空间依次检查：
1. 打开 `.py` 文件点“编辑”：关键字、字符串、注释有颜色；输入改动后可保存。
2. 打开 `.md` 文件点“编辑”：标题蓝色、行内代码红色、链接蓝色；右侧 Markdown 预览正常；可收起预览。
3. 打开 `.json`、`.yaml`：高亮与改动前一致。
4. 打开 `.txt`：纯文本显示，无控制台报错。
5. 浏览器 Network 面板：首次打开某类型文件时才出现对应语言 chunk，首屏主包体积未明显增大。

- [ ] **Step 4: 生成最终悬空快照（不提交）**

Run:
```bash
cd /Users/zhangfengda/workspace/groot
IDX=$(mktemp -u /tmp/groot-index-XXXXXX)
GIT_INDEX_FILE="$IDX" git read-tree HEAD
GIT_INDEX_FILE="$IDX" git add -A
TREE=$(GIT_INDEX_FILE="$IDX" git write-tree)
SNAP=$(git commit-tree "$TREE" -p HEAD -m "snapshot: task 3 build verification")
rm -f "$IDX"
echo "$SNAP"
```
Expected: 输出一个 40 位 commit 哈希；`git status` 仅显示源码/文档改动，无构建产物。

---

## Self-Review

- Spec 覆盖：Python 着色 → Task 1；Markdown 着色 → Task 2；懒加载与既有语言不变 → Task 1/2 的构建与人工验收；`make web`/`go:embed` → Task 3；依赖与锁文件 → Task 1/2 Step 1。
- Review Focus 的 5 条均有人工验收项（Task 3 Step 3）覆盖；其中 1、2 另有 Node 脚本硬验证。
- 类型一致性：`languageFor` 统一返回 Extension、Extension[] 或 null；`markdownExtensions()` 返回 `Promise<Extension[]>`，被 `languageFor` 直接返回，与 `initEditor` 的 `Promise.all` 消费方式一致。
