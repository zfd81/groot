# 语音合成字段名对齐 OpenAI 规范实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 风格指令字段全链路从 `instruct` 改名为 `instructions`（OpenAI 规范名，vLLM-Omni 识别），删除上游不识别的 `speaker` 字段，使 Web 界面配置的风格指令真正生效。

**Architecture:** 后端 `llm.SpeechRequest`、`types.SpeechRequest`、`SpeechHandler`、`checkSpeechFieldLimits` 去掉 Speaker、`Instruct→Instructions`；`internal/setting` 配置键 `speech.instruct→speech.instructions`、字段 `Instruct→Instructions`；`/web/settings/speech` JSON 键 `instruct→instructions`；前端类型、store、面板、合成请求同步，删掉 voice 双填 speaker 的逻辑；文档与测试同步。

**Tech Stack:** Go（hertz）、Vue 3 + TypeScript、pytest（用户运行）。

**Spec:** `docs/superpowers/specs/2026-10-24-text-to-speech-design.md`（1.4.2 请求字段、1.4.4 llm 客户端、1.5 朗读配置、1.6 合成与播放、2.4 字段名对齐）

## Global Constraints

- 直接在 `master` 工作区开发，不建分支/worktree，不自动 commit；「记录快照」用 `git add -A && T=$(git write-tree) && git commit-tree $T -p HEAD -m "<msg>"; git reset -q`。
- 本机 go 命令加前缀 `env -u GOROOT`。
- 命名一致性：Go 字段 `Instructions`、JSON `instructions`、配置键 `speech.instructions`、前端属性 `instructions`，全程同名；`Voice`/`voice`/`speech.voice` 不变。
- 长度上限：`voice` 64、`language` 16、`instructions` 500（按 rune 计）；超出 400 `invalid_request`，message「<字段> 超过 N 字符上限」（字段名用新名 `instructions`）。
- 配置表中遗留的 `speech.instruct` 行不迁移、不读取、不删除（与 `speech.speaker`、`speech.language` 一致）。
- 上游请求体键名：`model`、`input`、`voice`、`language`、`instructions`、`response_format`，空值省略；**不再出现 `speaker`、`instruct`**。
- `gofmt -l internal/` 已知会列出 5 个与本计划无关的既有文件，不处理。

## Review Focus

1. 上游请求体 JSON 中不得再含 `speaker`/`instruct` 键，且 `instructions` 原样下发 → Task 1 `llm/speech_test.go` 断言键集合。
2. 对外接口请求体带旧键 `instruct`/`speaker` 时被静默忽略、不下发、不报错（Go JSON 忽略未知字段）→ Task 1 handler 新增测试。
3. 表中遗留 `speech.instruct` 行不被读为 `Instructions` → Task 2 setting 测试。
4. 前端 `synthesize` 请求体只含 `voice`、`instructions`（非空时），不含 `speaker` → Task 3 代码检查 + vue-tsc。
5. README 对外接口参数表、错误表、curl 示例、Web 配置段四处全部改名，无 `instruct`/`speaker` 残留 → Task 4 grep 验证。

---

### Task 1: 合成链路改名（internal/llm + internal/api handler/types）

**Files:**
- Modify: `internal/llm/speech.go:14-50`
- Modify: `internal/api/types/types.go:312-321`（`SpeechRequest`）
- Modify: `internal/api/handler/speech.go:24-29,73-78,100-106,122-140`
- Test: `internal/llm/speech_test.go`、`internal/api/handler/speech_test.go`

**Interfaces:**
- Produces: `llm.SpeechRequest{Input, Voice, Language, Instructions, ResponseFormat string}`；`types.SpeechRequest{Input, Model, Voice, Language, Instructions, ResponseFormat}`（json `instructions`）；`checkSpeechFieldLimits(voice, language, instructions string) (string, bool)`；常量 `maxSpeechInstructionsRunes = 500`，删除 `maxSpeechSpeakerRunes`。Task 2 的 `PutSpeech` 调用新签名。

- [ ] **Step 1: 改写测试**
  - `internal/llm/speech_test.go`：构造请求用 `Instructions: "用轻快的语气朗读"`，删 `Speaker`；期望请求体 JSON 键为 `model/input/voice/language/instructions/response_format`，值原样；「为空不下发」表驱动用例的键列表改为 `voice`、`language`、`instructions`、`response_format`；新增断言：解析出的上游请求体 map 中不存在键 `speaker` 与 `instruct`。
  - `internal/api/handler/speech_test.go`：`TestSpeechHandler_PassesSpeakerLanguageInstruct` 改名 `TestSpeechHandler_PassesLanguageInstructions`，请求体 `{"input":"你好","voice":" vivian ","language":" zh ","instructions":" 用轻快的语气朗读 "}`，期望上游收到 `voice:"vivian"`、`language:"zh"`、`instructions:"用轻快的语气朗读"`；`TestSpeechHandler_OmitsEmptySpeakerLanguageInstruct` 改名 `..._OmitsEmptyLanguageInstructions`，键列表 `language`、`instructions`；长度上限表驱动 cases 改为 `{"voice",64},{"language",16},{"instructions",500}`；新增 `TestSpeechHandler_IgnoresLegacyFields`：请求体 `{"input":"你好","voice":"vivian","speaker":"ryan","instruct":"旧字段"}` 返回 200，上游收到的 JSON 中无 `speaker`、`instruct`、`instructions` 键。
- [ ] **Step 2: 运行确认失败**：`env -u GOROOT go test ./internal/llm/... ./internal/api/handler/... -run 'Speech' -v` → 编译失败。
- [ ] **Step 3: 实现**
  - `llm/speech.go`：`SpeechRequest` 删 `Speaker`，`Instruct→Instructions`（注释「风格指令（OpenAI 规范字段），空串不下发」）；payload 匿名结构体同步，json tag `instructions,omitempty`，删 speaker 行。
  - `types.go`：`SpeechRequest` 删 `Speaker`，`Instruct string \`json:"instruct"\`` → `Instructions string \`json:"instructions"\``。
  - `handler/speech.go`：常量块删 `maxSpeechSpeakerRunes`，`maxSpeechInstructRunes→maxSpeechInstructionsRunes`；`Serve` 中删 speaker 变量，`instruct→instructions`；`checkSpeechFieldLimits` 签名改三参，checks 表 `{"voice"},{"language"},{"instructions"}`；注释同步。
  - 此时 `internal/api/handler/setting.go` 对 `checkSpeechFieldLimits` 的四参调用会编译失败，**本任务一并把该调用改为 `checkSpeechFieldLimits(v.Voice, "", v.Instruct)`**（字段改名留给 Task 2），注释「language 不属于 Web 朗读配置，传空串占位」。
- [ ] **Step 4: 验证**：`gofmt -l internal/llm internal/api`（无输出）；`env -u GOROOT go build ./... && env -u GOROOT go test ./internal/llm/... ./internal/api/... -v | tail -5` → 全 PASS。
- [ ] **Step 5: 记录快照** `task1: 合成链路字段改名`

---

### Task 2: 朗读配置改名（internal/setting + Web 设置接口）

**Files:**
- Modify: `internal/setting/speech.go`、`defaults.go`、`settings.go`（`Speech`/`SetSpeech`）
- Modify: `internal/api/types/types.go:296-306`、`internal/api/handler/setting.go:138-182`
- Test: `internal/setting/settings_test.go`、`internal/api/handler/setting_test.go`

**Interfaces:**
- Consumes: Task 1 的 `checkSpeechFieldLimits(voice, language, instructions)`。
- Produces: `setting.SpeechSettings{Voice, Instructions string}`；`KeySpeechInstructions = "speech.instructions"`；`types.SpeechSettingsRequest/Response{Voice "voice"; Instructions "instructions"}`；GET 响应恰为 `{"voice":"","instructions":""}`。

- [ ] **Step 1: 改写测试**
  - `settings_test.go`：所有 `Instruct`→`Instructions`、`KeySpeechInstruct`→`KeySpeechInstructions`；`TestSettings_SpeechIgnoresLegacyKeys` 增加预置字面量 `"speech.instruct"="旧指令"`，断言 `Speech()` 仍返回默认值且 `SetSpeech` 后该行值不变。
  - `setting_test.go`：GET 默认 `{"voice":"","instructions":""}`；RoundTrip 请求体键 `instructions`；FieldLimits cases `{"voice",64},{"instructions",500}`，message 用新字段名；`TestSettingHandler_PutSpeechIgnoresLegacyFields` 请求体加 `"instruct":"旧"`，GET 恰为 `{"voice":"alloy","instructions":""}`。
- [ ] **Step 2: 运行确认失败**：`env -u GOROOT go test ./internal/setting/... ./internal/api/handler/... -run 'Speech' -v` → 编译失败。
- [ ] **Step 3: 实现**：`speech.go` 常量与字段改名；`defaults.go` 零值结构同步；`settings.go` 读写键改名；`types.go` 两结构体 `Instruct→Instructions`（json `instructions`）；`setting.go` `GetSpeech`/`PutSpeech` 字段改名，调用 `checkSpeechFieldLimits(v.Voice, "", v.Instructions)`。
- [ ] **Step 4: 验证**：`gofmt -l internal/setting internal/api`；`env -u GOROOT go build ./... && env -u GOROOT go test ./internal/... | grep -v "^ok\|no test files"` → 无输出；`grep -rn "Instruct\b\|instruct\"" internal/` → 仅 `settings_test.go` 遗留键字面量。
- [ ] **Step 5: 记录快照** `task2: 朗读配置字段改名`

---

### Task 3: 前端改名并去掉 speaker 双填

**Files:**
- Modify: `web/src/api/speech.ts`、`web/src/stores/speechSettings.ts`、`web/src/components/settings/SettingsModal.vue`（`speechUnchanged`、`speech.instruct` v-model）
- i18n 键名 `speechInstruct`/`speechInstructDesc` 保持不变（界面文案键，不是接口字段）。

**Interfaces:**
- Consumes: Task 2 的 `/web/settings/speech` 契约（`voice`、`instructions`）。
- Produces: `export interface SpeechSettings { voice: string; instructions: string }`；`synthesize` 请求体 `{ input, response_format: 'mp3' }` 加非空 `voice`、`instructions`，**无 `speaker`**。

- [ ] **Step 1**：`speech.ts` 接口字段 `instruct→instructions`，注释改为「风格指令（OpenAI 规范字段 instructions）」；`synthesize` 删 `body.speaker = params.voice`，`if (params.instructions) body.instructions = params.instructions`；删「同时填入两个字段」注释。
- [ ] **Step 2**：`speechSettings.ts` 初始值 `{ voice: '', instructions: '' }`；`SettingsModal.vue` `speechUnchanged` 比较 `a.instructions === b.instructions`，`v-model="speech.instructions"`。
- [ ] **Step 3: 验证**：`grep -rn "speaker\|\.instruct\b\|instruct:" web/src --include=*.ts --include=*.vue` → 仅 `SpeakerIcon` 组件引用；`cd web && npx vue-tsc -b` → exit 0。
- [ ] **Step 4: 记录快照** `task3: 前端字段改名`

---

### Task 4: 文档与系统测试同步

**Files:**
- Modify: `README.md`（约 587、1779-1804 行）、`tests/TEST_CASES.md`（约 343-345、632 行）、`tests/python/test_speech_settings.py`、`tests/python/test_speech.py`（如有 instruct/speaker 引用）

- [ ] **Step 1: README**
  - 对外接口参数表：删 `speaker` 行；`language` 说明改为「合成语种，取值由上游定义（如 vLLM-Omni 的 `Chinese`、`English`、`Auto`），省略时不下发」；`instruct` 行改 `instructions`，说明加「OpenAI 规范字段」。
  - 「均去除首尾空白后透传」句改为 `voice`、`language`、`instructions`，上限 64、16、500。
  - 错误表 400 行同步字段名。
  - curl 示例若含 `language: "zh"` 改为 `"instructions": "用轻快的语气朗读"` 示例或去掉 language。
  - 第 587 行 Web 配置段：`speech.instruct→speech.instructions`；删「合成时同时作为 voice 与 speaker 下发」改为「合成时作为规范字段 voice 下发」。
- [ ] **Step 2: pytest**：`test_speech_settings.py` 的 `SPEECH_KEYS = {"voice", "instructions"}`，所有 `instruct` 键与 docstring 改名，`test_legacy_fields_ignored` 请求体加 `"instruct": "旧"`；`test_instruct_too_long` 改名 `test_instructions_too_long`。`grep -n "instruct\b\|speaker" tests/python/test_speech.py` 如有命中同步改。`python3 -m py_compile` 两个文件。
- [ ] **Step 3: TEST_CASES.md**：343-345 行与 632 行按新字段名与新用例名改写（含「上游请求体不含 speaker/instruct」「对外接口旧字段被忽略」两条）。
- [ ] **Step 4: 验证**：`grep -rn "speaker\|instruct\b\|\binstruct\`" README.md tests/TEST_CASES.md tests/python/*.py` → 仅遗留键说明处出现 `speech.instruct`（如有）；`env -u GOROOT go build -o dist/groot ./cmd/groot`。
- [ ] **Step 5: 记录快照** `task4: 文档与系统测试同步`
