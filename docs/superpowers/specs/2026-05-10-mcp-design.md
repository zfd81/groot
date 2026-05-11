# groot MCP 设计文档

## 概述

MCP（Model Context Protocol）是 Groot 集成外部工具的标准化协议。通过配置 MCP Server，Groot 可以调用外部工具（如文件操作、网络请求、数据库查询等），从而扩展 Agent 的能力边界。

**核心特性：**
- **多连接类型**：支持 stdio、sse、streamable_http 三种连接方式，覆盖本地和远程场景
- **自动工具发现**：连接 MCP Server 后自动调用 `tools/list` 发现可用工具，无需手动配置
- **独立配置**：每个 MCP Server 以独立 JSON 文件管理，配置清晰可维护
- **CLI 管理**：提供 `groot mcp` 命令行工具查看已配置的 MCP Server

**MCP 在架构中的位置：**

MCP 属于 Intelligence Layer（智能层），外部 MCP 工具从配置文件加载并注册给 Agent，每个工具包含名称、描述、参数定义。

Agent 工具列表示例（MCP 工具）：

| 名称 | 所属 MCP | 描述 |
|------|---------|------|
| file_read | filesystem | 读取文件内容 |
| file_write | filesystem | 写入文件内容 |
| http_get | http_request | 发送 HTTP GET 请求 |
| web_search | web_search | 网络搜索 |

## 核心原则

- **一个 MCP 一个文件**：每个 MCP Server 以独立 JSON 文件存放在 `{GROOT_HOME}/mcp/` 目录
- **工具发现优先**：配置中不指定 `tools` 时，自动发现 MCP Server 的全部工具；指定 `tools` 时按名过滤，仅注册匹配的工具
- **统一连接流程**：无论是否指定 `tools`，都需要连接 MCP Server 并完成 Initialize 握手，工具的真实描述和参数 Schema 始终由 Server 提供
- **变更需重启**：MCP 配置修改后需重启服务才能生效，不支持热插拔
- **错误不阻塞**：单个 MCP 加载失败不阻塞其他 MCP 的加载和整体服务启动

## 目录结构

### 源码目录

```
internal/mcp/
  ├── config.go             # MCP 配置结构体定义
  └── manager.go            # MCP 运行时管理（连接、工具发现、注册、关闭等）
internal/cmd/
  ├── mcp.go                # CLI 命令实现
  └── mcp_test.go           # CLI 单元测试
```

### 数据目录

```
{GROOT_HOME}/mcp/
├── database_tool.json
├── WebParser.json
└── web_search.json
```

MCP 目录固定为 `{GROOT_HOME}/mcp`，不可配置。

## Manager 内部结构

```go
type Manager struct {
    mcps         map[string]*MCPConfig       // MCP 配置（name → config）
    clients      map[string]client.MCPClient // mcp-go 客户端（name → client）
    einoTools    map[string]tool.BaseTool    // eino 工具（供 Agent 调用）
    builtinTools map[string]tool.BaseTool    // 内置工具（如 schedule）
    toolInfos    map[string]*ToolInfo        // 工具元数据（供 API 查询）
    errors       map[string]string           // MCP 发现错误（供 /health 展示）
    logger       *logger.Logger
    mu           sync.RWMutex
}
```

关键方法：

| 方法 | 说明 |
|------|------|
| `LoadAll(dir)` | 启动时扫描目录，加载所有 `.json` 配置 |
| `Load(path)` | 加载单个配置文件（解析、连接、发现、注册） |
| `Register(config, tools, error)` | 注册 MCP 的工具元数据，记录发现错误 |
| `RegisterBuiltinTools(tools)` | 注册内置工具（如 schedule 工具），以 `"schedule"` 为 MCP 名 |
| `GetTools()` | 获取所有 eino 工具 + 内置工具，供 Agent Engine 调用 |
| `ListTools()` | 获取所有工具元数据，供 `/tools` API 使用 |
| `ListWithToolCount()` | 获取所有 MCP 及其工具计数，供 `/health` API 使用 |
| `Close()` | 优雅关闭所有 MCP 客户端连接 |

## 配置文件格式

每个 MCP Server 以独立 JSON 文件存放在 `{GROOT_HOME}/mcp/` 目录下。

### stdio 类型（本地工具）

```json
{
  "name": "database_tool",
  "type": "stdio",
  "description": "数据库查询和操作工具",
  "isActive": true,
  "command": "mcp-server-postgres",
  "args": ["--connection", "${DB_CONNECTION}"],
  "env": {
    "DB_CONNECTION": "${DB_CONNECTION}"
  }
}
```

### sse 类型（远程服务）

```json
{
  "name": "WebParser",
  "type": "sse",
  "description": "网页解析 MCP 服务",
  "isActive": true,
  "baseUrl": "https://dashscope.aliyuncs.com/api/v1/mcps/WebParser/sse",
  "headers": {
    "Authorization": "Bearer ${DASHSCOPE_API_KEY}"
  }
}
```

### streamable_http 类型

```json
{
  "name": "web_search",
  "type": "streamable_http",
  "description": "网络搜索工具",
  "isActive": true,
  "baseUrl": "https://mcp-search.example.com/api",
  "headers": {
    "X-API-Key": "${SEARCH_API_KEY}"
  }
}
```

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | MCP Server 名称（全局唯一） |
| `type` | string | 是 | 连接类型：`stdio`、`sse`、`streamable_http` |
| `description` | string | 是 | MCP Server 描述 |
| `isActive` | bool | 是 | 是否激活，false 时跳过加载 |
| `command` | string | stdio 必填 | 启动命令（仅 stdio 类型） |
| `args` | array | 否 | 命令参数（仅 stdio 类型），支持环境变量 `${VAR}` |
| `env` | object | 否 | 环境变量（仅 stdio 类型），支持 `${VAR}` 引用 |
| `baseUrl` | string | sse/http 必填 | 服务地址（sse / streamable_http 类型） |
| `headers` | object | 否 | 自定义请求头，支持 `${VAR}` 引用 |
| `tools` | array | 否 | 按名过滤工具列表，每项含 `name`（必填）。不填则自动发现全部工具 |

## 连接类型

| 类型 | 说明 | 适用场景 |
|------|------|---------|
| `stdio` | 启动子进程，通过标准输入输出通信 | 本地命令行工具 |
| `sse` | Server-Sent Events 长连接 | 远程 HTTP 服务（单向推送） |
| `streamable_http` | Streamable HTTP 双向流式通信 | 远程 HTTP 服务（双向流式） |

底层使用 [mcp-go](https://github.com/mark3labs/mcp-go) 客户端库实现三种连接类型的客户端创建和协议握手。

## 工具发现与加载

### 加载流程

服务启动时，`Manager.LoadAll()` 扫描 `{GROOT_HOME}/mcp/` 目录下所有 `.json` 文件，逐个调用 `Load()` 加载。

两种模式统一走同一流程，唯一区别在于 `ToolNameList` 是否设置：

```
读取 JSON 文件 → 解析为 MCPConfig →
│
├─ name 为空 → 返回错误
│
├─ isActive=false → 跳过，记录 INFO 日志
│
└─ isActive=true → 连接并发现
    ├─ 根据 type 创建对应的 mcp-go 客户端
    ├─ 执行 MCP Initialize 协议握手（30s 超时）
    │   └─ 失败 → 记录错误到 m.errors，跳过（不阻塞其他 MCP）
    │
    ├─ 构建 mcpp.Config：
    │   ├─ 始终设置 Cli、CustomHeaders
    │   ├─ config.Tools 不为空 → 提取 name 列表设置 ToolNameList（按名过滤）
    │   └─ config.Tools 为空 → ToolNameList 为空（获取全部）
    │
    ├─ 调用 mcpp.GetTools(ctx, mcppConf)
    │   └─ 失败 → 记录错误到 m.errors，跳过（不阻塞其他 MCP）
    │
    └─ 成功 → 存储客户端、创建 eino 工具、注册工具元数据
```

### 自动发现 vs 按名过滤

**自动发现（config.Tools 为空）：**

不指定 `tools` 字段，MCP Server 报告的所有工具都会被注册给 Agent。

```json
{
  "name": "filesystem",
  "type": "stdio",
  "description": "文件系统操作",
  "isActive": true,
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed"]
}
```

**按名过滤（config.Tools 不为空）：**

在配置文件中列出需要的工具名，只将匹配的工具注册给 Agent。适用于：
- MCP Server 提供大量工具，只需其中几个
- 需要限制 Agent 可见的工具范围

```json
{
  "name": "filesystem",
  "type": "stdio",
  "description": "文件系统操作（仅读取）",
  "isActive": true,
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed"],
  "tools": [
    {"name": "read_file"},
    {"name": "list_directory"}
  ]
}
```

> `tools` 数组每项只需填 `name`，`description` 和 `inputSchema` 字段由 MCP Server 自动发现填充，不在配置中覆盖。

### 内置工具注册

除 MCP 配置外，系统支持直接注册内置工具（非 MCP Server 来源）。这些工具以 `"schedule"` 为 MCP 名注册：

```go
// 注册内置工具（如定时任务工具）
mcpMgr.RegisterBuiltinTools(scheduleTools)
```

内置工具通过 `GetTools()` 返回给 Agent Engine，与 MCP 工具一起提供给 Agent 调用。

### 工具注册元数据

工具注册到 `Manager` 后，包含以下元数据：

| 字段 | 说明 |
|------|------|
| `Name` | 工具名称（全局唯一） |
| `Description` | 工具描述 |
| `InputSchema` | 参数 Schema（可选） |
| `MCP` | 所属来源（MCP Server 名称或 `"schedule"`） |

已注册的 eino 工具（可被 Agent 调用）通过 `Manager.GetTools()` 获取；工具元数据（供 API 查询）通过 `Manager.ListTools()` 获取。

### 错误记录与暴露

MCP 加载失败时，错误信息记录在 `Manager.errors` map 中：

- `Load()` 中客户端创建失败 → 调用 `Register(&config, nil, err.Error())` 记录错误
- `Load()` 中工具发现失败 → 调用 `Register(&config, nil, err.Error())` 记录错误
- `/health` API 通过 `ListWithToolCount()` 获取每个 MCP 的 Error 字段并展示

### 配置变更

MCP 配置变更需要重启服务才能生效。如需添加/修改/删除 MCP Server：
1. 在 `{GROOT_HOME}/mcp/` 目录下添加/修改/删除对应的 `.json` 文件
2. 重启 Groot 服务

## CLI 命令设计

CLI 仅提供查看能力：

```
groot mcp list                    # 列出所有已配置的 MCP Server
groot mcp -h / --help             # 显示帮助信息
```

### list - 列出已配置 MCP Servers

扫描 `{GROOT_HOME}/mcp/` 目录，读取所有 `.json` 文件，以表格形式展示。

- 只识别 `.json` 后缀的文件，忽略其他文件和子目录
- 解析每个 JSON 文件获取 name、type、isActive、description
- 配置解析失败的文件在列表中标记为「⚠ 配置解析失败」
- 未安装任何 MCP Server 时显示「未配置任何 MCP Server」
- MCP 目录不存在时也显示「未配置任何 MCP Server」

输出格式：

```
NAME             TYPE              STATUS    LAST_UPDATED         DESCRIPTION
---------------  ----------------  --------  -------------------  --------------------
web-search       stdio             active     2026-05-01 10:30     基于 SearXNG 的网页搜索
filesystem       stdio             active     2026-05-08 14:22     本地文件系统操作
database         streamable_http   inactive   2026-05-09 09:15     数据库查询服务
broken-config    -                 -          -                    ⚠ 配置解析失败
```

列宽规则：
- NAME 列宽：根据最长名称动态计算，上限 30
- TYPE 列宽：根据最长类型动态计算，上限 20
- STATUS 列宽：固定 8（active/inactive）
- LAST_UPDATED 列宽：固定 19
- DESCRIPTION 列宽：根据最长描述动态计算，上限 60（超出截断加 `...`）

表格后显示汇总行：

```
共 4 个 MCP Server（2 个活跃，1 个未激活，1 个异常）
```

### 核心数据结构

**McpFlags**

```go
type McpFlags struct {
    Subcommand string // list
}
```

**mcpItem**

```go
type mcpItem struct {
    name        string
    mcpType     string
    status      string
    lastUpdated string
    description string
    valid       bool
}
```

## 错误处理

| 场景 | 处理 |
|------|------|
| 配置文件 JSON 解析失败 | 记录错误日志，跳过该文件，继续加载其他配置 |
| 配置缺少 name 字段 | 返回错误，跳过该文件 |
| isActive=false | 跳过加载，记录 INFO 日志 |
| MCP 客户端创建失败 | 记录错误到 m.errors，不阻塞其他 MCP 加载 |
| tools/list 发现失败 | 记录错误到 m.errors，不阻塞其他 MCP 加载 |
| CLI 未知子命令 | 输出错误信息，exit 1 |
| CLI list 收到额外参数 | 输出错误信息，exit 1 |
| CLI 配置文件解析失败 | 在列表中标记「⚠ 配置解析失败」，继续处理其他文件 |
| CLI 未知 flag | 输出错误信息，exit 1 |

## 测试

- `internal/cmd/mcp_test.go` — CLI 命令测试（参数解析、list 表格输出、异常标记、汇总行、空目录提示等）
- `internal/mcp/` 运行时逻辑暂未覆盖，待后续补充
