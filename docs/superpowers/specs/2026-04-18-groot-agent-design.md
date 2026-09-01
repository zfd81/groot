# Groot AI Agent 设计文档

**版本:** 1.0.0
**日期:** 2026-04-18

---

## 一、概述

### 1.1 项目定位

Groot 是面向业务系统的 AI Agent 服务。通过 REST API 接入，让你的系统立刻拥有智能任务执行能力——理解指令、调用工具、自主完成任务。

**核心特性：**
- 自然语言交互：接收指令 + 附件，无需编写代码逻辑
- 智能决策执行：自动判断意图，自主选择调用 Skills 或 MCP 工具完成任务
- 流式进度反馈：通过 SSE 实时推送 thinking / tool_calls / tool_result / message 事件
- 多 Agent 编排：主 Agent 通过 `call_agent` 工具调度子 Agent 完成专项任务
- 热插拔扩展：Skills 支持动态添加，无需重启服务
- 定时任务调度：通过对话创建定时任务，系统定时自动执行并推送结果
- 消息通知推送：执行结果通过消息层统一路由到 Webhook / Email / Stdout 渠道

### 1.2 技术栈

| 组件 | 技术选型 |
|------|---------|
| HTTP 框架 | Hertz（字节开源） |
| Agent 框架 | eino / eino-ext（字节开源） |
| LLM 调用 | OpenAI 兼容协议（eino-ext openai） |
| 持久化存储 | 数据库（SQLite / MySQL / PostgreSQL） |
| 运行时状态 | 内存管理（sync.Map） |
| 配置格式 | YAML |
| 日志格式 | JSON 结构化（zap） |

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

每次 `POST /chat` 请求可以通过 `X-Model-Name` header 显式指定使用哪个 model；省略时使用 `default_model`。修改 `default_model` 默认值需要重启服务才能生效。

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
│  │  POST   /chat                   │  │  Auth Middleware (API Key)      │ │
│  │  GET    /chat/status/{sid}      │  │  Rate Limit Middleware          │ │
│  │  GET    /chat/{sid}             │  │  SSE Stream Handler             │ │
│  │  GET    /chat/{sid}/{cid}       │  │  Attachment Validator           │ │
│  │  GET    /sess/{sid}             │  │                                 │ │
│  │  GET    /sess/history           │  │                                 │ │
│  │  GET    /agents                 │  │                                 │ │
│  │  GET    /skills                 │  │                                 │ │
│  │  GET    /tools                  │  │                                 │ │
│  │  GET    /models                 │  │                                 │ │
│  │  GET    /health                 │  │                                 │ │
│  │  /schedule/* (CRUD)             │  │                                 │ │
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
│  │                      ReAct Agent Engine（eino ChatModelAgent）      │  │
│  ├─────────────────────────────────────────────────────────────────────┤  │
│  │                                                                     │  │
│  │   ┌────────────┐      ┌────────────┐      ┌────────────┐            │  │
│  │   │  Reasoning │ ───▶ │   Acting   │ ───▶ │ Observation│ ───┐       │  │
│  │   │            │      │            │      │            │    │       │  │
│  │   │  LLM调用   │      │  Skill调用 │      │  结果处理  │    │       │  │
│  │   │  上下文    │      │  MCP工具   │      │  状态更新  │    │       │  │
│  │   │  决策判断  │      │  call_agent│      │  终止检查  │ ◀──┘       │  │
│  │   └────────────┘      └────────────┘      └────────────┘            │  │
│  │                                                                     │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
│                          │              │              │                  │
│                          ▼              ▼              ▼                  │
│  ┌───────────────────────┐┌───────────────────────┐┌───────────────────┐ │
│  │        Skills         ││          MCP          ││       Memory      │ │
│  ├───────────────────────┤├───────────────────────┤├───────────────────┤ │
│  │  目录扫描             ││  MCP 配置加载         ││  Session 元数据   │ │
│  │  描述注入到 prompt    ││  工具自动发现         ││  ChatRecord 持久化│ │
│  │  按需加载完整指令     ││  stdio/sse/http       ││  历史按需聚合     │ │
│  │  热插拔               ││                       ││  RuntimeState     │ │
│  │  einoskill middleware ││                       ││  会话定时清理     │ │
│  └───────────────────────┘└───────────────────────┘└───────────────────┘ │
│                                                                           │
│  ┌───────────────────────┐┌───────────────────────┐┌───────────────────┐ │
│  │      Sub-Agents       ││       Schedule        ││       Message     │ │
│  ├───────────────────────┤├───────────────────────┤├───────────────────┤ │
│  │  agent.md 注册表      ││  任务 CRUD（DB）      ││  事件发布         │ │
│  │  call_agent 工具      ││  gocron 调度引擎      ││  渠道过滤+并发    │ │
│  │  独立 MCP / Skills    ││  Task Runner          ││  Webhook/Email/   │ │
│  │  semaphore 并发控制   ││  active/disabled/     ││  Stdout senders   │ │
│  │                       ││  archive 状态        ││  结果记录         │ │
│  └───────────────────────┘└───────────────────────┘└───────────────────┘ │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                           System Layer（系统层）                           │
├───────────────────────────────────────────────────────────────────────────┤
│                                                                           │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────────┐ │
│  │    Config    │ │    Logger    │ │   Cluster    │ │   DB / Repo      │ │
│  ├──────────────┤ ├──────────────┤ ├──────────────┤ ├──────────────────┤ │
│  │  YAML 加载   │ │  zap JSON    │ │  成员注册    │ │  sqlx 抽象       │ │
│  │  环境变量    │ │  按日期滚动  │ │  Leader 选举 │ │  SQLite/MySQL/PG │ │
│  │  参数校验    │ │  按天保留    │ │  心跳        │ │  Repo 工厂       │ │
│  │  默认值      │ │              │ │              │ │                  │ │
│  └──────────────┘ └──────────────┘ └──────────────┘ └──────────────────┘ │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
```

### 2.2 模块职责总览

**Access Layer（接入层）**

| 组件 | 职责 |
|------|------|
| REST API (Hertz) | HTTP 接口暴露、请求解析、SSE 流式响应、响应封装 |
| Auth Middleware | API Key 验证（`X-API-Key` header）、按 key 粒度的权限范围控制 |
| Rate Limit Middleware | 全局 / 按 key 的 QPS + 并发限流 |
| SSE Stream Handler | 流式响应，按事件类型推送 thinking / message / tool_calls / tool_result / finish / error / [DONE] |
| Attachment Validator | 上传校验（数量 / 单大小 / 总大小 / 类型白名单），不持久化 |

**Intelligence Layer（智能层）**

| 组件 | 职责 |
|------|------|
| Agent Engine（[engine.go](internal/agent/engine.go)） | 基于 eino `ChatModelAgent` + `Runner` 的 ReAct 执行；构造 system instruction、消息列表、工具集；事件循环转发 SSE；token / step 累积 |
| Executor（[executor.go](internal/agent/executor.go)） | 区分 Solo / 编排模式，挂载 `call_agent`、装配 skill middleware，落库 `ChatRecord` |
| Sub-Agent Registry（[subagent_registry.go](internal/agent/subagent_registry.go)） | 扫描 `subagents/{name}/agent.md`，加载子 Agent 元数据、独立 MCP / Skills，提供 `call_agent` 工具实现 |
| Skills | 通过 eino `einoskill` 中间件挂载：扫描 `{GROOT_HOME}/skills/`，向 system prompt 注入 skill 摘要表，按需通过工具加载完整指令 |
| MCP（[internal/mcp/](internal/mcp)） | 加载 `{GROOT_HOME}/mcp/*.json` 配置，自动 `tools/list` 发现，向引擎注册 BaseTool 列表 |
| Memory（[internal/memory/](internal/memory)） | Session 元数据 / 单轮 ChatRecord 持久化（`memory_sessions` / `memory_chats`），按需聚合历史消息，注入 `session_rules.md` 嵌入规则，`Cleanup` 定时清理 |
| Runtime State（[runtime_state.go](internal/agent/runtime_state.go)） | 活跃对话注册 / 并发互斥（`sync.Map`）、子 Agent 运行状态快照 |
| Schedule（[internal/schedule/](internal/schedule)） | 任务 CRUD（DB）、gocron 调度引擎、Task Runner（复用 Executor）、状态切换（active/disabled/archive） |
| Message（[internal/message/](internal/message)） | 事件发布、渠道过滤 + 并发发送（webhook / email / stdout senders）、SendResult 返回 |

**System Layer（系统层）**

| 组件 | 职责 |
|------|------|
| Config（[internal/config/](internal/config)） | YAML 解析、环境变量展开、参数范围校验、默认值生成 |
| Logger（[internal/logger/](internal/logger)） | zap JSON 结构化日志、按日期文件名滚动、`max_age` 天数清理 |
| Cluster（[internal/cluster/](internal/cluster)） | 节点注册 / 心跳 / Leader 选举，Leader 启停定时任务回调 |
| DB / Repo（[internal/db/](internal/db) / [internal/repo/](internal/repo)） | sqlx 抽象、SQLite/MySQL/PG 三方言、Repo 工厂统一注入 |

### 2.3 依赖关系说明

**层间依赖：**
- Access Layer → Intelligence Layer：API 请求转发给 Executor 执行
- Intelligence Layer → System Layer：读取配置、写入日志、读写数据库

**智能层内部依赖：**
- Executor → Engine：构造 EngineConfig 并调用 `Engine.Run`
- Engine → MCP Manager：通过 `MCPManager.GetTools()` 装配工具集
- Engine → einoskill middleware：通过 `agentConfig.Handlers` 挂载 skill 处理
- Executor → Memory：写入 `ChatRecord` 与历史 Message
- Executor → RuntimeState：注册活跃对话、子 Agent 状态记账
- Schedule.Runner → Executor：定时触发时构造 Task 调用 `Executor.Execute`
- Schedule.Runner → Message：执行结果通过消息层路由到外部渠道
- Schedule.Engine → Memory：注册 `Cleanup` 定时任务

**工具注册机制：**

**Skills 注册给 Agent：**
- 启动时由 `einoskill.NewBackendFromFilesystem` 扫描 `{GROOT_HOME}/skills/{name}/SKILL.md`
- `einoskill.NewMiddleware` 通过 `CustomSystemPrompt` 把 skill 名称 + 描述拼成"可用 Skill"摘要表注入 system prompt
- 用户请求触发匹配时，LLM 通过框架自带的 skill 工具按需加载完整指令（progressive disclosure）

**MCP 工具注册给 Agent：**
- 启动时 `mcp.Manager.LoadAll` 扫描 `{GROOT_HOME}/mcp/*.json`，连接 MCP Server
- 自动调用 `tools/list` 发现工具，缓存为 `tool.BaseTool` 列表
- `Engine.buildTools` 把 MCP 工具与 `extraTools`（编排模式下的 `call_agent`）合并

**Agent 工具列表示例：**

| 工具类型 | 名称 | 描述 |
|---------|------|------|
| 内置 | call_agent | 调用指定子 Agent 执行任务（仅主 Agent 编排模式） |
| 内置 | schedule_create / schedule_list / schedule_inspect / schedule_disable / schedule_enable / schedule_archive / schedule_history / schedule_delete | 定时任务管理工具，仅在 `schedule.enabled=true` 且当前实例为 Leader 时由主 Agent 注册 |
| Skill | （由 `einoskill` 工具加载，工具名 = skill 名） | 由 SKILL.md 定义；摘要进 system prompt，正文按需通过 skill 工具加载 |
| MCP | 由 MCP Server `tools/list` 自动发现 | 名称、描述、参数定义来自 MCP Server |

### 2.4 目录结构

默认工作目录：`~/.groot`，可通过环境变量 `GROOT_HOME` 更改。

```
{GROOT_HOME}/
├── config.yaml                    # 主配置文件
├── env.yaml                       # 节点本地基础设施配置（数据库连接凭据，可选）
├── GROOT.md                       # 项目规范文件（自动注入系统指令）
├── skills/                        # Skills 目录（固定位置）
│   └── {skill-name}/SKILL.md      # Skill 定义文件
├── mcp/                           # MCP 配置目录（固定位置）
│   └── {mcp-name}.json            # MCP 配置文件
├── subagents/                     # 子 Agent 目录（固定位置）
│   └── {agent-name}/agent.md      # 子 Agent 入口文件
├── {logDir}/                      # 日志目录（可配置位置，默认 logs）
│   └── groot-{date}.log           # 日志文件
└── groot.db                       # SQLite 模式下的本地数据库（cluster / schedule / memory / shared_resources 全部表）
```

**目录说明：**
- `skills`、`mcp`、`subagents` 目录固定在 `{GROOT_HOME}` 下，不可配置
- 运行时数据（cluster 成员注册 / schedule 任务 / memory 会话与对话）落到数据库后端：
  - SQLite 模式（默认）：`~/.groot/groot.db`
  - MySQL/PG 模式：远端数据库的 `cluster_members` / `schedule_tasks` / `schedule_executions` / `memory_sessions` / `memory_chats` 五张表
  详见 [数据库后端设计](2026-06-10-database-backend-design.md)
- 集群共享配置（`config.yaml` / `skills/` / `subagents/` / `mcp/` / `GROOT.md`）：本地 HOME 是运行时读取入口；MySQL/PG 模式下通过 `groot push/pull/diff` 同步到 `shared_resources` 表，详见 [sync 模块设计](2026-06-08-sync-design.md)
- `logs` 目录可通过 `logging.file.directory` 配置，支持相对/绝对路径，永远落本地磁盘（不参与同步）
- `env.yaml` 是节点本地配置（含数据库连接凭据），不参与集群同步；详见 [数据库后端设计 §1.5](2026-06-10-database-backend-design.md#15-envyaml-配置格式)
- 会话规则以 `//go:embed session_rules.md` 形式嵌入二进制（[internal/memory/session_rules.md](internal/memory/session_rules.md)），不落盘

---

## 三、Access Layer（接入层）

### 3.1 API 设计

> API 端点定义（请求/响应格式、SSE 事件协议、ID 生成规则等）已抽取至 [API 设计文档](2026-05-16-api-design.md)。

**完整处理流程：**

```
请求到达 → 请求校验 → 会话处理 → 注册活跃状态 → 返回响应 Header → 异步执行 →
│
├─ 1. 请求校验
│   ├─ 解析 JSON 请求体
│   ├─ 校验 instruction 非空
│   ├─ 提取 X-Model-Name header，校验模型名是否在 LLM 配置中
│   ├─ 提取 X-User-ID header
│   ├─ 提取 X-Agent-Name header（不传或传 "groot" → 编排模式；传子 Agent 名 → Solo 模式，需在 SubAgentRegistry 中存在）
│   ├─ 附件校验（attachment.Handler.Validate）：数量、单/总大小、扩展名白名单
│   └─ 任一校验失败 → 返回 400 + 对应 status code，终止
│
├─ 2. 并发预检
│   ├─ 提取 X-Session-ID
│   └─ sessionID 非空 AND RuntimeState.IsRunning(sid) → 返回 409 chat_limit_exceeded
│
├─ 3. 会话处理
│   ├─ sid 为空 OR memory.ExistsSession(sid) = false（新建会话）
│   │   ├─ sessionID = memory.GenerateSessionID()（格式 {YYYYMMDDHHMMSSmmm}_{random4}）
│   │   ├─ isNew = true、round = 1、historyMessages = []
│   │   └─ 标记需要后续创建 session
│   │
│   └─ sid 有值 AND memory.ExistsSession(sid) = true（继续会话）
│       ├─ isNew = false
│       ├─ round = memory.GetRoundCount(sid) + 1
│       └─ historyMessages = memory.GetContextMessages(sid, history_window)
│
├─ 4. 生成 chatID + 注册活跃状态
│   ├─ chatID = memory.GenerateChatID()（17 位 {YYYYMMDDHHMMSSmmm}）
│   ├─ RuntimeState.Register(sessionID, chatID)（LoadOrStore 原子操作）
│   │   └─ 已存在 → 返回 409 chat_limit_exceeded
│   └─ 若是新会话则 memory.CreateSession(sessionID, userID)
│
├─ 5. 附件处理
│   ├─ 遍历 req.Attachments：image/audio/video/file
│   ├─ 对每个附件 base64 解码原文（file 类型保留 DecodedContent，其余只保留 Base64Data）
│   ├─ 校验失败（缺 content / 解码失败 / 非法 type）→ 400 错误，终止
│   └─ 转为 []agent.MultimodalContent，写入 Task.MultiModalContents
│
├─ 6. 构建 Task 对象
│   └─ ID, Instruction, Prompt, StartTime, Round, HistoryMessages, ModelName, AgentName,
│      MultiModalContents, Status=running, Steps=[]
│
├─ 7. 设置 SSE 响应头并启动流式响应
│   ├─ X-Session-ID / X-Chat-ID
│   ├─ Content-Type: text/event-stream
│   ├─ Cache-Control: no-cache
│   ├─ Connection: keep-alive
│   └─ rc.SetBodyStream(io.PipeReader, -1) + Response.ImmediateHeaderFlush
│
├─ 8. 异步 goroutine 执行 Agent
│   ├─ 启动协程 → defer 注销 RuntimeState、关闭 pipe
│   ├─ recover panic → 写 [DONE]
│   └─ Executor.Execute(ctx, sessionID, task, sseWriter)
│
└─ 9. Executor 内部执行（详见 4.1 Engine 流程）
    ├─ 区分 Solo / 编排模式装配 mcpManager / middlewares / extraTools / agent.md
    ├─ 构造 Engine 并调用 Engine.Run（事件循环转发 SSE）
    ├─ 计算 duration、汇总 status / result / steps / tokens
    ├─ memory.SaveChatRecord（事务内写 memory_chats + 更新 memory_sessions.round）
    ├─ memory.AppendMessage（DB 模式 no-op）
    └─ 关闭 pipe → SSE 连接结束
```

> Engine 事件循环（[engine.go](internal/agent/engine.go) `Run`）的处理：每个 `adk.AgentEvent` 按 `Role` 分流——`Assistant` 流式分支拆分 `ReasoningContent` / `Content` / `ToolCalls` / `FinishReason` 与 token Usage 多路 chunk，`Tool` 分支按 `ToolCallID` 翻 step 状态，结束时写 `[DONE]`。

**会话处理逻辑总结：**

| 条件 | sid | 会话存在 | 处理方式 | isNew | round | historyMessages |
|------|-----|---------|---------|-------|-------|-----------------|
| 新会话 | 空 | - | 生成新 sid 并创建 | true | 1 | [] |
| 会话不存在 | 有值 | false | 生成新 sid 并创建 | true | 1 | [] |
| 继续会话 | 有值 | true | 使用传入 sid，检查并发 | false | count+1 | `GetContextMessages(sid, history_window)` |

### 附件到 LLM 的透传方式

附件在服务端不落盘：完成 `attachment.Handler.Validate` 校验后，ChatHandler 把每个附件 base64 解码并构造成 `agent.MultimodalContent`，仅在请求生命周期内驻留。

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

**会话数据存放位置：**

会话元数据与对话历史不再以文件形式存放，而是落到数据库（详见 [Memory 模块设计](2026-05-11-memory-design.md) 与 [数据库后端设计 §1.9.6 / §1.9.7](2026-06-10-database-backend-design.md)）：

| 数据 | 表 / 列 |
|---|---|
| 会话元数据（session_id / round / created_at / updated_at） | `memory_sessions` 行 |
| 单轮对话完整记录（chat_id / instruction / result / steps / tokens / status） | `memory_chats` 行 |
| 历史消息按需聚合 | `LoadHistory` 在 `memory_chats` 上实时查询 `status='completed' AND agent_name=''` |

**特点：**

- SQLite 模式（默认）：以上两张表落在 `~/.groot/groot.db`
- MySQL/PG 模式：以上两张表落在远端数据库，多节点实时共享
- 附件不持久化（详见 [附件设计 §1.5](2026-06-05-attachment-and-session-rules-design.md)）；附件内容随 `Task.MultiModalContents` 入参传给 Engine，仅在请求生命周期内驻留

**系统指令构造（buildSystemInstruction）：**

`Engine.buildSystemInstruction` 按序拼接以下片段，最终作为 ChatModelAgent 的 `Instruction`：

1. **GROOT.md / agent.md（二选一）**
   - 编排模式（主 Agent）：从 `{GROOT_HOME}/GROOT.md` 读取项目规范，留空则跳过
   - Solo 模式（子 Agent）：用子 Agent 的 `agent.md` 正文替换 GROOT.md
2. **会话规则（sessionMdContent）**：来自 `Memory.GetSessionMdContent(sessionID)`，由 `internal/memory/session_rules.md` `go:embed` 嵌入二进制，所有会话共享同一份正文
3. **prompt（用户传入）**：来自 `ChatRequest.prompt`

> Skill 摘要表（"## 可用 Skill"）由 `einoskill.NewMiddleware` 的 `CustomSystemPrompt` 在系统提示外补充，由 eino 框架在调用模型前与 Engine 的 instruction 合并；Engine 自身不直接拼接 skill 信息。

**历史消息传递方式：**

继续会话时，通过 `GetContextMessages(sid, history_window)` 获取最近 N 轮历史消息，构建为 schema.Message 格式传递给 Agent：

```
历史构建逻辑：
  1. 调用 GetContextMessages(sid, history_window) 获取最近 N 轮消息
  2. 遍历截断后的 historyMessages
  3. 每轮对话构建两条消息：
     - UserMessage：instruction
     - AssistantMessage：stripThinking(result)
       （strip 掉 <think>...</think> 标签再传给 LLM；数据库中原始内容不变）
  4. 添加当前用户消息
  5. 传递给 Agent 的 messages 数组

**为何 strip thinking：** 部分模型（如 MiniMax）把推理过程以 `<think>...</think>` 标签内嵌在 Content 里而非独立 ReasoningContent 字段。若把 thinking 带入上轮历史上下文，会显著增加 prompt token 消耗（thinking 通常比回答本身更长），且可能干扰模型行为。strip 只影响传给 LLM 的历史，不影响数据库存储和 API 返回。

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
| 请求校验 | 验证 instruction / X-Model-Name / X-Agent-Name / 附件参数合法性 | 返回 400，不创建会话 |
| 会话处理 | 判断新建或继续会话，检查并发 | 返回 409 chat_limit_exceeded（并发冲突） |
| RuntimeState.Register | LoadOrStore 原子注册活跃对话状态 | 已存在 → 返回 409 |
| 附件处理 | base64 解码、构建 MultimodalContent | SSE 推送 error，终止 |
| SSE thinking | 流式输出推理内容（`reasoning_content`） | 上层 SSE 写失败终止 |
| SSE tool_calls | LLM 决定调用工具，含 `tool_calls` 数组 | - |
| SSE tool_result | 工具执行结果（`role=tool`），可带 `error: true` | MCP 错误也通过 tool_result 推送 |
| SSE message | 流式输出最终回答（`content`） | 写失败终止 |
| SSE finish | 当前响应阶段结束，含 `finish_reason` | - |
| SSE [DONE] | 整个对话结束 | - |
| Memory.SaveChatRecord | 落库 `memory_chats` 行（事务内同时更新 `memory_sessions.round`） | 日志记录错误，不影响响应 |
| Memory.AppendMessage | DB 模式下为 no-op（轮次由 SaveChatRecord 自动维护） | 日志记录错误，不影响响应 |
| RuntimeState.Delete | goroutine 收尾时移除活跃状态（`defer`） | - |

> SSE 事件详细定义见 [API 设计文档 - 3.6 SSE 响应协议](2026-05-16-api-design.md#36-sse-响应协议)。

---

> API 请求/响应示例及查询端点（/chat/{sid}、/sess/{sid}、/health、/skills、/tools 等）定义已抽取至 [API 设计文档](2026-05-16-api-design.md)。

### 3.2 Attachment Validator

附件校验器只负责对用户上传的附件做请求级合规校验，不做任何持久化（详见 [internal/attachment/handler.go](internal/attachment/handler.go)）。

#### 3.2.1 附件配置

```yaml
attachment:
  max_size: 50                     # 单个附件最大大小（MB）
  max_total_size: 100              # 附件总大小上限（MB）
  max_count: 10                    # 附件数量上限
  allowed_types: []                # 允许的扩展名列表（空数组 = 允许全部）
```

**配置字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `max_size` | int | 否 | 单个附件最大大小（MB），默认 50 |
| `max_total_size` | int | 否 | 所有附件总大小上限（MB），默认 100 |
| `max_count` | int | 否 | 单次请求附件数量上限，默认 10 |
| `allowed_types` | []string | 否 | 允许的文件扩展名列表（小写、不带点）。空数组表示允许所有类型 |

#### 3.2.2 校验流程

```
收到附件 → 逐个校验 →
│
├─ 1. 数量校验
│     attachments.length > max_count → ErrCodeCountExceeded（400）
│
├─ 2. 必填字段校验
│     name 为空 → ErrCodeMissingName
│     content 为空 → ErrCodeMissingContent
│
├─ 3. 类型校验
│     type 不属于 file/image/audio/video → ErrCodeInvalidType
│     file/image 扩展名不在 allowed_types 中 → ErrCodeTypeNotAllowed（仅 allowed_types 非空时启用）
│
├─ 4. 大小校验（仅 file 类型计入）
│     单个 base64 解码后预估大小（len*3/4） > max_size → ErrCodeSizeExceeded
│     总大小 > max_total_size → ErrCodeTotalSizeExceeded
│
└─ 全部通过 → 进入下一步处理
```

> 校验在 ChatHandler 内调用 `attachment.Handler.Validate`；Validator 不持久化任何字节。Base64 解码与 `MultimodalContent` 构造由 ChatHandler 在校验通过后完成，附件原文仅在请求生命周期内驻留。

#### 3.2.3 处理流程

```
校验通过 → ChatHandler 解码为 agent.MultimodalContent →
│
├─ image / audio / video → 仅保留 Base64Data，等待 Engine.buildUserMessage 拼成 data URL
└─ file → 同时保留 Base64Data 与 DecodedContent，由 Engine 把原文拼入 instruction 文本
```

> 附件不写文件系统：不再有 `memory/{session_id}/attachments/` 目录，也不会在 ChatRecord 里持久化附件正文。如需 LLM 解析结构化内容，由对应 MCP 工具在请求生命周期内直接处理 base64。

> 附件校验错误码、API 认证与鉴权设计（API Key、权限定义）已抽取至 [API 设计文档](2026-05-16-api-design.md)。

---

## 四、Intelligence Layer（智能层）

### 4.1 Agent Engine

Agent Engine（[engine.go](internal/agent/engine.go)）封装 eino `adk.ChatModelAgent`，是 Groot 内 ReAct 执行的核心。

#### 4.1.1 入口与构造

`Engine.Run` 由 `Executor.Execute`（[executor.go](internal/agent/executor.go)）调用。每次执行：

1. 解析实际生效的 `model` 名（`X-Model-Name` → `LLMConfig.DefaultModel`），写入 `ctx`，使 `call_agent` 透传给子 Agent
2. 通过 [llm.NewChatModel](internal/llm/chatmodel.go) 创建带 `step_timeout` 的 OpenAI 兼容 ChatModel
3. `Engine.buildTools` 装配工具集（MCP 工具 + 编排模式下的 `call_agent`）
4. `Engine.buildSystemInstruction` 拼接 system prompt
5. 用 `agentConfig.Handlers` 注入 skill 中间件（编排模式）或子 Agent 自带的 skill 中间件（Solo 模式）
6. `agentConfig.ModelRetryConfig.MaxRetries` 来自 `react.error_retry`，覆盖瞬时网络抖动 / 5xx / 超时
7. 创建 `adk.NewChatModelAgent` + `adk.NewRunner`（开启 streaming）
8. `Engine.buildMessageList` 把历史 + 当前 user message 组装为 `[]adk.Message`
9. `runner.Run` 拿到事件流，进入事件循环

#### 4.1.2 ReAct 执行模式

eino `ChatModelAgent` 内部按 ReAct 模式循环：模型推理（含工具调用决策）→ 工具执行 → 结果反馈 → 再推理。Engine 只是把这个事件流折叠成 SSE 推送：

```
Runner.Run() 返回 AgentEvent 流
│
├─ event.AgentName 区分主 / 子 Agent；主 Agent 自身事件折叠为空 agent_name，子 Agent 透传名字（编排模式 EmitInternalEvents=true 时）
│
├─ event.Err 分支
│   ├─ 含 "mcp" / "command_not_allowed" → 写 tool_result(isError=true)，循环继续
│   ├─ 含 "connection refused" / "dial tcp" / "no such host" / "timeout" → 写 error 并返回致命错误
│   └─ 其他 NodeRunError → 写 error，循环继续
│
├─ msgOutput.Role == schema.Assistant
│   ├─ 流式分支（IsStreaming + MessageStream.Recv 直到 EOF）
│   │   ├─ ReasoningContent 非空 → WriteThinking
│   │   ├─ Content 非空且 ReasoningContent 为空 → WriteMessage（同时累加 finalResult）
│   │   ├─ ToolCalls 非空 → 过滤掉空名+空ID+空参的 streaming artifact 后 WriteToolCalls，记一条 running step
│   │   └─ ResponseMeta：FinishReason 写 finish；Usage 累加 token（每个 chunk 都加，不限于 finish）
│   └─ 非流式分支（msgOutput.Message != nil）：一次性处理上述四类
│
├─ msgOutput.Role == schema.Tool
│   └─ processToolEvent：写 tool_result，按 toolCallID 把对应 step 翻为 completed
│
└─ 循环结束 → WriteDone（[DONE]）
```

事件循环用 `eventCh` + 独立读取 goroutine 处理，让 `ctx.Done()`（SSE 客户端断开 → 父 ctx 取消）能及时打断。

#### 4.1.3 循环终止条件

| 条件 | 触发位置 | SSE 表现 |
|------|---------|---------|
| 模型生成最终答案 | finish_reason=stop / tool_calls 空 | `finish` + `[DONE]` |
| `MaxIterations` 上限 | eino ChatModelAgent 内部 | `error`（来源于 NodeRunError）+ `[DONE]` |
| 单次 LLM 调用超时 | `react.step_timeout` 作为 ChatModel.Timeout | NodeRunError → 命中 LLM 连接错误分支，致命错误 + `[DONE]` |
| 客户端断开（SSE 取消） | `ctx.Err() == context.Canceled` | 直接走 `agentCancelled` 分支：`[DONE]` |
| 致命 LLM 错误 | connection refused / dial tcp / no such host / timeout | `error` + 返回 error 终止 Run |

> 事件循环没有"最大 token"硬终止逻辑：token 用量只用于累加和后续 ChatRecord 持久化。上下文规模的实际约束来自两处：`memory.history_window`（按轮数截断）和模型的 `max_context_tokens`（按 token 预算截断）。

#### 4.1.4 客户端断开处理

Engine 没有专门的"取消 API"。客户端关闭 SSE 连接 → Hertz 取消 request ctx → `Engine.Run` 在事件循环顶层 `select` 命中 `ctx.Done()` → 把 `agentCancelled = true`、写 `[DONE]`、返回 `RunResult{Cancelled: true}`，Executor 据此把 ChatRecord 落库为 `cancelled` 状态。

#### 4.1.5 Solo 与编排两种模式

Executor 根据 `task.AgentName` 区分两种模式（[executor.go](internal/agent/executor.go)）：

| 维度 | 编排模式（默认） | Solo 模式 |
|---|---|---|
| 触发 | `X-Agent-Name` 不传或传 `groot` | `X-Agent-Name` 传已注册的子 Agent 名 |
| `agentName` | `MainAgentName`（"groot"） | 子 Agent 名（用于事件归属与 SSE `agent_name`） |
| MCP Manager | 全局 `mcpManager` | 子 Agent 自带的 `entry.MCPManager` |
| middlewares | 全局 skill middleware | 子 Agent 的 `entry.SkillBK` 重新构建 skill middleware（失败降级为无 skill） |
| `extraTools` | 注入 `call_agent`（仅在 SubAgentRegistry 非 nil 时） | 不注入 `call_agent`（Solo 不再嵌套调度） |
| `EmitInternalEvents` | true | false |
| 系统指令首段 | `GROOT.md` | 子 Agent 的 `agent.md` 正文 |
| `model` 选择 | `task.ModelName` → `default_model` | 子 Agent `agent.md.model` 优先于 `task.ModelName` |

### 4.2 Skills

Skills 通过 SKILL.md 文件以自然语言定义技能，由 eino 的 `einoskill` 中间件接入。

**核心能力：**

| 能力 | 说明 |
|------|------|
| 声明式定义 | 通过 SKILL.md（YAML frontmatter + Markdown）描述技能 |
| 自动发现 | `einoskill.NewBackendFromFilesystem` 扫描 `{GROOT_HOME}/skills/{name}/SKILL.md` |
| Progressive disclosure | 启动时把 skill 名 + 描述拼成"可用 Skill"摘要表注入 system prompt；正文按需通过 skill 工具加载 |
| 符号链接支持 | `filesystem.SymlinkBackend` 包装本地后端，支持 skill 目录是符号链接 |
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

Memory 模块负责会话数据的持久化存储。基于 `repo.MemoryRepo` 接口落到数据库（SQLite / MySQL / PostgreSQL）。

**核心设计：**
- **两表结构**：`memory_sessions`（会话元数据）+ `memory_chats`（每轮对话的结构化记录）
- **历史按需聚合**：不存独立的 history.json，每次 LLM 上下文构建时通过 `LoadHistory` 从 `memory_chats` 实时聚合
- **全量保存、按需截断**：DB 中保存全部轮次，传递给 LLM 的上下文只取最近 N 轮
- **事务原子性**：`SaveChat` 在事务内完成 `INSERT memory_chats + UPDATE memory_sessions.round`，乐观锁防并发同 round 冲突
- **定时清理**：会话过期清理由 gocron 统一调度（调度由 Schedule 模块管理）

> 详细设计见 [Memory 模块设计](2026-05-11-memory-design.md)。

### 4.5 Runtime State

#### 4.5.1 数据结构

```go
// ActiveChat 活跃对话状态（仅在内存中）
type ActiveChat struct {
    SessionID string        `json:"session_id"`
    ChatID    string        `json:"chat_id"`
    Status    string        `json:"status"`      // running, cancelled, completed
    Progress  *ChatProgress `json:"progress"`
    StartTime time.Time     `json:"start_time"`

    mu sync.RWMutex `json:"-"`
}

// ChatProgress 对话进度
type ChatProgress struct {
    CurrentStep    int                `json:"current_step"`
    StepsCompleted int                `json:"steps_completed"`
    Percentage     int                `json:"percentage"`
    SubAgents      []SubAgentProgress `json:"sub_agents,omitempty"`
}

// SubAgentProgress 单个子 Agent 的运行时状态
type SubAgentProgress struct {
    Name   string `json:"name"`
    Status string `json:"status"`
}
```

#### 4.5.2 接口定义

```go
type RuntimeState struct {
    activeChats sync.Map // session_id -> *ActiveChat
}

func NewRuntimeState() *RuntimeState
func (r *RuntimeState) Register(sessionID, chatID string) (*ActiveChat, error)
func (r *RuntimeState) Get(sessionID string) (*ActiveChat, bool)
func (r *RuntimeState) UpdateProgress(sessionID string, progress *ChatProgress) error
func (r *RuntimeState) Delete(sessionID string)                // 移除活跃状态（goroutine 收尾时 defer）
func (r *RuntimeState) IsRunning(sessionID string) bool
func (r *RuntimeState) RunningCount() int
func (r *RuntimeState) SnapshotProgress(sessionID string) *ChatProgress
func (r *RuntimeState) AddSubAgent(sessionID, name string)
func (r *RuntimeState) RemoveSubAgent(sessionID, name string)
```

**职责边界：**

RuntimeState 只负责**活跃对话状态管理**（注册、查询、清理、子 Agent 运行状态记账），**不参与数据持久化**。对话结果持久化由 `Executor.Execute` 直接写 Memory。

| 方法 | 说明 |
|------|------|
| `Register` | LoadOrStore 原子注册活跃对话；session 已存在活跃对话时返回错误 |
| `Get` | 取出 ActiveChat 引用；不存在返回 false |
| `UpdateProgress` | 在 mu 保护下更新 Progress 引用 |
| `SnapshotProgress` | mu 持锁深拷贝 Progress + SubAgents，handler 序列化期间不会被并发写污染 |
| `Delete` | 直接 `sync.Map.Delete`；不返回任何 ChatRecord |
| `IsRunning` | 仅检查是否存在，不读取内部字段 |
| `RunningCount` | 遍历 `sync.Map` 累加，O(N) |
| `AddSubAgent` | mu 持锁 copy+append（不复用底层数组），CallAgentTool 进入时调用 |
| `RemoveSubAgent` | mu 持锁过滤新 slice，CallAgentTool 退出 defer 调用 |

#### 4.5.3 并发控制

**同一会话并发限制：**

ChatHandler 在两处做并发预检：

```
POST /chat（已知 sessionID）:
  │
  ├─ 1) RuntimeState.IsRunning(sid) → 提前返回 409，规避竞态
  │
  ├─ 2) RuntimeState.Register(sid, chat_id)
  │       └─ LoadOrStore 落空 → 返回 409 chat_limit_exceeded
  │
  └─ 3) goroutine 内 defer RuntimeState.Delete(sid) 释放
```

#### 4.5.4 与 Memory 协作

```
对话生命周期：
  │
  ├─ 1. POST /chat 请求
  │     ├─ RuntimeState.IsRunning(sid) 提前检查
  │     ├─ RuntimeState.Register(session_id, chat_id)（LoadOrStore）
  │     └─ 启动 goroutine 执行
  │
  ├─ 2. 执行过程
  │     ├─ CallAgentTool.AddSubAgent / RemoveSubAgent 标记子 Agent 运行状态
  │     └─ 客户端断开 SSE 连接 → request ctx 取消 → Engine.Run 走 cancelled 分支
  │
  ├─ 3. 执行完成（Executor.Execute 主体后段）
  │     ├─ Executor 构造 ChatRecord（status / result / steps / tokens / model）
  │     ├─ Memory.SaveChatRecord（事务内 INSERT memory_chats + UPDATE memory_sessions.round）
  │     └─ Memory.AppendMessage（DB 模式 no-op）
  │
  ├─ 4. goroutine 收尾 defer RuntimeState.Delete(sid)
  │
  └─ 5. 历史查询
        ├─ GET /sess/{sid}      → Memory.GetHistory
        ├─ GET /chat/{sid}      → Memory.GetLatestChatRecord
        └─ GET /chat/{sid}/{cid} → Memory.GetChatRecord
```

### 4.6 Sub-Agents（多 Agent 编排）

子 Agent 让主 Agent 把专项任务委托给独立配置的子 Agent 执行。

**核心能力：**

| 能力 | 说明 |
|------|------|
| 声明式注册 | `{GROOT_HOME}/subagents/{name}/agent.md` 用 YAML frontmatter（description / model / temperature / max_tokens）+ Markdown 正文 |
| 独立资源 | 每个子 Agent 有自己的 MCP Manager 和 Skills Backend |
| 并发控制 | 全局 `subagent.max_concurrency` semaphore；子 Agent 调度严格 FIFO 排队 |
| 工具入口 | 主 Agent 通过内置 `call_agent(agent_name, task)` 工具调度（[call_agent.go](internal/agent/call_agent.go)） |
| 模型透传 | 默认跟随主 Agent 当前 model；agent.md 显式声明 `model` 时优先使用 |
| 截断保护 | `subagent.max_task_length` 限制 task 入参；`subagent.max_result_length` 截断子 Agent 输出（带警告横幅） |
| 独立持久化 | 每次调用独立写入子 ChatRecord（`agent_name = <子 Agent 名>`），含 token / steps / model / status |

**两种调用方式：**

| 模式 | 入口 | agentName | call_agent 是否可用 |
|------|------|----------|--------------------|
| 编排（默认） | `X-Agent-Name` 不传 / 传 `groot` | `MainAgentName` | 可用（自动挂载） |
| Solo | `X-Agent-Name` 传子 Agent 名 | 子 Agent 名 | 不挂载（避免无限嵌套） |

> 详细设计见 [多 Agent 设计文档](2026-05-24-multi-agent-design.md)。

### 4.7 Schedule（定时任务调度）

定时任务调度模块，用户通过对话创建定时任务，系统在指定时间自动执行 Agent 指令。

**核心能力：**

| 能力 | 说明 |
|------|------|
| 任务 CRUD | 通过 8 个内置工具（`schedule_create/list/inspect/disable/enable/archive/history/delete`）由主 Agent 调用，REST 接口暴露在 `/schedule/*` |
| 持久化 | 任务定义与执行历史落 `schedule_tasks` / `schedule_executions` 表 |
| gocron 调度 | 统一管理所有系统定时任务（含 Memory `Cleanup` 与目录同步），单 goroutine 轮询 |
| Task Runner | 定时触发时通过 `Schedule.Runner` 复用 `agent.Executor` 执行 |
| 状态管理 | `active` / `disabled` / `archive` 三态切换 |
| Leader 限定 | 仅集群 Leader 启动调度引擎与工具注册，避免多节点重复触发 |

> 详细设计见 [定时任务调度系统设计](2026-05-11-schedule-design.md)。

### 4.8 Message（消息层）

消息通知层，所有需要通知的场景统一通过消息层发布事件，由消息层负责路由分发，不经过 LLM。

**核心能力：**

| 能力 | 说明 |
|------|------|
| 事件发布 | 调用方构造 Event 入队，由 worker 异步路由 |
| 渠道过滤 | 仅向 `senders.*.enabled=true` 的渠道分发 |
| 并发发送 | 每个渠道独立 goroutine 调用 Sender |
| 内置 Sender | `webhook` / `email` / `stdout`（[internal/message/senders/](internal/message/senders)） |
| 结果记录 | 各渠道返回 `SendResult`，由调用方写入执行记录或日志 |

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
| 数据库连接 | `~/.groot/env.yaml`（与 `config.yaml` 解耦，支持 `${VAR}` 环境变量展开） |

#### 5.1.2 配置加载

启动时由 `config.Load(homeDir)` 一次性读取 `config.yaml` + `env.yaml`，与默认值合并、做参数范围校验、展开环境变量。修改任何配置项都需要重启服务才能生效；运行期生效的"动态发现"仅限 Skills 文件系统扫描的按需 List 行为。

### 5.2 Logger

#### 5.2.1 日志配置

```yaml
logging:
  level: info                      # 日志级别：debug/info/warn/error
  format: json                     # 日志格式：json/text
  output: [stdout, file]           # 输出目标：stdout/file（可同时输出）
  file:
    directory: logs                # 日志文件目录（相对路径基于 GROOT_HOME）
    filename_pattern: groot-{date}.log  # 文件名模式，{date} 替换为 YYYY-MM-DD
    max_age: 7                     # 日志保留天数
```

**配置字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `level` | string | 否 | 日志级别，默认 `info` |
| `format` | string | 否 | 输出格式，`json` 或 `text`，默认 `json` |
| `output` | array | 否 | 输出目标列表，默认 `[stdout, file]` |
| `file.directory` | string | 否 | 日志目录，默认 `logs`；相对路径基于 `GROOT_HOME` 解析 |
| `file.filename_pattern` | string | 否 | 文件名模式，默认 `groot-{date}.log` |
| `file.max_age` | int | 否 | 保留天数，默认 7 |

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

### 5.3 Health

`GET /health` 由 [internal/api/handler/health.go](internal/api/handler/health.go) 提供，单接口同时承担存活探针和依赖健康检查；不需要鉴权。

#### 5.3.1 响应结构

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm":         {"status": "healthy", "info": {"model": "gpt-4o"}},
    "mcp_servers": {"status": "healthy", "info": [{"name": "file_operations", "type": "stdio", "tools_count": 7, "isActive": true}]},
    "skills":      {"status": "healthy", "info": {"count": 4}},
    "memory":      {"status": "healthy", "info": {"sessions": 10}}
  },
  "metrics": {
    "chats_running": 1
  }
}
```

#### 5.3.2 依赖健康检查

| 检查项 | 检查方式 | 说明 |
|-------|---------|------|
| `llm` | [llm.CheckConnection](internal/llm/chatmodel.go) → 调用 `/v1/models` | 验证 default_model 的 base_url 可达 + API key 鉴权有效 |
| `mcp_servers` | 遍历 `mcp.Manager.ListWithToolCount()` | 列出每个 MCP Server 的 type / 工具数 / `isActive` / 错误（若有） |
| `skills` | `einoskill.Backend.List` 计数 | 统计已发现的 skill 数 |
| `memory` | `memory.Manager.ListSessions(1, 0)` 取 total | 统计会话总数，验证数据库可读 |
| `metrics.chats_running` | `RuntimeState.RunningCount()` | 当前活跃对话数 |

LLM 异常时 `checks.llm.info.error` 会带具体错误（如 `connection failed: timeout` / `authentication failed`），整体 `status` 字段仍返回 `healthy`（因为只是依赖标注），用调用方按需自行决定是否报警。

---

## 六、性能与并发

### 6.1 ReAct 执行限制

```yaml
react:
  max_iterations: 20          # ChatModelAgent 最大迭代次数（透传到 eino）
  step_timeout: 60            # 单次 LLM API 调用超时（秒）
  error_retry: 2              # ChatModel 瞬时错误（5xx / 网络抖动 / 超时）的重试次数
```

`react` 节的三个配置项全部生效，没有仅作占位的字段：

| 配置项 | 实际效果 | 默认值 |
|--------|---------|--------|
| `max_iterations` | 通过 `adk.ChatModelAgentConfig.MaxIterations` 透传给 eino，作为 ReAct 循环上限 | 20 |
| `step_timeout` | 作为 `openai.ChatModelConfig.Timeout`，控制每次 LLM HTTP 调用的最长时间 | 60 |
| `error_retry` | `> 0` 时启用 `adk.ModelRetryConfig{MaxRetries}`，对 LLM 瞬时错误自动重试 | 2 |

### 6.2 错误处理

Engine 事件循环按错误内容分流：

| 错误特征 | 处理 |
|---------|------|
| 错误信息含 `mcp` 或 `command_not_allowed` | 写 `tool_result(isError=true)`，循环继续 |
| 错误信息含 `connection refused` / `dial tcp` / `no such host` / `timeout` | 写 `error` 事件并结束 Run，Executor 标 `failed` |
| 其他 `NodeRunError` | 写 `error` 事件后继续循环 |

LLM 瞬时错误重试由 `react.error_retry` 控制，由 eino `ModelRetryConfig` 实现，无需业务层显式配置间隔。MCP 工具错误以 `tool_result` 形式回流给 LLM，不会自动重试。

---

## 七、部署与运维

### 7.1 启动参数

无子命令时启动 HTTP 服务；带子命令时进入对应 CLI。

| 参数 | 缩写 | 说明 | 默认值 |
|------|------|------|--------|
| `--port` | `-p` | HTTP 端口 | 配置文件值 |
| `--help` | `-h` | 显示帮助 | - |
| `--version` | `-v` | 显示版本 | - |

**支持的子命令（[main.go](cmd/groot/main.go)）：**

| 子命令 | 功能 |
|------|------|
| `init` | 初始化 `~/.groot` 工作目录与默认配置 |
| `status` | 查看运行实例状态 |
| `skills` | Skills 管理（list / install / uninstall） |
| `mcp` | MCP Servers 管理（list） |
| `schedule` | 定时任务管理（list / inspect / history / delete / disable / enable / archive） |
| `chat` | 启动交互式聊天 TUI（HTTP+SSE 连接到 groot 服务） |
| `tail` | 实时日志查看（支持 `-n` / `-l` / `-k` 过滤） |
| `push` / `pull` / `diff` | MySQL/PG 模式下与 `shared_resources` 表的双向同步 |

### 7.2 环境变量

| 变量 | 说明 | 必需 |
|------|------|------|
| `GROOT_HOME` | 工作目录，默认 `~/.groot` | 否 |
| `OPENAI_API_KEY` | LLM API 密钥，配合 `${OPENAI_API_KEY}` 引用 | 否（也可在配置文件直填） |
| `GROOT_API_KEY` | 默认 API Key 配置中引用的认证密钥 | 是（`security.auth.enabled=true` 时） |
| `ANTHROPIC_API_KEY` | Anthropic 系列模型 API 密钥 | 否 |

### 7.3 优雅关闭

收到 `SIGINT` / `SIGTERM` 后，按以下顺序退出：

1. 集群离线（`cluster.Leave`），把当前节点从 `cluster_members` 移除并触发 leader 重选
2. 停止 HTTP 服务，等待飞行中的请求完成（30 秒上限）
3. 关闭消息层 worker（停止队列消费）
4. 关闭 MCP 客户端连接
5. 关闭子 Agent 注册表（释放每个子 Agent 自带的 MCP）
6. 调度器（若是 Leader）通过 `stopLeaderTasks` 回调关闭
7. zap logger Sync，进程退出

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
  max_iterations: 20               # ChatModelAgent 最大迭代次数
  step_timeout: 60                 # LLM API 单次调用超时（秒）
  error_retry: 2                   # ChatModel 瞬时错误重试次数

# 附件处理配置
attachment:
  max_size: 50                     # 单个附件最大大小（MB）
  max_total_size: 100              # 附件总大小上限（MB）
  max_count: 10                    # 附件数量上限
  allowed_types: []                # 允许的扩展名（空数组 = 允许全部）

# 记忆模块配置
memory:
  history_window: 20               # LLM 上下文窗口（轮次数），-1 不限制

# 子 Agent 调度配置
subagent:
  max_concurrency: 5               # 全局 semaphore 大小（FIFO 排队）
  exec_timeout: "5m"               # 子 Agent 执行超时（排队不计入）
  max_task_length: 16000           # task 参数最大字符数
  max_result_length: 8000          # 子 Agent 返回文本截断长度

# 安全配置
security:
  auth:
    enabled: false                 # 是否开启认证
    type: api_key                  # 认证类型
    api_key:
      header_name: X-API-Key       # 认证 Header 名称
      keys:
        - name: default            # Key 名称（唯一标识）
          key: ${GROOT_API_KEY}    # Key 值（支持环境变量引用）
          permissions: [all]       # 权限范围：[all] 或 [chat, status, ...]
  rate_limit:
    enabled: false                 # 是否启用速率限制
    global_qps: 0                  # 全局 QPS（0 = 不限制）
    global_concurrency: 0          # 全局并发（0 = 不限制）
    default_qps: 10                # 每个 API Key 默认 QPS
    default_concurrency: 5         # 每个 API Key 默认并发
    cleanup_interval: 5m           # 空闲限流器清理间隔

# 定时任务配置
schedule:
  enabled: false                   # 是否允许 Agent 创建定时任务（不影响系统级清理任务）
  max_concurrent_tasks: 3          # 最大并发执行数
  sync_interval: 30s               # 任务列表同步间隔

# 消息通知配置
message:
  queue_size: 256                  # 发送队列容量
  workers: 2                       # 发送工作协程数
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
    directory: logs                # 日志文件目录（相对 GROOT_HOME）
    filename_pattern: groot-{date}.log  # 文件名模式
    max_age: 7                     # 日志保留天数
```

> 数据库连接（`driver` / `dsn` / 连接池等）通过 `~/.groot/env.yaml` 单独配置，不在 `config.yaml` 中。详见 [数据库后端设计 §1.5](2026-06-10-database-backend-design.md#15-envyaml-配置格式)。

**固定目录说明：**

以下目录位置固定，不可配置：
- `{GROOT_HOME}/skills` - Skills 定义目录
- `{GROOT_HOME}/mcp` - MCP 配置目录
- `{GROOT_HOME}/subagents` - 子 Agent 定义目录

---

## 九、迭代说明

### 9.1 配置项清理（2026-09-01）

对配置定义、`groot init` 生成的模板与代码实际引用做了一致性核对，移除了不再生效的配置项，补齐了缺失的配置节。

**移除：**

| 配置项 | 移除原因 |
|--------|---------|
| `react.max_tokens` | 除 config 包自身外无任何代码引用，事件循环从不据此终止；上下文规模由 `memory.history_window` 与模型 `max_context_tokens` 共同约束 |
| `react.nesting_max_depth` | 子 Agent 不具备 `call_agent` 工具，嵌套深度恒为 1，该字段无检查点 |
| `memory.directory` | 会话数据整体迁入数据库，`groot init` 已不创建 `memory/` 目录，字段填了默认值后无人读取 |
| `memory.retention_days` / `memory.cleanup_schedule` | 仅存在于本文档，`MemoryConfig` 中从未定义 |
| `Memory.GetMemoryDir()` | 数据库模式下恒返回空字符串的兼容签名，无生产调用方 |
| 模板中"存储抽象层配置"注释段 | 指向 `env.yaml` 中已不存在的 MinIO 开关；`push/pull/diff` 现走数据库同步 |

**新增：**

- 配置模板补充 `message` 节（队列容量、工作协程数、webhook / email 发送器）。该配置在 `main.go` 中用于注册定时任务的通知渠道，此前仅有 README 记录，生成的配置文件中不可见。
- `applyDefaults()` 补充 `memory.history_window` 默认值兜底。此前用户未显式配置时该值为 0，`windowSize > 0` 判断不成立，导致轮数不做截断，与文档声明的默认 20 轮不符。

**调整：**

- `groot --help` 中 `push` / `pull` / `diff` 的说明由"MinIO（minio 模式）"改为"数据库（MySQL/PG 模式）"，与实际行为一致。
- README 目录说明移除会话数据文件目录表项，明确会话、对话历史、附件内容均存于数据库。

---

## 附录

### A. Skill 示例

> 详见 [Skills 设计文档 - 七、Skill 示例](2026-05-10-skills-design.md#七skill-示例)。

> 错误码速查表已抽取至 [API 设计文档](2026-05-16-api-design.md)。

### C. 文件路径与数据存放约定

**本地文件系统**

| 路径 | 说明 |
|------|------|
| `{GROOT_HOME}/config.yaml` | 配置文件 |
| `{GROOT_HOME}/env.yaml` | 节点本地数据库连接凭据（可选） |
| `{GROOT_HOME}/GROOT.md` | 全局系统提示词 |
| `{GROOT_HOME}/skills/{name}/SKILL.md` | Skill 定义文件（固定位置） |
| `{GROOT_HOME}/mcp/{name}.json` | MCP 配置文件（固定位置） |
| `{GROOT_HOME}/subagents/{name}/agent.md` | 子 Agent 入口文件（固定位置） |
| `{GROOT_HOME}/groot.db` | SQLite 模式下的本地数据库（不参与同步） |
| `{logDir}/groot-{date}.log` | 日志文件 |

**数据库表（详见 [数据库后端设计](2026-06-10-database-backend-design.md)）**

| 表 | 说明 |
|---|---|
| `cluster_members` | 集群成员注册 / 心跳 |
| `schedule_tasks` / `schedule_executions` | 定时任务定义 / 执行历史 |
| `memory_sessions` / `memory_chats` | 会话元数据 / 单轮对话记录 |
| `shared_resources` | MySQL/PG 模式下集群共享配置的远端权威副本（push/pull/diff 同步对象） |

**说明：**
- `{logDir}` 由 `logging.file.directory` 配置决定，默认 `{GROOT_HOME}/logs`
- 附件不写文件系统，仅在请求生命周期内驻留（详见 [附件设计 §1.5](2026-06-05-attachment-and-session-rules-design.md)）
- 会话元数据 / 单轮对话记录 / 集群成员 / 定时任务全部落数据库；本地文件系统只存配置、文档、Skills/MCP/SubAgent 资产、日志和 SQLite 数据库