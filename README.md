<img src="groot.jpg" alt="Groot Logo" width="180" align="left">

<br><br>

<h1 style="border-bottom: none;">Groot AI Agent</h1>

**面向业务系统的 AI Agent 服务**

通过 REST API 接入，让你的系统立刻拥有智能任务执行能力  
理解指令 · 调用工具 · 自主完成任务

<img alt="Version" src="https://img.shields.io/badge/version-1.0.0-blue">
<img alt="License" src="https://img.shields.io/badge/license-MIT-green">
<img alt="Go" src="https://img.shields.io/badge/Go-1.21+-00ADD8">

<br clear="left">

## 一、产品介绍

### 1.1 什么是 Groot

Groot 是面向业务系统的 AI Agent 服务。通过 REST API 接入，让你的系统立刻拥有智能任务执行能力——理解指令、调用工具、自主完成任务。

**一句话概括：** 把 AI Agent 能力嵌入你的业务系统，像调用普通 API 一样使用智能执行能力。

### 1.2 核心特性

| 特性 | 说明 |
|------|------|
| **多轮对话** | 支持会话（Session）概念，同一会话内可进行多轮对话，Agent 自动记住历史上下文 |
| **自然语言交互** | 接收指令 + 附件，无需编写代码逻辑，AI 自动理解意图 |
| **智能决策执行** | 自动判断意图，自主选择调用 Skills 或 MCP 工具完成任务 |
| **流式进度反馈** | 实时返回执行过程和结果，调用方全程可见 |
| **定时任务调度** | 通过对话创建定时任务，系统在指定时间自动执行并推送通知 |
| **消息通知** | 支持 webhook / email / stdout 多渠道通知，任务完成/失败自动推送 |
| **Skills 嵌套** | 复杂任务自动拆解，子任务递归执行 |
| **热插拔扩展** | Skills 支持动态添加，无需重启服务 |
| **速率限制** | 支持按 API Key 的 QPS 和并发数限制，防止滥用 |

### 1.3 会话与对话

**会话（Session）：**
- 会话是多轮对话的容器，每个会话有唯一的 `session_id`
- 会话内的所有对话共享历史上下文，Agent 能记住之前的交流
- 会话数据存储在文件系统的 `memory` 目录

**对话（Chat）：**
- 每次调用 `/chat` API 都会产生一次对话
- 对话属于某个会话，同一会话内的对话按轮次编号
- 每次对话的详细执行记录独立存储

**关系图：**

```
Session（会话）
  ├── Chat 1（第1轮对话）→ 历史 + 结果
  ├── Chat 2（第2轮对话）→ 历史 + 结果 + 第1轮上下文
  ├── Chat 3（第3轮对话）→ 历史 + 结果 + 第1、2轮上下文
  └── ...
```

### 1.4 技术架构

```
┌─────────────────────────────────────────────────────────────┐
│                      你的业务系统                             │
│  (Java / Python / Go / 任意支持 HTTP 的系统)                  │
└─────────────────────────────────────────────────────────────┘
                              │ HTTP API
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      Groot Agent 服务                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐          │
│  │ REST API    │  │ Agent Engine│  │ MCP Tools   │          │
│  │ (SSE流式)   │→ │ (ReAct模式) │→ │ (文件/HTTP) │          │
│  └─────────────┘  └─────────────┘  └─────────────┘          │
│                              ↓                               │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐          │
│  │ Memory 存储 │  │ Skills 注册 │  │ 定时调度    │          │
│  │ (JSON文件)  │  │             │  │ (gocron)    │          │
│  └─────────────┘  └─────────────┘  └─────────────┘          │
│                                           ↓                  │
│                          ┌─────────────────────┐            │
│                          │ 消息通知层           │            │
│                          │ (webhook/email/stdout)│           │
│                          └─────────────────────┘            │
└─────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────┐
│                      LLM API 服务                             │
│  (OpenAI / Claude / 任意 OpenAI 兼容服务)                     │
└─────────────────────────────────────────────────────────────┘
```

---

## 二、工作目录结构

Groot 启动时会创建一个工作目录（Home 目录），默认位置为 `~/.groot`，可通过环境变量 `GROOT_HOME` 更改。

### 2.1 目录结构

```
{GROOT_HOME}/
├── config.yaml                    # 主配置文件
├── GROOT.md                       # 项目规范文件（自动注入系统指令）
├── skills/                        # Skills 目录
│   └── {skill-name}/SKILL.md      # Skill 定义文件
├── mcp/                           # MCP 配置目录
│   └── {mcp-name}.json            # MCP 配置文件
├── memory/                        # 记忆模块目录
│   ├── temp/                      # 附件处理临时目录
│   └── {session_id}/              # 会话目录
│       ├── history.json           # 对话历史（含执行元数据摘要）
│       ├── attachments/           # 附件目录
│       │   └── {filename}         # 附件文件
│       └── chats/                 # 详细执行记录目录
│           └── chat_{timestamp}.json  # 单次对话完整记录
├── logs/                          # 日志目录
│   └── groot-{date}.log           # 日志文件
├── cluster/                       # 集群管理目录
│   └── members/                   # 成员注册文件（用于多实例 Leader 选举）
├── schedules/                     # 定时任务目录
│   ├── active/                    # 活跃任务
│   │   └── {task-id}.json         # 任务定义文件
│   ├── disabled/                  # 已禁用任务
│   │   └── {task-id}.json         # 任务定义文件
│   ├── archive/                   # 已归档任务
│   │   └── {task-id}.json         # 任务定义文件
│   └── executions/                # 执行记录
│       └── {task-id}.json         # 历史执行记录
```

### 2.2 目录说明

**固定目录（不可配置）：**

| 目录/文件 | 说明 |
|----------|------|
| `config.yaml` | 主配置文件，控制服务行为 |
| `GROOT.md` | 项目规范文件，自动注入系统指令最前面，支持热加载 |
| `skills/` | Skills 定义目录（固定位置），支持热插拔 |
| `mcp/` | MCP 工具配置目录（固定位置），修改需重启服务 |
| `cluster/members/` | 集群成员注册目录（固定位置），存放实例注册文件，用于 Leader 选举 |
| `schedules/` | 定时任务存储目录（固定位置），active/disabled/archive 三目录状态流转 |

**可配置目录（支持相对/绝对路径）：**

| 目录/文件 | 说明 |
|----------|------|
| `memory/` | 会话数据目录（默认位置），可通过 `memory.directory` 配置 |
| `memory/temp/` | 附件处理临时目录（固定在 memory 目录下） |
| `memory/{sid}/attachments/` | 附件存储，保留原始文件名 |
| `memory/{sid}/chats/` | 每轮对话的详细执行记录 |
| `logs/` | 日志存储目录（默认位置），可通过 `logging.file.directory` 配置 |

> **说明：** `memory` 和 `logs` 目录支持通过配置文件修改位置，详见第五章"配置文件详解"。固定目录（skills/mcp/temp）位置不可更改。

### 2.3 ID 格式说明

| ID 类型 | 格式 | 示例 |
|---------|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260419103000523_a1b2` |
| `chat_id` | `chat_{YYYYMMDDHHMMSSmmm}` | `chat_20260419103000523` |
| `task_id` | `task-{kebab-case-name}` | `task-每日报表生成` |

**说明：**
- `session_id`：会话唯一标识，毫秒级时间戳 + 4位随机字符
- `chat_id`：单次对话标识，固定前缀 `chat_` + 毫秒级时间戳
- `task_id`：定时任务唯一标识，固定前缀 `task-` + 名称转 kebab-case
- 调度执行的会话 ID 格式：`{task_id}-{timestamp}-sched`（后缀区分标识）

### 2.4 工作目录配置方式

| 方式 | 示例 | 优先级 |
|------|------|--------|
| 环境变量 | `export GROOT_HOME=/opt/groot` | 高 |
| 默认值 | `~/.groot` | 低 |

### 2.5 项目规范文件（GROOT.md）

Groot 支持在 `{GROOT_HOME}/GROOT.md` 文件中定义项目规范，这些规范会自动注入到每次对话的系统指令最前面。

**功能特点：**
- 无需配置开关，默认启用
- 支持热加载，修改后自动生效
- 内容始终位于系统指令最前面，优先级最高

**使用示例：**

在 `~/.groot/GROOT.md` 中写入：

```markdown
# 项目规范

- 使用中文回答
- 代码风格遵循 Go 标准
- 优先使用已安装的工具
```

Groot 每次对话都会自动将这些规范注入系统指令，无需每次手动指定。

**系统指令构建顺序：**

```
GROOT.md（缓存）
→ prompt（用户传入）
→ Skills 指令
→ 执行规则
```

---

## 三、CLI 命令参考

Groot 提供一套命令行工具用于管理服务实例、Skills 和日志。

### 3.1 命令总览

| 命令 | 说明 |
|------|------|
| `groot` | 启动 Groot 服务 |
| `groot init` | 初始化工作目录 |
| `groot status` | 查看运行中实例的状态 |
| `groot skills list` | 列出所有已安装的 Skills |
| `groot skills install <path>` | 安装 Skill |
| `groot skills uninstall <name>` | 卸载 Skill |
| `groot mcp list` | 列出所有已配置的 MCP Servers |
| `groot schedule list` | 列出所有定时任务 |
| `groot chat` | 启动 Chat TUI（终端交互界面） |
| `groot tail` | 实时日志查看 |

**全局选项：**

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `-p, --port` | HTTP 端口 | 配置文件值 |
| `-h, --help` | 显示帮助 | - |
| `-v, --version` | 显示版本 | - |

### 3.2 启动服务（groot）

启动 Groot AI Agent 服务。

```bash
groot                      # 使用默认配置启动
groot -p 9090              # 指定端口启动
```

### 3.3 初始化工作目录（groot init）

初始化工作目录，创建必要的目录结构和配置文件。

```bash
groot init
```

创建的目录结构：

| 目录 | 说明 |
|------|------|
| `skills/` | Skills 定义目录 |
| `mcp/` | MCP 配置目录 |
| `memory/` | 会话数据目录 |
| `logs/` | 日志文件目录 |
| `cluster/members/` | 集群成员注册目录（用于多实例 Leader 选举） |
| `schedules/` | 定时任务存储目录（含 active/disabled/archive/executions） |
| `config.yaml` | 主配置文件 |

### 3.4 查看实例状态（groot status）

查看运行中 Groot 实例的状态和组件健康信息。

```bash
groot status                # 查看默认端口实例
groot status -p 9090        # 查看指定端口实例
```

| 选项 | 说明 |
|------|------|
| `-p <port>` | 指定服务端口 |
| `-h, --help` | 显示帮助 |

**输出示例（实例运行中）：**

```
Groot 实例状态

状态:      healthy
版本:      1.0.0
运行时间:  2h35m
端口:      8080

组件状态:
  LLM:          healthy (gpt-4o)
  MCP Servers:  healthy (3 个)
  Skills:       healthy (5 个)
  Memory:       healthy (12 个会话)

活跃对话: 1
```

**输出示例（实例未运行）：**

```
未检测到运行中的 Groot 实例（端口 8080）
提示: 请确认 Groot 是否已启动，或使用 -p 指定其他端口
```

### 3.5 管理 Skills（groot skills）

管理 Groot 的 Skills 安装、卸载和查看。

```bash
groot skills list                          # 列出已安装的 Skills
groot skills install /path/to/skill        # 安装 Skill（绝对路径）
groot skills install ./my-skill            # 安装 Skill（相对路径）
groot skills uninstall my-skill            # 卸载 Skill
```

**子命令说明：**

| 子命令 | 说明 |
|--------|------|
| `list` | 列出 `{GROOT_HOME}/skills/` 下所有 Skill，含名称和描述 |
| `install <path>` | 拷贝源目录到 skills 目录，重名则覆盖 |
| `uninstall <name>` | 删除指定的 Skill 目录 |

**`list` 输出示例：**

```
已安装的 Skills:

  pdf_analyzer      分析PDF文档并生成摘要
  code_generator    根据需求生成代码
  broken_skill      ⚠ 无效

共 2 个 Skill
```

### 3.6 管理 MCP Servers（groot mcp）

管理 Groot 的 MCP Servers 配置查看。

```bash
groot mcp list               # 列出所有已配置的 MCP Servers
```

**子命令说明：**

| 子命令 | 说明 |
|--------|------|
| `list` | 列出 `{GROOT_HOME}/mcp/` 下所有 MCP 配置，含名称、类型、状态和描述 |

**`list` 输出示例：**

```
NAME             TYPE              STATUS    LAST_UPDATED         DESCRIPTION
---------------  ----------------  --------  -------------------  --------------------
web-search       stdio             active     2026-05-01 10:30     基于 SearXNG 的网页搜索
filesystem       stdio             active     2026-05-08 14:22     本地文件系统操作
database         streamable_http   inactive   2026-05-09 09:15     数据库查询服务
broken-config    -                 -          -                    ⚠ 配置解析失败

共 4 个 MCP Server（2 个活跃，1 个未激活，1 个异常）
```

### 3.7 管理定时任务（groot schedule）

管理 Groot 的定时任务，支持查看、详情、历史、删除、禁用、启用和归档操作。

```bash
groot schedule list                          # 列出所有定时任务
groot schedule inspect task-xxx              # 查看任务详情（JSON 格式）
groot schedule history task-xxx              # 查看任务执行历史
groot schedule delete task-xxx               # 删除任务
groot schedule disable task-xxx              # 禁用任务（active → disabled）
groot schedule enable task-xxx               # 启用任务（disabled → active）
groot schedule archive task-xxx              # 归档任务（→ archive）
```

**子命令说明：**

| 子命令 | 说明 |
|--------|------|
| `list` | 列出所有任务（active/disabled/archive），含名称、调度表达式和状态 |
| `inspect <id>` | 查看任务完整定义（JSON 格式） |
| `history <id>` | 查看任务执行历史，含时间、触发类型、状态、耗时 |
| `delete <id>` | 物理删除任务及相关执行记录 |
| `disable <id>` | 禁用活跃任务，从调度器中移除 |
| `enable <id>` | 启用已禁用的任务，重新注册到调度器 |
| `archive <id>` | 归档任务（从任意状态） |

**`list` 输出示例：**

```
ID                                       NAME                          SCHEDULE              STATUS
----------------------------------------  ----------------------------  --------------------  ----------
task-每日报表生成                        每日报表生成                   0 9 * * *             active
task-每周数据清理                        每周数据清理                   0 2 * * 0             disabled
task-一次性提醒                          一次性提醒                    2026-06-01T09:00:00Z  archive

共 3 个任务（活跃: 1, 禁用: 1, 归档: 1）
```

**`history` 输出示例：**

```
EXEC_TIME            TRIGGER          STATUS      DURATION    STEPS
--------------------  ---------------  ----------  ----------  ----------
2026-05-11 09:00:05  cron             completed   1234ms      3
2026-05-10 09:00:02  cron             completed   1156ms      3
2026-05-09 09:00:08  cron             failed      5002ms      1

共 3 条记录
```

### 3.8 日志查看（groot tail）

实时查看 Groot 日志，类似 `tail -f`，支持格式化和过滤。

```bash
groot tail                  # 实时查看日志
groot tail -n 50            # 查看最近 50 行后实时跟踪
groot tail -l error         # 只查看错误级别日志
groot tail -k "api_request" # 过滤包含关键词的日志
```

| 选项 | 说明 |
|------|------|
| `-n <N>` | 显示最后 N 行历史日志，默认 100 |
| `-l <level>` | 按级别过滤：error/warn/info/debug |
| `-k <keyword>` | 按关键词过滤 |
| `-h, --help` | 显示帮助 |

退出方式：按 `Ctrl+C`。

### 3.9 Chat TUI（groot chat）

启动终端交互界面（Terminal User Interface），在终端中直接与大模型对话。

```bash
groot chat                   # 启动 Chat TUI
```

**启动流程：**

1. 从 `~/.groot/config.yaml` 加载配置
2. 检测配置端口上是否已有 Groot 服务运行
3. 如未检测到运行中服务 → 自动在进程内启动嵌入式 Groot 服务
4. 如检测到已有服务 → 直接连接，共享该服务的会话数据
5. 启动全屏终端界面，进入交互对话模式

**界面布局：**

TUI 使用全屏 AltScreen 模式，无外层边框。布局从顶到底依次为：

```
   ██████╗ ██████╗  ██████╗  ██████╗ ████████╗
  ██╔════╝ ██╔══██╗██╔═══██╗██╔═══██╗╚══██╔══╝
  ██║  ███╗██████╔╝██║   ██║██║   ██║   ██║         ← 欢迎画面（首次启动）
  ██║   ██║██╔══██╗██║   ██║██║   ██║   ██║
  ╚██████╔╝██║  ██║╚██████╔╝╚██████╔╝   ██║
   ╚═════╝ ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝

         Groot AI Agent · v1.0.0
    ─────────────────────────────
    输入你的问题开始对话
    输入 /help 查看系统命令

  > 用户消息                                      ← 消息区（viewport，无边框，占主要区域）

  助手回答内容（Markdown 渲染）...

  🤔 Thinking...                                  ← 思考过程（灰色斜体）
  ⚡ 调用技能: get-weather                         ← 技能/工具调用标签
  🔧 调用工具: file_read

  ┌─ 补全弹窗（条件显示，叠加在 viewport 底部）──┐
  │  /exit      退出聊天                            │  ← 圆角边框浮层
  │  /model     切换模型                            │
  └─────────────────────────────────────────────────┘

  ╔════════════════════════════════════════════════╗
  ║  > 用户输入内容...                              ║  ← 输入区（绿色双线边框）
  ╚════════════════════════════════════════════════╝
  模型: gpt-4o      会话: sess-abc123      对话: 第 3 轮   ← 状态栏（底部一行）
```

**界面说明：**

| 区域 | 说明 |
|------|------|
| 消息区 (viewport) | 无边框，占主要区域。显示用户消息、助手回答（Markdown 渲染）、思考过程、技能/工具调用状态 |
| 补全弹窗 | 输入 `/` 时自动弹出，叠加在 viewport 底部（viewport 自动裁剪让位），圆角边框 |
| 输入区 | 绿色双线边框多行文本输入，支持 Shift+Enter / Alt+Enter 插入换行 |
| 状态栏 | 底部一行，无边框。左：当前模型名，中：会话 ID，右：对话轮次 |

**消息类型展示：**

| 类型 | 图标 | 说明 |
|------|------|------|
| 用户消息 | `>` | 白色加粗文字，深灰色背景（`#2c313a`） |
| 助手回答 | — | Markdown 渲染，支持代码块、表格、列表等 |
| 思考过程 | 🤔 | 灰色斜体，展示模型推理过程 |
| 技能调用 | ⚡ | 紫色显示技能名称 |
| 工具调用 | 🔧 | 黄色显示工具名和参数摘要 |
| 错误信息 | ❌ | 红色显示错误详情 |

**系统命令：**

在输入框中输入以下命令进行操作：

| 命令 | 参数 | 功能 |
|------|------|------|
| `/help` | 无 | 显示所有命令帮助（渲染到消息区） |
| `/model` | `[名称]` | 切换模型。无参数弹出模型列表选择；带参数直接切换，模型名无效则弹出列表 |
| `/clear` | 无 | 清空屏幕对话，生成新会话 ID，开始全新对话 |
| `/skills` | 无 | 弹出 skill 选择列表，选中后可继续输入指令 |
| `/mcp` | 无 | 按 MCP 服务器分组列出所有可用工具，树状结构展示 |
| `/export` | 无 | 导出当前会话完整对话历史为 Markdown（保存到 `~/.groot/exports/`） |
| `/exit` | 无 | 退出 Chat TUI |

**快捷键：**

| 按键 | 上下文 | 行为 |
|------|--------|------|
| `Enter` | 输入框 | 发送消息 |
| `Shift+Enter` / `Alt+Enter` | 输入框 | 插入换行 |
| `Tab` | 补全弹窗可见 | 接受补全建议，关闭弹窗 |
| `↑` / `↓` | 补全弹窗可见 | 上下选择补全项 |
| `↑` / `↓` | 补全弹窗不可见 | 输入框内光标上下移动（textarea 处理） |
| `PgUp` / `PgDn` | 任意时刻 | 向上/向下半页滚动消息区 |
| `ESC` | 补全弹窗可见 | 关闭补全弹窗 |
| `ESC` | AI 回答中 | 断开 SSE 连接，取消当前回答 |
| `ESC` | 正常状态 | 清空输入框 |
| `Ctrl+C` | 任意时刻 | 退出 Chat TUI |
| 鼠标滚轮 | 任意时刻 | 逐行滚动（依赖终端将滚轮转为 Up/Down 键） |

**交互细节：**

- **加载状态**：发送消息后，收到首个响应前，消息区显示绿色 spinner 动画 + "正在思考..." 文字
- **流式输出**：助手回答和思考过程实时流式追加，自动跟随底部滚动；用户手动上滚后暂停跟随
- **文本选择**：不启用鼠标捕获，终端原生鼠标选择和复制正常工作
- **工具参数显示**：工具调用参数以 `├─ key = value` 树状格式逐行展示，非原始 JSON；超长值自动截断
- **工具结果**：工具执行结果不在消息区展示，由 LLM 最终回答体现
- **取消机制**：ESC 断开 SSE 连接停止生成

**Skills 快捷调用：**

输入 `/` 后跟 Skill 名称即可快速调用。例如：

```
/get-weather 北京今天天气如何
```

这会自动将指令转换为：`请使用 get-weather skill 来处理以下指令：北京今天天气如何`

**导出对话：**

使用 `/export` 命令可将当前会话的完整对话历史导出为 Markdown 文件，保存到 `~/.groot/exports/chat-<session_id>.md`。

**服务模式说明：**

| 场景 | 行为 |
|------|------|
| 端口上无服务运行 | 自动启动嵌入式服务，退出 TUI 时自动关闭 |
| 端口上已有服务 | 直接连接，退出 TUI 不影响服务运行 |
| 端口上服务为 chat 启动 | 共享内存中的会话数据 |

> **提示：** 嵌入式模式下日志仅输出到文件，不在终端显示，避免干扰界面渲染。

---

## 四、安装部署

### 4.1 系统要求

| 要求 | 说明 |
|------|------|
| 操作系统 | Linux / macOS / Windows |
| Go 版本 | Go 1.21+（仅源码编译需要） |
| 内存 | 建议 512MB+ |
| 磁盘 | 建议 1GB+（用于附件存储和会话数据） |

### 4.2 配置文件说明

初始化后生成的 `config.yaml` 包含完整配置模板，其中 **LLM 配置为必填项**，其他配置已注释并标注默认值。

**必填配置（LLM）：**

```yaml
llm:
  default_model: gpt-4o           # 默认模型名称
  models:
    gpt-4o:
      base_url: https://api.openai.com/v1    # API 地址
      api_key: ${OPENAI_API_KEY}             # API 密钥（建议使用环境变量）
      model: gpt-4o                          # 模型名称
```

`api_key` 支持两种写法：

```yaml
# 方式一：环境变量引用（推荐）
api_key: ${OPENAI_API_KEY}

# 方式二：直接写入密钥
api_key: sk-xxxxxxxxxxxx
```

> **推荐环境变量：** 避免密钥硬编码，便于环境切换。

**可选配置：**

配置模板中已注释展示所有可选配置项及其默认值，包括：
- `agent` - Agent 基础信息
- `server` - HTTP 服务配置
- `skills` - Skills 热插拔配置
- `react` - ReAct 执行配置
- `attachment` - 附件处理配置
- `memory` - 记忆模块配置
- `security` - 安全认证配置
- `logging` - 日志配置

如需修改，取消对应配置的注释即可。

### 4.3 环境变量

**固定环境变量：**

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `GROOT_HOME` | 工作目录 | `~/.groot` |

**用户自定义环境变量：**

配置文件中 `${VAR_NAME}` 引用的变量名由用户自定义，是否需要设置取决于配置文件的写法：

```bash
# 示例（变量名可自定义）
export OPENAI_API_KEY="sk-xxxx"
export ANTHROPIC_API_KEY="sk-ant-xxxx"
```

> **判断方法：** 配置文件有 `${VAR_NAME}` 引用则需设置，直接写密钥则不需要。

### 4.4 安装方式

#### 方式一：直接运行（推荐）

下载预编译的二进制文件：

```bash
# Linux
wget https://github.com/zfd81/groot/releases/download/v1.0.0/groot-linux-amd64
chmod +x groot-linux-amd64
mv groot-linux-amd64 /usr/local/bin/groot

# macOS
wget https://github.com/zfd81/groot/releases/download/v1.0.0/groot-darwin-arm64
chmod +x groot-darwin-arm64
mv groot-darwin-arm64 /usr/local/bin/groot
```

#### 方式二：源码编译

```bash
# 克隆仓库
git clone https://github.com/zfd81/groot.git
cd groot

# 编译当前平台
go build -o bin/groot ./cmd/groot

# 或使用 Makefile
make build            # 编译当前平台
make build-all        # 编译所有平台（macOS/Linux/Windows）

# 运行
./bin/groot
```

**Makefile 编译命令：**

| 命令 | 说明 |
|------|------|
| `make build` | 编译当前平台可执行文件 |
| `make build-all` | 编译三个平台可执行文件 |
| `make build-darwin` | 编译 macOS ARM64 |
| `make build-linux` | 编译 Linux AMD64 |
| `make build-windows` | 编译 Windows AMD64 |
| `make clean` | 清理编译产物 |

**编译产物：**

| 文件 | 平台 |
|------|------|
| `bin/darwin-arm64/groot` | macOS ARM64 |
| `bin/linux-amd64/groot` | Linux AMD64 |
| `bin/windows-amd64/groot.exe` | Windows AMD64 |

### 4.5 停止服务

```bash
# 发送终止信号
kill -SIGTERM <pid>

# 或使用 Ctrl+C（前台运行时）
```

服务会优雅关闭：
- 停止接受新请求
- 等待当前对话完成（超时 30 秒）
- 停止统一调度器（gocron）
- 停止消息通知层
- 关闭 MCP 连接
- 刷新日志
- 退出程序

## 五、配置文件详解

### 5.1 配置文件位置

首次启动时，Groot 会自动生成默认配置文件 `{GROOT_HOME}/config.yaml`。

### 5.2 完整配置文件示例

```yaml
# Groot Agent 配置文件
# 生成时间: 2026-04-18

# Agent 基础配置
agent:
  name: groot                      # Agent 名称
  version: 1.0.0                   # Agent 版本号

# HTTP 服务配置
server:
  host: 0.0.0.0                    # 服务监听地址
  port: 8080                       # 服务监听端口

# LLM 配置（OpenAI兼容协议）
llm:
  default_model: gpt-4o             # 默认模型名称
  models:
    gpt-4o:                        # 模型配置名称（自定义）
      base_url: https://api.openai.com/v1    # LLM API 地址
      api_key: ${OPENAI_API_KEY}             # API 密钥（支持环境变量引用）
      model: gpt-4o                          # 实际调用时的模型名称
      max_completion_tokens: 4096            # 最大输出 Token 数
      temperature: 0.7                       # 输出随机性（0.0~2.0）
      top_p: 1.0                             # 核采样系数（0.0~1.0）
      frequency_penalty: 0.0                 # 频率惩罚（-2.0~2.0）
      presence_penalty: 0.0                  # 存在惩罚（-2.0~2.0）
      seed: 0                                # 随机种子（0 表示不设置）
      stop: []                               # 停止序列
      thinking: false                        # 深度思考模式（Qwen/DeepSeek 等模型）
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_completion_tokens: 4096
      temperature: 0.7

# Skills 热插拔配置
skills:
  hot_reload:
    enabled: true                    # 是否启用 Skills 热插拔
    debounce_delay: 2                # 防抖延迟（秒）

# ReAct 执行配置
react:
  max_iterations: 20               # 最大循环次数，-1 表示不限制
  max_tokens: 100000               # 整个对话所有LLM调用的总Token消耗上限
  step_timeout: 60                 # 单步执行超时（秒），-1 表示不限制
  error_retry: 2                   # 单步失败重试次数
  nesting_max_depth: 3             # Skills嵌套最大深度，-1 表示不限制

# 附件处理配置
attachment:
  max_size: 50                     # 单个附件最大大小（MB）
  max_total_size: 100              # 附件总大小上限（MB）
  max_count: 10                    # 附件数量上限
  allowed_types: [pdf, doc, docx, txt, json, csv, xml, yaml, png, jpg, jpeg, zip]  # 允许的附件类型

# 记忆模块配置
memory:
  directory: memory                # 记忆目录（相对路径或绝对路径）
  retention_days: 7                # 会话保留天数
  cleanup_schedule: "02:00"        # 清理时间（HH:MM）

# 定时任务调度配置
schedule:
  enabled: false                   # 是否允许在对话中创建定时任务（默认关闭，不影响系统清理任务）
  max_concurrent_tasks: 10         # 最大并发执行任务数
  sync_interval: 30s               # 定期同步间隔（对比 active/ 目录与调度器状态，修复不一致）

# 消息通知配置
message:
  queue_size: 100                  # 消息队列容量
  workers: 3                       # 消息发送 worker 数量
  senders:
    webhook:
      enabled: false               # 是否启用 webhook 通知
      url: ""                      # Webhook URL（接收 POST JSON）
    email:
      enabled: false               # 是否启用邮件通知
      smtp_host: ""                # SMTP 服务器地址
      smtp_port: 587               # SMTP 端口
      username: ""                 # SMTP 用户名
      password: ""                 # SMTP 密码
      from: ""                     # 发件人地址

# 安全配置
security:
  rate_limit:
    enabled: false                 # 是否启用速率限制（默认关闭）
    global_qps: 0                  # 全局 QPS 限制（0=不限制）
    global_concurrency: 0          # 全局并发限制（0=不限制）
    default_qps: 10                # 每 API Key 默认 QPS
    default_concurrency: 5         # 每 API Key 默认并发数
    cleanup_interval: 5m           # 空闲限流器清理间隔
  auth:
    enabled: true                  # 是否开启认证
    type: api_key                  # 认证类型
    api_key:
      header_name: X-API-Key       # 认证 Header 名称
      keys:
        - name: default            # Key 名称（唯一标识）
          key: ${GROOT_API_KEY}    # Key 值（支持环境变量引用）
          permissions: all         # 权限范围：all 或 [chat, status, ...]

# 日志配置
logging:
  level: info                      # 日志级别：debug/info/warn/error
  format: json                     # 日志格式：json/text
  output: [stdout, file]           # 输出目标：stdout/file（可同时输出）
  file:
    directory: logs                # 日志文件目录
    filename_pattern: groot-{date}.log  # 文件名模式，{date} 替换为 YYYY-MM-DD
    max_age: 7                     # 日志保留天数
    max_size: 100                  # 单个日志文件最大大小（MB），超过则轮转
    compress: false                # 是否压缩旧日志文件
```

### 5.3 目录配置说明

所有目录配置支持相对路径和绝对路径：

- **相对路径**：相对于 `~/.groot` 目录（GROOT_HOME）
- **绝对路径**：直接使用指定路径

示例配置：

```yaml
# 相对路径示例（目录位于 ~/.groot/memory）
memory:
  directory: memory

# 绝对路径示例（目录位于 /data/logs）
logging:
  file:
    directory: /data/logs
```

可配置的目录包括：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `memory.directory` | `memory` | 会话记忆目录（支持相对/绝对路径） |
| `logging.file.directory` | `logs` | 日志文件目录（支持相对/绝对路径） |

**固定目录（不可配置）：**

| 目录 | 位置 | 说明 |
|------|------|------|
| `skills` | `{GROOT_HOME}/skills` | Skills 定义目录 |
| `mcp` | `{GROOT_HOME}/mcp` | MCP 配置目录 |
| `schedules` | `{GROOT_HOME}/schedules` | 定时任务存储目录 |
| `temp` | `{memoryDir}/temp` | 附件处理临时目录（固定在 memory 目录下） |

### 5.4 配置字段详解

#### Agent 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `name` | 否 | Agent 名称，用于日志标识，默认 `groot` |
| `version` | 否 | Agent 版本号，默认 `1.0.0` |

#### Server 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `host` | 否 | 监听地址，默认 `0.0.0.0`（所有网卡） |
| `port` | 否 | 监听端口，默认 `8080` |

#### LLM 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `default_model` | **是** | 默认模型名称，对应 models 中的某个 key，修改后需重启 |
| `models.{name}.base_url` | **是** | LLM API 地址（OpenAI 兼容协议） |
| `models.{name}.api_key` | **是** | API 密钥，支持 `${VAR_NAME}` 引用环境变量 |
| `models.{name}.model` | **是** | 实际调用时的模型名称 |
| `models.{name}.max_completion_tokens` | 否 | 最大输出 Token 数，默认 `4096` |
| `models.{name}.temperature` | 否 | 输出随机性（0.0~2.0），默认 `0.7` |
| `models.{name}.top_p` | 否 | 核采样系数（0.0~1.0），默认 `1.0` |
| `models.{name}.frequency_penalty` | 否 | 频率惩罚（-2.0~2.0），默认 `0.0` |
| `models.{name}.presence_penalty` | 否 | 存在惩罚（-2.0~2.0），默认 `0.0` |
| `models.{name}.seed` | 否 | 随机种子，`0` 表示不设置 |
| `models.{name}.stop` | 否 | 停止序列列表，默认空 |
| `models.{name}.thinking` | 否 | 深度思考模式（Qwen/DeepSeek 等），默认 `false` |

#### Skills 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `hot_reload.enabled` | 否 | 是否启用热插拔，默认 `true` |
| `hot_reload.debounce_delay` | 否 | 防抖延迟（秒），默认 `2` |

> **目录固定**：Skills 目录固定为 `{GROOT_HOME}/skills`，无需配置。

#### ReAct 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `max_iterations` | 否 | 最大循环次数，默认 `20`，`-1` 表示不限 |
| `max_tokens` | 否 | 整个对话所有LLM调用的总Token消耗上限，默认 `100000`，`-1` 表示不限 |
| `step_timeout` | 否 | 单步执行超时（秒），默认 `60`，`-1` 表示不限 |
| `error_retry` | 否 | 单步失败重试次数，默认 `2` |
| `nesting_max_depth` | 否 | Skills 嵌套最大深度，默认 `3`，`-1` 表示不限 |

#### Attachment 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `max_size` | 否 | 单个附件最大大小（MB），默认 `50` |
| `max_total_size` | 否 | 附件总大小上限（MB），默认 `100` |
| `max_count` | 否 | 单次请求最大附件数量，默认 `10` |
| `allowed_types` | 否 | 允许的文件扩展名列表，默认常见文档和图片类型 |

#### Memory 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `directory` | 否 | 记忆目录，相对路径拼接工作目录，绝对路径直接使用，默认 `memory` |
| `retention_days` | 否 | 会话保留天数，超过后自动清理，默认 `7`。清理依据目录最后修改时间（非创建时间） |
| `cleanup_schedule` | 否 | 清理任务执行时间（HH:MM），默认 `02:00`，由统一调度器按天执行 |

#### Schedule 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `enabled` | 否 | 是否允许在对话中创建定时任务，默认 `false`。关闭时对话中无法创建/管理任务（系统级清理和同步不受影响） |
| `max_concurrent_tasks` | 否 | 最大并发执行任务数，超出的任务跳过当次执行，默认 `3` |
| `sync_interval` | 否 | 定期同步间隔（Go duration 格式，如 `30s`/`1m`），对比 active/ 目录与调度器状态，自动修复不一致，默认 `30s` |

#### Message 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `queue_size` | 否 | 消息队列容量，队列满时发布方返回 `ErrQueueFull`，默认 `100` |
| `workers` | 否 | 消息发送 worker 数量，默认 `3` |
| `senders.webhook.enabled` | 否 | 是否启用 webhook 通知，默认 `false` |
| `senders.webhook.url` | 否 | Webhook URL，任务完成/失败时 POST JSON 到该地址 |
| `senders.email.enabled` | 否 | 是否启用邮件通知，默认 `false` |
| `senders.email.smtp_host` | 否 | SMTP 服务器地址 |
| `senders.email.smtp_port` | 否 | SMTP 端口，默认 `587` |
| `senders.email.username` | 否 | SMTP 认证用户名 |
| `senders.email.password` | 否 | SMTP 认证密码 |
| `senders.email.from` | 否 | 发件人邮箱地址 |

> **说明：** stdout sender 始终启用，无需配置。webhook 和 email sender 按需配置。定时任务的 `notify_on_success` / `notify_on_failure` 字段指定通知渠道。

#### Security 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `auth.enabled` | 否 | 是否开启认证，默认 `false` |
| `auth.type` | 否 | 认证类型，目前只支持 `api_key` |
| `auth.api_key.header_name` | 否 | 认证 Header 名称，默认 `X-API-Key` |
| `auth.api_key.keys[].name` | 否 | Key 名称（唯一标识） |
| `auth.api_key.keys[].key` | 否 | Key 值，支持 `${VAR_NAME}` 引用 |
| `auth.api_key.keys[].permissions` | 否 | 权限范围：`all` 或 `[chat, status, ...]` |
| `rate_limit.enabled` | 否 | 是否启用速率限制，默认 `false` |
| `rate_limit.global_qps` | 否 | 全局 QPS 限制，`0` 表示不限制 |
| `rate_limit.global_concurrency` | 否 | 全局并发限制，`0` 表示不限制 |
| `rate_limit.default_qps` | 否 | 每 API Key 默认 QPS，默认 `10` |
| `rate_limit.default_concurrency` | 否 | 每 API Key 默认并发数，默认 `5`（仅 `/chat` 生效） |
| `rate_limit.cleanup_interval` | 否 | 空闲限流器清理间隔，默认 `5m` |

> **速率限制说明：**
> - **匿名降级**：认证开启时按 API Key 名称限流；认证关闭（`auth.enabled: false`）时按客户端 IP 限流
> - **容错降级**：限流器配置异常时自动禁用限流，不影响服务正常启动

#### Logging 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `level` | 否 | 日志级别：`debug`/`info`/`warn`/`error`，默认 `info` |
| `format` | 否 | 日志格式：`json`/`text`，默认 `json` |
| `output` | 否 | 输出目标：`[stdout, file]`，可同时输出 |
| `file.directory` | 否 | 日志文件目录，默认 `logs` |
| `file.filename_pattern` | 否 | 文件名模式，`{date}` 替换为 YYYY-MM-DD |
| `file.max_age` | 否 | 日志保留天数，默认 `7` |
| `file.max_size` | 否 | 单个日志文件最大大小（MB），默认 `100` |
| `file.compress` | 否 | 是否压缩旧日志文件，默认 `false` |

### 5.5 权限说明

| 权限 | 对应 API | 说明 |
|------|---------|------|
| `chat` | POST /chat | 执行对话 |
| `status` | GET /chat/status/{sid} | 查询对话状态 |
| `detail` | GET /chat/{sid} | 查询对话详情 |
| `session` | GET /sess/{sid} | 查询会话详情 |
| `history` | GET /sess/history | 查询会话列表 |
| `skills` | GET /skills | 查看 Skills 列表 |
| `tools` | GET /tools | 查看工具列表（MCP 工具 + 调度工具） |
| `schedule` | GET/POST/DELETE /schedule | 管理定时任务 |
| `health` | GET /health | 健康检查 |
| `all` | 以上全部 | 全部权限 |

### 5.6 配置热更新

**支持热更新的配置：**
- Skills 配置：修改 SKILL.md 文件自动生效

**不支持热更新的配置：**
- LLM 配置、Server 配置、Security 配置、Rate Limit 配置、Memory 配置、Logging 配置、Schedule 配置、Message 配置需重启服务
- MCP 配置：修改 `{GROOT_HOME}/mcp/*.json` 文件需重启服务

---

## 六、Skills 配置（固定目录）

Skills 目录固定位于 `{GROOT_HOME}/skills`，无需在配置文件中指定。

### 6.1 Skills 目录结构

```
{GROOT_HOME}/skills/
├── pdf_analyzer/
│   └── SKILL.md
├── code_generator/
│   └── SKILL.md
└── data_analyzer/
    └── SKILL.md
```

### 6.2 Skill 文件格式

每个 Skill 是一个目录，包含一个 `SKILL.md` 文件，采用 YAML frontmatter + Markdown 格式：

```markdown
---
name: pdf_analyzer                    # Skill 名称（全局唯一）
description: "分析PDF文档并生成摘要"   # Skill 描述（Agent 工具列表展示）
dependencies: []                      # 依赖的其他 Skill（可选）
---

# PDF 文档分析

你是一个专业的 PDF 文档分析助手。

## 执行步骤

1. 使用 file_operations.file_read 工具读取 PDF 文件
2. 提取文档的关键内容和结构
3. 根据文档类型生成相应的结构化摘要
4. 输出结构化的分析结果

## 输出格式

{
  "document_type": "文档类型",
  "title": "文档标题",
  "key_points": ["关键要点"],
  "summary": "详细摘要",
  "recommendations": ["建议"]
}
```

### 6.3 热插拔机制

- 启用 `skills.hot_reload.enabled: true` 后，修改 `SKILL.md` 自动生效
- 防抖延迟 `debounce_delay` 防止编辑过程中频繁触发加载
- 新增 Skill：创建目录和 `SKILL.md` 文件
- 修改 Skill：编辑 `SKILL.md` 内容
- 删除 Skill：删除对应目录

---

## 七、MCP 工具配置

### 7.1 MCP 配置目录（固定位置）

MCP 配置目录固定位于 `{GROOT_HOME}/mcp`，无需在配置文件中指定。

```
{GROOT_HOME}/mcp/
├── database_tool.json     # 数据库查询工具（stdio 类型）
├── web_parser.json        # 网页解析服务（sse 类型）
└── web_search.json        # 网络搜索服务（streamable_http 类型）
```

每个 MCP 工具使用独立的 JSON 配置文件。添加、修改或删除 MCP 配置后需要重启服务才能生效。

### 7.2 连接类型

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `stdio` | 标准输入输出通信 | 本地命令行工具（如数据库客户端） |
| `sse` | Server-Sent Events（单向推送） | 远程 HTTP 服务，服务端主动推送事件 |
| `streamable_http` | Streamable HTTP（双向流式） | 远程 HTTP 服务，支持请求和响应双向流式 |

### 7.3 MCP 配置示例

**stdio 类型（本地命令行工具）：**

```json
{
  "name": "database_tool",
  "type": "stdio",
  "description": "数据库查询工具",
  "isActive": true,
  "command": "mcp-server-postgres",
  "args": ["--connection", "${DB_CONNECTION}"],
  "env": {
    "DB_CONNECTION": "${DB_CONNECTION}"
  }
}
```

| 字段 | 说明 |
|------|------|
| `name` | MCP 名称，用于日志和调试 |
| `type` | 连接类型，`stdio` 表示通过标准输入输出通信 |
| `description` | MCP 功能描述，注册给 Agent 作为工具说明 |
| `isActive` | 是否启用，`false` 时跳过加载 |
| `command` | 要执行的可执行程序名称 |
| `args` | 命令行参数数组，支持环境变量引用 `${VAR}` |
| `env` | 环境变量映射，传递给子进程 |

**sse 类型（远程 SSE 服务）：**

```json
{
  "name": "WebParser",
  "type": "sse",
  "description": "网页解析服务",
  "isActive": true,
  "baseUrl": "https://dashscope.aliyuncs.com/api/v1/mcps/WebParser/sse",
  "headers": {
    "Authorization": "Bearer ${DASHSCOPE_API_KEY}"
  }
}
```

| 字段 | 说明 |
|------|------|
| `name` | MCP 名称，用于日志和调试 |
| `type` | 连接类型，`sse` 表示 Server-Sent Events（单向推送） |
| `description` | MCP 功能描述，注册给 Agent 作为工具说明 |
| `isActive` | 是否启用，`false` 时跳过加载 |
| `baseUrl` | 远程服务的 SSE 接口地址 |
| `headers` | HTTP 请求头，用于认证等，支持环境变量引用 `${VAR}` |

**streamable_http 类型（HTTP 流式服务）：**

```json
{
  "name": "web_search",
  "type": "streamable_http",
  "description": "网络搜索服务",
  "isActive": true,
  "baseUrl": "https://mcp-search.example.com/api",
  "headers": {
    "X-API-Key": "${SEARCH_API_KEY}"
  }
}
```

| 字段 | 说明 |
|------|------|
| `name` | MCP 名称，用于日志和调试 |
| `type` | 连接类型，`streamable_http` 表示双向流式 HTTP 通信 |
| `description` | MCP 功能描述，注册给 Agent 作为工具说明 |
| `isActive` | 是否启用，`false` 时跳过加载 |
| `baseUrl` | 远程服务的 API 地址 |
| `headers` | HTTP 请求头，用于认证等，支持环境变量引用 `${VAR}` |

---

## 八、API 详细说明

### 8.1 API 列表

| API | 方法 | 用途 |
|-----|------|------|
| `/chat` | POST | 执行对话，SSE 流式返回（支持多轮对话） |

| `/chat/status/{sid}` | GET | 查询最近一次对话状态 |
| `/chat/{sid}` | GET | 查询最近一次对话详情（完整步骤记录） |
| `/sess/{sid}` | GET | 查询会话详情（完整对话历史） |
| `/sess/history` | GET | 查询会话列表 |
| `/health` | GET | 健康检查 |
| `/skills` | GET | 列出可用 Skills |
| `/tools` | GET | 列出可用 MCP 工具 |
| `/schedule` | GET | 列出所有定时任务 |
| `/schedule/:id` | GET | 查询任务详情 |
| `/schedule/:id` | DELETE | 删除定时任务 |
| `/schedule/:id/disable` | POST | 禁用定时任务 |
| `/schedule/:id/enable` | POST | 启用定时任务 |
| `/schedule/:id/archive` | POST | 归档定时任务 |
| `/schedule/:id/history` | GET | 查询任务执行历史 |

### 8.2 认证方式

如果启用了认证（`security.auth.enabled: true`），需要在请求头携带 API Key：

```http
X-API-Key: your-secret-key
```

Header 名称可在配置文件中自定义。

---

### 8.3 POST /chat - 执行对话（核心接口）

**请求 Header：**

| Header | 必填 | 说明 |
|--------|------|------|
| `X-Session-ID` | 否 | 会话ID（sid），为空则创建新会话；有值但会话不存在则生成新sid |
| `X-Model-Name` | 否 | 模型名称，指定本次对话使用的模型；为空则使用配置中的默认模型 |
| `Content-Type` | 是 | `application/json` |
| `X-API-Key` | 是 | 认证密钥（启用认证时） |

**请求 Body：**

```json
{
  "instruction": "自然语言指令",
  "prompt": "系统提示词，设定Agent角色和行为约束（可选）",
  "attachments": [
    {
      "type": "image",
      "name": "screenshot.png",
      "content": "base64编码内容"
    }
  ]
}
```

**参数说明：**

| 参数 | 必填 | 说明 |
|------|------|------|
| `instruction` | 是 | 用户任务指令 |
| `prompt` | 否 | 系统提示词，设定Agent角色、行为约束、背景信息 |
| `attachments` | 否 | 附件列表（Base64编码）|

**附件字段说明：**

| 字段 | 必填 | 说明 |
|------|------|------|
| `type` | 是 | 附件类型：`file`（文件）、`image`（图片）、`audio`（音频）、`video`（视频）|
| `name` | 是 | 附件文件名（含扩展名）|
| `content` | 否 | Base64 编码的附件内容。所有类型均以 Base64 data URL 透传给 LLM |

**响应 Header：**

| Header | 说明 |
|--------|------|
| `X-Session-ID` | 会话ID（新建或传入存在的） |
| `X-Chat-ID` | 本次对话ID |
| `Content-Type` | `text/event-stream` |
| `Cache-Control` | `no-cache` |
| `Connection` | `keep-alive` |

**SSE 响应格式：**

所有事件使用标准 SSE `data:` 格式，流结束时发送 `[DONE]`。

```
data: <JSON内容>\n\n
data: [DONE]
```

---

### 事件识别规则

每个事件通过 JSON 中的 **`role` 字段 + 特征字段** 组合来识别。前端解析策略：

```
解析 data JSON →
  role == "tool"       → tool_result 事件
  role == "assistant":
    有 tool_calls      → tool_calls 事件
    有 finish_reason   → finish 事件
    有 reasoning_content → thinking 事件
    有 content         → message 事件
```

### 事件类型与处理方式

| 事件 | role | 特征字段 | 内容字段 | 客户端处理 |
|------|------|---------|---------|-----------|
| `thinking` | `assistant` | `reasoning_content` | `reasoning_content` | 思考过程区（折叠/灰色），**不放入正文** |
| `message` | `assistant` | `content`（无 tool_calls 无 finish） | `content` | **正文区逐字追加渲染** |
| `tool_calls` | `assistant` | `tool_calls` | `tool_calls[].function.name` | 「正在调用 xxx」提示 |
| `finish` | `assistant` | `finish_reason` | `finish_reason` | 阶段结束标记，不展示 |
| `tool_result` | `tool` | — | `content` + `tool_name` | **工具调用结果区（调用详情面板）**，不放入正文 |
| `done` | — | — | — | 对话流结束 |

**关键规则：**

- `message` 事件 —— **唯一放入正文区的内容**，逐 chunk 拼接
- `tool_result` 事件 —— **不应出现在正文区**，放入独立的调用详情面板（含 tool_name、status、content）
- `thinking` 事件 —— 放入思考过程折叠区，用户可选展开
- `finish` 事件 —— 只用于判断 `stop` / `tool_calls`，不展示

---

### 事件 JSON 结构

**thinking：**

```json
{
  "role": "assistant",
  "reasoning_content": "思考内容"
}
```

**message：**

```json
{
  "role": "assistant",
  "content": "回答内容"
}
```

注意：thinking 和 message 是独立的两个 chunk，不会在同一条中出现 `reasoning_content` + `content`。

**tool_calls：**

```json
{
  "role": "assistant",
  "tool_calls": [
    {
      "id": "call_xxx",
      "type": "function",
      "function": {
        "name": "工具名称",
        "arguments": "JSON 格式参数字符串"
      },
      "extra": {}
    }
  ]
}
```

> `index` 和 `extra` 字段为 omitempty，存在多工具调用或模型附加元数据时可能出现。

**finish：**

```json
{
  "role": "assistant",
  "finish_reason": "stop"
}
```

| finish_reason | 含义 | 后续事件 |
|--------------|------|---------|
| `tool_calls` | LLM 决定调用工具 | 下一个事件为 `tool_result` |
| `stop` | 当前回答正常完成 | 可能继续下一轮 tool_calls，最终 `[DONE]` |
| `length` | 达到最大 token 限制 | 当前回答截断，可能继续或 `[DONE]` |
| `content_filter` | 内容被安全过滤 | 当前回答中断 |
| `null` | 未明确结束原因 | 流式传输中的中间状态 |

**tool_result：**

```json
{
  "role": "tool",
  "tool_call_id": "call_xxx",
  "tool_name": "list_directory",
  "content": "执行结果（可能是纯文本或 JSON 字符串）"
}
```

工具执行失败时，错误信息直接包含在 `content` 字段中。

---

### 事件流示例

**场景1：纯 LLM 回答（无 thinking）**

```
data: {"role":"assistant","content":"回答内容..."}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

**场景2：LLM 回答带 thinking**

```
data: {"role":"assistant","reasoning_content":"思考过程..."}
data: {"role":"assistant","content":"回答内容..."}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

**场景3：工具调用**

```
data: {"role":"assistant","reasoning_content":"我需要读取文件"}
data: {"role":"assistant","tool_calls":[{"id":"call_abc123","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"/etc/hosts\"}"}}]}
data: {"role":"assistant","finish_reason":"tool_calls"}
data: {"role":"tool","tool_call_id":"call_abc123","tool_name":"file_read","content":"127.0.0.1 localhost\n::1 localhost"}
data: {"role":"assistant","content":"文件内容如下：127.0.0.1 localhost"}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

### 前端实现伪代码

```javascript
eventSource.onmessage = (e) => {
  const data = JSON.parse(e.data);
  
  if (data.role === "tool") {
    // tool_result → 工具调用详情面板，不放入正文
    showToolResult(data.tool_name, data.content);
  } else if (data.role === "assistant") {
    if (data.reasoning_content) {
      // thinking → 思考区折叠显示
      appendThinking(data.reasoning_content);
    } else if (data.tool_calls) {
      // tool_calls → 工具调用中提示
      data.tool_calls.forEach(c => showToolCalling(c.function.name));
    } else if (data.content) {
      // message → 唯一放入正文区的内容
      appendMessage(data.content);
    }
    // finish → 内部判断，不展示
    if (data.finish_reason === "stop") { /* 阶段结束 */ }
  }
};

**请求示例：**

**新会话请求：**
```bash
curl -X POST http://localhost:8080/chat \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "帮我分析这份PDF财务报告", "attachments": [{"type": "file", "name": "Q3_Report.pdf", "content": "base64..."}]}'
```

**继续会话请求：**
```bash
curl -X POST http://localhost:8080/chat \
  -H "X-Session-ID: 20260419103000523_a1b2" \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "根据刚才的分析，生成一份总结报告"}'
```

---

### 8.4 GET /chat/status/{sid} - 查询对话状态

查询指定会话中最近一次对话的运行状态。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sid` | string | 是 | 会话 ID（路径参数） |

**运行中响应：**
```json
{
  "status": "success",
  "session_id": "20260419103000523_a1b2",
  "chat": {
    "chat_id": "chat_20260419103000523",
    "round": 4,
    "status": "running",
    "progress": {
      "current_step": 2,
      "steps_completed": 1,
      "percentage": 50
    },
    "started_at": "2026-04-19T10:30:00Z",
    "elapsed_time": "15s"
  }
}
```

**无运行对话响应：**
```json
{
  "status": "success",
  "session_id": "20260419103000523_a1b2",
  "chat": null
}
```

---

### 8.5 GET /chat/{sid}/{cid} - 查询对话详情

查询指定会话中某次对话的完整详情，包括指令、结果、执行步骤记录。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sid` | string | 是 | 会话 ID（路径参数） |
| `cid` | string | 是 | 对话 ID（路径参数） |

**响应示例：**
```json
{
  "status": "success",
  "session_id": "20260419103000523_a1b2",
  "chat": {
    "chat_id": "chat_20260419103000523",
    "round": 1,
    "instruction": "用户指令内容",
    "attachments": ["data.csv"],
    "result": {"summary": "执行结果..."},
    "status": "completed",
    "started_at": "2026-04-19T10:30:00Z",
    "ended_at": "2026-04-19T10:30:45Z",
    "duration": 45,
    "steps": [
      {
        "step_id": "20260419-103000000-a1b2c3",
        "type": "skill",
        "name": "pdf_analyzer",
        "start_time": "2026-04-19T10:30:00Z",
        "end_time": "2026-04-19T10:30:30Z",
        "status": "success"
      }
    ]
  }
}
```

---

### 8.6 GET /sess/{sid} - 查询会话详情

查询会话详情，包括完整对话历史（所有轮次）。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sid` | string | 是 | 会话 ID（路径参数） |

**响应示例：**
```json
{
  "status": "success",
  "session_id": "20260419103000523_a1b2",
  "session": {
    "created_at": "2026-04-19T10:00:00Z",
    "round_count": 4,
    "path": "/home/groot/memory/20260419103000523_a1b2"
  },
  "history": {
    "messages": [
      {
        "round": 1,
        "timestamp": "2026-04-19T10:00:00Z",
        "instruction": "帮我分析这个数据文件",
        "attachments": ["data.csv"],
        "result": "好的，分析结果如下...",
        "status": "completed",
        "duration": 45
      },
      {
        "round": 2,
        "timestamp": "2026-04-19T10:05:00Z",
        "instruction": "生成图表",
        "attachments": [],
        "result": "图表已生成...",
        "status": "completed",
        "duration": 30
      }
    ]
  }
}
```

---

### 8.7 GET /sess/history - 查询会话列表

查询所有会话列表，支持分页。参数通过 URL Query String 传递。

**请求示例：**

```http
GET /sess/history?limit=10&offset=0
X-API-Key: your-secret-key
```

**Query 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `limit` | int | 否 | 返回数量，默认 20，最大 100 |
| `offset` | int | 否 | 分页偏移，默认 0 |

**响应示例：**
```json
{
  "status": "success",
  "total": 50,
  "limit": 10,
  "offset": 0,
  "sessions": [
    {
      "session_id": "20260419103000523_a1b2",
      "created_at": "2026-04-19T10:00:00Z",
      "round_count": 4,
      "last_active_at": "2026-04-19T10:30:00Z"
    }
  ]
}
```

---

### 8.8 GET /health - 健康检查

查询服务健康状态，检查各组件运行情况。

**检查项说明：**

| 检查项 | 说明 | 检查内容 |
|-------|------|---------|
| `llm` | LLM 服务 | 实际调用 API 检查连接状态 |
| `mcp_servers` | MCP 工具 | 各 MCP 服务状态和工具数量 |
| `skills` | Skills | 已加载 Skills 数量 |
| `memory` | 会话存储 | 当前会话数量 |

**响应示例（健康）：**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "healthy", "info": {"model": "gpt-4o"}},
    "mcp_servers": {"status": "healthy", "info": [{"name": "file_operations", "tools_count": 7, "isActive": true}]},
    "skills": {"status": "healthy", "info": {"count": 4}},
    "memory": {"status": "healthy", "info": {"sessions": 10}}
  },
  "metrics": {
    "chats_running": 2
  }
}
```

**响应示例（LLM 异常）：**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "unhealthy", "info": {"model": "gpt-4o", "error": "connection failed: timeout"}},
    "mcp_servers": {"status": "healthy", "info": [...]},
    "skills": {"status": "healthy", "info": {"count": 4}},
    "memory": {"status": "healthy", "info": {"sessions": 10}}
  },
  "metrics": {
    "chats_running": 0
  }
}
```

---

### 8.9 GET /skills - 列出可用 Skills

**响应示例：**
```json
{
  "skills": [
    {"name": "pdf_analyzer", "description": "分析PDF文档并生成摘要"},
    {"name": "code_generator", "description": "根据需求生成代码"}
  ],
  "total": 2
}
```

---

### 8.10 GET /tools - 列出可用工具

列出所有可用 MCP 工具，按来源分组返回。

**响应示例：**
```json
{
  "filesystem": {
    "tools": [
      {"name": "file_read", "description": "读取文件内容"},
      {"name": "file_write", "description": "写入文件内容"}
    ],
    "total": 2
  },
  "http_request": {
    "tools": [
      {"name": "http_get", "description": "发送HTTP GET请求"}
    ],
    "total": 1
  }
}
```

**响应结构说明：**

| 字段 | 说明 |
|------|------|
| 顶层 key | MCP 服务名称 |
| `tools` | 工具列表数组 |
| `tools[].name` | 工具名称 |
| `tools[].description` | 工具描述 |
| `total` | 该组工具数量 |

> **注意：** `GET /tools` 返回所有工具，包括 MCP 工具和内置调度工具（`schedule_create`、`schedule_list` 等 8 个）。

---

### 8.11 GET /schedule - 列出定时任务

查询所有定时任务，支持按状态过滤。

**Query 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `status` | string | 否 | 状态过滤：`active`/`disabled`/`archive`/`all`（默认 `all`） |

**响应示例：**
```json
[
  {
    "id": "task-每日报表生成",
    "name": "每日报表生成",
    "schedule": "0 9 * * *",
    "instruction": "使用数据分析 skill 生成昨日报表，发送到 #report 频道",
    "missed_policy": "run_once",
    "status": "active",
    "created_at": "2026-05-11T09:00:00Z",
    "updated_at": "2026-05-11T09:00:00Z"
  }
]
```

**任务状态说明：**

| 状态 | 说明 |
|------|------|
| `active` | 活跃，调度器定时执行 |
| `disabled` | 已禁用，从调度器移除 |
| `archive` | 已归档，保留记录但不再执行 |

---

### 8.12 GET /schedule/:id - 查询任务详情

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | string | 是 | 任务 ID（路径参数） |

**响应：** 返回完整任务定义，格式同 8.12 中单条任务。

---

### 8.13 DELETE /schedule/:id - 删除任务

物理删除任务及关联文件。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | string | 是 | 任务 ID（路径参数） |

**响应示例：**
```json
{
  "status": "deleted",
  "id": "task-每日报表生成"
}
```

---

### 8.14 POST /schedule/:id/disable - 禁用任务

将任务从 `active` 移入 `disabled`，并从调度器移除。

**响应示例：**
```json
{
  "status": "disabled",
  "id": "task-每日报表生成"
}
```

---

### 8.15 POST /schedule/:id/enable - 启用任务

将任务从 `disabled` 移入 `active`，重新注册到调度器。

**响应示例：**
```json
{
  "status": "enabled",
  "id": "task-每日报表生成"
}
```

---

### 8.16 POST /schedule/:id/archive - 归档任务

将任务移入 `archive`（从任意状态）。

**响应示例：**
```json
{
  "status": "archived",
  "id": "task-每日报表生成"
}
```

---

### 8.17 GET /schedule/:id/history - 执行历史

查询某任务的执行记录。

**响应示例：**
```json
[
  {
    "task_id": "task-每日报表生成",
    "exec_time": "2026-05-11T09:00:05Z",
    "trigger_type": "cron",
    "session_id": "task-每日报表生成-20260511T090005-sched",
    "status": "completed",
    "duration_ms": 1234,
    "step_count": 3
  }
]
```

| 字段 | 说明 |
|------|------|
| `trigger_type` | 触发类型：`cron`（定时）/ `once`（一次性）/ `interval`（间隔）/ `manual`（手动重跑） |
| `session_id` | 调度执行使用的会话 ID，以 `-sched` 后缀区分 |
| `status` | 执行状态：`completed` / `failed` |
| `duration_ms` | 执行耗时（毫秒） |

---

### 8.18 定时任务创建（通过对话）

定时任务通过 Agent 对话创建，用户用自然语言描述需求，Agent 调用 `schedule_create` 工具：

```
用户：「每天早上 9 点帮我生成销售报表并通过 webhook 通知我」

Agent 自动调用 schedule_create 工具创建任务：
- name: "每日销售报表"
- schedule: "0 9 * * *"
- instruction: "生成销售报表并通过 webhook 通知我"
- notify_on_success: ["webhook"]
```

内置的 8 个调度工具（`schedule_create`、`schedule_list` 等）会自动注册到 Agent，可通过 `GET /tools` 查看。

---

## 九、客户端代码示例

完整的客户端代码及测试见 [`examples/`](examples/) 目录。

### 9.1 Python

```python
from groot_client import GrootClient

client = GrootClient("http://localhost:8080", "your-api-key")

# 新会话
result = client.execute_chat("分析这份财报", callback=lambda t, d: print(f"[{t}] {d}"))
print(f"会话ID: {result['session_id']}")

# 继续会话
result2 = client.execute_chat("生成摘要", session_id=result["session_id"])
```

> 完整代码及 15 个测试用例：[examples/python/](examples/python/)

### 9.2 Java

```java
GrootClient client = new GrootClient("http://localhost:8080", "your-api-key");

// 新会话
ChatResult result = client.executeChat("分析这份财报", (type, data) -> {
    System.out.println("[" + type + "] " + data);
});
System.out.println("会话ID: " + result.getSessionId());

// 继续会话
ChatResult result2 = client.executeChat("生成摘要", result.getSessionId(), null);
```

> 完整代码及 16 个测试用例：[examples/java/](examples/java/)

---

## 十、使用场景示例

### 10.1 多轮文档分析

```python
client = GrootClient("http://localhost:8080", "your-api-key")

# 第1轮：上传文档并分析
result1 = client.execute_chat("分析这份财报，提取营收、利润等关键指标")
sid = result1["session_id"]

# 第2轮：追问细节
result2 = client.execute_chat("重点分析利润增长的主要原因", session_id=sid)

# 第3轮：生成报告
result3 = client.execute_chat("生成一份分析报告摘要", session_id=sid)
```

### 10.2 渐进式代码开发

```python
client = GrootClient("http://localhost:8080", "your-api-key")

result1 = client.execute_chat("写一个 Python 数据处理工具类，包含 CSV 读取功能",
                              prompt="你是资深 Python 开发者")
sid = result1["session_id"]

result2 = client.execute_chat("添加数据清洗功能", session_id=sid)
result3 = client.execute_chat("写单元测试代码", session_id=sid)
```

### 10.3 定时任务自动化

通过对话创建定时任务，让 Agent 在指定时间自动执行并推送结果。

**1. 配置消息通知（config.yaml）：**

```yaml
message:
  senders:
    webhook:
      enabled: true
      url: "https://hooks.slack.com/services/xxx"
```

**2. 通过对话创建任务：**

```python
client = GrootClient("http://localhost:8080", "your-api-key")

# 创建定时任务
client.execute_chat("每天早上 9 点帮我生成前一天的销售数据报表，结果发送到 webhook")

# Agent 会自动调用 schedule_create 工具，创建 cron 任务
# 生成的 task.json 会保存到 schedules/active/ 目录
```

**3. 管理任务（CLI / API）：**

```bash
# CLI 查看任务列表
groot schedule list

# CLI 查看执行历史
groot schedule history task-每日销售报表

# 禁用/启用任务
groot schedule disable task-每日销售报表
groot schedule enable task-每日销售报表
```

```bash
# API 管理
curl -X POST http://localhost:8080/schedule/task-每日销售报表/disable   -H "X-API-Key: your-api-key"
```

**4. 执行结果通知：**

任务执行完成后，系统自动向配置的通知渠道推送结果：
- **成功** → `notify_on_success` 列表中的渠道
- **失败** → `notify_on_failure` 列表中的渠道

---

## 十一、常见问题

### Q1: 启动时报错 "配置文件不存在，请先运行 'groot init' 初始化"

**原因：** 未初始化工作目录。

**解决：**
```bash
groot init
```

---

### Q2: 启动时报错 "环境变量 OPENAI_API_KEY 未设置"

**原因：** 配置文件中 api_key 使用环境变量引用 `${OPENAI_API_KEY}`，但环境变量未设置。

**解决：**
```bash
export OPENAI_API_KEY="your-api-key"
```
或在配置文件中直接填写 api_key（不使用环境变量引用）。

---

### Q3: 多轮对话时 Agent 没记住之前的内容

**原因：** session_id 传错或会话不存在。

**解决：** 确保每次继续对话时传入正确的 `X-Session-ID`。

---

### Q4: 请求返回 429 或同一会话并发调用报错

**原因：**
- `429 rate_limited`：请求频率超过配置的 QPS 或并发限制（启用 `rate_limit` 时）
- `409 chat_limit_exceeded`：同一会话只能有一个活跃对话，防止执行冲突

**解决：**
- 降低请求频率，等待当前请求完成后再发起下一条
- 可通过配置文件调整 `rate_limit.default_qps` 和 `rate_limit.default_concurrency`

---

### Q5: 附件上传失败

**原因：** 附件类型不允许或大小超限。

**解决：** 检查 `allowed_types` 配置和 `max_size` 限制。

---

### Q6: 认证失败 401 Unauthorized

**原因：** API Key 无效或未携带。

**解决：**
1. 确认配置了正确的 API Key
2. 检查请求头是否携带 `X-API-Key`

---

### Q7: 会话数据如何清理

**说明：** 会话数据会自动清理。

- 每天在 `cleanup_schedule` 时间执行清理
- 清理超过 `retention_days` 天的会话（依据目录最后修改时间）

---

### Q8: 如何创建定时任务

**说明：** 定时任务通过对话创建，不使用 API 直接创建。

- 在对话中用自然语言描述需求（如「每天早上 9 点帮我生成报表」）
- Agent 自动调用 `schedule_create` 工具创建任务
- 创建的任务保存到 `schedules/active/` 目录，自动注册到调度器
- 可使用 CLI 或 REST API 管理已创建的任务（查看/禁用/启用/归档/删除）

---

### Q9: 定时任务支持哪些调度格式

**三种调度格式：**

| 格式 | 示例 | 说明 |
|------|------|------|
| Cron 表达式 | `0 9 * * *` | 每天 9 点执行 |
| ISO8601 时间戳 | `2026-06-01T09:00:00Z` | 一次性任务，指定时间执行一次后自动归档 |
| Go Duration | `30m` / `1h` / `2h30m` | 间隔重复执行，从启动时开始计时 |

---

### Q10: 定时任务执行时如何与普通对话区分

- 调度执行的会话 ID 以 `-sched` 为后缀：`{task_id}-{timestamp}-sched`
- 执行记录中 `trigger_type` 字段标识触发方式：`cron`/`once`/`interval`/`manual`
- 通过 `GET /sess/history` 可查看所有会话，含定时任务产生的会话

---

### Q11: 通知渠道如何配置

1. 在 `config.yaml` 中启用所需 sender（webhook / email）
2. 创建任务时通过 `notify_on_success` / `notify_on_failure` 指定渠道
3. stdout sender 始终启用，无需配置

示例：任务成功时推送到 webhook，失败时推送到 webhook + email

---

### Q12: 配置修改后需要重启吗

**需要重启的配置：**
- LLM、Server、Security、Rate Limit、Memory、Logging、Schedule、Message 配置
- MCP 配置文件（`mcp/*.json`）

**不需要重启的配置：**
- Skills（`SKILL.md`）：支持热加载
- GROOT.md：支持热加载
- 定时任务（`schedules/active/*.json`）：由 sync 机制自动同步，无需重启

---

## 附录

### A. 环境变量

**固定环境变量（程序识别）：**

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `GROOT_HOME` | 工作目录 | `~/.groot` |

**用户自定义环境变量：**

配置文件中使用 `${VAR_NAME}` 引用的环境变量，变量名由用户自定义。以下是常见示例：

| 变量（示例） | 用途 | 必需性 |
|------|------|------|
| `OPENAI_API_KEY` | OpenAI API 密钥 | 配置文件有 `${OPENAI_API_KEY}` 时需设置 |
| `ANTHROPIC_API_KEY` | Anthropic API 密钥 | 配置文件有 `${ANTHROPIC_API_KEY}` 时需设置 |
| `GROOT_API_KEY` | 认证密钥 | 启用认证且配置文件有引用时需设置 |

> **判断方法：** 查看配置文件中是否使用 `${VAR_NAME}` 格式引用。如果引用了某个变量，则需设置对应的环境变量；如果配置文件直接写明文密钥，则不需要设置环境变量。变量名可自定义。

### B. 文件路径约定

**固定目录（不可配置）：**

| 路径 | 说明 |
|------|------|
| `{GROOT_HOME}/config.yaml` | 配置文件 |
| `{GROOT_HOME}/GROOT.md` | 项目规范文件 |
| `{GROOT_HOME}/skills/{name}/SKILL.md` | Skill 定义文件 |
| `{GROOT_HOME}/mcp/{name}.json` | MCP 配置文件 |
| `{GROOT_HOME}/schedules/active/{id}.json` | 活跃定时任务 |
| `{GROOT_HOME}/schedules/disabled/{id}.json` | 已禁用定时任务 |
| `{GROOT_HOME}/schedules/archive/{id}.json` | 已归档定时任务 |
| `{GROOT_HOME}/schedules/executions/{id}.json` | 任务执行记录 |

**可配置目录（默认位置）：**

| 路径 | 说明 |
|------|------|
| `{memoryDir}/{session_id}/history.json` | 对话历史（memoryDir 可配置） |
| `{memoryDir}/temp/` | 附件处理临时目录（固定在 memory 目录下） |
| `{memoryDir}/{session_id}/attachments/` | 附件目录 |
| `{memoryDir}/{session_id}/chats/{chat_id}.json` | 详细执行记录 |
| `{logsDir}/groot-{date}.log` | 日志文件（logsDir 可配置） |

> **说明：** `{memoryDir}` 和 `{logsDir}` 可通过配置文件修改，默认为 `{GROOT_HOME}/memory` 和 `{GROOT_HOME}/logs`。

### C. 错误码速查表

| HTTP 状态码 | 错误码 | 说明 |
|------------|--------|------|
| 400 | `invalid_request` | 请求参数错误 |
| 400 | `attachment_count_exceeded` | 附件数量超限 |
| 400 | `attachment_type_not_allowed` | 附件类型不允许 |
| 400 | `attachment_size_exceeded` | 附件大小超限 |
| 401 | `unauthorized` | API Key 无效或缺失 |
| 403 | `forbidden` | 权限不足 |
| 409 | `chat_limit_exceeded` | 会话已有对话执行中 |
| 429 | `rate_limited` | 请求频率超过限制（QPS 或并发超限），稍后重试 |
| 500 | `config_error` | 配置错误 |
| 500 | `llm_connection_error` | LLM 连接失败 |
| 500 | `tool_call_error` | 工具调用失败 |
| 404 | `task_not_found` | 定时任务不存在 |
| 500 | `schedule_error` | 定时任务操作失败 |

### D. 联系与支持

- GitHub: https://github.com/zfd81/groot
- 问题反馈: GitHub Issues