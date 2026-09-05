# 会话日志查看功能设计文档

## 一、功能设计

### 1.1 功能概述

用户在 Web 对话界面排查问题时，需要知道当前会话在后端执行过程中是否产生了错误（如 LLM 调用失败、工具调用失败、保存记录失败等）。本功能让用户无需登录服务器翻日志文件，在对话窗口内点击一个按钮即可查看当前会话的执行日志，并按级别快速定位错误。

功能由三部分组成：

1. **日志会话标识**：会话执行链路上产生的每条日志携带 `session_id` 字段，使日志可按会话归属检索。
2. **会话日志查询接口**：后端提供按会话 ID 查询日志的 Web 端点，从日志文件中过滤出指定会话的日志并返回结构化结果。
3. **前端日志弹窗**：对话窗口右上角提供日志按钮，点击弹出当前会话的日志列表，支持级别筛选与手动刷新。

### 1.2 能力清单

- 会话执行链路（Agent 执行、LLM 调用、工具/MCP 调用、子 Agent 调用、对话记录保存等）产生的日志均携带 `session_id` 字段
- 系统级日志（系统启动、配置加载、MCP 热加载、API Key 管理等）不携带 `session_id`，以字段缺失作为系统级日志的天然标识
- 定时任务触发的执行同样携带其生成的 `session_id`
- 后端按会话 ID 查询日志：扫描最近 7 天的日志文件，返回该会话最新的至多 1000 条结构化日志
- 前端在对话窗口顶栏右侧提供日志按钮，会话尚未产生（未发送过消息）时按钮禁用
- 日志弹窗以格式化列表展示：时间、级别（带颜色徽标，error 红色高亮）、消息文本
- 弹窗内支持级别筛选（全部/error/warn/info/debug），筛选在前端本地完成，切换即时生效
- 弹窗提供手动刷新；加载中、空结果、请求失败均有对应状态提示
- 结果被截断（超过 1000 条）时提示"仅展示最新 1000 条"

### 1.3 设计细节

#### 1.3.1 日志会话标识（internal/logger + internal/agent）

`logger` 包提供基于 context 的 logger 传递机制：

```go
// 把 logger 放入 context
func NewContext(ctx context.Context, l *Logger) context.Context

// 从 context 取出 logger；ctx 中不存在时返回全局默认 logger
func FromContext(ctx context.Context) *Logger

// 派生携带固定字段的子 logger
func (l *Logger) With(fields ...zap.Field) *Logger
```

注入点与传播方式：

- `Executor.Execute`（`internal/agent/executor.go`）是会话执行入口。入口处派生 `sessionLog := e.logger.With(zap.String("session_id", sessionID))`，通过 `logger.NewContext` 放入 ctx 向下传递；`Execute` 内部的日志调用均使用 `sessionLog`。
- 下游组件（`Engine.Run`、`call_agent` 及执行链路上的其他日志调用）通过 `logger.FromContext(ctx)` 获取 logger，自动携带 `session_id`；ctx 中没有时回退到全局 logger。
- 定时任务（`internal/schedule/runner.go`）调用同一个 `Executor.Execute`，注入逻辑自动生效；runner 自身在调用前后的日志显式携带 `session_id` 字段。

会话标识的作用范围仅限执行链路：全局 logger 的创建方式、日志文件格式（JSON）、按日期轮转策略以及系统级日志的调用方式均与会话标识机制无关。

#### 1.3.2 会话日志查询接口

**日志读取组件**（`internal/logger/reader.go`）：

- 依据 `LoggingConfig` 的日志目录与 `{date}` 文件名模式，定位最近 7 天内实际存在的日志文件
- 按日期从旧到新逐行读取，解析 JSON 并匹配 `session_id` 等于目标会话 ID 的行
- JSON 解析失败的行跳过；文件不存在跳过；均不视为错误
- 匹配结果超过 1000 条时保留最新的 1000 条，并置 `truncated = true`

**HTTP 端点**（`internal/api/handler/logs.go`）：

- 路由：`GET /web/logs/:sid`，注册在 `webGroup`（自动获得 WebSession 登录校验与限流）
- handler 只做参数校验、调用 reader、组装响应
- 级别筛选不在接口层提供，由前端本地完成

响应格式：

```json
{
  "session_id": "sess_abc123",
  "count": 42,
  "truncated": false,
  "logs": [
    {
      "timestamp": "2026-09-05T10:23:05.123+08:00",
      "level": "error",
      "message": "工具调用失败: connection refused",
      "caller": "agent/executor.go:332",
      "fields": { "tool": "web_search" }
    }
  ]
}
```

`fields` 为该行日志除 `timestamp`/`level`/`message`/`caller`/`session_id` 外的其余 JSON 字段。

边界情况：会话不存在或没有日志时返回 `count: 0` 的空列表（HTTP 200，非 404）。

#### 1.3.3 前端日志弹窗

**按钮**（`web/src/views/ChatView.vue`）：

- 位于顶栏（topbar）右侧，与左侧标题两端对齐；文档/列表样式图标，hover 提示"查看日志"
- 当前会话尚无 sessionID（新建会话未发送消息）时按钮禁用

**弹窗**（`web/src/components/chat/LogModal.vue`）：

- 遮罩、居中布局、ESC 与点击遮罩关闭，交互与 `SearchModal.vue` 一致
- 布局：

```
┌──────────────────────────────────────────────┐
│ 会话日志       [全部|error|warn|info|debug] ⟳  ✕ │
├──────────────────────────────────────────────┤
│ 10:23:01  INFO   开始执行 Agent                │
│ 10:23:05  ERROR  工具调用失败: connection...    │  ← error 行红色高亮
│                  （滚动区域）                   │
├──────────────────────────────────────────────┤
│ 共 42 条（截断时提示"仅展示最新 1000 条"）        │
└──────────────────────────────────────────────┘
```

- 打开弹窗时请求 `GET /web/logs/:sid`；加载中显示 spinner，空结果显示"暂无日志"，请求失败显示错误提示与重试按钮
- 级别筛选为前端本地过滤，筛选后为空显示"该级别暂无日志"
- 每行展示时间（HH:mm:ss）、级别徽标（带色）、消息文本；消息过长单行省略，hover 显示完整内容
- 刷新按钮重新发起一次请求

**API 客户端**：`web/src/api/client.ts` 提供 `getSessionLogs(sid)` 方法；`web/src/api/types.ts` 定义日志条目与响应类型。

#### 1.3.4 数据流

```
用户点击日志按钮
  → LogModal 打开，调用 getSessionLogs(sid)
  → GET /web/logs/:sid（WebSession 鉴权）
  → handler 调用 logger reader
  → reader 扫描最近 7 天日志文件，按 session_id 过滤
  → 返回结构化日志（≤1000 条）
  → 弹窗渲染列表，级别筛选本地完成
```

#### 1.3.5 测试

- **单元测试（Go）**：
  - `logger`：`FromContext` 的回退逻辑；`With` 派生后 JSON 输出包含 `session_id`；reader 的匹配、坏行跳过、文件缺失、1000 条截断
  - `handler`：参数校验、空结果、正常返回
- **系统测试（Python）**：`tests/python/` 覆盖端点行为，由用户自行运行
- 前端不引入新的测试工具

## 二、迭代说明

### 2.1 与上一版差异

本功能为全新功能，无上一版本。相关现状说明：

- 新增：`logger` 包的 context 传递机制（`NewContext`/`FromContext`/`With`）与 `reader.go` 日志读取组件
- 新增：`GET /web/logs/:sid` 端点与 `internal/api/handler/logs.go`
- 新增：前端 `LogModal.vue` 组件、顶栏日志按钮、`getSessionLogs` API 方法
- 调整：`internal/agent` 执行链路的日志调用由直接使用注入的 logger 改为使用携带 `session_id` 的会话级 logger（经 context 传递）
- 现有日志文件格式、轮转策略、系统级日志调用方式均保持不变
