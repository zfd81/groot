# /tools 接口 MCP 分组设计

## 背景

当前 `/tools` 接口返回所有工具的平铺列表，不区分 MCP 来源。用户希望按 MCP 分组展示，更清晰地看到每个 MCP 提供的工具。

## 当前格式

```json
{
    "tools": [
        {"name": "read_file", "description": "...", "mcp": "filesystem"},
        {"name": "write_file", "description": "...", "mcp": "filesystem"},
        {"name": "get_editor_state", "description": "...", "mcp": "pencil"}
    ],
    "total": 19
}
```

## 目标格式

```json
{
    "filesystem": {
        "tools": [
            {"name": "read_file", "description": "..."},
            {"name": "write_file", "description": "..."}
        ],
        "total": 14
    },
    "pencil": {
        "tools": [
            {"name": "get_editor_state", "description": "..."}
        ],
        "total": 5
    }
}
```

### 设计要点

- MCP 名称作为顶层 key（对象结构，而非数组）
- 每个 MCP 包含 `tools` 数组和 `total` 计数
- 工具内部不再包含冗余的 `mcp` 字段（已在分组层级体现）

## 实现方案

采用 Handler 层分组方案（方案 A），最小改动范围。

### 改动文件

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `internal/api/types/types.go` | 新增 | 添加 `ToolsGroup` 类型 |
| `internal/api/handler/tools.go` | 修改 | 分组逻辑替换平铺列表 |

### 详细改动

**1. internal/api/types/types.go**

新增 `ToolsGroup` 类型：

```go
// ToolsGroup 表示单个 MCP 的工具集合
type ToolsGroup struct {
    Tools []ToolInfo `json:"tools"`
    Total int        `json:"total"`
}
```

注：`ToolInfo` 结构保持不变（含 `mcp` 字段），但分组时填充简化版本（不含 `mcp`）。

**2. internal/api/handler/tools.go**

修改 `Serve` 方法实现分组：

```go
func (h *ToolsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
    tools := h.mcpManager.ListTools()

    // 按 MCP 分组
    grouped := make(map[string]types.ToolsGroup)
    for _, t := range tools {
        group := grouped[t.MCP]
        group.Tools = append(group.Tools, types.ToolInfo{
            Name:        t.Name,
            Description: t.Description,
            // MCP 字段不填充，避免冗余
        })
        group.Total++
        grouped[t.MCP] = group
    }

    rc.JSON(200, grouped)
}
```

### 不改动部分

- `mcp.Manager.ListTools()` 保持不变，继续返回平铺列表
- 其他使用 `ListTools()` 的代码不受影响

## 测试要点

- 多 MCP 场景：验证分组正确性
- 单 MCP 场景：验证格式一致
- 空 MCP 场景：验证返回空对象 `{}`

## 影响范围

- 仅影响 `/tools` API 响应格式
- 向后不兼容：调用方需适配新格式