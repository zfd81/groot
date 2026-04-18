# Groot AI Agent 使用手册

**版本:** 1.0.0  
**日期:** 2026-04-18

---

## 一、产品介绍

### 1.1 什么是 Groot

Groot 是一款面向业务系统的 AI Agent 服务中间件。通过 REST API 接入，让你的系统立刻拥有智能任务执行能力——接收自然语言指令，自主调用工具完成任务，实时反馈执行进度。

**一句话概括：** 把 AI Agent 能力嵌入你的业务系统，像调用普通 API一样使用智能执行能力。

### 1.2 核心特性

| 特性 | 说明 |
|------|------|
| **自然语言交互** | 接收指令 + 附件，无需编写复杂逻辑，AI 自动理解意图 |
| **智能决策执行** | 自动判断需要调用哪些 Skills 或 MCP 工具，自主完成任务 |
| **流式进度反馈** | SSE 实时推送执行过程，调用方全程可见，便于监控和调试 |
| **附件处理** | 支持 Base64 编码文件和 URL 链接，自动存储、传递、清理 |
| **Skills 扩展** | 通过编写 Skill 文件定义专属任务模板，热插拔无需重启 |
| **MCP 工具集成** | 内置文件操作、HTTP 请求工具，支持外部 MCP 服务接入 |

### 1.3 典型应用场景

| 场景 | 说明 | 示例指令 |
|------|------|---------|
| **文档分析** | 上传 PDF/Word 文件，自动提取关键信息并生成摘要 | "分析这份财报，提取关键财务指标" |
| **数据处理** | 上传 CSV/JSON 数据文件，执行统计分析并生成报告 | "分析销售数据，计算月度增长趋势" |
| **代码生成** | 根据需求描述生成代码片段 | "写一个 Python 快速排序函数" |
| **内容创作** | 根据素材生成营销文案、技术文档等 | "根据产品特性写一篇推广文章" |
| **信息检索** | 通过 HTTP 工具获取网络数据并整理 | "获取天气信息并生成出行建议" |
| **多文件对比** | 同时上传多个文件，对比分析差异 | "对比这份合同和模板，找出修改条款" |

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
│  │ BoltDB 存储 │  │ Skills 注册 │                            │
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

Groot 启动时会创建一个工作目录（Home 目录），默认位置为 `~/.groot`，可通过参数或环境变量修改。

### 2.1 目录结构

```
{GROOT_HOME}/
├── config.yaml          # 主配置文件（首次启动自动生成）
├── groot.db             # BoltDB 数据库文件（任务记录）
├── skills/              # Skills 目录（任务模板）
│   └── {skill-name}/
│       └── SKILL.md     # Skill 定义文件
├── mcp/                 # MCP 配置目录（工具配置）
│   ├── file_operations.json   # 内置文件操作工具
│   ├── http_request.json      # 内置 HTTP 请求工具
│   └── {custom-mcp}.json      # 自定义 MCP 配置
├── logs/                # 日志目录
│   └── groot-{date}.log # 按日期分割的日志文件
└── temp/                # 附件临时存储目录
    └── task-{id}/       # 每个任务的独立目录
        ├── file1.pdf    # 任务附件
        └── file2.csv
```

### 2.2 目录说明

| 目录/文件 | 说明 | 可配置 |
|----------|------|--------|
| `config.yaml` | 主配置文件，控制服务行为 | 自动生成，可手动修改 |
| `groot.db` | BoltDB 数据库，存储任务记录 | 配置文件指定路径 |
| `skills/` | Skills 定义目录，支持热插拔 | 固定位置 |
| `mcp/` | MCP 工具配置目录，支持热插拔 | 固定位置 |
| `logs/` | 日志存储目录 | 配置文件指定 |
| `temp/` | 附件临时存储，任务完成后自动清理 | 支持绝对路径配置 |

### 2.3 工作目录配置方式

| 方式 | 示例 | 优先级 |
|------|------|--------|
| 命令行参数 | `groot -H /opt/groot` | 最高 |
| 环境变量 | `export GROOT_HOME=/opt/groot` | 中 |
| 默认值 | `~/.groot` | 最低 |

---

## 三、安装部署

### 3.1 系统要求

| 要求 | 说明 |
|------|------|
| 操作系统 | Linux / macOS / Windows |
| Go 版本 | Go 1.21+（仅源码编译需要） |
| 内存 | 建议 512MB+ |
| 磁盘 | 建议 1GB+（用于附件临时存储和数据库） |

### 3.2 环境准备

**配置 LLM API 密钥：**

Groot 需要配置 LLM API 密钥才能正常工作。有两种配置方式：

**方式一：配置文件中直接写入（简单但不够安全）**

```yaml
llm:
  models:
    gpt-4o:
      api_key: sk-xxxxxxxxxxxx    # 直接写密钥
```

**方式二：配置文件引用环境变量（推荐，更安全）**

```yaml
llm:
  models:
    gpt-4o:
      api_key: ${OPENAI_API_KEY}   # 引用环境变量
```

然后设置环境变量：

```bash
# LLM API 密钥（配置文件引用时需要）
export OPENAI_API_KEY="sk-xxxxxxxxxxxx"

# 其他 LLM 服务密钥（如使用多模型配置）
export ANTHROPIC_API_KEY="sk-ant-xxxx"
export DASHSCOPE_API_KEY="sk-xxxx"
```

> **说明：** 
> - 环境变量不是必须配置的，取决于配置文件中的写法
> - 使用 `${VAR_NAME}` 格式引用环境变量，避免密钥硬编码
> - 配置文件中可以配置多个模型的 API Key，见第四章配置详解

**认证密钥（启用认证时需要）：**

Groot 支持配置多个 API Key，每个 Key 可以设置不同的权限范围。

**配置示例：**

```yaml
security:
  auth:
    enabled: true
    api_key:
      keys:
        # Key 1：管理员，全部权限
        - name: admin
          key: ${GROOT_ADMIN_KEY}
          permissions: [all]
        
        # Key 2：业务系统，执行权限
        - name: business_system
          key: biz-key-2026-secret
          permissions: [execute, status, cancel]
        
        # Key 3：监控服务，只读权限
        - name: monitor
          key: ${GROOT_MONITOR_KEY}
          permissions: [status, health, skills, tools, history]
```

**对应的环境变量（配置文件引用时需要）：**

```bash
# 管理员 API Key
export GROOT_ADMIN_KEY="admin-secret-key"

# 监控服务 API Key
export GROOT_MONITOR_KEY="monitor-secret-key"
```

> **说明：**
> - 可以配置多个 API Key，每个 Key 有独立的名称和权限
> - 权限包括：`execute`、`cancel`、`status`、`history`、`detail`、`skills`、`tools`、`health`、`all`
> - 完整权限配置说明见第四章配置详解

**其他可选环境变量：**

```bash
# 工作目录（可选，默认 ~/.groot）
export GROOT_HOME="/opt/groot"
```

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

# 编译完成后，运行方式：

# 方式 A：直接运行编译后的程序
./bin/groot

# 方式 B：移动到 PATH 目录，方便全局调用
mv bin/groot /usr/local/bin/groot
groot

# 方式 C：直接运行（无需编译）
go run cmd/groot/main.go
```

### 3.4 启动服务

根据安装方式选择启动方法：

```bash
# 方式一：预编译二进制（已移动到 PATH）
groot

# 方式二：源码编译（未移动到 PATH）
./bin/groot

# 方式三：直接运行（无需编译）
go run cmd/groot/main.go
```

**常用启动参数：**

```bash
# 指定工作目录
groot -H /opt/groot

# 指定端口
groot -p 9090

# 组合使用
groot -H /opt/groot -p 9090

# 查看帮助
groot --help

# 查看版本
groot --version
```

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
    "skills": {"status": "healthy", "count": 4}
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
- 等待当前任务完成（超时 30 秒）
- 关闭 MCP 连接
- 刷新日志
- 退出程序

---

## 四、配置文件详解

### 4.1 配置文件位置

首次启动时，Groot 会自动生成默认配置文件 `{GROOT_HOME}/config.yaml`。

### 4.2 完整配置文件示例

```yaml
# Groot Agent 配置文件
# 生成时间: 2026-04-18

# ============================================================
# Agent 基础配置
# ============================================================
agent:
  name: groot           # 服务名称，用于日志和监控标识
  version: 1.0.0        # 版本号

# ============================================================
# HTTP 服务配置
# ============================================================
server:
  host: 0.0.0.0         # 监听地址，0.0.0.0 表示所有网卡
                        # 内网部署可改为 127.0.0.1
  port: 8080            # 监听端口
                        # 可通过命令行 -p 参数覆盖

# ============================================================
# LLM 配置（OpenAI 兼容协议）
# ============================================================
llm:
  active_model: gpt-4o  # 当前激活的模型，对应 models 中的某个 key
                        # 切换模型需重启服务
  
  models:
    # OpenAI GPT-4o
    gpt-4o:
      base_url: https://api.openai.com/v1   # API 地址
                                              # 可改为兼容服务的地址
      api_key: ${OPENAI_API_KEY}             # API 密钥，支持环境变量
                                              # 也可直接写明文（不推荐）
      model: gpt-4o                          # 实际模型名称
      max_tokens: 4096                       # 单次调用最大 Token
      temperature: 0.7                       # 输出随机性（0-1）
    
    # Anthropic Claude（示例）
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_tokens: 4096
      temperature: 0.7
    
    # 国内兼容服务（示例）
    dashscope:
      base_url: https://dashscope.aliyuncs.com/compatible-mode/v1
      api_key: ${DASHSCOPE_API_KEY}
      model: qwen-plus
      max_tokens: 4096
      temperature: 0.7

# ============================================================
# Skills 热插拔配置
# ============================================================
skills:
  hot_reload:
    enabled: true        # 是否启用热插拔
                        # true: 修改 SKILL.md 自动生效
                        # false: 需重启服务
    debounce_delay: 2    # 防抖延迟（秒）
                        # 防止编辑过程中频繁触发加载

# ============================================================
# MCP 热插拔配置
# ============================================================
mcp:
  hot_reload:
    enabled: true        # 是否启用热插拔
                        # true: 修改 .json 配置自动生效
                        # false: 需重启服务
    debounce_delay: 2    # 防抖延迟（秒）

# ============================================================
# 存储配置
# ============================================================
storage:
  engine: boltdb         # 存储引擎：boltdb（单机）、redis（集群预留）、etcd（集群预留）
  
  boltdb:
    file: groot.db       # 数据库文件名（相对工作目录）
                        # 也可使用绝对路径：/data/groot/groot.db
    bucket: tasks        # 存储桶名称
  
  redis:                 # Redis 配置（集群预留）
    endpoint: ${REDIS_ENDPOINT}
    password: ${REDIS_PASSWORD}
    key_prefix: groot:task:
  
  etcd:                  # etcd 配置（集群预留）
    endpoints: [${ETCD_ENDPOINT_1}, ${ETCD_ENDPOINT_2}]
    key_prefix: /groot/tasks/
  
  retention_days: 7      # 任务记录保留天数
                        # 超过天数自动清理
  cleanup_interval: 24h  # 清理任务执行间隔

# ============================================================
# 性能控制配置
# ============================================================
performance:
  rate_limit:
    max_concurrent_tasks: 10       # 最大并发任务数
                                    # 超过返回 429 Too Many Requests
    max_requests_per_minute: 60    # 每分钟最大请求数
    max_requests_per_hour: 1000    # 每小时最大请求数
  
  timeout:
    task_max_duration: 300         # 单任务最大执行时长（秒）
                                    # 超过自动终止
    llm_call_timeout: 60           # 单次 LLM 调用超时（秒）
    tool_call_timeout: 30          # 单次工具调用超时（秒）
  
  llm:
    max_concurrent_calls: 5        # LLM 并发调用数限制
    retry_on_failure: 3            # LLM 调用失败重试次数
    retry_delay: 2                 # 重试间隔（秒）
  
  mcp:
    max_concurrent_calls_per_server: 3  # 每个 MCP 服务并发调用数限制

# ============================================================
# ReAct 执行配置
# ============================================================
react:
  max_iterations: 20          # 最大循环次数，防止无限循环
                              # -1 表示不限制
  max_tokens: 100000          # 最大 Token 消耗，防止成本失控
                              # -1 表示不限制
  step_timeout: 60            # 单步执行超时（秒）
                              # -1 表示不限制
  error_retry: 2              # 单步失败重试次数
  nesting_max_depth: 3        # Skills 嵌套最大深度
                              # -1 表示不限制

# ============================================================
# 附件处理配置
# ============================================================
attachment:
  max_size: 50                    # 单个附件最大大小（MB）
                                  # 建议 10-50MB
  max_total_size: 100             # 所有附件总大小上限（MB）
  max_count: 10                   # 单次请求最大附件数量
  allowed_types:                  # 允许的附件类型（扩展名）
    - pdf
    - doc
    - docx
    - txt
    - json
    - csv
    - xml
    - yaml
    - png
    - jpg
    - zip
  
  # 附件临时存储目录配置说明：
  # --------------------------------
  # 相对路径：与工作目录拼接
  #   temp          → {GROOT_HOME}/temp
  #   ./temp        → {GROOT_HOME}/temp（等效）
  #   data/files    → {GROOT_HOME}/data/files
  #
  # 绝对路径：直接使用（以 / 开头）
  #   /home/zfd/temp           → /home/zfd/temp
  #   /tmp/groot               → /tmp/groot（系统临时目录）
  #   /data/storage/attachments → /data/storage/attachments
  #
  # 建议：
  #   - 单实例部署：使用相对路径 temp（默认）
  #   - 需要更大磁盘：使用绝对路径指向独立存储盘
  #   - 系统临时目录：使用 /tmp/groot（注意清理策略）
  temp_directory: temp

# ============================================================
# 安全配置
# ============================================================
security:
  auth:
    enabled: false              # 是否开启认证
                                # true: 需要 API Key
                                # false: 无需认证（内网/集群模式）
    type: api_key               # 认证类型，目前只支持 api_key
    
    api_key:
      header_name: X-API-Key    # 认证 Header 名称
                                # 客户端需在请求头携带此字段
      
      keys:                     # API Key 配置列表
        # 示例1：管理员账号，全部权限
        - name: admin
          key: ${GROOT_API_KEY}       # 密钥值，支持环境变量
          permissions:                # 权限列表
            - all                     # all 表示全部权限
        
        # 示例2：业务系统账号，执行权限
        - name: business_system
          key: biz-key-2026-secret
          permissions:
            - execute        # 执行任务
            - status         # 查询状态
            - cancel         # 取消任务
        
        # 示例3：监控账号，只读权限
        - name: monitor
          key: ${MONITOR_API_KEY}
          permissions:
            - status         # 查询状态
            - health         # 健康检查
            - skills         # 查看 Skills
            - tools          # 查看 MCP 工具
            - history        # 查询历史

# ============================================================
# 日志配置
# ============================================================
logging:
  level: info              # 日志级别：debug / info / warn / error
  format: json             # 日志格式：json / text
  output:                  # 输出目标
    - stdout               # 标准输出
    - file                 # 文件
  
  file:
    directory: logs        # 日志目录（相对工作目录）
                           # 也可使用绝对路径：/var/log/groot
    filename_pattern: groot-{date}.log   # 文件名模式
    max_age: 7             # 日志保留天数
```

### 4.3 权限说明

| 权限 | 对应 API | 说明 |
|------|---------|------|
| `execute` | POST /task/execute | 执行任务 |
| `cancel` | DELETE /task/{task_id} | 取消任务 |
| `status` | GET /task/status/{task_id} | 查询状态 |
| `history` | GET /task/history | 查询历史任务列表 |
| `detail` | GET /task/{task_id} | 查询任务详情 |
| `skills` | GET /skills | 查看 Skills 列表 |
| `tools` | GET /tools | 查看 MCP 工具列表 |
| `health` | GET /health | 健康检查 |
| `all` | 以上全部 | 全部权限 |

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

1. 使用 file_read 工具读取 PDF 文件
2. 提取文档的关键内容和结构
3. 根据文档类型生成相应的结构化摘要
4. 输出 JSON 格式的分析结果

## 输出格式

{
  "document_type": "文档类型",
  "title": "文档标题",
  "key_points": ["关键要点"],
  "summary": "详细摘要"
}
```

### 5.3 热插拔机制

- 启用 `skills.hot_reload.enabled: true` 后，修改 `SKILL.md` 自动生效
- 新增 Skill：创建目录和 `SKILL.md` 文件
- 修改 Skill：编辑 `SKILL.md` 内容
- 删除 Skill：删除对应目录

### 5.4 内置 Skills 示例

Groot 启动后可手动创建以下 Skills：

**code_generator（代码生成）：**

```markdown
---
name: code_generator
description: "根据需求描述生成代码，支持多种编程语言"
---

# 代码生成助手

你是一个专业的代码生成助手。

## 执行步骤

1. 分析用户需求，明确功能目标
2. 确定编程语言和代码结构
3. 生成完整代码实现，包含注释
4. 提供使用示例

## 输出格式

```{language}
// 代码内容
```

**使用示例：**
...
```

**data_analyzer（数据分析）：**

```markdown
---
name: data_analyzer
description: "分析结构化数据文件（CSV、JSON等）"
---

# 数据分析助手

## 执行步骤

1. 使用 file_read 工具读取数据文件
2. 解析数据结构，识别字段类型
3. 执行统计分析
4. 输出分析报告

## 输出格式

{
  "data_overview": {...},
  "statistics": {...},
  "insights": [...]
}
```

---

## 六、MCP 工具配置

### 6.1 MCP 配置目录

```
{GROOT_HOME}/mcp/
├── file_operations.json    # 内置文件操作
├── http_request.json       # 内置 HTTP 请求
└── custom_tool.json        # 自定义 MCP
```

### 6.2 内置 MCP 配置

**file_operations.json（文件操作）：**

```json
{
  "name": "file_operations",
  "type": "builtin",
  "description": "文件读写和目录操作",
  "isActive": true,
  "tools": ["file_read", "file_write", "file_search", "directory_list", "directory_create"],
  "restrictions": {
    "allowed_paths": [],
    "denied_operations": []
  }
}
```

| 字段 | 说明 |
|------|------|
| `type: "builtin"` | 表示内置工具，无需外部连接 |
| `allowed_paths` | 允许访问的目录列表，空数组表示无限制 |
| `denied_operations` | 禁止的操作列表 |

**http_request.json（HTTP 请求）：**

```json
{
  "name": "http_request",
  "type": "builtin",
  "description": "HTTP 请求发送",
  "isActive": true,
  "tools": ["http_get", "http_post", "http_put", "http_delete"],
  "restrictions": {
    "denied_domains": ["localhost", "127.0.0.1", "10.*", "192.168.*"],
    "timeout": 30,
    "max_response_size": 10
  }
}
```

| 字段 | 说明 |
|------|------|
| `denied_domains` | 禁止访问的域名/IP（防止 SSRF） |
| `timeout` | 请求超时时间（秒） |
| `max_response_size` | 最大响应大小（MB） |

### 6.3 外部 MCP 配置

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

---

## 七、API 详细说明

### 7.1 认证方式

如果启用了认证（`security.auth.enabled: true`），需要在请求头携带 API Key：

```http
X-API-Key: your-secret-key
```

Header 名称可在配置文件中自定义：

```yaml
security:
  auth:
    api_key:
      header_name: X-Groot-Key  # 自定义 Header 名称
```

---

### 7.2 POST /task/execute - 执行任务

**功能说明：**

执行 AI 任务，通过 SSE 流式返回执行进度和结果。适用于：
- 文档分析、数据处理、代码生成等智能任务
- 支持上传附件（PDF、CSV、图片等）
- 实时获取执行进度，便于监控和调试

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `instruction` | string | 是 | 自然语言指令，描述要执行的任务 |
| `prompt` | string | 否 | 系统提示词，设定 Agent 角色、行为约束 |
| `attachments` | array | 否 | 附件列表 |

**附件格式（file 类型）：**

```json
{
  "type": "file",
  "name": "report.pdf",
  "content": "base64编码内容"
}
```

**附件格式（url 类型）：**

```json
{
  "type": "url",
  "name": "external_data",
  "content": "https://example.com/data.json"
}
```

**响应头：**

| Header | 说明 |
|--------|------|
| `X-Task-ID` | 任务唯一标识，可用于查询状态、取消任务 |
| `Content-Type` | `text/event-stream`（SSE 流式响应） |

**SSE 事件类型：**

| 事件 | 说明 | 示例数据 |
|------|------|---------|
| `intent` | 任务开始 | `{"timestamp":"2026-04-18T10:30:00Z"}` |
| `step_start` | 步骤开始 | `{"type":"tool","name":"file_read","step_id":"xxx"}` |
| `progress` | 进度更新 | `{"message":"正在读取PDF..."}` |
| `step_end` | 步骤结束 | `{"status":"success"}` |
| `completed` | 任务完成 | `{"status":"success","result":"..."}` |

**请求示例：**

```bash
curl -X POST http://localhost:8080/task/execute \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "写一个 Python 快速排序函数"}'
```

**Java 示例：**

```java
// 执行简单任务（无附件）
public String executeTask(String instruction) throws Exception {
    String jsonBody = "{\"instruction\":\"" + instruction + "\"}";
    
    Request request = new Request.Builder()
        .url(baseUrl + "/task/execute")
        .header("X-API-Key", apiKey)
        .header("Content-Type", "application/json")
        .header("Accept", "text/event-stream")
        .post(RequestBody.create(jsonBody, MediaType.parse("application/json")))
        .build();
    
    // SSE 流式处理
    EventSourceListener listener = new EventSourceListener() {
        @Override
        public void onEvent(EventSource eventSource, String id, String type, String data) {
            System.out.println("[" + type + "] " + data);
            if ("completed".equals(type)) {
                System.out.println("任务完成");
            }
        }
    };
    
    EventSources.createFactory(client).newEventSource(request, listener);
    return null; // task_id 在响应头中获取
}

// 执行带附件任务
public String executeTaskWithFile(String instruction, String filePath) throws Exception {
    byte[] fileContent = Files.readAllBytes(Paths.get(filePath));
    String base64Content = Base64.getEncoder().encodeToString(fileContent);
    String fileName = Paths.get(filePath).getFileName().toString();
    
    String jsonBody = String.format("""
        {"instruction":"%s","attachments":[{"type":"file","name":"%s","content":"%s"}]}
        """, instruction, fileName, base64Content);
    
    Request request = new Request.Builder()
        .url(baseUrl + "/task/execute")
        .header("X-API-Key", apiKey)
        .header("Content-Type", "application/json")
        .header("Accept", "text/event-stream")
        .post(RequestBody.create(jsonBody, MediaType.parse("application/json")))
        .build();
    
    EventSources.createFactory(client).newEventSource(request, listener);
    return null;
}
```

**Python 示例：**

```python
def execute_task(self, instruction: str, attachments: list = None, callback: callable = None) -> str:
    """执行任务，返回 task_id"""
    body = {"instruction": instruction}
    
    # 处理附件
    if attachments:
        processed = []
        for att in attachments:
            if att["type"] == "file":
                with open(att["path"], "rb") as f:
                    content = base64.b64encode(f.read()).decode()
                processed.append({"type": "file", "name": att["name"], "content": content})
            elif att["type"] == "url":
                processed.append({"type": "url", "name": att["name"], "content": att["url"]})
        body["attachments"] = processed
    
    # 发起 SSE 请求
    response = requests.post(
        f"{self.base_url}/task/execute",
        headers=self.headers,
        json=body,
        stream=True
    )
    
    task_id = response.headers.get("X-Task-ID")
    
    # 处理 SSE 流
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
    
    return task_id

# 调用示例
task_id = groot.execute_task("分析这份财报", attachments=[
    {"type": "file", "name": "report.pdf", "path": "/path/to/report.pdf"}
], callback=lambda event, data: print(f"[{event}] {data}"))
```

**Go 示例：**

```go
// ExecuteTask 执行任务
func (c *Client) ExecuteTask(ctx context.Context, instruction string, attachments []Attachment, callback func(SSEEvent)) (taskID string, err error) {
    req := ExecuteRequest{
        Instruction: instruction,
        Attachments: attachments,
    }
    body, _ := json.Marshal(req)
    
    httpReq, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/task/execute", bytes.NewReader(body))
    c.setHeaders(httpReq)
    httpReq.Header.Set("Accept", "text/event-stream")
    
    resp, err := c.client.Do(httpReq)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    
    taskID = resp.Header.Get("X-Task-ID")
    
    // 处理 SSE 流
    scanner := bufio.NewScanner(resp.Body)
    var eventType string
    for scanner.Scan() {
        line := scanner.Text()
        if strings.HasPrefix(line, "event:") {
            eventType = strings.TrimPrefix(line, "event:")
        } else if strings.HasPrefix(line, "data:") {
            data := strings.TrimPrefix(line, "data:")
            if callback != nil {
                callback(SSEEvent{Type: eventType, Data: data})
            }
        }
    }
    
    return taskID, nil
}

// 调用示例
taskID, _ := client.ExecuteTask(ctx, "写一个快速排序函数", nil, func(event SSEEvent) {
    fmt.Printf("[%s] %s\n", event.Type, event.Data)
})
```

---

### 7.3 DELETE /task/{task_id} - 取消任务

**功能说明：**

取消正在执行的任务。只能取消状态为 `running` 的任务，已完成或已取消的任务无法再次取消。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `task_id` | string | 是 | 任务 ID（路径参数） |

**响应字段：**

| 字段 | 说明 |
|------|------|
| `status` | 结果状态：`success` 或失败状态码 |
| `task_id` | 任务 ID |
| `message` | 结果消息 |

**成功响应：**

```json
{
  "status": "success",
  "task_id": "task-20260418-103000523-a1b2",
  "message": "任务已取消"
}
```

**失败响应（任务已完成）：**

```json
{
  "status": "task_completed",
  "task_id": "task-20260418-103000523-a1b2",
  "message": "任务已完成，无法取消"
}
```

**失败响应（任务不存在）：**

```json
{
  "status": "task_not_found",
  "task_id": "task-xxx",
  "message": "任务不存在"
}
```

**请求示例：**

```bash
curl -X DELETE http://localhost:8080/task/task-20260418-103000523-a1b2 \
  -H "X-API-Key: your-api-key"
```

**Java 示例：**

```java
public String cancelTask(String taskId) throws Exception {
    Request request = new Request.Builder()
        .url(baseUrl + "/task/" + taskId)
        .header("X-API-Key", apiKey)
        .delete()
        .build();
    
    Response response = client.newCall(request).execute();
    return response.body().string();
}

// 调用示例
String result = groot.cancelTask("task-20260418-103000523-a1b2");
System.out.println(result);  // {"status":"success","message":"任务已取消"}
```

**Python 示例：**

```python
def cancel_task(self, task_id: str) -> dict:
    """取消任务"""
    response = requests.delete(
        f"{self.base_url}/task/{task_id}",
        headers=self.headers
    )
    return response.json()

# 调用示例
result = groot.cancel_task("task-20260418-103000523-a1b2")
print(result)  # {"status": "success", "message": "任务已取消"}
```

**Go 示例：**

```go
// CancelTask 取消任务
func (c *Client) CancelTask(ctx context.Context, taskID string) (map[string]interface{}, error) {
    httpReq, _ := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/task/"+taskID, nil)
    c.setHeaders(httpReq)
    
    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    return result, nil
}

// 调用示例
result, _ := client.CancelTask(ctx, "task-20260418-103000523-a1b2")
fmt.Println(result)  // map[status:success message:任务已取消]
```

---

### 7.4 GET /task/status/{task_id} - 查询任务状态

**功能说明：**

查询任务的当前状态和执行进度。适用于轮询监控任务执行情况。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `task_id` | string | 是 | 任务 ID（路径参数） |

**响应字段：**

| 字段 | 说明 |
|------|------|
| `status` | 查询结果：`success` 或 `task_not_found` |
| `task_id` | 任务 ID |
| `task_status` | 任务状态：`running` / `completed` / `failed` / `cancelled` |
| `progress` | 进度信息（运行中时） |
| `started_at` | 开始时间 |
| `elapsed_time` | 已耗时 |

**运行中响应：**

```json
{
  "status": "success",
  "task_id": "task-20260418-103000523-a1b2",
  "task_status": "running",
  "progress": {
    "current_step": 3,
    "steps_completed": 2,
    "percentage": 50
  },
  "started_at": "2026-04-18T10:30:00Z",
  "elapsed_time": "8s"
}
```

**已完成响应：**

```json
{
  "status": "success",
  "task_id": "task-20260418-103000523-a1b2",
  "task_status": "completed",
  "started_at": "2026-04-18T10:30:00Z",
  "elapsed_time": "45s"
}
```

**请求示例：**

```bash
curl http://localhost:8080/task/status/task-20260418-103000523-a1b2 \
  -H "X-API-Key: your-api-key"
```

**Java 示例：**

```java
public String getTaskStatus(String taskId) throws Exception {
    Request request = new Request.Builder()
        .url(baseUrl + "/task/status/" + taskId)
        .header("X-API-Key", apiKey)
        .get()
        .build();
    
    Response response = client.newCall(request).execute();
    return response.body().string();
}

// 调用示例
String status = groot.getTaskStatus("task-20260418-103000523-a1b2");
JSONObject json = new JSONObject(status);
System.out.println("任务状态: " + json.getString("task_status"));
```

**Python 示例：**

```python
def get_task_status(self, task_id: str) -> dict:
    """查询任务状态"""
    response = requests.get(
        f"{self.base_url}/task/status/{task_id}",
        headers=self.headers
    )
    return response.json()

# 调用示例
status = groot.get_task_status("task-20260418-103000523-a1b2")
print(f"任务状态: {status['task_status']}")
if status['task_status'] == 'running':
    print(f"进度: {status['progress']['percentage']}%")
```

**Go 示例：**

```go
// GetTaskStatus 查询任务状态
func (c *Client) GetTaskStatus(ctx context.Context, taskID string) (map[string]interface{}, error) {
    httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/task/status/"+taskID, nil)
    c.setHeaders(httpReq)
    
    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    return result, nil
}

// 调用示例
status, _ := client.GetTaskStatus(ctx, "task-20260418-103000523-a1b2")
fmt.Println("任务状态:", status["task_status"])
```

---

### 7.5 GET /task/{task_id} - 查询任务详情

**功能说明：**

查询任务的完整详情，包括指令、结果、错误信息、完整执行步骤记录。适用于任务完成后查看详细执行过程。

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `task_id` | string | 是 | 任务 ID（路径参数） |

**响应字段：**

| 字段 | 说明 |
|------|------|
| `status` | 查询结果：`success` 或 `task_not_found` |
| `task` | 任务详情对象 |

**task 对象字段：**

| 字段 | 说明 |
|------|------|
| `id` | 任务 ID |
| `instruction` | 用户指令 |
| `prompt` | 系统提示词 |
| `status` | 任务状态 |
| `start_time` | 开始时间 |
| `end_time` | 结束时间 |
| `duration` | 耗时（秒） |
| `caller` | 调用方名称 |
| `result` | 任务结果（成功时） |
| `error` | 错误信息（失败时） |
| `steps` | 步骤记录列表 |

**成功响应：**

```json
{
  "status": "success",
  "task": {
    "id": "task-20260418-103000523-a1b2",
    "instruction": "分析这份PDF报告",
    "status": "completed",
    "start_time": "2026-04-18T10:30:00Z",
    "end_time": "2026-04-18T10:30:45Z",
    "duration": 45,
    "caller": "business_system",
    "result": "分析结果...",
    "steps": [
      {"step_id": "xxx", "type": "tool", "name": "file_read", "status": "success"},
      {"step_id": "yyy", "type": "llm", "name": "model_response", "status": "success"}
    ]
  }
}
```

**请求示例：**

```bash
curl http://localhost:8080/task/task-20260418-103000523-a1b2 \
  -H "X-API-Key: your-api-key"
```

**Java 示例：**

```java
public String getTaskDetail(String taskId) throws Exception {
    Request request = new Request.Builder()
        .url(baseUrl + "/task/" + taskId)
        .header("X-API-Key", apiKey)
        .get()
        .build();
    
    Response response = client.newCall(request).execute();
    return response.body().string();
}

// 调用示例
String detail = groot.getTaskDetail("task-20260418-103000523-a1b2");
JSONObject json = new JSONObject(detail);
JSONObject task = json.getJSONObject("task");
System.out.println("指令: " + task.getString("instruction"));
System.out.println("步骤数: " + task.getJSONArray("steps").length());
```

**Python 示例：**

```python
def get_task_detail(self, task_id: str) -> dict:
    """查询任务详情"""
    response = requests.get(
        f"{self.base_url}/task/{task_id}",
        headers=self.headers
    )
    return response.json()

# 调用示例
detail = groot.get_task_detail("task-20260418-103000523-a1b2")
task = detail['task']
print(f"指令: {task['instruction']}")
print(f"步骤数: {len(task['steps'])}")
for step in task['steps']:
    print(f"  - {step['type']}: {step['name']} ({step['status']})")
```

**Go 示例：**

```go
// GetTaskDetail 查询任务详情
func (c *Client) GetTaskDetail(ctx context.Context, taskID string) (map[string]interface{}, error) {
    httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/task/"+taskID, nil)
    c.setHeaders(httpReq)
    
    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    return result, nil
}

// 调用示例
detail, _ := client.GetTaskDetail(ctx, "task-20260418-103000523-a1b2")
task := detail["task"].(map[string]interface{})
fmt.Println("指令:", task["instruction"])
fmt.Println("步骤数:", len(task["steps"].([]interface{})))
```

---

### 7.6 GET /task/history - 查询历史任务列表

**功能说明：**

查询历史任务列表，支持按状态、时间范围过滤和分页。适用于查看历史执行记录、统计分析。

**请求参数（Query 参数）：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `status` | string | 否 | 按状态过滤：`running` / `completed` / `failed` / `cancelled` |
| `start_time` | string | 否 | 开始时间，格式 `yyyyMMddHHmm`，如 `202604010000` |
| `end_time` | string | 否 | 结束时间，格式 `yyyyMMddHHmm`，如 `202604182359` |
| `limit` | int | 否 | 返回数量限制，默认 20，最大 100 |
| `offset` | int | 否 | 分页偏移，默认 0 |

**响应字段：**

| 字段 | 说明 |
|------|------|
| `status` | 查询结果：`success` |
| `total` | 符合条件的总数 |
| `limit` | 当前返回数量 |
| `offset` | 当前偏移 |
| `tasks` | 任务列表 |

**响应示例：**

```json
{
  "status": "success",
  "total": 50,
  "limit": 10,
  "offset": 0,
  "tasks": [
    {
      "id": "task-20260418-103000523-a1b2",
      "instruction": "分析PDF报告",
      "status": "completed",
      "start_time": "2026-04-18T10:30:00Z",
      "end_time": "2026-04-18T10:30:45Z",
      "duration": 45,
      "caller": "business_system"
    },
    {
      "id": "task-20260418-103010000-b2c3",
      "instruction": "写排序函数",
      "status": "completed",
      "start_time": "2026-04-18T10:31:00Z",
      "end_time": "2026-04-18T10:31:30Z",
      "duration": 30,
      "caller": "admin"
    }
  ]
}
```

**请求示例：**

```bash
curl "http://localhost:8080/task/history?status=completed&limit=10" \
  -H "X-API-Key: your-api-key"
```

**Java 示例：**

```java
public String getHistory(String status, int limit, int offset) throws Exception {
    HttpUrl url = HttpUrl.parse(baseUrl + "/task/history")
        .newBuilder()
        .addQueryParameter("status", status)
        .addQueryParameter("limit", String.valueOf(limit))
        .addQueryParameter("offset", String.valueOf(offset))
        .build();
    
    Request request = new Request.Builder()
        .url(url)
        .header("X-API-Key", apiKey)
        .get()
        .build();
    
    Response response = client.newCall(request).execute();
    return response.body().string();
}

// 调用示例
String history = groot.getHistory("completed", 10, 0);
JSONObject json = new JSONObject(history);
System.out.println("总数: " + json.getInt("total"));
JSONArray tasks = json.getJSONArray("tasks");
for (int i = 0; i < tasks.length(); i++) {
    JSONObject task = tasks.getJSONObject(i);
    System.out.println("  - " + task.getString("id") + ": " + task.getString("instruction"));
}
```

**Python 示例：**

```python
def get_history(self, status: str = None, limit: int = 20, offset: int = 0) -> dict:
    """查询历史任务"""
    params = {"limit": limit, "offset": offset}
    if status:
        params["status"] = status
    
    response = requests.get(
        f"{self.base_url}/task/history",
        headers=self.headers,
        params=params
    )
    return response.json()

# 调用示例
history = groot.get_history(status="completed", limit=10)
print(f"总数: {history['total']}")
for task in history['tasks']:
    print(f"  - {task['id']}: {task['instruction']} ({task['duration']}s)")
```

**Go 示例：**

```go
// GetHistory 查询历史任务
func (c *Client) GetHistory(ctx context.Context, status string, limit int, offset int) (map[string]interface{}, error) {
    url := fmt.Sprintf("%s/task/history?limit=%d&offset=%d", c.baseURL, limit, offset)
    if status != "" {
        url += "&status=" + status
    }
    
    httpReq, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    c.setHeaders(httpReq)
    
    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    return result, nil
}

// 调用示例
history, _ := client.GetHistory(ctx, "completed", 10, 0)
fmt.Println("总数:", history["total"])
tasks := history["tasks"].([]interface{})
for _, t := range tasks {
    task := t.(map[string]interface{})
    fmt.Printf("  - %s: %s (%ds)\n", task["id"], task["instruction"], int(task["duration"].(float64)))
}
```

---

### 7.7 GET /health - 健康检查

**功能说明：**

查询服务健康状态，包括 LLM 连接、MCP 服务、Skills 加载情况。适用于监控和运维检查。

**请求参数：** 无需认证，无需参数。

**响应字段：**

| 字段 | 说明 |
|------|------|
| `status` | 健康状态：`healthy` / `unhealthy` |
| `version` | 服务版本 |
| `uptime` | 运行时长 |
| `checks` | 各组件健康检查结果 |
| `metrics` | 运行指标 |

**响应示例：**

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "healthy", "model": "gpt-4o"},
    "mcp_servers": {"status": "healthy", "servers": ["file_operations", "http_request"]},
    "skills": {"status": "healthy", "count": 12},
    "memory": {"status": "healthy", "used_mb": 256}
  },
  "metrics": {
    "tasks_running": 5,
    "success_rate": 0.98
  }
}
```

**请求示例：**

```bash
curl http://localhost:8080/health
```

**Java 示例：**

```java
public String healthCheck() throws Exception {
    Request request = new Request.Builder()
        .url(baseUrl + "/health")
        .get()
        .build();
    
    Response response = client.newCall(request).execute();
    return response.body().string();
}

// 调用示例
String health = groot.healthCheck();
JSONObject json = new JSONObject(health);
System.out.println("状态: " + json.getString("status"));
System.out.println("运行时长: " + json.getString("uptime"));
```

**Python 示例：**

```python
def health_check(self) -> dict:
    """健康检查"""
    response = requests.get(f"{self.base_url}/health")
    return response.json()

# 调用示例
health = groot.health_check()
print(f"状态: {health['status']}")
print(f"运行时长: {health['uptime']}")
print(f"Skills 数量: {health['checks']['skills']['count']}")
```

**Go 示例：**

```go
// HealthCheck 健康检查
func (c *Client) HealthCheck(ctx context.Context) (map[string]interface{}, error) {
    httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
    
    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    return result, nil
}

// 调用示例
health, _ := client.HealthCheck(ctx)
fmt.Println("状态:", health["status"])
fmt.Println("运行时长:", health["uptime"])
```

---

### 7.8 GET /skills - 列出可用 Skills

**功能说明：**

列出当前已加载的 Skills，显示名称和描述。用于查看可用的任务模板。

**响应字段：**

| 字段 | 说明 |
|------|------|
| `skills` | Skills 列表 |
| `total` | 总数 |

**Skills 对象字段：**

| 字段 | 说明 |
|------|------|
| `name` | Skill 名称 |
| `description` | Skill 描述 |

**响应示例：**

```json
{
  "skills": [
    {"name": "pdf_analyzer", "description": "分析PDF文档并生成摘要"},
    {"name": "code_generator", "description": "根据需求生成代码"},
    {"name": "data_analyzer", "description": "分析结构化数据文件"}
  ],
  "total": 3
}
```

**请求示例：**

```bash
curl http://localhost:8080/skills \
  -H "X-API-Key: your-api-key"
```

**Java 示例：**

```java
public String listSkills() throws Exception {
    Request request = new Request.Builder()
        .url(baseUrl + "/skills")
        .header("X-API-Key", apiKey)
        .get()
        .build();
    
    Response response = client.newCall(request).execute();
    return response.body().string();
}

// 调用示例
String skills = groot.listSkills();
JSONObject json = new JSONObject(skills);
JSONArray skillsArray = json.getJSONArray("skills");
for (int i = 0; i < skillsArray.length(); i++) {
    JSONObject skill = skillsArray.getJSONObject(i);
    System.out.println("  - " + skill.getString("name") + ": " + skill.getString("description"));
}
```

**Python 示例：**

```python
def list_skills(self) -> dict:
    """列出 Skills"""
    response = requests.get(
        f"{self.base_url}/skills",
        headers=self.headers
    )
    return response.json()

# 调用示例
skills = groot.list_skills()
for skill in skills['skills']:
    print(f"  - {skill['name']}: {skill['description']}")
```

**Go 示例：**

```go
// ListSkills 列出 Skills
func (c *Client) ListSkills(ctx context.Context) (map[string]interface{}, error) {
    httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/skills", nil)
    c.setHeaders(httpReq)
    
    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    return result, nil
}

// 调用示例
skills, _ := client.ListSkills(ctx)
for _, s := range skills["skills"].([]interface{}) {
    skill := s.(map[string]interface{})
    fmt.Printf("  - %s: %s\n", skill["name"], skill["description"])
}
```

---

### 7.9 GET /tools - 列出可用 MCP 工具

**功能说明：**

列出当前已注册的 MCP 工具，显示名称、描述和所属 MCP。用于查看可用的工具列表。

**响应字段：**

| 字段 | 说明 |
|------|------|
| `tools` | 工具列表 |
| `total` | 总数 |

**Tools 对象字段：**

| 字段 | 说明 |
|------|------|
| `name` | 工具名称 |
| `description` | 工具描述 |
| `mcp` | 所属 MCP 服务名称 |

**响应示例：**

```json
{
  "tools": [
    {"name": "file_read", "description": "读取文件内容", "mcp": "file_operations"},
    {"name": "file_write", "description": "写入文件内容", "mcp": "file_operations"},
    {"name": "http_get", "description": "发送HTTP GET请求", "mcp": "http_request"},
    {"name": "http_post", "description": "发送HTTP POST请求", "mcp": "http_request"}
  ],
  "total": 4
}
```

**请求示例：**

```bash
curl http://localhost:8080/tools \
  -H "X-API-Key: your-api-key"
```

**Java 示例：**

```java
public String listTools() throws Exception {
    Request request = new Request.Builder()
        .url(baseUrl + "/tools")
        .header("X-API-Key", apiKey)
        .get()
        .build();
    
    Response response = client.newCall(request).execute();
    return response.body().string();
}

// 调用示例
String tools = groot.listTools();
JSONObject json = new JSONObject(tools);
JSONArray toolsArray = json.getJSONArray("tools");
for (int i = 0; i < toolsArray.length(); i++) {
    JSONObject tool = toolsArray.getJSONObject(i);
    System.out.println("  - " + tool.getString("name") + " [" + tool.getString("mcp") + "]");
}
```

**Python 示例：**

```python
def list_tools(self) -> dict:
    """列出 MCP 工具"""
    response = requests.get(
        f"{self.base_url}/tools",
        headers=self.headers
    )
    return response.json()

# 调用示例
tools = groot.list_tools()
for tool in tools['tools']:
    print(f"  - {tool['name']} [{tool['mcp']}]: {tool['description']}")
```

**Go 示例：**

```go
// ListTools 列出 MCP 工具
func (c *Client) ListTools(ctx context.Context) (map[string]interface{}, error) {
    httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/tools", nil)
    c.setHeaders(httpReq)
    
    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    return result, nil
}

// 调用示例
tools, _ := client.ListTools(ctx)
for _, t := range tools["tools"].([]interface{}) {
    tool := t.(map[string]interface{})
    fmt.Printf("  - %s [%s]: %s\n", tool["name"], tool["mcp"], tool["description"])
}
```

---

## 八、完整客户端代码

以下是三种语言的完整客户端封装代码，整合了所有 API 调用方法。

### 8.1 Java 完整客户端

```java
import okhttp3.*;
import okhttp3.sse.EventSource;
import okhttp3.sse.EventSourceListener;
import okhttp3.sse.EventSources;
import java.util.Base64;
import java.nio.file.Files;
import java.nio.file.Paths;

public class GrootClient {
    
    private final OkHttpClient client;
    private final String baseUrl;
    private final String apiKey;
    
    public GrootClient(String baseUrl, String apiKey) {
        this.baseUrl = baseUrl;
        this.apiKey = apiKey;
        this.client = new OkHttpClient.Builder()
            .connectTimeout(30, java.util.concurrent.TimeUnit.SECONDS)
            .readTimeout(300, java.util.concurrent.TimeUnit.SECONDS)
            .build();
    }
    
    // ========== 执行任务 ==========
    
    public void executeTask(String instruction, EventSourceListener listener) throws Exception {
        String jsonBody = "{\"instruction\":\"" + instruction + "\"}";
        
        Request request = new Request.Builder()
            .url(baseUrl + "/task/execute")
            .header("X-API-Key", apiKey)
            .header("Content-Type", "application/json")
            .header("Accept", "text/event-stream")
            .post(RequestBody.create(jsonBody, MediaType.parse("application/json")))
            .build();
        
        EventSources.createFactory(client).newEventSource(request, listener);
    }
    
    public void executeTaskWithFile(String instruction, String filePath, EventSourceListener listener) throws Exception {
        byte[] fileContent = Files.readAllBytes(Paths.get(filePath));
        String base64Content = Base64.getEncoder().encodeToString(fileContent);
        String fileName = Paths.get(filePath).getFileName().toString();
        
        String jsonBody = String.format(
            "{\"instruction\":\"%s\",\"attachments\":[{\"type\":\"file\",\"name\":\"%s\",\"content\":\"%s\"}]}",
            instruction, fileName, base64Content
        );
        
        Request request = new Request.Builder()
            .url(baseUrl + "/task/execute")
            .header("X-API-Key", apiKey)
            .header("Content-Type", "application/json")
            .header("Accept", "text/event-stream")
            .post(RequestBody.create(jsonBody, MediaType.parse("application/json")))
            .build();
        
        EventSources.createFactory(client).newEventSource(request, listener);
    }
    
    // ========== 取消任务 ==========
    
    public String cancelTask(String taskId) throws Exception {
        Request request = new Request.Builder()
            .url(baseUrl + "/task/" + taskId)
            .header("X-API-Key", apiKey)
            .delete()
            .build();
        
        Response response = client.newCall(request).execute();
        return response.body().string();
    }
    
    // ========== 查询状态 ==========
    
    public String getTaskStatus(String taskId) throws Exception {
        Request request = new Request.Builder()
            .url(baseUrl + "/task/status/" + taskId)
            .header("X-API-Key", apiKey)
            .get()
            .build();
        
        Response response = client.newCall(request).execute();
        return response.body().string();
    }
    
    // ========== 查询详情 ==========
    
    public String getTaskDetail(String taskId) throws Exception {
        Request request = new Request.Builder()
            .url(baseUrl + "/task/" + taskId)
            .header("X-API-Key", apiKey)
            .get()
            .build();
        
        Response response = client.newCall(request).execute();
        return response.body().string();
    }
    
    // ========== 查询历史 ==========
    
    public String getHistory(String status, int limit, int offset) throws Exception {
        HttpUrl url = HttpUrl.parse(baseUrl + "/task/history")
            .newBuilder()
            .addQueryParameter("limit", String.valueOf(limit))
            .addQueryParameter("offset", String.valueOf(offset))
            .build();
        if (status != null) {
            url = url.newBuilder().addQueryParameter("status", status).build();
        }
        
        Request request = new Request.Builder()
            .url(url)
            .header("X-API-Key", apiKey)
            .get()
            .build();
        
        Response response = client.newCall(request).execute();
        return response.body().string();
    }
    
    // ========== 健康检查 ==========
    
    public String healthCheck() throws Exception {
        Request request = new Request.Builder()
            .url(baseUrl + "/health")
            .get()
            .build();
        
        Response response = client.newCall(request).execute();
        return response.body().string();
    }
    
    // ========== 列出 Skills ==========
    
    public String listSkills() throws Exception {
        Request request = new Request.Builder()
            .url(baseUrl + "/skills")
            .header("X-API-Key", apiKey)
            .get()
            .build();
        
        Response response = client.newCall(request).execute();
        return response.body().string();
    }
    
    // ========== 列出 Tools ==========
    
    public String listTools() throws Exception {
        Request request = new Request.Builder()
            .url(baseUrl + "/tools")
            .header("X-API-Key", apiKey)
            .get()
            .build();
        
        Response response = client.newCall(request).execute();
        return response.body().string();
    }
}
```

**Maven 依赖：**

```xml
<dependencies>
    <dependency>
        <groupId>com.squareup.okhttp3</groupId>
        <artifactId>okhttp</artifactId>
        <version>4.12.0</version>
    </dependency>
    <dependency>
        <groupId>com.squareup.okhttp3</groupId>
        <artifactId>okhttp-sse</artifactId>
        <version>4.12.0</version>
    </dependency>
</dependencies>
```

---

### 8.2 Python 完整客户端

```python
import requests
import json
import base64
from pathlib import Path

class GrootClient:
    """Groot AI Agent 完整客户端"""
    
    def __init__(self, base_url: str, api_key: str = None):
        self.base_url = base_url
        self.headers = {"Content-Type": "application/json"}
        if api_key:
            self.headers["X-API-Key"] = api_key
    
    # ========== 执行任务 ==========
    
    def execute_task(self, instruction: str, attachments: list = None, callback: callable = None) -> str:
        """
        执行任务（SSE 流式返回）
        
        Args:
            instruction: 自然语言指令
            attachments: 附件列表 [{"type": "file", "name": "xxx", "path": "/path"}]
            callback: SSE 事件回调 callback(event_type, data)
        
        Returns:
            task_id: 任务 ID
        """
        body = {"instruction": instruction}
        
        if attachments:
            processed = []
            for att in attachments:
                if att["type"] == "file":
                    path = att.get("path", att.get("content"))
                    name = att.get("name", Path(path).name if "path" in att else "file")
                    if "path" in att:
                        with open(path, "rb") as f:
                            content = base64.b64encode(f.read()).decode()
                    else:
                        content = att["content"]
                    processed.append({"type": "file", "name": name, "content": content})
                elif att["type"] == "url":
                    processed.append({"type": "url", "name": att["name"], "content": att["url"]})
            body["attachments"] = processed
        
        response = requests.post(
            f"{self.base_url}/task/execute",
            headers=self.headers,
            json=body,
            stream=True
        )
        
        task_id = response.headers.get("X-Task-ID")
        
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
        
        return task_id
    
    # ========== 取消任务 ==========
    
    def cancel_task(self, task_id: str) -> dict:
        """取消任务"""
        response = requests.delete(
            f"{self.base_url}/task/{task_id}",
            headers=self.headers
        )
        return response.json()
    
    # ========== 查询状态 ==========
    
    def get_task_status(self, task_id: str) -> dict:
        """查询任务状态"""
        response = requests.get(
            f"{self.base_url}/task/status/{task_id}",
            headers=self.headers
        )
        return response.json()
    
    # ========== 查询详情 ==========
    
    def get_task_detail(self, task_id: str) -> dict:
        """查询任务详情"""
        response = requests.get(
            f"{self.base_url}/task/{task_id}",
            headers=self.headers
        )
        return response.json()
    
    # ========== 查询历史 ==========
    
    def get_history(self, status: str = None, limit: int = 20, offset: int = 0) -> dict:
        """查询历史任务列表"""
        params = {"limit": limit, "offset": offset}
        if status:
            params["status"] = status
        
        response = requests.get(
            f"{self.base_url}/task/history",
            headers=self.headers,
            params=params
        )
        return response.json()
    
    # ========== 健康检查 ==========
    
    def health_check(self) -> dict:
        """健康检查"""
        response = requests.get(f"{self.base_url}/health")
        return response.json()
    
    # ========== 列出 Skills ==========
    
    def list_skills(self) -> dict:
        """列出可用 Skills"""
        response = requests.get(
            f"{self.base_url}/skills",
            headers=self.headers
        )
        return response.json()
    
    # ========== 列出 Tools ==========
    
    def list_tools(self) -> dict:
        """列出可用 MCP 工具"""
        response = requests.get(
            f"{self.base_url}/tools",
            headers=self.headers
        )
        return response.json()
```

**安装依赖：**

```bash
pip install requests
```

---

### 8.3 Go 完整客户端

```go
package grootclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client Groot API 客户端
type Client struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewClient 创建客户端
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 300 * time.Second,
		},
	}
}

// Attachment 附件结构
type Attachment struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ExecuteRequest 执行请求
type ExecuteRequest struct {
	Instruction string       `json:"instruction"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// SSEEvent SSE 事件
type SSEEvent struct {
	Type string
	Data string
}

// ========== 执行任务 ==========

func (c *Client) ExecuteTask(ctx context.Context, req ExecuteRequest, callback func(SSEEvent)) (taskID string, err error) {
	body, _ := json.Marshal(req)
	
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/task/execute", bytes.NewReader(body))
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")
	
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	taskID = resp.Header.Get("X-Task-ID")
	c.processSSE(resp.Body, callback)
	
	return taskID, nil
}

func (c *Client) ExecuteTaskWithFile(ctx context.Context, instruction, filePath string, callback func(SSEEvent)) (taskID string, err error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	
	fileName := filePath
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		fileName = filePath[idx+1:]
	}
	
	req := ExecuteRequest{
		Instruction: instruction,
		Attachments: []Attachment{
			{Type: "file", Name: fileName, Content: base64.StdEncoding.EncodeToString(content)},
		},
	}
	
	return c.ExecuteTask(ctx, req, callback)
}

func (c *Client) processSSE(body io.ReadCloser, callback func(SSEEvent)) {
	buf := make([]byte, 4096)
	var eventType string
	var buffer string
	
	for {
		n, err := body.Read(buf)
		if err != nil {
			break
		}
		buffer += string(buf[:n])
		
		for {
			idx := strings.Index(buffer, "\n")
			if idx == -1 {
				break
			}
			line := buffer[:idx]
			buffer = buffer[idx+1:]
			
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimPrefix(line, "event:")
			} else if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
				if callback != nil {
					callback(SSEEvent{Type: eventType, Data: data})
				}
			}
		}
	}
}

// ========== 取消任务 ==========

func (c *Client) CancelTask(ctx context.Context, taskID string) (map[string]interface{}, error) {
	httpReq, _ := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/task/"+taskID, nil)
	c.setHeaders(httpReq)
	return c.doRequest(httpReq)
}

// ========== 查询状态 ==========

func (c *Client) GetTaskStatus(ctx context.Context, taskID string) (map[string]interface{}, error) {
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/task/status/"+taskID, nil)
	c.setHeaders(httpReq)
	return c.doRequest(httpReq)
}

// ========== 查询详情 ==========

func (c *Client) GetTaskDetail(ctx context.Context, taskID string) (map[string]interface{}, error) {
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/task/"+taskID, nil)
	c.setHeaders(httpReq)
	return c.doRequest(httpReq)
}

// ========== 查询历史 ==========

func (c *Client) GetHistory(ctx context.Context, status string, limit, offset int) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/task/history?limit=%d&offset=%d", c.baseURL, limit, offset)
	if status != "" {
		url += "&status=" + status
	}
	
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	c.setHeaders(httpReq)
	return c.doRequest(httpReq)
}

// ========== 健康检查 ==========

func (c *Client) HealthCheck(ctx context.Context) (map[string]interface{}, error) {
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	return c.doRequest(httpReq)
}

// ========== 列出 Skills ==========

func (c *Client) ListSkills(ctx context.Context) (map[string]interface{}, error) {
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/skills", nil)
	c.setHeaders(httpReq)
	return c.doRequest(httpReq)
}

// ========== 列出 Tools ==========

func (c *Client) ListTools(ctx context.Context) (map[string]interface{}, error) {
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/tools", nil)
	c.setHeaders(httpReq)
	return c.doRequest(httpReq)
}

// ========== 辅助方法 ==========

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
}

func (c *Client) doRequest(httpReq *http.Request) (map[string]interface{}, error) {
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	return result, err
}
```

---

## 九、完整使用示例

### 9.1 场景一：文档分析

**用户需求：** 上传一份 PDF 财报，分析关键财务指标。

**步骤：**

1. 准备 PDF 文件（如 `Q3_Report.pdf`）
2. 编写调用代码（以 Python 为例）

```python
groot = GrootClient("http://localhost:8080", "your-api-key")

# 定义回调处理
def handle_event(event_type, data):
    if event_type == "intent":
        print("任务开始...")
    elif event_type == "progress":
        print(f"进度: {data}")
    elif event_type == "completed":
        result = json.loads(data)
        if result["status"] == "success":
            print("分析结果:", result["result"])
        else:
            print("失败:", result["error"])

# 执行任务
task_id = groot.execute_task(
    instruction="分析这份财报，提取营收、利润、增长率等关键指标",
    attachments=[
        {"type": "file", "path": "Q3_Report.pdf"}
    ],
    callback=handle_event
)
```

**预期输出：**

```
任务 ID: task-20260418-103000523-a1b2
任务开始...
进度: 正在读取PDF...
进度: 正在提取关键信息...
进度: 正在生成分析报告...
分析结果: {
  "document_type": "财务报告",
  "key_metrics": {
    "revenue": "125.6亿",
    "profit": "18.2亿",
    "growth_rate": "12.3%"
  },
  "summary": "..."
}
```

---

### 9.2 场景二：代码生成

**用户需求：** 生成一个 Python 数据处理工具。

```python
task_id = groot.execute_task(
    instruction="写一个 Python 工具类，包含 CSV 读取、数据清洗、统计分析功能",
    prompt="你是资深 Python 开发者，注重代码质量和可维护性",
    callback=handle_event
)
```

---

### 9.3 场景三：多文件对比

**用户需求：** 对比两份合同文件，找出差异条款。

```python
task_id = groot.execute_task(
    instruction="对比这份合同和标准模板，列出所有修改过的条款",
    attachments=[
        {"type": "file", "path": "signed_contract.pdf"},
        {"type": "file", "path": "template.pdf"}
    ],
    callback=handle_event
)
```

---

### 9.4 场景四：数据分析

**用户需求：** 分析 CSV 销售数据，计算趋势。

```python
task_id = groot.execute_task(
    instruction="分析销售数据，计算月度趋势、同比增长，输出 JSON 格式报告",
    attachments=[
        {"type": "file", "path": "sales_2023.csv"}
    ],
    callback=handle_event
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

### Q2: 附件上传后 Agent 无法读取

**原因：** MCP `file_operations` 配置了 `allowed_paths` 限制。

**解决：** 检查 MCP 配置文件，确保 `allowed_paths` 包含附件临时目录，或设置为空数组（无限制）：

```json
{
  "restrictions": {
    "allowed_paths": []
  }
}
```

---

### Q3: 任务执行超时

**原因：** LLM 响应慢或任务复杂。

**解决：** 调整超时配置：

```yaml
performance:
  timeout:
    task_max_duration: 600   # 增加到 10 分钟
    llm_call_timeout: 120    # LLM 调用超时增加
```

---

### Q4: 如何查看执行日志

**方法：**

```bash
# 查看实时日志
tail -f ~/.groot/logs/groot-{date}.log

# 搜索错误日志
grep "error" ~/.groot/logs/groot-*.log
```

---

### Q5: 如何切换 LLM 模型

**步骤：**

1. 修改配置文件 `config.yaml`：

```yaml
llm:
  active_model: claude-3.5  # 切换到 Claude
```

2. 重启服务

```bash
groot -H ~/.groot
```

---

### Q6: 认证失败 401 Unauthorized

**原因：** API Key 无效或未携带。

**解决：**

1. 确认配置了正确的 API Key
2. 检查请求头是否携带 `X-API-Key`

```http
X-API-Key: your-secret-key
```

---

## 十一、最佳实践

### 11.1 生产部署建议

| 建议 | 说明 |
|------|------|
| 使用绝对路径 | `temp_directory` 配置到独立磁盘，避免磁盘空间不足 |
| 启用认证 | 配置 API Key，区分不同调用方权限 |
| 监控日志 | 接入日志采集系统（ELK 等），监控错误和性能 |
| 定期清理 | 根据业务需要调整 `retention_days` |
| 限流配置 | 根据并发量调整 `max_concurrent_tasks` |

### 11.2 Skills 编写建议

- 描述清晰：让 Agent 理解何时使用此 Skill
- 步骤明确：按顺序定义执行步骤
- 输出格式：指定结构化输出格式（如 JSON）
- 避免冗余：只写必要的指令内容

### 11.3 性能优化建议

- 控制附件大小：建议单个附件 < 10MB
- 控制并发量：根据 LLM API 限制调整并发配置
- 监控 Token 消耗：设置 `max_tokens` 防止成本失控

---

## 附录

### A. 端口说明

| 端口 | 服务 |
|------|------|
| 8080 | HTTP API（默认） |

### B. 环境变量列表

| 变量 | 说明 | 必需性 |
|------|------|--------|
| `OPENAI_API_KEY` | OpenAI API 密钥 | 配置文件使用 `${OPENAI_API_KEY}` 时必需 |
| `ANTHROPIC_API_KEY` | Anthropic API 密钥 | 配置文件引用时需要 |
| `GROOT_ADMIN_KEY` | 管理员 API Key | 配置文件引用时需要（可自定义变量名） |
| `GROOT_MONITOR_KEY` | 监控服务 API Key | 配置文件引用时需要（可自定义变量名） |
| `GROOT_HOME` | 工作目录 | 否（默认 ~/.groot） |

> **说明：**
> - 环境变量是否必需取决于配置文件中的写法
> - LLM API Key 和认证 API Key 都可以配置多个
> - 环境变量名可以自定义，配置文件中用 `${变量名}` 引用即可
> - 例如：`key: ${MY_CUSTOM_KEY}` → 对应环境变量 `MY_CUSTOM_KEY`

### C. 联系与支持

- GitHub: https://github.com/zfd81/groot
- 问题反馈: GitHub Issues