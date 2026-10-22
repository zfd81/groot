# 对话中止与会话删除 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供 `POST /chat/{sid}/{cid}/stop` 中止正在生成的对话，以及 `DELETE /sess/{sid}` 删除会话，二者遵循租户/用户隔离，并通过集群消息支持跨节点取消。

**Architecture:** `RuntimeState` 为每次对话持有可取消 ctx、cancel 与 done 通道，新增 `Cancel`/`Wait`。`agent.ClusterCanceller` 封装"先本地取消，未命中则广播集群消息 `chat.cancel`"；集群消息服务新增 `chat` 模块处理器在每个节点执行本地取消。两个新 handler 复用现有的 `requireOwner`/`ownsOrAdmin` 前置检查；删除在等待结束后用 `Register` 占位封堵并发续聊。新增权限点 `delete`。

**Tech Stack:** Go、Hertz、sqlx、现有 `internal/cluster` 消息服务（数据库表轮询，心跳 3 秒）。

**Spec:** `docs/superpowers/specs/2026-10-22-chat-stop-and-session-delete-design.md`（依赖 `2026-10-21-session-tenant-isolation-design.md` 已实现）

## Global Constraints

- 归属不符对调用方的表现与"会话不存在"完全相同：404 `{"status":"session_not_found","message":"会话不存在"}`，与 `GET /sess/{sid}` 现有响应体字节一致。
- 前置检查顺序：路径参数 → `requireOwner` → `ownsOrAdmin` → 业务逻辑。Web 管理员（`caller=="web"`）放行且不受归属限制。
- `RuntimeState.Cancel(sid, cid)`：cid 为空串匹配该会话任意活跃对话；非空则必须等于活跃 `ChatID`。Cancel 不删除状态，删除仍由执行 goroutine 的 `defer Delete` 完成。
- `ActiveChat.Status` 读写都在 `mu` 下进行；`Cancel` 成功置 `"cancelled"`。
- 集群消息：模块 `chat`，类型 `chat.cancel`，载荷 `{"session_id":..., "chat_id":...}`，`TargetInstance` 空（广播），`ExpiresAt` 为 now+30s。
- stop 响应：本地命中 200 `success`；本地未命中且广播成功 202 `accepted`；本地未命中且无广播能力或广播失败 409 `chat_not_running`。
- delete：本地活跃 → Cancel + `Wait(sid, 10*time.Second)`，超时 409 `chat_still_running` 且不删数据；本地无活跃且集群模式 → 广播（chat_id 空）后固定 `time.Sleep(5*time.Second)`；然后 `Register(ctx, sid, "delete")` 占位，失败 409 `chat_still_running`；`DeleteSession`；`Delete(sid)` 释放占位。
- 权限：`/chat/{sid}/{cid}/stop` POST → `chat`；`/sess/{sid}` DELETE → `delete`；`repo.ValidPermissions` 增加 `"delete"`（放在 `"session"` 之后、`"schedule"` 之前）。
- `/chat` 处理器 Register 之后、goroutine 启动之前的每条失败返回都必须 `runtimeState.Delete(sessionID)`。
- 所有 Commit 步骤均为"向用户请求提交"，不自动提交（CLAUDE.md）。
- `go build ./...`、`gofmt -l` 对触碰文件为空、`go test ./internal/...` 全绿。

## Review Focus

1. 对已结束对话（cid 曾存在但 goroutine 已退出）调 stop：本地 Cancel false；单机 → 409；集群 → 202 且所有节点空操作。(Task 1、Task 4 覆盖)
2. 删除时另一个 `/chat` 续聊恰在 Wait 返回后到达：占位 Register 先成功则续聊 409；续聊先成功则删除 409，数据不删。(Task 5 覆盖)
3. `/chat` 在 Register 后 CreateSession 失败：runtime 状态被清理，后续 Wait 立即返回 true。(Task 2 覆盖)
4. 集群处理器收到载荷缺字段或类型错误：返回 error 由消息服务记录，不 panic。(Task 3 覆盖)
5. Web 管理员删除归属为空的定时任务会话：200 且记录清空。(Task 5 覆盖)

---

### Task 1: RuntimeState 取消与等待

**Files:**
- Modify: `internal/agent/runtime_state.go:24-32,68-89,113-116`
- Test: `internal/agent/runtime_state_test.go`

**Interfaces:**
- Produces:
  - `ActiveChat` 新增非导出字段 `ctx context.Context`、`cancel context.CancelFunc`、`done chan struct{}`。
  - `(*ActiveChat).Context() context.Context`
  - `(*ActiveChat).StatusSnapshot() string` — 带读锁。
  - `(*RuntimeState).Register(parentCtx context.Context, sessionID, chatID string) (*ActiveChat, error)` — 签名增加 parentCtx；内部 `context.WithCancel(parentCtx)`，`done = make(chan struct{})`。
  - `(*RuntimeState).Cancel(sessionID, chatID string) bool`
  - `(*RuntimeState).Wait(sessionID string, timeout time.Duration) bool`
  - `(*RuntimeState).Delete(sessionID)` 关闭 `done`（幂等：用 `sync.Once` 或先 Load 再 close）。

- [ ] **Step 1: 写失败测试**

在 `runtime_state_test.go` 追加：

```go
func TestRuntimeState_Cancel(t *testing.T) {
	rt := NewRuntimeState()
	chat, _ := rt.Register(context.Background(), "s1", "c1")
	if rt.Cancel("s1", "other") { t.Fatal("wrong cid must not cancel") }
	if rt.Cancel("missing", "c1") { t.Fatal("missing session must not cancel") }
	if !rt.Cancel("s1", "c1") { t.Fatal("matching cid must cancel") }
	select { case <-chat.Context().Done(): default: t.Fatal("ctx not cancelled") }
	if chat.StatusSnapshot() != "cancelled" { t.Errorf("status=%q", chat.StatusSnapshot()) }
	if _, ok := rt.Get("s1"); !ok { t.Fatal("Cancel must not delete state") }
}

func TestRuntimeState_CancelEmptyChatIDMatchesAny(t *testing.T) {
	rt := NewRuntimeState()
	chat, _ := rt.Register(context.Background(), "s1", "c1")
	if !rt.Cancel("s1", "") { t.Fatal("empty cid must match") }
	select { case <-chat.Context().Done(): default: t.Fatal("ctx not cancelled") }
}

func TestRuntimeState_ParentCancelPropagates(t *testing.T) {
	rt := NewRuntimeState()
	parent, cancel := context.WithCancel(context.Background())
	chat, _ := rt.Register(parent, "s1", "c1")
	cancel()
	select { case <-chat.Context().Done(): default: t.Fatal("child ctx must follow parent") }
}

func TestRuntimeState_Wait(t *testing.T) {
	rt := NewRuntimeState()
	if !rt.Wait("none", 10*time.Millisecond) { t.Fatal("no active chat → true immediately") }
	rt.Register(context.Background(), "s1", "c1")
	if rt.Wait("s1", 20*time.Millisecond) { t.Fatal("active chat must time out") }
	go func() { time.Sleep(10 * time.Millisecond); rt.Delete("s1") }()
	if !rt.Wait("s1", time.Second) { t.Fatal("Delete must release Wait") }
	rt.Delete("s1") // 二次 Delete 不 panic
}
```

把文件内所有既有 `rt.Register("x", "y")` 调用改为 `rt.Register(context.Background(), "x", "y")`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run 'TestRuntimeState_' 2>&1 | head`
Expected: 编译错误（签名/方法不存在）。

- [ ] **Step 3: 实现**

`Cancel`：Load → 若 chatID != "" && chat.ChatID != chatID 返回 false → `chat.mu.Lock(); chat.Status = "cancelled"; chat.mu.Unlock(); chat.cancel()` → true。`Wait`：Load 失败返回 true；否则 `select { case <-chat.done: true; case <-time.After(timeout): false }`。`Delete`：`LoadAndDelete`，命中则 `cancel()`（释放 ctx 资源）并 close(done)。

- [ ] **Step 4: 修复包内其他编译点**

`grep -rn "\.Register(" internal --include=*.go | grep -i runtime` 列出的调用（主要是 `internal/api/handler/chat.go:226` 与 handler 测试、`status_test.go`）此时会编译失败。本任务只修 `internal/agent` 包内；handler 包的改动在 Task 2 完成。因此本步只要求 `go test ./internal/agent/... -v` 通过，`go build ./...` 允许在 handler 包失败。

- [ ] **Step 5: 运行**

Run: `gofmt -l internal/agent && go test ./internal/agent/... -v`
Expected: gofmt 无输出；PASS。

- [ ] **Step 6: 请求用户提交**

建议 message：`feat(agent): RuntimeState 支持取消与等待对话结束`

---

### Task 2: /chat 使用可取消 ctx 并补齐状态清理

**Files:**
- Modify: `internal/api/handler/chat.go:224-245,315-330`
- Modify: `internal/api/handler/status.go:100`（`activeChat.Status` → `activeChat.StatusSnapshot()`）
- Modify: 所有调用 `Register(` 的 handler 测试（`grep -ln "Register(" internal/api/handler/*_test.go`）改为带 `context.Background()`
- Test: `internal/api/handler/chat_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Register(ctx, sid, cid)`、`ActiveChat.Context()`、`Delete`。

- [ ] **Step 1: 写失败测试**

`chat.go` 完整路径需要 memory、settings 等依赖，现有 `newChatHandlerForTest` 没有。本任务的行为（ctx 传递、失败路径清理）用一个最小 handler 测试覆盖 Register 失败清理分支即可：

```go
func TestChatHandler_RegisterStateCleanedOnCreateSessionFailure(t *testing.T) {
	// 构造：runtimeState 真实；memory 指向一个已关闭的 sqlx.DB（CreateSession 必失败）；
	// settings、models 按 chat_test.go 现有方式构造；带 web 身份与合法 instruction。
	// 断言：响应 500；rt.IsRunning(任意新 sid)==false，即 rt.RunningCount()==0。
}
```

实现者按 `chat_test.go` 既有工厂拼装；若 settings 构造过于复杂导致无法在合理成本内落地，把该测试降级为对 `runtimeState.RunningCount()==0` 的断言写进报告并说明，Python 系统测试补覆盖（见 Task 7）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestChatHandler_RegisterStateCleaned -v`
Expected: FAIL 或编译错误。

- [ ] **Step 3: 实现**

- `active, err := h.runtimeState.Register(ctx, sessionID, chatID)`；后续 goroutine 内 `h.agentExecutor.Execute(active.Context(), sessionID, task, sseWriter)`。
- Register 成功之后到 `go func()` 之前的每条 `return`（CreateSession 失败、附件处理中的任何失败分支）前加 `h.runtimeState.Delete(sessionID)`。用 `grep -n "return" ` 对照行号逐条核对，报告里列出补了哪几处。
- `status.go:100`：`activeChat.StatusSnapshot()`。
- 测试文件中 `Register(` 调用全部加 `context.Background()`。

- [ ] **Step 4: 运行**

Run: `go build ./... && gofmt -l internal/api/handler && go test ./internal/api/handler/... ./internal/agent/... -v 2>&1 | tail -5`
Expected: 构建通过；gofmt 无输出；PASS。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(chat): 对话执行使用可取消上下文，补齐注册后失败路径的状态清理`

---

### Task 3: 集群取消：ChatCanceller 与 chat 模块处理器

**Files:**
- Create: `internal/agent/canceller.go`
- Create: `internal/agent/cluster_handler.go`
- Modify: `cmd/groot/main.go:475`（注册处理器）
- Test: `internal/agent/canceller_test.go`、`internal/agent/cluster_handler_test.go`

**Interfaces:**
- Consumes: Task 1 的 `RuntimeState.Cancel`；`cluster.Message`、`cluster.HandlerFunc`。
- Produces（包 `agent`）：
  - `const ClusterModuleName = "chat"`、`const MessageTypeCancel = "chat.cancel"`
  - `type MessageSender interface { SendMessage(ctx context.Context, msg cluster.Message) error }`
  - `type CancelResult int`，常量 `CancelLocal`（本地命中）、`CancelBroadcast`（本地未命中，已广播）、`CancelNotRunning`（本地未命中且无法广播）。
  - `type ChatCanceller interface { Cancel(ctx context.Context, sessionID, chatID string) (CancelResult, error) }`
  - `func NewClusterCanceller(rt *RuntimeState, sender MessageSender) *ClusterCanceller` — sender 为 nil 接口表示单机。本地命中 → `CancelLocal, nil`；sender nil → `CancelNotRunning, nil`；sender 成功 → `CancelBroadcast, nil`；sender 失败 → `CancelNotRunning, err`。
  - `func NewClusterHandler(rt *RuntimeState, log *logger.Logger) cluster.MessageHandler`
  - `cluster.Message` 构造：`Type: MessageTypeCancel, TargetModule: ClusterModuleName, Payload: map[string]any{"session_id": sid, "chat_id": cid}, ExpiresAt: time.Now().Add(30*time.Second)`。

注意：`internal/agent` 导入 `internal/cluster` 前先 `go list -deps ./internal/cluster | grep internal/agent` 确认无环；若有环，把两个文件放到新包 `internal/chatcancel`，接口名不变，并在报告中说明。

- [ ] **Step 1: 写失败测试**

`canceller_test.go`：

```go
type fakeSender struct{ msgs []cluster.Message; err error }
func (f *fakeSender) SendMessage(_ context.Context, m cluster.Message) error { f.msgs = append(f.msgs, m); return f.err }

func TestClusterCanceller_LocalHitDoesNotBroadcast(t *testing.T) {
	rt := NewRuntimeState(); rt.Register(context.Background(), "s1", "c1")
	fs := &fakeSender{}
	res, err := NewClusterCanceller(rt, fs).Cancel(context.Background(), "s1", "c1")
	if res != CancelLocal || err != nil || len(fs.msgs) != 0 { t.Fatalf("res=%v err=%v msgs=%d", res, err, len(fs.msgs)) }
}
func TestClusterCanceller_MissBroadcasts(t *testing.T) {
	rt := NewRuntimeState(); fs := &fakeSender{}
	res, err := NewClusterCanceller(rt, fs).Cancel(context.Background(), "s1", "c1")
	if res != CancelBroadcast || err != nil || len(fs.msgs) != 1 { t.Fatalf("res=%v err=%v msgs=%d", res, err, len(fs.msgs)) }
	m := fs.msgs[0]
	if m.Type != MessageTypeCancel || m.TargetModule != ClusterModuleName || m.TargetInstance != "" { t.Errorf("%+v", m) }
	if m.Payload["session_id"] != "s1" || m.Payload["chat_id"] != "c1" { t.Errorf("payload %+v", m.Payload) }
	if d := time.Until(m.ExpiresAt); d < 25*time.Second || d > 31*time.Second { t.Errorf("expires in %v", d) }
}
func TestClusterCanceller_NilSenderSingleNode(t *testing.T) {
	res, err := NewClusterCanceller(NewRuntimeState(), nil).Cancel(context.Background(), "s1", "c1")
	if res != CancelNotRunning || err != nil { t.Fatalf("res=%v err=%v", res, err) }
}
func TestClusterCanceller_SendError(t *testing.T) {
	fs := &fakeSender{err: errors.New("db down")}
	res, err := NewClusterCanceller(NewRuntimeState(), fs).Cancel(context.Background(), "s1", "c1")
	if res != CancelNotRunning || err == nil { t.Fatalf("res=%v err=%v", res, err) }
}
```

`cluster_handler_test.go`：

```go
func TestClusterHandler_CancelsLocal(t *testing.T) {
	rt := NewRuntimeState(); chat, _ := rt.Register(context.Background(), "s1", "c1")
	h := NewClusterHandler(rt, logger.NewNop())
	err := h.Handle(context.Background(), cluster.Message{Type: MessageTypeCancel, Payload: map[string]any{"session_id": "s1", "chat_id": "c1"}})
	if err != nil { t.Fatal(err) }
	select { case <-chat.Context().Done(): default: t.Fatal("not cancelled") }
}
func TestClusterHandler_MissIsNotError(t *testing.T) { /* 无活跃对话 → err == nil */ }
func TestClusterHandler_EmptyChatIDMatchesAny(t *testing.T) { /* chat_id "" → 取消 */ }
func TestClusterHandler_BadPayload(t *testing.T) {
	// 缺 session_id、session_id 非字符串、Type 不是 chat.cancel → 三种都返回 err != nil 且不 panic
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run 'TestClusterCanceller_|TestClusterHandler_' 2>&1 | head`
Expected: 编译错误。

- [ ] **Step 3: 实现两个文件 + main.go 注册**

`main.go:475` 之后：`clusterMsg.RegisterHandler(agent.ClusterModuleName, agent.NewClusterHandler(runtimeState, log))`（`runtimeState` 变量在 main 中已存在，传给 NewServer 的那个）。

- [ ] **Step 4: 运行**

Run: `go build ./... && gofmt -l internal/agent cmd && go test ./internal/agent/... -v 2>&1 | tail -5`
Expected: 通过。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(cluster): 对话取消的跨节点广播与本地处理器`

---

### Task 4: stop 接口

**Files:**
- Create: `internal/api/handler/stop.go`
- Modify: `internal/api/router.go:108`（之后加路由）、`internal/api/server.go:96-105`（构造 canceller 与 handler，传入 RegisterRoutes）、`internal/api/middleware/auth.go` `getRequiredPermission`
- Test: `internal/api/handler/stop_test.go`、`internal/api/middleware/auth_test.go`

**Interfaces:**
- Consumes: Task 3 `agent.ChatCanceller`；`requireOwner`、`ownsOrAdmin`；测试助手 `setIdentity`、`newSearchTestHandler`（取 Manager）、`param.Param`。
- Produces: `func NewStopHandler(canceller agent.ChatCanceller, mem *memory.Manager) *StopHandler`；`(*StopHandler).Serve(ctx, rc)`；路由 `apiGroup.POST("/chat/:sid/:cid/stop", stopH.Serve)`；权限映射：`strings.HasSuffix(path, "/stop") && strings.HasPrefix(path, "/chat/") && method == "POST"` → `"chat"`，放在现有 `/chat/status/` 判断之前。

server.go 构造：

```go
var cancelSender agent.MessageSender
if ms := clusterInst.MessageService(); ms != nil { cancelSender = ms }
canceller := agent.NewClusterCanceller(runtimeState, cancelSender)
stopH := handler.NewStopHandler(canceller, memMgr)
```

（`runtimeState`、`memMgr` 为 NewServer 现有参数名，按实际名称使用。）

- [ ] **Step 1: 写失败测试**

`stop_test.go`，用一个可控的 fake canceller：

```go
type fakeCanceller struct{ res agent.CancelResult; err error; calls [][2]string }
func (f *fakeCanceller) Cancel(_ context.Context, sid, cid string) (agent.CancelResult, error) { f.calls = append(f.calls, [2]string{sid, cid}); return f.res, f.err }

func newStopCtx(sid, cid string) *app.RequestContext { /* POST, params sid+cid */ }

func TestStop_LocalHit200(t *testing.T) {
	_, mem := newSearchTestHandler(t); mem.CreateSession("s1", "crm", "u1")
	fc := &fakeCanceller{res: agent.CancelLocal}
	h := NewStopHandler(fc, mem)
	rc := newStopCtx("s1", "c1"); setIdentity(rc, "svc", "crm", "u1")
	h.Serve(context.Background(), rc)
	// 200, body 含 "success", fc.calls == [["s1","c1"]]
}
func TestStop_MissBroadcast202(t *testing.T) { /* fakeCanceller{res: agent.CancelBroadcast} → 202 "accepted" */ }
func TestStop_MissNoBroadcast409(t *testing.T) { /* fakeCanceller{res: agent.CancelNotRunning} → 409 "chat_not_running"；err 非 nil 时同样 409 */ }
func TestStop_ForeignIs404(t *testing.T) { /* owner ("erp","u1") → 404 session_not_found，fc.calls 为空 */ }
func TestStop_MissingUser400(t *testing.T) {}
func TestStop_EmptyTenant403(t *testing.T) {}
func TestStop_WebAdminBypasses(t *testing.T) { /* caller=web 无归属 → 200 */ }
func TestStop_MissingParams400(t *testing.T) { /* cid 为空 → 400 */ }
```

stop handler 映射：`CancelLocal` → 200；`CancelBroadcast` → 202；`CancelNotRunning` → 409（err 非 nil 时记日志）。

`auth_test.go` 追加 `TestGetRequiredPermission_StopAndDelete`：`("/chat/s/c/stop","POST")→"chat"`，`("/sess/s","DELETE")→"delete"`，`("/sess/s","GET")→"session"`，`("/chat/status/s","GET")→"status"`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestStop_' -v 2>&1 | head; go test ./internal/api/middleware/ -run TestGetRequiredPermission_StopAndDelete -v`
Expected: 编译错误 / FAIL。

- [ ] **Step 3: 实现 stop.go、路由、server 构造、权限映射**

- [ ] **Step 4: 运行**

Run: `go build ./... && gofmt -l internal/api && go test ./internal/api/... -v 2>&1 | tail -5`
Expected: 通过。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(chat): POST /chat/{sid}/{cid}/stop 中止正在生成的对话`

---

### Task 5: delete 接口与 delete 权限点

**Files:**
- Modify: `internal/api/handler/session.go`（新增 `DeleteSession` 方法；`SessionHandler` 增加 `runtime *agent.RuntimeState`、`canceller agent.ChatCanceller`、`clustered bool` 字段，`NewSessionHandler` 签名相应扩展）
- Modify: `internal/api/router.go:115-117`（加 `apiGroup.DELETE("/sess/:sid", sessionH.DeleteSession)`）、`internal/api/server.go`（NewSessionHandler 传参）
- Modify: `internal/repo/apikey.go:21`（`ValidPermissions` 加 `"delete"`）
- Modify: `internal/api/middleware/auth.go` `getRequiredPermission`（`/sess/` 前缀且 DELETE → `"delete"`，在现有 `/sess/` 规则之前）
- Modify: `web/src/components/settings/ApiKeysPanel.vue:23`（`permissionOptions` 加 `'delete'`）
- Test: `internal/api/handler/session_test.go`、`internal/api/handler/search_test.go`（`newSearchTestHandler` 适配新签名）、`internal/repo/apikey_test.go` 或 `apikeys_test.go`（`delete` 合法）

**Interfaces:**
- Consumes: Task 1 `Register/Cancel/Wait/Delete`；Task 3 `ChatCanceller`、`CancelResult`；Task 4 的 `fakeCanceller`（同包测试助手）。
- Produces: `func NewSessionHandler(mem *memory.Manager, rt *agent.RuntimeState, canceller agent.ChatCanceller, clustered bool) *SessionHandler`；`(*SessionHandler).DeleteSession(ctx, rc)`。`clustered` 由 server.go 以 `clusterInst.MessageService() != nil` 计算。

等待常量：`const deleteWaitTimeout = 10 * time.Second`、`const deleteBroadcastGrace = 5 * time.Second`，包级变量以便测试缩短（测试中赋小值并 `t.Cleanup` 恢复）。

- [ ] **Step 1: 写失败测试**

`session_test.go` 追加（`newSearchTestHandler` 改为构造 `NewSessionHandler(mem, agent.NewRuntimeState(), &fakeCanceller{}, false)` 并额外返回 rt；已有调用点同步）：

```go
func TestDeleteSession_OwnerOK(t *testing.T) {
	// 建 ("crm","u1") s1 + 一条 chat；DELETE → 200 body 含 s1；mem.ExistsSession("s1")==false；GetHistory 报错或为空
}
func TestDeleteSession_ForeignIs404(t *testing.T) { /* ("erp","u1") → 404 且 ExistsSession 仍 true */ }
func TestDeleteSession_WebAdminDeletesScheduledSession(t *testing.T) { /* 会话 ("","")；caller=web → 200 */ }
func TestDeleteSession_RunningCancelledThenDeleted(t *testing.T) {
	// rt.Register(ctx,"s1","c1")；goroutine: <-chat.Context().Done() 后 time.Sleep(10ms) 再 rt.Delete("s1")
	// DELETE → 200；chat ctx 已 Done；ExistsSession false
}
func TestDeleteSession_RunningWaitTimeout409(t *testing.T) {
	// deleteWaitTimeout = 30ms；Register 后不 Delete；DELETE → 409 chat_still_running；ExistsSession 仍 true；rt.Get("s1") 仍存在
}
func TestDeleteSession_ConcurrentChatBlockedByPlaceholder(t *testing.T) {
	// 在 DeleteSession 执行到占位之后（用 fakeCanceller 的 hook 或直接测 Register 语义）：
	// 简化：先 rt.Register(ctx,"s1","delete") 模拟占位，然后 rt.Register(ctx,"s1","c2") 必须报错
}
func TestDeleteSession_PlaceholderRaceReturns409(t *testing.T) {
	// 无活跃；fakeCanceller 在 Cancel 时抢先 rt.Register(ctx,"s1","c9") 模拟续聊先到 —
	// 单机模式不会调 Cancel，所以改为：clustered=true、deleteBroadcastGrace=1ms，
	// fakeCanceller.Cancel 内 rt.Register(...)；DELETE → 409 且 ExistsSession 仍 true
}
func TestDeleteSession_MissingUser400(t *testing.T) {}
```

`apikeys_test.go` 追加：创建权限 `["delete"]` 的 Key 返回 200。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestDeleteSession_' 2>&1 | head`
Expected: 编译错误。

- [ ] **Step 3: 实现**

`DeleteSession` 流程严格按 Global Constraints 的 delete 条目；日志用现有 handler 模式。占位 `Register(ctx, sid, "delete")` 成功后用 `defer h.runtime.Delete(sid)` 释放。

- [ ] **Step 4: 运行**

Run: `go build ./... && gofmt -l internal && go test ./internal/... 2>&1 | grep -v "^ok\|no test files"; (cd web && npm run build 2>&1 | tail -1)`
Expected: 无非 ok 行；前端构建成功。

- [ ] **Step 5: 请求用户提交**

建议 message：`feat(sess): DELETE /sess/{sid} 删除会话，新增 delete 权限点`

---

### Task 6: 文档与测试用例

**Files:**
- Modify: `README.md`：权限表加 `delete` 行；接口总表加两行；新增 7.x 小节 `POST /chat/{sid}/{cid}/stop`（三种响应）与 `DELETE /sess/{sid}`（409 两种、已知边角：定时任务执行瞬间、远端收尾超 5 秒）；错误码附录加 `chat_not_running`、`chat_still_running`、`accepted`。
- Modify: `tests/TEST_CASES.md`：新增 `### 1.12 对话中止与会话删除测试`（列 Task 1-5 的 Go 测试名）；系统测试点：stop 命中 200 且 SSE 以 cancelled 收尾、stop 已结束对话 409、delete 后 GET 404、delete 运行中会话、集群下 stop 到非执行节点返回 202。
- Modify: `tests/python/conftest.py`：无需改动（权限 `all`）；若有列出 `ValidPermissions` 全集的断言则加 `delete`。
- Modify: `examples/python/groot_client.py`：增加 `stop_chat(sid, cid)` 与 `delete_session(sid)` 两个方法。

- [ ] **Step 1: 更新 README（正面陈述，禁止对比措辞）**
- [ ] **Step 2: 更新 TEST_CASES.md**
- [ ] **Step 3: 更新 Python 客户端与测试（`python3 -I -m py_compile`）**
- [ ] **Step 4: 全量验证**

Run: `go build -o dist/groot ./cmd/groot && go test ./internal/... 2>&1 | tail -3`
Expected: 全 ok。

- [ ] **Step 5: 请求用户提交**

建议 message：`docs: 对话中止与会话删除接口说明与测试用例`
