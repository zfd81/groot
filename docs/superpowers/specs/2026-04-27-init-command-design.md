# Groot Init 命令设计文档

## 一、功能设计

### 1.1 功能概述

`groot init` 是初始化命令，负责为 Groot Agent 准备好运行所需的工作目录、启动配置模板和主 Agent 全局指导文件。用户首次安装 Groot 后必须先运行 `groot init` 完成初始化，然后直接 `groot` 启动服务；模型等业务配置在 Web 设置面板中维护，不需要在启动前编辑任何文件。

初始化命令解决以下问题：

1. 让用户拥有一个明确的"准备工作"入口，而不是在启动失败的报错信息里摸索
2. 把目录和模板文件的创建从启动阶段剥离出来，启动阶段只负责加载、校验和运行
3. 模板文件以全注释形式列出每个启动配置项的键名、默认值与含义，并演示数据库凭据的环境变量引用语法

### 1.2 命令格式

```
groot init [选项]

选项:
  -h, --help            显示 init 子命令帮助
```

`init` 子命令本身不接受位置参数，也不再提供其他选项。工作目录通过 `GROOT_HOME` 环境变量决定，未设置时默认为 `~/.groot`，逻辑见 [`GetDefaultHome`](../../../internal/cmd/tail.go)。

命令解析与帮助输出由 [`internal/cmd/init.go`](../../../internal/cmd/init.go) 中的 `ParseInitFlags` 与 `PrintInitHelp` 实现，主入口 [`cmd/groot/main.go`](../../../cmd/groot/main.go) 在 `case "init"` 分支调用 `handleInitCommand`。

### 1.3 执行流程

`RunInit(homeDir)` 的执行顺序如下（实现见 [`internal/cmd/init.go`](../../../internal/cmd/init.go)）：

1. 创建工作目录根目录（`homeDir`）
2. 依次创建子目录：`skills/`、`mcp/`、`subagents/`、`logs/`
3. 创建启动配置模板 `bootstrap.yaml`（默认全注释）
4. 创建主 Agent 全局指导文件 `GROOT.md`（空文件）
5. 输出"下一步"操作指引

子目录的用途：

| 目录 | 用途 |
|------|------|
| `skills/` | 共享配置：技能集 |
| `mcp/` | 共享配置：MCP 服务定义 |
| `subagents/` | 共享配置：子 Agent 定义 |
| `logs/` | 运行日志 |

会话/对话、定时任务、集群成员等运行时数据存储在数据库（SQLite/MySQL/PostgreSQL）中，不再以本地目录形式存在。SQLite 模式下数据库文件为 `~/.groot/groot.db`，由首次启动时自动创建。

**目录与文件检查逻辑**：每个目录和文件单独处理，已存在则提示"已存在，跳过创建"，不存在则创建并提示"创建成功"，永远不会覆盖用户已有内容。`bootstrap.yaml` 文件以 `0600` 权限创建（可能承载数据库凭据），`GROOT.md` 以 `0644`、目录以 `0755` 创建。

**输出示例**（首次执行，工作目录不存在）：

```
初始化 Groot 工作目录...

工作目录 ~/.groot 创建成功
目录 skills /Users/alice/.groot/skills 创建成功
目录 mcp /Users/alice/.groot/mcp 创建成功
目录 subagents /Users/alice/.groot/subagents 创建成功
目录 logs /Users/alice/.groot/logs 创建成功
配置文件 bootstrap.yaml 创建成功
GROOT.md 创建成功

初始化完成

下一步：
  1. （可选）调整启动配置：服务端口、日志、数据库连接
     vim ~/.groot/bootstrap.yaml   # 默认全注释 → SQLite 本地模式
  2. 启动服务
     groot
  3. 打开 Web 界面，在设置面板中配置模型与其他业务项
  4. （可选）编辑 GROOT.md，填写主 Agent 需始终遵守的规则、风格、目标，每轮对话作为 system 提示注入
     vim ~/.groot/GROOT.md
```

工作目录根目录在输出中会被缩写为 `~/.groot`（前提是它位于 `$HOME` 下），子目录则保留完整路径，缩写规则见 `shortenPath`。

### 1.4 生成的配置模板

#### 1.4.1 `bootstrap.yaml`

由 [`config.GenerateBootstrapTemplate`](../../../internal/config/bootstrap_template.go) 生成，文件名常量 `config.BootstrapFileName`。文件只承载**启动时读取**的配置：Agent 元信息、HTTP 服务监听、日志、调度器与限流后台协程的构造参数、数据库连接。模型、限流阈值、通知渠道、附件限制、定时任务开关等业务配置存放在数据库配置表中，由 Web 设置面板维护，不出现在模板里。

模板内容**全注释**：每一项都标明键名、默认值与含义，全部缺省值由代码（`applyBootstrapDefaults`）提供，文件保持全注释状态即可正常启动。部署者只需取消注释并修改需要偏离默认值的那几行。改动本文件需重启服务才生效。

数据库节为 MySQL 与 PostgreSQL 各提供一个**完整示例块**，DSN 格式按驱动写好，用户二选一取消整块注释并填入真实连接信息即可启用；DSN 中的密码通过 `${ENV_VAR}` 引用环境变量，由 `LoadBootstrap` 在加载时展开。模板使用"先缩进后 `#`"格式（如 `#  driver:`），删掉每行行首的 `#` 后 yaml 缩进自动正确。SQLite 不需要配置：`database` 节保持注释、删除该节、文件全注释三种情况都自动落到 SQLite 本地模式（数据库文件 `GROOT_HOME/groot.db`）。

JWT 签名密钥不在此文件中，由服务首次启动时生成并存入数据库配置表。配置项的划分依据与完整字段语义见 [启动配置设计](2026-09-27-bootstrap-config-design.md)。

#### 1.4.2 `GROOT.md`

主 Agent 全局指导文件，每次对话都会作为 system 提示注入。`init` 创建的是一个空文件：文件内容会原样进入模型上下文，任何预置的占位说明都是无效输入，而空文件在 `grootmd.GetContent` 中视为「不注入」。文件的用途与填写方式在 `init` 结束时的「下一步」提示中告知用户（第 4 条）。子 Agent 调用约束由 `call_agent` 工具自身的参数描述承载，不放在此文件中。

### 1.5 启动时的配置加载

`groot` 启动流程先调用 [`config.MigrateLegacy`](../../../internal/config/migrate_legacy.go) 处理老部署（见 [启动配置设计](2026-09-27-bootstrap-config-design.md) §1.7），再通过 [`config.LoadBootstrap`](../../../internal/config/bootstrap.go) 加载启动配置：

- `bootstrap.yaml` 不存在 → 返回错误 `配置文件 bootstrap.yaml 不存在，请先运行 'groot init' 初始化`
- 读取失败 → 返回 `failed to read bootstrap file: ...`
- 解析 yaml 失败 → 返回 `failed to parse bootstrap file: ...`
- 解析成功 → 填充缺省值，展开 `database.dsn` 中的环境变量引用

启动入口 [`cmd/groot/main.go`](../../../cmd/groot/main.go) 在加载失败时输出 `无法加载配置: <详情>` 并退出，将上述错误透传给用户。

模型配置不在启动期校验：服务可以在没有任何模型的情况下启动，用户登录 Web 界面后在设置面板中添加模型，对话时才会因缺少可用模型而报错。

### 1.6 用户使用流程

```bash
# 1. 初始化工作目录
groot init

# 2. （可选）调整启动配置：端口、日志、数据库连接
vim ~/.groot/bootstrap.yaml

# 3. （可选）设置 DSN 中引用的环境变量
export GROOT_DB_PASSWORD="your-password"

# 4. 启动服务
groot

# 5. 打开 Web 界面，在设置面板中配置模型与其他业务项
```

## 二、迭代说明

### 2.1 与上一版差异

- **新增** `groot init` 子命令，集中处理工作目录、配置模板、`env.yaml`、`GROOT.md` 的创建
- **新增** [`internal/cmd/init.go`](../../../internal/cmd/init.go)、[`internal/config/template.go`](../../../internal/config/template.go)、[`internal/config/env_template.go`](../../../internal/config/env_template.go)
- **新增** `init` 创建空的 `GROOT.md`
- **移除** 默认模板内容，改为空文件：原模板的标题行与「请在此处填写……」说明是写给用户看的，却每轮注入 system 提示；用途说明迁移到「下一步」提示的第 4 条
- **移除** 原模板中的「子 Agent 调度」引导段：其中「附件引用：在 task 中写明附件路径」依赖的附件落盘机制已删除（附件现以内联方式进入用户消息，没有路径可写）；「按需调用」「逐个调用」分别已由 `call_agent` 工具描述中的子 Agent 列表和注册表并发信号量覆盖；「明确传参」迁移到 `call_agent` 的 `task` 参数描述中。模板只保留标题与给用户的填写说明，减少每轮注入的无效 token
- **移除** `config.Load` 中"配置不存在则自动创建默认配置"的逻辑，未初始化时直接返回错误并提示运行 `groot init`
- **移除** `memory/`、`schedules/`、`cluster/members/` 等运行时数据目录的创建：相关数据已迁入数据库（SQLite/MySQL/PostgreSQL）
- **移除** 配置模板中的 `storage` 节：MinIO 等基础设施凭据集中放在 `env.yaml`
- **调整** 启动期 LLM 校验，增强为细分错误：models 为空、base_url/api_key 为空、环境变量未设置、参数超出范围等，错误信息中给出具体修复操作
- **调整** `env.yaml` 模板：原先只给一个通用 `database` 块（`driver: sqlite` + 通用 `${DB_DSN}` 占位），改为 MySQL 与 PostgreSQL 两个完整示例块，DSN 按驱动写好，用户二选一取消注释；SQLite 不再出现在模板中（默认零配置即 SQLite）
- **调整** 文档与当前实现对齐：`init` 产出物为 `bootstrap.yaml`（启动配置，全注释）与空 `GROOT.md`，不再有 `config.yaml` / `env.yaml`；原 1.4.1、1.4.2 两节合并为 `bootstrap.yaml` 一节，1.5 节从 LLM 校验改为 `LoadBootstrap` 加载流程（模型配置已迁入数据库，不在启动期校验），1.3 示例输出与 1.6 使用流程按当前"下一步"提示重写。文件形态变更本身的设计见 [启动配置设计](2026-09-27-bootstrap-config-design.md)
