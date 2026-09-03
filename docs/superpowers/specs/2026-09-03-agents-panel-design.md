# WebUI Agents 面板设计文档

## 一、功能设计

### 1.1 功能概述

WebUI 设置弹窗中的 Agents 面板以卡片网格的形式展示系统内所有可调用的 Agent（主 Agent `groot` 与全部子 Agent），并支持查看每个 Agent 的定义文件（Markdown）原文。用户无需登录服务器即可在浏览器中直观了解每个 Agent 的职责说明与完整定义内容。

### 1.2 能力清单

- 以两列卡片网格展示 Agent 列表：每张卡片包含 Agent 名称、标签（主 Agent 显示「默认」标签）、职责描述、等宽字体的 Agent 标识，以及底部操作区。
- 卡片底部操作区提供一个「查看」图标按钮，点击后弹出定义查看弹窗。
- 定义查看弹窗：
  - 标题为「查看 · {Agent 名称}」；
  - 标题下方标注定义文件名（主 Agent 为 `GROOT.md`，子 Agent 为 `agent.md`）；
  - 正文为可滚动的等宽字体文本区域，原样展示定义文件的完整内容（含 frontmatter）；
  - 底部提供「关闭」按钮。
- 定义内容通过接口实时读取磁盘文件，保证查看到的始终是当前生效的定义。

### 1.3 设计细节

#### 1.3.1 接口设计

新增 Web UI 专用端点（走 Web 登录会话认证）：

```
GET /web/agents/:name/definition
```

响应 200：

```json
{
  "name": "db-agent",
  "file": "agent.md",
  "content": "---\ndescription: ...\n---\n\n# ..."
}
```

规则：

- `name` 为主 Agent 名（`groot`）时，读取 `{GROOT_HOME}/GROOT.md`，`file` 为 `GROOT.md`；
- 其他 `name` 必须命中子 Agent 注册表（`SubAgentRegistry.Get`），读取 `{GROOT_HOME}/subagents/{name}/agent.md`，`file` 为 `agent.md`；
- `name` 未注册时返回 404（`{"status":"not_found","message":...}`）；
- 文件读取失败（不存在/无权限）返回 404，避免向客户端暴露磁盘细节；
- 通过「必须命中注册表」的约束天然杜绝路径穿越——注册表键来自启动期目录扫描，不含路径分隔符。

#### 1.3.2 前端设计

- `SettingsModal.vue` Agents 分区渲染 `agent-grid`（`grid-template-columns: repeat(2, 1fr)`）；
- 卡片结构：标题行（名称 + 「默认」标签，仅主 Agent）→ 描述 → 等宽标识 → 分隔线 → 底部右对齐的查看图标按钮（`Document` 图标）；
- 查看弹窗为独立 `el-dialog`（`append-to-body`，嵌套于设置弹窗之上），内容区 `pre` 等宽字体 + 独立滚动条；
- 弹窗打开时调用 `GET /web/agents/{name}/definition` 拉取内容，加载中显示 loading，失败时在内容区展示错误提示。

## 二、迭代说明

### 2.1 与上一版差异

- 调整：Agents 面板由纵向列表（名称 + 描述 + skills 标签）改为两列卡片网格样式。
- 新增：卡片底部「查看」按钮与定义查看弹窗，展示 Agent 定义 md 文件原文。
- 新增：后端端点 `GET /web/agents/:name/definition`。
- 移除：Agent 卡片上的 skills 标签展示（skills 信息仍可在 Skills 面板按 Agent 筛选查看）。
