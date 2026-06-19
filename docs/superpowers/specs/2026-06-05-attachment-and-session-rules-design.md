# 附件访问与会话规则设计

**日期**：2026-06-05（初版）/ 2026-06-10（数据库后端迁移后修订）/ 2026-06-18（去除附件存储后修订）

---

## 一、功能设计

### 1.1 功能概述

本文档描述两块独立但相关的能力：

- **附件处理**：API 层在 `/chat` 请求中接收 Base64 编码的附件，做请求级校验后转成多模态消息送入 LLM，不做任何持久化。
- **会话规则注入**：`memory.Manager` 通过常量向每轮系统指令注入会话规则文本。

### 1.2 附件处理

#### 1.2.1 数据流

```
HTTP 请求 (/chat)
   │  attachments: [{type, name, content(base64)}, ...]
   ▼
attachment.Handler.Validate          // 仅做请求级校验
   │
   ▼
chat handler 解码 base64 → agent.MultimodalContent
   │
   ▼
Task.MultiModalContents              // 仅在请求生命周期内存在
   │
   ▼
agent.Engine.buildUserMessage        // 拼装为 LLM 消息
   │   ├─ image / audio / video → Base64 data URL，走 UserInputMultiContent
   │   └─ file → 解码后文本拼接进文本部分
   ▼
LLM 调用
```

附件不写本地磁盘、不写对象存储、不入数据库；`memory_chats.instruction` 列只保存用户的文本指令，不包含附件内容。

#### 1.2.2 attachment.Handler

定义在 [internal/attachment/handler.go](../../../internal/attachment/handler.go)。

构造与方法：

```go
func NewHandler(cfg config.AttachmentConfig) *Handler
func (h *Handler) Validate(attachments []Attachment) error
```

`Attachment` 结构：

```go
type Attachment struct {
    Type    string // "file" | "image" | "audio" | "video"
    Name    string
    Content string // base64 编码后的内容
}
```

`Validate` 校验项：

- 附件总数 ≤ `cfg.MaxCount`
- 必填字段 `Name` 非空
- `Type` 必须是 `file` / `image` / `audio` / `video` 之一
- `Content` 非空
- 对 `file` / `image` 类型，文件扩展名必须在 `cfg.AllowedTypes` 白名单内（白名单为空时不校验）
- 对 `file` 类型，按 `len(content) * 3 / 4` 估算解码后大小，单个 ≤ `cfg.MaxSize` MB，累计总和 ≤ `cfg.MaxTotalSize` MB

校验失败返回 `*AttachmentError`，错误码常量见 handler.go 顶部（`ErrCodeCountExceeded` / `ErrCodeTypeNotAllowed` / `ErrCodeSizeExceeded` / `ErrCodeTotalSizeExceeded` / `ErrCodeMissingName` / `ErrCodeInvalidType` / `ErrCodeMissingContent` / `ErrCodeDecodeError`）。

#### 1.2.3 配置项

`config.AttachmentConfig`（[internal/config/config.go](../../../internal/config/config.go)）：

| 字段 | yaml | 说明 |
| --- | --- | --- |
| MaxSize | `max_size` | 单个附件大小上限（MB） |
| MaxTotalSize | `max_total_size` | 一次请求中所有附件总大小上限（MB） |
| MaxCount | `max_count` | 一次请求中附件数量上限 |
| AllowedTypes | `allowed_types` | 文件扩展名白名单（小写，不带点） |

#### 1.2.4 API 集成

[internal/api/handler/chat.go](../../../internal/api/handler/chat.go) 的处理顺序：

1. 收到请求体 `req.Attachments`，转换为 `attachment.Attachment` 切片
2. 调用 `attachmentHandler.Validate`，校验失败按错误码返回 400
3. 通过 base64 解码每个附件 content，构造 `agent.MultimodalContent`：
   - 所有类型都保留 `Base64Data`
   - `file` 类型额外把解码后的文本写入 `DecodedContent`
4. 写入 `Task.MultiModalContents`，由 Executor 透传给 Engine
5. Engine `buildUserMessage` 根据是否含图/音/视附件决定拼装 `UserInputMultiContent` 还是纯文本消息

#### 1.2.5 agent.MultimodalContent

[internal/agent/executor.go](../../../internal/agent/executor.go) 中定义：

```go
type MultimodalContent struct {
    Type           string // image / audio / video / file
    Name           string
    MIMEType       string
    Base64Data     string // 图/音/视 透传给 LLM 的 Base64
    DecodedContent string // file 类型解码后的文本
}
```

`Task.MultiModalContents` 字段在 `Task` 结构上保存这一切片，仅在 Agent 执行期间存在，执行结束后随 Task 一起被回收。

### 1.3 会话规则

#### 1.3.1 嵌入机制

[internal/memory/session_rules.go](../../../internal/memory/session_rules.go) 通过 `//go:embed session_rules.md` 把规则正文打入二进制：

```go
//go:embed session_rules.md
var defaultSessionRules string
```

[internal/memory/manager.go](../../../internal/memory/manager.go) 的 `GetSessionMdContent` 直接返回该常量，签名上的 `sessionID` 仅作兼容保留：

```go
func (m *Manager) GetSessionMdContent(sessionID string) (string, error) {
    _ = sessionID
    return defaultSessionRules, nil
}
```

#### 1.3.2 注入点

[internal/agent/executor.go](../../../internal/agent/executor.go) 的 `Execute` 在每次任务开始时调用 `GetSessionMdContent(sessionID)` 取出文本，作为系统指令的一部分一起送给 Agent。

#### 1.3.3 文件布局

```
internal/memory/
├── session_rules.go    // //go:embed session_rules.md
└── session_rules.md    // 规则正文（当前为空文件）
```

`session_rules.md` 当前为空文件，因此 `defaultSessionRules` 实际上是空字符串。后续若要补充会话规则正文，直接编辑该 markdown 文件即可，无需改动代码。

### 1.4 Memory Manager（与本文档相关的部分）

[internal/memory/manager.go](../../../internal/memory/manager.go) 已切换到数据库后端，与本文档相关的关键点：

- `NewManager(retentionDays int, log *logger.Logger, memRepo repo.MemoryRepo) *Manager` —— 不再持有任何文件目录或 `storage.Storage`
- 会话与对话直接通过 `repo.MemoryRepo` 读写 `memory_sessions` / `memory_chats` 表
- `GetMemoryDir()` 仅作为兼容存根返回空字符串
- `Cleanup(ctx)` 调用 `MemoryRepo.DeleteExpiredSessions`，事务内删除过期 session 及其 chat
- `GetSessionMdContent` 不再读盘，直接返回 `defaultSessionRules` 常量

完整的 Memory 模块设计见 [Memory 模块设计](2026-05-11-memory-design.md) 与 [数据库后端设计](2026-06-10-database-backend-design.md)。

---

## 二、迭代说明

### 2.1 相对 2026-06-05 初版的差异

- **移除内置文件工具**：`internal/agent/file_tools.go` 已删除，`groot_file_list` / `groot_file_read` 工具及其构造函数 `NewGrootFileListTool` / `NewGrootFileReadTool` 不再存在。
- **移除附件持久化**：`attachment` 模块退化为只做请求级校验。下列接口/路径全部不再产生：
  - `Memory.SaveAttachment` / `Memory.GetAttachmentPath`
  - `Manager.AttachmentsDir`
  - `memory/{sid}/attachments/` 目录
  - `~/.groot/memory/temp/` 临时目录
  - `shared_resources` 表
  - 写 MinIO bucket / 写 `storage.Storage`
- **附件流改造**：附件改由 chat handler 当场解码为 `agent.MultimodalContent`，挂在 `Task.MultiModalContents` 上送给 Engine 拼装 LLM 消息，请求结束即丢弃；`memory_chats.instruction` 列不再承载附件内容。
- **session_rules.md 内容清空**：`groot_file_list` / `groot_file_read` 移除后，原规则正文不再适用，`session_rules.md` 当前为空文件，等待后续补充。
- **Manager 改造已被数据库后端替代**：`NewManager(memoryDir, retentionDays, log, store)` 调整为 `NewManager(retentionDays, log, memRepo)`；`memoryDir` / history.json / chats/ 目录概念全部移除，统一走 `memory_sessions` / `memory_chats` 表。
