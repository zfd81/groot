# 附件访问与会话规则设计

**日期**:2026-06-05
**状态**:已归档（内置文件工具部分已移除）

---

## 一、功能设计

### 1.1 功能概述

本文档描述了会话规则常量（`defaultSessionRules`）以及 Manager 的 storage 改造。

> **注**：本设计稿中描述的 `groot_file_list` / `groot_file_read` 两个内置文件工具（§1.2）在后续迭代中已被移除（`internal/agent/file_tools.go` 已删除）。附件访问改由 attachment handler 直接通过 `storage.Storage` 接口处理；session rules 常量（§1.3）和 Manager 改造（§1.4）保持有效。

### 1.2 内置工具（已移除）

> **此章节描述的功能已移除。** `internal/agent/file_tools.go` 已删除，`groot_file_list` / `groot_file_read` 工具不再存在。构造函数 `NewGrootFileListTool` / `NewGrootFileReadTool` 也已随之删除。`Memory.SaveAttachment` / `Memory.GetAttachmentPath` 接口方法、`Manager.AttachmentsDir` 辅助方法均未实现。

### 1.3 会话规则常量

`defaultSessionRules` 通过 `//go:embed session_rules.md` 嵌入二进制,`Manager.GetSessionMdContent` 直接返回该常量。

> 规则正文以代码仓库 [internal/memory/session_rules.md](../../../internal/memory/session_rules.md) 为准。当前 session_rules 内容仍提及 `groot_file_list` / `groot_file_read`,需在后续迭代中同步移除。

文件布局:

```
internal/memory/
├── session_rules.go        # //go:embed session_rules.md
└── session_rules.md        # 规则正文
```

### 1.4 Manager 改造

- **构造函数注入 storage**:`NewManager(memoryDir, retentionDays, log, store)` 增加 `store storage.Storage` 参数,`store == nil` 时 panic("memory: NewManager: storage must not be nil")。所有持久化操作都走 `Storage` 接口
- **不预创建 memoryDir**:`Manager` 不再 `os.MkdirAll(memoryDir)`(local 模式会被 `storage.Write` 按需建立;minio 模式下 memoryDir 是 object key 前缀,不需要预创建)
- `CreateSession`:删除写 `SESSION.md` 物理文件的代码块,只保留 `saveHistory` 写一份空 history.json
- `GetSessionMdContent(sessionID string) (string, error)`:接口签名**保持不变**(为最小化改动并保持向后兼容);实现忽略 sessionID 参数,直接返回 `defaultSessionRules` 常量,err 永远为 nil。方法注释中说明:sessionID 仅作签名兼容保留,不参与逻辑
- `Cleanup` 改造:不再分项删 history.json + chats/ + attachments/,改为单次 `storage.DeleteDir(sessionDir)` 整目录递归删除,旧版残留 `SESSION.md` 物理文件随之自然回收

#### 旧 SESSION.md 物理文件处理

升级前已经存在的 session 目录里可能残留 `SESSION.md` 物理文件:

- 新版 `GetSessionMdContent` 不再读取该物理文件,直接返回常量,因此旧文件**不影响功能**
- 不主动迁移、不主动删除旧文件;`Cleanup` 走 `storage.DeleteDir(sessionDir)` 时会随会话过期一并清理

---
