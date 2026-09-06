<p>&nbsp;</p>

<img src="groot.png" alt="Groot Logo" width="108" style="width:104px" align="left" hspace="12">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="groot-title-dark.png">
  <img src="groot-title.png" width="271" alt="Groot AI Agent">
</picture>

**面向业务系统的 AI Agent 服务**

通过 REST API 接入，让你的系统立刻拥有智能任务执行能力  
理解指令 · 调用工具 · 自主完成任务

<img alt="Version" src="https://img.shields.io/badge/version-1.0.0-blue"> <img alt="License" src="https://img.shields.io/badge/license-MIT-green"> <img alt="Go" src="https://img.shields.io/badge/Go-1.21+-00ADD8">

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
| **多 Agent 协作** | 支持声明子 Agent，主 Agent 自动编排调度，也可直连指定子 Agent（Solo 模式） |
| **多模型切换** | 支持创建多个 LLM 模型（Web UI 管理），按请求通过 `X-Model-Name` 指定，Web 界面可视化切换 |
| **流式进度反馈** | 实时返回执行过程和结果，调用方全程可见 |
| **定时任务调度** | 通过对话创建定时任务，系统在指定时间自动执行并推送通知 |
| **消息通知** | 支持 webhook / email / stdout 多渠道通知，任务完成/失败自动推送 |
| **热插拔扩展** | Skills 支持动态添加，无需重启服务 |
| **数据库后端** | 运行数据统一存储在数据库中，默认 SQLite 零配置，可切换 MySQL/PostgreSQL 支持多实例集群部署 |
| **速率限制** | 支持按 API Key 的 QPS 和并发数限制，防止滥用 |
| **Web 界面** | 内置图形化界面，浏览器访问 `/ui` 即可聊天、查看会话与服务状态，无需额外部署 |

### 1.3 会话与对话

**会话（Session）：**
- 会话是多轮对话的容器，每个会话有唯一的 `session_id`
- 会话内的所有对话共享历史上下文，Agent 能记住之前的交流
- 会话数据统一存储在数据库中（默认 SQLite，可切换 MySQL/PostgreSQL）

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
│  │ (数据库)    │  │             │  │ (gocron)    │          │
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

## 二、安装部署

### 2.1 系统要求

| 要求 | 说明 |
|------|------|
| 操作系统 | Linux / macOS / Windows |
| Go 版本 | Go 1.21+（仅源码编译需要） |
| 内存 | 建议 512MB+ |
| 磁盘 | 建议 1GB+（用于附件存储和会话数据） |

### 2.2 安装方式

#### 方式一：直接运行（推荐）

从仓库 `dist/` 目录下载对应平台的压缩包，解压即得可执行文件：

```bash
# Linux
wget https://github.com/zfd81/groot/raw/master/dist/groot-linux-amd64.zip
unzip groot-linux-amd64.zip
chmod +x groot
mv groot /usr/local/bin/groot

# macOS
wget https://github.com/zfd81/groot/raw/master/dist/groot-darwin-arm64.zip
unzip groot-darwin-arm64.zip
chmod +x groot
mv groot /usr/local/bin/groot

# Windows
# 下载 https://github.com/zfd81/groot/raw/master/dist/groot-windows-amd64.zip
# 解压后得到 groot.exe
```

#### 方式二：源码编译

```bash
# 克隆仓库
git clone https://github.com/zfd81/groot.git
cd groot

# 编译当前平台
go build -o dist/groot ./cmd/groot

# 或使用 Makefile
make build            # 编译当前平台（含 Web 界面）
make build-all        # 编译所有平台并打包 zip（macOS/Linux/Windows，含 Web 界面）

# 运行
./dist/groot
```

> **关于 Web 界面：** Web 前端在 Go 编译时通过 `go:embed` 嵌入二进制。`make build` 与
> `make build-all` 会先构建前端（需要 Node.js 18+）再编译，其中 `make build-all` 前端只构建一次，
> 三个平台共享同一份产物。`make build-darwin` / `build-linux` / `build-windows` / `build-go`
> **不会**构建前端，直接复用 `web/dist/` 中现有的产物——全新 clone 后单独执行这些命令得到的
> 二进制不包含 Web 界面（访问 `/ui/` 显示未构建提示页，API 功能不受影响）；若之前构建过前端，
> 则嵌入的是该版本。修改前端代码后，需重新执行 `make web`（或 `make build`）才能更新界面。

**Makefile 编译命令：**

| 命令 | 说明 |
|------|------|
| `make build` | 构建前端 + 编译当前平台可执行文件（`dist/groot`） |
| `make build-all` | 构建前端 + 编译三个平台并打包为 zip |
| `make build-darwin` | 编译 macOS ARM64 并打包（复用现有前端产物） |
| `make build-linux` | 编译 Linux AMD64 并打包（复用现有前端产物） |
| `make build-windows` | 编译 Windows AMD64 并打包（复用现有前端产物） |
| `make build-go` | 仅编译当前平台后端（复用现有前端产物，无需 Node.js） |
| `make web` | 仅构建 Web 前端（输出到 `web/dist/`） |
| `make clean` | 清理编译产物（删除 `dist/`） |

**编译产物（`make build-all`）：**

| 文件 | 平台 | 解压后 |
|------|------|--------|
| `dist/groot-darwin-arm64.zip` | macOS ARM64 | `groot` |
| `dist/groot-linux-amd64.zip` | Linux AMD64 | `groot` |
| `dist/groot-windows-amd64.zip` | Windows AMD64 | `groot.exe` |

三个 zip 同时作为发布产物提交到仓库（即「方式一」的下载来源）；开发用的单平台二进制 `dist/groot` 不入库。

> **关于分发：** zip 内不含目录层级，解压即得可执行文件。编译产物是**单文件自包含程序**，Web 界面与 SQLite 引擎都编译进了同一个二进制，
> 目标机器无需安装 Node.js、SQLite、C 运行库或任何第三方组件，拷贝过去直接运行即可。
> 全部数据库驱动（SQLite / MySQL / PostgreSQL）均为纯 Go 实现，不需要 cgo，
> 因此 `make build-all` 可在一台开发机上一次性交叉编译出三个平台的完整可用产物。

### 2.3 工作目录结构

Groot 启动时会创建一个工作目录（Home 目录），默认位置为 `~/.groot`，可通过环境变量 `GROOT_HOME` 更改。

```
{GROOT_HOME}/
├── config.yaml                    # 主配置文件（业务配置）
├── env.yaml                       # 环境配置文件（数据库等基础设施凭据）
├── GROOT.md                       # 项目规范文件（自动注入系统指令）
├── groot.db                       # SQLite 数据库文件（默认模式；MySQL/PG 模式下无此文件）
├── skills/                        # Skills 目录
│   └── {skill-name}/SKILL.md      # Skill 定义文件
├── mcp/                           # MCP 配置目录
│   └── {mcp-name}.json            # MCP 配置文件
├── subagents/                     # 子 Agent 目录
│   └── {agent-name}/              # 单个子 Agent
│       ├── agent.md               # 子 Agent 定义文件（frontmatter + 系统提示词）
│       ├── mcp/                   # 子 Agent 专属 MCP 配置（可选）
│       │   └── {mcp-name}.json
│       └── skills/                # 子 Agent 专属 Skills（可选）
│           └── {skill-name}/SKILL.md
├── logs/                          # 日志目录
│   └── groot-{date}.log           # 日志文件
```

> **运行时数据存储在数据库中：** 会话与对话历史、附件内容、定时任务及其执行记录、集群成员注册信息统一存储在数据库中（默认 SQLite，文件为 `{GROOT_HOME}/groot.db`；可通过 `env.yaml` 切换为 MySQL/PostgreSQL），没有对应的文件目录。

### 2.4 目录说明

**固定目录/文件（不可配置）：**

| 目录/文件 | 说明 |
|----------|------|
| `config.yaml` | 主配置文件，控制服务行为 |
| `env.yaml` | 环境配置文件，存放数据库等基础设施连接凭据，与业务配置解耦 |
| `GROOT.md` | 项目规范文件，自动注入系统指令最前面，支持热加载 |
| `skills/` | Skills 定义目录（固定位置），支持热插拔 |
| `mcp/` | MCP 工具配置目录（固定位置），修改需重启服务 |
| `subagents/` | 子 Agent 定义目录（固定位置），存放各子 Agent 的 `agent.md` 与专属 mcp/skills |

**可配置目录（支持相对/绝对路径）：**

| 目录/文件 | 说明 |
|----------|------|
| `logs/` | 日志存储目录（默认位置），可通过 `logging.file.directory` 配置 |

> **说明：** `logs` 目录支持通过配置文件修改位置，详见 [四、配置详解](#四配置详解)。固定目录（skills/mcp/subagents）位置不可更改。

### 2.5 工作目录配置方式

| 方式 | 示例 | 优先级 |
|------|------|--------|
| 环境变量 | `export GROOT_HOME=/opt/groot` | 高 |
| 默认值 | `~/.groot` | 低 |

### 2.6 环境变量

**固定环境变量：**

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `GROOT_HOME` | 工作目录 | `~/.groot` |

**用户自定义环境变量：**

配置文件与 Web UI 模型配置中 `${VAR_NAME}` 引用的变量名由用户自定义，是否需要设置取决于填写方式：

```bash
# 示例（变量名可自定义）
export OPENAI_API_KEY="sk-xxxx"
export DEEPSEEK_API_KEY="sk-xxxx"
```

> **判断方法：** 配置中有 `${VAR_NAME}` 引用则需设置，直接写密钥则不需要。

### 2.7 配置文件

`groot init` 会生成两个配置文件：

| 文件 | 用途 |
|------|------|
| `~/.groot/config.yaml` | 业务配置（服务端口、安全、日志等），包含完整注释模板 |
| `~/.groot/env.yaml` | 基础设施环境配置（数据库连接凭据），默认全注释即 SQLite 本地模式 |

`config.yaml` 中所有配置项（server、react、attachment、memory、security、logging 等）均已注释并标注默认值，按需取消注释即可。

> **模型配置不在配置文件中**：模型配置通过 Web UI 管理，登录后进入 设置 → 模型，可创建、编辑、删除模型，切换默认模型，启用/禁用模型并测试连接。API Key 支持填写 `${ENV_VAR}` 引用环境变量。

> 完整配置项说明见 [四、配置详解](#四配置详解)，数据库配置见 [4.7 数据库配置（env.yaml）](#47-数据库配置envyaml)。

### 2.8 停止服务

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

## 三、快速开始

> 如果还没安装 Groot，请先查看 [二、安装部署](#二安装部署)。

### 3.1 初始化

```bash
groot init
```

初始化会在 `~/.groot` 下生成 `config.yaml`、`env.yaml`、`GROOT.md` 及资源目录。数据默认使用 SQLite 本地存储，零配置即可运行；如需 MySQL/PostgreSQL，见 [4.7 数据库配置](#47-数据库配置envyaml)。

### 3.2 启动服务

```bash
groot
```

### 3.3 打开 Web 界面

服务启动后，浏览器访问：

```
http://localhost:8080/ui/
```

即可使用图形化界面聊天、查看历史会话与服务状态。界面内容随二进制一起分发，无需单独部署前端。

**主要功能：**

| 功能 | 说明 |
|------|------|
| 聊天 | 与 Agent 对话，流式输出，展示思考过程与工具调用详情；支持上传附件（含预览与删除）、切换模型与 Agent、中断执行中的对话 |
| 会话管理 | 侧边栏查看历史会话列表、继续会话、分页加载 |
| 会话搜索 | 侧边栏搜索图标或快捷键 `Ctrl`/`⌘` + `K`，按关键词搜索历史对话的指令与执行结果，点击结果跳转到对应会话并定位轮次 |
| 会话日志 | 顶部栏「查看日志」按钮，查看当前会话的运行日志（扫描最近 7 天，最多 1000 条），支持按级别过滤 |

**设置界面导航**（右上角进入）：

| 菜单 | 说明 |
|------|------|
| 通用 | 界面语言（中文/English）、外观主题（浅色/深色/跟随系统）、运行环境信息（工作目录、数据库类型、日志目录） |
| 模型 | 模型管理（创建、编辑、删除、启用/禁用、测试连接、切换默认），见 [3.4 创建模型](#34-创建模型) |
| Agents | 以卡片展示主 Agent 与所有子 Agent，每个卡片可查看定义文件（`GROOT.md` / `agent.md`）、Skills 列表和 MCP 工具 |
| API Keys | API Key 管理（创建、查看、复制、删除），见 [3.5 第一次调用](#35-第一次调用) |
| 集群管理 | 查看集群成员列表（地址、角色、进程 PID、心跳时间，Leader 排首位）；MySQL/PostgreSQL 多实例模式下使用，SQLite 单机模式下为空 |
| 修改密码 | 修改登录密码 |

Web 界面登录认证始终启用：

- **首次使用**：用户表为空时自动进入「创建用户」页面，输入用户名、密码（至少 8 位）和确认密码完成创建，随后跳转登录页登录。
- **日常使用**：输入用户名和密码登录。登录会话有效期 1 小时，活跃使用时自动续期。
- **修改密码**：在「设置 → 修改密码」中输入原始密码、新密码和确认新密码。修改成功后其他浏览器的登录会话立即失效。
- **重置用户**：忘记密码时在服务器上执行 `groot user reset`（删除用户表全部数据），重启服务后重新进入创建用户流程。

> 用户名和密码保存在数据库中（密码以 bcrypt 加密存储），无需任何配置。
> 经 https 反向代理部署时，会话 Cookie 会根据 `X-Forwarded-Proto` 自动置 `Secure`。

### 3.4 创建模型

模型配置通过 Web UI 管理：登录后进入 **设置 → 模型**，点击「新建模型」，填写模型名称、API 地址（`base_url`）、API 密钥（`api_key`）和 Model ID 后保存。首个创建的模型自动成为默认模型。

- 可创建、编辑、删除模型，切换默认模型，启用/禁用模型并测试连接
- API Key 支持填写 `${ENV_VAR}` 引用环境变量，例如填写 `${OPENAI_API_KEY}` 后：

```bash
export OPENAI_API_KEY="sk-xxxx"
```

> **从旧版本升级**：原 `config.yaml` 中的 `llm` 配置不再生效，需登录 Web UI 在 设置 → 模型 中重新创建模型。

### 3.5 第一次调用

API 认证始终开启，调用前先创建 API Key：登录 Web 界面，进入 **设置 → API Keys**，创建时设置名称、过期时间与权限范围，创建后复制 Key。

```bash
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: 在Web界面创建的APIKey" \
  -d '{"instruction": "你好，请介绍一下你自己"}'
```

> 更多安装方式见 [二、安装部署](#二安装部署)，完整配置说明见 [四、配置详解](#四配置详解)，API 详细说明见 [七、REST API](#七rest-api)。

---

## 四、配置详解

### 4.1 配置文件位置

配置文件由 `groot init` 生成：

- `{GROOT_HOME}/config.yaml`：业务配置（本节 4.2~4.6 的全部内容）
- `{GROOT_HOME}/env.yaml`：基础设施环境配置（数据库连接，见 [4.7](#47-数据库配置envyaml)）

未初始化直接启动会报错并提示先运行 `groot init`。

### 4.2 完整配置文件示例

```yaml
# Groot Agent 配置文件

# Agent 基础配置
agent:
  name: groot                      # Agent 名称
  version: 1.0.0                   # Agent 版本号

# HTTP 服务配置
server:
  host: 0.0.0.0                    # 服务监听地址
  port: 8080                       # 服务监听端口

# 模型配置通过 Web UI 管理（登录后进入 设置 → 模型），不在本文件中配置

# ReAct 执行配置
react:
  max_iterations: 20               # ReAct 循环最大迭代次数
  step_timeout: 60                 # 单步 LLM 调用超时（秒）
  error_retry: 2                   # 单步 LLM 调用失败重试次数

# 附件处理配置
attachment:
  max_size: 50                     # 单个附件最大大小（MB）
  max_total_size: 100              # 附件总大小上限（MB）
  max_count: 10                    # 附件数量上限
  allowed_types: []                # 允许的附件类型（空数组表示允许所有类型）

# 记忆模块配置
memory:
  history_window: 20               # LLM 上下文窗口（轮次），-1 表示不限制

# 定时任务调度配置
schedule:
  enabled: false                   # 是否允许在对话中创建定时任务（默认关闭）
  max_concurrent_tasks: 3          # 最大并发执行任务数
  sync_interval: 30s               # 定期同步间隔（对比数据库任务与调度器状态，修复不一致）

# 消息通知配置
message:
  queue_size: 256                  # 消息队列容量
  workers: 2                       # 消息发送 worker 数量
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

# 子 Agent 调度配置
subagent:
  max_concurrency: 5               # 同时运行的子 Agent 上限（FIFO 排队）
  exec_timeout: 5m                 # 单次子 Agent 执行超时（排队不计入）
  max_task_length: 16000           # call_agent task 参数长度上限（字符）
  max_result_length: 8000          # 子 Agent 返回文本截断长度

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
    header_name: X-API-Key         # API Key 请求头名称
    secret: "..."                  # JWT 签名密钥（init 自动生成，请勿泄露）

# 日志配置
logging:
  level: info                      # 日志级别：debug/info/warn/error
  format: json                     # 日志格式：json/text
  output: [stdout, file]           # 输出目标：stdout/file（可同时输出）
  file:
    directory: logs                # 日志文件目录
    filename_pattern: groot-{date}.log  # 文件名模式，{date} 替换为 YYYY-MM-DD
    max_age: 7                     # 日志保留天数
```

### 4.3 配置字段详解

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

#### 模型配置（Web UI 管理）

模型配置通过 Web UI 管理：登录后进入 **设置 → 模型**，可创建、编辑、删除模型，切换默认模型，启用/禁用模型并测试连接。API Key 支持填写 `${ENV_VAR}` 引用环境变量。模型配置存储在数据库中，增删改**立即生效，无需重启**。

模型参数说明（在 Web UI 表单中填写）：

| 字段 | 必填 | 说明 |
|------|------|------|
| 模型名称 | **是** | 模型的逻辑名称，全局唯一，即 `X-Model-Name` 的取值 |
| `base_url` | **是** | LLM API 地址（OpenAI 兼容协议） |
| `api_key` | **是** | API 密钥，支持 `${VAR_NAME}` 引用环境变量 |
| `model` | **是** | 实际调用时的模型名称（Model ID） |
| `max_completion_tokens` | 否 | 最大输出 Token 数，默认 `4096` |
| `max_context_tokens` | 否 | 输入上下文 Token 预算，超出时自动截断最早的历史轮次，`0` 表示不限制，默认 `0` |
| `temperature` | 否 | 输出随机性（0.0~2.0），默认 `0.7` |
| `top_p` | 否 | 核采样系数（0.0~1.0），默认 `1.0` |
| `frequency_penalty` | 否 | 频率惩罚（-2.0~2.0），默认 `0.0` |
| `presence_penalty` | 否 | 存在惩罚（-2.0~2.0），默认 `0.0` |
| `seed` | 否 | 随机种子，`0` 表示不设置 |
| `stop` | 否 | 停止序列列表，默认空 |
| `thinking` | 否 | 深度思考模式（Qwen/DeepSeek 等），默认 `false` |

默认模型规则：

- 首个创建的模型自动成为默认模型
- 默认模型不允许删除或禁用，需先将其他模型设为默认
- 禁用的模型不可设为默认，也不会出现在聊天下拉框中

> **从旧版本升级**：原 `config.yaml` 中的 `llm` 配置不再生效，需登录 Web UI 在 设置 → 模型 中重新创建模型。

> **目录固定**：Skills 目录固定为 `{GROOT_HOME}/skills`，无需配置。Skills 热插拔天然支持，无需配置开关。

#### ReAct 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `max_iterations` | 否 | ReAct 循环最大迭代次数，默认 `20` |
| `step_timeout` | 否 | 单步 LLM 调用超时（秒），默认 `60` |
| `error_retry` | 否 | 单步 LLM 调用失败重试次数，默认 `2` |

#### Attachment 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `max_size` | 否 | 单个附件最大大小（MB），默认 `50` |
| `max_total_size` | 否 | 附件总大小上限（MB），默认 `100` |
| `max_count` | 否 | 单次请求最大附件数量，默认 `10` |
| `allowed_types` | 否 | 允许的文件扩展名列表，默认空数组（允许所有类型） |

#### Memory 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `history_window` | 否 | 注入 LLM 上下文的历史对话轮次窗口，`-1` 表示不限制，默认 `20` |

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

#### SubAgent 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `max_concurrency` | 否 | 同时运行的子 Agent 上限（全局 semaphore，超出 FIFO 排队），默认 `5` |
| `exec_timeout` | 否 | 单次子 Agent 执行超时（Go duration 格式，如 `5m`/`30s`，排队不计入），默认 `5m` |
| `max_task_length` | 否 | `call_agent` 工具 `task` 参数最大字符数，超出报错，默认 `16000` |
| `max_result_length` | 否 | 子 Agent 返回文本截断长度，超出截断并附警告，默认 `8000` |

> **说明：** 子 Agent 的目录、`agent.md` 与专属 mcp/skills 配置放在 `{GROOT_HOME}/subagents/<name>/` 下，详见 [五、扩展能力](#五扩展能力) 中的「5.3 多 Agent」。

#### Security 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `auth.header_name` | 否 | API Key 请求头名称，默认 `X-API-Key` |
| `auth.secret` | 否 | JWT 签名密钥；`groot init` 自动生成，为空时启动自动补齐。更换后所有 API Key 立即失效 |
| `rate_limit.enabled` | 否 | 是否启用速率限制，默认 `false` |
| `rate_limit.global_qps` | 否 | 全局 QPS 限制，`0` 表示不限制 |
| `rate_limit.global_concurrency` | 否 | 全局并发限制，`0` 表示不限制 |
| `rate_limit.default_qps` | 否 | 每 API Key 默认 QPS，默认 `10` |
| `rate_limit.default_concurrency` | 否 | 每 API Key 默认并发数，默认 `5`（仅 `/chat` 生效） |
| `rate_limit.cleanup_interval` | 否 | 空闲限流器清理间隔，默认 `5m` |

> **速率限制说明：**
> - **限流维度**：按 API Key 名称（caller）维度限流
> - **容错降级**：限流器配置异常时自动禁用限流，不影响服务正常启动

> **API 认证说明：**
> - **API 认证始终开启**：对外 API 请求需在请求头（默认 `X-API-Key`）中携带 API Key
> - **API Key 管理**：登录 Web 界面，进入 **设置 → API Keys** 创建；每个 Key 可设置名称、过期时间（1天/7天/1个月/半年/1年/10年）与权限范围，创建后可随时查看、复制，删除后立即失效
> - **权限点**：`chat`、`status`、`detail`、`history`、`session`、`schedule`、`all`（各权限对应的 API 见 [4.5 权限说明](#45-权限说明)）

> **Web 登录说明：**
> - Web 界面登录认证始终启用，无需配置；用户名和密码保存在数据库 `users` 表中（密码 bcrypt 加密），首次访问 Web 界面时引导创建
> - 登录成功后下发 HttpOnly Cookie（`groot_web_session`），会话令牌保存在服务端内存中，进程重启后需重新登录；会话有效期固定 1 小时，活跃访问自动续期
> - 会话 Cookie 的 `Secure` 标志自动判断：TLS 直连或反向代理注入 `X-Forwarded-Proto: https` 时置位
> - 受 API 认证保护的端点同时接受 Cookie 与 `X-API-Key` 两种凭证，程序化调用不受影响；Web 会话通过后即赋予 `all` 等效权限（登录用户即管理员）
> - 登录失败限速以真实 TCP 对端地址为来源键（不采信 `X-Forwarded-For`）；同一来源在 10 分钟滑动窗口内失败达 5 次后暂时拒绝登录，返回 `429`；另设全局兜底，窗口内所有来源合计失败过多时一律锁定
> - 忘记密码时在服务器上执行 `groot user reset` 重置用户，重启服务后重新创建

#### Logging 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `level` | 否 | 日志级别：`debug`/`info`/`warn`/`error`，默认 `info` |
| `format` | 否 | 日志格式：`json`/`text`，默认 `json` |
| `output` | 否 | 输出目标：`[stdout, file]`，可同时输出 |
| `file.directory` | 否 | 日志文件目录，默认 `logs` |
| `file.filename_pattern` | 否 | 文件名模式，`{date}` 替换为 YYYY-MM-DD |
| `file.max_age` | 否 | 日志保留天数，默认 `7` |

### 4.4 目录配置说明

所有目录配置支持相对路径和绝对路径：

- **相对路径**：相对于 `~/.groot` 目录（GROOT_HOME）
- **绝对路径**：直接使用指定路径

示例配置：

```yaml
# 相对路径示例（目录位于 ~/.groot/logs）
logging:
  file:
    directory: logs

# 绝对路径示例（目录位于 /data/logs）
logging:
  file:
    directory: /data/logs
```

可配置的目录包括：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `logging.file.directory` | `logs` | 日志文件目录（支持相对/绝对路径） |

**固定目录（不可配置）：**

| 目录 | 位置 | 说明 |
|------|------|------|
| `skills` | `{GROOT_HOME}/skills` | Skills 定义目录 |
| `mcp` | `{GROOT_HOME}/mcp` | MCP 配置目录 |
| `subagents` | `{GROOT_HOME}/subagents` | 子 Agent 定义目录 |

### 4.5 权限说明

| 权限 | 对应 API | 说明 |
|------|---------|------|
| `chat` | POST /chat | 执行对话 |
| `status` | GET /chat/status/{sid} | 查询对话状态 |
| `detail` | GET /chat/{sid}、GET /chat/{sid}/{cid} | 查询对话详情 |
| `session` | GET /sess/{sid}、GET /sess/search | 查询会话详情、搜索历史对话 |
| `history` | GET /sess/history | 查询会话列表 |
| `schedule` | GET/POST/DELETE /schedule | 管理定时任务 |
| `all` | 以上全部 | 全部权限 |

> `GET /web/health` 不需要认证和权限，可直接访问（`groot status` 也使用该端点）。

### 4.6 配置热更新

**支持热更新的配置：**
- Skills 配置：修改 SKILL.md 文件自动生效
- GROOT.md：每次对话按需读取，修改后下次对话自动生效

**不支持热更新的配置：**
- LLM 配置、Server 配置、Security 配置、Rate Limit 配置、Memory 配置、Logging 配置、Schedule 配置、Message 配置需重启服务
- MCP 配置：修改 `{GROOT_HOME}/mcp/*.json` 文件需重启服务
- 数据库配置（`env.yaml`）需重启服务

---

### 4.7 数据库配置（env.yaml）

Groot 的运行时数据（会话与对话历史、附件、定时任务及执行记录、集群成员信息）统一存储在数据库中。数据库连接凭据存放在 `{GROOT_HOME}/env.yaml`，与业务配置 `config.yaml` 解耦。

**三种模式：**

| 模式 | 配置方式 | 适用场景 |
|------|---------|---------|
| SQLite（默认） | `env.yaml` 全注释（无 `database` 节），零配置 | 单机部署，数据文件为 `{GROOT_HOME}/groot.db` |
| MySQL | 取消 MySQL 示例块注释并填写连接信息 | 多实例集群部署、集中管理数据 |
| PostgreSQL | 取消 PostgreSQL 示例块注释并填写连接信息 | 多实例集群部署、集中管理数据 |

**配置示例（MySQL）：**

```yaml
database:
  driver: mysql
  dsn: "user:${GROOT_DB_PASSWORD}@tcp(host:3306)/groot?charset=utf8mb4&parseTime=True&loc=UTC"
  max_open_conns: 20                   # 最大打开连接数（默认 20）
  max_idle_conns: 5                    # 最大空闲连接数（默认 5）
  conn_max_lifetime: 30m               # 连接最大生命周期（默认 30m）
```

**配置示例（PostgreSQL）：**

```yaml
database:
  driver: postgres
  dsn: "host=host port=5432 user=groot password=${GROOT_DB_PASSWORD} dbname=groot sslmode=disable TimeZone=UTC"
  max_open_conns: 20
  max_idle_conns: 5
  conn_max_lifetime: 30m
```

**字段说明：**

| 字段 | 必需 | 说明 |
|------|------|------|
| `driver` | 是 | 数据库驱动：`mysql` / `postgres`（缺省整节即 SQLite 模式） |
| `dsn` | 是 | 连接字符串，密码等敏感信息建议用 `${ENV_VAR}` 引用环境变量 |
| `max_open_conns` | 否 | 最大打开连接数，默认 `20` |
| `max_idle_conns` | 否 | 最大空闲连接数，默认 `5` |
| `conn_max_lifetime` | 否 | 连接最大生命周期，默认 `30m` |

**注意事项：**

- `env.yaml` 中同一时间只能存在一个 `database` 节（MySQL/PostgreSQL 二选一）
- 即使 `config.yaml` 中残留数据库相关配置也不再生效，数据库连接只认 `env.yaml`
- MySQL/PostgreSQL 模式下，多实例共享同一数据库即组成集群，自动进行 Leader 选举（Leader 负责定时任务调度）；成员状态可在 Web 界面 **设置 → 集群管理** 中查看
- MySQL/PostgreSQL 模式下可使用 `groot push` / `groot pull` / `groot diff` 在本地目录与数据库之间同步 skills、subagents、mcp 等配置资源，详见 [6.9 配置同步](#69-配置同步pushpulldiff)

---

### 4.8 项目规范文件（GROOT.md）

Groot 支持在 `{GROOT_HOME}/GROOT.md` 文件中定义项目规范，这些规范会自动注入到每次对话的系统指令最前面。

**功能特点：**
- 无需配置开关，默认启用
- 修改后下次对话自动生效（按需读取，无需重启）
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
GROOT.md（按需读取）
→ prompt（用户传入）
→ Skills 指令
→ 执行规则
```

---

## 五、扩展能力

本章介绍 Groot 的三个扩展能力：Skills、MCP 工具、多 Agent。

### 5.1 Skills 配置

Skills 目录固定位于 `{GROOT_HOME}/skills`，无需在配置文件中指定。

#### 5.1.1 Skills 目录结构

```
{GROOT_HOME}/skills/
├── pdf_analyzer/
│   └── SKILL.md
├── code_generator/
│   └── SKILL.md
└── data_analyzer/
    └── SKILL.md
```

#### 5.1.2 Skill 文件格式

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

#### 5.1.3 热插拔机制

Skills 热插拔由 eino Backend 无缓存设计天然支持，无需配置或重启：

- 新增 Skill：创建目录和 `SKILL.md` 文件，下次 Agent 调用时自动生效
- 修改 Skill：编辑 `SKILL.md` 内容，下次 Agent 调用时自动生效
- 删除 Skill：删除对应目录，下次 Agent 调用时自动生效

---

### 5.2 MCP 工具配置

#### 5.2.1 MCP 配置目录（固定位置）

MCP 配置目录固定位于 `{GROOT_HOME}/mcp`，无需在配置文件中指定。

```
{GROOT_HOME}/mcp/
├── database_tool.json     # 数据库查询工具（stdio 类型）
├── web_parser.json        # 网页解析服务（sse 类型）
└── web_search.json        # 网络搜索服务（streamable_http 类型）
```

每个 MCP 工具使用独立的 JSON 配置文件。添加、修改或删除 MCP 配置后需要重启服务才能生效。

#### 5.2.2 连接类型

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `stdio` | 标准输入输出通信 | 本地命令行工具（如数据库客户端） |
| `sse` | Server-Sent Events（单向推送） | 远程 HTTP 服务，服务端主动推送事件 |
| `streamable_http` | Streamable HTTP（双向流式） | 远程 HTTP 服务，支持请求和响应双向流式 |

#### 5.2.3 MCP 配置示例

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

### 5.3 多 Agent

Groot 支持在 `~/.groot/subagents/` 下声明子 Agent，每个子 Agent 拥有独立的系统提示词、MCP 工具和 Skills。主 Agent（`groot`）可以根据指令自动调度子 Agent，也可由调用方直接指定子 Agent 执行。

#### 5.3.1 目录结构

```
~/.groot/subagents/db-agent/
├── agent.md          # 必填：frontmatter 含 description；正文为系统提示词
├── mcp/              # 可选：专属 MCP 配置（与主 Agent 隔离）
└── skills/           # 可选：专属 Skills（与主 Agent 隔离）
```

`agent.md` 示例：

```markdown
---
description: 数据库查询专家，擅长 SQL 编写与解读
model: kimi-k2.5
---

# 数据库 Agent

请直接基于数据库 schema 给出 SQL 查询，避免冗余解释。
```

frontmatter 字段：

| 字段 | 必填 | 说明 |
|------|------|------|
| `description` | 是 | 子 Agent 用途说明，主 Agent 编排时据此选择调用哪个子 Agent |
| `model` | 否 | 钉死特定模型（覆盖运行期跟随逻辑），值需为 Web UI 中已创建的模型名称；省略时跟随主 Agent 当前 model |

注：子 Agent 目录名不能为 `groot`（主 Agent 保留名）；缺 `description` 或 `agent.md` 的目录会在启动期被跳过并记录日志。

#### 5.3.2 调用方式

**编排模式（默认）**：主 Agent 根据 GROOT.md 引导段与子 Agent 描述，通过 `call_agent` 工具自动调度：

```bash
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: 在Web界面创建的APIKey" \
  -d '{"instruction":"查询昨天的订单总金额"}'
```

**Solo 模式**：调用方通过 `X-Agent-Name` header 直接指定子 Agent，跳过主 Agent 编排：

```bash
curl -X POST http://localhost:8080/chat \
  -H "X-Agent-Name: db-agent" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: 在Web界面创建的APIKey" \
  -d '{"instruction":"查询昨天的订单总金额"}'
```

`X-Agent-Name: groot` 等价于不传 header（走主 Agent）。指定未注册名时返回 HTTP 400。

#### 5.3.3 子 Agent 的 Model 选择

子 Agent 在调用 LLM 时，按以下优先级决定使用哪个 model：

1. **`agent.md` 的 `model` 字段**（最高优先级）：显式钉死特定模型，无视运行期切换
2. **主 Agent 当前 model**（编排模式默认）：编排模式下子 Agent 跟随主 Agent 实际选用的 model；Web 界面切换主 Agent 的 model 后，再触发的子 Agent 调用就用新 model
3. **默认模型**（兜底）：以上两者都缺时使用 Web UI 中设置的默认模型

Solo 模式（`X-Agent-Name` 直连子 Agent）下，第 2 步的"主 Agent 当前 model"取 `X-Model-Name` 请求头或默认模型，逻辑相同。

实际效果：

| 场景 | 子 Agent 实际使用的 model |
|------|--------------------------|
| `agent.md` 写了 `model: kimi-k2.5`，主 Agent 用 `gpt-4o` 编排 | `kimi-k2.5`（钉死） |
| `agent.md` 不写 `model`，主 Agent 在 Web 界面选用 `gpt-4o` | `gpt-4o`（跟随） |
| `agent.md` 不写 `model`，请求未指定 model | Web UI 中设置的默认模型 |

#### 5.3.4 Web 界面切换

Web 界面输入框左下角的 Agent 下拉可切换当前会话使用的 Agent：

| 选项 | 说明 |
|------|------|
| `groot` | 主 Agent（默认，编排模式），列表首位并标注「默认」 |
| 其他 Agent 名 | 切换到指定子 Agent（Solo 模式，直连该子 Agent） |

选中 `groot` 等价于不传 `X-Agent-Name`（走主 Agent 编排）。

**编排模式下的可视化：** 主 Agent 通过 `call_agent` 调度子 Agent 时，Web 界面会将其单独渲染为「调用 Agent」步骤，而不是普通工具调用，便于一眼区分主 Agent 自身的工具调用和子 Agent 的派发。

#### 5.3.5 API 关联

| 接口 | 多 Agent 行为 |
|-----|--------------|
| `GET /chat/status/:sid` | 编排模式下 `progress.sub_agents` 含当前运行的子 Agent 列表 |

#### 5.3.6 配置项（`config.yaml`）

```yaml
subagent:
  max_concurrency: 5        # 同时运行的子 Agent 上限（FIFO 排队）
  max_task_length: 16000    # call_agent task 参数长度上限（字符）
  max_result_length: 8000   # 子 Agent 结果长度上限，超出截断
  exec_timeout: 5m          # 单次子 Agent 执行超时
```

#### 5.3.7 关键限制

- **单层调用**：主 Agent → 子 Agent 一层；子 Agent 无法再调用其它子 Agent
- **`agent.md` 与 MCP 配置不支持热加载**：变更需重启服务才能生效
- **Skills 支持热加载**：`subagents/<name>/skills/` 下变更会触发 watcher 通知（具体重扫由后续版本完善）
- **隔离性**：每个子 Agent 的 MCP / Skills 与主 Agent 完全隔离，互不可见
- **Token 计入主 Chat**：编排模式下子 Agent 消耗的 tokens 累加到父 ChatRecord

#### 5.3.8 完整示例：从零创建一个子 Agent

以创建一个「天气查询子 Agent」为例：

**1. 创建目录与 agent.md**

```bash
mkdir -p ~/.groot/subagents/weather/mcp
mkdir -p ~/.groot/subagents/weather/skills

cat > ~/.groot/subagents/weather/agent.md <<'EOF'
---
description: 查询天气信息，当用户询问天气相关问题时使用
---

# 天气查询 Agent

你是天气查询专家，根据用户提问调用 weather MCP 工具返回结果，
不要回答与天气无关的问题。
EOF
```

**2.（可选）配置专属 MCP 工具**

```bash
cat > ~/.groot/subagents/weather/mcp/api-proxy.json <<'EOF'
{
  "name": "api-proxy",
  "type": "stdio",
  "command": "uvx",
  "args": ["mcp-server-api-proxy"],
  "env": { "WEATHER_API_KEY": "your-key" }
}
EOF
```

**3. 重启服务（`agent.md` / MCP 不支持热加载）**

```bash
groot
```

**4. 验证子 Agent 已注册**

登录 Web 界面（`http://localhost:8080/ui/`），进入 **设置 → Agents** 应能看到 `weather` 卡片；点击卡片上的「查看 MCP 工具」按钮可查看 weather 加载的 MCP 工具，点击「查看 Skills」按钮可查看其 Skills。

**5. 调用：编排模式 vs Solo 模式**

```bash
# 编排模式：让主 Agent 自己决定调度 weather
curl -X POST http://localhost:8080/chat \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: 在Web界面创建的APIKey' \
  -d '{"instruction":"今天北京天气怎么样？"}'

# Solo 模式：跳过主 Agent，直连 weather
curl -X POST http://localhost:8080/chat \
  -H 'X-Agent-Name: weather' \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: 在Web界面创建的APIKey' \
  -d '{"instruction":"今天北京天气怎么样？"}'
```

**6. Web 界面中切换**

打开 Web 界面（`http://localhost:8080/ui/`），在输入框左下角的 Agent 下拉中选择目标 Agent：

- `groot`（默认）：主 Agent 编排模式
- `weather`：直连 weather 子 Agent（Solo 模式）

切换后当前会话即使用所选 Agent。

详见 [设计文档](docs/superpowers/specs/2026-05-24-multi-agent-design.md)。

---

## 六、CLI 命令参考

Groot 提供一套命令行工具用于管理服务实例、Skills 和日志。

### 6.1 命令总览

| 命令 | 说明 |
|------|------|
| `groot` | 启动 Groot 服务 |
| `groot init` | 初始化工作目录 |
| `groot status` | 查看运行中实例的状态 |
| `groot tail` | 实时日志查看 |
| `groot push` | 将本地配置推送到数据库（MySQL/PG 模式） |
| `groot pull` | 从数据库拉取配置到本地（MySQL/PG 模式） |
| `groot diff` | 显示本地与数据库的配置差异（MySQL/PG 模式） |
| `groot user reset` | 重置 Web 登录用户（删除用户表全部数据） |

**全局选项：**

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `-p, --port` | HTTP 端口 | 配置文件值 |
| `-h, --help` | 显示帮助 | - |
| `-v, --version` | 显示版本 | - |

### 6.2 启动服务（groot）

启动 Groot AI Agent 服务。

```bash
groot                      # 使用默认配置启动
groot -p 9090              # 指定端口启动
```

### 6.3 初始化工作目录（groot init）

初始化工作目录，创建必要的目录结构和配置文件。

```bash
groot init
```

创建的目录和文件：

| 目录/文件 | 说明 |
|------|------|
| `skills/` | Skills 定义目录 |
| `mcp/` | MCP 配置目录 |
| `subagents/` | 子 Agent 定义目录 |
| `logs/` | 日志文件目录 |
| `config.yaml` | 主配置文件（业务配置） |
| `env.yaml` | 环境配置文件（数据库凭据，默认全注释即 SQLite 模式） |
| `GROOT.md` | 项目规范文件（含子 Agent 调度引导段） |

> 会话、定时任务、集群等运行时数据存储在数据库中，init 不创建对应目录。

### 6.4 查看实例状态（groot status）

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

### 6.5 日志查看（groot tail）

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

### 6.6 配置同步（push/pull/diff）

在 MySQL/PostgreSQL 模式下（需配置 `~/.groot/env.yaml` 中的 `database` 节），集群共享的配置资源（`config.yaml`、skills、subagents、mcp 等）以数据库中的镜像为准。三个子命令用于在本地工作目录与数据库之间同步：

```bash
groot push                       # 将本地全部白名单资源推送到数据库
groot push config.yaml           # 只推送主配置
groot push skills/weather        # 只推送单个 skill
groot push skills subagents mcp  # 推送多个类别
groot push -y skills             # 跳过交互确认直接推送

groot pull                       # 从数据库拉取全部白名单资源到本地
groot pull skills -y             # 拉取指定资源并跳过确认

groot diff                       # 显示本地与数据库的差异（只读，不修改）
groot diff skills/weather        # 只比较指定路径
```

| 命令 | 方向 | 说明 |
|------|------|------|
| `push [path...] [-y]` | 本地 → 数据库 | 将本地 HOME 的配置镜像推送到数据库，执行前列出差异并要求确认（`-y` 跳过） |
| `pull [path...] [-y]` | 数据库 → 本地 | 将数据库的配置镜像拉取到本地 HOME，执行前列出差异并要求确认（`-y` 跳过） |
| `diff [path...]` | 只读比较 | 显示本地与数据库之间的配置差异，不做任何修改 |

> **说明：** SQLite 模式（单机）下配置直接读取本地文件，这三个命令不可用，会提示同步功能未启用。

### 6.7 重置 Web 登录用户（groot user reset）

删除数据库用户表中的全部数据。重置后再次访问 Web 界面将重新进入创建用户流程，适用于忘记密码等场景。

```bash
groot user reset      # 显示将删除的用户数量，交互确认后执行
groot user reset -y   # 跳过确认直接执行
```

> **注意：** 正在运行的服务其内存中的登录会话不会随重置立即失效，需重启服务（或等原会话过期）后生效。

## 七、REST API

### 7.1 API 列表

| API | 方法 | 用途 |
|-----|------|------|
| `/chat` | POST | 执行对话，SSE 流式返回（支持多轮对话） |
| `/chat/status/{sid}` | GET | 查询最近一次对话状态 |
| `/chat/{sid}` | GET | 查询最近一次对话详情（完整步骤记录） |
| `/chat/{sid}/{cid}` | GET | 查询指定对话详情 |
| `/sess/{sid}` | GET | 查询会话详情（完整对话历史） |
| `/sess/history` | GET | 查询会话列表 |
| `/sess/search` | GET | 搜索历史对话（关键词匹配指令与结果） |
| `/schedule` | GET | 列出所有定时任务 |
| `/schedule/:id` | GET | 查询任务详情 |
| `/schedule/:id` | DELETE | 删除定时任务 |
| `/schedule/:id/disable` | POST | 禁用定时任务 |
| `/schedule/:id/enable` | POST | 启用定时任务 |
| `/schedule/:id/archive` | POST | 归档定时任务 |
| `/schedule/:id/history` | GET | 查询任务执行历史 |
| `/web/health` | GET | 健康检查（无需认证） |

### 7.2 认证方式

API 认证始终开启，请求需在请求头携带 API Key：

```http
X-API-Key: 在Web界面创建的APIKey
```

Header 名称可通过 `security.auth.header_name` 自定义。API Key 在 Web 界面 **设置 → API Keys** 中创建与管理（可设置名称、过期时间与权限范围），删除后立即失效。

在浏览器中通过 Web 界面登录后，受 API 认证保护的端点也接受登录 Cookie 作为凭证，两者任一有效即可通过认证。

---

### 7.3 POST /chat - 执行对话（核心接口）

**请求 Header：**

| Header | 必填 | 说明 |
|--------|------|------|
| `X-Session-ID` | 否 | 会话ID（sid），为空则创建新会话；有值但会话不存在则生成新sid |
| `X-Model-Name` | 否 | 模型名称，指定本次对话使用的模型（可选值为 Web 界面 设置 → 模型 中配置的模型名称）；为空则使用 Web UI 中设置的默认模型；指定不存在或已禁用的模型返回 400 |
| `X-Agent-Name` | 否 | 直连指定子 Agent（Solo 模式，见 5.3.2）；为空或 `groot` 走主 Agent 编排；指定未注册名返回 400 |
| `X-User-ID` | 否 | 用户标识，新会话创建时记录归属用户；`GET /sess/search` 携带同一标识时只搜索该用户的会话，为空时不按用户过滤 |
| `Content-Type` | 是 | `application/json` |
| `X-API-Key` | 是 | API Key（在 Web 界面 设置 → API Keys 中创建） |

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
| `content` | 是 | Base64 编码的附件内容。所有类型均以 Base64 data URL 透传给 LLM |

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

#### SSE 事件识别规则

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

#### SSE 事件类型与处理方式

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

#### SSE 事件 JSON 结构

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

#### SSE 事件流示例

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

#### SSE 前端实现伪代码

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
```

**请求示例：**

**新会话请求：**
```bash
curl -X POST http://localhost:8080/chat \
  -H "X-API-Key: 在Web界面创建的APIKey" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "帮我分析这份PDF财务报告", "attachments": [{"type": "file", "name": "Q3_Report.pdf", "content": "base64..."}]}'
```

**继续会话请求：**
```bash
curl -X POST http://localhost:8080/chat \
  -H "X-Session-ID: 20260419103000523_a1b2" \
  -H "X-API-Key: 在Web界面创建的APIKey" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "根据刚才的分析，生成一份总结报告"}'
```

---

### 7.4 GET /chat/status/{sid} - 查询对话状态

查询指定会话中最近一次对话的运行状态。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sid` | string | 是 | 会话 ID（路径参数） |

**有对话运行中的响应：**
```json
{
  "status": "success",
  "session_id": "20260419103000523_a1b2",
  "chat": {
    "chat_id": "20260419103000523",
    "round": 4,
    "status": "running",
    "progress": {
      "current_step": 2,
      "steps_completed": 1,
      "percentage": 50,
      "sub_agents": [
        {"name": "db-agent", "status": "running"}
      ]
    },
    "started_at": "2026-04-19T10:30:00Z",
    "elapsed_time": "15s"
  }
}
```

> `progress.sub_agents` 仅在编排模式下主 Agent 正在调度子 Agent 时出现。

**无运行中对话的响应（会话存在）：**
```json
{
  "status": "idle",
  "session_id": "20260419103000523_a1b2",
  "round_count": 3,
  "last_message": {
    "round": 3,
    "chat_id": "20260419102000123",
    "status": "completed"
  },
  "chat": null
}
```

会话不存在时同样返回 `200`，`status` 为 `idle`、`round_count` 为 `0`、`chat` 为 `null`（不返回 404）。

---

### 7.5 GET /chat/{sid}/{cid} - 查询对话详情

查询指定会话中某次对话的完整详情，包括指令、结果、执行步骤记录。省略 `cid`（即 `GET /chat/{sid}`）时返回该会话最近一次对话的详情。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sid` | string | 是 | 会话 ID（路径参数） |
| `cid` | string | 否 | 对话 ID（路径参数），省略时返回最近一次对话 |

**响应示例：**
```json
{
  "status": "success",
  "session_id": "20260419103000523_a1b2",
  "chat": {
    "chat_id": "20260419103000523",
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

### 7.6 GET /sess/{sid} - 查询会话详情

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
    "session_id": "20260419103000523_a1b2",
    "created_at": "2026-04-19T10:00:00Z",
    "round_count": 2,
    "last_active_at": "2026-04-19T10:05:30Z"
  },
  "history": {
    "session_id": "20260419103000523_a1b2",
    "created_at": "2026-04-19T10:00:00Z",
    "messages": [
      {
        "round": 1,
        "chat_id": "20260419100000123",
        "timestamp": "2026-04-19T10:00:00Z",
        "instruction": "帮我分析这个数据文件",
        "result": "好的，分析结果如下...",
        "status": "completed",
        "duration": 45,
        "steps_count": 3
      },
      {
        "round": 2,
        "chat_id": "20260419100500456",
        "timestamp": "2026-04-19T10:05:00Z",
        "instruction": "生成图表",
        "result": "图表已生成...",
        "status": "completed",
        "duration": 30,
        "steps_count": 2
      }
    ]
  }
}
```

| 字段 | 说明 |
|------|------|
| `messages[].status` | 该轮对话状态：`completed` / `failed` / `cancelled` |
| `messages[].duration` | 该轮耗时（秒） |
| `messages[].steps_count` | 该轮执行步骤数 |
| `messages[].agent_name` | Solo 模式下实际执行的子 Agent 名称（编排模式下省略） |

---

### 7.7 GET /sess/history - 查询会话列表

查询所有会话列表，支持分页。参数通过 URL Query String 传递。

**请求示例：**

```http
GET /sess/history?limit=10&offset=0
X-API-Key: 在Web界面创建的APIKey
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
      "last_active_at": "2026-04-19T10:30:00Z",
      "title": "帮我分析这个数据文件"
    }
  ]
}
```

> `title` 为该会话首轮用户指令，供列表展示；无对话时为空。

---

### 7.8 GET /sess/search - 搜索历史对话

按关键词在历史对话（主 Agent 已完成轮次）的指令与执行结果中模糊搜索，返回轮次级结果，按轮次开始时间倒序。

**请求示例：**

```http
GET /sess/search?q=销售报表&limit=20
X-API-Key: 在Web界面创建的APIKey
```

**Query 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `q` | string | 是 | 搜索关键词，匹配轮次的指令（instruction）与结果（result） |
| `limit` | int | 否 | 返回数量，默认 20，最大 50 |

**请求 Header：**

| Header | 必填 | 说明 |
|--------|------|------|
| `X-User-ID` | 否 | 用户标识；携带时只搜索该用户的会话（与 `/chat` 的 `X-User-ID` 对应），为空时搜索全部会话 |

**响应示例：**

```json
{
  "status": "success",
  "results": [
    {
      "session_id": "20260419103000523_a1b2",
      "chat_id": "20260419103000523",
      "round": 2,
      "title": "帮我分析这个数据文件",
      "snippet": "…销售报表已生成完毕…",
      "matched_field": "result",
      "timestamp": 1776561000000
    }
  ]
}
```

| 字段 | 说明 |
|------|------|
| `title` | 该会话首轮用户指令 |
| `snippet` | 命中位置的上下文摘要（关键词前后各截取若干字符） |
| `matched_field` | 命中字段：`instruction`（用户指令）/ `result`（执行结果） |
| `timestamp` | 该轮开始时间（毫秒时间戳） |

> `q` 为空（或全空白）时直接返回空结果，不视为错误。
> Web 界面侧边栏的搜索入口（快捷键 `Ctrl`/`⌘` + `K`）即调用此接口，点击结果跳转到对应会话并定位轮次。

---

### 7.9 GET /web/health - 健康检查

查询服务健康状态，检查各组件运行情况。

**检查项说明：**

| 检查项 | 说明 | 检查内容 |
|-------|------|---------|
| `llm` | LLM 服务 | 实际调用 API 检查连接状态；尚未创建任何模型时状态为 `unconfigured` |
| `mcp_servers` | MCP 工具 | 各 MCP 服务状态和工具数量 |
| `skills` | Skills | 已加载 Skills 数量 |
| `memory` | 会话存储 | 当前会话数量 |
| `environment` | 运行环境 | 工作目录、数据库类型（sqlite/mysql/postgres）、日志目录 |

**响应示例（健康）：**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "healthy", "info": {"model": "gpt-4o"}},
    "mcp_servers": {"status": "healthy", "info": [{"name": "file_operations", "type": "stdio", "description": "文件操作", "tools_count": 7, "isActive": true}]},
    "skills": {"status": "healthy", "info": {"count": 4}},
    "memory": {"status": "healthy", "info": {"sessions": 10}},
    "environment": {"status": "healthy", "info": {"home_dir": "/home/user/.groot", "database": "sqlite", "log_dir": "/home/user/.groot/logs"}}
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
    "memory": {"status": "healthy", "info": {"sessions": 10}},
    "environment": {"status": "healthy", "info": {"home_dir": "/home/user/.groot", "database": "sqlite", "log_dir": "/home/user/.groot/logs"}}
  },
  "metrics": {
    "chats_running": 0
  }
}
```

---

### 7.10 GET /schedule - 列出定时任务

查询所有定时任务，支持按状态过滤。

**Query 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `status` | string | 否 | 状态过滤：`active`/`disabled`/`archive`/`all`（默认 `all`） |

**响应示例：**
```json
[
  {
    "id": "daily-report",
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

### 7.11 GET /schedule/:id - 查询任务详情

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | string | 是 | 任务 ID（路径参数） |

**响应：** 返回完整任务定义，格式同 7.10 中单条任务。

---

### 7.12 DELETE /schedule/:id - 删除任务

物理删除任务及关联文件。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | string | 是 | 任务 ID（路径参数） |

**响应示例：**
```json
{
  "status": "deleted",
  "id": "daily-report"
}
```

---

### 7.13 POST /schedule/:id/disable - 禁用任务

将任务从 `active` 移入 `disabled`，并从调度器移除。

**响应示例：**
```json
{
  "status": "disabled",
  "id": "daily-report"
}
```

---

### 7.14 POST /schedule/:id/enable - 启用任务

将任务从 `disabled` 移入 `active`，重新注册到调度器。

**响应示例：**
```json
{
  "status": "enabled",
  "id": "daily-report"
}
```

---

### 7.15 POST /schedule/:id/archive - 归档任务

将任务移入 `archive`（从任意状态）。

**响应示例：**
```json
{
  "status": "archived",
  "id": "daily-report"
}
```

---

### 7.16 GET /schedule/:id/history - 执行历史

查询某任务的执行记录。

**响应示例：**
```json
[
  {
    "task_id": "daily-report",
    "exec_time": "2026-05-11T09:00:05Z",
    "trigger_type": "cron",
    "session_id": "daily-report-20260511T090005-sched",
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

### 7.17 定时任务创建（通过对话）

定时任务通过 Agent 对话创建，用户用自然语言描述需求，Agent 调用 `schedule_create` 工具：

```
用户：「每天早上 9 点帮我生成销售报表并通过 webhook 通知我」

Agent 自动调用 schedule_create 工具创建任务：
- name: "每日销售报表"
- schedule: "0 9 * * *"
- instruction: "生成销售报表并通过 webhook 通知我"
- notify_on_success: ["webhook"]
```

内置的 8 个调度工具（`schedule_create`、`schedule_list`、`schedule_inspect`、`schedule_history`、`schedule_delete`、`schedule_disable`、`schedule_enable`、`schedule_archive`）在 `schedule.enabled: true` 时自动注册到 Agent。

---

## 八、客户端代码示例

完整的客户端代码及测试见 [`examples/`](examples/) 目录。

### 8.1 Python

```python
from groot_client import GrootClient

client = GrootClient("http://localhost:8080", "在Web界面创建的APIKey")

# 新会话
result = client.execute_chat("分析这份财报", callback=lambda t, d: print(f"[{t}] {d}"))
print(f"会话ID: {result['session_id']}")

# 继续会话
result2 = client.execute_chat("生成摘要", session_id=result["session_id"])
```

> 完整代码及 15 个测试用例：[examples/python/](examples/python/)

### 8.2 Java

```java
GrootClient client = new GrootClient("http://localhost:8080", "在Web界面创建的APIKey");

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

## 九、使用场景

### 9.1 多轮文档分析

```python
client = GrootClient("http://localhost:8080", "在Web界面创建的APIKey")

# 第1轮：上传文档并分析
result1 = client.execute_chat("分析这份财报，提取营收、利润等关键指标")
sid = result1["session_id"]

# 第2轮：追问细节
result2 = client.execute_chat("重点分析利润增长的主要原因", session_id=sid)

# 第3轮：生成报告
result3 = client.execute_chat("生成一份分析报告摘要", session_id=sid)
```

### 9.2 渐进式代码开发

```python
client = GrootClient("http://localhost:8080", "在Web界面创建的APIKey")

result1 = client.execute_chat("写一个 Python 数据处理工具类，包含 CSV 读取功能",
                              prompt="你是资深 Python 开发者")
sid = result1["session_id"]

result2 = client.execute_chat("添加数据清洗功能", session_id=sid)
result3 = client.execute_chat("写单元测试代码", session_id=sid)
```

### 9.3 定时任务自动化

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
client = GrootClient("http://localhost:8080", "在Web界面创建的APIKey")

# 创建定时任务
client.execute_chat("每天早上 9 点帮我生成前一天的销售数据报表，结果发送到 webhook")

# Agent 会自动调用 schedule_create 工具，创建 cron 任务
# 任务定义保存到数据库并自动注册到调度器
```

**3. 管理任务（API）：**

```bash
# API 管理
curl -X POST http://localhost:8080/schedule/daily-sales-report/disable   -H "X-API-Key: 在Web界面创建的APIKey"
```

**4. 执行结果通知：**

任务执行完成后，系统自动向配置的通知渠道推送结果：
- **成功** → `notify_on_success` 列表中的渠道
- **失败** → `notify_on_failure` 列表中的渠道

---

## 十、常见问题

### Q1: 启动时报错 "配置文件不存在，请先运行 'groot init' 初始化"

**原因：** 未初始化工作目录。

**解决：**
```bash
groot init
```

---

### Q2: 模型调用报错 API Key 无效或环境变量未生效

**原因：** Web UI 模型配置中 api_key 填写了环境变量引用（如 `${OPENAI_API_KEY}`），但服务进程的环境变量未设置。

**解决：**
```bash
export OPENAI_API_KEY="your-api-key"
```
设置后重启服务（环境变量需在服务进程中生效），或在 Web UI 的模型配置中直接填写 api_key（不使用环境变量引用）。可在 设置 → 模型 → 编辑 中使用「测试连接」验证。

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

**原因：** API Key 无效、已过期、已删除或未携带。

**解决：**
1. 登录 Web 界面，在 设置 → API Keys 中确认 Key 存在且未过期，必要时重新创建
2. 检查请求头是否携带 `X-API-Key`（或自定义的 `auth.header_name`）

---

### Q7: 会话数据如何清理

**说明：** 会话数据长期保留在数据库中，不做内置定时清理，也没有删除会话的 API。

- 运维侧如需按时间批量清理，可直接对数据库执行 SQL（`memory_chats` 配有 `idx_started_at` 索引，`memory_sessions` 配有 `idx_updated_at` 索引）
- SQLite 模式下数据库文件为 `{GROOT_HOME}/groot.db`

---

### Q8: 如何创建定时任务

**说明：** 定时任务通过对话创建，不使用 API 直接创建（需先在 `config.yaml` 中开启 `schedule.enabled: true`）。

- 在对话中用自然语言描述需求（如「每天早上 9 点帮我生成报表」）
- Agent 自动调用 `schedule_create` 工具创建任务
- 创建的任务保存到数据库，自动注册到调度器
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
- Server、Security、Rate Limit、Memory、Logging、Schedule、Message 配置（`config.yaml`）
- 数据库配置（`env.yaml`）
- MCP 配置文件（`mcp/*.json`）
- 子 Agent 定义（`subagents/<name>/agent.md`）

**不需要重启的配置：**
- 模型配置：通过 Web UI（设置 → 模型）管理，存储在数据库中，增删改立即生效
- Skills（`SKILL.md`）：支持热加载
- GROOT.md：支持热加载
- 定时任务：存储在数据库中，由 sync 机制自动同步到调度器，无需重启

---

## 附录

### A. 环境变量

**固定环境变量（程序识别）：**

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `GROOT_HOME` | 工作目录 | `~/.groot` |

**用户自定义环境变量：**

配置文件或 Web UI 模型配置中使用 `${VAR_NAME}` 引用的环境变量，变量名由用户自定义。以下是常见示例：

| 变量（示例） | 用途 | 必需性 |
|------|------|------|
| `OPENAI_API_KEY` | OpenAI API 密钥 | Web UI 模型配置的 api_key 填写 `${OPENAI_API_KEY}` 时需设置 |
| `DEEPSEEK_API_KEY` | DeepSeek API 密钥 | Web UI 模型配置的 api_key 填写 `${DEEPSEEK_API_KEY}` 时需设置 |
| `GROOT_DB_PASSWORD` | 数据库密码 | `env.yaml` 的 DSN 中有引用时需设置 |

> **判断方法：** 查看配置文件或 Web UI 模型配置中是否使用 `${VAR_NAME}` 格式引用。如果引用了某个变量，则需设置对应的环境变量；如果直接写明文密钥，则不需要设置环境变量。变量名可自定义。

### B. 文件路径约定

**固定路径（不可配置）：**

| 路径 | 说明 |
|------|------|
| `{GROOT_HOME}/config.yaml` | 主配置文件（业务配置） |
| `{GROOT_HOME}/env.yaml` | 环境配置文件（数据库凭据） |
| `{GROOT_HOME}/GROOT.md` | 项目规范文件 |
| `{GROOT_HOME}/groot.db` | SQLite 数据库文件（默认模式） |
| `{GROOT_HOME}/skills/{name}/SKILL.md` | Skill 定义文件 |
| `{GROOT_HOME}/mcp/{name}.json` | MCP 配置文件 |
| `{GROOT_HOME}/subagents/{name}/agent.md` | 子 Agent 定义文件（frontmatter + 系统提示词） |
| `{GROOT_HOME}/subagents/{name}/mcp/{mcp-name}.json` | 子 Agent 专属 MCP 配置 |
| `{GROOT_HOME}/subagents/{name}/skills/{skill-name}/SKILL.md` | 子 Agent 专属 Skill 定义 |

**可配置目录（默认位置）：**

| 路径 | 说明 |
|------|------|
| `{logsDir}/groot-{date}.log` | 日志文件（logsDir 可通过 `logging.file.directory` 配置，默认 `{GROOT_HOME}/logs`） |

> **说明：** 会话历史、附件、定时任务、集群成员等运行时数据存储在数据库中，没有对应的文件路径。

### C. ID 格式说明

| ID 类型 | 格式 | 示例 |
|---------|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260419103000523_a1b2` |
| `chat_id` | `{YYYYMMDDHHMMSSmmm}` | `20260419103000523` |
| `task_id` | 名称转 kebab-case | `daily-report` |

**说明：**
- `session_id`：会话唯一标识，毫秒级时间戳 + 4位随机字符
- `chat_id`：单次对话标识，毫秒级时间戳
- `task_id`：定时任务唯一标识，由任务名转 kebab-case 生成（仅保留小写字母、数字和连字符）；名称无法转换（如纯中文）时回退为 `task-{纳秒时间戳}`
- 调度执行的会话 ID 格式：`{task_id}-{YYYYMMDDTHHMMSS}-sched`（`-sched` 后缀区分标识）

### D. 错误码速查表

| HTTP 状态码 | 错误码 | 说明 |
|------------|--------|------|
| 400 | `invalid_request` | 请求参数错误 |
| 400 | `attachment_count_exceeded` | 附件数量超限 |
| 400 | `attachment_type_not_allowed` | 附件类型不允许 |
| 400 | `attachment_size_exceeded` | 单个附件大小超限 |
| 400 | `attachment_total_size_exceeded` | 附件总大小超限 |
| 400 | `attachment_invalid_type` | 附件 `type` 字段无效（须为 file/image/audio/video） |
| 400 | `attachment_missing_name` | 附件缺少文件名 |
| 400 | `attachment_missing_content` | 附件缺少内容 |
| 400 | `attachment_decode_error` | 附件 Base64 解码失败 |
| 401 | `unauthorized` | API Key 无效或缺失 |
| 403 | `forbidden` | 权限不足 |
| 404 | `task_not_found` | 定时任务不存在 |
| 409 | `chat_limit_exceeded` | 会话已有对话执行中 |
| 429 | `rate_limited` | 请求频率超过限制（QPS 或并发超限），稍后重试 |
| 500 | `config_error` | 配置错误 |
| 500 | `llm_connection_error` | LLM 连接失败 |
| 500 | `tool_call_error` | 工具调用失败 |
| 500 | `schedule_error` | 定时任务操作失败 |

### E. 联系与支持

- GitHub: https://github.com/zfd81/groot
- 问题反馈: GitHub Issues

