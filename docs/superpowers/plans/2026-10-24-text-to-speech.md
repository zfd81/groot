# 文本转语音（TTS）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 OpenAI 兼容的文本转语音接口 `POST /audio/speech`、默认 TTS 模型类型、Web 音色配置，以及 Web 聊天窗口的流式分句朗读与单条点播。

**Architecture:** 后端沿用转录接口的三层结构：`internal/llm.Speech` 调上游并返回音频流，`handler.SpeechHandler` 做参数校验、模型解析与流式转发，路由分别挂到 API 组与 Web 组。模型默认类型 `voice` 重命名为 `asr` 并新增 `tts`（位 8）。前端新增 `speech` Pinia store 持有切句、合成队列与单个 `HTMLAudioElement`，`chat` store 在流式增量与结束时向它喂文本，顶部栏开关与消息底部小喇叭只读写该 store。

**Tech Stack:** Go 1.x + Hertz，SQLite/MySQL 配置表；Vue 3 + Pinia + Element Plus + vue-i18n。

**Spec:** `docs/superpowers/specs/2026-10-24-text-to-speech-design.md`

## Global Constraints

- 命名一致性：同一含义在 Go、JSON、前端使用同一名字。默认类型字符串标识为 `chat` / `asr` / `vision` / `tts`；`DefaultModels` JSON 字段为 `chat` / `asr` / `vision` / `tts`。
- 配置键 `voice.model`、`voice.auto_send` 与路由 `/web/settings/voice` 保持不变；新增配置键 `speech.voice`。
- 对外接口错误响应格式 `{"status": "...", "message": "..."}`，状态码与 status 取值见 spec 1.4.3。
- 上游超时 120 秒，与转录一致；`input` 上限 4096 rune；`voice` 设置上限 64 rune。
- 所有改动直接在 `master` 工作区进行，不建分支；每个任务的"快照"步骤用 `git write-tree` + `git commit-tree` 记录悬空对象，不触碰 HEAD、索引和分支；不自动提交。
- 单元测试只用 Go 标准测试框架；前端不写自动化测试。
- README 是用户手册，接口与设置变化必须同步到 README。
- 回复用中文；Go 代码经 `gofmt`。

## Review Focus

1. `input` 为纯空白或只含 Markdown 标记（清洗后为空）：接口按 `invalid_request` 拒绝；前端切句后为空的句子直接丢弃，不发请求。测试归 Task 3（接口）与 Task 8（前端纯函数自检）。
2. 上游返回 200 但 `Content-Type` 缺失且请求未带 `response_format`：响应应回落 `audio/mpeg`，不能是空或 `text/plain`。测试归 Task 2。
3. 显式 `model` 指向一个只持有 chat 默认、未设 tts 默认的模型：仍应使用该模型（显式指定不要求持有 tts 默认），只有省略 model 时才查默认 TTS。测试归 Task 3。
4. 位 2 的既有数据在改名后仍被识别为 ASR：`ParseDefaultFlag("asr")` 返回 2，`DefaultFlag(2).String()` 返回 `"asr"`，`ParseDefaultFlag("voice")` 返回 false。测试归 Task 1。
5. 流式自动朗读中用户点击"停止生成"：`chat.stop()` 必须让 `speech.stop()` 同步执行，否则已入队的句子会在回复中断后继续播放。归 Task 9，实施者在手动验证清单中确认。

---

### Task 1: 默认类型重命名 `voice` → `asr` 并新增 `tts`

**Files:**
- Modify: `internal/repo/model.go:12-32`
- Modify: `internal/llm/service.go:33-36`
- Modify: `internal/api/handler/models.go:150-158`
- Modify: `internal/api/handler/transcription.go:87`
- Modify: `internal/api/handler/setting.go:87`
- Modify: `internal/api/types/types.go:203-207`、`:224`
- Modify: `internal/repo/model_test.go`、`internal/repo/modeldb/model_test.go`、`internal/llm/service_test.go`、`internal/api/handler/models_test.go`、`internal/api/handler/transcription_test.go`、`internal/api/handler/setting_test.go`（所有 `DefaultVoice` → `DefaultASR`，字符串 `"voice"` → `"asr"`）
- Modify: `web/src/api/types.ts:85,90,108`、`web/src/stores/meta.ts:9,23`、`web/src/components/settings/ModelsPanel.vue`、`web/src/i18n/messages/zh-cn.ts`、`web/src/i18n/messages/en.ts`
- Modify: `tests/python/test_models_api.py:150-174`（`voice` → `asr`，并复制一组 `tts` 用例）
- Modify: `tests/TEST_CASES.md:393-397,867-869`

**Interfaces:**
- Produces: `repo.DefaultASR DefaultFlag = 2`、`repo.DefaultTTS DefaultFlag = 8`、`repo.AllDefaultFlags = [DefaultChat, DefaultASR, DefaultVision, DefaultTTS]`、`DefaultFlag.String()` 返回 `"asr"` / `"tts"`；`types.DefaultModels{Chat, ASR, Vision, TTS string}` JSON `asr` / `tts`；前端 `DefaultType = 'chat' | 'asr' | 'vision' | 'tts'`，`DefaultModels.tts: string`。

- [ ] **Step 1: 在 `internal/repo/model_test.go` 中改写并新增类型测试**

```go
func TestDefaultFlag_StringParse(t *testing.T) {
    cases := []struct{ f DefaultFlag; s string }{
        {DefaultChat, "chat"}, {DefaultASR, "asr"}, {DefaultVision, "vision"}, {DefaultTTS, "tts"},
    }
    for _, c := range cases {
        if c.f.String() != c.s { t.Errorf("%d.String()=%q want %q", c.f, c.f.String(), c.s) }
        got, ok := ParseDefaultFlag(c.s)
        if !ok || got != c.f { t.Errorf("Parse(%q)=%d,%v want %d", c.s, got, ok, c.f) }
    }
    if _, ok := ParseDefaultFlag("voice"); ok { t.Error("voice 不应再是合法标识") }
    if DefaultTTS != 8 { t.Errorf("DefaultTTS=%d want 8", DefaultTTS) }
    if len(AllDefaultFlags) != 4 || AllDefaultFlags[3] != DefaultTTS { t.Error("AllDefaultFlags 应为四项且末项为 tts") }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/repo/ -run TestDefaultFlag_StringParse -v`
Expected: 编译失败，`DefaultASR`、`DefaultTTS` 未定义

- [ ] **Step 3: 修改 `internal/repo/model.go`**

把 `DefaultVoice` 改为 `DefaultASR`，在 `DefaultVision` 后加 `DefaultTTS`，`AllDefaultFlags` 加入 `DefaultTTS`，`String` / 注释同步为 `asr` / `tts`。

- [ ] **Step 4: 修改 Go 侧其余引用**

- `internal/llm/service.go` `noDefaultError.Error()`：`DefaultASR` 分支文案「未配置默认 ASR 模型，请在模型管理中设置」，新增 `DefaultTTS` 分支「未配置默认 TTS 模型，请在模型管理中设置」。
- `internal/api/types/types.go` `DefaultModels`：字段 `ASR string json:"asr"`、新增 `TTS string json:"tts"`；`ModelInfo.DefaultTypes` 注释改为「按 chat、asr、vision、tts 排列」。
- `internal/api/handler/models.go` `List`：`DefaultASR` → `defaults.ASR`，新增 `DefaultTTS` → `defaults.TTS`。
- `internal/api/handler/transcription.go`、`internal/api/handler/setting.go`：`repo.DefaultVoice` → `repo.DefaultASR`。
- 全部 `_test.go`：`DefaultVoice` → `DefaultASR`，JSON 断言中 `"voice"` → `"asr"`；`models_test.go` 中列表 `defaults` 断言补 `tts` 为空串；为 `tts` 增加一组设默认 / 取消默认 / 取消后可删除的用例，与既有 vision 用例同构。

- [ ] **Step 5: 运行 Go 测试**

Run: `go build ./... && go test ./internal/repo/... ./internal/llm/... ./internal/api/... -count=1`
Expected: 全部 PASS

- [ ] **Step 6: 修改前端类型、store 与模型面板**

- `web/src/api/types.ts`：`DefaultType = 'chat' | 'asr' | 'vision' | 'tts'`；`DefaultModels` 字段 `asr: string`、`tts: string`。
- `web/src/stores/meta.ts`：初始值与兜底对象改为 `{ chat: '', asr: '', vision: '', tts: '' }`。
- `web/src/components/settings/ModelsPanel.vue`：`'voice'` → `'asr'`，命令名 `setDefaultVoice/clearDefaultVoice` → `setDefaultASR/clearDefaultASR`；复制一组 `setDefaultTTS/clearDefaultTTS` 下拉项与一个 `el-tag`（type 用 `info`），顺序 chat、asr、vision、tts。
- i18n（zh-cn / en）：`setDefaultVoice/clearDefaultVoice/defaultVoice` 重命名为 `setDefaultASR/clearDefaultASR/defaultASR`，文案「设为默认 ASR」「取消默认 ASR」「默认 ASR」；新增 `setDefaultTTS/clearDefaultTTS/defaultTTS`：「设为默认 TTS」「取消默认 TTS」「默认 TTS」；英文 `Set as default ASR` / `Unset default ASR` / `Default ASR` 及 TTS 对应。

- [ ] **Step 7: 前端类型检查与构建**

Run: `cd web && npx vue-tsc --noEmit && npm run build`
Expected: 无类型错误，构建成功

- [ ] **Step 8: 更新 Python 系统测试与用例汇总**

- `tests/python/test_models_api.py`：`type=voice` 与 `defaults["voice"]` 改为 `asr`；新增 `test_tts_default_set_clear_and_protection`，与 asr 用例同构，`type=tts`。
- `tests/TEST_CASES.md` 393-397、867-869 行：`voice` → `asr`，补 `tts=8` 与四字段 `defaults`。

- [ ] **Step 9: 快照**

用临时索引文件生成悬空快照，不触碰真实索引、HEAD 与分支：

```bash
export GIT_INDEX_FILE=.git/sdd-index
git read-tree HEAD && git add -A && T=$(git write-tree)
git commit-tree "$T" -p HEAD -m "task1: rename voice->asr, add tts default type"
unset GIT_INDEX_FILE; rm -f .git/sdd-index
```

输出的提交哈希记入 `.superpowers/sdd/text-to-speech/progress.md`，供评审者 `git diff HEAD <hash>`。后续任务的"快照"步骤同此，只改提交消息。

---

### Task 2: `internal/llm.Speech` 上游合成调用

**Files:**
- Create: `internal/llm/speech.go`
- Create: `internal/llm/speech_test.go`
- Reuse: `internal/llm/chatmodel.go:20` `openAIBaseURL`；`internal/llm/transcription.go:115-140` `upstreamMessage`、`truncate`、`ErrUpstream`、`transcribeTimeout`

**Interfaces:**
- Produces:

```go
type SpeechRequest struct {
    Input          string
    Voice          string // 空串不下发
    ResponseFormat string // 空串不下发
}
// Speech 返回上游响应体（音频流）与 Content-Type；调用方负责 Close。
func Speech(ctx context.Context, m *repo.Model, req SpeechRequest) (io.ReadCloser, string, error)
// SpeechContentType 由 response_format 推断 MIME，未知或空返回 "audio/mpeg"。
func SpeechContentType(format string) string
```

- [ ] **Step 1: 写失败测试 `internal/llm/speech_test.go`**

用 `httptest.NewServer` 记录请求并返回音频字节，`repo.Model{Model: "tts-1", BaseURL: srv.URL, APIKey: "sk-x"}`：

```go
func TestSpeech_Success(t *testing.T)            // 请求路径 /v1/audio/speech、Authorization Bearer sk-x、JSON 含 model=tts-1 input voice response_format；返回 body 字节一致，contentType == "audio/mpeg"（上游设置）
func TestSpeech_OmitsEmptyOptionalFields(t *testing.T) // Voice/ResponseFormat 为空时 JSON 中无 voice / response_format 键
func TestSpeech_ContentTypeFallback(t *testing.T) // 上游不设 Content-Type：ResponseFormat="wav" 时返回 "audio/wav"；ResponseFormat="" 时返回 "audio/mpeg"
func TestSpeech_UpstreamError(t *testing.T)      // 上游 400 {"error":{"message":"bad voice"}} → errors.Is(err, ErrUpstream) 且 err.Error() 含 "bad voice"
func TestSpeech_ContextCanceled(t *testing.T)    // 上游 sleep，ctx 先取消 → errors.Is(err, context.Canceled)
func TestSpeech_BaseURLWithV1(t *testing.T)      // BaseURL 以 /v1 结尾不重复补齐，路径仍为 /v1/audio/speech
func TestSpeechContentType(t *testing.T)         // mp3→audio/mpeg opus→audio/ogg aac→audio/aac flac→audio/flac wav→audio/wav pcm→audio/pcm ""→audio/mpeg xyz→audio/mpeg
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/llm/ -run 'TestSpeech' -v`
Expected: 编译失败，`Speech` 未定义

- [ ] **Step 3: 实现 `internal/llm/speech.go`**

- 端点 `openAIBaseURL(m.BaseURL) + "/audio/speech"`。
- 请求体用匿名 struct + `omitempty` 序列化 `model` / `input` / `voice` / `response_format`。
- `http.Client{Timeout: transcribeTimeout}`；`ctx.Err() != nil` 时直接返回 ctx 错误。
- `resp.StatusCode >= 400`：读 `io.LimitReader(resp.Body, 1<<20)`，关闭 body，返回 `fmt.Errorf("%w: %s", ErrUpstream, upstreamMessage(body, status))`。
- 成功：`ct := resp.Header.Get("Content-Type")`，为空取 `SpeechContentType(req.ResponseFormat)`；返回 `resp.Body, ct, nil`。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/llm/... -count=1`
Expected: 全部 PASS

- [ ] **Step 5: 快照**

同 Task 1 Step 9，消息 `task2: llm.Speech upstream call`。

---

### Task 3: `SpeechHandler` 与路由、鉴权

**Files:**
- Create: `internal/api/handler/speech.go`
- Create: `internal/api/handler/speech_test.go`
- Modify: `internal/api/types/types.go`（新增 `SpeechRequest`）
- Modify: `internal/api/router.go:33,69,113`（注入 handler，注册 `/web/audio/speech` 与 `/audio/speech`）
- Modify: `internal/api/server.go`（构造 `NewSpeechHandler` 并传入 router，位置与 `transcriptionH` 相邻）
- Modify: `internal/api/middleware/auth.go:122-126`（`/audio/speech` POST → `chat` 权限）
- Modify: `internal/api/middleware/auth_test.go`（补 `/audio/speech` 用例）
- Reuse: `handler.explicitModelName` 的请求头部分、`llm.ModelService.Resolve`

**Interfaces:**
- Consumes: Task 2 的 `llm.Speech`、`llm.SpeechContentType`；Task 1 的 `repo.DefaultTTS`。
- Produces:

```go
// types.go
type SpeechRequest struct {
    Input          string `json:"input"`
    Model          string `json:"model"`
    Voice          string `json:"voice"`
    ResponseFormat string `json:"response_format"`
}
// handler/speech.go
const maxSpeechInputRunes = 4096
type SpeechHandler struct { models *llm.ModelService; log *logger.Logger }
func NewSpeechHandler(models *llm.ModelService, log *logger.Logger) *SpeechHandler
func (h *SpeechHandler) Serve(ctx context.Context, rc *app.RequestContext)
```

- [ ] **Step 1: 写失败测试 `internal/api/handler/speech_test.go`**

助手 `newSpeechHandlerForTest(t, upstreamURL string, defaultTTS bool)` 仿 `newTranscriptionHandlerForTest`，创建模型 `tts-1`（首个模型自动成 chat 默认），`defaultTTS` 为真时 `SetDefault(ctx, "tts-1", repo.DefaultTTS)`。请求用 `callJSON(h.Serve, consts.MethodPost, body, nil)`，需要请求头时在调用前 `rc.Request.Header.Set("X-Model-Name", ...)`（为此在本文件写一个 `callJSONWithHeader` 变体）。

```go
func TestSpeechHandler_Success(t *testing.T)               // 默认 tts；body {"input":"你好"}；200，响应体 == 上游字节，Content-Type == "audio/mpeg"，X-Model-Name == "tts-1"；上游收到的 JSON model=="tts-1" 且无 voice 键
func TestSpeechHandler_PassesVoiceAndFormat(t *testing.T)  // body 含 voice=alloy response_format=wav → 上游 JSON 含两者；上游未设 CT 时响应 Content-Type == "audio/wav"
func TestSpeechHandler_BadJSON(t *testing.T)               // body "{" → 400 invalid_request
func TestSpeechHandler_EmptyInput(t *testing.T)            // input "   " → 400 invalid_request
func TestSpeechHandler_InputTooLong(t *testing.T)          // 4097 个 "字" → 400 invalid_request；4096 个 → 200
func TestSpeechHandler_BadResponseFormat(t *testing.T)     // response_format "midi" → 400 invalid_request
func TestSpeechHandler_NoDefaultTTS(t *testing.T)          // defaultTTS=false，省略 model → 400 invalid_model，message 含 "TTS"
func TestSpeechHandler_ExplicitModelWithoutTTSDefault(t *testing.T) // defaultTTS=false，body model="tts-1" → 200（显式指定不要求持有 tts 默认）
func TestSpeechHandler_HeaderModelUsed(t *testing.T)       // defaultTTS=false，X-Model-Name tts-1 → 200
func TestSpeechHandler_BodyModelWinsOverHeader(t *testing.T) // 另建模型 tts-2；body model=tts-2，头 tts-1 → 上游收到 tts-2
func TestSpeechHandler_ExplicitUnknownNoFallback(t *testing.T) // defaultTTS=true，body model="nope" → 400 invalid_model
func TestSpeechHandler_UpstreamError(t *testing.T)         // 上游 500 → 502 upstream_error
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handler/ -run 'TestSpeechHandler' -v`
Expected: 编译失败

- [ ] **Step 3: 实现 `internal/api/handler/speech.go`**

- `rc.BindJSON(&req)` 失败 → 400 `invalid_request` 「请求参数错误」。
- `input := strings.TrimSpace(req.Input)`，为空 → 400 `invalid_request` 「input 不能为空」；`utf8.RuneCountInString(input) > maxSpeechInputRunes` → 400 「input 超过 4096 字符上限」。
- `response_format` 非空且不在 `{mp3, opus, aac, flac, wav, pcm}` → 400 「不支持的 response_format」。
- 模型名：`strings.TrimSpace(req.Model)` 为空则取 `X-Model-Name` 头。`h.models.Resolve(ctx, name, repo.DefaultTTS)`，三类错误 → 400 `invalid_model`，其他 → 500。
- `body, ct, err := llm.Speech(ctx, m, llm.SpeechRequest{Input: input, Voice: strings.TrimSpace(req.Voice), ResponseFormat: req.ResponseFormat})`；错误 → 502 `upstream_error`，message 为 `err.Error()`。
- 成功：`rc.Response.Header.Set("X-Model-Name", m.Name)`，`rc.SetContentType(ct)`，`rc.SetBodyStream(body, -1)`，`rc.SetStatusCode(200)`。Hertz 在响应写完后关闭 stream。

- [ ] **Step 4: 注册路由与鉴权**

- `types.go` 加 `SpeechRequest`。
- `router.go`：`NewRouter` 参数新增 `speechH *handler.SpeechHandler`；`webGroup.POST("/audio/speech", speechH.Serve)` 紧随转录；`apiGroup.POST("/audio/speech", speechH.Serve)` 紧随转录，注释「对外合成接口」。
- `server.go`：在构造 `transcriptionH` 处同样构造 `handler.NewSpeechHandler(models, log)` 并传入。
- `auth.go`：在转录判定后加 `if path == "/audio/speech" && method == "POST" { return "chat" }`；`auth_test.go` 补一条断言。

- [ ] **Step 5: 运行测试与构建**

Run: `go build -o dist/groot ./cmd && go test ./internal/api/... -count=1`
Expected: 构建成功，全部 PASS

- [ ] **Step 6: 快照**

消息 `task3: speech handler, routes, auth scope`。

---

### Task 4: `speech.voice` 配置与 `/web/settings/speech` 接口

**Files:**
- Create: `internal/setting/speech.go`
- Modify: `internal/setting/defaults.go`（`defaultSpeech()`）
- Modify: `internal/setting/settings.go`（`Speech` / `SetSpeech` 方法，放在 `SetVoiceModel` 之后）
- Modify: `internal/setting/settings_test.go`
- Modify: `internal/api/types/types.go`（`SpeechSettingsRequest` / `SpeechSettingsResponse`）
- Modify: `internal/api/handler/setting.go`（`GetSpeech` / `PutSpeech`）
- Modify: `internal/api/handler/setting_test.go`
- Modify: `internal/api/router.go:71-72`（`GET/PUT /web/settings/speech`）

**Interfaces:**
- Produces:

```go
// setting
const KeySpeechVoice = "speech.voice"
type SpeechSettings struct{ Voice string }
func (s *Settings) Speech(ctx context.Context) (SpeechSettings, error)
func (s *Settings) SetSpeech(ctx context.Context, v SpeechSettings) error
// types
type SpeechSettingsRequest struct{ Voice string `json:"voice"` }
type SpeechSettingsResponse struct{ Voice string `json:"voice"` }
// handler
const maxSpeechVoiceRunes = 64
func (h *SettingHandler) GetSpeech(ctx context.Context, rc *app.RequestContext)
func (h *SettingHandler) PutSpeech(ctx context.Context, rc *app.RequestContext)
```

- [ ] **Step 1: 写失败测试（setting 包）**

```go
func TestSettings_SpeechDefaults(t *testing.T)     // 空表 → Voice == ""
func TestSettings_SpeechFromTable(t *testing.T)    // 表中 speech.voice=alloy → Voice == "alloy"
func TestSettings_SetSpeechRoundTrip(t *testing.T) // SetSpeech{Voice:"nova"} 后 Speech 返回 nova
func TestSettings_SpeechNilRepo(t *testing.T)      // repo nil：Speech 返回默认无错，SetSpeech 返回 ErrNoSettingStore
func TestSettings_SpeechRepoError(t *testing.T)    // fake repo err → Speech 返回该错误
```

- [ ] **Step 2: 写失败测试（handler）**

```go
func TestSettingHandler_GetSpeechDefaults(t *testing.T)   // 200 {"voice":""}
func TestSettingHandler_PutSpeechRoundTrip(t *testing.T)  // PUT {"voice":" alloy "} → 200；GET → "alloy"
func TestSettingHandler_PutSpeechTooLong(t *testing.T)    // 65 个 "a" → 400 invalid_request
func TestSettingHandler_PutSpeechBadJSON(t *testing.T)    // "{" → 400 invalid_request
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/setting/ ./internal/api/handler/ -run 'Speech' -v`
Expected: 编译失败

- [ ] **Step 4: 实现**

- `speech.go` 常量与结构体；`defaults.go` 增 `defaultSpeech() SpeechSettings { return SpeechSettings{} }`。
- `settings.go` `Speech`：仿 `Voice`，从 `globalValues` 取 `KeySpeechVoice`；`SetSpeech`：`Upsert` 一行。
- `setting.go` `GetSpeech` / `PutSpeech`：PUT 对 `Voice` `TrimSpace`，超 64 rune → 400 「voice 超过 64 字符上限」。
- `router.go`：`webGroup.GET("/settings/speech", settingH.GetSpeech)`、`PUT` 同理，紧随 voice。

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/setting/... ./internal/api/... -count=1`
Expected: PASS

- [ ] **Step 6: 快照**

消息 `task4: speech.voice setting and web endpoints`。

---

### Task 5: Python 系统测试与 TEST_CASES 补录

**Files:**
- Create: `tests/python/test_speech.py`
- Create: `tests/python/test_speech_settings.py`
- Modify: `tests/TEST_CASES.md`（1.5 节加 speech 条目；2.14 节加两行）

**Interfaces:**
- Consumes: Task 3、Task 4 的接口行为。

- [ ] **Step 1: 写 `test_speech.py`**

仿 `test_transcription.py` 的 fixture 用法：

```python
class TestSpeechErrors:
    def test_unauthorized(...)            # 无 key → 401
    def test_bad_json(...)                # 400 invalid_request
    def test_empty_input(...)             # {"input":"  "} → 400 invalid_request
    def test_bad_response_format(...)     # 400 invalid_request
    def test_unknown_model_no_fallback(...)  # {"input":"hi","model":"__nope__"} → 400 invalid_model
class TestSpeechSuccess:
    @pytest.mark.skipif(not os.getenv("GROOT_TTS_MODEL"), ...)
    def test_speech_mp3(...)              # 200，Content-Type 以 audio/ 开头，body 非空，X-Model-Name 非空
```

- [ ] **Step 2: 写 `test_speech_settings.py`**

仿 `test_voice_settings.py`：GET 响应只含 `voice`；PUT 回读裁剪空白；超长 400；非法 JSON 400；未登录 401。

- [ ] **Step 3: 更新 `tests/TEST_CASES.md`**

1.5 节加「合成 handler」「speech 配置」要点；2.14 节加 `TestSpeechErrors / TestSpeechSuccess / TestSpeechSettings` 三行。

- [ ] **Step 4: 语法检查**

Run: `cd tests/python && python -m py_compile test_speech.py test_speech_settings.py`
Expected: 无输出（用户自行运行 pytest）

- [ ] **Step 5: 快照**

消息 `task5: python system tests for speech`。

---

### Task 6: README 更新

**Files:**
- Modify: `README.md:333,351-353,465,487-491`（默认类型三种 → 四种，ASR / TTS 用词）
- Modify: `README.md:584`（设置说明：加「语音朗读」分组与音色）
- Modify: `README.md:1276`（API 列表加 `/audio/speech`）
- Modify: `README.md:1724-1763` 之后新增 `### 7.4.1 POST /audio/speech - 文本转语音`
- Modify: `README.md` Web 界面章节（朗读总开关与消息点播一段）

- [ ] **Step 1: 改写默认类型描述**

把「默认对话 / 语音 / 视觉模型」统一改为「默认对话 / ASR / 视觉 / TTS 模型」，补一句「ASR 模型用于音频转录，TTS 模型用于文本转语音」。

- [ ] **Step 2: 新增 7.4.1 小节**

包含：参数表（`input` 必填 ≤4096 字符、`model`、`voice`、`response_format` 允许值）、模型取用顺序（JSON `model` → `X-Model-Name` → 默认 TTS 模型）、curl 示例（`-o out.mp3`）、响应说明（音频二进制、`Content-Type`、`X-Model-Name`）、错误码表（400 invalid_request / invalid_model、502 upstream_error）。

- [ ] **Step 3: 设置与 Web 界面说明**

- 4.3 配置说明处补：「语音朗读」分组含「音色」，存配置表 `speech.voice`，留空用上游默认音色。
- Web 界面章节补：设置默认 TTS 模型后顶部栏出现喇叭开关（默认开、状态存浏览器本地），回复流式输出时按句朗读；每条回复底部小喇叭可点播 / 停止。

- [ ] **Step 4: 快照**

消息 `task6: README for speech API and settings`。

---

### Task 7: 前端音色设置与 speech API 封装

**Files:**
- Create: `web/src/api/speech.ts`
- Create: `web/src/stores/speechSettings.ts`
- Modify: `web/src/components/settings/SettingsModal.vue`（语音输入分组后新增「语音朗读」分组）
- Modify: `web/src/i18n/messages/zh-cn.ts`、`web/src/i18n/messages/en.ts`

**Interfaces:**
- Produces:

```ts
// api/speech.ts
export interface SpeechSettings { voice: string }
export const speechApi = {
  synthesize(input: string, voice: string, signal?: AbortSignal): Promise<Blob>, // POST /web/audio/speech {input, voice?, response_format:"mp3"}；非 2xx 抛 ApiError
  getSettings(): Promise<SpeechSettings>,   // GET /web/settings/speech
  saveSettings(s: SpeechSettings): Promise<{status: string}>,
}
// stores/speechSettings.ts
export const useSpeechSettingsStore // { settings: Ref<SpeechSettings>, loaded, load(), reload(), save(next) } 与 voice store 同构
```

- [ ] **Step 1: 实现 `api/speech.ts`**

`synthesize` 用 `fetch` 发 JSON（不能用 `api.post`，响应是二进制）：`voice` 为空不放进 body；401 调用 `notifyUnauthorized()` 并抛 `ApiError(401)`；非 2xx 解析 JSON 取 `message` 抛 `ApiError(status, message, data?.status)`；成功返回 `resp.blob()`。

- [ ] **Step 2: 实现 `stores/speechSettings.ts`**

照 `stores/voice.ts` 复制改名，初始 `{ voice: '' }`。

- [ ] **Step 3: 设置面板新增分组**

在 `SettingsModal.vue` 「语音」分组之后加 `group-title` 为 `t('settings.configSpeech')` 的分组，一行：标题 `t('settings.speechVoice')`、描述 `t('settings.speechVoiceDesc')`，控件 `el-input`（`v-model="speech.voice"`，`maxlength=64`，`clearable`，`@blur="saveSpeech"` 与 `@keyup.enter="saveSpeech"`），保存逻辑仿 `saveVoice`（失败提示 `settings.speechSaveFailed` 并 `reload`）。加载时机与 `loadVoice` 同处。

- [ ] **Step 4: i18n**

zh-cn：`configSpeech: '语音朗读'`、`speechVoice: '音色'`、`speechVoiceDesc: '朗读使用的音色，如 alloy、nova、shimmer；不同厂商音色名不同，留空使用上游默认音色'`、`speechSaveFailed: '保存失败：{msg}'`。en：`Speech playback` / `Voice` / `Voice used for playback, e.g. alloy, nova, shimmer. Names vary by provider; leave empty for the upstream default` / `Save failed: {msg}`。

- [ ] **Step 5: 构建检查**

Run: `cd web && npx vue-tsc --noEmit && npm run build`
Expected: 通过

- [ ] **Step 6: 快照**

消息 `task7: web speech api, settings store and voice field`。

---

### Task 8: 文本清洗与切句纯函数

**Files:**
- Create: `web/src/utils/speechText.ts`

**Interfaces:**
- Produces:

```ts
export function stripMarkdown(text: string): string
// 从 text[from..] 起切出所有完整句子；返回句子数组（已 stripMarkdown、去空白、过滤空句）与新的消费偏移。
// 未闭合的 ``` 围栏之后的内容不切；flush=true 时把剩余全部文本作为最后一句（流结束）。
export function extractSentences(text: string, from: number, flush: boolean): { sentences: string[]; next: number }
```

- [ ] **Step 1: 实现 `stripMarkdown`**

按 spec 1.6.3 规则，顺序：移除围栏代码块（正则 /```[\s\S]*?```/g，未闭合的 /```[\s\S]*$/ 也移除）→ 移除图片 `![..](..)` → 链接保留文字 → 移除表格行（以 `|` 开头或 `|---|` 形式的整行）→ 去掉行首 `#+ `、`> `、`[-*+] `、`\d+\. ` → 行内代码去反引号 → 去 `**`、`__`、`*`、`_`、`~~` 标记 → 连续空白压一个空格并 trim。

- [ ] **Step 2: 实现 `extractSentences`**

- 先判断 `text.slice(from)` 内 ``` 的出现次数是否为奇数；为奇数且 `!flush`，只处理到最后一个未闭合围栏开始之前的部分。
- 边界正则：`/[。！？；!?;\n]|\.(?=\s|$)/g`，从 `from` 起每个匹配结尾作为一个句子终点。
- 每段 `stripMarkdown` 后非空则收入 `sentences`；`next` 为最后一个边界之后的位置（`flush` 时为 `text.length`，剩余非空段也收入）。

- [ ] **Step 3: 浏览器控制台自检（无自动化测试）**

在 `web` 目录 `npx tsx -e` 或 node 内导入执行以下断言，记录到任务汇报：

```ts
stripMarkdown('# 标题\n\n这是**重点**和`code`，见[链接](http://x)。\n```js\nlet a=1\n```\n| a | b |\n|---|---|') === '标题 这是重点和code，见链接。'
extractSentences('你好。世界', 0, false)  // sentences ['你好。'], next 3
extractSentences('你好。世界', 0, true)   // ['你好。','世界'], next 5
extractSentences('A e.g. b. C', 0, false) // 允许 'A e.g.' 'b.' 误切，不要求完美
extractSentences('前文。```js\nx\n', 0, false) // ['前文。']，围栏内不切
stripMarkdown('**')  === ''              // 全标记清洗后为空，调用方丢弃
```

- [ ] **Step 4: 构建检查**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 通过

- [ ] **Step 5: 快照**

消息 `task8: speechText strip and sentence split`。

---

### Task 9: `speech` 播放器 store 与 chat store 接线

**Files:**
- Create: `web/src/stores/speech.ts`
- Modify: `web/src/stores/chat.ts`（`ChatMessage.id`、消息创建处生成 id、`applyEvent` 与 `finally` 喂文本、`stop`/`newSession`/`openSession` 调 `speech.stop()`）
- Modify: `web/src/stores/meta.ts`（`defaults.tts` 变空时调 `speech.stop()`，在 `load` 末尾 watch 或直接判断）

**Interfaces:**
- Consumes: Task 7 `speechApi.synthesize`、`useSpeechSettingsStore`；Task 8 `extractSentences`、`stripMarkdown`。
- Produces:

```ts
export const SPEECH_AUTOPLAY_KEY = 'groot.speech.autoplay'
export const useSpeechStore // {
//   autoplay: Ref<boolean>, playingId: Ref<string|null>,
//   setAutoplay(v: boolean): void,
//   feedStreaming(id: string, content: string, done: boolean): void,
//   playMessage(id: string, content: string): void,
//   stop(): void, unlock(): void }
// chat.ts
export interface ChatMessage { id: string; ... }  // 其余字段不变
```

- [ ] **Step 1: 实现 `stores/speech.ts`**

- `autoplay` 初始：`localStorage.getItem(KEY) !== '0'`（try/catch，异常按 true）；`setAutoplay` 写 `'1'`/`'0'`（try/catch），为 false 时调 `stop()`。
- 内部状态：`queue: Task[]`（`{ text, status: 'pending'|'fetching'|'ready'|'playing'|'done'|'failed', url?: string }`）、单个 `audio = new Audio()`、`offset = 0`、`streamingId: string|null`（正在自动朗读的流式消息）、`abandonedId: string|null`、`warned = false`、每次 `stop()` 递增的 `generation` 计数用于丢弃过期的 fetch 结果。`stop()` 不清 `streamingId` 与 `abandonedId`；下一条流式消息首次 `feedStreaming` 时把 `abandonedId` 置空。
- `feedStreaming(id, content, done)`：`!autoplay` 或 `id === abandonedId` → return；若 `playingId !== id`：若 `playingId !== null` 先 `stop()`，再设 `playingId = id, offset = 0, streamingId = id`；调 `extractSentences(content, offset, done)` 入队并更新 `offset`；`done` 时 `streamingId = null`；调 `pump()`。
- `setAutoplay(false)` 时若 `streamingId !== null`，把它记入 `abandonedId`，再 `stop()`。
- `playMessage(id, content)`：若 `streamingId !== null && streamingId !== id`，把 `streamingId` 记入 `abandonedId`；`stop()`；`playingId = id`；`extractSentences(content, 0, true)` 全部入队（切句函数内部已做清洗）；`pump()`。
- `pump()`：让 `fetching+ready` 数量 ≤ 2，对 pending 任务调 `speechApi.synthesize(text, settings.voice)`；成功设 `url = URL.createObjectURL(blob)`、`ready`；失败设 `failed`、`console.warn`，首次 `ElMessage.warning(t('chat.speechFailed'))`。若当前无 playing，取队首 ready 任务 `audio.src = url; audio.play()`；`audio.onended` 释放 URL、标 done、再 `pump()`；队列全 done/failed 且（非流式或已 done）→ `playingId = null`。
- `stop()`：`generation++`；`audio.pause(); audio.removeAttribute('src')`；释放所有 url；清空队列；`offset = 0`；`playingId = null`。
- `unlock()`：`audio.muted = true; audio.play().catch(()=>{}); audio.pause(); audio.muted = false`，供发送时调用解锁自动播放。
- 导出 `stop` 同时在 `setAutoplay(false)` 内调用。

- [ ] **Step 2: 修改 `stores/chat.ts`**

- `ChatMessage` 加 `id: string`；新增 `function newMessageId(): string`（`crypto.randomUUID?.() ?? Date.now()+'-'+Math.random().toString(36).slice(2)`）；三处 `messages.value.push({...})`（send 的 user/assistant、openSession 的两处）都加 `id: newMessageId()`。
- `send` 开头调 `speech.unlock()`；`applyEvent` 的 `if (ev.content)` 分支后加 `speech.feedStreaming(msg.id, msg.content, false)`；`finally` 中 `live.streaming = false` 之后加 `speech.feedStreaming(live.id, live.content, true)`。
- `stop()`、`newSession()`、`openSession()` 开头加 `speech.stop()`。
- `speech` 通过 `useSpeechStore()` 在 store 函数体内获取。

- [ ] **Step 3: `meta.ts` 联动**

`load` 结束后若 `defaults.value.tts === ''` 调 `useSpeechStore().stop()`。

- [ ] **Step 4: i18n**

zh-cn chat：`speechFailed: '朗读失败，已跳过该句'`；en：`Speech synthesis failed, sentence skipped`。

- [ ] **Step 5: 构建检查**

Run: `cd web && npx vue-tsc --noEmit && npm run build`
Expected: 通过

- [ ] **Step 6: 快照**

消息 `task9: speech player store and chat wiring`。

---

### Task 10: 顶部栏总开关与消息底部点播按钮

**Files:**
- Modify: `web/src/views/ChatView.vue:150-174`（topbar 新增喇叭按钮）
- Modify: `web/src/components/chat/MessageFooter.vue`（完成态新增小喇叭）
- Modify: `web/src/i18n/messages/zh-cn.ts`、`en.ts`

**Interfaces:**
- Consumes: Task 9 `useSpeechStore`（`autoplay`、`playingId`、`setAutoplay`、`playMessage`、`stop`）、`useMetaStore().defaults.tts`、`ChatMessage.id`。

- [ ] **Step 1: ChatView 总开关**

在日志按钮之前加：

```vue
<button v-if="meta.defaults.tts" class="topbar-logs" type="button"
  :title="speech.autoplay ? t('chat.speechAutoOn') : t('chat.speechAutoOff')"
  @click="speech.setAutoplay(!speech.autoplay)">
  <el-icon :size="17"><component :is="speech.autoplay ? Microphone : Mute" /></el-icon>
</button>
```

图标用 `@element-plus/icons-vue` 的 `Headset`/`Mute` 或 `Microphone`/`Mute`，实施者在 Element Plus 图标集中选最接近喇叭的一对（优先 `Headset` 与 `Mute`）。

- [ ] **Step 2: MessageFooter 点播按钮**

完成态复制按钮之后加：

```vue
<button v-if="meta.defaults.tts && message.content" class="copy-btn" type="button"
  :class="{ playing: isPlaying }"
  :title="isPlaying ? t('chat.speechStop') : t('chat.speechPlay')"
  @click="isPlaying ? speech.stop() : speech.playMessage(message.id, message.content)">
  <el-icon :size="15"><Headset /></el-icon>
</button>
```

`const isPlaying = computed(() => speech.playingId === props.message.id)`；`.copy-btn.playing` 加 CSS `animation: pulse 1s ease-in-out infinite`（opacity 1↔0.4）与主色。

- [ ] **Step 3: i18n**

zh-cn chat：`speechAutoOn: '自动朗读：开'`、`speechAutoOff: '自动朗读：关'`、`speechPlay: '朗读'`、`speechStop: '停止朗读'`。en：`Auto read: on` / `Auto read: off` / `Read aloud` / `Stop reading`。

- [ ] **Step 4: 构建**

Run: `cd web && npx vue-tsc --noEmit && npm run build && cd .. && go build -o dist/groot ./cmd`
Expected: 通过

- [ ] **Step 5: 手动验证清单（写入任务汇报）**

启动 `dist/groot`，浏览器登录：

1. 未设默认 TTS：顶部无喇叭、消息底部无喇叭。
2. 设默认 TTS 后刷新：顶部喇叭出现、默认开；发送问题，回复流式过程中第一句播完前不等整段结束。
3. 流式中点击「停止生成」：播放立即停止（Review Focus 5）。
4. 关闭总开关刷新页面仍为关；打开后下一轮自动朗读。
5. 点某条历史消息小喇叭：播放并带动效，再点停止；播放中点另一条切换。
6. 流式自动朗读中点别的消息小喇叭：自动朗读放弃，之后该轮不再续播。
7. 设置页音色填 `nova` 后朗读，抓包确认请求体含 `voice:"nova"` 与 `response_format:"mp3"`。

- [ ] **Step 6: 快照**

消息 `task10: autoplay toggle and per-message play button`。

---

## 自检记录

- Spec 覆盖：1.3 → Task 1；1.4 → Task 2、3；1.5 → Task 4、7；1.6.1–1.6.2 → Task 9、10；1.6.3 → Task 8、9；1.6.4 → Task 10；1.6.5–1.6.6 → Task 8、9；1.7 → Task 6；1.8 → Task 1–5。
- 类型一致：`repo.DefaultASR`/`DefaultTTS`、`types.DefaultModels.ASR/TTS`、前端 `asr`/`tts`、`speechApi.synthesize(input, voice, signal?)`、`useSpeechStore` 方法名在 Task 9、10 一致；`ChatMessage.id` 在 Task 9 定义、Task 10 使用。
- Review Focus 五条已分别挂到 Task 3、2、3、1、10。
