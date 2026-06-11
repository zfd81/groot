# Groot Init 命令设计文档

## 背景

当前 Groot 启动时会自动检查配置文件是否存在，如果不存在则创建默认配置文件。这种方式存在问题：

1. 自动创建的配置文件中 LLM 信息（如 API Key）是占位符，用户需要手动编辑才能正常使用
2. 即使配置不正确，服务也会尝试启动，最终因 LLM 配置错误而失败，用户体验不佳
3. 用户不清楚需要做哪些准备工作，缺少明确的初始化流程指引

## 目标

将初始化和启动流程分离，提供明确的初始化命令：

1. 新增 `groot init` 子命令，用于初始化工作目录和配置文件
2. 用户必须先完成初始化，编辑配置后才能启动服务
3. 启动时提供清晰的错误提示，指明需要做什么

## 设计方案

### 一、命令行结构

新增 `init` 子命令，命令行结构变为：

```
groot [选项]              # 启动服务（无子命令）
groot init [选项]         # 初始化工作目录
groot tail [选项]         # 实时日志查看（已有）

子命令选项：
  init:
    -h, --help            # 显示 init 帮助

全局选项：
  -p, --port <port>       # HTTP端口（仅启动服务时有效）
  -h, --help              # 显示帮助
  -v, --version           # 显示版本
```

### 二、`groot init` 子命令行为

**执行流程**：

1. 确定工作目录（通过 `GROOT_HOME` 环境变量或默认 `~/.groot`）
2. 检查并创建目录（每个目录单独处理）：
   - 工作目录根目录
   - `skills/` 子目录（共享配置：技能集）
   - `mcp/` 子目录（共享配置：MCP 服务定义）
   - `subagents/` 子目录（共享配置：子 Agent 定义）
   - `logs/` 子目录（运行日志）
3. 检查并创建配置模板文件 `config.yaml`
4. 检查并创建基础设施环境配置模板 `env.yaml`（**全注释模板**，启用数据库后端时取消注释）
5. 检查并创建 `GROOT.md`（主 Agent 全局指导）

> **不再创建** `memory/`、`schedules/`、`cluster/members/` 子目录：运行时数据（会话/对话、定时任务、集群成员）已迁入数据库（SQLite/MySQL/PostgreSQL）。SQLite 模式下数据库文件为 `~/.groot/groot.db`，由首次启动时自动创建。

**目录/文件检查逻辑**：

对于每个目录和文件：
- 已存在 → 提示"xxx 已存在，跳过创建"
- 不存在 → 创建并提示"xxx 创建成功"

**输出示例**：

```
初始化 Groot 工作目录...

工作目录 ~/.groot 已存在，跳过创建
目录 skills 已存在，跳过创建
目录 mcp 已存在，跳过创建
目录 subagents 创建成功
目录 logs 创建成功
配置文件 config.yaml 已存在，跳过创建
环境配置 env.yaml 创建成功
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

**`config.yaml` 模板内容**（不再生成 `storage:` 节，凭据走 env.yaml）：

```yaml
# Groot Agent 配置文件
# 请根据实际情况修改以下配置

# LLM 配置（必填）
# 请填写你的 LLM API 信息，支持 OpenAI 兼容协议
llm:
  default_model: gpt-4o           # 默认模型名称
  models:
    gpt-4o:
      base_url: https://api.openai.com/v1    # API 地址
      api_key: ${OPENAI_API_KEY}             # API 密钥（建议使用环境变量）
      model: gpt-4o                          # 模型名称
      max_completion_tokens: 4096                       # 最大 Token 数
      temperature: 0.7                       # 温度参数（0.0~2.0）
      top_p: 1.0                             # 核采样系数（0.0~1.0）
      frequency_penalty: 0.0                 # 频率惩罚（-2.0~2.0）
      presence_penalty: 0.0                  # 存在惩罚（-2.0~2.0）
      seed: 0                                # 随机种子（0 表示不设置）
      stop: []                               # 停止序列
      thinking: false                        # 深度思考模式（Qwen/DeepSeek 等模型）

# 其他配置项均有默认值，可按需修改
# 完整配置说明请参考：https://github.com/zfd81/groot
```

**`env.yaml` 模板内容**（由 `config.GenerateEnvTemplate()` 生成，全注释）：

```yaml
# Groot 基础设施环境配置
# 存放 MinIO 等外部服务的连接凭据，与业务配置 (config.yaml) 解耦。
#
# 默认情况下整个文件为注释，附件等文件存储走本地磁盘（零配置）。
# 如需启用 MinIO 对象存储，取消下方 minio 块的注释并填入连接信息：
#   - 删除整个文件 → 回退到本地磁盘存储
#   - 删除 minio 节（或保持注释）→ 回退到本地磁盘存储
#   - 完整填写 minio 节 → 启用 MinIO

#minio:
#  endpoint: localhost:9000          # MinIO 服务地址（host:port）
#  access_key: ${MINIO_ACCESS_KEY}   # 访问密钥（建议使用环境变量）
#  secret_key: ${MINIO_SECRET_KEY}   # 密钥（建议使用环境变量）
#  bucket: groot                     # 存储桶名称
#  use_ssl: false                    # 是否启用 HTTPS
```

> 模板使用"先缩进后 #"格式（如 `#  endpoint:`），用户删掉行首 `#` 后 yaml 缩进自动正确。env.yaml 加载机制详见 [存储抽象层设计](2026-06-06-storage-interface-design.md) §1.6 / §1.7。

### 三、`groot` 启动流程改动

**原来的逻辑**（在 `config.Load` 中）：

```
配置不存在 → 自动创建默认配置 → 验证 → 返回配置（可能因 LLM 配置不完整而启动失败）
配置存在 → 加载 → 验证 → 返回配置
```

**新的逻辑**：

```
配置不存在 → 返回错误："配置文件不存在，请先运行 'groot init' 初始化"
配置存在 → 加载 → 验证 → 返回详细错误信息
```

**验证逻辑增强**：

启动时检查以下内容，并给出具体错误提示：

| 检查项 | 错误提示 |
|--------|----------|
| config.yaml 不存在 | "配置文件不存在，请先运行 'groot init' 初始化" |
| models 配置为空 | "LLM models 配置为空，请编辑 config.yaml 添加模型配置" |
| api_key 使用环境变量但未设置 | "环境变量 OPENAI_API_KEY 未设置，请设置后重试" |
| api_key 为空字符串 | "模型 gpt-4o 的 api_key 为空，请编辑 config.yaml" |
| base_url 为空 | "模型 gpt-4o 的 base_url 为空，请编辑 config.yaml" |

**输出示例**：

配置不存在：

```
错误: 配置文件不存在，请先运行 'groot init' 初始化

提示: groot init
```

环境变量未设置：

```
错误: LLM 配置验证失败

详情: 环境变量 OPENAI_API_KEY 未设置
提示: export OPENAI_API_KEY="your-api-key"
      或在 config.yaml 中直接填写 api_key
```

### 四、代码改动范围

**新增文件**：

| 文件 | 说明 |
|------|------|
| `internal/cmd/init.go` | `init` 子命令实现（创建目录、生成配置模板） |

**修改文件**：

| 文件 | 改动内容 |
|------|----------|
| `cmd/groot/main.go` | 新增 `handleInitCommand` 函数，修改子命令 switch 分支 |
| `internal/config/loader.go` | 移除自动创建配置的逻辑，改为返回错误提示；增强验证逻辑 |
| `internal/config/defaults.go` | 新增 `TemplateConfig()` 函数，生成带注释的配置模板 |

**改动要点**：

1. **`internal/cmd/init.go`**（新增）
   - 实现 `RunInit(homeDir string)` 函数
   - 遍历创建各目录，对每个目录检查是否已存在
   - 创建配置模板文件（如果不存在）

2. **`cmd/groot/main.go`**
   - 在 switch 分支中新增 `case "init"`
   - 调用 `cmd.RunInit(homeDir)`

3. **`internal/config/loader.go`**
   - `Load()` 函数：配置不存在时返回明确错误，不再自动创建
   - 验证逻辑：检查环境变量是否设置、字段是否为空等

4. **`internal/config/defaults.go`**
   - 新增 `TemplateConfig()` 返回带注释提示的配置内容

## 用户使用流程

```bash
# 1. 初始化工作目录
groot init

# 2. 编辑配置文件，填写 LLM 信息
vim ~/.groot/config.yaml

# 3. 设置环境变量（如果配置使用了环境变量引用）
export OPENAI_API_KEY="your-api-key"

# 4. 启动服务
groot
```