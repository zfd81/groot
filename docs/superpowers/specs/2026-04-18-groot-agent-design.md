# Groot AI Agent 设计文档

**版本:** 1.0.0
**日期:** 2026-04-18
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
| 持久化存储 | Memory（文件系统：JSON + attachments） |
| 运行时状态 | 内存管理（sync.Map） |
| 配置格式 | YAML |
| 日志格式 | JSON 结构化（支持日志采集监控） |

### 1.3 LLM 配置

支持多模型配置，通过 `default_model` 指定默认使用的模型。

**配置示例：**

```yaml
llm:
  default_model: gpt-4o           # 默认模型
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
| `default_model` | 默认模型名称，对应 models 中的某个 key |
| `base_url` | LLM API 地址（OpenAI 兼容协议） |
| `api_key` | API 密钥，支持环境变量引用 `${VAR_NAME}` |
| `model` | 实际调用时的模型名称 |
| `max_tokens` | 单次调用最大 Token 数 |
| `temperature` | 输出随机性（0-1，越高越随机） |

**模型切换：**

修改 `default_model` 值后，需要重启服务才能生效。

> **说明：** Groot 仅支持 Skills 的热插拔，LLM 和 MCP 配置修改需重启服务。

---

## 二、架构设计

### 2.1 三层架构图

```
┌───────────────────────────────────────────────────────────────────────────┐
│                           Access Layer（接入层）                           │
├───────────────────────────────────────────────────────────────────────────┤
│                                                                           │
│  ┌─────────────────────────────────┐  ┌─────────────────────────────────┐ │
│  │         REST API (Hertz)        │  │      Security & Control         │ │
│  ├─────────────────────────────────┤  ├─────────────────────────────────┤ │
│  │  POST   /chat                   │  │  Auth Middleware                │ │
│  │  DELETE /chat/{sid}             │  │  SSE Stream Handler             │ │
│  │  GET    /chat/status/{sid}      │  │  Attachment Handler             │ │
│  │  GET    /chat/{sid}             │  │                                 │ │
│  │  GET    /sess/{sid}             │  │                                 │ │
│  │  GET    /sess/history           │  │                                 │ │
│  │  GET    /skills                 │  │                                 │ │
│  │  GET    /tools                  │  │                                 │ │
│  │  GET    /health                 │  │                                 │ │
│  └─────────────────────────────────┘  └─────────────────────────────────┘ │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                        Intelligence Layer（智能层）                        │
├───────────────────────────────────────────────────────────────────────────┤
│                                                                           │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │                      ReAct Agent Engine                             │  │
│  ├─────────────────────────────────────────────────────────────────────┤  │
│  │                                                                     │  │
│  │   ┌────────────┐      ┌────────────┐      ┌────────────┐            │  │
│  │   │  Reasoning │ ───▶ │   Acting   │ ───▶ │ Observation│ ───┐       │  │
│  │   │            │      │            │      │            │    │       │  │
│  │   │  LLM调用   │      │  Skill调用 │      │  结果处理  │    │       │  │
│  │   │  上下文    │      │  MCP工具   │      │  状态更新  │    │       │  │
│  │   │  决策判断  │      │  直接回答  │      │  终止检查  │ ◀──┘       │  │
│  │   └────────────┘      └────────────┘      └────────────┘            │  │
│  │                                                                     │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
│                          │              │              │                  │
│                          ▼              ▼              ▼                  │
│  ┌───────────────────────┐┌───────────────────────┐┌───────────────────┐ │
│  │        Skills         ││          MCP          ││       Memory      │ │
│  ├───────────────────────┤├───────────────────────┤├───────────────────┤ │
│  │  Skills加载           ││  MCP加载              ││  Session管理      │ │
│  │  指令解析             ││  工具调用              ││  History管理      │ │
│  │  注册给Agent          ││  权限检查              ││  ChatRecorder     │ │
│  │  热插拔               ││  热插拔                ││  RuntimeState     │ │
│  │                       ││                       ││  AttachmentStore  │ │
│  │                       ││                       ││  CleanupScheduler │ │
│  └───────────────────────┘└───────────────────────┘└───────────────────┘ │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                           System Layer（系统层）                           │
├───────────────────────────────────────────────────────────────────────────┤
│                                                                           │
│  ┌─────────────────────────────────┐  ┌─────────────────────────────────┐ │
│  │            Config               │  │            Logger                │ │
│  ├─────────────────────────────────┤  ├─────────────────────────────────┤ │
│  │  配置加载（YAML解析、环境变量）  │  │  日志写入（JSON结构化、滚动）    │ │
│  │  参数校验（必填、类型）         │  │  日志清理（滚动、过期删除）      │ │
│  │  默认配置生成                   │  │  日志级别控制                    │ │
│  │  配置热更新                     │  │  事件日志                        │ │
│  └─────────────────────────────────┘  └─────────────────────────────────┘ │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
```

### 2.2 模块职责总览

**Access Layer（接入层）**

| 组件 | 职责 |
|------|------|
| REST API (Hertz) | HTTP 接口暴露、请求解析、响应封装 |
| Auth Middleware | API Key 验证、权限检查（chat/cancel/status等） |
| SSE Stream Handler | 流式响应、事件推送（intent/step_start/progress/completed） |
| Attachment Handler | 上传校验（大小/类型/数量）、Base64 解码 |

**Intelligence Layer（智能层）**

| 组件 | 职责 |
|------|------|
| ReAct Agent Engine | Reasoning（LLM调用/决策）、Acting（Skill/MCP/直接回答）、Observation（结果处理）、循环终止控制 |
| Skills | Skills 加载、指令解析、注册给 Agent、热插拔管理、依赖解析 |
| MCP | 外部 MCP 加载、工具调用执行 |
| Memory | Session 管理（创建/查询）、History 管理（多轮对话上下文）、Chat Recorder（执行记录持久化）、Runtime State（活跃状态/进度/取消）、Attachment Store、Cleanup Scheduler |

**System Layer（系统层）**

| 组件 | 职责 |
|------|------|
| Config | 配置加载（YAML解析/环境变量）、参数校验、默认配置生成、配置热更新 |
| Logger | 日志写入（JSON结构化）、日志清理（滚动/过期删除）、日志级别控制、事件日志 |

### 2.3 依赖关系说明

**层间依赖：**
- Access Layer → Intelligence Layer：API 请求转发给 Agent 执行
- Intelligence Layer → System Layer：读取配置、写入日志

**智能层内部依赖：**
- Agent Engine → Skills：调用 Skills 执行任务
- Agent Engine → MCP：调用 MCP 工具执行操作
- Agent Engine → Memory：读取历史上下文、保存执行记录
- Memory → Runtime State：对话完成后持久化活跃状态

**工具注册机制：**

**Skills 注册给 Agent：**
- 启动时扫描 skills 目录，解析每个 SKILL.md
- 将 Skill 的 Instructions 作为工具描述注册给 Agent
- Skill 中的 Dependencies 在执行时递归加载

**MCP 工具注册给 Agent：**
- 外部 MCP 工具从配置文件加载并注册
- 每个工具包含：名称、描述、参数定义

**Agent 工具列表示例：**

| 工具类型 | 名称 | 描述 |
|---------|------|------|
| Skill | pdf_analyzer | 分析PDF文档并生成摘要 |
| Skill | code_generator | 根据需求生成代码 |
| MCP | file_read | 读取文件内容 |
| MCP | http_get | 发送HTTP GET请求 |

### 2.4 目录结构

默认工作目录：`~/.groot`，可通过命令行或环境变量更改。

```
{GROOT_HOME}/
├── config.yaml                    # 主配置文件
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

---

## 三、Access Layer（接入层）

### 3.1 API 设计

#### 3.1.1 API 列表

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

#### 3.1.2 POST /chat（核心接口）

**请求 Header：**

| Header | 必填 | 说明 |
|--------|------|------|
| `X-Session-ID` | 否 | 会话ID（sid），为空则创建新会话；有值但会话不存在则生成新sid |
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

**ID 生成规则：**

| ID 类型 | 格式 | 示例 |
|---------|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260418103000523_a1b2` |
| `chat_id` | `chat_{YYYYMMDDHHMMSSmmm}` | `chat_20260418103000523` |
| `step_id` | `{YYYYMMDD}-{HHMMSSmmm}-{random6}` | `20260418-103000000-a1b2c3` |

**说明：**
- `session_id`：会话唯一标识，毫秒级时间戳 + 4位随机字符
- `chat_id`：单次对话标识，固定前缀 `chat_` + 毫秒级时间戳
- `step_id`：步骤唯一标识，日期-时间-随机字符，用于 SSE 事件关联和存储记录

**响应 Header：**

| Header | 说明 |
|--------|------|
| `X-Session-ID` | 会话ID（新建或传入存在的） |
| `X-Chat-ID` | 本次对话ID |
| `Content-Type` | `text/event-stream` |
| `Cache-Control` | `no-cache` |
| `Connection` | `keep-alive` |

**SSE 响应事件格式：**

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

**finish_reason 流程详解：**

`finish_reason = "tool_calls"` 表示"对话暂停，AI 先去执行工具，回来继续回答"：

```
用户: "读取 /etc/hosts 文件"

AI: {"role":"assistant","reasoning_content":"用户要求读取文件..."}
AI: {"role":"assistant","tool_calls":[{"id":"call_abc",...}]}
AI: {"role":"assistant","finish_reason":"tool_calls"}  ← 暂停点
工具执行 file_read
工具: {"role":"tool","tool_call_id":"call_abc","content":"127.0.0.1 localhost..."}
AI: {"role":"assistant","reasoning_content":"文件已读取"}
AI: {"role":"assistant","content":"文件内容如下..."}
AI: {"role":"assistant","finish_reason":"stop"}  ← 对话结束
[DONE]
```

`finish_reason = "stop"` 表示"AI 说完了，对话直接结束"：

```
用户: "你好"

AI: {"role":"assistant","content":"你好！有什么可以帮助你的？"}
AI: {"role":"assistant","finish_reason":"stop"}  ← 直接结束
[DONE]
```

简单理解：
- **tool_calls** = 暂停一下，先执行工具，回来继续
- **stop** = 说完了，对话结束

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

**场景4：多工具调用：**
```
data: {"role":"assistant","tool_calls":[{"id":"call_001",...},{"id":"call_002",...}]}
data: {"role":"assistant","finish_reason":"tool_calls"}
data: {"role":"tool","tool_call_id":"call_001","tool_name":"file_read","content":"结果A"}
data: {"role":"tool","tool_call_id":"call_002","tool_name":"file_read","content":"结果B"}
data: {"role":"assistant","content":"两个文件已读取..."}
data: {"role":"assistant","finish_reason":"stop"}
data: [DONE]
```

**tool_calls 结构定义：**

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

**tool_result 结构定义：**

```json
{
  "role": "tool",
  "tool_call_id": "对应 tool_calls 中的 id",
  "tool_name": "工具名称",
  "content": "执行结果"
}
```

**错误处理：**

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

---

**完整处理流程：**

```
请求到达 → 请求校验 → 会话处理 → 返回响应 Header →
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
├─ 2. 会话处理
│   │
│   ├─ 提取 X-Session-ID（sid）
│   │
│   ├─ sid 为空 OR memory.ExistsSession(sid) = false（新建会话）
│   │   ├─ 生成 session_id（格式：{YYYYMMDDHHMMSSmmm}_{random4}）
│   │   ├─ memory.CreateSession(session_id)
│   │   ├─ isNew = true
│   │   ├─ round = 1
│   │   ├─ historyMessages = []（无历史）
│   │   └─ session_id = 新生成的ID
│   │
│   ├─ sid 有值 AND memory.ExistsSession(sid) = true（继续会话）
│   │   ├─ 检查该会话是否有对话正在执行
│   │   │   ├─ 有 → 返回 409：chat_limit_exceeded，终止
│   │   │   └─ 无 → 继续
│   │   ├─ isNew = false
│   │   ├─ historyMessages = memory.GetHistory(sid)
│   │   ├─ round = memory.GetRoundCount(sid) + 1
│   │   ├─ RuntimeState.IsRunning(sid) 检查
│   │   └─ session_id = sid
│   │
│   └─ 会话处理完成
│
├─ 3. 创建对话记录
│   ├─ 生成 chat_id（格式：chat_{YYYYMMDDHHMMSSmmm}）
│   ├─ 初始化对话状态为 running
│   ├─ 记录开始时间、调用方信息
│   ├─ RuntimeState.Register(session_id, chat_id)（注册活跃状态）
│   └─ 注册到取消管理器（用于取消功能）
│
├─ 4. 返回响应 Header
│   ├─ X-Session-ID: {session_id}
│   ├─ X-Chat-ID: {chat_id}
│   ├─ Content-Type: text/event-stream
│   ├─ Cache-Control: no-cache
│   ├─ Connection: keep-alive
│   └─ SSE 连接已建立，开始流式返回事件
│
├─ 5. 附件处理（如有附件）
│   ├─ 遍历每个附件：
│   │   ├─ Base64 解码
│   │   ├─ 文件名安全处理（替换 /、\、.. 等危险字符）
│   │   ├─ memory.SaveAttachment(session_id, filename, content)
│   │   │   └─ 保存到 memory/{session_id}/attachments/{filename}
│   │   ├─ 记录文件信息（路径、大小、类型）
│   │   └─ 同名文件会覆盖
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
│   ├─ 处理失败 → SSE 推送错误，终止
│   └─ 处理成功 → 继续
│
├─ 6. 构建 Agent 上下文
│   ├─ 系统提示词（prompt + Skills 指令）
│   ├─ 历史消息（historyMessages，继续会话时）
│   │   ├─ 每轮对话：instruction + result
│   │   ├─ 附件信息：attachments 文件名列表
│   │   └─ 构建为 schema.Message 格式
│   ├─ 当前用户消息（instruction + 附件路径信息）
│   ├─ 注册的工具列表（MCP 工具）
│   ├─ 执行限制配置（max_iterations、max_tokens 等）
│   └─ 附件路径：
│       ├─ memory/{session_id}/attachments/{filename}
│       └─ Agent 通过 MCP file_read 工具读取
│
├─ 7. Agent 执行（SSE 流式转发）
│   │
│   ├─ Agent 调用 eino 框架执行
│   │   ├─ 接收 AgentEvent 流
│   │   └─ 直接转发每个事件到 SSE 流
│   │
│   ├─ 事件处理（直接转发）
│   │   ├─ Assistant role + reasoning_content → 转发 thinking chunks
│   │   ├─ Assistant role + content → 转发 message chunks
│   │   ├─ Assistant role + tool_calls → 转发 tool_calls
│   │   ├─ Assistant role + finish_reason → 转发 finish 事件
│   │   ├─ Tool role → 转发 tool_result
│   │   └─ Agent 执行完成 → 发送 [DONE]
│   │
│   └─ 检查终止条件
│       ├─ finish_reason="stop" → 发送 [DONE]，结束
│       ├─ 达到最大循环次数 → 发送 [DONE]，终止
│       ├─ Token 消耗超限 → 发送 [DONE]，终止
│       ├─ 执行错误 → 发送 [DONE]，终止
│       ├─ 用户取消 → 发送 [DONE]，终止
│       └─ 继续执行
│
├─ 8. 对话完成处理
│   ├─ RuntimeState.Complete(session_id, result)
│   │   ├─ 移除活跃状态，返回 ChatRecord
│   │   └─ chatRecord 包含：status, duration, steps, error
│   │
│   ├─ memory.SaveChatRecord(session_id, chatRecord)
│   │   └─ 保存到 memory/{session_id}/chats/{chat_id}.json
│   │
│   ├─ memory.AppendMessage(session_id, message)
│   │   ├─ message.Round = round
│   │   ├─ message.Timestamp = 结束时间
│   │   ├─ message.Instruction = instruction
│   │   ├─ message.Attachments = [附件文件名列表]
│   │   ├─ message.Result = 执行结果
│   │   ├─ message.ResultAttachments = []（如有生成文件）
│   │   ├─ message.Status = success/failed/cancelled
│   │   ├─ message.Duration = 耗时
│   │   ├─ message.StepsCount = 步骤数
│   │   └─ 更新 history.json
│   │
│   ├─ 从取消管理器注销
│   └─ 关闭 SSE 连接
│
→ 流程结束
```

> **实现提示：** Agent 执行阶段直接转发 eino 框架返回的 AgentEvent，无需额外转换逻辑。eino 的 MessageStream chunks 和 MessageOutput 结构与上述 SSE 格式一致，代码仅需：
> 1. 流式 chunks 直接写入 SSE `data:`
> 2. Tool role 事件直接写入 SSE `data:`
> 3. 流结束时写入 `data: [DONE]`

**会话处理逻辑总结：**

| 条件 | sid | 会话存在 | 处理方式 | isNew | round | historyMessages |
|------|-----|---------|---------|-------|-------|-----------------|
| 新会话 | 空 | - | 生成新 sid 并创建 | true | 1 | [] |
| 会话不存在 | 有值 | false | 生成新 sid 并创建 | true | 1 | [] |
| 继续会话 | 有值 | true | 使用传入 sid，检查并发 | false | count+1 | 从 memory 读取 |

**intent 事件含义：**

`intent` 事件是 SSE 流的**首个事件**，表示"准备工作全部完成，Agent 开始执行"。准备工作包括：
- 请求校验
- 会话处理（创建或获取）
- 对话记录创建
- 附件处理（如有）

只有准备工作全部成功后，才推送 `intent` 事件。如果准备工作失败（如附件解码失败），直接返回错误响应并终止，不会建立 SSE 连接。

**附件存储目录结构：**

```
{GROOT_HOME}/memory/
├── 20260418103000523_a1b2/     # 会话A
│   ├── history.json                 # 对话历史
│   └── attachments/                 # 附件目录
│       ├── report.pdf               # 第1轮上传
│       ├── data.csv                 # 第1轮上传
│       ├── data.csv                 # 第3轮上传（覆盖第1轮）
│       └── chart.png                # 第3轮上传
├── 20260418103500123_b2c3/     # 会话B
│   ├── history.json
│   └── attachments/
│       └── config.json
└── ...
```

**特点：**
- 附件保存在会话目录下的 `attachments/` 子目录
- 保留原始文件名，同名文件会覆盖
- 附件随会话清理而删除（memory 清理任务）

**附件路径传递方式：**

附件信息以结构化文本形式嵌入用户消息，Agent 解析后调用 MCP `file_read` 工具读取：

```
用户指令内容

附件:
- report.pdf (file)
  路径: /home/groot/memory/20260418103000523_a1b2/attachments/report.pdf
  类型: application/pdf
  大小: 1024000 bytes
- data.csv (file)
  路径: /home/groot/memory/20260418103000523_a1b2/attachments/data.csv
  类型: text/csv
  大小: 512000 bytes
```

**历史消息传递方式：**

继续会话时，历史消息构建为 schema.Message 格式传递给 Agent：

```
历史构建逻辑：
  1. 遍历 historyMessages
  2. 每轮对话构建两条消息：
     - UserMessage：instruction + 附件文件名列表
     - AssistantMessage：result
  3. 添加当前用户消息
  4. 传递给 Agent 的 messages 数组

示例消息结构：
  [
    UserMessage("帮我分析数据\n\n附件:\n- data.csv"),
    AssistantMessage("分析结果如下..."),
    UserMessage("再画个图表"),
    AssistantMessage("图表已生成..."),
    UserMessage("继续分析")  // 当前指令
  ]
```

**关键节点说明：**

| 步骤 | 说明 | 失败处理 |
|------|------|---------|
| 请求校验 | 验证参数合法性 | 返回 400，不创建会话 |
| 会话处理 | 判断新建或继续会话，检查并发 | 返回 409（并发冲突）或继续 |
| RuntimeState.Register | 注册活跃对话状态 | - |
| 附件处理 | 解码并存储到 memory 目录 | SSE 推送 error，终止 |
| SSE started | 对话执行开始（整体开始信号） | - |
| SSE thinking_start | 思考阶段开始（可选） | - |
| SSE thinking | 流式输出思考内容 | 推送 error 事件，终止 |
| SSE thinking_end | 思考阶段结束 | - |
| SSE tool_call | 工具调用请求 | - |
| SSE tool_result | 工具执行结果 | 推送 error 事件，终止 |
| SSE message_start | 开始最终输出 | - |
| SSE message | 流式输出最终回答 | 推送 error 事件，终止 |
| SSE message_end | 最终输出结束 | - |
| RuntimeState.Complete | 移除活跃状态，生成执行记录 | - |
| Memory.SaveChatRecord | 保存详细执行记录 | 日志记录错误，不影响响应 |
| Memory.AppendMessage | 更新 history.json | 日志记录错误，不影响响应 |

---

**API 详细示例：**

**POST /chat 请求示例：**

**基本请求（新会话）：**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: groot-api-key-2026abc

{
  "instruction": "帮我分析这份PDF财务报告",
  "attachments": [
    {"type": "file", "name": "Q3_Report.pdf", "content": "base64..."}
  ]
}
```

**带 prompt 的请求：**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: groot-api-key-2026abc

{
  "instruction": "帮我分析这份PDF财务报告",
  "prompt": "你是一个财务分析师，重点关注利润增长率和潜在风险点。输出JSON格式。",
  "attachments": [
    {"type": "file", "name": "Q3_Report.pdf", "content": "base64..."}
  ]
}
```

**多附件请求：**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: groot-api-key-2026abc

{
  "instruction": "对比分析这份PDF报告和销售数据",
  "attachments": [
    {"type": "file", "name": "report.pdf", "content": "base64..."},
    {"type": "file", "name": "sales.csv", "content": "base64..."}
  ]
}
```

**无附件请求（纯 LLM 执行）：**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: groot-api-key-2026abc

{
  "instruction": "帮我写一个 Python 快速排序函数"
}
```

**继续会话请求：**

```http
POST /chat HTTP/1.1
Host: localhost:8080
X-Session-ID: 20260418103000523_a1b2
Content-Type: application/json
X-API-Key: groot-api-key-2026abc

{
  "instruction": "根据刚才的分析，生成一份总结报告"
}
```

**SSE 响应事件流示例：**

**场景1：新会话执行（带 thinking 和工具调用）：**

```
HTTP Header: 
  X-Session-ID: 20260418103000523_a1b2
  X-Chat-ID: chat_20260418103000523

event: started
data: {"session_id":"20260418103000523_a1b2","chat_id":"chat_20260418103000523","timestamp":"2026-04-18T10:30:00Z"}

event: thinking_start
data: {"step_id":"20260418-103000000-a1b2c3","timestamp":"2026-04-18T10:30:00Z"}

event: thinking
data: {"content":"我需要先读取PDF文件...","timestamp":"2026-04-18T10:30:01Z"}

event: thinking
data: {"content":"使用 file_read 工具...","timestamp":"2026-04-18T10:30:02Z"}

event: thinking_end
data: {"step_id":"20260418-103000000-a1b2c3","status":"success","timestamp":"2026-04-18T10:30:03Z"}

event: tool_call
data: {"step_id":"20260418-103005000-x9y8z7","name":"file_read","arguments":{"path":"memory/20260418103000523_a1b2/attachments/report.pdf"},"timestamp":"2026-04-18T10:30:03Z"}

event: tool_result
data: {"step_id":"20260418-103005000-x9y8z7","output":"PDF内容：财务报告2026Q3...","timestamp":"2026-04-18T10:30:05Z"}

event: thinking_start
data: {"step_id":"20260418-103010000-b3c4d5","timestamp":"2026-04-18T10:30:05Z"}

event: thinking
data: {"content":"根据PDF内容，我需要提取关键财务指标...","timestamp":"2026-04-18T10:30:06Z"}

event: thinking_end
data: {"step_id":"20260418-103010000-b3c4d5","status":"success","timestamp":"2026-04-18T10:30:10Z"}

event: message_start
data: {"timestamp":"2026-04-18T10:30:10Z"}

event: message
data: {"content":"财务报告分析结果：","timestamp":"2026-04-18T10:30:11Z"}

event: message
data: {"content":"\n\n1. 营业收入：增长15%","timestamp":"2026-04-18T10:30:12Z"}

event: message
data: {"content":"\n2. 净利润：增长20%","timestamp":"2026-04-18T10:30:13Z"}

event: message
data: {"content":"\n3. 关键风险：市场竞争加剧","timestamp":"2026-04-18T10:30:14Z"}

event: message_end
data: {"timestamp":"2026-04-18T10:30:15Z"}

event: completed
data: {"status":"success","timestamp":"2026-04-18T10:30:15Z","duration":"15s","round":1,"chat_id":"chat_20260418103000523","result":"财务报告分析结果..."}
```

**场景2：继续会话（带 thinking，无工具调用）：**

```
HTTP Header:
  X-Session-ID: 20260418103000523_a1b2
  X-Chat-ID: chat_20260418103500123

event: started
data: {"session_id":"20260418103000523_a1b2","chat_id":"chat_20260418103500123","timestamp":"2026-04-18T10:35:00Z"}

event: thinking_start
data: {"step_id":"20260418-103500000-d4e5f6","timestamp":"2026-04-18T10:35:00Z"}

event: thinking
data: {"content":"根据历史分析...","timestamp":"2026-04-18T10:35:01Z"}

event: thinking_end
data: {"step_id":"20260418-103500000-d4e5f6","status":"success","timestamp":"2026-04-18T10:35:02Z"}

event: message_start
data: {"timestamp":"2026-04-18T10:35:02Z"}

event: message
data: {"content":"根据之前的分析，我建议...","timestamp":"2026-04-18T10:35:03Z"}

event: message
data: {"content":"关注以下几个要点...","timestamp":"2026-04-18T10:35:04Z"}

event: message_end
data: {"timestamp":"2026-04-18T10:35:30Z"}

event: completed
data: {"status":"success","timestamp":"2026-04-18T10:35:30Z","duration":"30s","round":2,"chat_id":"chat_20260418103500123","result":"根据之前的分析..."}
```

**场景3：纯 LLM 回答（无 thinking，无工具）：**

```
HTTP Header:
  X-Session-ID: 20260418103000523_a1b2
  X-Chat-ID: chat_20260418104000123

event: started
data: {"session_id":"20260418103000523_a1b2","chat_id":"chat_20260418104000123","timestamp":"2026-04-18T10:40:00Z"}

event: message_start
data: {"timestamp":"2026-04-18T10:40:00Z"}

event: message
data: {"content":"好的，我来帮你...","timestamp":"2026-04-18T10:40:01Z"}

event: message
data: {"content":"这是一个简单的问题...","timestamp":"2026-04-18T10:40:02Z"}

event: message_end
data: {"timestamp":"2026-04-18T10:40:10Z"}

event: completed
data: {"status":"success","timestamp":"2026-04-18T10:40:10Z","duration":"10s","round":3,"chat_id":"chat_20260418104000123","result":"好的，我来帮你..."}
```

**场景4：工具调用失败：**

```
HTTP Header:
  X-Session-ID: 20260418103000523_a1b2
  X-Chat-ID: chat_20260418103000523

event: started
data: {"session_id":"20260418103000523_a1b2","chat_id":"chat_20260418103000523","timestamp":"2026-04-18T10:30:00Z"}

event: thinking_start
data: {"step_id":"20260418-103000000-a1b2c3","timestamp":"2026-04-18T10:30:00Z"}

event: thinking
data: {"content":"我需要读取PDF文件...","timestamp":"2026-04-18T10:30:01Z"}

event: thinking_end
data: {"step_id":"20260418-103000000-a1b2c3","status":"success","timestamp":"2026-04-18T10:30:02Z"}

event: tool_call
data: {"step_id":"20260418-103005000-x9y8z7","name":"file_read","arguments":{"path":"memory/.../report.pdf"},"timestamp":"2026-04-18T10:30:02Z"}

event: tool_result
data: {"step_id":"20260418-103005000-x9y8z7","error":"PDF文件已损坏","timestamp":"2026-04-18T10:30:05Z"}

event: completed
data: {"status":"failed","timestamp":"2026-04-18T10:30:05Z","duration":"5s","round":1,"chat_id":"chat_20260418103000523","error":{"code":"execution_error","message":"PDF文件已损坏"}}
```

**场景5：用户取消执行：**

```
HTTP Header:
  X-Session-ID: 20260418103000523_a1b2
  X-Chat-ID: chat_20260418103000523

event: started
data: {"session_id":"20260418103000523_a1b2","chat_id":"chat_20260418103000523","timestamp":"2026-04-18T10:30:00Z"}

event: thinking_start
data: {"step_id":"20260418-103000000-a1b2c3","timestamp":"2026-04-18T10:30:00Z"}

event: thinking
data: {"content":"正在处理...","timestamp":"2026-04-18T10:30:01Z"}

event: thinking
data: {"content":"调用工具...","timestamp":"2026-04-18T10:30:10Z"}

（用户发送 DELETE /chat/20260418103000523_a1b2）

event: completed
data: {"status":"cancelled","timestamp":"2026-04-18T10:30:12Z","duration":"12s","round":1,"chat_id":"chat_20260418103000523","message":"用户主动取消"}
```

#### 3.1.3 DELETE /chat/{sid}

取消指定会话中正在执行的对话。

**响应：**

成功：
```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "chat_id": "chat_20260418103000523",
  "message": "对话已取消"
}
```

失败：
```json
{
  "status": "no_running_chat",
  "session_id": "20260418103000523_a1b2",
  "message": "该会话当前没有正在执行的对话"
}
```

#### 3.1.4 GET /chat/status/{sid}

查询最近一次对话的运行状态。

**响应：**

有正在执行的对话：
```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "chat": {
    "chat_id": "chat_20260418103000523",
    "round": 4,
    "status": "running",
    "progress": {
      "current_step": 2,
      "steps_completed": 1,
      "percentage": 50
    },
    "started_at": "2026-04-18T10:30:00Z",
    "elapsed_time": "15s"
  }
}
```

无正在执行的对话：
```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "chat": null
}
```

#### 3.1.5 GET /chat/{sid}

查询最近一次对话的完整步骤记录。

**响应：**
```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "chat": {
    "chat_id": "chat_20260418103000523",
    "round": 4,
    "instruction": "用户指令内容",
    "attachments": ["data.csv"],
    "result": {"summary": "执行结果..."},
    "status": "completed",
    "started_at": "2026-04-18T10:30:00Z",
    "ended_at": "2026-04-18T10:30:45Z",
    "duration": 45,
    "steps": [
      {
        "step_id": "20260418-103000000-a1b2c3",
        "type": "skill",
        "name": "pdf_analyzer",
        "start_time": "2026-04-18T10:30:00Z",
        "end_time": "2026-04-18T10:30:30Z",
        "status": "success",
        "nesting_level": 0
      }
    ]
  }
}
```

#### 3.1.6 GET /sess/{sid}

查询会话详情（完整对话历史，所有轮次）。

**响应：**
```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "session": {
    "created_at": "2026-04-18T10:00:00Z",
    "round_count": 4,
    "path": "/home/groot/memory/20260418103000523_a1b2"
  },
  "history": {
    "messages": [
      {
        "round": 1,
        "timestamp": "2026-04-18T10:00:00Z",
        "instruction": "帮我分析这个数据文件",
        "attachments": ["data.csv"],
        "result": "好的，分析结果如下...",
        "result_attachments": []
      }
    ]
  }
}
```

#### 3.1.7 GET /sess/history

查询会话列表。

**Query 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `limit` | int | 否 | 返回数量，默认 20，最大 100 |
| `offset` | int | 否 | 分页偏移，默认 0 |

**响应：**
```json
{
  "status": "success",
  "total": 50,
  "limit": 10,
  "offset": 0,
  "sessions": [
    {
      "session_id": "20260418103000523_a1b2",
      "created_at": "2026-04-18T10:00:00Z",
      "round_count": 4,
      "last_active_at": "2026-04-18T10:30:00Z"
    }
  ]
}
```

#### 3.1.8 GET /health

健康检查接口。

**响应：**
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
    "chats_running": 5,
    "success_rate": 0.98
  }
}
```

#### 3.1.9 GET /skills

列出可用 Skills。

**响应：**
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

#### 3.1.10 GET /tools

列出可用 MCP 工具。

**响应：**
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

### 3.2 Attachment Handler

附件处理器负责用户上传附件的校验、解码和存储。

#### 3.2.1 附件配置

```yaml
attachment:
  max_size: 50                     # 单个附件最大大小（MB）
  max_total_size: 100              # 附件总大小上限（MB）
  max_count: 10                    # 附件数量上限
  allowed_types: [pdf, doc, docx, txt, json, csv, xml, yaml, png, jpg, jpeg, zip]  # 允许的附件类型
```

**配置字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `max_size` | int | 否 | 单个附件最大大小（MB），默认 50 |
| `max_total_size` | int | 否 | 所有附件总大小上限（MB），默认 100 |
| `max_count` | int | 否 | 单次请求附件数量上限，默认 10 |
| `allowed_types` | array | 否 | 允许的文件扩展名列表，默认常见文档和图片类型 |

#### 3.2.2 校验流程

```
收到附件 → 逐个校验 →
│
├─ 1. 数量校验
│     attachments.length > max_count → 返回 400：附件数量超限
│
├─ 2. 类型校验
│     提取文件扩展名（如 .pdf）
│     扩展名不在 allowed_types 中 → 返回 400：附件类型不允许
│
├─ 3. 大小校验
│     计算 Base64 解码后预估大小（len(content) * 3 / 4）
│     单个大小 > max_size → 返回 400：附件大小超限
│     总大小 > max_total_size → 返回 400：附件总大小超限
│
└─ 校验通过 → 继续处理
```

#### 3.2.3 处理流程

```
校验通过 → 遍历附件 →
│
├─ 文件类型（type=file/image）
│   ├─ Base64 解码 content 字段
│   ├─ 文件名安全处理（替换 /、\、.. 等危险字符）
│   ├─ 保存到 memory/{session_id}/attachments/{filename}
│   ├─ 返回完整路径供 Agent 读取
│   └─ 同名文件会覆盖
│
├─ URL 类型（type=url）
│   ├─ 记录 URL 地址
│   └─ Agent 需自行调用 http_get 工具获取内容
│
└─ 处理完成 → 构建附件信息文本 → 添加到用户消息
```

**文件名安全处理规则：**

| 原字符 | 替换为 |
|--------|--------|
| `/` | `_` |
| `\` | `_` |
| `..` | `_` |

**处理后的附件信息格式：**

```
附件:
- report.pdf (file)
  路径: /home/groot/memory/20260418103000523_a1b2/attachments/report.pdf
  类型: application/pdf
  大小: 1024000 bytes
```

#### 3.2.4 错误处理

| 错误码 | HTTP 状态 | 说明 |
|--------|-----------|------|
| `attachment_count_exceeded` | 400 | 附件数量超过 max_count |
| `attachment_type_not_allowed` | 400 | 文件类型不在 allowed_types 中 |
| `attachment_size_exceeded` | 400 | 单个附件超过 max_size |
| `attachment_total_size_exceeded` | 400 | 总大小超过 max_total_size |
| `attachment_decode_error` | 400 | Base64 解码失败 |

### 3.3 安全设计

#### 3.3.1 认证机制（API Key）

**认证配置：**

```yaml
security:
  auth:
    enabled: true               # 是否开启认证
    type: api_key               # 认证类型
    api_key:
      header_name: X-API-Key    # 认证 Header 名称
      keys:
        - name: default         # Key 名称（唯一标识）
          key: ${GROOT_API_KEY} # Key 值（支持环境变量）
          permissions: all      # 权限范围
```

**认证流程：**

```
请求到达 → Auth 中间件拦截 →
│
├─ enabled=false → 跳过认证，直接处理请求
│
├─ enabled=true → 执行认证
│   ├─ 提取 Header 中的 API Key
│   ├─ 检查 Key 是否在 keys 列表中
│   │   ├─ 不存在 → 返回 401 Unauthorized
│   │   └─ 存在 → 继续检查权限
│   ├─ 检查 permissions 是否包含该 API 所需权限
│   │   ├─ 不包含 → 返回 403 Forbidden
│   │   └─ 包含 → 认证通过
│   └─ 认证通过 → 记录调用方 name → 继续处理请求
│
└─ 处理请求
```

**认证失败响应：**

401 Unauthorized：
```json
{"status": "unauthorized", "message": "API Key 无效或缺失"}
```

403 Forbidden：
```json
{"status": "forbidden", "message": "权限不足，无法访问该 API"}
```

#### 3.3.2 权限定义

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

**多 Key 配置示例：**

```yaml
security:
  auth:
    enabled: true
    type: api_key
    api_key:
      header_name: X-API-Key
      keys:
        - name: internal_system
          key: ${GROOT_INTERNAL_KEY}
          permissions: all
        - name: external_partner
          key: partner-key-2026
          permissions: [chat, status]
        - name: monitor_service
          key: ${GROOT_MONITOR_KEY}
          permissions: [status, health, skills, tools]
```

---

## 四、Intelligence Layer（智能层）

### 4.1 Agent Engine

#### 4.1.1 ReAct 执行模式

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
      │   ├─ MCP 工具调用 → MCP 执行
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

**ReAct 执行循环（详细版）：**

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
│   │   ├─ MCP 工具调用 → MCP 执行
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

#### 4.1.2 循环终止条件

| 条件 | 说明 | SSE 事件 |
|------|------|---------|
| Agent 判断完成 | LLM 输出最终答案 | `completed` (success) |
| 达到最大循环次数 | iteration > max_iterations | `completed` (failed) |
| Token 消耗超限 | tokens_used > max_tokens | `completed` (failed) |
| 单步执行超时 | step_duration > step_timeout | `completed` (failed) |
| 用户取消 | DELETE /chat/{sid} | `completed` (cancelled) |

#### 4.1.3 取消机制

```
DELETE /chat/{sid} →
│
├─ 根据 session_id 查找执行状态
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
├─ RuntimeState.Complete(session_id)
│
└─ 关闭 SSE 连接
```

### 4.2 Skills

#### 4.2.1 Skill 定义格式

遵循 Claude Code 官方标准（YAML frontmatter + Markdown）。

**SKILL.md 结构：**

```markdown
---
name: skill_name
description: "技能描述，用于 Agent 工具列表展示"
dependencies: [other_skill]  # 可选，依赖的其他 Skill
---

# Skill 标题

技能的详细指令和说明内容...
```

**Frontmatter 字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | Skill 名称（全局唯一） |
| `description` | string | 是 | Skill 描述，用于 Agent 工具列表 |
| `dependencies` | array | 否 | 依赖的其他 Skill 名称列表 |

#### 4.2.2 目录结构

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

#### 4.2.3 加载与注册

**注册流程：**

```
程序启动 → 扫描 skills 目录 → 解析每个 SKILL.md →
提取 frontmatter 中的 name/description →
解析 Markdown 正文内容 → 注册到内存索引
```

当 Skill 声明了 dependencies，Agent 在执行时会自动识别并递归调用依赖的子 Skills。

#### 4.2.4 热插拔机制

支持运行时动态添加、修改、删除 Skills，无需重启服务。

**监听机制：**
- 使用 `fsnotify` 监听 skills 目录变化
- 只监听 `SKILL.md` 文件的创建、修改、删除事件
- 防抖机制：检测到变化后延迟 2秒再执行加载

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

### 4.3 MCP

#### 4.3.1 MCP 配置文件格式

每个 MCP 一个独立的 JSON 文件。

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
  "description": "网页解析 MCP 服务",
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

#### 4.3.2 连接类型

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `stdio` | 标准输入输出通信 | 本地命令行工具 |
| `sse` | Server-Sent Events | 远程 HTTP 服务（单向推送） |
| `streamable_http` | Streamable HTTP | 远程 HTTP 服务（双向流式） |

#### 4.3.3 工具发现机制

MCP 工具支持自动发现，无需手动配置工具列表。

**发现流程：**

```
加载 MCP 配置 → 
│
├─ 配置中有 tools 字段 → 直接使用配置的工具列表
│
└─ 配置中无 tools 字段 → 连接 MCP Server → 调用 tools/list → 自动注册发现的工具
```

**tools/list 协议：**

| 连接类型 | 发现方式 |
|---------|---------|
| `stdio` | JSON-RPC: `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` |
| `sse` | HTTP POST to baseUrl, 解析 SSE data |
| `streamable_http` | HTTP POST to `{baseUrl}/tools/list` |

**返回格式：**

```json
{
  "result": {
    "tools": [
      {"name": "read_file", "description": "读取文件内容"},
      {"name": "write_file", "description": "写入文件内容"}
    ]
  }
}
```

**配置示例（自动发现）：**

```json
{
  "name": "filesystem",
  "type": "stdio",
  "description": "文件系统操作",
  "isActive": true,
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed"]
}
```

无需指定 `tools` 字段，系统自动发现。

**配置示例（手动指定）：**

```json
{
  "name": "custom_mcp",
  "type": "stdio",
  "description": "自定义 MCP",
  "isActive": true,
  "command": "my-mcp-server",
  "tools": [
    {"name": "custom_tool", "description": "自定义工具"}
  ]
}
```

手动指定 `tools` 字段时，跳过自动发现。

#### 4.3.4 热插拔机制

**监听机制：**
- 使用 `fsnotify` 监听 mcp 目录变化
- 只监听 `.json` 文件的创建、修改、删除事件
- 防抖机制：检测到变化后延迟 2秒再执行加载

**处理流程：**

```
文件变化检测 → 防抖等待（2秒） →
│
├─ 新增 .json → 解析配置 → 工具发现（tools/list） → 注册 MCP → 输出日志
│
├─ 修改 .json → 重新解析 → 工具发现 → 断开旧连接 → 注册新配置 → 输出日志
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

### 4.4 Memory

#### 4.4.1 数据结构

**history.json 格式：**

```json
{
  "session_id": "20260418100000523_abc123",
  "created_at": "2026-04-18T10:00:00Z",
  "messages": [
    {
      "round": 1,
      "chat_id": "chat_20260418100000523",
      "timestamp": "2026-04-18T10:00:00Z",
      "instruction": "用户指令内容",
      "attachments": ["data.csv"],
      "result": "助手回复内容",
      "result_attachments": [],
      "status": "completed",
      "duration": 45,
      "steps_count": 3,
      "error": null
    }
  ]
}
```

**chats/{chat_id}.json 格式：**

```json
{
  "chat_id": "chat_20260418100000523",
  "session_id": "20260418100000523_abc123",
  "round": 1,
  "timestamp": "2026-04-18T10:00:00Z",
  "instruction": "帮我分析这份PDF报告",
  "attachments": ["report.pdf"],
  "result": "分析结果如下...",
  "result_attachments": [],
  "status": "completed",
  "duration": 45,
  "caller": "internal_system",
  "steps": [
    {
      "step_id": "20260418-100005000-a1b2c3",
      "type": "skill",
      "name": "pdf_analyzer",
      "start_time": "2026-04-18T10:00:05Z",
      "end_time": "2026-04-18T10:00:30Z",
      "status": "success",
      "nesting_level": 0
    }
  ],
  "error": null
}
```

#### 4.4.2 会话管理

**目录结构：**

```
memory/
├── 20260418100000523_abc123/
│   ├── history.json
│   ├── attachments/
│   │   ├── data.csv
│   │   └── report.pdf
│   └── chats/
│       ├── chat_20260418100000523.json
│       └── chat_20260418100500123.json
└── 20260418100500123_def456/
    ├── history.json
    └── ...
```

**核心能力：**

| 能力 | 说明 |
|------|------|
| CreateSession | 创建会话目录和 history.json |
| ExistsSession | 检查会话是否存在 |
| GetSessionInfo | 获取会话信息 |
| ListSessions | 查询会话列表 |

#### 4.4.3 历史持久化

| 能力 | 说明 |
|------|------|
| AppendMessage | 向会话追加新一轮对话记录 |
| GetHistory | 读取指定会话的历史消息列表 |
| GetRoundCount | 获取对话轮数 |
| SaveChatRecord | 保存详细执行记录到 chats/{chat_id}.json |
| GetChatRecord | 获取单次对话详情 |

#### 4.4.4 附件存储

| 能力 | 说明 |
|------|------|
| SaveAttachment | 保存附件到会话目录（返回完整路径） |
| GetAttachmentPath | 获取附件完整路径 |

**附件命名规则：**
- 保留原始文件名，不添加前缀
- 同名文件会覆盖
- 文件名记录在 history.json 的 `attachments` 字段

#### 4.4.5 定时清理

**配置项：**

```yaml
memory:
  directory: memory               # 记忆目录
  retention_days: 7               # 会话保留天数
  cleanup_schedule: "02:00"       # 清理时间（HH:MM）
```

**清理逻辑：**

```
触发：每天 cleanup_schedule 时间
流程：
  1. 遍历 memory 目录下所有子目录
  2. 获取每个目录的创建时间
  3. 计算年龄 = 当前时间 - 创建时间
  4. 如果年龄 >= retention_days * 24小时：
     - 删除整个会话目录（history.json + attachments）
     - 删除成功后记录日志：[INFO] [memory] 清理会话 {sessionID}，创建时间：{createdAt}，轮数：{roundCount}
     - 删除失败记录日志：[ERROR] [memory] 清理会话 {sessionID} 失败：{error}
  5. 汇总日志：[INFO] [memory] 清理完成，删除 {count} 个会话，剩余 {remain} 个
```

#### 4.4.6 附件处理流程

```
用户上传附件 → attachment 模块校验（大小、类型、数量）
            → 校验通过后，memory 模块保存到 memory/{sessionID}/attachments/{filename}
            → Agent 执行时从 memory 目录读取附件
            → 执行完成后，RuntimeState 调用 Memory.SaveChatRecord 持久化
```

**说明：**
- 附件直接保存到会话目录，不经过临时目录
- 同名附件会覆盖，保证文件名一致性
- 对话失败时附件已存在，下次可重新上传覆盖，最终随会话清理删除

#### 4.4.7 启动与停止流程

**Groot 启动时：**

```
启动流程：
  1. 解析 memory.directory 配置
     - 清理路径（去掉 ./ 前缀）
     - 绝对路径：直接使用
     - 相对路径：拼接 homeDir
  2. 确保 memory 目录存在，不存在则创建
  3. 初始化 Memory
  4. 启动清理调度器（注册定时任务）
```

**Groot 停止时：**

```
停止流程：
  1. 停止清理调度器
  2. 等待当前清理任务完成（如有）
  3. 释放资源
```

#### 4.4.8 目录路径解析

```go
// resolveMemoryDir 解析记忆目录路径
func resolveMemoryDir(memoryDir string, homeDir string) string {
    // 清理路径（处理 "./memory" -> "memory"）
    memoryDir = filepath.Clean(memoryDir)
    
    // 绝对路径：直接使用
    if filepath.IsAbs(memoryDir) {
        return memoryDir
    }
    
    // 相对路径：拼接 homeDir
    return filepath.Join(homeDir, memoryDir)
}
```

**目录路径解析规则：**

| 配置值 | homeDir | 解析结果 |
|--------|---------|---------|
| `/data/groot/memory` | `/home/groot` | `/data/groot/memory` |
| `memory` | `/home/groot` | `/home/groot/memory` |
| `./memory` | `/home/groot` | `/home/groot/memory` |

#### 4.4.9 清理日志

清理操作使用系统统一日志记录，级别 INFO：

```log
2026-04-18 02:00:01 [INFO] [memory] 开始清理，保留天数: 7，当前会话数: 15
2026-04-18 02:00:02 [INFO] [memory] 清理会话 20260410T100000_abc123，创建时间: 2026-04-10，轮数: 5
2026-04-18 02:00:03 [INFO] [memory] 清理会话 20260409T093000_def456，创建时间: 2026-04-09，轮数: 3
2026-04-18 02:00:05 [INFO] [memory] 清理完成，删除 2 个会话，剩余 13 个
```

#### 4.4.10 错误处理

| 场景 | 处理 |
|------|------|
| 会话不存在 | 返回错误，调用方需先 CreateSession |
| history.json 不存在 | 返回错误，会话目录可能损坏 |
| history.json 解析失败 | 记录 ERROR 日志，返回错误 |
| 附件写入失败 | 记录 ERROR 日志，返回错误 |
| 清理时目录读取失败 | 记录 ERROR 日志，跳过该目录继续清理 |

#### 4.4.11 实现文件

| 文件 | 说明 |
|------|------|
| `internal/memory/types.go` | 数据结构定义（SessionInfo, Message, History） |
| `internal/memory/memory.go` | Memory 接口定义 |
| `internal/memory/manager.go` | Manager 实现类，核心业务逻辑 |
| `internal/memory/cleanup.go` | 定时清理调度器 |
| `internal/config/config.go` | 添加 MemoryConfig 配置项 |

### 4.5 Runtime State

#### 4.5.1 数据结构

```go
// ActiveChat 活跃对话状态（内存中）
type ActiveChat struct {
    SessionID  string        `json:"session_id"`
    ChatID     string        `json:"chat_id"`
    Status     string        `json:"status"`      // running
    Progress   *ChatProgress `json:"progress"`
    StartTime  time.Time     `json:"start_time"`
    CancelCh   chan struct{} `json:"-"`           // 取消信号通道
}

// ChatProgress 对话进度
type ChatProgress struct {
    CurrentStep    int `json:"current_step"`
    StepsCompleted int `json:"steps_completed"`
    Percentage     int `json:"percentage"`
}
```

#### 4.5.2 接口定义

```go
type RuntimeStateManager interface {
    Register(sessionID, chatID string) (*ActiveChat, error)
    Get(sessionID string) (*ActiveChat, bool)
    UpdateProgress(sessionID string, progress *ChatProgress) error
    Cancel(sessionID string) error
    Complete(sessionID string, result *ChatResult) (*ChatRecord, error)
    IsRunning(sessionID string) bool
    RunningCount() int
}
```

#### 4.5.3 并发控制

**同一会话并发限制：**

同一会话只能有一个活跃对话，防止执行冲突。

```
POST /chat (sid=xxx):
  │
  ├─ RuntimeState.IsRunning(sid)
  │     ├─ true → 返回 409 Conflict
  │     └─ false → 继续
  │
  ├─ RuntimeState.Register(sid, chat_id)
  │
  └─ 执行...
```

#### 4.5.4 与 Memory 协作

```
对话生命周期：
  │
  ├─ 1. POST /chat 请求
  │     ├─ RuntimeState.Register(session_id, chat_id)
  │     └─ 开始执行
  │
  ├─ 2. 执行过程
  │     ├─ RuntimeState.UpdateProgress() 更新进度
  │     └─ DELETE /chat/{sid} → RuntimeState.Cancel()
  │
  ├─ 3. 执行完成
  │     ├─ RuntimeState.Complete() 返回 ChatRecord
  │     ├─ Memory.AppendMessage(ChatRecord)
  │     └─ RuntimeState 移除活跃状态
  │
  └─ 4. 查询历史
        └─ GET /sess/{sid} → Memory.GetHistory()
```

---

## 五、System Layer（系统层）

### 5.1 Config

#### 5.1.1 配置优先级

| 配置项 | 来源 |
|------|------|
| 工作目录 | 命令行 `-H` > 环境变量 `GROOT_HOME` > 默认 `~/.groot` |
| HTTP 端口 | 命令行 `-p` > 配置文件 |
| 其他配置 | 配置文件 `config.yaml` |

#### 5.1.2 配置热更新

**支持热更新的配置：**
- Skills 配置（添加/修改/删除 SKILL.md）
- MCP 配置（添加/修改/删除 .json 文件）

**不支持热更新的配置：**
- LLM 配置（需重启服务）
- Server 配置（需重启服务）
- Security 配置（需重启服务）
- Logging 配置（需重启服务）
- Memory 配置（需重启服务）
- Performance 配置（需重启服务）
- Attachment 配置（需重启服务）

### 5.2 Logger

#### 5.2.1 日志配置

```yaml
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

**配置字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `level` | string | 否 | 日志级别，默认 `info` |
| `format` | string | 否 | 输出格式，`json` 或 `text`，默认 `json` |
| `output` | array | 否 | 输出目标列表，默认 `[stdout, file]` |
| `file.directory` | string | 否 | 日志目录，默认 `logs` |
| `file.filename_pattern` | string | 否 | 文件名模式，默认 `groot-{date}.log` |
| `file.max_age` | int | 否 | 保留天数，默认 7 |
| `file.max_size` | int | 否 | 单文件最大 MB，默认 100 |
| `file.compress` | bool | 否 | 是否压缩旧日志，默认 false |

#### 5.2.2 日志格式

JSON 结构化日志：

```json
{
  "timestamp": "2026-04-18T10:30:00Z",
  "level": "INFO",
  "event": "chat_completed",
  "data": {
    "chat_id": "chat_xxx",
    "session_id": "20260418103000523_a1b2",
    "duration": 45,
    "status": "success"
  }
}
```

**日志事件类型：**

| event 值 | 用途 |
|----------|------|
| `api_request` | API 调用记录 |
| `skill_hot_reload` | Skills 热插拔事件 |
| `chat_completed` | 对话完成事件 |

#### 5.2.3 日志存储与轮转

- 目录：`{GROOT_HOME}/logs/`
- 格式：`groot-{date}.log`
- 保留：7天，自动删除过期日志

**日志级别：**

| 级别 | 说明 |
|------|------|
| `debug` | 详细调试信息 |
| `info` | 常规运行信息（默认） |
| `warn` | 警告信息 |
| `error` | 错误信息 |

#### 5.2.4 日志采集（ELK集成）

JSON 结构化日志可直接用于监控采集，通过 ELK 或类似日志系统分析。

### 5.3 Health Manager

#### 5.3.1 存活探针

通过 `/health` 接口返回服务存活状态。

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m"
}
```

#### 5.3.2 就绪探针

检查服务是否准备好接收请求：
- LLM 连接就绪
- MCP 服务连接就绪
- Skills 加载完成

#### 5.3.3 依赖健康检查

```json
{
  "checks": {
    "llm": {"status": "healthy", "model": "gpt-4o"},
    "mcp_servers": {"status": "healthy", "servers": [...]},
    "skills": {"status": "healthy", "count": 12},
    "memory": {"status": "healthy", "used_mb": 256}
  }
}
```

---

## 六、性能与并发

### 6.1 ReAct 执行限制

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
| `max_iterations` | ReAct 最大循环次数 | 20 |
| `max_tokens` | 单对话最大Token消耗 | 100000 |
| `step_timeout` | 单步执行超时时间（秒） | 60 |
| `error_retry` | 单步失败后重试次数 | 2 |
| `nesting_max_depth` | Skills 嵌套最大深度 | 3 |

### 6.2 错误码与重试策略

**错误码定义：**

| 错误码 | 说明 | 可恢复 |
|--------|------|--------|
| `invalid_request` | 请求参数错误 | 否 |
| `llm_connection_error` | LLM 连接失败 | 可重试 |
| `llm_rate_limited` | LLM API 限流 | 可重试 |
| `tool_call_error` | 工具调用失败 | 可重试 |
| `skill_not_found` | Skill 不存在 | 否 |
| `chat_cancelled` | 用户取消 | 否 |

**重试策略：**

| 场景 | 重试次数 | 重试间隔 |
|------|---------|---------|
| LLM 连接失败 | 3 | 2s |
| LLM Rate Limit | 3 | 5s |
| MCP 工具失败 | 2 | 1s |

---

## 七、部署与运维

### 7.1 启动参数

| 参数 | 缩写 | 说明 | 默认值 |
|------|------|------|--------|
| `--home` | `-H` | 工作目录 | `~/.groot` |
| `--port` | `-p` | HTTP端口 | 配置文件值 |
| `--help` | `-h` | 显示帮助 | - |
| `--version` | `-v` | 显示版本 | - |

### 7.2 环境变量

| 变量 | 说明 | 必需 |
|------|------|------|
| `OPENAI_API_KEY` | LLM API 密钥 | 否（可在配置文件中设置） |
| `GROOT_API_KEY` | Groot 认证密钥 | 是（启用认证时） |
| `GROOT_HOME` | 工作目录 | 否 |
| `ANTHROPIC_API_KEY` | Anthropic API 密钥 | 否 |

### 7.3 优雅关闭

- 停止接受新请求
- 等待当前对话完成（超时30秒）
- 停止清理调度器
- 关闭 MCP 连接
- 刷新日志
- 退出程序

---

## 八、配置模板

### 8.1 完整 config.yaml

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
  directory: skills                # Skills 目录（相对于 GROOT_HOME）
  hot_reload:
    enabled: true                  # 是否启用 Skills 热插拔
    debounce_delay: 2              # 防抖延迟（秒）

# MCP 配置目录
mcp:
  directory: mcp                   # MCP 配置目录（相对于 GROOT_HOME）

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

---

## 附录

### A. Skill 示例

**pdf_analyzer：**

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

**report_generator（嵌套Skill示例）：**

```markdown
---
name: report_generator
description: "综合分析多种来源的资料，生成完整的分析报告"
dependencies: [pdf_analyzer, data_analyzer]
---

# 报告生成

你是一个报告生成助手，可调用其他 Skills 完成综合分析。

## 执行步骤

1. 分析用户提供的资料类型
2. 根据资料类型调用相应的分析 Skills
3. 整合各 Skills 的分析结果
4. 生成结构化的综合报告
5. 使用 file_operations.file_write 保存报告
```

### B. 错误码速查表

| HTTP 状态码 | 错误码 | 说明 |
|------------|--------|------|
| 400 | `invalid_request` | 请求参数错误 |
| 401 | `unauthorized` | API Key 无效或缺失 |
| 403 | `forbidden` | 权限不足 |
| 409 | `chat_limit_exceeded` | 会话已有对话执行中 |
| 409 | `session_not_found` | 会话不存在 |
| 500 | `config_error` | 配置错误 |
| 500 | `llm_connection_error` | LLM 连接失败 |
| 500 | `tool_call_error` | 工具调用失败 |

### C. 文件路径约定

| 路径 | 说明 |
|------|------|
| `{GROOT_HOME}/config.yaml` | 配置文件 |
| `{GROOT_HOME}/skills/{name}/SKILL.md` | Skill 定义文件 |
| `{GROOT_HOME}/mcp/{name}.json` | MCP 配置文件 |
| `{GROOT_HOME}/memory/{session_id}/history.json` | 对话历史 |
| `{GROOT_HOME}/memory/{session_id}/attachments/` | 附件目录 |
| `{GROOT_HOME}/memory/{session_id}/chats/{chat_id}.json` | 详细执行记录 |
| `{GROOT_HOME}/logs/groot-{date}.log` | 日志文件 |