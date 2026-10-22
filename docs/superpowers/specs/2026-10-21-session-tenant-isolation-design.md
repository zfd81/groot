# 会话租户与用户隔离设计文档

## 一、功能设计

### 1.1 功能概述

groot 作为被多个业务系统共享调用的 AI Agent 服务，需要保证一个业务系统中某个用户产生的会话，只能被同一个业务系统中的同一个用户读取和续写。本功能为每个会话赋予明确的归属（租户 + 用户），并在所有涉及会话的对外接口上按归属做隔离，解决"知道会话 ID 就能读取或追加任何人的对话"的安全问题。

### 1.2 信任模型

groot 的对外接口只面向持有 API Key 的业务系统后端，浏览器不直接访问 groot。因此：

- **租户由 API Key 决定。** 每个 API Key 在创建时绑定一个租户标识。业务系统拿着哪把 Key 调用，就属于哪个租户。调用方无法在请求中指定或覆盖租户。
- **用户由调用方声明。** 业务系统后端已完成自己的用户登录鉴权，它在每个请求的 `X-User-ID` 请求头中声明当前用户。groot 信任持有有效 API Key 的调用方对用户的声明，不自行鉴别用户身份。
- **groot 负责记录与隔离。** 新建会话时记录归属 (租户, 用户)；读取或续写会话时，校验请求归属与会话归属一致。

API Key 与租户是多对一关系：同一个租户可以同时持有多把有效的 Key。这使得 Key 的轮换（新建一把、迁移调用方、删除旧的）不影响会话归属，会话始终按租户标识而不是按 Key 记录。

### 1.3 能力清单

1. API Key 创建时必须指定租户标识，租户标识随 Key 元数据存储，可在管理界面查看。
2. API Key 鉴权中间件在校验通过后，把 Key 所属租户与请求头中的用户标识一起注入请求上下文，供后续处理器统一读取。
3. 新建会话时持久化归属 (租户, 用户)。
4. 所有接受会话 ID 的对外接口校验归属，归属不符的会话对调用方表现为"不存在"。
5. 会话列表与会话搜索接口只返回当前归属的会话。
6. 涉及会话的对外接口在缺少用户标识时拒绝请求，不产生无主会话。

### 1.4 数据模型

**api_keys 表** 新增字段：

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `tenant_id` | SQLite `TEXT` / MySQL、Postgres `VARCHAR(64)` | `NOT NULL DEFAULT ''` | 租户标识，非唯一 |

`tenant_id` 在表中的位置固定为 `name` 之后、`permissions` 之前。三种方言的 `CREATE TABLE` 语句按此列序书写，只改建表语句，不做补列或重建。

对应 `repo.APIKey` 结构增加 `TenantID string` 字段。租户不进入 JWT claims：JWT 仍只携带 jti、sub、scope、iat、exp，确定性签发的前提不变。

**memory_sessions 表** 新增字段：

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `tenant_id` | SQLite `TEXT` / MySQL、Postgres `VARCHAR(64)` | `NOT NULL DEFAULT ''` | 会话所属租户 |

`tenant_id` 在表中的位置固定为 `session_id` 之后、`user_id` 之前。三种方言的 `CREATE TABLE` 语句按此列序书写。

新增联合索引 `idx_ms_tenant_user ON memory_sessions(tenant_id, user_id)`。现有 `user_id` 字段与 `idx_ms_user_id` 索引保持不变。对应 `repo.Session` 结构增加 `TenantID string` 字段。

**表结构变更方式。** 系统处于开发阶段，尚无需要保留的线上数据，因此 `api_keys` 与 `memory_sessions` 两张表的新增字段都只修改三种方言的 `CREATE TABLE` 语句，不使用 `addColumnIfMissing`，不提供补列、重建或任何升级迁移逻辑。已有的开发环境数据库由使用者自行删除后重新启动生成。联合索引 `idx_ms_tenant_user` 随建表语句一起用 `CREATE INDEX IF NOT EXISTS`（MySQL 写在 `CREATE TABLE` 的 `KEY` 子句中）创建。

### 1.5 租户标识的约束

- 非空，去除首尾空白后长度 1 到 64。
- 字符集限定为字母、数字、下划线、短横线（正则 `^[A-Za-z0-9_-]+$`），避免与请求头或日志格式产生歧义。
- 不唯一，多把 Key 可共享同一租户。
- 创建后不可修改。需要变更时删除旧 Key 新建。

用户标识 `X-User-ID` 的约束：非空，去除首尾空白后长度 1 到 64，字符集同上。超限或含非法字符按缺失处理。

### 1.6 鉴权中间件行为

API Key 鉴权中间件（`internal/api/middleware/auth.go`）在现有流程（验签、按 jti 反查 api_keys 行、权限校验）之后增加：

1. 从反查到的 api_keys 行读取 `tenant_id`，写入请求上下文键 `tenant_id`。
2. 读取并校验 `X-User-ID` 请求头，合法时写入请求上下文键 `user_id`；不合法或缺失时写入空串。

中间件本身不因用户标识缺失而拒绝请求，由各处理器根据自身是否涉及会话决定。这样 `/audio/transcriptions`、`/schedule` 等与会话无关的接口不受影响。

Web Cookie 凭证路径（管理员登录）不注入 `tenant_id` 与 `user_id`，保持管理员视角。Web 管理界面与业务系统共用同一组 `/chat`、`/chat/*`、`/sess/*` 路由，处理器以现有上下文键 `caller == "web"` 识别管理员请求：命中时跳过用户标识校验与归属校验，列表与搜索不过滤，行为与现状完全一致。

提供两个辅助函数供处理器调用：

- `GetTenant(rc) string`
- `GetUserID(rc) string`

### 1.7 会话归属的写入

`POST /chat` 新建会话时，把上下文中的 (tenant_id, user_id) 写入 `memory_sessions`。`memory.Manager.CreateSession` 的签名调整为接受 `(sessionID, tenantID, userID string)`。

定时任务（`internal/schedule/runner.go`）创建的会话归属为 (空租户, 空用户)。这类会话不属于任何业务系统用户，对外接口按归属过滤后自然不可见，只能通过 Web 管理界面查看。

### 1.8 会话归属的校验

归属校验规则：请求上下文中的 (tenant, user_id) 与会话记录中的 (tenant_id, user_id) 完全相等才算匹配。

以下接口（统称"会话类接口"）在进入业务逻辑前依次做三步前置检查，任一步失败即返回，且都发生在任何会话查询、活跃状态查询之前：

1. 管理员请求（`caller == "web"`）直接放行，不做后两步。
2. API Key 的租户为空：403 `forbidden`，消息"API Key 未绑定租户"。
3. 用户标识缺失或非法：400 `invalid_request`，消息"缺少或非法的 X-User-ID"。

通过前置检查后，归属不符时各接口的行为：

| 接口 | 会话归属不符时 |
|------|---------------|
| `POST /chat`（携带 `X-Session-ID` 续聊） | 与"会话不存在"完全一致：生成新会话 ID 开启新会话 |
| `GET /chat/status/{sid}` | 与"会话不存在"完全一致：200 `idle`、`round_count=0` |
| `GET /chat/{sid}` | 404 `session_not_found` |
| `GET /chat/{sid}/{cid}` | 404 `session_not_found`；sid 归属匹配但 cid 不属于该 sid 时同样 404 |
| `GET /sess/{sid}` | 404 `session_not_found` |
| `GET /sess/history` | 只返回匹配归属的会话，`total` 同样按归属统计 |
| `GET /sess/search` | 只搜索匹配归属的会话 |

核心原则是"归属不符"对调用方的可观测行为与"会话不存在"完全相同，不向调用方泄漏某个会话 ID 是否真实存在。每个接口沿用自己现有的"不存在"语义：`/chat/status` 现状对不存在的会话返回 200 `idle`，因此归属不符也返回 200 `idle`，不改为 404；其余接口现状为 404。

两处顺序要求：

- `POST /chat` 的归属判定必须先于并发冲突检查（`IsRunning`）。归属不符的调用方传入他人正在运行的会话 ID 时，直接走新建会话路径，不返回 409，避免泄漏他人会话的活跃状态。
- `GET /chat/status/{sid}` 的归属判定必须先于活跃状态查询（`runtimeState.Get`），理由相同。

`POST /chat` 续聊时归属不符生成新会话 ID 的行为，与 README 中"有值但会话不存在则生成新 sid"的既有语义对齐，调用方通过响应中的 `X-Session-ID` 发现会话 ID 发生了变化。

### 1.9 memory 与 repo 层接口

为支持按归属查询，`repo.MemoryRepo` 与 `memory.Manager` 做如下调整：

- `CreateSession`：`repo.Session` 携带 `TenantID`、`UserID` 写入。
- `GetSession` 返回的 `repo.Session` 携带 `TenantID`、`UserID`，处理器据此做归属比对。
- 新增类型 `repo.SessionOwner{TenantID, UserID string}` 表示归属过滤条件。
- `ListSessions(ctx, owner *SessionOwner)`：owner 非 nil 时按 (tenant_id, user_id) 等值过滤；nil 表示不过滤，供 Web 管理界面与 `health.go` 的会话总数统计使用。
- `SearchChats(ctx, owner *SessionOwner, keyword string, limit int)`：同上。原"userID 为空字符串时不过滤"的语义移除，空字符串是合法的等值匹配值（匹配定时任务会话），只有 nil 表示不过滤。
- `memory.Manager` 新增 `OwnsSession(sessionID, tenantID, userID string) bool`：会话存在且归属匹配返回 true，供 `/chat` 续聊、`/chat/status`、`/chat/{sid}`、`/chat/{sid}/{cid}` 使用，替代原来裸的 `ExistsSession` 调用。`ExistsSession` 保留供定时任务等内部路径使用。
- `memory.Manager.ListSessions(owner *repo.SessionOwner, limit, offset int)`、`Search(owner *repo.SessionOwner, keyword string, limit int)` 相应增加参数。
- `memory.Manager.GetChatRecord(sessionID, chatID)` 在返回前比对记录的 `SessionID` 与传入的 sessionID，不一致视为未找到。

### 1.10 API Key 管理

`POST /web/apikeys` 请求体增加 `tenant_id` 字段，按 1.5 校验，不合法返回 400。`GET /web/apikeys` 列表响应增加 `tenant_id` 字段。

Web 管理界面 `ApiKeysPanel.vue` 的创建表单增加"租户"输入框（必填），列表增加"租户"列；`web/src/api/types.ts` 与中英文 i18n 文案同步。

### 1.11 日志

`/chat` 派生的会话级 logger 在现有 `session_id` 字段之外增加 `tenant_id`、`user_id` 字段，便于按租户排查。

### 1.12 历史数据

系统处于开发阶段，无需保留历史会话与历史 API Key。开发环境数据库删除后重新启动即按新结构建表，所有 API Key 重新签发并绑定租户。不提供任何兼容旧数据的逻辑。

### 1.13 非目标

- 不做用户级鉴权（groot 不验证 `X-User-ID` 的真实性）。
- 不引入独立的用户 token 签发接口。
- 不把租户写入 JWT，不改变"每次请求反查 api_keys 表"的吊销机制。
- 不改动 Web 管理界面对会话、日志的全量可见性。
- 不提供表结构升级迁移，不兼容旧结构的数据库文件。

### 1.14 留给实施者的决定

- 请求上下文键名、辅助函数的具体放置文件。
- `OwnsSession` 内部是一次查询还是复用 `GetSession`。
- 单元测试的组织方式，需覆盖：中间件注入、用户标识校验边界、每个接口的 403、400 与 404 分支、列表与搜索的归属过滤、API Key 创建的租户校验、三种方言建表语句的列序。

## 二、迭代说明

### 2.1 与上一版差异

上一版（当前 master）的状态：

- `memory_sessions.user_id` 列已存在，`/chat` 已从 `X-User-ID` 读取并写入，但读取侧（`/sess/{sid}`、`/sess/history`、`/chat/*`）完全不校验归属。
- `/sess/search` 在 `X-User-ID` 非空时按用户过滤，为空时返回全部。
- API Key 没有租户概念，不同业务系统的用户标识可能冲突。

本版改动：

- 新增：api_keys 表 `tenant_id` 字段（位于 `name` 与 `permissions` 之间）及创建校验，管理界面增加租户输入与展示。只改建表语句，不提供升级迁移。
- 新增：memory_sessions 表 `tenant_id` 字段（位于 `session_id` 与 `user_id` 之间）与 (tenant_id, user_id) 联合索引。只改建表语句，不提供升级迁移，已有开发库需删除重建。
- 新增：鉴权中间件注入 `tenant_id`、`user_id` 上下文，提供 `GetTenant`、`GetUserID`。
- 新增：`memory.Manager.OwnsSession`，`/chat` 续聊与 `/chat/status`、`/chat/{sid}`、`/chat/{sid}/{cid}` 改用它做存在性与归属校验。
- 调整：`/sess/{sid}` 增加归属校验，不符返回 404。
- 调整：`/sess/history`、`/sess/search` 按 (tenant_id, user_id) 过滤，"用户为空则不过滤"的语义改为由 nil 归属条件表达，仅管理员路径使用。
- 调整：所有会话类对外接口在 API Key 未绑定租户时返回 403，在 `X-User-ID` 缺失或非法时返回 400；Web 管理员（Cookie 凭证）不受影响。
- 调整：`POST /chat` 续聊的归属判定前移到并发冲突检查之前；`GET /chat/status/{sid}` 的归属判定前移到活跃状态查询之前。
- 调整：`GET /chat/{sid}/{cid}` 增加 cid 归属 sid 的校验。
- 调整：`CreateSession`、`ListSessions`、`SearchChats` 等接口签名增加租户/用户参数。
- 调整：README 中 `X-User-ID` 由"可选"改为会话类接口"必填"，并补充租户说明与 API Key 创建参数。
