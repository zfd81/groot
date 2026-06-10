# 移除附件持久化与内置文件工具改造计划

## 目标

- 附件不再持久化到存储（本地 / MinIO），上传流程保留（校验仍执行），内容直接透传给 LLM
- 删除 `groot_file_list` / `groot_file_read` 两个内置工具
- `sessionMd` 能力保留，规则内容置空

---

## 代码改动

### 移除附件持久化

| 文件 | 改动 |
|------|------|
| `internal/memory/memory.go` | 删除接口方法 `SaveAttachment`、`GetAttachmentPath` |
| `internal/memory/manager.go` | 删除 `SaveAttachment`、`GetAttachmentPath`、`AttachmentsDir` 实现 |
| `internal/api/handler/chat.go` | 删除 `SaveAttachment` 调用（约行 225），附件校验流程保留，内容继续传给 engine |

### 移除两个内置文件工具

| 文件 | 改动 |
|------|------|
| `internal/agent/file_tools.go` | 整个文件删除 |
| `internal/agent/executor.go` | 删除注入 `groot_file_list` / `groot_file_read` 的代码（约行 141-158）；`call_agent` 透传中也去掉这两个工具 |

### 清理附件路径记录字段

| 文件 | 改动 |
|------|------|
| `internal/memory/types.go` | 删除 `Message.Attachments []string`、`Message.ResultAttachments []string`（及 `ChatRecord` 同名字段） |
| `internal/agent/executor.go` | 删除向 ChatRecord / Message 写附件文件名的代码（约行 353、387、410） |
| `internal/agent/runtime_state.go` | 删除 `ResultAttachments []string` |

### 清理死代码

| 文件 | 改动 |
|------|------|
| `internal/attachment/handler.go` | 删除 `Process` / `processSingle`，保留 `Validate` |

### sessionMd 规则置空

| 文件 | 改动 |
|------|------|
| `internal/memory/session_rules.md` | 清空内容 |

---

## 设计文档改动

| 文件 | 改动 |
|------|------|
| `docs/superpowers/specs/2026-06-05-attachment-and-session-rules-design.md` | 主文档，改动最大：移除内置工具能力描述、附件持久化描述，session rules 规则内容置空 |
| `docs/superpowers/specs/2026-05-11-memory-design.md` | 删除 1.9 节"附件访问内置工具"；删除 `AttachmentsDir`、`SaveAttachment`、`GetAttachmentPath` 相关描述 |
| `docs/superpowers/specs/2026-04-18-groot-agent-design.md` | 工具表格删除 `groot_file_list` / `groot_file_read` 两行及相关描述 |
| `docs/superpowers/specs/2026-05-24-multi-agent-design.md` | 多处引用这两个内置工具，全部清理；`extraTools` 顺序变为 `[call_agent]`（仅编排模式） |

---

## 不动的部分

- `internal/attachment/handler.go` 的 `Validate` 方法及 `AttachmentConfig` — 上传时校验仍需要
- `internal/config/config.go` / `defaults.go` / `template.go` 中的 `AttachmentConfig` — 同上
- `internal/api/handler/chat.go` 附件解码→传给 engine 的流程 — 保留
- `internal/memory/session_rules.go` 及 `manager.go` 的 `GetSessionMdContent` — 保留，只是规则内容为空
