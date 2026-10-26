# 图片解析（Image Understanding）设计文档

## 一、功能设计

### 1.1 功能概述

Groot 提供图片解析能力：调用方提交一张图片和一段可选的指令，Groot 调用 OpenAI 兼容的视觉模型，把模型返回的文本原样交给调用方。接口无状态，与会话、对话记录无关，与音频转录接口对称——图片进、文字出，不产生会话 ID，不写历史。

图片解析服务于「转换」类用法：客户程序需要从图片里取出内容（提取文字、转成结构化数据、描述图像），但不希望为此走进对话流程。调用方拿到文本后自行决定用途，包括把它作为 `instruction` 提交给 `/chat`。

解决的问题：

- 对外提供无状态的图片转文字接口，调用方用同一套模型管理与鉴权完成图片内容的提取。
- 复用 `/chat` 的附件结构与参数命名，客户端已有的图片构造与调用代码可以直接套用。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 解析接口 | `POST /image/understanding`，图片进、文本出 |
| 指令驱动 | 可选 `instruction` 描述解析要求（提取文字、转 JSON、按字段抽取等）；省略时使用内置默认指令 |
| 模型取用 | `X-Model-Name` 请求头 → 默认视觉模型；显式指定的模型不存在或已禁用直接报错，不回落 |
| 原样返回 | 响应 `text` 为模型输出的原文，Groot 不解析、不改写、不强制结构化 |
| 用量回传 | 上游返回 token 用量时随响应给出 |
| 单图约束 | 每次请求恰好一张图片；`attachments` 为数组形状，为以后多图预留 |

### 1.3 接口定义

#### 1.3.1 路由与鉴权

| 路由 | 鉴权 | 说明 |
|---|---|---|
| `POST /image/understanding` | API Key，`chat` 权限，计入限流 | 对外接口 |

与 `/audio/transcriptions`、`/audio/speech` 同在一个路由组，权限判定在 `internal/api/middleware/auth.go` 的 `getRequiredPermission` 中与之并列：图片解析是聊天输入的一种形式，权限点取 `chat`。`ValidPermissions` 与 Web 端 `permissionOptions` 的取值集合保持不变。

本接口与会话无关，不要求 `X-User-ID`，与 `/audio/*`、`/schedule/*` 一致。

只注册对外路由，不注册 `/web/image/understanding`：当前没有界面会调用它。以后需要时按音频接口的既有模式增加一行注册、共用同一个 `Serve` 方法即可。

#### 1.3.2 请求

`Content-Type: application/json`，结构与 `/chat` 的 `ChatRequest` 同形：

```json
{
  "instruction": "把这张发票转成 JSON，字段：金额、日期、开票方",
  "attachments": [
    { "type": "image", "name": "invoice.png", "content": "<base64>" }
  ]
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `instruction` | 否 | 解析指令，语义与 `/chat` 的 `instruction` 一致：作为 user 消息的文本部分。去除首尾空白后为空时使用默认指令 |
| `attachments` | 是 | 图片附件数组，长度必须恰好为 1 |
| `attachments[].type` | 是 | 必须为 `"image"` |
| `attachments[].name` | 是 | 文件名，扩展名用于判定图片类型（文件头嗅探失败时兜底） |
| `attachments[].content` | 是 | 图片的纯 Base64 内容，不带 `data:` 前缀，与 `/chat` 附件约定一致 |

模型名走请求头 `X-Model-Name`，与 `/chat` 一致；本接口不设 JSON `model` 字段（`/audio/transcriptions` 的 `model` 表单字段是其自身历史上的传参方式，不向新接口扩散）。

`instruction` 不设独立的长度上限：`/chat` 的 `instruction` 同样没有长度校验，两处口径保持一致。

#### 1.3.3 校验顺序

1. 请求体不是合法 JSON → 400 `invalid_request`。
2. `attachments` 长度不等于 1 → 400 `invalid_request`，message 说明「必须提供 1 张图片附件」。
3. `attachments[0].type != "image"` → 400 `attachment_invalid_type`（复用 `/chat` 的错误码）。这一步必须早于第 4 步：`attachment.Handler` 的类型校验接受 `file`/`image`/`audio`/`video` 四种，`audio`、`video` 能通过它，所以「只收图片」只能由本接口自己判。
4. 附件配置读取与校验：`h.settings.Attachment(ctx)` 取当前附件限额（与 `/audio/transcriptions` 同一读取方式），读取失败 → 500 `error`；随后 `attachment.NewHandler(cfg).Validate(...)`，沿用 `/chat` 的数量、单文件体积、总量、类型白名单校验与全部 `attachment_*` 错误码。体积按 `len(content)*3/4` 估算，`MaxSize` 单位为 MB，与 `/chat` 完全一致。
5. Base64 与图片类型校验（单次流式解码，不保留解码后的图片字节）：

   - 用 `base64.NewDecoder` 包住 `content` 逐段解码；先读解码后的前 512 字节供嗅探，再 `io.Copy(io.Discard, ...)` 走完剩余部分完成格式校验，解码失败 → 400 `attachment_decode_error`。
   - 合法性口径与 `/chat` 一致：`base64.NewDecoder` 与 `/chat` 用的 `base64.StdEncoding.DecodeString` 都会忽略 `\r`、`\n`（base64 命令行按 76 列换行产出的内容两者都接受），都会因空格等真正非法字符报错。
   - 嵌入 data URL 前剔除 `\r`、`\n`：JSON 字符串不允许裸控制字符（U+0000–U+001F），把这串 Base64 直接拼进请求体只会得到非法报文；剔除不改变解码结果，只在确实存在换行时才新建字符串。除这两类换行外不做任何归一化——空格等非法字符一律按 `attachment_decode_error` 报错，不被清洗掉。
   - 前 512 字节交给 `http.DetectContentType`：结果为 `image/*` 时以其为准，包含 `image/x-icon`（ICO 文件）——判定依据是「Go 认得这是图片」，而不是白名单，避免误拒合法图片。
   - 嗅探结果为 `application/octet-stream`（无法识别）时，按 `name` 扩展名映射兜底：`.png` → `image/png`、`.jpg`/`.jpeg` → `image/jpeg`、`.webp` → `image/webp`、`.gif` → `image/gif`、`.bmp` → `image/bmp`。
   - 其余情况（嗅探出明确的非图片类型，如 `text/plain`、`application/pdf`、`text/xml`，或兜底映射也没有结果）→ 400 `unsupported_type`，message 列出兜底支持的扩展名。扩展名兜底只服务于「嗅探不出类型」，不放行「嗅探出是别的东西」——把文本改名为 `invoice.png` 仍然被拒。

6. 模型解析：`models.Resolve(ctx, X-Model-Name, repo.DefaultVision)`。模型不存在、已禁用、未配置默认视觉模型统一映射为 400 `invalid_model`，错误文案取自 `llm` 包既有定义。

校验顺序保证「请求格式 → 附件 → 图片类型 → 模型」逐层推进，一次只报一类问题。

#### 1.3.4 响应

成功返回 200：

```json
{
  "text": "金额：1280.00 元；日期：2026-10-21；开票方：某某科技有限公司",
  "model": "qwen-vl-max",
  "usage": { "prompt_tokens": 1024, "completion_tokens": 48, "total_tokens": 1072 }
}
```

| 字段 | 说明 |
|---|---|
| `text` | 模型输出的原文，原样返回；Groot 不剥离 `think` 标签、不做结构校验 |
| `model` | 实际使用的模型名（逻辑名，即 `repo.Model.Name`） |
| `usage` | 上游返回了 token 用量时给出，字段名与 `/chat` SSE 的 usage chunk 一致；上游未返回时省略该字段。三个字段一律透传上游值：上游没给 `total_tokens` 时按 `prompt_tokens + completion_tokens` 补齐，保证这项在出现时总是自洽 |

非流式：整个响应一次性返回，与转录接口一致。

#### 1.3.5 错误码

| HTTP | status | 场景 |
|---|---|---|
| 400 | `invalid_request` | 请求体非法、`attachments` 数量不为 1 |
| 400 | `attachment_invalid_type` | 附件 `type` 不是 `image` |
| 400 | `attachment_count_exceeded` / `attachment_size_exceeded` / `attachment_total_size_exceeded` / `attachment_type_not_allowed` / `attachment_missing_name` / `attachment_missing_content` | 附件校验失败，文案与 `/chat` 相同 |
| 400 | `attachment_decode_error` | `content` 不是合法 Base64 |
| 400 | `unsupported_type` | 嗅探出明确的非图片类型，或嗅探不出且扩展名映射不到图片类型 |
| 400 | `invalid_model` | 模型不存在、已禁用或未配置默认视觉模型 |
| 500 | `error` | 内部错误（读附件配置失败等） |
| 502 | `upstream_error` | 上游返回 4xx/5xx、网络错误、响应不是合法 JSON、`choices[0].message.content` 为空。message 透传上游原文。调用方中断请求不属于此类 |
| 499 | `cancelled` | 调用方在拿到响应前放弃请求（上下文被取消）。message 为上下文错误原文 |

「请求被放弃」与「上游故障」分成两个状态：前者是没人等结果了，后者是上游坏了。两者都让请求取不到文本，但处置方式相反——前者不必报警、不必重试，后者需要查上游。合并成一个 502 `upstream_error` 会让日志与响应都把「请求被放弃」读成「上游坏了」，所以 499 单独成一类，日志也记为「图片解析已中断」（Info）而非「图片解析失败」（Warn）。

判定依据是「返回的错误本身就是该上下文的错误」（`ctx.Err()` 非空且 `errors.Is(err, ctx.Err())`）。只看 `ctx.Err()` 非空不够：上游真出错时恰好赶上取消，会被误记成「请求被放弃」，把需要排查的故障悄悄降级成正常事件。

空内容判为 `upstream_error` 而非 200 空串：转录接口同样把「上游没给出有效结果」视为失败（`ErrEmptyTranscript`），两处语义一致，调用方不会把「模型没答」误当成「图里什么都没有」。

日志只在失败时记一条（`Warn`，带模型名与错误），调用方取消时降为 `Info`；不记逐请求日志，也不记 token 用量——`usage` 已经随成功响应交给调用方（`/chat` 的用量则落库），日志里再抄一份没有读者。

### 1.4 上游调用

`internal/llm/image.go` 承载图片解析的上游调用：

```go
// ImageRequest 图片解析参数。
type ImageRequest struct {
    Instruction string // 已由 handler 填充默认指令，非空
    MIMEType    string // handler 嗅探或按扩展名判定的图片类型，如 image/png
    Base64      string // 附件原文，纯 Base64，不含 data: 前缀，不解码
}

// Usage 上游返回的 token 用量。
type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}

// UnderstandImage 请求 {base_url}/chat/completions，返回模型输出的文本与用量。
// 上游返回 >= 400、响应非法、内容为空时返回包装了 ErrUpstream 的错误。
func UnderstandImage(ctx context.Context, m *repo.Model, req ImageRequest) (string, *Usage, error)
```

- 端点：`openAIBaseURL(m.BaseURL) + "/chat/completions"`，`/v1` 补齐规则与对话、转录、合成一致。
- 请求体：把 JSON 拆成「前缀 + Base64 原文 + 后缀」三段，用 `io.MultiReader` 拼成请求体并显式设置 `Content-Length`：

  ```
  前缀  {"model":"<m.Model>","messages":[{"role":"user","content":[{"type":"text","text":<instruction>},{"type":"image_url","image_url":{"url":"data:<MIMEType>;base64,
  中段  req.Base64 原文
  后缀  "}}]}]}                      （m.MaxCompletionTokens > 0 时在末尾补 ,"max_completion_tokens":N）
  ```

  完整形态即：

  ```json
  {
    "model": "<m.Model>",
    "messages": [{
      "role": "user",
      "content": [
        { "type": "text", "text": "<instruction>" },
        { "type": "image_url", "image_url": { "url": "data:<MIMEType>;base64,<req.Base64>" } }
      ]
    }]
  }
  ```

  `instruction` 与模型名经 `json.Marshal` 编码（处理引号、换行等转义）；`Base64` 已是 JSON 安全的字符集，剔除换行后原样写入，不复制、不重新编码。

  内存账目（默认 `attachment.max_size` 为 50 MB，Base64 后约 67 MB）：

  | 组成 | 大小 | 是否可避免 |
  |---|---|---|
  | Hertz 请求体缓冲 | 67 MB | 否。框架在进 handler 前已把整个请求体读入内存 |
  | `BindJSON` 解出的 `content` 字符串 | 67 MB | 否。要校验与转发就必须持有它 |
  | data URL 拼接串 | 67 MB | 是。`"data:" + mime + ";base64," + content` 会新分配一整块 |
  | `json.Marshal` 序列化结果 | 67 MB | 是。序列化遍历结构体、写出新字节，不顶替前一份 |

  前两份任何写法都躲不掉，后两份正是本节的拼接方案省掉的。所以单请求峰值由约 268 MB 降到约 134 MB，是减半而非减到四分之一。请求结束即全部释放，这不是常驻占用，实际压力取决于同时在飞的请求数；`rate_limit` 的并发槽位当前只对 `POST /chat` 生效（见「二、迭代说明」2.3）。

  显式设 `Content-Length` 而非分块传输：`io.MultiReader` 的长度是已知的，`req.ContentLength` 设为三段长度之和，`http.Client` 便会以 `Content-Length` 发送而不是退化成 `Transfer-Encoding: chunked`，避免对不接受 chunked 请求体的 OpenAI 兼容后端造成兼容问题。

  消息形状与 `/chat` 发送图片附件时一致（文本在前、图片在后）。`image_url` 不设 `detail` 字段，理由同 `/chat`：部分 OpenAI 兼容后端只接受 `low`/`high`，不传由上游取默认。
- 采样参数：不传 `temperature`、不传 `thinking`，由上游默认行为决定。`m.MaxCompletionTokens > 0` 时下发 `max_completion_tokens`，与 `/chat` 一致，避免超长输出失控。
- 鉴权：`Authorization: Bearer {m.APIKey}`（`ModelService.Resolve` 已展开环境变量）。
- 超时：转录、合成、图片解析共用同一个整体超时常量，即 `internal/llm/transcription.go` 中的 `upstreamTimeout`，三个调用点取值一致。
- 上游错误信息提取复用 `upstreamMessage`。
- 响应只取 `choices[0].message.content` 与 `usage`，其余字段忽略。

### 1.5 默认指令

`instruction` 去除首尾空白后为空时，使用常量：

> 请详细描述这张图片的内容；如果图片中包含文字，请完整识别并逐字输出。

内容同时覆盖「图里有什么」与「图里写了什么」两类最常见的解析诉求，避免未传指令时模型只做其中一件。指令固定写在代码里，不做成配置项；确实需要调整时再评估是否提升为设置。

调用方传入 `instruction` 时，只下发调用方的文本，不叠加默认指令——避免两段指令互相干扰。

### 1.6 与既有接口的一致性

| 约定 | 本接口的做法 |
|---|---|
| 附件结构 | 与 `/chat` 的 `attachments[]` 完全相同（`type`/`name`/`content`），`content` 为纯 Base64 |
| 指令字段名 | 与 `/chat` 同名同义：`instruction` 是 user 消息的文本部分。不使用 `/chat` 中含义不同的 `prompt`（system 附加指令） |
| 模型指定 | 只用请求头 `X-Model-Name`，与 `/chat` 一致；不提供 JSON `model` |
| 模型回落 | 显式指定的模型不存在或已禁用时报错，不静默回落默认视觉模型（与转录、合成一致） |
| 附件校验 | 复用 `internal/attachment` 的校验器与错误码，配置取自 `attachment.*` |
| 附件的类型判定 | 本接口只收 `image`，在 `Validate` 之前先自判一次 `type`；`Validate` 本身接受 `file`/`image`/`audio`/`video`，光靠它挡不住 `audio`、`video` |
| 附件配置读取 | `settings.Attachment(ctx)`，读取失败返回 500 `error`，与 `/audio/transcriptions` 一致。`/chat` 走的是 `settings.Runtime(ctx)` 加 `RuntimeStatic()` 降级，那套降级是为「配置表故障不阻断对话」设计的；本接口与转录一样是无状态转换，没有需要保住的长流程，失败直接报错更简单 |
| 上游调用 | 直接发 HTTP，鉴权头、`/v1` 补齐、超时、错误提取均复用 `internal/llm` 的既有实现；不引入 eino ChatModel，因为诉求只是拿到 `content` 原文 |
| data URL 的 MIME | 由文件头嗅探（或扩展名兜底）判定真实类型后下发。`/chat` 对图片一律填 `image/png`（见 `agent.defaultMIMEType`）；多数 OpenAI 兼容后端不看 data URL 里的 MIME，填错也能用，但严格的后端会校验，这里顺手判一次代价很小 |

### 1.7 README 更新

- 「七、REST API」的 API 列表加入 `POST /image/understanding`（权限点 `chat`）。
- 图片解析小节排在 `/audio/speech` 之后，编号 `7.5`；其后的 `7.5`～`7.18` 整体顺延一号，共 17 个标题（14 个主号 + `7.6.1`、`7.7.1`、`7.9.1` 三个子号）。三个子号随主号一起变（`7.6.1` → `7.7.1`、`7.7.1` → `7.8.1`、`7.9.1` → `7.10.1`），逐条改，不要只改主号。
- 小节内容：字段表、curl 示例（含 base64 生成方式）、响应示例、错误码表、模型取用顺序说明。
- 明确列出与会话无关、不要求 `X-User-ID` 的接口清单中加上本接口。
- 附录 D 错误码速查表补充：`unsupported_type` 与 `invalid_model` 的说明中提及本接口；`upstream_error` 的说明补上「图片解析」。
- 模型管理章节说明视觉模型（`DefaultVision`）的用途为图片解析。
- 「八、客户端代码示例」的 8.1 小节在示例代码里补上 `understand_image` 的调用片段，并把「完整代码及 17 个测试用例」更新为本版新增 3 个用例后的实际数量。8.2 的 Java 小节不动。

### 1.8 测试

Go 单元测试：

- `internal/llm/image_test.go`：用 `httptest.Server` 模拟上游，覆盖：
  - 请求路径与 `/v1` 补齐、Bearer 鉴权；
  - 请求体逐字节可被 `json.Unmarshal` 还原为期望结构（text + image_url、无 `detail`、无 `temperature`），验证「前缀 + Base64 + 后缀」拼接没有漏逗号或括号；
  - 请求头带 `Content-Length` 且数值与实际 body 长度一致；
  - 大 Base64（几 MB）也能正确透传，data URL 前缀为 `data:<MIMEType>;base64,`；
  - `max_completion_tokens` 为 0 时不下发；`usage` 缺失时返回 nil、`total_tokens` 缺失时按前两项之和补齐；
  - 上游 4xx/5xx 错误透传原文、非法 JSON、空 content、ctx 取消。
- `internal/api/handler/image_test.go`：
  - 请求体非法、缺少 `attachments`、0 张/2 张、`type` 为 `audio`/`file`、`name` 缺失；
  - Base64 含非法字符（空格）→ `attachment_decode_error`；含 `\r\n` 换行的 76 列 Base64 → 成功（与 `/chat` 同口径）；
  - 纯文本 Base64 且 `name` 为 `a.png` → `unsupported_type`（扩展名兜底不得放行明确非图片的内容）；
  - `application/octet-stream` 内容 + `.png` 文件名 → 走扩展名兜底，data URL 前缀为 `data:image/png;base64,`；
  - ICO 二进制 → 接受，data URL 前缀为 `data:image/x-icon;base64,`；
  - 体积超限 → `attachment_size_exceeded`；
  - 模型解析：显式不存在模型、禁用模型、未配置默认视觉模型 → 全部 `invalid_model` 且不回落；
  - `X-Model-Name` 生效；未传指令时下发的文本为默认指令；传入指令时不叠加默认指令；
  - 上游错误 → 502 `upstream_error`；
  - 调用方在解析途中取消上下文 → 499 `cancelled`，不报成 502 `upstream_error`；
  - 成功：`text` 原样返回、`model` 为实际模型、`usage` 存在/省略两种情况。
- `internal/api/middleware/auth_test.go`：`getRequiredPermission("/image/understanding", POST)` 为 `chat` 的表驱动用例补充（漏掉这条映射时默认分支返回 `all`，不会报错，只会静默要求 `all` 权限，所以这条测试是必须的）。
- `internal/llm/transcription_test.go`、`speech_test.go`：同步 `transcribeTimeout` → `upstreamTimeout` 改名（若测试引用该常量）。

Python 系统测试（`tests/python/`，用户运行）：鉴权 401/403、参数校验、默认视觉模型缺失时的 400。

示例代码与示例测试：

- `examples/python/groot_client.py` 增加 `understand_image(image_path, instruction=None, model=None)`，内部读文件转 Base64、按 `attachments` 结构提交。
- `tests/examples/python/test_groot_client.py` 的 mock server 增加 `/image/understanding` 分支，并补充默认指令、显式指令、显式模型三个用例（与已有的 `transcribe_*` 用例同构）。
- Java 示例不在本版范围：`GrootClient.java` 目前也没有 `/audio/speech` 与 `/audio/transcriptions` 之外的新接口方法，保持与现状一致，不单独为本接口扩 Java 客户端。

`tests/TEST_CASES.md` 增加对应条目。

### 1.9 留给实施者的决定

- `defaultInstruction` 常量与图片扩展名到 MIME 的映射表放在 handler 还是 llm 包。倾向放 handler（属于请求处理策略，不属上游协议）。
- 请求日志的字段选择与日志级别：只在失败时记一条（模型名 + 错误），调用方取消时降为 `Info`；不记成功路径，也不记 token 用量（`usage` 已随响应交给调用方）。
- `usage` 是否并入现有 `TokenUsage` 类型（`internal/agent`）以避免重复定义。倾向在 `llm` 包内定义独立小结构，不跨包依赖 agent。
- 嗅探读头的具体写法（`io.ReadFull` 取 512 字节并容忍 `io.ErrUnexpectedEOF`、缓冲复用大小）。要求：不在内存中保留解码后的图片字节，解码结果只在 512 字节缓冲里短暂停留。

## 二、迭代说明

### 2.1 版本说明

首版。此前没有任何图片解析接口；视觉模型的默认类型 `DefaultVision` 已在模型管理中定义，但没有接口使用它，本版将其接入。

### 2.2 方案取舍

| 决策点 | 采用 | 备选与否决理由 |
|---|---|---|
| 上传格式 | JSON + Base64 `attachments` | multipart 上传（与 `/audio/transcriptions` 一致）。否决：与 `/chat` 的附件结构不同形，客户端要多维护一套构造与编码逻辑；且本接口的最近邻居是 `/chat` 的图片附件而非音频上传 |
| 图片转 Base64 的位置 | 调用方完成 | Groot 内部转换。否决：调用方既然已按 `/chat` 传 Base64，Groot 再做一次转换等于同一份数据两种约定；也让体积校验口径与 `/chat` 保持一致 |
| 权限点 | 复用 `chat` | 新增 `image` 权限点。否决：需同时改 `ValidPermissions`、路径映射与 Web 端权限列表三处，且图片解析与语音一样属于聊天输入；单独授权的需求出现时再拆 |
| 指令字段名 | `instruction` | `prompt`、`task`、`text`。否决：`instruction` 在 `/chat` 中就是「用户这句话」，语义一致；`prompt` 在 `/chat` 中是 system 附加指令，同名不同义会造成误用 |
| 指令传递方式 | JSON 字段（与 `/chat` 一致） | 请求头、查询参数。否决：`/chat` 的 `instruction` 在 JSON 体里，保持一致 |
| 图片数量 | 数组形状，恰好 1 张 | 无数组的单对象。否决：数组便于以后支持多图对比；允许 10 张（沿用 `max_count`）则现在就要处理顺序、总量与提示词语义，超出当前需求 |
| 上游调用 | 直接 HTTP 到 `/chat/completions` | 复用 eino `NewChatModel` + `adk`。否决：本接口只需 `content` 原文，走 Agent 抽象要处理 agent 生命周期与消息构建，收益为零；转录、合成的既有做法也是直接 HTTP |
| 请求体构造 | 前缀 + Base64 原文 + 后缀，`io.MultiReader` 送入请求 | 先拼完整 data URL 再 `json.Marshal`（`/audio/speech` 与 `/chat` 经 eino 最终都走到这一步）。否决：一份 50 MB 的图片按 Base64 后约 67 MB，朴素写法会额外多出 data URL 与序列化结果两份各 67 MB，单请求峰值约 268 MB；拼接方案只留请求体缓冲与解出的字符串两份，峰值约 134 MB。请求体缓冲是框架行为、解出的字符串要用于校验与转发，这两份任何写法都省不掉 |
| Base64 合法性口径 | 与 `/chat` 对齐（`base64.NewDecoder` 忽略 `\r\n`，其余非法字符报错）；仅剔除 `\r\n` 以免拼出非法 JSON | 额外做归一化（剔除空白）。否决：调用方既然按 `/chat` 传 Base64，两处口径就该一样；归一化会掩盖客户端的编码问题（空格照旧报 `attachment_decode_error`），也让两个接口对同一份内容给出不同结果 |
| 图片类型判定 | 嗅探出 `image/*` 即接受（含 ICO），仅「嗅探不出」时才用扩展名兜底 | 只认 png/jpeg/webp/gif/bmp 白名单。否决：白名单会把 Go 认得的其它图片类型挡在门外；反之若对「嗅探出是文本/PDF」也走扩展名兜底，改名就能绕过校验 |
| 空响应 | 502 `upstream_error` | 200 + 空 `text`。否决：调用方无法区分「图里没内容」与「模型没答」，与转录对空结果的处理也不一致 |
| Web 路由 | 不注册 | 同时注册 `/web/image/understanding`。否决：目前没有界面调用；以后加只需一行 |
| `usage` 字段 | 上游返回时带上 | 不返回。否决：token 用量对调用方排查与计费有用，字段名已与 SSE usage 对齐，成本仅一个可选字段 |

### 2.3 未纳入本版

- 多图输入与图片对比。
- 流式返回（`/chat` 以外的接口均为一次性返回）。
- 图片 URL 输入（上游支持 `image_url` 传公网地址，但本接口面向本地上传的文件）。
- `/web/image/understanding` 与 Web 界面的图片解析工具。
- 独立的图片体积上限（当前沿用 `attachment.max_size`，默认 50 MB）。
- 大请求体的并发上限。当前 `rate_limit` 的并发槽位只对 `POST /chat` 生效（`internal/api/middleware/ratelimit.go` 按路径判断），其余接口一律只查 QPS；且 `rate_limit.enabled` 默认为关闭。本接口沿用这套既有策略，不单独加锁——单请求峰值内存已由请求体构造方案压到约 134 MB，且请求结束即释放，真正的约束（「大请求 × 无限并发」）是全系统层面的问题，需要连同音频转录等接口一并评估，不在本版范围内。
