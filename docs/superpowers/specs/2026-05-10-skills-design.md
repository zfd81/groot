# groot Skills 设计文档

## 一、功能设计

### 1.1 功能概述

Skills 是 Groot 的核心扩展机制，允许用户通过 Markdown 文件定义自定义技能，从而扩展 Agent 的能力边界。

- **声明式定义**：通过 SKILL.md 文件以自然语言描述技能，无需编写代码
- **自动注册**：启动时自动扫描并注册为 Agent 的 `skill` 工具
- **热插拔**：运行时动态添加、修改、删除 Skills，无需重启服务
- **渐进披露**：Agent 先看到 Skill 概览（名称 + 描述），按需加载完整指令内容
- **CLI 管理**：提供 `groot skills` 命令行工具进行 Skills 的安装、卸载、查看
- **多 Agent 隔离**：主 Agent 与每个子 Agent 各自拥有独立的 Skills 目录与后端

Skills 基于 eino 框架（[`github.com/cloudwego/eino/adk/middlewares/skill`](../../../go.mod)）的 skill 中间件实现，属于 Intelligence Layer（智能层）：

```
Agent → ChatModelAgentMiddleware（skill 中间件）
  ├── System Prompt 注入：列出可用 Skill 概览（仅主 Agent 自定义为 Markdown 表格）
  └── skill 工具注册：Agent 按需调用加载完整 SKILL.md
```

### 1.2 核心原则

- **声明式定义**：Skill 通过 Markdown 文件定义，无需编写代码，降低扩展门槛
- **文件即接口**：每个 Skill 一个目录，目录下放 SKILL.md，结构简单直观
- **热插拔优先**：eino Backend 无缓存设计，每次调用实时读取文件系统，Skills 变更即时生效
- **框架复用**：Skill 加载、解析、注册均由 eino 框架提供，Groot 只做目录管理与符号链接支持

### 1.3 目录结构

#### 源码目录

| 路径 | 说明 |
|------|------|
| [`cmd/groot/main.go`](../../../cmd/groot/main.go) | 程序入口：`skills` 子命令分发 + 主 Agent Skills 后端/中间件初始化 |
| [`internal/cmd/skills.go`](../../../internal/cmd/skills.go) | `groot skills` CLI 命令实现（list/install/uninstall） |
| [`internal/cmd/skills_test.go`](../../../internal/cmd/skills_test.go) | CLI 单元测试 |
| [`internal/api/handler/skills.go`](../../../internal/api/handler/skills.go) | `GET /skills` API 处理器，按 `X-Agent-Name` 选择主 Agent 或子 Agent 后端 |
| [`internal/api/types/types.go`](../../../internal/api/types/types.go) | `SkillsResponse` / `SkillInfo` 类型定义 |
| [`internal/agent/subagent_registry.go`](../../../internal/agent/subagent_registry.go) | 子 Agent 加载：为每个子 Agent 构建独立的 Skill Backend 与 Middleware |
| [`internal/agent/executor.go`](../../../internal/agent/executor.go) | Solo 模式下用子 Agent 的 Skill Backend 重建中间件 |
| [`internal/filesystem/symlink_backend.go`](../../../internal/filesystem/symlink_backend.go) | 包装 eino local backend，使 Glob 跟随符号链接，支持以软链方式安装 Skill |

#### 数据目录

主 Agent 的 Skills 目录固定为 `{GROOT_HOME}/skills`，每个子 Agent 拥有自己的 `{GROOT_HOME}/subagents/<agent>/skills/`：

```
{GROOT_HOME}/
├── skills/                       # 主 Agent 专属
│   ├── pdf_analyzer/
│   │   └── SKILL.md
│   └── code_generator/
│       └── SKILL.md
└── subagents/
    └── researcher/
        ├── agent.md
        └── skills/               # 子 Agent 专属
            └── web_search/
                └── SKILL.md
```

每个一级子目录下的 `SKILL.md` 即为一个 Skill 定义，eino 框架只扫描一级子目录。子 Agent 的 `skills/` 目录在加载时由 `os.MkdirAll` 兜底创建，目录为空即视为该子 Agent 没有专属 Skill。

### 1.4 Skill 定义格式

遵循 Claude Code 官方标准（YAML frontmatter + Markdown），frontmatter 字段由 eino 框架解析。

#### SKILL.md 结构

```markdown
---
name: skill_name
description: "技能描述，用于 Agent 工具列表展示"
context: fork              # 可选，执行模式
agent: default             # 可选，指定执行 Agent
model: gpt-4o              # 可选，指定执行模型
---

# Skill 标题

技能的详细指令和说明内容...
```

#### Frontmatter 字段说明

字段定义见 eino 的 `FrontMatter` 结构。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | Skill 名称（全局唯一） |
| `description` | string | 是 | Skill 描述，用于 Agent 工具列表展示 |
| `context` | string | 否 | 执行模式：空（inline，默认）/ `fork`（新 Agent 不带历史）/ `fork_with_context`（新 Agent 带历史） |
| `agent` | string | 否 | 指定执行此 Skill 的 Agent 名称，空则使用默认 Agent |
| `model` | string | 否 | 指定执行此 Skill 的 LLM 模型名称，空则使用默认模型 |

`description` 字段支持带引号和不带引号两种写法，CLI 在解析时会自动去除两侧的 `"` 或 `'`（见 [`readSkillDescription`](../../../internal/cmd/skills.go)）。

### 1.5 加载与注册

Skills 的加载、解析、注册由 eino 框架的 `skill` 中间件完成。Groot 只负责目录管理、符号链接支持以及为主 Agent 自定义 System Prompt 的渲染。

#### 主 Agent 初始化流程（[`cmd/groot/main.go`](../../../cmd/groot/main.go)）

```
程序启动
  ├── os.MkdirAll({GROOT_HOME}/skills)
  ├── local.NewBackend                                  # eino 提供的本地文件系统后端
  ├── filesystem.NewSymlinkBackend(localBackend)        # 包装：让 Glob 跟随符号链接
  ├── einoskill.NewBackendFromFilesystem
  │     BaseDir = {GROOT_HOME}/skills
  ├── einoskill.NewMiddleware
  │     Backend = skillBackend
  │     CustomSystemPrompt = 渲染 "## 可用 Skill" Markdown 表格
  └── log "Skills 加载完成 count=N dir=..."
```

注入的 System Prompt 形如：

```
## 可用 Skill

以下 Skill 提供专业能力和结构化工作流程。当用户请求与某个 Skill 描述匹配时，
必须使用 `skill` 工具加载完整指令后执行。

| Skill | 描述 |
|-------|------|
| **<name>** | <description> |
...

**重要**：以上仅为概要，完整操作指令需通过 `skill("<名称>")` 工具获取。
匹配到 Skill 时必须先加载再执行，不要跳过。
```

`skillBackend.List` 出错或返回空时，`CustomSystemPrompt` 直接返回空字符串，不向 Prompt 注入任何内容。

#### 子 Agent 初始化流程（[`internal/agent/subagent_registry.go`](../../../internal/agent/subagent_registry.go)）

`buildSubAgentEntry` 为每个子 Agent 装配专属的 Skill 资源：

```
对每个 subagents/<name>/
  ├── os.MkdirAll(<name>/skills)
  ├── local.NewBackend
  ├── filesystem.NewSymlinkBackend
  ├── einoskill.NewBackendFromFilesystem(BaseDir = <name>/skills)
  └── einoskill.NewMiddleware(Backend)            # 不附加 CustomSystemPrompt
```

构建结果保存在 `SubAgentEntry` 上：

- `SkillBK`：供 `/agents`、`/skills` API 查询元数据
- `SkillMW`：在 Solo 模式下作为子 Agent 的中间件挂载

#### 主 Agent vs 子 Agent 中间件挂载（[`internal/agent/executor.go`](../../../internal/agent/executor.go)）

Executor 在每次 `Execute` 时根据 `task.AgentName` 决定挂哪个中间件：

| 模式 | 触发条件 | 挂载的 Skill 中间件 |
|------|---------|-------------------|
| 编排模式 / 主 Agent | `task.AgentName` 为空或等于 `MainAgentName`（"groot"） | `Executor.middlewares`（启动期注入的主 Agent skill middleware） |
| Solo 模式 | `task.AgentName` 命中已注册子 Agent | 临时基于 `entry.SkillBK` 重建一个 `einoskill.NewMiddleware`，用以在当前 `parentCtx` 下绑定子 Agent 的 Skill 后端；构建失败则降级为「无 skill」并 log error |

#### 渐进披露与执行流程

Skill 中间件通过两个层面将 Skills 暴露给 Agent：

1. **System Prompt 注入**：`BeforeAgent` 阶段注入当前可用的 Skills 概览（主 Agent 是自定义的 Markdown 表格，子 Agent 是 eino 默认渲染），使 Agent 知道有哪些 Skill 可用
2. **skill 工具注册**：注册一个名为 `skill` 的工具，Agent 调用 `skill("<名称>")` 时，中间件从 Backend 加载完整的 SKILL.md 内容返回给 Agent

```
Agent 收到用户消息
  │
  ├── BeforeAgent: System Prompt ⇐ 可用 Skill 概览
  ├── Agent 识别到匹配的 Skill → 调用 skill("pdf_analyzer")
  │     │
  │     ├── skillTool.Info(): 调用 Backend.List() 获取最新 Skill 列表
  │     └── skillTool.InvokableRun(): 调用 Backend.Get("pdf_analyzer")
  │           │
  │           ├── inline 模式（默认）: 返回 SKILL.md 完整内容
  │           ├── fork 模式: 创建子 Agent 执行，不带历史消息
  │           └── fork_with_context 模式: 创建子 Agent 执行，带历史消息
  │
  └── Agent 根据 Skill 指令执行任务
```

#### eino Backend 接口

Groot 直接使用 eino 提供的 `Backend` 接口（无自定义实现）：

```go
type Backend interface {
    List(ctx context.Context) ([]FrontMatter, error)  // 列出所有 Skill 元数据
    Get(ctx context.Context, name string) (Skill, error)  // 获取完整 Skill
}
```

`einoskill.NewBackendFromFilesystem` 返回的实现 **无缓存设计**：每次 `List()` / `Get()` 调用都会重新扫描目录、读取文件。这意味着 Skills 变更后，下一次 Agent 调用 `skill` 工具时即生效，无需重启服务。

#### 符号链接支持（[`internal/filesystem/symlink_backend.go`](../../../internal/filesystem/symlink_backend.go)）

`SymlinkBackend` 包装 eino 的 `local` 文件系统后端，重写 `GlobInfo` 使其在遍历 `{skillsDir}/*/SKILL.md` 时跟随目录级别的符号链接，其余方法直接转发给被包装的 backend。这使得 Skill 既可以拷贝安装，也可以以 `ln -s` 软链方式接入。

### 1.6 热插拔机制

Skills 热插拔由 eino Backend 天然支持，无需额外组件。

`filesystemBackend` 不缓存任何数据，`List()` 和 `Get()` 每次都实时扫描 `{skillsDir}/*/SKILL.md`。因此 Skills 的增/删/改在下一次 Agent 调用 `skill` 工具时自动生效，无需重启服务，无需配置开关。

> 热插拔是 eino Backend 无缓存设计的自然结果，不是独立功能。

### 1.7 CLI 命令设计

`groot CLI` 提供 Skills 管理子命令，支持列出、安装、卸载 Skill。CLI 直接操作 `{GROOT_HOME}/skills/` 目录（即主 Agent 的 Skills 目录），不依赖运行中实例，子 Agent 的 Skills 目录由用户手动维护，不在 CLI 管理范围内。

#### 命令用法

```
groot skills <子命令> [选项]
```

| 子命令 | 参数 | 说明 |
|--------|------|------|
| `list` | 无 | 列出所有已安装的 Skills |
| `install` | `<path>` | 安装 Skill（支持绝对/相对路径） |
| `uninstall` | `<name>` | 卸载 Skill |

`-h` / `--help` 显示帮助文档。

#### list - 列出已安装 Skills

扫描 `{GROOT_HOME}/skills/` 目录，以表格形式展示。

- 只识别子目录，忽略普通文件
- 读取每个子目录下的 `SKILL.md` 获取描述和最后修改时间
- 缺少 `SKILL.md` 的子目录标记为「⚠ 缺少 SKILL.md」
- 未安装任何 Skill 时显示「未安装任何 Skill」
- Skills 目录不存在时也显示「未安装任何 Skill」
- 表格末尾输出汇总行，例如：`共 3 个 Skill（2 个有效，1 个异常）`；全部有效时省略括号部分

输出格式：

```
NAME             LAST_UPDATED         DESCRIPTION
---------------  -------------------  ----------------------------------
web-search       2026-05-01 10:30     支持多搜索引擎的智能检索
my-skill         2026-05-09 08:12     我的自定义技能
broken-skill                          ⚠ 缺少 SKILL.md

共 3 个 Skill（2 个有效，1 个异常）
```

列宽规则：
- NAME 列宽：以 `"NAME"`（4）为初始下界，按最长 Skill 名称动态计算，上限 30
- LAST_UPDATED 列宽：固定 16
- DESCRIPTION 列宽：以 `"DESCRIPTION"`（11）为初始下界，按最长描述（按 rune 计）动态计算，上限 60；超出时按 rune 截断到 57 加 `...`

#### install - 安装 Skill

将源目录拷贝到 `{GROOT_HOME}/skills/<目录名>/`。

安装流程：
1. 如果是相对路径，通过 `os.Getwd()` 转为绝对路径
2. 检查源路径存在且为目录
3. 检查源目录下存在 `SKILL.md`
4. 目标已存在时，先 `os.RemoveAll` 再拷贝（覆盖安装）
5. 递归拷贝所有文件和子目录，保留文件权限（`os.Chmod` 同步源文件 Mode）
6. 输出 `Skill "<name>" 安装成功` 与目标路径

#### uninstall - 卸载 Skill

删除 `{GROOT_HOME}/skills/<name>/` 目录。

- 检查目录存在，且必须是目录
- 直接 `os.RemoveAll`，无需确认
- 已删除时再次执行报「Skill "<name>" 不存在」错误
- 输出 `Skill "<name>" 已卸载`

#### 错误处理

| 场景 | 处理 |
|------|------|
| 缺少子命令 | 报「缺少子命令: list, install, uninstall」，exit 1 |
| 未知子命令 | 报「未知子命令: X (可用: list, install, uninstall)」，exit 1 |
| install 缺少路径 | 报「install 子命令需要指定 Skill 路径」，exit 1 |
| install 多余位置参数 | 报「install 子命令只接受一个路径参数」，exit 1 |
| install 源路径不存在 | 报「源路径不存在」，exit 1 |
| install 源路径不是目录 | 报「源路径不是目录」，exit 1 |
| install 缺少 SKILL.md | 报「源目录中缺少 SKILL.md 文件」，exit 1 |
| uninstall 缺少名称 | 报「uninstall 子命令需要指定 Skill 名称」，exit 1 |
| uninstall 多余位置参数 | 报「uninstall 子命令只接受一个名称参数」，exit 1 |
| uninstall Skill 不存在 | 报「Skill "X" 不存在」，exit 1 |
| list 收到额外参数 | 报「unexpected argument: X」，exit 1 |
| 未知 flag | 报「unknown flag: X」，exit 1 |

#### 核心数据结构

```go
type SkillsFlags struct {
    Subcommand string // list, install, uninstall
    Path       string // source path for install
    Name       string // skill name for uninstall
}

type skillItem struct {
    name        string
    description string
    valid       bool
    lastUpdated string
}
```

### 1.8 API 端点

`GET /skills` 返回当前 Skill 列表，请求头通过 `X-Agent-Name` 指定查询主 Agent 还是某个子 Agent 的 Skill 后端，详细约定见 [`SkillsHandler`](../../../internal/api/handler/skills.go)：

- 不传 / `X-Agent-Name == "groot"`：返回主 Agent 后端的 Skill 列表
- 非空且非 `groot`：从 `SubAgentRegistry` 中查找对应子 Agent，未注册返回 400 `unknown_agent`
- 后端为 nil 或 `List` 出错：降级返回 `200 + 空数组`，并通过 `logger.Info` 记录原因

响应类型（[`internal/api/types/types.go`](../../../internal/api/types/types.go)）：

```go
type SkillsResponse struct {
    Skills []SkillInfo `json:"skills"`
    Total  int         `json:"total"`
}

type SkillInfo struct {
    Name        string `json:"name"`
    Description string `json:"description"`
}
```

> 完整 API 路由、Schema 见 [API 设计文档](2026-05-16-api-design.md)。Skills 热插拔基于 eino Backend 的无缓存设计，无需在 `config.yaml` 中配置。

### 1.9 Skill 示例

#### pdf_analyzer（inline 模式）

```markdown
---
name: pdf_analyzer
description: "分析PDF文档内容，提取关键信息并生成结构化摘要报告"
---

# PDF 文档分析

你是一个专业的PDF文档分析助手。

## 执行步骤

1. 使用 groot_file_read 内置工具按文件名读取 PDF 附件
2. 提取文档的关键内容和结构
3. 根据文档类型生成相应的结构化摘要
4. 输出结构化的分析结果

## 输出格式

{
  "document_type": "文档类型",
  "title": "文档标题",
  "key_points": ["关键要点"],
  "summary": "详细摘要",
  "recommendations": ["建议"]
}
```

#### report_generator（fork 模式）

使用 `context: fork` 创建独立子 Agent 执行，不带父 Agent 的历史消息：

```markdown
---
name: report_generator
description: "综合分析多种来源的资料，生成完整的分析报告"
context: fork
agent: default
---

# 报告生成

你是一个报告生成助手，可调用其他 Skills 完成综合分析。

## 执行步骤

1. 分析用户提供的资料类型
2. 根据资料类型调用相应的分析 Skills
3. 整合各 Skills 的分析结果
4. 生成结构化的综合报告
5. 使用 file_operations.file_write 保存报告
```

### 1.10 测试

CLI 测试见 [`internal/cmd/skills_test.go`](../../../internal/cmd/skills_test.go)，API Handler 测试见 [`internal/api/handler/skills_test.go`](../../../internal/api/handler/skills_test.go)，符号链接后端测试见 [`internal/filesystem/symlink_backend_test.go`](../../../internal/filesystem/symlink_backend_test.go)。

#### CLI 测试要点（`internal/cmd/skills_test.go`）

| 测试项 | 验证内容 |
|--------|----------|
| 参数解析 | list/install/uninstall 正确解析 |
| 参数解析 | 无参数报错（`缺少子命令: list, install, uninstall`） |
| 参数解析 | 未知子命令报错 |
| 参数解析 | install 缺少路径报错 |
| 参数解析 | uninstall 缺少名称报错 |
| 参数解析 | install 多余参数报错 |
| 参数解析 | list 多余参数报错 |
| 参数解析 | 未知 flag 报错 |
| SKILL.md 解析 | 带引号描述正确提取 |
| SKILL.md 解析 | 不带引号描述正确提取 |
| SKILL.md 解析 | 无 description 字段返回空 |
| SKILL.md 解析 | 无 frontmatter 返回空 |
| list | 表格输出包含 NAME/LAST_UPDATED/DESCRIPTION 列、有效/异常计数 |
| list | 空目录显示「未安装任何 Skill」 |
| list | 不存在目录显示「未安装任何 Skill」 |
| install | 目录、子目录文件正确拷贝 |
| install | 覆盖安装旧文件被清除 |
| install | 源路径不存在报「源路径不存在」 |
| install | 缺少 SKILL.md 报「缺少 SKILL.md」 |
| uninstall | 目录正确删除 |
| uninstall | Skill 不存在报错 |
| 集成测试 | install → list → uninstall → list 完整流程 |

#### API Handler 测试要点（`internal/api/handler/skills_test.go`）

| 测试项 | 验证内容 |
|--------|----------|
| `X-Agent-Name` 不存在的子 Agent | 返回 400 + `unknown_agent` |
| `SubAgentRegistry == nil` 且指定子 Agent | 返回 400 + `unknown_agent`，且打印警告日志 |
| 不传 / `X-Agent-Name = "groot"` | 返回主 Agent backend 的 Skill 列表 |
| `X-Agent-Name = <子 Agent>` | 返回该子 Agent 专属 backend 的 Skill 列表 |
| 后端 `List` 报错 | 降级返回 `200 + 空 SkillsResponse` |

## 二、迭代说明

### 2.1 与上一版差异

- **新增**：子 Agent 专属 Skills 目录与后端的描述（`{GROOT_HOME}/subagents/<name>/skills/`、`buildSubAgentEntry`、`SubAgentEntry.SkillBK/SkillMW`）
- **新增**：Executor 在 Solo 模式下基于子 Agent `SkillBK` 临时构建中间件并降级处理失败的说明
- **新增**：主 Agent 的 `CustomSystemPrompt` 渲染细节（"## 可用 Skill" Markdown 表格 + 重要提示）
- **新增**：`SymlinkBackend` 的角色与 `GlobInfo` 重写说明，并加入到源码目录表
- **新增**：`GET /skills` 通过 `X-Agent-Name` 选择主/子 Agent 后端、降级与日志策略，并把响应类型从外链补回文档
- **新增**：CLI `list` 输出末尾的汇总行规则、列宽下界以及描述按 rune 截断的细节
- **新增**：CLI 错误处理表的具体错误文案、`-h` / `--help` 行为、文件权限保留方式
- **新增**：API Handler 测试要点
- **调整**：源码目录从 ASCII 树改为表格 + 仓库链接，覆盖更全（增加 `subagent_registry.go`、`executor.go`、`symlink_backend.go`）
- **调整**：将「概述 / 核心原则 / 目录结构 / Skill 定义 / 加载与注册 / 热插拔 / CLI / 示例 / 测试」收纳到第一章「功能设计」，按项目设计文档结构规范分章节
- **移除**：原文中「Groot 使用 eino 提供的 `Backend` 接口，实现为 `filesystemBackend`」的说法（仓库内并无名为 `filesystemBackend` 的自定义实现，全部走 eino 框架）
- **移除**：错误处理表中重复的「未知子命令」条目（已合并为单条带具体文案）
- **移除**：散落在文末的孤立行 `internal/cmd/skills_test.go — CLI 参数解析测试`（已并入测试章节并补充链接）
