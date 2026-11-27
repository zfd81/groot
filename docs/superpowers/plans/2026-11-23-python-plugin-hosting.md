# Python 插件宿主与 asr-router 插件 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Groot 托管一个由它拉起与监督的 Python 插件进程，把 `plugins/<名>/` 下的插件以 `/plugins/<名>` 暴露，并实现第一个插件 asr-router（FireRedLID MLX 语种识别）。

**Architecture:** Go 侧新增 `internal/plugin`，负责发现、全局配置、SDK 解压、子进程监督、反向代理、权限解析与状态汇总；Python 侧新增嵌入式 SDK `groot_plugin_sdk`，作为进程启动器加载各插件的 `handle` 并注册到唯一的 FastAPI 应用；认证复用现有 `AuthMiddleware`，代理前剥离 `X-Groot-*` 并注入内部令牌与调用方身份。

**Tech Stack:** Go 1.26 + Hertz v0.10.4 + `net/http` 客户端；Python 3 + FastAPI + uvicorn；asr-router 使用 MLX INT8 版 FireRedLID。

**Spec:** `docs/superpowers/specs/2026-11-23-python-plugin-hosting-design.md`

## Global Constraints

- 一律直接在 `master` 工作区开发，不创建分支或 worktree。
- 不自动提交；每个任务结束用悬空快照对象记录工作区状态，全部改动留在工作区。
- 进度账本写在 `.superpowers/sdd/python-plugin-hosting/progress.md`。
- Python 进程每个 Groot 实例一个，绑定 `127.0.0.1:0`，不得绑定 `0.0.0.0`。
- 插件对外路径固定为 `/plugins/<插件名>`，进程内路径固定为 `/<插件名>`，不接受下级路径。
- 插件入口固定为 `app/main.py` 中的 `handle`；可选 `METHODS`、`startup`、`shutdown`。
- SDK 版本固定为 `1.0.0`，嵌入路径 `{GROOT_HOME}/plugins/.sdk/1.0.0/`。
- 默认 `startup_timeout: 180s`、`request_timeout: 120s`、停止等待 `10s`。
- 令牌经 stdin 第一行传递，插件清单经 stdin 第二行传递；令牌每次启动重新生成，不写 argv、环境变量、磁盘与日志。
- 代理入口无条件删除客户端 `X-Groot-*`，再注入内部令牌与身份头。
- `asr-router` 的模型为 `aufklarer/FireRedLID-887M-MLX-8bit`，revision `1ae1296262c51cfd38de0f134011e6fa4b657543`；v1 只在 Apple Silicon 的 macOS 上可运行。
- 音频时长 1.5–35 秒、上传上限 50MB；音频解码在内存中完成，不落盘。
- 单元测试：Go 用 `go test ./internal/... -v`；Python 用 `pytest tests/plugins/... -v`，测试依赖见 `tests/plugins/requirements.txt`。
- 目录规范变更（`plugins/`、`tests/plugins/`）同步写入 `CLAUDE.md`。

## Review Focus

- 大文件上传（如 100MB）经 `/plugins/asr-router`：请求体必须边读边转发，不能整体读入内存；插件按 50MB 拒绝并返回 413 信封。测试落在 Task 5 与 Task 11。
- 客户端伪造 `X-Groot-*`：代理必须全部删除并用真实值覆盖，`X-Groot-Internal-Token` 不能透传客户端值。测试落在 Task 5。
- Python 插件启动超时或反复崩溃：状态在 `starting`/`running`/`failed` 间正确迁移，未就绪请求返回 503，连续 10 次快速失败后停止重启。测试落在 Task 4。
- 一个插件 `startup` 抛异常：其余插件仍加载，进程照常就绪，失败项 503 且状态带原因。测试落在 Task 10。
- 客户端中途断开：上游请求被取消，不继续推理、不泄漏连接。测试落在 Task 5。

---

### Task 1: 全局配置与插件模型

**Files:**
- Create: `internal/plugin/config.go`
- Create: `internal/plugin/config_test.go`

**Interfaces:**
- Consumes: `internal/logger.Logger`（仅由调用方使用）。
- Produces:
  - `type GlobalConfig struct { StartupTimeout time.Duration; RequestTimeout time.Duration; Permission map[string]string }`
  - `func DefaultGlobalConfig() GlobalConfig`
  - `func LoadGlobalConfig(path string) (GlobalConfig, error)`
  - `func ValidPermission(p string) bool`
  - `type Plugin struct { Name, Dir, Permission, Invalid string }`

- [ ] **Step 1: 写失败测试**

`config_test.go` 覆盖：
- `TestLoadGlobalConfig_MissingFile`：路径不存在时返回 `DefaultGlobalConfig()`，`StartupTimeout == 180*time.Second`、`RequestTimeout == 120*time.Second`，error 为 nil。
- `TestLoadGlobalConfig_ExplicitValues`：写入 `startup_timeout: 300s`、`request_timeout: 60s`、`permission: {asr-router: chat}`，断言三项。
- `TestLoadGlobalConfig_UnknownField`：写入 `unknown: 1`，断言返回 error 且配置等于默认值。
- `TestLoadGlobalConfig_InvalidDuration`：写入 `startup_timeout: 0s`，断言 error。
- `TestValidPermission`：`chat`、`status`、`detail`、`history`、`delete`、`session`、`schedule`、`all` 为 true；`Chat`、`foo`、空串为 false。
- `TestLoadGlobalConfig_KeepsUnknownPermissionValue`：写入 `permission: {asr-router: nope}`，断言加载成功、值原样保留（由 Discover 回退为 `all` 并记警告）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/plugin/... -run TestLoadGlobalConfig -v`
Expected: FAIL，`undefined: LoadGlobalConfig`。

- [ ] **Step 3: 实现配置加载**

`config.go` 用 `gopkg.in/yaml.v3`，`Decoder.KnownFields(true)`。缺失文件（`os.IsNotExist`）返回默认值与 nil。解析或校验失败时返回 `DefaultGlobalConfig(), err`。校验规则：两个超时都必须大于 0；未知键视为错误。`permission` 的值不在已知集合时**不**让整份配置失败，而是原样保留，由 Discover 对该插件回退为 `all` 并记录警告。`ValidPermission` 的集合为 `chat/status/detail/history/delete/session/schedule/all`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/plugin/... -run TestLoadGlobalConfig -v && go test ./internal/plugin/... -run TestValidPermission -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

```bash
cd /Users/bonc/workspace/groot
snap=$(mktemp -u /tmp/groot-index.XXXXXX)
GIT_INDEX_FILE="$snap" git read-tree HEAD
GIT_INDEX_FILE="$snap" git add -A
tree=$(GIT_INDEX_FILE="$snap" git write-tree)
git commit-tree "$tree" -p HEAD -m "snapshot: task1 plugin config"
rm -f "$snap"
```

---

### Task 2: 插件目录发现

**Files:**
- Create: `internal/plugin/discover.go`
- Create: `internal/plugin/discover_test.go`

**Interfaces:**
- Consumes: `Plugin`、`GlobalConfig`（Task 1）。
- Produces:
  - `func Discover(pluginsDir string, cfg GlobalConfig) []Plugin`
  - `func InterpreterPath(pluginsDir string) string`（Linux/macOS 为 `<pluginsDir>/.venv/bin/python`，Windows 为 `<pluginsDir>\.venv\Scripts\python.exe`）
  - 返回结果按 `Name` 升序；每个目录一项，`Invalid` 非空表示不可用并带原因。

- [ ] **Step 1: 写失败测试**

`discover_test.go` 在 `t.TempDir()` 中建目录，覆盖：
- `TestDiscover_ValidPlugin`：建 `asr-router/app/main.py`、`asr-router/requirements.txt`，断言一项、`Invalid == ""`、`Permission == "all"`。
- `TestDiscover_PermissionFromConfig`：全局配置 `{asr-router: chat}`，断言权限为 `chat`。
- `TestDiscover_MissingFiles`：建 `broken/app/`（无 `main.py`）与 `nofile`（无 requirements），断言两项 `Invalid` 非空且原因含缺少的文件名。
- `TestDiscover_IgnoreDotAndUnderscore`：建 `.sdk/app/main.py`、`_template/app/main.py`，断言结果为空。
- `TestDiscover_DuplicateAfterNormalize`：建 `asr-router` 与 `asr_router`，断言只保留一个，另一个 `Invalid` 含「重名」。
- `TestDiscover_InvalidPermissionFallsBackToAll`：全局配置 `{asr-router: nope}`，断言该项 `Permission == "all"` 且 `Invalid == ""`。
- `TestDiscover_InvalidName`：建 `ASR/app/main.py`，断言 `Invalid` 含「命名规则」。
- `TestInterpreterPath`：断言当前 OS 的路径以 `.venv` 分隔符结尾正确。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/plugin/... -run TestDiscover -v`
Expected: FAIL，`undefined: Discover`。

- [ ] **Step 3: 实现发现**

`discover.go`：`os.ReadDir` 只取直接子目录；忽略以 `.` 或 `_` 开头；用自己的 `validName`（`^[a-z0-9][a-z0-9_-]*$`）校验；缺失 `app/main.py` 或 `requirements.txt` 记为 `Invalid`；`strings.ReplaceAll(name, "-", "_")` 后去重，后者记「重名」。`Permission` 从 `cfg.Permission[name]` 取，取不到或值非法时用 `all`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/plugin/... -run TestDiscover -v && go test ./internal/plugin/... -run TestInterpreterPath -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用 Task 1 的快照命令，消息改为 `snapshot: task2 plugin discovery`。

---

### Task 3: 嵌入资源、SDK 解压与 init 布局

**Files:**
- Create: `internal/plugin/assets.go`
- Create: `internal/plugin/assets_test.go`
- Create: `internal/plugin/sdk/groot_plugin_sdk/__init__.py`（占位，Task 9 填充）
- Create: `internal/plugin/template/plugin.yaml`
- Create: `internal/plugin/template/requirements.txt`
- Create: `internal/plugin/template/_template/requirements.txt`
- Create: `internal/plugin/template/_template/app/__init__.py`
- Create: `internal/plugin/template/_template/app/main.py`

**Interfaces:**
- Consumes: 无。
- Produces:
  - `const SDKVersion = "1.0.0"`
  - `func PluginsDir(homeDir string) string`
  - `func ExtractSDK(homeDir string) (string, error)`，返回 SDK 根目录（供 `PYTHONPATH`）
  - `func EnsureLayout(homeDir string) ([]string, error)`，返回本次新建的路径

- [ ] **Step 1: 写失败测试**

`assets_test.go`：
- `TestEnsureLayout_CreatesEverything`：临时 home，断言存在 `plugins/`、`plugins/.sdk/`、`plugins/_template/app/main.py`、`plugins/plugin.yaml`、`plugins/requirements.txt`；返回值包含这些路径。
- `TestEnsureLayout_Idempotent`：先写入自定义的 `plugins/plugin.yaml`，再次调用，断言内容未被覆盖、返回值为空。
- `TestExtractSDK`：断言 `plugins/.sdk/1.0.0/groot_plugin_sdk/__init__.py` 存在；调用两次不报错、内容不变。
- `TestTemplateMain`：读取 `plugins/_template/app/main.py`，断言包含 `async def handle`、`METHODS`、`async def startup`、`async def shutdown`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/plugin/... -run 'TestEnsureLayout|TestExtractSDK|TestTemplateMain' -v`
Expected: FAIL，`undefined: EnsureLayout`。

- [ ] **Step 3: 实现嵌入与解压**

`assets.go` 使用 `//go:embed all:sdk all:template`（`all:` 前缀用于包含 `_template` 与 `__init__.py`；默认 embed 规则会排除以 `_` 或 `.` 开头的路径）。`EnsureLayout` 依次创建目录并写模板文件（`os.Stat` 存在则跳过）。`ExtractSDK` 写入 `<home>/plugins/.sdk/1.0.0.tmp-<rand>/` 后原子改名为 `1.0.0`；目标已存在则直接返回。模板 `plugin.yaml` 内容与设计文档 1.4.5 的全注释模板一致；`requirements.txt` 写 `fastapi>=0.110`、`uvicorn[standard]>=0.29`；`_template/app/main.py` 写最小可运行插件（`METHODS = ["POST"]`、`startup`/`shutdown` 打日志、`handle` 返回 `{"ok": True}`）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/plugin/... -run 'TestEnsureLayout|TestExtractSDK|TestTemplateMain' -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task3 plugin assets`。

---

### Task 4: 子进程监督与就绪握手

**Files:**
- Create: `internal/plugin/supervisor.go`
- Create: `internal/plugin/supervisor_test.go`
- Create: `internal/plugin/signal_unix.go`
- Create: `internal/plugin/signal_windows.go`

**Interfaces:**
- Consumes: `Plugin`、`GlobalConfig`、`SDKVersion`、`ExtractSDK`。
- Produces:
  - `type ProcessStatus struct { Status string `json:"status"`; PID int `json:"pid,omitempty"`; Port int `json:"port,omitempty"`; Restarts int `json:"restarts"`; StartedAt string `json:"started_at,omitempty"`; Error string `json:"error,omitempty"` }`
  - `type supervisorConfig struct { PluginsDir, Interpreter, SDKPath string; Plugins []Plugin; Config GlobalConfig; Log *logger.Logger; LogLevel string; Runner childRunner }`
  - `type childRunner interface { Start(ctx context.Context, spec launchSpec) (childProcess, error) }`
  - `type childProcess interface { Stdin() io.WriteCloser; Stdout() io.Reader; Stderr() io.Reader; Wait() error; Kill() error; Pid() int }`
  - `type launchSpec struct { Interpreter, WorkDir, SDKPath, LogLevel string; Plugins []string; Token string }`
  - `supervisorConfig` 另含可注入的 `Backoff []time.Duration`、`FastFailWindow time.Duration`、`MaxFastFails int`、`Sleep func(time.Duration)`、`Now func() time.Time`，生产值取设计文档，测试用小值加速。
  - `func newSupervisor(cfg supervisorConfig) *supervisor`
  - `func (s *supervisor) run(ctx context.Context)`
  - `func (s *supervisor) stop()`
  - `func (s *supervisor) snapshot() ProcessStatus`
  - `func (s *supervisor) pluginResults() map[string]string`
  - `func (s *supervisor) target() (port int, ok bool)`
  - `func (s *supervisor) token() string`（当前进程的内部令牌，供代理注入）

- [ ] **Step 1: 写失败测试（用假 Runner）**

`supervisor_test.go` 定义 `fakeRunner`，可配置每次启动写入的 ready JSON、退出码、等待时机。覆盖：
- `TestSupervisor_ReadyThenRunning`：ready 行 `{"port":53124,"pid":123,"sdk":"1.0.0","plugins":{"a":"ok"}}`，断言 `snapshot().Status == "running"`、`Port == 53124`、`target()` ok。
- `TestSupervisor_WritesHandshake`：断言 `launchSpec.Token` 非空且 64 个十六进制字符；`Plugins` 为传入列表。
- `TestSupervisor_SDKVersionMismatch`：ready 行 sdk 为 `0.9.0`，断言启动失败并进入重启流程。
- `TestSupervisor_StartupTimeout`：Runner 不输出 ready，把 `StartupTimeout` 设为 50ms，断言状态 `starting` 且 `Error` 含「就绪行超时」。
- `TestSupervisor_RestartBackoff`：子进程立即退出，断言 `Restarts` 递增；用可注入的 `now`/`sleep` 或短退避配置加速。
- `TestSupervisor_GivesUpAfterFastFailures`：连续 10 次快速失败后状态 `failed`，不再调用 Runner。
- `TestSupervisor_StopClosesStdin`：调用 `stop()` 后断言 fake 子进程的 stdin 被关闭。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/plugin/... -run TestSupervisor -v`
Expected: FAIL，`undefined: newSupervisor`。

- [ ] **Step 3: 实现监督循环**

真实 `execRunner` 用 `exec.CommandContext`，`ExtraFiles` 不用；stdin/stdout/stderr 均为管道。启动后写 `token+"\n"+json(plugins)+"\n"`，不关闭 stdin。扫描 stdout 行：以 `GROOT_PLUGIN_READY ` 开头的行解析 JSON、核对 `sdk == SDKVersion`，成功后记录端口/pid/results 并置 `running`；其余行与 stderr 行用 `Log.Info/Warn` 输出并带 `plugin_pid`。等待就绪受 `StartupTimeout` 约束。退出后按 1s/2s/4s/8s/16s/30s 退避重启；存活不足 60 秒计快速失败，连续 10 次后停为 `failed`。`stop()` 关闭 stdin，Linux/macOS 调 `terminate()` 发 `SIGTERM`，等待 10 秒后 `Kill()`。`signal_unix.go` 用 `syscall.SIGTERM`，`signal_windows.go` 返回 nil。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/plugin/... -run TestSupervisor -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task4 plugin supervisor`。

---

### Task 5: 反向代理

**Files:**
- Create: `internal/plugin/proxy.go`
- Create: `internal/plugin/proxy_test.go`
- Create: `internal/api/middleware/bodylimit.go`
- Create: `internal/api/middleware/bodylimit_test.go`
- Modify: `internal/api/server.go`（Hertz 流式选项 + 注册 BodyLimit）
- Modify: `internal/api/errbody/errbody.go`（`ErrBodyTooLarge` 哨兵）

**Interfaces:**
- Consumes: `ProcessStatus` 的 target 查询；`errbody`；`logger.Logger`。
- Produces:
  - `type targetFunc func() (port int, ok bool)`
  - `func newProxy(name string, target targetFunc, timeout time.Duration, log *logger.Logger, token tokenFunc) app.HandlerFunc`，`type tokenFunc func() string`
  - 内部 `func buildUpstreamRequest(...)` 与 `func copyResponse(...)` 可单测。

- [ ] **Step 1: 写失败测试**

`proxy_test.go` 用 `server.Default(server.WithStreamBody(true))` 建测试引擎，注册一个设置 `caller`/`tenant_id`/`user_id` 的测试中间件，再挂代理 handler；上游用 `httptest.NewServer`。普通用例用 `ut.PerformRequest` 驱动；`TestProxy_ClientCancel` 改用真实监听端口 + `http.Client` 的可取消请求，因为 `ut.PerformRequest` 无法暴露客户端取消。
- `TestProxy_ForwardsMethodPathQueryAndBody`：`POST /plugins/asr-router?x=1` 带 body，断言上游收到相同方法、路径 `/asr-router`、查询与 body。
- `TestProxy_StripsSpoofedHeaders`：客户端带 `X-Groot-Caller: evil`、`X-Groot-Internal-Token: evil`、`X-Groot-Tenant-Id: evil`，断言上游收到的是注入值（token 为测试值，caller 为 `alice`，tenant 为 `t1`）。
- `TestProxy_NotReady503`：target 返回 false，断言 503 且错误码 `plugin_unavailable`。
- `TestProxy_UpstreamError502`：target 指向已关闭端口，断言 502。
- `TestProxy_LargeBodyStreams`：生成 20MB body 发往一个边读边计数的上游，断言完整到达、状态 200。
- `TestProxy_MultipartBodyStreams`：发送带大文件 part 的 multipart 请求，断言上游收到的字节与 `Content-Type` 边界和原请求一致。
- `TestProxy_BodyLimit`：把 BodyLimit 设为小值，发送超过上限的流式身体，断言 413 `file_too_large` 且上游未收到完整请求。
- `TestProxy_ClientCancel`：请求 context 取消后，上游 handler 观察到 `r.Context().Done()`，断言在 1 秒内关闭。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/plugin/... -run TestProxy -v`
Expected: FAIL，`undefined: newProxy`。

- [ ] **Step 3: 实现代理**

`proxy.go` 构建 `http.NewRequestWithContext(ctx, method, "http://127.0.0.1:<port>"+path, body)`；body 优先 `rc.Request.BodyStream()`，否则 `bytes.NewReader(rc.Request.Body())`，并设置 `ContentLength`。复制请求头，跳过 `Host` 与逐跳头，删除全部 `X-Groot-*`。从 Hertz 上下文读取认证中间件写入的 `caller`、`tenant_id`、`user_id`（`rc.Get`，缺失为空串；`user_id` 为空时回退到 Web 会话的 `web_user_id`），连同插件名、本次生成的请求 ID 与子进程令牌，写入设计文档 1.5.4 的六个头。`http.Client` 的 Transport 设置 `ResponseHeaderTimeout = timeout`，`DialContext` 5 秒。响应复制状态码与头（跳过逐跳头），`rc.SetBodyStream(resp.Body, size)`，`size` 为 `ContentLength`（未知用 -1）。超时映射 504，其他错误 502，未就绪 503，均用 `errbody.Write`。`server.go` 的 `server.Default` 选项加 `server.WithStreamBody(true)`、`server.WithSenseClientDisconnection(true)` 与 `server.WithDisablePreParseMultipartForm(true)`，并全局注册 `middleware.BodyLimit(maxBodySize)`。`BodyLimit` 对已知 `Content-Length > 200MB` 直接 413 `file_too_large`；对流式身体包一层限长 reader，超过 200MB 返回 `errbody.ErrBodyTooLarge`，代理把这个哨兵映射为 413。现有 `rc.FormFile`/`rc.PostForm` 在需要时按需解析，`go test ./internal/api/...` 必须保持通过。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/plugin/... -run TestProxy -v && go test ./internal/api/... -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task5 plugin proxy`。

---

### Task 6: Manager 编排、权限解析与状态

**Files:**
- Create: `internal/plugin/manager.go`
- Create: `internal/plugin/status.go`
- Create: `internal/plugin/manager_test.go`

**Interfaces:**
- Consumes: `Discover`、`LoadGlobalConfig`、`ExtractSDK`、`newSupervisor`、`newProxy`。
- Produces:
  - `type Options struct { HomeDir string; Log *logger.Logger; LogLevel string }`
  - `func NewManager(opts Options) *Manager`
  - `func (m *Manager) Plugins() []Plugin`
  - `func (m *Manager) PluginPermission(name string) (string, bool)`
  - `func (m *Manager) Proxy(name string) app.HandlerFunc`
  - `func (m *Manager) Start(ctx context.Context)`
  - `func (m *Manager) Stop(ctx context.Context)`
  - `func (m *Manager) Status() Status`
  - `type Status struct { Process ProcessStatus `json:"process"`; Plugins []PluginStatus `json:"plugins"` }`
  - `type PluginStatus struct { Name string `json:"name"`; Status string `json:"status"`; Error string `json:"error,omitempty"` }`
  - `func (s Status) HealthStatus() string`

- [ ] **Step 1: 写失败测试**

`manager_test.go` 使用临时 home 与假 Runner，覆盖：
- `TestManager_DiscoversBeforeStart`：`NewManager` 后 `Plugins()` 含有效项，`PluginPermission("asr-router") == ("chat", true)`；未知名字返回 false。
- `TestManager_StatusPendingBeforeStart`：未启动时每个插件 `pending`，`Process.Status == "disabled"`（无插件时）或 `"starting"`。
- `TestManager_StatusAggregatesInvalid`：建一个缺 `main.py` 的目录，断言 `Status.Plugins` 含 `invalid` 且 `HealthStatus() == "degraded"`。
- `TestManager_HealthStatusHealthy`：全部 `running` 且进程 `running` 时 `healthy`；没有插件时也 `healthy`。
- `TestManager_NoPluginsDoesNotStartProcess`：`plugins` 目录为空，`Start` 后 `Process.Status == "disabled"`，Runner 未被调用。
- `TestManager_Stop`：`Start` 后 `Stop`，断言 supervisor 收到停止。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/plugin/... -run TestManager -v`
Expected: FAIL，`undefined: NewManager`。

- [ ] **Step 3: 实现 Manager**

`NewManager` 读取 `<home>/plugins/plugin.yaml`（失败用默认值并记 error）、`Discover`、保存解释器路径与全局配置，但不启动。`Start` 先判断有效插件数：为 0 时直接置 `disabled`，不 `ExtractSDK`、不拉起进程；否则 `ExtractSDK`，失败则进程状态 `failed` 并返回，成功则 `go supervisor.run(ctx)`。`Stop` 调 `supervisor.stop()` 并等待不超过 10 秒。`Status` 合并 discovery 与 supervisor：无效项 `invalid`，有效项按 ready 结果 `running`/`failed`，未就绪 `pending`；进程状态取 supervisor。没有任何插件时 `Plugins` 必须是空切片而不是 nil，保证 JSON 为 `[]`。`HealthStatus`：进程 `running` 且所有有效项 `running`，或总插件数为 0 → `healthy`，否则 `degraded`。`Proxy(name)` 返回 `newProxy(name, sup.target, cfg.RequestTimeout, log, sup.token)`；名字不合法或未注册时返回一个直接 404 的 handler。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/plugin/... -run TestManager -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task6 plugin manager`。

---

### Task 7: 认证中间件的插件权限解析

**Files:**
- Modify: `internal/api/middleware/auth.go`
- Modify: `internal/api/middleware/auth_test.go`

**Interfaces:**
- Consumes: 现有 `AuthMiddleware`。
- Produces:
  - `type PluginPermissionResolver interface { PluginPermission(name string) (string, bool) }`
  - `func (m *AuthMiddleware) SetPluginPermissionResolver(r PluginPermissionResolver)`
  - `func pluginNameFromPath(path string) (string, bool)`（仅匹配 `/plugins/<单段>`）

- [ ] **Step 1: 写失败测试**

在 `auth_test.go` 增加：
- `TestPluginNameFromPath`：`/plugins/asr-router` → ok；`/plugins/asr-router/detect`、`/plugins/`、`/chat` → false。
- `TestAuth_PluginPermissionAllowsMatch`：resolver 返回 `chat`，API Key 权限 `["chat"]`，请求 `/plugins/asr-router` 通过。
- `TestAuth_PluginPermissionRejectsMismatch`：resolver 返回 `chat`，Key 权限 `["status"]`，断言 403 且错误码 `insufficient_permissions`。
- `TestAuth_PluginPermissionNilResolverFallsBackToAll`：resolver 为 nil，Key 只有 `chat`，断言 403（默认 `all`）。
- `TestAuth_CookieBypassesPluginPermission`：Web Cookie 有效时，resolver 返回 `all`，Key 无关，断言放行。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/middleware/... -run 'TestPluginNameFromPath|TestAuth_Plugin' -v`
Expected: FAIL，`undefined: pluginNameFromPath`。

- [ ] **Step 3: 实现解析器注入**

`auth.go` 在 `AuthMiddleware` 增加 `pluginPerm PluginPermissionResolver` 字段与 setter。`Serve` 中权限计算改为：先取 `getRequiredPermission(path, method)`；若 `pluginNameFromPath` 命中且 resolver 非 nil，则用 resolver 的结果覆盖；resolver 返回的名字不存在时保留 `all`。Cookie 分支不变（不检查权限点）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/middleware/... -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task7 auth plugin permission`。

---

### Task 8: 路由注册、健康检查与状态展示

**Files:**
- Modify: `internal/api/router.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/handler/health.go`
- Modify: `internal/api/handler/health_test.go`
- Modify: `internal/cmd/status.go`
- Create: `internal/api/handler/health_plugins_test.go`

**Interfaces:**
- Consumes: `*plugin.Manager`（Task 6）、`plugin.Status`。
- Produces:
  - `handler.PluginStatusProvider interface { Status() plugin.Status }`
  - `func (h *HealthHandler) SetPluginStatusProvider(p PluginStatusProvider)`
  - `RegisterRoutes(..., pluginMgr *plugin.Manager)` 新增末尾参数
  - `api.NewServer(..., pluginMgr *plugin.Manager)` 新增末尾参数

- [ ] **Step 1: 写失败测试**

`health_plugins_test.go` 用假的 `PluginStatusProvider`：
- `TestHealth_PluginsCheck`：状态含一个 `running` 与一个 `failed`，断言 `checks["plugins"].status == "degraded"`，`info.plugins` 长度为 2，失败项带 `error`。
- `TestHealth_NoProviderOmitsPlugins`：未设置 provider 时 `checks` 不含 `plugins`。
- `TestHealth_PluginsHealthy`：全部 running，断言 `healthy`。
`status_test.go`（如无则新建）测渲染函数：给定 `Status`，输出包含 `Plugins:` 汇总行与非 `running` 明细。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handler/... -run TestHealth_Plugins -v && go test ./internal/cmd/... -run TestRenderPlugins -v`
Expected: FAIL，`undefined: SetPluginStatusProvider`。

- [ ] **Step 3: 实现集成**

`server.go` 构造 `AuthMiddleware` 后调用 `authMW.SetPluginPermissionResolver(pluginMgr)`（`pluginMgr` 为 nil 时跳过），`healthH.SetPluginStatusProvider(pluginMgr)`。用 `rg 'NewServer\(|RegisterRoutes\('` 找出全部调用点并补上新参数，测试里的调用同步更新。`RegisterRoutes` 中在 `apiGroup` 上加循环：
```go
if pluginMgr != nil {
    for _, svc := range pluginMgr.Plugins() {
        if svc.Invalid != "" { continue }
        apiGroup.Any("/plugins/"+svc.Name, pluginMgr.Proxy(svc.Name))
    }
}
```
`health.go` 的 `Serve` 在 `Checks` 中按 provider 写入 `plugins`，`Info` 为 `plugin.Status` 值。`status.go` 抽出 `renderPlugins(w io.Writer, check types.CheckInfo)`，在组件状态区调用；输出格式与设计文档 1.6.5 一致。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/... ./internal/cmd/... -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task8 routes health status`。

---

### Task 9: Python SDK 错误信封与身份依赖

**Files:**
- Create: `internal/plugin/sdk/groot_plugin_sdk/errors.py`
- Create: `internal/plugin/sdk/groot_plugin_sdk/identity.py`
- Modify: `internal/plugin/sdk/groot_plugin_sdk/__init__.py`
- Create: `tests/plugins/sdk/test_errors.py`
- Create: `tests/plugins/sdk/test_identity.py`
- Create: `tests/plugins/requirements.txt`（`pytest`、`httpx`、`numpy`、`soundfile`）

**Interfaces:**
- Produces:
  - `class APIError(Exception)`，构造 `APIError(status_code: int, message: str, type: str = "api_error", param: str | None = None, code: str | None = None)`
  - `def error_envelope(exc: APIError) -> dict`
  - `@dataclass(frozen=True) class GrootIdentity`，字段 `caller, tenant_id, user_id, request_id, plugin`
  - `async def groot_identity(request: Request) -> GrootIdentity`

- [ ] **Step 1: 写失败测试**

`test_errors.py`：
- `test_envelope_shape`：`APIError(400, "bad", type="invalid_request_error", code="unsupported_type")` 生成的 dict 等于 OpenAI 信封，`param`/`code` 缺省为 `None`。
`test_identity.py`：
- `test_identity_from_headers`：构造带六个 `X-Groot-*` 头的 Starlette 请求，断言字段逐一对应。
- `test_identity_missing_headers_are_empty_strings`：无头时字段全为空串。
- `test_identity_dependency`：通过 `fastapi.testclient.TestClient` 调一个 `Depends(groot_identity)` 的端点，断言返回值。

- [ ] **Step 2: 运行测试确认失败**

Run: `pytest tests/plugins/sdk/test_errors.py tests/plugins/sdk/test_identity.py -v`
Expected: FAIL，`ModuleNotFoundError: groot_plugin_sdk`。

- [ ] **Step 3: 实现辅助接口**

`errors.py` 用 `dataclasses` 或普通类；`error_envelope` 输出 `{"error": {"message", "type", "param", "code"}}`。`identity.py` 从 `request.headers` 读 `x-groot-caller` 等（大小写不敏感），`plugin` 取 `x-groot-plugin`。`__init__.py` 导出三者。测试通过 `PYTHONPATH=internal/plugin/sdk` 找到包。

- [ ] **Step 4: 运行测试确认通过**

Run: `PYTHONPATH=internal/plugin/sdk pytest tests/plugins/sdk/test_errors.py tests/plugins/sdk/test_identity.py -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task9 sdk helpers`。

---

### Task 10: Python SDK 启动器

**Files:**
- Create: `internal/plugin/sdk/groot_plugin_sdk/launcher.py`
- Create: `tests/plugins/sdk/test_launcher.py`
- Create: `tests/plugins/sdk/fixtures/plugin_ok/app/main.py`
- Create: `tests/plugins/sdk/fixtures/plugin_fail/app/main.py`
- Create: `tests/plugins/sdk/fixtures/plugin_ok/requirements.txt`
- Create: `tests/plugins/sdk/fixtures/plugin_fail/requirements.txt`

**Interfaces:**
- Consumes: `APIError`、`error_envelope`、`GrootIdentity`、`groot_identity`（Task 9）。
- Produces:
  - `def load_plugin(plugins_root: Path, name: str) -> LoadedPlugin`（`LoadedPlugin` 含 `name`、`methods`、`handle`、`startup`、`shutdown`）
  - `def build_app(plugins_root: Path, names: list[str], token: str) -> tuple[FastAPI, dict[str, str]]`
  - `def main() -> None`
  - `class TokenMiddleware`

- [ ] **Step 1: 写失败测试**

`test_launcher.py`：
- `test_build_app_registers_handle`：加载 `plugin_ok`，`TestClient` 请求 `/plugin_ok`，断言 200 与 `{"ok": true}`。
- `test_token_required`：无 `X-Groot-Internal-Token` 请求 `/plugin_ok` 断言 401，错误码 `invalid_api_key`；`/healthz` 无令牌返回 200。
- `test_identity_dependency`：fixture 的 `handle` 返回 `caller/tenant_id/user_id/plugin`，带令牌与身份头请求，断言对应值。
- `test_apierror_envelope`：fixture 抛 `APIError(400, ...)`，断言响应体为标准信封。
- `test_validation_error_envelope`：缺必填字段时 400，`param` 为字段名。
- `test_startup_failure_isolated`：同时加载 `plugin_ok` 与 `plugin_fail`（`startup` 抛异常），断言进程可用、`/plugin_ok` 200、`/plugin_fail` 503 且错误码 `plugin_unavailable`，`results["plugin_fail"]` 以 `error:` 开头。
- `test_shutdown_reverse_order`：两个 fixture 记录调用顺序，断言 `shutdown` 逆序。
- `test_unknown_method_405`：`GET /plugin_ok` 返回 405 信封。

- [ ] **Step 2: 运行测试确认失败**

Run: `PYTHONPATH=internal/plugin/sdk pytest tests/plugins/sdk/test_launcher.py -v`
Expected: FAIL，`ModuleNotFoundError` 或 `undefined: build_app`。

- [ ] **Step 3: 实现启动器**

`launcher.py`：
- `read_handshake()` 从 `sys.stdin.buffer` 读两行；`--dev` 时改为扫描当前目录并跳过令牌。
- `load_plugin` 先为 `groot_plugins` 与 `groot_plugins.<name.replace("-","_")>` 建立空的命名空间包（`types.ModuleType` + `__path__` + `sys.modules`），再用 `importlib.util.spec_from_file_location` 把 `<root>/<name>/app/__init__.py` 注册为 `groot_plugins.<name.replace("-","_")>.app`，最后 `import_module` 加载 `.main`；取 `handle`、`METHODS`（默认 `["POST"]`）、`startup`、`shutdown`。相对导入因此可用，且不同插件的 `app` 包互不冲突。
- `build_app` 创建 `FastAPI(lifespan=..., docs_url=None, redoc_url=None, openapi_url=None)`，关闭自动文档端点。加载完成后立即为每个插件 `add_api_route(f"/{name}", guarded_handle(svc), methods=svc.methods)`，路由注册不依赖启动结果。`guarded_handle` 在 `svc.ready` 为 false 时抛 `APIError(503, ..., code="plugin_unavailable")`。lifespan 中按顺序调用 `startup`（用 `maybe_await` 兼容协程与普通函数），成功才置 `ready=True`，失败记入 `results` 并保持未就绪；退出时逆序 `shutdown`。添加 `TokenMiddleware`、`/healthz`、四类异常处理器（`HTTPException` 用 `starlette.exceptions.HTTPException` 注册，覆盖 FastAPI 与 Starlette 的 404/405）。
- `main()` 配置日志（`GROOT_LOG_LEVEL`，根 logger 到 stdout，格式含 `%(name)s`），绑定 `127.0.0.1:0`，以已绑定 socket 启动 `uvicorn.Server`，等 `server.started` 后打印 `GROOT_PLUGIN_READY {...}`，再等 `should_exit`。启动后台线程读 stdin，EOF 时置 `should_exit = True`。
- `TokenMiddleware` 用 `hmac.compare_digest`；`/healthz` 放行。

- [ ] **Step 4: 运行测试确认通过**

Run: `PYTHONPATH=internal/plugin/sdk pytest tests/plugins/sdk/test_launcher.py -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task10 sdk launcher`。

---

### Task 11: asr-router 音频解码与标签归一化

**Files:**
- Create: `plugins/asr-router/app/__init__.py`
- Create: `plugins/asr-router/app/audio.py`
- Create: `plugins/asr-router/app/labels.py`
- Create: `tests/plugins/asr-router/test_audio.py`
- Create: `tests/plugins/asr-router/test_labels.py`

**Interfaces:**
- Produces:
  - `MAX_BYTES = 50 * 1024 * 1024`
  - `MIN_SECONDS = 1.5`、`MAX_SECONDS = 35.0`
  - `def decode_to_pcm(data: bytes) -> tuple[np.ndarray, int]`（16kHz 单声道 float32；超时/过大抛 `APIError`）
  - `def normalize_label(text: str) -> tuple[str, str | None]`（返回 ISO 639-1 与可选方言）
  - `DIALECTS: set[str]`

- [ ] **Step 1: 写失败测试**

`test_labels.py`：
- `test_zh_mandarin` → `("zh", "mandarin")`
- `test_en` → `("en", None)`
- `test_yue` → `("zh", "yue")`
- `test_unknown_token` → `("und", None)`
- `test_three_tokens_uses_first_two` → `("zh", "mandarin")`

`test_audio.py` 在模块级 `pytest.importorskip` 之外，用 `shutil.which("ffmpeg")` 为空时 `pytest.skip(..., allow_module_level=True)`，避免未装 ffmpeg 的机器整组失败。
- `test_decode_wav_16k`：用 `numpy` + `soundfile` 生成 2 秒 16k 正弦波 WAV 字节，断采样率 16000、长度约 32000、dtype float32。
- `test_decode_resamples_8k`：生成 8k WAV，断言输出 16k。
- `test_rejects_short`：0.5 秒，断言 `APIError.status_code == 400`、`code == "invalid_request"`。
- `test_rejects_long`：40 秒，同上。
- `test_rejects_oversize`：`b"x" * (MAX_BYTES + 1)`，断言 413、`file_too_large`。
- `test_invalid_audio`：随机字节，断言 400 `unsupported_type`（ffmpeg 失败）。

- [ ] **Step 2: 运行测试确认失败**

Run: `PYTHONPATH=internal/plugin/sdk:plugins/asr-router pytest tests/plugins/asr-router/test_audio.py tests/plugins/asr-router/test_labels.py -v`
Expected: FAIL，`ModuleNotFoundError`。

- [ ] **Step 3: 实现**

`audio.py` 优先用 `soundfile.read(io.BytesIO(data), dtype="float32", always_2d=True)` 解码 wav/flac/ogg；`soundfile` 打不开时用 `subprocess.run(["ffmpeg","-hide_banner","-loglevel","error","-i","pipe:0","-f","f32le","-ac","1","-ar","16000","pipe:1"], input=data, capture_output=True)`，失败抛 `APIError(400, "音频解码失败", type="invalid_request_error", code="unsupported_type")`。soundfile 路径用 `scipy.signal.resample_poly` 重采样到 16kHz，单声道取均值。检查字节上限与时长，错误码按设计文档。`labels.py` 的 `DIALECTS` 至少包含 `mandarin/yue/wu/xiang/xinan/north/min`；`normalize_label` 取空白分隔的前两个字段，首字段匹配 `^[a-z]{2}$` 才作为语言，否则 `und`。

- [ ] **Step 4: 运行测试确认通过**

Run: `PYTHONPATH=internal/plugin/sdk:plugins/asr-router pytest tests/plugins/asr-router/test_audio.py tests/plugins/asr-router/test_labels.py -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task11 asr-router audio labels`。

---

### Task 12: asr-router FireRedLID MLX 后端

**Files:**
- Create: `plugins/asr-router/app/lid.py`
- Create: `tests/plugins/asr-router/test_lid.py`
- Create: `plugins/asr-router/requirements.txt`
- Create: `plugins/asr-router/README.md`
- Create: `plugins/asr-router/models/.gitkeep`

**Interfaces:**
- Consumes: `normalize_label`（Task 11）。
- Produces:
  - `class LIDBackend(Protocol): def load(self) -> None; def detect(self, pcm: np.ndarray) -> list[tuple[str, str | None, float]]`（language、dialect、confidence）
  - `class FireRedLIDBackend`
  - `def create_backend() -> LIDBackend`
  - `def _run_model(self, features: np.ndarray) -> list[tuple[str, float]]`（原始标签与置信度，供单测替换）
  - `def model_dir() -> Path`（`GROOT_PLUGIN_MODELS_DIR` 或 `<plugin>/models`）
  - `def platform_supported() -> bool`

- [ ] **Step 1: 写失败测试**

`test_lid.py`：
- `test_model_dir_env`：设置 `GROOT_PLUGIN_MODELS_DIR=/tmp/x`，断言 `model_dir()` 返回该路径。
- `test_model_dir_default`：未设置时返回 `plugins/asr-router/models` 的绝对路径。
- `test_platform_supported`：在 `platform.system()/machine()` 被 monkeypatch 为 Darwin/arm64 时 true，Linux/x86_64 时 false。
- `test_load_fails_without_model`：模型目录不存在时 `load()` 抛 `RuntimeError`，消息含 `huggingface-cli download`。
- `test_detect_parses_hypotheses`：给后端注入 fake frontend（`extract_pcm` 返回任意二维数组），monkeypatch 后端内部的 `_run_model` 返回 `[("zh mandarin", 0.97), ("en", 0.02)]`，断言 `detect` 返回 `[("zh", "mandarin", 0.97), ("en", None, 0.02)]`。

- [ ] **Step 2: 运行测试确认失败**

Run: `PYTHONPATH=internal/plugin/sdk:plugins/asr-router pytest tests/plugins/asr-router/test_lid.py -v`
Expected: FAIL，`ModuleNotFoundError`。

- [ ] **Step 3: 实现后端**

`lid.py`：
- `model_dir()` 读环境变量，否则 `<Path(__file__).parent.parent / "models">`。
- `platform_supported()` 仅在 `Darwin` + `arm64` 为 true。
- `load()`：`mlx`、`frontend`、`mlx_model` 均在方法内延迟导入，保证无 MLX 环境仍可导入本模块与跑单测。检查平台；检查必需文件 `config.json/cmvn.ark/dict.txt/model.safetensors/frontend.py/mlx_model.py`；用 `importlib.util.spec_from_file_location("_groot_asr_router_firered_frontend", ...)` 与 `_groot_asr_router_firered_mlx_model` 加载两个模块（扁平唯一名，避免与插件包结构或其他插件冲突）；`weights = dict(mx.load(str(dir/"model.safetensors")))`；`tokens = mlx_model.FireRedTokenDictionary.load(dir/"dict.txt")`；`FireRedLIDModel(weights, tokens)`；`FireRedFrontend(dir/"cmvn.ark")`。
- `detect(pcm)`：`features = frontend.extract_pcm(pcm, 16000)`；`hypotheses = model.identify_features(mx.array(features[None]), beam_size=3, nbest=3)[0]`；返回 `[(language, dialect, float(confidence)) for h in hypotheses]`，其中 `(language, dialect) = normalize_label(h.text)`，按置信度降序。不使用跨请求共享的 `last_labels`。
- 用 `threading.Lock` 保证同一时间一个推理。
- `requirements.txt` 固定为模型 README 的版本：`mlx==0.32.0`、`numpy==1.26.4`、`scipy==1.14.1`、`soundfile==0.13.1`、`kaldiio==2.18.1`、`kaldi-native-fbank==1.22.3`。
- `README.md` 写模型下载命令、`GROOT_PLUGIN_MODELS_DIR` 用法、Apple Silicon 限制、`ffmpeg` 前置依赖。

- [ ] **Step 4: 运行测试确认通过**

Run: `PYTHONPATH=internal/plugin/sdk:plugins/asr-router pytest tests/plugins/asr-router/test_lid.py -v`
Expected: PASS（真实模型加载测试在 `platform_supported()` 为 false 或无模型时 skip）。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task12 asr-router lid backend`。

---

### Task 13: asr-router 处理函数与接口

**Files:**
- Create: `plugins/asr-router/app/main.py`
- Create: `tests/plugins/asr-router/test_main.py`

**Interfaces:**
- Consumes: `decode_to_pcm`、`create_backend`、`normalize_label`、`GrootIdentity`、`APIError`。
- Produces: `METHODS = ["POST"]`、`async def startup()`、`async def shutdown()`、`async def handle(file: UploadFile = File(...)) -> dict`。

- [ ] **Step 1: 写失败测试**

`test_main.py` 用 `TestClient` 与 fake backend：
- `test_detect_success`：注入 fake backend 返回 `[("zh", "mandarin", 0.97), ("en", None, 0.02)]`，上传 2 秒 WAV，断言 200、`language == "zh"`、`dialect == "mandarin"`、`alternatives` 长度为 1、`duration_ms` 约 2000。
- `test_detect_und`：fake 返回 `[("und", None, 0.1)]`，断言 `language == "und"`、无 `dialect`。
- `test_missing_file`：无 `file` 字段，断言 400 信封。
- `test_backend_not_ready`：未调用 `startup` 时 503 `plugin_unavailable`。
- `test_identity_passed`：`handle` 依赖 `groot_identity`，断言不抛异常（身份由 SDK 提供，业务不校验）。

- [ ] **Step 2: 运行测试确认失败**

Run: `PYTHONPATH=internal/plugin/sdk:plugins/asr-router pytest tests/plugins/asr-router/test_main.py -v`
Expected: FAIL，`ModuleNotFoundError`。

- [ ] **Step 3: 实现**

`main.py` 模块级 `_backend`、`_ready`。`startup` 调 `create_backend()`、`.load()`、置 `_ready`。`shutdown` 调后端可选 `close()`。`handle`：未 ready 抛 `APIError(503, ..., code="plugin_unavailable")`；`data = await file.read()`；`pcm, _ = decode_to_pcm(data)`；`results = await asyncio.to_thread(_backend.detect, pcm)` 返回 `(language, dialect, confidence)` 三元组；主结果取第一项，`alternatives` 取第 2、3 项；`duration_ms = round(len(pcm)/16000*1000)`。响应字段按设计文档 1.10.1。

- [ ] **Step 4: 运行测试确认通过**

Run: `PYTHONPATH=internal/plugin/sdk:plugins/asr-router pytest tests/plugins/asr-router/test_main.py -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task13 asr-router handle`。

---

### Task 14: init 命令、main.go 接线、CLAUDE.md 与端到端

**Files:**
- Modify: `internal/cmd/init.go`
- Modify: `cmd/groot/main.go`
- Modify: `CLAUDE.md`
- Create: `tests/python/test_python_plugins.py`
- Modify: `README.md`（新增 Python 插件章节）

**Interfaces:**
- Consumes: `plugin.EnsureLayout`、`plugin.NewManager`、`plugin.Options`。
- Produces: 启动时托管 Python 插件的完整链路。

- [ ] **Step 1: 写失败测试/校验**

`tests/python/test_python_plugins.py`（系统测试，用户执行）：
- `test_plugins_init_creates_layout`：在临时 `GROOT_HOME` 运行 `groot init`，断言 `plugins/`、`.sdk/`、`_template/`、`plugin.yaml`、`requirements.txt` 存在。
- `test_plugins_status_in_health`：启动 Groot（无插件），`GET /web/health` 的 `checks.plugins.status == "healthy"`、`info.plugins == []`。
- `test_unknown_plugin_404`：`GET /plugins/nope` 带 API Key，断言 404 信封。
- `test_asr_router_requires_auth`：`POST /plugins/asr-router` 无 Key，断言 401。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd tests/python && pytest test_python_plugins.py -v`
Expected: FAIL（init 未创建 plugins 目录；health 无 plugins 项）。

- [ ] **Step 3: 接线**

`internal/cmd/init.go` 的 `RunInit` 调用 `plugin.EnsureLayout(homeDir)`，把返回的新建路径逐条打印；失败返回错误。
`cmd/groot/main.go` 在 `settings.AssembleConfig` 之后、`api.NewServer` 之前创建 manager：
```go
pluginMgr := plugin.NewManager(plugin.Options{HomeDir: homeDir, Log: log, LogLevel: cfg.Logging.Level})
```
把 `pluginMgr` 传给 `api.NewServer`。在 `srv.Start()` 前 `go pluginMgr.Start(context.Background())`；在 shutdown goroutine 中 `srv.Stop(ctx)` 之后调用 `pluginMgr.Stop(ctx)`。
`CLAUDE.md` 目录结构新增 `plugins/` 与 `tests/plugins/`，测试规范新增 Python SDK 与插件单元测试的位置。
`README.md` 新增「Python 插件」章节：目录结构、创建 venv、安装依赖、模型下载、`groot init`、插件清单与权限配置。

- [ ] **Step 4: 运行 Go 全量单元测试**

Run: `go test ./internal/... -v`
Expected: PASS。

- [ ] **Step 5: 运行 Python 测试**

Run: `PYTHONPATH=internal/plugin/sdk pytest tests/plugins/ -v`
Expected: PASS（依赖未安装的用例 skip）。

- [ ] **Step 6: 记录工作区快照**

复用快照命令，消息 `snapshot: task14 wiring docs`。

---

## Self-Review

**Spec coverage:** 1.1/1.2 能力 → Task 1–14；1.3 边界 → Task 6/10；1.4 目录与契约 → Task 2/3/10/11/12/13；1.5 运行时协议 → Task 4/10；1.6 网关与权限 → Task 5/7/8；1.7 多实例 → Task 4/6；1.8 故障 → Task 4/6；1.9 SDK → Task 9/10；1.10 asr-router → Task 11/12/13；1.11 目录测试 → Task 14；1.12 非目标 → 不在代码中实现。

**Step scan:** 每个任务都有失败测试、实现、通过验证、快照四段；没有 TBD、没有「处理边界情况」这类空话。

**Type consistency:** `Plugin`、`GlobalConfig`、`ProcessStatus`、`Status`、`PluginStatus`、`PluginPermissionResolver`、`PluginStatusProvider`、`APIError`、`GrootIdentity` 在前后任务中同名同字段。

**Review Focus:** 大文件流式（Task 5/11）、头伪造（Task 5）、启动超时与崩溃循环（Task 4）、单插件失败隔离（Task 10）、客户端断开（Task 5）均有对应测试。

**Proportion:** 计划约 700 行，规格约 600 行；任务以签名与断言为主，未复述实现体。
