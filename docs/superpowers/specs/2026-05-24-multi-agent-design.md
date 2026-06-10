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

> 主 Engine 与 Solo 模式 Engine 都通过 `Executor` 把 `agentName` 传给 `NewEngine(EngineConfig{AgentName: ...})`，事件循环按 `event.AgentName == e.agentName` 区分主/子。

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
| 系统提示词 | `agent.md` + `defaultSessionRules` + Request.prompt | GROOT.md + `defaultSessionRules` + Request.prompt | 仅 `agent.md` |
| MCP 工具 | 仅 `subagents/{name}/mcp/` | 全局 `mcp/` | 仅 `subagents/{name}/mcp/` |
| 内置工具 | `call_agent`（无，Solo 不挂） | `call_agent` / schedule 8 件套 | 无内置工具 |
| Skills | 仅 `subagents/{name}/skills/` | 全局 `skills/` | 仅 `subagents/{name}/skills/` |
| 模型 | `X-Model-Name` → `agent.md.model` → `llm.default_model` | `X-Model-Name` → `llm.default_model` | `agent.md.model` → 父任务运行时 model → `llm.default_model` |
| 会话规则 | `defaultSessionRules` 嵌入常量 | 同上 | 不注入 |
| HistoryMessages | 完整 session 历史 | 完整 session 历史 | 空切片（无状态） |
| ChatRecord | 写入 `chats/`，chatID 不含父前缀，`AgentName` = 子 Agent 名 | 写入 `chats/`，`AgentName` 为空 | 写入同一 `chats/`，chatID 含父前缀，`AgentName` = 子 Agent 名 |
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

1. **查找子 Agent**：从 `SubAgentRegistry` 按 `agent_name` 查 `SubAgentEntry`（v3.8 架构：entry 持有装配材料，不预构建 `Tool`）
2. **运行时组装**：`entry.BuildAgentTool(execCtx, parentModelName)` 现场组装 `ChatModel` + `ChatModelAgent` + `AgentTool`（详见 [4.7 节](#47-实现方案)）
3. **委托执行**：调用 `agentTool.InvokableRun(ctx, argumentsInJSON, opts...)`，eino 自动处理事件透传、错误传播、中断传播
4. **生成子 chatID**：见 [2.2 节](#22-chat-文件命名)
5. **结果返回主 Agent**：子 Agent 最终结果作为工具返回值
6. **事件透传**：通过 eino 的 `AsyncGenerator` + `EmitInternalEvents` 机制，子 Agent 的 thinking、tool_calls 等自动转发到父 Runner 的 SSE 流

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
- **附件访问**：编排模式下子 Agent 仅拿到 `task` 字符串。需要读取附件时，主 Agent 应在 `task` 参数中显式写明附件路径或内容（与 eino DeepAgent `task_tool` 行为一致）。**子 Agent ChatRecord 不写入 `Attachments` 字段**（附件归属主 chat）

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

设计原则：**执行层 100% 复用 eino，业务层自己写**。本质上是 DeepAgent `task_tool.go` 的「文件系统数据源」版本——DeepAgent 通过代码注册子 Agent，我们通过 `subagents/` 目录发现。

#### 4.7.1 v3.8 架构变更：运行时构建子 Agent ChatModel

**v3.8 之前**：启动期一次性构建子 Agent 的 `ChatModel` + `ChatModelAgent` + `AgentTool`，存入 `SubAgentEntry.Tool`，运行时直接调用。

**v3.8 起**：`SubAgentEntry` 不再持有 `Tool` 字段，改为持有"装配材料"。每次 `call_agent` 调用时由 `SubAgentEntry.BuildAgentTool` 现场组装 `ChatModel` + `ChatModelAgent` + `AgentTool`。

**变更原因**：启动期构建的 `ChatModel` 在运行时无法跟随父 Agent 当前 model 切换。主 Agent 切到新 model（如 `X-Model-Name: gpt-5`）时，子 Agent 仍连旧的（启动期默认）端点，不符合"编排模式下子 Agent 跟随父 Agent 选定 model"的预期。

#### 4.7.2 数据结构

**`SubAgentEntry`**（启动期一次性构建，运行时只读；不再含 `Tool` 字段）：

```go
type SubAgentEntry struct {
    Name        string
    Description string
    Instruction string                       // agent.md 正文，Solo + BuildAgentTool 都使用
    MCPManager  *mcp.Manager                 // 持有 MCP 连接生命周期
    SkillBK     einoskill.Backend            // 供 /agents、/skills API 查询

    // 构建子 ChatModelAgent 所需的纯配置；BuildAgentTool 每次现场用这些拼装。
    AgentMdModel  string                     // agent.md 中显式声明的 model；空字符串表示跟随父 Agent
    MaxIterations int                        // 已应用默认值（>=1）
    RetryConfig   *adk.ModelRetryConfig      // 可空
    SkillMW       adk.ChatModelAgentMiddleware // 已构建的 skill middleware；可空
    StepTimeout   time.Duration              // 单步 LLM 调用超时
    LLMCfg        config.LLMConfig           // ChatModel 实例化材料
}
```

**`SubAgentRegistry`**（全局单例）：

```go
type SubAgentRegistry struct {
    entries map[string]*SubAgentEntry
    sem     *semaphore.Weighted              // 全局并发控制
    log     *logger.Logger
    mu      sync.RWMutex
}

func (r *SubAgentRegistry) Get(name string) (*SubAgentEntry, bool)
func (r *SubAgentRegistry) Acquire(ctx context.Context) error
func (r *SubAgentRegistry) Release()
func (r *SubAgentRegistry) BuildDescription() string
func (r *SubAgentRegistry) Names() []string
func (r *SubAgentRegistry) Close() error    // shutdown 时关闭所有 MCP（先 detach 再串行 Close）
```

`Close()` 并发安全策略：在锁内 detach（拿到 entries snapshot 并把字段重置为新空 map），释放锁后才串行调用 `MCPManager.Close()`。这样 shutdown 期间的 `Get` / `Names` / `BuildDescription` 立即看到"空注册表"，消除 use-after-close 风险。

**`RuntimeState` 扩展**（已有结构补充 `SubAgents` 字段）：

```go
type ChatProgress struct {
    CurrentStep    int                 `json:"current_step"`
    StepsCompleted int                 `json:"steps_completed"`
    Percentage     int                 `json:"percentage"`
    SubAgents      []SubAgentProgress  `json:"sub_agents,omitempty"`
}

type SubAgentProgress struct {
    Name   string `json:"name"`
    Status string `json:"status"`  // "running"
}
```

#### 4.7.3 启动期构建（`BuildSubAgentRegistry`）

`BuildSubAgentRegistry(ctx, dir, reactCfg, subCfg, llmCfg, log)` 流程：

1. **`scanSubAgentDirs(dir)`**：文件系统遍历 + agent.md 解析。跳过规则（任一命中即跳过该目录并记 ERROR 日志）：
   1. 与主 Agent 同名（`MainAgentName="groot"`）
   2. 不是目录（含"指向目录的符号链接被解析失败"——用 `os.Stat` 而非 `entry.IsDir()`，让 `ln -s` 共享子 Agent 模板生效）
   3. 缺失 `agent.md` 文件
   4. `parseAgentMd` 失败（缺 description 等）

   dir 不存在时静默返回 nil（合法状态——用户尚未创建任何子 Agent）。
2. **`buildSubAgentEntry(p)`** 装配单个子 Agent：
   - **MCP Manager**：`mcp.NewManager(log).LoadAll(filepath.Join(p.dir, "mcp"))`，目录不存在视为合法
   - **Skill Backend**：`os.MkdirAll(skillsDir, 0755)` 兜底建目录 → `local.NewBackend` → `filesystem.NewSymlinkBackend` 包装 → `einoskill.NewBackendFromFilesystem` → `einoskill.NewMiddleware`
   - **装配材料**：填入 `MaxIterations`（默认 20）、`RetryConfig`（仅 `reactCfg.ErrorRetry > 0` 时构造）、`StepTimeout`、`LLMCfg`
   - **不构造 ChatModel / ChatModelAgent / AgentTool**（v3.8 变更点）
3. 任意子 Agent 构建失败 → 记 ERROR + `mcpMgr.Close()` 防资源泄漏 → 跳过该项继续

**设计原则**：所有错误（含 dir 不存在、单个子 Agent 构建失败）在内部消化，总是返回非 nil 的 `*SubAgentRegistry`（即使 entries 为空），供 `main.go` 注册到 `Executor`。因此不返回 error——避免调用方写出永远为死代码的 `if err != nil`。

#### 4.7.4 运行时构建 AgentTool（`BuildAgentTool`）

```go
func (e *SubAgentEntry) BuildAgentTool(
    ctx context.Context,
    parentModelName string,
) (tool.InvokableTool, error)
```

**model 选择优先级**：

1. `e.AgentMdModel` —— agent.md 显式声明（钉死特定模型，覆盖一切）
2. `parentModelName` —— 父任务运行时 model（编排模式默认行为）
3. `e.LLMCfg.DefaultModel` —— 配置默认值兜底

`parentModelName` 由 `CallAgentTool` 透传：在 `InvokableRun` 中通过 `ParentModelFromContext(ctx)` 读取主 Engine 当前 modelName 并作为参数。

**装配步骤**：

1. `llm.NewChatModel(ctx, e.LLMCfg, modelName, e.StepTimeout)`
2. `tools = e.MCPManager.GetTools()`
3. `adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{...})`
   - `Handlers = []adk.ChatModelAgentMiddleware{e.SkillMW}`（可空时不注入）
   - `ModelRetryConfig = e.RetryConfig`（可空时不注入）
   - 子 Agent 是叶子节点，`ToolsConfig.EmitInternalEvents` 不开（由父 Agent 透出）
4. `adk.NewAgentTool(ctx, cmAgent)` → 类型断言 `tool.InvokableTool`

#### 4.7.5 InvokableRun 委托链路

`CallAgentTool` 是请求级实例：`Executor.Execute()` 在创建主 Agent Engine 时新建并注入。

```go
func (t *CallAgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
    // 1. 解析 + 校验
    input := &callAgentArgument{}
    json.Unmarshal([]byte(argumentsInJSON), input)
    entry, ok := t.registry.Get(input.AgentName)
    if !ok {
        return "", fmt.Errorf("未知的子 Agent: %s", input.AgentName)
    }
    if len(input.Task) > t.maxTaskLen {
        return "", fmt.Errorf("task 长度超过 %d 字符上限", t.maxTaskLen)
    }

    // 2. 排队（ctx 取消立即返回）
    if err := t.registry.Acquire(ctx); err != nil { return "", err }
    defer t.registry.Release()

    // 3. 排队结束才创建执行超时 ctx
    execCtx, cancel := context.WithTimeout(ctx, t.execTimeout)
    defer cancel()

    // 4. 生成子 chatID（含 random4 防同毫秒并发碰撞）
    childChatID := memory.GenerateChildChatID(t.parentChatID, input.AgentName)
    execCtx = context.WithValue(execCtx, childChatIDKey{}, childChatID)

    // 5. 进度上报
    t.runtimeState.AddSubAgent(t.sessionID, input.AgentName)
    defer t.runtimeState.RemoveSubAgent(t.sessionID, input.AgentName)

    // 6. v3.8: 运行时构建 AgentTool
    parentModel := ParentModelFromContext(ctx)
    agentTool, err := entry.BuildAgentTool(execCtx, parentModel)
    if err != nil { return "", err }

    // 7. 委托 eino
    params, _ := sonic.MarshalString(map[string]string{"request": input.Task})
    result, runErr := agentTool.InvokableRun(execCtx, params, opts...)

    // 8. 截断（开头警告）
    if len(result) > t.maxResultLen { result = truncateResult(result, t.maxResultLen) }

    // 9. 写子 ChatRecord（吞错）
    tokens := t.tokenAccumulators.PopAndDelete(childChatID)
    saveErr := t.memory.SaveChatRecord(t.sessionID, &memory.ChatRecord{
        SessionID:        t.sessionID,
        ChatID:           childChatID,
        AgentName:        input.AgentName,
        Instruction:      input.Task,
        Result:           result,
        Status:           statusFromErr(runErr),
        Error:            errToMemoryError(runErr),
        PromptTokens:     tokens.Prompt,
        CompletionTokens: tokens.Completion,
        TotalTokens:      tokens.Total,
        // 不写 Attachments（附件归属主 chat）
    })
    if saveErr != nil { log.Error("save subagent chat record failed: %v", saveErr) }

    return result, runErr
}
```

**`CallAgentTool` 字段**：

| 字段 | 类型/来源 | 说明 |
|------|----------|------|
| `registry` | `*SubAgentRegistry` 全局单例 | 启动期构建 |
| `parentChatID` | 主 Agent 的 chatID（请求级） | 用于生成子 chatID 前缀 |
| `sessionID` | 当前会话 ID（请求级） | 用于 ChatRecord、RuntimeState |
| `runtimeState` | `*RuntimeState` 全局单例 | 更新子 Agent Progress |
| `memory` | `*memory.Manager` 全局单例 | 写子 Agent ChatRecord |
| `tokenAccumulators` | `*TokenAccumulators` 全局单例 | 按 `childChatID` 聚合子 Agent token |
| `execTimeout` | `config.SubAgent.ExecTimeout`（默认 5 min） | 排队不计入；`Acquire` 返回后才计时 |
| `maxTaskLen` | `config.SubAgent.MaxTaskLength`（默认 16000） | 超过直接拒绝 |
| `maxResultLen` | `config.SubAgent.MaxResultLength`（默认 8000） | 超过截断 + 开头警告 |

#### 4.7.6 主 Engine 改造

`Engine` 新增 `agentName string` 字段（由 Executor 传入：主 Agent 为 `MainAgentName`，Solo 子 Agent 为 `task.AgentName`）。

事件循环识别子 Agent 事件的逻辑：

```go
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

#### 4.7.7 工具集可见性

`Executor.Execute()` 按主 / Solo 模式分发：

```go
if task.AgentName != "" && task.AgentName != MainAgentName {
    // Solo 模式：不挂 call_agent；EmitInternalEvents 保持 false
    // 用 entry.SkillMW（若可用）替换 middlewares
} else if e.subAgentRegistry != nil {
    // 编排模式主 Agent：append callAgent 工具；EmitInternalEvents = true
    extraTools = append(extraTools, NewCallAgentTool(...))
}

engine := NewEngine(EngineConfig{
    AgentName:          agentName,
    ExtraTools:         extraTools,
    EmitInternalEvents: emitInternal,
    ...
})
```

- 主 Agent（编排模式）：`extraTools = [call_agent]`，`EmitInternalEvents=true`
- Solo 模式子 Agent：`extraTools = []`，`EmitInternalEvents=false`
- 编排模式子 Agent（通过 `call_agent` 调度）：`extraTools = []`（仅含 `entry.MCPManager` 工具）

#### 4.7.8 MCP Manager 生命周期

`SubAgentRegistry.Close()` 遍历所有子 Agent 的 `mcp.Manager.Close()`，在 `main.go` 的 shutdown hook 中调用：

```go
globalMCPManager.Close()       // 先关全局
subAgentRegistry.Close()        // 再关子 Agent
```

单个失败不影响其他（记录错误日志后继续）。

#### 4.7.9 测试注入路径

`SubAgentEntry.testTool` / `SetToolForTest`：仅供 `_test.go` 注入预制 `InvokableTool` 跳过 `BuildAgentTool` 的真实 LLM dial。`BuildAgentTool` 在 `e.testTool != nil` 时直接返回它。

`SubAgentRegistry.NewRegistryForTest` / `SetEntryForTest`：跳过启动期扫描，直接注入预构建 entry。

生产路径绝不调用，命名以 `ForTest` 结尾警示越权。

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

每个子 Agent 拥有独立的 Skill Backend + Middleware，**启动期一次性创建**：`einoskill.NewBackendFromFilesystem` 后产出 `entry.SkillBK`，再用 `einoskill.NewMiddleware` 包出 `entry.SkillMW`。`entry.SkillMW` 在 `BuildAgentTool` 中作为 `Handlers` 注入子 `ChatModelAgent`；`entry.SkillBK` 留作 `/agents`、`/skills` API 查询。

> Skill Backend 使用 `filesystem.NewSymlinkBackend` 包装 `local.NewBackend`，让 `subagents/{name}/skills/` 下的符号链接也能正确解析。

### 5.3 热加载策略

| 资源 | 热加载 | 说明 |
|------|--------|------|
| 子 Agent 注册（目录新增/删除） | ❌ | 启动期扫描，变更需重启 |
| `agent.md` 内容 | ❌ | 启动期固化进 `entry.Instruction` 与装配材料，运行时不重读 |
| Skills（全局 + 子 Agent） | ✅ | eino Backend 无缓存按需读取，`SKILL.md` 内容修改下次执行自动可见 |
| MCP | ❌ | 与全局 MCP 一致，变更需重启 |
| GROOT.md | ✅ | 按需读取，变更即时生效 |

**Skills 热加载机制**：所有 skills（主 Agent 与子 Agent 共用）通过 eino Backend 在每次执行期按需读取 `SKILL.md` 与脚本内容，无内存缓存层——本地文件系统直接修改即生效。Groot 不再维护 `internal/watcher` 文件系统监听器。

> 如果通过 `groot pull` 从 MinIO 拉取 skills（minio 模式），同样依赖 eino Backend 的按需读取——pull 写完本地文件后下次 LLM 触发 skill 时自动读到新版本。详见 [sync 模块设计](2026-06-08-sync-design.md) §1.10.5。

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
系统指令 = [agent.md 正文] + [defaultSessionRules] + [Request.prompt]
用户消息 = [Request.instruction]
HistoryMessages = 完整 session 历史
```

**编排模式 - 主 Agent**：
```
系统指令 = [GROOT.md] + [defaultSessionRules] + [Request.prompt]
用户消息 = [Request.instruction]
HistoryMessages = 完整 session 历史
```

**编排模式 - 子 Agent**（call_agent 触发）：
```
系统指令 = [agent.md 正文]   ← 不拼 GROOT.md / defaultSessionRules / Request.prompt
用户消息 = [task 参数]
HistoryMessages = 空
```

**Engine 系统指令拼接的实现**：

- `defaultSessionRules` 通过 `//go:embed session_rules.md` 嵌入二进制，由 `memory.Manager.GetSessionMdContent(sessionID)` 直接返回常量（实现忽略 sessionID 参数）。所有会话共享同一份规则。规则正文要点见 [Memory 模块设计](2026-05-11-memory-design.md) §1.10
- Solo 模式：Executor 从 `SubAgentRegistry.Get(name).Instruction` 读取 agent.md 正文，传给 `Engine.Run(..., agentMdContent)`。Engine 拼接 `agentMd + defaultSessionRules + Request.prompt`，`agentMd` 为空时退化到主 Agent 路径（拼 GROOT.md）
- 编排模式子 Agent：`agent.md` 经 v3.8 架构在 `BuildAgentTool` 现场注入 `ChatModelAgent.Instruction`，通过 eino 调度链路自然生效，主 Engine 无需感知

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

Token 累加值直接通过 `TokenAccumulators` 持久化到 ChatRecord，不写入 `RunResult`（`RunResult` 只含 `Content`、`Steps`、`Cancelled` 三个字段）。

**Token 汇总**：审计端通过 chatID 前缀匹配关联父子 Agent，离线查询各 ChatRecord 后自行求和。**不提供** `/chat/{chatID}/tokens` 等运行时聚合接口。

### 8.3 会话规则注入

`defaultSessionRules` 是嵌入二进制的常量（`//go:embed session_rules.md`），所有会话共享同一份。`memory.Manager.GetSessionMdContent(sessionID)` 实现忽略 sessionID 参数直接返回该常量；接口签名保留 sessionID 参数仅作向后兼容。

主 Agent 与 Solo 子 Agent 都会拼接 `defaultSessionRules` 到系统指令；编排模式子 Agent **不注入 defaultSessionRules**——子 Agent 每次调用是无状态的，`task` 参数已包含主 Agent 提炼后的完整上下文。

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
  ├── 从 SubAgentRegistry 取 entry.MCPManager / entry.SkillBK / entry.Instruction / 装配材料
  ├── 创建 Engine（AgentName="db-agent"，extraTools=[]，EmitInternalEvents=false）
  └── engine.Run(ctx, ..., agentMdContent=entry.Instruction)
        ▼
Engine.Run()
  ├── 系统指令: agent.md 正文 + defaultSessionRules + Request.prompt
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
  ├── 创建 CallAgentTool 实例（parentChatID, sessionID, memory, runtimeState）
  ├── 创建 Engine（AgentName=MainAgentName，extraTools=[call_agent]，EmitInternalEvents=true）
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
  │    ├── entry.BuildAgentTool(execCtx, parentModelName)
  │    │     ▼
  │    │   v3.8 现场组装:
  │    │     ├── llm.NewChatModel（按 model 优先级 AgentMdModel → parentModelName → DefaultModel）
  │    │     ├── tools = 子 Agent MCP 工具
  │    │     └── adk.NewChatModelAgent + adk.NewAgentTool
  │    ├── 委托 agentTool.InvokableRun(execCtx, {"request": task}, opts...)
  │    │     ▼
  │    │   子 Agent 执行:
  │    │     ├── 系统指令: agent.md 正文（无 GROOT.md / defaultSessionRules / Request.prompt）
  │    │     ├── 用户消息: task
  │    │     ├── HistoryMessages: 空
  │    │     ├── 工具: 子 Agent MCP + 子 Agent Skills
  │    │     ├── 模型: AgentMdModel → parentModelName → DefaultModel
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
| `agent.SubAgentRegistry` | 新增。启动期扫描 `subagents/` 装配每个 `SubAgentEntry`（含 MCP / SkillBK / SkillMW / 装配材料）；不预构建 ChatModel/AgentTool（v3.8 变更） |
| `agent.SubAgentEntry.BuildAgentTool` | 新增。运行时按 `parentModelName` 与 `extraTools` 现场组装 `ChatModel` + `ChatModelAgent` + `AgentTool` |
| `agent.CallAgentTool` | 新增。请求级实例，由 Executor 在主 Agent 路径注入 Engine；通过 `ParentModelFromContext(ctx)` 读取父 model 并透传给 `BuildAgentTool` |
| `agent.consts.MainAgentName` | 新增。`"groot"`，统一替换历史的 `"GrootAgent"` |
| `agent.Engine` | 新增 `agentName` 字段；`buildSystemInstruction` 支持 agent.md 替换 GROOT.md；事件循环按 `event.AgentName` 分流（SSE agent_name 注入、Token 累加）；主 Agent 路径打开 `ToolsConfig.EmitInternalEvents` |
| `agent.ProgressCallback` | 每个 Write* 函数新增首参 `agentName string` |
| `agent.RuntimeState` | `ChatProgress.SubAgents` 字段；`AddSubAgent` / `RemoveSubAgent` 方法 |
| `agent.Executor` | 注入 SubAgentRegistry；按 task.AgentName 分流；编排模式下追加 `call_agent` |
| `agent.Task` | 新增 `AgentName` 字段 |
| `api/handler/chat.go` | 解析 `X-Agent-Name`；Solo 模式 → 400 校验注册；两种模式分流 |
| `api/handler/skills.go` / `tools.go` | 解析 `X-Agent-Name`，从 SubAgentRegistry 取对应 Backend / MCPManager |
| `api/handler/agents.go` | 新增 `/agents` |
| `skills` 热加载 | **不再维护 `internal/watcher`**——所有 skills（主 + 子）通过 eino Backend 按需读取 |
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
