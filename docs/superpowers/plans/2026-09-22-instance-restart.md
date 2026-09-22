# 实例重启与进程监督 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `groot` 默认以「监督进程 + 工作进程」双进程运行，工作进程用退出码 3 请求重启、监督进程负责拉起与崩溃退避；管理员可在 Web **设置 → 集群管理** 对任一实例点击「重启」，通过集群点对点消息投递到目标实例执行优雅重启。

**Architecture:** 新增 `internal/lifecycle` 包：`Supervisor`（拉起子进程、stdin 管道关闭通知、退避、信号转发）、`Controller`（把信号 / 管道 EOF / 重启指令收敛为唯一停止原因并映射退出码）、`WatchStdin`（EOF 监听）、`ClusterHandler`（集群消息模块 `lifecycle`，处理 `restart`）。`cmd/groot/main.go` 按 `GROOT_SUPERVISED` 环境变量与 `--single-process` 标志判定角色。`ClusterHandler`（api/handler）新增 `POST /web/cluster/:reg_id/restart`，统一通过 `cluster.MessageService.SendMessage` 投递点对点消息，本机重启也走消息。前端集群面板加重启按钮、本机标签、重启中状态与本机重启后跳登录。

**Tech Stack:** Go 1.26、hertz、zap、sqlx（测试用 `db.Open(nil, t.TempDir())` 建临时 SQLite）；Vue 3 + Element Plus + vue-i18n；Go 标准 `testing`，Supervisor 测试用 `TestHelperProcess` 自举模式。

**Spec:** `docs/superpowers/specs/2026-09-22-instance-restart-design.md`

**与设计文档的对齐说明（实现时以本计划为准）：**

| 设计文档 | 本计划 | 原因 |
|---|---|---|
| Payload `requested_by` 为登录用户名 | 为登录用户系统编号（`web_user_id`） | 会话中间件只注入用户编号；解析用户名需给 ClusterHandler 增加 UserRepo 依赖，收益不值 |
| `NewClusterHandler(members, log)` | `NewClusterHandler(members, selfID func() string, sender RestartSender, restartSupported bool, log)` | 端点需要本机 reg_id、消息发送能力与进程模式 |
| `NewServer` 参数列表不变 | 增加 `clusterInst *cluster.Cluster, role lifecycle.Role` 两个参数 | 健康检查与集群处理器都需要 |
| 工作进程退出码由 `main` 直接 `os.Exit` | `main` 在 `srv.Start()` 返回后检查 Controller 是否已记录停止原因：已记录则以该原因的退出码退出（忽略 Start 的返回值），未记录且 Start 报错则退出 1 | 不依赖 hertz `Run()` 在优雅关闭后的返回值语义 |

**Git 规则：** 项目规范要求所有 commit 必须由用户明确请求。本计划的任务**不包含提交步骤**，全部任务完成并通过测试后，向用户报告并等待"提交"指令。

**行号说明：** 各任务 `Modify:` 后的行号以当前文件为准，前序任务编辑后会偏移，以引用的代码内容定位为准。

**运行测试的统一命令：** `go test ./internal/lifecycle/... ./internal/api/... ./internal/cmd/... -v`（各任务只跑本任务涉及的包）。

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/lifecycle/role.go` | 新建：`Role` 枚举（Supervisor/Worker/Single）、`DetectRole`、`ProcessMode()`、环境变量常量 |
| `internal/lifecycle/role_test.go` | 新建：角色判定测试 |
| `internal/lifecycle/controller.go` | 新建：`Controller`、`Reason`、退出码常量、`ErrRestartUnsupported` |
| `internal/lifecycle/controller_test.go` | 新建：Once 语义、退出码映射、单进程拒绝 |
| `internal/lifecycle/stdin_watch.go` | 新建：`WatchStdin(r io.Reader, onEOF func())` |
| `internal/lifecycle/stdin_watch_test.go` | 新建：管道 EOF 触发测试 |
| `internal/lifecycle/supervisor.go` | 新建：`Supervisor`、`Options`、`Backoff`、`Run(ctx)`、子进程拉起与管道 |
| `internal/lifecycle/supervisor_test.go` | 新建：`TestHelperProcess` 自举，覆盖重启、正常退出、崩溃退避放弃、管道关闭、超时 Kill |
| `internal/lifecycle/cluster_handler.go` | 新建：消息常量 `MessageTypeRestart`/`ModuleName`、`ClusterHandler` |
| `internal/lifecycle/cluster_handler_test.go` | 新建：过期忽略、正常触发、单进程拒绝、重复只触发一次 |
| `internal/api/types/types.go` | 修改：`ClusterResponse.Self`、`RestartResponse` |
| `internal/api/handler/cluster.go` | 修改：构造函数签名、`Self` 字段、`Restart` 端点 |
| `internal/api/handler/cluster_test.go` | 修改：适配新签名；新增 Restart 端点测试 |
| `internal/api/handler/health.go` | 修改：`processMode` 字段、`environmentInfo()` 抽出 |
| `internal/api/handler/health_test.go` | 修改：新增 `environmentInfo` 测试 |
| `internal/api/server.go` | 修改：`NewServer` 增参并接线 |
| `internal/api/router.go` | 修改：注册 `POST /web/cluster/:reg_id/restart` |
| `internal/cmd/status.go` | 修改：输出进程模式与 PID |
| `internal/cmd/status_test.go` | 修改：断言新增输出 |
| `cmd/groot/main.go` | 修改：`--single-process` 标志、角色分派、Controller 接线、显式退出码 |
| `web/src/api/types.ts` | 修改：`ClusterResp.self`、`RestartResp`、`HealthResp.environment.info` 增字段 |
| `web/src/i18n/messages/zh-cn.ts` / `en.ts` | 修改：`cluster.*` 新文案 |
| `web/src/components/settings/ClusterPanel.vue` | 修改：重启按钮、本机标签、重启中状态、本机重启流程 |
| `README.md` | 修改：集群管理功能表、启动服务、命令行、FAQ Q12 |
| `tests/TEST_CASES.md` | 修改：登记新增单元测试 |

---

## Task 1: 角色判定（lifecycle/role.go）

**Files:**
- Create: `internal/lifecycle/role.go`
- Create: `internal/lifecycle/role_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/lifecycle/role_test.go
package lifecycle

import "testing"

func TestDetectRole(t *testing.T) {
	cases := []struct {
		name          string
		supervisedEnv string
		singleFlag    bool
		want          Role
	}{
		{"默认为监督进程", "", false, RoleSupervisor},
		{"环境变量标记为工作进程", "1", false, RoleWorker},
		{"单进程标志", "", true, RoleSingle},
		{"环境变量优先于单进程标志", "1", true, RoleWorker},
		{"环境变量非 1 不算工作进程", "true", false, RoleSupervisor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectRole(c.supervisedEnv, c.singleFlag); got != c.want {
				t.Errorf("DetectRole(%q, %v) = %v, want %v", c.supervisedEnv, c.singleFlag, got, c.want)
			}
		})
	}
}

func TestRole_ProcessMode(t *testing.T) {
	if got := RoleWorker.ProcessMode(); got != "supervised" {
		t.Errorf("RoleWorker.ProcessMode() = %q, want supervised", got)
	}
	if got := RoleSingle.ProcessMode(); got != "single" {
		t.Errorf("RoleSingle.ProcessMode() = %q, want single", got)
	}
	if got := RoleSupervisor.ProcessMode(); got != "supervisor" {
		t.Errorf("RoleSupervisor.ProcessMode() = %q, want supervisor", got)
	}
}

func TestRole_RestartSupported(t *testing.T) {
	if !RoleWorker.RestartSupported() {
		t.Error("RoleWorker 应支持重启")
	}
	if RoleSingle.RestartSupported() {
		t.Error("RoleSingle 不应支持重启")
	}
	if RoleSupervisor.RestartSupported() {
		t.Error("RoleSupervisor 不应支持重启")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/lifecycle/... -run 'TestDetectRole|TestRole_' -v`
Expected: FAIL，`undefined: DetectRole`

- [ ] **Step 3: 实现**

```go
// internal/lifecycle/role.go
package lifecycle

// EnvSupervised 是监督进程拉起工作进程时设置的环境变量；值为 "1" 表示工作进程。
const EnvSupervised = "GROOT_SUPERVISED"

// Role 是进程在双进程模型中的角色。
type Role int

const (
	// RoleSupervisor 监督进程：不加载业务，只拉起并看护工作进程。
	RoleSupervisor Role = iota
	// RoleWorker 工作进程：由监督进程拉起，承担全部业务，支持重启。
	RoleWorker
	// RoleSingle 单进程：直接承担全部业务，没有监督进程，不支持重启。
	RoleSingle
)

// DetectRole 按优先级判定角色：环境变量 GROOT_SUPERVISED=1 → Worker；
// 否则 --single-process → Single；否则 Supervisor。
func DetectRole(supervisedEnv string, singleFlag bool) Role {
	if supervisedEnv == "1" {
		return RoleWorker
	}
	if singleFlag {
		return RoleSingle
	}
	return RoleSupervisor
}

// ProcessMode 返回健康检查与 status 命令展示用的模式字符串。
func (r Role) ProcessMode() string {
	switch r {
	case RoleWorker:
		return "supervised"
	case RoleSingle:
		return "single"
	default:
		return "supervisor"
	}
}

// RestartSupported 只有工作进程支持重启：以退出码 3 退出后由监督进程拉起。
func (r Role) RestartSupported() bool {
	return r == RoleWorker
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/lifecycle/... -run 'TestDetectRole|TestRole_' -v`
Expected: PASS

---

## Task 2: 生命周期控制器（lifecycle/controller.go）

**Files:**
- Create: `internal/lifecycle/controller.go`
- Create: `internal/lifecycle/controller_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/lifecycle/controller_test.go
package lifecycle

import (
	"errors"
	"testing"
	"time"
)

func TestController_FirstReasonWins(t *testing.T) {
	c := NewController(RoleWorker)
	c.RequestStop(ReasonSignal)
	if err := c.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart 在工作进程模式下不应报错: %v", err)
	}
	c.RequestStop(ReasonSupervisorClosed)

	select {
	case r := <-c.Done():
		if r != ReasonSignal {
			t.Errorf("Done() = %v, want ReasonSignal（第一个原因生效）", r)
		}
	case <-time.After(time.Second):
		t.Fatal("Done() 未收到停止原因")
	}
	if got := c.Reason(); got != ReasonSignal {
		t.Errorf("Reason() = %v, want ReasonSignal", got)
	}
	if !c.Stopping() {
		t.Error("Stopping() 应为 true")
	}
}

func TestController_RestartReason(t *testing.T) {
	c := NewController(RoleWorker)
	if err := c.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart: %v", err)
	}
	select {
	case r := <-c.Done():
		if r != ReasonRestart {
			t.Errorf("Done() = %v, want ReasonRestart", r)
		}
	case <-time.After(time.Second):
		t.Fatal("Done() 未收到停止原因")
	}
}

func TestController_NotStoppingInitially(t *testing.T) {
	c := NewController(RoleWorker)
	if c.Stopping() {
		t.Error("初始 Stopping() 应为 false")
	}
	if got := c.Reason(); got != ReasonNone {
		t.Errorf("初始 Reason() = %v, want ReasonNone", got)
	}
	select {
	case r := <-c.Done():
		t.Fatalf("未请求停止时 Done() 不应返回，got %v", r)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestController_SingleRejectsRestart(t *testing.T) {
	c := NewController(RoleSingle)
	err := c.RequestRestart()
	if !errors.Is(err, ErrRestartUnsupported) {
		t.Fatalf("RequestRestart = %v, want ErrRestartUnsupported", err)
	}
	if c.Stopping() {
		t.Error("单进程模式拒绝重启后不应进入停止状态")
	}
	if c.RestartSupported() {
		t.Error("单进程模式 RestartSupported() 应为 false")
	}
	if !NewController(RoleWorker).RestartSupported() {
		t.Error("工作进程模式 RestartSupported() 应为 true")
	}
}

func TestReason_ExitCode(t *testing.T) {
	cases := []struct {
		reason Reason
		want   int
	}{
		{ReasonNone, ExitOK},
		{ReasonSignal, ExitOK},
		{ReasonSupervisorClosed, ExitOK},
		{ReasonRestart, ExitRestart},
	}
	for _, c := range cases {
		if got := c.reason.ExitCode(); got != c.want {
			t.Errorf("%v.ExitCode() = %d, want %d", c.reason, got, c.want)
		}
	}
	if ExitRestart != 3 {
		t.Errorf("ExitRestart = %d, want 3（避开 Go 运行时 panic 的退出码 2）", ExitRestart)
	}
}

func TestReason_String(t *testing.T) {
	cases := map[Reason]string{
		ReasonNone:             "none",
		ReasonSignal:           "signal",
		ReasonSupervisorClosed: "supervisor_closed",
		ReasonRestart:          "restart",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(r), got, want)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/lifecycle/... -run 'TestController|TestReason' -v`
Expected: FAIL，`undefined: NewController`

- [ ] **Step 3: 实现**

```go
// internal/lifecycle/controller.go
package lifecycle

import (
	"errors"
	"sync"
)

// 工作进程退出码约定：监督进程据此决定下一步动作。
const (
	// ExitOK 正常结束，监督进程随之退出。
	ExitOK = 0
	// ExitRestart 请求重启，监督进程立即重新拉起。
	// 取 3：1 是通用错误，2 是 Go 运行时 panic 与 flag 解析失败的退出码。
	ExitRestart = 3
)

// ErrRestartUnsupported 在没有监督进程的模式下请求重启时返回：
// 此时以 ExitRestart 退出等同于停机，必须拒绝。
var ErrRestartUnsupported = errors.New("lifecycle: 实例以单进程模式运行，不支持重启")

// Reason 是工作进程停止的原因。
type Reason int

const (
	ReasonNone             Reason = iota // 尚未请求停止
	ReasonSignal                         // 收到 SIGINT / SIGTERM
	ReasonSupervisorClosed               // 监督进程关闭了 stdin 管道
	ReasonRestart                        // 收到重启指令
)

func (r Reason) String() string {
	switch r {
	case ReasonSignal:
		return "signal"
	case ReasonSupervisorClosed:
		return "supervisor_closed"
	case ReasonRestart:
		return "restart"
	default:
		return "none"
	}
}

// ExitCode 返回该原因对应的进程退出码。
func (r Reason) ExitCode() int {
	if r == ReasonRestart {
		return ExitRestart
	}
	return ExitOK
}

// Controller 把工作进程的所有停止来源收敛为唯一出口：
// 只有第一个到达的原因生效，之后的请求被忽略，保证关闭流程不会重入。
type Controller struct {
	role   Role
	once   sync.Once
	mu     sync.RWMutex
	reason Reason
	done   chan Reason
}

// NewController 创建控制器。role 决定是否接受重启请求。
func NewController(role Role) *Controller {
	return &Controller{role: role, done: make(chan Reason, 1)}
}

// RequestStop 请求以给定原因停止。只有第一次调用生效。
func (c *Controller) RequestStop(r Reason) {
	c.once.Do(func() {
		c.mu.Lock()
		c.reason = r
		c.mu.Unlock()
		c.done <- r
	})
}

// RequestRestart 请求重启。非工作进程模式返回 ErrRestartUnsupported 且不改变状态。
func (c *Controller) RequestRestart() error {
	if !c.role.RestartSupported() {
		return ErrRestartUnsupported
	}
	c.RequestStop(ReasonRestart)
	return nil
}

// RestartSupported 报告当前角色是否接受重启请求（供消息处理器在调度前判断）。
func (c *Controller) RestartSupported() bool {
	return c.role.RestartSupported()
}

// Done 返回一个通道，首次停止请求到达时收到其原因；只会发送一次。
func (c *Controller) Done() <-chan Reason {
	return c.done
}

// Reason 返回已记录的停止原因；未请求停止时为 ReasonNone。
func (c *Controller) Reason() Reason {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reason
}

// Stopping 报告是否已有停止请求。
func (c *Controller) Stopping() bool {
	return c.Reason() != ReasonNone
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/lifecycle/... -run 'TestController|TestReason' -v`
Expected: PASS

---

## Task 3: stdin EOF 监听（lifecycle/stdin_watch.go）

**Files:**
- Create: `internal/lifecycle/stdin_watch.go`
- Create: `internal/lifecycle/stdin_watch_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/lifecycle/stdin_watch_test.go
package lifecycle

import (
	"os"
	"testing"
	"time"
)

func TestWatchStdin_TriggersOnEOF(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fired := make(chan struct{}, 1)
	WatchStdin(r, func() { fired <- struct{}{} })

	select {
	case <-fired:
		t.Fatal("写端未关闭时不应触发")
	case <-time.After(50 * time.Millisecond):
	}

	// 写入数据不应触发：监听只关心 EOF
	if _, err := w.Write([]byte("noise\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
		t.Fatal("写入数据不应触发")
	case <-time.After(50 * time.Millisecond):
	}

	w.Close()
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("关闭写端后应触发回调")
	}
	r.Close()
}

func TestWatchStdin_TriggersOnClosedReader(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	fired := make(chan struct{}, 1)
	WatchStdin(r, func() { fired <- struct{}{} })
	// 读端自身被关闭（读错误）同样视为不可再等待，触发回调
	r.Close()
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("读端关闭后应触发回调")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/lifecycle/... -run TestWatchStdin -v`
Expected: FAIL，`undefined: WatchStdin`

- [ ] **Step 3: 实现**

```go
// internal/lifecycle/stdin_watch.go
package lifecycle

import "io"

// WatchStdin 在后台持续读取 r 并丢弃内容，直到遇到 EOF 或读错误，然后调用 onEOF 一次。
// 工作进程用它监听监督进程关闭管道的"请停下来"通知。
// 只在受监督模式下使用：单进程模式的 stdin 可能是终端（永不 EOF）或 /dev/null（立即 EOF）。
func WatchStdin(r io.Reader, onEOF func()) {
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := r.Read(buf); err != nil {
				onEOF()
				return
			}
		}
	}()
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/lifecycle/... -run TestWatchStdin -v`
Expected: PASS

---

## Task 4: 监督进程（lifecycle/supervisor.go）

**Files:**
- Create: `internal/lifecycle/supervisor.go`
- Create: `internal/lifecycle/supervisor_test.go`

监督进程用 `TestHelperProcess` 自举模式测试：测试用例把 `Options.Exe` 指向测试二进制自身（`os.Args[0]`），并用环境变量 `GROOT_TEST_HELPER=1` 让子进程走辅助分支。辅助分支按 `GROOT_TEST_MODE` 决定行为，用计数文件 `GROOT_TEST_COUNTER` 记录被拉起的次数。

- [ ] **Step 1: 写失败测试**

```go
// internal/lifecycle/supervisor_test.go
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess 是被 Supervisor 拉起的"工作进程"。只有设置了 GROOT_TEST_HELPER=1 才执行。
// 模式（GROOT_TEST_MODE）：
//   exit      按 GROOT_TEST_EXIT_SEQ（逗号分隔）中第 N 次拉起对应的码退出；越界取最后一个
//   wait      阻塞读 stdin 直到 EOF，然后 exit 0
//   hang      忽略 stdin，睡眠 60 秒
//   env       stdout 打印 GROOT_SUPERVISED 的值后 exit 0
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GROOT_TEST_HELPER") != "1" {
		return
	}
	defer os.Exit(0)

	n := bumpCounter(os.Getenv("GROOT_TEST_COUNTER"))
	switch os.Getenv("GROOT_TEST_MODE") {
	case "exit":
		seq := strings.Split(os.Getenv("GROOT_TEST_EXIT_SEQ"), ",")
		idx := n - 1
		if idx >= len(seq) {
			idx = len(seq) - 1
		}
		code, _ := strconv.Atoi(strings.TrimSpace(seq[idx]))
		os.Exit(code)
	case "wait":
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "hang":
		time.Sleep(60 * time.Second)
		os.Exit(0)
	case "env":
		fmt.Fprint(os.Stdout, os.Getenv(EnvSupervised))
		os.Exit(0)
	}
}

// bumpCounter 读取计数文件、加一写回，返回加一后的值。文件不存在视为 0。
func bumpCounter(path string) int {
	n := 0
	if b, err := os.ReadFile(path); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	n++
	os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644)
	return n
}

func readCounter(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// fastBackoff 让测试跑得快：窗口 5 秒、最多 3 次快速失败、延迟 10ms。
func fastBackoff() Backoff {
	return Backoff{
		FastFailWindow: 5 * time.Second,
		MaxFastFails:   3,
		Delays:         []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
		MinInterval:    0,
	}
}

// newTestSupervisor 构造指向测试二进制自身的 Supervisor。
func newTestSupervisor(t *testing.T, mode string, extraEnv ...string) (*Supervisor, string) {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "counter")
	env := append([]string{
		"GROOT_TEST_HELPER=1",
		"GROOT_TEST_MODE=" + mode,
		"GROOT_TEST_COUNTER=" + counter,
	}, extraEnv...)
	var out strings.Builder
	s := NewSupervisor(Options{
		Exe:         os.Args[0],
		Args:        []string{"-test.run=TestHelperProcess"},
		ExtraEnv:    env,
		Backoff:     fastBackoff(),
		StopTimeout: 2 * time.Second,
		Log:         &out,
		Stdout:      io.Discard,
		Stderr:      io.Discard,
	})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("supervisor log:\n%s", out.String())
		}
	})
	return s, counter
}

func TestSupervisor_RestartsOnExitRestart(t *testing.T) {
	s, counter := newTestSupervisor(t, "exit", "GROOT_TEST_EXIT_SEQ=3,0")
	code := s.Run(context.Background())
	if code != 0 {
		t.Errorf("Run() = %d, want 0", code)
	}
	if n := readCounter(t, counter); n != 2 {
		t.Errorf("子进程应被拉起 2 次（exit 3 → 重启 → exit 0），got %d", n)
	}
}

func TestSupervisor_ExitOKStopsLoop(t *testing.T) {
	s, counter := newTestSupervisor(t, "exit", "GROOT_TEST_EXIT_SEQ=0")
	code := s.Run(context.Background())
	if code != 0 {
		t.Errorf("Run() = %d, want 0", code)
	}
	if n := readCounter(t, counter); n != 1 {
		t.Errorf("exit 0 后不应再拉起，got %d 次", n)
	}
}

func TestSupervisor_GivesUpAfterMaxFastFails(t *testing.T) {
	s, counter := newTestSupervisor(t, "exit", "GROOT_TEST_EXIT_SEQ=1")
	start := time.Now()
	code := s.Run(context.Background())
	if code != 1 {
		t.Errorf("连续崩溃放弃后 Run() = %d, want 1", code)
	}
	if n := readCounter(t, counter); n != 3 {
		t.Errorf("MaxFastFails=3 时应拉起 3 次后放弃，got %d", n)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("退避延迟过长: %v", time.Since(start))
	}
}

func TestSupervisor_LongRunResetsFastFailCounter(t *testing.T) {
	s, counter := newTestSupervisor(t, "exit", "GROOT_TEST_EXIT_SEQ=1,1,1,1,0")
	// 窗口设为 0：任何存活时长都算"长跑"，计数每次归零，永远不会触发放弃
	b := fastBackoff()
	b.FastFailWindow = 0
	s.opts.Backoff = b
	code := s.Run(context.Background())
	if code != 0 {
		t.Errorf("Run() = %d, want 0", code)
	}
	if n := readCounter(t, counter); n != 5 {
		t.Errorf("计数归零后应一直拉起到 exit 0，got %d 次", n)
	}
}

func TestSupervisor_CancelClosesPipeAndChildExits(t *testing.T) {
	s, counter := newTestSupervisor(t, "wait")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- s.Run(ctx) }()

	// 等子进程起来（计数文件出现）
	deadline := time.Now().Add(5 * time.Second)
	for readCounter(t, counter) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("子进程未启动")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("管道关闭后子进程 exit 0，Run() = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后 Run 未返回")
	}
	if n := readCounter(t, counter); n != 1 {
		t.Errorf("关闭中不应重新拉起，got %d 次", n)
	}
}

func TestSupervisor_KillsHungChildAfterStopTimeout(t *testing.T) {
	s, counter := newTestSupervisor(t, "hang")
	s.opts.StopTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- s.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for readCounter(t, counter) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("子进程未启动")
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	cancel()

	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("被强制 Kill 的子进程视为异常，Run() = %d, want 1", code)
		}
		if el := time.Since(start); el < 250*time.Millisecond || el > 3*time.Second {
			t.Errorf("应在 StopTimeout 后 Kill，耗时 %v", el)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后 Run 未返回（Kill 未生效）")
	}
}

func TestSupervisor_SetsSupervisedEnv(t *testing.T) {
	s, _ := newTestSupervisor(t, "env")
	var stdout strings.Builder
	s.opts.Stdout = &stdout
	if code := s.Run(context.Background()); code != 0 {
		t.Fatalf("Run() = %d, want 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "1" {
		t.Errorf("子进程看到的 %s = %q, want \"1\"", EnvSupervised, got)
	}
}

func TestSupervisor_SpawnFailureCountsAsFastFail(t *testing.T) {
	s, _ := newTestSupervisor(t, "exit")
	s.opts.Exe = filepath.Join(t.TempDir(), "no-such-binary")
	code := s.Run(context.Background())
	if code != 1 {
		t.Errorf("可执行文件不存在时应在退避后放弃，Run() = %d, want 1", code)
	}
}

func TestBackoff_Delay(t *testing.T) {
	b := DefaultBackoff()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := b.Delay(i + 1); got != w {
			t.Errorf("Delay(%d) = %v, want %v", i+1, got, w)
		}
	}
	if b.FastFailWindow != 60*time.Second || b.MaxFastFails != 10 || b.MinInterval != time.Second {
		t.Errorf("DefaultBackoff 参数与设计不符: %+v", b)
	}
}

func TestExitCodeOf(t *testing.T) {
	if got := exitCodeOf(nil); got != 0 {
		t.Errorf("exitCodeOf(nil) = %d, want 0", got)
	}
	if got := exitCodeOf(errors.New("spawn failed")); got != 1 {
		t.Errorf("非 ExitError 应映射为 1，got %d", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/lifecycle/... -run 'TestSupervisor|TestBackoff|TestExitCodeOf' -v`
Expected: FAIL，`undefined: NewSupervisor`

- [ ] **Step 3: 实现**

```go
// internal/lifecycle/supervisor.go
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// Backoff 是工作进程异常退出后的重拉策略。
type Backoff struct {
	// FastFailWindow 存活不足此时长即异常退出，计为一次快速失败；0 表示任何存活时长都不算快速失败。
	FastFailWindow time.Duration
	// MaxFastFails 连续快速失败达到此次数后监督进程放弃并退出 1。
	MaxFastFails int
	// Delays 第 n 次连续快速失败后的等待时长（n 从 1 起）；越界取最后一项。
	Delays []time.Duration
	// MinInterval 任意两次拉起之间的最小间隔，对退出码 3 同样生效。
	MinInterval time.Duration
}

// DefaultBackoff 返回设计文档 1.3.5 节的默认策略。
func DefaultBackoff() Backoff {
	return Backoff{
		FastFailWindow: 60 * time.Second,
		MaxFastFails:   10,
		Delays:         []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second},
		MinInterval:    time.Second,
	}
}

// Delay 返回第 n 次连续快速失败后应等待的时长。
func (b Backoff) Delay(n int) time.Duration {
	if len(b.Delays) == 0 {
		return 0
	}
	if n < 1 {
		n = 1
	}
	if n > len(b.Delays) {
		n = len(b.Delays)
	}
	return b.Delays[n-1]
}

// Options 是监督进程的配置。
type Options struct {
	Exe         string        // 工作进程可执行文件路径
	Args        []string      // 传给工作进程的参数（不含程序名）
	ExtraEnv    []string      // 追加到子进程环境的变量（KEY=VALUE）
	Backoff     Backoff       // 退避策略；零值时使用 DefaultBackoff()
	StopTimeout time.Duration // 关闭管道后等待工作进程退出的时长，超时强制 Kill；零值 35 秒
	Log         io.Writer     // 监督进程自身日志；nil 则 os.Stderr
	Stdout      io.Writer     // 工作进程 stdout；nil 则 os.Stdout
	Stderr      io.Writer     // 工作进程 stderr；nil 则 os.Stderr
}

// Supervisor 拉起工作进程并按其退出码决定下一步：0 结束，3 立即重拉，其他退避重拉。
// 它不加载任何业务资源。
type Supervisor struct {
	opts Options
}

// NewSupervisor 创建监督进程；零值字段填入默认值。
func NewSupervisor(o Options) *Supervisor {
	if o.Backoff.MaxFastFails == 0 && len(o.Backoff.Delays) == 0 {
		o.Backoff = DefaultBackoff()
	}
	if o.StopTimeout == 0 {
		o.StopTimeout = 35 * time.Second
	}
	if o.Log == nil {
		o.Log = os.Stderr
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return &Supervisor{opts: o}
}

func (s *Supervisor) logf(format string, args ...any) {
	fmt.Fprintf(s.opts.Log, "[groot-supervisor] %s "+format+"\n",
		append([]any{time.Now().Format("2006-01-02 15:04:05")}, args...)...)
}

// Run 运行监督循环直到工作进程正常结束、ctx 被取消或连续快速失败达到上限。
// 返回监督进程应使用的退出码。
func (s *Supervisor) Run(ctx context.Context) int {
	fastFails := 0
	var lastSpawn time.Time

	for {
		if !lastSpawn.IsZero() {
			if wait := s.opts.Backoff.MinInterval - time.Since(lastSpawn); wait > 0 {
				if !sleepCtx(ctx, wait) {
					return 0
				}
			}
		}

		lastSpawn = time.Now()
		code, spawnErr := s.runOnce(ctx)
		alive := time.Since(lastSpawn)

		if ctx.Err() != nil {
			// 外部要求关闭：不再拉起。子进程正常退出则 0，否则 1。
			if code == 0 && spawnErr == nil {
				return 0
			}
			return 1
		}

		switch {
		case spawnErr == nil && code == ExitOK:
			s.logf("工作进程正常退出，监督进程结束")
			return 0
		case spawnErr == nil && code == ExitRestart:
			s.logf("工作进程请求重启（存活 %s）", alive.Round(time.Millisecond))
			fastFails = 0
			continue
		}

		// 异常退出或拉起失败
		if spawnErr != nil {
			s.logf("拉起工作进程失败: %v", spawnErr)
		} else {
			s.logf("工作进程异常退出，退出码 %d（存活 %s）", code, alive.Round(time.Millisecond))
		}
		if s.opts.Backoff.FastFailWindow > 0 && alive < s.opts.Backoff.FastFailWindow {
			fastFails++
		} else {
			fastFails = 1
		}
		if fastFails >= s.opts.Backoff.MaxFastFails {
			s.logf("连续 %d 次快速失败，放弃拉起，监督进程以退出码 1 结束", fastFails)
			return 1
		}
		delay := s.opts.Backoff.Delay(fastFails)
		s.logf("第 %d 次快速失败，%s 后重新拉起", fastFails, delay)
		if !sleepCtx(ctx, delay) {
			return 1
		}
	}
}

// runOnce 拉起一个工作进程并等待其结束。ctx 取消时关闭 stdin 管道通知其退出，
// 超过 StopTimeout 仍未退出则强制 Kill。返回退出码；拉起失败返回 spawnErr。
func (s *Supervisor) runOnce(ctx context.Context) (code int, spawnErr error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return 1, fmt.Errorf("创建管道失败: %w", err)
	}
	defer pr.Close()
	defer pw.Close()

	cmd := exec.Command(s.opts.Exe, s.opts.Args...)
	cmd.Stdin = pr
	cmd.Stdout = s.opts.Stdout
	cmd.Stderr = s.opts.Stderr
	cmd.Env = append(append(os.Environ(), EnvSupervised+"=1"), s.opts.ExtraEnv...)

	if err := cmd.Start(); err != nil {
		return 1, err
	}
	s.logf("已拉起工作进程 PID %d", cmd.Process.Pid)
	// 子进程已继承读端，父进程的副本立即关闭，保证关闭写端时子进程能读到 EOF。
	pr.Close()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	select {
	case err := <-waitDone:
		return exitCodeOf(err), nil
	case <-ctx.Done():
	}

	s.logf("收到关闭请求，通知工作进程 PID %d 退出", cmd.Process.Pid)
	pw.Close()
	select {
	case err := <-waitDone:
		return exitCodeOf(err), nil
	case <-time.After(s.opts.StopTimeout):
		s.logf("工作进程 %s 内未退出，强制终止", s.opts.StopTimeout)
		_ = cmd.Process.Kill()
		<-waitDone
		return 1, nil
	}
}

// exitCodeOf 把 cmd.Wait 的错误换算为退出码：nil→0；ExitError 取其码（被信号杀死时为 -1，按 1 处理）；其他错误→1。
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if c := ee.ExitCode(); c >= 0 {
			return c
		}
	}
	return 1
}

// sleepCtx 等待 d；ctx 先取消则返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/lifecycle/... -run 'TestSupervisor|TestBackoff|TestExitCodeOf' -v`
Expected: PASS（整组约 3～5 秒）

- [ ] **Step 5: 跑整个包并 vet**

Run: `go vet ./internal/lifecycle/... && go test ./internal/lifecycle/... -v`
Expected: 全部 PASS，无 vet 报告

---

## Task 5: 集群消息处理器（lifecycle/cluster_handler.go）

**Files:**
- Create: `internal/lifecycle/cluster_handler.go`
- Create: `internal/lifecycle/cluster_handler_test.go`

处理器注册为集群消息模块 `lifecycle`，只处理 `restart` 类型。先返回成功（让消费记录落库）再延迟触发重启，是设计文档 1.5.4 节防死循环第 2 层。

- [ ] **Step 1: 写失败测试**

```go
// internal/lifecycle/cluster_handler_test.go
package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/logger"
)

func restartMsg(createdAt time.Time) cluster.Message {
	return cluster.Message{
		ID:             1,
		Type:           MessageTypeRestart,
		TargetInstance: "reg-1",
		TargetModule:   ModuleName,
		Payload:        map[string]any{"reason": "web", "requested_by": "u1"},
		CreatedAt:      createdAt,
	}
}

func expectDone(t *testing.T, c *Controller, want Reason, within time.Duration) {
	t.Helper()
	select {
	case r := <-c.Done():
		if r != want {
			t.Fatalf("Done() = %v, want %v", r, want)
		}
	case <-time.After(within):
		t.Fatalf("%v 内未收到停止原因", within)
	}
}

func expectNotDone(t *testing.T, c *Controller, within time.Duration) {
	t.Helper()
	select {
	case r := <-c.Done():
		t.Fatalf("不应触发停止，got %v", r)
	case <-time.After(within):
	}
}

func TestClusterHandler_FreshMessageTriggersRestart(t *testing.T) {
	startedAt := time.Now().Add(-time.Minute)
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, startedAt, logger.NewNop())
	h.delay = 20 * time.Millisecond

	if err := h.Handle(context.Background(), restartMsg(time.Now())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// 返回时尚未触发（延迟中），随后才触发
	expectNotDone(t, c, 5*time.Millisecond)
	expectDone(t, c, ReasonRestart, time.Second)
}

func TestClusterHandler_IgnoresMessageOlderThanStart(t *testing.T) {
	startedAt := time.Now()
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, startedAt, logger.NewNop())
	h.delay = 10 * time.Millisecond

	if err := h.Handle(context.Background(), restartMsg(startedAt.Add(-time.Second))); err != nil {
		t.Fatalf("过期消息应返回成功（记消费）而不是错误: %v", err)
	}
	expectNotDone(t, c, 100*time.Millisecond)
}

func TestClusterHandler_SingleModeRejects(t *testing.T) {
	c := NewController(RoleSingle)
	h := NewClusterHandler(c, time.Now().Add(-time.Minute), logger.NewNop())
	h.delay = 10 * time.Millisecond

	err := h.Handle(context.Background(), restartMsg(time.Now()))
	if !errors.Is(err, ErrRestartUnsupported) {
		t.Fatalf("Handle = %v, want ErrRestartUnsupported", err)
	}
	expectNotDone(t, c, 100*time.Millisecond)
}

func TestClusterHandler_DuplicateTriggersOnce(t *testing.T) {
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, time.Now().Add(-time.Minute), logger.NewNop())
	h.delay = 10 * time.Millisecond

	for i := 0; i < 3; i++ {
		if err := h.Handle(context.Background(), restartMsg(time.Now())); err != nil {
			t.Fatalf("Handle #%d: %v", i, err)
		}
	}
	expectDone(t, c, ReasonRestart, time.Second)
	// done 通道容量 1 且 Once 保证只发一次：再等一小段不应再收到
	expectNotDone(t, c, 50*time.Millisecond)
}

func TestClusterHandler_UnknownTypeIsError(t *testing.T) {
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, time.Now().Add(-time.Minute), logger.NewNop())
	m := restartMsg(time.Now())
	m.Type = "shutdown"
	if err := h.Handle(context.Background(), m); err == nil {
		t.Fatal("未知类型应返回错误")
	}
	expectNotDone(t, c, 50*time.Millisecond)
}

func TestClusterHandler_NilLoggerDoesNotPanic(t *testing.T) {
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, time.Now().Add(-time.Minute), nil)
	h.delay = 10 * time.Millisecond
	if err := h.Handle(context.Background(), restartMsg(time.Now())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	expectDone(t, c, ReasonRestart, time.Second)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/lifecycle/... -run TestClusterHandler -v`
Expected: FAIL，`undefined: MessageTypeRestart`

- [ ] **Step 3: 实现**

```go
// internal/lifecycle/cluster_handler.go
package lifecycle

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/logger"
)

const (
	// ModuleName 是本处理器在集群消息服务中注册的模块名（TargetModule）。
	ModuleName = "lifecycle"
	// MessageTypeRestart 是重启指令的消息类型。
	MessageTypeRestart = "restart"
	// defaultRestartDelay 处理器返回成功后到真正触发重启的间隔：留给消费记录落库。
	defaultRestartDelay = time.Second
)

// ClusterHandler 处理集群消息模块 lifecycle 的指令。
// 它实现 cluster.MessageHandler。
type ClusterHandler struct {
	ctrl      *Controller
	startedAt time.Time
	delay     time.Duration
	log       *logger.Logger
}

// NewClusterHandler 创建处理器。startedAt 是本进程启动时间，早于它的消息被忽略。
func NewClusterHandler(ctrl *Controller, startedAt time.Time, log *logger.Logger) *ClusterHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &ClusterHandler{ctrl: ctrl, startedAt: startedAt, delay: defaultRestartDelay, log: log}
}

// Handle 实现 cluster.MessageHandler。
// 成功返回 nil 的含义是"已受理"：重启在 delay 之后异步触发，让调用方先把消费记录写入数据库。
func (h *ClusterHandler) Handle(ctx context.Context, msg cluster.Message) error {
	if msg.Type != MessageTypeRestart {
		return fmt.Errorf("lifecycle: 不支持的消息类型 %q", msg.Type)
	}
	if msg.CreatedAt.Before(h.startedAt) {
		// 至少一次语义下的重放保护：早于本进程启动的指令视为已由上一代进程处理
		h.log.Info("忽略早于本进程启动的重启指令",
			zap.Int64("msg_id", msg.ID),
			zap.Time("created_at", msg.CreatedAt),
			zap.Time("started_at", h.startedAt))
		return nil
	}
	if !h.ctrl.RestartSupported() {
		return ErrRestartUnsupported
	}
	h.log.Info("收到重启指令",
		zap.Int64("msg_id", msg.ID),
		zap.String("source", msg.SourceInstance),
		zap.Any("payload", msg.Payload),
		zap.Duration("delay", h.delay))
	time.AfterFunc(h.delay, func() {
		if err := h.ctrl.RequestRestart(); err != nil {
			h.log.Error("触发重启失败", zap.Error(err))
		}
	})
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/lifecycle/... -run TestClusterHandler -v`
Expected: PASS

- [ ] **Step 5: 整包回归**

Run: `go vet ./internal/lifecycle/... && go test ./internal/lifecycle/...`
Expected: `ok`

---

## Task 6: 集群 API：Self 字段与重启端点

**Files:**
- Modify: `internal/api/types/types.go:250-263`
- Modify: `internal/api/handler/cluster.go`
- Modify: `internal/api/handler/cluster_test.go`

- [ ] **Step 1: 扩展响应类型**

在 `internal/api/types/types.go` 中把 `ClusterResponse` 改为并追加 `RestartResponse`：

```go
// ClusterResponse 是 GET /web/cluster 的完整响应体。
// Self 是处理本次请求的实例的 reg_id；未注册集群时为空串。
type ClusterResponse struct {
	Members []ClusterMemberInfo `json:"members"`
	Self    string              `json:"self"`
}

// RestartResponse 是 POST /web/cluster/:reg_id/restart 的 202 响应体。
// Self 为 true 表示被重启的就是处理本次请求的实例，前端据此等待恢复并引导重新登录。
type RestartResponse struct {
	Status string `json:"status"`
	RegID  string `json:"reg_id"`
	Self   bool   `json:"self"`
}
```

- [ ] **Step 2: 更新现有测试以适配新签名并新增端点测试**

把 `internal/api/handler/cluster_test.go` 中 `fakeMemberRepo.Get` 改为真正查找，新增 `fakeSender`，所有 `NewClusterHandler(x, logger.NewNop())` 调用改为 `NewClusterHandler(x, nil, nil, false, logger.NewNop())`，`NewClusterHandler(fake, nil)` 改为 `NewClusterHandler(fake, nil, nil, false, nil)`。然后在文件末尾追加：

```go
// ---- 以下为 Self 字段与重启端点测试 ----

// fakeSender 记录 SendMessage 收到的消息，可注入错误。
type fakeSender struct {
	sent []cluster.Message
	err  error
}

func (f *fakeSender) SendMessage(ctx context.Context, msg cluster.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, msg)
	return nil
}

func selfID(id string) func() string { return func() string { return id } }

// serveRestart 构造带 reg_id 路径参数与 web_user_id 的 POST 请求并执行 Restart。
func serveRestart(h *ClusterHandler, regID string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Params = param.Params{{Key: "reg_id", Value: regID}}
	rc.Set("web_user_id", "u-42")
	h.Restart(context.Background(), rc)
	return rc
}

func TestClusterHandler_SelfField(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{
		{RegID: "n-001", Role: cluster.RoleLeader, Host: "10.0.0.1", Port: 8080},
	}}
	resp := decodeCluster(t, serveCluster(NewClusterHandler(fake, selfID("n-001"), nil, true, logger.NewNop())))
	if resp.Self != "n-001" {
		t.Errorf("Self = %q, want n-001", resp.Self)
	}
	// 未注册集群（selfID 为 nil）时 self 为空串且字段仍存在
	rc := serveCluster(NewClusterHandler(nil, nil, nil, false, logger.NewNop()))
	if body := string(rc.Response.Body()); !contains(body, `"self":""`) {
		t.Errorf("expected self:\"\" in body, got %s", body)
	}
}

func TestRestart_UnknownMember404(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-001", Host: "10.0.0.1", Port: 8080}}}
	s := &fakeSender{}
	rc := serveRestart(NewClusterHandler(fake, selfID("n-001"), s, true, logger.NewNop()), "n-999")
	if got := rc.Response.StatusCode(); got != 404 {
		t.Fatalf("expected 404, got %d body=%s", got, rc.Response.Body())
	}
	if body := string(rc.Response.Body()); !contains(body, `"code":"member_not_found"`) {
		t.Errorf("expected member_not_found, got %s", body)
	}
	if len(s.sent) != 0 {
		t.Errorf("未知成员不应发送消息，sent=%+v", s.sent)
	}
}

func TestRestart_SelfUnsupported409(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-001", Host: "10.0.0.1", Port: 8080}}}
	s := &fakeSender{}
	rc := serveRestart(NewClusterHandler(fake, selfID("n-001"), s, false, logger.NewNop()), "n-001")
	if got := rc.Response.StatusCode(); got != 409 {
		t.Fatalf("expected 409, got %d body=%s", got, rc.Response.Body())
	}
	if body := string(rc.Response.Body()); !contains(body, `"code":"restart_unsupported"`) {
		t.Errorf("expected restart_unsupported, got %s", body)
	}
	if len(s.sent) != 0 {
		t.Errorf("单进程模式本机不应发送消息，sent=%+v", s.sent)
	}
}

func TestRestart_OtherMemberSendsPointToPoint(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{
		{RegID: "n-001", Host: "10.0.0.1", Port: 8080},
		{RegID: "n-002", Host: "10.0.0.2", Port: 8080},
	}}
	s := &fakeSender{}
	before := time.Now()
	rc := serveRestart(NewClusterHandler(fake, selfID("n-001"), s, true, logger.NewNop()), "n-002")
	if got := rc.Response.StatusCode(); got != 202 {
		t.Fatalf("expected 202, got %d body=%s", got, rc.Response.Body())
	}
	var resp types.RestartResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "accepted" || resp.RegID != "n-002" || resp.Self {
		t.Errorf("unexpected resp: %+v", resp)
	}
	if len(s.sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(s.sent))
	}
	m := s.sent[0]
	if m.Type != lifecycle.MessageTypeRestart || m.TargetModule != lifecycle.ModuleName {
		t.Errorf("Type/Module = %q/%q", m.Type, m.TargetModule)
	}
	if m.TargetInstance != "n-002" {
		t.Errorf("TargetInstance = %q, want n-002（点对点）", m.TargetInstance)
	}
	if m.Priority != 1 {
		t.Errorf("Priority = %d, want 1", m.Priority)
	}
	if m.Payload["reason"] != "web" || m.Payload["requested_by"] != "u-42" {
		t.Errorf("Payload = %+v", m.Payload)
	}
	if m.ExpiresAt.Before(before.Add(90*time.Second)) || m.ExpiresAt.After(before.Add(3*time.Minute)) {
		t.Errorf("ExpiresAt = %v，应为约 2 分钟后", m.ExpiresAt)
	}
}

func TestRestart_SelfSupported202(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-001", Host: "10.0.0.1", Port: 8080}}}
	s := &fakeSender{}
	rc := serveRestart(NewClusterHandler(fake, selfID("n-001"), s, true, logger.NewNop()), "n-001")
	if got := rc.Response.StatusCode(); got != 202 {
		t.Fatalf("expected 202, got %d body=%s", got, rc.Response.Body())
	}
	var resp types.RestartResponse
	_ = json.Unmarshal(rc.Response.Body(), &resp)
	if !resp.Self {
		t.Errorf("重启本机应返回 self=true: %+v", resp)
	}
	if len(s.sent) != 1 || s.sent[0].TargetInstance != "n-001" {
		t.Errorf("本机重启同样走消息投递，sent=%+v", s.sent)
	}
}

func TestRestart_SenderError500(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-002", Host: "10.0.0.2", Port: 8080}}}
	s := &fakeSender{err: errors.New("db down")}
	rc := serveRestart(NewClusterHandler(fake, selfID("n-001"), s, true, logger.NewNop()), "n-002")
	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("expected 500, got %d", got)
	}
	if body := string(rc.Response.Body()); contains(body, "db down") {
		t.Errorf("不应泄漏底层错误: %s", body)
	}
}

func TestRestart_NoSender500(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-002", Host: "10.0.0.2", Port: 8080}}}
	rc := serveRestart(NewClusterHandler(fake, selfID("n-001"), nil, true, logger.NewNop()), "n-002")
	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("expected 500, got %d", got)
	}
}

func TestRestart_NilRepo404(t *testing.T) {
	rc := serveRestart(NewClusterHandler(nil, nil, &fakeSender{}, true, logger.NewNop()), "n-001")
	if got := rc.Response.StatusCode(); got != 404 {
		t.Fatalf("expected 404, got %d", got)
	}
}
```

同时把 `fakeMemberRepo.Get` 改为：

```go
func (f *fakeMemberRepo) Get(ctx context.Context, regID string) (*repo.Member, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, m := range f.members {
		if m.RegID == regID {
			return m, nil
		}
	}
	return nil, repo.ErrNotFound
}
```

并在 import 中加入 `"github.com/cloudwego/hertz/pkg/route/param"` 与 `"github.com/zfd81/groot/internal/lifecycle"`。

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/api/handler/... -run 'TestClusterHandler|TestRestart' -v`
Expected: 编译失败，`too many arguments in call to NewClusterHandler` / `undefined: h.Restart`

- [ ] **Step 4: 实现处理器**

把 `internal/api/handler/cluster.go` 整体替换为：

```go
package handler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/lifecycle"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// restartMessageTTL 重启指令的有效期：超过即使未被消费也不再投递。
const restartMessageTTL = 2 * time.Minute

// RestartSender 是投递重启指令所需的最小能力，由 *cluster.MessageService 满足。
type RestartSender interface {
	SendMessage(ctx context.Context, msg cluster.Message) error
}

// ClusterHandler 处理集群管理端点：列出成员、重启指定实例。
//
// 成员数据直接来自 cluster_members 表：过期成员由 leader 的心跳循环清理，
// 因此这里读到的即为当前存活成员，无需在查询侧再按超时过滤。
type ClusterHandler struct {
	members          repo.MemberRepo
	selfID           func() string
	sender           RestartSender
	restartSupported bool
	log              *logger.Logger
}

// NewClusterHandler 构造 ClusterHandler。
//   - members 为 nil（未启用集群）时列表为空、重启一律 404
//   - selfID 返回本实例 reg_id；nil 视为未注册（空串）
//   - sender 为 nil 时重启端点返回 500
//   - restartSupported 为本实例是否支持重启（工作进程模式为 true）
//   - log 为 nil 时用 NewNop() 兜底
func NewClusterHandler(members repo.MemberRepo, selfID func() string, sender RestartSender, restartSupported bool, log *logger.Logger) *ClusterHandler {
	if log == nil {
		log = logger.NewNop()
	}
	if selfID == nil {
		selfID = func() string { return "" }
	}
	return &ClusterHandler{members: members, selfID: selfID, sender: sender, restartSupported: restartSupported, log: log}
}

// Serve 输出 200 JSON：{"members":[{reg_id,role,address,pid,heartbeat_at,created_at}],"self":"<reg_id>"}
// address 为 IP:PORT 形式；时间字段是毫秒时间戳，由前端按本地时区格式化。
// leader 排在首位，其余按 reg_id 升序（即加入顺序）。
func (h *ClusterHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	resp := types.ClusterResponse{Members: []types.ClusterMemberInfo{}, Self: h.selfID()}
	if h.members == nil {
		rc.JSON(200, resp)
		return
	}

	list, err := h.members.ListAll(ctx)
	if err != nil {
		h.log.Error("列出集群成员失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "读取集群成员失败"})
		return
	}

	for _, m := range list {
		resp.Members = append(resp.Members, types.ClusterMemberInfo{
			RegID:       m.RegID,
			Role:        m.Role,
			Address:     fmt.Sprintf("%s:%d", m.Host, m.Port),
			Pid:         m.Pid,
			HeartbeatAt: m.HeartbeatAt.UnixMilli(),
			CreatedAt:   m.CreatedAt.UnixMilli(),
		})
	}

	sort.SliceStable(resp.Members, func(i, j int) bool {
		li := resp.Members[i].Role == cluster.RoleLeader
		lj := resp.Members[j].Role == cluster.RoleLeader
		if li != lj {
			return li
		}
		return resp.Members[i].RegID < resp.Members[j].RegID
	})

	rc.JSON(200, resp)
}

// Restart 处理 POST /web/cluster/:reg_id/restart：向目标实例投递点对点重启指令。
// 本机重启同样走消息投递，不另开直接调用路径（单一链路、消费记录即审计日志）。
func (h *ClusterHandler) Restart(ctx context.Context, rc *app.RequestContext) {
	regID := rc.Param("reg_id")
	if h.members == nil {
		rc.JSON(404, utils.H{"status": "error", "code": "member_not_found", "message": "实例不存在"})
		return
	}
	if _, err := h.members.Get(ctx, regID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			rc.JSON(404, utils.H{"status": "error", "code": "member_not_found", "message": "实例不存在"})
			return
		}
		h.log.Error("查询集群成员失败", zap.String("reg_id", regID), zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "读取集群成员失败"})
		return
	}

	self := regID == h.selfID()
	if self && !h.restartSupported {
		rc.JSON(409, utils.H{"status": "error", "code": "restart_unsupported", "message": "实例以单进程模式运行，不支持重启"})
		return
	}
	if h.sender == nil {
		h.log.Error("集群消息服务未挂接，无法投递重启指令")
		rc.JSON(500, utils.H{"status": "error", "message": "集群消息服务不可用"})
		return
	}

	requestedBy, _ := rc.Get("web_user_id")
	msg := cluster.Message{
		Type:           lifecycle.MessageTypeRestart,
		TargetInstance: regID,
		TargetModule:   lifecycle.ModuleName,
		Payload:        map[string]any{"reason": "web", "requested_by": fmt.Sprint(requestedBy)},
		Priority:       1,
		ExpiresAt:      time.Now().Add(restartMessageTTL),
	}
	if err := h.sender.SendMessage(ctx, msg); err != nil {
		h.log.Error("投递重启指令失败", zap.String("reg_id", regID), zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "投递重启指令失败"})
		return
	}
	h.log.Info("已投递重启指令", zap.String("reg_id", regID), zap.Bool("self", self), zap.Any("requested_by", requestedBy))
	rc.JSON(202, types.RestartResponse{Status: "accepted", RegID: regID, Self: self})
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/api/handler/... -run 'TestClusterHandler|TestRestart' -v`
Expected: PASS（旧的 4 个 ClusterHandler 测试与新增 8 个全部通过）

- [ ] **Step 6: 编译全仓库确认其他调用点**

Run: `go build ./... 2>&1 | head`
Expected: 只有 `internal/api/server.go` 报 `NewClusterHandler` 参数不匹配（Task 8 修复）。若还有其他文件报错，一并按新签名修正。

---

## Task 7: 健康检查增加 pid / process_mode

**Files:**
- Modify: `internal/api/handler/health.go:22-58,150-159`
- Modify: `internal/api/handler/health_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/api/handler/health_test.go` 末尾追加：

```go
func TestEnvironmentInfo(t *testing.T) {
	h := &HealthHandler{
		config:      config.Config{Logging: config.LoggingConfig{File: config.LogFileConfig{Directory: "/var/log/groot"}}},
		homeDir:     "/home/x/.groot",
		processMode: "supervised",
	}
	info := h.environmentInfo()
	if info["home_dir"] != "/home/x/.groot" {
		t.Errorf("home_dir = %q", info["home_dir"])
	}
	if info["log_dir"] != "/var/log/groot" {
		t.Errorf("log_dir = %q", info["log_dir"])
	}
	if info["database"] != "sqlite" {
		t.Errorf("database = %q, want sqlite（配置缺省）", info["database"])
	}
	if info["process_mode"] != "supervised" {
		t.Errorf("process_mode = %q", info["process_mode"])
	}
	if info["pid"] != strconv.Itoa(os.Getpid()) {
		t.Errorf("pid = %q, want %d", info["pid"], os.Getpid())
	}
}
```

import 增加 `"os"`、`"strconv"`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/... -run TestEnvironmentInfo -v`
Expected: FAIL，`unknown field processMode` / `undefined: h.environmentInfo`

- [ ] **Step 3: 实现**

`internal/api/handler/health.go`：

1. 结构体增加字段 `processMode string`（放在 `startTime` 之前）。
2. `NewHealthHandler` 增加末尾参数 `processMode string`，赋值 `processMode: processMode`。
3. `Serve` 中 `"environment"` 检查项的 `Info` 改为 `h.environmentInfo()`。
4. 新增方法：

```go
// environmentInfo 返回运行环境信息（供设置界面与 groot status 展示）。
// pid 是本进程 PID；受监督模式下即工作进程 PID，与集群成员表中登记的一致。
func (h *HealthHandler) environmentInfo() map[string]string {
	return map[string]string{
		"home_dir":     h.homeDir,
		"database":     databaseType(h.config.Database),
		"log_dir":      h.config.Logging.File.Directory,
		"pid":          strconv.Itoa(os.Getpid()),
		"process_mode": h.processMode,
	}
}
```

import 增加 `"os"`、`"strconv"`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/api/handler/... -run 'TestEnvironmentInfo|TestDatabaseType' -v`
Expected: PASS

---

## Task 8: Server 与路由接线

**Files:**
- Modify: `internal/api/server.go:35-52,86-100`
- Modify: `internal/api/router.go:58`

- [ ] **Step 1: 修改 NewServer 签名与处理器构造**

`internal/api/server.go` 的 `NewServer` 参数列表末尾追加两个参数：

```go
	syncResources repo.ResourceRepo, // 配置同步的远端仓储；SQLite 单机模式下为 nil（同步禁用）
	clusterInst *cluster.Cluster,    // 集群实例：提供本机 reg_id 与消息发送能力
	role lifecycle.Role,             // 进程角色：决定健康检查的 process_mode 与是否支持重启
) *Server {
```

把 `healthH := ...` 与 `clusterH := ...` 两行改为：

```go
	healthH := handler.NewHealthHandler(cfg, homeDir, skillBackend, mcpMgr, mem, runtime, models, log, role.ProcessMode())
	...
	// 集群消息服务未挂接时 sender 必须是 nil 接口，而不是包着 nil 指针的非空接口
	var restartSender handler.RestartSender
	if ms := clusterInst.MessageService(); ms != nil {
		restartSender = ms
	}
	clusterH := handler.NewClusterHandler(members, clusterInst.RegID, restartSender, role.RestartSupported(), log)
```

import 增加 `"github.com/zfd81/groot/internal/cluster"`、`"github.com/zfd81/groot/internal/lifecycle"`。

- [ ] **Step 2: 注册路由**

`internal/api/router.go` 在 `webGroup.GET("/cluster", clusterH.Serve)` 之后加一行：

```go
	webGroup.POST("/cluster/:reg_id/restart", clusterH.Restart)
```

- [ ] **Step 3: 编译确认只剩 main.go 报错**

Run: `go build ./... 2>&1 | head`
Expected: 只有 `cmd/groot/main.go` 报 `not enough arguments in call to api.NewServer`（Task 10 修复）

- [ ] **Step 4: 跑 api 包测试**

Run: `go test ./internal/api/... 2>&1 | tail -5`
Expected: 全部 `ok`

---

## Task 9: status 命令输出进程模式与 PID

**Files:**
- Modify: `internal/cmd/status.go:128-135`
- Modify: `internal/cmd/status_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/cmd/status_test.go` 末尾追加：

```go
func TestPrintStatusOutput_ProcessInfo(t *testing.T) {
	health := &types.HealthResponse{
		Status:  "healthy",
		Version: "1.0.0",
		Uptime:  "1m",
		Checks: map[string]types.CheckInfo{
			"environment": {
				Status: "healthy",
				Info:   map[string]interface{}{"pid": "4242", "process_mode": "supervised"},
			},
		},
		Metrics: map[string]interface{}{},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	printStatusOutput(health, 8080)
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	for _, want := range []string{"进程模式:", "监督进程 + 工作进程", "PID:", "4242"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
}

func TestProcessModeLabel(t *testing.T) {
	cases := map[string]string{
		"supervised": "监督进程 + 工作进程（支持重启）",
		"single":     "单进程（不支持重启）",
		"":           "",
		"weird":      "weird",
	}
	for in, want := range cases {
		if got := processModeLabel(in); got != want {
			t.Errorf("processModeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/cmd/... -run 'TestPrintStatusOutput_ProcessInfo|TestProcessModeLabel' -v`
Expected: FAIL，`undefined: processModeLabel`

- [ ] **Step 3: 实现**

`internal/cmd/status.go` 中 `printStatusOutput` 的 `fmt.Printf("端口:      %d\n", port)` 之后插入：

```go
	// 进程模式与 PID（environment 检查项；旧版后端缺失时跳过）
	if env, ok := health.Checks["environment"]; ok {
		if info, ok := env.Info.(map[string]interface{}); ok {
			if mode, ok := info["process_mode"].(string); ok && mode != "" {
				fmt.Printf("进程模式:  %s\n", processModeLabel(mode))
			}
			if pid, ok := info["pid"].(string); ok && pid != "" {
				fmt.Printf("PID:       %s\n", pid)
			}
		}
	}
```

文件末尾追加：

```go
// processModeLabel 把健康检查的 process_mode 换成中文说明；未知值原样返回。
func processModeLabel(mode string) string {
	switch mode {
	case "supervised":
		return "监督进程 + 工作进程（支持重启）"
	case "single":
		return "单进程（不支持重启）"
	default:
		return mode
	}
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/cmd/... -v 2>&1 | tail -20`
Expected: 全部 PASS

---

## Task 10: main.go 角色分派与工作进程接线

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 增加 --single-process 标志与帮助文案**

`var (...)` 块增加 `singleProcess bool`；`init()` 增加：

```go
	flag.BoolVar(&singleProcess, "single-process", false, "单进程运行（无监督进程，不支持重启）")
```

`printHelp()` 的「选项」段在 `-p, --port` 行后加：

```go
	fmt.Println("  --single-process  单进程运行（无监督进程，不支持 Web 重启；调试或容器托管场景使用）")
```

「示例」段末尾加：

```go
	fmt.Println("  groot --single-process        # 单进程运行，不启动监督进程")
```

- [ ] **Step 2: main() 尾部改为角色分派**

把 `main()` 末尾的 `startServer(cmd.GetDefaultHome(), port)` 替换为：

```go
	// No subcommand: 按角色启动
	role := lifecycle.DetectRole(os.Getenv(lifecycle.EnvSupervised), singleProcess)
	if role == lifecycle.RoleSupervisor {
		os.Exit(runSupervisor())
	}
	startServer(cmd.GetDefaultHome(), port, role)
```

新增函数：

```go
// runSupervisor 以监督进程身份运行：拉起同一二进制作为工作进程，并把关闭信号转交给它。
// 工作进程通过环境变量 GROOT_SUPERVISED=1 识别身份，命令行参数原样传递。
func runSupervisor() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法确定可执行文件路径: %s\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	sup := lifecycle.NewSupervisor(lifecycle.Options{
		Exe:  exe,
		Args: os.Args[1:],
	})
	return sup.Run(ctx)
}
```

import 增加 `"github.com/zfd81/groot/internal/lifecycle"`。

- [ ] **Step 3: startServer 接入 Controller**

签名改为 `func startServer(homeDir string, port int, role lifecycle.Role)`。

在 `log.Info("Groot Agent 启动中...", ...)` 之后加：

```go
	log.Info("进程模式", zap.String("mode", role.ProcessMode()), zap.Int("pid", os.Getpid()))
	startedAt := time.Now()
	ctrl := lifecycle.NewController(role)
```

在 `clusterInst.SetMessageService(clusterMsg)` 之后、`Join` 之前加：

```go
	// 生命周期指令处理器（重启）：模块名 lifecycle，早于 startedAt 的指令被忽略
	clusterMsg.RegisterHandler(lifecycle.ModuleName, lifecycle.NewClusterHandler(ctrl, startedAt, log))
```

`api.NewServer(...)` 调用末尾追加两个实参 `clusterInst, role`。

把「Setup graceful shutdown」整段（从 `sigCh := make(...)` 到匿名 goroutine结束）替换为：

```go
	// 停止来源 1：操作系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Info("收到信号，准备关闭", zap.String("signal", sig.String()))
		ctrl.RequestStop(lifecycle.ReasonSignal)
	}()

	// 停止来源 2：监督进程关闭了 stdin 管道（仅受监督模式；单进程的 stdin 可能是终端或 /dev/null）
	if role == lifecycle.RoleWorker {
		lifecycle.WatchStdin(os.Stdin, func() {
			log.Info("监督进程已关闭管道，准备关闭")
			ctrl.RequestStop(lifecycle.ReasonSupervisorClosed)
		})
	}

	// 停止来源 3：重启指令，由 lifecycle.ClusterHandler 调用 ctrl.RequestRestart()

	// 唯一的关闭流程：等第一个停止原因，然后按固定顺序释放资源
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		reason := <-ctrl.Done()
		log.Info("开始关闭", zap.String("reason", reason.String()), zap.Int("exit_code", reason.ExitCode()))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Leave cluster before shutting down
		clusterInst.Leave()

		// Stop server（使 srv.Start() 返回）
		srv.Stop(ctx)

		// Stop message layer
		msgLayer.Stop()

		// Close MCP clients
		mcpMgr.Close()

		// Close sub-agent registry (closes per-agent MCP managers)
		if subAgentReg != nil {
			subAgentReg.Close()
		}

		log.Info("Groot Agent 已关闭")
	}()
```

把文件末尾的 `srv.Start()` 段替换为：

```go
	// Start server
	log.Info("API 服务启动",
		zap.String("host", cfg.Server.Host),
		zap.Int("port", cfg.Server.Port),
	)
	err = srv.Start()

	// 已有停止原因：Start 的返回值是关闭的副产物，不作为错误处理；
	// 等关闭流程跑完，显式刷新日志、关库，再以该原因的退出码退出（os.Exit 不执行 defer）。
	if ctrl.Stopping() {
		<-shutdownDone
		code := ctrl.Reason().ExitCode()
		log.Info("进程退出", zap.Int("exit_code", code))
		log.Sync()
		sqlxDB.Close()
		os.Exit(code)
	}
	if err != nil {
		log.Error("服务启动失败", zap.Error(err))
		os.Exit(1)
	}
}
```

- [ ] **Step 4: 编译、vet、格式化**

Run: `gofmt -l ./cmd ./internal && go vet ./cmd/... ./internal/lifecycle/... ./internal/api/... && go build -o dist/groot ./cmd/groot`
Expected: gofmt 无输出，vet 无报告，编译成功

- [ ] **Step 5: 手工冒烟——双进程启动与信号关闭**

```bash
./dist/groot -p 18080 > /tmp/groot-smoke.log 2>&1 &
sleep 4
pgrep -fl 'dist/groot' ; echo "---"
curl -s http://127.0.0.1:18080/web/health | python3 -c 'import sys,json; e=json.load(sys.stdin)["checks"]["environment"]["info"]; print(e["process_mode"], e["pid"])'
kill -TERM %1 ; sleep 3
pgrep -fl 'dist/groot' || echo "两个进程均已退出"
grep -E 'groot-supervisor|开始关闭|进程退出' /tmp/groot-smoke.log
```

Expected：
- `pgrep` 第一次列出两个 `dist/groot` 进程
- health 输出 `supervised <工作进程 PID>`，且该 PID 与 pgrep 中非首个进程一致
- `kill -TERM` 后两个进程均退出
- 日志含 `[groot-supervisor] ... 已拉起工作进程 PID`、`开始关闭 ... reason=signal`、`进程退出 ... exit_code=0`、`[groot-supervisor] ... 工作进程正常退出`

- [ ] **Step 6: 手工冒烟——单进程模式**

```bash
./dist/groot -p 18081 --single-process > /tmp/groot-single.log 2>&1 &
sleep 4
pgrep -fl 'dist/groot' | wc -l
curl -s http://127.0.0.1:18081/web/health | python3 -c 'import sys,json; print(json.load(sys.stdin)["checks"]["environment"]["info"]["process_mode"])'
./dist/groot status -p 18081 | grep -E '进程模式|PID'
kill -TERM %1 ; sleep 2
```

Expected：进程数 `1`；health 输出 `single`；status 输出「进程模式:  单进程（不支持重启）」与「PID:」行。

---

## Task 11: 前端集群面板——重启按钮、本机标签、重启中状态

**Files:**
- Modify: `web/src/api/types.ts:131-153,232-243`
- Modify: `web/src/i18n/messages/zh-cn.ts:204-213`
- Modify: `web/src/i18n/messages/en.ts:204-213`
- Modify: `web/src/components/settings/ClusterPanel.vue`

前端没有单元测试框架（项目规范禁止引入），验证方式是 `npm run build`（含 `vue-tsc` 类型检查）与手工冒烟。

- [ ] **Step 1: 类型定义**

`web/src/api/types.ts` 中 `HealthResp.checks.environment.info` 改为：

```ts
      info: { home_dir: string; database: string; log_dir: string; pid?: string; process_mode?: string }
```

`ClusterResp` 改为并追加 `RestartResp`：

```ts
export interface ClusterResp {
  members: ClusterMemberInfo[]
  // 处理本次请求的实例的 reg_id；未注册集群时为空串
  self: string
}

// POST /web/cluster/:reg_id/restart 的 202 响应
export interface RestartResp {
  status: 'accepted'
  reg_id: string
  // 被重启的就是当前为浏览器服务的实例：需等待恢复并重新登录
  self: boolean
}
```

- [ ] **Step 2: 中文文案**

`web/src/i18n/messages/zh-cn.ts` 的 `cluster` 块替换为：

```ts
  cluster: {
    title: '集群管理',
    desc: '集群中已注册的实例列表，每 5 秒自动刷新。心跳超时的实例会被 leader 自动清理。单机多实例（SQLite）与数据库集群均在此显示。',
    refresh: '刷新',
    leader: 'Leader',
    follower: 'Follower',
    self: '本机',
    joinedAt: '加入时间',
    heartbeatAt: '心跳时间',
    empty: '暂无集群实例',
    restart: '重启',
    restarting: '重启中',
    restartTitle: '重启实例',
    restartConfirm: '确定重启 {address} 吗？该实例上进行中的对话会被中断，约有数秒不可用。',
    restartConfirmSelf: '确定重启 {address} 吗？这是当前为你提供服务的实例：进行中的对话会被中断，重启完成后需要重新登录。',
    restartAccepted: '已向 {address} 发出重启指令',
    restartSelfWaiting: '实例重启中，完成后将跳转到登录页…',
    restartMemberGone: '该实例已不在集群中',
    restartUnsupported: '该实例以单进程模式运行，不支持重启',
    restartTimeout: '{address} 未在预期时间内恢复，请检查其日志与运行模式',
    restartSelfTimeout: '实例未在预期时间内恢复，请检查服务日志',
  },
```

- [ ] **Step 3: 英文文案**

`web/src/i18n/messages/en.ts` 的 `cluster` 块替换为：

```ts
  cluster: {
    title: 'Cluster',
    desc: 'Instances registered in the cluster, refreshed every 5 seconds. Members whose heartbeat times out are removed by the leader. Both single-host multi-instance (SQLite) and database clusters appear here.',
    refresh: 'Refresh',
    leader: 'Leader',
    follower: 'Follower',
    self: 'This node',
    joinedAt: 'Joined',
    heartbeatAt: 'Last heartbeat',
    empty: 'No cluster instances',
    restart: 'Restart',
    restarting: 'Restarting',
    restartTitle: 'Restart instance',
    restartConfirm: 'Restart {address}? Conversations in progress on it will be interrupted and it will be unavailable for a few seconds.',
    restartConfirmSelf: 'Restart {address}? This is the instance serving you now: conversations in progress will be interrupted and you will need to sign in again afterwards.',
    restartAccepted: 'Restart command sent to {address}',
    restartSelfWaiting: 'Instance restarting, you will be redirected to the sign-in page…',
    restartMemberGone: 'That instance is no longer in the cluster',
    restartUnsupported: 'That instance runs in single-process mode and cannot be restarted',
    restartTimeout: '{address} did not come back in time; check its logs and process mode',
    restartSelfTimeout: 'The instance did not come back in time; check the service logs',
  },
```

- [ ] **Step 4: 改写 ClusterPanel.vue 的 script 部分**

把 `<script setup lang="ts">` 整段替换为：

```ts
<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { Refresh, RefreshRight } from '@element-plus/icons-vue'
// ElMessageBox / ElLoading 不在 unplugin 自动导入范围内，需显式引入（含样式）
import { ElMessageBox, ElLoading } from 'element-plus'
import { api, ApiError } from '../../api/client'
import type { ClusterMemberInfo, ClusterResp, RestartResp } from '../../api/types'

const { t } = useI18n()

// 后端集群角色常量（internal/cluster/election.go）
const ROLE_LEADER = 'leader'

const members = ref<ClusterMemberInfo[]>([])
// 当前为浏览器服务的实例的 reg_id（GET /web/cluster 的 self 字段）
const selfId = ref('')
const loading = ref(false)

// 心跳每 3s 写库，面板按 5s 轮询保持展示新鲜；仅在面板挂载期间运行。
const REFRESH_INTERVAL = 5000
let timer: ReturnType<typeof setInterval> | null = null

// 重启中状态以地址（IP:PORT）为键：实例重启后 reg_id 会变而地址不变。
// 值为点击重启的时刻，用于识别"重启后新注册"的成员（created_at 晚于该时刻）。
const RESTART_TIMEOUT = 60_000
const restarting = ref<Map<string, number>>(new Map())

// 重启本机时轮询 /web/health 的参数
const SELF_POLL_INTERVAL = 1000
const SELF_POLL_TIMEOUT = 90_000
const SELF_MIN_DOWN_WAIT = 10_000

function fmtTime(ms: number): string {
  return new Date(ms).toLocaleString()
}

function isRestarting(m: ClusterMemberInfo): boolean {
  return restarting.value.has(m.address)
}

// 每次刷新后核对重启中集合：同地址出现了点击之后新注册的成员即视为已恢复；超时则放弃并提示。
function reconcileRestarting() {
  const now = Date.now()
  for (const [address, since] of restarting.value) {
    const back = members.value.some((m) => m.address === address && m.created_at > since)
    if (back) {
      restarting.value.delete(address)
    } else if (now - since > RESTART_TIMEOUT) {
      restarting.value.delete(address)
      ElNotification.warning({ title: t('cluster.restartTitle'), message: t('cluster.restartTimeout', { address }) })
    }
  }
}

// silent: 轮询刷新不显示 loading 遮罩，也不弹错误提示，避免打断阅读
async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const resp = await api.get<ClusterResp>('/web/cluster')
    members.value = resp.members || []
    selfId.value = resp.self || ''
    reconcileRestarting()
  } catch (e) {
    if (!silent) {
      const message = e instanceof ApiError || e instanceof Error ? e.message : String(e)
      ElNotification.error({ title: t('cluster.title'), message })
    }
  } finally {
    if (!silent) loading.value = false
  }
}

async function confirmRestart(m: ClusterMemberInfo) {
  const self = m.reg_id === selfId.value
  const text = self
    ? t('cluster.restartConfirmSelf', { address: m.address })
    : t('cluster.restartConfirm', { address: m.address })
  try {
    await ElMessageBox.confirm(text, t('cluster.restartTitle'), {
      confirmButtonText: t('cluster.restart'),
      cancelButtonText: t('common.cancel'),
      type: 'warning',
    })
  } catch {
    return // 取消
  }
  await doRestart(m)
}

async function doRestart(m: ClusterMemberInfo) {
  try {
    const resp = await api.post<RestartResp>(`/web/cluster/${encodeURIComponent(m.reg_id)}/restart`)
    if (resp.self) {
      await waitForSelfRestart()
      return
    }
    restarting.value.set(m.address, Date.now())
    ElNotification.success({ title: t('cluster.restartTitle'), message: t('cluster.restartAccepted', { address: m.address }) })
  } catch (e) {
    if (e instanceof ApiError && e.code === 'member_not_found') {
      ElNotification.warning({ title: t('cluster.restartTitle'), message: t('cluster.restartMemberGone') })
      void load(true)
      return
    }
    if (e instanceof ApiError && e.code === 'restart_unsupported') {
      ElNotification.warning({ title: t('cluster.restartTitle'), message: t('cluster.restartUnsupported') })
      return
    }
    const message = e instanceof ApiError || e instanceof Error ? e.message : String(e)
    ElNotification.error({ title: t('cluster.restartTitle'), message })
  }
}

// 免登录探测健康端点：只关心能否拿到 200，不走 api 封装（避免 401 拦截）。
async function healthOK(): Promise<boolean> {
  try {
    const r = await fetch('/web/health', { cache: 'no-store', credentials: 'same-origin' })
    return r.ok
  } catch {
    return false
  }
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms))
}

// 重启本机：全屏遮罩 → 等旧进程退出（至少一次失败或 10 秒）→ 等新进程连续两次健康 → 整页跳转登录。
// 登录会话在工作进程内存中，重启后必然失效；整页跳转而非路由跳转，顺带重置前端状态。
async function waitForSelfRestart() {
  const mask = ElLoading.service({ lock: true, text: t('cluster.restartSelfWaiting'), background: 'rgba(0, 0, 0, 0.6)' })
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
  const started = Date.now()
  try {
    let sawDown = false
    let okStreak = 0
    while (Date.now() - started < SELF_POLL_TIMEOUT) {
      await sleep(SELF_POLL_INTERVAL)
      const ok = await healthOK()
      if (!ok) {
        sawDown = true
        okStreak = 0
        continue
      }
      // 尚未观察到停机且未过最短等待期：可能仍是旧进程在响应，继续等
      if (!sawDown && Date.now() - started < SELF_MIN_DOWN_WAIT) continue
      okStreak++
      if (okStreak >= 2) {
        window.location.href = '/ui/login'
        return
      }
    }
    ElNotification.error({ title: t('cluster.restartTitle'), message: t('cluster.restartSelfTimeout') })
  } finally {
    mask.close()
    if (timer === null) timer = setInterval(() => void load(true), REFRESH_INTERVAL)
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => void load(true), REFRESH_INTERVAL)
})

onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})
</script>
```

- [ ] **Step 5: 改写 ClusterPanel.vue 的 template 部分**

把 `<template>` 整段替换为：

```html
<template>
  <div v-loading="loading">
    <div class="label-desc panel-desc">{{ t('cluster.desc') }}</div>
    <div class="panel-toolbar">
      <el-button size="small" text :icon="Refresh" @click="load()">{{ t('cluster.refresh') }}</el-button>
    </div>

    <div v-for="m in members" :key="m.reg_id" class="list-item">
      <div class="item-header">
        <span class="member-addr mono">{{ m.address }}</span>
        <el-tag v-if="m.role === ROLE_LEADER" size="small" type="success" effect="light" class="role-tag">
          {{ t('cluster.leader') }}
        </el-tag>
        <el-tag v-else size="small" effect="plain" round class="role-tag">
          {{ t('cluster.follower') }}
        </el-tag>
        <el-tag v-if="m.reg_id === selfId" size="small" type="info" effect="plain" round class="role-tag">
          {{ t('cluster.self') }}
        </el-tag>
        <span class="header-spacer"></span>
        <el-tag v-if="isRestarting(m)" size="small" type="warning" effect="light" round class="role-tag">
          {{ t('cluster.restarting') }}
        </el-tag>
        <el-tooltip v-else :content="t('cluster.restart')" :show-after="200" placement="top">
          <el-button size="small" text :icon="RefreshRight" class="restart-btn" @click="confirmRestart(m)" />
        </el-tooltip>
      </div>
      <div class="item-meta">
        <span>{{ t('cluster.joinedAt') }}: {{ fmtTime(m.created_at) }}</span>
        <span>{{ t('cluster.heartbeatAt') }}: {{ fmtTime(m.heartbeat_at) }}</span>
        <span>PID: {{ m.pid }}</span>
      </div>
    </div>

    <el-empty v-if="!loading && !members.length" :description="t('cluster.empty')" :image-size="60" />
  </div>
</template>
```

- [ ] **Step 6: 追加样式**

在 `<style scoped>` 的 `.role-tag` 规则之后追加：

```css
/* 把重启按钮/重启中标签推到卡片标题行最右侧 */
.header-spacer {
  flex: 1;
}

.restart-btn {
  padding: 4px 6px;
}
```

- [ ] **Step 7: 类型检查与构建前端**

Run: `cd web && npm run build 2>&1 | tail -15`
Expected: `vue-tsc` 无类型错误，vite 输出 `✓ built in ...`，产物在 `web/dist`

- [ ] **Step 8: 重新编译后端（嵌入新前端）并手工冒烟**

Run: `cd .. && go build -o dist/groot ./cmd/groot`

冒烟步骤（同一台机器起两个实例，SQLite 单机多实例）：

```bash
./dist/groot -p 18080 > /tmp/g1.log 2>&1 &
./dist/groot -p 18081 > /tmp/g2.log 2>&1 &
sleep 5
```

1. 浏览器打开 `http://127.0.0.1:18080/ui/`，登录，进入 **设置 → 集群管理**。
   预期：两张卡片；18080 那张有「本机」标签；每张卡片右侧有重启图标；元信息含 PID。
2. 点击 18081 卡片的重启 → 确认。
   预期：提示「已向 … 发出重启指令」；卡片显示「重启中」；`/tmp/g2.log` 中出现 `收到重启指令`、`开始关闭 … reason=restart`、`进程退出 … exit_code=3`、`[groot-supervisor] … 工作进程请求重启`、`已拉起工作进程`；约 5～10 秒内卡片恢复正常，PID 变化。
3. 点击 18080 卡片（本机）的重启 → 确认。
   预期：全屏遮罩「实例重启中…」；数秒后整页跳到登录页；重新登录后集群面板中 18080 的 PID 已变化。
4. `kill -TERM` 两个监督进程收尾：

```bash
pkill -TERM -f 'dist/groot -p 1808' ; sleep 3 ; pgrep -fl 'dist/groot' || echo "已全部退出"
```

---

## Task 12: 文档对齐——README、测试用例汇总、设计文档

**Files:**
- Modify: `README.md:343,309-313,740,756,1169-1176,1200-1240,2258-2270`
- Modify: `tests/TEST_CASES.md:95-107`
- Modify: `docs/superpowers/specs/2026-09-22-instance-restart-design.md:253-272`

- [ ] **Step 1: README 功能表（第 343 行）**

把「集群管理」那一行替换为：

```markdown
| 集群管理 | 查看集群成员列表（地址、角色、进程 PID、心跳时间，Leader 排首位，当前实例标「本机」）并可重启指定实例；SQLite 单机多实例与 MySQL/PostgreSQL 集群均适用 |
```

- [ ] **Step 2: README 3.2 启动服务（第 309-313 行）**

替换为：

```markdown
### 3.2 启动服务

```bash
groot
```

`groot` 以「监督进程 + 工作进程」方式运行：监督进程只负责拉起工作进程，业务全部在工作进程中执行。工作进程异常退出时监督进程会自动重新拉起，并支持在 Web 界面中重启实例。`ps` 中会看到两个 `groot` 进程，属正常现象。调试或由容器运行时托管重启的场景可用 `groot --single-process` 以单进程运行（该模式不支持 Web 重启）。
```

- [ ] **Step 3: README 4.7 数据库说明（第 740 行）**

把「MySQL/PostgreSQL 模式下，多实例共享同一数据库即组成集群……」那一条替换为：

```markdown
- 多实例共享同一数据库即组成集群（SQLite 模式下为同一台机器上的多个实例，MySQL/PostgreSQL 模式下可跨主机），自动进行 Leader 选举（Leader 负责定时任务调度）；成员状态可在 Web 界面 **设置 → 集群管理** 中查看，并可在该面板重启指定实例
```

- [ ] **Step 4: README 4.7.1 配置同步说明（第 756 行）**

把「变更 `config.yaml`、`mcp/`、`subagents/` 下的内容需重启服务才生效，拉取后对话框会提示。」改为：

```markdown
变更 `config.yaml`、`mcp/`、`subagents/` 下的内容需重启服务才生效，拉取后对话框会提示；可在 **设置 → 集群管理** 中直接重启对应实例。
```

- [ ] **Step 5: README 6.2 启动服务（第 1169-1176 行）**

替换为：

```markdown
### 6.2 启动服务（groot）

启动 Groot AI Agent 服务。默认以「监督进程 + 工作进程」双进程运行，见 [3.2 启动服务](#32-启动服务)。

```bash
groot                      # 使用默认配置启动
groot -p 9090              # 指定端口启动
groot --single-process     # 单进程运行（无监督进程，不支持 Web 重启）
```

**退出码约定**（供 systemd / 容器编排参考）：

| 进程 | 退出码 | 含义 |
|------|--------|------|
| 监督进程 | 0 | 收到 SIGINT/SIGTERM 后工作进程正常退出 |
| 监督进程 | 1 | 工作进程连续快速失败达 10 次后放弃，或关闭时工作进程异常 |
| 工作进程 | 3 | 请求重启（内部约定，由监督进程处理） |

systemd 单元建议使用默认 `KillMode=control-group`，`Restart=on-failure` 只在监督进程放弃时介入。
```

- [ ] **Step 6: README 6.4 status 输出示例（第 1200-1240 行）**

在「输出示例（实例运行中）」代码块的 `端口:      8080` 行之后加两行：

```
进程模式:  监督进程 + 工作进程（支持重启）
PID:       12345
```

- [ ] **Step 7: README FAQ Q12（第 2258-2270 行）**

在「**需要重启的配置：**」列表之后、「**不需要重启的配置：**」之前插入：

```markdown
**如何重启：** 登录 Web 界面，进入 **设置 → 集群管理**，点击目标实例卡片右侧的重启按钮并确认。重启本机时页面会等待实例恢复后跳转登录页。也可以在服务器上向监督进程发送 SIGTERM 后重新执行 `groot`。
```

- [ ] **Step 8: 登记单元测试（tests/TEST_CASES.md）**

在 1.1 节末尾「**数据库迁移**」表之后、`---` 之前插入：

```markdown
**进程生命周期与实例重启** (`internal/lifecycle/`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestDetectRole | 环境变量 / --single-process / 默认 → Worker / Single / Supervisor |
| TestRole_ProcessMode | 角色到 supervised / single / supervisor 字符串 |
| TestRole_RestartSupported | 只有 Worker 支持重启 |
| TestController_FirstReasonWins | 第一个停止原因生效，后续忽略 |
| TestController_RestartReason | RequestRestart → ReasonRestart |
| TestController_NotStoppingInitially | 初始无停止原因 |
| TestController_SingleRejectsRestart | 单进程模式拒绝重启且不改变状态 |
| TestReason_ExitCode | signal / supervisor_closed → 0，restart → 3 |
| TestReason_String | 原因字符串 |
| TestWatchStdin_TriggersOnEOF | 写端关闭触发回调，写入数据不触发 |
| TestWatchStdin_TriggersOnClosedReader | 读端关闭同样触发 |
| TestSupervisor_RestartsOnExitRestart | 子进程 exit 3 后被重新拉起 |
| TestSupervisor_ExitOKStopsLoop | exit 0 后监督循环结束 |
| TestSupervisor_GivesUpAfterMaxFastFails | 连续快速失败达上限放弃并返回 1 |
| TestSupervisor_LongRunResetsFastFailCounter | 存活超过窗口后计数归零 |
| TestSupervisor_CancelClosesPipeAndChildExits | 取消后关闭管道，子进程优雅退出，不再拉起 |
| TestSupervisor_KillsHungChildAfterStopTimeout | 超时后强制 Kill，视为异常 |
| TestSupervisor_SetsSupervisedEnv | 子进程能看到 GROOT_SUPERVISED=1 |
| TestSupervisor_SpawnFailureCountsAsFastFail | 可执行文件不存在按快速失败退避后放弃 |
| TestBackoff_Delay | 默认退避序列 1/2/4/8/16/30 |
| TestExitCodeOf | Wait 错误到退出码 |
| TestClusterHandler_FreshMessageTriggersRestart | 先返回成功，延迟后触发重启 |
| TestClusterHandler_IgnoresMessageOlderThanStart | 早于进程启动的指令被忽略 |
| TestClusterHandler_SingleModeRejects | 单进程模式返回 ErrRestartUnsupported |
| TestClusterHandler_DuplicateTriggersOnce | 重复指令只触发一次 |
| TestClusterHandler_UnknownTypeIsError | 未知类型报错 |
| TestClusterHandler_NilLoggerDoesNotPanic | nil logger 兜底 |

**集群 API：Self 字段与重启端点** (`internal/api/handler/cluster_test.go`) 追加：

| 测试函数 | 测试内容 |
|---------|---------|
| TestClusterHandler_SelfField | 响应含本机 reg_id，未注册时为空串 |
| TestRestart_UnknownMember404 | 未知 reg_id → 404 member_not_found，不发消息 |
| TestRestart_SelfUnsupported409 | 本机单进程模式 → 409 restart_unsupported |
| TestRestart_OtherMemberSendsPointToPoint | 202；消息类型 / 模块 / 目标 / 优先级 / Payload / 有效期正确 |
| TestRestart_SelfSupported202 | 本机重启同样走消息投递，self=true |
| TestRestart_SenderError500 | 发送失败 → 500，不泄漏底层错误 |
| TestRestart_NoSender500 | 未挂接消息服务 → 500 |
| TestRestart_NilRepo404 | 未启用集群 → 404 |

**健康检查与 status 命令** 追加：

| 测试函数 | 测试内容 |
|---------|---------|
| TestEnvironmentInfo（`internal/api/handler/health_test.go`） | environment 含 pid / process_mode |
| TestPrintStatusOutput_ProcessInfo（`internal/cmd/status_test.go`） | status 输出进程模式与 PID |
| TestProcessModeLabel（`internal/cmd/status_test.go`） | 模式字符串到中文说明 |
```

- [ ] **Step 9: 设计文档代码组织节对齐**

`docs/superpowers/specs/2026-09-22-instance-restart-design.md` 1.11 节的 `internal/lifecycle/` 列表在 `controller.go` 之前加一行：

```
    role.go                            Role 枚举、DetectRole、ProcessMode、RestartSupported
```

并在 1.5.2 节表格的 `Payload` 行改为：

```
| Payload | `{"reason":"web","requested_by":"<登录用户系统编号>"}` |
```

同时在「二、迭代说明」的 2.1 末尾追加一条：

```markdown
- 对齐：实施计划（`docs/superpowers/plans/2026-09-22-instance-restart.md`）把 Payload 的 `requested_by` 定为登录用户系统编号而非用户名，避免 ClusterHandler 增加 UserRepo 依赖；`NewServer` 增加 `clusterInst`、`role` 两个参数。
```

---

## Task 13: 全量验证

**Files:** 无新增

- [ ] **Step 1: 格式化与静态检查**

Run: `gofmt -l ./cmd ./internal ; go vet ./...`
Expected: gofmt 无输出；vet 无报告

- [ ] **Step 2: 全部 Go 单元测试**

Run: `go test ./... 2>&1 | grep -v '^ok\|no test files' ; echo "exit=$?"`
Expected: 只打印 `exit=1`（grep 未匹配到任何 FAIL 行）。若有 FAIL，先修复再继续。

- [ ] **Step 3: 完整构建**

Run: `make build 2>&1 | tail -5`
Expected: 前端构建与 Go 编译均成功，`dist/groot` 更新

- [ ] **Step 4: 三平台交叉编译确认（管道与信号代码跨平台）**

Run: `GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/groot && GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/groot && echo cross-ok`
Expected: `cross-ok`

- [ ] **Step 5: 最终冒烟（重复 Task 10 Step 5 与 Task 11 Step 8）**

按前述步骤跑一遍双进程启动、Web 重启他机、Web 重启本机、SIGTERM 收尾。全部符合预期后，向用户报告完成并等待"提交"指令；**不要自行 commit**。

---

## 自查记录

**Spec 覆盖：**

| 设计文档节 | 对应任务 |
|---|---|
| 1.3.1-1.3.2 角色划分与判定 | Task 1（DetectRole）、Task 10（分派） |
| 1.3.3 退出码约定 | Task 2（ExitOK/ExitRestart）、Task 4（Run 按码分派） |
| 1.3.4 管道关闭协议 | Task 3（WatchStdin）、Task 4（runOnce 关闭写端 + 35 秒 Kill）、Task 10（Worker 接 WatchStdin） |
| 1.3.5 退避与放弃 | Task 4（Backoff / DefaultBackoff / MinInterval） |
| 1.3.6 监督进程输出 | Task 4（logf 前缀 `[groot-supervisor]`） |
| 1.4 生命周期控制器 | Task 2、Task 10（三种来源接入、显式 Sync/Close/os.Exit） |
| 1.5.1 Web 端点 | Task 6（Restart、404/409/500/202、Self 字段）、Task 8（路由） |
| 1.5.2 集群消息字段 | Task 6（Type/Target/Module/Payload/Priority/TTL） |
| 1.5.3 消息处理器 | Task 5 |
| 1.5.4 防死循环 | 第 1 层由现有 Leave() 删注册行保证；第 2 层 Task 5 延迟触发；第 3 层 Task 5 startedAt 比较 |
| 1.6 前端交互 | Task 11 |
| 1.7 状态可见性 | Task 7（health）、Task 9（status） |
| 1.8 命令行 | Task 10（--single-process、帮助） |
| 1.10 安全 | 端点在 /web 分组（Task 8）；只接受成员表 reg_id（Task 6）；requested_by 入 Payload（Task 6）；os.Executable 绝对路径（Task 10） |
| 1.12 测试策略 | Task 1-9 各自测试；系统测试留给用户 |
| 1.13 README | Task 12 |

**类型一致性核对：** `lifecycle.Role.ProcessMode()/RestartSupported()`（T1）→ T8/T10；`Controller.RequestStop/RequestRestart/Done/Reason/Stopping/RestartSupported`（T2）→ T5/T10；`WatchStdin(io.Reader, func())`（T3）→ T10；`NewSupervisor(Options).Run(ctx) int`（T4）→ T10；`lifecycle.ModuleName/MessageTypeRestart/NewClusterHandler(ctrl, startedAt, log)`（T5）→ T6/T10；`handler.RestartSender`、`NewClusterHandler(members, selfID, sender, restartSupported, log)`（T6）→ T8；`NewHealthHandler(..., processMode)`（T7）→ T8；`NewServer(..., clusterInst, role)`（T8）→ T10；`types.ClusterResponse.Self`、`types.RestartResponse`（T6）→ T11 的 `ClusterResp.self`、`RestartResp`。
