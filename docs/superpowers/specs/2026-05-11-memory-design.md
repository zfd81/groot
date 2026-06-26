# Memory 模块设计

**日期**：2026-05-11（初版）/ 2026-06-10（迁移到数据库后端后重写）/ 2026-06-26（移除定时清理）
**状态**：实现稿
**作者**：zfd81 + Claude

---

## 一、功能设计

### 1.1 概述

Memory 模块负责 Groot 会话数据的持久化存储。通过 `repo.MemoryRepo` 接口落到 `memory_sessions` / `memory_chats` 两张数据库表。

**核心设计原则**：

- **两表存储**：`memory_sessions`（会话元数据） + `memory_chats`（每轮对话的结构化记录）
- **历史按需聚合**：不存独立的 `history.json`，每次 LLM 上下文构建时通过 `LoadHistory` 从 `memory_chats` 实时聚合
- **全量保存、按需截断**：DB 中保存全部轮次，传递给 LLM 的上下文只取最近 N 轮
- **Repository 抽象**：所有持久化操作走 `MemoryRepo` 接口；`memorydb` 实现层根据 dialect 选择 `INSERT IGNORE` / `ON CONFLICT` 等方言

**核心概念层级**：

```
Session（会话）
  └─ Round / Chat（轮次，一次请求-响应）
       └─ Step（步骤，一次工具调用或 LLM 输出）
```

### 1.2 ID 格式

四种 ID 由 `internal/memory/idgen.go` 生成。

| ID | 格式 | 示例 |
|----|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260418100000523_a1b2` |
| `chat_id`（主 Agent） | `{YYYYMMDDHHMMSSmmm}`（17 位纯数字，无前缀） | `20260418100000523` |
| `step_id` | `{YYYYMMDD}-{HHMMSSmmm}-{random6}` | `20260418-100005000-a1b2c3` |
| `child_chat_id`（子 Agent） | `{parentChatID}_{HHMMSSmmm}_{random4}_{agentName}` | `20260418100000523_100002500_a1b2_db-agent` |

- 时间戳精确到毫秒
- `random4` / `random6` 由小写字母 + 数字组成
- 随机源统一 `crypto/rand`

#### 1.2.1 子 Agent ID 防碰撞

`GenerateChildChatID(parentChatID, agentName)` 在并发高频调用下保证唯一：

- **每毫秒随机起点 + 同毫秒严格自增**：`random4 = base36((offset + counter) mod 36^4, 4)`
- 同一毫秒内 `counter` 严格递增，不重抽 offset
- `offset` 仅在 ms 严格大于 lastMs 时重新随机
- 系统时钟回退（`ms < lastMs`）时把 ms 钳到 lastMs，并重算 timeStr，避免与历史 ID 碰撞
- 在锁内取 `time.Now()`，保证同一锁观察到的 ms 单调非递减

### 1.3 数据载体

`memory_sessions` 与 `memory_chats` 两张表的 DDL、字段语义、索引设计、写入事务约定详见 [数据库后端设计 §1.9.6 / §1.9.7](2026-06-10-database-backend-design.md)。本节只列与 memory 业务直接相关的关键点：

#### 1.3.1 memory_sessions

会话元数据表，**不存对话历史正文**。轻量字段：

| 列 | 含义 |
|---|---|
| `session_id` | 主键，业务 ID |
| `user_id` / `prompt` | 预留，未启用 |
| `round` | 当前对话总轮数；`SaveChat` 在事务内 +1 |
| `updated_at` | 每次新增 chat 时刷新；反映会话最后活跃时间 |

#### 1.3.2 memory_chats

每次 `/chat` 调用的完整结构化记录。一行一轮对话。关键列：

| 列 | 类型 | 含义 |
|---|---|---|
| `chat_id` | 主键 | 业务 ID |
| `session_id` / `round` | 外键风格 + INT | 所属 session + 该 session 内的轮次（主 Agent 推进 round；子 Agent 沿用父 round） |
| `agent_name` | VARCHAR(64) | 主 Agent 为空字符串 `''`；子 Agent 为对应 agent name |
| `caller` | VARCHAR(64) | `user` / `schedule` / `internal_system` 等 |
| `prompt` / `instruction` / `result` | LONGTEXT | 系统提示 + 用户指令 + 助手回复 |
| `steps` | LONGTEXT JSON | ReAct 步骤数组（每行的 steps 各自记录该 Agent 自身的工具调用序列） |
| `status` | VARCHAR(16) | `'running' \| 'completed' \| 'failed' \| 'cancelled'` |
| `error` | TEXT JSON | 失败原因 `{"code":"...","message":"..."}`；成功为空字符串 |
| `model` | VARCHAR(64) | 本次执行实际选用的模型 ID（如 `gpt-4o`、`claude-opus-4-7`） |
| `prompt_tokens` / `completion_tokens` / `total_tokens` | INT | LLM token 计数；按事件源 Agent 分别归属 |
| `duration_ms` | BIGINT | 执行耗时（毫秒） |
| `started_at` / `finished_at` | BIGINT | 毫秒戳；运行中 `finished_at = NULL` |

`(session_id, round)` 仅作非唯一索引——子 Agent 行沿用父 chat 的 round，会与主 Agent 同 round 共存；主 Agent 同 round 唯一性靠 `SaveChat` 事务里的乐观锁（CAS `UPDATE memory_sessions SET round=next WHERE round=cur`）保证。

### 1.4 子 Agent 记录策略

子 Agent 的对话执行记录**与主 Agent 同表（`memory_chats`）持久化**，通过 `agent_name` 区分；区别在于 round 与 session 的关系：

- 主 Agent 行：`agent_name = ''`，写入时事务推进 `memory_sessions.round`
- 子 Agent 行：`agent_name = '<sub agent name>'`，`round` 取调用方传入的父 chat round，**不推进 `memory_sessions.round`**

设计原因：

- session 的 round 是「主 Agent 视角」的轮次，子 Agent 不消耗 round
- `LoadHistory` 过滤 `agent_name = ''` 后得到的全是主 Agent 轮次，上下文顺序清晰
- 子 Agent 仍以独立行存在，token / steps / model 等字段不丢失，可观测性完整

CallAgentTool 在子 Agent 调用结束时，把累加器中按 `chat_id` 聚合的 token / steps / model / duration 一起写入子 Agent 行；调用失败也会写入（`status='failed'` + `error` JSON），不会静默丢失。

### 1.5 Memory 接口

```go
type Memory interface {
    // Session 管理
    CreateSession(sessionID, userID string) error
    ExistsSession(sessionID string) bool
    GetSessionInfo(sessionID string) (*SessionInfo, error)
    ListSessions(limit, offset int) ([]SessionInfo, int, error)

    // History 管理
    AppendMessage(sessionID string, message *Message) error  // DB 模式下为 no-op，签名保留
    GetHistory(sessionID string) (*History, error)
    GetRoundCount(sessionID string) int
    GetContextMessages(sessionID string, windowSize int) ([]Message, error)

    // Chat 记录管理
    SaveChatRecord(sessionID string, record *ChatRecord) error
    GetChatRecord(sessionID string, chatID string) (*ChatRecord, error)
    GetLatestChatRecord(sessionID string) (*ChatRecord, error)

    // 会话规则
    GetSessionMdContent(sessionID string) (string, error)

    // 兼容签名（DB 模式下永远返回空字符串）
    GetMemoryDir() string
}
```

`Manager` 是 `Memory` 的唯一实现，构造签名：

```go
func NewManager(log *logger.Logger, memRepo repo.MemoryRepo) *Manager
```

**强约束**：`memRepo == nil` 时 `panic("memory: NewManager: memRepo must not be nil")`。Manager 必须持有 `MemoryRepo` 引用，启动期通过 `repofactory.NewRepos` 构造的进程级单例注入。

`Manager` 在接口之外额外提供 `DeleteSession(sessionID string) error` 方法，调用 `MemoryRepo.DeleteSession` 删除会话及其所有对话记录；该方法不在 `Memory` 接口中。

### 1.6 类型归属

数据类型归属于 `internal/repo/memory.go`，`internal/memory/types.go` 通过类型别名转发，避免业务包与实现层的循环依赖：

```go
// internal/memory/types.go
type ChatRecord = repo.ChatRecord
type Step      = repo.Step
type Error     = repo.Error
```

`memory` 包额外定义 `SessionInfo` / `Message` / `History` 三个 API/视图层结构体，由 `Manager.GetHistory` / `ListSessions` 等方法把 `repo.ChatRecord` 转换成对外结构。

### 1.7 两条读路径

```
                     ┌───────────────────────┐
                     │      Memory 模块       │
                     │  memory_chats 实时聚合  │
                     └──────┬────────────────┘
                            │
               ┌────────────┴────────────┐
               │                         │
               ▼                         ▼
     ┌──────────────────┐      ┌──────────────────────┐
     │   API 查询路径     │      │   LLM 上下文路径       │
     │  GetHistory()    │      │  GetContextMessages() │
     │  返回全部轮次      │      │  返回最近 N 轮（截断）  │
     └──────────────────┘      └──────────────────────┘
```

- **API 路径**（`GET /sess/{sid}`、`GET /chat/{sid}`）：`MemoryRepo.LoadHistory` 返回该 session 全部主 Agent 完成轮次（`agent_name=''` AND `status='completed'`，按 `round ASC` 排序）
- **LLM 上下文路径**：`GetContextMessages` 调用 `GetHistory` 后按 `windowSize` 截取最后 N 条；`windowSize <= 0` 不限制

### 1.8 持久化路径

#### 1.8.1 写入

| 接口方法 | 实现 |
|---|---|
| `CreateSession` | `MemoryRepo.CreateSession(ctx, &Session{...})`（INSERT，session_id 已存在返回 `repo.ErrConflict`） |
| `SaveChatRecord` | `MemoryRepo.SaveChat(ctx, rec)`：事务内 `SELECT round → INSERT memory_chats → UPDATE memory_sessions` |

`SaveChat` 的并发写策略详见 [数据库后端设计 §1.9.7 写入新 chat 的事务约定](2026-06-10-database-backend-design.md#197-memory_chats)。

#### 1.8.2 读取

| 接口方法 | 实现 |
|---|---|
| `GetSessionInfo` | `MemoryRepo.GetSession`，`ErrNotFound` 包装为 `会话不存在: {sid}` |
| `GetHistory` | `MemoryRepo.GetSession` + `LoadHistory`，把 `[]ChatRecord` 转为 `History.Messages` |
| `GetChatRecord` | `MemoryRepo.GetChat`，`ErrNotFound` 包装为 `对话记录不存在: {cid}` |
| `GetLatestChatRecord` | `LoadHistory` 取末位 |
| `ExistsSession` | `MemoryRepo.ExistsSession` |
| `GetRoundCount` | `MemoryRepo.GetSession` 取 `Round` |

#### 1.8.3 列举

`ListSessions(limit, offset)` 调用 `MemoryRepo.ListSessions` 取全量按 `updated_at DESC` 排序，再在内存做 `[offset:offset+limit]` 切片。返回 `(infos, total, err)`，total 始终是全量计数。

### 1.9 会话规则常量

`defaultSessionRules` 通过 `//go:embed session_rules.md` 嵌入二进制；`GetSessionMdContent(sessionID)` 直接返回该常量并将 `error` 置为 `nil`，忽略 `sessionID` 参数。

```
internal/memory/
├── session_rules.go        //go:embed session_rules.md
└── session_rules.md        规则正文（当前为空文件，预留给后续填充）
```

所有会话共享同一份规则；不写入任何物理文件。

### 1.10 配置

```yaml
memory:
  history_window: 20              # LLM 上下文窗口（轮次数），-1 不限制
```

| 配置项 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `history_window` | int | `20` | LLM 上下文最大轮次数；-1 不限制 |

> `MemoryConfig.Directory` 字段仍保留在结构体中作为兼容字段，业务代码不再读取——DB 模式下不存在"memory 目录"概念。

### 1.11 错误处理

| 场景 | 处理 |
|---|---|
| 会话不存在（`MemoryRepo.GetSession` 返 `repo.ErrNotFound`） | `GetSessionInfo` / `GetHistory` 返回 `"会话不存在: {sessionID}"` |
| 对话记录不存在（`MemoryRepo.GetChat` 返 `repo.ErrNotFound`） | `GetChatRecord` 返回 `"对话记录不存在: {chatID}"` |
| `SaveChat` 主 Agent 路径乐观锁失败（`UPDATE memory_sessions SET round=... WHERE round=cur_round` 0 行） | 返 `repo.ErrConflict`；调用方需重新 `GetSession` 获取最新 round 重试 |

### 1.12 实现约束

**类型归属**

- `ChatRecord` / `Step` / `Error` 定义在 `internal/repo/memory.go`，`memory` 包通过类型别名导出
- `Session` 类型定义在 `internal/repo/memory.go`，`memory` 包用 `SessionInfo` 视图层结构对外
- `Message` / `History` / `SessionInfo` 由 `internal/memory/types.go` 拥有

**ID 生成**

- 所有 ID 生成函数收敛到 `memory/idgen.go`：`GenerateSessionID` / `GenerateChatID` / `GenerateStepID` / `GenerateChildChatID`
- 随机源统一 `crypto/rand`

**活跃状态管理**

- 活跃对话状态唯一数据源是 `RuntimeState.activeChats`（不在 DB 中）
- 状态查询走 `RuntimeState` 接口，不在 Memory 重复实现

**status 映射**

| 执行结果条件 | status 值 |
|---|---|
| `ctx.Err() == context.Canceled` | `cancelled` |
| `err != nil`（执行异常） | `failed` |
| `result.Cancelled == true` | `cancelled` |
| `result != nil`（正常完成） | `completed` |
| 其他未知情况 | `failed` |

ChatRecord 与 Message 的 status 映射逻辑只在 executor 中出现一次，Message 从 ChatRecord 拷贝。

**Repo 持有**

- Manager 必须持有 `repo.MemoryRepo` 引用（构造期校验非 nil）
- 启动期通过 `repofactory.NewRepos` 构造进程级单例后注入
- 运行期不切换后端

### 1.13 实现文件

| 文件 | 说明 |
|---|---|
| `internal/repo/memory.go` | `MemoryRepo` 接口 + `Session` / `ChatRecord` / `Step` / `Error` 数据结构 |
| `internal/repo/memorydb/memory.go` | `MemoryRepo` 数据库实现（SQLite / MySQL / PostgreSQL 共用一份 SQL） |
| `internal/memory/types.go` | `SessionInfo` / `Message` / `History` + ChatRecord 类型别名 |
| `internal/memory/memory.go` | `Memory` 接口定义 |
| `internal/memory/manager.go` | `Manager` 实现（持有 `MemoryRepo`） |
| `internal/memory/idgen.go` | ID 生成器（4 种 ID） |
| `internal/memory/session_rules.go` | `//go:embed` 加载 session_rules.md |
| `internal/memory/session_rules.md` | 会话规则正文 |
| `internal/config/config.go` | `MemoryConfig` 配置项 |

### 1.14 启动与停止

**启动**：

1. `db.Open(cfg.Database, homeDir)` 初始化数据库连接
2. `repofactory.NewRepos(sqlxDB, dialect, homeDir)` 构造 `MemoryRepo`
3. `memory.NewManager(log, repos.Memory)`

**停止**：上层关闭 `*sqlx.DB`。Memory 模块自身无后台任务需要停止。

### 1.15 设计决策记录

| 决策 | 结论 | 原因 |
|---|---|---|
| 持久化抽象 | `MemoryRepo` 接口 + `memorydb` 实现 | 一套 SQL 同时支持 SQLite / MySQL / PostgreSQL，方言差异由 `db.Dialect` 适配 |
| 历史正文存放 | `memory_chats` 行式存储，无单独 history.json | 行式按 `(session_id, round)` 自然有序；`LoadHistory` 实时聚合避免主从写放大 |
| ChatRecord 字段拆列 vs 单 JSON | 关键字段（status / round / agent_name / *_tokens / duration_ms）拆列，剩余 JSON | 索引/查询走列；扩展信息保留 JSON 不破坏 schema |
| 子 Agent 记录 | 与主 Agent 同表存储，`agent_name != ''` 区分；沿用父 round，不推进 session.round | token / steps / model / duration 完整可观测；session 的 round 仍是主 Agent 视角，`LoadHistory` 过滤 `agent_name=''` 即可只取主 Agent 轮次 |
| 会话规则存储 | `go:embed` 常量 | 所有会话共享同一份规则，无需会话级定制 |
| 会话生命周期 | 不做定时清理，会话长期保留在数据库中 | 数据库后端可承载长期数据；用户/运维可按需通过 SQL 或 `DeleteSession` 接口显式删除 |
| history.json 是否截断存储 | 不存 history.json，DB 全量保留 | API 需要返回完整历史；LLM 上下文按 round 截断 |
| LLM 上下文如何控制 | 读取时按轮次截断 | 简单有效，无需 token 计数 |
| 是否做摘要压缩 | 不做（`memory_sessions.prompt` 列预留以后用） | 先上纯截断；session 级长会话压缩摘要将来由 prompt 列承接 |
| 是否做行锁 | `SaveChat` 主 Agent 路径走「读 round → INSERT → CAS UPDATE round」乐观锁；子 Agent 路径仅 INSERT + 刷 updated_at | 同 session 并发写极少，乐观锁足够；子 Agent 行不参与 round 推进，无需互斥 |
| 多租户隔离 | 不做（`user_id` 列预留） | 一个实例服务一个企业应用 |

## 二、迭代说明

### 2.1 与上一版差异

历史版本基于 `storage.Storage` 接口 + `history.json` + `chats/{chat_id}.json` 文件实现，文档详见 [`archive/2026-05-11-memory-design.md`](archive/2026-05-11-memory-design.md)。

#### 持久化抽象

- **新增**：`repo.MemoryRepo` 接口（`CreateSession` / `GetSession` / `ExistsSession` / `ListSessions` / `SaveChat` / `GetChat` / `LoadHistory` / `DeleteSession`）
- **退役**：`storage.Storage` 接口在 memory 模块内的全部使用；`Manager` 不再持有 `storage.Storage` 字段
- **新增**：`internal/repo/memorydb/` 实现 `MemoryRepo`

#### 数据载体

- **调整**：会话元数据从 `{memoryDir}/{session_id}/history.json` 文件迁移到 `memory_sessions` 表
- **调整**：每轮对话详情从 `{memoryDir}/{session_id}/chats/{chat_id}.json` 文件迁移到 `memory_chats` 表
- **调整**：对话历史不再独立落盘——`GetHistory` / `GetContextMessages` 实时聚合 `memory_chats`
- **新增**：`memory_chats` 拆出 `agent_name` / `caller` / `prompt` / `*_tokens` / `started_at` / `finished_at` 等结构化列
- **新增**：`(session_id, round)` 索引（非唯一）+ `SaveChat` 事务内 CAS 乐观锁防主 Agent 并发写冲突

#### 类型归属

- **调整**：`ChatRecord` / `Step` / `Error` 类型从 `internal/memory/types.go` 移到 `internal/repo/memory.go`；`memory` 包通过 `type ChatRecord = repo.ChatRecord` 别名转发，调用方无需改代码
- **保留**：`SessionInfo` / `Message` / `History` 仍在 `internal/memory/types.go`

#### 接口签名

- **调整**：`NewManager(memoryDir, retentionDays, log, store)` → `NewManager(log, memRepo)`
- **调整**：`Memory` 接口移除 `Cleanup(ctx) (int, error)` 方法
- **保留**：`Memory` 接口其余方法签名向后兼容；`AppendMessage` 在 DB 模式下退化为 no-op（`SaveChat` 已自动维护 round）；`GetMemoryDir()` 返回空字符串

#### 配置入口

- **移除**：`memory.retention_days`、`memory.cleanup_schedule` 配置项
- **移除**：`memory.directory` 配置项（DB 模式下无 memoryDir 概念）
- **移除**：`env.yaml` 中的 `minio` 节
- **新增**：`env.yaml` 中的 `database` 节决定后端
- **保留**：`memory.history_window` 不变

#### 定时清理

- **移除**：`internal/memory/cleanup.go` 整文件、`Manager.Cleanup` 方法、`MemoryRepo.DeleteExpiredSessions` 接口及其 `memorydb` 实现
- **移除**：`gocron` 注册的每日 memory 清理 Job
- **保留**：`MemoryRepo.DeleteSession` 显式删除单个会话能力不变（API 层的 delete session 仍走该接口）

#### 附件接口

- **保留**：`Memory` 接口不再包含附件相关方法（自上一版起就已经移除）；附件仅由 `internal/attachment/` 做请求级校验，不做持久化（详见 [附件与会话规则设计](2026-06-05-attachment-and-session-rules-design.md)）
