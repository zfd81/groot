# /tools MCP 分组实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 /tools 接口返回格式从平铺列表改为按 MCP 分组结构

**Architecture:** Handler 层分组，最小改动范围，不影响 mcp.Manager.ListTools()

**Tech Stack:** Go, Hertz HTTP framework

---

## 文件结构

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/api/types/types.go` | 修改 | 新增 ToolsGroup 类型 |
| `internal/api/handler/tools.go` | 修改 | 分组逻辑替换平铺列表 |
| `tests/test_api_endpoints.py` | 修改 | 更新测试适配新格式 |

---

### Task 1: 新增 ToolsGroup 类型

**Files:**
- Modify: `internal/api/types/types.go:186-190` (在 ToolInfo 定义后)

- [ ] **Step 1: 添加 ToolsGroup 类型定义**

在 `internal/api/types/types.go` 文件的 `ToolInfo` 结构体定义后（约第 190 行），添加新类型：

```go
// ToolsGroup 表示单个 MCP 的工具集合
type ToolsGroup struct {
	Tools []ToolInfo `json:"tools"`
	Total int        `json:"total"`
}
```

- [ ] **Step 2: 验证代码编译**

Run: `go build ./internal/api/types`
Expected: 编译成功，无错误

- [ ] **Step 3: Commit**

```bash
git add internal/api/types/types.go
git commit -m "feat: add ToolsGroup type for MCP grouping"
```

---

### Task 2: 修改 Handler 实现分组逻辑

**Files:**
- Modify: `internal/api/handler/tools.go:23-41`

- [ ] **Step 1: 修改 Serve 方法实现分组**

将 `internal/api/handler/tools.go` 的 `Serve` 方法替换为：

```go
// Serve handles the tools request
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

- [ ] **Step 2: 验证代码编译**

Run: `go build ./internal/api/handler`
Expected: 编译成功，无错误

- [ ] **Step 3: Commit**

```bash
git add internal/api/handler/tools.go
git commit -m "feat: group tools by MCP in /tools endpoint"
```

---

### Task 3: 更新测试适配新格式

**Files:**
- Modify: `tests/test_api_endpoints.py:605-625`

- [ ] **Step 1: 修改测试验证新格式**

将 `tests/test_api_endpoints.py` 中的 `test_list_tools` 方法替换为：

```python
def test_list_tools(self, server, api_headers):
    """TC-022: 列出 MCP 工具 (按 MCP 分组)"""
    response = requests.get(
        f"{BASE_URL}/tools",
        headers=api_headers
    )

    assert response.status_code == 200
    data = response.json()

    # 验证新格式：按 MCP 分组
    # data 应为 {"filesystem": {"tools": [...], "total": N}, ...}
    assert isinstance(data, dict)

    # 验证每个 MCP 分组结构
    for mcp_name, group in data.items():
        assert "tools" in group
        assert "total" in group
        assert isinstance(group["tools"], list)
        assert group["total"] == len(group["tools"])

        # 验证工具字段（不包含冗余的 mcp 字段）
        for tool in group["tools"]:
            assert "name" in tool
            assert "description" in tool
            # mcp 字段不应存在
            assert "mcp" not in tool
```

- [ ] **Step 2: 运行测试验证失败（因实现未生效）**

Run: `pytest tests/test_api_endpoints.py::TestToolsEndpoints::test_list_tools -v`
Expected: FAIL - 测试期望新格式但当前代码返回旧格式

- [ ] **Step 3: 重新编译并启动服务验证测试通过**

Run: `go build ./cmd/groot && pytest tests/test_api_endpoints.py::TestToolsEndpoints::test_list_tools -v`
Expected: PASS - 测试通过，新格式正确

- [ ] **Step 4: Commit**

```bash
git add tests/test_api_endpoints.py
git commit -m "test: update test for MCP grouped tools format"
```

---

### Task 4: 验证完整功能

- [ ] **Step 1: 运行完整测试套件**

Run: `pytest tests/ -v --tb=short`
Expected: 所有测试通过

- [ ] **Step 2: 手动验证 API 响应格式**

Run: `curl -H "Authorization: Bearer <token>" http://localhost:8080/tools | jq`
Expected: 返回按 MCP 分组的 JSON 格式，如：
```json
{
  "filesystem": {
    "tools": [{"name": "read_file", "description": "..."}],
    "total": 14
  },
  "pencil": {
    "tools": [{"name": "get_editor_state", "description": "..."}],
    "total": 5
  }
}
```

---

## Self-Review 检查

- [x] Spec coverage: 所有设计要点已覆盖（ToolsGroup 类型、Handler 分组、测试更新）
- [x] Placeholder scan: 无 TBD/TODO，所有代码完整
- [x] Type consistency: ToolsGroup 在 Task 1 定义，Task 2 正确引用