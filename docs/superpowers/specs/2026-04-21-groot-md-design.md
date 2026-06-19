# GROOT.md 功能设计

## 一、功能设计

### 1.1 功能概述

GROOT.md 是位于 `GROOT_HOME` 目录下的一份 Markdown 文件，承载用户对主 Agent 的全局指导（规则、风格、目标等）。每次构建系统指令时，groot 自动读取该文件并将其内容拼接到系统指令最前面，等价于 Claude Code 的 CLAUDE.md。

| 要点 | 说明 |
|------|------|
| 文件位置 | `{GROOT_HOME}/GROOT.md`（默认 `~/.groot/GROOT.md`） |
| 加载时机 | 每次构建系统指令时按需读取 |
| 配置开关 | 无需配置，文件存在即生效 |
| 缓存策略 | 无缓存，每次直接读文件 |
| 初始化 | `groot init` 自动写入默认模板，已存在则跳过 |

### 1.2 系统指令拼接顺序

[`buildSystemInstruction`](internal/agent/engine.go) 按以下顺序拼接系统指令字符串：

```
GROOT.md / agent.md  →  SESSION.md  →  prompt
```

- 主 Agent / 编排模式：第一段读取 `{homeDir}/GROOT.md`，存在且非空则注入，否则跳过
- Solo 模式（子 Agent 直接执行）：第一段使用调用方传入的 `agentMdContent`（即子 Agent 自己的 agent.md），完全替换 GROOT.md，不再读取 GROOT.md
- 第二段 SESSION.md：会话维度的目录提示，由调用方提供
- 第三段 prompt：本轮请求传入的用户指令

Skills 指令通过 eino 的 skill 中间件在运行时注入，不出现在该字符串中；因此本设计只关注 GROOT.md 的读取与拼接。

### 1.3 边界条件

GROOT.md 按需读取，每次构建系统指令时直接读文件：

| 情况 | 处理方式 |
|------|----------|
| 文件不存在 | 返回空字符串，跳过该段 |
| 文件存在但为空 | 返回空字符串，跳过该段 |
| 文件读取失败（权限等） | 返回空字符串，跳过该段 |

### 1.4 加载机制

与 Skills 的 eino Backend 策略一致：**无缓存，每次请求时直接读文件**。

请求处理流程：

1. 用户发起请求
2. `Engine.buildSystemInstruction()` 被调用
3. 编排模式下调用 `grootmd.GetContent(e.homeDir)`，直接读 `{homeDir}/GROOT.md`
4. 文件存在且可读 → 返回内容；否则 → 返回空字符串
5. 拼接为完整系统指令

设计要点：

- **无 Watcher**：不依赖 fsnotify，不维护缓存，不创建后台 goroutine
- **天然热加载**：文件变更在下次请求时自动生效，与 Skills 行为一致
- **零配置**：文件存在就加载，不存在就跳过

### 1.5 关键代码定位

| 关注点 | 位置 |
|--------|------|
| `GetContent(homeDir)` 实现 | [`internal/grootmd/content.go`](internal/grootmd/content.go) |
| `GetContent` 单元测试 | [`internal/grootmd/content_test.go`](internal/grootmd/content_test.go) |
| 系统指令拼接（`buildSystemInstruction`） | [`internal/agent/engine.go`](internal/agent/engine.go) |
| `Engine.homeDir` / `EngineConfig.HomeDir` 字段 | [`internal/agent/engine.go`](internal/agent/engine.go) |
| Solo 模式契约的回归测试 | [`internal/agent/engine_test.go`](internal/agent/engine_test.go) |
| `groot init` 写入默认 GROOT.md | [`internal/cmd/init.go`](internal/cmd/init.go) |
| GROOT.md 作为可同步资源 | [`internal/sync/resource.go`](internal/sync/resource.go) |

### 1.6 GetContent 接口

```go
package grootmd

// GetContent 每次调用时直接读取 GROOT.md 文件。
// 文件存在且可读 → 返回内容；文件不存在/为空/读取失败 → 返回空字符串。
func GetContent(homeDir string) string
```

签名为 `func GetContent(homeDir string) string`，调用方仅需传入 GROOT_HOME 目录路径。

### 1.7 buildSystemInstruction 行为

```go
func (e *Engine) buildSystemInstruction(prompt, sessionMdContent, agentMdContent string) string {
    sb := &strings.Builder{}

    if agentMdContent != "" {
        // Solo 模式：用 agent.md 替换 GROOT.md
        sb.WriteString(agentMdContent)
        sb.WriteString("\n\n")
    } else {
        // 编排/主 Agent 模式：按需读取 GROOT.md
        grootMd := grootmd.GetContent(e.homeDir)
        if grootMd != "" {
            sb.WriteString(grootMd)
            sb.WriteString("\n\n")
        }
    }

    if sessionMdContent != "" {
        sb.WriteString(sessionMdContent)
        sb.WriteString("\n\n")
    }

    if prompt != "" {
        sb.WriteString(prompt)
        sb.WriteString("\n\n")
    }

    return sb.String()
}
```

关键不变量：`agentMdContent != ""` 时**不读取**也**不拼接** GROOT.md，避免 Solo 模式下两段全局指导互相干扰。

### 1.8 默认 GROOT.md 模板

`groot init` 在 `{homeDir}/GROOT.md` 不存在时写入一份默认模板，内容包含主 Agent 全局指导说明，以及一段「子 Agent 调度」引导，提示主 Agent 在拥有 `call_agent` 工具时如何使用子 Agent（按需调用、逐个调用、明确传参、附件引用）。模板已存在则跳过，不覆盖用户内容。

### 1.9 使用示例

用户在 `~/.groot/GROOT.md` 写入：

```markdown
# 项目规范

- 使用中文回答
- 代码风格遵循 Go 标准
```

groot 每次对话都会自动将这些规范注入系统指令第一段。
