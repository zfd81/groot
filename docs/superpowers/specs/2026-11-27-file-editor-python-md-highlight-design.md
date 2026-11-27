# 工作空间文件编辑器 Python 与 Markdown 语法高亮设计文档

## 一、功能设计

### 1.1 功能概述

Groot Web 工作空间的文件编辑弹窗基于 CodeMirror 6 提供就地编辑。编辑器按文件扩展名加载语言包，为源码提供与开发工具一致的语法着色。本功能使 Python 与 Markdown 文件在编辑弹窗中具备清晰可见的语法高亮。

### 1.2 能力清单

- `.py` 文件：关键字、字符串、数字、注释、函数与类型名等按 Python 语法着色。
- `.md` 文件：标题、行内代码、链接、引用具备区别于正文的颜色；强调、加粗、删除线沿用既有样式。
- 语言包按扩展名动态加载：只有打开对应类型的文件时才加载该语言包，不增加首屏体积。
- `.json`、`.yaml`/`.yml` 以及无语言包的文件类型保持现有行为。

### 1.3 设计细节

- 语言选择：`FileEditorModal` 的 `languageFor(path)` 按扩展名返回 CodeMirror 扩展。
  - `py` → `@codemirror/lang-python` 提供的 `python()`。
  - `md` → `@codemirror/lang-markdown` 提供的 `markdown()`，并在编辑器扩展中追加 Markdown 高亮样式。
  - `json`、`yaml`/`yml` 维持现状。
- Markdown 高亮样式定义标题、行内代码、链接、引用的颜色与约定字重/装饰（标题加粗、链接下划线、标题去除下划线）。样式以 CodeMirror 默认高亮规则（`defaultHighlightStyle`）为基础合并，其规则在前、自定义规则在后，因此加粗、斜体、删除线、裸链接等默认样式继续生效，重叠处以自定义规则为准。不改变编辑器主题、字体以及明暗主题切换逻辑。
- 依赖：`web/package.json` 增加 `@codemirror/lang-python`、`@lezer/highlight`、`@codemirror/language`、`@codemirror/state` 为直接依赖，并同步更新 `package-lock.json`。
- 构建：`make web` 重新生成 `web/dist`，Go 二进制通过 `go:embed` 携带新前端。

## 二、迭代说明

### 2.1 与上一版差异

- 新增：`@codemirror/lang-python`、`@lezer/highlight`、`@codemirror/language`、`@codemirror/state` 依赖与 `.py` 语言分支。
- 新增：Markdown 专用高亮样式，标题、行内代码、链接、引用有颜色区分；样式合并默认高亮规则，加粗、斜体、删除线保持既有样式。
- 调整：编辑弹窗内 Markdown 的观感由「仅加粗/下划线」变为带颜色的语法高亮。

### 2.2 非目标

- 不为 Go、Java、JavaScript、TypeScript、Bash 等语言增加语言包。
- 不为 Markdown 代码围栏内的语言做嵌套高亮。
- 不引入完整编辑器主题（如 one-dark），不改动 Element Plus 主题体系。
