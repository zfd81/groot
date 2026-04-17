# Groot AI Agent 设计文档

**版本:** 1.0.0
**日期:** 2026-04-16
**状态:** 设计完成，待实现

---

## 一、概述

### 1.1 项目定位

Groot 是一个通过 REST API 提供服务的 AI Agent，作为"AI 能力中间层"为其他系统提供智能任务执行能力。

**核心特性：**
- 接收自然语言指令和附件
- 自动判断意图，调用预置 Skills 或自主决策执行
- 基于 eino 框架构建 Agent，自主决策调用 MCP 工具
- SSE 流式返回执行进度和结果
- 支持 Skills 嵌套调用

### 1.2 技术栈

| 组件 | 技术选型 |
|------|---------|
| HTTP 框架 | Hertz（字节开源） |
| Agent 框架 | eino（字节开源） |
| LLM 调用 | OpenAI 兼容协议 |
| 配置格式 | YAML |
| 日志格式 | JSON 结构化 |
| 监控 | Prometheus Metrics |

---

## 二、整体架构

### 2.1 架构图

```
┌─────────────────────────────────────────────────────────────┐
│                      REST API Layer (Hertz)                  │
│  - 接收请求（指令 + prompt + 附件）                            │
│  - SSE 流式返回进度和结果                                      │
│  - 限流、超时控制                                              │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      Agent Engine (eino)                     │
│  - ReAct 执行模式（Reasoning + Acting + Observation）        │
│  - 注册 Skills 和 MCP 工具列表                                 │
│  - Agent 自主决策调用工具或直接生成回答                        │
│  - 循环执行直到任务完成或达到限制                               │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      MCP Manager                             │
│  - 加载内置 MCP（file_operations、http_request）              │
│  - 从配置文件加载外部 MCP                                      │
│  - 提供 MCP 工具列表给 Agent                                   │
│  - 执行 MCP 工具调用、权限检查                                  │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    Config & Registry                         │
│  - LLM 配置（多模型，OpenAI 协议）                              │
│  - Skills 目录扫描注册、热插拔                                  │
│  - MCP 配置文件解析                                            │
│  - ReAct 执行限制配置                                          │
│  - 日志配置                                                    │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 模块职责

| 模块 | 职责 | 扩展预留点 |
|------|------|-----------|
| REST API Layer | 请求解析、响应封装、限流、超时控制 | 可拆分为独立 Gateway |
| Agent Engine | ReAct 执行、工具注册、自主决策 | 支持多 Agent 协作扩展 |
| MCP Manager | MCP 加载、工具调用、权限检查 | 支持动态 MCP 加载/卸载 |
| Config & Registry | 配置管理、Skills 注册、热插拔 | 支持分布式配置中心 |

### 2.3 ReAct 执行模式

Agent 使用 ReAct（Reasoning + Acting + Observation）模式执行任务：

```
用户指令 → Agent 开始 →
│
循环：│
      ├─ Reasoning（思考）：LLM 分析当前状态，决定下一步动作
      │   ├─ 调用某个 Skill（如指令中提及或Agent判断需要）
      │   ├─ 调用某个 MCP 工具
      │   └─ 直接生成回答（任务完成）
      │
      ├─ Acting（执行）：执行决定的动作
      │   ├─ Skill 调用 → 递归执行
      │   ├─ MCP 工具调用 → MCP Manager 执行
      │   └─ LLM 生成 → 直接输出
      │
      ├─ Observation（观察）：获取执行结果，更新上下文
      │
      ├─ SSE 推送进度事件
      │
      └─ 检查终止条件
          ├─ 任务完成 → 推送 completed 事件，结束
          ├─ 达到最大循环次数 → 推送 error 事件，终止
          ├─ Token 消耗超限 → 推送 error 事件，终止
          ├─ 单步失败 → Agent 判断是否重试或终止
          └─ 继续循环
│
→ 循环结束，返回最终结果
```

### 2.4 工具注册机制

**Skills 注册给 Agent：**
- 启动时扫描 skills 目录，解析每个 skill.md
- 将 Skill 的 Instructions 作为工具描述注册给 eino Agent
- Skill 中的 Dependencies 在执行时递归加载

**MCP 工具注册给 Agent：**
- 内置 MCP 工具自动注册
- 外部 MCP 工具从配置加载并注册
- 每个工具包含：名称、描述、参数定义

**Agent 工具列表示例：**

| 工具类型 | 名称 | 描述 |
|---------|------|------|
| Skill | pdf_analyzer | 分析PDF文档并生成摘要 |
| Skill | code_generator | 根据需求生成代码 |
| MCP | file_read | 读取文件内容 |
| MCP | http_get | 发送HTTP GET请求 |

### 2.5 循环终止条件

| 条件 | 说明 |
|------|------|
| Agent 判断完成 | LLM 输出"任务完成"或最终答案 |
| 达到最大循环次数 | 防止无限循环，配置限制 |
| Token 消耗超限 | 防止成本失控，配置限制 |
| 单步执行超时 | 单个动作执行超时 |
| 用户取消 | 通过 API 主动取消任务 |

---

## 三、API 设计

### 3.1 API 列表

| API | 方法 | 用途 |
|-----|------|------|
| `/task/execute` | POST | 执行任务，SSE 流式返回 |
| `/task/cancel` | POST | 取消正在执行的任务 |
| `/task/status/{task_id}` | GET | 查询任务状态 |
| `/health` | GET | 健康检查 |
| `/skills` | GET | 列出可用 Skills |
| `/tools` | GET | 列出可用 MCP 工具 |

### 3.2 POST /task/execute

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

Agent 会自动分析指令，决定调用 Skills 或自主执行任务。

**SSE 响应事件类型：**

| 事件类型 | 发送频率 | 说明 |
|---------|---------|------|
| `intent` | 1次 | 任务开始，标记执行起点 |
| `step_start` | 多次 | 步骤开始（Skill/工具/LLM调用） |
| `progress` | 多次 | 中间进度更新 |
| `step_end` | 多次 | 步骤结束（含状态、时间戳） |
| `completed` | 1次 | 任务完成（含最终结果） |

**响应 Header 元信息：**

| Header | 说明 |
|--------|------|
| `X-Task-ID` | 任务唯一标识（请求发起时立即返回） |
| `Content-Type` | `text/event-stream` |

**说明：**
- `X-Task-ID` 在 Header 中立即返回，调用方可用于查询状态或取消任务
- `step_start` 和 `step_end` 通过 `step_id` 关联，调用方可计算耗时

**SSE 事件返回值结构：**

**intent（任务开始）：**

```json
{"timestamp":"2026-04-17T10:30:00Z"}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `timestamp` | string | 是 | 任务开始时间戳（ISO格式） |

**step_start（步骤开始）：**

```json
{"type":"skill","name":"pdf_analyzer","step_id":"20260417-103000000-a1b2c3","timestamp":"2026-04-17T10:30:00Z","nesting_level":0}
```

```json
{"type":"tool","name":"file_read","step_id":"20260417-103005000-x9y8z7","timestamp":"2026-04-17T10:30:05Z","params":{"path":"temp/report.pdf"}}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | string | 是 | 执行类型：`skill` / `tool` / `llm` |
| `name` | string | 是 | 名称（Skill名或工具名） |
| `step_id` | string | 是 | 步骤编号（与 step_end 关联） |
| `timestamp` | string | 是 | 时间戳（ISO格式） |
| `nesting_level` | int | 否 | 嵌套层级（0=主，1=子） |
| `params` | object | 否 | 参数（工具调用时） |

**step_end（步骤结束）：**

成功：
```json
{"step_id":"20260417-103000000-a1b2c3","timestamp":"2026-04-17T10:30:45Z","status":"success"}
```

失败：
```json
{"step_id":"20260417-103005000-x9y8z7","timestamp":"2026-04-17T10:30:05Z","status":"failed","error":{"code":"FILE_ERROR","message":"文件不存在"}}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `step_id` | string | 是 | 步骤编号（与 step_start 关联） |
| `timestamp` | string | 是 | 时间戳（ISO格式） |
| `status` | string | 是 | 执行状态：`success` / `failed` |
| `error` | object | 否 | 错误信息（失败时） |

**progress（进度更新）：**

```json
{"step_id":"20260417-103000000-a1b2c3","message":"正在读取PDF...","timestamp":"2026-04-17T10:30:10Z"}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `step_id` | string | 否 | 关联的步骤编号 |
| `message` | string | 是 | 进度消息 |
| `timestamp` | string | 是 | 时间戳（ISO格式） |

**completed（任务完成）：**

成功：
```json
{"status":"success","timestamp":"2026-04-17T10:30:45Z","duration":"45s","result":{"document_type":"report","key_points":[...]}}
```

失败：
```json
{"status":"failed","timestamp":"2026-04-17T10:30:05Z","duration":"5s","error":{"code":"SKILL_ERROR","message":"执行失败"}}
```

取消：
```json
{"status":"cancelled","timestamp":"2026-04-17T10:30:03Z","duration":"3s","message":"用户主动取消"}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `status` | string | 是 | 任务状态：`success` / `failed` / `cancelled` |
| `timestamp` | string | 是 | 时间戳（任务结束时间） |
| `duration` | string | 是 | 总耗时（如"45s"、"1m30s"） |
| `result` | object | 否 | 任务结果（成功时） |
| `error` | object | 否 | 错误信息（失败时） |
| `message` | string | 否 | 取消原因（取消时） |

**step_id 生成规则：**
- 格式：`{YYYYMMDD}-{HHMMSSmmm}-{random6}`
- 示例：`20260417-103005523-a1b2c3`
- 时间戳精确到毫秒，random为6位随机字符
- 全局唯一，多实例部署不冲突

**nesting_level 使用场景：**
- `0`：主Skill/主步骤
- `1`：子Skill/子步骤（主步骤内部调用）
- `2+`：更深层嵌套

### 3.3 POST /task/cancel

**请求 Body：**

```json
{
  "task_id": "task-xxx"
}
```

**响应：**

成功：
```json
{
  "status": "success",
  "task_id": "task-xxx",
  "message": "任务已取消"
}
```

失败：
```json
{
  "status": "task_completed",
  "task_id": "task-xxx",
  "message": "任务已完成，无法取消"
}
```

```json
{
  "status": "task_not_found",
  "task_id": "task-xxx",
  "message": "任务不存在"
}
```

**响应字段：**

| 字段 | 说明 |
|------|------|
| `status` | 结果状态：`success` 或失败状态码 |
| `task_id` | 任务ID |
| `message` | 结果消息 |

**失败状态码：**

| 状态码 | 说明 |
|--------|------|
| `task_completed` | 任务已完成 |
| `task_failed` | 任务已失败 |
| `task_not_found` | 任务不存在 |

### 3.4 GET /task/status/{task_id}

**请求方式：** 路径参数

```
GET /task/status/task-xxx
```

**响应：**

成功：
```json
{
  "status": "success",
  "task_id": "task-xxx",
  "task_status": "running",
  "progress": {
    "current_step": 3,
    "steps_completed": 2,
    "percentage": 50
  },
  "started_at": "2026-04-17T10:30:00Z",
  "elapsed_time": "8s"
}
```

失败（任务不存在）：
```json
{
  "status": "task_not_found",
  "task_id": "task-xxx",
  "message": "任务不存在"
}
```

**响应字段：**

| 字段 | 说明 |
|------|------|
| `status` | 查询结果：`success` 或 `task_not_found` |
| `task_id` | 任务ID |
| `task_status` | 任务状态：`running` / `completed` / `failed` / `cancelled` |
| `progress` | 进度信息（运行中时） |
| `started_at` | 开始时间 |
| `elapsed_time` | 已耗时 |

**task_status 状态说明：**

| 状态 | 说明 |
|------|------|
| `running` | 正在执行 |
| `completed` | 已完成 |
| `failed` | 已失败 |
| `cancelled` | 已取消 |

### 3.5 GET /health

**响应：**

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "healthy", "model": "gpt-4o"},
    "mcp_servers": {"status": "healthy", "servers": [...]},
    "skills": {"status": "healthy", "count": 12},
    "memory": {"status": "healthy", "used_mb": 256}
  },
  "metrics": {
    "tasks_running": 5,
    "success_rate": 0.98
  }
}
```

---

## 四、Skills 注册机制

### 4.1 目录结构

```
{GROOT_HOME}/skills/
├── pdf_analyzer/
│   └── skill.md
├── code_generator/
│   └── skill.md
├── data_analyzer/
│   └── skill.md
└── report_generator/
    └── skill.md
```

### 4.2 Skill 定义格式

遵循 Claude Code 标准，兼容 skills.sh 和 skillstore.io。

**skill.md 结构：**

```markdown
# Skill: skill_name

## Description
技能描述

## Triggers
- 触发条件1
- 触发条件2

## Instructions
执行指令的自然语言描述

## Dependencies
- dependent_skill_1
- dependent_skill_2

## Output Format
输出格式定义（可选）

## Examples
使用示例（可选）
```

### 4.3 注册流程

```
程序启动 → 扫描 skills 目录 → 解析每个 skill.md →
提取 name/description/triggers/instructions/dependencies →
注册到内存索引
```

### 4.4 Skills 热插拔机制

支持运行时动态添加、修改、删除 Skills，无需重启服务。

**监听机制：**
- 使用 `fsnotify` 监听 skills 目录变化
- 只监听 `skill.md` 文件的创建、修改、删除事件
- 防抖机制：检测到变化后延迟 2秒再执行加载，避免编辑过程中频繁触发

**处理流程：**

```
文件变化检测 →防抖等待（2秒） →
│
├─ 新增 skill.md → 解析并注册 → 输出日志
│
├─ 修改 skill.md → 重新解析并更新 → 输出日志
│
└─ 删除 skill.md → 移除对应 Skill → 输出日志
```

**配置项：**

```yaml
skills:
  hot_reload:
    enabled: true       # 是否启用热插拔
    debounce_delay: 2   # 防抖延迟（秒）
```

Skills 目录为工作目录下的固定结构 `{GROOT_HOME}/skills/`，无需配置路径。

**日志输出：**

热插拔事件会记录到日志：

```json
{
  "timestamp": "2026-04-17T10:30:00Z",
  "level": "INFO",
  "event": "skill_hot_reload",
  "data": {
    "action": "added",
    "skill_name": "new_skill",
    "skills_count": 13
  }
}
```

## 五、Agent 执行流程

### 5.1 ReAct 执行循环

Agent 使用 ReAct 模式自主执行任务，框架自动决策调用 Skills 或 MCP 工具：

```
用户请求到达 → 构建 Agent 上下文 →
│
├─ 上下文内容
│   ├─ 用户指令 + prompt + 附件
│   ├─ 已注册的 Skills 列表（含 Instructions）
│   ├─ 已注册的 MCP 工具列表
│   └─ 执行限制配置（最大循环次数、Token限制等）
│
├─ ReAct 执行循环
│   │
│   ├─ Reasoning（思考）
│   │   LLM 分析当前状态，决定下一步动作：
│   │   ├─ 调用 Skill（如指令提及或 Agent 判断需要）
│   │   ├─ 调用 MCP 工具
│   │   └─ 直接生成回答（任务完成）
│   │
│   ├─ Acting（执行）
│   │   ├─ Skill 调用 → 递归执行子 Skill
│   │   ├─ MCP 工具调用 → MCP Manager 执行
│   │   └─ LLM 生成 → 输出结果
│   │
│   ├─ Observation（观察）
│   │   获取执行结果，更新上下文，SSE 推送进度事件
│   │
│   └─ 检查终止条件
│       ├─ Agent 判断完成 → 结束循环
│       ├─ 达到最大循环次数 → 终止
│       ├─ Token 消耗超限 → 终止
│       ├─ 单步执行超时 → 终止
│       ├─ 用户取消 → 终止
│       └─ 继续循环
│
└─ 输出最终结果，SSE 推送 completed 事件
```

### 5.2 Skills 嵌套支持

**嵌套场景：**

| 场景 | 说明 |
|------|------|
| Skill 包含 Skills | Skill 的 Instructions 中声明调用其他 Skill，通过 Dependencies 字段定义 |

Agent 在执行 Skill 时，会自动识别并递归调用依赖的子 Skills。

**执行依赖树示例：**

```
report_generator (主Skill)
│
├─ pdf_analyzer (子Skill)
│   └─ 工具: file_read
│
├─ data_analyzer (子Skill)
│   └─ 工具: csv_parser
│
└─ 工具: file_write
```

### 5.3 取消任务机制

```
POST /task/cancel →
│
├─ 根据 task_id 查找执行状态
│
├─ 设置状态为 cancelled
│
├─ 中断 Agent 执行循环
│   ├─ 停止当前 LLM 调用
│   ├─ 停止当前 MCP 工具调用
│   └─ 清理资源
│
├─ SSE 推送取消事件
│
└─ 关闭 SSE 连接
```

---

## 六、MCP 配置与管理

### 6.1 MCP 配置目录结构

MCP 配置采用独立目录，每个 MCP 一个 JSON 文件，支持热插拔：

```
{GROOT_HOME}/mcp/
├── file_operations.json      # 内置 MCP（文件操作）
├── http_request.json         # 内置 MCP（HTTP请求）
├── web_parser.json           # 外部 MCP（网页解析）
└── database_tool.json        # 外部 MCP（数据库操作）
```

### 6.2 MCP 连接类型

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `stdio` | 标准输入输出通信 | 本地命令行工具 |
| `sse` | Server-Sent Events | 远程 HTTP 服务（单向推送） |
| `streamable_http` | Streamable HTTP | 远程 HTTP 服务（双向流式） |

### 6.3 MCP 配置文件格式

每个 MCP 一个独立的 JSON 文件，符合官方标准格式：

**stdio 类型示例（本地工具）：**

```json
{
  "name": "database_tool",
  "type": "stdio",
  "description": "数据库查询和操作工具",
  "isActive": true,
  "command": "mcp-server-postgres",
  "args": ["--connection", "${DB_CONNECTION}"],
  "env": {
    "DB_CONNECTION": "${DB_CONNECTION}"
  }
}
```

**sse 类型示例（远程服务）：**

```json
{
  "name": "WebParser",
  "type": "sse",
  "description": "网页解析 MCP 服务，专用于网页内容解析",
  "isActive": true,
  "baseUrl": "https://dashscope.aliyuncs.com/api/v1/mcps/WebParser/sse",
  "headers": {
    "Authorization": "Bearer ${DASHSCOPE_API_KEY}"
  }
}
```

**streamable_http 类型示例：**

```json
{
  "name": "web_search",
  "type": "streamable_http",
  "description": "网络搜索工具",
  "isActive": true,
  "baseUrl": "https://mcp-search.example.com/api",
  "headers": {
    "X-API-Key": "${SEARCH_API_KEY}"
  }
}
```

### 6.4 配置字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | MCP名称（全局唯一） |
| `type` | string | 是 | 连接类型：`stdio` / `sse` / `streamable_http` |
| `description` | string | 是 | MCP描述（用于Agent工具列表） |
| `isActive` | boolean | 是 | 是否启用 |
| `command` | string | stdio必填 | 命令行程序名 |
| `args` | array | 否 | 命令行参数 |
| `env` | object | 否 | 环境变量 |
| `baseUrl` | string | sse/streamable_http必填 | 服务地址 |
| `headers` | object | 否 | HTTP请求头 |

### 6.5 MCP 热插拔机制

支持运行时动态添加、修改、删除 MCP，无需重启服务。

**监听机制：**
- 使用 `fsnotify` 监听 mcp 目录变化
- 只监听 `.json` 文件的创建、修改、删除事件
- 防抖机制：检测到变化后延迟 2秒再执行加载

**处理流程：**

```
文件变化检测 → 防抖等待（2秒） →
│
├─ 新增 .json → 解析并注册 MCP →建立连接 → 输出日志
│
├─ 修改 .json → 重新解析 → 断开旧连接 → 建立新连接 → 输出日志
│
└─ 删除 .json → 断开连接 → 移除 MCP 注册 → 输出日志
```

**配置项：**

```yaml
mcp:
  hot_reload:
    enabled: true       # 是否启用热插拔
    debounce_delay: 2   # 防抖延迟（秒）
```

MCP 目录为工作目录下的固定结构 `{GROOT_HOME}/mcp/`，无需配置路径。

**日志输出：**

```json
{
  "timestamp": "2026-04-17T10:30:00Z",
  "level": "INFO",
  "event": "mcp_hot_reload",
  "data": {
    "action": "added",
    "mcp_name": "WebParser",
    "mcp_type": "sse",
    "mcp_count": 5
  }
}
```

### 6.6 内置 MCP 工具

内置 MCP 存放在 `mcp/` 目录下，默认包含以下工具：

**file_operations.json（文件操作）：**

```json
{
  "name": "file_operations",
  "type": "builtin",
  "description": "文件读写和目录操作",
  "isActive": true,
  "tools": ["file_read", "file_write", "file_search", "directory_list", "directory_create"],
  "restrictions": {
    "allowed_paths": ["temp", "skills", "output"],
    "denied_operations": ["file_delete"]
  }
}
```

**http_request.json（HTTP请求）：**

```json
{
  "name": "http_request",
  "type": "builtin",
  "description": "HTTP请求发送",
  "isActive": true,
  "tools": ["http_get", "http_post", "http_put", "http_delete"],
  "restrictions": {
    "denied_domains": ["localhost", "127.0.0.1", "10.*", "192.168.*"],
    "timeout": 30,
    "max_response_size": 10
  }
}
```

**code_execution.json（代码执行，默认禁用）：**

```json
{
  "name": "code_execution",
  "type": "builtin",
  "description": "代码片段执行（高风险）",
  "isActive": false,
  "tools": ["execute_python", "execute_javascript", "execute_shell"],
  "restrictions": {
    "sandbox": true,
    "timeout": 30,
    "network_access": false
  }
}
```

---

## 七、内置 MCP 工具定义

### 8.1 file_operations

| 操作 | 说明 | 参数 |
|------|------|------|
| `file_read` | 读取文件 | `path` |
| `file_write` | 写入文件 | `path`, `content` |
| `file_delete` | 删除文件 | `path` |
| `file_search` | 搜索文件 | `pattern`, `directory` |
| `directory_list` | 列出目录 | `path` |
| `directory_create` | 创建目录 | `path` |
| `file_exists` | 检查存在 | `path` |
| `file_info` | 获取信息 | `path` |

**安全限制：**
- 仅允许访问 temp、skills、output 目录
- 默认禁止删除操作

### 8.2 http_request

| 操作 | 说明 | 参数 |
|------|------|------|
| `http_get` | GET 请求 | `url`, `headers` |
| `http_post` | POST 请求 | `url`, `body`, `headers` |
| `http_put` | PUT 请求 | `url`, `body`, `headers` |
| `http_delete` | DELETE 请求 | `url`, `headers` |

**安全限制：**
- 禁止请求 localhost、内网 IP
- 超时 30秒
- 最大响应 10MB

### 8.3 code_execution（默认禁用）

| 操作 | 说明 | 参数 |
|------|------|------|
| `execute_python` | 执行 Python | `code`, `timeout` |
| `execute_javascript` | 执行 JS | `code`, `timeout` |
| `execute_shell` | 执行 Shell | `command`, `timeout` |

**安全限制：**
- 默认禁用（高风险）
- 启用后需沙箱执行
- 禁止网络访问

---

## 八、并发与性能控制

### 8.1 限流配置

```yaml
performance:
  rate_limit:
    max_concurrent_tasks: 10       # 最大并发任务数，超过则返回 429
    max_requests_per_minute: 60    # 每分钟最大请求数，超过则返回 429
    max_requests_per_hour: 1000    # 每小时最大请求数，超过则返回 429
  
  timeout:
    task_max_duration: 300        # 单任务最大执行时长（秒），超过则终止
    llm_call_timeout: 60          # 单次 LLM 调用超时（秒）
    tool_call_timeout: 30         # 单次工具调用超时（秒）
  
  llm:
    max_concurrent_calls: 5       # LLM 并发调用数限制
    retry_on_failure: 3           # LLM 调用失败重试次数
    retry_delay: 2                # 重试间隔（秒）
  
  mcp:
    max_concurrent_calls_per_server: 3  # 每个 MCP 服务并发调用数限制
```

### 8.2 ReAct 执行限制

防止 Agent 无限循环或成本失控：

```yaml
react:
  max_iterations: 20          # 最大循环次数
  max_tokens: 100000          # 最大Token消耗
  step_timeout: 60            # 单步执行超时（秒）
  error_retry: 2              # 单步失败重试次数
  nesting_max_depth: 3        # Skills嵌套最大深度
```

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `max_iterations` | ReAct 最大循环次数，防止无限循环 | 20 |
| `max_tokens` | 单任务最大Token消耗，防止成本失控 | 100000 |
| `step_timeout` | 单步执行超时时间（秒） | 60 |
| `error_retry` | 单步失败后重试次数 | 2 |
| `nesting_max_depth` | Skills 嵌套最大深度 | 3 |

**终止条件说明：**

| 条件 | 触发时机 | SSE 事件 |
|------|---------|---------|
| Agent判断完成 | LLM输出最终答案 | `completed` (success) |
| 达到最大循环次数 | iteration > max_iterations | `completed` (failed) |
| Token消耗超限 | tokens_used > max_tokens | `completed` (failed) |
| 单步执行超时 | step_duration > step_timeout | `completed` (failed) |
| 用户取消 | 调用 /task/cancel | `completed` (cancelled) |

### 8.3 错误响应

| 场景 | HTTP 状态码 |
|------|------------|
| 请求限流触发 | 429 |
| 参数错误 | 400 |
| 未认证 | 401 |
| 权限不足 | 403 |
| 配置错误 | 500 |

---

## 九、错误处理机制

### 9.1 错误码定义

| 错误码 | 说明 | 可恢复 |
|--------|------|--------|
| `INVALID_REQUEST` | 请求参数错误 | 否 |
| `RATE_LIMITED` | 请求被限流 | 否 |
| `LLM_CONNECTION_ERROR` | LLM 连接失败 | 可重试 |
| `LLM_RATE_LIMITED` | LLM API 限流 | 可重试 |
| `LLM_TIMEOUT` | LLM 调用超时 | 可重试 |
| `TOOL_CALL_ERROR` | 工具调用失败 | 可重试 |
| `SKILL_NOT_FOUND` | Skill 不存在 | 否 |
| `TASK_TIMEOUT` | 任务执行超时 | 否 |
| `TASK_CANCELLED` | 用户取消 | 否 |

### 9.2 重试策略

| 场景 | 重试次数 | 重试间隔 |
|------|---------|---------|
| LLM 连接失败 | 3 | 2s |
| LLM Rate Limit | 3 | 5s |
| MCP 工具失败 | 2 | 1s |

---

## 十、日志与监控机制

### 11.1 日志类型

| 类型 | 用途 | 级别 |
|------|------|------|
| 请求日志 | API 调用记录 | INFO |
| Skills 日志 | Skills 调用详情 | INFO |
| LLM 日志 | LLM 调用详情 | DEBUG |
| MCP 日志 | MCP 工具调用 | DEBUG |
| 执行日志 | Agent 执行过程 | INFO |
| 错误日志 | 所有错误 | ERROR |
| 性能日志 | 耗时指标 | INFO |

### 11.2 日志存储

- 目录：`{GROOT_HOME}/logs/`
- 格式：`groot-{date}.log`
- 保留：7天，自动删除过期日志

### 11.3 监控指标

| 类别 | 指标 |
|------|------|
| 请求 | `requests_total`、`requests_success`、`requests_failed` |
| 任务 | `tasks_running`、`tasks_completed`、`tasks_duration` |
| Skills | `skill_calls_total`、`skill_calls_by_name`、`skill_duration` |
| LLM | `llm_calls_total`、`llm_tokens_used`、`llm_latency` |
| MCP | `mcp_calls_total`、`mcp_calls_by_server` |

---

## 十一、安全性设计

### 12.1 API 认证

```yaml
security:
  auth:
    enabled: true
    type: api_key
    api_key:
      keys:
        - name: default
          key: ${GROOT_API_KEY}
          permissions: all
```

### 12.2 权限定义

| 权限 | 对应 API |
|------|---------|
| `execute` | POST /task/execute |
| `cancel` | POST /task/cancel |
| `status` | GET /task/status |
| `skills` | GET /skills |
| `tools` | GET /tools |
| `health` | GET /health |
| `all` | 以上全部 |

### 12.3 敏感信息保护

- API Key 通过环境变量存储
- 不记录敏感信息到日志
- 日志脱敏处理

---

## 十二、附件处理机制

### 13.1 支持的附件类型

| 类型 | 格式 | 处理方式 |
|------|------|---------|
| 文档 | PDF、DOC、DOCX、TXT | 文本提取 |
| 数据 | JSON、CSV、XML、YAML | 结构化解析 |
| 代码 | 源码文件 | 内容读取 |
| 图片 | PNG、JPG | 图片数据 |
| 压缩 | ZIP、TAR | 解压处理 |

### 13.2 传输方式

| 方式 | 适用场景 |
|------|---------|
| Base64 编码 | 小文件（<10MB） |
| URL 链接 | 大文件或外部资源 |

### 13.3 配置

```yaml
attachment:
  max_size: 50
  max_total_size: 100
  max_count: 10
  allowed_types: [pdf, doc, json, csv, png, zip]
  temp_directory: temp
```

---

## 十三、目录结构与配置

### 14.1 工作目录

默认：`~/.groot`，可通过命令行或环境变量更改。

```
{GROOT_HOME}/
├── config.yaml
├── skills/
│   └── {skill-name}/skill.md
├── mcp/
│   └── {mcp-name}.json
├── logs/
│   └── groot-{date}.log
└── temp/（任务临时文件）
```

### 14.2 配置优先级

| 配置项 | 来源 |
|------|------|
| 工作目录 | 命令行 `-H` > 环境变量 `GROOT_HOME` > 默认 |
| HTTP 端口 | 命令行 `-p` > 配置文件 |
| 其他配置 | 配置文件 |

---

## 十四、启动与部署

### 15.1 命令行参数

| 参数 | 缩写 | 说明 | 默认值 |
|------|------|------|--------|
| `--home` | `-H` | 工作目录 | `~/.groot` |
| `--port` | `-p` | HTTP端口 | 配置文件值 |
| `--help` | `-h` | 显示帮助 | - |
| `--version` | `-v` | 显示版本 | - |

### 15.2 启动流程

```
解析参数 → 确定工作目录 → 检查/创建目录结构 →
加载配置 → 初始化日志 → 注册 Skills → 加载 MCP →
初始化 LLM → 启动 HTTP 服务 → 等待请求
```

### 15.3 优雅关闭

- 停止接受新请求
- 等待当前任务完成（超时30秒）
- 关闭 MCP 连接
- 刷新日志
- 退出程序

---

## 十五、完整配置文件模板

首次启动生成的默认 `config.yaml`：

```yaml
# Groot Agent 配置文件
# 生成时间: 2026-04-16

# Agent 基础配置
agent:
  name: groot
  version: 1.0.0

# HTTP 服务配置
server:
  host: 0.0.0.0
  port: 8080

# LLM 配置（OpenAI兼容协议）
llm:
  active_model: gpt-4o
  models:
    gpt-4o:
      endpoint: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4o
      max_tokens: 4096
      temperature: 0.7
    claude-3.5:
      endpoint: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_tokens: 4096
      temperature: 0.7

# Skills 热插拔配置
skills:
  hot_reload:
    enabled: true       # 是否启用热插拔
    debounce_delay: 2   # 防抖延迟（秒）

# MCP 热插拔配置
mcp:
  hot_reload:
    enabled: true       # 是否启用热插拔
    debounce_delay: 2   # 防抖延迟（秒）

# 性能控制配置
performance:
  rate_limit:
    max_concurrent_tasks: 10
    max_requests_per_minute: 60
    max_requests_per_hour: 1000
  timeout:
    task_max_duration: 300
    llm_call_timeout: 60
    tool_call_timeout: 30
  llm:
    max_concurrent_calls: 5
    retry_on_failure: 3
    retry_delay: 2
  mcp:
    max_concurrent_calls_per_server: 3

# ReAct 执行配置
react:
  max_iterations: 20          # 最大循环次数，防止无限循环
  max_tokens: 100000          # 最大Token消耗，防止成本失控
  step_timeout: 60            # 单步执行超时（秒）
  error_retry: 2              # 单步失败重试次数
  nesting_max_depth: 3        # Skills 嵌套最大深度

# 附件处理配置
attachment:
  max_size: 50
  max_total_size: 100
  max_count: 10
  allowed_types: [pdf, doc, docx, txt, json, csv, xml, yaml, png, jpg, zip]
  temp_directory: temp

# 安全配置
security:
  auth:
    enabled: true
    type: api_key
    api_key:
      keys:
        - name: default
          key: ${GROOT_API_KEY}
          permissions: all

# 日志配置
logging:
  level: info
  format: json
  output: [stdout, file]
  file:
    directory: logs
    filename_pattern: groot-{date}.log
    max_age: 7
  categories:
    request: {enabled: true, level: info}
    skill: {enabled: true, level: info, log_input: true, log_output: true}
    llm: {enabled: true, level: debug}
    mcp: {enabled: true, level: debug}
    error: {enabled: true, level: error}

# 监控配置
monitoring:
  enabled: true
  metrics_port: 9090
  health_check:
    enabled: true
    endpoint: /health
```

---

## 十六、Skill 示例

### 17.1 pdf_analyzer

```markdown
# Skill: pdf_analyzer

## Description
分析PDF文档内容，提取关键信息并生成结构化摘要报告。

## Triggers
- 用户上传 PDF 文件并请求分析
- 用户提到"分析PDF"、"PDF摘要"、"文档分析"等关键词

## Instructions
你是一个专业的PDF文档分析助手。执行以下步骤：
1. 使用 file_operations.file_read 工具读取PDF文件
2. 提取文档的关键内容和结构
3. 根据文档类型生成相应的结构化摘要
4. 输出结构化的分析结果

## Output Format
{
  "document_type": "文档类型",
  "title": "文档标题",
  "key_points": ["关键要点"],
  "summary": "详细摘要",
  "recommendations": ["建议"]
}
```

### 17.2 code_generator

```markdown
# Skill: code_generator

## Description
根据用户需求描述生成代码，支持多种编程语言。

## Triggers
- 用户请求"生成代码"、"写一个函数"、"实现某个功能"

## Instructions
你是一个专业的代码生成助手。执行以下步骤：
1. 分析用户需求，明确功能目标、输入输出规格、编程语言
2. 设计代码结构和逻辑
3. 生成完整的代码实现，包含注释和错误处理
4. 生成使用示例或测试代码

## Output Format
{
  "language": "编程语言",
  "code": "完整代码",
  "usage_example": "使用示例",
  "test_code": "测试代码"
}
```

### 17.3 data_analyzer

```markdown
# Skill: data_analyzer

## Description
分析结构化数据文件（CSV、JSON等），执行统计分析和趋势识别。

## Triggers
- 用户上传数据文件并请求分析
- 用户提到"分析数据"、"数据统计"、"数据趋势"

## Instructions
你是一个数据分析助手。执行以下步骤：
1. 使用 file_operations.file_read 工具读取数据文件
2. 解析数据结构，识别字段含义和数据类型
3. 执行统计分析、趋势分析、关联分析
4. 生成分析结果和可视化建议

## Output Format
{
  "data_overview": {"rows": 数量, "columns": 数量},
  "statistics": {"summary": "统计摘要"},
  "trends": ["趋势描述"],
  "insights": ["数据洞察"]
}
```

### 17.4 report_generator（嵌套Skill示例）

```markdown
# Skill: report_generator

## Description
综合分析多种来源的资料，生成完整的分析报告。

## Triggers
- 用户请求"生成报告"、"综合分析"
- 用户提供多种类型的附件

## Instructions
你是一个报告生成助手。执行以下步骤：
1. 分析用户提供的资料类型
2. 根据资料类型调用相应的分析Skills
3. 整合各Skills的分析结果
4. 生成结构化的综合报告
5. 使用 file_operations.file_write 保存报告

## Dependencies
- pdf_analyzer
- data_analyzer
```

---

## 十七、API 详细示例

### 18.1 POST /task/execute 请求示例

**基本请求：**
```json
{
  "instruction": "帮我分析这份PDF财务报告",
  "attachments": [
    {"type": "file", "name": "Q3_Report.pdf", "content": "base64..."}
  ]
}
```

**带 prompt 的请求：**
```json
{
  "instruction": "帮我分析这份PDF财务报告",
  "prompt": "你是一个财务分析师，重点关注利润增长率和潜在风险点。输出JSON格式。",
  "attachments": [
    {"type": "file", "name": "Q3_Report.pdf", "content": "base64..."}
  ]
}
```

**多附件请求：**
```json
{
  "instruction": "对比分析这份PDF报告和销售数据",
  "attachments": [
    {"type": "file", "name": "report.pdf", "content": "base64..."},
    {"type": "file", "name": "sales.csv", "content": "base64..."}
  ]
}
```

**无附件请求（纯 LLM 执行）：**
```json
{
  "instruction": "帮我写一个 Python 快速排序函数"
}
```

### 18.2 SSE 响应事件流示例

**成功执行：**
```
HTTP Header: X-Task-ID: task-xxx

event: intent
data: {"timestamp":"2026-04-17T10:30:00Z"}

event: step_start
data: {"type":"skill","name":"pdf_analyzer","step_id":"20260417-103000000-a1b2c3","timestamp":"2026-04-17T10:30:00Z","nesting_level":0}

event: progress
data: {"step_id":"20260417-103000000-a1b2c3","message":"正在读取PDF...","timestamp":"2026-04-17T10:30:05Z"}

event: step_start
data: {"type":"tool","name":"file_read","step_id":"20260417-103005000-x9y8z7","timestamp":"2026-04-17T10:30:05Z","params":{"path":"temp/report.pdf"}}

event: step_end
data: {"step_id":"20260417-103005000-x9y8z7","timestamp":"2026-04-17T10:30:05.2Z","status":"success"}

event: progress
data: {"step_id":"20260417-103000000-a1b2c3","message":"正在生成摘要...","timestamp":"2026-04-17T10:30:20Z"}

event: step_end
data: {"step_id":"20260417-103000000-a1b2c3","timestamp":"2026-04-17T10:30:45Z","status":"success"}

event: completed
data: {"status":"success","timestamp":"2026-04-17T10:30:45Z","duration":"45s","result":{"document_type":"report","key_points":[...],"summary":"..."}}
```

**失败执行：**
```
HTTP Header: X-Task-ID: task-xxx

event: intent
data: {"timestamp":"2026-04-17T10:30:00Z"}

event: step_start
data: {"type":"skill","name":"pdf_analyzer","step_id":"20260417-103000000-a1b2c3","timestamp":"2026-04-17T10:30:00Z","nesting_level":0}

event: progress
data: {"step_id":"20260417-103000000-a1b2c3","message":"正在读取PDF...","timestamp":"2026-04-17T10:30:02Z"}

event: step_end
data: {"step_id":"20260417-103000000-a1b2c3","timestamp":"2026-04-17T10:30:05Z","status":"failed","error":{"code":"FILE_ERROR","message":"PDF文件已损坏"}}

event: completed
data: {"status":"failed","timestamp":"2026-04-17T10:30:05Z","duration":"5s","error":{"code":"SKILL_ERROR","message":"pdf_analyzer执行失败"}}
```

**取消执行：**
```
HTTP Header: X-Task-ID: task-xxx

event: intent
data: {"timestamp":"2026-04-17T10:30:00Z"}

event: step_start
data: {"type":"skill","name":"pdf_analyzer","step_id":"20260417-103000000-a1b2c3","timestamp":"2026-04-17T10:30:00Z"}

event: progress
data: {"step_id":"20260417-103000000-a1b2c3","message":"正在处理...","timestamp":"2026-04-17T10:30:10Z"}

（用户发送取消请求）

event: completed
data: {"status":"cancelled","timestamp":"2026-04-17T10:30:12Z","duration":"12s","message":"用户主动取消"}
```

### 18.3 其他 API 响应示例

**GET /health：**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "checks": {
    "llm": {"status": "healthy", "model": "gpt-4o"},
    "skills": {"count": 12}
  }
}
```

**GET /skills：**
```json
{
  "skills": [
    {"name": "pdf_analyzer", "description": "分析PDF文档"},
    {"name": "code_generator", "description": "生成代码"}
  ],
  "total": 5
}
```

---

## 附录

### A. 环境变量

| 变量 | 说明 | 必需 |
|------|------|------|
| `OPENAI_API_KEY` | LLM API 密钥 | 是 |
| `GROOT_API_KEY` | Groot 认证密钥 | 是（启用认证时） |
| `GROOT_HOME` | 工作目录 | 否 |
| `ANTHROPIC_API_KEY` | Anthropic API 密钥 | 否 |
| `DB_CONNECTION` | 数据库连接 | 否 |

### B. 默认端口

| 服务 | 端口 |
|------|------|
| HTTP API | 8080 |
| Metrics | 9090（可选） |

### C. 文件路径约定

| 路径 | 说明 |
|------|------|
| `{home}/config.yaml` | 配置文件 |
| `{home}/skills/` | Skills 目录 |
| `{home}/logs/` | 日志目录 |
| `{home}/temp/` | 临时文件 |
| `{home}/output/` | 任务输出 |