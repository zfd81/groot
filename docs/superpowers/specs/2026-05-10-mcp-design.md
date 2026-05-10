# groot mcp 命令设计文档

**Goal:** 为 groot 添加 MCP Servers 管理子命令，首期支持列出所有已配置的 MCP 服务器。

**Architecture:** 新增 `groot mcp` 子命令，直接读取 `{GROOT_HOME}/mcp/` 目录中的 JSON 配置文件，不依赖运行中实例。

**Tech Stack:** Go、encoding/json

---

## 1. 命令用法

```
groot mcp <子命令> [选项]
```

| 子命令 | 参数 | 说明 |
|--------|------|------|
| `list` | 无 | 列出所有已配置的 MCP Servers |

---

## 2. 子命令详解

### 2.1 list - 列出已配置 MCP Servers

扫描 `{GROOT_HOME}/mcp/` 目录，读取所有 `.json` 文件，以表格形式展示。

- 只识别 `.json` 后缀的文件，忽略其他文件
- 解析每个 JSON 文件为 `MCPConfig` 结构
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

---

## 3. MCP 配置文件格式

每个 MCP Server 以独立 JSON 文件存放在 `{GROOT_HOME}/mcp/` 目录：

```json
{
  "name": "web-search",
  "type": "stdio",
  "description": "基于 SearXNG 的网页搜索",
  "isActive": true,
  "command": "node",
  "args": ["/path/to/server.js"],
  "env": {"API_KEY": "xxx"}
}
```

关键字段：`name`（名称）、`type`（类型）、`description`（描述）、`isActive`（是否激活）。

---

## 4. 错误处理

| 场景 | 处理 |
|------|------|
| 未知子命令 | 输出错误信息，exit 1 |
| list 收到额外参数 | 输出错误信息，exit 1 |
| 配置文件解析失败 | 在列表中标记「⚠ 配置解析失败」，继续处理其他文件 |
| 未知 flag | 输出错误信息，exit 1 |

---

## 5. 文件结构

```
cmd/groot/main.go          # 修改：添加 mcp 子命令分发
internal/cmd/
  ├── mcp.go               # mcp 命令核心实现
  └── mcp_test.go          # 单元测试
```

---

## 6. 核心数据结构

### McpFlags

```go
type McpFlags struct {
    Subcommand string // list (未来扩展: install, uninstall)
}
```

### mcpItem

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

---

## 7. 测试要点

| 测试项 | 验证内容 |
|--------|----------|
| 参数解析 | list 正确解析 |
| 参数解析 | 无参数报错 |
| 参数解析 | 未知子命令报错 |
| 参数解析 | list 多余参数报错 |
| 参数解析 | 未知 flag 报错 |
| list | 表格输出包含所有必要列（含 LAST_UPDATED） |
| list | 正确显示 active/inactive 状态 |
| list | 配置解析失败的文件标记「⚠ 配置解析失败」 |
| list | 汇总行包含活跃/未激活/异常计数 |
| list | 空目录显示提示 |
| list | 不存在目录显示提示 |
| list | 忽略非 .json 文件 |
