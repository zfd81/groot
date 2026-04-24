# 目录路径统一处理设计

## 背景

项目中有两类目录：**固定目录**和**可配置目录**。

**固定目录：** 位置固定，不可通过配置更改。
- `skills/` - Skills 定义目录，固定在 `{GROOT_HOME}/skills`
- `mcp/` - MCP 配置目录，固定在 `{GROOT_HOME}/mcp`
- `api/` - API 工具配置目录，固定在 `{GROOT_HOME}/api`
- `temp/` - 附件处理临时目录，固定在 `{memoryDir}/temp`（位置取决于 memory.directory 配置）

**可配置目录：** 支持相对路径和绝对路径配置。
- `memory/` - 会话记忆目录
- `logs/` - 日志文件目录

## 设计方案

### 1. 固定目录（不可配置）

以下目录位置固定，用户无法通过配置更改：

| 目录 | 固定位置 | 说明 |
|------|----------|------|
| `skills` | `{GROOT_HOME}/skills` | Skills 定义目录 |
| `mcp` | `{GROOT_HOME}/mcp` | MCP 配置目录 |
| `api` | `{GROOT_HOME}/api` | API 工具配置目录 |
| `temp` | `{memoryDir}/temp` | 附件处理临时目录（固定在 memory 目录下） |

**说明：** temp 目录的位置取决于 memory.directory 配置：
- 若 memory.directory = "memory"（默认），则 temp = `{GROOT_HOME}/memory/temp`
- 若 memory.directory = "/data/groot/memory"，则 temp = `/data/groot/memory/temp`

### 2. 可配置目录（支持相对/绝对路径）

以下目录支持用户配置，可使用相对路径或绝对路径：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `memory.directory` | `memory` | 会话记忆目录 |
| `logging.file.directory` | `logs` | 日志文件目录 |

**路径解析规则：**

| 配置值 | Home | 实际路径 |
|--------|------|----------|
| `"memory"` | `~/.groot` | `~/.groot/memory` |
| `"logs"` | `~/.groot` | `~/.groot/logs` |
| `/data/memory` | `~/.groot` | `/data/memory` |
| `/var/log/groot` | `~/.groot` | `/var/log/groot` |

### 3. 路径处理函数

文件: `internal/config/path.go`

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

### 4. 配置结构

文件: `internal/config/config.go`

**AttachmentConfig（移除 TempDirectory）：**
```go
type AttachmentConfig struct {
    MaxSize      int      `yaml:"max_size"`
    MaxTotalSize int      `yaml:"max_total_size"`
    MaxCount     int      `yaml:"max_count"`
    AllowedTypes []string `yaml:"allowed_types"`
    // TempDirectory 已移除，固定为 {memoryDir}/temp
}
```

**SkillsConfig（移除 Directory）：**
```go
type SkillsConfig struct {
    HotReload HotReloadConfig `yaml:"hot_reload"`
    // Directory 已移除，固定为 {GROOT_HOME}/skills
}
```

**MCPConfig 和 APIToolsConfig 已移除：**
- MCP 目录固定为 `{GROOT_HOME}/mcp`
- API 工具目录固定为 `{GROOT_HOME}/api`

### 5. main.go 路径处理

文件: `cmd/groot/main.go`

```go
// 固定目录路径
skillsDir := filepath.Join(homeDir, "skills")
mcpDir := filepath.Join(homeDir, "mcp")
apiDir := filepath.Join(homeDir, "api")

// 可配置目录路径（使用 ResolvePath）
memoryDir := config.ResolvePath(cfg.Memory.Directory, homeDir)
cfg.Logging.File.Directory = config.ResolvePath(cfg.Logging.File.Directory, homeDir)
```

### 6. Attachment Handler

文件: `internal/attachment/handler.go`

```go
func NewHandler(cfg config.AttachmentConfig, memoryDir string) *Handler {
    // temp 目录固定在 memory 目录下
    tempDir := filepath.Join(memoryDir, "temp")
    os.MkdirAll(tempDir, 0755)
    ...
}
```

## 改动文件清单

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `internal/config/config.go` | 修改 | 移除 MCPConfig、APIToolsConfig、SkillsConfig.Directory、AttachmentConfig.TempDirectory |
| `internal/config/defaults.go` | 修改 | 移除相关默认配置值 |
| `internal/config/loader.go` | 修改 | 移除相关默认填充逻辑 |
| `internal/attachment/handler.go` | 修改 | NewHandler 参数改为 memoryDir，temp 固定为 memoryDir/temp |
| `internal/api/server.go` | 修改 | NewServer 增加 memoryDir 参数 |
| `cmd/groot/main.go` | 修改 | 使用固定路径，移除 TempDirectory 解析 |
| `README.md` | 修改 | 更新目录配置说明 |

## 设计原因

1. **简化配置：** skills、mcp、api 目录通常不需要自定义位置，固定路径减少配置复杂度。
2. **避免错误：** 固定路径避免用户配置错误导致工具加载失败。
3. **temp 目录归属：** temp 是附件处理的临时目录，逻辑上属于 memory 模块，放在 memory 目录下更合理。
4. **保持灵活性：** memory 和 logs 目录可能需要放在不同存储位置（如 SSD 或网络存储），保留可配置性。