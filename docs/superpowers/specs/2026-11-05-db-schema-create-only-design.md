# 数据库建表策略设计文档

## 一、功能设计

### 1.1 功能概述

Groot 启动时在目标数据库上建立运行所需的全部表结构。建表遵循"只新建"原则：首次启动在空库上一次性建出完整结构；库中已存在的表保持原样，程序不对其做任何结构改动。

系统定位为开发阶段，不承担历史库的结构升级。开发者本地或测试环境需要变更表结构时，手工删除历史库后重启，服务按当前建表语句重新建库。

### 1.2 能力清单

1. 启动时按方言（SQLite / MySQL / PostgreSQL）执行各自的建表语句，建出 12 张表：
   `cluster_members`、`schedule_tasks`、`schedule_executions`、`memory_sessions`、`memory_chats`、`shared_resources`、`users`、`models`、`settings`、`api_keys`、`cluster_messages`、`cluster_message_consumers`。
2. 表上的索引随建表语句一并建立：SQLite / PostgreSQL 用独立的 `CREATE INDEX IF NOT EXISTS`，MySQL 用 `CREATE TABLE` 内的 `KEY` / `UNIQUE KEY` 子句。
3. 建表幂等：表与索引已存在时跳过，重复启动不改动已有数据，也不报错。
4. 不含任何修改既有表结构的语句——没有 `ALTER TABLE ADD COLUMN`，没有 `DROP INDEX`，没有针对历史结构的探测。

### 1.3 设计细节

**入口。** `db.Migrate(db *sqlx.DB, dialect Dialect) error` 由 `db.Open` 在连接建立、`Ping` 通过后调用。迁移失败时关闭连接并向上返回错误，启动流程打印"无法初始化数据库"后退出。

**执行流程。** `Migrate` 只做一件事：按方言取出建表语句列表，顺序执行，任一条失败即返回错误。

```go
func Migrate(db *sqlx.DB, dialect Dialect) error {
    for _, stmt := range ddlStatements(dialect) {
        if _, err := db.Exec(stmt); err != nil {
            return fmt.Errorf("db migrate: %w", err)
        }
    }
    return nil
}
```

方言到语句列表的映射：`DialectMySQL → mysqlDDL()`，`DialectPostgres → postgresDDL()`，其余（含 SQLite）→ `sqliteDDL()`。

**建表语句的组织。** 三种方言各一个函数返回 `[]string`，语句与方言的数据类型一一对应：

| 用途 | SQLite | MySQL | PostgreSQL |
|------|--------|-------|------------|
| 字符串 | `TEXT` / `VARCHAR(n)` | `VARCHAR(n)` / `LONGTEXT` / `TEXT` | `VARCHAR(n)` / `TEXT` |
| 整数 | `INTEGER` | `INT` / `BIGINT` | `INTEGER` / `BIGINT` |
| 浮点 | `REAL` | `DOUBLE` | `DOUBLE PRECISION` |
| 二进制 | `BLOB` | `LONGBLOB` | `BYTEA` |
| 布尔 | `INTEGER` | `TINYINT(1)` | `BOOLEAN` |
| 自增主键 | `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGINT PRIMARY KEY AUTO_INCREMENT` | `BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY` |

索引位置：SQLite 与 PostgreSQL 在建表语句之后用 `CREATE [UNIQUE] INDEX IF NOT EXISTS` 追加；MySQL 不支持该语法，索引写在建表语句内部。

### 1.4 结构变更的处理方式

需要新增或调整列时，直接修改三种方言的建表语句，并同步调整使用该列的仓储代码。程序侧不提供补列、重建、版本号比对等任何自动升级路径。

开发与测试环境的结构同步方式是手工删除历史库：

- SQLite：删除 `{GROOT_HOME}/groot.db`、`groot.db-shm`、`groot.db-wal`。
- MySQL：`DROP DATABASE <库名>` 后重新 `CREATE DATABASE`。
- PostgreSQL：`DROP DATABASE <库名>` 后重新 `CREATE DATABASE`。

删除后重新启动服务，即按当前建表语句建出完整结构。

### 1.5 错误处理

建表语句执行失败时，返回带上下文前缀的错误（`db migrate: ...`），由 `db.Open` 再包一层 `db migrate:` 抛出，`cmd/groot/main.go` 记录"无法初始化数据库"并退出。错误信息保持原样透传数据库返回的文本，便于定位具体是哪条语句在哪张表上失败。

### 1.6 约束

- 建表语句只使用各方言通用的 `CREATE ... IF NOT EXISTS` 语法（MySQL 索引除外），不依赖方言扩展。
- 三种方言的列集合必须完全一致，仅数据类型不同；同一含义的列在三种方言与 Go 结构体中同名。
- 表已存在时语句被跳过，因此库中残留的旧结构与当前代码可能不一致，这类不一致由使用者按 1.4 手工清理，程序不检测、不修复。

## 二、迭代说明

### 2.1 与上一版差异

上一版除建表语句外，还带有两段面向历史库的结构修补逻辑：启动时探测并删除 `memory_chats` 上的历史唯一索引 `uk_session_round`；启动时探测并为 `shared_resources` 补 `status` 列。两段逻辑都依赖方言目录查询（`information_schema` / `pg_indexes` / `sqlite_master` / `pragma_table_info`）先判断再执行。

本版改动：

- 移除：`dropLegacyIndices`、`dropIndexIfExists` —— 不再自动清理历史 `uk_session_round` 索引。
- 移除：`addMissingColumns`、`addColumnIfMissing`、`statusColumnDef` —— 不再为历史库补 `shared_resources.status` 列。
- 移除：`indexExists`、`columnExists` 的生产实现；测试中需要校验建表结果时，以测试辅助函数的形式保留同名能力。
- 移除：依赖历史库修补行为的测试 `TestMigrate_AddsStatusColumnToLegacySharedResources`、`TestMigrate_StatusColumnIsIdempotent`、`TestMigrate_DropsLegacyUKSessionRound`、`TestMigrate_NoLegacyIndexIsNoop`。
- 调整：`Migrate` 语义收敛为"按方言执行建表语句"，不含任何结构探测与修补。
- 调整：`shared_resources.status` 列与 `memory_chats` 的非唯一索引 `idx_mc_session_round` 仅存在于建表语句中，由新建库直接建出。
