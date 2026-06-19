# Memory 模块设计

> ⚠️ **本文档已归档**：memory 模块已迁移到 `repo.MemoryRepo` + 数据库 `memory_sessions` / `memory_chats` 表实现，原"基于 storage.Storage 接口 + history.json + chats/{chat_id}.json 文件"的方案已退役。
>
> **后续设计**：见同目录 [`../2026-05-11-memory-design.md`](../2026-05-11-memory-design.md)（已重写为数据库后端版本）。
>
> 本文档仅作历史参考保留。

---

## 一、功能设计

### 1.1 概述

Memory 模块负责 Groot 会话数据的持久化存储。基于 Storage 抽象层（local 文件系统或 MinIO 对象存储），运行期不感知后端类型。

**核心设计原则：**

- **两级存储**：`history.json`（会话索引） + `chats/{chat_id}.json`（单轮详情）
- **全量保存、按需截断**：history.json 保存全部轮次，传递给 LLM 的上下文只取最近 N 轮
- **Storage 抽象**：所有持久化操作走 `storage.Storage` 接口（Read/Write/List/Stat/Delete/DeleteDir），原子写入由实现层保证（local: tmp+rename；minio: PutObject 协议）

**核心概念层级：**

```
Session（会话）
  └─ Round / Chat（轮次，一次请求-响应）
       └─ Step（步骤，一次工具调用或 LLM 输出）
```

### 1.2 ID 格式与目录映射

四个 ID 均由 `internal/memory/idgen.go` 生成。

#### 1.2.1 ID 格式

| ID | 格式 | 示例 |
|----|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260418100000523_a1b2` |
| `chat_id`（主 Agent） | `chat_{YYYYMMDDHHMMSSmmm}` | `chat_20260418100000523` |
| `step_id` | `{YYYYMMDD}-{HHMMSSmmm}-{random6}` | `20260418-100005000-a1b2c3` |
| `child_chat_id`（子 Agent） | `{parentChatID}_{HHMMSSmmm}_{random4}_{agentName}` | `chat_20260418100000523_100002500_a1b2_db-agent` |

- 时间戳精确到毫秒（`mmm` = 毫秒，3 位）
- `random4` / `random6` 由小写字母 + 数字组成
- 随机源统一使用 `crypto/rand`

#### 1.2.2 子 Agent ID 防碰撞策略

`GenerateChildChatID(parentChatID, agentName)` 在并发高频调用下需保证 ID 唯一：

- **每毫秒随机起点 + 同毫秒严格自增**：`random4 = base36((offset + counter) mod 36^4, 4)`
- 同一毫秒内 `counter` 严格递增，不重抽 offset
- `offset` 仅在 ms 严格大于 lastMs 时重新随机
- 系统时钟回退（`ms < lastMs`）时把 ms 钳到 lastMs，并重算 timeStr，避免"老 timeStr + 新 counter"与历史 ID 碰撞
- 在锁内取 `time.Now()`，保证同一锁观察到的 ms 单调非递减

#### 1.2.3 ID → Storage 路径的映射

```
session_id  →  {memoryDir}/{session_id}/                          （会话目录）
chat_id     →  {memoryDir}/{session_id}/chats/{chat_id}.json      （单轮详情文件）
step_id     →  嵌在 ChatRecord.steps[].step_id 中，无独立文件
```

> **关键约束**：`chat_id` 必须与所属 `session_id` 配合才能定位文件。

### 1.3 目录结构

```
{memoryDir}/
├── 20260418100000523_a1b2/          ← Session A 目录
│   ├── history.json                   ← 会话索引（全部轮次摘要）
│   ├── chats/
│   │   ├── chat_20260418100000523.json
│   │   └── chat_20260418100500123.json
│   └── attachments/
│       ├── report.pdf
│       └── data.csv
├── 20260419103000523_c3d4/            ← Session B 目录
│   ├── history.json
│   ├── chats/
│   │   └── chat_20260419103000523.json
│   └── attachments/
└── ...
```

**一个 Session 一个目录，目录名即 SessionID。**

会话规则提示由内置常量 `defaultSessionRules` 提供（`//go:embed session_rules.md`），不再写入物理文件 `SESSION.md`。

### 1.4 数据结构

#### 1.4.1 history.json — 会话索引

```json
{
  "session_id": "20260418100000523_a1b2",
  "created_at": "2026-04-18T10:00:00Z",
  "messages": [
    {
      "round": 1,
      "chat_id": "chat_20260418100000523",
      "timestamp": "2026-04-18T10:00:00Z",
      "instruction": "用户指令内容",
      "result": "助手回复内容",
      "status": "completed",
      "duration": 45,
      "steps_count": 3,
      "agent_name": "",
      "error": null
    }
  ]
}
```

`Message.AgentName`（v3.8 新增）：Solo 模式持久化的子 Agent 名；主 Agent 通常省略，使用 `omitempty`。

#### 1.4.2 chats/{chat_id}.json — 单轮执行档案

```json
{
  "chat_id": "chat_20260418100000523",
  "session_id": "20260418100000523_a1b2",
  "round": 1,
  "timestamp": "2026-04-18T10:00:00Z",
  "started_at": "2026-04-18T10:00:00Z",
  "ended_at": "2026-04-18T10:00:45Z",
  "instruction": "帮我分析这份PDF报告",
  "result": "分析结果如下...",
  "status": "completed",
  "duration": 45,
  "caller": "internal_system",
  "steps": [...],
  "agent_name": "",
  "prompt_tokens": 0,
  "completion_tokens": 0,
  "total_tokens": 0,
  "error": null
}
```

JSON 字段策略：
- `Error` 不带 `omitempty`：消费方可稳定假设此 key 存在（值为 null 或对象）
- v3.8 多 Agent 扩展字段（`AgentName` / `PromptTokens` / `CompletionTokens` / `TotalTokens`）使用 `omitempty`，主 Agent 主路径输出格式不变

### 1.5 Memory 接口

```go
type Memory interface {
    // Session 管理
    CreateSession(sessionID string) error
    ExistsSession(sessionID string) bool
    GetSessionInfo(sessionID string) (*SessionInfo, error)
    ListSessions(limit, offset int) ([]SessionInfo, int, error)

    // History 管理
    AppendMessage(sessionID string, message *Message) error
    GetHistory(sessionID string) (*History, error)
    GetRoundCount(sessionID string) int
    GetContextMessages(sessionID string, windowSize int) ([]Message, error)

    // Chat 记录管理
    SaveChatRecord(sessionID string, record *ChatRecord) error
    GetChatRecord(sessionID string, chatID string) (*ChatRecord, error)
    GetLatestChatRecord(sessionID string) (*ChatRecord, error)

    // 会话规则
    GetSessionMdContent(sessionID string) (string, error)

    // 清理
    Cleanup(ctx context.Context) (int, error)

    // 目录路径
    GetMemoryDir() string
}
```

`Manager` 是 `Memory` 的唯一实现，构造签名：

```go
func NewManager(memoryDir string, retentionDays int, log *logger.Logger, store storage.Storage) *Manager
```

**强约束**：`store == nil` 时 `panic("memory: NewManager: storage must not be nil")`。Manager 必须持有 `storage.Storage` 引用，启动期通过 `storage.New(cfg.Storage)` 构造的进程级单例注入。

### 1.6 两条读路径

```
                     ┌──────────────────┐
                     │    Memory 模块    │
                     │   history.json    │
                     └──────┬───────────┘
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

- **API 路径**（`GET /sess/{sid}`、`GET /chat/{sid}`）：返回全部历史
- **LLM 上下文路径**：只取最后 N 轮，N 由 `history_window` 配置

`GetContextMessages` 规则：`windowSize <= 0` 不限制，否则返回最后 `windowSize` 条。

### 1.7 持久化路径（全部走 Storage 接口）

#### 1.7.1 不预创建目录

`NewManager` **不预创建** `memoryDir`：所有目录由 `storage.Write` 在第一次写入时按需建立：

- local 实现：内部 `MkdirAll`
- minio 模式：目录是隐式前缀，无需预建

这样在 minio 模式下不会在进程 cwd 下意外创建一个名为 `memoryDir` 的本地目录。

#### 1.7.2 写入操作

| 方法 | 路径 | content-type | 原子性来源 |
|---|---|---|---|
| `saveHistory` | `{sessionDir}/history.json` | `application/json` | storage 实现 |
| `SaveChatRecord` | `{sessionDir}/chats/{chat_id}.json` | `application/json` | storage 实现 |

所有写入都是 `storage.Write(ctx, path, reader, size, contentType)` 单次调用，原子性由实现层保证（local: tmp+rename；minio: PutObject 协议）。Manager 不再实现 tmp+rename。

#### 1.7.3 读取操作

| 方法 | 实现 |
|---|---|
| `GetHistory` | `storage.Read` → `io.ReadAll` → `json.Unmarshal`；`ErrNotFound` 包装为"会话不存在" |
| `GetChatRecord` | 同上，`ErrNotFound` 包装为"对话记录不存在" |
| `ExistsSession` | `storage.Stat(historyPath)` 不返回错误即视为存在 |

#### 1.7.4 列举操作

`listSessionIDs(ctx)` —— 私有 helper，统一会话 ID 列举：

1. `storage.List(ctx, memoryDir)`
2. `ErrNotFound` 视为空切片（首次启动 / 全清理后）—— 这是对外可观测的行为
3. 过滤非目录条目
4. 对每个 sessionID 调用 `ExistsSession` 校验有 `history.json`

`ListSessions` 与 `Cleanup` 都在内部复用此 helper。

### 1.8 会话规则常量

`defaultSessionRules` 通过 `//go:embed session_rules.md` 嵌入二进制，`Manager.GetSessionMdContent` 直接返回该常量。

```
internal/memory/
├── session_rules.go        //go:embed session_rules.md
└── session_rules.md        规则正文
```

`GetSessionMdContent(sessionID string) (string, error)` 接口签名保持不变；实现忽略 `sessionID` 参数，直接返回 `defaultSessionRules`，err 永远为 nil。所有会话共享同一份规则。

### 1.10 Cleanup 流程

`Cleanup(ctx) (int, error)` 清理过期会话：

1. `listSessionIDs(ctx)` 列举有效 sessionID
2. `cutoff := time.Now().AddDate(0, 0, -retentionDays)`
3. 对每个 sessionID：
   - `storage.Stat(sessionDir)` 取 ModTime；失败 → 跳过该 session
   - `ModTime.Before(cutoff)` → `storage.DeleteDir(sessionDir)`
4. 任意一步失败时跳过该 session（`deleted` 不增加），下次 Cleanup 自动重试

#### 1.11.1 一次性删整个 sessionDir 的设计决策

整个 sessionDir（含 `history.json` / `chats/` / `attachments/` / 旧版残留 `SESSION.md`）通过单次 `storage.DeleteDir` 一次性删除，**不拆分**为"先删附件再删元数据"两步。

**原因**：拆分会引入"元数据已删但附件残留"或反向的不一致状态。整体一次性删，失败时整体重试，保证元数据与附件始终同步。

#### 1.11.2 时间判断使用目录 ModTime

使用目录 ModTime 而非 `history.json` 中的 `created_at`：会话可能创建很早但持续活跃，用创建时间会误删仍在使用的会话。每次写 `history.json` 或 `chats/` 都会更新目录 ModTime（local 实现）或前缀下最新对象的 LastModified（minio 实现，由 `Stat` 取前缀最大 ModTime）。

#### 1.11.3 旧 SESSION.md 物理文件处理

升级前已经存在的 session 目录里可能残留 `SESSION.md` 物理文件：

- 新版 `GetSessionMdContent` 不再读取该物理文件，直接返回常量，旧文件**不影响功能**
- 不主动迁移、不主动删除；`Cleanup` 走 `DeleteDir(sessionDir)` 时会随会话过期一并清理

### 1.12 配置

```yaml
memory:
  directory: memory               # 记忆目录（相对路径或绝对路径）
  retention_days: 7               # 会话保留天数
  cleanup_schedule: "02:00"       # 清理时间（HH:MM）
  history_window: 20              # LLM 上下文窗口（轮次数），-1 不限制
```

| 配置项 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `directory` | string | `memory` | 存储目录，支持相对/绝对路径 |
| `retention_days` | int | `7` | 会话保留天数 |
| `cleanup_schedule` | string | `02:00` | 每日清理时间（gocron 解析） |
| `history_window` | int | `20` | LLM 上下文最大轮次数 |

清理通过 gocron 统一调度引擎执行（详见 [定时任务调度系统设计](2026-05-11-schedule-design.md)），`memory` 包提供 `Task` 实现。

### 1.13 错误处理

| 场景 | 处理 |
|---|---|
| 会话不存在（`storage.ErrNotFound` on `historyPath`）| `GetHistory` 返回 `"会话不存在: {sessionID}"` |
| 对话记录不存在（`storage.ErrNotFound` on `chatPath`） | `GetChatRecord` 返回 `"对话记录不存在: {chatID}"` |
| memoryDir 不存在 | `listSessionIDs` 视同空切片（首次启动 / 全清理后）|
| history.json 解析失败 | 返回 `"解析 history 失败: ..."`，记录 ERROR 日志 |
| 附件写入失败 | 返回 `"保存附件失败: ..."` |
| Cleanup 时某 session Stat 失败 | 记录 INFO 日志（`跳过会话（无法获取目录信息）`），跳过 |
| Cleanup 时 DeleteDir 失败 | 记录 ERROR 日志，跳过该 session 下次重试 |

### 1.14 实现约束

**类型归属**

- `Message`、`History`、`ChatRecord`、`Step`、`Error`、`SessionInfo` 统一归属 `memory/types.go`，其他包通过 `import` 引用，不得在他处重复定义

**ID 生成**

- 所有 ID 生成函数统一收敛到 `memory/idgen.go`：`GenerateSessionID` / `GenerateChatID` / `GenerateStepID` / `GenerateChildChatID`
- 随机源统一使用 `crypto/rand`

**活跃状态管理**

- 活跃对话状态唯一数据源是 `RuntimeState.activeChats`
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

**Storage 持有**

- Manager 必须持有 `storage.Storage` 引用（构造期校验非 nil）
- 启动期通过 `storage.New(cfg.Storage)` 构造进程级单例后注入
- 运行期不切换后端

### 1.15 实现文件

| 文件 | 说明 |
|---|---|
| `internal/memory/types.go` | 数据结构（SessionInfo, Message, History, ChatRecord, Step, Error） |
| `internal/memory/memory.go` | Memory 接口定义 |
| `internal/memory/manager.go` | Manager 实现（含 Storage 字段） |
| `internal/memory/idgen.go` | ID 生成器（4 种 ID） |
| `internal/memory/cleanup.go` | 清理 Job 实现（gocron Task 接口） |
| `internal/memory/session_rules.go` | `//go:embed` 加载 session_rules.md |
| `internal/memory/session_rules.md` | 会话规则正文 |
| `internal/config/config.go` | MemoryConfig 配置项 |

### 1.16 启动与停止

**启动**：

1. 解析 `memory.directory`（相对/绝对路径），不预创建
2. `storage.New(cfg.Storage)` 构造进程级 Storage 单例
3. `memory.NewManager(dir, retention, log, store)`
4. 向 gocron 注册清理 Job

**停止**：

1. gocron 调度器停止（Scheduler 模块统一处理）
2. 等待当前清理任务完成
3. 释放资源

### 1.17 设计决策记录

| 决策 | 结论 | 原因 |
|---|---|---|
| 存储抽象 | Storage 接口（local / minio） | 支持单机与对象存储两种部署模式 |
| 原子写入 | 由 storage 实现保证 | local: tmp+rename；minio: PutObject 协议 |
| 不预创建 memoryDir | 由 storage.Write 按需建立 | minio 模式下避免在 cwd 下意外创建本地目录 |
| 会话规则存储 | go:embed 常量 | 所有会话共享同一份规则，无需会话级定制 |
| Cleanup 删除粒度 | 单次 DeleteDir 整个 sessionDir | 避免"元数据/附件"不一致状态 |
| 时间判断 | 目录 ModTime | 反映最后活跃时间 |
| history.json 是否截断存储 | 不截断，全量保存 | API 需要返回完整历史 |
| LLM 上下文如何控制 | 读取时按轮次截断 | 简单有效，无需 token 计数 |
| 是否做摘要压缩 | 不做 | 先上纯截断 |
| 是否做文件锁 | 不做 | 同一 Session 无并发（RuntimeState 保证） |
| 多租户隔离 | 不做 | 一个实例服务一个企业应用 |

## 二、迭代说明

### 2.1 与上一版差异

#### 数据流抽象

- **新增**：Manager 注入 `storage.Storage` 字段（`NewManager` 签名增加 `store storage.Storage` 参数；`store == nil` 直接 panic）
- **调整**：`saveHistory` / `SaveChatRecord` / `SaveAttachment` / `GetHistory` / `GetChatRecord` / `ExistsSession` 全部改走 `storage.Storage` 接口
- **移除**：Manager 内部不再实现 tmp+rename，原子写入下沉到 storage 实现
- **调整**：`NewManager` 不再预创建 `memoryDir`，所有目录由 `storage.Write` 按需建立（minio 模式下避免污染 cwd）

#### Cleanup

- **调整**：从"分项删除 history.json + chats/ + attachments"改为"单次 DeleteDir 整个 sessionDir"。失败时跳过整个 session，下次 Cleanup 重试。该决策与"附件 + 元数据合并为原子操作"对齐
- **新增**：`listSessionIDs` 私有 helper，统一会话 ID 列举（`memoryDir` 不存在时返回空切片）

#### 子 Agent 支持（v3.8）

- **新增**：第四种 ID 格式 `GenerateChildChatID(parentChatID, agentName)`，含并发去碰撞策略
- **新增**：`Message.AgentName` / `ChatRecord.AgentName` / `PromptTokens` / `CompletionTokens` / `TotalTokens` 扩展字段（`omitempty`，主 Agent 主路径 JSON 输出格式不变）

#### 附件访问

- **移除**：`Memory.SaveAttachment` / `Memory.GetAttachmentPath` 接口方法（附件存储由 attachment handler 直接调用 storage.Storage，不再经 Memory 接口）
- **移除**：`Manager.AttachmentsDir(sessionID) string` 辅助方法
- **移除**：`internal/agent/file_tools.go`（`groot_file_list` / `groot_file_read` 内置工具）

#### 会话规则

- **调整**：`SESSION.md` 物理文件改为 `//go:embed session_rules.md` 嵌入常量 `defaultSessionRules`
- **调整**：`GetSessionMdContent` 实现忽略 sessionID 参数，直接返回常量；接口签名保持向后兼容
- **调整**：`CreateSession` 不再写 SESSION.md 物理文件
- **保留**：旧 session 目录中残留的 SESSION.md 物理文件不主动迁移、不主动删除，随 Cleanup 自然回收

#### 接口扩展

- **新增**：`Memory.GetMemoryDir() string` 接口方法

#### 实现文件

- **新增**：`internal/memory/session_rules.go` / `session_rules.md`
