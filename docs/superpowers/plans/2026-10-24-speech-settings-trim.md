# Web 朗读配置收窄（去掉说话人与语言）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Web 朗读配置（`/web/settings/speech`、配置键、设置页「语音朗读」分组）只保留「音色」与「风格指令」两个字段，彻底移除「说话人」与「语言」。

**Architecture:** 后端 `internal/setting.SpeechSettings` 收窄为 `Voice`、`Instruct` 两字段并连带收窄配置键、读写方法、`/web/settings/speech` 请求响应类型与 handler；对外合成接口 `/audio/speech` 的 `speaker`、`language` 透传参数及 `llm.SpeechRequest` 不动。前端 `SpeechSettings` 接口、store、设置面板与 i18n 去掉 `language`；合成请求继续把 `voice` 同时填入 `voice` 与 `speaker`。文档与测试同步。

**Tech Stack:** Go（hertz）、Vue 3 + TypeScript + Element Plus、pytest（系统测试，用户自行运行）。

**Spec:** `docs/superpowers/specs/2026-10-24-text-to-speech-design.md`（1.5 语音朗读配置、1.6 合成与播放、2.3 本轮再次调整）

## Global Constraints

- 所有开发直接在 `master` 工作区进行，不建分支、不建 worktree，不自动 commit（见 CLAUDE.md）；计划中的「Commit」步骤改为用 `git write-tree` + `git commit-tree` 记录悬空快照。
- 不触碰对外合成接口：`types.SpeechRequest`、`llm.SpeechRequest`、`SpeechHandler.Serve`、`checkSpeechFieldLimits` 的签名与行为保持不变（仍接受并透传 `speaker`、`language`）。
- 长度上限：`voice` 64、`instruct` 500（按 rune 计）；超出返回 400 `invalid_request`，message 为「<字段> 超过 N 字符上限」。
- 配置表中已存在的 `speech.speaker`、`speech.language` 行不迁移、不删除、不读取。
- 命名一致性：字段名 `Voice`/`Instruct`、JSON `voice`/`instruct`、配置键 `speech.voice`/`speech.instruct` 全程沿用。
- 用户手册即根目录 `README.md`，接口与配置字段变化必须同步修改。

## Review Focus

1. PUT `/web/settings/speech` 带上已废弃的 `speaker` 或 `language` 字段时应被静默忽略（Go 的 JSON 解码默认忽略未知字段），返回 200，GET 回读不含这两个键 → Task 2 增加测试。
2. 旧版前端或脚本把 `language` 存进配置表后，GET 不再返回它、朗读合成不再下发它，页面不报错 → Task 1 `Speech()` 不读取对应键即可，Task 2 测试 GET 响应体精确等于两键 JSON。
3. 前端 `speechStore.settings` 初始值与 `SpeechSettings` 类型一致，`speechUnchanged()` 只比较两字段，去掉 `language` 后类型检查（`vue-tsc`）通过 → Task 3 以 `npm run build` 验证。
4. 合成请求体仍同时含 `voice` 与 `speaker`（Qwen 系模型依赖 `speaker`），不含 `language` → Task 3 保持 `speechApi.synthesize` 对 voice 的双填逻辑，仅删 language 行。
5. README 对外接口章节（`/audio/speech` 的 `speaker`、`language` 参数）不得被误删 → Task 4 只改 Web 朗读配置相关段落。

---

### Task 1: 后端配置层收窄（internal/setting）

**Files:**
- Modify: `internal/setting/speech.go`
- Modify: `internal/setting/defaults.go:17-21`
- Modify: `internal/setting/settings.go:250-290`（`Speech`、`SetSpeech`）
- Test: `internal/setting/settings_test.go:257-350`

**Interfaces:**
- Produces: `type SpeechSettings struct { Voice string; Instruct string }`；常量仅 `KeySpeechVoice = "speech.voice"`、`KeySpeechInstruct = "speech.instruct"`；`(*Settings).Speech(ctx) (SpeechSettings, error)`、`(*Settings).SetSpeech(ctx, SpeechSettings) error` 签名不变。

- [ ] **Step 1: 改写测试为两字段**

在 `settings_test.go` 中：
- `TestSettings_SpeechFromTable`：只写入 `KeySpeechVoice="alloy"`、`KeySpeechInstruct="用轻快的语气朗读"`，期望 `SpeechSettings{Voice: "alloy", Instruct: "用轻快的语气朗读"}`。
- `TestSettings_SpeechFromTablePartial`：改为只写 `KeySpeechInstruct="slow"`，期望 `SpeechSettings{Instruct: "slow"}`。
- `TestSettings_SetSpeechRoundTrip`：`in := SpeechSettings{Voice: "nova", Instruct: "slow"}`；期望仓库中恰有 `KeySpeechVoice:"nova"`、`KeySpeechInstruct:"slow"` 两行；后续清空断言不变。
- 新增 `TestSettings_SpeechIgnoresLegacyKeys`：仓库中预置 `speech.speaker="vivian"`、`speech.language="zh"`（用字面量字符串，不引用常量），`Speech()` 返回 `defaultSpeech()`；`SetSpeech(SpeechSettings{Voice:"x"})` 后仓库中这两行值不变（未被覆盖或删除）。

- [ ] **Step 2: 运行测试确认编译失败**

Run: `go test ./internal/setting/... -run 'Speech' -v`
Expected: FAIL（`Speaker`/`Language` 字段与 `KeySpeechSpeaker` 常量仍存在时为 vet/字段不匹配错误，或新测试因读到旧键而失败）。

- [ ] **Step 3: 收窄实现**

- `speech.go`：删除 `KeySpeechSpeaker`、`KeySpeechLanguage` 常量与 `Speaker`、`Language` 字段；注释改为「两个字段」，`Voice` 注释改为「音色，各模型可选值不同」。
- `defaults.go`：`defaultSpeech()` 返回 `SpeechSettings{Voice: "", Instruct: ""}`。
- `settings.go`：`Speech()` 只读 `KeySpeechVoice`、`KeySpeechInstruct`；`SetSpeech()` 只 Upsert 这两行；注释中「四个字段」改为「两个字段」。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/setting/... -v`
Expected: PASS（全部）。此时 `go build ./...` 预期在 `internal/api/handler` 处失败，属 Task 2 范围。

- [ ] **Step 5: 记录快照**

```bash
git add -A && git write-tree && git commit-tree <tree> -p HEAD -m "task1: setting 层收窄" ; git reset -q
```
（不触碰 HEAD；把得到的 commit 哈希记入进度账本。）

---

### Task 2: Web 设置接口收窄（internal/api）

**Files:**
- Modify: `internal/api/types/types.go:296-310`
- Modify: `internal/api/handler/setting.go:138-182`（`GetSpeech`、`PutSpeech`）
- Test: `internal/api/handler/setting_test.go:249-350`

**Interfaces:**
- Consumes: Task 1 的 `setting.SpeechSettings{Voice, Instruct}`。
- Produces: `types.SpeechSettingsRequest{Voice string "json:voice"; Instruct string "json:instruct"}`、`types.SpeechSettingsResponse` 同两字段；`GET /web/settings/speech` 响应体恰为 `{"voice":"","instruct":""}`。
- 不改：`checkSpeechFieldLimits(voice, speaker, language, instruct string) (string, bool)` 签名不变，`PutSpeech` 调用时 speaker、language 传空串。

- [ ] **Step 1: 改写测试**

在 `setting_test.go` 中：
- `TestSettingHandler_GetSpeechDefaults`：`want := `{"voice":"","instruct":""}``。
- `TestSettingHandler_PutSpeechRoundTrip`：请求体 `{"voice":" alloy ","instruct":" 用轻快的语气朗读 "}`，期望 `SpeechSettingsResponse{Voice:"alloy", Instruct:"用轻快的语气朗读"}`；第二段只传 voice 的断言不变。
- `TestSettingHandler_PutSpeechFieldLimits`：cases 只留 `{"voice",64}`、`{"instruct",500}`，switch 分支相应删除 speaker/language。
- 新增 `TestSettingHandler_PutSpeechIgnoresLegacyFields`：PUT `{"voice":"alloy","speaker":"vivian","language":"zh","instruct":""}` 返回 200；GET 响应体字符串恰为 `{"voice":"alloy","instruct":""}`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handler/... -run 'Speech' -v`
Expected: 编译失败（`Speaker`/`Language` 字段不存在）。

- [ ] **Step 3: 收窄实现**

- `types.go`：两个结构体删去 `Speaker`、`Language`；注释「四个字段」改「两个字段」。
- `setting.go`：`GetSpeech` 只填 `Voice`、`Instruct`；`PutSpeech` 构造 `setting.SpeechSettings{Voice, Instruct}`，调用 `checkSpeechFieldLimits(v.Voice, "", "", v.Instruct)`；注释去掉「说话人、语种」。

- [ ] **Step 4: 运行测试与全量编译**

Run: `go build ./... && go test ./internal/setting/... ./internal/api/... -v`
Expected: 编译通过，全部 PASS（含 `speech_test.go` 中对外接口的 speaker/language 用例，证明透传未受影响）。

- [ ] **Step 5: 记录快照**（同 Task 1 方式，message `task2: web 设置接口收窄`）

---

### Task 3: 前端去掉「语言」输入框与字段

**Files:**
- Modify: `web/src/api/speech.ts:10-31`
- Modify: `web/src/stores/speechSettings.ts:8`
- Modify: `web/src/components/settings/SettingsModal.vue:244-254`（`speechUnchanged`）、`:532-585`（语音朗读分组）
- Modify: `web/src/i18n/messages/zh-cn.ts:149-150`、`web/src/i18n/messages/en.ts:149-150`

**Interfaces:**
- Consumes: Task 2 的 `/web/settings/speech` 两字段契约。
- Produces: `export interface SpeechSettings { voice: string; instruct: string }`；`speechApi.synthesize` 请求体含 `voice`、`speaker`（同值）、`instruct`，不含 `language`。

- [ ] **Step 1: 收窄类型与请求**

- `speech.ts`：`SpeechSettings` 删除 `language`，注释改为「两个字段」；`synthesize` 删除 `if (params.language) body.language = params.language`，voice 双填 `voice`/`speaker` 保留。
- `speechSettings.ts`：初始值改为 `{ voice: '', instruct: '' }`。

- [ ] **Step 2: 收窄设置面板**

- `SettingsModal.vue`：`speechUnchanged()` 删除 `a.language === b.language` 一行；注释「三个字段」改「两个字段」；删除整个「语言」`<div class="row">` 块（`t('settings.speechLanguage')` 所在块）；分组注释中「三个字段」改「两个字段」。
- 若 `speech` 的 `ref` 初始值或 `loadSpeech` 中显式写了 `language`，同步删除（用 `grep -n language SettingsModal.vue` 核对，保留界面语言 `useLanguageStore` 相关行）。

- [ ] **Step 3: 删除 i18n 文案**

`zh-cn.ts` 与 `en.ts` 删除 `speechLanguage`、`speechLanguageDesc` 两键。

- [ ] **Step 4: 验证**

Run: `grep -rn "speechLanguage\|\.language\b" web/src/api/speech.ts web/src/stores/speechSettings.ts web/src/components/settings/SettingsModal.vue web/src/i18n/messages/`
Expected: 仅剩界面语言（`useLanguageStore`、`settings.language`）相关行，无朗读语言残留。

Run: `cd web && npm run build`
Expected: vite 构建与 `vue-tsc -b` 均无错误。

- [ ] **Step 5: 记录快照**（message `task3: 前端去掉朗读语言字段`）

---

### Task 4: 文档与系统测试同步

**Files:**
- Modify: `README.md:323`、`README.md:587`
- Modify: `tests/python/test_speech_settings.py`
- Modify: `tests/TEST_CASES.md:345`、`tests/TEST_CASES.md:632`

**Interfaces:**
- Consumes: Task 2 的接口契约。

- [ ] **Step 1: README**

- 第 323 行：「音色、说话人、语言、风格指令在 设置 → 通用 …」改为「音色、风格指令在 设置 → 通用 …」。
- 第 587 行：改为只描述两个配置键：`speech.voice` 音色（音色名，不同模型可选值不同，如 OpenAI 系的 `alloy`、Qwen3-TTS 的 `vivian`，合成时同时作为 `voice` 与 `speaker` 下发）、`speech.instruct` 风格指令（朗读风格描述，仅支持指令的模型有效）。其余句子（留空不下发、只作用于 Web 界面、对外接口自行传参）保留。
- 不改 1770-1810 行对外接口 `/audio/speech` 的参数表。

- [ ] **Step 2: Python 系统测试**

`test_speech_settings.py`：
- 模块 docstring：改为「speech.voice / speech.instruct …（64 / 500）」，用例点删除 language 一条，新增「PUT 带已废弃的 speaker / language 字段被忽略，GET 不返回这两个键」。
- `SPEECH_KEYS = {"voice", "instruct"}`。
- `test_put_trims_fields`：请求体与断言去掉 speaker、language。
- 删除 `test_language_too_long`；新增 `test_legacy_fields_ignored(self, web, original)`：PUT `{"voice":"alloy","speaker":"vivian","language":"zh","instruct":""}` 返回 200，GET 的 keys 恰为 `SPEECH_KEYS`。
- 新增 `test_instruct_too_long`：`"instruct": "a" * 501` 返回 400 invalid_request，GET 回读 `instruct` 等于 `original["instruct"]`。

Run: `cd tests/python && python3 -m py_compile test_speech_settings.py`
Expected: 无输出（语法通过）。系统测试本身由用户运行。

- [ ] **Step 3: TEST_CASES.md**

- 第 345 行：speech 配置段改为「表为空回落默认值（voice / instruct 均为空串）、speech.voice / speech.instruct 两行存在时读出、只有部分行时其余回落空串、表中遗留的 speech.speaker / speech.language 行不读取且 SetSpeech 不触碰、SetSpeech 两字段整体回写…；设置 handler：GET 默认返回 `{"voice":"","instruct":""}`、PUT 两字段回读裁剪空白且未传字段清空、带已废弃 speaker / language 字段的 PUT 返回 200 且 GET 不含这两个键、voice / instruct 长度上限 64 / 500…」。第 344 行合成 handler 的描述不动。
- 第 632 行 `TestSpeechSettings`：改为「响应体恰为 voice / instruct 两个键；PUT 回读裁剪空白；voice 超过 64 字符、instruct 超过 500 字符 400 invalid_request 且不写入；已废弃的 speaker / language 字段被忽略；非法 JSON 400 invalid_request；未登录 401」。

- [ ] **Step 4: 最终验证**

Run: `grep -n "说话人" README.md docs/superpowers/specs/2026-10-24-text-to-speech-design.md web/src -r`
Expected: 仅设计文档迭代说明章节（2.x）出现。

Run: `gofmt -l internal/ && go build -o dist/groot ./cmd && go test ./internal/... 2>&1 | tail -20`
Expected: gofmt 无输出，构建成功，全部 ok。

- [ ] **Step 5: 记录快照**（message `task4: 文档与系统测试同步`）
