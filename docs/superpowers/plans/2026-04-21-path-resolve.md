# 目录路径统一处理实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 统一所有目录配置的路径处理逻辑：相对路径相对于 Home，绝对路径直接使用

**Architecture:** 新增 ResolvePath 函数处理路径解析，修改配置结构体新增 Directory 字段，在 main.go 中统一使用

**Tech Stack:** Go

---

## 文件结构

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/config/path.go` | 新增 | 路径处理函数 |
| `internal/config/config.go` | 修改 | 新增 Directory 字段 |
| `internal/config/defaults.go` | 修改 | 新增默认配置值 |
| `cmd/groot/main.go` | 修改 | 使用 ResolvePath |
| `README.md` | 修改 | 添加路径配置说明 |

---

### Task 1: 新增 ResolvePath 函数

**Files:**
- Create: `internal/config/path.go`

- [ ] **Step 1: 创建 path.go 文件**

```go
package config

import "path/filepath"

// ResolvePath 解析路径：绝对路径直接使用，相对路径相对于 homeDir
func ResolvePath(path, homeDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(homeDir, path)
}
```

- [ ] **Step 2: 验证代码编译**

Run: `go build ./internal/config`
Expected: 编译成功，无错误

- [ ] **Step 3: Commit**

```bash
git add internal/config/path.go
git commit -m "feat: add ResolvePath function for path resolution"
```

---

### Task 2: 新增配置字段

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: 修改 SkillsConfig 结构体**

在 `internal/config/config.go` 中找到 `SkillsConfig` 结构体（约第 50 行），添加 `Directory` 字段：

```go
type SkillsConfig struct {
	Directory  string          `yaml:"directory"`    // skills 目录
	HotReload  HotReloadConfig `yaml:"hot_reload"`
}
```

- [ ] **Step 2: 修改 MCPConfig 结构体**

在 `internal/config/config.go` 中找到 `MCPConfig` 结构体（约第 55 行），添加 `Directory` 字段：

```go
type MCPConfig struct {
	Directory  string          `yaml:"directory"`    // MCP 配置目录
	HotReload  HotReloadConfig `yaml:"hot_reload"`
}
```

- [ ] **Step 3: 验证代码编译**

Run: `go build ./internal/config`
Expected: 编译成功，无错误

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add Directory field to SkillsConfig and MCPConfig"
```

---

### Task 3: 更新默认配置

**Files:**
- Modify: `internal/config/defaults.go`

- [ ] **Step 1: 更新 SkillsConfig 默认值**

在 `internal/config/defaults.go` 的 `DefaultConfig()` 函数中找到 `Skills` 配置（约第 26-31 行），添加 `Directory` 字段：

```go
Skills: SkillsConfig{
	Directory: "skills",
	HotReload: HotReloadConfig{
		Enabled:       true,
		DebounceDelay: 2,
	},
},
```

- [ ] **Step 2: 更新 MCPConfig 默认值**

在 `internal/config/defaults.go` 的 `DefaultConfig()` 函数中找到 `MCP` 配置（约第 32-37 行），添加 `Directory` 字段：

```go
MCP: MCPConfig{
	Directory: "mcp",
	HotReload: HotReloadConfig{
		Enabled:       true,
		DebounceDelay: 2,
	},
},
```

- [ ] **Step 3: 验证代码编译**

Run: `go build ./internal/config`
Expected: 编译成功，无错误

- [ ] **Step 4: Commit**

```bash
git add internal/config/defaults.go
git commit -m "feat: add default directory values for skills and mcp"
```

---

### Task 4: 修改 main.go 使用 ResolvePath

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 修改 skills 目录处理**

将 `cmd/groot/main.go` 中第 95 行的代码修改为：

```go
// Load skills
skillsDir := config.ResolvePath(cfg.Skills.Directory, homeDir)
if err := skillLoader.LoadAll(skillsDir); err != nil {
	log.Error("无法加载Skills", zap.Error(err))
}
log.Info("Skills 加载完成", zap.Int("count", skillsRegistry.Count()), zap.String("dir", skillsDir))
```

- [ ] **Step 2: 修改 mcp 目录处理**

将 `cmd/groot/main.go` 中第 111 行的代码修改为：

```go
// Load MCP configs
mcpDir := config.ResolvePath(cfg.MCP.Directory, homeDir)
if err := mcpMgr.LoadAll(mcpDir); err != nil {
	log.Error("无法加载MCP配置", zap.Error(err))
}
log.Info("MCP 加载完成", zap.Int("count", mcpMgr.Count()), zap.String("dir", mcpDir))
```

- [ ] **Step 3: 修改 memory 目录处理**

将 `cmd/groot/main.go` 中第 124 行的代码修改为：

```go
// Initialize memory manager
memoryDir := config.ResolvePath(cfg.Memory.Directory, homeDir)
memMgr := memory.NewManager(memoryDir, cfg.Memory.RetentionDays, log)
```

- [ ] **Step 4: 修改 logs 目录处理（在 logger 初始化前）**

在 `cmd/groot/main.go` 中，logger 初始化之前（约第 82 行之前），添加：

```go
// Resolve log directory path
cfg.Logging.File.Directory = config.ResolvePath(cfg.Logging.File.Directory, homeDir)

// Initialize logger
log := logger.New(cfg.Logging)
```

- [ ] **Step 5: 修改 temp 目录处理**

在 `cmd/groot/main.go` 中，添加 temp 目录处理（在 API server 创建之前）：

```go
// Resolve temp directory path
cfg.Attachment.TempDirectory = config.ResolvePath(cfg.Attachment.TempDirectory, homeDir)
```

- [ ] **Step 6: 验证代码编译**

Run: `go build ./cmd/groot`
Expected: 编译成功，无错误

- [ ] **Step 7: Commit**

```bash
git add cmd/groot/main.go
git commit -m "feat: use ResolvePath for all directory paths"
```

---

### Task 5: 更新用户手册

**Files:**
- Modify: `README.md`

- [ ] **Step 1: 在配置说明章节添加路径处理说明**

在 `README.md` 的配置说明章节（找到配置相关内容），添加路径配置说明：

```markdown
### 目录配置说明

所有目录配置支持相对路径和绝对路径：

- **相对路径**：相对于 `~/.groot` 目录（GROOT_HOME）
- **绝对路径**：直接使用指定路径

示例配置：

```yaml
# 相对路径示例（目录位于 ~/.groot/memory）
memory:
  directory: memory

# 绝对路径示例（目录位于 /data/logs）
logging:
  file:
    directory: /data/logs
```

可配置的目录包括：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `skills.directory` | `skills` | Skills 脚本目录 |
| `mcp.directory` | `mcp` | MCP 配置目录 |
| `memory.directory` | `memory` | 会话记忆目录 |
| `logging.file.directory` | `logs` | 日志文件目录 |
| `attachment.temp_directory` | `temp` | 附件临时目录 |
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add directory path configuration documentation"
```

---

### Task 6: 验证功能

- [ ] **Step 1: 运行完整测试套件**

Run: `pytest tests/ -v --tb=short -x --ignore=tests/test_api_endpoints.py::TestChatAPI`
Expected: 相关测试通过

- [ ] **Step 2: 验证编译**

Run: `go build ./cmd/groot && echo "Build successful"`
Expected: 编译成功

---

## Self-Review 检查

- [x] Spec coverage: 所有设计要点已覆盖（ResolvePath 函数、配置字段、默认值、main.go 修改、README 更新）
- [x] Placeholder scan: 无 TBD/TODO，所有代码完整
- [x] Type consistency: ResolvePath 在 Task 1 定义，后续任务正确引用