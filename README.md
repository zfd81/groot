<table style="border: none; border-collapse: collapse;">
  <tr style="border: none;">
    <td width="200" valign="middle" style="border: none; padding: 10px;">
      <img src="groot.jpg" alt="Groot Logo" width="180">
    </td>
    <td valign="middle" style="border: none; padding: 10px;">
      <h1>Groot AI Agent</h1>
      <p><strong>面向业务系统的 AI Agent 服务</strong></p>
      <p>通过 REST API 接入，让你的系统立刻拥有智能任务执行能力<br>
      理解指令 · 调用工具 · 自主完成任务</p>
      <p>
        <img alt="Version" src="https://img.shields.io/badge/version-1.0.0-blue">
        <img alt="License" src="https://img.shields.io/badge/license-MIT-green">
        <img alt="Go" src="https://img.shields.io/badge/Go-1.21+-00ADD8">
      </p>
    </td>
  </tr>
</table>

---

## 📖 产品介绍

### 什么是 Groot

Groot 是面向业务系统的 AI Agent 服务。通过 REST API 接入，让你的系统立刻拥有智能任务执行能力——理解指令、调用工具、自主完成任务。

**一句话概括：** 把 AI Agent 能力嵌入你的业务系统，像调用普通 API 一样使用智能执行能力。

### 1.2 核心特性

| 特性 | 说明 |
|------|------|
| **多轮对话** | 支持会话（Session）概念，同一会话内可进行多轮对话，Agent 自动记住历史上下文 |
| **自然语言交互** | 接收指令 + 附件，无需编写代码逻辑，AI 自动理解意图 |
| **智能决策执行** | 自动判断意图，自主选择调用 Skills 或 MCP 工具完成任务 |
| **流式进度反馈** | 实时返回执行过程和结果，调用方全程可见 |
| **Skills 嵌套** | 复杂任务自动拆解，子任务递归执行 |
| **热插拔扩展** | Skills 支持动态添加，无需重启服务 |

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
│  ┌─────────────┐  ┌─────────────┐                            │
│  │ Memory 存储 │  │ Skills 注册 │                            │
│  │ (JSON文件)  │  │             │                            │
│  └─────────────┘  └─────────────┘                            │
└─────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────┐
│                      LLM API 服务                             │
│  (OpenAI / Claude / 任意 OpenAI 兼容服务)                     │
└─────────────────────────────────────────────────────────────┘
```

---

## 二、工作目录结构

Groot 启动时会创建一个工作目录（Home 目录），默认位置为 `~/.groot`，可通过命令行或环境变量更改。

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
│   └── {session_id}/              # 会话目录
│       ├── history.json           # 对话历史（含执行元数据摘要）
│       ├── attachments/           # 附件目录
│       │   └── {filename}         # 附件文件
│       └── chats/                 # 详细执行记录目录
│           └── chat_{timestamp}.json  # 单次对话完整记录
├── logs/                          # 日志目录
│   └── groot-{date}.log           # 日志文件
```

### 2.2 目录说明

| 目录/文件 | 说明 |
|----------|------|
| `config.yaml` | 主配置文件，控制服务行为 |
| `GROOT.md` | 项目规范文件，自动注入系统指令最前面，支持热加载 |
| `skills/` | Skills 定义目录，支持热插拔 |
| `mcp/` | MCP 工具配置目录，修改需重启服务 |
| `memory/` | 会话数据目录（JSON 存储） |
| `memory/{sid}/attachments/` | 附件存储，保留原始文件名 |
| `memory/{sid}/chats/` | 每轮对话的详细执行记录 |
| `logs/` | 日志存储目录 |

### 2.3 ID 格式说明

| ID 类型 | 格式 | 示例 |
|---------|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260419103000523_a1b2` |
| `chat_id` | `chat_{YYYYMMDDHHMMSSmmm}` | `chat_20260419103000523` |

**说明：**
- `session_id`：会话唯一标识，毫秒级时间戳 + 4位随机字符
- `chat_id`：单次对话标识，固定前缀 `chat_` + 毫秒级时间戳

### 2.4 工作目录配置方式

| 方式 | 示例 | 优先级 |
|------|------|--------|
| 命令行参数 | `groot -H /opt/groot` | 最高 |
| 环境变量 | `export GROOT_HOME=/opt/groot` | 中 |
| 默认值 | `~/.groot` | 最低 |

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

## 三、安装部署

### 3.1 系统要求

| 要求 | 说明 |
|------|------|
| 操作系统 | Linux / macOS / Windows |
| Go 版本 | Go 1.21+（仅源码编译需要） |
| 内存 | 建议 512MB+ |
| 磁盘 | 建议 1GB+（用于附件存储和会话数据） |

### 3.2 环境准备

#### 配置文件

Groot 首次启动时会自动生成默认配置文件 `{GROOT_HOME}/config.yaml`。

**配置项概览：**

| 配置项 | 必需性 | 说明 |
|------|------|------|
| `llm` | **必需** | LLM 配置，必须至少配置一个可用模型 |
| 其他配置项 | 可选 | 均有默认值，详见第四章"配置文件详解" |

> **重点：** 只有 `llm` 配置是必需的，其他配置项均可使用默认值。

#### LLM 配置示例

LLM 配置决定 Agent 使用哪个大模型执行任务。以下是一个最小配置示例：

```yaml
llm:
  default_model: gpt-4o           # 默认模型
  models:
    gpt-4o:
      base_url: https://api.openai.com/v1
      api_key: xxx                          # API 密钥
      model: gpt-4o
```

`api_key` 支持两种写法：

```yaml
# 方式一：环境变量引用（推荐）
api_key: ${OPENAI_API_KEY}

# 方式二：直接写入密钥
api_key: sk-xxxxxxxxxxxx
```

> **推荐环境变量：** 避免密钥硬编码，便于环境切换。若使用 `${VAR_NAME}`，需设置对应环境变量；若直接写密钥，则不需要。

#### 多模型配置示例

可配置多个模型，通过 `default_model` 指定默认使用的模型（需重启）：

```yaml
llm:
  default_model: gpt-4o
  models:
    gpt-4o:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4o
    
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
    
    qwen-plus:
      base_url: https://dashscope.aliyuncs.com/compatible-mode/v1
      api_key: ${DASHSCOPE_API_KEY}
      model: qwen-plus
```

#### 环境变量

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

### 3.3 安装方式

#### 方式一：直接运行（推荐）

下载预编译的二进制文件：

```bash
# Linux
wget https://github.com/zfd81/groot/releases/download/v1.0.0/groot-linux-amd64
chmod +x groot-linux-amd64
mv groot-linux-amd64 /usr/local/bin/groot

# macOS
wget https://github.com/zfd81/groot/releases/download/v1.0.0/groot-darwin-amd64
chmod +x groot-darwin-amd64
mv groot-darwin-amd64 /usr/local/bin/groot
```

#### 方式二：源码编译

```bash
# 克隆仓库
git clone https://github.com/zfd81/groot.git
cd groot

# 编译
go build -o bin/groot cmd/groot/main.go

# 或者使用 Makefile
make build

# 运行
./bin/groot
```

### 3.4 启动服务

```bash
# 方式一：预编译二进制
groot

# 方式二：源码编译
./bin/groot

# 指定工作目录和端口
groot -H /opt/groot -p 9090

# 查看帮助
groot --help

# 查看版本
groot --version
```

**启动参数：**

| 参数 | 缩写 | 说明 | 默认值 |
|------|------|------|--------|
| `--home` | `-H` | 工作目录 | `~/.groot` |
| `--port` | `-p` | HTTP端口 | 配置文件值 |
| `--help` | `-h` | 显示帮助 | - |
| `--version` | `-v` | 显示版本 | - |

**启动输出示例：**

```
Groot Agent 启动中...
  home: ~/.groot
  config: ~/.groot/config.yaml

Skills 加载完成  count=4
MCP 加载完成  count=2

API 服务启动
  host: 0.0.0.0
  port: 8080
```

### 3.5 验证安装

```bash
# 健康检查
curl http://localhost:8080/health

# 预期响应
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "1m",
  "checks": {
    "llm": {"status": "healthy", "model": "gpt-4o"},
    "mcp_servers": {"status": "healthy", "servers": ["file_operations", "http_request"]},
    "skills": {"status": "healthy", "count": 4},
    "memory": {"status": "healthy", "used_mb": 256}
  }
}
```

### 3.6 停止服务

```bash
# 发送终止信号
kill -SIGTERM <pid>

# 或使用 Ctrl+C（前台运行时）
```

服务会优雅关闭：
- 停止接受新请求
- 等待当前对话完成（超时 30 秒）
- 停止清理调度器
- 关闭 MCP 连接
- 刷新日志
- 退出程序

### 3.7 日志查看命令（groot tail）

Groot 提供了类似 `tail -f` 的实时日志查看命令，方便开发调试和运维监控。

**基本用法：**

```bash
# 实时查看日志（类似 tail -f）
groot tail

# 查看最近 50 行日志后实时跟踪
groot tail -n 50

# 只查看错误级别日志
groot tail -l error

# 过滤包含特定关键词的日志
groot tail -k "api_request"

# 组合使用：查看最近 100 行错误日志并实时跟踪
groot tail -n 100 -l error
```

**命令参数：**

| 参数 | 说明 | 示例 |
|------|------|------|
| `-n N` | 显示最后 N 行历史日志后实时跟踪 | `groot tail -n 50` |
| `-l level` | 按级别过滤，可选值：error/warn/info/debug | `groot tail -l error` |
| `-k keyword` | 关键词过滤，只显示包含关键词的日志 | `groot tail -k "connection"` |

**输出格式：**

日志以易读格式输出，带颜色高亮：

```
2026-04-21T19:18:38+08:00 INFO   api/server.go:42  API 服务启动  event=api_request  port=8080
2026-04-21T19:18:40+08:00 WARN   system/memory.go:8  内存使用率偏高  usage=85%
2026-04-21T19:18:42+08:00 ERROR  service/connection.go:15  服务连接失败  error="connection refused"
```

**颜色说明：**

| 级别 | 颜色 | 说明 |
|------|------|------|
| ERROR | 红色 | 错误日志，需要关注 |
| WARN | 黄色 | 警告日志，可能有问题 |
| INFO | 绿色 | 正常信息日志 |
| DEBUG | 灰色 | 调试日志，默认不显示 |

**退出方式：**

按 `Ctrl+C` 退出实时跟踪。

**日志文件位置：**

日志文件默认存放在 `{GROOT_HOME}/logs/groot-{YYYY-MM-DD}.log`，可通过配置文件修改：

```yaml
logging:
  file:
    directory: logs                # 日志目录
    filename_pattern: groot-{date}.log  # 文件名模式
```

---

## 四、配置文件详解

### 4.1 配置文件位置

首次启动时，Groot 会自动生成默认配置文件 `{GROOT_HOME}/config.yaml`。

### 4.2 完整配置文件示例

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
      max_tokens: 4096                       # 单次调用最大 Token 数
      temperature: 0.7                       # 输出随机性（0-1，越高越随机）
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_tokens: 4096
      temperature: 0.7

# Skills 热插拔配置
skills:
  directory: skills                  # Skills 目录（相对于 GROOT_HOME）
  hot_reload:
    enabled: true                    # 是否启用 Skills 热插拔
    debounce_delay: 2                # 防抖延迟（秒）

# MCP 配置目录
mcp:
  directory: mcp                     # MCP 配置目录（相对于 GROOT_HOME）

# ReAct 执行配置
react:
  max_iterations: 20               # 最大循环次数，-1 表示不限制
  max_tokens: 100000               # 最大Token消耗，-1 表示不限制
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

# 安全配置
security:
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

### 4.3 目录配置说明

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
| `skills.directory` | `skills` | Skills 脚本目录 |
| `mcp.directory` | `mcp` | MCP 配置目录 |
| `memory.directory` | `memory` | 会话记忆目录 |
| `logging.file.directory` | `logs` | 日志文件目录 |
| `attachment.temp_directory` | `temp` | 附件临时目录 |

### 4.4 配置字段详解

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
| `models.{name}.max_tokens` | 否 | 单次调用最大 Token 数，默认 `4096` |
| `models.{name}.temperature` | 否 | 输出随机性（0-1），默认 `0.7` |

#### Skills 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `directory` | 否 | Skills 目录（相对于 GROOT_HOME），默认 `skills` |
| `hot_reload.enabled` | 否 | 是否启用热插拔，默认 `true` |
| `hot_reload.debounce_delay` | 否 | 防抖延迟（秒），默认 `2` |

#### MCP 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `directory` | 否 | MCP 配置目录（相对于 GROOT_HOME），默认 `mcp` |

#### ReAct 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `max_iterations` | 否 | 最大循环次数，默认 `20`，`-1` 表示不限 |
| `max_tokens` | 否 | 最大 Token 消耗，默认 `100000`，`-1` 表示不限 |
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
| `retention_days` | 否 | 会话保留天数，超过后自动清理，默认 `7` |
| `cleanup_schedule` | 否 | 清理任务执行时间（HH:MM），默认 `02:00` |

#### Security 配置

| 字段 | 必需 | 说明 |
|------|------|------|
| `auth.enabled` | 否 | 是否开启认证，默认 `false` |
| `auth.type` | 否 | 认证类型，目前只支持 `api_key` |
| `auth.api_key.header_name` | 否 | 认证 Header 名称，默认 `X-API-Key` |
| `auth.api_key.keys[].name` | 否 | Key 名称（唯一标识） |
| `auth.api_key.keys[].key` | 否 | Key 值，支持 `${VAR_NAME}` 引用 |
| `auth.api_key.keys[].permissions` | 否 | 权限范围：`all` 或 `[chat, status, ...]` |

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

### 4.5 权限说明

| 权限 | 对应 API | 说明 |
|------|---------|------|
| `chat` | POST /chat | 执行对话 |
| `cancel` | DELETE /chat/{sid} | 取消对话 |
| `status` | GET /chat/status/{sid} | 查询对话状态 |
| `detail` | GET /chat/{sid} | 查询对话详情 |
| `session` | GET /sess/{sid} | 查询会话详情 |
| `history` | GET /sess/history | 查询会话列表 |
| `skills` | GET /skills | 查看 Skills 列表 |
| `tools` | GET /tools | 查看 MCP 工具列表 |
| `health` | GET /health | 健康检查 |
| `all` | 以上全部 | 全部权限 |

### 4.6 配置热更新

**支持热更新的配置：**
- Skills 配置：修改 SKILL.md 文件自动生效
- MCP 配置：修改 .json 文件自动生效

**不支持热更新的配置：**
- LLM 配置、Server 配置、Security 配置、Memory 配置、Logging 配置需重启服务

---

## 五、Skills 配置

### 5.1 Skills 目录结构

```
{GROOT_HOME}/skills/
├── pdf_analyzer/
│   └── SKILL.md
├── code_generator/
│   └── SKILL.md
└── data_analyzer/
    └── SKILL.md
```

### 5.2 Skill 文件格式

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

### 5.3 热插拔机制

- 启用 `skills.hot_reload.enabled: true` 后，修改 `SKILL.md` 自动生效
- 防抖延迟 `debounce_delay` 防止编辑过程中频繁触发加载
- 新增 Skill：创建目录和 `SKILL.md` 文件
- 修改 Skill：编辑 `SKILL.md` 内容
- 删除 Skill：删除对应目录

---

## 六、MCP 工具配置

### 6.1 MCP 配置目录

```
{GROOT_HOME}/mcp/
├── database_tool.json     # 数据库查询工具（stdio 类型）
├── web_parser.json        # 网页解析服务（sse 类型）
└── web_search.json        # 网络搜索服务（streamable_http 类型）
```

每个 MCP 工具使用独立的 JSON 配置文件。添加、修改或删除 MCP 配置后需要重启服务才能生效。

### 6.2 连接类型

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `stdio` | 标准输入输出通信 | 本地命令行工具（如数据库客户端） |
| `sse` | Server-Sent Events（单向推送） | 远程 HTTP 服务，服务端主动推送事件 |
| `streamable_http` | Streamable HTTP（双向流式） | 远程 HTTP 服务，支持请求和响应双向流式 |

### 6.3 MCP 配置示例

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

**连接类型对比：**

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `stdio` | 标准输入输出通信 | 本地命令行工具（如数据库客户端） |
| `sse` | Server-Sent Events（单向推送） | 远程 HTTP 服务，服务端主动推送事件 |
| `streamable_http` | Streamable HTTP（双向流式） | 远程 HTTP 服务，支持请求和响应双向流式 |

---

## 七、API 详细说明

### 7.1 API 列表

| API | 方法 | 用途 |
|-----|------|------|
| `/chat` | POST | 执行对话，SSE 流式返回（支持多轮对话） |
| `/chat/{sid}` | DELETE | 取消正在执行的对话 |
| `/chat/status/{sid}` | GET | 查询最近一次对话状态 |
| `/chat/{sid}` | GET | 查询最近一次对话详情（完整步骤记录） |
| `/sess/{sid}` | GET | 查询会话详情（完整对话历史） |
| `/sess/history` | GET | 查询会话列表 |
| `/health` | GET | 健康检查 |
| `/skills` | GET | 列出可用 Skills |
| `/tools` | GET | 列出可用 MCP 工具 |

### 7.2 认证方式

如果启用了认证（`security.auth.enabled: true`），需要在请求头携带 API Key：

```http
X-API-Key: your-secret-key
```

Header 名称可在配置文件中自定义。

---

### 7.3 POST /chat - 执行对话（核心接口）

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
      "type": "file",
      "name": "filename.ext",
      "content": "base64编码内容"
    },
    {
      "type": "url",
      "name": "filename.ext",
      "url": "https://example.com/file"
    }
  ]
}
```

**参数说明：**

| 参数 | 必填 | 说明 |
|------|------|------|
| `instruction` | 是 | 用户任务指令 |
| `prompt` | 否 | 系统提示词，设定Agent角色、行为约束、背景信息 |
| `attachments` | 否 | 附件列表（Base64编码或URL）|

**响应 Header：**

| Header | 说明 |
|--------|------|
| `X-Session-ID` | 会话ID（新建或传入存在的） |
| `X-Chat-ID` | 本次对话ID |
| `Content-Type` | `text/event-stream` |
| `Cache-Control` | `no-cache` |
| `Connection` | `keep-alive` |

**SSE 响应格式：**

所有事件使用标准 SSE `data:` 格式：

```
data: <JSON内容>\n\n
```

流结束时发送：

```
data: [DONE]
```

**事件类型：**

| 事件类型 | role 字段 | 说明 |
|---------|----------|------|
| thinking | `assistant` | AI 思考过程，逐步流式输出（`reasoning_content` 字段） |
| message | `assistant` | AI 回答内容，逐步流式输出（`content` 字段） |
| tool_calls | `assistant` | AI 决定调用工具（`tool_calls` 字段） |
| finish | `assistant` | 当前响应阶段结束（`finish_reason` 字段） |
| tool_result | `tool` | 工具执行结果 |
| done | - | 整体对话结束标记 `[DONE]` |

**事件流示例：**

```
data: {"role":"assistant","reasoning_content":"用户"}
data: {"role":"assistant","reasoning_content":"要求"}
data: {"role":"assistant","reasoning_content":"读取文件"}
data: {"role":"assistant","tool_calls":[{"id":"call_abc123","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"/etc/hosts\"}"}}]}
data: {"role":"assistant","finish_reason":"tool_calls"}
data: {"role":"tool","tool_call_id":"call_abc123","tool_name":"file_read","content":"127.0.0.1 localhost\n::1 localhost"}
data: {"role":"assistant","reasoning_content":"好的"}
data: {"role":"assistant","content":"文件内容如下："}
data: {"role":"assistant","content":"127.0.0.1 localhost"}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

**finish_reason 值说明：**

| 值 | 含义 | 后续事件 |
|---|------|---------|
| `tool_calls` | AI 需要调用工具 | 后续有 `tool_result` 事件，然后 AI 继续响应 |
| `stop` | 对话完成 | 后续为 `[DONE]` |

**事件可选性说明：**

| 事件类型 | 是否必须 | 说明 |
|---------|---------|------|
| thinking (`reasoning_content`) | 可选 | 仅当 AI 输出思考内容时发送 |
| message (`content`) | **必须** | 最终回答内容，至少发送一次 |
| tool_calls | 可选 | 仅当调用工具时发送 |
| finish (`finish_reason`) | **必须** | 每个响应阶段结束时发送 |
| tool_result | 可选 | 仅当调用工具时发送（紧跟 tool_calls） |
| `[DONE]` | **必须** | 整体对话结束标记 |

**不同场景的事件流：**

**场景1：纯 LLM 回答（无 thinking）：**

```
data: {"role":"assistant","content":"回答内容..."}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

**场景2：LLM 回答带 thinking：**

```
data: {"role":"assistant","reasoning_content":"思考..."}
data: {"role":"assistant","content":"回答内容..."}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

**场景3：工具调用：**

```
data: {"role":"assistant","reasoning_content":"我需要调用工具..."}
data: {"role":"assistant","tool_calls":[...]}
data: {"role":"assistant","finish_reason":"tool_calls"}
data: {"role":"tool","tool_call_id":"xxx","tool_name":"file_read","content":"结果"}
data: {"role":"assistant","content":"最终回答..."}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

**数据结构定义：**

**thinking / message：**

```json
{
  "role": "assistant",
  "reasoning_content": "思考内容（可选）",
  "content": "回答内容（可选）"
}
```

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
        "arguments": "JSON格式参数字符串"
      }
    }
  ]
}
```

**tool_result：**

```json
{
  "role": "tool",
  "tool_call_id": "对应 tool_calls 中的 id",
  "tool_name": "工具名称",
  "content": "执行结果"
}
```

工具执行失败时：

```json
{
  "role": "tool",
  "tool_call_id": "call_xxx",
  "tool_name": "file_read",
  "content": "",
  "error": "文件不存在"
}
```

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

### 7.4 DELETE /chat/{sid} - 取消对话

取消指定会话中正在执行的对话。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sid` | string | 是 | 会话 ID（路径参数） |

**成功响应：**
```json
{
  "status": "success",
  "session_id": "20260419103000523_a1b2",
  "chat_id": "chat_20260419103000523",
  "message": "对话已取消"
}
```

**失败响应（无运行对话）：**
```json
{
  "status": "no_running_chat",
  "session_id": "20260419103000523_a1b2",
  "message": "该会话当前没有正在执行的对话"
}
```

**请求示例：**
```bash
curl -X DELETE http://localhost:8080/chat/20260419103000523_a1b2 \
  -H "X-API-Key: your-api-key"
```

---

### 7.5 GET /chat/status/{sid} - 查询对话状态

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

### 7.6 GET /chat/{sid}/{cid} - 查询对话详情

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

### 7.7 GET /sess/{sid} - 查询会话详情

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

### 7.8 GET /sess/history - 查询会话列表

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

### 7.9 GET /health - 健康检查

查询服务健康状态。

**响应示例：**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "healthy", "model": "gpt-4o"},
    "mcp_servers": {"status": "healthy", "servers": ["database_tool", "web_parser"]},
    "skills": {"status": "healthy", "count": 12},
    "memory": {"status": "healthy", "used_mb": 256}
  },
  "metrics": {
    "chats_running": 5,
    "success_rate": 0.98
  }
}
```

---

### 7.10 GET /skills - 列出可用 Skills

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

### 7.11 GET /tools - 列出可用 MCP 工具

**响应示例：**
```json
{
  "tools": [
    {"name": "file_read", "description": "读取文件内容", "mcp": "file_operations"},
    {"name": "file_write", "description": "写入文件内容", "mcp": "file_operations"},
    {"name": "http_get", "description": "发送HTTP GET请求", "mcp": "http_request"}
  ],
  "total": 3
}
```

---

## 八、客户端代码示例

### 8.1 Python 客户端

```python
import requests
import json
import base64
from pathlib import Path

class GrootClient:
    """Groot AI Agent 客户端"""
    
    def __init__(self, base_url: str, api_key: str = None):
        self.base_url = base_url.rstrip('/')
        self.headers = {"Content-Type": "application/json"}
        if api_key:
            self.headers["X-API-Key"] = api_key
    
    def execute_chat(self, instruction: str, attachments: list = None, 
                     session_id: str = None, prompt: str = None,
                     callback: callable = None) -> dict:
        """执行对话（SSE 流式返回）"""
        body = {"instruction": instruction}
        if prompt:
            body["prompt"] = prompt
        
        if attachments:
            processed = []
            for att in attachments:
                if att["type"] == "file":
                    with open(att["path"], "rb") as f:
                        content = base64.b64encode(f.read()).decode()
                    processed.append({"type": "file", "name": att["name"], "content": content})
                elif att["type"] == "url":
                    processed.append({"type": "url", "name": att["name"], "url": att["url"]})
            body["attachments"] = processed
        
        headers = self.headers.copy()
        if session_id:
            headers["X-Session-ID"] = session_id
        
        response = requests.post(
            f"{self.base_url}/chat",
            headers=headers,
            json=body,
            stream=True
        )
        
        result = {
            "session_id": response.headers.get("X-Session-ID"),
            "chat_id": response.headers.get("X-Chat-ID"),
        }
        
        event_type = None
        for line in response.iter_lines():
            if line:
                line = line.decode()
                if line.startswith("event:"):
                    event_type = line[6:].strip()
                elif line.startswith("data:"):
                    data = line[5:].strip()
                    if callback:
                        callback(event_type, data)
                    if event_type == "completed":
                        parsed = json.loads(data)
                        result["status"] = parsed.get("status")
                        if parsed.get("status") == "success":
                            result["result"] = parsed.get("result")
        
        return result
    
    def cancel_chat(self, session_id: str) -> dict:
        """取消对话"""
        response = requests.delete(
            f"{self.base_url}/chat/{session_id}",
            headers=self.headers
        )
        return response.json()
    
    def get_chat_status(self, session_id: str) -> dict:
        """查询对话状态"""
        response = requests.get(
            f"{self.base_url}/chat/status/{session_id}",
            headers=self.headers
        )
        return response.json()
    
    def get_chat_detail(self, session_id: str, chat_id: str) -> dict:
        """查询对话详情"""
        response = requests.get(
            f"{self.base_url}/chat/{session_id}/{chat_id}",
            headers=self.headers
        )
        return response.json()
    
    def get_session(self, session_id: str) -> dict:
        """查询会话详情"""
        response = requests.get(
            f"{self.base_url}/sess/{session_id}",
            headers=self.headers
        )
        return response.json()
    
    def list_sessions(self, limit: int = 20, offset: int = 0) -> dict:
        """查询会话列表"""
        response = requests.get(
            f"{self.base_url}/sess/history",
            headers=self.headers,
            params={"limit": limit, "offset": offset}
        )
        return response.json()
    
    def health_check(self) -> dict:
        """健康检查"""
        response = requests.get(f"{self.base_url}/health")
        return response.json()
    
    def list_skills(self) -> dict:
        """列出可用 Skills"""
        response = requests.get(
            f"{self.base_url}/skills",
            headers=self.headers
        )
        return response.json()
    
    def list_tools(self) -> dict:
        """列出可用 MCP 工具"""
        response = requests.get(
            f"{self.base_url}/tools",
            headers=self.headers
        )
        return response.json()


# 使用示例
groot = GrootClient("http://localhost:8080", "your-api-key")

# 新会话
result1 = groot.execute_chat(
    instruction="分析这份PDF报告",
    attachments=[{"type": "file", "name": "report.pdf", "path": "./report.pdf"}],
    callback=lambda e, d: print(f"[{e}] {d}")
)
print(f"会话ID: {result1['session_id']}")

# 继续会话（多轮）
result2 = groot.execute_chat(
    instruction="生成分析摘要",
    session_id=result1['session_id'],
    callback=lambda e, d: print(f"[{e}] {d}")
)
print(f"第2轮结果: {result2['result']}")
```

---

## 九、使用场景示例

### 9.1 多轮文档分析

```python
# 第1轮：上传文档并分析
result1 = groot.execute_chat(
    instruction="分析这份财报，提取营收、利润、增长率等关键指标",
    attachments=[{"type": "file", "name": "Q3_Report.pdf", "path": "Q3_Report.pdf"}]
)
session_id = result1['session_id']

# 第2轮：追问细节
result2 = groot.execute_chat(
    instruction="重点分析利润增长的主要原因",
    session_id=session_id
)

# 第3轮：生成报告
result3 = groot.execute_chat(
    instruction="生成一份分析报告摘要",
    session_id=session_id
)
```

### 9.2 渐进式代码开发

```python
# 第1轮：基础功能
result1 = groot.execute_chat(
    instruction="写一个 Python 数据处理工具类，包含 CSV 读取功能",
    prompt="你是资深 Python 开发者"
)
session_id = result1['session_id']

# 第2轮：添加功能
result2 = groot.execute_chat(
    instruction="添加数据清洗功能（处理缺失值、异常值）",
    session_id=session_id
)

# 第3轮：添加测试
result3 = groot.execute_chat(
    instruction="写单元测试代码",
    session_id=session_id
)
```

---

## 十、常见问题

### Q1: 启动时报错 "OPENAI_API_KEY not set"

**原因：** 未配置 LLM API 密钥。

**解决：**
```bash
export OPENAI_API_KEY="sk-xxxxx"
```
或在配置文件中直接写入密钥。

---

### Q2: 多轮对话时 Agent 没记住之前的内容

**原因：** session_id 传错或会话不存在。

**解决：** 确保每次继续对话时传入正确的 `X-Session-ID`。

---

### Q3: 同一会话并发调用报错

**原因：** 同一会话只能有一个活跃对话，防止执行冲突。

**解决：** 等待当前对话完成后再发起下一轮，或取消当前对话。

---

### Q4: 附件上传失败

**原因：** 附件类型不允许或大小超限。

**解决：** 检查 `allowed_types` 配置和 `max_size` 限制。

---

### Q5: 认证失败 401 Unauthorized

**原因：** API Key 无效或未携带。

**解决：**
1. 确认配置了正确的 API Key
2. 检查请求头是否携带 `X-API-Key`

---

### Q6: 会话数据如何清理

**说明：** 会话数据会自动清理。

- 每天在 `cleanup_schedule` 时间执行清理
- 清理超过 `retention_days` 天的会话

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

| 路径 | 说明 |
|------|------|
| `{GROOT_HOME}/config.yaml` | 配置文件 |
| `{GROOT_HOME}/skills/{name}/SKILL.md` | Skill 定义文件 |
| `{GROOT_HOME}/mcp/{name}.json` | MCP 配置文件 |
| `{GROOT_HOME}/memory/{session_id}/history.json` | 对话历史 |
| `{GROOT_HOME}/memory/{session_id}/attachments/` | 附件目录 |
| `{GROOT_HOME}/memory/{session_id}/chats/{chat_id}.json` | 详细执行记录 |
| `{GROOT_HOME}/logs/groot-{date}.log` | 日志文件 |

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
| 500 | `config_error` | 配置错误 |
| 500 | `llm_connection_error` | LLM 连接失败 |
| 500 | `tool_call_error` | 工具调用失败 |

### D. 联系与支持

- GitHub: https://github.com/zfd81/groot
- 问题反馈: GitHub Issues