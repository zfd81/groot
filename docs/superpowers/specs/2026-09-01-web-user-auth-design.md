# Web 用户认证设计文档

## 一、功能设计

### 1.1 功能概述

Groot WebUI 采用基于数据库用户表的登录认证体系。系统在数据库中维护用户账户（用户名 + bcrypt 加密密码），Web 登录认证始终启用。首次使用时，系统引导访问者创建用户账户；此后所有访问均需通过用户名密码登录建立会话。用户可在 Web 页面中修改密码，也可通过 CLI 子命令重置用户数据。

该设计解决的问题：

- 登录凭证持久化于数据库，密码以 bcrypt 哈希存储，不以明文出现在任何配置文件中；
- 首次使用零配置：无需预先编辑配置文件即可完成账户初始化；
- 凭证管理有完整闭环：创建、登录、修改密码、重置。

### 1.2 能力清单

1. **用户存储**：数据库 `users` 表持久化用户账户，按多用户结构设计（用户名唯一），为未来多用户扩展预留。
2. **首次初始化**：用户表为空时，WebUI 自动进入"创建用户"页面，输入用户名、密码、密码确认完成创建，随后跳转登录页；表中已有用户时直接进入登录页。
3. **登录认证**：登录时以用户名查库并用 bcrypt 校验密码；登录成功建立服务端会话并记录最后登录时间。
4. **会话管理**：会话有效期 1 小时，滑动续期（任何携带有效会话的请求都会刷新有效期）；会话凭证通过 HttpOnly、SameSite=Strict 的 Cookie 下发，Secure 标志由程序按请求自动判断（TLS 连接或 `X-Forwarded-Proto: https`）。
5. **修改密码**：Web 页面提供修改密码入口，输入原始密码、新密码、新密码确认；修改成功后该用户的其他会话全部失效，当前会话保留。
6. **密码规则**：密码最少 8 位，前端与后端双重校验，创建用户与修改密码共用同一规则。
7. **用户重置**：`groot user reset` 子命令删除用户表全部数据（交互式确认，支持 `-y` 跳过），重置后 WebUI 重新进入首次初始化流程。
8. **登录保护**：同一来源连续登录失败会被临时锁定（服务端内存计数）。

### 1.3 设计细节

#### 1.3.1 数据模型

`users` 表（SQLite / MySQL / PostgreSQL 三方言，随 `db.Migrate` 自动建表）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | VARCHAR(32) PK | 系统编号，创建时按 `yyyyMMddHHmmss` 格式自动生成（14 位） |
| username | VARCHAR(64) UNIQUE | 用户名，唯一索引 `uk_users_username` |
| password | VARCHAR(100) | bcrypt 哈希 |
| created_at | BIGINT | 创建时间，UnixMilli |
| updated_at | BIGINT | 修改时间，UnixMilli |
| last_login_at | BIGINT NULL | 最后登录时间，UnixMilli，从未登录为 NULL |

#### 1.3.2 Repo 接口

`internal/repo/user.go` 定义 `UserRepo`（实现在 `internal/repo/userdb/`）：

```go
type UserRepo interface {
    Create(ctx context.Context, u *User) error
    GetByUsername(ctx context.Context, username string) (*User, error)
    GetByID(ctx context.Context, id string) (*User, error)
    Count(ctx context.Context) (int64, error)
    UpdatePassword(ctx context.Context, id, passwordHash string) error
    UpdateLastLogin(ctx context.Context, id string, at time.Time) error
    DeleteAll(ctx context.Context) (int64, error)
}
```

#### 1.3.3 HTTP 接口

Web 专用接口全部位于 `/web` 前缀下，仅供 WebUI 使用：

| 端点 | 认证 | 说明 |
|---|---|---|
| `GET /web/me` | 免登录 | 返回 `{authenticated, auth_required, needs_setup, username}`，`needs_setup` 为用户表是否为空 |
| `POST /web/setup` | 免登录 | 创建首个用户 `{username, password}`；表非空返回 409，参数不合规返回 400 |
| `POST /web/login` | 免登录 | 登录 `{username, password}`；成功下发会话 Cookie，失败 401，锁定 429 |
| `POST /web/logout` | 免登录（幂等） | 注销当前会话并清除 Cookie |
| `POST /web/password` | 需会话 | 修改密码 `{old_password, new_password}`；旧密码错误 401，新密码不合规 400 |
| `GET /web/health` | 免登录 | 健康检查（同时供 `groot status` 使用） |
| `GET /web/agents` `/web/skills` `/web/tools` `/web/models` | 需会话 | WebUI 元信息接口 |

会话保护由 `WebSession` 中间件实现：校验 Cookie 中的会话 token，通过后向请求上下文注入 `caller=web` 与 `web_user_id`。

对外 API（`/chat`、`/sess`、`/schedule` 等）继续走 API Key 认证；携带有效 Web 会话 Cookie 的请求同样具备全部 API 权限。

#### 1.3.4 会话存储

`websession.Store`（内存实现）：`token → {userID, 过期时间}`。

- `Create(userID)` 生成 32 字节随机 token 并登记；
- `Validate(token)` 返回 `(userID, ok)`，命中即将过期时间刷新为"当前时间 + 1 小时"（滑动续期）；
- `DeleteOtherByUser(userID, keepToken)` 删除该用户除指定 token 外的全部会话（修改密码后调用）；
- Cookie 的 `Max-Age` 为 0（浏览器会话 Cookie），有效期完全由服务端控制。

#### 1.3.5 CLI 子命令

```
groot user reset [-y]
```

删除用户表全部数据。执行前显示将删除的用户数量并要求确认（`y/n`），`-y` 跳过确认。运行中的服务重启后（或原会话过期后）重置才对 Web 会话生效。

#### 1.3.6 前端流程

路由守卫在首次进入时调用 `GET /web/me`：

```
needs_setup = true            → /setup（创建用户页）
needs_setup = false 且未登录  → /login
已登录                        → 正常进入应用；访问 /login、/setup 时重定向到主页面
```

- **SetupView**：用户名 / 密码 / 密码确认，前端校验必填、密码 ≥ 8 位、两次一致；创建成功后跳转登录页（不自动登录）。
- **LoginView**：用户名 / 密码登录。
- **修改密码**：设置弹窗中的"账户"区块，原密码 / 新密码 / 新密码确认三项。

## 二、迭代说明

### 2.1 与上一版差异

- **新增**：数据库 `users` 表及 `UserRepo`（`internal/repo/userdb`）；`POST /web/setup`、`POST /web/password` 端点；`GET /web/me` 响应增加 `needs_setup`、`username` 字段；`WebSession` 中间件；前端 SetupView 创建用户页与设置弹窗修改密码区块；`groot user reset` 子命令；会话滑动续期与按用户踢会话能力；Cookie Secure 自动判断；`last_login_at` 记录。
- **移除**：配置文件 `security.web` 段（`enabled/username/password/session_ttl/secure`）及 `WebConfig` 结构体、`GROOT_WEB_PASS` 环境变量约定，不做向后兼容（旧配置中的该段会被忽略）；公共 API 中的 `GET /health`、`/agents`、`/skills`、`/tools`、`/models` 五个端点。
- **调整**：上述五个信息端点迁移至 `/web` 前缀下，仅供 WebUI 使用（`/web/health` 免登录，其余需会话）；`groot status` 改调 `GET /web/health`；密码校验由配置明文比对改为数据库 bcrypt 校验；Web 登录认证由可配置开关改为始终启用；会话有效期由可配置（默认 24h）改为固定 1 小时滑动续期；API Key 权限映射中 `skills`、`tools` 权限对应的公共端点不复存在。
