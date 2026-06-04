# GROOT.md 功能设计

## 背景

类似 Claude Code 的 CLAUDE.md 功能，用户可以在 `GROOT_HOME` 目录下放置 `GROOT.md` 文件，groot 自动读取内容作为系统指令的最前面部分。

## 功能概述

| 要点 | 说明 |
|------|------|
| 文件位置 | `{GROOT_HOME}/GROOT.md`（默认 `~/.groot/GROOT.md`） |
| 加载时机 | 每次构建系统指令时按需读取 |
| 配置开关 | 无需配置，自动生效 |
| 缓存机制 | 无缓存，每次直接读文件，有就加载，没有就跳过 |

## 系统指令构建顺序

```
GROOT.md（按需读取）
→ prompt（用户传入）
→ Skills 指令
→ 执行规则
```

## 边界条件处理

GROOT.md 按需读取，每次构建系统指令时直接读文件：

| 情况 | 处理方式 |
|------|----------|
| GROOT.md 不存在 | 返回空字符串，正常运行 |
| GROOT.md 存在但为空 | 返回空字符串，正常运行 |
| GROOT.md 读取失败 | 记录错误日志，返回空字符串，正常运行 |

## 加载机制

与 Skills 的 eino Backend 策略一致：**无缓存，每次请求时直接读文件**。

### 请求处理流程

1. 用户发起请求
2. Engine.buildSystemInstruction()
3. `grootmd.GetContent(homeDir)` → 直接读 `{GROOT_HOME}/GROOT.md`
4. 文件存在 → 返回内容；不存在 → 返回空字符串
5. 构建完整系统指令

### 设计要点

- **无需 Watcher**：不依赖 fsnotify，不维护缓存，不创建后台 goroutine
- **天然热加载**：文件变更在下次请求时自动生效，与 Skills 行为一致
- **零配置**：文件存在就加载，不存在就跳过，完全自动

## 改动文件清单

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `internal/grootmd/content.go` | 修改 | GetContent 改为直接读文件 |
| `internal/grootmd/watcher.go` | 删除 | 不再需要 fsnotify watcher |
| `internal/agent/engine.go` | 修改 | 传入 homeDir 参数，直接调用 grootmd.GetContent |

## 核心代码设计

### GetContent 实现

```go
package grootmd

// GetContent 每次调用时直接读取 GROOT.md 文件。
// 文件存在且可读 → 返回内容；文件不存在/为空/读取失败 → 返回空字符串。
func GetContent(homeDir string) string {
    path := filepath.Join(homeDir, "GROOT.md")

    info, err := os.Stat(path)
    if err != nil || info.Size() == 0 {
        return ""
    }

    content, err := os.ReadFile(path)
    if err != nil {
        return ""
    }

    return string(content)
}
```

### buildSystemInstruction 修改

```go
func (e *Engine) buildSystemInstruction(prompt, sessionMdContent, agentMdContent string) string {
    sb := &strings.Builder{}

    if agentMdContent != "" {
        // Solo 模式：用 agent.md 替换 GROOT.md
        sb.WriteString(agentMdContent)
        sb.WriteString("\n\n")
    } else {
        // 1. GROOT.md（按需读取，有就加载，没有就跳过）
        grootMd := grootmd.GetContent(e.homeDir)
        if grootMd != "" {
            sb.WriteString(grootMd)
            sb.WriteString("\n\n")
        }
    }
    // ...
}
```

## 使用示例

用户在 `~/.groot/GROOT.md` 写入：

```markdown
# 项目规范

- 使用中文回答
- 代码风格遵循 Go 标准
```

groot 每次对话都会自动将这些规范注入系统指令。