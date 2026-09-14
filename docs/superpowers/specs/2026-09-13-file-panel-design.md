# 文件面板（Web 文件管理器）设计文档

> 位置：`docs/superpowers/specs/2026-09-13-file-panel-design.md`
> 日期：2026-09-13

## 一、功能设计

### 1.1 功能概述

文件面板是 Groot Web 对话界面中的文件管理器，让用户在不离开对话的情况下浏览、查看和管理 Groot home 目录（`~/.groot`，或 `GROOT_HOME` 指向的目录）中的内容。

它解决的问题：Groot 的 skill、subagent、MCP 配置、全局记忆（GROOT.md）等都以文件形式存放在 home 目录中，此前只能通过终端或本地文件管理器修改；文件面板把这些能力搬进 Web 界面，用户在与 agent 对话的同时即可查看 agent 的运行环境、编辑 skill 和配置、创建新的 skill/mcp/agent。

### 1.2 能力清单

1. **浏览**：树形展示 home 目录的目录与文件，目录懒加载展开/折叠
2. **预览**：单击文件在面板内只读预览（Markdown 渲染、代码高亮、图片显示、二进制提示下载）
3. **编辑**：在大弹窗中用代码编辑器编辑文本文件，Markdown 支持左右分屏实时预览
4. **语义化创建**：一键创建 skill、mcp、agent，按各自的目录/文件模板生成骨架并直接进入编辑
5. **文件管理**：重命名、删除（文件与空的非一级目录）、下载；在任意已存在目录内上传文件
6. **安全边界**：所有操作限制在 home 目录内；数据库文件对用户完全不可见；核心配置文件只读

### 1.3 界面设计

#### 1.3.1 入口与面板形态

- 对话页顶栏、日志图标右侧新增文件夹图标按钮
- 点击后从主区右侧推入**抽屉面板**，挤压（非遮盖）对话区宽度；再次点击收起
- 面板默认宽 390px，左边缘可拖动调节（280–600px）
- 面板开关状态与宽度持久化到 localStorage，刷新后保持

#### 1.3.2 面板结构（自上而下）

1. **头部工具栏**：标题「文件」+ 五个图标按钮
   - 创建 skill
   - 创建 mcp
   - 创建 agent
   - 全屏（面板临时扩展为覆盖整个主区，再点恢复）
   - 收起面板
2. **路径栏**：显示 home 目录绝对路径；右侧刷新按钮（重新加载整棵树，保持已展开状态）
3. **文件树**：主体区域
4. **预览区**：选中文件后在树下方（或替换树，见 1.3.4）展示预览

#### 1.3.3 文件树

- 树形结构，目录节点可展开/折叠，子内容在首次展开时向后端请求（懒加载）
- 排序：目录在前、文件在后，各自按名称排序
- 文件行显示类型图标（按扩展名区分 md/yaml/json/图片等）；只读文件显示 🔒 标记
- 行悬停浮现「⋯」按钮，点击弹出操作菜单：
  - 文件：重命名 / 下载 / 删除
  - 目录：重命名 / 上传 / 删除（一级目录的重命名与删除禁用）
  - Skill、MCP、Agent 的创建入口在面板头部，不在行菜单中
- 只读文件与结构性条目（一级目录、GROOT.md）的菜单中重命名、删除置灰

#### 1.3.4 预览

- 单击文件，面板内容切换为预览视图（顶部返回按钮回到树；树的展开状态保留）
- Markdown：使用与对话消息一致的渲染器（MarkdownView）渲染
- 代码/文本：语法高亮只读展示
- 图片（png/jpg/gif/svg/webp）：直接显示
- 其他二进制：提示「无法预览」并提供下载按钮
- 可编辑的文本文件在预览顶部显示「编辑」按钮

#### 1.3.5 编辑弹窗

- 从预览点「编辑」打开居中大弹窗（约 80% 视口宽高）
- 编辑器：CodeMirror 6，语法高亮（markdown/yaml/json 等）+ 行号，按需动态加载（不影响首屏体积）
- Markdown 文件：左侧源码、右侧实时渲染预览（复用 MarkdownView），预览可一键收起变全宽编辑；非 Markdown 文件无分屏
- 顶部：文件名、保存按钮、关闭按钮；内容有改动未保存时关闭需确认
- 保存走后端写入接口；只读文件不会出现编辑按钮，后端同时拒绝写入

#### 1.3.6 语义化创建

三个创建按钮的交互一致：点击 → 小对话框输入名称（前端校验：非空、无路径分隔符、不与现有同名冲突）→ 后端按模板生成 → 树刷新并定位到新条目 → 自动打开编辑弹窗。

生成模板：

| 按钮 | 生成内容 |
|---|---|
| 创建 skill | `skills/<名称>/SKILL.md`，frontmatter 含 `name`、`description` 占位 |
| 创建 mcp | `mcp/<名称>.json`，含 `name`/`type`/`description`/`isActive`/`command`/`args` 骨架 |
| 创建 agent | `subagents/<名称>/agent.md`（frontmatter 含 `description` 占位）+ 空目录 `mcp/`、`skills/` |

### 1.4 后端设计

#### 1.4.1 处理器与路由

新增 `internal/api/handler/files.go`，`FilesHandler` 构造时注入 `homeDir`。全部路由挂在 `/web` 分组（WebSession 中间件保护，需登录会话）：

```
GET    /web/files/list?path=       目录单层列表（树懒加载）
GET    /web/files/content?path=    读文件内容（含 readonly/binary 标记）
PUT    /web/files/content          保存文件 {path, content}
POST   /web/files/rename           重命名 {from, to}
DELETE /web/files?path=            删除文件或空目录（一级目录除外）
POST   /web/files/upload           上传（multipart：目标目录 + 文件）
GET    /web/files/download?path=   下载（attachment 流式输出）
POST   /web/files/scaffold         语义化创建 {kind: skill|mcp|agent, name}
```

`list` 返回条目结构：`{name, type: dir|file, size, mtime, readonly}`。

#### 1.4.2 路径安全（所有端点共用）

统一的路径解析函数，任何端点收到的 `path` 都必须经过它：

1. 拒绝空路径与含 NUL 的路径
2. `filepath.Clean` 后与 homeDir 拼接
3. `filepath.EvalSymlinks` 解析真实路径
4. 校验真实路径仍以 homeDir（同样经 EvalSymlinks）为前缀，否则返回 404
5. 命中隐藏规则的路径返回 404（与不存在不可区分）

#### 1.4.3 文件可见性与权限规则

| 规则 | 内容 |
|---|---|
| 隐藏 | home 根目录下基名匹配 `groot.db*` 的文件（含 `-wal`、`-shm`）：列表中过滤，直接访问返回 404 |
| 隐藏 | 所有目录下的 `.DS_Store`：仅列表过滤 |
| 只读 | home 根目录下的 `env.yaml`、`config.yaml`：可列出（带 readonly 标记）、可读、可下载；保存/重命名/删除返回 403 |
| 结构保护 | home 一级目录（skills/mcp/subagents/logs 等）与 `GROOT.md`：禁止重命名与删除（403）；GROOT.md 内容仍可编辑 |

#### 1.4.4 上传范围

上传允许在 home 内的任意已存在目录进行，包含 home 根目录本身；目标路径必须落在 home 内且指向一个已存在的目录，否则拒绝。后端强制校验，前端在每个目录的菜单中提供上传入口。

编辑保存、重命名、删除、下载不受上传规则约束（只受只读与隐藏规则约束）。

#### 1.4.5 限制与约束

- 读取/保存文本：≤ 2MB，超限返回明确错误
- 上传单文件：≤ 20MB
- 删除：仅文件与空目录；home 一级目录与 GROOT.md 禁删（403），非空目录返回 409
- 重命名：仅同目录；home 一级目录与 GROOT.md 禁改名（403）
- 内容读取对不可见字符/编码不做转换，按 UTF-8 处理；探测为二进制（含 NUL 字节）时 content 接口返回 binary 标记而非内容

### 1.5 前端设计

#### 1.5.1 组件

`web/src/components/files/` 下新增：

| 组件 | 职责 |
|---|---|
| `FilePanel.vue` | 抽屉容器：工具栏、路径栏、宽度拖动、全屏、树/预览切换 |
| `FileTree.vue` | 树渲染、懒加载、行悬停「⋯」菜单、重命名/删除/新建对话框 |
| `FilePreview.vue` | 面板内只读预览（md/代码/图片/二进制分派） |
| `FileEditorModal.vue` | 大编辑弹窗：CodeMirror 动态加载、md 分屏预览、保存 |

#### 1.5.2 状态与数据

- 新增 Pinia store `stores/files.ts`：面板开关、宽度、全屏态、树节点缓存、当前预览路径、编辑状态
- API 封装加在 `api/client.ts` 之上的 `api/files.ts`；类型定义入 `api/types.ts`
- localStorage 键：`groot-files-open`、`groot-files-width`

#### 1.5.3 依赖

- 新增 CodeMirror 6（`codemirror`、`@codemirror/lang-markdown`、`@codemirror/lang-yaml`、`@codemirror/lang-json`），仅在 `FileEditorModal` 内 `import()` 动态加载
- Markdown 预览、代码高亮复用现有 `MarkdownView.vue` / highlight.js，不引新库

#### 1.5.4 ChatView 集成

- 顶栏在日志按钮右侧加文件夹图标按钮（样式与 `topbar-logs` 一致，始终可用，不依赖会话）
- 主区布局改为 `main` + `FilePanel` 水平排列，面板展开时对话区自然变窄
- i18n：`zh-cn.ts` / `en.ts` 增加 `files.*` 文案组

### 1.6 错误处理

- 后端错误统一 JSON：`{status, message}`，前端 ElMessage 提示
- 树加载失败：节点上显示重试
- 保存冲突不做乐观锁（单用户场景），保存失败保留编辑器内容
- 401 沿用现有拦截跳登录

### 1.7 测试设计

Go 单元测试（`internal/api/handler/files_test.go`，必要时抽 `internal/webfiles` 包单测）：

- 路径安全：`../` 穿越、绝对路径注入、符号链接逃逸（链接指向 home 外）、NUL 字节
- 隐藏规则：列表过滤 `groot.db*`；直接 GET/PUT/DELETE `groot.db` 返回 404
- 只读规则：`env.yaml`/`config.yaml` 可读、写/删/改名 403
- 白名单：各位置的新建/上传允许与拒绝矩阵
- 删除：空的二级目录成功、一级目录 403、非空 409、文件成功
- scaffold：三种模板生成正确、重名冲突报错
- 大小限制：超限读取/保存/上传被拒

系统测试（Python，用户自行运行）：`tests/python/` 下补充文件面板 API 的端到端用例。

## 二、迭代说明

### 2.1 与上一版差异

本功能为全新增加，无上一版。相关的既有能力衔接：

- 新增：Web 界面文件面板全部能力（浏览/预览/编辑/创建/管理）
- 复用：`MarkdownView.vue`（预览渲染）、`/web` 分组的 WebSession 认证与限流中间件、`api/client.ts` 请求封装
- 调整：`ChatView.vue` 顶栏与主区布局（加图标、右侧挂面板）；`RegisterRoutes` 与 `NewServer` 增加 files 处理器装配
- 新依赖：CodeMirror 6（动态加载）

### 2.2 本次迭代

- 移除：行菜单中的「新建文件」与「新建子目录」两项；`/web/files/mkdir`、`/web/files/create` 两个端点及其服务层与前端 API 封装；`newFile`/`newDir` 两条 i18n 词条。创建资源统一走面板头部的 Skill / MCP / Agent 入口
- 调整：上传范围从白名单目录（`skills/` 及其子目录、`mcp/`、`subagents/` 及其子目录）放开为 home 内任意已存在目录，含 home 根目录；目标不存在或不是目录时返回 404
- 调整：目录折叠改为对任意层级直接生效——折叠一个目录时同步清理其整棵子树的展开状态，重新展开时子目录一律为收起态
