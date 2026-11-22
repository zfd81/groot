# 语音输入按钮可用性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让话筒按钮的显示只由识别模型配置决定，所有「不能用」的原因在点击时提示；模型禁用、删除、重命名时同步维护识别模型配置。

**Architecture:** 前端把可见性与可用性拆开：`showMic` 只看 `voice.model` 非空，录音器在 `start()` 内返回并抛出不可用原因。后端在 `ModelsHandler.Update/Delete` 成功后调用 `setting.Settings` 新增的条件写入方法，清空或跟随识别模型配置。

**Tech Stack:** Go（Hertz、sqlx）、Vue 3 + TypeScript、Element Plus、vue-i18n。

**Spec:** `docs/superpowers/specs/2026-11-22-voice-mic-availability-design.md`

## Global Constraints

- 所有开发直接在 `master` 工作区进行，不创建分支或 worktree。
- 不自动 `git commit`。每个任务末尾用悬空快照记录工作区状态：临时 index + `git write-tree` + `git commit-tree`，不触碰 HEAD、索引和分支；快照哈希记入 `.superpowers/sdd/voice-mic-availability/progress.md`。
- 识别模型配置清空时写入空串，不删除配置行（行不存在会触发默认 ASR 初值写入）。
- 前端不引入测试框架；前端验证命令为 `cd web && npm run build`（含 `vue-tsc` 类型检查）。
- 设计文档分「功能设计 / 迭代说明」两部分，正面陈述与对比性表述不交叉。

## Review Focus

- 更新模型时请求体省略 `enabled` 字段：应视为启用，不得清空语音配置。（Task 2 测试）
- 同一次请求既改名又禁用：以清空为准，不能只改名。（Task 2 测试）
- 清空后 `voice.model` 行必须仍存在且值为空串：`Voice()` 返回 `modelSet == true`，避免被默认 ASR 重新填充。（Task 1、Task 2 测试）
- 语音配置指向其他模型时，禁用、删除、改名都不得影响它。（Task 2 测试）
- 非安全上下文且没有 `navigator.mediaDevices`：点击必须提示 `insecure`，而不是落入 `failed`（`getUserMedia` 的 TypeError）。（Task 3 类型检查 + 人工验证清单）

---

### Task 1: Settings 条件写入方法

**Files:**
- Modify: `internal/setting/settings.go`（`SetVoiceModel` 之后，约 248 行）
- Test: `internal/setting/settings_test.go`

**Interfaces:**
- Consumes: `Settings.Voice(ctx) (VoiceSettings, modelSet bool, error)`、`Settings.SetVoiceModel(ctx, model string) error`（均已存在）
- Produces:
  - `func (s *Settings) ClearVoiceModelIf(ctx context.Context, model string) error`
  - `func (s *Settings) RenameVoiceModelIf(ctx context.Context, oldName, newName string) error`

- [ ] **Step 1: 写失败测试**

在 `internal/setting/settings_test.go` 追加（沿用文件内 `newFakeRepo`、`key`）：

```go
func TestSettings_ClearVoiceModelIf(t *testing.T) {
	ctx := context.Background()

	t.Run("匹配时写入空串并保留行", func(t *testing.T) {
		f := newFakeRepo()
		f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = "arabic"
		s := New(config.Bootstrap{}, f)

		if err := s.ClearVoiceModelIf(ctx, "arabic"); err != nil {
			t.Fatalf("ClearVoiceModelIf: %v", err)
		}
		v, modelSet, err := s.Voice(ctx)
		if err != nil {
			t.Fatalf("Voice: %v", err)
		}
		if v.Model != "" {
			t.Errorf("Model = %q, want 空串", v.Model)
		}
		if !modelSet {
			t.Error("清空后 voice.model 行应仍存在（modelSet 为 true）")
		}
	})

	t.Run("不匹配时保持不变", func(t *testing.T) {
		f := newFakeRepo()
		f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = "whisper-1"
		s := New(config.Bootstrap{}, f)

		if err := s.ClearVoiceModelIf(ctx, "arabic"); err != nil {
			t.Fatalf("ClearVoiceModelIf: %v", err)
		}
		v, _, _ := s.Voice(ctx)
		if v.Model != "whisper-1" {
			t.Errorf("Model = %q, want whisper-1", v.Model)
		}
	})

	t.Run("无配置仓储时无操作", func(t *testing.T) {
		if err := New(config.Bootstrap{}, nil).ClearVoiceModelIf(ctx, "arabic"); err != nil {
			t.Errorf("ClearVoiceModelIf = %v, want nil", err)
		}
	})
}

func TestSettings_RenameVoiceModelIf(t *testing.T) {
	ctx := context.Background()

	t.Run("匹配时改为新名称", func(t *testing.T) {
		f := newFakeRepo()
		f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = "arabic"
		s := New(config.Bootstrap{}, f)

		if err := s.RenameVoiceModelIf(ctx, "arabic", "arabic-2"); err != nil {
			t.Fatalf("RenameVoiceModelIf: %v", err)
		}
		v, _, _ := s.Voice(ctx)
		if v.Model != "arabic-2" {
			t.Errorf("Model = %q, want arabic-2", v.Model)
		}
	})

	t.Run("不匹配时保持不变", func(t *testing.T) {
		f := newFakeRepo()
		f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = "whisper-1"
		s := New(config.Bootstrap{}, f)

		if err := s.RenameVoiceModelIf(ctx, "arabic", "arabic-2"); err != nil {
			t.Fatalf("RenameVoiceModelIf: %v", err)
		}
		v, _, _ := s.Voice(ctx)
		if v.Model != "whisper-1" {
			t.Errorf("Model = %q, want whisper-1", v.Model)
		}
	})
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/setting/... -run 'TestSettings_(ClearVoiceModelIf|RenameVoiceModelIf)' -v`
Expected: 编译失败，`s.ClearVoiceModelIf undefined` / `s.RenameVoiceModelIf undefined`

- [ ] **Step 3: 实现两个方法**

在 `internal/setting/settings.go` 的 `SetVoiceModel` 之后追加。两者共用一个条件写入辅助函数：

```go
// ClearVoiceModelIf 当 voice.model 等于 model 时写入空串。
// 写入空串而非删除行：行存在表示识别模型已确定，不再被默认 ASR 初值填充。
func (s *Settings) ClearVoiceModelIf(ctx context.Context, model string) error

// RenameVoiceModelIf 当 voice.model 等于 oldName 时改为 newName。
func (s *Settings) RenameVoiceModelIf(ctx context.Context, oldName, newName string) error
```

实现要求：
- `s.repo == nil` 时直接返回 `nil`（没有可清理的配置，不报错）。
- 读当前值用 `s.Voice(ctx)`；`modelSet == false` 或当前值不等于期望值时直接返回 `nil`。
- 匹配时调用 `s.SetVoiceModel(ctx, 新值)`。
- `RenameVoiceModelIf` 在 `oldName == newName` 时直接返回 `nil`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/setting/... -run 'TestSettings_(ClearVoiceModelIf|RenameVoiceModelIf)' -v`
Expected: PASS

- [ ] **Step 5: 记录快照**

```bash
idx=$(mktemp); GIT_INDEX_FILE="$idx" git add -A
tree=$(GIT_INDEX_FILE="$idx" git write-tree); rm -f "$idx"
git commit-tree "$tree" -m "snapshot: task 1 settings conditional voice model"
```

把输出的哈希追加到 `.superpowers/sdd/voice-mic-availability/progress.md`。

---

### Task 2: ModelsHandler 同步识别模型配置

**Files:**
- Modify: `internal/api/handler/models.go`（`ModelsHandler` 结构、构造函数、`Update`、`Delete`）
- Modify: `internal/api/server.go:105`
- Test: `internal/api/handler/models_test.go`

**Interfaces:**
- Consumes: Task 1 的 `ClearVoiceModelIf` / `RenameVoiceModelIf`
- Produces: `func NewModelsHandler(models *llm.ModelService, settings *setting.Settings, log *logger.Logger) *ModelsHandler`

- [ ] **Step 1: 改造测试辅助函数并写失败测试**

在 `internal/api/handler/models_test.go`：
- 保持 `newModelsHandlerForTest(t) *ModelsHandler` 签名不变（已有测试继续可用），改为转调新的辅助函数；
- 新增返回配置对象的辅助函数：

```go
func newModelsHandlerAndSettingsForTest(t *testing.T) (*ModelsHandler, *setting.Settings) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	settings := setting.New(config.Bootstrap{}, settingdb.New(sqlxDB, dialect))
	h := NewModelsHandler(llm.NewModelService(modeldb.New(sqlxDB, dialect)), settings, logger.NewNop())
	return h, settings
}
```

新增测试（注意：`ModelService.Create` 会把库中第一个模型设为默认并强制启用，默认模型禁止禁用/删除，所以每个用例先创建一个占位默认模型）：

```go
func modelBody(name string, enabled bool) string {
	return fmt.Sprintf(`{"name":%q,"model":%q,"base_url":"https://api.openai.com/v1",`+
		`"api_key":"sk-test-1234abcd","temperature":0,"top_p":1,"stop":[],"enabled":%t}`,
		name, name, enabled)
}

func seedDefaultModel(t *testing.T, h *ModelsHandler) {
	t.Helper()
	rc := callJSON(h.Create, consts.MethodPost, modelBody("placeholder", true), nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("创建占位默认模型 status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}
```

用例与断言：
- `TestModelsHandler_DisableClearsVoiceModel`：seed → 创建 `arabic`（启用）→ `settings.SetVoiceModel(ctx, "arabic")` → PUT `enabled:false` → 200 → `Voice` 返回 `Model == ""` 且 `modelSet == true`。
- `TestModelsHandler_DeleteClearsVoiceModel`：seed → 创建 `arabic` → 设置 `voice.model=arabic` → DELETE `/web/models/arabic` → 200 → `Voice` 返回 `Model == ""`、`modelSet == true`。
- `TestModelsHandler_RenameFollowsVoiceModel`：seed → 创建 `arabic` → 设置 `voice.model=arabic` → PUT 改名 `arabic-2`（enabled true）→ 200 → `Voice().Model == "arabic-2"`。
- `TestModelsHandler_DisableKeepsUnrelatedVoiceModel`：seed → 创建 `arabic` → 设置 `voice.model=whisper-1` → PUT 禁用 `arabic` → `Voice().Model == "whisper-1"`。
- `TestModelsHandler_UpdateWithoutEnabledKeepsVoiceModel`：seed → 创建 `arabic` → 设置 `voice.model=arabic` → PUT 请求体不含 `enabled` → `Voice().Model == "arabic"`。
- `TestModelsHandler_RenameAndDisableClearsVoiceModel`：seed → 创建 `arabic` → 设置 `voice.model=arabic` → PUT 同时改名 `arabic-2` 且 `enabled:false` → `Voice().Model == ""`。

请求体统一用 `modelBody`；省略 `enabled` 的用例单独拼一个不含该键的 JSON：

```go
body := `{"name":"arabic","model":"arabic","base_url":"https://api.openai.com/v1","api_key":"sk-test-1234abcd","stop":[]}`
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handler/... -run 'TestModelsHandler_(Disable|Delete|Rename|Update)' -v`
Expected: 编译失败（`NewModelsHandler` 参数数量不匹配 / 方法未定义）

- [ ] **Step 3: 实现同步逻辑并接入 server**

`internal/api/handler/models.go`：

```go
type ModelsHandler struct {
	models   *llm.ModelService
	settings *setting.Settings
	log      *logger.Logger
}

func NewModelsHandler(models *llm.ModelService, settings *setting.Settings, log *logger.Logger) *ModelsHandler
```

`Update` 在 `h.models.Update` 成功后调用同步函数；`Delete` 在 `h.models.Delete` 成功后调用 `h.settings.ClearVoiceModelIf(ctx, name)`。

同步规则（`Update`）：
- `m.Enabled == false` → `ClearVoiceModelIf(ctx, name)`；
- `m.Enabled == true` 且 `m.Name != name` → `RenameVoiceModelIf(ctx, name, m.Name)`；
- 其余不动。

同步写入失败只记录 `h.log.Error(...)`，不改变已返回的成功响应（模型变更已落库）。日志字段沿用现有风格：`zap.String("model", name)`、`zap.Error(err)`。

`internal/api/server.go:105` 改为：

```go
modelsH := handler.NewModelsHandler(models, settings, log)
```

需要的 import：`github.com/zfd81/groot/internal/setting`、`go.uber.org/zap`（若文件尚无）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/handler/... -v`
Expected: 全部 PASS（含既有模型测试）

- [ ] **Step 5: 记录快照**

```bash
idx=$(mktemp); GIT_INDEX_FILE="$idx" git add -A
tree=$(GIT_INDEX_FILE="$idx" git write-tree); rm -f "$idx"
git commit-tree "$tree" -m "snapshot: task 2 models handler voice sync"
```

---

### Task 3: 前端可见性与可用性拆分

**Files:**
- Modify: `web/src/composables/useRecorder.ts`
- Modify: `web/src/components/chat/ChatInput.vue`
- Modify: `web/src/i18n/messages/zh-cn.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `docs/superpowers/specs/2026-09-24-voice-input-design.md`（迭代说明追加一条）
- Modify: `docs/superpowers/specs/2026-10-01-voice-model-visibility-design.md`（迭代说明追加一条）

**Interfaces:**
- Produces:
  - `export type RecordingUnavailable = 'insecure' | 'unsupported'`
  - `export function recordingUnavailable(): RecordingUnavailable | null`
  - `RecorderError` 增加 `'insecure'`
- Consumes: 无新增依赖

- [ ] **Step 1: 改造录音器**

`web/src/composables/useRecorder.ts`：
- 删除 `isRecordingSupported()`，新增：

```ts
export type RecordingUnavailable = 'insecure' | 'unsupported'

// recordingUnavailable 返回当前环境无法录音的原因；null 表示可以录音。
// 缺少麦克风 API 时区分安全上下文：非 HTTPS/localhost 是浏览器策略，
// 而不是浏览器能力不足，两者提示不同。
export function recordingUnavailable(): RecordingUnavailable | null {
  if (typeof window === 'undefined' || typeof navigator === 'undefined') return 'unsupported'
  if (navigator.mediaDevices?.getUserMedia && audioContextCtor()) return null
  return window.isSecureContext ? 'unsupported' : 'insecure'
}
```

- `RecorderError` 类型改为 `'permission_denied' | 'no_device' | 'unsupported' | 'insecure' | 'failed'`。
- `start()` 开头、`if (ctx || starting) return` 之后加入：

```ts
const reason = recordingUnavailable()
if (reason) throw reason as RecorderError
```

保留其后 `const Ctor = audioContextCtor()` 与 `if (!Ctor) throw 'unsupported'`（`Ctor` 在下方创建 AudioContext 时仍要用）。

- [ ] **Step 2: 改造 ChatInput**

`web/src/components/chat/ChatInput.vue`：
- import 改为 `import { useRecorder, type RecorderError } from '../../composables/useRecorder'`。
- 删除 `const recordSupported = isRecordingSupported()`。
- `showMic`：`const showMic = computed(() => !!voice.value.model)`。
- 删除 `micDenied` ref 及 `if (err === 'permission_denied') micDenied.value = true`。
- `recorderErrorKey` 增加 `insecure: 'chat.recordInsecure'`。
- `micTitle` 去掉 `micWarn` 分支：`computed(() => transcribing.value ? t('chat.transcribing') : t('chat.recordStart'))`。
- 话筒按钮：删除 `:class="{ 'mic-warn': micWarn }"`；`:disabled` 改为 `transcribing || props.sending`。
- 删除 `.mic-warn` CSS 规则（约 563 行）。
- `micWarn` 计算属性保留，仅用于 `startRecording` 开头的失效提示。

- [ ] **Step 3: 补 i18n 文案**

`web/src/i18n/messages/zh-cn.ts` 的 `chat` 段、`recordUnsupported` 之后：

```ts
recordInsecure: '语音输入需通过 HTTPS 或 localhost 访问，当前页面环境不允许使用麦克风',
```

`web/src/i18n/messages/en.ts` 对应位置：

```ts
recordInsecure: 'Voice input requires HTTPS or localhost; the current page environment does not allow microphone access',
```

- [ ] **Step 4: 更新既有设计文档的迭代说明**

`docs/superpowers/specs/2026-09-24-voice-input-design.md` 与 `2026-10-01-voice-model-visibility-design.md` 的「迭代说明」各追加一条，说明：话筒可见性只由 `voice.model` 决定、环境问题在点击时提示、模型禁用/删除时清空配置、重命名跟随。指向本设计文档。

- [ ] **Step 5: 类型检查与构建**

Run: `cd web && npm run build`
Expected: 构建成功，`vue-tsc` 无错误。

- [ ] **Step 6: 人工验证清单（由使用者执行）**

在浏览器中依次确认：
1. 配置识别模型 + `localhost` 访问：话筒显示，点击可录音。
2. 配置识别模型 + 局域网 HTTP 地址访问：话筒显示，点击提示 HTTPS/localhost 文案。
3. 清空识别模型：话筒不显示。
4. 禁用已选中的识别模型后刷新：设置面板识别模型变为「未选择」，话筒消失。
5. 拒绝麦克风授权后再点：每次点击都提示权限被拒，按钮不变灰。

- [ ] **Step 7: 记录快照**

```bash
idx=$(mktemp); GIT_INDEX_FILE="$idx" git add -A
tree=$(GIT_INDEX_FILE="$idx" git write-tree); rm -f "$idx"
git commit-tree "$tree" -m "snapshot: task 3 frontend mic availability"
```

---

### Task 4: 整体验证

**Files:** 无新增；仅运行验证命令

- [ ] **Step 1: 运行后端测试**

Run: `go test ./internal/setting/... ./internal/api/handler/... -v`
Expected: 全部 PASS

- [ ] **Step 2: 运行前端构建**

Run: `cd web && npm run build`
Expected: 构建成功，无类型错误

- [ ] **Step 3: 核对工作区**

Run: `git status --short`
Expected: 只包含本计划涉及的文件；无编译产物、无临时目录。

- [ ] **Step 4: 记录快照**

```bash
idx=$(mktemp); GIT_INDEX_FILE="$idx" git add -A
tree=$(GIT_INDEX_FILE="$idx" git write-tree); rm -f "$idx"
git commit-tree "$tree" -m "snapshot: task 4 verification"
```
