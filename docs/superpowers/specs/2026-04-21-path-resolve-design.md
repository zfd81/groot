# 目录路径统一处理设计

## 背景

项目中有两类目录：**固定目录**和**可配置目录**。

**固定目录：** 位置固定，不可通过配置更改。
- `skills/` - Skills 定义目录，固定在 `{GROOT_HOME}/skills`
- `mcp/` - MCP 配置目录，固定在 `{GROOT_HOME}/mcp`
- `subagents/` - 子 Agent 定义目录，固定在 `{GROOT_HOME}/subagents`
- `cluster/` - 集群成员目录，固定在 `{GROOT_HOME}/cluster`（minio 模式下走 object key 前缀，由 `Storage.List` 模拟）
- `temp/` - 附件请求级暂存目录，固定在 `{GROOT_HOME}/memory/temp`（与 `cfg.Memory.Directory` 解耦）
- `env.yaml` - 节点本地基础设施配置文件，固定为 `{GROOT_HOME}/env.yaml`

**可配置目录：** 支持相对路径和绝对路径配置。
- `memory/` - 会话记忆目录（minio 模式下作为 object key 前缀生效）
- `logs/` - 日志文件目录（永远落本地磁盘）

## 设计方案

### 1. 固定目录（不可配置）

以下目录位置固定，用户无法通过配置更改：

| 目录 | 固定位置 | 说明 |
|------|----------|------|
| `skills` | `{GROOT_HOME}/skills` | 全局 Skills 定义目录 |
| `mcp` | `{GROOT_HOME}/mcp` | 全局 MCP 配置目录 |
| `subagents` | `{GROOT_HOME}/subagents` | 子 Agent 定义目录 |
| `cluster` | `{GROOT_HOME}/cluster` | 集群成员目录（local 模式真实目录；minio 模式 object key 前缀 `cluster/members`） |
| `temp` | `{GROOT_HOME}/memory/temp` | 附件请求级暂存目录（即便用户把 `memory.directory` 配为绝对路径，temp 仍落 `${homeDir}/memory/temp`） |
| `env.yaml` | `{GROOT_HOME}/env.yaml` | 基础设施凭据（含 MinIO，可选；不参与集群同步） |

**说明：** temp 目录与 `memory.directory` 解耦——历史实现下 temp 跟随 `memory.directory` 移动，但 v3.8 之后 attachment Handler 始终用 `${homeDir}/memory/temp` 作为基目录，避免 minio 模式下用户配置造成困惑。已知影响参见 [存储抽象与 MinIO 模式设计](2026-06-01-storage-abstraction-and-minio-mode-design.md) §1.9.5。

### 2. 可配置目录（支持相对/绝对路径）

以下目录支持用户配置，可使用相对路径或绝对路径：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `memory.directory` | `memory` | 会话记忆目录（local 模式 = 文件系统路径；minio 模式 = object key 前缀，绝对路径前缀会被 `filepath.Join` 处理） |
| `logging.file.directory` | `logs` | 日志文件目录（永远本地磁盘） |

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

**AttachmentConfig（不含 TempDirectory）：**
```go
type AttachmentConfig struct {
    MaxSize      int      `yaml:"max_size"`
    MaxTotalSize int      `yaml:"max_total_size"`
    MaxCount     int      `yaml:"max_count"`
    AllowedTypes []string `yaml:"allowed_types"`
}
```

**SkillsConfig（不含 Directory）：**
```go
type SkillsConfig struct {
    HotReload HotReloadConfig `yaml:"hot_reload"`
    // Skills 目录固定为 {GROOT_HOME}/skills
}
```

**MCPConfig 已移除：**
- MCP 目录固定为 `{GROOT_HOME}/mcp`

### 5. main.go 路径处理

文件: `cmd/groot/main.go`

```go
// 固定目录路径
skillsDir := filepath.Join(homeDir, "skills")
mcpDir := filepath.Join(homeDir, "mcp")
subAgentsDir := filepath.Join(homeDir, "subagents")

// 可配置目录路径(使用 ResolvePath)
memoryDir := config.ResolvePath(cfg.Memory.Directory, homeDir)
cfg.Logging.File.Directory = config.ResolvePath(cfg.Logging.File.Directory, homeDir)

// minio 模式下:再为运行时模块按对象 key 前缀计算 basePath
//   memoryBase   = "memory"
//   scheduleBase = "schedules"
//   clusterBase  = "cluster/members"
// 详见 [存储抽象与 MinIO 模式设计](2026-06-01-storage-abstraction-and-minio-mode-design.md) §1.7.1
```

### 6. Attachment Handler

文件: `internal/attachment/handler.go`

```go
func NewHandler(cfg config.AttachmentConfig, homeDir string) *Handler {
    // temp 目录固定为 ${homeDir}/memory/temp,与 cfg.Memory.Directory 解耦
    tempDir := filepath.Join(homeDir, "memory", "temp")
    os.MkdirAll(tempDir, 0755)
    ...
}
```

## 改动文件清单

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `internal/config/config.go` | 修改 | 移除 MCPConfig、SkillsConfig.Directory、AttachmentConfig.TempDirectory |
| `internal/config/defaults.go` | 修改 | 移除相关默认配置值 |
| `internal/config/loader.go` | 修改 | 移除相关默认填充逻辑 |
| `internal/config/env.go` | 新增 | 加载 `env.yaml`（MinIO 凭据） |
| `internal/config/env_template.go` | 新增 | `init` 子命令生成的全注释 env.yaml 模板 |
| `internal/attachment/handler.go` | 修改 | NewHandler 参数改为 homeDir，temp 固定为 `${homeDir}/memory/temp` |
| `internal/api/server.go` | 修改 | NewServer 增加 homeDir 参数 |
| `cmd/groot/main.go` | 修改 | 使用固定路径；按 storage 类型分别拼 memoryBase / scheduleBase / clusterBase |
| `README.md` | 修改 | 更新目录配置说明 |

## 设计原因

1. **简化配置：** skills、mcp、subagents 目录通常不需要自定义位置，固定路径减少配置复杂度。
2. **避免错误：** 固定路径避免用户配置错误导致工具加载失败。
3. **temp 与 memory 解耦：** 旧设计下 temp 跟随 `memory.directory` 移动；改造后 temp 始终落在 `${homeDir}/memory/temp`，原因：minio 模式下 `memory.directory` 是 object key 前缀（不是本地路径），temp 必须落本地。
4. **保持灵活性：** memory 和 logs 目录可能需要放在不同存储位置（如 SSD 或网络存储），保留可配置性。
5. **env.yaml 节点本地化：** 含 MinIO 凭据等基础设施信息，每个节点独立维护，不参与 sync 分发。