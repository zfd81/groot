# 插件（Plugins）命名设计文档

## 一、功能设计

### 1.1 功能概述

Groot 可以托管 Python 插件：插件是独立目录中的 Python 应用，由 Groot 主进程按需拉起为单个 Python 子进程，装进共享虚拟环境，并通过 HTTP 反向代理对外提供能力。插件与 Skills、MCP、子 Agent 一起构成 Groot 的扩展体系，区别在于插件是 Groot 托管的进程外代码，可以携带自己的依赖与模型。

本设计统一这套能力的命名为「插件（plugin）」：中文一律称「插件」，英文标识一律用 `plugin` / `plugins`，不再使用「服务」「子服务」「service」指代该能力。

### 1.2 命名总则

- 中文术语：**插件**。单个称「插件」，集合称「插件」，不再出现「子服务」。
- 英文标识：单个实体 `Plugin` / `plugin`，集合与目录 `Plugins` / `plugins`。
- Go 包名：`plugin`；包内核心类型 `plugin.Plugin`、`plugin.PluginStatus`、`plugin.Manager`。
- 变量与函数：单一插件用 `plugin` / `p`，集合用 `plugins`，管理器变量统一 `pluginMgr`。
- 与「服务」有关的通用用法不属于本设计范围，保持原名（见 1.6）。

### 1.3 对外契约

| 契约 | 名称 |
|---|---|
| CLI 命令 | `groot plugins`（进入插件环境）、`groot plugins init`（初始化插件环境） |
| HTTP 代理路由 | `/plugins/{name}`（鉴权按插件声明的权限点覆盖） |
| 健康检查 | `/web/health` 的 `checks.plugins`；`info` 结构为 `{"process": {...}, "plugins": [...]}` |
| 错误码 | `plugin_unavailable`（503） |
| 子进程就绪行 | `GROOT_PLUGIN_READY {"port":...,"pid":...,"sdk":...,"plugins":{...}}` |
| 内部请求头 | `X-Groot-Plugin`（携带被调用插件名） |
| Python SDK 包 | `groot_plugin_sdk`，模块命名空间 `groot_plugins.<name>.app`，启动器 `python -m groot_plugin_sdk.launcher` |
| SDK 身份 | `GrootIdentity.plugin`，取自 `x-groot-plugin` 请求头 |
| 运行时目录 | `{GROOT_HOME}/plugins/` |
| 全局配置 | `{GROOT_HOME}/plugins/plugin.yaml`（`startup_timeout`、`request_timeout`、`permission`） |
| 共享环境 | `{GROOT_HOME}/plugins/.venv`、`{GROOT_HOME}/plugins/.sdk/<版本>/`、`{GROOT_HOME}/plugins/requirements.txt` |
| 模板 | `{GROOT_HOME}/plugins/_template/` |
| 状态输出 | `groot status` 显示 `Plugins:` 行；进程日志字段 `plugin_pid` |
| 插件环境变量 | `GROOT_PLUGIN_MODELS_DIR`（asr-router 模型目录覆盖） |

### 1.4 仓库布局

- Go 包：`internal/pysvc/` → `internal/plugin/`（嵌入的 SDK 与模板随包迁移：`internal/plugin/sdk/groot_plugin_sdk/`、`internal/plugin/template/plugin.yaml`）。
- 插件源码：`services/asr-router/` → `plugins/asr-router/`。
- Python 测试：`tests/services/` → `tests/plugins/`（`asr-router/`、`sdk/`、`conftest.py`、`requirements.txt`）；fixture 目录 `svc_*` → `plugin_*`。
- 系统测试：`tests/python/test_python_services.py` → `tests/python/test_python_plugins.py`（用例编号 TC-PY-101～105 不变）。
- 文档：4 篇 spec、5 篇 plan 的文件名与内容同步改名（见 2.1）。

### 1.5 完整改名映射

**Go 包与类型**

| 旧 | 新 |
|---|---|
| 包 `pysvc` | `plugin` |
| `Service` | `Plugin` |
| `ServiceStatus` | `PluginStatus` |
| `Status.Services` / JSON `services` | `Status.Plugins` / JSON `plugins` |
| `ServicesDir` | `PluginsDir` |
| `Manager.Services()` | `Manager.Plugins()` |
| `Manager.ServicePermission()` | `Manager.PluginPermission()` |
| `ServicePermissionResolver` / `SetServicePermissionResolver` | `PluginPermissionResolver` / `SetPluginPermissionResolver` |
| `ServiceStatusProvider` / `SetServiceStatusProvider` | `PluginStatusProvider` / `SetPluginStatusProvider` |
| `AuthMiddleware.servicePerm` | `pluginPerm` |
| `serviceNameFromPath` | `pluginNameFromPath` |
| `serviceNamePattern` / `serviceNameRe` | `pluginNamePattern` / `pluginNameRe` |
| `reservedServiceNames` | `reservedPluginNames` |
| `noServiceReason` / `missingResultReason` | `noPluginReason` / `missingResultReason`（措辞改为插件） |
| `mergeServiceStatus` | `mergePluginStatus` |
| `readyMessage.Services` / JSON `services` | `readyMessage.Plugins` / JSON `plugins` |
| `readyPrefix` 值 `GROOT_SERVICE_READY ` | `GROOT_PLUGIN_READY ` |
| 日志字段 `service_pid` | `plugin_pid` |
| `headerService` / `X-Groot-Service` | `headerPlugin` / `X-Groot-Plugin` |
| `errbody.CodeServiceUnavailable` / `"service_unavailable"` | `CodePluginUnavailable` / `"plugin_unavailable"` |

**命令与 API**

| 旧 | 新 |
|---|---|
| `handleServicesCommand` | `handlePluginsCommand` |
| `ServicesInitFlags` / `ServicesInitOptions` / `ParseServicesInitFlags` | `PluginsInitFlags` / `PluginsInitOptions` / `ParsePluginsInitFlags` |
| `PrintServicesHelp` / `PrintServicesInitHelp` / `RunServicesInit` | `PrintPluginsHelp` / `PrintPluginsInitHelp` / `RunPluginsInit` |
| `ServicesShellOptions` / `RunServicesShell` | `PluginsShellOptions` / `RunPluginsShell` |
| `servicesShellEnv` / `servicesShell` / `servicesShellLaunch` | `pluginsShellEnv` / `pluginsShell` / `pluginsShellLaunch` |
| `renderServices` / `servicesSummary` / `serviceStatusFromInfo` | `renderPlugins` / `pluginsSummary` / `pluginStatusFromInfo` |
| 变量 `pyMgr` | `pluginMgr` |
| 健康检查项 `"services"` / `addServicesCheck` / `servicesCheck` | `"plugins"` / `addPluginsCheck` / `pluginsCheck` |
| 文件 `services.go` / `services_shell.go` / 测试同名 | `plugins.go` / `plugins_shell.go` / 测试同名 |
| 路由前缀 `"/services/"` | `"/plugins/"` |

**Python SDK 与插件**

| 旧 | 新 |
|---|---|
| `groot_service_sdk` | `groot_plugin_sdk` |
| 命名空间 `groot_services` | `groot_plugins` |
| `READY_PREFIX = "GROOT_SERVICE_READY "` | `"GROOT_PLUGIN_READY "` |
| 就绪 JSON 键 `services` | `plugins` |
| `code="service_unavailable"` | `code="plugin_unavailable"` |
| `GrootIdentity.service` / `x-groot-service` | `GrootIdentity.plugin` / `x-groot-plugin` |
| `load_service` / `scan_services` / `services_root` | `load_plugin` / `scan_plugins` / `plugins_root` |
| `GROOT_SERVICE_MODELS_DIR` | `GROOT_PLUGIN_MODELS_DIR` |
| 模板 `service.yaml` | `plugin.yaml` |

**中文措辞**：功能范围内的「Python 服务」「子服务」「服务环境」「Services环境」统一改为「Python 插件」「插件」「插件环境」。

### 1.6 范围边界

不随本设计改名：

- 通用「服务」概念：`llm.ModelService`、`cluster.MessageService`、`internal/webfiles/service.go`、`MessageService()` 等。
- 泛指的基础设施服务措辞：Groot 服务、LLM 服务、MCP 服务、上游转录/语音/图片服务、HTTP 服务、`groot status` 中的「Groot 服务端口」等。
- Web UI：没有任何插件相关界面，`web/` 不改。
- `AGENTS.md`：当前内容不含本功能，不改。
- 历史文档中与插件无关的「服务」措辞。

不做兼容：旧命令、旧路由、旧目录、旧配置文件名、旧错误码、旧 SDK 包名与旧请求头一律不再保留，也不做运行时目录自动迁移。

## 二、迭代说明

### 2.1 与上一版差异

- 重命名：Python 子服务能力的全部命名由「服务 / service」改为「插件 / plugin」，覆盖设计文档、实施计划、Go 包与标识符、CLI 命令、HTTP 路由、健康检查、错误码、就绪协议、内部请求头、Python SDK、运行时目录、配置文件名、仓库目录与测试。
- 文档改名：`2026-11-23-python-service-hosting-design.md` → `2026-11-23-python-plugin-hosting-design.md`，`2026-11-24-python-services-init-command-design.md` → `2026-11-24-python-plugins-init-command-design.md`，`2026-11-25-services-shell-design.md` → `2026-11-25-plugins-shell-design.md`，`2026-11-26-services-init-output-cleanup-design.md` → `2026-11-26-plugins-init-output-cleanup-design.md`；5 篇 plan 同理。
- 无功能变更：插件发现、启动、代理、权限、初始化、环境进入、asr-router 行为均保持不变。

## 三、实施方案

| 方案 | 做法 | 取舍 |
|---|---|---|
| A（采用） | 分层顺序硬切：目录与包名 → 内部标识符与中文措辞 → 对外契约 → 模板与 asr-router → 测试 → 文档，每层跑 `go build` + `go test ./internal/...` 与 Python 单测 | 每层有验证点，通用「服务」不会被误伤；需逐层推进 |
| B | 全局替换 `service/services/pysvc` 后靠编译器纠错 | 快，但会误改 `ModelService`、`MessageService`、`webfiles` 等通用用法 |
| C | 只改对外契约与文档，内部保留 `pysvc` | 与「所有名字都要改」冲突，未采用 |

采用 A：改动面明确、通用概念需要保护，逐层验证的成本低于事后纠错。
