# groot status 命令设计文档

**Goal:** 为 groot 提供实例状态查看子命令,通过调用运行中 groot 实例的 `/health` 接口显示实例运行状态和组件健康信息。

**Architecture:** `groot status` 子命令向 `http://127.0.0.1:{port}/health` 发送 HTTP GET 请求,获取 JSON 响应后格式化输出为人类可读的摘要信息。

**Tech Stack:** Go, `net/http`

---

## 一、功能设计

### 1.1 功能概述

`groot status` 用于查看本机运行中 Groot 实例的整体运行状态。它通过 HTTP 调用实例的 `/health` 接口,把响应中的状态、版本、运行时间、组件健康度、关键运行指标等信息渲染成中文摘要输出。

代码位置: [`internal/cmd/status.go`](../../../internal/cmd/status.go)。

### 1.2 命令用法

```
groot status [选项]
```

| 参数 | 必填 | 说明 |
|------|------|------|
| `-p <port>` | 否 | 指定 Groot 服务端口,合法范围 `1-65535`。未指定时从配置读取。 |
| `-h`, `--help` | 否 | 显示 status 子命令帮助 |

帮助文本由 [`PrintStatusHelp`](../../../internal/cmd/status.go) 输出,包含上述选项及示例:

```
groot status            # 查看默认端口实例状态
groot status -p 9090    # 查看 9090 端口实例状态
```

### 1.3 端口确定逻辑

实现于 [`RunStatus`](../../../internal/cmd/status.go):

1. 解析 `-p` 后,若标志值为 `0`(未显式指定),则加载 `{GROOT_HOME}/config.yaml` 的 `server.port` 作为目标端口。
2. 若用户显式传入 `-p`,则参数解析阶段已校验 `1-65535`,直接使用该端口。
3. 使用确定后的端口向 `http://127.0.0.1:{port}/health` 发起 GET,超时 5 秒。

### 1.4 输出格式

#### 1.4.1 实例运行中

代码: [`printStatusOutput`](../../../internal/cmd/status.go)。

```
Groot 实例状态

状态:      healthy
版本:      1.0.0
运行时间:  2h35m
端口:      8080

组件状态:
  LLM:         healthy (gpt-4o)
  MCP Servers: healthy (3 个)
  Skills:      healthy (5 个)
  Memory:      healthy (12 个会话)

活跃对话:  1
```

各字段对应 `/health` 响应中的字段:

| 显示项 | 数据来源 |
|--------|----------|
| 状态 | `status` |
| 版本 | `version` |
| 运行时间 | `uptime` |
| 端口 | 命令行确定的目标端口 |
| LLM | `checks.llm.status`、`checks.llm.info.model`、`checks.llm.info.error` |
| MCP Servers | `checks.mcp_servers.status`、`checks.mcp_servers.info`(数组长度) |
| Skills | `checks.skills.status`、`checks.skills.info.count` |
| Memory | `checks.memory.status`、`checks.memory.info.sessions` |
| 活跃对话 | `metrics.chats_running` |

LLM 行的细节:

- 仅有模型名时显示 `healthy (gpt-4o)`。
- 同时存在 `error` 时,模型名与错误信息以逗号拼接,例如 `unhealthy (gpt-4o, connection refused)`。
- `info.model` 与 `info.error` 都缺失时,只显示状态,不带括号补充。

组件块中每一项只在 `checks` 中存在对应键时才输出。

#### 1.4.2 实例未运行 / 请求失败

代码: [`printNotRunning`](../../../internal/cmd/status.go)。

```
未检测到运行中的 Groot 实例（端口 8080）
提示: 请确认 Groot 是否已启动，或使用 -p 指定其他端口
```

任何 HTTP 调用失败(连接拒绝、超时、DNS 失败、非 200 响应、JSON 解析失败)都走这条路径。

### 1.5 错误处理

| 场景 | 处理 | 退出码 |
|------|------|--------|
| 连接拒绝 / 超时 / DNS 失败 | 输出"未检测到运行中实例"提示 | 0 |
| HTTP 非 200 响应 | 同上,输出"未检测到运行中实例"提示 | 0 |
| 响应 JSON 解析失败 | 同上,输出"未检测到运行中实例"提示 | 0 |
| 配置加载失败(未指定 `-p` 时) | 输出错误信息 | 1 |
| `-p` 参数缺值 / 非数字 / 越界 | 输出错误信息 | 1 |
| 未知参数 / 多余位置参数 | 输出错误信息 | 1 |

参数解析的退出码由 [`cmd/groot/main.go`](../../../cmd/groot/main.go) 中 `handleStatusCommand` 调用 `os.Exit(1)` 触发;`-h`/`--help` 在解析阶段直接打印帮助并 `os.Exit(0)`。

### 1.6 文件结构

```
cmd/groot/main.go            # status 子命令分发入口 handleStatusCommand
internal/cmd/
  ├── status.go              # 命令解析、端口决议、HTTP 请求、输出渲染
  └── status_test.go         # 单元测试
```

`/health` 接口由服务端 [`internal/api/handler/health.go`](../../../internal/api/handler/health.go) 提供,响应结构定义在 [`internal/api/types/types.go`](../../../internal/api/types/types.go) 的 `HealthResponse`。

### 1.7 测试要点

测试代码位于 [`internal/cmd/status_test.go`](../../../internal/cmd/status_test.go),覆盖:

| 测试项 | 验证内容 |
|--------|----------|
| 参数解析 | 无参数时端口默认为 `0`(回退到配置) |
| 参数解析 | `-p` 接受合法端口 |
| 参数解析 | 非数字、负数、`0`、超过 `65535` 的端口被拒绝 |
| 参数解析 | 缺值的 `-p` 报错 |
| 参数解析 | 未知参数与多余位置参数报错 |
| HTTP 请求 | 成功响应正确解析为 `HealthResponse` |
| HTTP 请求 | 非 200 响应被识别为错误 |
| HTTP 请求 | 响应体非 JSON 时返回解析错误 |
| 输出格式 | 健康实例输出包含状态、版本、运行时间、端口、组件块、活跃对话 |
| 输出格式 | LLM 异常时输出包含 `info.error` 的内容 |
| 输出格式 | 0 个 skill / 0 个会话等零值场景正确显示 |
| 集成测试 | 完整流程(读配置 → HTTP 请求 → 输出)正确 |
| 未运行实例 | 连接失败时输出"未检测到运行中实例"提示 |

---

## 二、迭代说明

本文档为 `groot status` 命令的初版设计文档,无历史版本。后续迭代请在此章节追加变更点。
