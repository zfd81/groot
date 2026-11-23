# 聊天模型下拉框过滤语音模型 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 聊天输入框的模型下拉框不再出现持有 ASR/TTS 默认类型标记的模型，已选模型失去对话资格时自动回落。

**Architecture:** 在 Pinia 的 `meta` store 中由现有 `models` 列表派生 `chatModels`（启用且 `default_types` 不含 `asr`/`tts`），`ChatInput.vue` 的选项与选中项回落改用它。后端接口、数据结构、`/v1/models` 不变。

**Tech Stack:** Vue 3 + Pinia + Element Plus + TypeScript；构建校验 `vite build` + `vue-tsc`。

**Spec:** `docs/superpowers/specs/2026-11-22-chat-model-selector-speech-filter-design.md`

## Global Constraints

- 一律直接在 `master` 工作区开发，不创建分支或 worktree。
- 不自动提交；每个任务结束用悬空快照对象记录工作区状态（命令见各任务步骤），全部改动留在工作区。
- 前端不引入新的测试框架或工具（CLAUDE.md 测试规范）；本计划以 `npm run build`（含 `vue-tsc` 类型检查）和人工核对作为验证手段。
- 过滤语义为方案 A：`default_types` 含 `asr` 或 `tts` 即不进入对话下拉，即使同时含 `chat`/`vision`。
- 目录与 naming：代表同一含义的标识统一用 `chatModels` / `default_types`。
- 进度账本写在 `.superpowers/sdd/chat-model-selector-speech-filter/progress.md`。

## Review Focus

- 模型列表拉取失败或响应缺 `default_types`：下拉应正常为空，不抛异常、不白屏（须用可选链 `m.default_types?.includes(...)`）。
- 全部模型都被标为 ASR/TTS：下拉为空，输入框仍可输入，发送沿用现有回落，不新增报错。
- 当前选中模型在会话中被标为 ASR/TTS 后刷新：选中项回落到默认对话模型（若它在对话列表中）或列表首项，不回落到被隐藏的模型；用户正在输入的文本不受影响。
- 默认对话模型本身持有 `asr`/`tts` 标记：它不在选项里，回落取列表首项而非默认模型。
- 模型列表为空数组：watch 不访问不存在的首项，`selectedModel` 保持空串。

---

### Task 1: meta store 派生 `chatModels`

**Files:**
- Modify: `web/src/stores/meta.ts:7-44`

**Interfaces:**
- Consumes: `models: Ref<ModelInfo[]>`、`ModelInfo.default_types: DefaultType[]`（`web/src/api/types.ts`）。
- Produces: store 导出 `chatModels: ComputedRef<ModelInfo[]>`，供 Task 2 使用。

- [ ] **Step 1: 在 `useMetaStore` 中新增派生列表**

在 `defaultModel` 定义之后加入（导出放在 return 的对象里）：

```ts
// 对话模型列表：排除禁用模型，以及持有 asr/tts 标记的语音模型。
// default_types 缺失时按非语音模型处理，避免旧响应导致整个下拉为空。
const chatModels = computed(() =>
  models.value.filter(
    (m) => m.enabled && !m.default_types?.includes('asr') && !m.default_types?.includes('tts')
  )
)
```

并在 `return { models, defaults, defaultModel, agents, loaded, load, reload }` 中补上 `chatModels`。

- [ ] **Step 2: 运行构建校验**

Run: `cd web && npm run build`
Expected: 构建成功，`vue-tsc` 无类型错误。

- [ ] **Step 3: 人工检查派生逻辑的边界**

在 `web/src/stores/meta.ts` 中确认：`default_types` 为 `[]`、缺失时均保留模型；含 `['chat']` 保留；含 `['chat','asr']`、`['tts']`、`['asr','vision']` 均排除。此步骤通过阅读代码确认，无需运行。

- [ ] **Step 4: 记录工作区快照**

```bash
cd /Users/bonc/workspace/groot
snap=$(mktemp -u /tmp/groot-index.XXXXXX)
GIT_INDEX_FILE="$snap" git read-tree HEAD
GIT_INDEX_FILE="$snap" git add -A
tree=$(GIT_INDEX_FILE="$snap" git write-tree)
git commit-tree "$tree" -p HEAD -m "snapshot: task1 chatModels"
rm -f "$snap"
```
Expected: 输出一个 commit hash；`git status` 仍显示改动未提交，HEAD 不变。

---

### Task 2: ChatInput 使用 `chatModels` 并调整回落

**Files:**
- Modify: `web/src/components/chat/ChatInput.vue:21`（storeToRefs 解构）
- Modify: `web/src/components/chat/ChatInput.vue:169-186`（watch 与 modelOptions）

**Interfaces:**
- Consumes: `chatModels`（Task 1）。`models` 仍用于 `micWarn`（第 49-51 行），保持解构不变。
- Produces: 无对外接口变化；`emit('send', ..., model, ...)` 语义不变。

- [ ] **Step 1: 解构 `chatModels`**

把第 21 行改为同时取出 `chatModels`：

```ts
const { models, chatModels, defaultModel, agents, loaded: metaLoaded } = storeToRefs(meta)
```

- [ ] **Step 2: 改写选中项回落 watch**

替换第 169-177 行的 watch 为（注释同步更新）：

```ts
// 模型选择必须在对话模型列表内：未选择、或已选模型不再属于对话模型
// （例如被标记为 ASR/TTS）时，回落到默认对话模型，默认模型不在列表中则取首项。
watch(
  [defaultModel, chatModels],
  ([def, list]) => {
    if (selectedModel.value && list.some((m) => m.name === selectedModel.value)) return
    const fallback = list.some((m) => m.name === def) ? def : list[0]?.name || ''
    selectedModel.value = fallback
  },
  { immediate: true }
)
```

- [ ] **Step 3: 改写 `modelOptions`**

替换第 180-186 行，数据源改为 `chatModels`（不再重复判断 `enabled`）：

```ts
const modelOptions = computed(() =>
  chatModels.value.map((m) => ({
    label: m.name,
    value: m.name,
    isDefault: m.name === defaultModel.value,
  }))
)
```

- [ ] **Step 4: 运行构建校验**

Run: `cd web && npm run build`
Expected: 构建成功，`vue-tsc` 无类型错误。

- [ ] **Step 5: 人工核对四个场景**

启动开发服务器（`cd web && npm run dev`）或使用构建产物，核对：

1. 普通对话模型出现在下拉框；禁用模型不出现。
2. 在 设置 → 模型 中把某模型设为默认 ASR（或 TTS）后，回到聊天页刷新，该模型从下拉框消失；取消标记后重新出现。
3. 当前选中的模型被设为 ASR/TTS 后刷新页面，选中项变为默认对话模型（或对话列表首项），输入框已输入文字未被清空。
4. 把全部模型都标为 ASR/TTS 后，下拉框为空且页面不报错。

- [ ] **Step 6: 记录工作区快照**

同 Task 1 Step 4，`-m` 改为 `"snapshot: task2 chatinput"`。

---

### Task 3: 同步 README 与关联设计文档

**Files:**
- Modify: `README.md:523`
- Modify: `docs/superpowers/specs/2026-09-30-model-default-types-design.md`（文末「迭代说明」章节追加一条）

**Interfaces:**
- Consumes: Task 1、Task 2 的最终行为。
- Produces: 文档与实现一致。

- [ ] **Step 1: 更新 README 默认模型规则**

把 `README.md:523` 的：

```markdown
- 禁用的模型不可设为默认，也不会出现在聊天下拉框中
```

改为：

```markdown
- 禁用的模型不可设为默认，也不会出现在聊天下拉框中；持有 ASR 或 TTS 默认标记的模型是语音模型，同样不进入聊天下拉框
```

- [ ] **Step 2: 在关联设计文档追加迭代说明**

在 `docs/superpowers/specs/2026-09-30-model-default-types-design.md` 的「二、迭代说明」末尾追加：

```markdown
- 调整（2026-11-22）：聊天下拉框除禁用模型外，也不再展示持有 `asr`/`tts` 标记的模型，并处理选中项失去对话资格时的回落。详见 `2026-11-22-chat-model-selector-speech-filter-design.md`。
```

- [ ] **Step 3: 核对文档**

Run: `grep -n "ASR 或 TTS 默认标记" README.md && grep -n "2026-11-22-chat-model-selector" docs/superpowers/specs/2026-09-30-model-default-types-design.md`
Expected: 两条命令各输出一行命中。

- [ ] **Step 4: 记录工作区快照**

同 Task 1 Step 4，`-m` 改为 `"snapshot: task3 docs"`。

---

## 完成后验证

- [ ] `cd web && npm run build` 通过。
- [ ] Review Focus 五条逐条人工确认。
- [ ] `git status` 列出预期改动的六个文件（两处源码、两处既有文档、两份新增流程文档），无临时文件残留。
