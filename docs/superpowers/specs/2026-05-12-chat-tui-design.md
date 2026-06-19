# groot chat TUI 设计文档

## 一、功能设计

### 1.1 功能概述

`groot chat` 子命令在终端打开一个类 Claude Code 的 TUI 聊天界面，作为 HTTP+SSE 客户端连接 groot API 服务，复用 agent 引擎的全部能力（tool use、MCP、memory、子 Agent、技能）。

TUI 使用 [Bubble Tea](https://charm.land) 框架，运行于 Alternate Screen（独占全屏模式），退出后恢复原终端画面。启用鼠标 cell motion 模式以接收滚轮和点击事件，由 TUI 自身处理滚动和输入框聚焦。

实现入口：[`internal/cmd/chat.go`](../../../internal/cmd/chat.go)，TUI 模型层位于 [`internal/cmd/chat/`](../../../internal/cmd/chat/) 包。

### 1.2 启动流程

`groot chat` 启动时执行以下流程，无论 API 服务是否已在运行用户都可以使用 TUI：

#### 步骤 1：读取配置

[`config.Load`](../../../internal/config/config.go) 从 `~/.groot/config.yaml` 读取配置。如果配置文件不存在或加载失败，返回错误：

```
配置文件不存在，请先执行 groot init 初始化配置
```

#### 步骤 2：检测服务

向 `http://localhost:<server.port>/health` 发送 GET 请求，超时 2 秒。

#### 步骤 3A：服务已运行（健康检查成功）

```
检测到已有服务运行 (端口 8080)
```

- 不启动嵌入服务
- 打开 TUI，HTTP 客户端指向已有服务
- TUI 退出时只关闭自身，已有服务继续运行

#### 步骤 3B：服务未运行（健康检查失败）

```
未检测到运行中的服务，正在启动嵌入服务...
```

- 在当前进程中通过 [`startEmbedServer`](../../../internal/cmd/chat.go) 启动一个完整 API 服务
- 强制把日志输出改成只写文件（去掉 `stdout`），并将 Hertz 内部日志重定向到 `io.Discard`，避免污染 TUI 渲染
- 初始化 skills、MCP、数据库、Memory、消息层、子 Agent、Executor 等组件，与 `cmd/groot/main.go` 保持一致；嵌入模式下 Schedule 调度器关闭，且不注册 stdout 消息发送器
- 在后台 goroutine 中启动服务，主流程轮询 `/health`（10 秒超时）等待就绪
- TUI 退出时 `defer srv.Stop(context.Background())` 优雅关闭嵌入服务，并打印 `嵌入服务已关闭`

#### 步骤 4：首次会话初始化

TUI 启动时没有会话 ID，状态栏显示 `会话: 新会话 | 对话: 第 0 轮`。首次发送消息时不带 `X-Session-ID`，API 会自动创建新会话并通过响应头 `X-Session-ID` 返回。客户端在 [`client.go`](../../../internal/cmd/chat/client.go) 的 `SendChatStream` 中读取该响应头并发送 `SessionIDMsg`，由 model 层更新状态栏与 client 内部 sessionID。

`/clear` 清除会话：丢弃当前会话 ID，状态栏恢复为 `会话: 新会话 | 对话: 第 0 轮`，下一条消息重新触发上述流程。

#### 步骤 5：显示欢迎画面

进入 TUI 后，对话区显示欢迎画面（定义在 [`welcome.go`](../../../internal/cmd/chat/welcome.go)）：

```
   ██████╗ ██████╗  ██████╗  ██████╗ ████████╗
  ██╔════╝ ██╔══██╗██╔═══██╗██╔═══██╗╚══██╔══╝
  ██║  ███╗██████╔╝██║   ██║██║   ██║   ██║
  ██║   ██║██╔══██╗██║   ██║██║   ██║   ██║
  ╚██████╔╝██║  ██║╚██████╔╝╚██████╔╝   ██║
   ╚═════╝ ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝

        Groot AI Agent · v1.0.0
   ─────────────────────────────
   输入你的问题开始对话
   输入 /help 查看系统命令
```

欢迎画面在首次发送消息后自动滚出视野。

#### 步骤 6：进入 TUI 主循环

启动 Bubble Tea 主循环。在 `Init` 中并行触发模型列表、Agent 列表两个异步预取（[`fetchModelsCmd`](../../../internal/cmd/chat/model.go)、[`fetchAgentsCmd`](../../../internal/cmd/chat/model.go)），结果通过 `ModelsListMsg`、`AgentsListMsg` 异步到达后缓存到 model 字段。

#### 嵌入服务 vs 已有服务区别

| 行为 | 已有服务 | 嵌入服务 |
|------|---------|---------|
| 启动方式 | 用户手动 `groot` 启动 | TUI 自动启动 |
| 生命周期 | 独立于 TUI | 跟随 TUI |
| TUI 退出 | 服务继续运行 | 服务随 TUI 退出（defer srv.Stop） |
| 日志输出 | 正常输出 | 只写文件，不输出 stdout |
| 配置来源 | 同一份 config.yaml | 同一份 config.yaml |
| Schedule | 启用 | 禁用（传入 nil） |

### 1.3 界面布局

TUI 使用 AltScreen 全屏模式，从顶到底依次为：

```
   ██████╗ ██████╗  ██████╗  ██████╗ ████████╗
  ██╔════╝ ██╔══██╗██╔═══██╗██╔═══██╗╚══██╔══╝
  ██║  ███╗██████╔╝██║   ██║██║   ██║   ██║         ← 欢迎画面（无边框）
  ██║   ██║██╔══██╗██║   ██║██║   ██║   ██║
  ╚██████╔╝██║  ██║╚██████╔╝╚██████╔╝   ██║
   ╚═════╝ ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝

         Groot AI Agent · v1.0.0
    ─────────────────────────────
    输入你的问题开始对话
    输入 /help 查看系统命令

  > 用户消息                                         ← 消息区（viewport，无边框）

  助手回答内容（Markdown 渲染）...

  🤔 Thinking...                                     ← 思考过程（灰色斜体）
  🔧 调用工具: groot_file_read ⠹                     ← 工具调用 + 进行中 spinner
     ├─ name = report.pdf

  ┌─ 补全浮层（条件显示，叠加在 viewport 底部）────┐
  │  /exit      退出聊天                             │  ← 圆角边框覆盖层
  │  /model     切换模型                             │
  └──────────────────────────────────────────────────┘

  ╔══════════════════════════════════════════════════╗
  ║  > 用户输入内容...                               ║  ← 输入区（绿色双线边框）
  ╚══════════════════════════════════════════════════╝
  模型: gpt-4o | Agent: groot   会话: sess-abc123    对话: 第 3 轮  ← 状态栏（底部一行）
```

布局算法（[`Model.View`](../../../internal/cmd/chat/model.go)）：

- viewport 高度 = 终端高度 - 6（输入框边框 2 + 内容 3 + 分隔 1 + 状态栏 1）
- 当 popup 或 completion 浮层可见时，从 viewport 底部裁剪等量行让位，输入框位置不变
- 内容总行数若超过终端高度，从顶部裁剪并同步修正光标 Y 坐标
- 通过 `tea.View` 暴露 textarea 内置光标，支持 IME 输入法

### 1.4 状态栏

[`StatusBar`](../../../internal/cmd/chat/statusbar.go) 渲染单行三段布局：左中右。

- 左：`模型: <ModelName> | Agent: <AgentName>`，AgentName 为空时显示为 `groot`（主 Agent）
- 中：`会话: <SessionID>`（初始为 `新会话`），居中对齐
- 右：`对话: 第 <Round> 轮`

颜色为 `#888888` 灰色，无背景色。每发送一条消息 Round +1，跨 `/clear` 重置为 0。切换 Agent 或 `/clear` 时不重置 ModelName/AgentName。

### 1.5 对话显示区渲染规则

[`viewport.go`](../../../internal/cmd/chat/viewport.go) 中的 `ViewportModel.rerender` 根据 `ChatMessage.Role` 字段产出不同样式的块：

| Role | 显示形式 |
|------|----------|
| `user` | `> 内容`，整行使用浅色背景（`#2c313a`），背景宽度填充至 viewport 内容宽 |
| `thinking` | `🤔 Thinking...` 标签 + 灰色斜体内容，缩进 3 字符，按宽度自动换行 |
| `tool_call` (普通) | `🔧 调用工具: <name>`（黄色 `#e5c07b`），下方以树形 `├─ key = value` 显示参数 |
| `tool_call` (skill) | `⚡ 调用技能: <skill_name>`（紫色 `#c678dd`）；skill_name 从 JSON 参数 `skill`/`name`/`skill_name` 提取 |
| `tool_call` (call_agent) | `🤖 调用子 Agent: <agent_name>`（青色 `#56b6c2`）；从 JSON 参数 `agent_name` 提取 |
| `tool_result` | 静默不显示（由 LLM 最终回答中体现） |
| `tool_error` | `❌ 工具错误: <name>` + 红色 `#e06c75` 内容；超过 200 字符截断为 `... [展开]` |
| `assistant` | glamour Markdown 渲染（详见 1.6） |
| `cancel` | `⏹️ 已取消` （灰色） |
| `length` | `⚠️ 已达 token 上限` （黄色） |
| `error` | `❌ 错误: <内容>` （红色） |
| `loading` | 三空格缩进 + 当前 spinner 帧 + 空格 + `正在思考...` （绿色斜体） |
| `system` | 等宽渲染，用于 `/mcp`、`/export` 等命令的 API 响应 |

#### SSE 事件分类

[`classifyEvent`](../../../internal/cmd/chat/model.go) 根据 SSE JSON 字段判别事件类型：

| 判别条件 | 类型 | 处理 |
|----------|------|------|
| `event == "error"` | `error` | 添加 error 块，停止流式 |
| `reasoning_content != ""` | `thinking` | 追加到尾部 thinking 块或新建 |
| `tool_calls` 非空 | `tool_calls` | 按 `id` / `index` / `name` 顺序聚合 streaming delta；不存在则新建 |
| `role == "tool"` 且 `error == true` | `tool_error` | 新建 tool_error 块 |
| `role == "tool"` | `tool_result` | 静默 |
| `finish_reason != ""` | `finish_reason` | `stop` 静默；`cancelled`/`user_cancelled` 添加 cancel 块；`length` 添加 length 块 |
| `content != ""` | `message` | 追加到尾部 assistant 块或新建，按 Markdown 渲染 |
| 其他 | `unknown` | 忽略 |

[`ViewportModel.UpdateToolCall`](../../../internal/cmd/chat/viewport.go) 用三段匹配（id → index → name）将 OpenAI 流式 tool_call delta 拼接到同一条消息上，匹配范围限制在当前轮次内（向前扫到 `user` 消息为止）。

#### 加载与进行中动画

- 用户发送消息后立即追加 `loading` 占位，使用 [`bubbles/spinner`](https://charm.land) 的 Dot 风格、绿色 `#98c379`
- spinner.TickMsg 同时驱动 viewport 中"正在进行中的尾部 tool_call"末尾的旋转图标，让 skill / call_agent / 普通工具的等待期间也有动画反馈
- 收到下一个 SSE 事件时先移除尾部 loading，再处理事件，处理完后若尾部不是 assistant/thinking/loading，则重新追加 loading 占位（[`maybeAppendLoading`](../../../internal/cmd/chat/model.go)）
- StreamDoneMsg / StreamErrorMsg / `error` / `finish_reason` 收到时清除 loading

### 1.6 Markdown 渲染

assistant 消息使用 [`charmbracelet/glamour`](https://github.com/charmbracelet/glamour) 在终端渲染 Markdown：

- 固定主题 `pink`（`glamour.WithStylePath("pink")`），避免 `WithAutoStyle` 触发终端能力查询导致输入框收到转义字符；pink 主题将 `###` 标题语法替换为 `┃ ` 装饰前缀
- 按 viewport 宽度自动换行（`glamour.WithWordWrap(width-4)`）
- 启用 `WithPreservedNewLines`，保留 LLM 输出中的原始换行
- 渲染前调用 [`normalizeMarkdown`](../../../internal/cmd/chat/viewport.go) 把 LLM 常用的 Unicode 项目符号（`•`、`◦`、`▪`、`●`、`○`、`·`、`∙`、`⋅`、`‣`、`▸`）转换成标准 `- `
- 调用 [`preserveLineBreaks`](../../../internal/cmd/chat/viewport.go) 给非块结构相邻行追加 `  \n` 硬换行，避免 glamour 把多行合并成段落
- 调用 [`breakLongLines`](../../../internal/cmd/chat/viewport.go) 对 CJK 长行做硬断行，补充 wordwrap 库对无空格文本的支持
- 窗口宽度变化时重建 renderer（仅宽度变化才触发，避免频繁重建）

### 1.7 交互细节

- **用户消息样式**：`> ` 前缀 + 内容，整行浅灰背景，宽度填满 viewport 内容区域
- **滚动**：viewport 自动跟随底部；用户手动上滚后暂停跟随
- **行尾清理**：渲染前清除 `\r`，防止 Windows 终端光标异常
- **粘贴**：textarea 原生支持系统粘贴（Cmd+V / Ctrl+V）
- **拖拽路径**：拖拽文件到光标处时，[`autoPrefixBarePaths`](../../../internal/cmd/chat/attachment.go) 检测裸路径（`/`、`~`、`./`、`../` 起始且文件存在）并自动加上 `@` 前缀
- **取消机制**：流式过程中按 ESC 关闭 `cancelCh`，goroutine 中 cancel context 中断 HTTP 请求；同时通过 [`Client.CancelChat`](../../../internal/cmd/chat/client.go) 向 `/chat/:sessionID` 发送 DELETE
- **鼠标点击聚焦**：左键点击输入框区域设置 `focusInInput = true`，此时 ↑↓ 移动光标；点击其他区域置 false，此时 ↑↓ 滚动 viewport
- **鼠标滚轮**：TUI 直接处理 `tea.MouseWheelMsg`，每次滚动 3 行
- **文本选择**：启用了 cell motion 鼠标，但仍可在多数终端下用 Shift/Option + 拖拽进行原生选择

### 1.8 键盘快捷键

| 按键 | 上下文 | 行为 |
|------|--------|------|
| Enter | popup 可见 | 关闭 popup |
| Enter | completion 可见 | 接受当前补全项（按模式分派） |
| Enter | 流式中 | 忽略 |
| Enter | 其他 | 发送消息 |
| Alt+Enter / Shift+Enter | 输入框 | 插入换行；textarea KeyMap.InsertNewline 绑定 `alt+enter`、`shift+enter` |
| Tab | completion 可见（ModeModel） | 把选中模型名拼成 `/model <name> ` 写回输入框，关闭浮层 |
| Tab | completion 可见（ModeAgent） | 把选中 Agent 名拼成 `/agent <name> ` 写回输入框，关闭浮层 |
| Tab | completion 可见（其他模式） | 接受幽灵文本写入输入框 |
| ESC | popup 可见 | 关闭 popup |
| ESC | completion 可见 | 关闭补全浮层 |
| ESC | 流式中 | 取消当前回答（关闭 cancelCh + DELETE 请求） |
| ESC | 正常状态 | 清空输入框 |
| Ctrl+C | 任意 | 退出 TUI |
| ↑↓ | completion 可见 | 上下选择补全项 |
| ↑↓ | 输入框聚焦 | 输入框内光标上下移动（textarea 处理） |
| ↑↓ | 其他 | 滚动 viewport 1 行 |
| PgUp | 任意 | 向上半页滚动 |
| PgDown | 任意 | 向下半页滚动 |
| 鼠标滚轮 | 任意 | 滚动 viewport 3 行 |
| 鼠标左键 | 输入框区域 | 切换 ↑↓ 行为为输入框光标移动 |

## 二、系统命令

所有系统命令以 `/` 开头，在输入框中输入后按 Enter 执行，由 [`commands.go`](../../../internal/cmd/chat/commands.go) 中的 `ParseCommand` / `ExecuteCommand` 解析与分派。

| 命令 | 参数 | 功能 |
|------|------|------|
| `/exit` | 无 | 退出 TUI |
| `/model` | `[model_name]` | 不带参数：异步加载模型列表后弹出选择浮层；带参数：直接切换；模型名无效则弹出列表 |
| `/agent` | `[agent_name]` | 不带参数：弹出 Agent 选择浮层；带参数：切换 Agent 并新建会话；空串或 `groot` 视为切回主 Agent |
| `/clear` | 无 | 重置 viewport 到欢迎画面、丢弃 sessionID、Round 归零 |
| `/help` | 无 | 弹出 popup 显示命令与快捷键表 |
| `/skills` | 无 | 异步获取 skill 列表，弹出 ModeSkill 补全浮层 |
| `/mcp` | 无 | 调用 `GET /tools`，按 MCP 服务器分组渲染到对话区 |
| `/export` | 无 | 调用 `GET /sess/<sessionID>` 获取完整会话，导出为 Markdown 到 `~/.groot/exports/chat-<id>.md` |

未识别命令显示 `未知命令: <cmd>，输入 /help 查看可用命令` 的 popup。

### 2.1 `/model` 命令交互

1. 用户输入 `/model` 回车
2. 若 `availableModels` 缓存为空：触发 `fetchModelsCmd`，pendingModelAction = `"popup"`，等待 `ModelsListMsg` 后弹出
3. 弹出时 popup 项以 `Description` 字段显示 `✓` 标记当前模型；上下键选择
4. Enter/Tab 在 ModeModel 下行为不同：
   - Enter：直接切换模型（更新 `client.modelName` 与 `status.ModelName`），输入框清空，浮层关闭
   - Tab：把选中模型名拼成 `/model <name> ` 写入输入框
5. ESC 关闭浮层
6. 用户输入 `/model gpt-99`（不存在的模型名）→ 弹出选择浮层
7. 用户输入 `/model `（带空格）触发命令补全：从缓存中过滤模型名

### 2.2 `/agent` 命令交互

1. 用户输入 `/agent` 回车
2. 若 `availableAgents` 缓存为空：触发 `fetchAgentsCmd`，pendingAgentAction = `"popup"`，等待 `AgentsListMsg` 后弹出
3. popup 列表前缀加 `✓` 标记当前 Agent；列表通常已包含 `groot` 主 Agent，代码层做去重保险
4. Enter 在 ModeAgent 下：调用 `applyAgentSwitch(name)` 切换并 `clearSession`（新会话）
5. Tab 在 ModeAgent 下：把选中 Agent 名拼成 `/agent <name> ` 写入输入框
6. 切换到主 Agent（空串或 `groot`）时，client 不再发送 `X-Agent-Name`，减小后端负担

### 2.3 `/skills` 命令交互

`/skills` 采用 Claude Code 风格的交互，用于查找并调用已安装 skill：

1. 用户输入 `/skills` 回车
2. TUI 异步请求 `GET /skills`，结果作为 `SkillsListMsg` 到达
3. 弹出 ModeSkill 补全浮层（每项 Name 是 `/<skill-name>`）
4. 用户上下键选择，Tab/Enter 接受幽灵文本，输入框变为 `/<skill-name> `（带尾部空格），浮层关闭
5. 用户继续输入指令内容，如 `/code-review 请审查 main.go`
6. 按 Enter 发送时，[`handleSendMessage`](../../../internal/cmd/chat/model.go) 检测到 skill 前缀，把消息改写为：
   `请使用 code-review skill 来处理以下指令：请审查 main.go`
7. 后端 LLM 通过 eino skill middleware 调用 `skill("code-review")` tool，加载 SKILL.md 内容并执行

skill 列表会缓存在 `m.skillsList` 字段，避免每次都请求后端。

### 2.4 `/mcp` 命令交互

输入 `/mcp` 回车后，TUI 通过 `GET /tools` 获取所有 MCP 工具，[`formatAPIResponse`](../../../internal/cmd/chat/model.go) 中的 `detectGroupedTools` + `writeToolsTree` 按 MCP 服务器分组渲染到对话区，树状结构展示：

```
🔧 cmd-exec (1 个工具)
  run_command — Execute a system command. The commandLine should be a complete
                command string with interpreter for scripts...

🔧 file-system (8 个工具)
  create_directory — Create a new directory or ensure a directory exists
  delete_file      — Delete a file or directory from the file system
  ...
```

设计细节：

- MCP 标题行：`🔧 <名称> (<n> 个工具)`，无工具时显示 `🔧 <名称> (无工具)`
- 工具条目缩进 2 空格，所有 group 内的工具名按全局最大宽度对齐
- 长描述按 80 字符宽度换行，第二行起与描述起始位置对齐
- 不同 MCP 分组之间用空行分隔
- MCP 名称按字母序排序，输出确定

### 2.5 `/export` 命令交互

1. 若当前没有 sessionID，输出系统消息：`没有活动会话可以导出，请先开始对话`
2. 否则触发 `GET /sess/<sessionID>` 拉取完整会话
3. [`ExportToMarkdown`](../../../internal/cmd/chat/commands.go) 将响应中的 `session.session_id` / `created_at` / `round_count` 与 `history.messages` 渲染成 Markdown
4. 写入 `~/.groot/exports/chat-<sessionID>.md`，对话区显示 `对话已导出到: <path>`

### 2.6 `/help` 命令

弹出 popup，显示命令与快捷键合并表（[`HelpText`](../../../internal/cmd/chat/commands.go) 常量）：

```
  命令                                  快捷键
  ───────────────────                   ───────────────────────────
  /exit         退出                    Enter           发送
  /model [name] 切换模型                Alt+Enter / Shift+Enter 换行
  /agent [name] 切换 Agent              Tab             补全
  /clear        新对话                  ESC             关闭 / 取消
  /help         帮助                    Ctrl+C          退出
  /skills       技能
  /mcp          工具
  /export       导出
```

ESC 或 Enter 关闭 popup。

## 三、附件引用（`@path`）

用户在输入框中可通过 `@/path/to/file` 引用本地文件，发送时 [`attachment.go`](../../../internal/cmd/chat/attachment.go) 自动读取文件内容作为附件提交到 `/chat` API。也支持引用目录（一层文件，不递归子目录）。

### 3.1 使用方式

**手动输入：**

```
帮我分析这个日志 @/var/log/app.log
```

**拖拽路径：** 拖拽文件到光标处，TUI 自动给裸路径前面加上 `@` 标记后再做后续处理。

**目录引用：**

```
对比这些文件 @/home/zfd/docs/
```

引用目录时读取该目录下一层所有文件，每个文件作为独立附件。

### 3.2 `@` 路径补全

输入 `@` 后自动触发文件路径补全，复用 [`CompletionModel`](../../../internal/cmd/chat/completion.go)，模式为 `ModeFile`。

**触发条件**：[`extractActiveFileRef`](../../../internal/cmd/chat/attachment.go) 提取最近一个 `@` 后的路径片段（不含空白）。

**补全行为：**

| 输入框内容 | 补全列表显示 |
|-----------|------------|
| `帮我分析 @/` | `/` 下所有名称 |
| `帮我分析 @/h` | `/` 下以 `h` 开头的名称 |
| `帮我分析 @/home/zfd/` | `/home/zfd/` 下所有名称 |
| `帮我分析 @/home/zfd/a` | `/home/zfd/` 下以 `a` 开头的名称 |

- 列表中目录名带尾部 `/`，文件名不带；按前缀匹配过滤
- 继续输入 `/` 可再次触发补全进入下一层目录
- ModeFile 下，幽灵文本是 `<filePrefix>@<选中路径>` 整行替换
- 仅 **Tab** 键确认补全（Enter 用于发送消息）

### 3.3 发送时处理流程

```
用户输入: 帮我分析 @/var/log/app.log

1. ExtractFileRefs(text) 扫描 @path 与裸路径（裸路径需 os.Stat 存在）
   → ["/var/log/app.log"]

2. ReadAttachments(refs) 处理每个路径
   - 文件 → 读取 → Base64 编码 → 加入 attachments
   - 目录 → os.ReadDir 一层文件，逐个独立编码（跳过子目录）
   - 不存在 / 无权限 / 目录为空 → 返回错误，弹出 error 块阻止发送

3. StripFileRefs(text, pathToNames) 把 @path 与裸路径替换为文件名
   - 文件 → "帮我分析 app.log"
   - 目录 → "帮我分析 app.log error.log access.log"

4. POST /chat:
   {
     "instruction": "帮我分析 app.log",
     "attachments": [
       {"type": "file", "name": "app.log", "content": "<base64>"}
     ]
   }
```

### 3.4 文件类型判断

[`guessFileType`](../../../internal/cmd/chat/attachment.go) 根据扩展名设置 `attachment.type`：

| 类型 | 扩展名 |
|------|--------|
| `image` | png, jpg, jpeg, gif, bmp, webp, svg |
| `audio` | mp3, wav, aac, ogg, flac |
| `video` | mp4, avi, mov, mkv, webm |
| `file` | 其他所有 |

### 3.5 错误处理

TUI 仅校验客户端能直接判断的错误：

| 场景 | 行为 |
|------|------|
| 路径不存在 | 输出 error 块，阻止发送 |
| 无读取权限 | 输出 error 块，阻止发送 |
| 目录为空 | 输出 error 块，阻止发送 |

文件大小、附件数量等限制由 `/chat` API 端点统一校验，TUI 透传 API 错误信息。

## 四、补全机制

补全采用**浮层 + 幽灵文本**双提示，由 [`CompletionModel`](../../../internal/cmd/chat/completion.go) 管理。

### 4.1 模式

```go
ModeCommand // 命令/子命令补全（填入输入框）
ModeModel   // 模型选择
ModeSkill   // 技能选择（填入输入框，前缀化为 /skill-name ）
ModeFile    // 文件路径补全（@path）
ModeAgent   // Agent 选择
```

### 4.2 触发与确认

| 输入框状态 | 弹出列表 | 模式 |
|-----------|---------|------|
| 包含 `@<path>` 片段 | 当前路径下子项 | ModeFile |
| 以 `/model ` 开头（带空格） | 缓存的模型列表 | ModeModel |
| 以 `/` 开头（其他） | `SystemCommands` 列表 | ModeCommand |
| 不以 `/` 开头 | 关闭 | — |

| Enter 处理（按 Mode） | 行为 |
|----------------------|------|
| ModeModel | 直接切换模型，输入框清空 |
| ModeAgent | 直接切换 Agent 并 `/clear`，输入框清空 |
| ModeCommand / ModeSkill / ModeFile | 接受幽灵文本写入输入框 |

| Tab 处理（按 Mode） | 行为 |
|--------------------|------|
| ModeModel | 写入 `/model <选中> ` 到输入框 |
| ModeAgent | 写入 `/agent <选中> ` 到输入框 |
| 其他 | 同 Enter 接受幽灵文本 |

ESC 任何模式下都关闭浮层并清除幽灵文本。

### 4.3 幽灵文本

- 灰色 `#666666`，出现在 textarea 下一行（输入框边框内）。开启幽灵文本时输入框 textarea 高度 -1 让出底行
- ModeFile 下幽灵文本是 `filePrefix + @ + 选中完整路径`，整行替换式补全
- 其他模式下幽灵文本是 `选中名称 + 空格`，与已输入前缀的尾部重叠部分自动剔除（大小写不敏感），避免 `/session llist` 这类重复

### 4.4 浮层样式

[`CompletionModel.View`](../../../internal/cmd/chat/completion.go)：

- 圆角边框，颜色 `#444444`
- 宽度 = `m.width - 2`
- 最多显示 8 项（`maxItems = 8`），超出滚动；以选中项为中心居中显示窗口
- 名称列等宽对齐，描述列按可用宽度截断加 `...`
- 当前选中项使用反色（`#444444` 背景 + 白色前景）

## 五、输入框

[`InputModel`](../../../internal/cmd/chat/input.go) 包装 `bubbles/textarea`：

- 双线边框（`lipgloss.DoubleBorder`），绿色 `#98c379`
- placeholder：`输入消息，或 / 开头使用命令...`
- 高度：默认 3 行，最大 10 行；启用幽灵文本时内部高度 -1 让出底行，整体边框高度不变
- `KeyMap.InsertNewline` 绑定 `alt+enter` 和 `shift+enter`；textarea 关闭虚拟光标（`SetVirtualCursor(false)`）以使用 model 层暴露的 `tea.View.Cursor` 支持 IME 输入法
- `CharLimit = 0`（无字数限制），`ShowLineNumbers = false`

## 六、组件树

```
Model（顶层 Bubble Tea Model，定义于 internal/cmd/chat/model.go）
├── ViewportModel       — 可滚动对话显示区（占主要区域，无边框）
├── PopupModel          — 通用浮层（用于 /help 和未知命令提示，圆角边框）
├── CompletionModel     — 命令/模型/技能/Agent/文件补全浮层（叠加在 viewport 底部，裁剪 viewport 让位）
├── InputModel          — 绿色双线边框多行输入框
├── StatusBar           — 状态栏（底部固定一行，无边框，模型名 / Agent / SessionID / Round）
└── Client              — HTTP+SSE 客户端
```

## 七、数据流

```
用户输入 → InputModel
  │
  ├─ 匹配已知 skill 前缀 (/<skill-name> 指令) → 改写为 skill 调用指令 → 发送
  │
  ├─ 以 / 开头 → ParseCommand → ExecuteCommand → handleCommand
  │   ├─ /exit         → tea.Quit
  │   ├─ /model        → 缓存命中直接切换/弹 popup；否则触发 fetchModelsCmd
  │   ├─ /agent        → 缓存命中直接切换/弹 popup；否则触发 fetchAgentsCmd
  │   ├─ /clear        → clearSession()：viewport 清空回欢迎画面、SessionID 清空、Round=0
  │   ├─ /help         → popup 显示 HelpText
  │   ├─ /skills       → fetchSkillsCmd → SkillsListMsg → 弹 ModeSkill 浮层
  │   ├─ /mcp          → doFetchAPI("/tools") → 树状渲染
  │   └─ /export       → doFetchAPI("/sess/<sid>") → ExportToMarkdown → 写文件
  │
  └─ 普通文本 → handleSendMessage
        │
        ├─ ExtractFileRefs(text)
        │     ├─ 有引用 → ReadAttachments → 文件/目录 → Base64 编码到 attachments；错误时输出 error 块阻止发送
        │     └─ 无引用 → attachments = []
        │
        ├─ StripFileRefs(text) 把 @path/裸路径换成文件名
        │
        ├─ AddMessage(user) + 追加 loading 占位
        ├─ Client.SendChatStream(text, atts, eventsCh, cancelCh)
        │     ├─ POST /chat（HTTP+SSE）
        │     ├─ 请求头：Content-Type、X-Session-ID、X-Model-Name、X-Agent-Name（仅子 Agent 时携带）
        │     └─ 请求体：{ instruction, attachments }
        ├─ Round +1
        │
        └─ tea.Batch(waitForEvents, spinner.Tick)
              │
              └─ SSE 事件循环（waitForEvents）
                  ├─ SessionIDMsg   → 存储 sessionID，更新状态栏
                  ├─ SseEventMsg    → handleSseEvent → 按 classifyEvent 路由
                  │                    （thinking / tool_calls / tool_result(忽略) / tool_error
                  │                     / message / finish_reason / error）
                  ├─ waitMsg        → 50ms 超时心跳 → 继续 waitForEvents
                  ├─ spinner.TickMsg→ 更新 spinner 帧并触发 viewport rerender
                  ├─ StreamDoneMsg  → streaming=false，清 loading
                  └─ StreamErrorMsg → streaming=false，输出 error 块
```

## 八、文件结构

```
internal/cmd/
├── chat.go              # 子命令入口: RunChat / startEmbedServer / waitForHealth
├── chat/
│   ├── model.go         # 顶层 Bubble Tea Model：Init/Update/View、事件循环、命令分派、API fetch
│   ├── statusbar.go     # 状态栏组件（模型 / Agent / SessionID / Round）
│   ├── viewport.go      # 对话显示区 + glamour Markdown 渲染、tool_call 聚合、长行处理
│   ├── input.go         # 输入框（textarea + 幽灵文本 + 双线边框）
│   ├── completion.go    # 补全浮层（5 种模式、过滤、ghost text）
│   ├── popup.go         # 通用 popup（/help、未知命令）
│   ├── commands.go      # 命令解析与分派、HelpText、ExportToMarkdown
│   ├── client.go        # HTTP+SSE 客户端：流式请求、取消、FetchJSON
│   ├── attachment.go    # @path 解析、文件读取编码、路径补全、裸路径 @ 自动前缀
│   ├── styles.go        # Lipgloss 样式与预渲染标签
│   ├── messages.go      # SseEvent 类型与 Bubble Tea Msg 类型
│   └── welcome.go       # 欢迎画面 ASCII art
```

## 九、依赖

```
charm.land/bubbletea/v2                 # TUI 框架
charm.land/bubbles/v2                   # textarea / viewport / spinner / key 组件
charm.land/lipgloss/v2                  # 终端样式
github.com/charmbracelet/glamour        # Markdown 终端渲染
```

## 十、SSE 事件循环机制

TUI 使用 Bubble Tea 的 Cmd 机制驱动 SSE 事件循环：

```
handleSendMessage()
  → 创建 eventsCh channel (buffered 100) 与 cancelCh
  → goroutine: Client.SendChatStream() 读取 HTTP SSE 流，解析后写入 eventsCh
  → 返回 tea.Batch(waitForEvents, spinner.Tick)

waitForEvents() Cmd:
  → select { eventsCh 有事件 → 返回事件; 50ms 超时 → 返回 waitMsg }

Update() 处理:
  → SseEventMsg     → handleSseEvent → 返回 waitForEvents (继续轮询)
  → waitMsg         → 返回 waitForEvents (继续轮询)
  → SessionIDMsg    → 存储 session ID → 返回 waitForEvents
  → StreamDoneMsg   → streaming=false → 返回 nil (停止轮询)
  → StreamErrorMsg  → 显示错误 → 返回 nil (停止轮询)
  → spinner.TickMsg → 更新 spinner 帧；streaming 时触发 rerender 并继续 spinner.Tick
```

关键设计：每个非终止事件处理后都返回 `waitForEvents()` 作为后续 Cmd，保持事件循环持续运行。只有 `StreamDoneMsg` 和 `StreamErrorMsg` 返回 nil 终止循环。

## 十一、嵌入服务日志处理

[`startEmbedServer`](../../../internal/cmd/chat.go) 启动嵌入服务时复用现有日志基础设施：

- **文件日志**：正常写入 `~/.groot/logs/groot-{date}.log`，无需额外处理
- **stdout 输出**：嵌入模式下把 `cfg.Logging.Output` 改成 `["file"]`，去掉 `stdout`，避免污染 TUI 画面
- **Hertz 日志**：`hlog.SetOutput(io.Discard)` 禁止 Hertz 内部日志输出 stderr
- **stdout sender**：嵌入模式下不注册 stdout 消息发送器，因为 `fmt.Printf` 会破坏终端渲染

TUI 退出后，如需排查历史日志，使用 `groot tail` 查看。

## 十二、HTTP 客户端设计

[`Client`](../../../internal/cmd/chat/client.go) 使用自定义 `http.Transport`，只设置 dial 和 TLS 超时（10 秒），不设置总超时，确保 SSE 长连接不会被意外断开。

请求头：

- `Content-Type: application/json`
- `X-Session-ID`：仅在已有 sessionID 时设置；首次请求不带，由响应头 `X-Session-ID` 回填
- `X-Model-Name`：当前模型名
- `X-Agent-Name`：仅子 Agent 模式（非空且非 `groot`）时设置；主 Agent 不发送

取消机制：

- 用户按 ESC → 关闭 `cancelCh` → 监听 goroutine cancel context → HTTP 请求中断
- 同时通过 `Client.CancelChat` 发送 `DELETE /chat/:sessionID` 通知服务端停止生成
- channel 关闭后 `waitForEvents` 读到关闭信号，返回 `StreamDoneMsg`

与其他子命令关系：`groot chat` 是独立子命令，不影响现有 `init` / `status` / `skills` / `mcp` / `schedule` / `tail` / `pull` / `push` / `diff` 子命令。
