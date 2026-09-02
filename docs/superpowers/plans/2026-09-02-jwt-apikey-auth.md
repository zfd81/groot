# JWT API Key 认证实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 API Key 认证从"配置文件明文列表"重构为"JWT 签名 + 数据库元数据"，认证始终开启，并提供 Web 管理界面。

**Architecture:** API Key 本身是 HS256 签名的 JWT（claims 全部来自 `api_keys` 表行，用 `security.auth.secret` 签名，确定性还原）。中间件先验签再以 `jti` 反查数据库（删除即吊销），权限检查以库中行为准。Web 界面通过 `/web/apikeys` 端点做全生命周期管理。

**Tech Stack:** Go + Hertz + sqlx（后端）、`github.com/golang-jwt/jwt/v5`（新依赖）、Vue 3 + Element Plus（前端）。

**设计文档:** `docs/superpowers/specs/2026-09-02-jwt-apikey-auth-design.md`

> ⚠️ **提交规范**：本项目禁止自动 git commit（见 CLAUDE.md）。每个 Task 完成后停在"测试通过"状态，由用户确认后统一提交。计划中不含 commit 步骤。

---

### Task 1: 添加 golang-jwt 依赖

**Files:**
- Modify: `go.mod`、`go.sum`

- [ ] **Step 1: 安装依赖**

Run: `cd /Users/zhangfengda/workspace/groot && go get github.com/golang-jwt/jwt/v5`
Expected: `go: added github.com/golang-jwt/jwt/v5 v5.x.x`

- [ ] **Step 2: 验证编译**

Run: `go build ./...`
Expected: 无输出（编译通过）

---

### Task 2: repo 层类型、DDL 与 apikeydb 实现

**Files:**
- Create: `internal/repo/apikey.go`
- Create: `internal/repo/apikeydb/apikey.go`
- Create: `internal/repo/apikeydb/apikey_test.go`
- Modify: `internal/db/migrate.go`（三个 dialect 的 DDL 函数末尾各追加 api_keys 表）
- Modify: `internal/repo/repofactory/factory.go`

- [ ] **Step 1: 定义领域类型与接口**

创建 `internal/repo/apikey.go`：

```go
package repo

import (
	"context"
	"time"
)

// APIKey 对外 API 访问凭证的元数据。完整 JWT 不落库：
// 由 config 中的 secret + 本结构按需确定性还原（见 internal/auth）。
type APIKey struct {
	ID          string   // 主键，yyyyMMddHHmmss 格式（如 "20260902153045"），同时作为 JWT 的 jti
	Name        string   // 全局唯一
	Permissions []string // 权限点集合，创建时校验非空
	ExpiresAt   time.Time
	CreatedAt   time.Time // 秒级精度（签发 JWT 时取 Unix 秒）
}

// ValidPermissions API Key 可用的权限点全集，
// 与 middleware.getRequiredPermission 的路径映射保持一致。
var ValidPermissions = []string{"chat", "status", "detail", "history", "session", "schedule", "all"}

// APIKeyRepo API Key 元数据存储接口
type APIKeyRepo interface {
	// Create 按 k.ID 原样写入；主键或名称唯一冲突时返回底层驱动错误
	Create(ctx context.Context, k *APIKey) error
	// GetByID 按 ID 查询，未找到返回 ErrNotFound
	GetByID(ctx context.Context, id string) (*APIKey, error)
	// GetByName 按名称查询，未找到返回 ErrNotFound
	GetByName(ctx context.Context, name string) (*APIKey, error)
	// List 返回全部 Key，按 created_at 降序
	List(ctx context.Context) ([]*APIKey, error)
	// DeleteByID 按 ID 删除；未找到返回 ErrNotFound
	DeleteByID(ctx context.Context, id string) error
}
```

- [ ] **Step 2: 添加三个方言的 DDL**

修改 `internal/db/migrate.go`。在 `sqliteDDL()` 返回的切片末尾（`uk_models_name` 索引之后）追加：

```go
		`CREATE TABLE IF NOT EXISTS api_keys (
			id          TEXT NOT NULL PRIMARY KEY,
			name        TEXT NOT NULL,
			permissions TEXT NOT NULL DEFAULT '[]',
			expires_at  INTEGER NOT NULL,
			created_at  INTEGER NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_api_keys_name ON api_keys(name)`,
```

在 `mysqlDDL()` 末尾追加：

```go
		`CREATE TABLE IF NOT EXISTS api_keys (
			id          VARCHAR(14)  NOT NULL PRIMARY KEY,
			name        VARCHAR(64)  NOT NULL,
			permissions TEXT         NOT NULL,
			expires_at  BIGINT       NOT NULL,
			created_at  BIGINT       NOT NULL,
			UNIQUE KEY uk_api_keys_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
```

在 `postgresDDL()` 末尾追加：

```go
		`CREATE TABLE IF NOT EXISTS api_keys (
			id          VARCHAR(14)  NOT NULL PRIMARY KEY,
			name        VARCHAR(64)  NOT NULL,
			permissions TEXT         NOT NULL DEFAULT '[]',
			expires_at  BIGINT       NOT NULL,
			created_at  BIGINT       NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_api_keys_name ON api_keys(name)`,
```

- [ ] **Step 3: 编写 apikeydb 失败测试**

创建 `internal/repo/apikeydb/apikey_test.go`（测试基建仿照 `internal/repo/modeldb/model_test.go`，用 SQLite 临时库）：

```go
package apikeydb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.APIKeyRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func newKey(id, name string) *repo.APIKey {
	created := time.Now().Truncate(time.Second)
	return &repo.APIKey{
		ID:          id,
		Name:        name,
		Permissions: []string{"chat", "status"},
		ExpiresAt:   created.AddDate(0, 0, 7),
		CreatedAt:   created,
	}
}

func TestAPIKeyRepo_CreateAndGet(t *testing.T) {
	r := newTestRepo(t)
	k := newKey("20260902120000", "svc-a")
	if err := r.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := r.GetByID(context.Background(), "20260902120000")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "svc-a" || len(got.Permissions) != 2 || got.Permissions[0] != "chat" {
		t.Errorf("unexpected row: %+v", got)
	}
	// 毫秒级时间戳往返无损
	if !got.CreatedAt.Equal(k.CreatedAt) || !got.ExpiresAt.Equal(k.ExpiresAt) {
		t.Errorf("timestamps not round-tripped: got %v/%v want %v/%v",
			got.CreatedAt, got.ExpiresAt, k.CreatedAt, k.ExpiresAt)
	}
	byName, err := r.GetByName(context.Background(), "svc-a")
	if err != nil || byName.ID != "20260902120000" {
		t.Errorf("GetByName: %v, %+v", err, byName)
	}
}

func TestAPIKeyRepo_GetNotFound(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.GetByID(context.Background(), "20000101000000"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("GetByID missing should be ErrNotFound, got %v", err)
	}
	if _, err := r.GetByName(context.Background(), "nope"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("GetByName missing should be ErrNotFound, got %v", err)
	}
}

func TestAPIKeyRepo_DuplicateIDAndName(t *testing.T) {
	r := newTestRepo(t)
	if err := r.Create(context.Background(), newKey("20260902120000", "svc-a")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 主键冲突
	if err := r.Create(context.Background(), newKey("20260902120000", "svc-b")); err == nil {
		t.Error("duplicate id should fail")
	}
	// 名称唯一冲突
	if err := r.Create(context.Background(), newKey("20260902120001", "svc-a")); err == nil {
		t.Error("duplicate name should fail")
	}
}

func TestAPIKeyRepo_ListOrder(t *testing.T) {
	r := newTestRepo(t)
	old := newKey("20260901000000", "old")
	old.CreatedAt = time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := r.Create(context.Background(), old); err != nil {
		t.Fatalf("Create old: %v", err)
	}
	if err := r.Create(context.Background(), newKey("20260902120000", "new")); err != nil {
		t.Fatalf("Create new: %v", err)
	}
	list, err := r.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %v, len=%d", err, len(list))
	}
	if list[0].Name != "new" {
		t.Errorf("List should be created_at DESC, got first=%s", list[0].Name)
	}
}

func TestAPIKeyRepo_Delete(t *testing.T) {
	r := newTestRepo(t)
	if err := r.Create(context.Background(), newKey("20260902120000", "svc-a")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.DeleteByID(context.Background(), "20260902120000"); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	if _, err := r.GetByID(context.Background(), "20260902120000"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("deleted key should be ErrNotFound, got %v", err)
	}
	if err := r.DeleteByID(context.Background(), "20260902120000"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("delete missing should be ErrNotFound, got %v", err)
	}
}

func TestAPIKeyRepo_CorruptPermissions(t *testing.T) {
	r := newTestRepo(t)
	k := newKey("20260902120000", "svc-a")
	k.Permissions = nil // permsJSON 应写入 '[]'
	if err := r.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := r.GetByID(context.Background(), "20260902120000")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Permissions == nil || len(got.Permissions) != 0 {
		t.Errorf("nil permissions should round-trip as empty slice, got %+v", got.Permissions)
	}
}
```

- [ ] **Step 4: 运行测试确认失败**

Run: `go test ./internal/repo/apikeydb/... -v`
Expected: 编译失败 `undefined: New`（包尚未实现）

- [ ] **Step 5: 实现 apikeydb**

创建 `internal/repo/apikeydb/apikey.go`（模式与 `modeldb` 完全一致）：

```go
package apikeydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

type apiKeyRepo struct {
	db      *sqlx.DB
	dialect db.Dialect
}

func New(sqlxDB *sqlx.DB, dialect db.Dialect) repo.APIKeyRepo {
	return &apiKeyRepo{db: sqlxDB, dialect: dialect}
}

type apiKeyRow struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Permissions string `db:"permissions"`
	ExpiresAt   int64  `db:"expires_at"`
	CreatedAt   int64  `db:"created_at"`
}

const apiKeyColumns = `id, name, permissions, expires_at, created_at`

func rowToAPIKey(row apiKeyRow) *repo.APIKey {
	var perms []string
	// 序列化损坏时按空数组处理（等同无任何权限），不拖垮整个列表
	if err := json.Unmarshal([]byte(row.Permissions), &perms); err != nil || perms == nil {
		perms = []string{}
	}
	return &repo.APIKey{
		ID:          row.ID,
		Name:        row.Name,
		Permissions: perms,
		ExpiresAt:   time.UnixMilli(row.ExpiresAt),
		CreatedAt:   time.UnixMilli(row.CreatedAt),
	}
}

func permsJSON(perms []string) string {
	if perms == nil {
		perms = []string{}
	}
	b, err := json.Marshal(perms)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (r *apiKeyRepo) Create(ctx context.Context, k *repo.APIKey) error {
	q := r.db.Rebind(`INSERT INTO api_keys (id, name, permissions, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		k.ID, k.Name, permsJSON(k.Permissions), k.ExpiresAt.UnixMilli(), k.CreatedAt.UnixMilli())
	return err
}

func (r *apiKeyRepo) GetByID(ctx context.Context, id string) (*repo.APIKey, error) {
	var row apiKeyRow
	q := r.db.Rebind(`SELECT ` + apiKeyColumns + ` FROM api_keys WHERE id=?`)
	err := r.db.GetContext(ctx, &row, q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToAPIKey(row), nil
}

func (r *apiKeyRepo) GetByName(ctx context.Context, name string) (*repo.APIKey, error) {
	var row apiKeyRow
	q := r.db.Rebind(`SELECT ` + apiKeyColumns + ` FROM api_keys WHERE name=?`)
	err := r.db.GetContext(ctx, &row, q, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rowToAPIKey(row), nil
}

func (r *apiKeyRepo) List(ctx context.Context) ([]*repo.APIKey, error) {
	var rows []apiKeyRow
	q := `SELECT ` + apiKeyColumns + ` FROM api_keys ORDER BY created_at DESC`
	if err := r.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, err
	}
	result := make([]*repo.APIKey, 0, len(rows))
	for _, row := range rows {
		result = append(result, rowToAPIKey(row))
	}
	return result, nil
}

func (r *apiKeyRepo) DeleteByID(ctx context.Context, id string) error {
	q := r.db.Rebind(`DELETE FROM api_keys WHERE id=?`)
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/repo/apikeydb/... -v`
Expected: 全部 PASS

- [ ] **Step 7: 注册进 repofactory**

修改 `internal/repo/repofactory/factory.go`：import 增加 `"github.com/zfd81/groot/internal/repo/apikeydb"`；`Repos` 结构体增加字段 `APIKey repo.APIKeyRepo`；`NewRepos` 返回值增加 `APIKey: apikeydb.New(sqlxDB, dialect),`。

- [ ] **Step 8: 验证编译与既有测试**

Run: `go build ./... && go test ./internal/db/... ./internal/repo/... -v`
Expected: 编译通过，全部 PASS（含既有 modeldb 等测试）

---

### Task 3: internal/auth JWT 签发与验证包

**Files:**
- Create: `internal/auth/jwt.go`
- Create: `internal/auth/jwt_test.go`

- [ ] **Step 1: 编写失败测试**

创建 `internal/auth/jwt_test.go`：

```go
package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testKey() *repo.APIKey {
	created := time.Date(2026, 9, 2, 12, 0, 0, 0, time.Local)
	return &repo.APIKey{
		ID:          "20260902120000",
		Name:        "svc-a",
		Permissions: []string{"chat", "status"},
		ExpiresAt:   created.AddDate(0, 0, 7),
		CreatedAt:   created,
	}
}

// TestSign_Deterministic 同元数据 + 同 secret 多次签发输出字节级一致（还原恒等性的基础）。
func TestSign_Deterministic(t *testing.T) {
	k := testKey()
	t1, err := Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	t2, err := Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign again: %v", err)
	}
	if t1 != t2 {
		t.Errorf("Sign not deterministic:\n%s\n%s", t1, t2)
	}
	if len(strings.Split(t1, ".")) != 3 {
		t.Errorf("not a JWT: %s", t1)
	}
}

// TestVerify_RoundTrip 签发后验证返回原 jti。
func TestVerify_RoundTrip(t *testing.T) {
	token, err := Sign(testKey(), testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	jti, err := Verify(token, testSecret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if jti != "20260902120000" {
		t.Errorf("jti = %q, want 20260902120000", jti)
	}
}

// TestVerify_Expired 过期 token 拒绝。
func TestVerify_Expired(t *testing.T) {
	k := testKey()
	k.CreatedAt = time.Now().AddDate(0, 0, -8)
	k.ExpiresAt = time.Now().AddDate(0, 0, -1)
	token, err := Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Verify(token, testSecret); err == nil {
		t.Error("expired token should fail")
	}
}

// TestVerify_WrongSecret 错误 secret 验签失败（换 secret 即全部失效）。
func TestVerify_WrongSecret(t *testing.T) {
	token, err := Sign(testKey(), testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Verify(token, "another-secret"); err == nil {
		t.Error("wrong secret should fail")
	}
}

// TestVerify_Tampered 篡改载荷验签失败。
func TestVerify_Tampered(t *testing.T) {
	token, err := Sign(testKey(), testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(token, ".")
	tampered := parts[0] + ".eyJqdGkiOiJoYWNrZWQifQ." + parts[2]
	if _, err := Verify(tampered, testSecret); err == nil {
		t.Error("tampered token should fail")
	}
}

// TestVerify_Garbage 非 JWT 字符串（如旧版随机串 Key）拒绝。
func TestVerify_Garbage(t *testing.T) {
	for _, s := range []string{"", "not-a-jwt", "a.b", "a.b.c.d"} {
		if _, err := Verify(s, testSecret); err == nil {
			t.Errorf("garbage %q should fail", s)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/auth/... -v`
Expected: 编译失败 `undefined: Sign`

- [ ] **Step 3: 实现 jwt.go**

创建 `internal/auth/jwt.go`：

```go
// Package auth 提供 API Key（JWT）的签发与验证。
// 签发是确定性的：同一份元数据 + 同一 secret，任何时候输出的 JWT 字节级一致，
// 因此完整 Key 无需落库，可随时由数据库元数据还原（jwt.MapClaims 底层是 map，
// json.Marshal 对 map 按 key 排序，保证序列化稳定）。
package auth

import (
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"

	"github.com/zfd81/groot/internal/repo"
)

// ErrInvalidToken 验签失败、格式非法或已过期。
// 对外统一映射为 401 且不区分原因，避免向攻击者泄露信息。
var ErrInvalidToken = errors.New("auth: invalid token")

// Sign 用 secret 对 API Key 元数据签发 HS256 JWT。
func Sign(k *repo.APIKey, secret string) (string, error) {
	perms := k.Permissions
	if perms == nil {
		perms = []string{}
	}
	claims := jwt.MapClaims{
		"jti":   k.ID,
		"sub":   k.Name,
		"scope": perms,
		"iat":   k.CreatedAt.Unix(),
		"exp":   k.ExpiresAt.Unix(),
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("auth sign: %w", err)
	}
	return s, nil
}

// Verify 验签并校验过期（jwt.Parse 对存在的 exp 自动校验），
// 成功返回 jti（API Key 的数据库 ID），供调用方反查吊销状态。
func Verify(tokenStr, secret string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid {
		return "", ErrInvalidToken
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", ErrInvalidToken
	}
	jti, ok := claims["jti"].(string)
	if !ok || jti == "" {
		return "", ErrInvalidToken
	}
	return jti, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/auth/... -v`
Expected: 全部 PASS

---

### Task 4: 配置结构改造与 secret 自动生成

**Files:**
- Modify: `internal/config/config.go:104-121`（AuthConfig 展平，删除 APIKeyConfig/KeyInfo）
- Modify: `internal/config/defaults.go:56-65`
- Modify: `internal/config/loader.go:145-157`（expandConfigEnvVars 删 keys 循环）+ applyDefaults 补 HeaderName 默认值
- Create: `internal/config/secret.go`
- Create: `internal/config/secret_test.go`

- [ ] **Step 1: 改造配置结构**

`internal/config/config.go` 中，将 `AuthConfig`、`APIKeyConfig`、`KeyInfo` 三个类型替换为：

```go
// AuthConfig holds authentication settings（认证始终开启，无开关）
type AuthConfig struct {
	HeaderName string `yaml:"header_name"` // API Key 请求头名称，默认 X-API-Key
	Secret     string `yaml:"secret"`      // JWT 签名密钥；为空时服务启动自动生成并回写 config.yaml
}
```

`internal/config/defaults.go` 中 `Auth` 字段改为：

```go
			Auth: AuthConfig{
				HeaderName: "X-API-Key",
			},
```

`internal/config/loader.go`：
1. `applyDefaults` 的 Logging defaults 之前插入：

```go
	// Auth defaults（认证始终开启，只需 header 名默认值；secret 由 EnsureAuthSecret 兜底）
	if cfg.Security.Auth.HeaderName == "" {
		cfg.Security.Auth.HeaderName = "X-API-Key"
	}
```

2. `expandConfigEnvVars` 中删除 `cfg.Security.Auth.APIKey.Keys` 的循环（保留 Database DSN 展开）。

- [ ] **Step 2: 编写 secret 生成/回写的失败测试**

创建 `internal/config/secret_test.go`：

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestGenerateAuthSecret(t *testing.T) {
	s1, err := GenerateAuthSecret()
	if err != nil {
		t.Fatalf("GenerateAuthSecret: %v", err)
	}
	if len(s1) != 64 {
		t.Errorf("secret length = %d, want 64 hex chars", len(s1))
	}
	s2, _ := GenerateAuthSecret()
	if s1 == s2 {
		t.Error("two secrets should differ")
	}
}

// TestEnsureAuthSecret_AlreadySet 已配置 secret 时不改文件。
func TestEnsureAuthSecret_AlreadySet(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "security:\n  auth:\n    secret: existing\n")
	before, _ := os.ReadFile(p)
	cfg := &Config{}
	cfg.Security.Auth.Secret = "existing"
	if err := EnsureAuthSecret(dir, cfg); err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("file should be untouched when secret already set")
	}
}

// TestEnsureAuthSecret_AppendWhenNoSecurityNode 无活动 security 节（如全注释模板）时
// 追加文本块，且原有注释内容原样保留。
func TestEnsureAuthSecret_AppendWhenNoSecurityNode(t *testing.T) {
	dir := t.TempDir()
	original := "# Groot 配置\n#security:\n#  auth:\n#    secret: xxx\nserver:\n  port: 8080\n"
	p := writeConfig(t, dir, original)
	cfg := &Config{}
	if err := EnsureAuthSecret(dir, cfg); err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	if cfg.Security.Auth.Secret == "" {
		t.Fatal("cfg secret should be set")
	}
	data, _ := os.ReadFile(p)
	content := string(data)
	if !strings.Contains(content, "# Groot 配置") || !strings.Contains(content, "port: 8080") {
		t.Error("original content should be preserved")
	}
	// 回写后的文件必须能被 yaml 解析且读出同一 secret
	var parsed Config
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("patched file not valid yaml: %v\n%s", err, content)
	}
	if parsed.Security.Auth.Secret != cfg.Security.Auth.Secret {
		t.Errorf("file secret %q != cfg secret %q", parsed.Security.Auth.Secret, cfg.Security.Auth.Secret)
	}
}

// TestEnsureAuthSecret_PatchExistingSecurityNode 已有活动 security 节时以节点方式写入，
// 不产生重复键，且已有子节点保留。
func TestEnsureAuthSecret_PatchExistingSecurityNode(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "security:\n  rate_limit:\n    enabled: true\n")
	cfg := &Config{}
	if err := EnsureAuthSecret(dir, cfg); err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	data, _ := os.ReadFile(p)
	var parsed Config
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("patched file not valid yaml: %v\n%s", err, string(data))
	}
	if parsed.Security.Auth.Secret != cfg.Security.Auth.Secret {
		t.Errorf("secret not written: %q", parsed.Security.Auth.Secret)
	}
	if !parsed.Security.RateLimit.Enabled {
		t.Error("existing rate_limit node should be preserved")
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/config/ -run 'TestGenerateAuthSecret|TestEnsureAuthSecret' -v`
Expected: 编译失败 `undefined: GenerateAuthSecret`

- [ ] **Step 4: 实现 secret.go**

创建 `internal/config/secret.go`：

```go
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// GenerateAuthSecret 生成 32 字节强随机 JWT 签名密钥（hex 编码，64 字符）。
func GenerateAuthSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机密钥失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// EnsureAuthSecret 确保 cfg 拥有 JWT 签名密钥：已配置则直接返回；
// 为空则生成新密钥、回写 config.yaml 并更新 cfg（覆盖老版本升级场景）。
func EnsureAuthSecret(homeDir string, cfg *Config) error {
	if cfg.Security.Auth.Secret != "" {
		return nil
	}
	secret, err := GenerateAuthSecret()
	if err != nil {
		return err
	}
	configPath := filepath.Join(homeDir, "config.yaml")
	if err := writeSecretToFile(configPath, secret); err != nil {
		return err
	}
	cfg.Security.Auth.Secret = secret
	return nil
}

// writeSecretToFile 把 security.auth.secret 写入配置文件。
// 文件中无活动 security 节（常见：模板全注释）时直接追加文本块，原文一字不动；
// 已有活动 security 节时用 yaml.Node 就地插入（避免重复键），注释随节点保留。
func writeSecretToFile(configPath, secret string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}
	if !hasTopLevelKey(&root, "security") {
		block := fmt.Sprintf("\n# JWT 签名密钥（系统自动生成，请勿泄露；更换后所有 API Key 立即失效）\nsecurity:\n  auth:\n    secret: \"%s\"\n", secret)
		if err := os.WriteFile(configPath, append(data, []byte(block)...), 0644); err != nil {
			return fmt.Errorf("写入配置文件失败: %w", err)
		}
		return nil
	}
	doc := root.Content[0]
	authNode := ensureMapChild(ensureMapChild(doc, "security"), "auth")
	setMapValue(authNode, "secret", secret)
	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := os.WriteFile(configPath, out, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	return nil
}

// hasTopLevelKey 判断 yaml 文档顶层映射是否存在指定 key。
func hasTopLevelKey(root *yaml.Node, key string) bool {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return false
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == key {
			return true
		}
	}
	return false
}

// ensureMapChild 在映射节点 parent 下取 key 对应的子映射节点；不存在则创建。
func ensureMapChild(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, k, v)
	return v
}

// setMapValue 设置映射节点下 key 的字符串值；已存在则覆盖。
func setMapValue(parent *yaml.Node, key, value string) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/config/ -run 'TestGenerateAuthSecret|TestEnsureAuthSecret' -v`
Expected: 全部 PASS

- [ ] **Step 6: 修复受影响的既有代码与测试**

Run: `go build ./... 2>&1 | head -30`

此时 `internal/api/middleware/auth.go`、`auth_test.go` 会因引用已删除的 `APIKeyConfig`/`KeyInfo`/`Enabled` 编译失败——这是预期的，留到 Task 6 一并重写。`internal/config` 包内的既有测试（`config_test.go`、`loader_test.go` 等）若引用了删除的字段，按新结构修正断言（改为断言 `HeaderName`/`Secret`）。

Run: `go test ./internal/config/... -v`
Expected: 全部 PASS

---

### Task 5: 配置模板与 init 命令

**Files:**
- Modify: `internal/config/template.go`（签名改为接收 secret；security 节激活）
- Modify: `internal/config/template_test.go`（4 处调用补参数）
- Modify: `internal/cmd/init.go:139`

- [ ] **Step 1: 更新模板函数**

`internal/config/template.go`：签名改为 `func GenerateConfigTemplate(authSecret string) string`，函数体返回值改用 `fmt.Sprintf`（import 增加 `"fmt"`），并把原"安全配置"注释块（`#security:` 到 `#          permissions: [all]       # 权限范围` 的整段）替换为：

```go
# 安全配置
security:
  auth:
    header_name: X-API-Key         # API Key 请求头名称
    secret: "%s"                   # JWT 签名密钥（自动生成，请勿泄露；更换后所有 API Key 立即失效）
#  rate_limit:
#    enabled: false                   # 是否启用速率限制
#    global_qps: 0                    # 全局 QPS 限制（0 表示不限制）
#    global_concurrency: 0            # 全局并发限制（0 表示不限制）
#    default_qps: 10                  # 每个 API Key 的默认 QPS
#    default_concurrency: 5           # 每个 API Key 的默认并发数
#    cleanup_interval: 5m             # 空闲限流器清理间隔
```

（`%s` 占位由 `fmt.Sprintf(template, authSecret)` 填充；注意模板字符串中若还有其他 `%` 字符需转义为 `%%`——当前模板没有。API Key 的维护入口在 Web 界面"设置 → API Keys"，模板中不再包含 keys 列表。）

- [ ] **Step 2: 更新 init 命令**

`internal/cmd/init.go` 的 `createConfigFile` 中：

```go
	secret, err := config.GenerateAuthSecret()
	if err != nil {
		return fmt.Errorf("生成认证密钥失败: %w", err)
	}
	template := config.GenerateConfigTemplate(secret)
```

- [ ] **Step 3: 更新模板测试并运行**

`internal/config/template_test.go` 中 4 处 `GenerateConfigTemplate()` 调用改为 `GenerateConfigTemplate("test-secret")`；在 `TestGenerateConfigTemplate` 中追加断言：

```go
	if !strings.Contains(content, `secret: "test-secret"`) {
		t.Error("template should contain the injected auth secret")
	}
	if strings.Contains(content, "keys:") || strings.Contains(content, "enabled: false                 # 是否开启认证") {
		t.Error("template should not contain legacy auth keys config")
	}
```

Run: `go test ./internal/config/... ./internal/cmd/... -v`
Expected: 全部 PASS（若 `init_test.go` 有模板相关断言一并按新内容修正）

---

### Task 6: 认证中间件改造

**Files:**
- Modify: `internal/api/middleware/auth.go`（整体重写验证流程）
- Modify: `internal/api/middleware/auth_test.go`（整体重写）

- [ ] **Step 1: 重写失败测试**

`internal/api/middleware/auth_test.go` 整体替换为：

```go
package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/apikeydb"
)

const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testSecurityConfig() config.SecurityConfig {
	return config.SecurityConfig{
		Auth: config.AuthConfig{HeaderName: "X-API-Key", Secret: testSecret},
	}
}

// newTestMiddleware 返回中间件与真实 SQLite 仓库，并预置一把 chat+history 权限的 Key。
func newTestMiddleware(t *testing.T) (*AuthMiddleware, repo.APIKeyRepo, string) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	keys := apikeydb.New(sqlxDB, dialect)
	created := time.Now().Truncate(time.Second)
	k := &repo.APIKey{
		ID:          "20260902120000",
		Name:        "svc-a",
		Permissions: []string{"chat", "history"},
		ExpiresAt:   created.AddDate(0, 0, 7),
		CreatedAt:   created,
	}
	if err := keys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create key: %v", err)
	}
	token, err := auth.Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	m := NewAuthMiddleware(testSecurityConfig(), websession.NewStore(time.Hour), keys)
	return m, keys, token
}

func serveAuth(m *AuthMiddleware, method, uri string, setup func(rc *app.RequestContext)) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	rc.Request.SetRequestURI(uri)
	setup(rc)
	m.Serve()(context.Background(), rc)
	return rc
}

// TestAuth_ValidCookie 有效 Web 会话 Cookie 通过认证，caller 为 web。
func TestAuth_ValidCookie(t *testing.T) {
	m, _, _ := newTestMiddleware(t)
	token := m.webStore.Create("u1")
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("Cookie", websession.CookieName+"="+token)
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("valid cookie should pass auth")
	}
	if GetCaller(rc) != "web" {
		t.Errorf("caller should be web, got %q", GetCaller(rc))
	}
}

// TestAuth_MissingToken 无凭证返回 401（认证始终开启，不存在匿名放行）。
func TestAuth_MissingToken(t *testing.T) {
	m, _, _ := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_ValidToken 有效 JWT 通过，caller 为 Key 名称。
func TestAuth_ValidToken(t *testing.T) {
	m, _, token := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if rc.Response.StatusCode() == 401 {
		t.Fatal("valid token should pass")
	}
	if GetCaller(rc) != "svc-a" {
		t.Errorf("caller should be svc-a, got %q", GetCaller(rc))
	}
}

// TestAuth_InvalidToken 非法字符串（含旧版随机串 Key）返回 401。
func TestAuth_InvalidToken(t *testing.T) {
	m, _, _ := newTestMiddleware(t)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", "legacy-random-key")
	})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("expected 401, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_RevokedToken 删除数据库行后原 token 立即失效（删除即吊销）。
func TestAuth_RevokedToken(t *testing.T) {
	m, keys, token := newTestMiddleware(t)
	if err := keys.DeleteByID(context.Background(), "20260902120000"); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("revoked token should 401, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_PermissionDenied Key 无所需权限点返回 403。
func TestAuth_PermissionDenied(t *testing.T) {
	m, _, token := newTestMiddleware(t)
	// 预置 Key 只有 chat+history，访问 /schedule 需要 schedule 权限
	rc := serveAuth(m, consts.MethodGet, "/schedule", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if rc.Response.StatusCode() != 403 {
		t.Fatalf("expected 403, got %d", rc.Response.StatusCode())
	}
}

// TestAuth_AllPermission all 权限可访问任意端点。
func TestAuth_AllPermission(t *testing.T) {
	m, keys, _ := newTestMiddleware(t)
	created := time.Now().Truncate(time.Second)
	k := &repo.APIKey{
		ID: "20260902120001", Name: "admin", Permissions: []string{"all"},
		ExpiresAt: created.AddDate(0, 0, 7), CreatedAt: created,
	}
	if err := keys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, _ := auth.Sign(k, testSecret)
	rc := serveAuth(m, consts.MethodGet, "/schedule", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if code := rc.Response.StatusCode(); code == 401 || code == 403 {
		t.Fatalf("all permission should pass, got %d", code)
	}
}

// TestAuth_EmptyPermissionsDenied 空权限集合一律拒绝（脏数据兜底，不再视为全权限）。
func TestAuth_EmptyPermissionsDenied(t *testing.T) {
	m, keys, _ := newTestMiddleware(t)
	created := time.Now().Truncate(time.Second)
	k := &repo.APIKey{
		ID: "20260902120002", Name: "empty", Permissions: []string{},
		ExpiresAt: created.AddDate(0, 0, 7), CreatedAt: created,
	}
	if err := keys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, _ := auth.Sign(k, testSecret)
	rc := serveAuth(m, consts.MethodGet, "/sess/history", func(rc *app.RequestContext) {
		rc.Request.Header.Set("X-API-Key", token)
	})
	if rc.Response.StatusCode() != 403 {
		t.Fatalf("empty permissions should 403, got %d", rc.Response.StatusCode())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/middleware/ -run TestAuth -v`
Expected: 编译失败（NewAuthMiddleware 签名不匹配 / 字段不存在）

- [ ] **Step 3: 重写 auth.go**

`internal/api/middleware/auth.go` 整体替换为（`getRequiredPermission` 与 `GetCaller` 原样保留）：

```go
package middleware

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/websession"
	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// AuthMiddleware 提供 API Key（JWT）/ Web Cookie 认证，始终开启。
type AuthMiddleware struct {
	config   config.SecurityConfig
	webStore *websession.Store
	apiKeys  repo.APIKeyRepo
}

// NewAuthMiddleware creates a new auth middleware.
// webStore 为 Web 登录会话存储；传 nil 表示不启用 Cookie 凭证。
func NewAuthMiddleware(cfg config.SecurityConfig, webStore *websession.Store, apiKeys repo.APIKeyRepo) *AuthMiddleware {
	return &AuthMiddleware{config: cfg, webStore: webStore, apiKeys: apiKeys}
}

// Serve returns a Hertz middleware handler
func (m *AuthMiddleware) Serve() app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		// Web 会话 Cookie 凭证：有效则等同 all 权限放行（Validate 顺带滑动续期）
		if m.webStore != nil {
			if token := string(rc.Cookie(websession.CookieName)); token != "" {
				if userID, ok := m.webStore.Validate(token); ok {
					rc.Set("caller", "web")
					rc.Set("web_user_id", userID)
					rc.Next(ctx)
					return
				}
			}
		}

		headerName := m.config.Auth.HeaderName
		if headerName == "" {
			headerName = "X-API-Key"
		}
		tokenStr := string(rc.GetHeader(headerName))
		if tokenStr == "" {
			writeUnauthorized(rc)
			return
		}

		// 1) 验签 + 过期检查；2) 以 jti 反查数据库确认未被删除（删除即吊销）。
		// 三类失败统一 401 不区分原因，避免向攻击者泄露信息。
		jti, err := auth.Verify(tokenStr, m.config.Auth.Secret)
		if err != nil {
			writeUnauthorized(rc)
			return
		}
		key, err := m.apiKeys.GetByID(ctx, jti)
		if err != nil {
			writeUnauthorized(rc)
			return
		}

		// 权限检查以数据库行为准
		path := string(rc.URI().Path())
		method := string(rc.Method())
		requiredPerm := getRequiredPermission(path, method)
		if requiredPerm != "" && !hasPermission(key.Permissions, requiredPerm) {
			rc.SetContentType("application/json")
			rc.SetStatusCode(403)
			rc.Write([]byte(fmt.Sprintf(`{"status":"forbidden","message":"权限不足: 需要 %s 权限"}`, requiredPerm)))
			rc.Abort()
			return
		}

		rc.Set("caller", key.Name)
		rc.Next(ctx)
	}
}

func writeUnauthorized(rc *app.RequestContext) {
	rc.SetContentType("application/json")
	rc.SetStatusCode(401)
	rc.Write([]byte(`{"status":"unauthorized","message":"API Key 无效或缺失"}`))
	rc.Abort()
}

// hasPermission 判断权限集合是否满足所需权限点。
// 空集合一律拒绝：创建流程保证至少一个权限点，空集只可能来自脏数据。
func hasPermission(perms []string, required string) bool {
	for _, perm := range perms {
		perm = strings.TrimSpace(perm)
		if perm == "all" || perm == required {
			return true
		}
	}
	return false
}
```

（保留文件中原有的 `getRequiredPermission` 与 `GetCaller` 两个函数；删除原方法版 `hasPermission`。）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/middleware/... -v`
Expected: 全部 PASS（含既有 websession/ratelimit 测试）

---

### Task 7: 管理端点 handler、路由与装配

**Files:**
- Create: `internal/api/handler/apikeys.go`
- Create: `internal/api/handler/apikeys_test.go`
- Modify: `internal/api/router.go`（RegisterRoutes 增加参数与 4 条路由）
- Modify: `internal/api/server.go`（NewServer 增加参数、构造 handler、传递装配）
- Modify: `cmd/groot/main.go`（startServer 调用 EnsureAuthSecret；NewServer 调用传 repos.APIKey）

- [ ] **Step 1: 编写 handler 失败测试**

创建 `internal/api/handler/apikeys_test.go`：

```go
package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/apikeydb"
)

const testAuthSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newAPIKeysHandler(t *testing.T) (*APIKeysHandler, repo.APIKeyRepo) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	keys := apikeydb.New(sqlxDB, dialect)
	cfg := config.SecurityConfig{Auth: config.AuthConfig{Secret: testAuthSecret}}
	return NewAPIKeysHandler(keys, cfg, logger.New(config.LoggingConfig{Output: []string{}})), keys
}

func postJSON(h func(context.Context, *app.RequestContext), body string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.SetRequestURI("/web/apikeys")
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	rc.Request.SetBody([]byte(body))
	h(context.Background(), rc)
	return rc
}

// TestAPIKeys_CreateAndVerify 创建成功返回可验证的 token，且库存元数据能还原出同一 token。
func TestAPIKeys_CreateAndVerify(t *testing.T) {
	h, keys := newAPIKeysHandler(t)
	rc := postJSON(h.Create, `{"name":"svc-a","expires_in":"7d","permissions":["chat","status"]}`)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("Create status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if len(resp.ID) != 14 {
		t.Errorf("id should be yyyyMMddHHmmss, got %q", resp.ID)
	}
	jti, err := auth.Verify(resp.Token, testAuthSecret)
	if err != nil || jti != resp.ID {
		t.Errorf("token not verifiable: %v, jti=%q", err, jti)
	}
	// 还原恒等性：库存元数据 + secret 重签 == 创建时返回的 token
	stored, err := keys.GetByID(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	restored, err := auth.Sign(stored, testAuthSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if restored != resp.Token {
		t.Errorf("restored token differs:\n%s\n%s", restored, resp.Token)
	}
}

// TestAPIKeys_CreateValidation 非法参数各返回 400。
func TestAPIKeys_CreateValidation(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	cases := []string{
		`{"name":"","expires_in":"7d","permissions":["chat"]}`,       // 空名称
		`{"name":"a","expires_in":"3d","permissions":["chat"]}`,      // 非法 expires_in
		`{"name":"a","expires_in":"7d","permissions":[]}`,            // 空权限
		`{"name":"a","expires_in":"7d","permissions":["superuser"]}`, // 非法权限点
	}
	for _, body := range cases {
		if rc := postJSON(h.Create, body); rc.Response.StatusCode() != 400 {
			t.Errorf("body %s: status=%d, want 400", body, rc.Response.StatusCode())
		}
	}
}

// TestAPIKeys_CreateDuplicateName 重名返回 409。
func TestAPIKeys_CreateDuplicateName(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	postJSON(h.Create, `{"name":"svc-a","expires_in":"7d","permissions":["chat"]}`)
	rc := postJSON(h.Create, `{"name":"svc-a","expires_in":"1d","permissions":["all"]}`)
	if rc.Response.StatusCode() != 409 {
		t.Fatalf("duplicate name status=%d, want 409", rc.Response.StatusCode())
	}
}

// TestAPIKeys_CreateSameSecondRetry 同秒创建两把 Key，主键 +1 秒重试后都成功。
func TestAPIKeys_CreateSameSecondRetry(t *testing.T) {
	h, keys := newAPIKeysHandler(t)
	fixed := time.Date(2026, 9, 2, 12, 0, 0, 500e6, time.Local)
	origNow := apiKeyNow
	apiKeyNow = func() time.Time { return fixed }
	t.Cleanup(func() { apiKeyNow = origNow })

	if rc := postJSON(h.Create, `{"name":"a","expires_in":"7d","permissions":["chat"]}`); rc.Response.StatusCode() != 200 {
		t.Fatalf("first create: %d", rc.Response.StatusCode())
	}
	if rc := postJSON(h.Create, `{"name":"b","expires_in":"7d","permissions":["chat"]}`); rc.Response.StatusCode() != 200 {
		t.Fatalf("second create same second: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	list, err := keys.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}
	if list[0].ID == list[1].ID {
		t.Error("ids should differ after retry")
	}
}

// TestAPIKeys_ListAndExpired 列表包含过期状态计算。
func TestAPIKeys_ListAndExpired(t *testing.T) {
	h, keys := newAPIKeysHandler(t)
	created := time.Now().AddDate(0, 0, -8).Truncate(time.Second)
	expired := &repo.APIKey{
		ID: "20260825120000", Name: "old", Permissions: []string{"chat"},
		ExpiresAt: created.AddDate(0, 0, 7), CreatedAt: created,
	}
	if err := keys.Create(context.Background(), expired); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/web/apikeys")
	h.List(context.Background(), rc)
	var resp struct {
		Keys []struct {
			Name    string `json:"name"`
			Expired bool   `json:"expired"`
		} `json:"keys"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("bad response: %v body=%s", err, rc.Response.Body())
	}
	if resp.Total != 1 || !resp.Keys[0].Expired {
		t.Errorf("expected 1 expired key, got %+v", resp)
	}
}

// TestAPIKeys_TokenAndDelete 查看 token 与删除端点。
func TestAPIKeys_TokenAndDelete(t *testing.T) {
	h, _ := newAPIKeysHandler(t)
	rcCreate := postJSON(h.Create, `{"name":"svc-a","expires_in":"7d","permissions":["chat"]}`)
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rcCreate.Response.Body(), &created); err != nil {
		t.Fatalf("bad create response: %v", err)
	}

	// GET /web/apikeys/:id/token 还原出同一 token
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/web/apikeys/" + created.ID + "/token")
	rc.Params.Set("id", created.ID)
	h.Token(context.Background(), rc)
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &tokenResp); err != nil {
		t.Fatalf("bad token response: %v body=%s", err, rc.Response.Body())
	}
	if tokenResp.Token != created.Token {
		t.Error("restored token should equal original")
	}

	// DELETE 后再查 token 返回 404
	rcDel := app.NewContext(0)
	rcDel.Request.Header.SetMethod(consts.MethodDelete)
	rcDel.Params.Set("id", created.ID)
	h.Delete(context.Background(), rcDel)
	if rcDel.Response.StatusCode() != 200 {
		t.Fatalf("Delete: %d", rcDel.Response.StatusCode())
	}
	rcGone := app.NewContext(0)
	rcGone.Params.Set("id", created.ID)
	h.Token(context.Background(), rcGone)
	if rcGone.Response.StatusCode() != 404 {
		t.Errorf("token after delete = %d, want 404", rcGone.Response.StatusCode())
	}
}
```

（注意：`rc.Params.Set` 若 Hertz 版本无此方法，用 `rc.Params = param.Params{{Key: "id", Value: created.ID}}`，import `"github.com/cloudwego/hertz/pkg/route/param"`——以 `session.go` 等既有 handler 测试对 `:id` 参数的处理方式为准。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handler/ -run TestAPIKeys -v`
Expected: 编译失败 `undefined: NewAPIKeysHandler`

- [ ] **Step 3: 实现 handler**

创建 `internal/api/handler/apikeys.go`：

```go
package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/auth"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// APIKeysHandler API Key 管理（/web/apikeys 系列端点，WebSession 保护）
type APIKeysHandler struct {
	keys   repo.APIKeyRepo
	secret string
	log    *logger.Logger
}

func NewAPIKeysHandler(keys repo.APIKeyRepo, cfg config.SecurityConfig, log *logger.Logger) *APIKeysHandler {
	return &APIKeysHandler{keys: keys, secret: cfg.Auth.Secret, log: log}
}

// apiKeyNow 可注入时钟，便于测试主键同秒冲突重试
var apiKeyNow = time.Now

// expiresInOptions 过期时间枚举 → 距创建时刻的日历偏移
var expiresInOptions = map[string]func(t time.Time) time.Time{
	"1d":  func(t time.Time) time.Time { return t.AddDate(0, 0, 1) },
	"7d":  func(t time.Time) time.Time { return t.AddDate(0, 0, 7) },
	"1mo": func(t time.Time) time.Time { return t.AddDate(0, 1, 0) },
	"6mo": func(t time.Time) time.Time { return t.AddDate(0, 6, 0) },
	"1y":  func(t time.Time) time.Time { return t.AddDate(1, 0, 0) },
	"10y": func(t time.Time) time.Time { return t.AddDate(10, 0, 0) },
}

type apiKeyCreateRequest struct {
	Name        string   `json:"name"`
	ExpiresIn   string   `json:"expires_in"`
	Permissions []string `json:"permissions"`
}

type apiKeyInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	ExpiresAt   int64    `json:"expires_at"`
	CreatedAt   int64    `json:"created_at"`
	Expired     bool     `json:"expired"`
}

func toAPIKeyInfo(k *repo.APIKey, now time.Time) apiKeyInfo {
	return apiKeyInfo{
		ID:          k.ID,
		Name:        k.Name,
		Permissions: k.Permissions,
		ExpiresAt:   k.ExpiresAt.UnixMilli(),
		CreatedAt:   k.CreatedAt.UnixMilli(),
		Expired:     now.After(k.ExpiresAt),
	}
}

// validatePermissions 校验权限点非空且都在合法全集内。
func validatePermissions(perms []string) bool {
	if len(perms) == 0 {
		return false
	}
	valid := make(map[string]bool, len(repo.ValidPermissions))
	for _, p := range repo.ValidPermissions {
		valid[p] = true
	}
	for _, p := range perms {
		if !valid[p] {
			return false
		}
	}
	return true
}

// List 处理 GET /web/apikeys
func (h *APIKeysHandler) List(ctx context.Context, rc *app.RequestContext) {
	list, err := h.keys.List(ctx)
	if err != nil {
		h.log.Error("API Key 列表查询失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	now := apiKeyNow()
	infos := make([]apiKeyInfo, 0, len(list))
	for _, k := range list {
		infos = append(infos, toAPIKeyInfo(k, now))
	}
	rc.JSON(200, utils.H{"keys": infos, "total": len(infos)})
}

// Create 处理 POST /web/apikeys：校验 → 名称查重 → 写入（主键同秒冲突 +1 秒重试）→ 签发 token
func (h *APIKeysHandler) Create(ctx context.Context, rc *app.RequestContext) {
	var req apiKeyCreateRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "名称不能为空"})
		return
	}
	expFn, ok := expiresInOptions[req.ExpiresIn]
	if !ok {
		rc.JSON(400, utils.H{"status": "invalid_expires_in", "message": "过期时间只支持 1d/7d/1mo/6mo/1y/10y"})
		return
	}
	if !validatePermissions(req.Permissions) {
		rc.JSON(400, utils.H{"status": "invalid_permissions", "message": "权限范围为空或包含非法权限点"})
		return
	}

	if _, err := h.keys.GetByName(ctx, req.Name); err == nil {
		rc.JSON(409, utils.H{"status": "name_exists", "message": "名称已存在"})
		return
	} else if !errors.Is(err, repo.ErrNotFound) {
		h.log.Error("API Key 名称查重失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}

	// 主键为秒级时间戳（yyyyMMddHHmmss）：同秒创建冲突时 +1 秒重试，最多 3 次
	now := apiKeyNow()
	var k *repo.APIKey
	var createErr error
	for i := 0; i < 3; i++ {
		created := now.Add(time.Duration(i) * time.Second).Truncate(time.Second)
		k = &repo.APIKey{
			ID:          created.Format("20060102150405"),
			Name:        req.Name,
			Permissions: req.Permissions,
			ExpiresAt:   expFn(created),
			CreatedAt:   created,
		}
		if createErr = h.keys.Create(ctx, k); createErr == nil {
			break
		}
	}
	if createErr != nil {
		h.log.Error("API Key 创建失败: " + createErr.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}

	token, err := auth.Sign(k, h.secret)
	if err != nil {
		h.log.Error("API Key 签发失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	info := toAPIKeyInfo(k, apiKeyNow())
	rc.JSON(200, utils.H{
		"id": info.ID, "name": info.Name, "permissions": info.Permissions,
		"expires_at": info.ExpiresAt, "created_at": info.CreatedAt, "expired": info.Expired,
		"token": token,
	})
}

// Token 处理 GET /web/apikeys/:id/token：按需用 secret + 元数据确定性还原完整 JWT
func (h *APIKeysHandler) Token(ctx context.Context, rc *app.RequestContext) {
	k, err := h.keys.GetByID(ctx, rc.Param("id"))
	if errors.Is(err, repo.ErrNotFound) {
		rc.JSON(404, utils.H{"status": "not_found", "message": "API Key 不存在"})
		return
	}
	if err != nil {
		h.log.Error("API Key 查询失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	token, err := auth.Sign(k, h.secret)
	if err != nil {
		h.log.Error("API Key 还原失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	rc.JSON(200, utils.H{"token": token})
}

// Delete 处理 DELETE /web/apikeys/:id：删除即吊销
func (h *APIKeysHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	err := h.keys.DeleteByID(ctx, rc.Param("id"))
	if errors.Is(err, repo.ErrNotFound) {
		rc.JSON(404, utils.H{"status": "not_found", "message": "API Key 不存在"})
		return
	}
	if err != nil {
		h.log.Error("API Key 删除失败: " + err.Error())
		rc.JSON(500, utils.H{"status": "internal_error", "message": "内部错误"})
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}
```

- [ ] **Step 4: 运行 handler 测试**

Run: `go test ./internal/api/handler/ -run TestAPIKeys -v`
Expected: 全部 PASS

- [ ] **Step 5: 路由与装配**

1. `internal/api/router.go`：`RegisterRoutes` 参数列表在 `webAuthH *handler.WebAuthHandler,` 后追加 `apiKeysH *handler.APIKeysHandler,`；webGroup 中 models 路由之后追加：

```go
	webGroup.GET("/apikeys", apiKeysH.List)
	webGroup.POST("/apikeys", apiKeysH.Create)
	webGroup.GET("/apikeys/:id/token", apiKeysH.Token)
	webGroup.DELETE("/apikeys/:id", apiKeysH.Delete)
```

2. `internal/api/server.go`：
   - `NewServer` 参数在 `models *llm.ModelService,` 后追加 `apiKeys repo.APIKeyRepo,`
   - `authMW := middleware.NewAuthMiddleware(cfg.Security, webStore)` 改为 `authMW := middleware.NewAuthMiddleware(cfg.Security, webStore, apiKeys)`
   - handlers 区追加 `apiKeysH := handler.NewAPIKeysHandler(apiKeys, cfg.Security, log)`
   - `RegisterRoutes(...)` 调用末尾追加 `apiKeysH`

3. `cmd/groot/main.go`：
   - `startServer` 中 `config.Load` 成功、端口覆盖之后插入：

```go
	// 认证始终开启：secret 缺失（老版本升级）时自动生成并回写 config.yaml
	if err := config.EnsureAuthSecret(homeDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "初始化认证密钥失败: %s\n", err)
		os.Exit(1)
	}
```

   - `main.go:464` 的 `api.NewServer(...)` 调用在 `modelService` 后追加 `repos.APIKey`

- [ ] **Step 6: 全量编译与单元测试**

Run: `go build -o bin/groot ./cmd && go test ./internal/... -v 2>&1 | grep -E "^(=== RUN|--- FAIL|FAIL|ok)" | tail -40`
Expected: 编译通过、无 FAIL。若 `internal/api/handler` 其他既有测试（如 `webauth_test.go`）因 NewServer/中间件签名变化编译失败，按新签名修正调用处。

---

### Task 8: Web 前端 API Keys 管理页

**Files:**
- Modify: `web/src/api/types.ts`（追加类型）
- Create: `web/src/components/settings/ApiKeysPanel.vue`
- Modify: `web/src/components/settings/SettingsModal.vue`（菜单项 + 面板挂载）
- Modify: `web/src/i18n/messages/zh-cn.ts`、`web/src/i18n/messages/en.ts`

- [ ] **Step 1: 追加 API 类型**

`web/src/api/types.ts` 末尾追加：

```ts
// API Key 管理（/web/apikeys）
export interface ApiKeyInfo {
  id: string
  name: string
  permissions: string[]
  expires_at: number
  created_at: number
  expired: boolean
}

export interface ApiKeysResp {
  keys: ApiKeyInfo[]
  total: number
}

export interface ApiKeyCreateResp extends ApiKeyInfo {
  token: string
}
```

- [ ] **Step 2: 创建 ApiKeysPanel 组件**

创建 `web/src/components/settings/ApiKeysPanel.vue`：

```vue
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api } from '../../api/client'
import type { ApiKeyInfo, ApiKeysResp, ApiKeyCreateResp } from '../../api/types'

const { t } = useI18n()

const keys = ref<ApiKeyInfo[]>([])
const loading = ref(false)

// 创建对话框
const showCreate = ref(false)
const creating = ref(false)
const form = ref({ name: '', expires_in: '1y', permissions: ['all'] as string[] })

const expiresOptions = ['1d', '7d', '1mo', '6mo', '1y', '10y'] as const
const permissionOptions = ['chat', 'status', 'detail', 'history', 'session', 'schedule', 'all'] as const

// 查看 Key 对话框（创建成功与点击"查看"共用）
const showToken = ref(false)
const tokenValue = ref('')
const tokenKeyName = ref('')

function fmtTime(ms: number): string {
  return new Date(ms).toLocaleString()
}

async function load() {
  loading.value = true
  try {
    const resp = await api.get<ApiKeysResp>('/web/apikeys')
    keys.value = resp.keys || []
  } catch (e) {
    ElNotification.error({ title: t('apikeys.title'), message: e instanceof Error ? e.message : String(e) })
  } finally {
    loading.value = false
  }
}

function openCreate() {
  form.value = { name: '', expires_in: '1y', permissions: ['all'] }
  showCreate.value = true
}

async function handleCreate() {
  if (!form.value.name.trim()) {
    ElNotification.warning({ title: t('apikeys.title'), message: t('apikeys.nameRequired') })
    return
  }
  if (!form.value.permissions.length) {
    ElNotification.warning({ title: t('apikeys.title'), message: t('apikeys.permissionsRequired') })
    return
  }
  creating.value = true
  try {
    const resp = await api.post<ApiKeyCreateResp>('/web/apikeys', {
      name: form.value.name.trim(),
      expires_in: form.value.expires_in,
      permissions: form.value.permissions,
    })
    showCreate.value = false
    tokenKeyName.value = resp.name
    tokenValue.value = resp.token
    showToken.value = true
    await load()
  } catch (e) {
    ElNotification.error({ title: t('apikeys.title'), message: e instanceof Error ? e.message : String(e) })
  } finally {
    creating.value = false
  }
}

async function viewToken(k: ApiKeyInfo) {
  try {
    const resp = await api.get<{ token: string }>(`/web/apikeys/${k.id}/token`)
    tokenKeyName.value = k.name
    tokenValue.value = resp.token
    showToken.value = true
  } catch (e) {
    ElNotification.error({ title: t('apikeys.title'), message: e instanceof Error ? e.message : String(e) })
  }
}

async function copyToken() {
  try {
    await navigator.clipboard.writeText(tokenValue.value)
    ElNotification.success({ title: t('apikeys.title'), message: t('apikeys.copied') })
  } catch {
    ElNotification.error({ title: t('apikeys.title'), message: t('apikeys.copyFailed') })
  }
}

async function handleDelete(k: ApiKeyInfo) {
  try {
    await ElMessageBox.confirm(t('apikeys.deleteConfirm', { name: k.name }), t('apikeys.deleteTitle'), {
      type: 'warning',
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
    })
  } catch {
    return // 用户取消
  }
  try {
    await api.delete(`/web/apikeys/${k.id}`)
    ElNotification.success({ title: t('apikeys.title'), message: t('apikeys.deleted') })
    await load()
  } catch (e) {
    ElNotification.error({ title: t('apikeys.title'), message: e instanceof Error ? e.message : String(e) })
  }
}

onMounted(load)
</script>

<template>
  <div class="apikeys-panel">
    <div class="panel-header">
      <div class="label-desc">{{ t('apikeys.desc') }}</div>
      <el-button type="primary" size="small" @click="openCreate">{{ t('apikeys.create') }}</el-button>
    </div>

    <el-table :data="keys" v-loading="loading" size="small" style="width: 100%">
      <el-table-column prop="name" :label="t('apikeys.name')" min-width="110" />
      <el-table-column :label="t('apikeys.permissions')" min-width="160">
        <template #default="{ row }">
          <el-tag v-for="p in row.permissions" :key="p" size="small" effect="light" class="perm-tag">
            {{ p }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column :label="t('apikeys.createdAt')" min-width="140">
        <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column :label="t('apikeys.expiresAt')" min-width="140">
        <template #default="{ row }">{{ fmtTime(row.expires_at) }}</template>
      </el-table-column>
      <el-table-column :label="t('apikeys.status')" width="90">
        <template #default="{ row }">
          <el-tag :type="row.expired ? 'danger' : 'success'" size="small" effect="light">
            {{ row.expired ? t('apikeys.expired') : t('apikeys.valid') }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column :label="t('apikeys.actions')" width="140" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="viewToken(row)">{{ t('apikeys.view') }}</el-button>
          <el-button link type="danger" size="small" @click="handleDelete(row)">{{ t('common.delete') }}</el-button>
        </template>
      </el-table-column>
      <template #empty>
        <el-empty :description="t('apikeys.empty')" :image-size="60" />
      </template>
    </el-table>

    <!-- 创建对话框 -->
    <el-dialog v-model="showCreate" :title="t('apikeys.createTitle')" width="420px" append-to-body>
      <el-form label-position="top">
        <el-form-item :label="t('apikeys.name')">
          <el-input v-model="form.name" :placeholder="t('apikeys.namePlaceholder')" maxlength="64" />
        </el-form-item>
        <el-form-item :label="t('apikeys.expiresIn')">
          <el-select v-model="form.expires_in" style="width: 100%">
            <el-option v-for="o in expiresOptions" :key="o" :value="o" :label="t(`apikeys.expires_${o}`)" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('apikeys.permissions')">
          <el-select v-model="form.permissions" multiple style="width: 100%">
            <el-option v-for="p in permissionOptions" :key="p" :value="p" :label="p" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showCreate = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="creating" @click="handleCreate">{{ t('apikeys.create') }}</el-button>
      </template>
    </el-dialog>

    <!-- 查看/复制 Key 对话框 -->
    <el-dialog v-model="showToken" :title="t('apikeys.tokenTitle', { name: tokenKeyName })" width="520px" append-to-body>
      <div class="label-desc">{{ t('apikeys.tokenDesc') }}</div>
      <el-input v-model="tokenValue" type="textarea" :rows="4" readonly class="token-box" />
      <template #footer>
        <el-button @click="showToken = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" @click="copyToken">{{ t('apikeys.copy') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.label-desc {
  font-size: 0.82em;
  opacity: 0.6;
}

.perm-tag {
  margin-right: 4px;
  margin-bottom: 2px;
}

.token-box {
  margin-top: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
</style>
```

- [ ] **Step 3: 挂载到设置弹窗**

`web/src/components/settings/SettingsModal.vue`：

1. import 区追加 `import ApiKeysPanel from './ApiKeysPanel.vue'`
2. `menuOptions` 中 `menuModels` 之后插入 `{ label: t('settings.menuApiKeys'), key: 'apikeys' },`
3. 模板中"模型"面板块之后插入：

```html
        <!-- API Keys -->
        <div v-else-if="section === 'apikeys'">
          <ApiKeysPanel />
        </div>
```

- [ ] **Step 4: 补充 i18n 文案**

`web/src/i18n/messages/zh-cn.ts` 的 `settings` 节内追加 `menuApiKeys: 'API Keys',`；顶层追加：

```ts
  apikeys: {
    title: 'API Keys',
    desc: 'API Key 用于程序化调用对外 API，随请求头发送。删除后立即失效。',
    create: '创建',
    createTitle: '创建 API Key',
    name: '名称',
    namePlaceholder: '如 my-service',
    nameRequired: '请输入名称',
    permissions: '权限范围',
    permissionsRequired: '请至少选择一个权限',
    expiresIn: '过期时间',
    expires_1d: '1 天',
    expires_7d: '7 天',
    expires_1mo: '1 个月',
    expires_6mo: '半年',
    expires_1y: '1 年',
    expires_10y: '10 年',
    createdAt: '创建时间',
    expiresAt: '过期时间',
    status: '状态',
    valid: '有效',
    expired: '已过期',
    actions: '操作',
    view: '查看',
    empty: '暂无 API Key',
    tokenTitle: 'API Key：{name}',
    tokenDesc: '请妥善保管。也可以之后随时在此查看和复制。',
    copy: '复制',
    copied: '已复制到剪贴板',
    copyFailed: '复制失败，请手动选择复制',
    deleteTitle: '删除 API Key',
    deleteConfirm: '确定删除 "{name}" 吗？删除后该 Key 立即失效。',
    deleted: '已删除',
  },
```

`web/src/i18n/messages/en.ts` 对应追加（key 一一对应）：

```ts
  apikeys: {
    title: 'API Keys',
    desc: 'API Keys authenticate programmatic access to the public API via request header. Deleting a key revokes it immediately.',
    create: 'Create',
    createTitle: 'Create API Key',
    name: 'Name',
    namePlaceholder: 'e.g. my-service',
    nameRequired: 'Name is required',
    permissions: 'Permissions',
    permissionsRequired: 'Select at least one permission',
    expiresIn: 'Expires in',
    expires_1d: '1 day',
    expires_7d: '7 days',
    expires_1mo: '1 month',
    expires_6mo: '6 months',
    expires_1y: '1 year',
    expires_10y: '10 years',
    createdAt: 'Created',
    expiresAt: 'Expires',
    status: 'Status',
    valid: 'Valid',
    expired: 'Expired',
    actions: 'Actions',
    view: 'View',
    empty: 'No API keys yet',
    tokenTitle: 'API Key: {name}',
    tokenDesc: 'Keep it safe. You can view and copy it here anytime.',
    copy: 'Copy',
    copied: 'Copied to clipboard',
    copyFailed: 'Copy failed, please select and copy manually',
    deleteTitle: 'Delete API Key',
    deleteConfirm: 'Delete "{name}"? The key stops working immediately.',
    deleted: 'Deleted',
  },
```

（`settings` 节内 en.ts 也要加 `menuApiKeys: 'API Keys',`。）

- [ ] **Step 5: 构建前端**

Run: `cd web && npm run build`
Expected: vite 构建成功，`web/dist/` 更新，无 TypeScript 错误

---

### Task 9: README 更新与全量验证

**Files:**
- Modify: `README.md`（认证相关章节；`/web/apikeys` 是 Web 自用端点，按惯例不列入对外 API 文档）

- [ ] **Step 1: 更新 README 认证章节**

按以下要点修改（先用 grep 定位对应行）：

1. **配置示例**（约 425 行附近）：`auth` 节替换为：

```yaml
  auth:
    header_name: X-API-Key       # API Key 请求头名称
    secret: "..."                # JWT 签名密钥（init 自动生成，请勿泄露）
```

2. **配置项说明表**（约 556 行附近）：删除 `auth.enabled`、`auth.type`、`auth.api_key.header_name` 三行，替换为：

```markdown
| `auth.header_name` | 否 | API Key 请求头名称，默认 `X-API-Key` |
| `auth.secret` | 否 | JWT 签名密钥；`groot init` 自动生成，为空时启动自动补齐。更换后所有 API Key 立即失效 |
```

3. **认证说明**（约 570-577 行附近的引用块）：
   - 删除"匿名降级"中"认证关闭（`auth.enabled: false`）时按客户端 IP 限流"的表述，改为"API 认证始终开启，按 API Key 名称限流"
   - 补充一段 API Key 获取与使用方式：

```markdown
> - **API 认证始终开启**：对外 API 请求需在请求头（默认 `X-API-Key`）中携带 API Key
> - **API Key 管理**：登录 Web 界面，进入 **设置 → API Keys** 创建；每个 Key 可设置名称、过期时间（1天/7天/1个月/半年/1年/10年）与权限范围，创建后可随时查看、复制，删除后立即失效
> - **权限点**：`chat`、`status`、`detail`、`history`、`session`、`schedule`、`all`
```

4. 全文搜索 `GROOT_API_KEY` 与旧版 `keys:` 配置示例，一并清除或改写。

- [ ] **Step 2: 全量单元测试**

Run: `go test ./internal/... 2>&1 | grep -v "^ok" | head -20`
Expected: 无 FAIL 输出

- [ ] **Step 3: 编译产物验证**

Run: `go build -o bin/groot ./cmd && ls -la bin/groot`
Expected: 编译成功，产物在 bin/ 目录

- [ ] **Step 4: 冒烟验证（可选，需本地环境）**

```bash
rm -rf /tmp/groot-smoke && bin/groot init --home /tmp/groot-smoke 2>/dev/null || bin/groot init
grep -A3 "^security:" /tmp/groot-smoke/config.yaml
```

Expected: config.yaml 中有 `security.auth.secret`（64 位 hex）。若 init 不支持 `--home` 参数，跳过此步，由用户在系统测试时验证。

**系统测试**（Python，用户自行运行）：端到端"创建 Key → 带 Key 调用 /chat → 删除 Key → 调用被拒"流程属于 `tests/python/` 系统测试范畴，由用户决定何时补充与运行。

---

## Self-Review 记录

- **Spec 覆盖**：配置结构（Task 4/5）、JWT 设计（Task 3）、数据库（Task 2）、中间件六步流程（Task 6）、四个管理端点与前端（Task 7/8）、错误处理策略（401 统一/403、Task 6/7）、README（Task 9）——设计文档 1.1–1.9 全部有对应任务。
- **确定性还原**：Task 7 的 `TestAPIKeys_CreateAndVerify` 直接断言"库存元数据重签 == 创建时返回的 token"，这是本设计的核心不变量。
- **类型一致性**：`repo.APIKey`（Task 2）在 Task 3/6/7 中签名一致；`NewAuthMiddleware` 三参数签名在 Task 6 定义、Task 7 装配处一致；`expires_in` 枚举 `1d/7d/1mo/6mo/1y/10y` 在 handler、前端、i18n、README 中一致。
- **旧 Key 废弃**：无迁移代码（设计决策）；旧随机串 Key 在 `TestVerify_Garbage` / `TestAuth_InvalidToken` 中确认被拒。
