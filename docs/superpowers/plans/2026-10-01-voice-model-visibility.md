# 语音识别模型与话筒可见性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 去掉 `voice.enabled`，由识别模型 `voice.model` 是否为空决定话筒是否显示；首次读取语音配置时以默认语音模型为初值写入配置表，此后与默认语音模型相互独立。

**Architecture:** `internal/setting` 的 `Voice` 额外返回 `modelSet`（表中是否存在 `voice.model` 行），新增只写 `voice.model` 的 `SetVoiceModel`；初值判定放在设置 handler（setting 包不依赖 llm）：`GET` 发现行不存在时 `Resolve(ctx, "", DefaultVoice)`，取到则写入。`PUT` 只校验非空模型。前端话筒按 `voice.model` 非空显示，所选模型不在启用列表时显示警告态，转录总是带 `X-Model-Name`。

**Tech Stack:** Go（hertz、sqlx）、Vue 3 + Pinia + Element Plus 2.14 + vue-i18n、pytest。

**设计文档:** `docs/superpowers/specs/2026-10-01-voice-model-visibility-design.md`

**全局约束:**

- 所有 commit 步骤 **需用户明确确认后执行**，禁止自动提交。
- Go 代码改完执行 `gofmt -w <文件>`。
- 编译命令：`go build -o dist/groot ./cmd/groot`。
- Task 1 完成后 `internal/api/handler` 因 `Voice` 签名变化编译失败，属预期；Task 2 结束后恢复。
- README 只写对外 API，本次只涉及 `/web/settings/voice`（Web 自用），不改 README。
- Python 系统测试由用户运行，Claude 只编写。

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/setting/voice.go` | 修改 | 删除 `KeyVoiceEnabled`、`Enabled` 字段 |
| `internal/setting/defaults.go` | 修改 | `defaultVoice` 去掉 `Enabled` |
| `internal/setting/settings.go` | 修改 | `Voice` 返回 `modelSet`；`SetVoice` 写两键；新增 `SetVoiceModel` |
| `internal/setting/settings_test.go` | 修改 | 配置对象测试 |
| `internal/api/types/types.go` | 修改 | 请求/响应体去掉 `Enabled` |
| `internal/api/handler/setting.go` | 修改 | `GetVoice` 初值写入；`PutVoice` 简化校验 |
| `internal/api/handler/setting_test.go` | 修改 | 语音设置 handler 测试重写 |
| `web/src/api/voice.ts` | 修改 | 类型去掉 `enabled`；`transcribe` 的 `model` 必填 |
| `web/src/stores/voice.ts` | 修改 | 默认值去掉 `enabled` |
| `web/src/components/chat/ChatInput.vue` | 修改 | `showMic` / `micWarn` 新条件；转录总带模型 |
| `web/src/components/settings/SettingsModal.vue` | 修改 | 可清空下拉框、移除启用开关、自动发送随模型禁用 |
| `web/src/i18n/messages/zh-cn.ts` / `en.ts` | 修改 | 文案增删 |
| `tests/python/test_voice_settings.py` | 新建 | 语音设置接口系统测试 |
| `tests/TEST_CASES.md` | 修改 | 用例汇总 |

---

### Task 1: setting 包

**Files:**
- Modify: `internal/setting/voice.go`
- Modify: `internal/setting/defaults.go:8-16`
- Modify: `internal/setting/settings.go:204-238`
- Test: `internal/setting/settings_test.go:70-203`

- [ ] **Step 1: 改写测试**

`internal/setting/settings_test.go` 中用下列代码替换 `TestSettings_VoiceDefaults` 至 `TestSettings_SetVoiceRepoError`（第 70-203 行），`TestSettings_BootstrapCategories` 原样保留在其中：

```go
func TestSettings_VoiceDefaults(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())

	v, modelSet, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if want := defaultVoice(); v != want {
		t.Errorf("Voice = %+v, want %+v（表为空时应返回默认值）", v, want)
	}
	if modelSet {
		t.Error("表为空时 modelSet 应为 false")
	}
}

func TestSettings_VoicePartialOverride(t *testing.T) {
	f := newFakeRepo()
	f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = "whisper-1"

	s := New(config.Bootstrap{}, f)
	v, modelSet, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v.Model != "whisper-1" {
		t.Errorf("Model = %q, want whisper-1", v.Model)
	}
	if !modelSet {
		t.Error("voice.model 行存在时 modelSet 应为 true")
	}
	if v.AutoSend {
		t.Error("AutoSend 未在表中，应回落到默认值 false")
	}
}

// TestSettings_VoiceEmptyModelRowIsSet voice.model 行存在但为空串，同样视为已确定。
func TestSettings_VoiceEmptyModelRowIsSet(t *testing.T) {
	f := newFakeRepo()
	f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = ""

	v, modelSet, err := New(config.Bootstrap{}, f).Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v.Model != "" || !modelSet {
		t.Errorf("Model=%q modelSet=%v, want 空串且 modelSet=true", v.Model, modelSet)
	}
}

func TestSettings_VoiceBoolParsing(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
		{"", false},
		{"garbage", false}, // 无法解析时按默认值处理，不让脏数据导致取值失败
	}
	for _, c := range cases {
		f := newFakeRepo()
		f.data[key(repo.ScopeGlobal, "", KeyVoiceAutoSend)] = c.raw
		s := New(config.Bootstrap{}, f)
		v, _, err := s.Voice(context.Background())
		if err != nil {
			t.Fatalf("raw=%q Voice: %v", c.raw, err)
		}
		if v.AutoSend != c.want {
			t.Errorf("raw=%q AutoSend = %v, want %v", c.raw, v.AutoSend, c.want)
		}
	}
}

func TestSettings_SetVoiceRoundTrip(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	in := VoiceSettings{Model: "whisper-1", AutoSend: true}
	if err := s.SetVoice(ctx, in); err != nil {
		t.Fatalf("SetVoice: %v", err)
	}
	out, modelSet, err := s.Voice(ctx)
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if out != in || !modelSet {
		t.Errorf("回读 = %+v modelSet=%v, want %+v 且 modelSet=true", out, modelSet, in)
	}
}

// TestSettings_SetVoiceModelOnlyWritesModel SetVoiceModel 只写 voice.model，不碰 auto_send。
func TestSettings_SetVoiceModelOnlyWritesModel(t *testing.T) {
	f := newFakeRepo()
	s := New(config.Bootstrap{}, f)
	ctx := context.Background()

	if err := s.SetVoiceModel(ctx, "whisper-1"); err != nil {
		t.Fatalf("SetVoiceModel: %v", err)
	}
	if got := f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)]; got != "whisper-1" {
		t.Errorf("voice.model = %q, want whisper-1", got)
	}
	if _, ok := f.data[key(repo.ScopeGlobal, "", KeyVoiceAutoSend)]; ok {
		t.Error("SetVoiceModel 不应写入 voice.auto_send")
	}
}

func TestSettings_BootstrapCategories(t *testing.T) {
	// ……原样保留……
}

func TestSettings_NilRepoUsesDefaults(t *testing.T) {
	// 配置表不可用时（如仓库未装配），来自表的分类回落到默认值而非 panic
	s := New(config.Bootstrap{}, nil)
	v, modelSet, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v != defaultVoice() || modelSet {
		t.Errorf("Voice = %+v modelSet=%v, want 默认值且 modelSet=false", v, modelSet)
	}
}

func TestSettings_VoiceRepoError(t *testing.T) {
	f := newFakeRepo()
	f.err = errors.New("boom")
	_, _, err := New(config.Bootstrap{}, f).Voice(context.Background())
	if !errors.Is(err, f.err) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestSettings_SetVoiceNilRepo(t *testing.T) {
	err := New(config.Bootstrap{}, nil).SetVoice(context.Background(), VoiceSettings{})
	if !errors.Is(err, ErrNoSettingStore) {
		t.Fatalf("err = %v, want ErrNoSettingStore", err)
	}
}

func TestSettings_SetVoiceRepoError(t *testing.T) {
	f := newFakeRepo()
	f.err = errors.New("boom")
	err := New(config.Bootstrap{}, f).SetVoice(context.Background(), VoiceSettings{Model: "x"})
	if !errors.Is(err, f.err) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestSettings_SetVoiceModelNilRepo(t *testing.T) {
	err := New(config.Bootstrap{}, nil).SetVoiceModel(context.Background(), "x")
	if !errors.Is(err, ErrNoSettingStore) {
		t.Fatalf("err = %v, want ErrNoSettingStore", err)
	}
}

func TestSettings_SetVoiceModelRepoError(t *testing.T) {
	f := newFakeRepo()
	f.err = errors.New("boom")
	err := New(config.Bootstrap{}, f).SetVoiceModel(context.Background(), "x")
	if !errors.Is(err, f.err) {
		t.Fatalf("err = %v, want boom", err)
	}
}
```

（`TestSettings_BootstrapCategories` 不改动，上面的 `// ……原样保留……` 仅表示其位置，不要真的替换它的函数体。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/setting/... 2>&1 | head -20`
Expected: 编译失败，提示 `assignment mismatch: 3 variables but s.Voice returns 2 values`、`s.SetVoiceModel undefined`。

- [ ] **Step 3: 修改 `internal/setting/voice.go`**

整个文件替换为：

```go
// internal/setting/voice.go

package setting

// 语音配置在配置表中的键名。点号分层，镜像 YAML 的层级路径。
const (
	KeyVoiceModel    = "voice.model"
	KeyVoiceAutoSend = "voice.auto_send"
)

// VoiceSettings 语音输入配置。
type VoiceSettings struct {
	// Model Web 界面语音输入使用的识别模型，空串表示不启用语音输入。
	// 只影响界面，不影响对外转录接口的模型取用
	Model string
	// AutoSend 转录完成后是否自动发送
	AutoSend bool
}
```

- [ ] **Step 4: 修改 `internal/setting/defaults.go` 第 8-16 行**

```go
// defaultVoice 返回语音配置的默认值。
// 默认不启用：识别模型为空时界面不显示话筒按钮。
func defaultVoice() VoiceSettings {
	return VoiceSettings{
		Model:    "",
		AutoSend: false,
	}
}
```

- [ ] **Step 5: 修改 `internal/setting/settings.go` 第 204-238 行**

```go
// Voice 读取语音配置。表内缺失的字段由代码默认值填充。
// modelSet 表示配置表中是否存在 voice.model 一行：不存在说明识别模型从未确定过，
// 调用方可据此以默认语音模型作为初值；存在（含空串）则表示已确定，不再自动填充。
// 出错时返回值无意义，调用方须先检查 error。
func (s *Settings) Voice(ctx context.Context) (v VoiceSettings, modelSet bool, err error) {
	v = defaultVoice()
	if s.repo == nil {
		return v, false, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return VoiceSettings{}, false, err
	}
	if raw, ok := vals[KeyVoiceModel]; ok {
		v.Model = raw
		modelSet = true
	}
	if raw, ok := vals[KeyVoiceAutoSend]; ok {
		v.AutoSend = parseBool(raw, v.AutoSend)
	}
	return v, modelSet, nil
}

// SetVoice 整体保存语音配置的两个字段。
// 写入后即视为明确设置，之后不再跟随代码默认值变化；恢复默认需删除对应行。
func (s *Settings) SetVoice(ctx context.Context, v VoiceSettings) error {
	if s.repo == nil {
		return ErrNoSettingStore
	}
	return s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceModel, Value: v.Model},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceAutoSend, Value: strconv.FormatBool(v.AutoSend)},
	)
}

// SetVoiceModel 只写入 voice.model，用于首次读取时写入识别模型初值。
// 不碰 voice.auto_send，使其继续回落到代码默认值。
func (s *Settings) SetVoiceModel(ctx context.Context, model string) error {
	if s.repo == nil {
		return ErrNoSettingStore
	}
	return s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceModel, Value: model},
	)
}
```

- [ ] **Step 6: 格式化并运行测试**

Run: `gofmt -w internal/setting/voice.go internal/setting/defaults.go internal/setting/settings.go internal/setting/settings_test.go && go test ./internal/setting/... -v 2>&1 | tail -30`
Expected: 全部 PASS。

- [ ] **Step 7: Commit（需用户确认后执行）**

```bash
git add internal/setting/
git commit -m "feat(setting): 语音配置去掉 enabled，Voice 返回 modelSet 并新增 SetVoiceModel"
```

---

### Task 2: 接口类型与设置 handler

**Files:**
- Modify: `internal/api/types/types.go:282-295`
- Modify: `internal/api/handler/setting.go:55-119`
- Test: `internal/api/handler/setting_test.go:66-206`

- [ ] **Step 1: 改写 handler 测试**

`internal/api/handler/setting_test.go` 中删除第 66-206 行（`TestSettingHandler_GetVoiceDefaults` 至 `TestSettingHandler_DisableWithStaleModel`），替换为：

```go
// getVoice 调 GET /web/settings/voice 并解析响应。
func getVoice(t *testing.T, h *SettingHandler) types.VoiceSettingsResponse {
	t.Helper()
	rc := callJSON(h.GetVoice, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("GetVoice status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out types.VoiceSettingsResponse
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	return out
}

// voiceModelSet 读配置表中是否已存在 voice.model 一行。
func voiceModelSet(t *testing.T, h *SettingHandler) bool {
	t.Helper()
	_, set, err := h.settings.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	return set
}

func TestSettingHandler_GetVoiceDefaults(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	out := getVoice(t, h)
	if out.Model != "" || out.AutoSend {
		t.Errorf("表为空且无默认语音模型时应返回默认值，得到 %+v", out)
	}
	if voiceModelSet(t, h) {
		t.Error("无默认语音模型时不应写入 voice.model")
	}
}

// TestSettingHandler_GetVoiceInitFromDefaultVoice 首次读取以默认语音模型为初值并写入，
// 之后取消默认语音模型不影响已写入的识别模型。
func TestSettingHandler_GetVoiceInitFromDefaultVoice(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)
	ctx := context.Background()
	if err := h.models.SetDefault(ctx, "whisper-1", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}

	if out := getVoice(t, h); out.Model != "whisper-1" {
		t.Fatalf("Model = %q, want 默认语音模型 whisper-1", out.Model)
	}
	if !voiceModelSet(t, h) {
		t.Fatal("取到默认语音模型后应写入 voice.model")
	}

	if err := h.models.ClearDefault(ctx, "whisper-1", repo.DefaultVoice); err != nil {
		t.Fatalf("ClearDefault: %v", err)
	}
	if out := getVoice(t, h); out.Model != "whisper-1" {
		t.Errorf("取消默认语音模型后 Model = %q, want 仍为 whisper-1", out.Model)
	}
}

// TestSettingHandler_GetVoiceLaterDefaultVoice 首次读取时无默认语音模型，
// 之后设置了默认语音模型，再读取时取其作为初值。
func TestSettingHandler_GetVoiceLaterDefaultVoice(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	if out := getVoice(t, h); out.Model != "" {
		t.Fatalf("Model = %q, want 空串", out.Model)
	}
	if err := h.models.SetDefault(context.Background(), "whisper-1", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if out := getVoice(t, h); out.Model != "whisper-1" {
		t.Errorf("Model = %q, want whisper-1", out.Model)
	}
}

// TestSettingHandler_GetVoiceEmptyRowNoAutofill voice.model 行为空串时视为已确定，不自动填充。
func TestSettingHandler_GetVoiceEmptyRowNoAutofill(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)
	ctx := context.Background()
	if err := h.settings.SetVoiceModel(ctx, ""); err != nil {
		t.Fatalf("SetVoiceModel: %v", err)
	}
	if err := h.models.SetDefault(ctx, "whisper-1", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}

	if out := getVoice(t, h); out.Model != "" {
		t.Errorf("Model = %q, want 空串（已确定的空值不应被默认语音模型覆盖）", out.Model)
	}
}

func TestSettingHandler_PutVoiceRoundTrip(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"model":"whisper-1","auto_send":true}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PutVoice status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if out := getVoice(t, h); out.Model != "whisper-1" || !out.AutoSend {
		t.Errorf("回读 = %+v, want 全部生效", out)
	}
}

// TestSettingHandler_PutVoiceEmptyModel model 为空表示关闭语音输入，无需任何默认语音模型。
func TestSettingHandler_PutVoiceEmptyModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"model":"","auto_send":true}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !voiceModelSet(t, h) {
		t.Error("保存后应写入 voice.model 行")
	}
	if out := getVoice(t, h); out.Model != "" || !out.AutoSend {
		t.Errorf("回读 = %+v, want model 为空、auto_send 为 true", out)
	}
}

// TestSettingHandler_PutVoiceClearStaleModel 所选模型已禁用时，清空识别模型不应被拦截。
func TestSettingHandler_PutVoiceClearStaleModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"model":"","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s, want 200", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestSettingHandler_PutVoiceUnknownModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"model":"nope","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
	if !strings.Contains(string(rc.Response.Body()), "nope") {
		t.Errorf("响应应包含模型名 nope，得到 %s", rc.Response.Body())
	}
}

func TestSettingHandler_PutVoiceDisabledModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"model":"whisper-1","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400（已禁用的模型不应被选为识别模型）", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
	if !strings.Contains(string(rc.Response.Body()), "whisper-1") {
		t.Errorf("响应应包含模型名 whisper-1，得到 %s", rc.Response.Body())
	}
}

func TestSettingHandler_PutVoiceBadJSON(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{not json`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
}

func TestSettingHandler_PutVoiceTrimsModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"model":"  whisper-1  ","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PutVoice status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if out := getVoice(t, h); out.Model != "whisper-1" {
		t.Errorf("Model = %q, want 去除首尾空白后的 whisper-1", out.Model)
	}
}
```

说明：设计文档 1.5.1 中「默认语音模型已禁用时返回空串且不写入」在当前规则下无法构造（持有默认类型的模型不可禁用），不写单测；handler 代码仍处理 `ErrModelDisabled`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handler/... -run SettingHandler 2>&1 | head -20`
Expected: 编译失败（`h.settings.Voice` 返回值数量不匹配、`types.VoiceSettingsResponse` 仍有 `Enabled` 等）。

- [ ] **Step 3: 修改 `internal/api/types/types.go` 第 282-295 行**

```go
// VoiceSettingsRequest 是 PUT /web/settings/voice 的请求体。
// 两个字段整体保存，不支持部分更新：设置面板一次提交整个分区。
type VoiceSettingsRequest struct {
	Model    string `json:"model"`
	AutoSend bool   `json:"auto_send"`
}

// VoiceSettingsResponse 是 GET /web/settings/voice 的响应体。
type VoiceSettingsResponse struct {
	Model    string `json:"model"`
	AutoSend bool   `json:"auto_send"`
}
```

- [ ] **Step 4: 修改 `internal/api/handler/setting.go` 第 55-119 行**

```go
// GetVoice 处理 GET /web/settings/voice。
// 配置表中没有 voice.model 行时，说明识别模型从未确定过：存在可用的默认语音模型就把它
// 写入作为初值，此后识别模型与默认语音模型相互独立；不存在则返回空串且不写入，
// 下次读取重新判定。并发的首次读取写入的是同一个模型名，结果一致，无需加锁。
func (h *SettingHandler) GetVoice(ctx context.Context, rc *app.RequestContext) {
	v, modelSet, err := h.settings.Voice(ctx)
	if err != nil {
		h.log.Error("读取语音配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	if !modelSet {
		m, err := h.models.Resolve(ctx, "", repo.DefaultVoice)
		switch {
		case err == nil:
			if err := h.settings.SetVoiceModel(ctx, m.Name); err != nil {
				h.log.Error("写入识别模型初值失败", zap.String("model", m.Name), zap.Error(err))
				rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
				return
			}
			v.Model = m.Name
		case errors.Is(err, llm.ErrNoDefaultModel), errors.Is(err, llm.ErrModelDisabled):
			// 没有可用的默认语音模型：保持空串，不写入
		default:
			h.log.Error("查询默认语音模型失败", zap.Error(err))
			rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
			return
		}
	}
	rc.JSON(200, types.VoiceSettingsResponse{
		Model:    v.Model,
		AutoSend: v.AutoSend,
	})
}

// PutVoice 处理 PUT /web/settings/voice，整体保存两个字段。
// model 非空时必须是已存在且启用的模型，否则话筒一按就报错；
// model 为空表示关闭语音输入，不做校验，这样所选模型被删除或禁用后仍能清空。
func (h *SettingHandler) PutVoice(ctx context.Context, rc *app.RequestContext) {
	var req types.VoiceSettingsRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}

	name := strings.TrimSpace(req.Model)
	if name != "" {
		if _, err := h.models.GetByName(ctx, name); err != nil {
			if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) {
				rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
				return
			}
			h.log.Error("校验语音模型失败", zap.String("model", name), zap.Error(err))
			rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
			return
		}
	}

	err := h.settings.SetVoice(ctx, setting.VoiceSettings{
		Model:    name,
		AutoSend: req.AutoSend,
	})
	if err != nil {
		h.log.Error("保存语音配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}
```

- [ ] **Step 5: 格式化并运行测试**

Run: `gofmt -w internal/api/types/types.go internal/api/handler/setting.go internal/api/handler/setting_test.go && go test ./internal/api/handler/... -run SettingHandler -v 2>&1 | tail -40`
Expected: 全部 PASS。

- [ ] **Step 6: 全量编译与测试**

Run: `go build ./... && go test ./internal/... 2>&1 | tail -40`
Expected: 编译通过，全部 `ok`。若有其他包引用 `VoiceSettings.Enabled` 或两返回值的 `Voice`，按新签名修正。

- [ ] **Step 7: Commit（需用户确认后执行）**

```bash
git add internal/api/types/types.go internal/api/handler/setting.go internal/api/handler/setting_test.go
git commit -m "feat(api): 语音设置去掉 enabled，首次读取以默认语音模型为识别模型初值"
```

---

### Task 3: 前端

**Files:**
- Modify: `web/src/api/voice.ts`
- Modify: `web/src/stores/voice.ts:8`
- Modify: `web/src/components/chat/ChatInput.vue:21,32-46,101-102`
- Modify: `web/src/components/settings/SettingsModal.vue:43,63-72,222-241,461-499`
- Modify: `web/src/i18n/messages/zh-cn.ts`、`web/src/i18n/messages/en.ts`

- [ ] **Step 1: 修改 `web/src/api/voice.ts`**

接口类型替换为：

```ts
export interface VoiceSettings {
  // Web 界面语音输入使用的识别模型，空串表示不启用语音输入
  model: string
  auto_send: boolean
}
```

`transcribe` 签名与请求头替换为：

```ts
  // transcribe 上传音频并返回识别文本。识别模型经请求头 X-Model-Name 传递，
  // 由设置中选定的模型决定，不依赖服务端的默认语音模型。
  async transcribe(blob: Blob, filename: string, model: string): Promise<TranscriptionResult> {
    const fd = new FormData()
    fd.append('file', blob, filename)

    const resp = await fetch('/web/audio/transcriptions', {
      method: 'POST',
      body: fd,
      headers: { 'X-Model-Name': model },
      credentials: 'same-origin',
    })
```

（函数其余部分不变。）

- [ ] **Step 2: 修改 `web/src/stores/voice.ts` 第 8 行**

```ts
  const settings = ref<VoiceSettings>({ model: '', auto_send: false })
```

- [ ] **Step 3: 修改 `web/src/components/chat/ChatInput.vue`**

第 21 行：

```ts
const { models, defaultModel, agents, loaded: metaLoaded } = storeToRefs(meta)
```

第 32-46 行替换为：

```ts
// 语音输入状态。语音配置来自 store，与设置面板共享，
// 设置里改动后这里立即跟随，无需刷新页面。
const voiceStore = useVoiceStore()
const voice = computed(() => voiceStore.settings)
const transcribing = ref(false)
const { recording, duration, start, stop, cancel } = useRecorder()
const recordSupported = isRecordingSupported()

// 话筒按钮的显示条件：设置中选了识别模型 + 浏览器支持。
// 两者缺一就不渲染，而不是渲染成禁用态，避免留下一个永远点不动的按钮。
const showMic = computed(() => !!voice.value.model && recordSupported)

// 所选识别模型已被禁用或删除：话筒显示警告态，点击只提示、不录音。
// 隐藏会让使用者误以为语音输入消失却无从得知原因，警告态直接指向设置页。
// 模型列表加载完成前不判定，避免首屏短暂误报。
const micWarn = computed(
  () =>
    metaLoaded.value &&
    !!voice.value.model &&
    !models.value.some((m) => m.enabled && m.name === voice.value.model)
)
```

第 101-102 行替换为：

```ts
    // 总是经 X-Model-Name 传递设置中选定的识别模型
    const res = await voiceApi.transcribe(rec.blob, rec.filename, voice.value.model)
```

确认 `models` 在 ChatInput 中原有其他用途（模型下拉框），故保留；`defaults` 已不再使用，已从解构中去掉。若 `models` 元素类型无 `enabled` 字段导致 TS 报错，检查 `web/src/api/types.ts` 的 `ModelInfo`（应已有 `enabled: boolean`）。

- [ ] **Step 4: 修改 `web/src/components/settings/SettingsModal.vue` 脚本部分**

第 43 行：

```ts
const { models } = storeToRefs(meta)
```

第 63-72 行替换为：

```ts
// 选项为全部启用的模型（转录接口要求模型已启用）；下拉框可清空，清空即关闭语音输入
const voiceModelOptions = computed(() =>
  (models.value || []).filter((m) => m.enabled).map((m) => ({ label: m.name, value: m.name }))
)
```

第 222-241 行替换为：

```ts
// saveVoice 保存整个分区。失败时提示并从服务端重新读取，界面回到真实值。
// 保存成功不弹提示，与通用面板的语言、外观行为一致。
async function saveVoice() {
  voiceSaving.value = true
  try {
    // 写 store 而非直接调接口：成功后聊天输入框共享同一份状态，话筒随即显示或隐藏
    await voiceStore.save(voice.value)
  } catch (e: any) {
    ElMessage.error(t('settings.voiceSaveFailed', { msg: e?.message || '' }))
    await loadVoice()
  } finally {
    voiceSaving.value = false
  }
}
```

改完后在该文件搜索 `defaults`，确认已无引用。

- [ ] **Step 5: 修改 `SettingsModal.vue` 模板第 461-499 行**

```html
          <!-- 语音输入：影响聊天页话筒按钮的行为，属于界面交互偏好，
               故放在通用而非配置分区（后者承载 Agent 运行参数）。
               识别模型仅供 Web 界面使用，空串表示不启用语音输入。 -->
          <div class="config-group">
            <div class="group-title">{{ t('settings.configVoice') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.voiceModel') }}</div>
                <div class="label-desc">{{ t('settings.voiceModelDesc') }}</div>
              </div>
              <!-- 清空时取空串而非 undefined，与后端「空串 = 不启用」一致 -->
              <el-select
                v-model="voice.model"
                style="width: 220px"
                clearable
                :placeholder="t('settings.voiceModelPlaceholder')"
                :value-on-clear="''"
                @change="saveVoice"
              >
                <el-option
                  v-for="o in voiceModelOptions"
                  :key="o.value"
                  :label="o.label"
                  :value="o.value"
                />
              </el-select>
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.voiceAutoSend') }}</div>
                <div class="label-desc">{{ t('settings.voiceAutoSendDesc') }}</div>
              </div>
              <!-- 识别模型为空时保留原值，仅置为不可操作 -->
              <el-switch
                v-model="voice.auto_send"
                :loading="voiceSaving"
                :disabled="!voice.model"
                @change="saveVoice"
              />
            </div>
          </div>
```

说明：去掉了 `:empty-values="[null, undefined]"`。Element Plus 默认空值为 `['', undefined, null]`，空串被视为未选中，从而显示 placeholder；`:value-on-clear="''"` 保证清空后写回空串。所选模型已被禁用或删除时，它不在选项中，下拉框显示原值文本，使用者可据此重新选择或清空。

- [ ] **Step 6: 修改 i18n**

`web/src/i18n/messages/zh-cn.ts`：

- 第 87 行：`recordNoModel: '所选识别模型不可用，请在设置中重新选择',`
- 删除第 136-137 行 `voiceEnabled`、`voiceEnabledDesc`
- `voiceModelDesc` 改为：`voiceModelDesc: '用于音频转录的模型，留空则不显示语音输入',`，并在其后新增一行 `voiceModelPlaceholder: '未选择',`
- 删除 `voiceModelRequired` 一行
- 删除 `voiceModelFollowDefault`、`voiceModelFollowDefaultUnset` 两行

`web/src/i18n/messages/en.ts`（相同位置）：

- `recordNoModel: 'The selected transcription model is unavailable. Pick another in Settings',`
- 删除 `voiceEnabled`、`voiceEnabledDesc`
- `voiceModelDesc: 'Model used to transcribe audio; leave empty to hide voice input',`，其后新增 `voiceModelPlaceholder: 'None',`
- 删除 `voiceModelRequired`
- 删除 `voiceModelFollowDefault`、`voiceModelFollowDefaultUnset`

- [ ] **Step 7: 检查残留引用**

Run: `grep -rnE "voiceEnabled|voiceModelRequired|voiceModelFollow|\.enabled && recordSupported|voice\.enabled|voice\.value\.enabled" web/src`
Expected: 无输出。

- [ ] **Step 8: 前端构建**

Run: `cd web && npm run build 2>&1 | tail -20`
Expected: 构建成功，无 TS 类型错误。

- [ ] **Step 9: Commit（需用户确认后执行）**

```bash
git add web/src/api/voice.ts web/src/stores/voice.ts web/src/components/chat/ChatInput.vue web/src/components/settings/SettingsModal.vue web/src/i18n/messages/zh-cn.ts web/src/i18n/messages/en.ts
git commit -m "feat(web): 识别模型决定话筒可见性，移除启用语音输入开关"
```

---

### Task 4: Python 系统测试与用例汇总

**Files:**
- Create: `tests/python/test_voice_settings.py`
- Modify: `tests/TEST_CASES.md`

- [ ] **Step 1: 新建 `tests/python/test_voice_settings.py`**

```python
"""语音设置 API 系统测试（/web/settings/voice）。

识别模型 voice.model 是 Web 界面语音输入使用的模型，空串表示不启用语音输入。
本文件只验证接口契约，不依赖服务端是否已配置默认语音模型。

运行前提：groot 服务已启动，且已完成 Web 用户初始化（POST /web/setup）。
环境变量：
  GROOT_TEST_HOST / GROOT_TEST_PORT  服务地址（默认 localhost:8080，见 conftest）
  GROOT_WEB_USER / GROOT_WEB_PASS    Web 登录凭据

用例点：
- GET 响应体只含 model、auto_send
- PUT 清空 model 保存成功，回读为空串
- PUT 不存在的模型返回 400 invalid_model，消息含模型名
- PUT 非法 JSON 返回 400 invalid_request
- 未登录访问返回 401
"""
import os
import uuid

import pytest
import requests

from conftest import BASE_URL, TEST_WEB_PASS, TEST_WEB_USER

WEB_USER = os.environ.get("GROOT_WEB_USER", TEST_WEB_USER)
WEB_PASS = os.environ.get("GROOT_WEB_PASS", TEST_WEB_PASS)

VOICE_URL = f"{BASE_URL}/web/settings/voice"


@pytest.fixture(scope="module")
def web():
    """已登录的 Web 会话（Cookie 认证）；登录失败时跳过整个模块"""
    s = requests.Session()
    try:
        resp = s.post(f"{BASE_URL}/web/login", json={
            "username": WEB_USER,
            "password": WEB_PASS,
        }, timeout=10)
    except requests.RequestException as e:
        pytest.skip(f"groot 服务不可达: {e}")
    if resp.status_code != 200:
        pytest.skip(f"Web 登录失败（请设置 GROOT_WEB_USER / GROOT_WEB_PASS）: {resp.text}")
    yield s


@pytest.fixture()
def original(web):
    """记录测试前的语音配置，测试结束后恢复"""
    resp = web.get(VOICE_URL, timeout=10)
    assert resp.status_code == 200, resp.text
    saved = resp.json()
    yield saved
    web.put(VOICE_URL, json=saved, timeout=10)


class TestVoiceSettings:
    def test_get_fields(self, web, original):
        assert set(original.keys()) == {"model", "auto_send"}
        assert isinstance(original["model"], str)
        assert isinstance(original["auto_send"], bool)

    def test_clear_model(self, web, original):
        resp = web.put(VOICE_URL, json={"model": "", "auto_send": False}, timeout=10)
        assert resp.status_code == 200, resp.text

        got = web.get(VOICE_URL, timeout=10).json()
        assert set(got.keys()) == {"model", "auto_send"}
        # 清空后已写入 voice.model 行，不会再被默认语音模型自动填充
        assert got["model"] == ""
        assert got["auto_send"] is False

    def test_unknown_model(self, web, original):
        name = f"nope-{uuid.uuid4().hex[:8]}"
        resp = web.put(VOICE_URL, json={"model": name, "auto_send": False}, timeout=10)
        assert resp.status_code == 400
        body = resp.json()
        assert body["status"] == "invalid_model"
        assert name in body["message"]

    def test_bad_json(self, web):
        resp = web.put(VOICE_URL, data="{not json",
                       headers={"Content-Type": "application/json"}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_unauthorized(self):
        resp = requests.get(VOICE_URL, timeout=10)
        assert resp.status_code == 401
```

- [ ] **Step 2: 语法检查（不运行，系统测试由用户执行）**

Run: `python3 -m py_compile tests/python/test_voice_settings.py && echo OK`
Expected: `OK`。完成后删除生成的 `tests/python/__pycache__/test_voice_settings.*.pyc`（`__pycache__` 已存在，只删本次新增的文件）。

- [ ] **Step 3: 更新 `tests/TEST_CASES.md`**

1.5 节「配置对象」一行替换为：

```markdown
- 配置对象：表为空回落代码默认值且 modelSet 为假、voice.model 行存在（含空串）时 modelSet 为真、部分键覆盖、布尔解析（含 parseBool 回落表）、SetVoice 回写、SetVoiceModel 只写 voice.model、YAML 分类透传、仓库为 nil 时读用默认值 / 写返回 ErrNoSettingStore、仓库错误透传
```

1.5 节「设置 handler」一行替换为：

```markdown
- 设置 handler：无 voice.model 行且有默认语音模型时返回该模型并写入配置表，之后取消默认语音模型仍返回该模型；无默认语音模型时返回空串且不写入，之后设置默认语音模型再读取返回该模型；voice.model 行为空串时不自动填充；保存时 model 为空直接保存（含所选模型已禁用时清空）、模型不存在或已禁用拒绝且消息含模型名、非法 JSON、model 裁剪空白
```

2.14 节「语音转录测试」表格中，在 `TestTranscriptionSuccess` 行之后追加一行：

```markdown
| TestVoiceSettings | test_voice_settings.py | `GET/PUT /web/settings/voice` 请求响应体只含 model、auto_send；清空 model 保存成功且回读为空串；不存在的模型 400 invalid_model 且消息含模型名；非法 JSON 400 invalid_request；未登录 401 |
```

第三章手工验证表格末尾新增两行：

```markdown
| 话筒可见性 | 设置 → 通用 → 语音中选择识别模型后，聊天输入框立即显示话筒；清空识别模型后话筒消失，「识别后自动发送」开关置灰 |
| 识别模型失效 | 将所选识别模型禁用（需先取消其默认标记）后，话筒显示警告态，点击提示「所选识别模型不可用，请在设置中重新选择」且不录音 |
```

- [ ] **Step 4: Commit（需用户确认后执行）**

```bash
git add tests/python/test_voice_settings.py tests/TEST_CASES.md
git commit -m "test: 语音设置接口系统测试与用例汇总"
```

---

### Task 5: 最终验证

- [ ] **Step 1: Go 全量测试与静态检查**

Run: `go test ./internal/... 2>&1 | tail -40 && go vet ./internal/... && gofmt -l internal/`
Expected: 全部 `ok`，`go vet` 无输出，`gofmt -l` 无输出。

- [ ] **Step 2: 编译**

Run: `go build -o dist/groot ./cmd/groot && echo BUILD_OK`
Expected: `BUILD_OK`。

- [ ] **Step 3: 前端构建**

Run: `cd web && npm run build 2>&1 | tail -10`
Expected: 构建成功。

- [ ] **Step 4: 残留检查**

Run: `grep -rnE "KeyVoiceEnabled|voice\.enabled|VoiceSettings\{[^}]*Enabled" internal cmd web/src`
Expected: 无输出（配置表中残留的 `voice.enabled` 行不再读取，不做清理，见设计文档迭代说明）。

- [ ] **Step 5: 提示用户运行系统测试**

告知用户运行：`cd tests/python && pytest test_voice_settings.py test_transcription.py -v`，并按 `tests/TEST_CASES.md` 第三章新增两行做手工验证。
