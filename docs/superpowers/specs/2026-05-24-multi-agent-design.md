# Groot 子 Agent 设计

| 属性 | 内容 |
|------|------|
| 版本 | v3.8 |
| 日期 | 2026-05-27 |
| 状态 | 设计中，待评审 |

## 一、概述

### 1.1 背景

当前 Groot 所有 `/chat` 请求共享同一套 MCP 工具、Skills 和 GROOT.md。虽然上层应用可以通过 `instruction` 参数和 `X-Model-Name` header 区分角色，但无法做到**工具隔离**——每个角色都能看到全部工具和全部 Skill。

### 1.2 目标

引入两种 Agent 概念：

- **主 Agent（即 Groot 自身）**：由 `{GROOT_HOME}/GROOT.md` + 全局 `skills/` + 全局 `mcp/` 定义，是默认 Agent。
- **子 Agent（Sub-Agent）**：采用「文件系统即配置」方式定义，`{GROOT_HOME}/subagents/{name}/agent.md` 即 Agent 定义文件——frontmatter 声明身份（`description`），正文为系统提示词。目录内存放该 Agent 专属的 `skills/`、`mcp/`。

使用方式：

- `/chat` 请求 header 中未携带 `X-Agent-Name` 字段 → 使用主 Agent（即现有行为，向后兼容）
- `/chat` 请求 header 中携带 `X-Agent-Name` 字段 → 使用指定子 Agent 独立执行（Solo 模式）
- 主 Agent 可通过 `call_agent` 工具调用子 Agent，子 Agent 通过 `subagents/` 目录注册

### 1.3 两种使用模式

| 模式 | 触发方式 | 行为 |
|------|---------|------|
| **Solo 模式** | `X-Agent-Name: db-agent` | 仅使用指定子 Agent 执行，不挂载 call_agent |
| **编排模式** | 不传 `X-Agent-Name` | 主 Agent 通过 `call_agent` 工具发现/调度子 Agent |

### 1.4 非目标

- **不实现子 Agent 之间的互相调用**：只支持主 Agent 调用子 Agent。子调子会引入循环风险和图复杂度，与我们「主拆解 → 子执行 → 主汇总」的场景不匹配。
- **不引入 eino 的 flowAgent/多 Agent 图**：flowAgent 要求所有 Agent 在编译期注册到图中，与「文件系统即配置」启动期发现机制冲突。我们的调用关系仅一层（主 → 子），工具调用模式（call_agent）已足够。
- **不改变现有全局 `skills/`、`mcp/`、`GROOT.md` 的行为**：向后兼容，不传 `X-Agent-Name` 时行为与现在完全一致。

---

## 二、目录结构与隔离规则

### 2.1 目录结构

```
{GROOT_HOME}/                       # 默认 ~/.groot
├── config.yaml                     # 主配置
├── GROOT.md                        # 全局系统提示词
├── skills/                         # 全局 Skills（主 Agent 用）
├── mcp/                            # 全局 MCP 配置（主 Agent 用）
├── subagents/                      # ★ 新增：子 Agent 定义目录
│   ├── db-agent/
│   │   ├── agent.md               # 定义文件（frontmatter + 正文）
│   │   ├── skills/                # 专属 Skills（可空）
│   │   └── mcp/                   # 专属 MCP 配置（可空）
│   ├── weather-agent/
│   └── code-agent/
├── memory/                         # 会话存储
│   └── {session_id}/
│       ├── SESSION.md
│       ├── history.json
│       ├── attachments/
│       └── chats/
│           ├── chat_20260524103000523.json                          # 主 Agent
│           └── chat_20260524103000523_103001523_a3f8_db-agent.json  # 子 Agent
├── logs/
└── cluster/
```

### 2.2 chat 文件命名

| 角色 | 格式 |
|------|------|
| 主 Agent | `chat_{YYYYMMDDHHMMSSmmm}.json` |
| 子 Agent | `chat_{主ts}_{HHMMSSmmm}_{random4}_{agentName}.json` |

> 子时间戳 `HHMMSSmmm` 不含年月日（父 chatID 中已有完整日期）。**`random4` 是 4 位随机后缀，避免同一毫秒内并发调用同一个子 Agent 产生重名**（毫秒粒度 + `MaxConcurrency=5` 默认值下完全可能）。文件名直接体现调用时序和父子关系（前缀匹配），无需额外 `parent_chat_id` 字段。

### 2.3 主 Agent 名常量

主 Agent 名集中定义于 `internal/agent/consts.go`：

```go
package agent

// 主 Agent 名。需与 ChatModelAgent 的 Name 字段保持一致。
// 启动期扫描 subagents/ 时，若发现同名目录，跳过并报错日志（保留主名独占）。
const MainAgentName = "groot"
```

> **现状对齐**：当前 [engine.go:92](internal/agent/engine.go) 使用 `Name: "GrootAgent"`，本设计落地时需统一改为 `MainAgentName`，否则事件循环按 `event.AgentName == MainAgentName` 判定主/子时会全部误判为子 Agent。

### 2.4 集群部署说明

Groot 集群所有实例共享同一份 `GROOT_HOME`/`subagents/`，因此不存在配置同步问题——任一实例修改 `subagents/{name}/agent.md` 后，其他实例**重启**才能看到新内容（agent.md 不支持热加载，详见 [5.3 节](#53-热加载策略)）。`SubAgentRegistry` 是每实例自己的本地内存缓存。

### 2.5 Agent 目录约定

| 路径 | 必填 | 说明 |
|------|------|------|
| `subagents/{name}/` | 是 | 目录名即 Agent 名，唯一 |
| `subagents/{name}/agent.md` | 是 | 子 Agent 定义文件，YAML frontmatter（`description` 必填）+ Markdown 正文 |
| `subagents/{name}/skills/` | 否 | 子 Agent 专属 Skills，不存在则无 Skill |
| `subagents/{name}/mcp/` | 否 | 子 Agent 专属 MCP 配置，不存在则无 MCP 工具 |

### 2.6 隔离规则（权威表）

**核心原则：子 Agent 是完全隔离的。子 Agent 的 agent.md / skills/ / mcp/ 都是私有的，主 Agent 不访问。**

| 资源 | Solo 模式 | 编排模式（主 Agent） | 编排模式（子 Agent） |
|------|-----------|---------------------|----------------------|
| 系统提示词 | `agent.md` + SESSION.md + Request.prompt | GROOT.md + SESSION.md + Request.prompt | 仅 `agent.md` |
| MCP 工具 | 仅 `subagents/{name}/mcp/` | 全局 `mcp/` | 仅 `subagents/{name}/mcp/` |
| 内置工具 | 无 schedule、无 call_agent | 全局 builtin（schedule 等）+ `call_agent` | 无 schedule、无 call_agent |
| Skills | 仅 `subagents/{name}/skills/` | 全局 `skills/` | 仅 `subagents/{name}/skills/` |
| 模型 | `X-Model-Name` → `agent.md.model` → `llm.default_model` | `X-Model-Name` → `llm.default_model` | `agent.md.model` → `llm.default_model` |
| SESSION.md | 可读写，chatID 不含父前缀 | 可读写 | 不访问 |
| HistoryMessages | 完整 session 历史 | 完整 session 历史 | 空切片（无状态） |
| ChatRecord | 写入 `chats/`，chatID 不含父前缀 | 写入 `chats/` | 写入同一 `chats/`，chatID 含父前缀 |
| Request.prompt | 拼入系统指令 | 拼入系统指令 | 不透传 |

> **call_agent 返回值可见性**：子 Agent 的执行过程（thinking、内部 tool_calls 等）通过 SSE 透传给客户端，但对主 Agent 的 LLM 而言，`call_agent` 就是一个普通工具——入参是 `agent_name` + `task`，返回值是最终文本结果。子 Agent 内部的工具调用细节对主 Agent 不可见。

### 2.7 注册方式

子 Agent 通过 `subagents/` 目录**启动期一次性扫描**注册：

- **注册条件**：目录存在 + `agent.md` 存在 + frontmatter 含非空 `description`
- **无效跳过**：缺少 frontmatter 或 `description` 为空 → 启动日志报错，跳过该子 Agent
- **运行时只读**：`SubAgentRegistry` 启动后不再扫描，新增/修改/删除子 Agent 需**重启服务**（与 MCP 一致）
- **集合稳定**：`call_agent` 执行时若查找不到 → 返回错误（与 MCP server 离线时的行为一致）

---

## 三、agent.md 格式

### 3.1 frontmatter 字段

| 字段 | 必填 | 说明 |
|------|------|------|
| `description` | 是 | Agent 能力描述。**缺失则启动时跳过该子 Agent**。直接出现在 `call_agent` 工具描述中，是 LLM 调度的唯一依据 |
| `model` | 否 | 子 Agent 默认使用的模型。优先级见 [2.6 节](#26-隔离规则权威表) |
| `temperature` | 否 | LLM 采样温度，0.0~2.0，不设则继承模型默认值 |
| `max_tokens` | 否 | LLM 最大输出 token 数，不设则继承模型默认值 |

### 3.2 示例

```markdown
---
description: 数据库查询 Agent，支持 MySQL 和 PostgreSQL 的 SELECT 查询、表结构查看、执行计划分析。不支持数据修改。
---

# 数据库查询 Agent

## 适用场景
- 查询统计数据
- 联表查询
- 表结构查看

## 不适用场景
- DDL 变更（CREATE/ALTER/DROP）
- 数据插入/更新/删除

## 核心规则
1. 查询前必须先了解表结构（使用 DESCRIBE 或 SHOW CREATE TABLE）
2. 只执行 SELECT 查询，绝对不修改数据
3. 查询结果用 Markdown 表格展示
4. 大数据量查询时先 COUNT 估算，超过 1000 行建议分页
```

### 3.3 description 编写规范

1. **明确能力边界**：不是「帮助用户」，而是「MySQL/PostgreSQL 数据库查询，支持 SELECT、EXPLAIN、DESCRIBE，不修改数据」
2. **在正文中列出适用/不适用场景**：帮助 LLM 快速判断
3. **避免 Agent 间能力重叠**：多个 Agent 的 description 应差异化。若能力重合，LLM 会随机选一个，结果不可预期

---

## 四、call_agent 工具

### 4.1 工具描述

**所有子 Agent 调用走统一入口 `call_agent`。** 工具描述在启动期由 `SubAgentRegistry` 一次性拼接，运行时不再变化（agent.md 不支持热加载）：

```
工具名: call_agent
描述: 调用指定的子 Agent 执行任务。可用的子 Agent：

      - db-agent: 数据库查询专家，支持 MySQL 和 PostgreSQL 的 SELECT 查询
      - weather-agent: 天气查询专家，支持全球城市天气查询
      - code-agent: 代码生成和测试专家

      参数：
        - agent_name: 子 Agent 名称（必填）
        - task: 任务描述（必填）

参数:
  - agent_name: string  (必填，子 Agent 名)
  - task: string        (必填，任务描述)
```

描述生成规则：
- 每个子 Agent 一行：`- {目录名}: {frontmatter.description}`
- `subagents/` 为空时显示「无可用子 Agent」（工具仍然可见，LLM 调用返回错误后会停止尝试）

### 4.2 上下文开销

无论有多少个子 Agent，主 Agent 上下文中只增加 **1 个 call_agent 工具定义**：~100 token + 每个子 Agent 一行描述（~30 token）。10 个子 Agent ≈ 400 token，远比每 Agent 一个工具（~800 token/个 ≈ 8000 token）省。

### 4.3 执行机制

主 Agent 调用 `call_agent(agent_name="db-agent", task="查昨天的订单量")` 时：

1. **查找子 Agent**：从 `SubAgentRegistry` 按 `agent_name` 查 `SubAgentEntry`（启动期已通过 `adk.NewTypedAgentTool(ctx, ChatModelAgent)` 预构建 `entry.Tool`）
2. **委托执行**：调用 `entry.Tool.InvokableRun(ctx, argumentsInJSON, opts...)`，eino 自动处理事件透传、错误传播、中断传播
3. **生成子 chatID**：见 [2.2 节](#22-chat-文件命名)
4. **结果返回主 Agent**：子 Agent 最终结果作为工具返回值
5. **事件透传**：通过 eino 的 `AsyncGenerator` + `EmitInternalEvents` 机制，子 Agent 的 thinking、tool_calls 等自动转发到父 Runner 的 SSE 流

> **⚠️ 关键开关**：主 Agent 的 `ChatModelAgentConfig.ToolsConfig.EmitInternalEvents` 必须设置为 `true`（默认 false），否则子 Agent 事件不会冒泡到父 Runner。本项目当前 [engine.go:97-101](internal/agent/engine.go) 未启用此开关，落地时必须打开。

### 4.4 调用限制

- **只支持主调子，不支持子调子**：子 Agent 没有 `call_agent` 工具。嵌套深度恒为 1，无需调整 `config.React.NestingMaxDepth`
- **并发限制**：semaphore 放在 `SubAgentRegistry`（全局单例）中，所有 `/chat` 请求共享，默认 `config.SubAgent.MaxConcurrency = 5`，FIFO 排队
- **排队/超时关系**：`Acquire` 返回后才创建带 `config.SubAgent.ExecTimeout`（默认 5 分钟）的 `execCtx`——排队不计入执行超时
- **排队期取消**：若 `Acquire` 期间父 ctx（用户断开 SSE）取消，`Acquire` 返回错误，立即释放队列名额并返回主 Agent
- **Task 长度上限**：`task` 参数最大 `config.SubAgent.MaxTaskLength`（默认 16000 字符），超出则在 `InvokableRun` 中提前拒绝
- **结果截断**：子 Agent 返回文本默认截断至 `config.SubAgent.MaxResultLength`（默认 8000 字符）。截断时**将醒目警告放在开头**（而非末尾），避免 LLM 基于不完整数据决策：

```
⚠️ 结果已被截断（原始长度: 12500 字符，仅显示前 8000 字符）。如需完整数据，请缩小 task 范围或指定输出字段。
──────────────────
[截断后的内容...]
```

- **取消粒度**：`call_agent` 在 eino 中是同步工具 `(string, error)`，执行期间无外部信号注入点。父 ctx 取消时子 Agent 终止，但**无法在保留主 Agent 的情况下单独取消一个子 Agent**——这是 eino 同步工具模型的固有限制
- **无状态调用**：编排模式下子 Agent 的每次 `call_agent` 调用都是全新的、无状态的。`HistoryMessages` 传入空切片，前一次调用结果不会自动带入下一次。需要上下文传递时，主 Agent 应在 `task` 参数中显式写明
- **附件访问**：编排模式下子 Agent 仅拿到 `task` 字符串。如需引用附件，主 Agent 必须在 `task` 中显式写明附件路径（与 eino DeepAgent `task_tool` 行为一致）。子 Agent 能否读取文件取决于其 MCP 工具是否包含文件系统能力。**子 Agent ChatRecord 不写入 `Attachments` 字段**（附件归属主 chat）

> **应对策略**：建议主 Agent 的 GROOT.md 中引导「逐个调用子 Agent，确认前一个返回足够信息后再决定是否调下一个」，避免盲目并行导致资源浪费。`groot init` 时会在默认 GROOT.md 中追加此引导段。

### 4.5 错误处理

子 Agent 执行失败时，错误通过 eino 的标准 error 返回值传播。`ChatModelAgent` 会将工具错误转换为 tool result 文本提供给 LLM。

| 失败类型 | 错误原因 | 主 Agent 行为建议 |
|---------|---------|-----------------|
| 子 Agent 未注册 | `subagent "xxx" not found` | 检查 agent_name 拼写，查看工具描述中的可用列表 |
| MCP 连接失败 | 子 Agent MCP 工具执行错误 | 告知用户该 Agent 不可用，或换其他 Agent |
| LLM 调用超时 | context deadline exceeded | 简化 task 后重试，或拆分任务 |
| 子 Agent 返回空结果 | 正常完成但无文本输出 | 检查 task 描述是否清晰 |
| 达到最大迭代 | 子 Agent 内部达到 max_iterations | 任务太复杂，简化后重试 |
| ChatRecord 写入失败 | 文件系统错误等 | **吞错并记录日志**，不影响子 Agent 成功结果返回给主 Agent |

> 子 Agent 错误事件通过 SSE 携带 `agent_name` 字段标记来源（详见 [4.8 节](#48-sse-事件格式)）。

### 4.6 日志与审计

- **ChatRecord**：子 Agent 执行结束后写入完整 ChatRecord（含 `agent_name`、`error`、累加的 Token 字段），存储在同一 `chats/` 目录下。通过 chatID 前缀关联父子关系
- **不包含详细 Steps**：子 Agent 的 `InvokableRun` 返回 `(string, error)`，不暴露内部 ReAct 循环的 steps。执行细节已通过 SSE 实时展示，如需审计从 SSE 日志中回溯
- **不在主 ChatRecord 重复存储子 Agent 的 steps**：主 Agent 的 ChatRecord 只记录「调用了 call_agent」这个 tool call
- **SSE 透传**：子 Agent 的 thinking、tool_calls、tool_result 实时通过 SSE 推送给客户端，事件中携带 `agent_name` 字段区分来源

### 4.7 实现方案

设计原则：**执行层 100% 复用 eino，业务层自己写**。本质上就是 DeepAgent `task_tool.go` 的「文件系统数据源」版本——DeepAgent 通过代码注册子 Agent，我们通过 `subagents/` 目录发现。核心的 `InvokableRun` 委托链路完全一致。

#### 4.7.1 数据结构

**`SubAgentEntry`**（启动期一次性构建，运行时只读）：

```go
type SubAgentEntry struct {
    Name        string
    Description string
    Instruction string                   // agent.md 正文，Solo 模式 Engine 读取
    Tool        tool.InvokableTool       // 启动期由 NewTypedAgentTool(ctx, ChatModelAgent) 预构建
    MCPManager  *mcp.Manager             // 持有连接生命周期，shutdown 时关闭
    SkillBK     einoskill.Backend        // 供 /agents、/skills API 查询；Watcher 热更新入口
}
```

**`SubAgentRegistry`**（全局单例）：

```go
type SubAgentRegistry struct {
    entries map[string]*SubAgentEntry
    sem     *semaphore.Weighted          // 全局并发控制
}

func (r *SubAgentRegistry) Get(name string) (*SubAgentEntry, bool)
func (r *SubAgentRegistry) Acquire(ctx context.Context) error    // 排队，ctx 取消立即返回
func (r *SubAgentRegistry) Release()
func (r *SubAgentRegistry) BuildDescription() string              // 拼接 call_agent 工具描述
func (r *SubAgentRegistry) Close() error                          // shutdown 时关闭所有 MCP
```

**`RuntimeState` 扩展**（已有结构补充 `SubAgents` 字段）：

```go
type ChatProgress struct {
    CurrentStep    int                 `json:"current_step"`
    StepsCompleted int                 `json:"steps_completed"`
    Percentage     int                 `json:"percentage"`
    SubAgents      []SubAgentProgress  `json:"sub_agents,omitempty"`  // 新增
}

type SubAgentProgress struct {
    Name   string `json:"name"`
    Status string `json:"status"`  // "running"
}
```

#### 4.7.2 启动期构建

参照 eino DeepAgent `typedNewTaskTool`：循环 `subAgents` → `NewTypedAgentTool` → 存 map。

```go
func buildSubAgentRegistry(
    ctx context.Context,
    dir string,
    reactCfg config.ReactConfig,
    subCfg config.SubAgentConfig,
    llmFactory llm.Factory,
    log *logger.Logger,
) (*SubAgentRegistry, error) {
    reg := &SubAgentRegistry{
        entries: map[string]*SubAgentEntry{},
        sem:     semaphore.NewWeighted(int64(subCfg.MaxConcurrency)),
    }
    for _, d := range scanSubAgentDirs(dir) {
        if d.name == MainAgentName {
            log.Error("skip subagent: name conflicts with main agent", zap.String("name", d.name))
            continue
        }
        md, err := parseAgentMd(filepath.Join(d.path, "agent.md"))
        if err != nil || md.Description == "" {
            log.Error("skip subagent: invalid agent.md", zap.String("name", d.name), zap.Error(err))
            continue
        }
        // MCP Manager：从 subagents/{name}/mcp/ 加载配置并建立连接
        mcpMgr := mcp.NewManager(log)
        if err := mcpMgr.LoadAll(filepath.Join(d.path, "mcp")); err != nil {
            log.Error("skip subagent: MCP load failed", zap.String("name", d.name), zap.Error(err))
            continue
        }
        // Skill Backend + Middleware
        skillBK := einoskill.NewBackendFromFilesystem(filepath.Join(d.path, "skills"))
        skillMW := einoskill.NewMiddleware(ctx, skillBK)

        // 模型仅看 agent.md → 默认模型，启动期即固定（不读 X-Model-Name、不读 parent 模型）
        modelName := md.Model
        if modelName == "" {
            modelName = reactCfg.DefaultModel
        }
        chatModel := llmFactory.Build(modelName, md.Temperature, md.MaxTokens)

        cmAgent, err := adk.NewTypedChatModelAgent(ctx, &adk.TypedChatModelAgentConfig[*schema.Message]{
            Name:          d.name,
            Description:   md.Description,
            Instruction:   md.Content,  // 仅 agent.md 正文，不拼 parent prompt
            Model:         chatModel,
            MaxIterations: reactCfg.MaxIterations,
            Handlers:      []adk.ChatModelAgentMiddleware{skillMW},   // ★ 用 Handlers，Middlewares 已废弃
            ToolsConfig: adk.ToolsConfig{
                ToolsNodeConfig: compose.ToolsNodeConfig{Tools: mcpMgr.GetTools()},
                // 子 Agent 的 ToolsConfig 不需要 EmitInternalEvents（叶子节点，没有更深的子 Agent）
            },
        })
        if err != nil {
            log.Error("skip subagent: build chat model agent failed", zap.String("name", d.name), zap.Error(err))
            mcpMgr.Close()
            continue
        }

        // 关键复用点：直接调用 eino DeepAgent 同款 API
        agentTool := adk.NewTypedAgentTool(ctx, cmAgent)

        reg.entries[d.name] = &SubAgentEntry{
            Name:        d.name,
            Description: md.Description,
            Instruction: md.Content,
            Tool:        agentTool,
            MCPManager:  mcpMgr,
            SkillBK:     skillBK,
        }
    }
    return reg, nil
}
```

> **为什么启动期预构建**：与 DeepAgent `task_tool.go` 一致；模型/指令/工具来源全部静态化；避免每次调用重建 ChatModel 实例；`InvokableRun` 退化为单次委托，代码极简。

#### 4.7.3 InvokableRun 委托链路

```go
func (t *CallAgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
    input := &callAgentArgument{}
    if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
        return "", fmt.Errorf("failed to unmarshal call_agent input: %w", err)
    }
    entry, ok := t.registry.Get(input.AgentName)
    if !ok {
        return "", fmt.Errorf("未知的子 Agent: %s，请检查 call_agent 工具描述中的可用子 Agent 列表", input.AgentName)
    }
    if len(input.Task) > t.maxTaskLen {
        return "", fmt.Errorf("task 长度超过 %d 字符上限", t.maxTaskLen)
    }

    // 全局并发控制：semaphore 在 SubAgentRegistry 中（全局单例），ctx 取消立即返回
    if err := t.registry.Acquire(ctx); err != nil {
        return "", err
    }
    defer t.registry.Release()

    // 排队结束，开始计时：创建独立的执行超时 context
    execCtx, cancel := context.WithTimeout(ctx, t.execTimeout)
    defer cancel()

    // 生成子 chatID（含 random4 后缀，避免并发同毫秒冲突）
    childChatID := genChildChatID(t.parentChatID, input.AgentName)

    // 注入 childChatID 到 ctx，供主 Engine 事件循环累加该子 Agent 的 Token
    execCtx = context.WithValue(execCtx, childChatIDKey{}, childChatID)

    // 更新 ProgressInfo（defer 保证异常退出也能清理）
    t.runtimeState.AddSubAgent(t.sessionID, input.AgentName)
    defer t.runtimeState.RemoveSubAgent(t.sessionID, input.AgentName)

    // 委托：与 DeepAgent task_tool.go 完全一致——直接调用启动时预构建的 entry.Tool
    // task 包装为 {"request": "..."} 是 eino NewTypedAgentTool 内部 agentToolRequest{Request} 约定
    params, _ := sonic.MarshalString(map[string]string{"request": input.Task})
    result, runErr := entry.Tool.InvokableRun(execCtx, params, opts...)

    // 结果截断（开头警告）
    if len(result) > t.maxResultLen {
        result = truncateResult(result, t.maxResultLen)
    }

    // 写入子 Agent ChatRecord（错误不影响调用结果返回给主 Agent）
    status := "completed"
    if runErr != nil {
        status = "failed"
    }
    tokens := t.tokenAccumulators.PopAndDelete(childChatID)  // 累加器由主 Engine 事件循环填充
    if saveErr := t.memory.SaveChatRecord(t.sessionID, &memory.ChatRecord{
        SessionID:        t.sessionID,
        ChatID:           childChatID,
        AgentName:        input.AgentName,
        Instruction:      input.Task,
        Result:           result,
        Status:           status,
        Error:            errToString(runErr),
        PromptTokens:     tokens.Prompt,
        CompletionTokens: tokens.Completion,
        TotalTokens:      tokens.Total,
        // 不写 Attachments（附件归属主 chat）
    }); saveErr != nil {
        log.Error("save subagent chat record failed: %v", saveErr) // 吞错，不影响 runErr 返回
    }

    return result, runErr
}
```

**`CallAgentTool` 字段**：

| 字段 | 类型/来源 | 说明 |
|------|----------|------|
| `registry` | `*SubAgentRegistry` 全局单例 | 启动期构建，`Get`/`Acquire`/`Release` |
| `parentChatID` | 主 Agent 的 chatID（请求级） | 用于生成子 chatID 前缀 |
| `sessionID` | 当前会话 ID（请求级） | 用于 ChatRecord、RuntimeState |
| `runtimeState` | `*RuntimeState` 全局单例 | 更新子 Agent Progress |
| `memory` | `*memory.Manager` 全局单例 | 写入子 Agent ChatRecord |
| `tokenAccumulators` | `*TokenAccumulators` 全局单例 | 按 `childChatID` 聚合子 Agent token |
| `execTimeout` | `config.SubAgent.ExecTimeout`（默认 5 min） | 排队不计入；`Acquire` 返回后才计时 |
| `maxTaskLen` | `config.SubAgent.MaxTaskLength`（默认 16000） | 超过直接拒绝 |
| `maxResultLen` | `config.SubAgent.MaxResultLength`（默认 8000） | 超过截断 + 开头警告 |

`CallAgentTool` 是**请求级实例**：`Executor.Execute()` 在创建主 Agent Engine 时新建并注入。

#### 4.7.4 主 Engine 改造

`Engine` 新增 `agentName string` 字段（由 Executor 传入：主 Agent 为 `MainAgentName`，Solo 子 Agent 为 `task.AgentName`）。

事件循环识别子 Agent 事件的逻辑：

```go
// 事件循环（简化版）
for event := range eventCh {
    agentName := ""
    if event.AgentName != "" && event.AgentName != e.agentName {
        agentName = event.AgentName  // 来自子 Agent
    }

    // 累加子 Agent 的 Token：当事件来自子 Agent 且是 assistant 消息时
    if agentName != "" && msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
        if childChatID, ok := getChildChatIDFromCtx(ctx); ok {
            tokenAccumulators.Add(childChatID, msg.ResponseMeta.Usage)
        }
    }

    // SSE 输出：agentName 非空时注入 agent_name 字段
    cb.WriteThinking(agentName, content)
}
```

**`ProgressCallback` 调整**（每个 Write* 函数新增首参 `agentName string`）：

```go
type ProgressCallback struct {
    WriteThinking   func(agentName, content string) error
    WriteMessage    func(agentName, content string) error
    WriteToolCalls  func(agentName string, toolCalls []ToolCall) error
    WriteFinish     func(agentName, reason string) error
    WriteToolResult func(agentName, toolCallID, toolName, content string, isError bool) error
    WriteError      func(agentName, message string) error
    WriteDone       func() error  // 仅一次性结束信号，不区分 Agent
}
```

SSEWriter 收到非空 `agentName` 时在事件 JSON 中注入 `"agent_name": "db-agent"`。

#### 4.7.5 工具集可见性

`call_agent` 仅在主 Agent 路径出现，由 `Executor` 创建主 Agent 的 `Engine` 时把 `callAgentTool` 加入工具列表：

```go
// Executor.Execute() 中（主 Agent 路径）
tools := mainMCPMgr.GetTools()
tools = append(tools, scheduleTool, callAgentTool)
engine := NewEngine(EngineConfig{
    AgentName: MainAgentName,
    Tools:     tools,
    ...
    ToolsConfig: adk.ToolsConfig{EmitInternalEvents: true},  // ★ 必须打开
})
```

- 主 Agent：挂载 `call_agent` 与 `schedule`
- Solo 模式子 Agent：复用同一个 `Engine`，但 Executor 跳过 append `callAgentTool` / `scheduleTool`；`AgentName` 设为 `task.AgentName`；`EmitInternalEvents` 不需要开
- 编排模式子 Agent：完全由 eino `NewTypedAgentTool` 包装的 `ChatModelAgent` 驱动，工具集即启动期 `mcpMgr.GetTools()`

#### 4.7.6 MCP Manager 生命周期

`SubAgentRegistry.Close()` 遍历所有子 Agent 的 `mcp.Manager.Close()`，在 `main.go` 的 shutdown hook 中调用：

```go
globalMCPManager.Close()       // 先关全局
subAgentRegistry.Close()        // 再关子 Agent
```

单个失败不影响其他（记录错误日志后继续）。

### 4.8 SSE 事件格式

子 Agent 事件透传时**复用现有 SSE 事件类型**（thinking、message、tool_calls、tool_result、error），在事件数据 JSON 中**可选地**加入 `agent_name` 字段区分来源：

- `agent_name` 是**可选字段**：主 Agent 自身事件不携带；子 Agent 事件携带（值为子 Agent 名如 `"db-agent"`）
- Solo 模式所有事件均不携带 `agent_name`（只有一个 Agent，无需区分）
- error 事件同样携带 `agent_name`，便于 TUI 区分错误来源

示例（子 Agent 的 tool_calls 事件）：

```
event: tool_calls
data: {"agent_name":"db-agent","tool_calls":[{"index":0,"id":"call_001","function":{"name":"query","arguments":"SELECT ..."}}]}
```

**TUI 渲染**：
- 有 `agent_name` → 子 Agent 执行过程，缩进显示或可折叠面板
- 无 `agent_name` → 当前 Agent 自身事件，正常渲染
- 面包屑导航：`主 Agent → db-agent` 显示当前执行的 Agent 链

---

## 五、MCP 与 Skills 加载

### 5.1 MCP Manager

每个子 Agent 拥有独立的 `mcp.Manager` 实例，启动时创建，运行时常驻：

- 启动时从 `subagents/{name}/mcp/` 加载 MCP 配置，创建 client 连接
- 子 Agent 的 `mcp.Manager` **不注册内置工具**（schedule 等），仅包含 `subagents/{name}/mcp/` 下的 MCP 工具

> **资源考虑**：如 10 个子 Agent 都配置同一个 MySQL MCP，就会有 10 个连接。初期保持简单，后续可考虑连接池共享。

### 5.2 Skill Middleware

每个子 Agent 拥有独立的 Skill Backend + Middleware，**启动期一次性创建**并通过 `Handlers` 字段注入 `ChatModelAgent`，与 `entry.Tool` 一并固化。`entry.SkillBK` 留作 `/agents`、`/skills` API 查询及 Watcher 热更新入口。

### 5.3 热加载策略

| 资源 | 热加载 | 说明 |
|------|--------|------|
| 子 Agent 注册（目录新增/删除） | ❌ | 启动期扫描，变更需重启 |
| `agent.md` 内容 | ❌ | 启动期固化进 `entry.Tool`（参与 `Instruction`、模型绑定），运行时无回灌路径 |
| Skills（全局 + 子 Agent） | ✅ | Watcher 监听变更，按路径匹配 Agent 名，通知对应 `entry.SkillBK` 重新扫描 |
| MCP | ❌ | 与全局 MCP 一致，变更需重启 |
| GROOT.md | ✅ | 按需读取，变更即时生效 |

**Skills Watcher 路径→Agent 映射**：

| 监听目录 | 文件类型 | 变更处理 |
|---------|---------|---------|
| `skills/` | `SKILL.md` | 全局 Skill Backend 重新扫描 |
| `subagents/*/skills/` | `SKILL.md` | 按路径提取 Agent 名 → SubAgentRegistry 中对应 `entry.SkillBK` 重新扫描 |

实现要点：
- Watcher 监听 `subagents/` 顶层目录（递归），但在 `isSkillChange` 事件回调中**按路径前缀过滤**——只处理 `skills/SKILL.md`、`subagents/*/skills/**/SKILL.md`，丢弃 `subagents/*/agent.md` 与 `subagents/*/mcp/*` 等非 Skill 文件的事件
- 路径解析：`subagents/db-agent/skills/sql-review/SKILL.md` → Agent 名 = `"db-agent"`，更新 `entry["db-agent"].SkillBK`

---

## 六、API 变更

### 6.1 `/chat` 接口

**新增 Header：**

| Header | 必填 | 说明 |
|--------|------|------|
| `X-Agent-Name` | 否 | 子 Agent 名。不传 = 编排模式，传入 = Solo 模式 |

为与已有的 `X-Model-Name`、`X-Session-ID` 保持一致，继续使用 `X-` 前缀。

**现有参数保持不变**（`X-Model-Name`、`X-Session-ID`、Body `instruction`、Body `prompt`）。

### 6.2 系统提示词拼接

**Solo 模式**：
```
系统指令 = [agent.md 正文] + [SESSION.md] + [Request.prompt]
用户消息 = [Request.instruction]
HistoryMessages = 完整 session 历史
```

**编排模式 - 主 Agent**：
```
系统指令 = [GROOT.md] + [SESSION.md] + [Request.prompt]
用户消息 = [Request.instruction]
HistoryMessages = 完整 session 历史
```

**编排模式 - 子 Agent**（call_agent 触发）：
```
系统指令 = [agent.md 正文]   ← 不拼 GROOT.md / SESSION.md / Request.prompt
用户消息 = [task 参数]
HistoryMessages = 空
```

**Engine 系统指令拼接的实现**：

- Solo 模式：Executor 从 `SubAgentRegistry.Get(name).Instruction` 读取 agent.md 正文，传给 `Engine.Run(..., agentMdContent)`。Engine 拼接 `agentMd + SESSION.md + Request.prompt`，`agentMd` 为空时退化到主 Agent 路径（拼 GROOT.md）
- 编排模式子 Agent：`agent.md` 已在启动期注入 `ChatModelAgent.Instruction`，通过 `entry.Tool.InvokableRun` 链路自然生效，Engine 无需感知

### 6.3 `/agents` 接口（新增）

```
GET /agents
```

返回所有可用 Agent，含 Skills 摘要：

```json
{
  "agents": [
    {
      "name": "groot",
      "description": "默认 Agent（全局配置）",
      "skills": [
        {"name": "sql-review", "description": "SQL 审查"}
      ]
    },
    {
      "name": "db-agent",
      "description": "数据库查询专家",
      "skills": [{"name": "sql-review", "description": "SQL 审查"}]
    },
    {
      "name": "weather-agent",
      "description": "天气查询专家",
      "skills": []
    }
  ]
}
```

- `groot` 始终排第一
- 子 Agent 的 `description` 来自 `agent.md` frontmatter（必填，缺失会在启动时跳过）
- `skills` 返回该 Agent 实际拥有的 Skill 名称和描述列表

### 6.4 `/skills`、`/tools` 接口（变更）

沿用 `X-Agent-Name` header，不传 = 全局，传入 = 指定子 Agent：

| 请求 | 返回 |
|------|------|
| `GET /skills` | 全局 Skills 列表 |
| `GET /skills` + `X-Agent-Name: db-agent` | db-agent 的 Skills 列表 |
| `GET /tools` | 全局 MCP 工具列表（按 MCP 分组） |
| `GET /tools` + `X-Agent-Name: db-agent` | db-agent 的 MCP 工具列表 |

响应格式不变。实现要点：handler 解析 `X-Agent-Name` header，从 `SubAgentRegistry.Get(name).SkillBK` / `.MCPManager` 取对应实例。

### 6.5 `/chat/status/:sid` 接口（变更）

当主 Agent 正在执行 `call_agent`（子 Agent 运行中），`progress.sub_agents` 反映子 Agent 状态：

```json
{
  "session_id": "sess_001",
  "status": "running",
  "current_step": "正在等待 2 个子 Agent 返回结果...",
  "progress": {
    "sub_agents": [
      {"name": "db-agent", "status": "running"},
      {"name": "weather-agent", "status": "running"}
    ]
  }
}
```

- 主 Agent 自身执行时 `sub_agents` 为空数组
- **集群说明**：`/chat/status` 只展示**当前实例**上的运行态。LB 将请求路由到非执行实例时，`sub_agents` 字段为空（与现有"主 Agent 活跃状态也是每实例本地内存"的限制一致）。客户端如需跨实例查询，应通过执行实例的直连地址，或使用 chatID 查询离线 ChatRecord

### 6.6 HTTP 错误处理

| 情况 | HTTP 状态码 | 说明 |
|------|------------|------|
| `X-Agent-Name` 指定的 Agent 不在 SubAgentRegistry 中 | 400 | `Unknown agent: {name}`（无论是目录不存在还是 agent.md 无效，启动时已跳过） |
| 不传 `X-Agent-Name` | 200 | 编排模式 |

### 6.7 `/models` 接口（不变）

子 Agent 与主 Agent 共享模型列表。子 Agent 可通过 `agent.md` frontmatter 的 `model` 字段指定默认模型。

### 6.8 使用示例

```bash
# 编排模式：主 Agent 根据指令自动判断是否调子 Agent
curl -X POST http://localhost:8080/chat \
  -H "X-Session-ID: sess_001" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "查一下杭州天气，然后存到数据库里", "prompt": ""}'

# Solo 模式：直接用子 Agent 执行
curl -X POST http://localhost:8080/chat \
  -H "X-Agent-Name: db-agent" \
  -H "X-Session-ID: sess_001" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "查询昨天的订单总金额", "prompt": ""}'
```

> **Solo 模式 Session 隔离警告**：API 调用者在同一个 `X-Session-ID` 下混用主 Agent 与不同子 Agent 时，`history.json` 会混入不同 Agent 的对话历史，LLM 可能产生困惑。建议为不同 Agent 使用独立的 Session ID。TUI 不受影响（`/agent <name>` 自动生成新 Session ID）。

---

## 七、TUI 变更

新增 `/agent` 命令：

```
/agent              列出所有可用 Agent
/agent <name>       切换到指定 Agent，同时开启新会话
/agent groot        切回主 Agent
```

切换即新会话（生成新 Session ID，不保留上一 Agent 的对话历史）。状态栏增加 `Agent: {name}` 显示。`/agent` 命令下 Tab 补全所有 Agent 名（含 `groot`）。`/clear` 不重置 agentName。

实现：

```go
type Client struct {
    // ... 现有字段 ...
    agentName string  // 新增，默认 MainAgentName
}
```

---

## 八、Memory 变更

### 8.1 目录结构不变

文件命名见 [2.2 节](#22-chat-文件命名)。

### 8.2 ChatRecord 新增字段

```go
type ChatRecord struct {
    // ... 现有字段不变 ...
    AgentName        string `json:"agent_name,omitempty"`         // 使用的 Agent 名
    PromptTokens     int    `json:"prompt_tokens,omitempty"`      // LLM 输入 token 累加
    CompletionTokens int    `json:"completion_tokens,omitempty"`  // LLM 输出 token 累加
    TotalTokens      int    `json:"total_tokens,omitempty"`       // LLM token 总数累加
}
```

**Token 累加机制**（仅用于事后审计，无运行时查询接口）：

所有 Agent 走单一统一路径——主 `Engine.Run()` 事件循环按 `event.AgentName` 路由到累加器：

| 来源 | 处理 |
|------|------|
| 主 Agent 自身的 LLM 响应 | `event.AgentName == "" \|\| event.AgentName == MainAgentName` → 累加到主 Agent ChatRecord 的 Token 字段 |
| Solo 模式子 Agent | 复用同一个 `Engine`，事件循环同样累加，写入该子 Agent 的 ChatRecord |
| 编排模式子 Agent | `event.AgentName != MainAgentName` → 按 `childChatID`（从 ctx 中取）累加到 `tokenAccumulators` 全局 map；`CallAgentTool.InvokableRun` 委托返回后取出累加值写入子 Agent ChatRecord，并清理累加器条目 |

> **累加而非单次取值**：子 Agent 多轮 ReAct（先查表结构再查数据）必须把每轮 `ResponseMeta.Usage` 累加，否则 token 数只是最后一次的值。
>
> **没有计数 middleware**：4.7 节启动期构建 `ChatModelAgent` 不挂任何 Token middleware；Token 在主 Engine 事件循环中单次累加。

`RunResult`（engine.go:635）需同步新增 `PromptTokens`、`CompletionTokens`、`TotalTokens` 字段。

**Token 汇总**：审计端通过 chatID 前缀匹配关联父子 Agent，离线查询各 ChatRecord 后自行求和。**不提供** `/chat/{chatID}/tokens` 等运行时聚合接口。

### 8.3 SESSION.md 读写

内容不变。编排模式子 Agent **不访问 SESSION.md**——子 Agent 每次调用是无状态的，`task` 参数已包含主 Agent 提炼后的完整上下文。

---

## 九、执行流程

### 9.1 Solo 模式

```
POST /chat   Header: X-Agent-Name: db-agent
  ▼
ChatHandler.Handle()
  ├── task.AgentName = "db-agent"
  ├── 校验 SubAgentRegistry.Get("db-agent") 存在 → 不存在则 400
  └── executor.Execute(ctx, sessionID, task, sseWriter)
        ▼
Executor.Execute()
  ├── 从 SubAgentRegistry 取 entry.MCPManager / entry.SkillBK / entry.Instruction
  ├── 创建 Engine（AgentName="db-agent"，工具集不挂 call_agent / schedule，EmitInternalEvents=false）
  └── engine.Run(ctx, ..., agentMdContent=entry.Instruction)
        ▼
Engine.Run()
  ├── 系统指令: agent.md 正文 + SESSION.md + Request.prompt
  ├── 用户消息: Request.instruction
  ├── HistoryMessages: 完整 session 历史
  └── 工具: 子 Agent MCP + 子 Agent Skills
```

### 9.2 编排模式

```
POST /chat   （无 X-Agent-Name）
  ▼
ChatHandler.Handle()
  └── executor.Execute(ctx, sessionID, task, sseWriter)
        ▼
Executor.Execute()
  ├── 全局 MCP Manager + 全局 Skill Middleware
  ├── 创建 CallAgentTool 实例（parentChatID, sessionID）
  ├── 创建 Engine（AgentName=MainAgentName，工具集 = 全局 MCP + schedule + call_agent，EmitInternalEvents=true）
  └── engine.Run()
        ▼
主 Agent Engine.Run()
  │  主 Agent 决定调用 call_agent(agent_name="db-agent", task="查昨天的订单量")
  │    ▼
  │  CallAgentTool.InvokableRun():
  │    ├── 校验 task 长度 → 超长拒绝
  │    ├── 从 SubAgentRegistry 查 SubAgentEntry → 不存在返回错误
  │    ├── Acquire 全局 semaphore（ctx 取消立即返回）→ defer Release
  │    ├── 创建 execCtx（独立超时 context，从此时开始计时）
  │    ├── 生成 childChatID，注入 ctx
  │    ├── 更新 RuntimeState.AddSubAgent → defer RemoveSubAgent
  │    ├── 委托 entry.Tool.InvokableRun(execCtx, {"request": task}, opts...)
  │    │     ▼
  │    │   子 Agent 执行（启动期已固化）:
  │    │     ├── 系统指令: agent.md 正文（无 GROOT.md / SESSION.md / Request.prompt）
  │    │     ├── 用户消息: task
  │    │     ├── HistoryMessages: 空
  │    │     ├── 工具: 子 Agent MCP + 子 Agent Skills
  │    │     ├── 模型: agent.md 指定（不读 X-Model-Name）
  │    │     └── 事件透传（含 AgentName）→ 主 Engine 事件循环
  │    ├── 主 Engine 事件循环按 event.AgentName 累加 Token 到 tokenAccumulators[childChatID]
  │    ├── 结果截断（开头警告）
  │    └── 写入子 Agent ChatRecord（含累加的 Token），清理累加器
  │
  ▼
结果保存
  ├── 主 Agent ChatRecord → chats/chat_{ts}.json
  └── 子 Agent ChatRecord → chats/chat_{ts}_{HHMMSSmmm}_{random4}_{agentName}.json
```

---

## 十、与现有架构的关系

### 10.1 改动总览

| 改动等级 | 模块 |
|---------|------|
| 新增 | `internal/agent/consts.go`、`internal/agent/subagent_registry.go`、`internal/agent/call_agent.go`、`internal/agent/token_accumulators.go` |
| 中改 | `internal/agent/executor.go`、`internal/api/handler/chat.go` |
| 轻改 | `internal/agent/engine.go`、`internal/agent/runtime_state.go`、`internal/agent/sse.go`、`internal/api/handler/skills.go`、`internal/api/handler/tools.go`、`internal/memory/types.go`、`internal/memory/idgen.go`、`internal/cmd/chat.go`、`internal/cmd/init.go`、`config.yaml` |
| 无改动 | `GROOT.md`、全局 `skills/`、全局 `mcp/`、`internal/grootmd/`、`internal/mcp/manager.go`、`/models` |

### 10.2 关键变更点

| 模块 | 变更内容 |
|------|---------|
| `agent.SubAgentRegistry` | 新增。启动期一次性通过 `adk.NewTypedAgentTool` 预构建每个 `entry.Tool`（与 eino DeepAgent `task_tool.go` 一致）。运行时仅查表+委托 |
| `agent.CallAgentTool` | 新增。请求级实例，由 Executor 在主 Agent 路径注入 Engine |
| `agent.consts.MainAgentName` | 新增。`"groot"`，统一替换现有 `"GrootAgent"` |
| `agent.Engine` | 新增 `agentName` 字段；`buildSystemInstruction` 支持 agent.md 替换 GROOT.md；事件循环按 `event.AgentName` 分流（SSE agent_name 注入、Token 累加）；主 Agent 路径打开 `ToolsConfig.EmitInternalEvents` |
| `agent.ProgressCallback` | 每个 Write* 函数新增首参 `agentName string` |
| `agent.RuntimeState` | `ChatProgress.SubAgents` 字段；`AddSubAgent` / `RemoveSubAgent` 方法 |
| `agent.Executor` | 注入 SubAgentRegistry；按 task.AgentName 分流；编排模式下创建 CallAgentTool 实例 |
| `agent.Task` | 新增 `AgentName` 字段 |
| `api/handler/chat.go` | 解析 `X-Agent-Name`；Solo 模式 → 400 校验注册；两种模式分流 |
| `api/handler/skills.go` / `tools.go` | 解析 `X-Agent-Name`，从 SubAgentRegistry 取对应 Backend / MCPManager |
| `api/handler/agents.go` | 新增 `/agents` |
| `skills.Watcher` | 监听范围扩大到 `subagents/*/skills/`；按路径匹配 Agent 名；事件层过滤丢弃 `subagents/*/agent.md` 与 `subagents/*/mcp/*` 事件 |
| `memory.ChatRecord` | 新增 `AgentName`、`PromptTokens`、`CompletionTokens`、`TotalTokens` |
| `memory.idgen` | 新增 `GenerateChildChatID(parent, agentName)`，含 random4 后缀 |
| `cmd/init.go` | 创建 `subagents/` 目录；默认 GROOT.md 末尾追加 `call_agent` 调度引导段 |
| `config.yaml` | 新增 `subagent.max_concurrency`、`subagent.exec_timeout`、`subagent.max_task_length`、`subagent.max_result_length` |
| `main.go` shutdown hook | 先关全局 MCP，再 `subAgentRegistry.Close()` |

---

## 十一、向后兼容

| 场景 | 行为 |
|------|------|
| 旧版本升级，无 `subagents/` 目录 | 完全不变 |
| 有 `subagents/`，不传 `X-Agent-Name` | 编排模式，`call_agent` 工具描述自动列出可用子 Agent |
| 传入 `X-Agent-Name` | Solo 模式 |
| `subagents/{name}/` 下无 `agent.md` 或 description 为空 | 启动时跳过，不注册为子 Agent |

---

## 十二、安全性

- **隔离即安全**：子 Agent 的工具和主 Agent 完全隔离，主 Agent 无法意外调用子 Agent 的危险工具
- **目录权限**：`subagents/{name}/mcp/` 下的配置由管理员控制，普通用户不应有写入权限
- **call_agent 是唯一入口**：API 调用者无法直接触发子 Agent（除非通过 Solo 模式的 `X-Agent-Name`）
- **审计可追溯**：ChatRecord 记录 `agent_name` 字段，子 chatID 前缀关联父子调用关系
- **注入风险**：用户 prompt 可能诱导主 Agent 调用不该调的子 Agent，但子 Agent 的工具集是启动期固化的，无法被诱导扩大权限

初期不做额外权限控制。子 Agent 的权限由管理员通过 MCP 配置控制，这与 Groot 自身的权限模型一致。
