# Web 消息底部状态栏设计文档

## 一、功能设计

### 1.1 功能概述

Web 聊天界面中，每条助手回复的底部有一个状态栏，用于呈现该次回复的执行状态与结果元信息，解决三个问题：

1. 回复生成过程中，用户无法感知任务已经运行了多久；
2. 回复完成后，用户需要快捷复制整条回复正文，并了解本次回复的总用时与完成时刻；
3. 用户需要了解每轮回复的 LLM token 消耗（输入 / 输出）。

同时，输入框下方有一个会话级统计条，呈现整个会话的累计指标（轮次、总耗时、累计 token 用量、当前模型）。

### 1.2 能力清单

#### 消息底部状态栏（每条助手回复）

- **实时计时**：回复流式生成期间，底部显示「深度思考中 X秒」，秒数每秒跳动一次，从消息发出时刻起计。
- **复制回复**：回复完成后，底部显示复制按钮，点击将整条回复正文（Markdown 原文）写入剪贴板，并以轻提示反馈结果；剪贴板 API 不可用（如非 HTTPS 环境）时退回 `execCommand` 兜底。
- **Token 用量**：回复完成后显示本轮的「输入 X tok · 输出 X tok」；数量在千位及以上缩写为 `K`（保留一位小数，如 `39.5K`）、百万位及以上缩写为 `M`；无 token 数据（旧记录）时不显示。
- **总用时**：回复完成后显示「用时 X秒」（超过一分钟显示「X分Y秒」），位于完成时刻之前。
- **完成时刻**：回复完成后显示完成的钟点时间，格式 `HH:MM`（如 `19:01`）。

底部一行的展示顺序为：复制按钮 → 输入 Token → 输出 Token → 用时 → 完成时刻。
- **历史回填**：打开历史会话时，每条助手消息同样显示总用时、token 用量与完成时刻（数据来自会话历史接口）；复制按钮同样可用。

#### 会话统计条（输入框下方）

- **轮次**：当前会话的对话轮数。
- **总耗时**：会话内各轮回复耗时之和；一分钟内保留一位小数（如 `15.3s`），超过一分钟显示「X分Y秒」。
- **累计 Token**：会话内各轮的输入 / 输出 token 之和，展示为「输入 X tok · 输出 X tok」，数量在千位及以上缩写为 `K`、百万位及以上缩写为 `M`（保留一位小数）。
- **模型**：最近一轮使用的模型名。

### 1.3 设计细节

#### 数据模型（前端 store）

`ChatMessage` 携带计时与 token 字段（仅助手消息使用）：

| 字段 | 含义 | 来源 |
|------|------|------|
| `startedAt` | 流式开始的墙钟时间戳（ms） | `send()` 创建助手消息时打点 |
| `durationMs` | 整条回复总用时（ms） | 流结束（含停止/出错）时结算；历史消息取接口的 `duration_ms`（旧版后端缺失时退回 `duration * 1000`） |
| `finishedAt` | 完成时刻墙钟时间戳（ms） | 流结束时打点；历史消息取 `timestamp`（该轮结束时间） |
| `promptTokens` | 本轮输入 token 数 | 历史消息取接口的 `prompt_tokens`；流式消息在流结束后由最近一次对话详情（`GET /chat/:sid`）回填 |
| `completionTokens` | 本轮输出 token 数 | 同上，取 `completion_tokens` |

store 另提供计算属性 `sessionStats`，对当前已加载的各轮助手消息求和，得出会话累计的 `durationMs` / `promptTokens` / `completionTokens`，供统计条展示。

#### 后端接口

会话历史接口（`GET /sess/:sid`）的每条消息（`memory.Message`）携带该轮的执行指标，与该轮 `ChatRecord` 对应字段一致：

| 字段 | 含义 |
|------|------|
| `duration_ms` | 毫秒级总用时 |
| `prompt_tokens` | 输入 token 数 |
| `completion_tokens` | 输出 token 数 |
| `total_tokens` | token 合计 |

#### 组件

- `web/src/components/chat/MessageFooter.vue`，由 `MessageList` 在每条助手消息的正文之后渲染：
  - 流式中（`streaming === true`）：渲染加载图标 + 「深度思考中 X秒」，内部以 1 秒间隔的定时器驱动刷新，仅在流式期间运行，组件卸载或流结束即清除；
  - 完成后：渲染「复制按钮 + 输入/输出 Token + 用时 + 完成时刻」一行，任一信息存在即显示该行，复制按钮仅在正文非空时出现；token 数量经 K/M 缩写后展示。
- `web/src/components/chat/StatsBar.vue`，渲染于输入框下方：接收 `round`（轮次）、`stats`（会话累计统计）与 `record`（最近一轮记录，仅取模型名），按「轮次 · 耗时 · 输入 Token · 输出 Token · 模型」顺序展示，无数据的段落自动省略。

#### 文案

中英文案均在 i18n 下的 `chat` 命名空间：`thinkingLive`、`timeUsed`、`durationSec`、`durationMinSec`、`tokenInputShort`、`tokenOutputShort`、`round`、`duration`、`copy`、`copied`、`copyFailed`。消息底部状态栏与会话统计条的 token 文案统一使用 `tokenInputShort` / `tokenOutputShort`（「输入 {n} tok」样式），数量缩写由共享工具 `web/src/utils/format.ts` 的 `fmtTok` 提供。

## 二、迭代说明

### 2.1 与上一版差异

- 新增：消息底部状态栏展示本轮「输入 Token / 输出 Token」；`ChatMessage` 新增 `promptTokens`、`completionTokens` 字段，历史加载与流式结束两条路径分别回填（流式路径复用流结束后的 `GET /chat/:sid` 拉取结果）。
- 调整：会话统计条（StatsBar）的「耗时」由最近一轮耗时改为**会话总耗时**（各轮求和）；「Token」由最近一轮的合计展示改为**会话累计的输入 / 输出分列展示**；模型名保留，仍取最近一轮记录。
- 新增：后端会话历史消息（`memory.Message`）增加 `duration_ms`、`prompt_tokens`、`completion_tokens`、`total_tokens` 字段，由该轮 `ChatRecord` 填充；历史消息的用时回填优先使用毫秒级 `duration_ms`。

### 2.2 v2 变更：token 展示样式与底部栏顺序调整

- 调整：消息底部状态栏与会话统计条的 token 文案统一由「输入 Token X · 输出 Token Y」改为「输入 X tok · 输出 X tok」，数量千位以上缩写为 `K`、百万位以上缩写为 `M`（保留一位小数）；i18n key 由 `tokenInput` / `tokenOutput` 替换为 `tokenInputShort` / `tokenOutputShort`，缩写逻辑抽取为共享工具 `web/src/utils/format.ts`。
- 调整：消息底部状态栏展示顺序由「复制 → 用时 → Token → 时刻」改为「复制 → Token → 用时 → 时刻」，「用时」移至倒数第二位、紧邻完成时刻。
- 不变：会话统计条的段落顺序（轮次 → 耗时 → Token → 模型）、后端接口。

### 2.3 v3 修复：打开历史会话时统计条不显示模型

- 修复：从侧栏点击打开历史会话时，会话统计条缺失「模型」段落。原因是模型名取自最近一轮对话详情（`GET /chat/:sid`），而该请求此前只在页面带会话地址首次加载和一轮回复结束后发起，侧栏切换会话不会触发。现将该请求收敛进 store 的 `openSession`（加载完历史消息后发起），所有打开历史会话的入口统一回填。
- 不变：后端接口与存储（每轮 `ChatRecord` 的 `model` 字段一直随 chats 表持久化）。
