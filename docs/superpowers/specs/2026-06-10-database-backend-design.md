# 数据库后端设计

**日期**：2026-06-10 / 2026-06-26（移除 memory 定时清理相关字段与接口）
**作者**：zfd81 + Claude

---

## 目录

- [一、功能设计](#一功能设计)
  - [1.1 概述](#11-概述)
  - [1.2 设计目标](#12-设计目标)
  - [1.3 数据分类](#13-数据分类)
  - [1.4 后端定位与替代关系](#14-后端定位与替代关系)
  - [1.5 env.yaml 配置格式](#15-envyaml-配置格式)
  - [1.6 业务 ID 格式规范](#16-业务-id-格式规范)
  - [1.7 抽象层路线](#17-抽象层路线)
  - [1.8 模块分工](#18-模块分工)
  - [1.9 数据模型](#19-数据模型)
    - [1.9.1 设计原则](#191-设计原则)
    - [1.9.2 表清单总览](#192-表清单总览)
    - [1.9.3 cluster_members](#193-cluster_members)
    - [1.9.4 schedule_tasks](#194-schedule_tasks)
    - [1.9.5 schedule_executions](#195-schedule_executions)
    - [1.9.6 memory_sessions](#196-memory_sessions)
    - [1.9.7 memory_chats](#197-memory_chats)
    - [1.9.8 shared_resources](#198-shared_resources)
    - [1.9.9 跨表事务边界](#199-跨表事务边界)
    - [1.9.10 数据规模与索引策略](#1910-数据规模与索引策略)
    - [1.9.11 与文件实现的语义对齐](#1911-与文件实现的语义对齐)
  - [1.10 Repository 接口](#110-repository-接口)
    - [1.10.1 MemberRepo](#1101-memberrepo)
    - [1.10.2 ScheduleRepo](#1102-schedulerepo)
    - [1.10.3 MemoryRepo](#1103-memoryrepo)
    - [1.10.4 ResourceRepo](#1104-resourcerepo)
  - [1.11 已知限制](#111-已知限制)
- [二、迭代说明](#二迭代说明)
  - [2.1 与上一版差异](#21-与上一版差异)

---

## 一、功能设计

### 1.1 概述

数据库后端为 Groot 引入第三种持久化形态：**关系数据库**（MySQL / PostgreSQL）。它替代 MinIO 模式承担"运行时数据 + 集群共享资源"的远端存储职责，让多主机多实例部署下所有节点实时共享同一份权威数据。

后端选型在 `~/.groot/env.yaml` 中通过 `database` 节启用；启用时所有运行时数据（cluster 成员、schedule 任务、memory 会话/对话）走数据库读写，集群共享资源（skills / subagents / mcp / GROOT.md / config.yaml）通过 `groot push/pull/diff` 命令在本地 HOME 与数据库之间显式同步——本地 HOME 仍是业务运行时读取入口。

数据库后端严格按"模型一致、方言可切换"原则设计：MySQL 与 PostgreSQL 共用同一份逻辑表结构、同一份索引、同一份 Go 接口，差异仅在方言适配层吸收。运行期可通过仅改 `env.yaml` 的 driver 配置在两种数据库间切换。

### 1.2 设计目标

1. **跨数据库一致**：MySQL 与 PostgreSQL 表结构、字段语义、索引、约束完全一致，方言差异由适配层封装
2. **业务对象抽象**：抽象层按业务领域定义 Repository 接口（MemberRepo / ScheduleRepo / MemoryRepo / ResourceRepo），充分利用数据库的事务、索引、并发原语
3. **跨节点强一致**：所有运行时状态实时读写数据库，不在节点本地缓存——任意节点对状态的读写都立即对其他节点可见
4. **生命周期清晰**：业务运行时数据进库；集群共享资源仍以本地 HOME 为运行时读取入口，靠 push/pull/diff 与库内权威副本同步
5. **MinIO 退役**：启用数据库后不再需要 MinIO；`Storage` 抽象层及 local / minio 实现整包退役
6. **单机零配置**：不配置 `database` 节时自动使用 SQLite，开箱即用，无需任何额外基础设施

### 1.3 数据分类

Groot 的所有持久化数据按用途和读写特性分为四类，每类有不同的存储策略和生命周期。

#### 总览

```
GROOT_HOME/
├── logs/             ① 运行日志 — 永远本地，不进数据库，不跨节点共享
├── env.yaml          ② 节点本地配置 — 数据库连接配置，每节点独立，不参与同步
│
├── config.yaml       ③ 集群共享配置 — 本地 HOME 是运行时读取入口
├── GROOT.md          ③   MySQL/PG 模式：HOME ⇄ shared_resources 表（groot push/pull/diff）
├── skills/           ③   SQLite 模式：文件就在本机，无需同步
├── subagents/        ③
└── mcp/              ③

# ④ 运行时数据 — 直接读写数据库，不落本地文件
#
#   SQLite 模式：  ~/.groot/groot.db
#   MySQL/PG 模式：远端数据库
#
#   cluster_members    — 集群成员注册 / 心跳 / Leader 选举
#   schedule_tasks     — 用户定时任务定义
#   schedule_executions — 任务执行历史
#   memory_sessions    — 会话元数据
#   memory_chats       — 每次对话的完整记录
```

| 类别 | 代表内容 | SQLite 模式 | MySQL/PG 模式 | 参与 push/pull/diff |
|---|---|---|---|---|
| ① 运行日志 | `logs/` | 本地文件 | 本地文件 | ❌ |
| ② 节点本地配置 | `env.yaml` | 本地文件 | 本地文件 | ❌ |
| ③ 集群共享配置 | `config.yaml` / `skills/` / `subagents/` / `mcp/` / `GROOT.md` | 本地文件（直接读） | 本地文件（运行时读）+ `shared_resources` 表（权威副本） | ❌ SQLite / ✅ MySQL/PG |
| ④ 运行时数据 | `cluster_members` / `schedule_tasks` / `schedule_executions` / `memory_sessions` / `memory_chats` | `~/.groot/groot.db` | 远端数据库 | ❌ |

#### 1.3.1 节点本地配置

**定义**：每个节点独有的基础设施凭据与本地参数，不跨节点共享。

**特点**：
- 包含数据库连接凭据，是整个持久化层的前置条件——它本身无法被持久化系统自身管理
- 每个节点单独维护，不参与任何形式的同步
- 变更频率极低（部署时配置，长期不变）

**内容**：`~/.groot/env.yaml`（数据库连接配置。不配置则使用 SQLite 本地模式）

#### 1.3.2 集群共享配置

**定义**：所有节点共享同一份，描述 Groot 实例的业务行为，与基础设施无关。

**特点**：
- 以**本地 HOME 文件**为运行时读取入口——业务代码运行时直接读本地文件，不走数据库
- 本地 HOME 与数据库之间通过 `groot push/pull/diff` 命令**显式同步**（不自动同步）
- 部分资源支持热加载（skills / GROOT.md），部分需重启（config.yaml / mcp 配置 / subagent 入口）
- **热加载仅对已执行 `groot pull` 的节点生效**——Node A `push` 新内容到数据库后，Node B 必须手动执行 `groot pull` 才能感知更新；push 到数据库不等于所有节点立即更新
- 变更频率低（运维操作）

**内容**：
| 路径 | 热加载 |
|---|---|
| `config.yaml` | ❌ 需重启 |
| `GROOT.md` | ✅ 立即生效 |
| `skills/<skill>/` | ✅ 立即生效 |
| `subagents/<name>/` | ❌ 需重启（agent.md / mcp 配置）；skills 子目录 ✅ |
| `mcp/<server>.json` | ❌ 需重启 |

**SQLite 模式**：配置文件就在本机，无需 `shared_resources` 表，`groot push/pull/diff` 不可用。

**MySQL/PG 模式**：数据库中的 `shared_resources` 表是权威副本，本地 HOME 是工作副本。

#### 1.3.3 运行时数据

**定义**：系统运行过程中产生的状态数据，多实例之间需要实时共享。

**特点**：
- **直接读写数据库**，不在本地缓存，任意节点写入立即对其他节点可见
- 变更频率高（心跳 3 秒/次、每次对话写入）
- 生命周期由业务逻辑控制（任务归档、会话显式删除等）

**内容**：

| 数据 | 表 | 说明 |
|---|---|---|
| 集群成员注册 / 心跳 | `cluster_members` | Leader 选举的状态载体 |
| 定时任务定义 | `schedule_tasks` | 用户创建的定时任务 |
| 定时任务执行历史 | `schedule_executions` | 每次执行的完整记录 |
| 会话元数据 | `memory_sessions` | 多轮对话的容器 |
| 对话记录 | `memory_chats` | 每次 `/chat` 的结构化记录 |

#### 1.3.4 运行日志

**定义**：服务运行日志，仅用于运维排查。

**特点**：
- 永远写本地文件（`~/.groot/logs/`），不进数据库
- 不跨节点共享，每节点各自维护

### 1.4 后端定位与替代关系

后端指"运行时数据 + 集群共享配置"的持久化引擎，通过 `env.yaml` 配置选择。

| 后端 | 运行时数据 | 共享配置 | 适用部署场景 |
|---|---|---|---|
| **SQLite** | `~/.groot/groot.db` | 本地文件系统（无需同步） | 单实例 / 单机多实例 |
| **MySQL** | 远端 MySQL | `shared_resources` 表 + `push/pull/diff` | 多主机集群 |
| **PostgreSQL** | 远端 PostgreSQL | `shared_resources` 表 + `push/pull/diff` | 多主机集群 |

**替代关系**：
- MySQL / PostgreSQL 后端替代原有的 MinIO 模式，覆盖其全部职责
- MinIO 模式退役
- SQLite 替代原有的"local 文件系统"模式作为单机后端，运行时数据从文件迁入 SQLite，集群共享配置不受影响
- 不支持混合部署（同一进程内一部分走 SQLite、一部分走 MySQL）

**选择逻辑**：`env.yaml` 不配置 `database` 节 → SQLite；配置 `database` 节 → MySQL 或 PostgreSQL（由 `driver` 字段区分）。

### 1.5 env.yaml 配置格式

`env.yaml` 位于 `~/.groot/env.yaml`，是节点本地配置文件，**不参与任何形式的同步**。

#### 1.5.1 SQLite 模式（默认，无需配置）

`env.yaml` 不存在，或存在但不含 `database` 节时，自动使用 SQLite，数据库文件为 `~/.groot/groot.db`。

```yaml
# env.yaml 不存在，或为空文件，或不包含 database 节
# 均等价于 SQLite 模式
```

#### 1.5.2 MySQL 模式

```yaml
database:
  driver: mysql
  dsn: "user:password@tcp(host:3306)/groot?charset=utf8mb4&parseTime=True&loc=UTC"
  # 可选参数（不配置则使用默认值）
  max_open_conns: 20      # 最大连接数，默认 20
  max_idle_conns: 5       # 最大空闲连接数，默认 5
  conn_max_lifetime: 30m  # 连接最大生存时间，默认 30m
```

DSN 中密码可引用环境变量：

```yaml
database:
  driver: mysql
  dsn: "user:${GROOT_DB_PASSWORD}@tcp(host:3306)/groot?charset=utf8mb4&parseTime=True&loc=UTC"
```

#### 1.5.3 PostgreSQL 模式

```yaml
database:
  driver: postgres
  dsn: "host=host port=5432 user=groot password=password dbname=groot sslmode=disable TimeZone=UTC"
  max_open_conns: 20
  max_idle_conns: 5
  conn_max_lifetime: 30m
```

同样支持环境变量引用：

```yaml
database:
  driver: postgres
  dsn: "host=host port=5432 user=groot password=${GROOT_DB_PASSWORD} dbname=groot sslmode=disable"
```

#### 1.5.4 配置字段说明

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `database.driver` | string | ✅ | `mysql` 或 `postgres` |
| `database.dsn` | string | ✅ | 数据库连接字符串，格式由 driver 决定。支持 `${ENV_VAR}` 环境变量替换 |
| `database.max_open_conns` | int | ❌ | 最大打开连接数，默认 20 |
| `database.max_idle_conns` | int | ❌ | 最大空闲连接数，默认 5 |
| `database.conn_max_lifetime` | duration | ❌ | 连接最大生存时间（如 `30m`、`1h`），默认 30m |

#### 1.5.5 启动检查

服务启动时对数据库连接执行 fail-fast 校验（参见 [`internal/db/db.go`](../../../internal/db/db.go)）：

1. `db.Ping()` 验证连通性
2. 执行 schema 迁移（参见 [`internal/db/migrate.go`](../../../internal/db/migrate.go)，幂等，已存在的表不重建；同时清理历史遗留索引如 `memory_chats.uk_session_round`）
3. 任一失败立即退出，输出明确错误信息

### 1.6 业务 ID 格式规范

各业务对象的 ID 由应用层生成，不依赖数据库自增。格式规范如下，实现代码位于对应模块。

| ID | 格式 | 示例 | 说明 |
|---|---|---|---|
| `reg_id` | `YYYYMMDDHHMMSSmmm`（17 位纯数字） | `20260610143022123` | `GenerateRegID()`，精确到毫秒，字典序 = 时间序，用于选举排序 |
| `session_id` | `{YYYYMMDDHHMMSSmmm}_{random4}` | `20260610143022123_a3f9` | `GenerateSessionID()`，时间戳 + 4 位随机字符（小写字母+数字），防同毫秒碰撞 |
| `chat_id` | `{YYYYMMDDHHMMSSmmm}` | `20260610143022123` | `GenerateChatID()`，同一 session 内毫秒级唯一 |
| `task_id` | 与 chat_id 同格式或 UUID | 待定 | `schedule` 模块生成，保证全局唯一 |
| `execution_id` | 与 chat_id 同格式或 UUID | 待定 | `schedule` 模块生成，保证全局唯一 |

**子 Agent chat ID**（`GenerateChildChatID`）：
```
格式：{parentChatID}_{HHMMSSmmm}_{random4}_{agentName}
示例：20260610143022123_143025456_a3f9_qa-agent
```
同一毫秒内并发生成时采用"随机起点 + 同毫秒自增"策略避免碰撞（详见 `internal/memory/idgen.go`）。

### 1.7 抽象层路线

数据库后端不复用现有 `Storage` 接口（`Read/Write/List/Stat/Rename` 这套"path + 字节流"语义不适合关系数据库），改为按业务领域定义 Repository 接口：

```
internal/repo/
├── errors.go                       # 通用 ErrNotFound / ErrConflict 哨兵错误
├── member.go       + memberdb/     # MemberRepo（SQLite / MySQL / PG 共用一套 SQL）
├── memory.go       + memorydb/     # MemoryRepo（含 ChatRecord / Step / Error 类型）
├── resource.go     + resourcelocal/ + resourcedb/   # ResourceRepo
├── schedule.go                     # 仅 TaskStatus 常量
└── repofactory/                    # 工厂：按 dialect 一次性构造 4 个 Repo

internal/schedule/
└── repo.go                         # ScheduleRepo 接口 + schedule.ErrNotFound / schedule.ErrConflict
```

代码位置：[`internal/repo/`](../../../internal/repo/)、[`internal/schedule/repo.go`](../../../internal/schedule/repo.go)。

`ScheduleRepo` 接口定义在 [`internal/schedule/repo.go`](../../../internal/schedule/repo.go) 而非 `internal/repo/schedule.go`，原因是其入参/出参 (`*schedule.Task` / `*schedule.ExecutionRecord`) 直接用 `internal/schedule/types.go` 定义的领域类型，放在 `schedule` 包内可避免 `repo → schedule → repo` 反向依赖。`internal/repo/schedule.go` 只承载 `TaskStatus` 三个常量供其他模块引用。

`MemoryRepo` 的 `ChatRecord` / `Step` / `Error` 三个数据类型定义在 [`internal/repo/memory.go`](../../../internal/repo/memory.go) 内（`internal/memory/types.go` 通过 `type ChatRecord = repo.ChatRecord` 提供别名），让 `memorydb` 实现层和 `memory` 业务层共用同一份结构体而无导入循环。

`ResourceRepo` 有两套实现：
- [`resourcelocal`](../../../internal/repo/resourcelocal/)：直接调用 `os.*` 透传本地文件系统。SQLite 模式下 `groot push/pull/diff` 命令统一返回 `ErrSyncDisabled`，因此 `resourcelocal` 无实际业务调用路径，其存在仅为满足工厂模式接口一致性，使 `internal/sync/` 无需做 nil 判断
- [`resourcedb`](../../../internal/repo/resourcedb/)：读写 `shared_resources` 表——MySQL/PG 模式下的远端权威副本

其余三个 Repo（[`memberdb`](../../../internal/repo/memberdb/) / [`scheduledb`](../../../internal/repo/scheduledb/) / [`memorydb`](../../../internal/repo/memorydb/)）只有一套 `db` 实现，三种 driver（sqlite / mysql / postgres）共用相同 SQL，差异由方言层（[`internal/db/dialect.go`](../../../internal/db/dialect.go)）吸收。

工厂 [`repofactory`](../../../internal/repo/repofactory/factory.go) 根据传入的 dialect 一次性构造四个 Repo：SQLite 时 `Resource` 走 `resourcelocal`，MySQL/PG 时走 `resourcedb`。

### 1.8 模块分工

| 模块 | 职责 | 入参 |
|---|---|---|
| `internal/repo/` | 定义 Repository 接口 + db / local 实现 + 工厂 | 配置（db 连接或 homeDir） |
| `internal/db/` | 数据库连接管理、方言适配（sqlite/mysql/postgres）、schema 迁移 | DSN、driver 类型 |
| `internal/cluster/` | 调用 MemberRepo 完成注册 / 心跳 / 选举 / 故障转移 | MemberRepo |
| `internal/schedule/` | 调用 ScheduleRepo 完成任务 CRUD / 状态流转 / 执行历史落库 | ScheduleRepo |
| `internal/memory/` | 调用 MemoryRepo 完成会话/对话读写 | MemoryRepo |
| `internal/sync/` | 调用 ResourceRepo 实现 push/pull/diff | ResourceRepo |
| `cmd/groot/main.go` | 启动期根据 `env.yaml` 选择 driver，构造 DB 连接 + Repository 单例并注入各业务模块 | env.yaml |

### 1.9 数据模型

数据模型是整个数据库后端的基石——所有方言、所有 Repository 实现、所有业务读写路径都建立在它之上。本节定义全部 6 张表的结构、约束、索引、语义。

> **DDL 方言说明**：以下所有 DDL 均为 **MySQL 示例**。PostgreSQL 方言差异（`BIGINT GENERATED ALWAYS AS IDENTITY` 替代 `AUTO_INCREMENT`、`TEXT` 替代 `LONGTEXT`、`BYTEA` 替代 `LONGBLOB`、`CREATE INDEX` 替代 `KEY`、大小写敏感无需 `COLLATE utf8mb4_bin`）由 `internal/db/` 方言适配层统一处理，schema 逻辑本身不分叉。

#### 1.9.1 设计原则

| 原则 | 说明 |
|---|---|
| **MySQL / PostgreSQL 表结构一致** | 同一张表、同样的列、同样的索引、同样的语义。数据类型、默认值、UPSERT 语法的差异由方言层吸收，schema 本身不分叉 |
| **主键策略：业务键优先，代理键次选** | `cluster_members` / `memory_sessions` / `memory_chats` / `shared_resources` 用业务键直接做主键（reg_id / session_id / chat_id / path）；`schedule_tasks` / `schedule_executions` 因数据量大、可能被关联，沿用 BIGINT 自增代理键 + 业务键唯一约束 |
| **时间戳统一 BIGINT 毫秒** | 不用 `DATETIME` / `TIMESTAMP` / `TIMESTAMPTZ`——它们在两个 DB 之间精度、时区、范围都不一致。统一用 BIGINT 毫秒戳（自 1970-01-01 UTC），由 Go 侧 `time.Now().UnixMilli()` 写入，读取时 `time.UnixMilli(...)` 转回。整套系统不依赖 DB 时区 |
| **字符集统一 utf8mb4 / UTF8** | MySQL 强制 `utf8mb4`，默认 collation `utf8mb4_0900_ai_ci`（5.7 用 `utf8mb4_unicode_ci`）；PG 默认 UTF8。所有 VARCHAR 长度按字符数算 |
| **大文本统一 LONGTEXT / TEXT** | 历史 JSON、chat 详情等不定长 UTF-8 文本：MySQL 用 `LONGTEXT`，PG 用 `TEXT`（PG 的 `TEXT` 无长度限制） |
| **二进制内容统一 LONGBLOB / BYTEA** | `shared_resources.content` 必须支持任意字节流（md / py / sh / jar / 可执行二进制）：MySQL 用 `LONGBLOB`，PG 用 `BYTEA` |
| **不用 DB 外键** | 跨表关系（如 `schedule_executions.task_id` → `schedule_tasks.task_id`）由应用层维护。理由：删除任务/会话时清理多表数据，应用层用事务更可控；FK 在迁移、分库分表场景下都很碍事 |
| **不用 DB 触发器、存储过程、视图** | 一切逻辑在应用层。DB 只做存储和检索 |
| **默认硬删除** | memory 显式删除会话、schedule archive 等都是真实的 DELETE/UPDATE。不引入 `deleted_at` 软删字段 |
| **乐观锁字段** | 有并发更新风险的表加乐观锁防冲突。`schedule_tasks` 加 `version BIGINT NOT NULL DEFAULT 0` 列，UPDATE 时 `WHERE id=? AND version=?` 并 `version=version+1`；`memory_sessions` 以 `round` 字段替代 version 充当乐观锁（round 天然单调递增，INSERT chat 时 `WHERE session_id=? AND round=?` 保证同 session 同轮号不重复）。其他表不启用乐观锁 |
| **索引保守设计** | 只为已知查询路径建索引，不预先为可能用到的字段加索引。后续按慢查询补 |
| **大小写敏感的字符串主键** | `shared_resources.path` 在 MySQL 必须显式 `COLLATE utf8mb4_bin`，避免 `Skills/x.md` 与 `skills/x.md` 被识别为同一行；PG 默认敏感无需特殊处理 |

#### 1.9.2 表清单总览

| 表名 | 主键 | 对应 Repo | 行数级别 | 写频率 |
|---|---|---|---|---|
| `cluster_members` | `reg_id` | MemberRepo | 个位数到几十 | 高（每 3 秒心跳） |
| `schedule_tasks` | `id`（auto） | ScheduleRepo | 百~千 | 低（用户操作） |
| `schedule_executions` | `id`（auto） | ScheduleRepo | 万~十万 | 中（每次任务执行） |
| `memory_sessions` | `session_id` | MemoryRepo | 千~万 | 中（每次新会话 / 每轮对话刷 round） |
| `memory_chats` | `chat_id` | MemoryRepo | 万~百万 | 高（每次对话） |
| `shared_resources` | `path` | ResourceRepo | 百~千 | 极低（push 时） |

#### 1.9.3 cluster_members

**职责**：实例注册凭证 + 心跳锚点 + Leader 选举的状态载体。

```sql
CREATE TABLE cluster_members (
    reg_id          VARCHAR(32)  NOT NULL PRIMARY KEY,  -- 17 位毫秒时间戳字符串
    role            VARCHAR(16)  NOT NULL,              -- 'leader' | 'follower'
    host            VARCHAR(64)  NOT NULL,
    port            INT          NOT NULL,
    pid             INT          NOT NULL,
    heartbeat_at    BIGINT       NOT NULL,              -- 心跳时间戳（ms）
    created_at      BIGINT       NOT NULL               -- 注册时间戳（ms）
);
```

**字段语义**：

- `reg_id`：注册编号，沿用现有 `GenerateRegID()` 生成的 17 位毫秒时间戳格式。**主键。**
- `role`：当前角色，`'leader'` 或 `'follower'`。由 `MemberRepo.UpdateRole` 显式更新，与心跳写入分开
- `host` / `port` / `pid`：实例的 HTTP 监听地址与进程号，仅用于运维查看，不参与选举
- `heartbeat_at`：替代现有方案的"文件 mtime"，由实例每 3 秒自更新一次。Leader 判活以 `heartbeat_at > now - 7s` 为准
- `created_at`：注册时刻

**写入路径（对照 cluster spec §1.6 / §1.8）**：

| 操作 | 触发时机 | SQL 形态 |
|---|---|---|
| INSERT / UPSERT | `Register()`：首次启动 / 自检发现自己丢失 | `INSERT INTO cluster_members ... ON DUPLICATE KEY UPDATE`（MySQL）/ `ON CONFLICT(reg_id) DO UPDATE`（PG/SQLite） |
| UPDATE heartbeat_at | 每 3 秒心跳 | `UPDATE cluster_members SET heartbeat_at=? WHERE reg_id=?` |
| UPDATE role | leader 升级 / follower 降级 | `UPDATE cluster_members SET role=? WHERE reg_id=?` |
| DELETE 单行 | `Leave()` 优雅退出 | `DELETE FROM cluster_members WHERE reg_id=?` |
| DELETE 批量 | leader 心跳清理超时成员 | `DELETE FROM cluster_members WHERE heartbeat_at < ?` |
| SELECT | `ListAll()` 列出所有成员 | `SELECT * FROM cluster_members` |

**索引策略**：

- 仅 PK（`reg_id`）一个索引
- 表行数 ≤ 几十，全表扫秒级，无需建二级索引
- `host+port` 不做 UNIQUE：同一物理机会跑多实例（不同 port），且实例重启后 `reg_id` 会变（新生成时间戳），用 host+port 反而会冲突

**与文件实现的对照**：

| 文件实现 | 数据库实现 |
|---|---|
| `cluster/members/{regID}` 文件存在 | `cluster_members.reg_id = ?` 行存在 |
| 文件 mtime | `heartbeat_at` |
| 文件内容 `role|host:port|pid` | `role` / `host` / `port` / `pid` 列 |
| 文件丢失（`ErrNotFound`） | `SELECT ... WHERE reg_id=?` 返回空 |

#### 1.9.4 schedule_tasks

**职责**：用户定时任务的权威定义。

```sql
CREATE TABLE schedule_tasks (
    id              BIGINT       PRIMARY KEY AUTO_INCREMENT,
    task_id         VARCHAR(64)  NOT NULL,
    name            VARCHAR(255) NOT NULL,
    schedule_expr   VARCHAR(64)  NOT NULL,              -- 调度表达式：cron / ISO8601 时间戳（一次性）/ Go duration（间隔）
    status          VARCHAR(16)  NOT NULL,              -- 'active' | 'disabled' | 'archive'
    payload         LONGTEXT     NOT NULL,              -- 任务定义 JSON 兜底字段
    next_run_at     BIGINT       NULL,                  -- 下次执行时间戳（ms），active 才有值
    last_run_at     BIGINT       NULL,                  -- 最近一次执行时间戳（ms）
    version         BIGINT       NOT NULL DEFAULT 0,    -- 乐观锁
    created_at      BIGINT       NOT NULL,
    updated_at      BIGINT       NOT NULL,

    UNIQUE KEY uk_task_id (task_id),
    KEY idx_status_next_run (status, next_run_at),
    KEY idx_updated_at (updated_at)
);
```

**字段语义**：

- `id`：自增代理键，仅用于内部高效定位，不对外暴露
- `task_id`：业务 ID（沿用现有任务 ID 生成方式），对外唯一标识。`UNIQUE KEY` 约束
- `name` / `schedule_expr` / `status`：调度路径强需要的字段，提取为列以便走索引
- `schedule_expr`：调度表达式，与代码中 `Task.Schedule` 字段语义一致，支持三种格式：cron 表达式（`0 9 * * *`）、ISO8601 时间戳（`2026-07-01T10:00:00Z`，一次性执行）、Go duration（`30m`，间隔执行）
- `payload`：任务定义的全量 JSON——指令文本、附件元数据、回调 URL 等扩展字段全部留在 payload 内。表结构不为业务字段做 schema 演进
- `next_run_at` / `last_run_at`：调度引擎扫描"待执行任务"的核心字段，提取为列以便走索引
- `version`：乐观锁。用户改 cron 与调度器更新 `next_run_at` 可能并发，UPDATE 失败重试

**索引策略**：

- `uk_task_id`：业务 ID 唯一约束
- `idx_status_next_run (status, next_run_at)`：调度器每秒扫 `status='active' AND next_run_at <= now` 的核心索引
- `idx_updated_at`：管理界面"最近修改的任务"列表

**对照现有文件实现**：

| 文件实现 | 数据库实现 |
|---|---|
| `schedules/{status}/{id}/task.json` 文件 | `schedule_tasks.task_id, status, payload` 行 |
| `MoveTask` 重命名目录 | `UPDATE schedule_tasks SET status=?, version=version+1` |
| `LoadTask` 遍历三个目录 | `SELECT ... WHERE task_id=?` |

#### 1.9.5 schedule_executions

**职责**：任务每次执行的记录（append-only）。

```sql
CREATE TABLE schedule_executions (
    id              BIGINT       PRIMARY KEY AUTO_INCREMENT,
    execution_id    VARCHAR(64)  NOT NULL,              -- 业务 ID，对应 ExecutionRecord.ExecutionID（新增字段）
    task_id         VARCHAR(64)  NOT NULL,              -- 关联 schedule_tasks.task_id（无 FK）
    started_at      BIGINT       NOT NULL,              -- 对应 ExecutionRecord.ExecTime（字段重命名为 StartedAt）
    finished_at     BIGINT       NULL,                  -- 对应 ExecutionRecord.FinishedAt（新增字段），未完成时为 NULL
    status          VARCHAR(16)  NOT NULL,              -- 'running' | 'completed' | 'failed' | 'cancelled'
    detail          LONGTEXT     NOT NULL,              -- 完整执行记录 JSON

    UNIQUE KEY uk_execution_id (execution_id),
    KEY idx_task_started (task_id, started_at DESC),
    KEY idx_started_at (started_at)
);
```

**字段语义**：

- `execution_id`：执行记录业务 ID，全局唯一。对应 `ExecutionRecord.ExecutionID`
- `task_id`：指向 `schedule_tasks.task_id`（业务 ID）而非 `schedule_tasks.id`——避免任务被 archive/删除后执行历史成孤立外键引用
- `started_at`：执行开始时间戳（ms），对应 `ExecutionRecord.StartedAt`
- `finished_at`：执行结束时间戳（ms），对应 `ExecutionRecord.FinishedAt`，执行中为 `nil`，完成时回填
- `detail`：执行的完整 JSON 记录，含步骤、token 计数、错误等所有信息
- `status` 与 chat 状态枚举一致：`'running' | 'completed' | 'failed' | 'cancelled'`
- 表是 append-only：`SaveExecution` 写入初始行（`status='running'`，`finished_at=NULL`），`CompleteExecution` 回填结果

**`ExecutionRecord` 的字段构成**：见 `internal/schedule/types.go`，关键字段为 `ExecutionID` / `TaskID` / `StartedAt time.Time` / `FinishedAt *time.Time` / `Status string` / `DurationMs int64` / `StepCount int` / `Error string` / `Notifications []NotificationResult`。

**索引策略**：

- `uk_execution_id`：业务 ID 唯一
- `idx_task_started (task_id, started_at DESC)`：列出某任务最近 N 次执行（核心查询）
- `idx_started_at`：全局"最近执行历史" + 老旧记录 TTL 清理

#### 1.9.6 memory_sessions

**职责**：会话元数据（轻量级，不存历史正文）。

```sql
CREATE TABLE memory_sessions (
    session_id      VARCHAR(64)  NOT NULL PRIMARY KEY,
    user_id         VARCHAR(64)  NOT NULL DEFAULT '',   -- 创建会话时由 /chat 接口传入，可为空
    prompt          LONGTEXT     NOT NULL,              -- session 级系统提示词 / 历史压缩摘要（预留）
    round           INT          NOT NULL DEFAULT 0,    -- 当前对话总轮数
    created_at      BIGINT       NOT NULL,
    updated_at      BIGINT       NOT NULL,              -- 每次新增 chat 时刷新

    KEY idx_user_id (user_id),
    KEY idx_updated_at (updated_at)
);
```

**字段语义**：

- `session_id`：业务 ID，主键
- `user_id`：会话所属用户标识，由 `/chat` 接口在创建会话时传入，可为空字符串。配套 `idx_user_id` 索引支持"列出某用户的所有 session"
- `prompt`：预留字段，将来存放：
  - session 级系统提示词（类似 GROOT.md，但作用域为单个 session）
  - 长会话的历史压缩摘要（当 round 很大时无法把全部历史塞 LLM）
  当前阶段不写入，留作未来扩展
- `round`：当前对话总轮数。每写一条新 chat 时事务内 +1
- `updated_at`：每次新增 chat 时刷新，反映会话最后活跃时间

**对话历史的获取**：

不在本表存。每次需要构造 LLM 上下文时，从 `memory_chats` 实时聚合：

```
LoadHistory(session_id):
    SELECT instruction, result, round
    FROM memory_chats
    WHERE session_id = ? AND status = 'completed' AND agent_name = ''
    ORDER BY round ASC
    → 应用层组装为 [{role: user, content: instruction}, {role: assistant, content: result}, ...]
```

**索引策略**：

- PK：`session_id`
- `idx_user_id`：支持按用户检索 session
- `idx_updated_at`：按最近活跃时间排序 / 范围查询

#### 1.9.7 memory_chats

**职责**：每次 `/chat` 调用的完整结构化记录。

```sql
CREATE TABLE memory_chats (
    chat_id            VARCHAR(64)  NOT NULL PRIMARY KEY,
    session_id         VARCHAR(64)  NOT NULL,
    round              INT          NOT NULL,             -- 第几轮（在该 session 内），子 Agent 记录不占轮次
    agent_name         VARCHAR(64)  NOT NULL DEFAULT '',  -- 主 Agent 为空字符串，子 Agent 填 agent name
    caller             VARCHAR(64)  NOT NULL DEFAULT '',  -- 调用来源标识
    prompt             LONGTEXT     NOT NULL,             -- /chat 调用时携带的系统提示词
    instruction        LONGTEXT     NOT NULL,             -- 用户输入指令（含附件元数据 JSON）
    result             LONGTEXT     NOT NULL,             -- 大模型最终回复文本
    steps              LONGTEXT     NOT NULL,             -- 执行步骤数组 JSON（ReAct 思考链 / 工具调用 / 子 Agent 执行记录）
    status             VARCHAR(16)  NOT NULL,             -- 'running' | 'completed' | 'failed' | 'cancelled'
    error              TEXT         NOT NULL,             -- 失败原因 JSON（{"code":"...","message":"..."}），成功时为空字符串
    model              VARCHAR(64)  NOT NULL DEFAULT '',  -- 使用的模型 ID
    prompt_tokens      INT          NOT NULL DEFAULT 0,
    completion_tokens  INT          NOT NULL DEFAULT 0,
    total_tokens       INT          NOT NULL DEFAULT 0,
    duration_ms        BIGINT       NOT NULL DEFAULT 0,   -- 执行耗时（毫秒），与 schedule 侧 int64 保持一致
    started_at         BIGINT       NOT NULL,
    finished_at        BIGINT       NULL,                 -- running 时为 NULL

    KEY idx_session_round (session_id, round),
    KEY idx_session_started (session_id, started_at DESC),
    KEY idx_started_at (started_at),
    KEY idx_status (status)
);
```

**字段语义**：

- `chat_id`：业务 ID，主键。主 Agent 17 位毫秒时间戳；子 Agent 形如 `{父chatID}_{HHMMSSmmm}_{random4}_{agentName}`
- `round`：本 chat 在所属 session 内的轮次（1-based）。**仅主 Agent 对话推进轮次**；子 Agent 记录沿用其父 chat 的 round（不递增 session.round）
- `agent_name`：主 Agent 记录填空字符串 `''`；子 Agent 记录填对应 agent name
- `caller`：调用来源标识，对应现有 `ChatRecord.Caller` 字段
- `prompt`：本次调用携带的系统提示词（API `/chat` 入参显式传入时使用）
- `instruction`：用户输入指令的完整内容，附件元数据以 JSON 形式嵌入
- `result`：大模型最终回复文本。失败时为空字符串
- `steps`：ReAct 模式下的步骤数组 JSON（thought / action / observation 序列、工具调用等）
- `status`：执行状态四态
- `error`：失败原因，存储完整 JSON `{"code":"...","message":"..."}`，与现有 `ChatRecord.Error` 结构保持一致。`status='completed'` 时为空字符串
- `model`：使用的模型 ID（如 `gpt-4o`、`claude-opus-4-7`），便于按模型统计
- `prompt_tokens` / `completion_tokens` / `total_tokens`：三个 token 计数全存。OpenAI 等 API 偶尔返回的 total 与 prompt+completion 不严格相等（推理 token），全存避免重算误差
- `duration_ms`：执行耗时
- `started_at` / `finished_at`：开始 / 结束时间戳。running 时 `finished_at = NULL`

**子 Agent 记录的处理方式**：

子 Agent 的对话执行记录**与主 Agent 同表（`memory_chats`）持久化**，通过 `agent_name` 区分：

- 主 Agent 记录：`agent_name = ''`，`round = session.round + 1`，写入时事务推进 `memory_sessions.round`
- 子 Agent 记录：`agent_name = '<sub agent name>'`，`round = 父 chat 的 round`（沿用），**不推进 `memory_sessions.round`**

这样：

- session 的 round 序列是「主 Agent 视角」的轮次，子 Agent 不消耗 round
- `LoadHistory` 过滤 `agent_name = ''` 后得到的全是主 Agent 轮次，上下文顺序清晰
- 子 Agent 仍以独立行存在，token / steps / model 等字段不丢失，可观测性完整
- 同一父 chat 下若并发触发多个子 Agent，它们行的 `round` 都等于父轮次，靠 `chat_id` 区分

**约束与索引**：

- PK：`chat_id`（主 Agent 与子 Agent 的 chatID 形态不冲突，详见上文字段语义）
- `idx_session_round (session_id, round)`：按主 Agent 轮次回放/检索父子记录
- `idx_session_started`：列出会话历史
- `idx_started_at`：全局按时间范围扫描（管理用途，如运维 SQL 手动清理历史记录）
- `idx_status`：查"卡住的 running" / "失败的"

> 历史曾设计过 `uk_session_round (session_id, round)` 唯一约束，但子 Agent 沿用父轮次会与主 Agent 同 round 冲突，所以**仅作非唯一索引**保留，主 Agent 同 round 唯一性靠下文事务里的乐观锁保证。

**写入新 chat 的事务约定**：

主 Agent 路径（`agent_name=''`）：

```
BEGIN
  cur_round  := SELECT round FROM memory_sessions WHERE session_id = :sid
  next_round := cur_round + 1
  INSERT INTO memory_chats (chat_id, session_id, round, agent_name, ...) VALUES (..., next_round, '', ...)
  rows := UPDATE memory_sessions
             SET round = next_round, updated_at = :now
           WHERE session_id = :sid AND round = cur_round   -- 乐观锁
  if rows == 0 → ROLLBACK → return ErrConflict   -- 另一事务已推进 round，本次写作废
COMMIT
```

子 Agent 路径（`agent_name != ''`）：

```
BEGIN
  exists := SELECT COUNT(*) FROM memory_sessions WHERE session_id = :sid
  if exists == 0 → ROLLBACK → return ErrNotFound
  INSERT INTO memory_chats (chat_id, session_id, round, agent_name, ...) VALUES (..., :parent_round, :agent_name, ...)
  UPDATE memory_sessions SET updated_at = :now WHERE session_id = :sid   -- 仅刷新 mtime，不动 round
COMMIT
```

`ErrConflict` 触发点（仅主 Agent 路径）：
- `UPDATE 0 行`：SELECT 和 UPDATE 之间另一事务已提交新 chat 推进了 round，`WHERE round = cur_round` 不再匹配。**必须 rollback**，否则 `memory_sessions.round` 不更新，导致后续写入永远拿到同一个 `next_round`

调用方收到 `ErrConflict` 后重新 `GetSession` 获取最新 round，再次尝试。

**LoadHistory 查询约定**：

```sql
SELECT chat_id, session_id, round, prompt, instruction, result, steps, status,
       error, agent_name, caller, model, prompt_tokens, completion_tokens,
       total_tokens, duration_ms, started_at, finished_at
FROM memory_chats
WHERE session_id = ? AND status = 'completed' AND agent_name = ''
ORDER BY round ASC
```

`agent_name = ''` 过滤确保只取主 Agent 的对话轮次，不混入任何子 Agent 记录。

#### 1.9.8 shared_resources

**职责**：集群共享资源的远端权威副本，是 `groot push/pull/diff` 的远端侧。

```sql
CREATE TABLE shared_resources (
    path            VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    content         LONGBLOB     NOT NULL,                -- 字节流，文本/二进制统一存储
    content_type    VARCHAR(64)  NOT NULL DEFAULT '',     -- MIME，仅用于展示，不参与 diff
    size            BIGINT       NOT NULL,                -- 字节数（diff 快速字段）
    content_hash    CHAR(40)     NOT NULL DEFAULT '',     -- SHA-1 hex，对原始字节流计算
    updated_at      BIGINT       NOT NULL                 -- 写入时刻
);

CREATE INDEX idx_updated_at ON shared_resources (updated_at);
```

PG 方言：`LONGBLOB` → `BYTEA`，其余字段无差异。MySQL 中 `path` 列必须显式 `COLLATE utf8mb4_bin` 以保留大小写敏感（默认 `utf8mb4_0900_ai_ci` 会把 `Skills/x.md` 与 `skills/x.md` 视为同一行）；PG 默认大小写敏感无需特殊处理。

**字段语义**：

- `path`：相对 `~/.groot/` 的路径，如 `skills/weather/SKILL.md`、`subagents/qa/skills/lint/run.sh`、`GROOT.md`、`config.yaml`。不带任何前缀。**大小写敏感** —— `Skills/X` 与 `skills/x` 是不同的路径
- `content`：原始字节流。文本（md / yaml / json）与二进制（py / sh / jar / 可执行）统一存储，不区分类型，应用层按 path 后缀决定如何解释
- `content_type`：MIME 类型，仅用于展示用途（将来 web UI 预览资源时需要）。**不参与 diff 比对**，不影响读写逻辑。push 时调用方未指定可留空
- `size`：字节数。冗余字段，避免 diff 时反复 `LENGTH(content)`
- `content_hash`：原始字节流的 SHA-1 hex（40 字符）。**diff 比对的核心字段**——免疫时钟漂移、内容相同时刻不变。git 同款方案
- `updated_at`：写入时刻毫秒戳。仅用于"新旧"参考显示，不参与判等

**diff 比对规则**（替代现 sync 模块的 size + mtime ± 1s 容差）：

| 条件 | 结果 |
|---|---|
| 仅本地存在 | Added（本地有 / 远端没有） |
| 仅远端存在 | Removed（远端有 / 本地没有） |
| 双侧 size 不同 | Modified |
| 双侧 size 相同，content_hash 不同 | Modified |
| 双侧 size 相同，content_hash 相同 | Same |

mtime 不再参与判等，仅作为"新旧"参考显示给用户。因此 **pull 操作不再需要锚定本地文件的 mtime**（原实现中 `os.Chtimes` 把本地 mtime 对齐远端 LastModified 的步骤可以删除）。本地侧 diff 时计算文件 SHA-1（`crypto/sha1`，10 MB 内 < 10ms），代替 mtime 比对。

**为什么 content 必须是二进制**：

- `skills/<skill>/` 目录下不仅有 SKILL.md，还可能有 py 脚本、sh 脚本、jar 文件、编译好的可执行二进制
- `subagents/<name>/skills/<skill>/` 同理
- 强制 LONGTEXT 会因非 UTF-8 字节序列报错。LONGBLOB / BYTEA 才能装任何文件

**索引策略**：

- PK：`path`
- `idx_updated_at`：管理用途（查最近 push 的资源）

#### 1.9.9 跨表事务边界

| 场景 | 涉及表 | 事务 |
|---|---|---|
| Leader 心跳更新 | `cluster_members` | 否（单行 UPDATE） |
| Leader 选举尝试 | `cluster_members` | 否（单 UPDATE / INSERT 靠 SQL 原子性） |
| Leader 清理超时成员 | `cluster_members` | 否（单 DELETE 多行） |
| 创建任务 | `schedule_tasks` | 否（单 INSERT） |
| 任务状态流转（active → archive） | `schedule_tasks` | 否（单 UPDATE，靠 version 乐观锁） |
| 任务执行完成 | `schedule_executions` + `schedule_tasks` | **是**（写 execution + 更新 task.last_run_at 必须原子） |
| 写一条新 chat | `memory_chats` + `memory_sessions` | **是**（INSERT chat + UPDATE session.round 必须原子，靠乐观锁） |
| 删除会话 | `memory_chats` + `memory_sessions` | **是**（先删 chats 后删 session，原子） |
| Push 一批资源 | `shared_resources` | 否（一行一行 UPSERT，sync 本身幂等） |

#### 1.9.10 数据规模与索引策略

| 表 | 预估行数 | 单行大小 | 总数据量 | 索引数 |
|---|---|---|---|---|
| `cluster_members` | 个位数到几十 | < 1 KB | 忽略 | 1（PK） |
| `schedule_tasks` | 千 | ~5 KB（含 payload） | < 50 MB | 3 |
| `schedule_executions` | 万~十万 | ~10 KB | < 2 GB | 3 |
| `memory_sessions` | 千~万 | < 1 KB（无 history 列后） | < 10 MB | 3 |
| `memory_chats` | 万~百万 | ~50 KB | **~50 GB** | 4 |
| `shared_resources` | 百~千 | ~10 KB（含 LONGBLOB） | < 50 MB | 2 |

`memory_chats` 是唯一的"重表"，但单库数十 GB 远在 MySQL/PG 单库胜任范围内。如需手工运维清理（例如按时间删除 N 天前的 chat），`idx_started_at` 索引支持高效的范围 DELETE。系统本身不内置定时清理任务。

不预先做分库分表。

#### 1.9.11 与文件实现的语义对齐

| 文件实现 | 数据库实现 | 是否完全等价 |
|---|---|---|
| `cluster/members/{regID}` 文件存在 | `cluster_members` 行存在 | ✅ |
| 文件 mtime（local fs 纳秒 / minio 秒级） | `heartbeat_at`（毫秒） | ✅ 精度统一为毫秒 |
| `schedules/{status}/{id}/task.json` | `schedule_tasks` 行 | ✅ |
| MoveTask 重命名目录 | `UPDATE schedule_tasks SET status=?` | ✅ 原子性提升（不再依赖 minio 两阶段补偿） |
| `memory/{sid}/history.json` | 不存——从 `memory_chats` 实时聚合 | ⚠️ 取消"历史摘要落盘"概念，未来由 `memory_sessions.prompt` 承接长会话压缩摘要 |
| `memory/{sid}/chats/{ts}.json` | `memory_chats` 行（结构化拆字段） | ✅（结构化粒度更细） |
| MinIO `LastModified` | `shared_resources.updated_at` | ✅ 精度提升为毫秒 |
| MinIO 对象 path | `shared_resources.path` | ✅ 大小写敏感性靠 `utf8mb4_bin` 保证 |

### 1.10 Repository 接口

本节定义四个业务领域的 Repository 接口、入参出参类型、语义契约、事务边界。所有接口定义在 `internal/repo/` 包，实现在对应子包。

接口设计原则：
- 方法签名直接表达业务意图，不暴露 SQL 细节
- 第一个参数统一为 `context.Context`，支持超时与取消
- 错误统一返回 `error`，"不存在"场景返回 `repo.ErrNotFound`（调用方用 `errors.Is` 判断）。`ScheduleRepo` 因接口位于 `internal/schedule/` 包，独立定义 `schedule.ErrNotFound` / `schedule.ErrConflict` 与 `repo.ErrNotFound` / `repo.ErrConflict` 语义对齐
- 不在接口层暴露事务对象——跨表事务由实现层内部管理，接口方法保证原子性

```go
// ErrNotFound 表示查询目标不存在，调用方用 errors.Is(err, repo.ErrNotFound) 判断。
var ErrNotFound = errors.New("repo: not found")
// ErrConflict 表示乐观锁冲突，调用方需重新加载后重试。
var ErrConflict = errors.New("repo: version conflict")
```

#### 1.10.1 MemberRepo

**位置**：`internal/repo/member.go`，实现在 `internal/repo/memberdb/`

```go
// Member 集群成员信息
type Member struct {
    RegID       string    // 注册编号（17 位毫秒时间戳）
    Role        string    // "leader" | "follower"
    Host        string
    Port        int
    Pid         int
    HeartbeatAt time.Time // 最近心跳时间
    CreatedAt   time.Time
}

// MemberRepo 集群成员仓储
type MemberRepo interface {
    // Register 注册新成员。reg_id 已存在时覆盖写（幂等）。
    Register(ctx context.Context, m *Member) error

    // Heartbeat 更新指定成员的心跳时间戳为当前时刻。
    // reg_id 不存在返回 ErrNotFound。
    Heartbeat(ctx context.Context, regID string) error

    // UpdateRole 更新指定成员的角色（leader ↔ follower）。
    // reg_id 不存在返回 ErrNotFound。
    UpdateRole(ctx context.Context, regID, role string) error

    // Get 查询单个成员。不存在返回 ErrNotFound。
    Get(ctx context.Context, regID string) (*Member, error)

    // ListAll 列出所有成员（不过滤超时）。
    ListAll(ctx context.Context) ([]*Member, error)

    // Remove 删除指定成员。不存在视为成功（幂等）。
    Remove(ctx context.Context, regID string) error

    // RemoveExpired 删除 heartbeat_at < expiredBefore 的所有成员。
    // 仅 leader 调用。返回被删除的成员数量。
    RemoveExpired(ctx context.Context, expiredBefore time.Time) (int, error)
}
```

**Leader 选举的实现思路**：

数据库后端下选举不再依赖 mtime 排序，改为以下原子流程（由 `internal/cluster/` 在 `MemberRepo` 接口上组合实现）：

1. `ListAll` 取全量成员，过滤存活（`heartbeat_at > now - 7s`）
2. 按 `reg_id` 升序排序，自己是最小 → 发起升级
3. `UpdateRole(regID, "leader")` + `RemoveExpired` 在同一心跳轮次内完成
4. 并发安全由"reg_id 时间戳单调递增 + 全量扫描"保证——极端并发下两个实例都认为自己是最小，都执行 UpdateRole，最终 DB 里两行都是 leader；下一轮心跳（最长 **3 秒**，即一个 heartbeatInterval）各自 ListAll 后重新判断，RegID 大的自动降级（followerHeartbeat 覆盖写 role=follower）。无 split-brain，因为全局唯一任务的真正保护是 `IsLeader()` 的实时读取，不是一次性的 CAS

该逻辑与现有 `internal/cluster/cluster.go` 的心跳流程完全一致，只是把"文件 mtime"换成了"`heartbeat_at` 列"。

#### 1.10.2 ScheduleRepo

**位置**：`internal/repo/schedule.go`，实现在 `internal/repo/scheduledb/`

沿用现有 `internal/schedule/` 包的 `Task` 和 `ExecutionRecord` 类型，不重新定义。

```go
// TaskStatus 任务状态
type TaskStatus = string

const (
    TaskStatusActive   TaskStatus = "active"
    TaskStatusDisabled TaskStatus = "disabled"
    TaskStatusArchive  TaskStatus = "archive"
)

// ScheduleRepo 调度任务仓储
type ScheduleRepo interface {
    // SaveTask 创建或更新任务（按 task_id UPSERT）。
    // 写入时自动更新 updated_at；首次写入时设置 created_at。
    SaveTask(ctx context.Context, task *schedule.Task) error

    // LoadTask 按业务 ID 查询任务，遍历三种 status。不存在返回 ErrNotFound。
    LoadTask(ctx context.Context, taskID string) (*schedule.Task, error)

    // ListByStatus 列出指定状态的所有任务，按 created_at 升序。
    ListByStatus(ctx context.Context, status TaskStatus) ([]*schedule.Task, error)

    // DueTasks 查询 status='active' 且 next_run_at <= now 的待执行任务。
    // 调度引擎每秒调用，走 idx_status_next_run 索引。
    DueTasks(ctx context.Context, now time.Time) ([]*schedule.Task, error)

    // UpdateNextRun 更新 next_run_at 和 last_run_at。
    // 使用乐观锁（version）：version 不匹配返回 ErrConflict，调用方重新 LoadTask 后重试。
    UpdateNextRun(ctx context.Context, taskID string, nextRunAt, lastRunAt time.Time, version int64) error

    // MoveStatus 变更任务状态（active → disabled → archive 等）。
    // 使用乐观锁，version 不匹配返回 ErrConflict。
    MoveStatus(ctx context.Context, taskID string, newStatus TaskStatus, version int64) error

    // DeleteTask 物理删除任务（archive 后彻底清除时使用）。不存在视为成功。
    DeleteTask(ctx context.Context, taskID string) error

    // SaveExecution 保存一条执行记录（INSERT，幂等：execution_id 重复时忽略）。
    // 用于执行开始时写入初始记录（status='running'）。
    SaveExecution(ctx context.Context, rec *schedule.ExecutionRecord) error

    // CompleteExecution 原子完成：回填执行结果 + 更新任务 last_run_at / next_run_at。
    // 在事务内原子提交：UPDATE schedule_executions + UPDATE schedule_tasks。
    // version 不匹配（schedule_tasks 被并发修改）返回 ErrConflict，调用方重新 LoadTask 后重试。
    CompleteExecution(ctx context.Context, rec *schedule.ExecutionRecord, nextRunAt, lastRunAt time.Time, version int64) error

    // ListExecutions 按 task_id 倒序列出最近 N 条执行记录。
    ListExecutions(ctx context.Context, taskID string, limit int) ([]*schedule.ExecutionRecord, error)
}

// schedule 包内独立定义错误哨兵，与 repo.* 语义对齐：
var ErrNotFound = errors.New("schedule: not found")
var ErrConflict = errors.New("schedule: version conflict")
```

**事务边界**：

`CompleteExecution` 在实现层内部开启事务，原子完成执行结果回填与任务调度时间更新，接口保持干净。调用方（`internal/schedule/`）无需感知事务，也不需要类型断言。

#### 1.10.3 MemoryRepo

**位置**：`internal/repo/memory.go`，实现在 `internal/repo/memorydb/`

`History` 类型不再落盘，由 `LoadHistory` 从 `memory_chats` 实时聚合返回。

**`ChatRecord` 字段构成**（见 `internal/repo/memory.go`，`internal/memory/types.go` 通过 `type ChatRecord = repo.ChatRecord` 暴露给 memory 包）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `ChatID` / `SessionID` / `Round` | string / string / int | 主键、所属 session、本 chat 在 session 内的轮次 |
| `Prompt` | string | 本次调用携带的系统提示词，对应 `memory_chats.prompt` 列 |
| `Instruction` / `Result` | string / string | 用户指令与最终回复 |
| `Steps` | `[]Step` | ReAct 步骤数组，落库前序列化为 JSON |
| `Status` | string | `'running' \| 'completed' \| 'failed' \| 'cancelled'` |
| `Error` | `*Error` | 失败原因（含 `Code` / `Message`），成功时为 nil；落库前序列化为 JSON |
| `Caller` / `AgentName` | string / string | 调用来源 / 子 Agent 名（主 Agent 为空字符串） |
| `Duration` (deprecated) | int | 单位秒，从 `DurationMs` 整除 1000 得到，仅供旧 API 响应字段 `duration` 兼容 |
| `DurationMs` | int64 | 单位毫秒，DB 真实存储字段 |
| `PromptTokens` / `CompletionTokens` / `TotalTokens` | int / int / int | LLM token 计数 |
| `StartedAt` / `EndedAt` | time.Time / time.Time | 开始时刻；运行中 `EndedAt` 为零值，对应 DB `finished_at = NULL` |
| `Timestamp` | time.Time | 兼容字段，等于 `EndedAt`（部分 API 仍按"timestamp"读取） |

**与文件实现差异**（相对历史的 `chats/{chat_id}.json` 落盘方案）：

- 单 JSON detail 拆为结构化字段，便于 SQL 查询/索引
- 新增 `Prompt` / `AgentName` / `Caller` / `*Tokens` 列
- `Duration` 与 `DurationMs` 双字段并存：DB 层只存毫秒，读出时由实现层填充 `Duration` 兼容旧响应

```go
// Session 会话元数据（对应 memory_sessions 表）
type Session struct {
    SessionID string
    UserID    string    // 预留
    Prompt    string    // 预留
    Round     int
    CreatedAt time.Time
    UpdatedAt time.Time
}

// MemoryRepo 会话与对话记录仓储
type MemoryRepo interface {
    // CreateSession 新建会话。session_id 已存在时返回 ErrConflict（不覆盖）。
    CreateSession(ctx context.Context, s *Session) error

    // GetSession 查询会话元数据。不存在返回 ErrNotFound。
    GetSession(ctx context.Context, sessionID string) (*Session, error)

    // ExistsSession 判断会话是否存在。
    ExistsSession(ctx context.Context, sessionID string) (bool, error)

    // ListSessions 列出所有会话元数据，按 updated_at 倒序。
    // 返回完整 Session struct 供 API 层渲染列表（保留 session_id / round / created_at / updated_at 等字段）。
    ListSessions(ctx context.Context) ([]*Session, error)

    // SaveChat 写入一条对话记录。事务内原子完成：
    //   - 主 Agent 记录（rec.AgentName == ""）：INSERT memory_chats + 乐观锁 UPDATE memory_sessions
    //     （round=session.round+1、updated_at 刷新）；CAS 失败返回 ErrConflict
    //   - 子 Agent 记录（rec.AgentName != ""）：INSERT memory_chats（round 用调用方传入的父 round）
    //     + UPDATE memory_sessions 仅刷新 updated_at；不动 round
    // session 不存在返回 ErrNotFound。
    SaveChat(ctx context.Context, rec *memory.ChatRecord) error

    // GetChat 查询单条对话记录。不存在返回 ErrNotFound。
    GetChat(ctx context.Context, chatID string) (*memory.ChatRecord, error)

    // LoadHistory 从 memory_chats 实时聚合对话历史（替代原 history.json）。
    // 返回该 session 下 status='completed' AND agent_name='' 的所有主 Agent chat，按 round 升序排列。
    // agent_name='' 过滤确保子 Agent 记录不混入 LLM 上下文。
    // 调用方按需截取最近 N 轮（由 config.memory.history_window 控制）。
    LoadHistory(ctx context.Context, sessionID string) ([]*memory.ChatRecord, error)

    // DeleteSession 删除会话及其所有对话记录（事务内原子完成）。
    // 不存在视为成功（幂等）。
    DeleteSession(ctx context.Context, sessionID string) error
}
```

**`LoadHistory` 与 `history_window` 的协作**：

```
LoadHistory 返回全量 completed chat（按 round 升序）
→ 调用方（agent engine）按 config.memory.history_window 截取最近 N 轮
→ -1 表示不限制（全量）
→ 截取后的 []ChatRecord 组装为 LLM messages 输入
```

这与现有逻辑完全一致，只是数据源从"读 history.json 文件"改为"查 memory_chats 表"。

#### 1.10.4 ResourceRepo

**位置**：`internal/repo/resource.go`，实现在 `internal/repo/resourcedb/`（MySQL/PG）和 `internal/repo/resourcelocal/`（SQLite / 本地文件系统透传）

```go
// Resource 集群共享资源条目（对应 shared_resources 表一行）
type Resource struct {
    Path        string    // 相对 HOME 的路径，如 "skills/weather/SKILL.md"
    Content     []byte    // 原始字节流（文本/二进制统一）
    ContentType string    // MIME，仅用于展示
    Size        int64
    ContentHash string    // SHA-1 hex（40 字符）
    UpdatedAt   time.Time
}

// ResourceEntry diff 比对时的轻量元数据（不含 Content，避免大量数据传输）
type ResourceEntry struct {
    Path        string
    Size        int64
    ContentHash string
    UpdatedAt   time.Time
}

// ResourceRepo 集群共享资源仓储
type ResourceRepo interface {
    // Put 写入或更新一个资源（按 path UPSERT）。
    // 调用方负责在写入前计算 SHA-1 并填入 Resource.ContentHash。
    Put(ctx context.Context, r *Resource) error

    // Get 读取单个资源（含 Content）。不存在返回 ErrNotFound。
    Get(ctx context.Context, path string) (*Resource, error)

    // Stat 读取单个资源的元数据（不含 Content）。不存在返回 ErrNotFound。
    Stat(ctx context.Context, path string) (*ResourceEntry, error)

    // List 列出指定前缀下的所有资源元数据（不含 Content），按 path 升序。
    // prefix 为空时列出所有资源。
    List(ctx context.Context, prefix string) ([]*ResourceEntry, error)

    // Delete 删除单个资源。不存在视为成功（幂等）。
    Delete(ctx context.Context, path string) error
}
```

**两套实现的行为对照**：

| 方法 | `resourcedb`（MySQL/PG） | `resourcelocal`（SQLite 模式） |
|---|---|---|
| `Put` | UPSERT `shared_resources` 表 | `os.WriteFile`（原子写：tmp + rename） |
| `Get` | `SELECT * WHERE path=?` | `os.ReadFile` |
| `Stat` | `SELECT path,size,content_hash,updated_at WHERE path=?` | `os.Stat` + 计算 SHA-1 |
| `List` | `SELECT path,size,content_hash,updated_at WHERE path LIKE ?` | `filepath.WalkDir` |
| `Delete` | `DELETE WHERE path=?` | `os.Remove` |

`resourcelocal` 的 `Stat` 计算 SHA-1 会读取文件内容——仅在 diff 比对时调用，文件数量有限，可接受。

**sync 模块的适配**：

`internal/sync/` 模块将原来依赖 `storage.Storage`（MinIO）的远端操作，全部替换为 `ResourceRepo` 接口调用：

| 原 sync 操作 | 新 ResourceRepo 调用 |
|---|---|
| `store.Write(remotePath, reader)` | `repo.Put(ctx, &Resource{...})` |
| `store.Read(remotePath)` | `repo.Get(ctx, path)` → `.Content` |
| `store.Stat(remotePath)` | `repo.Stat(ctx, path)` |
| `listRemoteRecursive` | `repo.List(ctx, prefix)` |
| `store.Delete(remotePath)` | `repo.Delete(ctx, path)` |

diff 比对规则从"size + mtime ± 1s 容差"改为"size + SHA-1"，由 sync 模块的 `ComputeDiff` 函数负责，不在 ResourceRepo 层做。

### 1.11 已知限制

**自动化测试覆盖范围**

repo 层（`internal/repo/memorydb`、`memberdb`、`scheduledb`、`resourcedb`）的单元测试统一通过 [`internal/db/db.Open`](../../../internal/db/db.go) 构造的 **SQLite 内存库** 跑用例，目的是用一套通用的方言抽象覆盖三种后端的业务行为。

但以下方言差异 **没有** 在 CI 里跑真实 MySQL / PostgreSQL：

- DDL 兼容性：`LONGTEXT` / `LONGBLOB` / `BIGINT` / `VARCHAR(N)` 等类型在不同数据库上的实际行为
- 占位符重写：`?` ↔ `$1`（dialect.Rebind）在长 SQL 上的正确性
- UPSERT 语义：MySQL 的 `INSERT ... ON DUPLICATE KEY UPDATE` 与 PG 的 `ON CONFLICT (col) DO UPDATE` 是否在所有调用点行为一致
- 事务隔离级别：MySQL 默认 `REPEATABLE READ` 与 PG 默认 `READ COMMITTED` 下乐观锁 CAS 的可见性差异

**部署前建议**：

1. 用目标数据库（MySQL 8.x 或 PostgreSQL 14+）启动一个测试实例
2. 运行 `tests/python/` 下的系统测试（端到端跑 chat / schedule / sync 全套流程）
3. 至少覆盖：多主机并发写 chat、schedule_tasks 多节点抢占、shared_resources 大对象读写

如果在系统测试中发现方言差异，优先在 `internal/db/dialect.go` 的对应实现里修复，而不是在业务层 repo 中加分支。


---

## 二、迭代说明

### 2.1 与上一版差异

本文档为数据库后端首次独立 spec。在此之前，多主机多实例的远端持久化由以下文档定义：

- [`2026-06-01-storage-abstraction-and-minio-mode-design.md`](2026-06-01-storage-abstraction-and-minio-mode-design.md)：把 memory / schedule / cluster 接入 `Storage` 抽象，远端使用 MinIO
- [`2026-05-15-cluster-management-design.md`](2026-05-15-cluster-management-design.md)：基于文件 mtime 心跳的 Leader 选举
- [`2026-06-08-sync-design.md`](2026-06-08-sync-design.md)：基于 MinIO 的集群共享配置同步

相对上述基线的主要差异：

#### 抽象层

- **新增**：按业务领域定义的 `Repository` 接口（`MemberRepo` / `ScheduleRepo` / `MemoryRepo` / `ResourceRepo`），取代以"path + 字节流"为单位的通用 `Storage` 接口
- **退役**：`internal/storage/` 整包（`Storage` 接口、`Local` 实现、`Minio` 实现）全部移除。`resourcelocal` 直接调用 `os.*`，不再依赖 `Storage` 中间层

#### 持久化形态

- **新增**：MySQL / PostgreSQL 后端，统一逻辑表结构，方言差异封装在适配层
- **退役**：MinIO 模式
- **保留**：local 模式（单实例 / 单机多实例零回退）

#### 数据模型

- **新增**：6 张表（`cluster_members` / `schedule_tasks` / `schedule_executions` / `memory_sessions` / `memory_chats` / `shared_resources`）
- **结构变化**（相对原文件实现）：
  - `schedule_tasks.cron_expr` → `schedule_expr`：列名改为与代码 `Task.Schedule` 字段语义一致，支持 cron / ISO8601 / Go duration 三种格式
  - `memory_sessions` 不再持有 `history` 字段，对话历史从 `memory_chats` 实时聚合；新增 `prompt`（预留）/ `round` / `user_id`（预留）
  - `memory_chats` 由单 JSON detail 拆为结构化字段；新增 `agent_name`（主 Agent 为 `''`，子 Agent 为对应 agent name）/ `caller`；`error` 字段存完整 JSON `{"code":"...","message":"..."}`
  - `memory_chats` 不再保留 `uk_session_round` 唯一约束，改为非唯一索引 `idx_session_round`：子 Agent 沿用父 chat 的 round，会与主 Agent 同 round 共存；主 Agent 同 round 唯一性靠事务里的乐观锁保证
  - `ChatRecord` 新增 `Prompt`、`Model`、`PromptTokens` / `CompletionTokens` / `TotalTokens` 字段全链路填值；`Duration int`（秒）扩展为 `DurationMs int64`（毫秒），`Duration` 保留为 deprecated 向后兼容
  - `cluster_members` 用 `reg_id` 直接做主键，去掉代理键 `id`
  - `shared_resources.content` 改为 `LONGBLOB / BYTEA`，支持任意字节流
  - `shared_resources` 新增 `content_hash`（SHA-1 hex），diff 比对从 size+mtime 改为 size+hash
  - 时间戳全部改为 BIGINT 毫秒戳，跨 DB 时区/精度无歧义

#### Leader 选举

- **调整**：判活锚点从"文件 mtime"改为 `cluster_members.heartbeat_at`，毫秒精度统一
- **调整**：选举依赖 SQL 原子语义（UPDATE / INSERT + UNIQUE / 乐观锁），不再依赖文件系统的 List + 排序
- **保留**：心跳间隔 3 秒、超时 7 秒、`reg_id` 17 位毫秒时间戳格式

#### 集群共享资源同步

- **保留**：sync 模块的接口、命令（push/pull/diff）、白名单、skill 目录原子性、`-y` 跳过确认等行为
- **调整**：远端从 MinIO 对象切换为 `shared_resources` 表行；diff 比对从 size+mtime 容差改为 size+SHA-1
- **调整**：`ErrSyncDisabled` 错误信息从 `"sync: minio 模式未启用 — 请在 env.yaml 中配置 minio 节"` 改为 `"sync: 仅在 MySQL/PostgreSQL 模式下可用 — 请在 env.yaml 中配置 database 节"`
- **保留**：本地 HOME 是业务运行时读取入口的语义不变

#### 配置入口

- **新增**：`env.yaml` 中 `database` 节启用数据库模式
- **退役**：`env.yaml` 中 `minio` 节
- **保留**：local 模式无需任何额外配置

#### memory 定时清理

- **移除**：`MemoryRepo.DeleteExpiredSessions` 接口及其 `memorydb` 实现
- **说明**：会话不再做内置定时清理，长期保留在数据库中；运维侧如需按时间清理可直接对 `memory_chats` / `memory_sessions` 跑 SQL，配套 `idx_started_at` / `idx_updated_at` 索引



