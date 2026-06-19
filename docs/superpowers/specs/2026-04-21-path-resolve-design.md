# 目录路径统一处理设计

## 一、功能设计

### 1.1 功能概述

本文档定义 Groot 在本地文件系统上解析路径的统一规则——决定哪些路径固定、哪些可配置、相对路径如何相对 `GROOT_HOME` 展开。

项目中持久化数据按用途分为 4 类（详见 [数据库后端设计 §1.3](2026-06-10-database-backend-design.md#13-数据分类)）：

1. **节点本地配置**（`env.yaml`）— 含数据库连接凭据，按节点本地维护
2. **集群共享配置**（`config.yaml` / `skills/` / `subagents/` / `mcp/` / `GROOT.md`）— 本地 HOME 是运行时读取入口；多主机模式下通过 `groot push/pull/diff` 与 `shared_resources` 表同步
3. **运行时数据**（cluster 成员 / schedule 任务 / memory 会话/对话）— SQLite 模式落 `~/.groot/groot.db`，MySQL/PG 模式落远端数据库
4. **运行日志**（`logs/`）— 永远本地磁盘，不参与同步

运行时数据已下沉到数据库后端，不再需要在文件系统上拼基目录；本文档只关心**本地文件系统路径**的解析规则。

### 1.2 GROOT_HOME 的确定

工作目录由 [`GetDefaultHome`](../../../internal/cmd/tail.go) 决定，优先级：

1. 环境变量 `GROOT_HOME`
2. `$HOME/.groot`（Windows 回退到 `$USERPROFILE/.groot`）
3. 兜底相对路径 `.groot`

### 1.3 固定目录（不可配置）

以下文件 / 目录位置由 `filepath.Join(homeDir, ...)` 直接拼接，用户无法通过配置更改：

| 路径 | 固定位置 | 说明 |
|------|----------|------|
| `skills` | `{GROOT_HOME}/skills` | 全局 Skills 定义目录 |
| `mcp` | `{GROOT_HOME}/mcp` | 全局 MCP 配置目录 |
| `subagents` | `{GROOT_HOME}/subagents` | 子 Agent 定义目录 |
| `GROOT.md` | `{GROOT_HOME}/GROOT.md` | 全局系统提示词 |
| `config.yaml` | `{GROOT_HOME}/config.yaml` | 主配置文件 |
| `env.yaml` | `{GROOT_HOME}/env.yaml` | 节点本地基础设施配置（数据库凭据，可选；不参与集群同步） |
| `groot.db` | `{GROOT_HOME}/groot.db` | SQLite 模式下的本地数据库；不参与集群同步 |

memory 模块走数据库后端，不再有"memory 目录"概念（详见 [Memory 模块设计](2026-05-11-memory-design.md)）；附件不做持久化（详见 [附件与会话规则设计](2026-06-05-attachment-and-session-rules-design.md)），不再有"temp 目录"概念。

### 1.4 可配置目录（支持相对/绝对路径）

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `logging.file.directory` | `logs` | 日志文件目录（永远本地磁盘） |

**路径解析规则：**

| 配置值 | Home | 实际路径 |
|--------|------|----------|
| `"logs"` | `~/.groot` | `~/.groot/logs` |
| `/var/log/groot` | `~/.groot` | `/var/log/groot` |

### 1.5 路径处理函数

文件：[`internal/config/path.go`](../../../internal/config/path.go)

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

调用点：

- [`cmd/groot/main.go`](../../../cmd/groot/main.go) `startServer` — 解析 `cfg.Logging.File.Directory`
- [`internal/cmd/chat.go`](../../../internal/cmd/chat.go) `startEmbedServer` — TUI 嵌入服务模式下解析日志目录
- [`internal/cmd/tail.go`](../../../internal/cmd/tail.go) `resolveLogDir` — `groot tail` 子命令查找日志目录

### 1.6 配置结构

文件：[`internal/config/config.go`](../../../internal/config/config.go)

**AttachmentConfig：**

```go
type AttachmentConfig struct {
    MaxSize      int      `yaml:"max_size"`
    MaxTotalSize int      `yaml:"max_total_size"`
    MaxCount     int      `yaml:"max_count"`
    AllowedTypes []string `yaml:"allowed_types"`
}
```

附件 Handler 仅做请求级校验，不持有 homeDir、不需要 temp 目录。

**MemoryConfig：**

```go
type MemoryConfig struct {
    Directory       string `yaml:"directory"`
    RetentionDays   int    `yaml:"retention_days"`
    CleanupSchedule string `yaml:"cleanup_schedule"`
    HistoryWindow   int    `yaml:"history_window"`
}
```

`Directory` 字段保留在结构体中以兼容历史 `config.yaml`，运行时不参与任何路径解析——memory 模块通过 `MemoryRepo` 接口走数据库。

**DatabaseConfig**（节点本地，由 `env.yaml` 加载）：

```go
type DatabaseConfig struct {
    Driver          string `yaml:"driver"`            // "sqlite" | "mysql" | "postgres"
    DSN             string `yaml:"dsn"`               // 连接字符串，支持 ${ENV_VAR}
    MaxOpenConns    int    `yaml:"max_open_conns"`    // 默认 20
    MaxIdleConns    int    `yaml:"max_idle_conns"`    // 默认 5
    ConnMaxLifetime string `yaml:"conn_max_lifetime"` // 默认 "30m"
}
```

`env.yaml` 不存在或 `database` 节缺失时，`cfg.Database` 为 `nil`，[`db.Open`](../../../internal/db/db.go) 自动落到 `{GROOT_HOME}/groot.db` 的 SQLite 模式。

### 1.7 主程序路径处理

文件：[`cmd/groot/main.go`](../../../cmd/groot/main.go)

```go
// 固定目录路径
skillsDir := filepath.Join(homeDir, "skills")
mcpDir := filepath.Join(homeDir, "mcp")
subAgentDir := filepath.Join(homeDir, "subagents")

// 可配置目录路径（使用 ResolvePath）
cfg.Logging.File.Directory = config.ResolvePath(cfg.Logging.File.Directory, homeDir)

// 运行时数据由 db.Open + repofactory.NewRepos 接管，不再在文件系统上拼基目录
sqlxDB, dbDialect, _ := db.Open(cfg.Database, homeDir)
repos := repofactory.NewRepos(sqlxDB, dbDialect, homeDir)
```

`api.NewServer` 仍接受 `homeDir` 参数，转交给内部子模块按需使用。

### 1.8 Attachment Handler

文件：[`internal/attachment/handler.go`](../../../internal/attachment/handler.go)

```go
func NewHandler(cfg config.AttachmentConfig) *Handler {
    return &Handler{
        maxSize:      int64(cfg.MaxSize) * 1024 * 1024,
        maxTotalSize: int64(cfg.MaxTotalSize) * 1024 * 1024,
        maxCount:     cfg.MaxCount,
        allowedTypes: cfg.AllowedTypes,
    }
}
```

签名只接受 `AttachmentConfig`，不持有 homeDir，不操作文件系统；仅做请求级校验：count / size / 类型白名单。

### 1.9 设计原则

1. **简化配置**：skills、mcp、subagents 目录通常不需要自定义位置，固定路径减少配置复杂度
2. **避免错误**：固定路径避免用户配置错误导致工具加载失败
3. **运行时数据下沉到 DB**：cluster / schedule / memory 三个模块走 `Repository` 接口，无需在文件系统上拼基目录；统一由 `env.yaml` 的 `database` 节决定 SQLite / MySQL / PostgreSQL
4. **保持灵活性**：日志目录可能需要放在不同存储位置（如 SSD），保留可配置性
5. **env.yaml 节点本地化**：含数据库凭据等基础设施信息，每个节点独立维护，不参与 sync 分发
