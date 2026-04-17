# Groot AI Agent 设计文档

**版本:** 1.0.0
**日期:** 2026-04-16
**状态:** 设计完成，待实现

---

## 一、概述

### 1.1 项目定位

Groot 是面向业务系统的 AI Agent 服务。通过 REST API 接入，让你的系统立刻拥有智能任务执行能力——理解指令、调用工具、自主完成任务。

**核心特性：**
- 自然语言交互：接收指令 + 附件，无需编写代码逻辑
- 智能决策执行：自动判断意图，自主选择调用 Skills 或 MCP 工具完成任务
- 流式进度反馈：实时返回执行过程和结果，调用方全程可见
- Skills 嵌套：复杂任务自动拆解，子任务递归执行
- 热插拔扩展：Skills 和 MCP 工具支持动态添加，无需重启服务

### 1.2 技术栈

| 组件 | 技术选型 |
|------|---------|
| HTTP 框架 | Hertz（字节开源） |
| Agent 框架 | eino（字节开源） |
| LLM 调用 | OpenAI 兼容协议 |
| 存储（单机） | BoltDB（嵌入式键值数据库） |
| 存储（集群预留） | Redis / etcd |
| 配置格式 | YAML |
| 日志格式 | JSON 结构化（支持日志采集监控） |

### 1.3 LLM 配置

支持多模型配置，通过 `active_model` 指定当前使用的模型。

**配置示例：**

```yaml
llm:
  active_model: gpt-4o           # 当前激活的模型
  models:
    gpt-4o:                      # 模型名称（自定义）
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4o              # 实际模型名称
      max_tokens: 4096
      temperature: 0.7
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_tokens: 4096
      temperature: 0.7
```

**字段说明：**

| 字段 | 说明 |
|------|------|
| `active_model` | 当前激活的模型名称，对应 models 中的某个 key |
| `base_url` | LLM API 地址（OpenAI 兼容协议） |
| `api_key` | API 密钥，支持环境变量引用 `${VAR_NAME}` |
| `model` | 实际调用时的模型名称 |
| `max_tokens` | 单次调用最大 Token 数 |
| `temperature` | 输出随机性（0-1，越高越随机） |

**模型切换：**

修改 `active_model` 值后，需要重启服务才能生效。

> **说明：** Groot 仅支持 Skills 和 MCP 的热插拔，LLM 配置修改需重启服务。

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
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│   MCP Manager   │  │ Task Storage    │  │ Config & Registry│
│  - 加载内置 MCP  │  │  - 任务持久化    │  │  - 配置管理      │
│  - 加载外部 MCP  │  │  - 状态查询      │  │  - Skills 注册   │
│  - 工具调用执行  │  │  - 历史记录      │  │  - MCP 配置解析  │
│  - 权限检查      │  │  - 清理过期数据  │  │  - 热插拔管理    │
└─────────────────┘  └─────────────────┘  └─────────────────┘
                              │
                              ▼
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        ┌───────────┐   ┌───────────┐   ┌───────────┐
        │  BoltDB   │   │   Redis   │   │   etcd    │
        │ （单机版） │   │（集群预留）│   │（集群预留）│
        └───────────┘   └───────────┘   └───────────┘
```

### 2.2 模块职责

| 模块 | 职责 | 扩展预留点 |
|------|------|-----------|
| REST API Layer | 请求解析、响应封装、限流、超时控制 | 可拆分为独立 Gateway |
| Agent Engine | ReAct 执行、工具注册、自主决策 | 支持多 Agent 协作扩展 |
| MCP Manager | MCP 加载、工具调用、权限检查 | 支持动态 MCP 加载/卸载 |
| Task Storage | 任务持久化、状态查询、历史记录、过期清理 | 支持 Redis/etcd 集群扩展 |
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
- 启动时扫描 skills 目录，解析每个 SKILL.md
- 将 Skill 的 Instructions 作为工具描述注册给 Agent
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
| `/task/{task_id}` | DELETE | 取消正在执行的任务 |
| `/task/status/{task_id}` | GET | 查询任务状态 |
| `/task/history` | GET | 查询历史任务列表 |
| `/task/{task_id}` | GET | 查询任务详情（含完整步骤记录） |
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

**完整处理流程：**

```
请求到达 → 请求校验 → 生成 task_id → 返回 X-Task-ID Header →
│
├─ 1. 请求校验
│   ├─ 检查 instruction 是否为空
│   ├─ 检查 prompt 格式（可选）
│   ├─ 检查附件数量（不超过 max_count）
│   ├─ 检查单个附件大小（不超过 max_size）
│   ├─ 检查附件总大小（不超过 max_total_size）
│   ├─ 检查附件类型（必须在 allowed_types 中）
│   ├─ 校验失败 → 返回 400 错误，终止
│   └─ 校验通过 → 继续
│
├─ 2. 创建任务记录
│   ├─ 生成 task_id（格式：task-{YYYYMMDD}-{HHMMSSmmm}-{random4}）
│   ├─ 初始化任务状态为 running
│   ├─ 记录开始时间、调用方信息
│   └─ 持久化到存储（BoltDB）
│
├─ 3. 返回响应 Header
│   ├─ Content-Type: text/event-stream
│   └─ X-Task-ID: {task_id}
│   （此时 SSE 连接已建立，开始流式返回事件）
│
├─ 4. 附件处理（如有附件）
│   ├─ 创建任务临时目录：temp/{task_id}/
│   ├─ 遍历每个附件：
│   │   ├─ Base64 解码
│   │   ├─ 文件名安全处理（替换 /、\、.. 等危险字符）
│   │   ├─ 保存到临时目录：temp/{task_id}/{safe_filename}
│   │   └─ 记录文件信息（路径、大小、类型）
│   ├─ 构建附件信息文本：
│   │   格式：
│   │   ```
│   │   附件:
│   │   - {原始文件名} (file)
│   │     路径: {绝对路径}
│   │     类型: {MIME类型}
│   │     大小: {文件大小} bytes
│   │   ```
│   ├─ 拼接到用户消息中（instruction + 附件信息）
│   ├─ 处理失败 → SSE 推送 error，跳转到步骤9清理，终止
│   └─ 处理成功 → 继续
│
├─ 5. SSE 推送 intent 事件
│   └─ {"timestamp":"开始时间"}
│   （表示准备工作完成，Agent 开始执行）
│
├─ 6. 构建 Agent 上下文
│   ├─ 系统提示词（prompt + Skills 指令）
│   ├─ 用户消息（instruction + 附件路径信息）
│   ├─ 注册的工具列表（MCP 工具）
│   └─ 执行限制配置（max_iterations、max_tokens 等）
│
├─ 7. ReAct 执行循环
│   │
│   ├─ Reasoning（思考）
│   │   LLM 分析当前状态，决定下一步动作
│   │
│   ├─ Acting（执行）
│   │   ├─ 调用 MCP 工具（如 file_read 读取附件）
│   │   ├─ 调用 Skill
│   │   └─ 直接生成回答
│   │
│   ├─ Observation（观察）
│   │   获取执行结果，更新上下文
│   │
│   ├─ SSE 推送进度事件
│   │   ├─ step_start：步骤开始
│   │   ├─ progress：进度更新
│   │   ├─ step_end：步骤结束
│   │
│   └─ 检查终止条件
│       ├─ Agent 判断完成 → 结束循环
│       ├─ 达到 max_iterations → 终止
│       ├─ Token 消耗超限 → 终止
│       ├─ 执行超时 → 终止
│       ├─ 用户取消 → 终止
│       └─ 继续循环
│
├─ 8. 任务完成处理
│   ├─ 更新任务状态（completed/failed/cancelled）
│   ├─ 记录结束时间、耗时
│   ├─ 保存结果或错误信息
│   ├─ SSE 推送 completed 事件
│   │
│   └─ 9. 清理临时文件
│       ├─ 删除任务临时目录：temp/{task_id}/
│       ├─ 清理所有附件文件
│       └─ 关闭 SSE 连接
│
→ 流程结束
```

**关键节点说明：**

| 步骤 | 说明 | 失败处理 |
|------|------|---------|
| 请求校验 | 验证参数合法性 | 返回 400，不创建任务 |
| 附件处理 | 解码并存储附件 | SSE 推送 error，清理临时文件后终止 |
| ReAct 执行 | Agent 自主执行 | 推送 error 事件，终止 |
| 清理临时文件 | 删除 temp/{task_id}/ | 无论成功/失败/取消都执行 |

**intent 事件含义：**

`intent` 事件表示"准备工作全部完成，Agent 开始执行"。准备工作包括：
- 请求校验
- 任务记录创建
- 附件处理（如有）

只有准备工作全部成功后，才推送 `intent` 事件。如果准备工作失败（如附件解码失败），直接推送错误事件并清理，不会推送 `intent`。

**附件存储目录结构：**

```
{GROOT_HOME}/temp/
├── task-20260417-103000523-a1b2/     # 任务A的临时目录
│   ├── report.pdf                    # 附件1
│   └── data.csv                      # 附件2
├── task-20260417-103010000-b2c3/     # 任务B的临时目录
│   └── config.json                   # 附件（同名不冲突）
└── ...
```

每个任务的附件存储在独立目录 `temp/{task_id}/` 下，并发请求完全隔离。

**附件路径传递方式：**

附件信息以结构化文本形式嵌入用户消息，Agent 解析后调用 MCP `file_read` 工具读取：

```
用户指令内容

附件:
- report.pdf (file)
  路径: /Users/xxx/.groot/temp/task-xxx/report.pdf
  类型: application/pdf
  大小: 1024000 bytes
```

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

**task_id 生成规则：**
- 格式：`task-{YYYYMMDD}-{HHMMSSmmm}-{random4}`
- 示例：`task-20260417-103000523-a1b2`
- 时间戳精确到毫秒，random 为 4 位随机字符
- 全局唯一，多实例部署不冲突

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
{"step_id":"20260417-103005000-x9y8z7","timestamp":"2026-04-17T10:30:05Z","status":"failed","error":{"code":"file_error","message":"文件不存在"}}
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
{"status":"failed","timestamp":"2026-04-17T10:30:05Z","duration":"5s","error":{"code":"skill_error","message":"执行失败"}}
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

### 3.3 DELETE /task/{task_id}

**请求方式：** 路径参数

```
DELETE /task/task-xxx
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

### 3.5 GET /task/history

**请求方式：** Query 参数

```
GET /task/history?status=completed&start_time=202604010000&end_time=202604172359&limit=10&offset=0
```

**Query 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `status` | string | 否 | 按状态过滤：`running` / `completed` / `failed` / `cancelled` |
| `start_time` | string | 否 | 开始时间，格式 `yyyyMMddHHmm`，如 `202604010000` |
| `end_time` | string | 否 | 结束时间，格式 `yyyyMMddHHmm`，如 `202604172359` |
| `limit` | int | 否 | 返回数量限制，默认 20，最大 100 |
| `offset` | int | 否 | 分页偏移，默认 0 |

**响应：**

成功：
```json
{
  "status": "success",
  "total": 50,
  "limit": 10,
  "offset": 0,
  "tasks": [
    {
      "id": "task-xxx",
      "instruction": "分析PDF报告",
      "status": "completed",
      "start_time": "2026-04-17T10:30:00Z",
      "end_time": "2026-04-17T10:30:45Z",
      "duration": 45,
      "caller": "internal_system"
    },
    ...
  ]
}
```

失败（无匹配）：
```json
{
  "status": "success",
  "total": 0,
  "tasks": []
}
```

### 3.6 GET /task/{task_id}

**请求方式：** 路径参数

```
GET /task/task-xxx
```

**响应：**

成功（包含完整步骤记录）：
```json
{
  "status": "success",
  "task": {
    "id": "task-xxx",
    "instruction": "分析PDF报告",
    "prompt": "你是一个财务分析师...",
    "status": "completed",
    "start_time": "2026-04-17T10:30:00Z",
    "end_time": "2026-04-17T10:30:45Z",
    "duration": 45,
    "caller": "internal_system",
    "result": {
      "document_type": "report",
      "summary": "..."
    },
    "steps": [
      {
        "step_id": "20260417-103000000-a1b2c3",
        "type": "skill",
        "name": "pdf_analyzer",
        "start_time": "2026-04-17T10:30:00Z",
        "end_time": "2026-04-17T10:30:30Z",
        "status": "success",
        "nesting_level": 0
      },
      {
        "step_id": "20260417-103005000-x9y8z7",
        "type": "tool",
        "name": "file_read",
        "start_time": "2026-04-17T10:30:05Z",
        "end_time": "2026-04-17T10:30:10Z",
        "status": "success",
        "nesting_level": 1
      }
    ]
  }
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

### 3.7 GET /health

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

### 3.8 API 详细示例

#### POST /task/execute 请求示例

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

#### SSE 响应事件流示例

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
data: {"step_id":"20260417-103000000-a1b2c3","timestamp":"2026-04-17T10:30:05Z","status":"failed","error":{"code":"file_error","message":"PDF文件已损坏"}}

event: completed
data: {"status":"failed","timestamp":"2026-04-17T10:30:05Z","duration":"5s","error":{"code":"skill_error","message":"pdf_analyzer执行失败"}}
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

#### 其他 API 响应示例

**DELETE /task/{task_id}：**

请求：
```http
DELETE /task/task-20260417-103000523-a1b2 HTTP/1.1
Host: localhost:8080
X-API-Key: groot-api-key-2026abc
```

成功响应：
```json
{
  "status": "success",
  "task_id": "task-20260417-103000523-a1b2",
  "message": "任务已取消"
}
```

失败响应（任务已完成）：
```json
{
  "status": "task_completed",
  "task_id": "task-20260417-103000523-a1b2",
  "message": "任务已完成，无法取消"
}
```

失败响应（任务不存在）：
```json
{
  "status": "task_not_found",
  "task_id": "task-20260417-103000523-a1b2",
  "message": "任务不存在"
}
```

**GET /task/status/{task_id}：**

请求：
```http
GET /task/status/task-20260417-103000523-a1b2 HTTP/1.1
Host: localhost:8080
X-API-Key: groot-api-key-2026abc
```

运行中响应：
```json
{
  "status": "success",
  "task_id": "task-20260417-103000523-a1b2",
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

已完成响应：
```json
{
  "status": "success",
  "task_id": "task-20260417-103000523-a1b2",
  "task_status": "completed",
  "started_at": "2026-04-17T10:30:00Z",
  "elapsed_time": "45s"
}
```

失败响应（任务不存在）：
```json
{
  "status": "task_not_found",
  "task_id": "task-20260417-103000523-a1b2",
  "message": "任务不存在"
}
```

**GET /health：**

请求：
```http
GET /health HTTP/1.1
Host: localhost:8080
```

健康响应：
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

**GET /skills：**

请求：
```http
GET /skills HTTP/1.1
Host: localhost:8080
X-API-Key: groot-api-key-2026abc
```

响应：
```json
{
  "skills": [
    {"name": "pdf_analyzer", "description": "分析PDF文档并生成摘要"},
    {"name": "code_generator", "description": "根据需求生成代码"},
    {"name": "data_analyzer", "description": "分析结构化数据文件"},
    {"name": "report_generator", "description": "综合分析生成报告"}
  ],
  "total": 4
}
```

**GET /tools：**

请求：
```http
GET /tools HTTP/1.1
Host: localhost:8080
X-API-Key: groot-api-key-2026abc
```

响应：
```json
{
  "tools": [
    {"name": "file_read", "description": "读取文件内容", "mcp": "file_operations"},
    {"name": "file_write", "description": "写入文件内容", "mcp": "file_operations"},
    {"name": "directory_list", "description": "列出目录内容", "mcp": "file_operations"},
    {"name": "http_get", "description": "发送HTTP GET请求", "mcp": "http_request"},
    {"name": "http_post", "description": "发送HTTP POST请求", "mcp": "http_request"}
  ],
  "total": 5
}
```

---

## 四、存储架构设计

### 4.1 存储抽象层

Groot 采用存储抽象层设计，支持多种存储引擎，通过配置文件指定。

**架构示意：**

```
┌─────────────────────────────────────────────────────────────┐
│                     TaskStorage Interface                    │
│  - Create(task) → task_id                                   │
│  - Get(task_id) → Task                                      │
│  - Update(task_id, updates) → bool                          │
│  - Delete(task_id) → bool                                   │
│  - List(query) → []Task                                     │
│  - Exists(task_id) → bool                                   │
└─────────────────────────────────────────────────────────────┘
                              │
          ┌──────────────────┼──────────────────┐
          ▼                  ▼                  ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│ BoltDB Storage  │  │  Redis Storage  │  │   etcd Storage  │
│   (单机版)       │  │  (集群预留)      │  │   (集群预留)     │
└─────────────────┘  └─────────────────┘  └─────────────────┘
```

### 4.2 存储引擎配置

```yaml
storage:
  engine: boltdb          # 存储引擎：boltdb（单机）、redis（集群）、etcd（集群）
  
  boltdb:                 # BoltDB 配置（单机版）
    file: groot.db        # 数据库文件路径（相对工作目录）
    bucket: tasks         # 存储桶名称
    
  redis:                  # Redis 配置（集群版预留）
    endpoint: ${REDIS_ENDPOINT}
    password: ${REDIS_PASSWORD}
    key_prefix: groot:task:
    
  etcd:                   # etcd 配置（集群版预留）
    endpoints: [${ETCD_ENDPOINT_1}, ${ETCD_ENDPOINT_2}]
    key_prefix: /groot/tasks/
```

### 4.3 TaskStorage 接口定义

```go
// TaskStorage 存储接口
type TaskStorage interface {
    // Create 创建新任务记录
    Create(task *Task) (taskID string, err error)
    
    // Get 根据ID获取任务
    Get(taskID string) (*Task, error)
    
    // Update 更新任务状态/进度
    Update(taskID string, updates map[string]interface{}) error
    
    // Delete 删除任务记录
    Delete(taskID string) error
    
    // List 查询任务列表（支持过滤）
    List(query *TaskQuery) ([]*Task, error)
    
    // Exists 检查任务是否存在
    Exists(taskID string) bool
    
    // Close 关闭存储连接
    Close() error
}

// TaskQuery 查询条件
type TaskQuery struct {
    Status    []string  // 按状态过滤：running, completed, failed, cancelled
    StartTime *TimeRange // 时间范围
    Limit     int       // 返回数量限制
    Offset    int       // 分页偏移
}

// TimeRange 时间范围
type TimeRange struct {
    Start time.Time
    End   time.Time
}
```

### 4.4 BoltDB 实现（单机版）

BoltDB 是嵌入式键值数据库，无需额外部署，适合单机运行场景。

**实现要点：**

- 数据存储在本地文件 `{GROOT_HOME}/groot.db`
- 使用 Bucket 按任务状态分区
- 支持 TTL 自动清理过期任务记录（默认保留7天）

**存储结构：**

```
Bucket: tasks
├── {task_id} → Task JSON
├── {task_id} → Task JSON
└── ...

Bucket: tasks_by_status
├── running → [{task_id_1}, {task_id_2}, ...]
├── completed → [{task_id_3}, {task_id_4}, ...]
├── failed → [...]
└── cancelled → [...]
```

### 4.5 Redis 实现（集群版预留）

用于多实例部署场景，提供：
- 任务状态共享（所有实例可查询）
- 任务取消广播（Pub/Sub 消息通知）
- 分布式锁（防止多实例重复处理）

**预留接口：**

```go
// RedisStorage 扩展接口（预留）
type RedisStorage interface {
    TaskStorage
    
    // Subscribe 订阅任务取消消息
    Subscribe(cancelChan chan string) error
    
    // Publish 发布任务取消消息
    Publish(taskID string) error
    
    // AcquireLock 获取任务处理锁
    AcquireLock(taskID string, ttl int) bool
    
    // ReleaseLock 释放任务处理锁
    ReleaseLock(taskID string) error
}
```

### 4.6 etcd 实现（集群版预留）

用于多实例部署场景，提供：
- 任务状态共享（强一致性）
- 任务取消广播（Watch 监听）
- 分布式锁

**预留接口：**

```go
// EtcdStorage 扩展接口（预留）
type EtcdStorage interface {
    TaskStorage
    
    // Watch 监听任务状态变更
    Watch(taskID string) (<-chan TaskEvent, error)
    
    // Broadcast 广播任务取消
    Broadcast(taskID string, event string) error
    
    // AcquireLock 分布式锁
    AcquireLock(taskID string, ttl int) bool
    
    // ReleaseLock 释放锁
    ReleaseLock(taskID string) error
}

// TaskEvent 任务事件
type TaskEvent struct {
    TaskID    string
    EventType string  // cancelled, status_changed
    Timestamp time.Time
}
```

### 4.7 任务数据结构

```go
// Task 任务记录
type Task struct {
    ID           string        `json:"id"`             // task_id
    Instruction  string        `json:"instruction"`    // 用户指令
    Prompt       string        `json:"prompt"`         // 系统提示词
    Attachments  []Attachment  `json:"attachments"`    // 附件列表
    Status       string        `json:"status"`         // running, completed, failed, cancelled
    Progress     *TaskProgress `json:"progress"`       // 执行进度
    Result       interface{}   `json:"result"`         // 最终结果
    Error        *TaskError    `json:"error"`          // 错误信息
    StartTime    time.Time     `json:"start_time"`     // 开始时间
    EndTime      time.Time     `json:"end_time"`       // 结束时间
    Duration     int           `json:"duration"`       // 耗时（秒）
    Caller       string        `json:"caller"`         // 调用方（API Key name）
    Steps        []StepRecord  `json:"steps"`          // 步骤记录
}

// Attachment 附件信息
type Attachment struct {
    Type    string `json:"type"`    // file, url
    Name    string `json:"name"`    // 文件名
    Content string `json:"content"` // Base64内容或URL
}

// TaskProgress 任务进度
type TaskProgress struct {
    CurrentStep    int `json:"current_step"`
    StepsCompleted int `json:"steps_completed"`
    Percentage     int `json:"percentage"`
}

// TaskError 错误信息
type TaskError struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}

// StepRecord 步骤记录
type StepRecord struct {
    StepID        string    `json:"step_id"`
    Type          string    `json:"type"`        // skill, tool, llm
    Name          string    `json:"name"`
    StartTime     time.Time `json:"start_time"`
    EndTime       time.Time `json:"end_time"`
    Status        string    `json:"status"`
    NestingLevel  int       `json:"nesting_level"`
    Error         *TaskError `json:"error,omitempty"`
}
```

### 4.8 任务生命周期管理

**任务状态流转：**

```
创建 → running → completed（成功）
                → failed（失败）
                → cancelled（取消）
```

**任务清理策略：**

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `retention_days` | 任务记录保留天数 | 7 |
| `cleanup_interval` | 清理任务执行间隔 | 24h |

配置示例：

```yaml
storage:
  engine: boltdb
  boltdb:
    file: groot.db
    bucket: tasks
  retention_days: 7           # 任务记录保留天数
  cleanup_interval: 24h       # 清理任务执行间隔
```

---

## 五、Skills 注册机制

### 5.1 目录结构

```
{GROOT_HOME}/skills/
├── pdf_analyzer/
│   └── SKILL.md
├── code_generator/
│   └── SKILL.md
├── data_analyzer/
│   └── SKILL.md
└── report_generator/
    └── SKILL.md
```

### 5.2 Skill 定义格式

遵循 Claude Code 官方标准（YAML frontmatter + Markdown），兼容 skills.sh 和 skillstore.io。

**SKILL.md 结构：**

```markdown
---
name: skill_name
description: "技能描述，用于 Agent 工具列表展示"
---

# Skill 标题

技能的详细指令和说明内容...
```

**Frontmatter 字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | Skill 名称（全局唯一） |
| `description` | string | 是 | Skill 描述，用于 Agent 工具列表 |
| `dependencies` | array | 否 | 依赖的其他 Skill 名称列表（可选） |

**dependencies 字段示例：**

```markdown
---
name: report_generator
description: "综合分析多种来源的资料，生成完整的分析报告"
dependencies: [pdf_analyzer, data_analyzer]
---
```

当 Skill 声明了 dependencies，Agent 在执行时会自动识别并递归调用依赖的子 Skills。依赖处理由 eino 框架内部完成。

Markdown 正文部分可自由组织，通常包含：
- 执行步骤说明
- 使用工具说明
- 输出格式定义
- 示例（可选）

### 5.3 注册流程

```
程序启动 → 扫描 skills 目录 → 解析每个 SKILL.md →
提取 frontmatter 中的 name/description →
解析 Markdown 正文内容 → 注册到内存索引
```

### 5.4 Skills 热插拔机制

支持运行时动态添加、修改、删除 Skills，无需重启服务。

**监听机制：**
- 使用 `fsnotify` 监听 skills 目录变化
- 只监听 `SKILL.md` 文件的创建、修改、删除事件
- 防抖机制：检测到变化后延迟 2秒再执行加载，避免编辑过程中频繁触发

**处理流程：**

```
文件变化检测 → 防抖等待（2秒） →
│
├─ 新增 SKILL.md → 解析并注册 → 输出日志
│
├─ 修改 SKILL.md → 重新解析并更新 → 输出日志
│
└─ 删除 SKILL.md → 移除对应 Skill → 输出日志
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

## 六、Agent 执行流程

### 6.1 ReAct 执行循环

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

### 6.2 Skills 嵌套支持

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

### 6.3 取消任务机制

```
DELETE /task/{task_id} →
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

## 七、MCP 配置与管理

### 7.1 MCP 配置目录结构

MCP 配置采用独立目录，每个 MCP 一个 JSON 文件，支持热插拔：

```
{GROOT_HOME}/mcp/
├── file_operations.json      # 内置 MCP（文件操作）
├── http_request.json         # 内置 MCP（HTTP请求）
├── web_parser.json           # 外部 MCP（网页解析）
└── database_tool.json        # 外部 MCP（数据库操作）
```

### 7.2 MCP 连接类型

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `stdio` | 标准输入输出通信 | 本地命令行工具 |
| `sse` | Server-Sent Events | 远程 HTTP 服务（单向推送） |
| `streamable_http` | Streamable HTTP | 远程 HTTP 服务（双向流式） |

### 7.3 MCP 配置文件格式

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

### 7.4 配置字段说明

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

### 7.5 MCP 热插拔机制

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

**说明：** MCP 连接的健康检查、重连机制等由 eino 框架内部处理，无需额外设计。

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

### 7.6 内置 MCP 工具

内置 MCP 工具是 Groot 自带的工具集，与外部 MCP 配置方式不同：

**内置 MCP 特点：**
- 无需配置连接参数，直接可用
- 有独立的安全限制配置
- 配置文件中 `type: "builtin"` 表示内置工具

**内置 MCP 配置示例：**

**file_operations（文件操作）：**

```json
{
  "name": "file_operations",
  "type": "builtin",
  "description": "文件读写和目录操作",
  "isActive": true,
  "tools": ["file_read", "file_write", "file_search", "directory_list", "directory_create"],
  "restrictions": {
    "allowed_paths": ["/home/zfd/temp", "/home/zfd/workspace/groot/skills"],
    "denied_operations": ["file_delete"]
  }
}
```

**说明：** `type: "builtin"` 表示这是内置工具，不是 MCP 连接类型。内置工具直接由 Groot 执行，无需通过 MCP 协议连接。

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

## 八、内置 MCP 工具定义

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
- 仅允许访问配置中 `allowed_paths` 指定的真实目录路径
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

## 九、并发与性能控制

### 9.1 限流配置

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

### 9.2 ReAct 执行限制

防止 Agent 无限循环或成本失控：

```yaml
react:
  max_iterations: 20          # 最大循环次数，-1 表示不限制
  max_tokens: 100000          # 最大Token消耗，-1 表示不限制
  step_timeout: 60            # 单步执行超时（秒），-1 表示不限制
  error_retry: 2              # 单步失败重试次数
  nesting_max_depth: 3        # Skills嵌套最大深度，-1 表示不限制
```

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `max_iterations` | ReAct 最大循环次数，防止无限循环。`-1` 表示不限制 | 20 |
| `max_tokens` | 单任务最大Token消耗，防止成本失控。`-1` 表示不限制 | 100000 |
| `step_timeout` | 单步执行超时时间（秒）。`-1` 表示不限制 | 60 |
| `error_retry` | 单步失败后重试次数 | 2 |
| `nesting_max_depth` | Skills 嵌套最大深度。`-1` 表示不限制 | 3 |

**终止条件说明：**

| 条件 | 触发时机 | SSE 事件 |
|------|---------|---------|
| Agent判断完成 | LLM输出最终答案 | `completed` (success) |
| 达到最大循环次数 | iteration > max_iterations | `completed` (failed) |
| Token消耗超限 | tokens_used > max_tokens | `completed` (failed) |
| 单步执行超时 | step_duration > step_timeout | `completed` (failed) |
| 用户取消 | 调用 DELETE /task/{task_id} | `completed` (cancelled) |

### 9.3 错误响应

**HTTP 状态码：**

| 场景 | HTTP 状态码 |
|------|------------|
| 请求限流触发 | 429 |
| 参数错误 | 400 |
| 未认证 | 401 |
| 权限不足 | 403 |
| 配置错误 | 500 |

**JSON 响应格式：**

所有错误响应均返回统一 JSON 格式：

```json
{
  "status": "error_code",
  "message": "错误描述信息"
}
```

**示例：**

400 参数错误：
```json
{
  "status": "invalid_request",
  "message": "instruction 字段不能为空"
}
```

429 限流：
```json
{
  "status": "rate_limited",
  "message": "请求频率超限，请稍后重试"
}
```

500 配置错误：
```json
{
  "status": "config_error",
  "message": "LLM 配置无效，请检查 base_url 和 api_key"
}
```

---

## 十、错误处理机制

### 10.1 错误码定义

| 错误码 | 说明 | 可恢复 |
|--------|------|--------|
| `invalid_request` | 请求参数错误 | 否 |
| `rate_limited` | 请求被限流 | 否 |
| `llm_connection_error` | LLM 连接失败 | 可重试 |
| `llm_rate_limited` | LLM API 限流 | 可重试 |
| `llm_timeout` | LLM 调用超时 | 可重试 |
| `tool_call_error` | 工具调用失败 | 可重试 |
| `skill_not_found` | Skill 不存在 | 否 |
| `task_timeout` | 任务执行超时 | 否 |
| `task_cancelled` | 用户取消 | 否 |

### 10.2 重试策略

| 场景 | 重试次数 | 重试间隔 |
|------|---------|---------|
| LLM 连接失败 | 3 | 2s |
| LLM Rate Limit | 3 | 5s |
| MCP 工具失败 | 2 | 1s |

---

## 十一、日志机制

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

### 11.3 日志监控采集

JSON 结构化日志可直接用于监控采集，通过 ELK（Elasticsearch + Logstash + Kibana）或类似日志系统分析。

**日志事件字段：**

| 字段 | 说明 |
|------|------|
| `event` | 事件类型（如 `task_completed`、`skill_call`） |
| `timestamp` | 时间戳（ISO格式） |
| `level` | 日志级别 |
| `data` | 事件详情（含耗时、计数等） |

**监控日志示例：**

```json
{
  "timestamp": "2026-04-17T10:30:00Z",
  "level": "INFO",
  "event": "task_completed",
  "data": {
    "task_id": "task-xxx",
    "duration": 45,
    "skill_calls": 3,
    "llm_tokens": 5000,
    "status": "success"
  }
}
```

```json
{
  "timestamp": "2026-04-17T10:30:05Z",
  "level": "INFO",
  "event": "skill_call",
  "data": {
    "skill_name": "pdf_analyzer",
    "duration": 30,
    "nesting_level": 0
  }
}
```

---

## 十二、安全性设计

### 12.1 认证配置

```yaml
security:
  auth:
    enabled: true               # 是否开启认证，true 开启，false 关闭
    type: api_key               # 认证类型，目前只支持 api_key
    api_key:
      header_name: X-API-Key    # 认证 Header 名称（可选，默认 X-API-Key）
      keys:
        - name: default         # Key 名称（唯一标识）
          key: ${GROOT_API_KEY} # Key 值（支持环境变量）
          permissions: all      # 权限范围
```

### 12.2 认证类型

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `api_key` | API Key 认证，通过 HTTP Header 传递 | 服务间调用、简单鉴权 |

`type` 字段预留扩展能力，后续可支持其他认证类型（如 JWT、OAuth2）。

### 12.3 API Key 认证流程

**调用方请求示例：**

```http
POST /task/execute HTTP/1.1
Host: localhost:8080
X-API-Key: groot-api-key-2026abc
Content-Type: application/json

{
  "instruction": "帮我分析这份PDF报告",
  "attachments": [...]
}
```

**cURL 示例：**

```bash
curl -X POST http://localhost:8080/task/execute \
  -H "X-API-Key: groot-api-key-2026abc" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "帮我分析这份PDF报告"}'
```

**认证流程：**

```
请求到达 → Auth 中间件拦截 →
│
├─ enabled=false → 跳过认证，直接处理请求
│
├─ enabled=true → 执行认证
│   ├─ 提取 Header 中的 API Key（Header 名称由 header_name 配置）
│   ├─ 检查 Key 是否在 keys 列表中
│   │   ├─ 不存在 → 返回 401 Unauthorized
│   │   └─ 存在 → 继续检查权限
│   ├─ 检查 Key 关联的 permissions 是否包含该 API所需权限
│   │   ├─ 不包含 → 返回 403 Forbidden
│   │   └─ 包含 → 认证通过
│   └─ 认证通过 → 记录调用方 name 到日志 → 继续处理请求
│
└─ 处理请求
```

**认证失败响应示例：**

401 Unauthorized（Key 无效或缺失）：
```json
{
  "status": "unauthorized",
  "message": "API Key 无效或缺失"
}
```

403 Forbidden（Key 有效但权限不足）：
```json
{
  "status": "forbidden",
  "message": "权限不足，无法访问该 API"
}
```

### 12.4 多 Key 配置示例

**场景：不同调用方使用不同 Key 和权限**

```yaml
security:
  auth:
    enabled: true
    type: api_key
    api_key:
      header_name: X-API-Key
      keys:
        - name: internal_system        # 内部业务系统
          key: ${GROOT_INTERNAL_KEY}   # Key 值：自定义字符串
          permissions: all             # 全部权限
        
        - name: external_partner       # 外部合作方
          key: partner-key-2026        # Key 值：直接写或环境变量
          permissions: [execute, status]  # 只能执行和查询
        
        - name: monitor_service        # 监控服务
          key: ${GROOT_MONITOR_KEY}
          permissions: [status, health, skills, tools]  # 只能查询
```

### 12.5 权限定义

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

### 12.6 认证开启/关闭场景

| 运行模式 | enabled | 说明 |
|----------|---------|------|
| 单实例部署 | `true` | Groot 自身做 API Key 鉴权，保护 API 安全 |
| 集群部署（有统一 Gateway） | `false` | Gateway 统一鉴权，Groot 不重复验证 |
| 内网环境（可信网络） | `false` | 内网隔离，无需认证 |

**集群模式架构示意：**

```
调用方 → Gateway（统一鉴权） → Groot 实例 1
                           → Groot 实例 2
                           → Groot 实例 3
```

Gateway 验证后，转发请求到 Groot 实例，Groot 不再重复验证（`enabled: false`）。

### 12.7 敏感信息保护

- API Key 值建议通过环境变量存储，避免硬编码
- 不记录 API Key 值到日志（只记录调用方 name）
- 日志脱敏处理，敏感字段不输出

---

## 十三、附件处理配置

> **处理流程说明：** 附件的完整处理流程（校验、解码、存储、路径传递、清理）已在前文 **3.2 POST /task/execute** 的"完整处理流程"中详细说明，本节仅补充配置和技术细节。

### 13.1 支持的附件类型

| 类型 | 格式 | MIME 类型 |
|------|------|-----------|
| 文档 | PDF、DOC、DOCX、TXT | application/pdf, application/msword, text/plain |
| 数据 | JSON、CSV、XML、YAML | application/json, text/csv, application/xml |
| 代码 | 源码文件 | text/plain |
| 图片 | PNG、JPG | image/png, image/jpeg |
| 压缩 | ZIP、TAR | application/zip, application/x-tar |

### 13.2 传输方式

| 方式 | 适用场景 | 说明 |
|------|---------|------|
| Base64 编码 | 小文件（<10MB） | 附件内容通过 `content` 字段传递 |
| URL 链接 | 大文件或外部资源 | 通过 `url` 字段传递，不进行本地存储 |

### 13.3 处理责任划分

**Groot 核心负责：**
- 接收附件并验证（大小、数量、类型）
- Base64 解码并存储到临时目录 `temp/{task_id}/`
- 将附件路径信息嵌入用户消息
- 任务完成后自动清理临时文件

**MCP 工具负责：**
- 实际读取文件内容（`file_read`）
- 解析特定格式（PDF、CSV 等）
- 处理图片、压缩包等

### 13.4 配置

```yaml
attachment:
  max_size: 50                    # 单个附件最大大小（MB）
  max_total_size: 100             # 所有附件总大小上限（MB）
  max_count: 10                   # 单次请求最大附件数量
  allowed_types: [pdf, doc, json, csv, png, zip]  # 允许的附件类型
  temp_directory: temp            # 附件临时存储目录（支持绝对路径或相对路径，见下方说明）
```

**temp_directory 配置说明：**

| 配置值 | 实际路径 | 说明 |
|--------|---------|------|
| `temp` | `{GROOT_HOME}/temp` | 相对路径，与工作目录拼接 |
| `./temp` | `{GROOT_HOME}/temp` | 相对路径，等效于 `temp` |
| `/home/zfd/temp` | `/home/zfd/temp` | 绝对路径，直接使用 |
| `/tmp/groot` | `/tmp/groot` | 绝对路径，系统临时目录 |

**配置规则：**
- 以 `/` 开头：视为绝对路径，直接使用
- 其他情况：视为相对路径，与 `{GROOT_HOME}` 拼接
- `filepath.Clean` 会自动处理 `./temp` → `temp`

**建议：**
- 单实例部署：使用相对路径 `temp`（默认）
- 需要更大磁盘空间：使用绝对路径指向独立存储盘
- 需要系统临时目录：使用 `/tmp/groot`（注意清理策略）

---

## 十四、目录结构与配置

### 14.1 工作目录

默认：`~/.groot`，可通过命令行或环境变量更改。

```
{GROOT_HOME}/
├── config.yaml
├── skills/
│   └── {skill-name}/SKILL.md
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

## 十五、启动与部署

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
加载配置 → 初始化日志 → 初始化存储引擎 → 注册 Skills → 加载 MCP →
初始化 LLM → 启动 HTTP 服务 → 等待请求
```

### 15.3 优雅关闭

- 停止接受新请求
- 等待当前任务完成（超时30秒）
- 关闭 MCP 连接
- 刷新日志
- 退出程序

---

## 十六、完整配置文件模板

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
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4o
      max_tokens: 4096
      temperature: 0.7
    claude-3.5:
      base_url: https://api.anthropic.com/v1
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

# 存储配置
storage:
  engine: boltdb                # 存储引擎：boltdb（单机）、redis（集群预留）、etcd（集群预留）
  boltdb:
    file: groot.db              # 数据库文件路径（相对工作目录）
    bucket: tasks               # 存储桶名称
  retention_days: 7             # 任务记录保留天数
  cleanup_interval: 24h         # 清理任务执行间隔

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
  max_iterations: 20          # 最大循环次数，防止无限循环，-1 表示不限制
  max_tokens: 100000          # 最大Token消耗，防止成本失控，-1 表示不限制
  step_timeout: 60            # 单步执行超时（秒），-1 表示不限制
  error_retry: 2              # 单步失败重试次数
  nesting_max_depth: 3        # Skills 嵌套最大深度，-1 表示不限制

# 附件处理配置
attachment:
  max_size: 50                    # 单个附件最大大小（MB）
  max_total_size: 100             # 所有附件总大小上限（MB）
  max_count: 10                   # 单次请求最大附件数量
  allowed_types: [pdf, doc, docx, txt, json, csv, xml, yaml, png, jpg, zip]  # 允许的附件类型
  temp_directory: temp            # 附件临时存储目录（支持绝对路径或相对路径）

# 安全配置
security:
  auth:
    enabled: true               # 是否开启认证，集群模式可关闭
    type: api_key               # 认证类型
    api_key:
      header_name: X-API-Key    # 认证 Header 名称
      keys:
        - name: default         # Key 名称（唯一标识）
          key: ${GROOT_API_KEY} # Key 值（环境变量或直接写）
          permissions: all      # 权限范围

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
```

---

## 十七、Skill 示例

### 17.1 pdf_analyzer

**文件路径：** `{GROOT_HOME}/skills/pdf_analyzer/SKILL.md`

```markdown
---
name: pdf_analyzer
description: "分析PDF文档内容，提取关键信息并生成结构化摘要报告"
---

# PDF 文档分析

你是一个专业的PDF文档分析助手。

## 执行步骤

1. 使用 file_operations.file_read 工具读取PDF文件
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

### 17.2 code_generator

**文件路径：** `{GROOT_HOME}/skills/code_generator/SKILL.md`

```markdown
---
name: code_generator
description: "根据用户需求描述生成代码，支持多种编程语言"
---

# 代码生成

你是一个专业的代码生成助手。

## 执行步骤

1. 分析用户需求，明确功能目标、输入输出规格、编程语言
2. 设计代码结构和逻辑
3. 生成完整的代码实现，包含注释和错误处理
4. 生成使用示例或测试代码

## 输出格式

{
  "language": "编程语言",
  "code": "完整代码",
  "usage_example": "使用示例",
  "test_code": "测试代码"
}
```

### 17.3 data_analyzer

**文件路径：** `{GROOT_HOME}/skills/data_analyzer/SKILL.md`

```markdown
---
name: data_analyzer
description: "分析结构化数据文件（CSV、JSON等），执行统计分析和趋势识别"
---

# 数据分析

你是一个数据分析助手。

## 执行步骤

1. 使用 file_operations.file_read 工具读取数据文件
2. 解析数据结构，识别字段含义和数据类型
3. 执行统计分析、趋势分析、关联分析
4. 生成分析结果和可视化建议

## 输出格式

{
  "data_overview": {"rows": 数量, "columns": 数量},
  "statistics": {"summary": "统计摘要"},
  "trends": ["趋势描述"],
  "insights": ["数据洞察"]
}
```

### 17.4 report_generator（嵌套Skill示例）

**文件路径：** `{GROOT_HOME}/skills/report_generator/SKILL.md`

```markdown
---
name: report_generator
description: "综合分析多种来源的资料，生成完整的分析报告"
---

# 报告生成

你是一个报告生成助手，可调用其他 Skills 完成综合分析。

## 执行步骤

1. 分析用户提供的资料类型
2. 根据资料类型调用相应的分析 Skills：
   - PDF 文件 → 调用 pdf_analyzer
   - 数据文件 → 调用 data_analyzer
3. 整合各 Skills 的分析结果
4. 生成结构化的综合报告
5. 使用 file_operations.file_write 保存报告
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
| `REDIS_ENDPOINT` | Redis 服务地址 | 否（集群版时需要） |
| `REDIS_PASSWORD` | Redis 密码 | 否（集群版时需要） |
| `ETCD_ENDPOINT_*` | etcd 服务地址 | 否（集群版时需要） |

### B. 默认端口

| 服务 | 端口 |
|------|------|
| HTTP API | 8080 |

### C. 文件路径约定

| 路径 | 说明 |
|------|------|
| `{GROOT_HOME}/config.yaml` | 配置文件 |
| `{GROOT_HOME}/skills/` | Skills 目录 |
| `{GROOT_HOME}/mcp/` | MCP 配置目录 |
| `{GROOT_HOME}/logs/` | 日志目录 |
| `{GROOT_HOME}/temp/` | 临时文件 |
| `{GROOT_HOME}/groot.db` | BoltDB 数据库文件（单机版） |