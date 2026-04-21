# GROOT.md 功能设计

## 背景

类似 Claude Code 的 CLAUDE.md 功能，用户可以在 `GROOT_HOME` 目录下放置 `GROOT.md` 文件，groot 自动读取内容作为系统指令的最前面部分。

## 功能概述

| 要点 | 说明 |
|------|------|
| 文件位置 | `{GROOT_HOME}/GROOT.md`（默认 `~/.groot/GROOT.md`） |
| 加载时机 | 服务启动时加载 + 热加载监听 |
| 配置开关 | 无需配置，默认启用 |
| 缓存机制 | 启动时加载到缓存，请求时从缓存读取，文件变化时更新缓存 |

## 系统指令构建顺序

```
GROOT.md（缓存）
→ prompt（用户传入）
→ Skills 指令
→ 执行规则
```

## 边界条件处理

| 情况 | 处理方式 |
|------|----------|
| GROOT.md 不存在 | 正常运行，清空缓存 |
| GROOT.md 存在但为空 | 正常运行，清空缓存 |
| GROOT.md 读取失败 | 记录警告日志，正常运行，清空缓存 |
| GROOT.md 被删除 | Watcher 检测到，清空缓存 |
| GROOT.md 被创建 | Watcher 检测到，加载内容 |

## 热加载机制

### 启动流程

1. Engine 初始化（grootMdContent = ""）
2. GROOT.md Watcher 启动（无条件）
3. Watcher 读取 GROOT.md → 写入 Engine.grootMdContent
4. Watcher 监听 GROOT_HOME 目录变化

### 文件变化流程

1. fsnotify 检测到 GROOT.md 变化
2. Watcher.reload()
3. 读取文件内容（或清空）
4. Engine.SetGrootMdContent(content)
5. 缓存更新完成

### 请求处理流程

1. 用户发起请求
2. Engine.buildSystemInstruction()
3. Engine.GetGrootMdContent()（从缓存读取，不读文件）
4. 构建完整系统指令

## 改动文件清单

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `internal/grootmd/watcher.go` | 新增 | GROOT.md 热加载 Watcher |
| `internal/agent/engine.go` | 修改 | 新增缓存字段和读写方法 |
| `cmd/groot/main.go` | 修改 | 启动 Watcher（无条件） |

## 核心代码设计

### Engine 结构体修改

```go
type Engine struct {
    // ...existing fields
    grootMdContent string     // GROOT.md 内容缓存
    grootMdMu      sync.RWMutex
}

func (e *Engine) SetGrootMdContent(content string) {
    e.grootMdMu.Lock()
    e.grootMdContent = content
    e.grootMdMu.Unlock()
}

func (e *Engine) GetGrootMdContent() string {
    e.grootMdMu.RLock()
    defer e.grootMdMu.RUnlock()
    return e.grootMdContent
}
```

### buildSystemInstruction 修改

```go
func (e *Engine) buildSystemInstruction(prompt string) string {
    sb := &strings.Builder{}

    // 1. GROOT.md（从缓存读取）
    grootMd := e.GetGrootMdContent()
    if grootMd != "" {
        sb.WriteString(grootMd)
        sb.WriteString("\n\n")
    }

    // 2. prompt
    if prompt != "" {
        sb.WriteString(prompt)
        sb.WriteString("\n\n")
    }

    // 3. Skills + 执行规则（原有逻辑）
    // ...

    return sb.String()
}
```

### Watcher 实现

```go
package grootmd

type Watcher struct {
    engine     *agent.Engine
    homeDir    string
    watcher    *fsnotify.Watcher
    stopChan   chan struct{}
    log        *logger.Logger
}

func NewWatcher(engine *agent.Engine, homeDir string, log *logger.Logger) *Watcher {
    return &Watcher{
        engine:   engine,
        homeDir:  homeDir,
        stopChan: make(chan struct{}),
        log:      log,
    }
}

func (w *Watcher) Start() error {
    // 初始加载
    w.reload()

    // 启动 fsnotify 监听
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        return err
    }
    w.watcher = watcher

    // 监听 homeDir 目录
    if err := watcher.Add(w.homeDir); err != nil {
        return err
    }

    go w.run()
    return nil
}

func (w *Watcher) Stop() {
    close(w.stopChan)
    if w.watcher != nil {
        w.watcher.Close()
    }
}

func (w *Watcher) reload() {
    path := filepath.Join(w.homeDir, "GROOT.md")

    if _, err := os.Stat(path); os.IsNotExist(err) {
        w.engine.SetGrootMdContent("")
        return
    }

    content, err := os.ReadFile(path)
    if err != nil {
        w.log.Warn("无法读取 GROOT.md", zap.Error(err))
        w.engine.SetGrootMdContent("")
        return
    }

    w.engine.SetGrootMdContent(string(content))
}

func (w *Watcher) run() {
    for {
        select {
        case <-w.stopChan:
            return
        case event, ok := <-w.watcher.Events:
            if !ok {
                return
            }
            // 只处理 GROOT.md 相关事件
            if strings.HasSuffix(event.Name, "GROOT.md") {
                w.reload()
            }
        case err, ok := <-w.watcher.Errors:
            if !ok {
                return
            }
            w.log.Error("Watcher 错误", zap.Error(err))
        }
    }
}
```

### main.go 修改

```go
// 启动 GROOT.md watcher（无条件）
grootMdWatcher := grootmd.NewWatcher(engine, homeDir, log)
if err := grootMdWatcher.Start(); err != nil {
    log.Error("无法启动 GROOT.md watcher", zap.Error(err))
}

// ...shutdown 时
grootMdWatcher.Stop()
```

## 使用示例

用户在 `~/.groot/GROOT.md` 写入：

```markdown
# 项目规范

- 使用中文回答
- 代码风格遵循 Go 标准
```

groot 每次对话都会自动将这些规范注入系统指令。