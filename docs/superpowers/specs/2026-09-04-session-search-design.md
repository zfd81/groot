# 会话搜索功能设计文档

## 一、功能设计

### 1.1 功能概述

Groot Web 提供历史会话的全文搜索能力。用户点击左侧栏顶部的搜索图标（或按 `Cmd/Ctrl + K`）打开搜索弹窗，输入关键词即可在所有历史会话的用户指令与 AI 回复中模糊匹配，点击结果直接进入对应会话并定位到匹配的那轮消息。

解决的问题：历史会话越积越多，侧栏列表只能按时间翻页浏览，用户想找"之前聊过的某个话题"时没有快捷手段。

### 1.2 能力清单

- 侧栏搜索入口：展开态在品牌区（logo 与折叠按钮之间）显示搜索图标按钮；收起态窄栏中搜索按钮位于展开按钮下方、新建会话按钮上方。
- 快捷键：`Cmd/Ctrl + K` 打开搜索弹窗，`Esc` 关闭。
- 空输入默认页：弹窗打开且输入框为空时，显示最近活跃的会话列表（标题 + 时间），点击直接进入该会话。
- 关键词搜索：对每轮对话的用户指令（instruction）和 AI 回复（result）做模糊匹配，输入 300ms 防抖后自动触发。
- 轮次级结果：每个匹配的轮次单独显示一条结果；同一会话多轮匹配时出现多条。每条结果显示所属会话标题、匹配摘要（关键词高亮）、匹配来源标识（我的提问 / AI 回复）、日期。
- 键盘导航：`↑` `↓` 移动选中项，`Enter` 进入选中结果。
- 跳转定位：点击搜索结果后关闭弹窗、打开对应会话，自动滚动到匹配轮次的消息并短暂高亮（约 1.5 秒）；从空输入默认页点击会话则停在最新消息，不定位。
- 状态反馈：请求中显示 loading，无结果显示"未找到相关话题"，请求失败显示错误提示。
- 国际化：所有文案提供中英文（i18n `search.*` 命名空间）。

### 1.3 设计细节

#### 1.3.1 后端搜索 API

**端点**：`GET /sess/search?q=<关键词>&limit=20`

- 注册位置：`internal/api/router.go` 的 `apiGroup`（走现有认证与限流中间件）。
- 参数：`q` 必填（去除首尾空白后非空，否则返回空结果）；`limit` 默认 20，上限 50。
- 查询逻辑：`memory_chats` JOIN `memory_sessions`，只匹配主 Agent 的已完成轮次（`agent_name = ''` 且 `status = 'completed'`，与会话标题、历史加载的口径一致，避免子 Agent 的内部指令与失败轮次混入结果）；`WHERE instruction LIKE '%q%' OR result LIKE '%q%'`，对 `%`、`_`、`!` 转义；按轮次开始时间（`started_at`）倒序。
- 用户过滤：用户标识取请求头 `X-User-ID`（与 `/chat` 端点一致）；非空时限定该用户的会话，为空时不按用户过滤（与 `/sess/history` 的行为一致）。
- SQL 兼容三种方言（SQLite / MySQL / Postgres），LIKE 语法通用，转义符用 `ESCAPE '!'` 显式声明（`!` 在三种方言的字符串字面量中均无特殊含义，规避反斜杠在 MySQL 字面量中的转义问题）。

**响应结构**：

```json
{
  "status": "success",
  "results": [
    {
      "session_id": "…",
      "chat_id": "…",
      "round": 3,
      "title": "会话标题（首轮指令）",
      "snippet": "…匹配关键词前后截取的原文片段…",
      "matched_field": "instruction | result",
      "timestamp": 1756900000000
    }
  ]
}
```

- `snippet` 在 Go 侧生成：定位关键词在匹配字段中的首次出现位置，向前取约 20 个字符、向后取约 60 个字符（按 rune 截取，保证 UTF-8 安全），两端被截断时补省略号。当 instruction 与 result 同时匹配时，优先返回 instruction 的片段。
- 关键词高亮由前端完成，后端只返回原文片段。

**分层实现**：

| 层 | 位置 | 实现内容 |
|---|---|---|
| repo 层 | `internal/repo/memorydb/memory.go` | `SearchChats(userID, keyword string, limit int)` 执行 SQL |
| 业务层 | `internal/memory/manager.go` | `Search(userID, keyword string, limit int)`，含参数校验与 snippet 生成 |
| API 层 | `internal/api/handler/search.go` | `SearchSessions` handler（Hertz 签名） |

#### 1.3.2 前端入口（SessionSidebar.vue）

- 展开态：品牌区（`.brand`）内、折叠按钮左侧设置搜索图标按钮，使用 `@element-plus/icons-vue` 的 `Search` 图标，尺寸与 hover 样式与折叠按钮一致。
- 收起态：窄栏（rail）中设置同样的搜索按钮，位置在展开按钮下方、新建会话按钮上方（自上而下依次为：展开、搜索、新建会话）。
- 两处按钮均触发 `emit('openSearch')`，由 ChatView 控制弹窗显隐。

#### 1.3.3 搜索弹窗（新组件 SearchModal.vue）

- 位置：`web/src/components/chat/SearchModal.vue`，基于 `el-dialog`，由 `ChatView.vue` 挂载并以 `v-model:show` 控制（与 SettingsModal 的接入方式一致）。
- 布局：上方为搜索输入框（打开时自动聚焦、带清空按钮），下方为可滚动的结果列表。
- 数据来源：
  - 输入框为空 → 调现有 `GET /sess/history?limit=20`，渲染最近会话列表（标题 + 相对时间）。
  - 输入非空 → 300ms 防抖调 `GET /sess/search`，渲染轮次级结果列表。
- 关键词高亮：将 snippet 按关键词切分后用文本节点分段渲染（高亮段加样式类），不使用 `v-html`，避免 XSS。
- 键盘交互：`↑` `↓` 在结果间移动选中，`Enter` 进入选中项，`Esc` 关闭弹窗。
- 全局快捷键 `Cmd/Ctrl + K` 在 ChatView 注册（`keydown` 监听，卸载时移除），打开弹窗。

#### 1.3.4 跳转与消息定位

- 点击搜索结果：关闭弹窗 → 调用现有 `chat.openSession(sid)` 加载会话 → `router.replace` 到 `/ui/chat/:sid`（复用 ChatView 现有 `handleSelect` 流程，扩展为可携带目标轮次 `round`）。
- 定位实现：`MessageList.vue` 为用户消息元素添加 `data-round` 属性；会话加载完成后 `nextTick` 查找目标轮次的用户消息元素，`scrollIntoView` 滚动到位，并添加约 1.5 秒的高亮背景样式后移除。
- 锚点取值：`data-round` 的值是后端 `/sess/{sid}` 每条历史消息返回的真实轮次号（`HistoryMessage.round`，由 `chat.openSession` 透传到 `ChatMessage.round`），与搜索结果的 `round` 同源。历史加载只返回已完成轮次，而轮次号是会话内的绝对序号，两者在存在失败轮次时并不连续，因此不能按消息顺序推算轮次。
- 目标轮次不存在（会话在搜索后被删改、或目标是本次会话中刚发出尚未带轮次号的消息）时静默降级为停在最新消息，不报错。

#### 1.3.5 错误处理

- 后端：`q` 为空或全空白 → 返回空结果；数据库错误 → 500 + 错误信息（与现有 handler 的错误返回风格一致）。
- 前端：请求失败在弹窗内显示错误文案（不弹全局通知，避免打断）；搜索请求发出后若输入已变化，丢弃过期响应（以最后一次请求为准）。

#### 1.3.6 测试

- Go 单元测试（`internal/repo/memorydb`、`internal/memory`）覆盖：正常匹配（指令命中 / 回复命中）、LIKE 特殊字符转义（`%`、`_`、`\`）、空关键词、用户数据隔离、limit 边界（默认值 / 上限截断）、snippet 截取（UTF-8 中文边界、两端省略号）。
- 系统测试（Python，`tests/python/`）由用户自行编写运行。

### 1.4 明确排除的内容

- 不做搜索结果分页（上限 50 条，靠更换关键词收敛结果）。
- 弹窗内不做"今天 / 昨天"时间分组，仅显示日期。
- 不引入 FTS5、分词或相关性排序，按时间倒序即可。

## 二、迭代说明

### 2.1 与上一版差异

本功能为首次设计，无历史版本。相关现状：

- 新增：后端 `GET /sess/search` 搜索端点（此前后端无任何搜索能力）。
- 新增：前端 `SearchModal.vue` 组件与侧栏搜索入口（此前前端无搜索相关代码）。
- 调整：`ChatView.vue` 的 `handleSelect` 扩展为支持携带目标轮次并定位；`MessageList.vue` 增加 `data-round` 锚点。
- 现有 `/sess/history` 端点、`openSession` 流程、路由结构均不变。
