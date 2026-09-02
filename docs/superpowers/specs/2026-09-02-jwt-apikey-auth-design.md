# JWT API Key 认证设计文档

## 一、功能设计

### 1.1 功能概述

Groot 对外 API 采用基于 JWT 的 API Key 认证。认证始终开启，所有对外 API 请求必须携带有效凭证：

- **API 调用方**：在请求头（头名称可配置，默认 `X-API-Key`）中携带 API Key。API Key 本身是一个 HS256 签名的 JWT，内含 Key 标识、名称、权限范围、签发时间与过期时间。
- **Web 界面用户**：使用登录会话 Cookie 认证，与 API Key 体系互不干扰。

API Key 的元数据保存在数据库中，管理员通过 Web 界面的系统配置对 API Key 进行创建、查看、复制、删除。完整的 JWT 字符串不落库——由配置文件中的 secret 与数据库中的元数据按需**确定性还原**：同一行元数据 + 同一 secret，任何时候重签出的 JWT 字节级一致。

该设计解决的问题：

1. API 凭证的全生命周期（创建、查看、吊销、过期）可在界面上维护，无需修改配置文件和重启服务。
2. 凭证信息自包含（权限、过期时间编码在 JWT 内），验证时先验签再查库，库中行不存在即视为已吊销。
3. 数据库不存储任何可直接使用的完整凭证，凭证的有效性最终由配置文件中的 secret 背书；更换 secret 即可让全部 Key 立即失效。

### 1.2 能力清单

- 认证始终开启，无开关；未携带凭证或凭证无效的对外 API 请求返回 401。
- 请求头名称可通过配置项 `security.auth.header_name` 定制，默认 `X-API-Key`。
- JWT 签名密钥 `security.auth.secret` 由 `groot init` 自动生成（32 字节强随机，hex 编码）；服务启动时检测到 secret 为空则自动生成并回写 config.yaml。
- Web 界面提供 API Keys 管理页：列表、创建、查看完整 Key、复制、删除。
- 创建 Key 时指定：名称（唯一）、过期时间（1天 / 7天 / 1个月 / 半年 / 1年 / 10年，自创建时刻起算）、权限范围（多选）。
- Key 创建后不可编辑；需要变更时删除重建（保证还原出的 JWT 恒等于最初签发的那个）。
- 删除即吊销：删除数据库行后，调用方手中的 JWT 虽验签仍通过，但因 `jti` 反查不到而被拒绝。
- 权限模型：权限点为 `chat`、`status`、`detail`、`history`、`session`、`schedule`、`all`，请求路径到权限点的映射关系见中间件 `getRequiredPermission`。

### 1.3 配置设计

`~/.groot/config.yaml`：

```yaml
security:
  auth:
    header_name: X-API-Key   # 调用方携带 API Key 的请求头名称
    secret: "a3f8...64位hex"  # JWT 签名密钥，init 自动生成
  rate_limit: ...
```

Go 配置结构：

```go
type AuthConfig struct {
    HeaderName string `yaml:"header_name"`
    Secret     string `yaml:"secret"`
}
```

secret 变更的影响：全部已签发 Key 立即失效（验签不通过），此为预期行为，用于紧急吊销全部凭证的场景，写入使用说明。

### 1.4 JWT 设计

- 算法：HS256，依赖库 `github.com/golang-jwt/jwt/v5`。
- Claims 全部来源于数据库行，保证确定性还原：

| Claim   | 来源           | 说明 |
|---------|---------------|------|
| `jti`   | api_keys.id   | Key 标识，字符串，格式 `yyyyMMddHHmmss` |
| `sub`   | name          | Key 名称，同时作为 caller 标识（限流、日志沿用） |
| `scope` | permissions   | 权限点数组 |
| `iat`   | created_at    | 签发时间 |
| `exp`   | expires_at    | 过期时间 |

- 签发与解析逻辑封装在独立包 `internal/auth/`，提供纯函数：
  - 元数据 + secret → JWT 字符串（签发 / 还原共用同一函数）
  - JWT 字符串 + secret → claims（验签失败、已过期返回错误）
- claims 使用固定字段顺序的 struct 序列化，确保同输入同输出。

### 1.5 数据库设计

新增表 `api_keys`：

| 列          | 类型   | 说明 |
|-------------|--------|------|
| id          | string | 主键，`yyyyMMddHHmmss` 格式（如 `20260902153045`），创建时刻生成 |
| name        | string | 唯一 |
| permissions | string | JSON 数组，如 `["chat","status"]` |
| expires_at  | int64  | 毫秒时间戳 |
| created_at  | int64  | 毫秒时间戳 |

- 主键同秒冲突兜底：插入遇主键冲突时自动 +1 秒重试（有限次数），重试仍失败才向用户报错。
- 仓库层：`repo.APIKeyRepo` 接口（`Create / List / GetByID / DeleteByID`），实现位于 `internal/repo/apikeydb/`，注册进 repofactory；permissions 的 JSON 序列化处理方式与 modeldb 的 stop 字段一致（反序列化失败按空数组处理）。

### 1.6 认证中间件流程

对外 API 请求的验证顺序：

1. Web 会话 Cookie 有效 → 等同 all 权限放行。
2. 从 `header_name` 指定的请求头取 token；缺失 → 401。
3. JWT 验签 + 过期检查；失败 → 401。
4. 以 `jti` 查 `api_keys` 表；行不存在（已吊销）→ 401。
5. 权限检查以数据库行的 permissions 为准，按路径→权限点映射判定；不足 → 403。
6. `caller` 设为 Key 名称，后续限流按 caller 维度执行。

错误响应策略：验签失败、过期、已吊销统一返回 401 且不区分原因（避免向攻击者泄露信息）；权限不足返回 403。

每个请求查询一次数据库；本地 SQLite 查询开销为微秒级，当前规模不引入缓存。

### 1.7 Web 管理接口与界面

后端端点（`/web` 组，WebSession 中间件保护）：

```
GET    /web/apikeys            列表（元数据 + 计算状态：有效 / 已过期）
POST   /web/apikeys            创建 {name, expires_in, permissions} → 返回元数据 + 完整 JWT
GET    /web/apikeys/:id/token  按需重签还原完整 JWT（查看 / 复制）
DELETE /web/apikeys/:id        删除（即吊销）
```

- `expires_in` 为枚举：`1d / 7d / 1mo / 6mo / 1y / 10y`，服务端按创建时刻换算 `expires_at`。
- 参数校验：名称非空且唯一、`expires_in` 合法、permissions 为合法权限点子集。

前端（Vue 3 + Element Plus，交互模式与模型管理页一致）：

- 系统配置中新增 "API Keys" 管理页。
- 表格列：名称、权限范围（标签展示）、创建时间、过期时间、状态、操作（查看 Key / 复制 / 删除）。
- 创建对话框：名称输入 + 过期时间下拉 + 权限多选。
- 创建成功后弹窗展示完整 Key 供复制；之后可随时通过"查看 Key"再次获取。
- 删除需二次确认，提示"删除后该 Key 立即失效"。

### 1.8 测试设计

Go 单元测试：

- `internal/auth/`：签发-还原恒等性（同元数据同 secret 输出字节一致）、过期 token 拒绝、篡改 token 验签失败、错误 secret 验签失败。
- `internal/repo/apikeydb/`：CRUD、名称唯一约束、主键同秒冲突重试。
- `internal/api/middleware/`：无凭证 401、无效 token 401、已删除 Key 401、权限不足 403、Web Cookie 放行、caller 注入。
- `internal/api/handler/`：创建参数校验（名称重复、非法 expires_in、非法权限点）、token 还原端点、删除端点。

系统测试（Python，用户自行运行）：端到端创建 Key → 调用 API → 删除 Key → 调用被拒。

### 1.9 文档更新

- README（用户使用手册）的 API 认证章节：说明请求头携带方式、Key 在 Web 界面获取；`/web/apikeys` 为 Web 自用端点，不列入对外 API 文档。

## 二、迭代说明

### 2.1 与上一版差异

- 移除：`security.auth.enabled` 开关（认证改为始终开启）、`security.auth.type`、`security.auth.api_key.keys` 配置列表。
- 移除：配置文件中维护 API Key 的方式；原随机串格式的 Key 全部废弃，升级后需在 Web 界面重新创建（不做自动迁移）。
- 新增：`security.auth.secret` 配置项（init 自动生成；启动时为空则自动生成回写）。
- 新增：`api_keys` 数据库表、`repo.APIKeyRepo` 接口与 `internal/repo/apikeydb/` 实现。
- 新增：`internal/auth/` JWT 签发 / 解析包，依赖 `github.com/golang-jwt/jwt/v5`。
- 新增：`/web/apikeys` 管理端点与前端 API Keys 管理页。
- 调整：认证中间件从"遍历配置列表比对明文 Key"改为"JWT 验签 + 数据库反查吊销状态"；`caller`、限流、路径→权限点映射逻辑保持原有行为。
- 调整：API Key 从可随时改配置变为创建后不可编辑（删除重建代替修改）。
- Web Cookie 登录体系无变化。
