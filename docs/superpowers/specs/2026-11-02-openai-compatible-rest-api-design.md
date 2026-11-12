# OpenAI 兼容 REST API 设计文档

## 一、功能设计

### 1.1 功能概述

Groot 对外提供的 REST API 采用 OpenAI 的接口形态：路由路径、请求参数、响应结构与鉴权方式与 OpenAI 保持一致，使现成的 OpenAI 官方 SDK 与第三方兼容客户端只需把 `base_url` 指向 Groot 的服务地址、把 `api_key` 换成 Groot 签发的 API Key，就能完成模型列举、多轮对话、语音转录、语音合成等调用。

Groot 在对话上有一项 OpenAI 没有的能力：会话由服务端维护，调用方不需要把整段历史塞进 `messages` 就能获得连续上下文。这项能力通过扩展字段 `session_id` 保留——不带 `session_id` 时新建会话，带上时在既有会话上继续。OpenAI 标准的 `messages` 参数被完整支持，调用方自己拼接的历史消息会接在服务端上下文之后，两种用法可以并存。

解决的问题：

- 客户端不必为 Groot 写专用 SDK，直接用 OpenAI 官方 SDK 接入。
- 参数命名、返回值结构、鉴权方式、错误结构在一套体系内统一，减少跨系统对接时的理解成本。
- Groot 特有的能力（服务端会话、Agent 选择、无状态图片解析、定时任务、会话管理）以扩展字段与扩展接口的形式保留，不与 OpenAI 规范冲突。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| OpenAI 客户端兼容 | OpenAI 官方 SDK 与兼容客户端改 `base_url`、`api_key` 即可接入 |
| 对话补全 | `POST /chat/completions`，支持流式与非流式两种返回 |
| 服务端会话 | 扩展字段 `session_id`，不传即新建会话、传了即续接，上下文由服务端维护 |
| 调用方拼接上下文 | 标准 `messages` 参数被完整接受，其中非系统消息接在服务端上下文之后 |
| 多模态输入 | 文本、图片、音频、文件四类内容片段，遵循 OpenAI 的 content parts 格式 |
| Agent 选择 | 扩展字段 `agent_name`，指定由哪个子 Agent 执行本轮对话 |
| 模型列举 | `GET /models`，返回 OpenAI 的列表格式，条目为 Groot 中配置的模型逻辑名 |
| 语音转录 | `POST /audio/transcriptions`，上游返回什么格式就转发什么格式 |
| 语音合成 | `POST /audio/speech`，`voice` 为必填 |
| 图片解析 | `POST /image/understanding`，无状态图片转文字 |
| 会话管理 | `/sess/*` 系列，会话详情、历史、检索、改名、删除 |
| 任务查询与控制 | `/chat/status/*`、`/chat/{sid}`、`/chat/{sid}/{cid}`、`/chat/{sid}/{cid}/stop` |
| 定时任务 | `/schedule/*` 系列 |
| Bearer 鉴权 | 标准 `Authorization: Bearer <API Key>` |
| OpenAI 错误体 | 全部接口的失败响应统一为 `{"error": {...}}` 结构 |
| Web 界面自用 | Web 界面沿用 Cookie 会话，调用同一批接口 |

### 1.3 对外接口总览

#### 1.3.1 路由表

对外（API Key / Web Cookie 均可）路由：

| 路由 | 方法 | 权限点 | 说明 |
|---|---|---|---|
| `/chat/completions` | POST | `chat` | 对话补全，流式与非流式 |
| `/models` | GET | `chat` | 模型列表，OpenAI 列表格式 |
| `/audio/transcriptions` | POST | `chat` | 语音转录 |
| `/audio/speech` | POST | `chat` | 语音合成 |
| `/image/understanding` | POST | `chat` | 图片解析 |
| `/chat/status/{sid}` | GET | `status` | 会话执行状态 |
| `/chat/{sid}` | GET | `detail` | 会话最近一次对话详情 |
| `/chat/{sid}/{cid}` | GET | `detail` | 指定对话详情 |
| `/chat/{sid}/{cid}/stop` | POST | `chat` | 中止正在生成的对话 |
| `/sess/{sid}` | GET | `session` | 会话详情 |
| `/sess/{sid}` | PATCH | `session` | 会话改名 |
| `/sess/{sid}` | DELETE | `delete` | 会话删除 |
| `/sess/history` | GET | `history` | 会话列表 |
| `/sess/search` | GET | `session` | 会话检索 |
| `/schedule/` | GET | `schedule` | 定时任务列表 |
| `/schedule/{id}` | GET | `schedule` | 定时任务详情 |
| `/schedule/{id}` | DELETE | `schedule` | 定时任务删除 |
| `/schedule/{id}/disable` | POST | `schedule` | 停用 |
| `/schedule/{id}/enable` | POST | `schedule` | 启用 |
| `/schedule/{id}/archive` | POST | `schedule` | 归档 |
| `/schedule/{id}/history` | GET | `schedule` | 执行历史 |
| `/web/health` | GET | 无 | 健康检查，免鉴权 |

`/web/*` 下的界面专用路由保持 Cookie 会话鉴权，与会话相关的接口共用与对外接口相同的 handler。

路径不带 `/v1` 前缀：OpenAI SDK 的 `base_url` 按调用方传入原样使用，`base_url="http://host:8080"` 即命中 `/chat/completions`，`base_url="http://host:8080/v1"` 由反向代理把 `/v1/*` 改写为 `/*`。Groot 自身不假设调用方会带 `/v1`。

#### 1.3.2 认证

对外接口使用 Bearer 鉴权：

```
Authorization: Bearer <API Key>
```

`API Key` 为 Groot 在 Web 界面「API Key 管理」中签发的 JWT，携带租户与权限点集合。中间件按以下顺序处理每个请求：

1. 请求携带有效 Web 登录 Cookie 时，视为管理员（等同 `all` 权限），跳过 API Key 校验。
2. 否则从 `Authorization` 头取出 `Bearer` 后的令牌，校验签名与有效期。
3. 以令牌的 `jti` 反查 API Key 记录，确认未被删除。
4. 按路由与方法取所需权限点，校验令牌权限集合是否满足。
5. 注入调用方身份：`caller`（Key 名称）、`tenant_id`（Key 所属租户）、`user_id`（请求头 `user` 的值，见 1.3.3）。

鉴权的失败统一返回 401 或 403 的 OpenAI 错误体，不区分签名错误、过期、已吊销，避免向调用方泄露判别信息。

`Authorization` 是唯一的 API Key 传递位置。Groot 不读取其它自定义请求头作为凭证，配置文件与 Web 设置中也不再存在「认证请求头名」这一配置项。

#### 1.3.3 终端用户标识

终端用户标识统一叫 `user`，承载会话归属。传递位置按接口形态区分：

| 接口 | 位置 | 说明 |
|---|---|---|
| `POST /chat/completions` | 请求体 `user` | OpenAI 标准字段，由对话 handler 读取 |
| `/sess/*`、`/chat/*`（`/chat/completions` 除外） | 请求头 `user` | 这些接口在 OpenAI 中没有对应路由，且多数是 `GET`/`DELETE`，没有请求体；沿用 Groot 原有的「标识走请求头」方式，只把名字从 `X-User-ID` 改为 `user` |
| `/audio/*`、`/image/understanding`、`/schedule/*` | 不使用 | 这些接口不关联会话归属 |

```
POST /chat/completions
{"messages": [...], "user": "u_10086"}

GET /sess/history
user: u_10086
```

取值限字母、数字、下划线、连字符，长度 1..64，与既有的标识校验规则一致。中间件从请求头读出并注入上下文；`/chat/completions` 的 handler 在读请求体后自行覆盖该值。

会话类 handler 在缺少或非法时报 400 `invalid_request`，`param` 为 `user`，与既有逻辑一致；`/audio/*`、`/image/understanding`、`/schedule/*` 不读该值。Web Cookie 调用方视为管理员，`user` 可以缺省，会话归属不受限制。

#### 1.3.4 错误响应

所有接口的失败响应统一为：

```json
{
  "error": {
    "message": "模型不存在: gpt-4o",
    "type": "invalid_request_error",
    "param": "model",
    "code": "model_not_found"
  }
}
```

| 字段 | 说明 |
|---|---|
| `message` | 可读的错误描述，中文 |
| `type` | 错误大类，取值见下表 |
| `param` | 出错的请求参数名；与具体参数无关时为 `null` |
| `code` | 具体错误码，取值见下表；无细分时为 `null` |

| HTTP | `type` | `code` 示例 | 触发场景 |
|---|---|---|---|
| 400 | `invalid_request_error` | `invalid_request` | 请求体不是合法 JSON、必填字段缺失、字段取值非法；附件数量超限、文件名缺失、内容缺失、Base64 解码失败 |
| 400 | `invalid_request_error` | `model_not_found` | `model` 指定的模型不存在、已禁用，或未配置对应类型的默认模型 |
| 400 | `invalid_request_error` | `unsupported_file_type` | 文件片段的内容不是可解码的文本 |
| 400 | `invalid_request_error` | `unsupported_type` | 上传文件的类型不在支持集合内（`/chat/completions`、`/audio/transcriptions`、`/image/understanding`） |
| 401 | `authentication_error` | `invalid_api_key` | 凭证缺失、签名错误、过期、已吊销 |
| 403 | `permission_error` | `insufficient_permissions` | API Key 缺少所需权限点 |
| 403 | `permission_error` | `tenant_not_bound` | API Key 未绑定租户，无法确定会话归属 |
| 404 | `invalid_request_error` | `session_not_found` | 会话不存在或不属于当前调用方 |
| 404 | `invalid_request_error` | `task_not_found` | 对话 ID 不存在 |
| 404 | `invalid_request_error` | `schedule_not_found` | 定时任务不存在 |
| 409 | `invalid_request_error` | `chat_limit_exceeded` | 同一会话已有对话正在执行 |
| 409 | `invalid_request_error` | `chat_still_running` | 会话仍在执行，暂不能删除或改名 |
| 413 | `invalid_request_error` | `file_too_large` | 上传文件超过单文件上限或附件总体积上限 |
| 429 | `rate_limit_error` | `rate_limit_exceeded` | 触发限流 |
| 499 | `invalid_request_error` | `client_closed_request` | 调用方在拿到响应前放弃请求 |
| 500 | `api_error` | `internal_error` | 服务端内部错误 |
| 502 | `api_error` | `upstream_error` | 上游模型服务返回错误 |
| 503 | `api_error` | `schedule_unavailable` | 定时任务服务不可用或未启用 |

流式响应中出错时，HTTP 状态码保持 200，错误以 SSE 事件下发：

```
data: {"error":{"message":"Agent 执行异常: ...","type":"api_error","param":null,"code":null}}

data: [DONE]

```

**所有由应用处理的请求**（含 `/web/*` 下的界面专用端点、未匹配的路径与方法，以及两个中间件）都使用这一错误信封：未匹配的路径与方法一律返回 404，鉴权中间件的 401/403、限流中间件的 429 同样使用这一结构。Web 前端因此只维护一种错误解析逻辑。

方法不匹配不单独返回 405：Hertz 的 405 处理链只包含全局中间件，不含路由分组上的鉴权中间件，会让未携带凭证的请求直接拿到 405，等同于无鉴权的路由探测；统一落到 404 既保持信封一致，也不泄露路径是否存在。

**唯一例外：** 请求体超过 HTTP 服务器 200MB 硬上限时，服务器在进入路由前直接返回纯文本 413，不遵循错误信封。

中间件与 handler 共用同一个错误体构造函数，该函数放在 `internal/api/errbody`，middleware 与 handler 都向它依赖，避免 middleware 反向依赖 handler。

### 1.4 POST /chat/completions

#### 1.4.1 请求

`Content-Type: application/json`。

```json
{
  "model": "qwen-max",
  "messages": [
    { "role": "system", "content": "你是一名严谨的财务助手。" },
    { "role": "user", "content": "帮我看下这张发票" }
  ],
  "stream": true,
  "stream_options": { "include_usage": true },
  "session_id": "ses_7f3a...",
  "agent_name": "analyst",
  "user": "u_10086"
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `model` | 否 | 模型逻辑名（`repo.Model.Name`）。为空时取默认对话模型；指定的模型不存在或已禁用报错，不回落 |
| `messages` | 是 | 消息数组，至少一条，最后一条必须是 `user` |
| `messages[].role` | 是 | `system`、`user`、`assistant` 三者之一 |
| `messages[].content` | 是 | 字符串，或内容片段数组（仅 `user` 支持数组） |
| `stream` | 否 | `true` 走 SSE 流式返回，缺省或 `false` 走一次性返回 |
| `stream_options.include_usage` | 否 | 为 `true` 时在流末尾下发 token 用量 chunk |
| `session_id` | 否 | 扩展字段。为空则新建会话；非空则在既有会话上继续 |
| `no_history` | 否 | 扩展字段。为 `true` 时本轮仍归入 `session_id` 指定的会话并正常留档，但不把该会话的历史作为上下文；缺省或 `false` 时按会话历史构建上下文。未传 `session_id` 时无实际影响 |
| `agent_name` | 否 | 扩展字段。指定执行本轮对话的子 Agent 名称；为空或等于主 Agent 名时走编排流程 |
| `user` | 否 | 终端用户标识，作为会话归属的 `user`。仅允许字母、数字、下划线、连字符，长度 1..64 |
| `n` | 否 | 仅支持 `1`，其它取值报 400 |
| `stream` 之外的采样参数 | 否 | `temperature`、`top_p`、`max_tokens`、`max_completion_tokens`、`stop`、`presence_penalty`、`frequency_penalty`、`seed`、`response_format`、`tools`、`tool_choice`、`logprobs`、`top_logprobs` 全部被接受但不生效，采样参数以模型记录中的配置为准 |

采样参数取模型记录的原因：同一模型在多处被复用时，采样参数属于部署配置而非单次请求的调整项，由模型记录统一约束可以避免同一租户的不同调用方对同一模型给出互相矛盾的采样设置。请求中携带这些字段不会报错，便于同一份客户端代码在 OpenAI 与 Groot 之间切换。

`messages` 中 `role` 为 `tool`、`function`、`developer` 的消息报 400 `invalid_request`：Groot 的工具调用由服务端 Agent 编排产生，不接受调用方注入工具结果。

#### 1.4.2 内容片段

`user` 消息的 `content` 为数组时，元素遵循 OpenAI 的 content part 格式：

| `type` | 载荷 | Groot 处理 |
|---|---|---|
| `text` | `{"type":"text","text":"..."}` | 直接作为文本 |
| `image_url` | `{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}` | 解出 MIME 与 Base64，作为图片片段交给模型 |
| `input_audio` | `{"type":"input_audio","input_audio":{"data":"<base64>","format":"wav"}}` | 解出 Base64 与格式，作为音频片段交给模型 |
| `file` | `{"type":"file","file":{"filename":"a.txt","file_data":"data:text/plain;base64,..."}}` | 服务端解码为文本，拼进该条 user 消息的文本部分 |
| `video_url` | — | 不支持，出现时报 400 `invalid_request` |

`image_url.url` 必须是 `data:<mime>;base64,<payload>` 形式。`image_url.detail` 被接受但忽略。

`input_audio.format` 仅接受 `wav` 与 `mp3`。

`file.file_data` 必须是 `data:<mime>;base64,<payload>` 形式。服务端按 UTF-8 解码后作为文本处理；`mime` 不是文本类（`text/*`、`application/json`、`application/xml`、`*+json`、`*+xml`）时报 400 `unsupported_file_type`，message 说明只接受文本类文件。

Groot 的附件校验按文件名校验扩展名，而 OpenAI 的内容片段不总带文件名。服务端在构造附件时按内容类型合成名称：

| 片段 | 合成名称 |
|---|---|
| `image_url` | 由 data URL 的 MIME 推扩展名，`image.png`、`image.jpg`、`image.webp`、`image.gif`、`image.bmp`；无法映射时 `image.png` |
| `input_audio` | `audio.wav` 或 `audio.mp3`，按 `format` |
| `file` | 用 `file.filename`；缺失时由 data URL 的 MIME 推扩展名，无法映射时 `file.txt` |

合成名称只用于通过既有的附件数量、体积、类型白名单校验，不出现在响应中。

#### 1.4.3 消息组装

一次对话请求最终交给 Agent 引擎的消息序列按以下顺序组装：

1. **系统指令**：由 Agent 引擎按既有规则生成，包含 GROOT.md（编排模式）或子 Agent 定义（Solo 模式）、会话级记忆、以及本次请求的系统提示。
2. **服务端上下文**：续接既有会话时，按 `memory.history_window` 与模型的 `max_context_tokens` 取出该会话的历史轮次。请求中 `no_history` 为 `true` 时这一步为空，模型只收到调用方提供的消息。
3. **调用方历史**：`messages` 中除 `system` 与最后一条 `user` 之外的消息，按原顺序追加。
4. **本次用户消息**：`messages` 的最后一条 `user`，其文本部分为 `instruction`，内容片段为多模态输入。

其中第 1 步的系统提示取自 `messages` 中 `role=system` 的全部消息，按出现顺序以空行连接。系统提示只作用于本次请求，不写入会话记忆，也不进入对话记录。

调用方自行拼接历史与使用服务端会话是两种并行的用法：两者同时使用时，拼接的历史接在服务端上下文之后，服务端上下文的截断策略不受调用方历史影响。

最后一条 `user` 消息只含内容片段、不含文本时，`instruction` 为空字符串，服务端不补充任何默认指令。

#### 1.4.4 会话

`session_id` 为空时，服务端新建会话：

1. 生成会话 ID，写入会话表，标题取本次用户消息文本的前若干个字符。
2. 会话归属为 `(tenant_id, user)`，其中 `tenant_id` 取自 API Key、`user` 取自请求体的 `user` 字段。
3. 轮次从 1 开始。

`session_id` 非空时：

1. 校验会话存在且归属于当前 `(tenant_id, user)`。不存在或不属于当前调用方时按「新建会话」处理，报错会暴露其它租户的会话是否存在，因此这里静默新建。
2. 轮次为既有轮次加 1，上下文按 1.4.3 第 2 步取出；`no_history` 为 `true` 时服务端上下文为空，轮次累加与对话留档不受影响。

Web Cookie 调用方视为管理员，不受 `(tenant_id, user)` 限制。

同一会话在上一轮尚未结束时再次提交，返回 409 `chat_limit_exceeded`。判断依据是运行态注册表中该会话是否已有活跃对话。

#### 1.4.5 非流式响应

`stream` 为 `false` 或缺省时，接口等本轮执行完毕后一次性返回：

```json
{
  "id": "cht_9b21...",
  "object": "chat.completion",
  "created": 1762056000,
  "model": "qwen-max",
  "session_id": "ses_7f3a...",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "这张发票的开票日期是 2026-10-21，金额 1280.00 元。"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 1024,
    "completion_tokens": 128,
    "total_tokens": 1152
  }
}
```

| 字段 | 说明 |
|---|---|
| `id` | 对话 ID，与流式 chunk 的 `id` 同源 |
| `object` | 固定为 `chat.completion` |
| `created` | 本轮开始的 Unix 秒时间戳 |
| `model` | 实际使用的模型逻辑名 |
| `session_id` | 扩展字段。本轮对应的会话 ID |
| `choices[0].message.content` | 模型输出的正文 |
| `choices[0].message.reasoning_content` | 扩展字段。模型的思考过程；模型未产出思考内容时不出现 |
| `choices[0].finish_reason` | 上游给出的结束原因，如 `stop`、`length`、`tool_calls` |
| `usage` | 全轮（主 Agent 与子 Agent 汇总）的 token 用量；无用量时不出现 |

Agent 执行失败时返回 200，`choices[0].finish_reason` 为 `"error"`，`choices[0].message.content` 为空字符串，错误详情通过扩展字段 `choices[0].message.error` 给出，结构为 `{"message":"..."}`。

非流式响应由本轮执行过程中产生的同一批事件聚合而来，聚合规则如下：

| 输出 | 来源 |
|---|---|
| `content` | 全部 `WriteMessage` 事件按到达顺序拼接，主 Agent 与子 Agent 的正文都计入 |
| `reasoning_content` | 全部 `WriteThinking` 事件按到达顺序拼接，模型未产出思考内容时不出现 |
| `finish_reason` | 最后一个非空的 `WriteFinish` 原因 |
| `usage` | 全轮（主 Agent 与子 Agent）汇总，与流式 `include_usage` chunk 同源 |
| `id`、`session_id`、`model` | 本轮执行的对话 ID、会话 ID、实际模型名 |

`content` 的取值口径与读流式 chunk 的方式一致：把同一次请求的 SSE `delta.content` 依次拼接，得到的就是非流式响应的 `content`。子 Agent 的正文在流式路径中以 `agent_name` 标注，在非流式路径中不加区分地拼入，两者的文本内容相同。同一个请求无论 `stream` 取何值，正文都一致。

`content` 同时与 `Task.Result`（写入会话记忆、由 `GET /chat/{sid}/{cid}` 返回的正文）使用同一口径，两者都来自同一次 `WriteMessage` 序列，不会出现同一轮对话在 `/chat/completions` 与会话详情里内容不一致的情况。

本轮未产出任何正文时，`content` 沿用既有的兜底文本「任务执行完成，但未获得明确结果」，与 `Task.Result` 保持一致。

调用方在本轮结束前断开时，按失败处理：返回 200，`finish_reason` 为 `"error"`，`message.error` 的 message 说明请求已取消。非流式路径没有中途下发的通道，与流式路径的取消语义（已下发的内容 + `[DONE]`）不同，这里以完整响应体给出结论。

#### 1.4.6 流式响应

`stream` 为 `true` 时，响应头为：

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

响应体为 OpenAI 的 chunk 序列，每个事件形如 `data: <json>\n\n`，以 `data: [DONE]\n\n` 结束。

```json
{
  "id": "cht_9b21...",
  "object": "chat.completion.chunk",
  "created": 1762056000,
  "model": "qwen-max",
  "session_id": "ses_7f3a...",
  "choices": [
    { "index": 0, "delta": { "role": "assistant", "content": "这张发票" }, "finish_reason": null }
  ],
  "usage": null
}
```

| 字段 | 说明 |
|---|---|
| `id` | 对话 ID，同一轮的所有 chunk 相同 |
| `object` | 固定为 `chat.completion.chunk` |
| `created` | 本轮开始的 Unix 秒时间戳 |
| `model` | 实际使用的模型逻辑名 |
| `session_id` | 扩展字段。每个 chunk 都带，值为本轮对应的会话 ID |
| `choices[].delta` | 增量内容，见下表 |
| `choices[].finish_reason` | 结束原因；未结束时为 `null` |
| `usage` | 仅在 `stream_options.include_usage` 为 `true` 时出现，见下 |

`delta` 的形态：

| 场景 | `delta` |
|---|---|
| 思考内容 | `{"role":"assistant","reasoning_content":"..."}` |
| 正文 | `{"role":"assistant","content":"..."}` |
| 工具调用 | `{"role":"assistant","tool_calls":[...]}` |
| 工具结果 | `{"role":"tool","tool_call_id":"...","content":"...","tool_name":"...","error":false}` |
| 结束 | `{}`，同时 `finish_reason` 为具体原因 |

来自子 Agent 的 chunk 在 `delta` 中额外带 `agent_name` 字段，为主 Agent 时不出现。

`tool_name` 与 `error` 是 Groot 的扩展字段：Groot 的 Agent 编排在服务端完成，工具调用与工具结果都通过流下发，便于界面展示执行过程。标准客户端读到不认识的 `delta` 字段会忽略。

`include_usage` 为 `true` 时，在 `finish_reason` chunk 之后、`[DONE]` 之前追加一个 `choices` 为空数组、`usage` 为具体用量的 chunk：

```json
{
  "id": "cht_9b21...",
  "object": "chat.completion.chunk",
  "created": 1762056000,
  "model": "qwen-max",
  "session_id": "ses_7f3a...",
  "choices": [],
  "usage": { "prompt_tokens": 1024, "completion_tokens": 128, "total_tokens": 1152 }
}
```

未开启时该 chunk 不下发，其余 chunk 的 `usage` 为 `null`。

执行失败时，在流内下发一个错误事件，随后是 `[DONE]`：

```json
{
  "error": {
    "message": "Agent 执行异常: context deadline exceeded",
    "type": "api_error",
    "param": null,
    "code": null
  }
}
```

`chat_id` 的获取方式：流式响应取任意 chunk 顶层的 `id`，非流式响应取响应体的 `id`。两者都不出现在响应头中。

#### 1.4.7 校验顺序

1. 请求体不是合法 JSON → 400 `invalid_request`。
2. `messages` 为空或缺失 → 400 `invalid_request`。
3. `messages` 中出现不支持的 `role` → 400 `invalid_request`，`param` 为 `messages`。
4. 最后一条消息不是 `user` → 400 `invalid_request`。
5. `n` 不为 0 或 1 → 400 `invalid_request`，`param` 为 `n`。
6. `stream_options.include_usage` 出现在 `stream` 为 `false` 的请求中时忽略，不报错。
7. 内容片段逐条解析：`type` 不认识 → 400 `invalid_request`；`data:` URL 不是合法 Base64 → 400 `invalid_request`（细分原因只出现在 `message` 里）；`input_audio.format` 不是 `wav`/`mp3` → 400 `invalid_request`；`file` 的内容不是文本类 → 400 `unsupported_file_type`。
8. 附件配置读取失败 → 500 `internal_error`；附件数量、单文件体积、总体积、类型白名单校验不通过 → 附件模块内部错误码统一映射为对外公开码：数量超限、文件名/内容缺失、Base64 解码失败 → 400 `invalid_request`；类型不在白名单 → 400 `unsupported_type`；单文件或总体积超限 → 413 `file_too_large`。细分原因只出现在 `message` 里。
9. 模型解析：`models.GetByName(model)`。模型不存在、已禁用、未配置默认对话模型 → 400 `model_not_found`。
10. `agent_name` 非空且不是已注册的子 Agent → 400 `invalid_request`，`code` 为 `unknown_agent`。
11. 会话归属检查：Web 调用方通过；API Key 调用方缺少租户 → 403 `tenant_not_bound`；`user` 缺失或非法 → 400 `invalid_request`，`param` 为 `user`。
12. 会话忙 → 409 `chat_limit_exceeded`。

校验顺序保证「请求格式 → 内容片段 → 模型 → Agent → 归属 → 并发」逐层推进，一次只报一类问题。

### 1.5 GET /models

返回 Groot 中配置的全部启用模型，采用 OpenAI 的列表格式：

```json
{
  "object": "list",
  "data": [
    {
      "id": "qwen-max",
      "object": "model",
      "created": 1762000000,
      "owned_by": "groot"
    }
  ]
}
```

| 字段 | 说明 |
|---|---|
| `object` | 固定为 `list` |
| `data[].id` | 模型逻辑名，即 `/chat/completions` 的 `model` 取值 |
| `data[].object` | 固定为 `model` |
| `data[].created` | 模型记录的创建时间，Unix 秒 |
| `data[].owned_by` | 固定为 `groot` |

只列启用的模型：被禁用的模型不能用于对话，列出来只会诱导调用失败。A、P、K 等模型配置细节不在此接口暴露，界面改由 `/web/models` 读取。

### 1.6 POST /audio/transcriptions

`Content-Type: multipart/form-data`。

| 字段 | 必填 | 说明 |
|---|---|---|
| `file` | 是 | 音频文件 |
| `model` | 否 | 模型逻辑名。为空时取默认 ASR 模型 |
| `language` | 否 | 语种，如 `zh`。为空时由上游判断 |
| `prompt` | 否 | 提示文本，透传给上游 |
| `response_format` | 否 | `json`、`text`、`srt`、`verbose_json`、`vtt`，缺省 `json` |
| `temperature` | 否 | 透传给上游 |
| `timestamp_granularities[]` | 否 | 透传给上游 |

成功时原样转发上游的响应：状态码 200、上游的 `Content-Type` 与响应体字节。上游返回 `json` 时响应体是 `{"text":"..."}`，返回 `text` 时是纯文本，返回 `srt`/`vtt` 时是字幕文本，返回 `verbose_json` 时是带分段与时间戳的完整对象。Groot 不解析、不改写，也不在响应中附加字段。

上游返回非 2xx 且响应体是 OpenAI 的 JSON 错误信封时，原样转发上游的状态码、`Content-Type` 与响应体；其它情况（如网关的 HTML 错误页、连接失败、超时）返回 502 `upstream_error`。

文件大小上限取自附件配置的 `max_size`，超限返回 413 `file_too_large`。扩展名不在支持集合（`.webm`、`.mp3`、`.mp4`、`.mpeg`、`.mpga`、`.m4a`、`.wav`、`.ogg`、`.flac`）时报 400 `invalid_request`，`code` 为 `unsupported_type`。

### 1.7 POST /audio/speech

`Content-Type: application/json`。

```json
{
  "model": "tts-1",
  "input": "你好，欢迎使用 Groot。",
  "voice": "alloy",
  "response_format": "mp3",
  "speed": 1.0,
  "instructions": "用温和的语气朗读",
  "language": "zh"
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `input` | 是 | 待合成的文本，去除首尾空白后不得为空，长度上限 4096 字符 |
| `voice` | 是 | 音色，长度上限 64 字符 |
| `model` | 否 | 模型逻辑名。为空时取默认 TTS 模型 |
| `response_format` | 否 | `mp3`、`opus`、`aac`、`flac`、`wav`、`pcm`，缺省 `mp3` |
| `speed` | 否 | 接受并透传给上游 |
| `instructions` | 否 | 风格指令，长度上限 500 字符 |
| `language` | 否 | 合成语种，长度上限 16 字符 |

成功时返回音频字节流，状态码 200，`Content-Type` 取上游给出的值；上游未给出时按 `response_format` 推断（`opus`→`audio/ogg`、`aac`→`audio/aac`、`flac`→`audio/flac`、`wav`→`audio/wav`、`pcm`→`audio/pcm`、其它→`audio/mpeg`）。响应不带任何自定义头。

上游返回非 2xx 时按 1.6 中的同一规则处理：OpenAI 错误信封原样转发，其它情况返回 502 `upstream_error`。

`voice` 必填：Groot 不再为对外接口提供默认音色。Web 界面的朗读功能从「语音设置」中读出音色，填写到请求的 `voice` 字段后再调用本接口；「语音设置」中的音色与指令只作用于 Web 界面的朗读。

### 1.8 POST /image/understanding

`Content-Type: application/json`。

```json
{
  "model": "qwen-vl-max",
  "instruction": "把这张发票转成 JSON，字段：金额、日期、开票方",
  "attachments": [
    { "type": "image", "name": "invoice.png", "content": "<base64>" }
  ]
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `attachments` | 是 | 图片附件数组，长度恰好为 1 |
| `attachments[].type` | 是 | 固定为 `"image"` |
| `attachments[].name` | 是 | 文件名，扩展名用于图片类型兜底判定 |
| `attachments[].content` | 是 | 图片的纯 Base64 内容，不带 `data:` 前缀 |
| `instruction` | 否 | 解析指令，作为 user 消息的文本部分；为空时使用内置默认指令 |
| `model` | 否 | 模型逻辑名。为空时取默认视觉模型 |

本接口在 OpenAI 中没有对应路由，因此保留 Groot 的请求结构，只把模型名从请求头移入请求体的 `model` 字段，与 OpenAI 的参数命名一致。

成功返回 200：

```json
{
  "text": "金额：1280.00 元；日期：2026-10-21；开票方：某某科技有限公司",
  "model": "qwen-vl-max",
  "usage": { "prompt_tokens": 1024, "completion_tokens": 48, "total_tokens": 1072 }
}
```

错误处理沿用既有逻辑：附件类型、数量、体积、白名单、Base64、图片类型、模型解析的失败按 1.3.4 中对应的 `code` 返回；调用方放弃请求返回 499 `client_closed_request`；上游错误返回 502 `upstream_error`。

### 1.9 会话与任务类接口

这些接口在 OpenAI 中没有对应路由，路径与响应结构保持 Groot 既有形态；鉴权方式与参数命名对齐 OpenAI：凭证走 `Authorization: Bearer`，终端用户标识统一叫 `user`，失败响应为 1.3.4 的错误体。

`user` 以请求头传递（见 1.3.3）：

```
GET /sess/history?limit=20&offset=0
user: u_10086

GET /sess/search?q=发票
user: u_10086

DELETE /sess/7f3a...
user: u_10086

GET /chat/status/7f3a...
user: u_10086
```

`PATCH /sess/{sid}` 与 `POST /chat/{sid}/{cid}/stop` 同样使用请求头 `user`：前者有请求体（`title`），后者没有，统一走请求头可以避免同一标识在不同接口里换位置。

`GET /sess/history` 与 `GET /sess/search` 保持既有的分页、检索参数与响应结构。

`/sess/search` 在 `q` 为空时返回空结果，此时不校验 `user`。

`/schedule/*` 不关联会话归属，不要求 `user`。

### 1.10 Web 界面接口

Web 界面端点保持 Cookie 会话鉴权，路径与用途不变：

| 路由 | 说明 |
|---|---|
| `/web/health` | 健康检查 |
| `/web/login`、`/web/logout`、`/web/me`、`/web/setup` | 登录、登出、当前用户、初始化 |
| `/web/models` 系列 | 模型配置管理 |
| `/web/settings/voice`、`/web/settings/speech`、`/web/settings/runtime` | 运行时设置读写 |
| `/web/settings/auth` | 读取 JWT 签名密钥的掩码与是否已设置 |
| `/web/settings/auth/secret` | 重新生成 JWT 签名密钥 |
| `/web/audio/transcriptions`、`/web/audio/speech` | 聊天页的语音输入与朗读，与对外接口共用 handler |
| `/web/files/*`、`/web/sync/*`、`/web/apikeys`、`/web/cluster`、`/web/logs` | 文件、同步、密钥、集群、日志 |

聊天页调用对话、语音、图片解析时，请求与响应的格式与对外接口完全一致，区别只在于凭证是 Cookie 而非 API Key。Web 前端以 OpenAI 的请求形状发起调用，从而在每次使用时验证兼容性。

朗读的 `voice` 取自「语音设置」。设置为空时界面提示先填写音色，不发出请求——服务端已不为对外接口提供默认音色。转录的 `response_format` 固定为 `json`，界面按 `{"text": "..."}` 解析响应体。

「认证设置」界面只保留 JWT 签名密钥的展示与重新生成，请求头名的读写接口与其数据字段一并移除。

### 1.11 实现结构

| 位置 | 职责 |
|---|---|
| `internal/api/router.go` | 路由注册：`/chat/completions`、`/models`、`/audio/*`、`/image/understanding`、`/sess/*`、`/chat/*`、`/schedule/*` |
| `internal/api/handler/chat.go` | 对话补全：解析请求、组装任务、流式与非流式两条输出路径 |
| `internal/api/handler/chat_req.go` | 请求解析：`messages` 与内容片段到 `agent.Task` 的转换 |
| `internal/api/errbody/errbody.go` | OpenAI 错误体构造与写出，handler 与 middleware 共用 |
| `internal/api/handler/models_public.go` | `GET /models` 的列表转换 |
| `internal/api/handler/transcription.go` | 转录透传 |
| `internal/api/handler/speech.go` | 语音合成 |
| `internal/api/handler/owner.go` | 会话归属前置检查，`user` 统一从请求头读取 |
| `internal/api/middleware/auth.go` | Bearer 令牌解析、权限映射 |
| `internal/api/middleware/ratelimit.go` | 限流与 OpenAI 错误体；长连接分支的路径改为 `/chat/completions` |
| `internal/api/middleware/identity.go` | 终端用户标识读取 |
| `internal/agent/sse.go` | chunk 构造：顶层 `session_id`、`usage` 按需下发 |
| `internal/agent/aggregator.go` | 非流式输出：把事件聚合为 `chat.completion` |
| `internal/llm/transcription.go` | 上游响应原样返回 |
| `internal/llm/speech.go` | 上游响应原样返回 |
| `internal/agent/engine.go` | 消息组装：调用方历史接在服务端上下文之后 |

流式与非流式共用同一条执行路径：`agent.Executor.Execute` 接收一个事件写出接口，`SSEWriter` 把它变成 SSE chunk，新增的 `Aggregator` 把它变成一次性响应。两者实现同一接口，对话 handler 按 `stream` 选择。

### 1.12 使用手册与示例

根目录 `README.md` 是全量重写的范围：

| 章节 | 内容 |
|---|---|
| 1.2 核心特性 | 模型与 Agent 的引用方式改为请求体字段 |
| 1.3 会话与对话 | 会话 ID 的获取方式由响应头改为响应体 |
| 3.5 第一次调用 | 示例改为 `Authorization: Bearer` + `model` 字段 |
| 4.2 完整配置文件示例 | 移除 `security.auth.header_name` |
| 4.3 配置字段详解 | 移除请求头名条目 |
| 4.5 权限说明 | `user` 的传递方式 |
| 5.3.2、5.3.3、5.3.8 | Agent 与模型的引用方式、完整示例 |
| 7.1 API 列表 | 全量重写 |
| 7.2 认证方式 | Bearer |
| 7.3 POST /chat/completions | 请求、响应、流式事件、扩字段 `session_id`/`agent_name` |
| 7.x GET /models | 新增一节 |
| 7.x POST /audio/transcriptions | 透传语义、各 `response_format` 的响应形态 |
| 7.x POST /audio/speech | `voice` 必填 |
| 7.x POST /image/understanding | `model` 移入请求体 |
| 7.x 会话与任务类 | `user` 请求头 |
| 附录 D 错误码速查表 | 按 1.3.4 重写 |
| 第十章 FAQ | 401 的排查步骤、429 的触发条件 |

`examples/python/` 与 `examples/java/` 的示例客户端改为直接使用 OpenAI 官方 SDK，只演示 `base_url` 与 `api_key` 的替换，以及扩展字段 `session_id`、`agent_name` 的用法。

## 二、迭代说明

### 2.1 与上一版差异

**路由**

- 移除 `POST /chat`，由 `POST /chat/completions` 取代，旧路径不做兼容映射。
- 新增 `GET /models`（对外），返回 OpenAI 列表格式。原 `/web/models` 的配置管理接口不变。
- `/audio/transcriptions`、`/audio/speech`、`/image/understanding`、`/sess/*`、`/schedule/*`、`/chat/status/*` 路径不变。

**认证**

- 凭证从 `X-API-Key` 改为 `Authorization: Bearer`。
- 移除配置项 `security.auth.header_name`（`internal/config/config.go`、`SecurityConfig`）。配置加载使用非严格 `yaml.Unmarshal`，旧配置中残留该键不会导致启动失败。
- 移除 `internal/setting` 中的请求头名读写：`DefaultAuthHeaderName`、`SetAuthHeaderName` 及其校验、`migrate_legacy` 中的迁移分支、`AuthSettingsPayload.HeaderName`/`HeaderNameDefault`、`PutAuthSettingsRequest`、`PUT /web/settings/auth` 路由与方法、`types.PutAuthSettingsRequest`。
- 保留 `GET /web/settings/auth`（只读取密钥的掩码与是否已设置）与 `POST /web/settings/auth/secret`，其响应体均为 `{"secret_masked": "...", "secret_set": true}`。
- Web 界面移除「认证请求头名」设置项与 `web/src/api/authSettings.ts` 中对应的读写，i18n 中 `authHeaderName*` 相关文案移除。

**参数**

- `X-Model-Name` 请求头 → 请求体 `model`（`/chat/completions`、`/image/understanding`、`/audio/speech`；`/audio/transcriptions` 维持 `model` 表单字段）。
- `X-Session-ID` 请求头 → 请求体 `session_id`（扩展字段）。
- 新增扩展字段 `no_history`：为 `true` 时本轮只把会话用于归组与留档，不把会话历史作为上下文交给模型；缺省时行为不变。
- `X-Agent-Name` 请求头 → 请求体 `agent_name`（扩展字段）。
- `X-User-ID` 请求头 → `user`：`/chat/completions` 用请求体 `user`，无请求体的接口用请求头 `user`。
- `/chat` 的 `instruction`、`prompt`、`attachments` 由 `messages` 与 content parts 表达：
  - `instruction` → `messages` 最后一条 `user` 的文本部分；
  - `prompt` → `messages` 中的 `role=system` 消息；
  - `attachments` → `image_url`、`input_audio`、`file`、`text` 内容片段；
  - `video` 附件不再支持。
- `stream` 从「接受但不生效」变为真实支持：`false` 返回 `chat.completion`，`true` 返回 SSE。

**响应**

- 移除响应头 `X-Session-ID` 与 `X-Chat-ID`。`session_id` 出现在非流式响应体顶层与每个流式 chunk 顶层；`chat_id` 即响应体的 `id` 与 chunk 的 `id`。
- `/audio/transcriptions` 移除 `model` 回显，改为原样透传上游响应体与 `Content-Type`。
- `/audio/speech` 移除响应头 `X-Model-Name`。
- 流式 chunk 的 `usage` 在未开启 `stream_options.include_usage` 时为 `null`，此前每个 chunk 都带 `"usage":null` 且末尾无条件下发用量 chunk。
- 全部错误响应改为 OpenAI 信封（`status`/`message` 两字段的结构不再出现），包括限流中间件的 429、鉴权中间件的 401/403、`/sess/*` 与 `/schedule/*` 的错误，以及 `/web/*` 下的界面专用端点。`middleware/websession.go` 的未登录 401 与 `middleware/auth.go` 的 401 合并为同一构造。

**逻辑**

- 引擎的消息组装增加一段：调用方 `messages` 中的非系统、非末条消息接在服务端上下文之后。
- `agent.Task` 增加承载调用方历史消息与系统提示的字段；对话记录中的 `Prompt` 列保持为空。
- `/audio/speech` 的 `voice` 变为必填。

**Web 前端**

- `web/src/api/sse.ts`：`POST /chat/completions`，请求体改为 `{model, messages, stream:true, session_id, agent_name}`；不再读响应头，会话 ID 从 chunk 顶层的 `session_id` 取；错误解析改为 `data.error.message`。
- `web/src/api/sseChunk.ts`：`flattenChunk` 透出顶层 `session_id`。
- `web/src/api/speech.ts`：错误解析改为 `data?.error?.message`。
- `web/src/api/voice.ts`：模型名从请求头移入表单字段 `model`。
- `web/src/components/chat/ChatInput.vue`：上传的附件构造为 OpenAI 内容片段。
- `web/src/components/settings/SettingsModal.vue`：移除认证请求头名设置。

**示例与测试**

- `examples/python/groot_client.py`、`examples/java/.../GrootClient.java` 改为基于 OpenAI 官方 SDK。
- 既有 Go 单元测试中引用旧请求头与旧错误体的断言同步更新；`tests/python/conftest.py` 的 `api_headers` 改为 `Authorization`，`security.auth.header_name` 相关 fixture 移除。

### 2.2 方案取舍

讨论阶段比较了三种落地方式：

1. **双轨并行**：保留 `/chat` 等旧路径，同时新增 OpenAI 形状的新路径，两套 handler 并存。优点是零破坏，缺点是两套路径要长期同时维护，参数与错误体分叉后行为会逐渐漂移，且用户手册要写两遍。
2. **路径兼容层**：只加 `/v1` 形状的新路径，旧路径内部转发到新 handler。优点是破坏面小，缺点是 Groot 内部多一层无实际价值的转发，旧路径的语义（`instruction`/`attachments`）与 OpenAI 语义并不等价，转发时要写一堆适配。
3. **整体切换**：对外路径、参数、响应、鉴权一次性切换到 OpenAI 形态，旧路径直接移除。优点是只有一套 API、一套文档、一套示例；缺点是不保留旧接口，已有调用方必须改。

用户选择第 3 种。Groot 处于对外接口尚未大规模铺开的阶段，一次性切换的成本低于长期维护两套接口。

`/v1` 前缀经过两轮确认后决定不加：OpenAI SDK 对 `base_url` 原样使用，是否带 `/v1` 由部署方的反向代理决定，服务端不应假设。

`messages` 的拼接由调用方决定，服务端只负责把调用方传来的历史接在服务端上下文之后，不做去重、不做合并、不做「`session_id` 与 `messages` 二选一」的互斥校验。

### 2.3 我的判断

以下几处对话中没有逐条确认，由我按上下文决定，实现时可直接推翻：

| 位置 | 判断 | 理由 |
|---|---|---|
| `GET /models` 权限点 | `chat` | 模型列表是发起对话的前置信息，只给 `status` 或 `detail` 权限的 Key 没有列模型的必要 |
| `/models` 是否列禁用模型 | 只列启用 | 列出的模型必须能用于对话，否则等于诱导调用失败 |
| 采样参数 | 接受但忽略 | 采样参数属于模型记录中的部署配置，与 `internal/llm/chatmodel.go` 注释里「只透传 temperature、max_completion_tokens、thinking」的口径一致 |
| `n` 的取值 | 只支持 1，其它报 400 | 静默忽略会给出与请求不符的返回条数，宁可报错 |
| `messages` 中的 `tool`/`function`/`developer` | 报 400 | Groot 的工具编排在服务端完成，调用方注入的工具结果无法与该轮编排对齐 |
| 系统提示是否落库 | 不落库 | 系统提示是单次请求的临时约束，落库会让后续轮次被它继续影响 |
| 系统提示多条时的连接方式 | 空行连接 | 与 `internal/agent/engine.go` 中系统指令各段以 `\n\n` 分隔的既有写法一致 |
| 附件合成名称 | 按 MIME/format 推扩展名 | `attachment.Validate` 要求名称非空且按扩展名判白名单，OpenAI 片段不总带文件名 |
| `file` 片段的文本判定 | 只接受文本类 MIME | 二进制文件按文本解码会产出大量无效字符，明确报 `unsupported_file_type` 比静默塞进上下文更负责 |
| 上游错误透传 | JSON 错误信封原样转发，其它包装为 502 | 上游是 OpenAI 兼容服务，其错误本来就是标准信封；网关的 HTML 页转发给 SDK 只会解析失败 |
| `/audio/transcriptions` 的空文本 | 不再报 `ErrEmptyTranscript` | 透传语义下上游返回什么就是什么，空 `text` 是上游的判断 |
| 非流式执行失败 | 200 + `finish_reason: "error"` + `message.error` | 与流式路径的「流内含错误事件」对称；HTTP 层已经完整返回，用 5xx 会让 SDK 抛异常而丢失会话 ID |
| `/image/understanding` 的响应 | 保留 `text`/`model`/`usage` | 该路由在 OpenAI 中没有对应项，按「OpenAI 没有的接口保持 Groot 原接口」处理 |
| `/image/understanding` 的 `model` 位置 | 请求体 | 虽然接口本身保留，但参数名与位置对齐 OpenAI 的 `model` |
| `/schedule/*` 的错误体 | 改为 OpenAI 信封 | 用户要求这些接口「验证方式和参数名要和 OpenAI 保持一致」，错误体属于验/返回方式 |
| Web 端「认证请求头名」 | 整块移除 | Bearer 是唯一定式，没有可配置的请求头名 |
| 错误信封的适用范围 | 全部接口，含 `/web/*` 与中间件 | 两套错误体意味着前端与客户端都要写两套解析；`/web/*` 只改变错误体，不影响其 Cookie 鉴权与路径 |
| 错误体的放置位置 | `internal/api/errbody` | handler 与 middleware 都要用；放在 handler 下会让 middleware 反向依赖 handler |
| `user` 的传递位置 | 请求头 `user`；`/chat/completions` 用请求体 | 无请求体的接口（`GET`/`DELETE`）只能走请求头；`/chat/completions` 是 OpenAI 标准接口，`user` 本就属于请求体 |
| `PATCH /sess/{sid}`、`POST /chat/{sid}/{cid}/stop` 的 `user` | 请求头 | 与其它会话类接口一致，不因是否有请求体而换位置 |
| 非流式 `content` 的聚合范围 | 主 Agent 与子 Agent 的正文全部拼接 | 与流式路径的 `delta.content` 拼接结果一致；若只取主 Agent，同一个请求换 `stream` 会得到不同正文 |
| 非流式无正文时的兜底 | 沿用「任务执行完成，但未获得明确结果」 | 该文本同时写入 `Task.Result`，两条路径必须同源；单独改聚合器会让响应与对话记录分叉 |
| Web 朗读音色为空 | 界面提示先填写，不发请求 | 服务端不再为对外接口提供默认音色；服务端按入口区分默认值是隐藏的第二套规则 |
| Web 转录的 `response_format` | 固定 `json` | 界面按 `{"text": "..."}` 解析，透传语义下必须显式要求 JSON |
