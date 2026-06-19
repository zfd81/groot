# groot tail 命令设计文档

**Goal:** 为 groot 提供实时日志查看子命令,类似 `tail -f`,支持格式化输出和颜色高亮。

**Architecture:** `groot tail` 子命令使用 fsnotify 监听日志目录变化,实时读取新写入内容,JSON 解析后格式化输出,支持按级别和关键词过滤,并能在日志文件轮转时自动切换到最新文件。

**Tech Stack:** Go、[fsnotify](https://github.com/fsnotify/fsnotify)(文件监听)、ANSI 颜色码

---

## 一、功能设计

### 1.1 命令用法

```
groot tail [-n N] [-l level] [-k keyword] [-h|--help]
```

| 参数 | 必填 | 说明 | 示例 |
|------|------|------|------|
| `-n N` | 否 | 启动时先显示最近 N 行历史日志,然后实时跟踪。默认 `100`。N 必须是正整数 | `groot tail -n 50` |
| `-l level` | 否 | 按级别过滤,可选值见下方"级别参数"。不指定则不按级别过滤 | `groot tail -l error` |
| `-k keyword` | 否 | 关键词过滤,只显示包含该关键词的日志行。不指定则不过滤 | `groot tail -k "api_request"` |
| `-h`, `--help` | 否 | 打印 tail 子命令帮助并退出 | `groot tail -h` |

级别参数取值(大小写不敏感,支持别名):

| 输入 | 归一化结果 |
|------|-----------|
| `error`, `err` | `error` |
| `warn`, `warning` | `warn` |
| `info` | `info` |
| `debug` | `debug` |

参数可组合使用:

```bash
groot tail                                  # 显示最近 100 行历史日志后实时跟踪
groot tail -n 50 -l error                   # 显示最近 50 行错误日志后实时跟踪
groot tail -k "connection" -l error         # 实时跟踪包含 connection 的错误日志
groot tail -n 20 -k "session"               # 显示最近 20 行包含 session 的日志后实时跟踪
```

入口实现见 [`internal/cmd/tail.go`](../../../internal/cmd/tail.go) 的 `ParseTailFlags` 与 `RunTail`,命令注册见 [`cmd/groot/main.go`](../../../cmd/groot/main.go) 的 `handleTailCommand`。

### 1.2 文件定位逻辑

#### 1.2.1 日志目录获取

日志目录通过配置文件读取,而非固定路径,流程:

1. 确定 groot 工作目录:优先 `GROOT_HOME` 环境变量,否则取 `$HOME/.groot`(Windows 下回退到 `%USERPROFILE%\.groot`,均缺失时使用相对路径 `.groot`)。见 `GetDefaultHome`。
2. 读取 `<homeDir>/config.yaml` 中的 `logging.file.directory` 配置项。配置文件不存在时使用默认值 `logs`。见 `loadConfig` / `getDefaultLogConfig`。
3. 通过 `config.ResolvePath(dir, homeDir)` 解析为绝对路径,支持相对路径(相对 `homeDir`)与绝对路径。见 `resolveLogDir`。

配置示例:

```yaml
logging:
  file:
    directory: logs            # 相对路径,解析为 <homeDir>/logs
    # 或
    directory: /var/log/groot  # 绝对路径
```

#### 1.2.2 文件定位规则

在日志目录下定位当天最新的日志文件,实现见 [`internal/cmd/tail_file.go`](../../../internal/cmd/tail_file.go) 的 `findLatestLogFile`:

1. 检查日志目录是否存在,不存在则报错 `日志目录不存在: <路径>` 并退出。
2. 取当天日期字符串 `YYYY-MM-DD`(`time.Now().Format("2006-01-02")`)。
3. 遍历目录,筛选文件名包含当天日期串的所有普通文件(目录跳过)。
4. 按文件修改时间倒序,取最新的一个。
5. 当天没有匹配文件时报错 `当天暂无日志文件` 并退出。

为什么用"当天最新"而非固定文件名:当前 logger 仅按日期轮转,但未来可能加入按大小分割,例如产生 `groot-{date}-001.log`、`groot-{date}-002.log` 等;基于"最新修改时间"的规则可在不改造 tail 的前提下自动跟踪当前正在写入的文件。

### 1.3 输出格式

#### 1.3.1 JSON 解析

原始日志为 JSON 行,例如:

```json
{"timestamp":"2026-04-21T19:18:38+08:00","level":"info","caller":"api/server.go:42","message":"API 服务启动","event":"api_request","path":"/chat","method":"POST"}
```

解析时按以下顺序抽取字段(见 `parseLogJSON`):`timestamp`、`level`、`caller`、`message`、`event`,其余字段统一作为 extra 输出。解析失败的行(非 JSON)按原样输出,不做任何格式化或着色。

#### 1.3.2 格式化输出

格式化后字段顺序:

```
{timestamp}  {level}  {caller}  {message}  event={event}  {key1}={value1}  {key2}={value2}...
```

各字段之间使用两个空格(`"  "`)作为分隔符(见 `buildOutput`)。

字段格式化规则:

- `timestamp`:保留 JSON 原始字符串(通常是 ISO8601),不做修改。
- `level`:转大写并固定为 5 字符宽度,右侧用空格补齐:`INFO `、`WARN `、`ERROR`、`DEBUG`。未知级别也补齐到 5 字符。
- `caller`、`message`:存在则原样输出。
- `event`:存在时输出为 `event=<值>`。
- 其余字段:按 `key=value` 格式逐个拼接,字符串/布尔/null 直接转成文本,数字若为整数则以整数形式输出,复杂结构体(数组、对象)序列化为 JSON 字符串。各字段之间用两个空格分隔,然后整体追加到输出末尾。

示例:

```
2026-04-21T19:18:38+08:00  INFO   api/server.go:42  API 服务启动  event=api_request  path=/chat  method=POST
```

实现见 [`internal/cmd/tail_format.go`](../../../internal/cmd/tail_format.go) 的 `Formatter`、`buildOutput`、`buildExtraFields`。

### 1.4 颜色高亮

按级别给整行套色,色码定义在 `tail_format.go`:

| 级别 | ANSI 颜色码 | 显示效果 |
|------|-------------|----------|
| ERROR | `\x1b[31m` | 红色 |
| WARN  | `\x1b[33m` | 黄色 |
| INFO  | `\x1b[32m` | 绿色 |
| DEBUG | `\x1b[90m` | 灰色 |

每行末尾追加 `\x1b[0m`(Reset)恢复默认色。未知级别不着色,直接输出原文。逻辑见 `applyColor`。

### 1.5 过滤逻辑

实现见 [`internal/cmd/tail_filter.go`](../../../internal/cmd/tail_filter.go) 的 `Filter.Match`。

#### 1.5.1 级别过滤 `-l level`

- 命令行入参经 `validateLevel` 归一化(见 1.1 节别名表)。
- 比较时同样将日志中的 `level` 字段转小写,严格相等才匹配。

#### 1.5.2 关键词过滤 `-k keyword`

- 在整行原始文本(JSON 字符串)中查找子串,大小写敏感。
- 使用 `strings.Contains` 实现。

#### 1.5.3 组合过滤

`-l` 与 `-k` 同时指定时,两个条件必须**同时**满足:

```bash
groot tail -l error -k "connection"
# 仅显示 level=error 且整行含 "connection" 的日志
```

#### 1.5.4 非 JSON 行的过滤行为

当某行无法解析为 JSON 时:

- 既未指定 `-l` 也未指定 `-k` → 显示。
- 仅指定 `-l` → 不显示(无法判断级别)。
- 指定了 `-k`(可同时带 `-l`) → 仅按关键词匹配该行原始文本。

JSON 解析成功时,正常按 `levelMatch && keywordMatch` 判断。

### 1.6 实时跟踪与轮转处理

实现见 [`internal/cmd/tail_file.go`](../../../internal/cmd/tail_file.go) 的 `FileWatcher`。

启动流程(`RunTail`):

1. 解析参数后定位当天最新的日志文件。
2. 通过 `readLastNLines` 读取最后 N 行(默认 100),逐行经 `Filter.Match` 过滤后由 `Formatter.Format` 着色输出。
3. 注册 `SIGINT` / `SIGTERM` 信号,收到后打印"退出..."并通过 `context.CancelFunc` 优雅停止 watcher。
4. 创建 `FileWatcher` 并 `Start`,先将当前文件读取位置初始化到文件末尾(`initPosition`,seek 到 EOF),然后进入事件循环。

事件循环:

- 监听对象是日志**目录**(`fsnotify.Watcher.Add(logDir)`),而不是单个文件,以便同时捕获文件创建、轮转与写入。
- `Write` 事件:仅当事件目标等于 `currentFile` 时,从 `currentPosition` 处读取新内容,逐行过滤、格式化并打印,然后更新 `currentPosition` 为新的文件末尾。
- `Remove` / `Rename` 事件:若被移除/重命名的是 `currentFile`,调用 `findLatestLogFile` 重新定位,把 `currentFile` 切换到新文件并重置位置到新文件的末尾。
- `Watcher.Errors` 通道上的错误打印到 stderr,但不退出循环。
- `ctx.Done()` 触发时优雅返回。

按 `Ctrl+C` 触发 `SIGINT`,信号 goroutine 取消 context,事件循环检测到后退出,`Stop` 关闭 watcher。

### 1.7 错误处理

| 场景 | 处理 |
|------|------|
| 日志目录不存在 | 报错 `日志目录不存在: <路径>` 并退出 |
| 日志目录不是目录 | 报错 `<路径> 不是目录` 并退出 |
| 当天无匹配文件 | 报错 `当天暂无日志文件` 并退出 |
| 非法 `-l` 取值 | 报错 `invalid level: <值> (valid levels: error, warn, info, debug)` 并退出 |
| `-n` 缺值或非正整数 | 报错并退出(`-n requires a value` / `invalid value for -n: <值>` / `-n must be a positive integer`) |
| `-k` 缺值 | 报错 `-k requires a value` 并退出 |
| 未知 flag / 多余位置参数 | 报错 `unknown flag: <值>` 或 `unexpected argument: <值>` 并退出 |
| 配置文件不存在 | 使用默认配置(directory=`logs`)继续 |
| 读取文件出错(运行期) | 写 stderr 提示 `读取新日志行失败` / `查找新日志文件失败`,继续监听 |
| `fsnotify.Errors` 异常 | 写 stderr 提示 `文件监听错误`,继续监听 |

### 1.8 文件结构

```
cmd/groot/main.go                # 注册 tail 子命令(handleTailCommand)
internal/cmd/
  ├── tail.go                    # 命令入口、参数解析(ParseTailFlags)、配置加载、主流程(RunTail)、帮助打印(PrintTailHelp)
  ├── tail_file.go               # 文件定位(findLatestLogFile)、最后 N 行读取(readLastNLines)、fsnotify 目录监听与轮转切换(FileWatcher)
  ├── tail_format.go             # JSON 解析(parseLogJSON)、格式化输出(buildOutput)、颜色处理(applyColor)
  └── tail_filter.go             # 级别 / 关键词过滤(Filter.Match)
```

---

## 二、迭代说明

### 2.1 与上一版差异

- **`-n` 默认值**:由"默认 0(不显示历史)"调整为默认 100。
- **`-l` 级别参数**:补充支持的别名 `err`(=`error`)、`warning`(=`warn`),并明确大小写不敏感。
- **`-h` / `--help`**:在文档中显式列出 tail 子命令的帮助选项。
- **配置缺省**:补充"配置文件不存在时使用默认目录 `logs`"的行为。
- **fsnotify 监听对象**:由"监听单个文件"修正为"监听日志目录",事件循环明确处理 `Write` / `Remove` / `Rename`。
- **格式化分隔符**:明确字段之间使用两个空格连接;`level` 字段固定 5 字符宽度并按 `INFO `、`WARN `、`ERROR`、`DEBUG` 处理。
- **extra 字段格式化**:补充对数字、布尔、null、复杂对象的具体序列化规则。
- **过滤对非 JSON 行的处理**:明确无法解析 JSON 时的三种情形(无过滤显示 / 只 `-l` 不显示 / 含 `-k` 仅按关键词匹配)。
- **错误处理**:补充非法 `-n`、缺值 / 未知 flag、多余位置参数、运行期 fsnotify 错误等场景的提示。
- **文件结构**:在职责描述中补充各文件具体函数,使文档与实现一一对应。
