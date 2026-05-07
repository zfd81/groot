# groot tail 命令设计文档

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 groot 添加实时日志查看子命令，类似 `tail -f`，支持格式化输出和颜色高亮。

**Architecture:** 新增 `groot tail` 子命令，使用 fsnotify 监听日志文件变化，实时读取新内容，JSON 解析后格式化输出，支持按级别和关键词过滤。

**Tech Stack:** Go、fsnotify（文件监听）、ANSI 颜色码

---

## 1. 命令用法

```
groot tail [-n N] [-l level] [-k keyword]
```

| 参数 | 必填 | 说明 | 示例 |
|------|------|------|------|
| `-n N` | 否 | 显示最后 N 行历史日志后实时跟踪，默认 0（不显示历史） | `groot tail -n 50` |
| `-l level` | 否 | 按级别过滤，可选值：error/warn/info/debug，不指定则显示全部 | `groot tail -l error` |
| `-k keyword` | 否 | 关键词过滤，只显示包含关键词的日志行，不指定则不过滤 | `groot tail -k "api_request"` |

参数可组合使用：
```bash
groot tail -n 100 -l error                  # 显示最近100行错误日志后实时跟踪错误
groot tail -k "connection" -l error         # 实时跟踪包含connection的错误日志
groot tail -n 20 -k "session"               # 显示最近20行包含session的日志后实时跟踪
```

---

## 2. 文件定位逻辑

### 2.1 日志目录获取

日志目录从配置文件读取，而非固定路径：

流程：
1. 确定 groot 工作目录（默认 `~/.groot`，可通过 `GROOT_HOME` 环境变量指定）
2. 加载配置文件 `~/.groot/config.yaml`
3. 读取 `logging.file.directory` 配置项
4. 使用 `config.ResolvePath()` 解析为绝对路径（支持相对路径如 `logs`）

配置示例：
```yaml
logging:
  file:
    directory: logs          # 相对路径，解析为 ~/.groot/logs
    # 或
    directory: /var/log/groot  # 绝对路径
```

### 2.2 文件定位规则

在日志目录下定位当天最新的日志文件：

1. 获取当天日期字符串 `YYYY-MM-DD`
2. 在日志目录下筛选文件名包含当天日期的所有文件
3. 按文件修改时间排序，取最新的一个
4. 如果当天没有日志文件，提示用户 "当天暂无日志文件" 并退出
5. 如果日志目录不存在，提示 "日志目录不存在: <路径>" 并退出

**为什么用"最新创建"而非固定文件名：**
- 当前 logger 只有日期轮转，但未来可能添加大小分割
- 大小分割后会产生 `groot-{date}-001.log`、`groot-{date}-002.log` 等文件
- 使用"最新创建"规则，tail 会自动跟踪正在写入的最新文件，无需改造

---

## 3. 输出格式

### 3.1 JSON 解析

原始日志（JSON 格式）：
```json
{"timestamp":"2026-04-21T19:18:38+08:00","level":"info","caller":"api/server.go:42","message":"API 服务启动","event":"api_request","path":"/chat","method":"POST"}
```

解析后的字段顺序输出：
```
{timestamp} {level} {caller} {message} {event} {其他字段...}
```

### 3.2 格式化输出示例

```
2026-04-21T19:18:38+08:00 INFO  api/server.go:42  API 服务启动  event=api_request  path=/chat  method=POST
```

格式化规则：
- timestamp：保留原始 ISO8601 格式
- level：固定 5 字符宽度，左对齐（INFO、WARN、ERROR、DEBUG）
- caller：可选字段，存在则显示
- message：核心信息，紧跟 level/caller 之后
- event：可选字段，显示为 `event=xxx`
- 其他字段：按 key=value 格式依次显示

### 3.2 格式化输出示例

## 4. 颜色高亮

**方案：整行按级别颜色**

| 级别 | ANSI 颜色码 | 显示效果 |
|------|-------------|----------|
| ERROR | `\x1b[31m` | 红色 |
| WARN | `\x1b[33m` | 黄色 |
| INFO | `\x1b[32m` | 绿色 |
| DEBUG | `\x1b[90m` | 灰色 |

输出后需添加 `\x1b[0m`（Reset）恢复默认颜色。

示例：
```
[红色] 2026-04-21T19:18:42+08:00 ERROR  service/connection.go:15  服务连接失败  error="connection refused"
[黄色] 2026-04-21T19:18:40+08:00 WARN   system/memory.go:8  内存使用率偏高  usage=85%
[绿色] 2026-04-21T19:18:38+08:00 INFO   api/server.go:42  API 服务启动  event=api_request  port=8080
[灰色] 2026-04-21T19:18:45+08:00 DEBUG  handler/chat.go:120  收到请求  body={"instruction":"hello"}
```

---

## 5. 过滤逻辑

### 5.1 级别过滤 `-l level`

- 支持的值：`error`、`warn`、`info`、`debug`
- 不区分大小写（Error、ERROR、error 都有效）
- 匹配规则：只显示 level 字段等于指定级别的日志

### 5.2 关键词过滤 `-k keyword`

- 在整行日志文本中搜索关键词
- 大小写敏感（保持原始匹配）
- 匹配规则：行中包含关键词字符串即显示

### 5.3 组合过滤

当 `-l` 和 `-k` 同时指定时，两个条件都需满足才显示：
```bash
groot tail -l error -k "connection"
# 只显示级别为 error 且包含 "connection" 的日志
```

---

## 6. 实时跟踪实现

使用 `fsnotify` 库监听文件变化：

流程：
1. 定位到当天最新的日志文件
2. 如果指定 `-n N`，先读取并输出最后 N 行历史
3. 创建 fsnotify watcher，监听该文件的 Write 事件
4. 收到 Write 事件时，读取文件新增内容
5. 解析 JSON、格式化、过滤、输出
6. 持续监听直到用户按 Ctrl+C 退出

文件轮转处理：
- 如果检测到文件被重命名或删除（可能发生日志轮转），重新定位最新文件并继续监听

---

## 7. 错误处理

| 场景 | 处理 |
|------|------|
| 当天无日志文件 | 提示 "当天暂无日志文件" 并退出 |
| 日志目录不存在 | 提示 "日志目录不存在: <路径>" 并退出 |
| 无效级别参数 | 提示 "无效级别: xxx，可选值: error/warn/info/debug" 并退出 |
| 文件读取错误 | 输出错误信息，尝试重新定位文件 |

---

## 8. 文件结构

```
cmd/groot/main.go          # 修改：添加 tail 子命令解析
internal/cmd/
  ├── tail.go              # tail 命令核心实现
  ├── tail_file.go         # 文件定位与监听逻辑
  ├── tail_format.go       # JSON 解析与格式化输出
  └── tail_filter.go       # 过滤逻辑（级别、关键词）
```

职责划分：
- `tail.go`：命令入口、参数解析、主流程协调
- `tail_file.go`：文件定位、fsnotify 监听、文件读取
- `tail_format.go`：JSON 解析、格式化输出、颜色处理
- `tail_filter.go`：级别过滤、关键词过滤、组合过滤

---

## 9. 测试要点

| 测试项 | 验证内容 |
|--------|----------|
| 文件定位 | 当天有多个文件时选择最新的 |
| 文件定位 | 当天无文件时正确提示退出 |
| 格式化 | 标准 JSON 正确解析和格式化 |
| 格式化 | 非 JSON 行原样输出 |
| 颜色 | 各级别颜色正确应用 |
| 过滤 | `-l` 单独过滤正确 |
| 过滤 | `-k` 单独过滤正确 |
| 过滤 | `-l` + `-k` 组合过滤正确 |
| `-n` | 正确读取最后 N 行历史 |
| 实时跟踪 | 新日志写入后立即显示 |
| 文件轮转 | 文件切换后继续跟踪新文件 |
| Ctrl+C | 正确退出，清理资源 |