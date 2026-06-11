# 数据库后端系统测试报告

**日期**：2026-06-11
**测试范围**：SQLite、MySQL、PostgreSQL 三种后端 + 多实例集群
**测试结果**：✅ 全部通过

---

## 一、测试环境

| 项目 | 配置 |
|---|---|
| MySQL | localhost:3306, zfd/12345678, database=groot |
| PostgreSQL | localhost:5432, zfd/12345678, dbname=groot |
| SQLite | 内置 (`~/.groot/groot.db`, WAL 模式) |
| LLM | MiniMax-M3 (api.minimaxi.com/v1) |
| 测试机 | macOS Darwin 25.5.0 |

---

## 二、测试覆盖

### 2.1 Repository 层（DB CRUD 验证）

通过 [tests/sysdb/dbtest.go](tests/sysdb/dbtest.go) 对每种数据库执行：

| 模块 | 测试项 | SQLite | MySQL | PostgreSQL |
|---|---|---|---|---|
| **MemberRepo** | Register / Get / Heartbeat / UpdateRole / ListAll / Remove / RemoveExpired | ✅ | ✅ | ✅ |
| **ScheduleRepo** | SaveTask / LoadTask / ListByStatus / MoveStatus / SaveExecution / CompleteExecution / ListExecutions / DeleteTask | ✅ | ✅ | ✅ |
| **MemoryRepo** | CreateSession / ExistsSession / SaveChat (×2) / 轮次自增 / LoadHistory（按轮次顺序）/ DeleteSession（事务） | ✅ | ✅ | ✅ |
| **ResourceRepo** | Put / Get（UTF-8 中文）/ Stat / List（前缀过滤）/ 二进制内容保存 / Delete（幂等） | N/A* | ✅ | ✅ |

*SQLite 模式下 ResourceRepo 走本地文件系统（`resourcelocal`），单独测试通过。

### 2.2 端到端服务（启动 groot + LLM 多轮对话）

| 后端 | 启动 | health endpoint | 单轮对话 | 多轮对话（历史记忆）|
|---|---|---|---|---|
| **SQLite** | ✅ | ✅ | ✅ | ✅ 第二轮正确回忆"7777" |
| **MySQL** | ✅ | ✅ | ✅ | ✅ 第二轮正确回忆"Tokyo" |
| **PostgreSQL** | ✅ | ✅ | ✅ | ✅ 第二轮正确回忆"Coffee" |

### 2.3 多实例集群（MySQL 共享存储）

| 测试项 | 结果 |
|---|---|
| 两实例同时启动 | ✅ 两实例都通过 health check |
| Leader 选举 | ✅ 自动选出 1 个 leader + 1 个 follower（按 reg_id 字典序最小） |
| 故障转移（杀掉 leader） | ✅ 心跳超时 7 秒后，follower 自动晋升为 leader，并清理过期记录 |

### 2.4 单元测试套件

`go test ./...` — 全部通过，零 FAIL：
```
ok  internal/agent
ok  internal/api/handler
ok  internal/cluster (10.5s)
ok  internal/cmd
ok  internal/config
ok  internal/db
ok  internal/memory
ok  internal/repo/memberdb
ok  internal/repo/memorydb
ok  internal/repo/resourcedb
ok  internal/repo/resourcelocal
ok  internal/repo/scheduledb
ok  internal/schedule
ok  internal/sync
... (共 22 个包全部通过)
```

---

## 三、测试中发现并修复的 Bug

系统测试覆盖到了单元测试无法覆盖的"真实数据库"场景，发现并修复了 **4 个真实 bug**：

### Bug #1：MySQL TEXT/BLOB 列不允许 DEFAULT 值（高危）

**症状**：MySQL 启动 schema migration 时报错
```
Error 1101: BLOB, TEXT, GEOMETRY or JSON column 'prompt' can't have a default value
```

**根因**：MySQL 严格禁止 TEXT/LONGTEXT/BLOB 列定义 `DEFAULT ''`（与 SQLite/PostgreSQL 不同）。

**修复**：[internal/db/migrate.go](internal/db/migrate.go) — 移除 MySQL DDL 中 `LONGTEXT` 列上的 `DEFAULT ''`，改为应用层 INSERT 时显式提供空字符串。

### Bug #2：MySQL UPSERT 语法不兼容（高危）

**症状**：`Member.Register` 在 MySQL 报错
```
Error 1064: ... near 'CONFLICT(reg_id) DO UPDATE SET...
```

**根因**：原代码统一使用 SQLite/PostgreSQL 的 `ON CONFLICT(...) DO UPDATE` 语法，但 MySQL 用 `ON DUPLICATE KEY UPDATE ... = VALUES(...)`。

**修复**：
- [internal/db/dialect.go](internal/db/dialect.go) — 新增 `Dialect.UpsertSuffix()` 和 `InsertIgnorePrefix()` 方言适配方法
- 4 个 repo（memberdb / memorydb / scheduledb / resourcedb）的构造函数新增 `dialect db.Dialect` 参数
- 所有 SQL 语句通过 `db.Rebind()` 处理 PostgreSQL 占位符差异（`?` → `$1`）

### Bug #3：PostgreSQL 占位符差异（高危）

**症状**：原代码统一使用 `?` 占位符，PostgreSQL 需要 `$1, $2, ...`。

**修复**：所有 SQL 语句包裹 `r.db.Rebind(...)`，sqlx 自动按 driver 转换占位符。

### Bug #4：LoadHistory 状态过滤值错误（中等）

**症状**：第二轮对话时 LLM 回答"this is the first message"，多轮历史失效。

**根因**：
- 现有代码（runner.go、agent/executor.go）写入 chat 时使用 `status='completed'`
- 我新写的 `LoadHistory` 过滤条件用了 `status='success'` — 与现有约定不符

**修复**：[internal/repo/memorydb/memory.go:242](internal/repo/memorydb/memory.go#L242) — 改为 `status='completed'`，与项目既有命名一致。

---

## 四、跨数据库切换验证

通过修改 `~/.groot/env.yaml` 中的 `database.driver` 字段，在三种后端之间无缝切换：

```yaml
# SQLite（默认，零配置）
# env.yaml 不存在 database 节即可

# MySQL
database:
  driver: mysql
  dsn: "user:pass@tcp(host:3306)/groot?charset=utf8mb4&parseTime=True&loc=UTC"

# PostgreSQL
database:
  driver: postgres
  dsn: "host=host port=5432 user=u password=p dbname=groot sslmode=disable"
```

**逻辑表结构（schema）三种数据库完全一致**，方言差异（`AUTO_INCREMENT` vs `IDENTITY`、`LONGTEXT` vs `TEXT`、`LONGBLOB` vs `BYTEA`、`?` vs `$1`、`ON DUPLICATE KEY` vs `ON CONFLICT`、`utf8mb4_bin` collation）全部由 `internal/db/migrate.go` + `internal/db/dialect.go` 适配层自动处理。

---

## 五、性能与稳定性观察

- SQLite WAL 模式：`groot.db-shm`、`groot.db-wal` 文件正常生成，并发读写无锁等待
- MySQL/PostgreSQL：连接池配置（max_open=20, max_idle=5, lifetime=30m）默认值适用，无连接泄漏
- 心跳间隔 3 秒、心跳超时 7 秒：故障转移在约 10 秒内完成（包含心跳轮次延迟）
- LLM 响应延迟：MiniMax-M3 平均 2-5 秒，对集群心跳无影响

---

## 六、结论

**数据库后端系统测试全部通过。** 三种后端的 schema 一致性、CRUD 正确性、端到端服务可用性、多实例集群协调能力均符合设计文档 [2026-06-10-database-backend-design.md](docs/superpowers/specs/2026-06-10-database-backend-design.md) 的要求。

测试中暴露的 4 个 bug 均已修复并提交完整测试覆盖，证明设计 + 实现 + 测试三层均能在真实数据库环境下正确运行。
