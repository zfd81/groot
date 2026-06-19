# Groot Init 命令设计文档

## 一、功能设计

### 1.1 功能概述

`groot init` 是初始化命令，负责为 Groot Agent 准备好运行所需的工作目录、配置文件模板和主 Agent 全局指导文件。用户首次安装 Groot 后必须先运行 `groot init` 完成初始化，再编辑配置文件填入 LLM API 信息，最终通过 `groot` 启动服务。

初始化命令解决以下问题：

1. 让用户拥有一个明确的"准备工作"入口，而不是在启动失败的报错信息里摸索
2. 把目录和模板文件的创建从启动阶段剥离出来，启动阶段只负责加载、校验和运行
3. 模板文件提示用户哪些字段必填、哪些字段可选，并演示环境变量引用语法

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
3. 创建配置模板文件 `config.yaml`
4. 创建基础设施环境配置模板 `env.yaml`（默认全注释）
5. 创建主 Agent 全局指导文件 `GROOT.md`
6. 输出"下一步"操作指引

子目录的用途：

| 目录 | 用途 |
|------|------|
| `skills/` | 共享配置：技能集 |
| `mcp/` | 共享配置：MCP 服务定义 |
| `subagents/` | 共享配置：子 Agent 定义 |
| `logs/` | 运行日志 |

会话/对话、定时任务、集群成员等运行时数据存储在数据库（SQLite/MySQL/PostgreSQL）中，不再以本地目录形式存在。SQLite 模式下数据库文件为 `~/.groot/groot.db`，由首次启动时自动创建。

**目录与文件检查逻辑**：每个目录和文件单独处理，已存在则提示"已存在，跳过创建"，不存在则创建并提示"创建成功"，永远不会覆盖用户已有内容。`env.yaml` 文件以 `0600` 权限创建（包含凭据相关注释），其他文件以 `0644`、目录以 `0755` 创建。

**输出示例**（首次执行，工作目录不存在）：

```
初始化 Groot 工作目录...

工作目录 ~/.groot 创建成功
目录 skills /Users/alice/.groot/skills 创建成功
目录 mcp /Users/alice/.groot/mcp 创建成功
目录 subagents /Users/alice/.groot/subagents 创建成功
目录 logs /Users/alice/.groot/logs 创建成功
配置文件 config.yaml 创建成功
环境配置文件 env.yaml 创建成功
GROOT.md 创建成功

初始化完成

下一步：
  1. 编辑配置文件，填写 LLM API 信息
     vim ~/.groot/config.yaml
  2. 设置环境变量（如果配置文件使用了 ${VAR_NAME}）
     export OPENAI_API_KEY="your-api-key"
  3. （可选）启用数据库后端：编辑环境配置文件
     vim ~/.groot/env.yaml   # 默认全注释 → SQLite 本地模式
  4. 启动服务
     groot
```

工作目录根目录在输出中会被缩写为 `~/.groot`（前提是它位于 `$HOME` 下），子目录则保留完整路径，缩写规则见 `shortenPath`。

### 1.4 生成的配置模板

#### 1.4.1 `config.yaml`

由 [`config.GenerateConfigTemplate`](../../../internal/config/template.go) 生成。模板结构：

- 顶部 `llm` 节为**必填**示例，包含 `default_model` 与 `models.gpt-4o`，所有字段已填好默认值
- 其余配置项（`agent` / `server` / `react` / `attachment` / `memory` / `schedule` / `subagent` / `security` / `logging`）以**注释形式**列出，注明默认值，用户取消注释即可覆盖
- 模板不再生成 `storage` 节：MinIO 等基础设施凭据已剥离到 `env.yaml`

`llm.models.gpt-4o` 中包含的字段：`base_url`、`api_key`、`model`、`max_completion_tokens`、`temperature`、`top_p`、`frequency_penalty`、`presence_penalty`、`seed`、`stop`、`thinking`。`api_key` 默认值是 `${OPENAI_API_KEY}`，引导用户优先使用环境变量。

#### 1.4.2 `env.yaml`

由 [`config.GenerateEnvTemplate`](../../../internal/config/env_template.go) 生成，文件名常量 `config.EnvFileName`。模板内容**全注释**，等价于本地零配置模式：

```yaml
# Groot 基础设施环境配置
# 存放数据库等外部服务的连接凭据，与业务配置 (config.yaml) 解耦。
#
# 默认情况下整个文件为注释（cfg.Database == nil）。
# 如需启用数据库后端，取消下方 database 块的注释并填入连接信息：
#   - 删除整个文件 → cfg.Database 为 nil
#   - 删除 database 节（或保持注释）→ cfg.Database 为 nil
#   - 完整填写 database 节 → 启用数据库

#database:
#  driver: sqlite                       # "sqlite" | "mysql" | "postgres"
#  dsn: ${DB_DSN}                       # 连接字符串（建议使用环境变量）
#  max_open_conns: 20                   # 最大打开连接数
#  max_idle_conns: 5                    # 最大空闲连接数
#  conn_max_lifetime: 30m               # 连接最大生命周期
```

模板使用"先缩进后 `#`"格式（如 `#  driver:`），用户删掉行首 `#` 后 yaml 缩进自动正确。env.yaml 加载机制与字段语义详见 [数据库后端设计](2026-06-10-database-backend-design.md) §1.5。

不配置 `database` 节使用 SQLite 单机模式（数据库文件 `~/.groot/groot.db`）；配置 `driver=mysql/postgres` 进入远端数据库多主机模式。

#### 1.4.3 `GROOT.md`

主 Agent 全局指导文件，每次对话都会作为 system 提示注入。`init` 写入的默认内容引导用户填写自己的规则、风格、目标，并附有"子 Agent 调度"段落，告诉主 Agent 在拥有 `call_agent` 工具时如何使用子 Agent（按需调用、逐个调用、明确传参、附件引用）。具体内容见 [`internal/cmd/init.go`](../../../internal/cmd/init.go) 的 `defaultGrootMdContent`。

### 1.5 启动时的配置校验

`groot` 启动流程通过 [`config.Load`](../../../internal/config/loader.go) 加载并校验配置：

- `config.yaml` 不存在 → 返回错误 `配置文件不存在，请先运行 'groot init' 初始化`
- 解析 yaml 失败 → 返回 `failed to parse config file: ...`
- 加载 `env.yaml` 失败 → 返回 `failed to load env file: ...`
- LLM 配置校验由 [`ValidateLLMConfig`](../../../internal/config/config.go) 执行

`ValidateLLMConfig` 的检查项与对应错误：

| 检查项 | 错误提示 |
|--------|----------|
| `models` 为空 | `LLM models 配置为空，请编辑 config.yaml 添加模型配置` |
| `default_model` 未填 | 自动取 `models` 中的第一项 |
| `default_model` 不在 `models` 中 | `default_model 'xxx' 不存在于 models 配置中` |
| `base_url` 为空 | `模型 xxx 的 base_url 为空，请编辑 config.yaml` |
| `api_key` 为空 | `模型 xxx 的 api_key 为空，请编辑 config.yaml 或设置对应的环境变量` |
| `api_key` 引用的环境变量未设置 | `环境变量 XXX 未设置，请设置后重试`，并附带 `export` 提示 |
| `temperature` / `top_p` / `frequency_penalty` / `presence_penalty` 超出有效范围 | 各自具体的范围错误信息 |

启动入口 [`cmd/groot/main.go`](../../../cmd/groot/main.go) 中的 `startServer` 在加载失败时输出 `无法加载配置: <详情>` 并退出，将上述错误透传给用户。

### 1.6 用户使用流程

```bash
# 1. 初始化工作目录
groot init

# 2. 编辑配置文件，填写 LLM 信息
vim ~/.groot/config.yaml

# 3. 设置环境变量（如果配置使用了环境变量引用）
export OPENAI_API_KEY="your-api-key"

# 4. （可选）启用远端数据库
vim ~/.groot/env.yaml

# 5. 启动服务
groot
```

## 二、迭代说明

### 2.1 与上一版差异

- **新增** `groot init` 子命令，集中处理工作目录、配置模板、`env.yaml`、`GROOT.md` 的创建
- **新增** [`internal/cmd/init.go`](../../../internal/cmd/init.go)、[`internal/config/template.go`](../../../internal/config/template.go)、[`internal/config/env_template.go`](../../../internal/config/env_template.go)
- **新增** `GROOT.md` 默认模板（含子 Agent 调度引导段）
- **移除** `config.Load` 中"配置不存在则自动创建默认配置"的逻辑，未初始化时直接返回错误并提示运行 `groot init`
- **移除** `memory/`、`schedules/`、`cluster/members/` 等运行时数据目录的创建：相关数据已迁入数据库（SQLite/MySQL/PostgreSQL）
- **移除** 配置模板中的 `storage` 节：MinIO 等基础设施凭据集中放在 `env.yaml`
- **调整** 启动期 LLM 校验，增强为细分错误：models 为空、base_url/api_key 为空、环境变量未设置、参数超出范围等，错误信息中给出具体修复操作
