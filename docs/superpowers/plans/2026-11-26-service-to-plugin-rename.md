# 服务改名插件实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Groot 中支撑 Python 能力的「服务 / services」整体改名为「插件 / plugins」，覆盖设计文档、实施计划、Go 源码、Python SDK、仓库目录、测试、CLI 命令与对外接口。

**Architecture:** 分层顺序硬切。Task 1 只改 Go 包与标识符（对外字符串保持旧值，保证每步可编译、测试全绿）；Task 2 切换 Go 侧对外契约与全部用户可见文案；Task 3 切换 Python SDK 协议、插件源码、Python 测试与仓库目录；Task 4 更新文档；Task 5 全量验证。改动全部留在工作区，每个任务结束记录一个悬空快照供评审生成 diff，不执行 git commit。

**Tech Stack:** Go（`go test`、`go:embed`）、Python 3.9+/pytest、Vue 前端（本次不涉及）。

**Spec:** `docs/superpowers/specs/2026-11-26-plugin-naming-design.md`

## Global Constraints

- **不做兼容**：旧命令、旧路由、旧运行时目录、旧配置文件名、旧错误码、旧 SDK 包名、旧请求头一律删除，不留别名、不做迁移。
- **不动的「服务」**：`llm.ModelService`、`cluster.MessageService()`、`internal/webfiles/service.go`、`MessageService`，以及泛指基础设施的文案（Groot 服务、LLM 服务、MCP 服务、上游转录/语音/图片服务、Groot 服务端口）。
- **术语**：功能范围内中文一律「插件」，不出现「子服务」；英文标识一律 `plugin` / `plugins`。
- **代码风格**：`gofmt`；Go 测试命令 `go test ./internal/...`；Python 测试命令 `tests/plugins/.venv/bin/pytest tests/plugins -v -p no:cacheprovider`（Task 3 前路径为 `tests/services`）。
- **本次不涉及**：`web/`（无插件相关界面）、`examples/`（无相关引用）、`AGENTS.md`（不含本功能）。
- **Git**：直接在 `master` 工作区开发，不建分支/worktree，不执行 `git commit`。每个任务结束用下面命令记录悬空快照，hash 写入 `.superpowers/sdd/2026-11-26-service-to-plugin-rename/progress.md`：

```bash
mkdir -p .superpowers/sdd/2026-11-26-service-to-plugin-rename
idx=$(mktemp -u /tmp/groot-snap-XXXXXX)
GIT_INDEX_FILE="$idx" git read-tree HEAD
GIT_INDEX_FILE="$idx" git add -A
tree=$(GIT_INDEX_FILE="$idx" git write-tree)
snap=$(git commit-tree "$tree" -p <上一任务快照或 HEAD> -m "snapshot: task N")
rm -f "$idx"
echo "task N $snap" >> .superpowers/sdd/2026-11-26-service-to-plugin-rename/progress.md
```

评审者用 `git diff <上一任务快照> <本任务快照>` 获取精确 diff。

## Review Focus

- 通用「服务」被误改：`ModelService`、`MessageService`、`webfiles` 与泛指服务文案必须原样保留 → Task 1/2 的 diff 检查 + Task 5 的 allowlist grep。
- Go 宿主与嵌入 SDK 协议不一致：就绪行前缀、JSON 键、错误码、请求头在两侧必须同名 → Task 2（Go）与 Task 3（Python）各自的断言 + Task 5 交叉 grep。
- 嵌入资源路径失效：`go:embed`、`layoutDirs`、`layoutFiles` 改名后仍须命中 → Task 2 的 `TestEnsureLayout*`、`TestExtractSDK*`、`TestTemplateMain`。
- 旧名残留：`/services`、`groot services`、`service.yaml`、`groot_service_sdk`、`X-Groot-Service`、`checks.services`、`service_unavailable` → Task 5 的残留扫描。
- 测试只改名不改断言：改名后测试仍按旧值断言会假绿/假红 → Task 2/3 先改断言再改实现，并核对断言中的新值。

---

### Task 1: Go 包与标识符改名（对外字符串保持旧值）

**Files:**
- Move: `internal/pysvc/` → `internal/plugin/`（含 `sdk/groot_service_sdk/`、`template/`）
- Modify: `internal/plugin/*.go` 及 `*_test.go`（包声明、类型、函数、字段、局部变量、测试函数名）
- Modify: `cmd/groot/main.go`、`internal/api/router.go`、`internal/api/server.go`、`internal/api/handler/health.go`、`internal/api/handler/health_services_test.go`、`internal/api/middleware/auth.go`、`internal/api/middleware/auth_test.go`、`internal/api/errbody/errbody.go`
- Modify: `internal/cmd/services.go`、`internal/cmd/services_shell.go`、`internal/cmd/status.go`、`internal/cmd/services_test.go`、`internal/cmd/services_shell_test.go`、`internal/cmd/status_test.go`、`internal/cmd/init_test.go`
- Modify: `tests/services/conftest.py`（SDK 路径）

**Interfaces:**
- Produces: Go 包 `internal/plugin`；导出 `Plugin`、`PluginStatus`、`PluginsDir`、`Manager.Plugins() ([]Plugin)`、`Manager.PluginPermission(name string) (string, bool)`；消费方接口 `middleware.PluginPermissionResolver`、`handler.PluginStatusProvider`；`errbody.CodePluginUnavailable`。
- 保持旧值（Task 2/3 才改）：CLI 名 `services`、路由 `"/services/"`、健康检查键 `"services"`、错误码字符串 `"service_unavailable"`、就绪前缀 `"GROOT_SERVICE_READY "`、请求头值 `"X-Groot-Service"`、运行时目录 `"services"`、配置名 `"service.yaml"`、SDK 包 `groot_service_sdk`、JSON 标签 `services`。

- [ ] **Step 1: 迁移包目录并统一包声明**

```bash
git mv internal/pysvc internal/plugin
grep -rl '^package pysvc' internal/plugin | xargs sed -i '' 's/^package pysvc$/package plugin/'
```

预期：`go build ./...` 因调用方仍引用 `pysvc` 而失败，失败点即 Step 4 的清单。

- [ ] **Step 2: 改包内类型、字段、方法与测试函数名**

按 spec 1.5「Go 包与类型」表改动（本步不含字符串值）：`Service`→`Plugin`、`ServiceStatus`→`PluginStatus`、`Status.Services`→`Status.Plugins`（JSON 标签保持 `services`）、`ServicesDir`→`PluginsDir`、`Manager.Services`→`Manager.Plugins`、`Manager.ServicePermission`→`Manager.PluginPermission`、`serviceNamePattern`→`pluginNamePattern`、`serviceNameRe`→`pluginNameRe`、`reservedServiceNames`→`reservedPluginNames`、`noServiceReason`→`noPluginReason`、`mergeServiceStatus`→`mergePluginStatus`、`readyMessage.Services`→`readyMessage.Plugins`（JSON 标签保持 `services`）、`headerService`→`headerPlugin`（值保持 `X-Groot-Service`）、局部变量 `services`/`servicesDir`/`svc` → `plugins`/`pluginsDir`/`p`。测试函数名中的 `Service`/`Services` 同样改为 `Plugin`/`Plugins`。

- [ ] **Step 3: 更新 Python 测试的 SDK 导入路径**

`tests/services/conftest.py` 的 `internal / "pysvc"` 改为 `internal / "plugin"`。`assets.go` 中 `//go:embed`、`layoutDirs`/`layoutFiles` 的字符串保持旧值，留待 Task 2。

- [ ] **Step 4: 适配调用方标识符**

- `cmd/groot/main.go`：import 改 `internal/plugin`，`pysvc.NewManager`→`plugin.NewManager`，变量 `pyMgr`→`pluginMgr`。
- `internal/api/server.go`、`router.go`：参数 `pluginMgr *plugin.Manager`；`pluginMgr.Plugins()`；`SetPluginPermissionResolver`、`SetPluginStatusProvider`。
- `internal/api/handler/health.go`：`PluginStatusProvider`、字段 `pluginStatus`、`SetPluginStatusProvider`、`pluginsCheck`、`addPluginsCheck`、`plugin.Status`；检查项键仍写 `"services"`。
- `internal/api/middleware/auth.go`：`PluginPermissionResolver`、`PluginPermission`、`pluginPerm`、`SetPluginPermissionResolver`、`pluginNameFromPath`；前缀常量值仍为 `"/services/"`。
- `internal/api/errbody/errbody.go`：`CodeServiceUnavailable`→`CodePluginUnavailable`，值仍为 `"service_unavailable"`。
- `internal/cmd/services.go`：`PluginsInitFlags`、`PluginsInitOptions`、`ParsePluginsInitFlags`、`PrintPluginsHelp`、`PrintPluginsInitHelp`、`RunPluginsInit`、`plugin.` 调用、变量 `pluginsDir`。
- `internal/cmd/services_shell.go`：`PluginsShellOptions`、`RunPluginsShell`、`pluginsShellEnv`、`pluginsShell`、`pluginsShellLaunch`、`plugin.PluginsDir`、变量 `pluginsDir`。
- `internal/cmd/status.go`：`renderPlugins`、`pluginsSummary`、`pluginStatusFromInfo`、`st.Plugins`；健康检查键仍读 `"services"`。
- 同目录测试同步改名。

- [ ] **Step 5: 编译并运行 Go 测试**

Run: `go build ./... && go test ./internal/...`
Expected: 全部 PASS。

- [ ] **Step 6: 格式化与残留检查**

Run: `gofmt -l cmd internal pkg` → 无输出
Run: `grep -rn 'pysvc' --include='*.go' .` → 无输出（`tests/services/conftest.py` 已在 Step 3 处理）

- [ ] **Step 7: 记录悬空快照**

使用 Global Constraints 的命令，`-p` 用 `$(git rev-parse HEAD)`，消息 `snapshot: task1 go package identifiers`，hash 写入进度账本。

---

### Task 2: Go 对外契约与用户可见文案切换

**Files:**
- Move: `internal/cmd/services.go`→`internal/cmd/plugins.go`；`internal/cmd/services_shell.go`→`internal/cmd/plugins_shell.go`；`internal/cmd/services_test.go`→`internal/cmd/plugins_test.go`；`internal/cmd/services_shell_test.go`→`internal/cmd/plugins_shell_test.go`；`internal/api/handler/health_services_test.go`→`internal/api/handler/health_plugins_test.go`；`internal/plugin/template/service.yaml`→`internal/plugin/template/plugin.yaml`
- Modify: `cmd/groot/main.go`、`internal/cmd/{plugins,plugins_shell,status,init}.go`、`internal/api/{router.go,server.go}`、`internal/api/handler/health.go`、`internal/api/middleware/auth.go`、`internal/api/errbody/errbody.go`、`internal/plugin/{assets,discover,manager,proxy,status,supervisor,venv,config}.go` 及全部 `internal/plugin/*_test.go`、上述移动后的 cmd/api 测试、`internal/cmd/{status_test,init_test}.go`、`internal/plugin/template/{requirements.txt,_template/requirements.txt,_template/app/__init__.py,_template/app/main.py}`
- 不改：`services/asr-router/`、`tests/`、`docs/`（Task 3/4）

**Interfaces:**
- Consumes: Task 1 的 `internal/plugin` 导出名。
- Produces: 契约值 `groot plugins`、`groot plugins init`、`/plugins/{name}`、`checks.plugins`、`info.plugins`、`plugin_unavailable`、`GROOT_PLUGIN_READY `、`X-Groot-Plugin`、`{GROOT_HOME}/plugins/`、`plugins/plugin.yaml`、`Plugins:`、日志键 `plugin_pid`/`plugin`。

- [ ] **Step 1: 重命名文件**

```bash
git mv internal/cmd/services.go internal/cmd/plugins.go
git mv internal/cmd/services_shell.go internal/cmd/plugins_shell.go
git mv internal/cmd/services_test.go internal/cmd/plugins_test.go
git mv internal/cmd/services_shell_test.go internal/cmd/plugins_shell_test.go
git mv internal/api/handler/health_services_test.go internal/api/handler/health_plugins_test.go
git mv internal/plugin/template/service.yaml internal/plugin/template/plugin.yaml
```

- [ ] **Step 2: 先改测试断言为新契约（红）**

按下表把断言值改成新名（`internal/plugin/*_test.go`、`internal/api/handler/health_plugins_test.go`、`internal/api/middleware/auth_test.go`、`internal/cmd/plugins_test.go`、`internal/cmd/plugins_shell_test.go`、`internal/cmd/status_test.go`、`internal/cmd/init_test.go`）：

| 旧断言 | 新断言 |
|---|---|
| `GROOT_SERVICE_READY ` | `GROOT_PLUGIN_READY ` |
| `"services":[]` / `"services":{...}` | `"plugins":[]` / `"plugins":{...}` |
| `service.yaml` | `plugin.yaml` |
| `groot services init` / `groot services` | `groot plugins init` / `groot plugins` |
| `"/services/asr-router"` 等 | `"/plugins/asr-router"` 等 |
| `checks["services"]` / `"services":` 标签 | `checks["plugins"]` / `"Plugins:"` |
| `service_unavailable` | `plugin_unavailable` |
| `X-Groot-Service` | `X-Groot-Plugin` |
| `zap` 字段 `service_pid` / `service` | `plugin_pid` / `plugin` |
| 布局目录 `services/`、`services/.sdk`、`services/_template` | `plugins/`、`plugins/.sdk`、`plugins/_template` |

Run: `go test ./internal/...`
Expected: FAIL（实现仍是旧值）。

- [ ] **Step 3: 切换 CLI 命令与帮助文案**

`cmd/groot/main.go`：`case "services"`→`"plugins"`，`handleServicesCommand`→`handlePluginsCommand`；主帮助中 `services` 行与 `services 子命令` 段改为 `plugins`，所有「Python 服务 / 子服务」→「Python 插件 / 插件」，`groot services` / `groot services init` → `groot plugins` / `groot plugins init`。

`internal/cmd/plugins.go`、`plugins_shell.go`：帮助、成功/失败提示、错误文本里的 `services`/`Services环境`/「服务」「子服务」全部改为 `plugins`/`插件环境`/「插件」；例如 `初始化Services环境...`→`初始化插件环境...`、`进入Services环境：...`→`进入插件环境：...`、`Services: %s`→`插件环境: %s`。

`internal/cmd/init.go`：注释与提示 `groot services init`→`groot plugins init`，「Python 服务布局（services/）」→「Python 插件布局（plugins/）」。

- [ ] **Step 4: 切换路由、健康检查、错误码与日志契约**

- `internal/api/router.go`：`"/services/"+p.Name`→`"/plugins/"+p.Name`，注释同步。
- `internal/api/middleware/auth.go`：前缀 `"/services/"`→`"/plugins/"`，注释同步。
- `internal/api/handler/health.go`：`checks["services"]`→`checks["plugins"]`，注释同步；`pysvc` 注释改 `plugin`。
- `internal/api/errbody/errbody.go`：`CodePluginUnavailable = "plugin_unavailable"`。
- `internal/plugin/status.go`：`Plugins` 字段 JSON 标签 `services`→`plugins`。
- `internal/plugin/supervisor.go`：`readyPrefix = "GROOT_PLUGIN_READY "`；`readyMessage.Plugins` 标签 `services`→`plugins`；全部 `zap.Int("service_pid"`→`zap.Int("plugin_pid"`。
- `internal/plugin/manager.go`：权限警告 `zap.String("service", ...)`→`zap.String("plugin", ...)`；`service.yaml`→`plugin.yaml`。
- `internal/plugin/proxy.go`：`headerPlugin = "X-Groot-Plugin"`；注释 `/services/<name>`→`/plugins/<name>`。
- `internal/cmd/status.go`：读取 `health.Checks["plugins"]`，输出标签 `Services:`→`Plugins:`。

- [ ] **Step 5: 切换运行时目录、布局与模板**

`internal/plugin/assets.go`：`layoutDirs` 全部 `services`→`plugins`；`layoutFiles` 中 `{"template/plugin.yaml", "plugins/plugin.yaml"}`、`plugins/requirements.txt`、`plugins/_template/...`；`PluginsDir` 返回 `filepath.Join(homeDir, "plugins")`。

模板内容：`internal/plugin/template/plugin.yaml` 首行注释 `# Python 插件全局配置`；`template/requirements.txt`、`template/_template/requirements.txt`、`_template/app/__init__.py`、`_template/app/main.py` 中的「子服务」→「插件」。

`internal/plugin/config.go`、`discover.go`、`venv.go`、`manager.go`、`assets.go` 中的注释与用户可见文案「子服务」→「插件」、「Python 服务」→「Python 插件」、`groot services init`→`groot plugins init`。

- [ ] **Step 6: 运行 Go 测试**

Run: `go test ./internal/...`
Expected: 全部 PASS，特别确认 `TestEnsureLayout*`、`TestExtractSDK*`、`TestTemplateMain`、`TestProxy_*`、`TestManager_*`、`TestSupervisor_*`、`TestRunPluginsInit_*`、`TestRunPluginsShell_*`、健康检查与鉴权测试。

- [ ] **Step 7: 格式化与残留检查**

Run: `gofmt -l cmd internal pkg` → 无输出
Run: `grep -rn 'GROOT_SERVICE_READY\|X-Groot-Service\|service_unavailable\|service\.yaml\|groot services\|/services/\|checks\.services\|checks\["services"\]' cmd internal --exclude-dir=sdk` → 无输出（`internal/plugin/sdk` 下的 Python SDK 归 Task 3）

- [ ] **Step 8: 记录悬空快照**

`-p` 用 Task 1 快照，消息 `snapshot: task2 go contracts`。

---

### Task 3: Python SDK、插件源码、Python 测试与仓库目录

**Files:**
- Move: `services/`→`plugins/`；`tests/services/`→`tests/plugins/`；`internal/plugin/sdk/groot_service_sdk/`→`internal/plugin/sdk/groot_plugin_sdk/`；`tests/python/test_python_services.py`→`tests/python/test_python_plugins.py`
- Move: `tests/plugins/sdk/fixtures/svc_ok`→`plugin_ok`、`svc_b`→`plugin_b`、`svc_fail`→`plugin_fail`、`svc_import_fail`→`plugin_import_fail`、`svc_bad_route`→`plugin_bad_route`
- Modify: `internal/plugin/sdk/groot_plugin_sdk/{__init__,launcher,identity}.py`、`internal/plugin/supervisor.go`（启动模块名）、`internal/plugin/supervisor_test.go`、`plugins/asr-router/{README.md,app/main.py,app/audio.py,app/lid.py,app/labels.py}`、`tests/plugins/**`（含 `tests/plugins/sdk/fixtures/*/app/main.py`）、`tests/python/test_python_plugins.py`、`.gitignore`
- 不改：`tests/python/` 其余文件

**Interfaces:**
- Consumes: Task 2 的 Go 契约值（就绪前缀、JSON 键、错误码、请求头、目录）。
- Produces: Python 包 `groot_plugin_sdk`（`SDK_VERSION = "1.0.0"`）、命名空间 `groot_plugins.<name>.app`、`GrootIdentity.plugin`、`x-groot-plugin`；仓库 `plugins/asr-router/`、`tests/plugins/`。

- [ ] **Step 1: 迁移目录与 SDK 包名**

```bash
git mv services plugins
git mv tests/services tests/plugins
git mv internal/plugin/sdk/groot_service_sdk internal/plugin/sdk/groot_plugin_sdk
git mv tests/python/test_python_services.py tests/python/test_python_plugins.py
git mv tests/plugins/sdk/fixtures/svc_ok tests/plugins/sdk/fixtures/plugin_ok
git mv tests/plugins/sdk/fixtures/svc_b tests/plugins/sdk/fixtures/plugin_b
git mv tests/plugins/sdk/fixtures/svc_fail tests/plugins/sdk/fixtures/plugin_fail
git mv tests/plugins/sdk/fixtures/svc_import_fail tests/plugins/sdk/fixtures/plugin_import_fail
git mv tests/plugins/sdk/fixtures/svc_bad_route tests/plugins/sdk/fixtures/plugin_bad_route
```

- [ ] **Step 2: 先改 Python 测试断言（红）**

`tests/plugins/sdk/test_*.py`、`tests/plugins/asr-router/test_*.py`：

- `import groot_service_sdk` → `groot_plugin_sdk`（含 `from ... import`）。
- 就绪行前缀断言 `GROOT_SERVICE_READY ` → `GROOT_PLUGIN_READY `。
- 就绪 JSON 键 `services` → `plugins`。
- `error["code"] == "service_unavailable"` → `"plugin_unavailable"`。
- 身份字段 `.service` → `.plugin`，请求头 `x-groot-service` → `x-groot-plugin`。
- fixture 路径/名字 `svc_*` → `plugin_*`；fixture 入口 `tests/plugins/sdk/fixtures/*/app/main.py` 中的 `groot_service_sdk` 导入改为 `groot_plugin_sdk`，返回体字段 `"service": identity.service` 改为 `"plugin": identity.plugin`。
- 调用 `load_service(...)`→`load_plugin(...)`、`scan_services(...)`→`scan_plugins(...)`、`build_app(services_root=...)`→`build_app(plugins_root=...)`；局部变量 `services`/`services_root` 改为 `plugins`/`plugins_root`。
- 函数名 `test_load_service_reads_entry_contract`→`test_load_plugin_reads_entry_contract`，`test_startup_failure_yields_service_unavailable`→`test_startup_failure_yields_plugin_unavailable`。

`tests/plugins/conftest.py`：docstring 与路径改为 `internal/plugin/sdk`、`plugins/asr-router`。

- [ ] **Step 3: 改 SDK 实现使断言通过**

`internal/plugin/sdk/groot_plugin_sdk/__init__.py`：docstring `groot_plugin_sdk：Groot Python 插件协议库。`，导出不变。

`launcher.py`：`READY_PREFIX = "GROOT_PLUGIN_READY "`；命名空间 `groot_plugins`（`app_name = f"groot_plugins.{module_name}.app"`、`_ensure_namespace(...)`）；就绪 JSON 键 `services`→`plugins`；`load_service`→`load_plugin`、`scan_services`→`scan_plugins`、`services_root`→`plugins_root`、局部 `services`→`plugins`、参数 `service`→`plugin`；`code="service_unavailable"`→`"plugin_unavailable"`；`prog` 与 dev server 打印改为 `groot_plugin_sdk`；日志「子服务」→「插件」。

`identity.py`：dataclass 字段 `service`→`plugin`，读取 `x-groot-plugin`；docstring 同步。

`internal/plugin/supervisor.go`：`exec.Command(spec.Interpreter, "-m", "groot_plugin_sdk.launcher")`；`supervisor_test.go` 中模块名断言同步。

- [ ] **Step 4: 改 asr-router 与系统测试**

`plugins/asr-router/app/main.py`：`from groot_plugin_sdk import ...`；docstring「asr-router 插件入口」「插件未就绪」「其他插件」；`code="plugin_unavailable"`；`app/audio.py`、`app/lid.py`、`app/labels.py` 中的「子服务」→「插件」。

`plugins/asr-router/README.md`：标题 `# asr-router 插件`；`groot services init`→`groot plugins init`（含 `--py` 示例）；`{GROOT_HOME}/services/`→`{GROOT_HOME}/plugins/`；`services/asr-router`→`plugins/asr-router`；`POST /services/asr-router`→`POST /plugins/asr-router`；「子服务」→「插件」。

`tests/python/test_python_plugins.py`：docstring 的源码依据改为 `internal/cmd/plugins.go`、`internal/api/handler/health.go`、`internal/api/router.go`；命令 `groot plugins init`；布局断言 `plugins/`、`plugins/.sdk/`、`plugins/_template/`、`plugins/plugin.yaml`、`plugins/requirements.txt`、`plugins/.venv/`；健康检查键 `plugins`、`info.plugins`；路由 `/plugins/nope`、`/plugins/asr-router`；用例编号 TC-PY-101～105 不变。

`.gitignore`：`tests/services/.venv/`→`tests/plugins/.venv/`；`services/asr-router/models/*`→`plugins/asr-router/models/*`；`!services/asr-router/models/.gitkeep`→`!plugins/asr-router/models/.gitkeep`。

- [ ] **Step 5: 重建 Python 测试环境并运行**

```bash
python3 -m venv tests/plugins/.venv
tests/plugins/.venv/bin/pip install -r tests/plugins/requirements.txt
tests/plugins/.venv/bin/pytest tests/plugins -v -p no:cacheprovider
```

Expected: 全部 PASS。

Run: `python3 -c "compile(open('tests/python/test_python_plugins.py').read(), 'test_python_plugins.py', 'exec')"`
Expected: 无输出（语法通过，不生成 `__pycache__`；系统测试由用户后续执行）。

- [ ] **Step 6: Go 侧回归**

Run: `go build ./... && go test ./internal/...`
Expected: 全部 PASS。

- [ ] **Step 7: 残留检查**

Run: `grep -rn 'groot_service_sdk\|GROOT_SERVICE_READY\|x-groot-service\|X-Groot-Service\|service_unavailable\|svc_' --exclude-dir=node_modules --exclude-dir=.venv --exclude-dir=.git --exclude-dir=dist --exclude-dir=.superpowers .`
Expected: 仅 `docs/` 中的旧文档（Task 4 处理）与本计划/命名设计文档（记录旧名）命中；`cmd`、`internal`、`plugins`、`tests` 无命中。

- [ ] **Step 8: 记录悬空快照**

`-p` 用 Task 2 快照，消息 `snapshot: task3 python plugins layout`。

---

### Task 4: 文档改名与内容更新

**Files:**
- Move: `docs/superpowers/specs/2026-11-23-python-service-hosting-design.md`→`2026-11-23-python-plugin-hosting-design.md`；`2026-11-24-python-services-init-command-design.md`→`2026-11-24-python-plugins-init-command-design.md`；`2026-11-25-services-shell-design.md`→`2026-11-25-plugins-shell-design.md`；`2026-11-26-services-init-output-cleanup-design.md`→`2026-11-26-plugins-init-output-cleanup-design.md`
- Move: `docs/superpowers/plans/2026-11-23-python-service-hosting.md`→`2026-11-23-python-plugin-hosting.md`；`2026-11-24-python-services-init-command.md`→`2026-11-24-python-plugins-init-command.md`；`2026-11-25-python-services-init-polish.md`→`2026-11-25-python-plugins-init-polish.md`；`2026-11-25-services-shell.md`→`2026-11-25-plugins-shell.md`；`2026-11-26-services-init-output-cleanup.md`→`2026-11-26-plugins-init-output-cleanup.md`
- Modify: 上述 9 篇文档内容、`README.md`、`CLAUDE.md`、`tests/TEST_CASES.md`
- 不改：`docs/superpowers/specs/2026-11-26-plugin-naming-design.md`、本计划（两者按设计保留旧名映射）、`docs/superpowers/specs/archive/`、`AGENTS.md`

**Interfaces:**
- Consumes: Task 1～3 的最终名字。
- Produces: 文档中不再存在本功能的旧名；交叉引用路径全部指向新文件名。

- [ ] **Step 1: 重命名 9 篇文档**

```bash
cd docs/superpowers/specs
git mv 2026-11-23-python-service-hosting-design.md 2026-11-23-python-plugin-hosting-design.md
git mv 2026-11-24-python-services-init-command-design.md 2026-11-24-python-plugins-init-command-design.md
git mv 2026-11-25-services-shell-design.md 2026-11-25-plugins-shell-design.md
git mv 2026-11-26-services-init-output-cleanup-design.md 2026-11-26-plugins-init-output-cleanup-design.md
cd ../plans
git mv 2026-11-23-python-service-hosting.md 2026-11-23-python-plugin-hosting.md
git mv 2026-11-24-python-services-init-command.md 2026-11-24-python-plugins-init-command.md
git mv 2026-11-25-python-services-init-polish.md 2026-11-25-python-plugins-init-polish.md
git mv 2026-11-25-services-shell.md 2026-11-25-plugins-shell.md
git mv 2026-11-26-services-init-output-cleanup.md 2026-11-26-plugins-init-output-cleanup.md
```

- [ ] **Step 2: 更新 9 篇文档内容**

术语与标识按下表替换，保留各自原有的「功能设计 / 迭代说明」结构与用例编号：

| 旧 | 新 |
|---|---|
| Python 服务 / 子服务 / 服务环境 | Python 插件 / 插件 / 插件环境 |
| `groot services` / `groot services init` | `groot plugins` / `groot plugins init` |
| `/services/{name}` | `/plugins/{name}` |
| `{GROOT_HOME}/services/`、`services/service.yaml` | `{GROOT_HOME}/plugins/`、`plugins/plugin.yaml` |
| `internal/pysvc`、`internal/cmd/services.go`、`internal/cmd/services_shell.go` | `internal/plugin`、`internal/cmd/plugins.go`、`internal/cmd/plugins_shell.go` |
| `groot_service_sdk`、`groot_services` | `groot_plugin_sdk`、`groot_plugins` |
| `GROOT_SERVICE_READY`、`X-Groot-Service`、`service_unavailable`、`checks.services` | `GROOT_PLUGIN_READY`、`X-Groot-Plugin`、`plugin_unavailable`、`checks.plugins` |
| `services/asr-router`、`tests/services`、`test_python_services.py` | `plugins/asr-router`、`tests/plugins`、`test_python_plugins.py` |

同时更新文档内交叉引用路径（如 plan 的 `**Spec:**` 与新 spec 文件名、进度账本路径），以及 `python-service-hosting`/`services-shell` 等旧文件名引用。

- [ ] **Step 3: 更新 README、CLAUDE.md、TEST_CASES.md**

`README.md`：目录树中的 `services/`、`services/service.yaml`、`{service-name}/`；命令表与「6.4 初始化 Python 子服务环境」「6.5 进入 Python 子服务环境」「7.8 Python 服务 - 子服务托管与 /services/{name}」整节；命令示例；`/services/{name}` 权限表与错误码表（`service_unavailable`）；`checks.services`；部署/排障中的相关引用。泛指服务（Groot 服务、LLM 服务、MCP 服务等）保持不变。

`CLAUDE.md`：目录结构中的 `services/`→`plugins/`、`tests/services/`→`tests/plugins/`；规则 5/6 的「Python 子服务」→「Python 插件」；Python 测试小节中的路径、`pysvc`、`groot_service_sdk`、运行命令；测试目录树。

`tests/TEST_CASES.md`：1.16「Python 服务宿主测试」、1.17「Python 服务初始化命令测试」、1.18「Python 服务环境进入命令测试」、2.31「Python 子服务系统测试」及用例表中的命令、路径、文件名、错误码、Go 测试函数名中的 `Service`/`Services`（如 `TestRunServicesInit_*`→`TestRunPluginsInit_*`）。用例编号不变。

- [ ] **Step 4: 文档残留与链接检查**

Run: `grep -rn 'groot services\|/services/\|service\.yaml\|groot_service_sdk\|GROOT_SERVICE_READY\|X-Groot-Service\|service_unavailable\|python-service-hosting\|python-services-init\|services-shell\|services-init-output\|子服务' README.md CLAUDE.md tests/TEST_CASES.md docs/superpowers/specs docs/superpowers/plans --exclude='2026-11-26-plugin-naming-design.md' --exclude='2026-11-26-service-to-plugin-rename.md'`
Expected: 无输出。

Run: `grep -rn 'Spec:\|docs/superpowers/specs/' docs/superpowers/plans | grep -v '2026-11-26-service-to-plugin-rename'`
Expected: 每条引用的 spec 文件都存在（可配合 `ls` 抽查）。

- [ ] **Step 5: 记录悬空快照**

`-p` 用 Task 3 快照，消息 `snapshot: task4 docs`。

---

### Task 5: 全量验证

**Files:** 无新增；只执行验证并修复发现的问题。

- [ ] **Step 1: Go 构建、格式与测试**

```bash
gofmt -l cmd internal pkg
go build -o dist/groot ./cmd/groot
go test ./internal/... 
```

Expected: `gofmt -l` 无输出；构建成功；测试全部 PASS。

- [ ] **Step 2: Python 测试**

```bash
tests/plugins/.venv/bin/pytest tests/plugins -v -p no:cacheprovider
python3 -c "compile(open('tests/python/test_python_plugins.py').read(), 'test_python_plugins.py', 'exec')"
```

Expected: pytest 全部 PASS；语法检查无输出。

- [ ] **Step 3: 旧名残留扫描**

```bash
grep -rn 'pysvc\|groot_service_sdk\|GROOT_SERVICE_READY\|X-Groot-Service\|x-groot-service\|service_unavailable\|service\.yaml\|/services/\|groot services\|checks\.services\|internal/pysvc' \
  --exclude-dir=node_modules --exclude-dir=.venv --exclude-dir=.git --exclude-dir=dist --exclude-dir=.superpowers \
  --exclude='2026-11-26-plugin-naming-design.md' --exclude='2026-11-26-service-to-plugin-rename.md' .
```

Expected: 无输出。

- [ ] **Step 4: 通用「服务」未被误伤**

```bash
grep -rn 'ModelService\|MessageService' internal cmd | head
test -f internal/webfiles/service.go && echo "webfiles/service.go 保留"
```

Expected: `llm.ModelService`、`cluster.MessageService()`、`internal/webfiles/service.go` 仍存在。

- [ ] **Step 5: 按 spec 1.3 逐项核对对外契约**

逐条确认并记录到进度账本：`groot plugins` 与 `groot plugins init` 可执行/帮助正确；`/plugins/{name}` 路由注册；`checks.plugins` 与 `info.plugins`；`plugin_unavailable`；`GROOT_PLUGIN_READY`（Go 与 SDK 两侧）；`X-Groot-Plugin`（Go 与 SDK 两侧）；`groot_plugin_sdk` 与 `groot_plugins`；`{GROOT_HOME}/plugins/`、`plugins/plugin.yaml`、`.venv`、`.sdk`；`groot status` 的 `Plugins:` 行。

Run（抽查）: `grep -rn 'GROOT_PLUGIN_READY' internal | head`，`grep -rn 'X-Groot-Plugin' internal | head`，`grep -rn 'groot_plugin_sdk' internal plugins tests | head`
Expected: Go 宿主、嵌入 SDK、asr-router、测试两侧命名一致。

- [ ] **Step 6: 变更清单核对**

Run: `git status --short`
Expected: 只包含本次改名涉及的文件（新增 2 篇文档、重命名与修改的源码/测试/文档），无构建产物（`dist/` 被 gitignore）。

- [ ] **Step 7: 记录悬空快照**

`-p` 用 Task 4 快照，消息 `snapshot: task5 verification`。整体评审通过后按 CLAUDE.md 删除 `.superpowers/sdd/2026-11-26-service-to-plugin-rename/`。
