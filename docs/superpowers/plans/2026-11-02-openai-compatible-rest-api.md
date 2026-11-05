# OpenAI 兼容 REST API 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `Authorization: Bearer` + OpenAI 官方 SDK 只改 `base_url` 与 `api_key` 就能调用 Groot 的对话、模型列表、转录、语音合成、图片解析接口，并把全部接口的错误体统一为 OpenAI 错误信封。

**Architecture:** 对话接口从 `POST /chat` 迁到 `POST /chat/completions`，请求体按 OpenAI Chat Completions 规范解析，服务端再把 messages 翻译成 `agent.Task`（末条 user 的 content parts → `MultiModalContents`，更早的 user/assistant → `CallerHistory`，system → `Task.Prompt`）。执行路径只有一条：`Executor.Execute` 改为接收 `agent.EventWriter` 接口，`SSEWriter` 把它写成 SSE chunk，新增的 `Aggregator` 把它聚合成一次性响应，handler 按 `stream` 选择。

**Tech Stack:** Go 1.2x + CloudWeGo Hertz + eino/adk；Vue 3 + TypeScript（`web/`）；测试用 Go 标准测试框架。

**Spec:** `docs/superpowers/specs/2026-11-02-openai-compatible-rest-api-design.md`

## Global Constraints

- 所有改动直接在 `master` 工作区进行，**禁止创建分支或 worktree**。
- **不自动提交**。每个任务末尾的「快照」步骤用悬空对象记录工作区，不触碰 HEAD、索引和分支：
  ```bash
  export GIT_INDEX_FILE=.git/sdd-index
  git read-tree HEAD && git add -A && T=$(git write-tree)
  git commit-tree "$T" -p HEAD -m "task<N>: <标题>"
  unset GIT_INDEX_FILE; rm -f .git/sdd-index
  ```
- 单元测试只用 Go 标准测试框架，测试文件与源码同目录（`internal/xxx/xxx_test.go`）；系统测试只用 pytest，不得新增测试工具，且由用户自行运行。
- `examples/` 下不得出现测试文件；测试一律放 `tests/`。
- README（根目录）是用户手册，只写对外公开 API；Web 自用端点不写（`/web/health` 例外）。
- 代表同一含义的标识符在 DB 列、Go 字段、JSON key 中统一命名（如 `tenant_id`/`TenantID`）。
- 回复用中文；Go 代码经 `gofmt`；编译产物只放 `dist/`（`go build -o dist/groot ./cmd/groot`）。
- 路径**不加** `/v1` 前缀：`/chat/completions`、`/models`、`/audio/*`、`/image/understanding`。
- 错误信封统一为 `{"error":{"message":..,"type":..,"param":..,"code":..}}`，构造函数只在 `internal/api/errbody` 定义；`param`/`code` 无值时输出 JSON `null`。

## Review Focus

1. **SSE 头已下发后的失败**：写到一半才出错时，响应必须是 HTTP 200 + `data: {"error":{...}}` + `data: [DONE]`，不得改写状态码或漏掉 `[DONE]`（Task 10 的 `TestChatHandler_StreamErrorAfterHeader` 固定）。
2. **`session_id` 不存在或不属于调用方**：静默新建会话并正常执行，不得返回 404（Task 10 的 `TestChatHandler_SessionNotOwnedCreatesNew` 固定）。
3. **`stream_options.include_usage` 未开启**：SSE 中不得出现 usage chunk，其余 chunk 的 `usage` 为 `null`；`stream=false` 时该参数被忽略而不是报错（Task 6、Task 9 固定）。
4. **`Authorization` 缺失、无 `Bearer ` 前缀、或前缀后为空**：一律 401 信封，不得落入 Cookie 分支、不得 500（Task 3 的 `TestAuthMiddleware_BearerForms` 固定）。
5. **末条 user 只有图片没有文本**：`instruction` 为空串仍必须执行（不补默认指令、不报 400），任务标题取兜底文案（Task 9 的 `TestParseChatRequest_ImageOnlyContent` 固定）。

---

## File Structure

**新建**
- `internal/api/errbody/errbody.go` — OpenAI 错误信封的结构、类型/码常量、`Write`/`Envelope.Bytes`
- `internal/api/errbody/errbody_test.go`
- `internal/api/handler/chat_req.go` — `/chat/completions` 请求体解码与校验，产出 `agent.Task` 片段
- `internal/api/handler/chat_req_test.go`
- `internal/api/handler/models_public.go` — `GET /models`
- `internal/api/handler/models_public_test.go`
- `internal/agent/aggregator.go` — 事件写出接口的非流式实现
- `internal/agent/aggregator_test.go`
- `internal/agent/event_writer.go` — `EventWriter` 接口与 `nopEventWriter`

**重写**
- `internal/api/handler/chat.go` — 编排：校验 → 会话 → 执行 → 流式/非流式
- `internal/api/handler/chat_test.go`
- `internal/api/middleware/auth.go`、`internal/api/middleware/auth_test.go`

**修改**
- `internal/api/middleware/identity.go`、`ratelimit.go`、`websession.go`
- `internal/api/router.go`、`internal/api/server.go`
- `internal/api/types/types.go`
- `internal/api/handler/owner.go`、`transcription.go`、`speech.go`、`image.go`、`auth_setting.go`、`setting.go`
- `internal/agent/sse.go`、`executor.go`、`call_agent.go`
- `internal/llm/transcription.go`、`internal/llm/speech.go`
- `internal/setting/auth.go`、`defaults.go`、`migrate_legacy.go`、`internal/config/config.go`
- `web/src/api/*`、`web/src/components/chat/ChatInput.vue`、`web/src/components/settings/SettingsModal.vue`
- `README.md`、`examples/python/*`、`examples/java/*`

---

### Task 1: 错误信封包 `internal/api/errbody`

**Files:**
- Create: `internal/api/errbody/errbody.go`
- Test: `internal/api/errbody/errbody_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
```go
package errbody

// Envelope 是 OpenAI 错误响应的完整响应体。
type Envelope struct {
	Error Error `json:"error"`
}

// Error 是信封内的 error 对象；Param 与 Code 无值时序列化为 null。
type Error struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

// 信封 type 取值
const (
	TypeInvalidRequest = "invalid_request_error"
	TypeAuthentication = "authentication_error"
	TypePermission     = "permission_error"
	TypeRateLimit      = "rate_limit_error"
	TypeAPI            = "api_error"
)

// 信封 code 取值
const (
	CodeInvalidRequest          = "invalid_request"
	CodeModelNotFound           = "model_not_found"
	CodeUnsupportedFileType     = "unsupported_file_type"
	CodeUnsupportedType         = "unsupported_type"
	CodeInvalidAPIKey           = "invalid_api_key"
	CodeInsufficientPermissions = "insufficient_permissions"
	CodeTenantNotBound          = "tenant_not_bound"
	CodeSessionNotFound         = "session_not_found"
	CodeTaskNotFound            = "task_not_found"
	CodeScheduleNotFound        = "schedule_not_found"
	CodeChatLimitExceeded       = "chat_limit_exceeded"
	CodeChatStillRunning        = "chat_still_running"
	CodeFileTooLarge            = "file_too_large"
	CodeRateLimitExceeded       = "rate_limit_exceeded"
	CodeClientClosedRequest     = "client_closed_request"
	CodeInternalError           = "internal_error"
	CodeUpstreamError           = "upstream_error"
	CodeScheduleUnavailable     = "schedule_unavailable"
	CodeUnknownAgent            = "unknown_agent"
)

// New 构造信封；param 与 code 传空串表示 JSON null。
func New(message, typ, param, code string) Envelope

// Bytes 序列化信封，供 SSE 的 data 行复用。
func (e Envelope) Bytes() []byte

// Write 以 status 写回信封并 Abort 后续 handler。Content-Type 固定 application/json。
func Write(rc *app.RequestContext, status int, message, typ, param, code string)
```
- 信封中的 `type` 与 `code` 是两个独立维度：`type` 只有上表 5 个取值；`code` 可为空（如 500 `internal_error` 之外的部分场景）。
- `Write` 内部不得使用 `fmt.Sprintf` 拼 JSON。

- [ ] **Step 1: 写失败测试**

```go
func TestNew_NullParamAndCode(t *testing.T) {
	b := New("模型不存在", TypeInvalidRequest, "", "")
	got := string(b.Bytes())
	want := `{"error":{"message":"模型不存在","type":"invalid_request_error","param":null,"code":null}}`
	if got != want { t.Fatalf("got %s", got) }
}

func TestNew_WithParamAndCode(t *testing.T) {
	b := New("缺少 user", TypeInvalidRequest, "user", CodeInvalidRequest)
	got := string(b.Bytes())
	if !strings.Contains(got, `"param":"user"`) || !strings.Contains(got, `"code":"invalid_request"`) {
		t.Fatalf("got %s", got)
	}
}

func TestWrite(t *testing.T) {
	rc := app.NewContext(0)
	Write(rc, 401, "API Key 无效或缺失", TypeAuthentication, "", CodeInvalidAPIKey)
	if rc.Response.StatusCode() != 401 { t.Fatalf("status %d", rc.Response.StatusCode()) }
	if !strings.Contains(string(rc.Response.Body()), `"type":"authentication_error"`) { t.Fatal("body") }
}
```
（`Write` 断言 `Abort` 后 `rc.IsAborted()` 为 true。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/errbody/... -v`
Expected: FAIL — `undefined: New`

- [ ] **Step 3: 实现 `errbody.go`**

`New` 对空串的 param/code 置 `nil`；`Bytes` 用 `json.Marshal` 且忽略不可能出现的错误；`Write` 设 `Content-Type: application/json`、状态码，`rc.Write` 后 `rc.Abort()`。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api/errbody/... -v`
Expected: PASS

- [ ] **Step 5: 快照**

```bash
export GIT_INDEX_FILE=.git/sdd-index
git read-tree HEAD && git add -A && T=$(git write-tree)
git commit-tree "$T" -p HEAD -m "task1: 新增 errbody 错误信封包"
unset GIT_INDEX_FILE; rm -f .git/sdd-index
```

---

### Task 2: `user` 请求头常量

**Files:**
- Modify: `internal/api/middleware/identity.go:5`
- Test: `internal/api/middleware/identity_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `middleware.UserIDHeader = "user"`；`ValidIdentifier(s string) (string, bool)` 与 `GetTenant`/`GetUserID`/`IsWebCaller` 签名不变。

- [ ] **Step 1: 写失败测试**

```go
func TestUserIDHeader(t *testing.T) {
	if UserIDHeader != "user" { t.Fatalf("got %q", UserIDHeader) }
}
func TestValidIdentifier(t *testing.T) {
	ok := []string{"abc", "a_b-c", "A1", strings.Repeat("a", 64)}
	bad := []string{"", strings.Repeat("a", 65), "a b", "中文", "a.b"}
	// 逐个断言 ValidIdentifier 的第二返回值为 true / false
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/middleware/ -run 'TestUserIDHeader|TestValidIdentifier' -v`
Expected: FAIL — `UserIDHeader` 为 `"X-User-ID"`

- [ ] **Step 3: 改常量**

把 `const UserIDHeader = "X-User-ID"` 改为 `"user"`。同一提交内检索全项目 `X-User-ID` 残留，改为 `"user"`（`internal/api/handler/owner.go` 的提示文案在 Task 15 处理，此处只改常量引用处）。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api/middleware/ -v`
Expected: PASS

- [ ] **Step 5: 快照**（同 Task 1，`-m "task2: user 请求头常量"`）

---

### Task 3: 认证中间件改为 Bearer + 权限表 + 信封

**Files:**
- Modify: `internal/api/middleware/auth.go`
- Test: `internal/api/middleware/auth_test.go`

**Interfaces:**
- Consumes: `errbody.Write`、`errbody.New`（Task 1）；`UserIDHeader`（Task 2）
- Produces: `AuthMiddleware` 构造签名不变；`getRequiredPermission(path, method string) string` 新增两支：`/chat/completions` POST → `"chat"`、`/models` GET → `"chat"`；删除 `m.config.Auth.HeaderName` 的读取（`config.AuthConfig.HeaderName` 字段在 Task 5 删除，本任务先不再读它）。

**行为要点**
- Cookie 分支保持不变（有效 → `caller="web"`、`web_user_id`，按 `all` 放行）。
- Bearer 解析：取 `Authorization` 头；必须以 `"Bearer "` 开头（大小写不敏感比较前缀，token 本身原样保留），去前缀并 `TrimSpace` 后非空，否则 401。
- 顺序：验签 → `jti` 反查 API Key → 权限点 → 注入 `caller`/`tenant_id`/`user_id`。
- 401：`TypeAuthentication` + `CodeInvalidAPIKey`，message `API Key 无效或缺失`。
- 403：`TypePermission` + `CodeInsufficientPermissions`，message `权限不足: 需要 <perm> 权限`。
- `user_id` 取自请求头 `"user"`，非法或缺失归一为空串（不在此拒绝）。

- [ ] **Step 1: 写失败测试**

```go
func TestAuthMiddleware_BearerForms(t *testing.T) {
	// 表驱动：header 值 → 期望状态码
	cases := []struct{ auth string; want int }{
		{"", 401},
		{"Bearer", 401},
		{"Bearer   ", 401},
		{"bearer " + validToken, 200},
		{"Bearer " + validToken, 200},
		{"Token " + validToken, 401},
		{"X-API-Key: " + validToken, 401}, // 旧头不再被读取
	}
}
func TestAuthMiddleware_PermissionDeniedEnvelope(t *testing.T) {
	// Key 权限只有 status，请求 POST /chat/completions → 403
	// 断言 body 含 `"type":"permission_error"` 与 `"code":"insufficient_permissions"`
}
func TestAuthMiddleware_PermissionsForNewRoutes(t *testing.T) {
	// 持有 chat 的 Key：POST /chat/completions → 放行；GET /models → 放行
	// 只持有 session 的 Key：POST /chat/completions → 403
}
```
测试沿用现有 `auth_test.go` 的 fake `repo.APIKeyRepo` 与 `auth.Sign` 辅助函数。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/middleware/ -run TestAuthMiddleware -v`
Expected: FAIL — 旧头 `X-API-Key` 仍被接受、新路由返回 403

- [ ] **Step 3: 实现**

删掉 `headerName := m.config.Auth.HeaderName ...` 五行的取头逻辑，改为：

```go
func bearerToken(rc *app.RequestContext) string  // 返回去掉 "Bearer " 前缀的 token；不合法返回 ""
```

`getRequiredPermission` 在 `path == "/chat"` 分支旁增加 `path == "/chat/completions" && method == "POST"` → `"chat"`；在 `/image/understanding` 分支后增加 `path == "/models" && method == "GET"` → `"chat"`。
401/403 改用 `errbody.Write`。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api/middleware/ -v`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task3: 认证中间件改 Bearer 与统一错误信封"`）

---

### Task 4: 限流与 Web 会话中间件对齐新路径与信封

**Files:**
- Modify: `internal/api/middleware/ratelimit.go`、`internal/api/middleware/websession.go`
- Test: `internal/api/middleware/auth_test.go`（限流部分若无测试文件则在 `ratelimit_test.go` 新增）

**Interfaces:**
- Consumes: `errbody.Write`（Task 1）
- Produces: 构造签名不变。

**行为要点**
- `ratelimit.Serve()` 的长连接（并发）分支判定从 `path == "/chat" && method == "POST"` 改为 `path == "/chat/completions" && method == "POST"`。
- 429 响应改为 `errbody.Write(rc, 429, "请求过于频繁，请稍后重试", errbody.TypeRateLimit, "", errbody.CodeRateLimitExceeded)`，删除硬编码 JSON 与无参 `fmt.Sprintf`。
- `websession.go` 的 `abortUnauthorized` 改为 `errbody.Write(rc, 401, "未登录或会话已过期", errbody.TypeAuthentication, "", errbody.CodeInvalidAPIKey)`。

- [ ] **Step 1: 写失败测试**

```go
func TestRateLimitMiddleware_ChatCompletionsUsesConcurrencyBranch(t *testing.T) {
	// 并发上限 1：第一个 POST /chat/completions 放行且不 Release，第二个立即 429
}
func TestRateLimitMiddleware_Envelope(t *testing.T) {
	// 断言 429 body 为 {"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"...}}
}
func TestWebSession_Envelope(t *testing.T) {
	// 无 Cookie → 401，body 含 `"error":{`
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/middleware/ -run 'TestRateLimitMiddleware|TestWebSession' -v`
Expected: FAIL

- [ ] **Step 3: 实现**（仅替换路径判定与错误写出）

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api/middleware/ -v`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task4: 限流与会话中间件对齐"`）

---

### Task 5: 删除「认证请求头名」配置项全链路

**Files:**
- Modify: `internal/config/config.go`（`AuthConfig` 删 `HeaderName`）、`internal/setting/auth.go`、`internal/setting/defaults.go`、`internal/setting/migrate_legacy.go:91`、`internal/api/types/types.go:390-403`、`internal/api/handler/auth_setting.go`、`internal/api/router.go:83-84`
- Test: `internal/setting/auth_test.go`、`internal/setting/defaults_test.go`、`internal/api/handler/auth_setting_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
```go
// types.AuthSettingsPayload 只剩两个字段
type AuthSettingsPayload struct {
	SecretMasked string `json:"secret_masked"`
	SecretSet    bool   `json:"secret_set"`
}
// types.PutAuthSettingsRequest 与 handler.PutAuthSettings 一并删除
```
`setting.Settings.RegenerateAuthSecret(ctx) (string, error)` 与 `EnsureAuthSecret` 签名不变；删除 `KeyAuthHeaderName`、`SetAuthHeaderName`、`defaultAuthHeaderName()`、`DefaultAuthHeaderName`、`authHeaderNamePattern`。

- [ ] **Step 1: 改测试到新形态并确认失败**

```go
// auth_test.go：删除 TestSetAuthHeaderName 系列，保留 Secret 相关
// defaults_test.go：删掉对 DefaultAuthHeaderName 的断言
// auth_setting_test.go：GET /web/settings/auth 与 POST /web/settings/auth/secret 的用例只测 secret 读取/重新生成，
//   断言响应体恰为 {"secret_masked":"...","secret_set":true}（无 header_name 字段）
```
Run: `go test ./internal/setting/... ./internal/api/handler/... -run 'Auth' -v`
Expected: FAIL — 响应仍含 `header_name`

- [ ] **Step 2: 删代码**

- `config.AuthConfig` 只留 `Secret string`（`yaml:"secret"`）。
- `setting/auth.go`：`Auth()` 只读 `KeyAuthSecret`；删除 `KeyAuthHeaderName`、`SetAuthHeaderName`、`authHeaderNamePattern`。
- `setting/defaults.go`：删除 `DefaultAuthHeaderName` 与 `defaultAuthHeaderName()`。
- `setting/migrate_legacy.go`：删除 `addStr(KeyAuthHeaderName, lb.Auth.HeaderName)`。
- `types.go`：`AuthSettingsPayload` 缩为两字段，删除 `PutAuthSettingsRequest`。
- `handler/auth_setting.go`：删 `PutAuthSettings`；`GetAuthSettings` 与 `RegenerateAuthSecret` 保留，`respondAuthSettings` 只返回两字段。
- `router.go`：删除 `webGroup.PUT("/settings/auth", ...)`；保留 `webGroup.GET("/settings/auth", ...)` 与 `POST /settings/auth/secret`。

- [ ] **Step 3: 全项目检索残留**

Run: `grep -rn "HeaderName\|header_name\|X-API-Key" --include="*.go" --include="*.yaml" --include="*.md" .`
Expected: 仅剩 README 与示例中的旧说明（Task 17/18 处理），Go 代码与配置样例零命中。

- [ ] **Step 4: 运行测试与构建**

Run: `go test ./internal/setting/... ./internal/api/... -v && go build ./...`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task5: 移除认证请求头名配置"`）

---

### Task 6: SSEWriter 对齐 OpenAI chunk 规范

**Files:**
- Modify: `internal/agent/sse.go`
- Test: `internal/agent/sse_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
```go
// 构造签名新增 includeUsage：仅当为 true 时下发 usage chunk
func NewSSEWriter(w flushWriter, sessionID, chatID string, round int, model string, created int64, includeUsage bool) *SSEWriter

type chatChunk struct {
	ID        string        `json:"id"`
	Object    string        `json:"object"`
	Created   int64         `json:"created"`
	Model     string        `json:"model"`
	SessionID string        `json:"session_id"`
	Choices   []chunkChoice `json:"choices"`
	Usage     *chunkUsage   `json:"usage"`
}
```
- `WriteError` 的 delta 负载改为 `{"message":..., "type":"api_error", "param":null, "code":null}`（用 `errbody.New(message, errbody.TypeAPI, "", "")` 后取其 `Bytes()` 作为 data，即整行是 `data: {"error":{...}}`）。
- `WriteUsage`：`includeUsage` 为 false 时直接返回 nil，不写任何 chunk。

- [ ] **Step 1: 写失败测试**

```go
func TestSSEWriter_ChunkHasTopLevelSessionID(t *testing.T)  // 任一 chunk 含 `"session_id":"s1"`
func TestSSEWriter_UsageOnlyWhenIncludeUsage(t *testing.T)  // false → 输出中无 `"usage":{`；true → 恰有一个且 choices 为 []
func TestSSEWriter_ErrorUsesAPIErrorAndEnvelope(t *testing.T) // data 行含 `"error":{"message":"boom","type":"api_error"`
```
现有 `sse_test.go` 的构造调用全部补第 7 个参数 `false`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run TestSSEWriter -v`
Expected: FAIL — 缺少 `session_id` 字段 / usage 无条件输出

- [ ] **Step 3: 实现**（`chatChunk` 加字段；`writeChunk` 填 `SessionID`；`WriteUsage` 加条件；`WriteError` 换负载）

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/agent/ -run TestSSEWriter -v`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task6: SSE chunk 对齐 OpenAI 规范"`）

---

### Task 7: `EventWriter` 接口与 `Aggregator`

**Files:**
- Create: `internal/agent/event_writer.go`、`internal/agent/aggregator.go`
- Test: `internal/agent/aggregator_test.go`

**Interfaces:**
- Consumes: `TokenUsage`（`internal/agent/token_accumulators.go`）、`ToolCall`（`sse.go`）
- Produces:
```go
// EventWriter 是对话执行期间的事件写出接口，SSEWriter 与 Aggregator 都实现它。
type EventWriter interface {
	SetModel(model string)
	SetAgentModel(agentName, model string)
	WriteThinking(agentName, content string) error
	WriteMessage(agentName, content string) error
	WriteToolCalls(agentName string, toolCalls []ToolCall) error
	WriteFinish(agentName, reason string) error
	WriteToolResult(agentName, toolCallID, toolName, content string, isError bool) error
	WriteError(agentName, message string) error
	WriteUsage(usage TokenUsage) error
	WriteDone() error
}

// 编译期确认
var _ EventWriter = (*SSEWriter)(nil)
var _ EventWriter = (*Aggregator)(nil)

// Result 是非流式聚合结果，由 handler 组装成 chat.completion。
type Result struct {
	Content          string
	ReasoningContent string
	FinishReason     string
	Usage            TokenUsage
	Error            string
}

func NewAggregator() *Aggregator
func (a *Aggregator) Result() Result

// nopEventWriter 供不需要事件输出的执行路径（定时任务、Web 静默调用）使用。
type nopEventWriter struct{}
func NopEventWriter() EventWriter
```
- `Aggregator` 的聚合规则：`WriteMessage` 追加到 `Content`（主 + 子）；`WriteThinking` 追加到 `ReasoningContent`；`WriteFinish` 在 `reason` 非空时覆盖 `FinishReason`；`WriteUsage` 记录 `Usage`；`WriteError` 记录 `Error`；`WriteToolCalls`/`WriteToolResult`/`SetModel`/`SetAgentModel`/`WriteDone` 空实现返回 nil。

- [ ] **Step 1: 写失败测试**

```go
func TestAggregator_ConcatenatesMainAndSubAgent(t *testing.T)  // "" + "a" + "sub" + "b" → "asubb"
func TestAggregator_ReasoningAndFinish(t *testing.T)          // 无 thinking → ReasoningContent == ""；最后一个非空 finish 生效
func TestAggregator_Error(t *testing.T)                       // WriteError → Result().Error 非空
func TestNopEventWriter_NoPanic(t *testing.T)                 // 全部方法返回 nil
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run 'TestAggregator|TestNopEventWriter' -v`
Expected: FAIL — `undefined: NewAggregator`

- [ ] **Step 3: 实现两个文件**

`Aggregator` 用 `strings.Builder` 累积；`nopEventWriter` 各方法 `return nil`。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/agent/ -run 'TestAggregator|TestNopEventWriter' -v`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task7: 新增 EventWriter 接口与 Aggregator"`）

---

### Task 8: Executor 改接 EventWriter、Task 增加调用方历史

**Files:**
- Modify: `internal/agent/executor.go:126-380`、`internal/agent/call_agent.go:58,75,93,169`、`internal/schedule/runner.go:16-19,82,152`、`internal/schedule/notify.go:84`、`internal/schedule/notify_test.go:108`
- Test: `internal/agent/executor_test.go`、`internal/agent/engine_test.go`

**Interfaces:**
- Consumes: `EventWriter`、`NopEventWriter`（Task 7）
- Produces:
```go
// Task 新增一个字段（放在 HistoryMessages 之后）
CallerHistory []memory.Message // 调用方在 messages 中提供的历史，接在服务端上下文之后

// Execute 第四参数改为接口；nil 视为 NopEventWriter
func (e *Executor) Execute(parentCtx context.Context, sessionID string, task *Task, events EventWriter)

// CallAgentToolConfig.SSE 字段类型改为 EventWriter（注释保留「可为 nil」）
```
- `Execute` 开头 `if events == nil { events = NopEventWriter() }`，其后全部调用点（`:179`、`:227-228`、`:302-362`）把 `sse.` 换成 `events.`。
- `engine.Run` 的 `historyMessages` 实参改为 `append(append([]memory.Message{}, task.HistoryMessages...), task.CallerHistory...)`。
- `task.Prompt` 保持原样传给 `engine.Run` 的第 3 个参数（system 提示）。

- [ ] **Step 1: 写失败测试**

```go
func TestExecutor_NilEventWriterDoesNotPanic(t *testing.T)  // Execute(ctx, "s1", &Task{...}, nil) 正常返回
func TestEngine_MessageOrderServerThenCallerThenUser(t *testing.T) {
	// buildMessageList("最后提问", []memory.Message{{Instruction:"旧问题", Result:"旧回答"}}, nil)
	// 与 callerHistory 合并后：user(旧问题) → assistant(旧回答) → user(调用方问题) → assistant(调用方回答) → user(最后提问)
}
```
`executor_test.go` / `engine_test.go` 中所有 `*SSEWriter` 实参改用真实 `NewSSEWriter(..., false)` 或 `NopEventWriter()`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -v`
Expected: FAIL — 签名不匹配

- [ ] **Step 3: 实现**

`internal/schedule/runner.go` 的 `taskExecutor` 接口签名同步为 `Execute(ctx, sessionID, task, events agent.EventWriter)`；`runner.go:82,152` 与 `notify.go:84` 传 `nil`。`notify_test.go` 的 fake 同步。

- [ ] **Step 4: 运行测试与构建**

Run: `go test ./internal/agent/... ./internal/schedule/... -v && go build ./...`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task8: Executor 改接 EventWriter 并支持调用方历史"`）

---

### Task 9: `/chat/completions` 请求解析 `chat_req.go`

**Files:**
- Create: `internal/api/handler/chat_req.go`
- Test: `internal/api/handler/chat_req_test.go`

**Interfaces:**
- Consumes: `agent.Task`、`agent.MultimodalContent`、`memory.Message`
- Produces:
```go
// chatRequest 是 POST /chat/completions 的请求体。采样参数接受但忽略。
type chatRequest struct {
	Model             string            `json:"model"`
	Messages          []chatMessage     `json:"messages"`
	Stream            bool              `json:"stream"`
	StreamOptions     *streamOptions    `json:"stream_options"`
	SessionID         string            `json:"session_id"`
	AgentName         string            `json:"agent_name"`
	User              string            `json:"user"`
	N                 *int              `json:"n"`
	Temperature       *float64          `json:"temperature"`
	TopP              *float64          `json:"top_p"`
	MaxTokens         *int              `json:"max_tokens"`
	MaxCompletionTokens *int            `json:"max_completion_tokens"`
	Stop              json.RawMessage   `json:"stop"`
	PresencePenalty   *float64          `json:"presence_penalty"`
	FrequencyPenalty  *float64          `json:"frequency_penalty"`
	Seed              *int              `json:"seed"`
	ResponseFormat    json.RawMessage   `json:"response_format"`
	Tools             json.RawMessage   `json:"tools"`
	ToolChoice        json.RawMessage   `json:"tool_choice"`
	Logprobs          *bool             `json:"logprobs"`
	TopLogprobs       *int              `json:"top_logprobs"`
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // 字符串或内容片段数组
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// parsedRequest 是解析结果，供 handler 组装 agent.Task。
type parsedRequest struct {
	Instruction    string                   // 末条 user 的文本（可能为空串）
	Attachments    []agent.MultimodalContent
	SystemPrompt   string                   // 全部 system 消息按出现顺序空行连接
	CallerHistory  []memory.Message         // 除 system 与末条 user 之外的全部消息
	Title          string                   // 会话标题素材：Instruction 前若干字符，为空时取 "新会话"
	Model          string
	SessionID      string
	AgentName      string
	User           string
	Stream         bool
	IncludeUsage   bool
}

// parseChatRequest 按 spec §1.4.7 的 1~8 步校验请求体。
// 返回的错误已是 errbody 信封所需的 status/message/type/code。
type requestError struct {
	Status  int
	Message string
	Type    string
	Param   string
	Code    string
}
func parseChatRequest(body []byte) (*parsedRequest, *requestError)
```
- 校验顺序（与 spec 一致）：JSON 解不出 → 400 `invalid_request`；`messages` 为空 → 400；`role` 不在 `system|user|assistant` → 400（`tool`/`function`/`developer` 同样 400）；末条 `role` 不是 `user` → 400；`n` 存在且不为 1 → 400；逐条解析 content parts；附件校验由 handler 用 `attachment.NewHandler` 做（本函数只产出 `Attachments`）。
- content parts → `MultimodalContent`：`text` 累加到文本；`image_url` 的 data URL → `Type:"image"`，合成名按 MIME（`image/png→image.png`、`jpeg→image.jpg`、`webp`、`gif`、`bmp`，兜底 `image.png`）；`input_audio` 仅 `wav`/`mp3` → `Type:"audio"`，名 `audio.wav`/`audio.mp3`；`file` 仅文本类 MIME（`text/*`、`application/json`、`application/xml`、`*+json`、`*+xml`），名取 `filename`，缺失按 MIME 推导，兜底 `file.txt`；`video_url` → 400 `invalid_request`；未知 `type` → 400 `invalid_request`；base64 解不出 → 400 `invalid_request`（细分原因只出现在 message 里）。
- 仅 `user` 允许 content 为数组；`system`/`assistant` 必须是字符串。
- `stream=false` 时 `stream_options.include_usage` 忽略（不报错）。

- [ ] **Step 1: 写失败测试**

```go
func TestParseChatRequest_InvalidMessages(t *testing.T)        // 空数组 / 末条 assistant → code invalid_request
func TestParseChatRequest_UnsupportedRole(t *testing.T)        // role=tool → 400，param "messages"
func TestParseChatRequest_NNotOne(t *testing.T)                // n=2 → 400
func TestParseChatRequest_StringContent(t *testing.T)          // 纯文本 → Instruction 正确、Attachments 为空
func TestParseChatRequest_ImageOnlyContent(t *testing.T) {
	// 末条 user 只有 image_url 片段 → Instruction == ""、len(Attachments)==1、Attachments[0].Name=="image.png"
	// （Review Focus 5）
}
func TestParseChatRequest_VideoRejected(t *testing.T)          // video_url → 400 invalid_request
func TestParseChatRequest_FileTextOrRejected(t *testing.T)     // text/plain 通过；application/pdf → unsupported_file_type
func TestParseChatRequest_BadBase64(t *testing.T)              // data URL 非法 base64 → code invalid_request
func TestParseChatRequest_IncludeUsageIgnoredWhenNotStream(t *testing.T) // stream=false + include_usage → IncludeUsage == false，无错误（Review Focus 3）
func TestParseChatRequest_SystemAndHistory(t *testing.T)        // 两条 system 空行连接；中间 user/assistant 进 CallerHistory
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestParseChatRequest -v`
Expected: FAIL — `undefined: parseChatRequest`

- [ ] **Step 3: 实现 `chat_req.go`**

data URL 解析复用 `strings.Cut` 拆 `data:<mime>;base64,`；`input_audio.format` 只认 `wav`/`mp3`；`CallerHistory` 按 `memory.Message{Instruction / Result}` 交替填充（user → `Instruction`，assistant → `Result`）。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api/handler/ -run TestParseChatRequest -v`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task9: chat/completions 请求解析"`）

---

### Task 10: 重写 `handler/chat.go` 与路由

**Files:**
- Modify: `internal/api/handler/chat.go`（重写）、`internal/api/router.go:113`、`internal/api/server.go`（若构造参数变化）
- Test: `internal/api/handler/chat_test.go`（重写）

**Interfaces:**
- Consumes: `parseChatRequest`（Task 9）、`errbody`（Task 1）、`agent.NewAggregator`/`agent.NopEventWriter`（Task 7）、`agent.NewSSEWriter(..., includeUsage)`（Task 6）、`middleware.UserIDHeader`（Task 2）、`requireOwner`/`ownsOrAdmin`（现有）
- Produces: `NewChatHandler(mem, runtime, executor, mcpMgr, subAgentReg, models, settings, log)` 签名不变（7 参数）；路由 `apiGroup.POST("/chat/completions", chatH.Serve)`，删除 `apiGroup.POST("/chat", chatH.Serve)`。

**Handler 流程（顺序不可调整）**
1. `parseChatRequest(rc.Body())`，错误 → `errbody.Write` 后返回。
2. `model`：`h.models.Resolve(ctx, p.Model, repo.DefaultChat)`；`ErrModelNotFound`/`ErrModelDisabled`/`ErrNoDefaultModel` → 400 `model_not_found`，其它 → 500 `internal_error`。
3. `agent_name`：`== agent.MainAgentName` 归一为空；非空且注册表为 nil 或 `Get(name)` 不存在 → 400 `unknown_agent`。
4. `requireOwner(rc)`；失败已写出。`user`：请求体 `p.User` 非空时覆盖上下文值，再 `ValidIdentifier` 校验。
5. 会话：`sessionID := p.SessionID`；非空且 `!ownsOrAdmin` → 置空（静默新建，不报 404）。
6. `runtimeState.IsRunning(sessionID)` 且 `sessionID != ""` → 409 `chat_limit_exceeded`（body 不含 session_id）。
7. `settings.Runtime` / `RuntimeStatic()` 回退。
8. 附件校验：`attachment.NewHandler(cfg).Validate(p.Attachments)`；`*attachment.AttachmentError` → 400 用其 `.Code`/`.Message`（type `invalid_request_error`）。
9. 会话：新建时 `CreateSession(sessionID, tenantID, userID, memory.TruncateTitle(p.Title))`；续接时 `round = GetRoundCount+1` 且 `GetContextMessagesWithTokenLimit(sessionID, rt.Memory.HistoryWindow, mdl.MaxContextTokens)`。
10. `chatID := memory.GenerateChatID()`；`runtimeState.Register` 失败 → 409。此后**每一条失败返回都必须先 `runtimeState.Delete(sessionID)`**。
11. 组装 `agent.Task`：`Instruction: p.Instruction`、`Prompt: p.SystemPrompt`、`CallerHistory: p.CallerHistory`、`HistoryMessages`、`ModelName`、`AgentName`、`MultiModalContents: p.Attachments`、`Caller: middleware.GetCaller(rc)`、`Round`、`StartTime`。
12. **流式**（`p.Stream`）：设 `Content-Type: text/event-stream`、`Cache-Control: no-cache`、`Connection: keep-alive`、`ImmediateHeaderFlush = true`；`io.Pipe` + `SetBodyStream(pr, -1)`；goroutine 内 `defer runtimeState.Delete`、`defer pw.Close()`、recover 时 `events.WriteError("", ...)` + `WriteDone()`、`NewSSEWriter(pipeFlushWriter{pw}, sessionID, chatID, round, mdl.Name, start.Unix(), p.IncludeUsage)`、`h.agentExecutor.Execute(active.Context(), sessionID, task, events)`。**不设置** `X-Session-ID`/`X-Chat-ID` 响应头。
13. **非流式**（`!p.Stream`）：`agg := agent.NewAggregator()`，同步执行 `Execute`（在 goroutine 中跑、主协程等待，保留 `c.Request.Context()` 取消）；结束后组响应体：

```json
{"id":"<chatID>","object":"chat.completion","created":<unix>,"model":"<mdl.Name>","session_id":"<sid>",
 "choices":[{"index":0,"message":{"role":"assistant","content":"...","reasoning_content":"...","error":{"message":"..."}},
             "finish_reason":"stop"}],
 "usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}
```
- `content` 为空且有 `Error` → `content` 保持空串、`finish_reason` 置 `"error"`；`content` 为空且无 `Error` → `"任务执行完成，但未获得明确结果"`。
- `finish_reason`：`agg.Result().FinishReason` 为空时按状态补 `"stop"`；失败路径固定 `"error"`。
- `reasoning_content` 与 `message.error` 为空时省略（`omitempty`）。
- 调用方断开 → 仍返回 200 + `finish_reason:"error"`。

- [ ] **Step 1: 写失败测试**

```go
func TestChatHandler_NonStreamCompletionShape(t *testing.T)   // object/id/choices[0].message.content/usage 齐备，且 chunk 无
func TestChatHandler_StreamChunksHaveSessionID(t *testing.T)   // SSE 每行含 "session_id"
func TestChatHandler_StreamErrorAfterHeader(t *testing.T) {
	// 让 executor 在执行中报错 → 响应仍为 200，含 data: {"error":...} 与 data: [DONE]（Review Focus 1）
}
func TestChatHandler_SessionNotOwnedCreatesNew(t *testing.T)   // session_id 指向他人会话 → 200，且返回的 session_id != 传入值（Review Focus 2）
func TestChatHandler_ImageOnlyMessage(t *testing.T)            // 末条 user 只有图片 → 200，会话已创建（Review Focus 5）
func TestChatHandler_IncludeUsageOff(t *testing.T)             // 未开启时 SSE 中无 usage chunk（Review Focus 3）
func TestChatHandler_BusySession(t *testing.T)                // IsRunning → 409 chat_limit_exceeded
func TestChatHandler_UnknownAgent(t *testing.T)                // agent_name 不存在 → 400 unknown_agent
```
测试用 `httptest` 风格：`app.NewContext` + `rc.Request.SetBody`，executor 用 fake（现有 `chat_test.go` 里的 stub 复用并改签名）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestChatHandler -v`
Expected: FAIL — 旧请求体字段不存在

- [ ] **Step 3: 重写 handler 与路由**

`pipeFlushWriter` 保持（`Flush() error { return nil }`）。路由改 `POST /chat/completions`。

- [ ] **Step 4: 运行测试与构建**

Run: `go test ./internal/api/... -v && go build ./...`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task10: 重写 chat handler 与路由"`）

---

### Task 11: `GET /models`

**Files:**
- Create: `internal/api/handler/models_public.go`
- Modify: `internal/api/server.go`（构造 + 传参）、`internal/api/router.go`（`apiGroup.GET("/models", modelsPublicH.Serve)`）
- Test: `internal/api/handler/models_public_test.go`

**Interfaces:**
- Consumes: `llm.ModelService.List(ctx) ([]*repo.Model, error)`
- Produces:
```go
type ModelsPublicHandler struct { models *llm.ModelService; log *logger.Logger }
func NewModelsPublicHandler(models *llm.ModelService, log *logger.Logger) *ModelsPublicHandler
func (h *ModelsPublicHandler) Serve(ctx context.Context, rc *app.RequestContext)

type modelsListResponse struct {
	Object string      `json:"object"` // 固定 "list"
	Data   []modelCard `json:"data"`
}
type modelCard struct {
	ID      string `json:"id"`      // repo.Model.Name（Groot 模型逻辑名）
	Object  string `json:"object"`  // 固定 "model"
	Created int64  `json:"created"` // repo.Model.CreatedAt.Unix()
	OwnedBy string `json:"owned_by"`// 固定 "groot"
}
```
- 只列 `Enabled == true` 的模型；不暴露 `base_url`/`api_key`/`default_types`；`List` 出错 → 500 `internal_error`。
- `RegisterRoutes` 参数表在 `modelsH` 之后插入 `modelsPublicH *handler.ModelsPublicHandler`。

- [ ] **Step 1: 写失败测试**

```go
func TestModelsPublicHandler_ListsEnabledOnly(t *testing.T)  // 3 个模型，2 启用 → data 长度 2
func TestModelsPublicHandler_CardShape(t *testing.T)         // object=="list"；元素 object=="model"、owned_by=="groot"、created>0
func TestModelsPublicHandler_ListError(t *testing.T)         // models 为 nil 服务 → 500 internal_error
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestModelsPublicHandler -v`
Expected: FAIL — `undefined: NewModelsPublicHandler`

- [ ] **Step 3: 实现 handler 与接线**

`server.go` 中 `modelsPublicH := handler.NewModelsPublicHandler(models, log)`，加到 `RegisterRoutes(...)` 实参末尾前；`router.go` 在 `/image/understanding` 之后注册 `apiGroup.GET("/models", modelsPublicH.Serve)`。

- [ ] **Step 4: 运行测试与构建**

Run: `go test ./internal/api/handler/ -run TestModelsPublicHandler -v && go build ./...`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task11: 新增 GET /models"`）

---

### Task 12: 转录改透传、超大文件 413

**Files:**
- Modify: `internal/llm/transcription.go`、`internal/api/handler/transcription.go`
- Test: `internal/llm/transcription_test.go`、`internal/api/handler/transcription_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
```go
// TranscriptionParams 是转发给上游的除文件外的表单参数。
type TranscriptionParams struct {
	Model             string   // 上游模型名（repo.Model.Model）
	Language          string
	Prompt            string
	ResponseFormat    string   // json/text/srt/verbose_json/vtt；空按 json
	Temperature       string   // 以字符串透传，空不下发
	TimestampGranularities []string // 每项一个 timestamp_granularities[] 字段
}

// UpstreamResponse 是上游原始响应，调用方负责 Close。
type UpstreamResponse struct {
	StatusCode  int
	ContentType string
	Body        io.ReadCloser
}

// Transcribe 转发到上游并原样返回响应，不解析、不改写。
// 网络/读失败返回 ErrUpstream 包装的错误；ctx 取消返回 ctx.Err()。
func Transcribe(ctx context.Context, m *repo.Model, file io.Reader, filename string, p TranscriptionParams) (*UpstreamResponse, error)

// UpstreamBody 读入完整响应体（上限 1 MiB），供 handler 在需要时透传错误体。
```
- 删除 `ErrEmptyTranscript` 的对外使用（常量可保留，供旧调用方；handler 不再使用）。
- 参数名逐字：`file`、`model`、`language`、`prompt`、`response_format`、`temperature`、`timestamp_granularities[]`。
- handler：`max_size` 超限 → **413** `file_too_large`；扩展名白名单外 → 400 `unsupported_type`；`Resolve` 失败 → 400 `model_not_found`；上游 2xx → 原样转发 status + `Content-Type` + body；上游非 2xx 且 body 是 OpenAI 信封（`error` 对象含 `message`）→ 原样转发 status + body；否则 502 `upstream_error`。
- `explicitModelName` 删掉请求头回退，只读表单 `model`。

- [ ] **Step 1: 写失败测试**

```go
func TestTranscribe_ForwardsAllFields(t *testing.T)   // 上游收到 model/language/prompt/response_format/temperature/timestamp_granularities[] 
func TestTranscribe_ReturnsRawBodyAndStatus(t *testing.T) // 上游 200 text/plain "hi" → StatusCode 200、ContentType、Body 原文
func TestTranscriptionHandler_TooLargeReturns413(t *testing.T)
func TestTranscriptionHandler_UpstreamEnvelopePassthrough(t *testing.T) // 上游 400 信封 → 400 且 body 逐字节相同
func TestTranscriptionHandler_UpstreamNonEnvelopeReturns502(t *testing.T)
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/llm/ -run TestTranscribe -v; go test ./internal/api/handler/ -run TestTranscriptionHandler -v`
Expected: FAIL — 签名不匹配 / 413 断言失败

- [ ] **Step 3: 实现**

`multipart` 写入顺序：`file` → `model` → 其余非空字段；`timestamp_granularities[]` 用 `mw.WriteField("timestamp_granularities[]", v)` 逐项写。

- [ ] **Step 4: 运行测试与构建**

Run: `go test ./internal/llm/... ./internal/api/handler/ -v && go build ./...`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task12: 转录改透传"`）

---

### Task 13: 语音合成补 `speed`、`voice` 必填

**Files:**
- Modify: `internal/llm/speech.go`、`internal/api/handler/speech.go`、`internal/api/types/types.go:314-323`
- Test: `internal/llm/speech_test.go`、`internal/api/handler/speech_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
```go
type SpeechRequest struct { // internal/llm
	Input          string
	Voice          string
	Language       string
	Instructions   string
	ResponseFormat string
	Speed          *float64 // nil 不下发
}
// 上游非 2xx：返回 (*UpstreamError, error)，UpstreamError{StatusCode, ContentType, Body []byte}
func Speech(ctx context.Context, m *repo.Model, sr SpeechRequest) (io.ReadCloser, string, *UpstreamError, error)
```
`types.SpeechRequest` 增加 `Speed *float64 \`json:"speed"\``，注释去掉 `X-Model-Name`。
handler：`voice` 必填（trim 空 → 400 `invalid_request`，param `voice`）；`response_format` 非 6 值之一 → 400；`input` 超 4096 → 400；`model` 空 → 默认 TTS；成功**不设置** `X-Model-Name`；上游信封原样转发，否则 502。

- [ ] **Step 1: 写失败测试**

```go
func TestSpeechHandler_VoiceRequired(t *testing.T)          // 缺 voice → 400，param=="voice"
func TestSpeechHandler_SpeedPassthrough(t *testing.T)       // speed=1.5 → 上游 payload 含 "speed":1.5
func TestSpeechHandler_NoModelHeader(t *testing.T)          // 成功响应头无 X-Model-Name
func TestSpeechHandler_UpstreamEnvelopePassthrough(t *testing.T)
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestSpeechHandler -v`
Expected: FAIL

- [ ] **Step 3: 实现**

payload 结构加 `Speed *float64 \`json:"speed,omitempty"\``；handler 把 `req.Speed` 透传；成功分支删除 `rc.Response.Header.Set("X-Model-Name", ...)`。

- [ ] **Step 4: 运行测试与构建**

Run: `go test ./internal/llm/... ./internal/api/handler/ -run 'TestSpeech' -v && go build ./...`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task13: 语音合成补 speed 与 voice 必填"`）

---

### Task 14: 图片解析 `model` 移入请求体

**Files:**
- Modify: `internal/api/handler/image.go`、`internal/api/types/types.go:21-25`
- Test: `internal/api/handler/image_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
```go
type ImageUnderstandingRequest struct {
	Instruction string       `json:"instruction"`
	Model       string       `json:"model"`
	Attachments []Attachment `json:"attachments"`
}
```
- `h.models.Resolve(ctx, strings.TrimSpace(req.Model), repo.DefaultVision)`；`model` 为空取默认视觉模型，指定但不存在/禁用 → 400 `model_not_found`（不得静默回落）。
- 取消路径错误码改为 `client_closed_request`（原 `cancelled`）。
- 其余错误体改 `errbody.Write`。

- [ ] **Step 1: 写失败测试**

```go
func TestImageHandler_ModelFromBody(t *testing.T)      // body.model 生效；请求头 X-Model-Name 被忽略
func TestImageHandler_UnknownModel(t *testing.T)       // body.model="nope" → 400 model_not_found（不回落默认）
func TestImageHandler_ClientClosed(t *testing.T)       // ctx 取消 → 499 client_closed_request
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestImageHandler -v`
Expected: FAIL

- [ ] **Step 3: 实现**

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api/handler/ -run TestImageHandler -v`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task14: 图片解析 model 移入请求体"`）

---

### Task 15: 会话类 handler 的归属校验与错误信封

**Files:**
- Modify: `internal/api/handler/owner.go`、`session.go`、`status.go`、`detail.go`、`stop.go`、`search.go`
- Test: `internal/api/handler/owner_test.go`、`session_test.go`、`status_test.go`、`detail_test.go`、`stop_test.go`、`search_test.go`

**Interfaces:**
- Consumes: `errbody`（Task 1）、`middleware.UserIDHeader`（Task 2）
- Produces:
```go
// requireOwner 语义不变：Web Cookie → (nil, true)
func requireOwner(rc *app.RequestContext) (*repo.SessionOwner, bool)
func ownsOrAdmin(mem *memory.Manager, owner *repo.SessionOwner, sid string) bool
func ownerFields(owner *repo.SessionOwner) (tenantID, userID string)
```
- 缺租户 → 403 `TypePermission` / `tenant_not_bound` / message `API Key 未绑定租户`。
- `user` 缺失或非法 → 400 `TypeInvalidRequest` / `CodeInvalidRequest` / param `user` / message `缺少或非法的 user`。
- `/sess/search` 的 `q` 为空 → 返回空结果且**不校验** `user`。
- 会话/任务/定时任务 404 分别用 `session_not_found`/`task_not_found`/`schedule_not_found`；停用接口的 `chat_still_running`、`chat_not_running` 归入 409 的 `chat_still_running`。

- [ ] **Step 1: 写失败测试**

```go
func TestRequireOwner_MissingTenant(t *testing.T)   // 403 tenant_not_bound
func TestRequireOwner_MissingUser(t *testing.T)     // 400 param=="user"
func TestSearchSessions_EmptyQuerySkipsUserCheck(t *testing.T) // 无 user 头、q="" → 200 空列表
func TestSessionHandler_NotFoundEnvelope(t *testing.T)         // 404 session_not_found
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestRequireOwner|TestSearchSessions|TestSessionHandler' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api/handler/ -v`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task15: 会话类错误信封与 user 头"`）

---

### Task 16: Web 与定时任务 handler 的错误体清剿

**Files:**
- Modify: `internal/api/handler/{agents,skills,tools,models,apikeys,cluster,logs,files,sync,setting,webauth,schedule}.go`、`internal/schedule/*.go`（错误体构造处）
- Test: 对应 `*_test.go`

**Interfaces:**
- Consumes: `errbody`
- Produces: 所有 HTTP **错误**响应体为信封；**成功**响应体（含 `"status":"success"`/`"ok"`）保持原样；`internal/schedule/tools.go:112` 的工具 schema 状态枚举**不改**。

**错误码映射**
| 原 `status` | 新 type | 新 code |
|---|---|---|
| `invalid_request` | `invalid_request_error` | `invalid_request` |
| `unauthorized` | `authentication_error` | `invalid_api_key` |
| `forbidden` | `permission_error` | `insufficient_permissions`（缺租户用 `tenant_not_bound`） |
| `not_found` / `session_not_found` / `chat_not_found` | `invalid_request_error` | `session_not_found` / `task_not_found` |
| `invalid_model` / `model_not_found` | `invalid_request_error` | `model_not_found` |
| `upstream_error` | `api_error` | `upstream_error` |
| `internal_error` / `error`（5xx） | `api_error` | `internal_error` |
| `chat_limit_exceeded` / `chat_still_running` / `chat_not_running` | `invalid_request_error` | `chat_limit_exceeded` / `chat_still_running` |
| `schedule_unavailable` | `api_error` | `schedule_unavailable` |
| `schedule_error`（5xx） | `api_error` | `internal_error` |
| `unknown_agent` | `invalid_request_error` | `unknown_agent` |
| `sync_disabled` | `invalid_request_error` | `invalid_request` |
| `name_exists` | `invalid_request_error` | `invalid_request` |

- [ ] **Step 1: 改测试断言到信封并确认失败**

把所有断言旧体（`data.status == "invalid_request"` 等）的用例改为断言 `data.error.type` / `data.error.code`。
Run: `go test ./internal/api/handler/ ./internal/schedule/ -v`
Expected: FAIL（大范围）

- [ ] **Step 2: 逐文件替换错误写出**

只替换错误分支；命中清单用 `grep -rn '"status":"' --include="*.go" internal/` 逐条确认是成功体还是错误体。

- [ ] **Step 3: 残留核对**

Run: `grep -rn '"status":"\(invalid_request\|unauthorized\|forbidden\|not_found\|internal_error\|error\|upstream_error\)"' --include="*.go" internal/`
Expected: 零命中（成功体与 schema 枚举除外，人工确认）。

- [ ] **Step 4: 运行测试与构建**

Run: `go test ./... && go build ./...`
Expected: PASS

- [ ] **Step 5: 快照**（`-m "task16: 全项目错误体改 OpenAI 信封"`）

---

### Task 17: Web 前端改用 OpenAI 请求形状

**Files:**
- Modify: `web/src/api/client.ts`、`authSettings.ts`、`speech.ts`、`sse.ts`、`sseChunk.ts`、`voice.ts`、`types.ts`、`web/src/components/chat/ChatInput.vue`、`web/src/components/settings/SettingsModal.vue`

**Interfaces:**
- Consumes: 后端 Task 3/10/12/13/14 的接口形状
- Produces: 前端不再引用 `header_name`/`header_name_default`；SSE 解析读取 chunk 顶层 `session_id`（不再读 `X-Session-ID` 响应头）。

- [ ] **Step 1: 改错误解析**

`client.ts` 的 `request()`：失败消息取 `data?.error?.message`，code 取 `data?.error?.code ?? data?.error?.type`；保留 401 跳登录。

- [ ] **Step 2: 改对话请求与 SSE**

- 请求体改为 `{ model, messages, stream, stream_options:{include_usage:true}, session_id?, agent_name?, user? }`，messages 为 `[{role:'user', content:[{type:'text',text}, {type:'image_url',image_url:{url}}]}]`。
- 删除对响应头 `X-Session-ID`/`X-Chat-ID` 的读取；`session_id`/`chat_id` 取自首个 chunk 的 `session_id` 与顶层 `id`。
- `sseChunk.ts` 的类型补 `session_id`、`usage` 可空、`delta.agent_name`。

- [ ] **Step 3: 改语音与认证设置**

- 朗读：`voice` 取「语音设置」，为空时**不发出请求**并提示；请求体加 `speed`（若界面已有对应控件则绑定，否则不传）。
- 转录：`response_format` 固定 `json`，按 `{text}` 解析。
- `authSettings.ts`：`AuthSettings` 只剩 `secret_masked`/`secret_set`；删 `saveAuthHeaderName`；`SettingsModal.vue` 删「认证请求头名」整块与相关 i18n key。

- [ ] **Step 4: 构建验证**

Run: `cd web && npm run build`
Expected: 构建成功，无 TypeScript 错误

- [ ] **Step 5: 检索残留**

Run: `grep -rn "X-API-Key\|X-Session-ID\|X-Chat-ID\|header_name\|X-Model-Name" web/src`
Expected: 零命中

- [ ] **Step 6: 快照**（`-m "task17: Web 前端改用 OpenAI 请求形状"`）

---

### Task 18: README 更新

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: 已完成的后端接口
- Produces: 只写对外公开 API（`/web/*` 自用端点不写，`/web/health` 例外）。

- [ ] **Step 1: 逐节改写**

覆盖 spec §1.12 列出的章节：核心特性、会话与对话、第一次调用（`base_url` + `api_key`）、配置文件示例（移除 `security.auth.header_name`）、配置字段详解、权限说明、接口详述（`POST /chat/completions`、`GET /models`、两个 audio、`/image/understanding`、会话与任务类 `user` 请求头）、附录错误码速查、FAQ（401 排查、429 触发）。

- [ ] **Step 2: 写清楚两个非标准字段的获取方式**

`session_id`（响应体顶层；流式为 chunk 顶层）与 `chat_id`（响应体 `id`/chunk 顶层 `id`），并说明它们是扩展字段。

- [ ] **Step 3: 检索残留**

Run: `grep -n "X-API-Key\|X-Session-ID\|X-Chat-ID\|X-Model-Name\|X-User-ID\|header_name\|POST /chat\b" README.md`
Expected: 零命中

- [ ] **Step 4: 快照**（`-m "task18: README 更新为 OpenAI 兼容接口"`）

---

### Task 19: 示例代码改用 OpenAI 官方 SDK

**Files:**
- Modify: `examples/python/groot_client.py`、`examples/python/requirements.txt`、`examples/java/pom.xml`、`examples/java/src/**`
- Test: 无（示例代码测试放 `tests/examples/`，由用户运行）

**Interfaces:**
- Consumes: 后端接口
- Produces: Python 用 `openai` 包、Java 用 `com.openai:openai-java`，只演示 `base_url`/`api_key` 替换与扩展字段 `session_id`/`agent_name`/`user` 的用法。

- [ ] **Step 1: Python**

`requirements.txt` 改为 `openai>=1.40`；`groot_client.py` 用 `OpenAI(base_url="http://host:8080", api_key=...)` 调 `chat.completions.create`，流式与非流式各一例，并读取 `session_id`。

- [ ] **Step 2: Java**

`pom.xml` 引入 `com.openai:openai-java`；示例调 `client.chat().completions().create(...)`，`baseUrl` 指向 Groot。

- [ ] **Step 3: 编译验证**

Run: `cd examples/java && mvn -q -DskipTests package`
Expected: BUILD SUCCESS
（Python 侧执行 `python3 -c "import ast,sys; ast.parse(open('examples/python/groot_client.py').read())"` 确认语法。）

- [ ] **Step 4: 快照**（`-m "task19: 示例改用 OpenAI 官方 SDK"`）

---

## Self-Review

**1. Spec 覆盖**
- §1.3.1 路由表 → Task 10（`/chat/completions`）、Task 11（`/models`）、Task 12/13/14（audio/image）、Task 15（sess/chat 子路由）、Task 5（删 `/web/settings/auth` 的 PUT）。
- §1.3.2 认证 → Task 3、Task 5。
- §1.3.3 `user` 位置 → Task 2、Task 9、Task 10、Task 15。
- §1.3.4 错误码表 → Task 1（常量）、Task 3/4（中间件）、Task 10/12/13/14/15/16（handler）。
- §1.4.1~§1.4.3 参数与消息组装 → Task 9（解析）、Task 8（顺序）。
- §1.4.4 会话 → Task 10。
- §1.4.5 非流式 → Task 7、Task 10。
- §1.4.6 流式 → Task 6、Task 10。
- §1.4.7 校验顺序 → Task 9（1~8）、Task 10（9~12）。
- §1.5 `/models` → Task 11；§1.6/§1.7 audio → Task 12/13；§1.8 image → Task 14。
- §1.9 会话与任务类 → Task 15；§1.10 Web → Task 17；§1.11 结构 → File Structure 与 Task 1/6/7/9/10/11。
- §1.12 README → Task 18；示例 → Task 19。
- §2.1 删除清单 → Task 5。
无遗漏。

**2. 步骤扫描**：每个 Step 都给出可执行命令、精确签名或断言；未出现「处理边界情况」「补充校验」这类空指令。Task 19 的 Java/Python 示例给出的是 SDK 调用形态而非完整代码，因示例长度由 SDK 决定。

**3. 类型一致性**：`EventWriter`（Task 7）→ `Execute` 第四参（Task 8）→ `NewSSEWriter` 第 7 参（Task 6，Task 10 调用）一致；`parseChatRequest` 返回的 `parsedRequest` 字段名与 Task 10 组装 `Task` 的用法一致；`TranscriptionParams`/`UpstreamResponse`（Task 12）与 handler 用法一致；`Task.CallerHistory`（Task 8）与 Task 9/10 的赋值一致。

**4. Review Focus 归属**：5 条分别由 Task 10、Task 10、Task 6+Task 9、Task 3、Task 9 的测试固定。

**5. 比例**：计划约 700 行，与 748 行 spec 相当；代码块以签名、断言和命令为主，未整段转录实现。
