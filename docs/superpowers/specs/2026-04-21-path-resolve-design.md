# 目录路径统一处理设计

## 背景

当前项目中各目录路径处理方式不一致：

| 目录 | 当前行为 | 问题 |
|------|----------|------|
| `skills` | 硬编码 `filepath.Join(homeDir, "skills")` | 无配置项，不支持自定义 |
| `mcp` | 硬编码 `filepath.Join(homeDir, "mcp")` | 无配置项，不支持自定义 |
| `memory` | 强制 `filepath.Join(homeDir, cfg.Memory.Directory)` | 相对路径强制拼接，绝对路径无法使用 |
| `logs` | 直接使用 `cfg.Logging.File.Directory` | 相对路径变成相对于执行目录 |
| `temp` | 直接使用 `cfg.Attachment.TempDirectory` | 相对路径变成相对于执行目录 |

用户希望统一行为：**相对路径相对于 Home，绝对路径直接使用**。

## 目标格式

| 配置值 | Home | 实际路径 |
|--------|------|----------|
| `"memory"` | `~/.groot` | `~/.groot/memory` |
| `"logs"` | `~/.groot` | `~/.groot/logs` |
| `/data/memory` | `~/.groot` | `/data/memory` |
| `/var/log/groot` | `~/.groot` | `/var/log/groot` |

## 设计方案

### 1. 新增路径处理函数

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

### 2. 新增配置项

为 `skills` 和 `mcp` 目录添加配置项。

文件: `internal/config/config.go`

**SkillsConfig 新增 Directory 字段：**
```go
type SkillsConfig struct {
    Directory  string        `yaml:"directory"`    // skills 目录
    HotReload  HotReloadConfig `yaml:"hot_reload"`
}
```

**MCPConfig 新增 Directory 字段：**
```go
type MCPConfig struct {
    Directory  string `yaml:"directory"`    // MCP 配置目录
}
```

### 3. 更新默认配置

文件: `internal/config/defaults.go`

```go
Skills: SkillsConfig{
    Directory: "skills",  // 新增
    HotReload: HotReloadConfig{...},
},
MCP: MCPConfig{
    Directory: "mcp",  // 新增
},
Memory: MemoryConfig{
    Directory: "memory",  // 保持不变
    ...
},
Logging: LoggingConfig{
    ...
    File: LogFileConfig{
        Directory: "logs",  // 保持不变
        ...
    },
},
Attachment: AttachmentConfig{
    ...
    TempDirectory: "temp",  // 保持不变
},
```

### 4. 修改 main.go 路径处理

文件: `cmd/groot/main.go`

将所有目录路径处理改为使用 `ResolvePath`：

```go
// Skills
skillsDir := config.ResolvePath(cfg.Skills.Directory, homeDir)

// MCP
mcpDir := config.ResolvePath(cfg.MCP.Directory, homeDir)

// Memory
memoryDir := config.ResolvePath(cfg.Memory.Directory, homeDir)

// Logs - 在 logger 初始化前处理
cfg.Logging.File.Directory = config.ResolvePath(cfg.Logging.File.Directory, homeDir)

// Temp - 在 attachment 处理前处理
cfg.Attachment.TempDirectory = config.ResolvePath(cfg.Attachment.TempDirectory, homeDir)
```

### 5. 更新用户手册

文件: `README.md`

添加路径配置说明，说明相对路径和绝对路径的行为。

## 改动文件清单

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `internal/config/path.go` | 新增 | 路径处理函数 |
| `internal/config/config.go` | 修改 | 新增 SkillsConfig.Directory 和 MCPConfig.Directory |
| `internal/config/defaults.go` | 修改 | 新增默认配置值 |
| `cmd/groot/main.go` | 修改 | 使用 ResolvePath 处理所有目录路径 |
| `README.md` | 修改 | 添加路径配置说明 |

## 测试要点

- 默认配置（相对路径）正常工作，目录在 `~/.groot/` 下
- 绝对路径配置正常工作，目录在指定位置
- 现有功能不受影响（skills 加载、MCP 加载、memory 存储、日志写入）