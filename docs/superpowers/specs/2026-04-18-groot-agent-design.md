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
- 热插拔扩展：Skills 支持动态添加，无需重启服务
- 定时任务调度：通过对话创建定时任务，系统定时自动执行并推送结果
- 消息通知推送：执行结果通过消息层统一路由到飞书/钉钉/邮件/Webhook 等渠道

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
      max_completion_tokens: 4096
      temperature: 0.7
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_completion_tokens: 4096
      temperature: 0.7
```

**字段说明：**

| 字段 | 说明 |
|------|------|
| `default_model` | 默认模型名称，对应 models 中的某个 key |
| `base_url` | LLM API 地址（OpenAI 兼容协议） |
| `api_key` | API 密钥，支持环境变量引用 `${VAR_NAME}` |
| `model` | 实际调用时的模型名称 |
| `max_completion_tokens` | 最大输出 Token 数，默认 4096 |
| `temperature` | 输出随机性（0.0~2.0），默认 0.7 |
| `top_p` | 核采样系数（0.0~1.0），默认 1.0 |
| `frequency_penalty` | 频率惩罚（-2.0~2.0），默认 0.0 |
| `presence_penalty` | 存在惩罚（-2.0~2.0），默认 0.0 |
| `seed` | 随机种子，0 表示不设置 |
| `stop` | 停止序列列表，默认空 |
| `thinking` | 深度思考模式（Qwen/DeepSeek 等），默认 false |

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
│  │  热插拔               ││                       ││  RuntimeState     │ │
│  │                       ││                       ││  AttachmentStore  │ │
│  └───────────────────────┘└───────────────────────┘└───────────────────┘ │
│                                                                           │
│  ┌───────────────────────┐┌───────────────────────────────────────────┐  │
│  │       Schedule        ││               Message                    │  │
│  ├───────────────────────┤├───────────────────────────────────────────┤  │
│  │  定时任务CRUD          ││  事件发布                                │  │
│  │  gocron调度引擎        ││  渠道过滤 + 并发发送（Webhook/邮件）      │  │
│  │  Task Runner           ││  并发发送                                │  │
│  │  active/disabled/      ││  结果记录                                │  │
│  │  archive 状态管理      ││                                          │  │
│  └───────────────────────┘└───────────────────────────────────────────┘  │
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
| Memory | Session 管理（创建/查询）、History 索引（全量保存）、LLM 上下文构建（窗口截断）、Chat 详情持久化、Attachment Store、会话清理（由 gocron 调度） |
| Schedule | 定时任务生命周期管理、gocron 调度引擎、Task Runner（复用 Agent 执行）、active/disabled/archive 状态管理 |
| Message | 事件发布、渠道过滤 + 并发发送（Webhook/邮件）、结果记录、全链路日志 |

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
- Schedule → Agent Engine：定时触发时调用 Agent 执行任务
- Schedule → Message：执行完成后发布通知事件
- Schedule → Memory：管理清理 Job（注册 gocron 定时清理任务）
- Message → Config：读取消息渠道配置（Sender 启用/参数）

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

默认工作目录：`~/.groot`，可通过环境变量 `GROOT_HOME` 更改。

```
{GROOT_HOME}/
├── config.yaml                    # 主配置文件
├── GROOT.md                       # 项目规范文件（自动注入系统指令）
├── skills/                        # Skills 目录（固定位置）
│   └── {skill-name}/SKILL.md      # Skill 定义文件
├── mcp/                           # MCP 配置目录（固定位置）
│   └── {mcp-name}.json            # MCP 配置文件
├── schedules/                     # 定时任务目录（固定位置）
│   ├── active/                    # 活跃任务
│   │   └── {task-id}/
│   │       ├── task.json          # 任务定义
│   │       └── executions/        # 执行历史
│   ├── disabled/                  # 已禁用任务
│   └── archive/                   # 已归档任务
├── {memoryDir}/                   # 记忆模块目录（可配置位置，默认 memory）
│   ├── temp/                      # 附件处理临时目录（固定在 memory 目录下）
│   └── {session_id}/              # 会话目录
│       ├── SESSION.md              # 会话文件目录提示（LLM 上下文注入）
│       ├── history.json           # 对话历史（含执行元数据摘要）
│       ├── attachments/           # 附件目录
│       │   └── {filename}         # 附件文件
│       └── chats/                 # 详细执行记录目录
│           └── chat_{timestamp}.json  # 单次对话完整记录
├── {logDir}/                      # 日志目录（可配置位置，默认 logs）
│   └── groot-{date}.log           # 日志文件
```

**目录说明：**
- `skills`、`mcp`、`api` 目录固定在 `{GROOT_HOME}` 下，不可配置
- `memory` 目录可通过 `memory.directory` 配置，支持相对/绝对路径
- `temp` 目录固定在 memory 目录下，位置取决于 memory.directory 配置
- `logs` 目录可通过 `logging.file.directory` 配置，支持相对/绝对路径

---

## 三、Access Layer（接入层）

### 3.1 API 设计

> API 端点定义（请求/响应格式、SSE 事件协议、ID 生成规则等）已抽取至 [API 设计文档](2026-05-16-api-design.md)。

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
│   │   ├─ Base64 解码 → []byte
│   │   ├─ 文件名安全处理（替换 /、\、.. 等危险字符）
│   │   ├─ memory.SaveAttachment(session_id, filename, content)
│   │   │   └─ 保存到 memory/{session_id}/attachments/{filename}
│   │   ├─ 收集文件名列表（用于 history.json 记录）
│   │   └─ 同名文件会覆盖
│   ├─ 处理失败 → SSE 推送错误，终止
│   └─ 处理成功 → 继续，构建 MultimodalContent 传递给 LLM（详见下方"附件到 LLM 的透传方式"）
│
├─ 6. 构建 Agent 上下文
│   ├─ 系统指令（buildSystemInstruction），按序拼接：
│   │   ├─ 1. GROOT.md（项目规范）
│   │   ├─ 2. SESSION.md（会话文件目录提示，新会话首轮由 CreateSession 写入）
│   │   ├─ 3. prompt（用户传入的系统提示词）
│   │   ├─ 4. Skills 指令
│   │   └─ 5. 执行规则
│   ├─ 历史消息（historyMessages，继续会话时）
│   │   ├─ 通过 GetContextMessages(sid, history_window) 获取最近 N 轮
│   │   ├─ 每轮对话：instruction + result
│   │   └─ 构建为 schema.Message 格式
│   ├─ 当前用户消息（buildUserMessage 构建，根据附件类型不同处理）
│   ├─ 注册的工具列表（MCP 工具）
│   └─ 执行限制配置（max_iterations、max_tokens 等）
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

### 附件到 LLM 的透传方式

附件在服务端的处理分两步：**先落盘，再透传**。两者在同一请求中串行执行，先保存到磁盘，再构建消息发送给 LLM。

因 eino 框架不支持 `file_url` 类型的消息部分，不同附件类型采用不同的透传方式：

#### image / audio / video — Base64 data URL 透传

对于图片、音频、视频附件，通过 OpenAI 的 `UserInputMultiContent` 消息格式，以 Base64 data URL 方式直接发送给 LLM：

```
用户指令 + 附件 → buildUserMessage()
  ├─ image  → ChatMessagePartTypeImageURL  → data:image/png;base64,{data}
  ├─ audio  → ChatMessagePartTypeAudioURL  → data:audio/wav;base64,{data}
  └─ video  → ChatMessagePartTypeVideoURL  → data:video/mp4;base64,{data}
```

LLM 直接解析 data URL 获取二进制内容。此方式依赖 LLM 模型本身支持多模态输入（如 Qwen3.5、GPT-4o 等视觉/音频模型）。

#### file — 解码后拼入指令

对于文件附件，eino 不支持 `file_url` 类型（会报 `unsupported chat message part type: file_url`），因此采用服务端解码后拼入 instruction 的方式：

```
用户指令 + 附件 → buildUserMessage()
  ├─ Base64 解码 → 原文（string）
  └─ 拼接为："{原指令}\n\n{文件名} 的文件内容如下：\n{原文}"
```

发送给 LLM 的消息示例：

```
帮我看看文件内容是什么

数据文件.txt 的文件内容如下：
Hello from test file
Line 2: test data
```

LLM 收到的是自然可读的文本，无需自身解码 Base64。

#### 混合附件

当同时存在 image/audio/video 和 file 附件时：

```
buildUserMessage()
  ├─ instruction 文本部分 = 原指令 + file 解码内容
  ├─ image/audio/video → UserInputMultiContent 的 data URL 部分
  └─ 消息类型 → schema.User（UserInputMultiContent）
```

- file 解码内容拼接在 instruction 文本中
- image/audio/video 以 data URL 附带在消息的额外部分

**附件存储目录结构：**

```
{GROOT_HOME}/memory/
├── 20260418103000523_a1b2/     # 会话A
│   ├── SESSION.md                    # 会话文件目录提示
│   ├── history.json                 # 对话历史
│   ├── attachments/                 # 附件目录
│   │   ├── report.pdf               # 第1轮上传
│   │   ├── data.csv                 # 第1轮上传
│   │   ├── data.csv                 # 第3轮上传（覆盖第1轮）
│   │   └── chart.png                # 第3轮上传
│   └── chats/                       # 详细执行记录
├── 20260418103500123_b2c3/     # 会话B
│   ├── SESSION.md
│   ├── history.json
│   ├── attachments/
│   │   └── config.json
│   └── chats/
└── ...
```

**特点：**
- 附件保存在会话目录下的 `attachments/` 子目录
- 保留原始文件名，同名文件会覆盖
- 附件随会话清理而删除（memory 清理任务）

**会话文件目录提示（SESSION.md）：**

新会话创建时，在会话根目录生成 `SESSION.md` 文件，内容：

```
本会话涉及的文件均存放在以下目录：/home/groot/memory/20260418103000523_a1b2/attachments
如需读取文件内容，请从该目录中查找对应的文件名。
```

引擎启动时，`buildSystemInstruction` 读取 SESSION.md 并注入系统指令（位于 GROOT.md 之后、prompt 之前）。LLM 从系统指令获知附件目录位置，结合对话上下文中的文件名，自行构建路径并通过 MCP `file_read` 工具读取。

不再将附件路径拼接进用户消息中。

**历史消息传递方式：**

继续会话时，通过 `GetContextMessages(sid, history_window)` 获取最近 N 轮历史消息，构建为 schema.Message 格式传递给 Agent：

```
历史构建逻辑：
  1. 调用 GetContextMessages(sid, history_window) 获取最近 N 轮消息
  2. 遍历截断后的 historyMessages
  3. 每轮对话构建两条消息：
     - UserMessage：instruction
     - AssistantMessage：result
  4. 添加当前用户消息
  5. 传递给 Agent 的 messages 数组

示例（history_window=2，会话共4轮，仅最近2轮进入上下文）：
  [
    UserMessage("再画个图表"),          ← 第3轮
    AssistantMessage("图表已生成..."),   ← 第3轮
    UserMessage("继续分析"),             ← 当前指令（第4轮）
  ]
```

> **注意：** `GET /sess/{sid}` API 返回全部历史（不受 history_window 影响），只有传递给 LLM 的上下文才进行窗口截断。

**关键节点说明：**

| 步骤 | 说明 | 失败处理 |
|------|------|---------|
| 请求校验 | 验证参数合法性 | 返回 400，不创建会话 |
| 会话处理 | 判断新建或继续会话，检查并发 | 返回 409（并发冲突）或继续 |
| RuntimeState.Register | 注册活跃对话状态 | - |
| 附件处理 | 解码并存储到 memory 目录 | SSE 推送 error，终止 |
| SSE thinking | 流式输出思考内容（reasoning_content） | 推送 error 事件，终止 |
| SSE tool_calls | AI 决定调用工具，含 tool_calls 数组 | - |
| SSE tool_result | 工具执行结果 | 推送 error 事件，终止 |
| SSE message | 流式输出最终回答（content） | 推送 error 事件，终止 |
| SSE finish | 当前响应阶段结束，含 finish_reason | - |
| SSE [DONE] | 整个对话结束 | - |
| Memory.SaveChatRecord | 保存详细执行记录 | 日志记录错误，不影响响应 |
| Memory.AppendMessage | 更新 history.json | 日志记录错误，不影响响应 |

> SSE 事件详细定义见 [API 设计文档 - 3.6 SSE 响应协议](2026-05-16-api-design.md#36-sse-响应协议)。

---

> API 请求/响应示例及查询端点（/chat/{sid}、/sess/{sid}、/health、/skills、/tools 等）定义已抽取至 [API 设计文档](2026-05-16-api-design.md)。

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

**临时目录：** 附件处理临时目录固定为 `{memoryDir}/temp`，位置取决于 memory.directory 配置。

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

> 附件校验错误码、API 认证与鉴权设计（API Key、权限定义）已抽取至 [API 设计文档](2026-05-16-api-design.md)。

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
          ├─ 任务完成 → SSE 推送 finish(stop) + [DONE]，结束
          ├─ 达到最大循环次数 → SSE 推送 error 事件 + [DONE]，终止
          ├─ Token 消耗超限 → SSE 推送 error 事件 + [DONE]，终止
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
└─ 输出最终结果，SSE 推送 finish(stop) + [DONE]
```

#### 4.1.2 循环终止条件

| 条件 | 说明 | SSE 事件 |
|------|------|---------|
| Agent 判断完成 | LLM 输出最终答案 | `finish(stop)` + `[DONE]` |
| 达到最大循环次数 | iteration > max_iterations | `error` + `[DONE]` |
| Token 消耗超限 | tokens_used > max_tokens | `error` + `[DONE]` |
| 单步执行超时 | step_duration > step_timeout | `error` + `[DONE]` |
| 用户取消 | DELETE /chat/{sid} | `error` + `[DONE]` |

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

Skills 是 Groot 的核心扩展机制，通过 SKILL.md 文件以自然语言定义技能，支持声明式定义、自动注册、热插拔、依赖嵌套和 CLI 管理。

**核心能力：**

| 能力 | 说明 |
|------|------|
| 声明式定义 | 通过 SKILL.md（YAML frontmatter + Markdown）描述技能 |
| 自动注册 | 启动时扫描 `{GROOT_HOME}/skills/` 目录，解析并注册为 Agent 工具 |
| 热插拔 | fsnotify 监听目录变化，运行时动态增删改，无需重启 |
| 依赖嵌套 | Skill 声明 dependencies 后，Agent 执行时自动递归调用 |
| CLI 管理 | `groot skills list/install/uninstall` 命令管理 Skills |

> 详细设计见 [Skills 设计文档](2026-05-10-skills-design.md)。

### 4.3 MCP

MCP（Model Context Protocol）是 Groot 集成外部工具的标准化协议，支持 stdio/sse/streamable_http 三种连接类型。

**核心能力：**

| 能力 | 说明 |
|------|------|
| 多连接类型 | stdio（本地命令行）、sse（远程单向推送）、streamable_http（远程双向流式） |
| 自动工具发现 | 连接 MCP Server 后自动调用 `tools/list` 发现可用工具，无需手动配置 |
| 独立配置 | 每个 MCP Server 以独立 JSON 文件存放在 `{GROOT_HOME}/mcp/` 目录 |
| CLI 管理 | `groot mcp list` 命令查看已配置的 MCP Server |

> 详细设计见 [MCP 设计文档](2026-05-10-mcp-design.md)。

### 4.4 Memory

Memory 模块负责会话数据的持久化存储。基于文件系统（JSON），无外部数据库依赖。

**核心设计：**
- **两级存储**：`history.json`（会话索引） + `chats/{chat_id}.json`（单轮详情）
- **全量保存、按需截断**：history.json 保存全部轮次，但传递给 LLM 的上下文只取最近 N 轮
- **原子写入**：所有 JSON 文件写入采用 tmp + rename 模式，防止进程崩溃导致数据损坏
- **定时清理**：会话过期清理由 gocron 统一调度（调度由 Schedule 模块管理）

> 详细设计见 [Memory 模块设计](2026-05-11-memory-design.md)。

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
    Delete(sessionID string)                // 移除活跃状态（对话完成后调用）
    IsRunning(sessionID string) bool
    RunningCount() int
}
```

**职责边界：**

RuntimeState 只负责**活跃对话状态管理**（注册、查询、取消、删除），**不参与数据持久化**。对话结果持久化到 Memory 由 Executor 直接完成。

| 方法 | 说明 |
|------|------|
| `Register` | 原子注册活跃对话，已存在则返回错误 |
| `Get` | 获取活跃对话状态 |
| `UpdateProgress` | 更新执行进度 |
| `Cancel` | 取消对话（close CancelCh，使用 sync.Once 防 panic） |
| `Delete` | 移除活跃状态（对话完成后清理） |
| `IsRunning` | 检查会话是否有活跃对话 |
| `RunningCount` | 返回当前活跃对话总数 |

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
  │     ├─ RuntimeState.IsRunning(sid) 检查并发
  │     ├─ RuntimeState.Register(session_id, chat_id)  ← LoadOrStore 原子操作
  │     └─ 开始执行
  │
  ├─ 2. 执行过程
  │     ├─ RuntimeState.UpdateProgress() 更新进度
  │     └─ DELETE /chat/{sid} → RuntimeState.Cancel() → close(CancelCh)
  │
  ├─ 3. 执行完成（由 Executor 处理）
  │     ├─ Executor 构建 ChatRecord、Message
  │     ├─ Memory.SaveChatRecord() → 写入 chats/{chat_id}.json
  │     ├─ Memory.AppendMessage()  → 追加到 history.json
  │     └─ RuntimeState.Delete(sid)  → 移除活跃状态
  │
  └─ 4. 查询历史
        ├─ GET /sess/{sid}     → Memory.GetHistory()         （全量）
        └─ GET /chat/{sid}    → Memory.GetChatRecord()       （单轮）
```

> **注意：** 当前代码中 Executor 直接构建 ChatRecord 并调用 Memory 持久化，RuntimeState 只在步骤 1-2 做并发控制和取消，步骤 3 用 Delete 清理。这里不存在 `RuntimeState.Complete()` 方法，不引入中间层。详见 4.4.18 实现约束。

### 4.6 Schedule（定时任务调度）

定时任务调度模块，用户通过对话创建定时任务，系统在指定时间自动执行 Agent 指令。

**核心能力：**

| 能力 | 说明 |
|------|------|
| 任务 CRUD | 通过内置工具由 Agent 创建/查询/删除任务 |
| gocron 调度 | 统一管理所有系统定时（含内存清理），单 goroutine 轮询 |
| Task Runner | 定时触发时调用 agent.Executor 执行任务 |
| 状态管理 | active/disabled/archive 三目录，移动即状态变更 |
| 内置工具 | schedule_create/list/delete/disable/enable/archive/history/inspect |

> 详细设计见 [定时任务调度系统设计](2026-05-11-schedule-design.md)。

### 4.7 Message（消息层）

消息通知层，所有需要通知的场景统一通过消息层发布事件，由消息层负责路由分发，不经过 LLM。

**核心能力：**

| 能力 | 说明 |
|------|------|
| 事件发布 | 调用方构建 Event，指定渠道列表 |
| 渠道过滤 | Worker 发送时过滤掉未启用的渠道 |
| 并发发送 | 多渠道 goroutine 并发调用 Sender |
| 内置 Sender | webhook、email、stdout |
| 结果记录 | 返回各渠道 SendResult，由调用方写入执行记录 |

> 详细设计见 [消息层设计](2026-05-11-message-design.md)。

---

## 五、System Layer（系统层）

### 5.1 Config

#### 5.1.1 配置优先级

| 配置项 | 来源 |
|------|------|
| 工作目录 | 环境变量 `GROOT_HOME` > 默认 `~/.groot` |
| HTTP 端口 | 命令行 `-p` > 配置文件 |
| 其他配置 | 配置文件 `config.yaml` |

#### 5.1.2 配置热更新

**支持热更新的配置：**
- Skills 配置（添加/修改/删除 SKILL.md）

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

检查各依赖组件的运行状态：

| 检查项 | 检查方式 | 说明 |
|-------|---------|------|
| `llm` | 调用 `/models` API | 验证 LLM API 连接和认证有效性 |
| `mcp_servers` | 遍历已注册 MCP | 检查各 MCP 工具数量和错误状态 |
| `skills` | 统计 Skills 数量 | 验证 Skills 加载完成 |
| `memory` | 统计会话数量 | 验证会话存储正常 |

**响应示例：**
```json
{
  "checks": {
    "llm": {"status": "healthy", "info": {"model": "gpt-4o"}},
    "mcp_servers": {"status": "healthy", "info": [{"name": "file_operations", "tools_count": 7, "isActive": true}]},
    "skills": {"status": "healthy", "info": {"count": 4}},
    "memory": {"status": "healthy", "info": {"sessions": 10}}
  }
}
```

**LLM 异常时：**
```json
{
  "checks": {
    "llm": {"status": "unhealthy", "info": {"model": "gpt-4o", "error": "connection failed: timeout"}},
    ...
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
  history_window: 20               # LLM 上下文窗口（轮次数），-1 不限制

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

# 定时任务配置
schedule:
  max_concurrent_tasks: 3           # 最大并发执行数
  sync_interval: 30s                # 目录同步间隔

# 消息通知配置
message:
  queue_size: 256           # 发送队列容量
  workers: 2                # 发送工作协程数
  senders:
    webhook:
      enabled: false
      url: ""
    email:
      enabled: false
      smtp_host: ""
      smtp_port: 587
      username: ""
      password: ""
      from: ""

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

**固定目录说明：**

以下目录位置固定，不可配置：
- `{GROOT_HOME}/skills` - Skills 定义目录
- `{GROOT_HOME}/mcp` - MCP 配置目录
- `{GROOT_HOME}/schedules` - 定时任务目录
- `{memoryDir}/temp` - 附件处理临时目录（固定在 memory 目录下）

---

## 附录

### A. Skill 示例

> 详见 [Skills 设计文档 - 七、Skill 示例](2026-05-10-skills-design.md#七skill-示例)。

> 错误码速查表已抽取至 [API 设计文档](2026-05-16-api-design.md)。

### C. 文件路径约定

| 路径 | 说明 |
|------|------|
| `{GROOT_HOME}/config.yaml` | 配置文件 |
| `{GROOT_HOME}/skills/{name}/SKILL.md` | Skill 定义文件（固定位置） |
| `{GROOT_HOME}/mcp/{name}.json` | MCP 配置文件（固定位置） |
| `{GROOT_HOME}/schedules/active/{task-id}/task.json` | 定时任务定义文件（固定位置） |
| `{GROOT_HOME}/schedules/active/{task-id}/executions/` | 定时任务执行历史 |
| `{memoryDir}/temp/` | 附件处理临时目录（固定在 memory 目录下） |
| `{memoryDir}/{session_id}/history.json` | 对话历史 |
| `{memoryDir}/{session_id}/attachments/` | 附件目录 |
| `{memoryDir}/{session_id}/chats/{chat_id}.json` | 详细执行记录 |
| `{logDir}/groot-{date}.log` | 日志文件 |

**说明：**
- `{memoryDir}` 由 memory.directory 配置决定，默认为 `{GROOT_HOME}/memory`
- `{logDir}` 由 logging.file.directory 配置决定，默认为 `{GROOT_HOME}/logs`