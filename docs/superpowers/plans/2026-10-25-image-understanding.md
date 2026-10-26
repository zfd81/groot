# 图片解析（Image Understanding）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增无状态的对外接口 `POST /image/understanding`：提交一张图片（JSON + Base64）与可选指令，返回 OpenAI 兼容视觉模型输出的原文，不产生会话、不写历史。

**Architecture:** 沿用转录、合成的既有两层结构：`internal/llm.UnderstandImage` 直接向 `{base_url}/chat/completions` 发请求并返回文本与 token 用量，请求体用 `io.MultiReader` 把「前缀 + Base64 原文 + 后缀」三段拼成，避免额外的 data URL 与序列化副本；`handler.ImageHandler` 按「请求格式 → 附件 → 图片类型 → 模型」六步校验后调用它；路由挂在对外 API 组，权限点复用 `chat`。

**Tech Stack:** Go + Hertz，`internal/attachment` 既有校验器，`internal/llm` 既有上游辅助函数，`net/http`、`encoding/base64`、`encoding/json`。

**Spec:** `docs/superpowers/specs/2026-10-25-image-understanding-design.md`

## Global Constraints

- 请求：`Content-Type: application/json`，字段 `instruction`（可选）+ `attachments`（必填，恰好 1 张）；`attachments[]` 结构与 `/chat` 的 `types.Attachment` 完全一致（`type`/`name`/`content`），`content` 为纯 Base64，不带 `data:` 前缀。
- **参数名与传入方式必须与既有接口一致**：指令字段叫 `instruction`（不是 `prompt`），模型只用请求头 `X-Model-Name`（不设 JSON `model` 字段）。
- 只收 `type == "image"`；`attachments` 长度必须恰好为 1。
- 模型解析：`models.Resolve(ctx, X-Model-Name, repo.DefaultVision)`；显式指定的模型不存在或已禁用报 400 `invalid_model`，不回落默认视觉模型。
- 默认指令常量（逐字）：`请详细描述这张图片的内容；如果图片中包含文字，请完整识别并逐字输出。`
- 默认指令只在调用方 `instruction` 裁剪空白后为空时使用；调用方给了指令就不叠加默认指令。
- 图片类型判定：`http.DetectContentType` 嗅探解码后前 512 字节，结果为 `image/*` 即接受（含 `image/x-icon`）；仅当结果为 `application/octet-stream` 时才按 `name` 扩展名兜底；其余一律 400 `unsupported_type`。
- Base64 合法性口径与 `/chat` 一致：`\r`、`\n` 被忽略，空格等非法字符报错。嵌入 data URL 前剔除 `\r`、`\n`（JSON 字符串不允许裸控制字符，直接拼接会得到非法报文），除这两类换行外不做任何归一化。
- 图片不在 Groot 内解码保存：单次流式解码，只保留 512 字节嗅探缓冲。
- 上游超时常量统一命名为 `upstreamTimeout`（120 秒），转录、合成、图片解析共用。
- 响应成功 200：`{"text","model","usage"}`，`usage` 在上游未返回时省略；`total_tokens` 缺失时按前两项之和补齐。
- 错误响应格式 `{"status": "...", "message": "..."}`，取值见 spec 1.3.5。
- 命名一致性：同一含义在 Go、JSON、README 使用同一名字（`instruction`、`model`、`usage.prompt_tokens` 等）。
- 所有改动直接在 `master` 工作区进行，不建分支；每个任务的"快照"步骤用 `git write-tree` + `git commit-tree` 记录悬空对象，不触碰 HEAD、索引和分支；不自动提交。
- 单元测试只用 Go 标准测试框架；系统测试只用 pytest。
- README 是用户手册，接口变化必须同步到 README。
- 只注册对外路由，不注册 `/web/image/understanding`。
- 回复用中文；Go 代码经 `gofmt`。

## Review Focus

1. 大 Base64 的请求体长度：`io.MultiReader` 三段之和必须等于 `req.ContentLength`，上游收到的是 `Content-Length` 而不是 `Transfer-Encoding: chunked`；否则部分 OpenAI 兼容后端会拒收。测试归 Task 1。
2. `attachments[0].type` 为 `audio` 或 `video`：必须在 `attachment.Handler.Validate` 之前被本接口拦下，返回 400 `attachment_invalid_type`；`Validate` 自身放行这两种类型，光靠它会漏过去。测试归 Task 2。
3. 明确非图片的内容改名成 `.png`：必须仍返回 400 `unsupported_type`，扩展名兜底只能用于 `application/octet-stream`；否则改名即可绕过校验。测试归 Task 2。
4. 漏注册权限映射：`getRequiredPermission` 缺少 `POST /image/understanding` 分支时不会报错，只会命中默认分支返回 `all`，持 `chat` 权限的 Key 静默收到 403。测试归 Task 3。
5. 带 `\r\n` 换行的 Base64（`base64` 命令行的默认输出）：必须与 `/chat` 一样被接受；若本接口额外严格，调用方按文档生成的 Base64 会被拒。测试归 Task 2。

---

### Task 1: 上游调用 `llm.UnderstandImage` 与超时常量改名

**Files:**
- Create: `internal/llm/image.go`
- Create: `internal/llm/image_test.go`
- Modify: `internal/llm/transcription.go:24`（注释）、`:26`（常量声明）、`:77`（`Transcribe` 内使用）
- Modify: `internal/llm/speech.go:60`（`Speech` 内使用）
- Reuse: `internal/llm/chatmodel.go:20` `openAIBaseURL`；`internal/llm/transcription.go:117-140` `upstreamMessage`、`truncate`、`ErrUpstream`

**Interfaces:**
- Consumes: `repo.Model{BaseURL, APIKey, Model, MaxCompletionTokens}`；`openAIBaseURL(baseURL string) string`；`upstreamMessage(body []byte, status int) string`；`truncate(s string, n int) string`；`ErrUpstream error`。
- Produces:

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
func UnderstandImage(ctx context.Context, m *repo.Model, req ImageRequest) (string, *Usage, error)

// upstreamTimeout 转录、合成、图片解析共用的整体超时。
const upstreamTimeout = 120 * time.Second
```

- [ ] **Step 1: 写失败测试 `internal/llm/image_test.go`**

```go
package llm

// imageRecord 记录伪上游收到的一次图片解析请求。
type imageRecord struct {
    path          string
    auth          string
    contentLength int64  // 取 r.ContentLength：net/http 把 Content-Length 头解析进该字段，头本身已被移除
    body          []byte
}

// newImageServer 启动伪上游：记录请求后交给 respond 应答。
func newImageServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *imageRecord)

func imageModel(baseURL string) *repo.Model {
    return &repo.Model{Model: "qwen-vl-max", BaseURL: baseURL, APIKey: "sk-img"}
}

// 成功响应用的上游报文
const imageOKBody = `{"choices":[{"message":{"role":"assistant","content":"图里是一张发票"}}],
    "usage":{"prompt_tokens":1024,"completion_tokens":48,"total_tokens":1072}}`
```

测试函数与断言（每个函数一组）：

- `TestUnderstandImage_Success`：`path == "/v1/chat/completions"`；`Authorization == "Bearer sk-img"`；`json.Unmarshal(rec.body)` 还原出的结构里 `model=="qwen-vl-max"`、`messages[0].role=="user"`、`messages[0].content[0].type=="text"` 且 `text` 等于传入的 `Instruction`、`content[1].type=="image_url"` 且 `url == "data:image/png;base64,"+base64Content`；`content[1].image_url` 下**没有** `detail` 键；顶层**没有** `temperature`、`thinking`；返回 `text` 等于上游 `content`，`usage.TotalTokens == 1072`。
- `TestUnderstandImage_BaseURLAlreadyHasV1`：`BaseURL: srv.URL+"/v1"` 时 `path` 仍为 `/v1/chat/completions`（不重复补 `/v1`）。
- `TestUnderstandImage_ContentLengthMatchesBody`：`rec.contentLength == int64(len(rec.body))`（三段拼接的总长度与实际字节一致），且 `rec.contentLength > 0`。
- `TestUnderstandImage_LargeBase64`：`Base64` 取 4 MB 的 `strings.Repeat("QUJD", ...)`（4 的倍数）；上游收到的 body 里该字符串原样出现一次，data URL 前缀为 `data:image/jpeg;base64,`。
- `TestUnderstandImage_MaxCompletionTokens`：表驱动——`MaxCompletionTokens: 0` 时 unmarshal 出的 map 中无 `max_completion_tokens` 键；`:2000` 时该键值为 2000。
- `TestUnderstandImage_UsageOmitted`：上游报文无 `usage` 字段时返回的 `*Usage` 为 nil、无错误。
- `TestUnderstandImage_TotalTokensBackfill`：上游只给 `prompt_tokens:10`、`completion_tokens:5` 时 `TotalTokens == 15`；上游给 `total_tokens:99` 时原样为 99。
- `TestUnderstandImage_UpstreamError`：上游 429 + `{"error":{"message":"rate limited"}}` 时错误 `errors.Is(err, ErrUpstream)` 且 `strings.Contains(err.Error(), "rate limited")`。
- `TestUnderstandImage_InvalidJSON`：上游 200 返回 `not json` 时 `errors.Is(err, ErrUpstream)`。
- `TestUnderstandImage_EmptyContent`：上游 `{"choices":[{"message":{"content":"  "}}]}` 时 `errors.Is(err, ErrUpstream)`（空内容判失败，不返回空串）。
- `TestUnderstandImage_ContextCanceled`：`ctx` 已取消时返回的错误 `errors.Is(err, context.Canceled)`，且不被包成 `ErrUpstream`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/llm/ -run TestUnderstandImage -v`
Expected: FAIL / 编译错误（`undefined: UnderstandImage`、`undefined: ImageRequest`、`undefined: imageRecord` 等）

- [ ] **Step 3: 改名 `transcribeTimeout` → `upstreamTimeout`（4 行）**

- `internal/llm/transcription.go:24` 注释首句改为 `// upstreamTimeout 转录、合成、图片解析共用的整体超时。`
- `internal/llm/transcription.go:26` 声明改为 `const upstreamTimeout = 120 * time.Second`
- `internal/llm/transcription.go:77` `&http.Client{Timeout: transcribeTimeout}` → `upstreamTimeout`
- `internal/llm/speech.go:60` 同上

改名后执行 `grep -rn transcribeTimeout internal/` 必须无输出（无任何测试引用该常量）。

- [ ] **Step 4: 实现 `internal/llm/image.go`**

签名与取值见本任务 Interfaces。实现要点：

- 端点：`openAIBaseURL(m.BaseURL) + "/chat/completions"`。
- 请求体三段拼接，用 `io.MultiReader`：

  - 前缀：`{"model":` + `json.Marshal(m.Model)` + `,"messages":[{"role":"user","content":[{"type":"text","text":` + `json.Marshal(req.Instruction)` + `},{"type":"image_url","image_url":{"url":"data:` + `req.MIMEType` + `;base64,`
  - 中段：`strings.NewReader(req.Base64)`
  - 后缀：`"}}]}]}`；`m.MaxCompletionTokens > 0` 时改为 `"}}]}],"max_completion_tokens":` + `strconv.Itoa(...)` + `}`

- `http.NewRequestWithContext(ctx, http.MethodPost, endpoint, io.MultiReader(prefix, payload, suffix))`，随后 `req.ContentLength = int64(前缀长度 + len(req.Base64) + 后缀长度)`。
- 请求头：`Authorization: Bearer {m.APIKey}`；`Content-Type: application/json`。
- `client := &http.Client{Timeout: upstreamTimeout}`；`client.Do` 失败时若 `ctx.Err() != nil` 直接返回 `ctx.Err()`，否则 `fmt.Errorf("%w: %w", ErrUpstream, err)`。
- 响应体 `io.ReadAll(io.LimitReader(resp.Body, 1<<20))`；`resp.StatusCode >= 400` → `fmt.Errorf("%w: %s", ErrUpstream, upstreamMessage(body, resp.StatusCode))`；非法 JSON → `fmt.Errorf("%w: 响应不是合法 JSON: %s", ErrUpstream, truncate(string(body), 200))`。
- 解出 `choices[0].message.content` 与 `usage`；`strings.TrimSpace(content) == ""` → `fmt.Errorf("%w: 上游未返回内容", ErrUpstream)`。
- `usage` 指针：上游报文无 `usage` 字段时为 nil；有则回填，`TotalTokens == 0 && (PromptTokens > 0 || CompletionTokens > 0)` 时置为两者之和。

- [ ] **Step 5: 运行测试与构建**

Run: `gofmt -l internal/llm/ && go test ./internal/llm/... -v && go build ./...`
Expected: `gofmt -l` 无输出；`internal/llm` 全部 PASS；构建成功

- [ ] **Step 6: 快照**

```bash
export GIT_INDEX_FILE=.git/sdd-index
git read-tree HEAD && git add -A && T=$(git write-tree)
git commit-tree "$T" -p HEAD -m "task1: 新增 llm.UnderstandImage 与 upstreamTimeout 改名"
unset GIT_INDEX_FILE; rm -f .git/sdd-index
```

输出的提交哈希记入 `.superpowers/sdd/image-understanding/progress.md`。后续任务的"快照"步骤同此，只改提交消息。

---

### Task 2: 请求类型与 handler

**Files:**
- Modify: `internal/api/types/types.go:19`（在 `Attachment` 结构体结束处之后、`ExecuteResponse` 之前新增结构体）
- Create: `internal/api/handler/image.go`
- Create: `internal/api/handler/image_test.go`
- Reuse: `internal/api/handler/transcription.go:18-34`（`audioExtensions` / `supportedAudioExtensions` 的写法）、`:36-47`（handler 结构与构造函数）、`:86-104`（模型解析与错误映射）；`internal/attachment/handler.go:40-120`（`NewHandler`、`Validate`、`AttachmentError`）；`internal/api/handler/models_test.go:31-43` `callJSON`；`internal/api/handler/speech_test.go:55-68` `callJSONWithHeader`；`internal/api/handler/transcription_test.go:17-56` `newTranscriptionHandlerForTest` 的建库建模型写法

**Interfaces:**
- Consumes: `llm.UnderstandImage(ctx, m *repo.Model, req llm.ImageRequest) (string, *llm.Usage, error)`；`llm.ErrUpstream`；`attachment.NewHandler(cfg config.AttachmentConfig) *attachment.Handler`、`(*Handler).Validate([]attachment.Attachment) error`、`*attachment.AttachmentError{Code, Message}`；`settings.Attachment(ctx) (config.AttachmentConfig, error)`；`models.Resolve(ctx, name string, flag repo.DefaultFlag) (*repo.Model, error)`、`llm.ErrModelNotFound`/`ErrModelDisabled`/`ErrNoDefaultModel`；`repo.DefaultVision`。
- Produces:

```go
// internal/api/types/types.go
type ImageUnderstandingRequest struct {
    Instruction string       `json:"instruction"`
    Attachments []Attachment `json:"attachments"`
}

// internal/api/handler/image.go
const defaultInstruction = "请详细描述这张图片的内容；如果图片中包含文字，请完整识别并逐字输出。"

// statusClientClosedRequest 调用方在拿到响应前放弃请求时返回的状态码（nginx 约定的 499）。
// 与 502 分开是为了让「请求被放弃」不再被读成「上游故障」。
const statusClientClosedRequest = 499

// imageExtensions 文件头无法识别时按扩展名兜底映射的图片类型。
var imageExtensions = map[string]string{
    ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
    ".webp": "image/webp", ".gif": "image/gif", ".bmp": "image/bmp",
}

func supportedImageExtensions() string // 排序后以 ", " 连接，得到 ".bmp, .gif, .jpeg, .jpg, .png, .webp"

var (
    errDecodeImage = errors.New("图片内容不是合法 Base64")
    errNotImage    = errors.New("内容不是受支持的图片")
)

// imageMIME 判定 content 代表的图片类型：单次流式解码，只保留 512 字节嗅探缓冲。
// 解码失败返回 errDecodeImage；类型不可用返回 errNotImage。
func imageMIME(content, name string) (string, error)

type ImageHandler struct {
    settings *setting.Settings
    models   *llm.ModelService
    log      *logger.Logger
}

func NewImageHandler(settings *setting.Settings, models *llm.ModelService, log *logger.Logger) *ImageHandler
func (h *ImageHandler) Serve(ctx context.Context, rc *app.RequestContext)
```

- [ ] **Step 1: 写失败测试 `internal/api/handler/image_test.go`**

测试辅助（与转录测试同构，模型名换为视觉模型）：

```go
// newImageHandlerForTest 建一套真实的仓库与配置对象；upstreamURL 非空时创建名为 qwen-vl-max 的模型
// （库中首个模型，自动成为默认对话模型且启用），其 base_url 指向 httptest 假上游；
// defaultVision 为 true 时把它设为默认视觉模型。
func newImageHandlerForTest(t *testing.T, upstreamURL string, defaultVision bool) *ImageHandler

// imageUpstream 记录假上游收到的请求体（供断言 instruction 与 data URL）。
type imageUpstream struct {
    mu   sync.Mutex
    body []byte
}

// newImageUpstream 启动假上游：记录请求体，返回 text 与 usage 固定的成功报文。
func newImageUpstream(t *testing.T) (*httptest.Server, *imageUpstream)

// imageBody 拼一个单图请求体；content 直接嵌入（不转义），便于构造非法 JSON 之外的用例。
func imageBody(instruction, typ, name, content string) string

// callJSONWithContext 与既有 callJSONWithHeader 相同，但用调用方给定的 ctx 调 handler，
// 供「调用方中断请求」这类需要取消上下文的用例使用（callJSONWithHeader 固定用 Background）。
func callJSONWithContext(ctx context.Context, h func(context.Context, *app.RequestContext),
    method, body string, headers map[string]string) *app.RequestContext

// 可用的测试图片 Base64（均已实测）
const (
    pngBase64  = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC" // 嗅探 image/png
    icoBase64  = "AAABAAEA"                                                                                    // 嗅探 image/x-icon
    octetBase64 = "AQIDBAUGBwg="                                                                               // 嗅探 application/octet-stream
    textBase64 = "aGVsbG8gd29ybGQ="                                                                            // 嗅探 text/plain; charset=utf-8
)
```

测试函数与断言：

- `TestImageHandler_BadJSON`：body 为 `{not json` → 400、`status == "invalid_request"`。
- `TestImageHandler_AttachmentCount`：`attachments` 缺省（`{}`）与 2 张图 → 均 400 `invalid_request` 且 message 含 `必须提供 1 张图片附件`。
- `TestImageHandler_NotImageType`：`type` 为 `audio`、`file` → 400 `attachment_invalid_type`（不得进入 `Validate` 的类型分支）。
- `TestImageHandler_MissingName`：`name` 为空 → 400 `attachment_missing_name`。
- `TestImageHandler_DecodeError`：`content` 为 `"aGVs bG8="`（含空格）→ 400 `attachment_decode_error`。
- `TestImageHandler_Base64WithNewlines`：`pngBase64` 按 76 列插入 `\r\n` → 200，且上游收到的 data URL 前缀为 `data:image/png;base64,`。
- `TestImageHandler_TextRenamedPNG`：`textBase64` + `name: "a.png"` → 400 `unsupported_type`（扩展名兜底不得放行明确非图片内容）。
- `TestImageHandler_OctetStreamExtensionFallback`：`octetBase64` + `name: "a.png"` → 200，上游 data URL 前缀为 `data:image/png;base64,`。
- `TestImageHandler_OctetStreamUnknownExtension`：`octetBase64` + `name: "a.tiff"` → 400 `unsupported_type` 且 message 含 `.png`。
- `TestImageHandler_ICOSniffed`：`icoBase64` + `name: "a.ico"` → 200，上游 data URL 前缀为 `data:image/x-icon;base64,`。
- `TestImageHandler_SizeExceeded`：`rt := h.settings.RuntimeStatic(); rt.Attachment.MaxSize = 1; h.settings.SetRuntime(ctx, rt)` 后提交约 2 MB Base64 → 400 `attachment_size_exceeded`。
- `TestImageHandler_InvalidModel`：表驱动——`X-Model-Name: __nope__`；先创建 `seed-default` 再创建 `Enabled: false` 的 `qwen-vl-max` 并指定它；未设默认视觉模型且不传头 → 三种均 400 `invalid_model`，且 message 非空、不回落。
- `TestImageHandler_ModelHeader`：`X-Model-Name: qwen-vl-max` 且未设默认视觉模型 → 200（显式指定不要求持有 vision 默认）。
- `TestImageHandler_DefaultInstruction`：不传 `instruction`（以及传 `"   "`）→ 上游收到的 `messages[0].content[0].text` 恰为 `defaultInstruction`。
- `TestImageHandler_CustomInstruction`：传 `"提取发票字段"` → 上游收到的 text 恰为 `"提取发票字段"`，不含 `defaultInstruction` 任何片段。
- `TestImageHandler_UpstreamError`：假上游返回 500 + `{"error":{"message":"boom"}}` → 502 `upstream_error`，message 含 `boom`。
- `TestImageHandler_CallerCancelled`：`ctx, cancel := context.WithCancel(context.Background())`；假上游收到请求后 `close(started)` 并阻塞在 `<-unblock`；用 `callJSONWithContext(ctx, h.Serve, ...)` 调用，`go func(){ <-started; cancel() }()`；断言 499、`status == "cancelled"`，且**不是** 502 `upstream_error`。`defer srv.Close()` 之后再 `defer close(unblock)`，保证上游协程一定退出，测试不会卡在 `srv.Close`。
- `TestImageHandler_Success`：200 且 `text` 与上游原文逐字相同、`model == "qwen-vl-max"`、`usage.total_tokens == 1072`；另一用例上游无 `usage` 时响应 JSON 中不含 `usage` 键。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run TestImageHandler -v`
Expected: FAIL / 编译错误（`undefined: ImageHandler` 等）

- [ ] **Step 3: 在 `internal/api/types/types.go` 新增 `ImageUnderstandingRequest`**

紧跟 `Attachment` 结构体之后，字段与 JSON 标签按 Interfaces 中的定义逐字写入。

- [ ] **Step 4: 实现 `internal/api/handler/image.go`**

`Serve` 的校验顺序严格按 spec 1.3.3，一次只报一类问题：

1. `rc.BindJSON(&req)` 失败 → `400 invalid_request`，message `请求参数错误`。
2. `len(req.Attachments) != 1` → `400 invalid_request`，message `必须提供 1 张图片附件`。
3. `req.Attachments[0].Type != "image"` → `400 attachment_invalid_type`，message `无效的附件类型：` + type。
4. `h.settings.Attachment(ctx)` 失败 → `h.log.Error("读取附件配置失败", zap.Error(err))` + `500 error`（message `内部错误`）；成功则把 `req.Attachments` 逐项转成 `[]attachment.Attachment` 后 `attachment.NewHandler(cfg).Validate(...)`，错误用类型断言取 `*attachment.AttachmentError` 并 `400 {"status": attErr.Code, "message": attErr.Message}`（与 `/chat` 同写法），非该类型错误一律 `400 attachment_validation_error`。
5. `imageMIME(att.Content, att.Name)`：返回 `errDecodeImage` → `400 attachment_decode_error`，message `附件 <name> 的 Base64 解码失败`；返回 `errNotImage` → `400 unsupported_type`，message `附件 <name> 不是受支持的图片（支持的扩展名：<supportedImageExtensions()>）`。
6. `h.models.Resolve(ctx, strings.TrimSpace(string(rc.GetHeader("X-Model-Name"))), repo.DefaultVision)`：`ErrModelNotFound`/`ErrModelDisabled`/`ErrNoDefaultModel`（`errors.Is`）→ `400 invalid_model`，message `err.Error()`；其余错误 → `h.log.Error` + `500 error`。

`imageMIME` 的实现要点：

- `dec := base64.NewDecoder(base64.StdEncoding, strings.NewReader(content))`
- `head := make([]byte, 512)`；`n, err := io.ReadFull(dec, head)`；`err` 不为 nil 且不是 `io.ErrUnexpectedEOF`/`io.EOF` → `errDecodeImage`
- `io.Copy(io.Discard, dec)` 走完剩余部分，出错 → `errDecodeImage`（这一步才能发现尾部非法字符）
- `mt := http.DetectContentType(head[:n])`；`strings.HasPrefix(mt, "image/")` → 返回 `mt`
- `mt != "application/octet-stream"` → `errNotImage`（嗅探出明确非图片类型，不兜底）
- 否则 `mime, ok := imageExtensions[strings.ToLower(filepath.Ext(name))]`；`ok` → 返回 `mime`，否则 `errNotImage`

调用上游与响应：

```go
instruction := strings.TrimSpace(req.Instruction)
if instruction == "" {
    instruction = defaultInstruction
}
text, usage, err := llm.UnderstandImage(ctx, m, llm.ImageRequest{
    Instruction: instruction,
    MIMEType:    mimeType,
    Base64:      att.Content,
})
if err != nil {
    // 调用方上下文已结束（请求被放弃）不是上游故障：日志与响应都据此分开，
    // 否则一条「图片解析失败」会把「没人等结果了」记成「上游坏了」。
    // 只有错误本身就是该上下文错误时才这样判定，避免上游真出错时恰好赶上取消而被误记。
    if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
        h.log.Info("图片解析已中断", zap.String("model", m.Name), zap.Error(ctxErr))
        rc.JSON(statusClientClosedRequest, utils.H{"status": "cancelled", "message": ctxErr.Error()})
        return
    }
    h.log.Warn("图片解析失败", zap.String("model", m.Name), zap.Error(err))
    rc.JSON(502, utils.H{"status": "upstream_error", "message": err.Error()})
    return
}
resp := utils.H{"text": text, "model": m.Name}
if usage != nil {
    resp["usage"] = utils.H{
        "prompt_tokens":     usage.PromptTokens,
        "completion_tokens": usage.CompletionTokens,
        "total_tokens":      usage.TotalTokens,
    }
}
rc.JSON(200, resp)
```

- [ ] **Step 5: 运行测试与构建**

Run: `gofmt -l internal/api/ && go test ./internal/api/handler/ -run TestImageHandler -v && go build ./...`
Expected: `gofmt -l` 无输出；`TestImageHandler*` 全部 PASS；构建成功

- [ ] **Step 6: 快照**

```bash
export GIT_INDEX_FILE=.git/sdd-index
git read-tree HEAD && git add -A && T=$(git write-tree)
git commit-tree "$T" -p HEAD -m "task2: 新增 ImageHandler 与图片解析请求类型"
unset GIT_INDEX_FILE; rm -f .git/sdd-index
```

---

### Task 3: 路由注册与权限映射

**Files:**
- Modify: `internal/api/server.go:125`（`speechH := ...` 之后新增一行）、`:137`（`RegisterRoutes` 实参中 `speechH` 之后）
- Modify: `internal/api/router.go:34`（参数列表中 `speechH` 之后）、`:121`（`apiGroup.POST("/audio/speech", ...)` 之后）
- Modify: `internal/api/middleware/auth.go:131`（`/audio/speech` 分支之后）
- Modify: `internal/api/middleware/auth_test.go:198`（doc comment）、`:205` 之后（用例行）
- Reuse: `internal/api/handler/image.go` `NewImageHandler`、`(*ImageHandler).Serve`（Task 2）

**Interfaces:**
- Consumes: `handler.NewImageHandler(settings *setting.Settings, models *llm.ModelService, log *logger.Logger) *handler.ImageHandler`；`(*handler.ImageHandler).Serve`。
- Produces: 对外路由 `POST /image/understanding`（API Key 鉴权 + `chat` 权限 + 计入限流）；`getRequiredPermission("/image/understanding", "POST") == "chat"`。

- [ ] **Step 1: 在 `internal/api/middleware/auth_test.go` 补归属断言（先让测试失败）**

- doc comment（第 198 行）改为 `// TestGetRequiredPermission 路径与方法到权限点的映射表；/audio/transcriptions、/audio/speech、/image/understanding 与 /chat 共用 chat 权限。`
- 在 `/audio/speech` 两行之后插入：

```go
        {"/image/understanding", consts.MethodPost, "chat"},
        {"/image/understanding", consts.MethodGet, "all"},
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/middleware/ -run TestGetRequiredPermission -v`
Expected: FAIL，`getRequiredPermission("/image/understanding", "POST") = "all", want "chat"`

- [ ] **Step 3: 在 `auth.go` 增加权限映射**

`/audio/speech` 分支之后插入：

```go
	// Image understanding endpoint (POST /image/understanding)：图片解析是聊天输入的一种形式，
	// 与 /chat 共用 chat 权限
	if path == "/image/understanding" && method == "POST" {
		return "chat"
	}
```

- [ ] **Step 4: 在 `server.go` 构造 handler 并传入 `RegisterRoutes`**

- 第 125 行 `speechH := handler.NewSpeechHandler(models, log)` 之后新增 `imageH := handler.NewImageHandler(settings, models, log)`
- 第 137 行 `RegisterRoutes(...)` 实参中，`speechH` 与 `settingH` 之间插入 `imageH`

- [ ] **Step 5: 在 `router.go` 增加参数与路由**

- 第 34 行 `speechH *handler.SpeechHandler,` 之后新增 `imageH *handler.ImageHandler,`
- 第 121 行 `apiGroup.POST("/audio/speech", speechH.Serve)` 之后新增：

```go
	// 对外图片解析接口：与 /chat 同组，API Key 鉴权并计入限流
	apiGroup.POST("/image/understanding", imageH.Serve)
```

不新增 `webGroup` 路由。

- [ ] **Step 6: 运行测试与构建**

Run: `gofmt -l internal/api/ && go test ./internal/api/... -v && go build -o dist/groot ./cmd/groot`
Expected: `gofmt -l` 无输出；`internal/api/...` 全部 PASS；`dist/groot` 生成

- [ ] **Step 7: 快照**

```bash
export GIT_INDEX_FILE=.git/sdd-index
git read-tree HEAD && git add -A && T=$(git write-tree)
git commit-tree "$T" -p HEAD -m "task3: 注册 /image/understanding 路由与 chat 权限映射"
unset GIT_INDEX_FILE; rm -f .git/sdd-index
```

---

### Task 4: README、示例代码与测试清单

**Files:**
- Modify: `README.md`（API 列表 1279 行后、新增 7.5 小节、17 个标题重编号、2348/2390/2694 三处正文引用、648 行清单、352-353 行、489-490 行规则、附录 D 2850/2856/2859/2861/2877 行、8.1 小节 2508-2524 行）
- Modify: `examples/python/groot_client.py`（第 39 行后补 `import base64`；`transcribe` 之后新增方法）
- Modify: `tests/examples/python/test_groot_client.py`（`do_POST` 分支、mock 处理函数、3 个用例）
- Create: `tests/python/test_image_understanding.py`
- Modify: `tests/TEST_CASES.md`（新增 §1.14、§2.30，更新 §五 统计）
- Reuse: `README.md:1768-1809`（7.4.1 的 JSON 接口写法）、`README.md:1728-1765`（7.4 的 curl 与错误写法）；`tests/python/test_speech.py`（pytest 结构模板）；`examples/python/groot_client.py:280-301` `transcribe`（方法结构模板，但**不要**复制其 `headers={"Content-Type": None}`——本接口发 JSON）

**Interfaces:**
- Consumes: HTTP 契约 `POST /image/understanding`（请求/响应/错误码见 spec 1.3 与 Global Constraints）。
- Produces: `GrootClient.understand_image(image_path: str, instruction: Optional[str] = None, model: Optional[str] = None) -> dict`，返回服务端 JSON（含 `text`、`model`，可能含 `usage`）。

- [ ] **Step 1: 在 `examples/python/groot_client.py` 新增 `understand_image`**

模块头部 `import json` 上方补 `import base64`（保持标准库字母序为 `base64`、`json`、`os`）。方法追加在 `transcribe` 之后：

```python
    def understand_image(self, image_path: str, instruction: Optional[str] = None,
                         model: Optional[str] = None) -> dict:
        """图片解析。返回 {"text": 模型输出, "model": 实际使用的模型}，上游给用量时含 usage。

        本接口与会话无关：不产生会话、不写历史。instruction 省略时由服务端使用默认指令；
        model 省略时由服务端使用默认视觉模型。
        """
        with open(image_path, "rb") as f:
            content = base64.b64encode(f.read()).decode()
        payload = {"attachments": [{
            "type": "image",
            "name": os.path.basename(image_path),
            "content": content,
        }]}
        if instruction:
            payload["instruction"] = instruction
        headers = {"X-Model-Name": model} if model else None
        resp = self.session.post(f"{self.base_url}/image/understanding",
                                 json=payload, headers=headers)
        resp.raise_for_status()
        return resp.json()
```

- [ ] **Step 2: 在 `tests/examples/python/test_groot_client.py` 补 mock 分支与 3 个用例**

- `do_POST` 中 `/audio/transcriptions` 分支之后加 `elif self.path == "/image/understanding": self._handle_image_understanding()`
- 新增处理函数：读 `Content-Length` 后 `json.loads`，校验 `attachments[0]["type"] == "image"` 且 `content` 非空（否则 400 `invalid_request`），回 `self._json_response(200, {"text": "图里是一张发票", "model": model, "instruction": instruction})`，其中 `model` 取 `X-Model-Name` 头、缺省为 `"qwen-vl-max"`，`instruction` 取请求体同名键、缺省为 `""`
- 新增辅助 `_tmp_png()`：与 `_tmp_audio()` 同构，`suffix=".png"`，写入 `b"\x89PNG\r\n\x1a\n" + b"\x00" * 32`
- 新增用例（放在 `test_transcribe_explicit_model` 之后）：

```python
    def test_understand_image_default_instruction(self):
        result = self.client.understand_image(self._tmp_png())
        # 未指定 instruction 时客户端不下发该字段，由服务端使用默认指令
        self.assertEqual(result["instruction"], "")
        self.assertEqual(result["model"], "qwen-vl-max")

    def test_understand_image_explicit_instruction(self):
        result = self.client.understand_image(self._tmp_png(), instruction="提取文字")
        self.assertEqual(result["instruction"], "提取文字")

    def test_understand_image_explicit_model(self):
        result = self.client.understand_image(self._tmp_png(), model="my-vlm")
        self.assertEqual(result["model"], "my-vlm")
```

- [ ] **Step 3: 运行示例测试**

Run: `cd tests/examples/python && python3 -m unittest test_groot_client -v`
Expected: PASS，用例数由 25 变为 28

- [ ] **Step 4: 新建 `tests/python/test_image_understanding.py`**

结构照 `tests/python/test_speech.py`：

```python
"""对外图片解析接口系统测试（POST /image/understanding）。

运行前提：groot 服务已启动（见 conftest 的 server fixture）。
环境变量：
  GROOT_VISION_MODEL  一个已在设置中创建且能解析图片的视觉模型名；不设置时跳过成功用例。

用例点：
- 未携带 API Key（401 unauthorized）
- 非法 JSON（400 invalid_request）
- attachments 缺失或超过 1 张（400 invalid_request）
- type 不是 image（400 attachment_invalid_type）
- content 不是合法 Base64（400 attachment_decode_error）
- 纯文本内容改名 .png（400 unsupported_type）
- 显式指定不存在的模型，不回落默认视觉模型（400 invalid_model）
- 仅含 status 权限的 Key 调用（403 forbidden）；仅含 chat 权限的 Key 放行
- 未配置默认视觉模型（400 invalid_model，不回落其它类型默认）
- 正常解析 1x1 PNG（需 GROOT_VISION_MODEL）
"""
```

- 常量：`URL = f"{BASE_URL}/image/understanding"`、`VISION_MODEL = os.environ.get("GROOT_VISION_MODEL", "")`、`PNG_BASE64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"`
- fixture `key_headers(api_key)` 同 `test_speech.py`；另加 module 级 `web` 会话 fixture（登录失败时 `pytest.skip`），供改默认视觉模型的用例使用
- `class TestImageUnderstandingErrors`：TC-IMG-001..007、009，依次覆盖上面前 7 个错误用例加权限用例，均断言 `resp.status_code` 与 `resp.json()["status"]`；`test_unknown_model_no_fallback` 额外断言 `"__nope__" in body["message"]`
  - TC-IMG-001..007 对应 `test_unauthorized`、`test_bad_json`、`test_bad_attachment_count`、`test_non_image_type`、`test_bad_base64`、`test_text_renamed_png`、`test_unknown_model_no_fallback`
  - TC-IMG-009 为 `test_forbidden_without_chat_permission`：先用仅含 `status` 权限的 Key 断言 403，再用仅含 `chat` 权限的 Key 断言不被鉴权拦截（正例控制项用 `X-Model-Name: __nope__` 让请求停在模型解析阶段，不打到上游）
- `@pytest.mark.skipif(not VISION_MODEL, reason="未设置 GROOT_VISION_MODEL")` 的 `class TestImageUnderstandingSuccess`：TC-IMG-008 用 `{"attachments":[{"type":"image","name":"a.png","content":PNG_BASE64}]}` 与请求头 `X-Model-Name: VISION_MODEL` 提交，断言 200、`resp.json()["text"]` 非空、`resp.json()["model"] == VISION_MODEL`
- `class TestImageUnderstandingNoDefaultVision`：TC-IMG-010 借 Web 会话新建一个视觉模型并摘掉 `vision` 默认，断言调用返回 400 `invalid_model` 且 message 含「默认视觉模型」；`finally` 里先摘标记再删模型并恢复原默认，且只在用例本身无异常时才断言清理结果

- [ ] **Step 5: 更新 `tests/TEST_CASES.md`**

- 在 §1.13 之后新增一级小节 `### 1.14 图片解析测试`，正文列出文件 `internal/llm/image_test.go`、`internal/api/handler/image_test.go`、`internal/api/middleware/auth_test.go`，覆盖点 bullets：解析客户端（`/v1` 补齐、Bearer、三段拼接的请求体可被 `json.Unmarshal` 还原、`Content-Length` 与实际一致、大 Base64 透传、`max_completion_tokens` 为 0 不下发、`usage` 缺失/补齐、上游错误透传、非法 JSON、空内容、ctx 取消）；解析 handler（六步校验顺序、只收 image、Base64 换行、非法字符、文本改名、octet-stream 扩展名兜底、ICO 嗅探、体积上限、模型解析三态、`X-Model-Name`、默认/自定义指令、上游 502、调用方取消 499 `cancelled`、usage 有无）；权限映射（`/image/understanding` POST 为 chat、GET 为 all）
- 在 §2.29 之后新增 `### 2.30 图片解析测试`，表格三行：`TestImageUnderstandingErrors` | `test_image_understanding.py` | 覆盖 TC-IMG-001..007、009（未鉴权 401、非法 JSON、附件数量、type 非 image、Base64 解码失败、文本改名、显式模型不存在 400 `invalid_model` 不回落、仅 status 权限 403 与 chat 权限放行）；`TestImageUnderstandingSuccess` | `test_image_understanding.py` | 覆盖 TC-IMG-008（真实视觉模型解析 1x1 PNG，需 `GROOT_VISION_MODEL`），200 且 text 非空、model 为所用模型；`TestImageUnderstandingNoDefaultVision` | `test_image_understanding.py` | 覆盖 TC-IMG-010（取消默认视觉模型后调用 → 400 `invalid_model`，message 含「默认视觉模型」，不回落其它类型默认；用例结束恢复原默认）
- 更新 §五 统计表：用以下命令取实际值替换表内数字与"总计"——`grep -rhE '^func Test' --include='*_test.go' . | wc -l`（Go 函数数）、`find . -name '*_test.go' -not -path './.git/*' | wc -l`（Go 文件数）、`cd tests/python && python -m pytest --collect-only -q | tail -1`（Python 用例数，需要本机已装 pytest；仓库内可用虚拟环境 `tests/temp/venv-sse/bin/python`）、`ls tests/python/test_*.py | wc -l`（Python 系统测试文件数）。改动前基线（已实测）：Go 984 个函数 / 115 个文件；`tests/python` 收集到 404 个用例、32 个 `test_*.py`；`tests/examples/python` 1 个文件、25 个用例（本任务加 3 个变 28）。§五 的表格现有 438/61/360/28 与"约 798"均为过期数字，全部按实测替换

- [ ] **Step 6: 更新 `README.md`**

栏目顺序不可颠倒——先重编号（自下而上），再插入新小节，避免编号冲突。

1. API 列表（1279 行 `/audio/speech` 行之后）插入：

   `| \`/image/understanding\` | POST | \`chat\` | 图片解析（Base64 上传，返回文本） |`

2. 17 个标题整体顺延一号，**从文件末尾往前改**，子号随主号一起改：

   | 原 | 新 |
   |---|---|
   | 7.18 定时任务创建（通过对话） | 7.19 |
   | 7.17 GET /schedule/:id/history | 7.18 |
   | 7.16 POST /schedule/:id/archive | 7.17 |
   | 7.15 POST /schedule/:id/enable | 7.16 |
   | 7.14 POST /schedule/:id/disable | 7.15 |
   | 7.13 DELETE /schedule/:id | 7.14 |
   | 7.12 GET /schedule/:id | 7.13 |
   | 7.11 GET /schedule | 7.12 |
   | 7.10 GET /web/health | 7.11 |
   | 7.9.1 DELETE /sess/{sid} | 7.10.1 |
   | 7.9 GET /sess/search | 7.10 |
   | 7.8 GET /sess/history | 7.9 |
   | 7.7.1 PATCH /sess/{sid} | 7.8.1 |
   | 7.7 GET /sess/{sid} | 7.8 |
   | 7.6.1 POST /chat/{sid}/{cid}/stop | 7.7.1 |
   | 7.6 GET /chat/{sid}/{cid} | 7.7 |
   | 7.5 GET /chat/status/{sid} | 7.6 |

3. 正文中的编号引用同步改：2348 行 `7.11 ~ 7.17` → `7.12 ~ 7.18`；2390 行 `格式同 7.11 中单条任务` → `格式同 7.12 中单条任务`；2694 行 `[7.9.1](#791-delete-sesssid---删除会话)` → `[7.10.1](#7101-delete-sesssid---删除会话)`（全文唯一的 markdown 锚点）

4. 在 7.4.1 小节结束的 `---` 之后、新 7.6 之前插入（原文照抄）：

````markdown
### 7.5 POST /image/understanding - 图片解析

需要携带具有 `chat` 权限的 API Key，与 `/chat` 相同。本接口与会话无关，不要求 `X-User-ID`。

提交一张图片和一段可选的解析指令，返回模型输出的文本。请求为 `application/json`，附件结构与 `/chat` 相同：

| 字段 | 必填 | 说明 |
|------|------|------|
| `instruction` | 否 | 解析指令，如「把这张发票转成 JSON，字段：金额、日期、开票方」。省略或为空白时使用内置默认指令：请详细描述这张图片的内容；如果图片中包含文字，请完整识别并逐字输出。 |
| `attachments` | 是 | 图片附件数组，长度必须为 1 |
| `attachments[].type` | 是 | 必须为 `"image"` |
| `attachments[].name` | 是 | 文件名，扩展名用于文件头识别失败时判定类型 |
| `attachments[].content` | 是 | 图片的纯 Base64 内容，不带 `data:` 前缀 |

图片类型由文件头识别（`image/*`，含 ICO）；文件头无法识别时按扩展名兜底：`.png`、`.jpg`、`.jpeg`、`.webp`、`.gif`、`.bmp`。

**请求示例：**

```bash
# 生成不带换行的 Base64（带换行的同样可用）
BASE64=$(base64 -w 0 invoice.png)   # macOS: base64 -i invoice.png | tr -d '\n'

curl -X POST http://localhost:8080/image/understanding \
  -H "X-API-Key: 在Web界面创建的APIKey" \
  -H "Content-Type: application/json" \
  -d '{"instruction":"把这张发票转成 JSON，字段：金额、日期、开票方",
       "attachments":[{"type":"image","name":"invoice.png","content":"'"$BASE64"'"}]}'
```

**响应：**

```json
{
  "text": "金额：1280.00 元；日期：2026-10-21；开票方：某某科技有限公司",
  "model": "qwen-vl-max",
  "usage": { "prompt_tokens": 1024, "completion_tokens": 48, "total_tokens": 1072 }
}
```

- `text`：模型输出的原文，原样返回，Groot 不解析、不改写。
- `model`：实际使用的模型名。
- `usage`：上游返回 token 用量时给出，未返回时省略该字段。

**错误响应：**

| HTTP 状态码 | 错误码 | 说明 |
|---|---|---|
| 400 | `invalid_request` | 请求体非法、`attachments` 数量不为 1 |
| 400 | `attachment_invalid_type` | 附件 `type` 不是 `image` |
| 400 | `attachment_count_exceeded` / `attachment_size_exceeded` / `attachment_total_size_exceeded` / `attachment_type_not_allowed` / `attachment_missing_name` / `attachment_missing_content` | 附件校验失败，文案与 `/chat` 相同 |
| 400 | `attachment_decode_error` | `content` 不是合法 Base64 |
| 400 | `unsupported_type` | 文件头识别出明确的非图片类型，或无法识别且扩展名不匹配 |
| 400 | `invalid_model` | 模型不存在、已禁用或未配置默认视觉模型 |
| 502 | `upstream_error` | 上游图片解析服务出错，`message` 透传上游原文 |

模型用请求头 `X-Model-Name` 指定，与 `/chat` 的约定一致。模型名取用顺序为请求头 `X-Model-Name` → 默认视觉模型（在 设置 → 模型 中设置）。显式指定的模型不存在或已禁用时直接返回 400，不回落到默认视觉模型。

---
````

5. 648 行「与会话无关」清单加上本接口：`- `/audio/transcriptions`、`/audio/speech`、`/image/understanding`、`/schedule/*` 与会话无关，不要求 `X-User-ID`。`

6. 352-353 行与 489-490 行的默认模型说明补上视觉模型用途：把「ASR 模型用于音频转录，TTS 模型用于文本转语音」改为「ASR 模型用于音频转录，视觉模型用于图片解析，TTS 模型用于文本转语音」；把「ASR 模型用于音频转录（`/audio/transcriptions`，音频转文字），TTS 模型用于文本转语音（`/audio/speech`，文字转音频）」改为「ASR 模型用于音频转录（`/audio/transcriptions`，音频转文字），视觉模型用于图片解析（`/image/understanding`，图片转文字），TTS 模型用于文本转语音（`/audio/speech`，文字转音频）」；同处「调用方不指定模型时按用途取对应默认」一句补上「图片解析取默认视觉模型」

7. 附录 D 错误码速查表：

   - 2850 行 `invalid_model`：说明末尾补「，或未配置对应用途的默认模型（对话 / ASR / 视觉 / TTS）」
   - 2856 行 `attachment_invalid_type`：说明补「；`/image/understanding` 仅接受 image」
   - 2859 行 `attachment_decode_error`：说明补「（`/chat` 与 `/image/understanding`）」
   - 2861 行 `unsupported_type`：说明补「；`/image/understanding` 在文件头识别出非图片或扩展名不匹配时返回（扩展名支持 png/jpg/jpeg/webp/gif/bmp）」
   - 2877 行 `upstream_error`：`上游转录或语音合成服务出错` → `上游转录、语音合成或图片解析服务出错`

8. §8.1（2508-2524 行，`### 8.1 Python` 至 `> 完整代码及 17 个测试用例` 那一行）：代码块末尾补一段调用示例，并把 `2524` 行的用例数由 17 改为 28（先用 Step 3 的实际用例数确认；`2541` 行 Java 小节的 18 不动）：

   ```python
   # 图片解析（无状态，不产生会话）
   result = client.understand_image("invoice.png", instruction="把这张发票转成 JSON")
   print(result["text"])
   ```

   `> 完整代码及 28 个测试用例：[examples/python/](examples/python/)`

- [ ] **Step 7: 校验文档改动**

Run: `grep -nE '^#{3,4} 7\.' README.md && grep -nE '(^|[^0-9.])7\.(1[0-9]|[0-9])([^0-9.]|$)' README.md | grep -v '^[0-9]*:#'`
Expected: 出现 23 个 7.x 标题（`###` 的 7.1～7.4、7.5、7.6～7.19，加 `####` 的 7.4.1，再加三个子号 7.7.1、7.8.1、7.10.1），编号连续无重号、无跳号；正文引用只剩 7.12（两处），锚点为 `#7101-`。注意两个命令的层级：`7.4.1` 是 `####`，只数 `^### 7\.` 会漏掉它（改前 21 个、改后 22 个）

Run: `grep -c 'image/understanding' README.md`
Expected: ≥ 6（API 列表、7.5 标题、4.5 清单、默认模型规则、附录 D、8.1 示例）

- [ ] **Step 8: 全量测试与快照**

Run: `go test ./... && go build -o dist/groot ./cmd/groot`
Expected: 全部 PASS；`dist/groot` 生成

```bash
export GIT_INDEX_FILE=.git/sdd-index
git read-tree HEAD && git add -A && T=$(git write-tree)
git commit-tree "$T" -p HEAD -m "task4: README、Python 示例与测试清单补充图片解析"
unset GIT_INDEX_FILE; rm -f .git/sdd-index
```

---

## 自检记录

- **Spec 覆盖**：1.3.1 路由与鉴权 → Task 3；1.3.2 请求字段 → Task 2（`ImageUnderstandingRequest`）+ Task 4（README 字段表）；1.3.3 六步校验顺序 → Task 2 Step 4；1.3.4 响应（含 `usage` 拼接与 `total_tokens` 补齐）→ Task 1 + Task 2；1.3.5 错误码 → Task 2；1.4 上游调用（三段拼接、`Content-Length`、无 `detail`/`temperature`、`max_completion_tokens`、`upstreamTimeout`）→ Task 1；1.5 默认指令 → Task 2；1.6 与既有接口一致性 → Global Constraints；1.7 README 更新 → Task 4；1.8 测试 → Task 1/2/3 的测试步骤与 Task 4 的 pytest、示例测试；1.9 四个留给实施者的决定 → 已在本计划中定案（`defaultInstruction` 与扩展名表放 handler、日志仅失败时 `Warn`、`Usage` 独立定义在 `llm`、嗅探用 `io.ReadFull` + `io.Discard`）。
- **类型一致**：`llm.ImageRequest`/`llm.Usage`/`llm.UnderstandImage` 在 Task 1 定义、Task 2 使用，字段名 `Instruction`/`MIMEType`/`Base64`、`PromptTokens`/`CompletionTokens`/`TotalTokens` 前后一致；`types.ImageUnderstandingRequest` 只在 Task 2 定义与使用；`handler.NewImageHandler`/`ImageHandler.Serve` 在 Task 2 定义、Task 3 使用；JSON 键 `instruction`/`attachments`/`text`/`model`/`usage`/`prompt_tokens` 在 handler、README、示例代码、pytest 中同名。
- **Review Focus**：五条已分别挂到 Task 1（`Content-Length`）、Task 2（type 自判先于 `Validate`、扩展名兜底不放行非图片、Base64 换行）、Task 3（权限映射静默失败），每条都有对应的测试函数与断言。
