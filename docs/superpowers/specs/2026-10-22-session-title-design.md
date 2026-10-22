# 会话标题设计文档

## 一、功能设计

### 1.1 功能概述

每个会话拥有一个标题，用于在会话列表、搜索结果和会话详情中标识该会话。标题在会话创建时由首轮对话的用户指令自动生成，之后可以通过接口重命名。标题是会话记录的一部分，存储在会话表中，读取接口直接返回，不再由对话记录现算。

### 1.2 能力清单

1. 会话创建时自动初始化标题：取首轮对话的用户指令，去首尾空白后按字符数截取前 100 个字符。
2. `PATCH /sess/{sid}` 重命名会话，请求体 `{"title": "..."}`。
3. `GET /sess/{sid}`、`GET /sess/history`、`GET /sess/search` 返回的标题均来自会话表的 `title` 列。
4. 重命名遵循会话租户与用户隔离规则，归属不符的会话对调用方表现为"不存在"；Web 管理员不受限制。

### 1.3 数据模型

`memory_sessions` 表新增字段：

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `title` | SQLite `TEXT` / MySQL、Postgres `VARCHAR(100)` | `NOT NULL DEFAULT ''` | 会话标题 |

`title` 在表中的位置固定为 `user_id` 之后、`prompt` 之前。三种方言只改 `CREATE TABLE` 语句，不补列、不重建；开发库删除后重启即按新结构建表。

`repo.Session.Title` 由 `CreateSession` 写入、`GetSession` 与 `ListSessions` 回填。`repo.SearchHit.Title` 来自 `memory_sessions.title`。`ListSessions` 与 `SearchChats` 的 SQL 删除原先从 `memory_chats` 首轮指令现算标题的子查询，直接选取 `s.title`。

### 1.4 标题规则

- 字符计数按 Unicode 字符（Go 的 rune），不按字节。
- 初始化：`strings.TrimSpace(instruction)` 后若超过 100 个字符，截取前 100 个字符；不加省略号；不做换行处理。空指令不可能到达此处（`/chat` 已校验 instruction 非空）。
- 重命名：请求体 `title` 去首尾空白后长度必须在 1 到 100 之间，否则 400 `invalid_request`，消息"标题不能为空，且长度不能超过 100 个字符"。不限制字符集。

### 1.5 接口

**创建时写入。** `memory.Manager.CreateSession(sessionID, tenantID, userID, title string)`。`/chat` 处理器在新建会话时传入 `truncateTitle(req.Instruction)`；定时任务创建会话时传入 `truncateTitle(task.TaskDef.Instruction)`（`internal/schedule/runner.go` 两处）；定时任务由对话创建，指令即创建时的用户要求，不为空。

**重命名。** `PATCH /sess/{sid}`，权限点 `session`。

处理流程：

1. 路径参数 `sid` 为空：400 `invalid_request`。
2. 请求体解析失败：400 `invalid_request`，消息"请求参数错误"。
3. 标题校验失败：400 `invalid_request`，消息见 1.4。
4. 前置检查：`requireOwner`（Web 管理员放行；租户为空 403；用户缺失 400）。
5. 归属判定：`ownsOrAdmin` 为 false → 404 `{"status":"session_not_found","message":"会话不存在"}`，与会话不存在时完全相同。
6. `memory.Manager.RenameSession(sid, title)` → repo `UpdateTitle(ctx, sessionID, title string) error`，只更新 `title` 列，不更新 `updated_at`（标题是元数据而非对话活动，重命名不改变列表排序与"最后活跃时间"）。影响行数为 0 时再查会话是否存在：存在（标题与原值相同，MySQL 默认不计未变更行）返回 nil，不存在（并发删除）返回 `ErrNotFound`，处理器按 404 处理；其他错误 500 `{"status":"error","message":"重命名失败"}`。
7. 成功 → 200 `{"status":"success","session_id":sid,"title":title}`。

**读取。** `GET /sess/{sid}` 响应的 `session` 对象增加 `title` 字段。`GET /sess/history` 的 `sessions[].title` 与 `GET /sess/search` 的 `results[].title` 语义不变，数据源改为 `title` 列。

### 1.6 权限点

`PATCH /sess/{sid}` 归入现有的 `session` 权限点，中间件现有的 `/sess/` 前缀规则已覆盖（DELETE 之外的方法都落入 `session`），无需新增映射。

### 1.7 路由

```
apiGroup.PATCH("/sess/:sid", sessionH.RenameSession)
```

### 1.8 测试要点

- 建表语句：三方言 `title` 列位于 `user_id` 与 `prompt` 之间。
- repo：`CreateSession` 写入并由 `GetSession`、`ListSessions` 回填 `Title`；`SearchChats` 的 `Title` 来自 `title` 列；`UpdateTitle` 命中返回 nil 且 `updated_at` 不变，未命中返回 `ErrNotFound`。
- 截取函数：空白修剪、恰好 100 字符不截、101 字符截到 100、多字节字符按字符计数。
- 重命名接口：400 三种（参数缺失、请求体非法、标题空/超长）、403/400 前置、404 归属不符、200 成功且 `GetSession` 读到新标题、Web 管理员放行。
- `/chat` 新建会话后 `GetSessionInfo` 的 `Title` 为截取后的指令。
- 列表与搜索返回的标题等于会话表值，与首轮对话内容无关（重命名后列表立即反映）。

### 1.9 非目标

- 不在 Web 管理界面增加重命名入口（界面只读取 `title` 展示，已有逻辑无需改动）。
- 不提供批量重命名。
- 不保留标题修改历史。

## 二、迭代说明

### 2.1 与上一版差异

上一版标题不是列，而是 `ListSessions` 与 `SearchChats` 查询时从 `memory_chats` 首轮主 Agent 对话的 instruction 现算的子查询，`GET /sess/{sid}` 不返回标题，无法重命名。

本版改动：

- 新增：`memory_sessions.title` 列（`user_id` 之后、`prompt` 之前），只改建表语句。
- 新增：`PATCH /sess/{sid}` 重命名接口，权限 `session`；`repo.MemoryRepo.UpdateTitle`、`memory.Manager.RenameSession`。
- 调整：`CreateSession` 签名增加 `title` 参数；`/chat` 与定时任务创建会话时写入截取后的指令。
- 调整：`ListSessions`、`SearchChats` 直接读 `title` 列，移除现算子查询。
- 调整：`GET /sess/{sid}` 的 `session` 对象增加 `title`。
