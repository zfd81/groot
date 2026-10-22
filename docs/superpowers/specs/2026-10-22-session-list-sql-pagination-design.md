# 会话列表 SQL 分页设计文档

## 一、功能设计

### 1.1 功能概述

会话列表接口 `GET /sess/history` 按归属（租户 + 用户）返回当前调用方的会话，按最近活跃时间倒序，支持 `limit`/`offset` 分页并返回总数 `total`。分页与计数全部由数据库完成：每次请求只从数据库取当前页的行，总数由一条独立的计数语句得到。进程内不持有、不遍历该归属下的全部会话，接口的开销只与页大小相关，与会话总量无关。

### 1.2 能力清单

1. 仓储层 `MemoryRepo.ListSessions` 接收 `limit`、`offset`，生成的 SQL 带 `ORDER BY updated_at DESC LIMIT ? OFFSET ?`，只返回当前页。
2. 仓储层新增 `MemoryRepo.CountSessions`，用 `SELECT COUNT(*)` 返回该归属下的会话总数。
3. `memory.Manager.ListSessions` 先取总数再取当前页，把两者组装为接口响应，自身不做任何切片与裁剪。
4. `offset` 越界时数据库返回空集，接口返回空列表且 `total` 正确。
5. `limit` 非正数时仓储层直接返回空结果，不下发 SQL（三方言对 `LIMIT 0`/负数行为不一致）。
6. SQLite、MySQL、PostgreSQL 三方言使用同一条 `LIMIT ? OFFSET ?` 语句，占位符经 `Rebind` 适配。

### 1.3 设计细节

**仓储接口**（`internal/repo/memory.go`）：

```go
// ListSessions 按 updated_at 倒序返回会话的一页。owner 为 nil 时不过滤；
// 非 nil 时只返回 tenant_id 与 user_id 同时匹配的会话。
// limit 非正数返回空切片；offset 负数按 0 处理。
ListSessions(ctx context.Context, owner *SessionOwner, limit, offset int) ([]*Session, error)
// CountSessions 返回与 ListSessions 相同过滤条件下的会话总数。
CountSessions(ctx context.Context, owner *SessionOwner) (int, error)
```

**SQL**（`internal/repo/memorydb/memory.go`）：

```sql
SELECT s.session_id, s.tenant_id, s.user_id, s.title, s.prompt, s.round, s.created_at, s.updated_at
  FROM memory_sessions s [WHERE s.tenant_id = ? AND s.user_id = ?]
 ORDER BY s.updated_at DESC
 LIMIT ? OFFSET ?;

SELECT COUNT(*) FROM memory_sessions s [WHERE s.tenant_id = ? AND s.user_id = ?];
```

两条语句共用 `ownerCond` 生成 WHERE 片段，保证过滤条件一致。归属过滤命中已有联合索引 `idx_ms_tenant_user`。

**业务层**（`internal/memory/manager.go`）：`ListSessions(owner, limit, offset)` 依次调用 `CountSessions` 与 `ListSessions`，把仓储返回的行映射为 `SessionInfo` 后原样返回，`total` 取自计数结果。

**接口层**（`internal/api/handler/session.go`）：参数解析与响应结构不变。`limit` 默认 20、上限 100；`offset` 默认 0。

## 二、迭代说明

### 2.1 与上一版差异

- 调整：`MemoryRepo.ListSessions` 签名增加 `limit, offset` 参数，SQL 追加 `LIMIT ? OFFSET ?`。
- 新增：`MemoryRepo.CountSessions`。
- 移除：`memory.Manager.ListSessions` 中"全量加载后用 Go 切片 `infos[offset:end]` 取页、`total = len(infos)`"的内存分页逻辑。
- 原因：原实现每次翻页都加载该归属下的全部会话到进程内存，数据量增长后成为性能瓶颈。项目原则：分页查找一律在 SQL 层实现。
