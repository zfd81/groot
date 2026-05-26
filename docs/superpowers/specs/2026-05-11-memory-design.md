# Memory 模块设计

## 概述

Memory 模块负责 Groot 会话数据的持久化存储。基于文件系统（JSON），无外部数据库依赖。

**核心设计原则：**

- **两级存储**：`history.json`（会话索引） + `chats/{chat_id}.json`（单轮详情）
- **全量保存、按需截断**：history.json 保存全部轮次，但传递给 LLM 的上下文只取最近 N 轮
- **原子写入**：所有 JSON 文件写入采用 tmp + rename 模式，防止进程崩溃导致数据损坏

**核心概念层级：**

```
Session（会话）
  └─ Round / Chat（轮次，一次请求-响应）
       └─ Step（步骤，一次工具调用或 LLM 输出）
```

## ID 格式与目录映射

三个核心 ID 均由 `internal/memory/idgen.go` 生成，使用 `crypto/rand` 作为随机源。

### ID 格式

| ID | 格式 | 示例 |
|----|------|------|
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260418100000523_a1b2` |
| `chat_id` | `chat_{YYYYMMDDHHMMSSmmm}` | `chat_20260418100000523` |
| `step_id` | `{YYYYMMDD}-{HHMMSSmmm}-{random6}` | `20260418-100005000-a1b2c3` |

- 时间戳精确到毫秒（`mmm` = 毫秒，3 位），取自当前系统时间
- 随机字符串由小写字母 + 数字组成

### ID → 文件系统路径的映射

三个 ID 直接决定了存储路径，规则统一且无歧义：

```
session_id  →  {memoryDir}/{session_id}/                          （会话目录）
chat_id     →  {memoryDir}/{session_id}/chats/{chat_id}.json      （单轮详情文件）
step_id     →  嵌在 ChatRecord.steps[].step_id 中，无独立文件
```

**示例** — 假设 `memoryDir = memory`，`session_id = 20260418100000523_a1b2`，`chat_id = chat_20260418100000523`：

```
memory/20260418100000523_a1b2/                  ← session_id 直接做目录名
├── history.json
├── chats/
│   └── chat_20260418100000523.json               ← chat_id + ".json" 做文件名
└── attachments/
```

> **关键约束：** `chat_id` 必须与它所属的 `session_id` 配合才能定位文件（路径中包含两者）。单独一个 `chat_id` 无法找到文件。

## 目录结构

```
{memoryDir}/
├── 20260418100000523_a1b2/          ← Session A 目录
│   ├── SESSION.md                     ← 附件目录路径提示（注入 LLM 上下文）
│   ├── history.json                   ← 会话索引（全部轮次摘要）
│   ├── chats/
│   │   ├── chat_20260418100000523.json  ← 第1轮详细记录
│   │   └── chat_20260418100500123.json  ← 第2轮详细记录
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

## 数据结构

### history.json — 会话索引（目录页）

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

### chats/{chat_id}.json — 单轮执行档案（正文）

```json
{
  "chat_id": "chat_20260418100000523",
  "session_id": "20260418100000523_a1b2",
  "round": 1,
  "timestamp": "2026-04-18T10:00:00Z",
  "started_at": "2026-04-18T10:00:00Z",
  "ended_at": "2026-04-18T10:00:45Z",
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

**两者关系：**

- `history.json` 是目录页，存全部轮次的摘要信息（含 instruction 和 result），看一眼就知道会话全貌
- `chats/{chat_id}.json` 是正文，存单轮的完整执行详情（含所有 steps、caller 等）
- 通过 `chat_id` 关联，`history.json` 中的每条 Message 对应 `chats/` 下一个文件
- instruction 和 result 在两个文件中都有存储，这是有意为之——避免只看目录页时还需要逐个打开 chat 文件

## 两条读路径

Memory 有两个消费者，需求不同：

```
                     ┌──────────────────┐
                     │    Memory 模块    │
                     │   history.json    │  ← 存全部轮次
                     └──────┬───────────┘
                            │
               ┌────────────┴────────────┐
               │                         │
               ▼                         ▼
     ┌──────────────────┐      ┌──────────────────────┐
     │   API 查询路径     │      │   LLM 上下文路径       │
     │                  │      │                      │
     │  GetHistory()    │      │  GetContextMessages() │
     │  返回全部轮次      │      │  返回最近 N 轮（截断）   │
     │                  │      │                      │
     │  消费者：调用方应用 │      │  消费者：LLM 模型       │
     └──────────────────┘      └──────────────────────┘
```

- **API 路径**（`GET /sess/{sid}`、`GET /chat/{sid}`）：调用 `GetHistory`，返回全部历史，用户可查看完整对话
- **LLM 上下文路径**（构建 Agent 消息列表时）：调用 `GetContextMessages`，只取最后 N 轮，N 由 `history_window` 配置

两条路径读的是**同一份 history.json**，区别只是返回时是否截断。

**为什么 LLM 上下文需要截断：**

- 长会话的全部历史会撑爆 LLM 上下文窗口
- 过早的对话轮次对当前任务帮助有限
- 每轮都传全量历史导致 API 延迟线性增长

**截断策略：纯窗口截断（当前方案）**

取最近 N 轮原文，窗口外的直接丢弃。简单有效，零额外成本。

> **未来扩展方向：** 如果纯截断导致早期重要上下文丢失，可在窗口外轮次上叠加「摘要压缩」——将窗口外的轮次用 LLM 压缩为一段摘要，拼在窗口内消息前面。当前版本不实现此功能。

## 会话管理

| 能力 | 说明 |
|------|------|
| CreateSession | 创建会话目录和 history.json |
| ExistsSession | 检查会话是否存在 |
| GetSessionInfo | 获取会话信息（含 last_active_at） |
| ListSessions | 查询会话列表（支持分页） |

## 历史持久化

| 能力 | 说明 |
|------|------|
| AppendMessage | 向 history.json 追加新一轮记录 |
| GetHistory | 读取全部历史消息（API 查询用） |
| GetContextMessages | 读取最近 N 轮消息（LLM 上下文用） |
| GetRoundCount | 获取对话轮数 |
| SaveChatRecord | 保存详细执行记录到 chats/{chat_id}.json |
| GetChatRecord | 获取单次对话详情 |
| GetLatestChatRecord | 获取最近一次对话详情 |

**GetContextMessages 说明：**

```
输入：sessionID, windowSize
输出：history.Messages 的最后 windowSize 条
规则：windowSize <= 0 表示不限制，返回全部
```

**对话完成后持久化流程：**

```
Executor 执行完成 →
  1. SaveChatRecord(sessionID, chatRecord)  → 写入 chats/{chat_id}.json
  2. AppendMessage(sessionID, message)       → 追加到 history.json
```

## 附件存储

| 能力 | 说明 |
|------|------|
| SaveAttachment | 保存附件到会话目录（返回完整路径） |
| GetAttachmentPath | 获取附件完整路径 |

**附件命名规则：**
- 保留原始文件名，不添加前缀
- 同名文件会覆盖
- 文件名记录在 history.json 的 `attachments` 字段

## 定时清理

Memory 模块的会话过期清理通过 gocron 统一调度引擎执行，不再使用独立的 `CleanupScheduler`。

> gocron 调度引擎由 Schedule 模块管理，详细设计见 [定时任务调度系统设计](2026-05-11-schedule-design.md)。

**配置项：**

```yaml
memory:
  directory: memory               # 记忆目录
  retention_days: 7               # 会话保留天数
  cleanup_schedule: "02:00"       # 清理时间（HH:MM）
  history_window: 20              # LLM 上下文窗口（轮次数），-1 不限制
```

**清理逻辑（作为 gocron Job 执行）：**

```
触发：每天 cleanup_schedule 时间（由 gocron 调度）
流程：
  1. 遍历 memory 目录下所有子目录
  2. 获取每个会话目录的最后修改时间（ModTime）
  3. 计算空闲时间 = 当前时间 - ModTime
  4. 如果空闲时间 >= retention_days * 24小时：
     - 删除整个会话目录（history.json + chats/ + attachments）
     - 删除成功后记录日志：[INFO] [memory] 清理会话 {sessionID}，最后活跃：{modTime}，轮数：{roundCount}
     - 删除失败记录日志：[ERROR] [memory] 清理会话 {sessionID} 失败：{error}
  5. 汇总日志：[INFO] [memory] 清理完成，删除 {count} 个会话，剩余 {remain} 个
```

> **设计决策：** 使用目录 ModTime 判断过期，而非 history.json 中的 `created_at`。原因：会话可能创建很早但持续活跃，用创建时间会误删仍在使用的会话。每次写 `history.json` 或 `chats/` 都会更新目录 ModTime，自然反映最后活跃时间。

## 数据安全

**原子写入（所有 JSON 文件写入必须遵循）：**

```
写入流程：
  1. 序列化 JSON
  2. 写入 {filename}.tmp 临时文件
  3. os.Rename({filename}.tmp, {filename})

保证：任何时候进程崩溃，要么旧文件完好，要么已成功替换为新文件。不存在半截文件。
```

**适用方法：**
- `saveHistory` — 写入 history.json
- `SaveChatRecord` — 写入 chats/{chat_id}.json

**并发说明：**
- 同一 Session 同一时间只有一个活跃对话（RuntimeState 保证），因此不存在同一 Session 的并发写入
- 不同 Session 之间的写入天然隔离（不同目录），无竞争

## 附件处理流程

```
用户上传附件 → attachment 模块校验（大小、类型、数量）
            → 校验通过后，memory 模块保存到 memory/{sessionID}/attachments/{filename}
            → Agent 执行时从 memory 目录读取附件
            → 执行完成后，持久化 ChatRecord 和 Message
```

**说明：**
- 附件直接保存到会话目录，不经过临时目录
- 同名附件会覆盖，保证文件名一致性
- 对话失败时附件已存在，下次可重新上传覆盖，最终随会话清理删除

## 启动与停止流程

**Groot 启动时：**

```
启动流程：
  1. 解析 memory.directory 配置
     - 清理路径（去掉 ./ 前缀）
     - 绝对路径：直接使用
     - 相对路径：拼接 homeDir
  2. 确保 memory 目录存在，不存在则创建
  3. 初始化 Memory Manager
  4. 向 gocron 注册清理 Job（由 Scheduler 模块统一管理）
```

**Groot 停止时：**

```
停止流程：
  1. gocron 调度器停止（由 Scheduler 模块统一处理）
  2. 等待当前清理任务完成（如有）
  3. 释放资源
```

## 目录路径解析

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

## 错误处理

| 场景 | 处理 |
|------|------|
| 会话不存在 | 返回错误，调用方需先 CreateSession |
| history.json 不存在 | 返回错误，会话目录可能损坏 |
| history.json 解析失败 | 记录 ERROR 日志，返回错误 |
| 附件写入失败 | 记录 ERROR 日志，返回错误 |
| 清理时目录读取失败 | 记录 ERROR 日志，跳过该目录继续清理 |
| 清理时某会话 history.json 损坏 | 记录 ERROR 日志，跳过该会话继续清理 |
| 原子写入时 rename 失败 | 返回错误，.tmp 文件残留（下次写入覆盖） |

## 清理日志

清理操作使用系统统一日志记录，级别 INFO：

```log
2026-04-18 02:00:01 [INFO] [memory] 开始清理，保留天数: 7，当前会话数: 15
2026-04-18 02:00:02 [INFO] [memory] 清理会话 20260410100000000_x2y5，最后活跃: 2026-04-10，轮数: 5
2026-04-18 02:00:03 [INFO] [memory] 清理会话 20260409093000000_z8k3，最后活跃: 2026-04-09，轮数: 3
2026-04-18 02:00:05 [INFO] [memory] 清理完成，删除 2 个会话，剩余 13 个
```

## 配置参考

```yaml
memory:
  directory: memory               # 记忆目录（相对路径或绝对路径）
  retention_days: 7               # 会话保留天数
  cleanup_schedule: "02:00"       # 清理时间（HH:MM）
  history_window: 20              # LLM 上下文窗口（轮次数），-1 表示不限制
```

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `directory` | string | `memory` | 存储目录，支持相对/绝对路径 |
| `retention_days` | int | `7` | 会话保留天数，超过则清理 |
| `cleanup_schedule` | string | `02:00` | 每日清理时间（HH:MM 格式，由 gocron 解析） |
| `history_window` | int | `20` | LLM 上下文最大轮次数，`-1` 或 `0` 不限制 |

## 实现文件

| 文件 | 说明 |
|------|------|
| `internal/memory/types.go` | 数据结构定义（SessionInfo, Message, History, ChatRecord） |
| `internal/memory/memory.go` | Memory 接口定义 |
| `internal/memory/manager.go` | Manager 实现类，核心业务逻辑 |
| `internal/memory/idgen.go` | ID 生成器（session_id, chat_id, step_id） |
| `internal/memory/cleanup.go` | 清理逻辑（实现 gocron Task 接口，调度由 Scheduler 模块管理） |
| `internal/config/config.go` | MemoryConfig 配置项 |

## 设计决策记录

| 决策 | 结论 | 原因 |
|------|------|------|
| 存储引擎 | 文件系统（JSON） | 单机部署，无需数据库依赖 |
| history.json 是否截断存储 | 不截断，全量保存 | API 需要返回完整历史 |
| LLM 上下文如何控制 | 读取时按轮次截断 | 简单有效，无需 token 计数 |
| 是否做摘要压缩 | 当前不做 | 先上纯截断看效果，后续按需迭代 |
| JSON 写入方式 | 原子写入（tmp + rename） | 防止进程崩溃导致数据损坏 |
| 清理调度 | gocron 统一调度 | 和定时任务共享调度引擎，去掉重复的 CleanupScheduler |
| 清理时间判断 | 目录 ModTime | 反映最后活跃时间，避免误删持续活跃的旧会话 |
| 是否做文件锁 | 不做 | 同一 Session 无并发（RuntimeState 保证） |
| 多租户隔离 | 不做 | 一个实例服务一个企业应用 |

## 实现约束

以下约束在设计文档中不易体现，但编码时必须遵守，防止代码腐化。

**类型归属**

- `AttachmentPath` 只在 `memory/types.go` 中定义，`agent` 包直接引用 `memory.AttachmentPath`，**不得在 agent 包中重复定义同名结构体**
- `ChatRecord`、`Message`、`Step`、`Error` 等核心数据结构同理，统一归属 `memory` 包，其他包通过 `import` 引用

**ID 生成**

- 所有 ID 生成函数统一收敛到 `memory/idgen.go`：`GenerateSessionID`、`GenerateChatID`、`GenerateStepID`
- `agent/runtime_state.go` 中的 `GenerateTaskID`、`GenerateStepID` 应删除，改用 memory 包函数
- 随机源统一使用 `crypto/rand`

**活跃状态管理**

- 活跃对话状态（是否 running）**唯一数据源**是 `RuntimeState.activeChats`
- `Executor.runningTasks` 应删除，状态查询统一走 `RuntimeState.IsRunning()` / `RunningCount()`

**状态映射规则**

Executor 执行完成后，同时写入 `ChatRecord`（`chats/` 详情）和 `Message`（`history.json` 索引）。两者的 `status` 字段映射必须一致。**映射逻辑在 executor 中只应出现一次，Message 的 status 从 ChatRecord 拷贝，避免两处重复判断。**

| 执行结果条件 | status 值 |
|------------|----------|
| `ctx.Err() == context.Canceled` | `cancelled` |
| `err != nil`（执行异常） | `failed` |
| `result.Cancelled == true`（Agent 内部取消） | `cancelled` |
| `result != nil`（正常完成） | `completed` |
| 其他未知情况 | `failed` |
