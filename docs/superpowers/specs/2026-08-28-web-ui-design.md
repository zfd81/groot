# Groot Web 界面设计文档

## 一、功能设计

### 1.1 功能概述

Groot Web 界面是 Groot Agent 服务自带的图形化操作入口。用户启动 `groot` 服务后，用浏览器访问 `http://<host>:<port>/ui` 即可使用，无需任何额外部署。

Web 界面解决的问题：

- 让使用者不依赖终端（TUI）或编写客户端代码，就能与 Agent 对话、查看历史会话；
- 提供服务运行状态、Agent 能力（子 Agents / Skills / MCP 工具 / 模型）的可视化视图；
- 与 TUI、REST API 客户端完全对等——三者都是同一套 HTTP 协议的客户端，服务端引擎对它们一视同仁。

### 1.2 能力清单

首期版本包含以下模块：

| 模块 | 能力 |
|------|------|
| 登录页 | 用户名密码登录；Web 认证关闭时自动跳过登录直接进入界面 |
| 仪表盘 | 服务健康状态、运行时长、LLM 连通性、运行中对话数、会话总数、Skills / MCP 工具计数 |
| 聊天页（主界面） | 流式对话（增量渲染）、thinking 内容折叠展示、工具调用与结果折叠面板、子 Agent 事件标注、Markdown 渲染与代码高亮、附件上传、模型切换、Agent 切换（Solo 模式）、停止生成、可收起的左侧会话列表（分页）、继续任意历史会话、对话统计条（轮次 / 耗时 / Token 用量） |
| 设置弹窗 | 通用设置（界面语言：中文 / English；外观主题：浅色 / 深色 / 跟随系统）；能力总览只读列表：子 Agents / Skills / MCP 工具 / 可用模型 |

界面语言支持中文与 English 两档，用户可在通用设置中随时切换，界面全部文案与 Element Plus 组件内置文案（空状态、校验提示等）一并切换。首次访问时语言跟随浏览器语言（`navigator.language` 以 `zh` 开头则中文，否则英文），用户的选择持久化保存，刷新与重启浏览器后保持不变。

### 1.3 总体架构

```
┌───────────────────────────────────────────────┐
│                浏览器（Vue 3 SPA）              │
│   登录页 │ 仪表盘 │ 聊天页（主界面）│ 设置弹窗   │
└───────────────────────────────────────────────┘
        │ 同源 HTTP（无 CORS）
        ▼
┌───────────────────────────────────────────────┐
│           Groot 服务（Hertz，单进程单端口）      │
│  /ui/*        → 静态资源（go:embed web/dist）  │
│  /web/login   → Web 登录（发 HttpOnly Cookie） │
│  /web/logout  → 退出登录                       │
│  /web/me      → 登录态查询                     │
│  /chat 等现有 API → 认证中间件（Cookie 或       │
│                     X-API-Key 双凭证）→ 引擎   │
└───────────────────────────────────────────────┘
```

设计原则：

1. **单二进制交付**：前端构建产物通过 `go:embed` 编译进 groot 二进制，与 API 服务共用同一进程、同一端口、同一份 `config.yaml`，升级二进制即同时升级界面。
2. **同源架构**：前端与 API 同源，天然规避 CORS 与跨域认证问题。
3. **引擎零改动**：Web 界面只消费现有 HTTP 协议（`POST /chat` SSE 流式 + REST 查询端点），`internal/agent/` 引擎层不做任何修改。

### 1.4 后端设计（Go）

改动集中在 `internal/api/` 与 `internal/config/`。

#### 1.4.1 静态资源托管

- 文件 `internal/api/webui.go`：embed `web/dist` 目录，注册 `GET /ui/*` 路由。
- 对未匹配静态文件的路径回退返回 `index.html`，支持前端 history 路由。
- `web/dist` 为空时（未构建前端）Go 编译不受影响，访问 `/ui` 返回"前端未构建"的提示页。

#### 1.4.2 Web 认证

配置 `security.web` 段：

```yaml
security:
  web:
    enabled: true                # 是否启用 Web 登录认证
    username: admin              # 登录用户名
    password: ${GROOT_WEB_PASS}  # 登录密码（支持环境变量引用）
    session_ttl: 24h             # 登录会话有效期
    secure: false                # 会话 Cookie 是否置 Secure（经 https 部署时设为 true）
```

端点：

| 方法+路径 | 作用 |
|---|---|
| `POST /web/login` | 校验用户名密码，成功后签发会话令牌，写入 HttpOnly + SameSite=Strict Cookie |
| `POST /web/logout` | 使当前会话令牌失效并清除 Cookie |
| `GET /web/me` | 返回当前登录态（前端启动时判断是否需要跳登录页） |

会话令牌为随机值，存储在服务进程内存中（带 TTL 过期清理）；服务重启后需重新登录，符合单账号定位。登录失败做简单限速，防止密码爆破。

默认值与联动规则：

- `security.web.enabled` 默认为 `false`（Web 免登录直接使用）；
- 当 `security.auth.enabled` 为 `true`（API Key 认证开启）而 Web 认证关闭时，浏览器请求没有任何凭证会被 API 认证拦截，Web 界面不可用。此时服务启动日志给出警告，提示需要开启 `security.web`；
- 两者都开启时，浏览器走 Cookie、API 客户端走 API Key，互不干扰。

#### 1.4.3 认证中间件双凭证

现有 auth 中间件（`internal/api/middleware/auth.go`）的判定逻辑为：

1. 请求携带有效 Web 会话 Cookie → 通过认证，权限等同 `all`；
2. 否则按 `X-API-Key` 逻辑判定（原有行为不变）。

API 客户端（Python / Java 示例、TUI）的使用方式完全不受影响。

**权限边界**：Web 会话是单账号登录凭证，通过后即赋予 `all` 等效权限，**不参与 API Key 的按端点细粒度权限校验**（`chat`/`session`/`skills` 等）。这与 Web 界面的定位一致——登录用户即管理员，可访问界面呈现的全部功能。若需要为 Web 用户施加受限权限，须引入多账号/角色体系（见二期候选），当前版本不支持。

**登录限速来源标识**：登录失败限速以真实 TCP 对端地址（`RemoteAddr()`）为来源键，而非采信 `X-Forwarded-For` 的 `ClientIP()`，避免攻击者每次伪造请求头即可绕过锁定。此外设有全局兜底计数：即便单来源未超限，窗口内所有来源合计失败过多也一律锁定，防止用大量不同来源键规避单来源限制。

**会话 Cookie Secure 标志**：由 `security.web.secure` 配置控制。Groot 常以 http 在内网/本机运行，默认 `false`；经 https 反向代理部署时应设为 `true`，使 Cookie 仅随 https 请求发送。

#### 1.4.4 停止生成

前端通过 `AbortController` 断开 `POST /chat` 的 SSE 连接，服务端依既有机制（连接断开触发 context 取消）终止本次对话执行。不引入独立的取消端点。

#### 1.4.5 仪表盘数据来源

仪表盘完全基于现有端点聚合：`GET /health`（uptime、LLM 连通性、MCP / Skills / 会话计数、运行中对话数）与 `GET /sess/history`。不引入统计类新端点。

### 1.5 前端设计（web/）

#### 1.5.1 技术栈与目录

- Vue 3 + TypeScript + Vite + Element Plus + Vue Router + Pinia。
- 源码位于仓库 `web/` 目录，是带独立 `package.json` 的子项目。
- 开发模式：Vite dev server 将 API 请求代理到 `localhost:8080`，前端改动即时热更新，不需要重编译 Go。

```
web/
├── package.json
├── vite.config.ts          # dev 代理 + build 输出到 dist/
├── index.html
└── src/
    ├── main.ts
    ├── router/             # 路由（含登录态守卫）
    ├── stores/             # Pinia（登录态、当前会话、模型/Agent 列表）
    ├── api/                # HTTP 封装（fetch，401 统一拦截跳登录）
    ├── views/
    │   ├── LoginView.vue
    │   ├── DashboardView.vue
    │   └── ChatView.vue        # 主界面：侧栏 + 对话区
    └── components/
        ├── chat/           # 消息气泡、thinking 折叠、工具调用面板、输入框、附件、会话侧栏、统计条
        ├── settings/       # 设置弹窗（通用设置 + 能力总览各分类面板）
        └── common/
```

#### 1.5.2 界面布局与视觉风格

整体为简洁浅色风格（圆角卡片、灰白配色、留白充分），支持浅色 / 深色 / 跟随系统三档主题（基于 Element Plus 主题机制实现）。

布局骨架：

- **左侧栏（可收起）**：顶部为 Logo 与"新会话"按钮及收起按钮；中部为会话历史列表（标题 + 相对时间，分页加载），会话标题取该会话首轮的用户指令（由 `GET /sess/history` 的 `title` 字段提供，超长省略号截断；无对话记录时回退显示会话 ID 前缀）；提供仪表盘入口；底部为"设置"按钮。
- **主对话区**：顶部显示当前会话标题；消息流中用户消息为右侧气泡、AI 回复为全宽 Markdown 排版；thinking 内容折叠为单行（可展开）；工具调用与结果为折叠面板。
- **输入区**：底部圆角卡片式输入框，工具条内嵌附件"+"按钮、模型切换下拉、Agent 切换（Solo 模式）、发送/停止按钮；输入框下方为对话统计条（轮次、耗时、Token 用量，数据来自对话详情端点）。
- **设置弹窗**：点击侧栏底部"设置"弹出模态窗，左侧为分类导航（通用设置 / 模型 / Skills / MCP 工具 / 子 Agents），右侧为内容区；通用设置含界面语言选择（右侧下拉：中文 / English）、外观主题选择（一排三个大卡片按钮：浅色 / 深色 / 跟随系统，各带图标，选中态以主题色高亮）与运行环境信息（工作目录、数据库类型、日志目录三行只读展示，每行左侧为加粗标题与灰色说明、右侧为等宽字体的值，数据来自 `GET /health` 的 `environment` 检查项），其余分类为只读能力列表。

#### 1.5.3 聊天 SSE 处理

- 使用 `fetch` + `ReadableStream` 逐行解析 SSE（`EventSource` 不支持 POST）。
- 事件 schema 与 TUI 客户端一致（参照 `internal/cmd/chat/messages.go` 的 `SseEvent`）：按 JSON 字段区分 thinking 增量 / 消息增量 / 工具调用 / 工具结果 / finish / error / `[DONE]`；带 `agent_name` 字段的事件标注为子 Agent 产生。
- 请求头携带 `X-Session-ID`（继续会话）、`X-Model-Name`、`X-Agent-Name`（Solo 模式）；从响应头读取 `X-Session-ID` / `X-Chat-ID`。
- 附件沿用现有协议：文件读取为 base64，放入请求体 `attachments` 数组。

#### 1.5.4 渲染

- Markdown 渲染用 `markdown-it`，代码块用 `highlight.js` 高亮。
- 流式输出增量更新消息内容，依赖 Vue 响应式做细粒度重渲染。
- thinking 内容与工具调用/结果默认折叠，可展开查看详情。

#### 1.5.5 国际化（中英文切换）

界面支持中文（`zh-cn`）与英文（`en`）两种语言，全站文案与 Element Plus 组件内置文案统一切换。

- **文案库**：基于 `vue-i18n`（Composition API 模式，`legacy: false`），文案按命名空间组织（`common` / `login` / `chat` / `sidebar` / `dashboard` / `settings` / `tool` / `transcript` / `error`），中英两份 message 的 key 一一对应。带动态值的文案用具名参数插值（如轮次、相对时间、请求失败状态码、附件名），不做字符串拼接。设 `fallbackLocale: 'zh-cn'`，避免缺 key 时暴露 key 名。
- **单一数据源**：新增 `language` Pinia store（仿 `theme` store 模式）作为语言状态的唯一来源，切换语言即写 store 并持久化到 `localStorage`（key `groot-language`）。`App.vue` 通过 `watch` 将 store 的语言同步给 `vue-i18n` 实例，同时用 `el-config-provider` 按当前语言注入 Element Plus 对应的 locale 包（`zh-cn` / `en`），使组件内置文案随之切换。
- **初值时序**：`vue-i18n` 实例创建与 store 初始化各自独立读取同一来源确定初值——先读 `localStorage`，无值则回退到浏览器语言（`navigator.language` 以 `zh` 开头为中文，否则英文），保证首屏语言即正确，无闪烁。
- **组件外文案**：非组件模块（`api/*.ts`、`stores/*.ts`）中的提示与抛错文案无法使用 `useI18n()`，改用全局实例 `i18n.global.t(...)` 取值。
- **自动导入**：`vite.config.ts` 的 `AutoImport` 引入 `vue-i18n` 预设，组件内直接使用 `useI18n()` 无需手写 import。

### 1.6 构建与交付

- Makefile 目标 `make web`：在 `web/` 下执行 `npm ci && npm run build`，产出 `web/dist`。
- `make build` 依赖 `make web`，最终 `go build -o bin/groot ./cmd` 产出含前端的单二进制。
- `web/node_modules`、`web/dist` 加入 `.gitignore`（构建产物不入库）。

### 1.7 错误处理

| 场景 | 行为 |
|---|---|
| 未登录访问（401） | 前端统一拦截，跳转登录页 |
| 同会话并发对话（409 `chat_limit_exceeded`） | 提示"该会话有正在进行的对话" |
| SSE 中途断线 | 提示"连接中断"，保留已接收内容 |
| 登录失败 | 提示错误；服务端对失败尝试限速 |

### 1.8 测试设计

| 测试 | 范围 | 执行者 |
|---|---|---|
| Go 单元测试 | 登录/登出/会话过期、双凭证中间件（Cookie / API Key / 都无）、静态路由回退 | Claude（随代码提交） |
| Python 系统测试（`tests/python/`） | 登录流程、未登录访问 401、携带 Cookie 访问各端点、登录限速 | 用户 |
| 前端 | 首期不引入前端测试框架（遵守项目测试规范） | — |

## 二、迭代说明

### 2.1 与上一版差异

本文档为 Web 界面的首个设计版本。相对现状（仅 TUI + REST API 两种使用方式）的变化：

- 新增：`web/` 前端子项目（Vue 3 + Vite + Element Plus）。
- 新增：`internal/api/webui.go` 静态资源托管（go:embed），路由 `/ui/*`。
- 新增：`POST /web/login`、`POST /web/logout`、`GET /web/me` 三个端点及内存会话存储。
- 新增：配置段 `security.web`（enabled / username / password / session_ttl）。
- 调整：auth 中间件支持 Cookie 与 `X-API-Key` 双凭证（API Key 行为不变）。
- 调整：Makefile 增加 `make web` 目标，`make build` 依赖之。
- 不变：`internal/agent/` 引擎、`POST /chat` SSE 协议、所有现有 REST 端点、TUI 与 Python/Java 客户端行为。

### 2.2 v2 变更：国际化与通用设置改版

- 新增：全站中英文（`zh-cn` / `en`）切换能力，界面文案与 Element Plus 内置文案统一切换，默认跟随浏览器语言并持久化到 `localStorage`。
- 新增：`vue-i18n` 依赖与文案库（`web/src/i18n/`，含 `zh-cn` / `en` 两份 message）；`web/src/stores/language.ts` 语言 store。
- 新增：`App.vue` 接入 `el-config-provider`，按语言注入 Element Plus locale 包，并 `watch` 语言 store 同步 `vue-i18n`。
- 调整：`vite.config.ts` 的 `AutoImport` 增加 `vue-i18n` 预设。
- 调整：通用设置由单一"外观主题"行改版为"界面语言下拉 + 外观主题三卡片按钮"，原 `el-radio-group` 外观控件移除。
- 调整：全部含用户可见中文的前端文件，硬编码文案替换为 `t()` / `i18n.global.t()`。
- 不变：后端、`/chat` SSE 协议、所有 REST 端点与既有能力（纯前端改动）。

### 2.3 v3 变更：会话侧栏标题与实时刷新

- 新增：`GET /sess/history` 的会话条目返回 `title` 字段（该会话首轮主 Agent 对话的用户指令，来自 `memory_chats` 表，SQL 关联子查询获取，无 schema 变更）。
- 调整：侧栏会话标题由"会话 ID 前 8 位"改为显示 `title`，无 title 时回退 ID 前缀。
- 调整：新会话在流式响应开始（拿到 `X-Session-ID` 响应头）时即插入侧栏顶部本地占位条目（标题为本次输入的指令），执行动画随即可见；流结束后由服务端列表数据覆盖。
- 不变：`/sess/history` 既有字段、分页协议、其余端点与 TUI 行为。

### 2.4 v4 变更：通用设置展示运行环境信息

- 新增：`GET /health` 的 `checks` 增加 `environment` 检查项，`info` 携带 `home_dir`（工作目录，即 GROOT_HOME 解析后的绝对路径）、`database`（数据库类型：sqlite / mysql / postgres，配置缺省按 sqlite）、`log_dir`（日志目录，启动时已解析为绝对路径）。
- 调整：`handler.NewHealthHandler` 签名增加 `homeDir` 参数（`NewServer` 已持有该值，仅透传）。
- 新增：设置弹窗通用面板在外观主题下方增加三行只读运行环境信息（工作目录 / 数据库 / 日志目录），沿用既有 `.row` 行式布局与 `.mono` 值样式；对应中英文 i18n 文案。
- 不变：`/health` 既有检查项与响应结构、其余端点与页面。

### 2.5 明确排除（二期候选）

- 定时任务管理页（列表/启停/删除/执行历史）；
- Skills 在线安装/卸载（需新增写操作端点）;
- 显式取消端点（顺带修复客户端 `DELETE /chat/:sid` 调用与服务端路由缺失的不一致）；
- 多账号 / 角色权限体系；
- Token 消耗统计端点与图表；
- 会话"轨迹"视图（按时间线展示每一步工具调用与结果的独立页签）与会话记录导出。
