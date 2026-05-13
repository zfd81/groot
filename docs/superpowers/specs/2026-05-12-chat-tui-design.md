# groot chat TUI 设计

## 概述

新增 `groot chat` 子命令，打开类 Claude Code 的 TUI 聊天界面。TUI 作为 HTTP+SSE 客户端连接 groot API，复用现有 agent 引擎的全部能力（tool use、MCP、memory）。

TUI 使用独占全屏模式（Alternate Screen），隐藏进入前的终端历史。退出后恢复原终端画面。不启用鼠标捕获，保留终端原生文本选择和复制能力。内容滚动依赖 PageUp/PageDown 键和鼠标滚轮（鼠标滚轮依赖终端自身将滚轮事件转换为 Up/Down 按键序列，大多数现代终端如 iTerm2、Kitty、Alacritty、WezTerm 自动支持。macOS Terminal.app 需在偏好设置中开启"Scroll alternate screen"选项）。↑↓ 键用于输入框内光标上下移动。

## 架构

```
┌─ groot chat TUI ─────────────────────────────────────┐
│                                                       │
│  ┌─ 对话显示区 (viewport) ──────────────────────────┐ │
│  │                                                  │ │
│  │   ██████╗ ██████╗  ██████╗  ██████╗ ████████╗   │ │
│  │  ██╔════╝ ██╔══██╗██╔═══██╗██╔═══██╗╚══██╔══╝   │ │
│  │  ██║  ███╗██████╔╝██║   ██║██║   ██║   ██║       │ │
│  │  ██║   ██║██╔══██╗██║   ██║██║   ██║   ██║       │ │
│  │  ╚██████╔╝██║  ██║╚██████╔╝╚██████╔╝   ██║       │ │
│  │   ╚═════╝ ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝       │ │
│  │                                                  │ │
│  │        Groot AI Agent · v1.0.0                  │ │
│  │   ─────────────────────────────                  │ │
│  │   输入你的问题开始对话                           │ │
│  │   输入 /help 查看系统命令                        │ │
│  │                                                  │ │
│  └──────────────────────────────────────────────────┘ │
│                                                       │
│  ┌─ 补全浮层 (条件显示) ──────────────────────────┐ │
│  │  /exit      退出聊天                               │ │
│  │  /model     切换模型                               │ │
│  │  /clear     清空对话                               │ │
│  │  /help      显示帮助                               │ │
│  └──────────────────────────────────────────────────┘ │
│                                                       │
│  ╔════════════════════════════════════════════════════╗
│  ║                                                    ║
│  ╚════════════════════════════════════════════════════╝
│  ┌─ 状态栏 ─────────────────────────────────────────┐│
│  │ 模型: gpt-4o  │  会话: sess-abc123  │  对话: 第 3 轮 ││
│  └───────────────────────────────────────────────────┘│
└────────────────────────────────────────────────────────┘
```

## 启动流程

`groot chat` 启动时执行以下流程，保证无论服务是否已在运行，用户都可以使用 TUI：

### 步骤 1：读取配置

从 `~/.groot/config.yaml` 读取 `server.port`（默认 8080）。如果配置文件不存在，打印错误提示并退出：

```
错误: 配置文件不存在，请先执行 groot init 初始化配置。
```

### 步骤 2：检测服务

向 `http://localhost:<port>/health` 发送 GET 请求，超时 2 秒。

### 步骤 3A：服务已运行（健康检查成功）

```
检测到已有服务运行 (端口 8080)
  │
  ├─ 不启动嵌入服务
  ├─ 打开 TUI，HTTP 客户端指向已有服务
  ├─ 建立 SSE 长连接 → 流式渲染对话
  │
  └─ TUI 退出时
       ├─ 只关闭 TUI 自身
       ├─ 已有服务 不受影响，继续运行
       └─ 不发送任何关闭信号给已有服务
```

### 步骤 3B：服务未运行（健康检查失败）

```
未检测到运行中的服务
  │
  ├─ 后台 goroutine 启动嵌入 HTTP 服务
  │   ├─ 调用 internal/api.Server.Start()
  │   ├─ 使用 ~/.groot/config.yaml 完整配置
  │   ├─ 初始化所有组件（skills、MCP、memory、scheduler 等）
  │   └─ 监听 localhost:<port>
  │
  ├─ 轮询 GET /health（间隔 200ms，超时 10 秒）
  │   ├─ 200 → 服务就绪，继续
  │   └─ 超时 → 打印错误，退出
  │
  ├─ 打开 TUI
  │
  └─ TUI 退出时
       ├─ 关闭 TUI 自身
       ├─ defer srv.Stop() 优雅关闭嵌入服务
       └─ 输出: 嵌入服务已关闭
```

### 步骤 4：首次会话初始化

TUI 启动时没有会话 ID，状态栏显示 `模型: <默认模型> | 会话: 新会话 | 对话: 第 0 轮`。首次发送消息时不带 `X-Session-ID`，API 会自动创建新会话并通过响应头 `X-Session-ID` 返回。TUI 从首个 SSE 响应中捕获该 Header，更新状态栏（显示完整会话 ID），后续请求均带上。

`/clear` 清除会话：丢弃当前会话 ID，状态栏恢复为 `会话: 新会话 | 对话: 第 0 轮`，下一条消息重新触发上述流程。

### 步骤 5：显示欢迎画面

进入 TUI 后，对话区显示欢迎画面：

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

欢迎画面在首次发送消息后自动滚出视野。欢迎画面内容定义在 `internal/cmd/chat/welcome.go` 中，方便后续修改。

### 步骤 6：进入 TUI 主循环

启动 Bubble Tea 主循环，进入聊天界面。

### 流程图

```
groot chat 启动
  │
  ├─ 读取 ~/.groot/config.yaml
  │   └─ 不存在 → 报错退出
  │
  ├─ GET /health (2s 超时)
  │   │
  │   ├─ 200 OK ──→ 服务已运行 ──→ 直接打开 TUI
  │   │                              │
  │   │                              └─ /exit → 关闭 TUI，服务继续运行
  │   │
  │   └─ 失败 ──→ 服务未运行
  │                │
  │                ├─ 后台启动嵌入服务
  │                │   └─ 轮询 /health (10s 超时)
  │                │       └─ 超时 → 报错退出
  │                │
  │                ├─ 打开 TUI
  │                │
  │                └─ /exit → 关闭 TUI + 关闭嵌入服务
```

### 嵌入服务 vs 独立服务区别

| 行为 | 已有服务 | 嵌入服务 |
|------|---------|---------|
| 启动方式 | 用户手动 `groot` 启动 | TUI 自动启动 |
| 生命周期 | 独立于 TUI | 跟随 TUI |
| TUI /exit | 服务继续运行 | 服务随 TUI 退出 |
| Ctrl+C | 服务继续运行 | TUI 退出，defer 关闭服务 |
| 日志输出 | 正常输出 | 静默（不污染 TUI） |
| 配置来源 | 同一份 config.yaml | 同一份 config.yaml |

## 对话显示区渲染规则

每个 SSE 事件对应一个阶段标签，在当前轮对话中实时切换：

| SSE 事件 | UI 标签 | 颜色 | 说明 |
|----------|---------|------|------|
| `thinking` | 🤔 Thinking... | 灰色斜体 | 推理内容流式追加 |
| `tool_calls` (普通工具) | 🔧 调用工具: `<name>` | 黄色 | 工具名 + 参数摘要（key: value 格式） |
| `tool_calls` (skill) | ⚡ 调用技能: `<skill_name>` | 紫色 | 当 function.name == "skill" 时识别为技能调用 |
| `tool_result` | (不展示) | — | 工具结果不在 UI 中显示，由 LLM 回答体现 |
| `message` | (无标签) | Markdown 渲染 | 最终回答流式追加，使用 glamour 渲染为终端富文本 |
| `finish_reason: stop` | (无标签) | — | 正常结束，静默不显示 |
| `finish_reason: 用户取消` | ⏹️ 已取消 | 灰色 | 用户主动取消 |
| `finish_reason: length` | ⚠️ 已达 token 上限 | 黄色 | 达到最大 token 数 |
| `error` | ❌ 错误 | 红色 | 错误信息 |

### 加载状态指示

用户发送消息后，在收到第一个 SSE 内容事件之前，显示 loading 状态：

- 使用 `bubbles/spinner`（Dot 样式，绿色 `#98c379`）
- 显示 spinner 动画 + "正在思考..." 文字
- 收到第一个内容事件（thinking/message/tool_call）后自动消失
- 错误或取消时也会正确清除 loading 状态

### Markdown 渲染

assistant 消息使用 `charmbracelet/glamour` 库进行终端 Markdown 渲染：

- 使用固定主题（`glamour.WithStylePath("pink")`），避免 `WithAutoStyle()` 发送终端查询序列导致输入框收到转义字符。pink 主题会将 `###` 标题语法标记隐藏，替换为 `┃ ` 装饰前缀
- 按 viewport 宽度自动换行（`glamour.WithWordWrap(width-4)`）
- 启用 `WithPreservedNewLines()` 保留 LLM 输出中的原始换行
- 渲染前进行 Markdown 标准化：将 LLM 常用的 Unicode 项目符号（`•`、`◦`、`▪` 等）转换为标准 `- ` 前缀，确保 goldmark 正确识别为列表
- 渲染前进行长行断行处理（`breakLongLines`）：对 CJK 等无空格文本按宽度硬断行，补充 wordwrap 库的不足
- 支持标题、代码块（语法高亮）、列表、粗体/斜体等 Markdown 元素
- 窗口宽度变化时重新创建 renderer 以适配新宽度（仅宽度变化时触发，避免频繁重建）

### 交互细节

- **用户消息样式**：`> ` 前缀 + 内容，整行使用浅色背景（`#2c313a`），背景宽度为屏幕宽度减去两侧各 1 字符边距
- **内容自动换行**：所有文本内容（assistant、thinking 等）按 viewport 宽度自动换行，不会被截断
- **滚动行为**：流式输出时自动跟随底部；用户手动上滚后暂停跟随，不会被强制拉回底部
- **文本选择**：不启用鼠标捕获，终端原生鼠标选择和复制正常工作
- **粘贴支持**：输入框支持系统粘贴（Cmd+V / Ctrl+V），由 textarea 组件原生处理
- **工具调用显示**：工具参数以 `key: value` 格式逐行显示，非原始 JSON；超长值截断为 80 字符
- **工具结果隐藏**：工具执行结果不在对话区显示，由 LLM 最终回答中体现
- **对话分离**：每轮对话用空行分隔
- **轮数计算**：状态栏轮数本地计数，每发送一条消息 +1
- **取消机制**：ESC 取消流式响应时，除关闭本地 context 外，还会向 `/chat/:sessionID` 发送 DELETE 请求通知服务端停止生成

### 键盘快捷键

| 按键 | 上下文 | 行为 |
|------|--------|------|
| Enter | 输入框 | 发送消息。Enter 事件先转发给 textarea，若触发 InsertNewline（Alt+Enter/Shift+Enter）则值变化，不发送 |
| Alt+Enter / Ctrl+N | 输入框 | 插入换行。Alt+Enter 由 model 层直接检测 msg.Alt 插入换行；Ctrl+N 由 textarea InsertNewline 绑定触发。Shift+Enter 因 Bubble Tea 不支持 Shift 修饰键而移除 |
| Tab | 补全浮层可见 | 接受幽灵文本，关闭浮层 |
| ESC | 补全浮层可见 | 关闭补全浮层 |
| ESC | AI 回答中 | 取消当前回答（发送 DELETE 请求） |
| ESC | 正常状态 | 清空输入框 |
| Ctrl+C | 任意时刻 | 退出 TUI |
| ↑↓ | 补全浮层可见 | 上下选择补全项 |
| ↑↓ | 焦点在输入框内 | 输入框内光标上下移动 |
| ↑↓ | 焦点不在输入框内 | 逐行滚动对话显示区 |
| PgUp | 任意时刻 | 向上半屏滚动对话区 |
| PgDown | 任意时刻 | 向下半屏滚动对话区 |
| 鼠标滚轮 | 任意时刻 | 逐行滚动（依赖终端将滚轮转为 Up/Down 键） |

## 系统命令

所有系统命令以 `/` 开头，在输入框中输入后按 Enter 执行。

| 命令 | 参数 | 功能 |
|------|------|------|
| `/exit` | 无 | 退出 TUI |
| `/model` | `[model_name]` | 不带参数：弹出模型选择列表；带参数：切换指定模型，若模型名无效则弹出列表 |
| `/clear` | 无 | 清空屏幕对话，生成新会话 ID，开始全新对话 |
| `/help` | 无 | 显示所有命令帮助（渲染到对话区） |
| `/skills` | 无 | 弹出 skill 选择列表（Claude Code 风格），选中后可输入指令 |
| `/mcp` | 无 | 按 MCP 服务器分组列出所有可用工具，树状结构展示 |
| `/export` | 无 | 导出当前完整对话为 Markdown（通过 API 获取完整会话记录） |

### `/model` 命令详细交互

1. 用户输入 `/model` 回车 → 弹出模型列表浮层，显示所有已配置模型
2. 当前使用的模型有 `✓` 标记
3. 上下键移动选择，Enter/Tab 确认 → **直接切换模型**，状态栏立即更新模型名，输入框不填入内容
4. ESC 取消关闭浮层
5. 用户输入 `/model gpt-99`（不存在的模型名）→ 弹出模型列表供选择
6. 用户输入 `/model `（带空格）触发补全，直接过滤模型名，选择后也是直接切换

注意：模型选择确认后直接生效，不会将模型名填入输入框。后续发送消息自动使用新模型。

### `/skills` 命令详细交互

`/skills` 采用 Claude Code 风格的交互，支持 skill 选择和调用：

1. 用户输入 `/skills` 回车
2. TUI 异步请求 `GET /skills` 获取已安装 skill 列表
3. 弹出补全浮层，显示所有 skill（名称左对齐 + 描述自动截断）
4. 用户上下键选择，Tab/Enter 接受
5. 输入框变为 `/skillName `（带尾部空格，用户可直接输入指令），浮层关闭
6. 用户继续输入指令内容，如 `/code-review 请审查 main.go`
7. 按 Enter 发送时，TUI 检测到 skill 前缀，自动将消息改写为：
   `请使用 code-review skill 来处理以下指令：请审查 main.go`
8. 后端 LLM 收到指令后，通过 eino skill middleware 调用 `skill("code-review")` tool，加载完整 SKILL.md 内容并执行

注意：skill 列表会缓存在 TUI 本地（`skillsList` 字段），避免每次都请求后端。

### `/mcp` 命令详细交互

输入 `/mcp` 回车后，TUI 通过 `GET /tools` 获取所有 MCP 工具，按 MCP 服务器分组渲染到对话区，树状结构展示：

```
🔧 cmd-exec (1 个工具)
  run_command — Execute a system command. The commandLine should be a complete
                command string with interpreter for scripts...

📁 file-system (8 个工具)
  create_directory — Create a new directory or ensure a directory exists
  delete_file — Delete a file or directory from the file system
  get_file_info — Get detailed information about a file or directory
  list_directory — List all files and directories in a specified directory
  move_file — Move or rename a file from one location to another
  search_files — Search for files matching a glob pattern in a directory
  read_file — Read the complete contents of a file from the file system
  write_file — Write content to a file, creating or overwriting as necessary
```

设计细节：
- MCP 标题行加粗，带 `🔧` 图标 + MCP 名称 + 工具数量
- 每个工具条目缩进 2 空格，格式为 `工具名` — `描述`
- 长描述自动换行，第二行起与工具名对齐（缩进到描述起始位置）
- 不同 MCP 分组之间用空行分隔
- 如果某个 MCP 没有工具（total=0），灰色显示 `(无工具)`
- 处理逻辑位于 `formatAPIResponse()` 函数中，识别 API 返回的 `map[MCP名称 → ToolsGroup]` 分组格式

## 补全机制

补全采用**浮层 + 幽灵文本**双提示：

### 交互流程

1. 用户输入 `/m`
2. 输入框上方弹出补全浮层，显示匹配项（`/model`、`/mcp`）
3. 同时输入框内光标后出现**灰色幽灵文本**，显示第一匹配项的剩余部分（`odel`）
4. 按 Tab/Enter → 接受幽灵文本，补全为 `/model`，关闭浮层
5. 按 ESC → 同时关闭浮层和幽灵文本

### 触发规则

- 输入 `/` → 弹出所有系统命令列表 + 幽灵文本显示第一匹配项
- 输入 `/model `（命令名 + 空格）→ 弹出模型名称列表 + 幽灵文本
- 输入 `/skills ` 或 `/mcp ` → 不触发补全（无子命令）
- 继续输入字母 → 实时过滤浮层 + 更新幽灵文本
- 按 Tab/Enter → 接受补全（模型选择直接切换模型并更新状态栏；其他填入输入框）
- 按 ESC → 关闭浮层和幽灵文本

### 幽灵文本重叠处理

当用户输入与补全项末尾有重叠时（如输入 `/session l`，补全项为 `list`），幽灵文本自动计算重叠并只补未输入部分（`ist `），避免接受后出现 `/session llist`。

### 补全确认行为

根据补全模式，Enter/Tab 确认时执行不同操作：

| 模式 | 行为 |
|------|------|
| 模型选择 | 直接切换模型，更新状态栏，输入框不填入内容 |
| 技能选择 | 技能名填入输入框（如 `/code-review `），用户可继续输入 |
| 命令/子命令 | 接受幽灵文本填入输入框 |

### 浮层样式

- 位于输入框正上方，宽度与输入框一致
- 最多显示 8 项，超出部分可滚动
- 当前选中项高亮（反转色）
- 每项显示命令名（左对齐）+ 简短描述（灰色，超出宽度自动截断加 `...`）
- 名称列等宽对齐，描述列根据可用空间自动截断

```
┌─ 补全浮层 ──────────────────────────────────────┐
│  /exit      退出聊天            ← 高亮选中项      │
│  /model     切换模型                              │
│  /clear     清空对话                              │
│  /help      显示帮助                              │
│  /skills    查看已安装 skill                       │
│  /mcp       查看可用工具                        │
│  /export    导出对话                              │
└──────────────────────────────────────────────────┘
```

### 幽灵文本样式

- 出现在输入框光标后，颜色为暗灰色（Lipgloss `color("#666666")`）
- 内容是当前浮层高亮项的名称 + 尾部空格（接受后光标在空格后，用户可直接输入参数）
- 按 Tab/Enter 接受补全

```
输入框: ║ /mod▒odel          ║  ← "odel" 为灰色幽灵文本
        ╚══════════════════════╝
```

## 输入框样式

输入框采用**双线边框**设计，位于屏幕底部状态栏上方：

```
╔════════════════════════════════════════════════════╗
║                                                    ║
╚════════════════════════════════════════════════════╝
```

- 双线边框颜色：与主题色一致（默认 `#98c379` 绿色）
- 内部区域：多行文本输入，支持 Alt+Enter 换行（兼容 Shift+Enter）
- 高度：最小 1 行，最大 5 行（内容超出时自动扩展）
- 幽灵文本：在光标后显示灰色补全建议

## 组件树

```
App (Bubble Tea Model)
├── Viewport           — 可滚动对话显示区（占主要区域）
├── CompletionPopup    — 命令/模型补全浮层（覆盖在输入框上方）
├── InputArea          — 双线边框多行文本输入框
└── StatusBar          — 状态栏（底部固定，显示当前模型名、会话ID、轮数）
```

## 数据流

```
用户输入 → InputArea
  │
  ├─ 匹配已知 skill 前缀 (/skillName 指令) → 改写为 skill 调用指令 → 发送
  │
  ├─ 以 / 开头 → 命令路由
  │   ├─ /exit       → 发送退出信号 (tea.Quit)
  │   ├─ /model      → 弹出选择列表 或 直接切换
  │   ├─ /clear      → 重置本地对话，清空屏幕，开始新会话
  │   ├─ /help       → 渲染帮助文本到 Viewport
  │   ├─ /skills     → GET /skills 获取列表 → 弹出 skill 选择浮层
  │   ├─ /mcp        → GET /tools 列出所有可用工具
  │   └─ /export     → GET /sess/:sid 获取完整会话历史, 写入 Markdown 到 ~/.groot/exports/
  │
  └─ 普通文本 → 构造请求体
                  │
                  ├─ 显示 loading spinner + "正在思考..."
                  ├─ POST /chat (HTTP+SSE)
                  ├─ 请求头: X-Session-ID, X-Model-Name
                  ├─ 请求体: { "instruction": "...", "attachments": [] }
                  │
                  └─ SSE 流解析（事件循环持续轮询 channel）
                      ├─ SessionIDMsg  → 存储会话 ID，更新状态栏
                      ├─ thinking      → 移除 loading，添加/更新 Thinking 块
                      ├─ tool_calls    → 添加工具调用块
                      ├─ tool_result   → 添加工具结果块
                      ├─ message       → 追加消息内容（glamour 渲染）
                      ├─ finish_reason → 标记本轮完成
                      ├─ error         → 显示错误块
                      └─ StreamDone    → 结束事件循环
```

## 文件结构

```
internal/cmd/
├── chat.go              # 子命令入口: RunChat, 嵌入服务启动
├── chat/
│   ├── model.go         # Bubble Tea 顶层 Model (Init/Update/View), 事件循环
│   ├── statusbar.go     # 状态栏组件
│   ├── viewport.go      # 对话显示区 + glamour Markdown 渲染
│   ├── input.go         # 输入框组件 (textarea + ghost text)
│   ├── completion.go    # 补全浮层 (过滤、选择、ghost text)
│   ├── commands.go      # 系统命令定义 + 处理函数 + 导出功能
│   ├── client.go        # HTTP+SSE 客户端 (流式请求 + 取消)
│   ├── styles.go        # Lipgloss 样式定义
│   ├── messages.go      # Bubble Tea 消息类型定义
│   └── welcome.go       # 欢迎画面 ASCII art
```

## 新增依赖

```
github.com/charmbracelet/bubbletea       # TUI 框架
github.com/charmbracelet/bubbles         # textarea, viewport, spinner 组件
github.com/charmbracelet/lipgloss        # 终端样式
github.com/charmbracelet/glamour         # Markdown 终端渲染
```

## SSE 事件循环机制

TUI 使用 Bubble Tea 的 Cmd 机制驱动 SSE 事件循环：

```
handleSendMessage()
  → 创建 eventsCh channel (buffered 100)
  → goroutine: client.SendChatStream() 读取 HTTP SSE 流，解析后写入 channel
  → 返回 tea.Batch(waitForEvents(), spinner.Tick)

waitForEvents() Cmd:
  → select { channel 有事件 → 返回事件; 50ms 超时 → 返回 waitMsg }

Update() 处理:
  → SseEventMsg   → handleSseEvent() → 返回 waitForEvents() (继续轮询)
  → waitMsg       → 返回 waitForEvents() (继续轮询)
  → SessionIDMsg  → 存储 session ID → 返回 waitForEvents() (继续轮询)
  → StreamDoneMsg → streaming=false → 返回 nil (停止轮询)
  → StreamErrorMsg → 显示错误 → 返回 nil (停止轮询)
  → spinner.TickMsg → 更新 spinner 帧 → 返回 spinner.Tick (loading 时) 或 nil
```

关键设计：每个非终止事件处理后必须返回 `waitForEvents()` 作为后续 Cmd，保持事件循环持续运行。只有 `StreamDoneMsg` 和 `StreamErrorMsg` 返回 nil 终止循环。

## 嵌入服务日志处理

嵌入服务启动时，复用现有 Logger 基础设施：

- **文件日志**：正常写入 `~/.groot/logs/groot-{date}.log`，无需额外处理
- **stdout 输出**：嵌入模式下需抑制 stdout 输出，避免污染 TUI 画面。具体做法：嵌入启动时将日志 Output 配置改为只保留 `["file"]`，去掉 `"stdout"`

TUI 退出后，如需排查历史日志，使用 `groot tail` 查看。

## HTTP 客户端设计

`Client` 使用自定义 `http.Transport`，只设置 dial 和 TLS 超时（10s），不设置总超时，确保 SSE 长连接不会被意外断开。

取消机制：
- 用户按 ESC → 关闭 `cancelCh` → goroutine 中 cancel context → HTTP 请求中断
- 同时发送 DELETE `/chat/:sessionID` 通知服务端停止生成
- channel 关闭后 `waitForEvents()` 读到关闭信号，返回 `StreamDoneMsg`

与其他子命令关系：`groot chat` 新增子命令，不影响现有 `init` / `status` / `skills` / `mcp` / `schedule` / `tail` 子命令。
