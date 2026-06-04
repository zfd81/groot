# groot Skills 设计文档

## 概述

Skills 是 Groot 的核心扩展机制，允许用户通过 Markdown 文件定义自定义技能，从而扩展 Agent 的能力边界。

- **声明式定义**：通过 SKILL.md 文件以自然语言描述技能，无需编写代码
- **自动注册**：启动时自动扫描并注册为 Agent 的 `skill` 工具
- **热插拔**：运行时动态添加、修改、删除 Skills，无需重启服务
- **渐进披露**：Agent 先看到 Skill 概览（名称 + 描述），按需加载完整指令内容
- **CLI 管理**：提供 `groot skills` 命令行工具进行 Skills 的安装、卸载、查看

Skills 基于 eino 框架的 skill 中间件实现，属于 Intelligence Layer（智能层）：

```
Agent → ChatModelAgentMiddleware（skill 中间件）
  ├── System Prompt 注入：列出可用 Skill 概览
  └── skill 工具注册：Agent 按需调用加载完整 SKILL.md
```

## 核心原则

- **声明式定义**：Skill 通过 Markdown 文件定义，无需编写代码，降低扩展门槛
- **文件即接口**：每个 Skill 一个目录，目录下放 SKILL.md，结构简单直观
- **热插拔优先**：eino Backend 无缓存设计，每次调用实时读取文件系统，Skills 变更即时生效
- **框架复用**：Skill 加载、解析、注册均由 eino 框架提供，Groot 只做目录管理

## 目录结构

### 源码目录

```
cmd/groot/main.go            # CLI 入口：skills 子命令分发 + 服务端 Skills 初始化
internal/
├── cmd/
│   ├── skills.go            # skills CLI 命令实现（list/install/uninstall）
│   └── skills_test.go       # CLI 单元测试
├── api/
│   ├── handler/skills.go    # GET /skills API 处理器
│   └── types/types.go       # SkillsResponse / SkillInfo 类型定义
```

### 数据目录

Skills 目录固定为 `{GROOT_HOME}/skills`，不可配置。

```
{GROOT_HOME}/skills/
├── pdf_analyzer/
│   └── SKILL.md
├── code_generator/
│   └── SKILL.md
└── data_analyzer/
    └── SKILL.md
```

每个子目录下的 `SKILL.md` 即为一个 Skill 定义，eino 框架只扫描一级子目录。

## Skill 定义格式

遵循 Claude Code 官方标准（YAML frontmatter + Markdown），frontmatter 字段由 eino 框架解析。

### SKILL.md 结构

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

### Frontmatter 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | Skill 名称（全局唯一） |
| `description` | string | 是 | Skill 描述，用于 Agent 工具列表展示 |
| `context` | string | 否 | 执行模式：空（inline，默认）/ `fork`（新 Agent 不带历史）/ `fork_with_context`（新 Agent 带历史） |
| `agent` | string | 否 | 指定执行此 Skill 的 Agent 名称，空则使用默认 Agent |
| `model` | string | 否 | 指定执行此 Skill 的 LLM 模型名称，空则使用默认模型 |

`description` 字段支持带引号和不带引号两种写法。

## 加载与注册

Skills 的加载、解析、注册由 eino 框架的 `skill` 中间件完成。

### 初始化流程

```
程序启动 → 创建 local filesystem backend →
创建 SymlinkBackend（支持符号链接） →
einoskill.NewBackendFromFilesystem（配置 BaseDir = {GROOT_HOME}/skills） →
einoskill.NewMiddleware（创建 ChatModelAgentMiddleware） →
  ├── BeforeAgent: 向 System Prompt 注入 Skill 概览（名称 + 描述）
  └── 注册 skill 工具: Agent 按需调用，加载完整 SKILL.md 内容
```

### 注册给 Agent 的机制

Skill 中间件通过两个层面将 Skills 暴露给 Agent：

1. **System Prompt 注入**：`BeforeAgent` 阶段向 agent 指令中注入当前可用的 Skills 列表（名称 + 描述），使 Agent 知道有哪些 Skill 可用
2. **skill 工具注册**：注册一个名为 `skill` 的工具，Agent 调用 `skill("<名称>")` 时，中间件从 Backend 加载完整的 SKILL.md 内容返回给 Agent

这种「渐进披露」设计使 Agent 先看到概要再按需获取详情，避免 System Prompt 过长。

### 中间件执行流程

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

### eino Backend 接口

Groot 使用 eino 提供的 `Backend` 接口，实现为 `filesystemBackend`：

```go
type Backend interface {
    List(ctx context.Context) ([]FrontMatter, error)  // 列出所有 Skill 元数据
    Get(ctx context.Context, name string) (Skill, error)  // 获取完整 Skill
}
```

`filesystemBackend` **无缓存设计**：每次 `List()` / `Get()` 调用都会重新扫描目录、读取文件。这意味着 Skills 变更后，下一次 Agent 调用 `skill` 工具时即生效，无需重启服务。

## 热插拔机制

Skills 热插拔由 eino Backend 天然支持，无需额外组件。

`filesystemBackend` 不缓存任何数据，`List()` 和 `Get()` 每次都实时扫描 `{GROOT_HOME}/skills/*/SKILL.md`。因此 Skills 的增/删/改在下一次 Agent 调用 `skill` 工具时自动生效，无需重启服务，无需配置开关。

> 热插拔是 eino Backend 无缓存设计的自然结果，不是独立功能。

## CLI 命令设计

为 groot CLI 添加 Skills 管理子命令，支持列出、安装、卸载 Skill。CLI 直接操作 `{GROOT_HOME}/skills/` 目录，不依赖运行中实例。

### 命令用法

```
groot skills <子命令> [选项]
```

| 子命令 | 参数 | 说明 |
|--------|------|------|
| `list` | 无 | 列出所有已安装的 Skills |
| `install` | `<path>` | 安装 Skill（支持绝对/相对路径） |
| `uninstall` | `<name>` | 卸载 Skill |

### list - 列出已安装 Skills

扫描 `{GROOT_HOME}/skills/` 目录，以表格形式展示。

- 只识别子目录，忽略普通文件
- 读取每个子目录下的 `SKILL.md` 获取描述和最后修改时间
- 缺少 `SKILL.md` 的子目录标记为「⚠ 缺少 SKILL.md」
- 未安装任何 Skill 时显示「未安装任何 Skill」
- Skills 目录不存在时也显示「未安装任何 Skill」

输出格式：

```
NAME             LAST_UPDATED         DESCRIPTION
---------------  -------------------  ----------------------------------
web-search       2026-05-01 10:30     支持多搜索引擎的智能检索
my-skill         2026-05-09 08:12     我的自定义技能
broken-skill                          ⚠ 缺少 SKILL.md
```

列宽规则：
- NAME 列宽：根据最长 Skill 名称动态计算，上限 30
- LAST_UPDATED 列宽：固定 16
- DESCRIPTION 列宽：根据最长描述动态计算，上限 60（超出截断加 `...`）

### install - 安装 Skill

将源目录拷贝到 `{GROOT_HOME}/skills/<目录名>/`。

安装流程：
1. 如果是相对路径，通过 `os.Getwd()` 转为绝对路径
2. 检查源路径存在且为目录
3. 检查源目录下存在 `SKILL.md`
4. 目标已存在时，先删除再拷贝（覆盖安装）
5. 递归拷贝所有文件和子目录，保留文件权限

### uninstall - 卸载 Skill

删除 `{GROOT_HOME}/skills/<name>/` 目录。

- 检查目录存在
- 直接删除，无需确认
- 已删除时再次执行报错

### 错误处理

| 场景 | 处理 |
|------|------|
| 未知子命令 | 输出错误信息，exit 1 |
| install 缺少路径 | 输出错误信息，exit 1 |
| install 源路径不存在 | 输出错误信息，exit 1 |
| install 缺少 SKILL.md | 输出错误信息，exit 1 |
| uninstall 缺少名称 | 输出错误信息，exit 1 |
| uninstall Skill 不存在 | 输出错误信息，exit 1 |
| list 收到额外参数 | 输出错误信息，exit 1 |
| 未知 flag | 输出错误信息，exit 1 |

### 核心数据结构

```go
type SkillsFlags struct {
    Subcommand string // list, install, uninstall
    Path       string // source path for install
    Name       string // skill name for uninstall
}
```

```go
type skillItem struct {
    name        string
    description string
    valid       bool
    lastUpdated string
}
```

> API 端点定义已抽取至 [API 设计文档](2026-05-16-api-design.md)。

Skills 热插拔基于 eino Backend 的无缓存设计，无需在 `config.yaml` 中配置。

## Skill 示例

### pdf_analyzer

```markdown
---
name: pdf_analyzer
description: "分析PDF文档内容，提取关键信息并生成结构化摘要报告"
---

# PDF 文档分析

你是一个专业的PDF文档分析助手。

## 执行步骤

1. 使用 file_operations.file_read 工具读取PDF文件
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

### report_generator（fork 模式 Skill 示例）

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

## 测试

### 测试要点

| 测试项 | 验证内容 |
|--------|----------|
| 参数解析 | list/install/uninstall 正确解析 |
| 参数解析 | 无参数报错 |
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
| list | 表格输出包含所有必要列 |
| list | 有效/无效 Skill 正确标记 |
| list | 空目录显示提示 |
| list | 不存在目录显示提示 |
| install | 目录和文件正确拷贝 |
| install | 覆盖安装旧文件被清除 |
| install | 源路径不存在报错 |
| install | 缺少 SKILL.md 报错 |
| uninstall | 目录正确删除 |
| uninstall | 不存在报错 |
| 集成测试 | install → list → uninstall → list 完整流程 |

- `internal/cmd/skills_test.go` — CLI 参数解析测试
