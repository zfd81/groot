# 单一 bootstrap.yaml 配置文件设计文档

## 一、功能设计

### 1.1 功能概述

Groot 的文件配置集中于一个文件：`~/.groot/bootstrap.yaml`。它承载启动路径上读取的配置——进程起来之前、数据库打开之前就必须确定的那部分参数：服务地址与端口、日志、数据库连接、以及各常驻组件的构造参数。

业务配置存放于数据库配置表，由 Web 设置面板维护，保存即写表；限流、发送渠道、调度开关等项按各自的生效机制在线生效（见《限流、消息发送器与调度开关的运行时配置设计文档》）。

两个存放地各司其职：

- **bootstrap.yaml**：回答「进程怎么起」——监听哪个端口、日志写到哪、连哪个数据库。这些项的读取点在数据库可用之前，只能放文件；改动需重启。
- **配置表**：回答「服务怎么跑」——限多少流、往哪投递消息、允不允许模型建定时任务。这些项在数据库可用之后才被使用，放表里即可经界面维护，且多节点共享同一数据库时天然共享同一份值。

解决的问题：

- 配置只有一个文件，部署者不需要在多个 YAML 之间判断某个参数该写在哪。
- 业务参数经界面维护并落表，多节点部署时各节点取到同一份值，不存在节点间文件内容漂移。
- 数据库凭据这类环境信息与业务参数分离：前者随文件走、可用环境变量注入，后者随数据库走。

### 1.2 划分依据

一个配置项放文件还是放表，判据是**读取时机**：

1. **读取点在数据库连接建立之前**。日志器在 `db.Open` 之前构造——数据库打不开这件事本身要能写进日志；数据库连接参数更是打开数据库的前提。这些项别无选择，只能放文件。
2. **不连数据库的 CLI 需要读到**。`groot status` 读 `server.port` 探活，`groot tail` 读 `logging.file.directory` 定位日志文件。这两条命令面向的场景恰恰包含「服务没起来」「数据库有问题」，不能以数据库可用为前提。
3. **值是常驻组件的构造参数**。消息层的队列容量与工作协程数、调度器的并发数与目录同步间隔、限流器空闲桶的回收周期，都在组件构造时固化进实例，改动本来就需要重启。放表得不到「保存即生效」的收益，反而制造「表里改了、实际没变」的误解，因此放文件。

不满足以上任何一条的项进配置表。

### 1.3 配置项归属

| 配置项 | 归属 |
|---|---|
| `agent.name` / `agent.version` | bootstrap.yaml |
| `server.host` / `server.port` | bootstrap.yaml |
| `logging.*` | bootstrap.yaml |
| `database.*` | bootstrap.yaml |
| `message.queue_size` / `message.workers` | bootstrap.yaml |
| `schedule.max_concurrent_tasks` / `schedule.sync_interval` | bootstrap.yaml |
| `security.rate_limit.cleanup_interval` | bootstrap.yaml |
| `security.auth.secret` / `security.auth.header_name` | 配置表（启动时读一次） |
| `security.rate_limit` 其余五项 | 配置表 |
| `memory.*` / `react.*` / `subagent.*` / `attachment.*` | 配置表 |
| `schedule.enabled` | 配置表 |
| `message.senders.*` | 配置表 |

### 1.4 文件形态

- **权限 0600**。文件可能含数据库凭据，创建与迁移生成时一律置 0600。
- **全注释即可工作**。`groot init` 产出的模板每一项都是注释行，标明键名、默认值与含义；文件保持全注释状态时服务照常启动，全部缺省值由代码提供。部署者只需取消注释并修改需要变的那几行。
- **DSN 支持环境变量展开**。`database.dsn` 中的 `${ENV_VAR}` 在加载时展开，凭据可以不落盘、由部署环境注入。

Web 文件面板中 bootstrap.yaml 以只读方式展示：它的每一项都在重启时才生效，面板上可改会造成「改了即生效」的误解。同理，目录下如存在 `config.yaml`、`env.yaml`（迁移来源文件，见 1.7），面板同样只读展示——它们的内容对运行中的服务没有作用。

### 1.5 JWT 签名密钥

API Key 的签名密钥 `security.auth.secret` 存放于配置表。理由是集群：各节点共享同一数据库，密钥进表即天然共享，A 节点签发的 API Key 在 B 节点可以验证；密钥若随文件走，则需要部署者手工保证各节点文件一致。

认证中间件在每个请求上执行签名校验，密钥在启动时读取一次并交给中间件，不为每个请求增加一次数据库查询。启动时表内无密钥则生成一个随机密钥写入表（`EnsureAuthSecret`），首个启动的节点完成生成，后续节点直接读到。

更换密钥会使已签发的全部 API Key 立即失效，是一个需要通知全部调用方的运维动作，因此密钥改动需重启生效，经设置面板的认证分组管理（重启生效）——重新生成密钥带二次确认，界面上一次误触不会打掉所有调用方的凭证。详见《认证配置面板设计文档》（2026-09-27-auth-settings-panel-design.md）。

`security.auth.header_name` 同样存放于配置表、启动时读一次，与密钥共用同一读取路径。

### 1.6 启动顺序

```
LoadBootstrap            读取 bootstrap.yaml，缺省值由代码补齐
  → config.MigrateLegacy 文件侧一次性迁移（见 1.7）
  → logger               依据 logging.* 构造日志器
  → db.Open              依据 database.* 打开数据库，失败可记日志
  → repos                构造各仓库
  → settings             构造配置表访问层，执行表侧一次性迁移（见 1.7）
  → EnsureAuthSecret     读取或生成签名密钥
  → AssembleConfig       组装启动配置快照
  → 其余组件             api.NewServer、限流器、消息层、调度器等
```

`Settings.AssembleConfig` 把 bootstrap 静态项与配置表业务项组装成一个 `config.Config` 快照，交给 `api.NewServer` 等以整份配置为入参的构造方。快照体现启动时刻的表内值；运行期的在线生效不依赖该快照，仍由各持有对象完成——限流器 `Reconfigure`、消息层 `SetSender`、内置工具门控与调度接口每次求值读表。

不连数据库的 CLI 走短路径：`groot status` 与 `groot tail` 只执行 `LoadBootstrap` 取 `server.port` 与 `logging.file.directory`；bootstrap.yaml 不存在时回落读取 `config.yaml` 中的同名项，两个文件都没有时使用代码默认值。

`groot init` 产出 bootstrap.yaml（全注释模板）、GROOT.md 与 skills、mcp、subagents、logs 四个目录。init 不生成签名密钥——密钥属于配置表，由服务首次启动写入。

### 1.7 老部署迁移

首次启动时检测 `~/.groot/config.yaml` 与 `env.yaml`，检测到即执行一次性迁移，把其中的配置按 1.3 的归属表分流到 bootstrap.yaml 与配置表。迁移分两段，各自幂等：

**文件侧（`config.MigrateLegacy`，在日志器构造之前）**：bootstrap.yaml 已存在则整段跳过；否则从 config.yaml 取归属为文件的项、从 env.yaml 取数据库连接项，生成 bootstrap.yaml。生成采用原子写（写半途失败不留下半成品文件），文件带说明性头注释（注明由迁移产生、老文件可删），权限 0600。

**表侧（`Settings.ImportLegacy`，在配置表访问层就绪之后）**：从 config.yaml 取归属为表的项写入配置表，含 `security.auth.secret`。表内已有的键不覆盖——表值可能是使用者后来经面板改过的，迁移不回退它。布尔项只在值为 true 时写入，false 与缺省不可区分，交由代码默认值判定。

三条不变式：

1. **只迁非零值**。老文件中的零值无从区分「显式写了零」与「没写」，一律不迁，落到代码默认值。
2. **目标侧已有值不覆盖**。bootstrap.yaml 已存在则文件侧不动；表内已有键则表侧不写。
3. **老文件原地保留**。config.yaml 与 env.yaml 是使用者的文件，程序不删除、不改写，仅供 `groot status` / `groot tail` 在 bootstrap.yaml 缺失时回落读取。

### 1.8 集群同步范围

集群文件同步的白名单为四类纯资源文件：`skills`、`subagents`、`mcp`、`GROOT.md`。它们是交给模型的能力定义与提示词，各节点需要同一份副本，适合按文件同步。

配置不经文件同步：业务配置的共享经数据库配置表完成——各节点连同一个库即读到同一份值；bootstrap.yaml 描述的是单节点自身的环境（本机端口、本机日志目录、数据库地址），各节点内容本就允许不同，同步它反而会把一台机器的环境写到另一台上。

### 1.9 改动清单

| 文件 | 说明 |
|---|---|
| `internal/config/bootstrap.go`（新） | `Bootstrap` 结构与 `LoadBootstrap`，含缺省值补齐与 DSN 环境变量展开 |
| `internal/config/bootstrap_template.go`（新） | 全注释模板，供 `groot init` 与迁移生成使用 |
| `internal/config/migrate_legacy.go`（新） | 文件侧一次性迁移：config.yaml + env.yaml → bootstrap.yaml |
| `internal/setting/defaults.go` | 配置表分类的代码默认值 |
| `internal/setting/auth.go`（新） | `Auth`、`EnsureAuthSecret`、`SetAuthHeaderName` |
| `internal/setting/settings.go` | 静态层为 `Bootstrap`，基准层为代码默认值 |
| `internal/setting/migrate_legacy.go`（新） | 表侧一次性迁移 `ImportLegacy` |
| `internal/setting/assemble.go`（新） | `AssembleConfig` 启动配置快照 |
| `internal/cmd/init.go` | init 产出 bootstrap.yaml 模板 |
| `cmd/groot/main.go` | 按 1.6 的顺序启动装配 |
| `internal/cmd/status.go` / `internal/cmd/tail.go` | 读 bootstrap.yaml，缺失时向 config.yaml 回落 |
| `internal/sync/resource.go` / `webview.go` / `resolver.go` | 同步白名单四类资源 |
| `internal/webfiles/resolver.go` | 面板只读规则：bootstrap.yaml 与迁移来源文件只读 |
| `web/src/components/files/FileTree.vue` | 前端同步根常量 |
| `internal/config/template.go`、`env_template.go`（删除） | 与 `loader.go` 的 `Load`/`applyDefaults`、`env.go` 的 `loadEnvFile`、`secret.go` 的 `EnsureAuthSecret` 及 yaml 回写辅助一并删除（`GenerateAuthSecret` 保留供 setting 包使用） |
| `README.md` / `tests/TEST_CASES.md` / `tests/python/test_cli_commands.py` | 文档与系统测试对齐 |

## 二、迭代说明

### 2.1 与上一版差异

**新增：**

- `~/.groot/bootstrap.yaml`，合并原 `config.yaml`（启动静态项部分）与 `env.yaml`（数据库凭据）为单一文件，配套 `config.LoadBootstrap`（读取解析、缺省值补齐、DSN 环境变量展开）与 `config.GenerateBootstrapTemplate`（全注释模板，供 `groot init` 使用）。
- `security.auth.secret` 与 `security.auth.header_name` 两键进配置表，配套 `internal/setting` 的 `Settings.Auth`（启动时一次性读取两键）、`Settings.EnsureAuthSecret`（表内无密钥则生成写表）、`Settings.SetAuthHeaderName`。
- 老部署两段式一次性迁移：文件侧 `config.MigrateLegacy` 生成 bootstrap.yaml 并返回待入表的业务项，表侧 `Settings.ImportLegacy` 把业务项与 JWT 密钥导入配置表（只迁非零值、表内已有键不覆盖）；老文件原地保留。
- `Settings.AssembleConfig`，启动时组装 bootstrap 静态项与表内业务项为一份 `config.Config` 快照，交给以整份配置为入参的构造方。

**移除：**

- `config.yaml` / `env.yaml` 的生成与加载：`config.Load`（含 `applyDefaults`）、`env.go` 的 `loadEnvFile`、`template.go` 的 `GenerateConfigTemplate`、`env_template.go` 的 `GenerateEnvTemplate`、`secret.go` 的 `config.EnsureAuthSecret` 与 yaml 回写辅助、`defaults.go` 的 `DefaultConfig`（`GenerateAuthSecret` 保留供 setting 包使用）。
- `config.yaml` 退出集群同步白名单（`internal/sync/resource.go` 的 SyncableResourceRoots 剩 skills、subagents、mcp、GROOT.md 四项），并退出 `internal/sync/webview.go` 的 `needsRestartPaths`（拉取后标记「需重启」的路径只剩 `mcp/`、`subagents/`）。

**调整：**

- `Settings` 静态层类型从 `config.Config` 改为 `config.Bootstrap`；配置表各分类的基准层从 YAML 中的值改为代码默认值（`internal/setting/defaults.go`），表中缺失的键回落到代码默认值，配置表创建后为空即可工作。
- JWT 签名密钥从 config.yaml 移入配置表，`EnsureAuthSecret` 从 `internal/config` 移到 `internal/setting`，改为启动时读表、缺失则生成写表；`groot init` 不再生成密钥。
- `memory.*`、`react.*`、`subagent.*`、`attachment.*`、`security.auth.*` 从 YAML 移入配置表；此前已入表的限流五项、发送渠道、`schedule.enabled` 归属不变。
- `groot init` 产出物从 config.yaml + env.yaml 改为 bootstrap.yaml 全注释模板（0600、不含密钥）；`groot status` / `groot tail` 改读 bootstrap.yaml，并向老 config.yaml 回落以兼容服务从未启动过的老部署，两个文件都没有时用代码默认值。
- Web 文件面板只读文件清单改为 bootstrap.yaml；遗留的 config.yaml / env.yaml 同样只读（已不生效，可改会造成误解）。
- 1.5 节「密钥不经界面暴露、换密钥需直接改表」的表述随认证配置面板落地而过时：两项认证配置改为经设置面板的认证分组管理（重启生效），见《认证配置面板设计文档》（2026-09-27-auth-settings-panel-design.md）。
