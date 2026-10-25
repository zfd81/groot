# 文本转语音（TTS）设计文档

## 一、功能设计

### 1.1 功能概述

Groot 提供文本转语音能力：调用方提交一段文字，Groot 调用 OpenAI 兼容的语音合成模型，把合成的音频返回给调用方。接口无状态，与对话、会话无关，与音频转录接口对称。

模型管理中，语音类模型分为两种默认类型：**ASR 模型**（音频转文字）与 **TTS 模型**（文字转音频）。每种默认类型全表至多一个模型持有。文本转语音接口在调用方未指定模型时取默认 TTS 模型。

Web 调试界面提供朗读能力：设置了默认 TTS 模型时，聊天窗口顶部出现朗读总开关；开启时每轮回复在流式输出过程中按句合成、连续播放；每条助手消息底部有一个小喇叭，点击朗读该条内容。音色在设置页「语音朗读」分组中配置。

解决的问题：

- 对外提供与转录对称的语音合成接口，调用方用同一套模型管理与鉴权完成语音双向转换。
- 调试界面可直接听到回复，验证 TTS 模型与音色配置是否正确。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 合成接口 | `POST /audio/speech`，文本进、音频出，参数与 OpenAI 一致 |
| 模型取用 | JSON `model` → 请求头 `X-Model-Name` → 默认 TTS 模型；显式指定的模型不存在或已禁用直接报错，不回落 |
| 音频透传 | 上游返回的音频流原样转发给调用方，不在内存完整展开，`Content-Type` 随上游 |
| 默认 TTS 模型 | 模型管理中可设置、取消默认 TTS 模型 |
| 朗读参数配置 | Web 设置页「语音朗读」分组提供音色、风格指令两个文本框，存配置表 |
| 朗读总开关 | 存在默认 TTS 模型时聊天窗口顶部显示喇叭开关，默认开启，状态存浏览器本地 |
| 流式分句朗读 | 总开关开启时，回复流式输出过程中按句切分、分段合成、串行播放 |
| 单条点播 | 每条助手消息底部的小喇叭，点击朗读该条，再点停止 |
| Markdown 清洗 | 送合成前去除代码块、行内代码标记、表格、链接地址与标记符号 |

### 1.3 模型默认类型

`internal/repo` 中的默认类型位标志：

```go
const (
    DefaultChat   DefaultFlag = 1 << iota // 1 默认对话模型
    DefaultASR                            // 2 默认 ASR 模型（音频转文字）
    DefaultVision                         // 4 默认视觉模型
    DefaultTTS                            // 8 默认 TTS 模型（文字转音频）
)

var AllDefaultFlags = []DefaultFlag{DefaultChat, DefaultASR, DefaultVision, DefaultTTS}
```

字符串标识：`chat` / `asr` / `vision` / `tts`。`models.default_flags` 列按位存储，表结构不变。

默认 TTS 模型的设置与取消沿用模型管理既有接口：

| 路由 | 说明 |
|---|---|
| `PUT /web/models/:name/default?type=tts` | 设为默认 TTS 模型 |
| `DELETE /web/models/:name/default?type=tts` | 取消默认 TTS 模型 |

`GET /web/models` 响应的 `defaults` 对象含四个键：

```json
{ "chat": "gpt-4o", "asr": "whisper-1", "vision": "", "tts": "tts-1" }
```

`ModelInfo.default_types` 按 chat、asr、vision、tts 顺序排列。

### 1.4 合成接口

#### 1.4.1 路由与鉴权

| 路由 | 鉴权 | 说明 |
|---|---|---|
| `POST /audio/speech` | API Key，`chat` 权限，计入限流 | 对外接口 |
| `POST /web/audio/speech` | 登录会话 | Web 界面自用 |

两条路由共用同一个 handler，鉴权差异由路由层中间件承担，与转录接口一致。权限判定在 `internal/api/middleware/auth.go` 中与 `/audio/transcriptions` 并列。

#### 1.4.2 请求

`Content-Type: application/json`

| 字段 | 必填 | 说明 |
|---|---|---|
| `input` | 是 | 待合成文本，去除首尾空白后不能为空，长度上限 4096 字符（按 rune 计） |
| `model` | 否 | 模型名，省略时按 `X-Model-Name` 请求头，再按默认 TTS 模型 |
| `voice` | 否 | 音色 / 说话人（OpenAI 规范字段），如 OpenAI 系的 `alloy`、Qwen3-TTS 的 `vivian`；省略时不向上游下发该字段 |
| `language` | 否 | 合成语种，取值由上游定义（如 vLLM-Omni 的 `Chinese`、`English`、`Auto`），省略时不下发，由上游自行判断 |
| `instructions` | 否 | 风格指令（如「用轻快的语气朗读」，OpenAI 规范字段），仅支持指令的模型有效；省略时不下发 |
| `response_format` | 否 | 音频格式，省略时不下发；允许值 `mp3` / `opus` / `aac` / `flac` / `wav` / `pcm` |

`voice`、`language`、`instructions` 均去除首尾空白后透传；长度上限分别为 64、16、500 字符（按 rune 计），超出返回 400 `invalid_request`。

#### 1.4.3 响应

成功：`200`，响应体为音频二进制，`Content-Type` 取上游响应头；上游未给出时按 `response_format` 推断（`mp3`→`audio/mpeg`、`opus`→`audio/ogg`、`aac`→`audio/aac`、`flac`→`audio/flac`、`wav`→`audio/wav`、`pcm`→`audio/pcm`），都没有时为 `audio/mpeg`。响应头 `X-Model-Name` 给出实际使用的模型名。

错误响应为 JSON，与转录接口格式一致：

| 状态码 | status | 场景 |
|---|---|---|
| 400 | `invalid_request` | 请求体不是合法 JSON、`input` 为空或超长、`response_format` 不在允许值内 |
| 400 | `invalid_model` | 模型不存在、已禁用或未配置默认 TTS 模型 |
| 502 | `upstream_error` | 上游返回 4xx/5xx 或网络错误，message 透传上游原文 |
| 500 | `error` | 内部错误 |

#### 1.4.4 上游调用

`internal/llm` 新增：

```go
// SpeechRequest 合成参数。
type SpeechRequest struct {
    Input          string
    Voice          string // 空串不下发
    Language       string // 空串不下发
    Instructions   string // 空串不下发
    ResponseFormat string // 空串不下发
}

// Speech 请求 {base_url}/audio/speech，返回上游响应体（音频流）与 Content-Type。
// 调用方负责 Close 返回的 io.ReadCloser。
// 上游返回 >= 400 时读取错误体并返回包装了 ErrUpstream 的错误。
func Speech(ctx context.Context, m *repo.Model, req SpeechRequest) (io.ReadCloser, string, error)
```

- 端点由 `openAIBaseURL(m.BaseURL) + "/audio/speech"` 得出，`/v1` 补齐规则与对话、转录一致。
- 请求体 JSON：`{"model": m.Model, "input": ..., "voice": ..., "response_format": ...}`，`voice`、`language`、`instructions`、`response_format` 为空时省略。
- 鉴权 `Authorization: Bearer {m.APIKey}`。
- 整体超时 120 秒，与转录一致。
- 上游错误信息提取复用 `upstreamMessage`。
- handler 拿到 `io.ReadCloser` 后用流式方式写入响应体，不读入内存。

### 1.5 语音朗读配置

`internal/setting` 新增 `speech.go`：

```go
const (
    KeySpeechVoice        = "speech.voice"
    KeySpeechInstructions = "speech.instructions"
)

// SpeechSettings 语音朗读配置。
type SpeechSettings struct {
    Voice        string // 音色，空串不下发
    Instructions string // 风格指令，空串不下发
}

func (s *Settings) Speech(ctx context.Context) (SpeechSettings, error)
func (s *Settings) SetSpeech(ctx context.Context, v SpeechSettings) error
```

Web 接口：

| 路由 | 说明 |
|---|---|
| `GET /web/settings/speech` | 读取，响应 `{ "voice": "alloy", "instructions": "" }` |
| `PUT /web/settings/speech` | 整体保存两个字段，各字段去首尾空白；长度上限 `voice` 64、`instructions` 500，超出返回 400 `invalid_request` |

设置页 **通用** 中的「语音朗读」分组与「语音输入」分组相邻，含两个文本框：

| 字段 | 标签 | 控件 | 描述文案要点 |
|---|---|---|---|
| `voice` | 音色 | 单行输入，maxlength 64 | 音色名，不同模型可选值不同：OpenAI 系如 alloy、nova，Qwen3-TTS 如 vivian、ryan；留空用上游默认 |
| `instructions` | 风格指令 | 多行输入（2 行），maxlength 500 | 朗读风格描述，仅支持指令的模型有效 |

每个控件失焦、回车（多行框为 Ctrl/Cmd+Enter）或清除时整体保存两个字段。

合成时 `voice` 与 `instructions` 直接作为同名字段下发，字段名与 OpenAI 规范及 vLLM-Omni 等兼容服务一致。语种不在 Web 端配置，由模型按文本自行判断。

### 1.6 Web 聊天窗口朗读

#### 1.6.1 可见性

`useMetaStore().defaults.tts` 非空时，聊天窗口顶部栏显示朗读总开关按钮（喇叭图标），位于日志按钮左侧；消息底部显示小喇叭。`defaults.tts` 为空时两者均不显示，已有的播放队列清空并停止。

#### 1.6.2 总开关

- 默认开启。状态存 `localStorage`，键 `groot.speech.autoplay`，值 `"0"` 表示关闭，其他或缺失表示开启。读写包在 try/catch 内，存储不可用时按开启处理且不报错。
- 开启：喇叭实心；关闭：喇叭带斜线。
- 关闭时立即停止当前播放并清空队列。

#### 1.6.3 流式分句朗读

新增 `web/src/composables/useSpeechPlayer.ts`，持有一个全局播放器实例（Pinia store `speech`），负责切句、合成、队列与播放。

**切句**。流式过程中对当前助手消息的 `content` 做增量处理：维护「已消费到的字符偏移」，每次新 token 到达后，从偏移处起查找句末边界。边界为以下任一字符后的位置：`。！？；!?;` 以及换行符；英文 `.` 后紧跟空白或行尾时也算边界。切出的句子经 Markdown 清洗后若为空白则丢弃。流结束时把剩余文本作为最后一句。

**Markdown 清洗**（纯函数 `stripMarkdown(text: string): string`，位置见 1.6.6）：

- 围栏代码块 ``` ``` ``` 整段移除（切句时围栏内不算边界，未闭合的围栏延后到闭合或流结束）
- 行内代码去掉反引号保留内容
- 链接 `[文字](url)` 只保留文字；图片 `![alt](url)` 移除
- 表格行移除
- 标题 `#`、强调 `*` `_`、引用 `>`、列表标记 `-` `*` `1.` 去掉
- 连续空白压成一个空格

**合成与播放**。每切出一句生成一个任务进入队列，任务状态 `pending → fetching → ready → playing → done`。最多同时预取 2 个任务（即正在播放的任务之后最多 2 个处于 fetching/ready）。调用 `POST /web/audio/speech`，请求体 `{ input, voice, instructions, response_format: "mp3" }`，`voice` 取 `speech.voice`、`instructions` 取 `speech.instructions`，为空不传；`response_format` 固定为 `mp3`，保证返回的音频是浏览器 `HTMLAudioElement` 一定能播放的格式，不受上游默认输出格式（可能是 pcm）影响。响应 blob 用 `URL.createObjectURL` 交给单个 `HTMLAudioElement` 播放；播放结束触发下一个 ready 任务，并释放前一个 object URL。某句合成失败时跳过该句并记录一次 `console.warn`，不中断后续，首次失败时弹一次 `ElMessage.warning`。

**朗读来源互斥**。播放器同一时刻只服务一个来源：自动朗读的某条消息，或点播的某条消息。来源切换时先 `stop()`（暂停音频、清空队列、释放 URL、重置偏移）。

**自动朗读被打断后不续接**。流式自动朗读进行中，若用户点播另一条消息或关闭总开关，本轮回复的自动朗读即告放弃：`stop()` 后播放器记录「已放弃的消息 id」，后续对该 id 的 `feedStreaming` 调用一律忽略，直到下一条流式消息开始。理由：从头重念会重复，从当前位置接续会漏掉中间一段，两者都让使用者困惑；放弃最可预期，使用者想听可点底部小喇叭从头播放。流结束后用户重新打开总开关也不补播。

**停止条件**（均调用 `stop()`）：用户发送新消息、点击停止生成、关闭总开关、切换或删除会话、点播另一条消息、默认 TTS 模型被取消。

**自动播放限制**。浏览器要求首次播放由用户手势触发。用户发送消息的点击/回车即为手势，播放器在发送时同步创建并 `play()` 一次静音的空 Audio 以解锁。解锁失败时不报错，等待下一次用户手势。

#### 1.6.4 单条点播

`MessageFooter.vue` 完成态在复制按钮右侧新增小喇叭按钮，仅当 `defaults.tts` 非空且 `message.content` 非空时渲染。

- 未播放：静态喇叭图标，title「朗读」。
- 该条正在播放：图标带动效（Element Plus `is-loading` 或 CSS 脉冲），title「停止」，点击 `stop()`。
- 点击未播放的消息：先 `stop()`，再把整条 `content` 清洗、切句后入队播放，流程与自动朗读相同。

#### 1.6.5 消息标识与状态暴露

`ChatMessage` 新增字段 `id: string`，在消息对象创建时生成（历史加载与流式新建均生成，`crypto.randomUUID()` 可用时用它，否则用时间戳加随机数拼接），只在前端内存中存在，不持久化。播放器以该 `id` 标识正在朗读的消息，不依赖会话 ID 或列表索引，因此新会话首轮会话 ID 在流中途才确定、以及分页加载改变索引，都不影响状态匹配。

`speech` store 对外：

```ts
{
  autoplay: Ref<boolean>          // 总开关
  playingId: Ref<string | null>   // 正在朗读的消息 id，null 表示空闲
  setAutoplay(v: boolean): void
  feedStreaming(id: string, content: string, done: boolean): void // 流式增量输入
  playMessage(id: string, content: string): void
  stop(): void
}
```

`chat` store 在收到流式内容和流结束时调用 `feedStreaming`；仅在 `autoplay` 开启且该条消息是当前会话正在流式的消息时生效。

#### 1.6.6 前端代码组织

`stripMarkdown` 与切句函数放在 `web/src/utils/speechText.ts`，为无副作用的纯函数，不依赖 Vue 或 store。Web 工程没有单元测试框架，项目规范也不引入新测试工具，因此前端不写自动化测试；纯函数拆分只是代码组织要求，便于在浏览器控制台或将来人工验证。

### 1.7 README 更新

- 模型管理章节：三种默认类型改为四种（对话 / ASR / 视觉 / TTS），说明各自用途。
- 新增「POST /audio/speech - 文本转语音」小节，与转录小节相邻，给出 curl 示例、参数表、错误码。
- 接口总表加一行 `/audio/speech`。
- 设置章节说明「语音朗读」分组与音色。
- Web 界面章节说明朗读总开关与消息点播。

### 1.8 测试

Go 单元测试：

- `internal/repo`：`DefaultTTS` 的 String / Parse，`AllDefaultFlags` 顺序。
- `internal/llm/speech_test.go`：用 `httptest.Server` 模拟上游，覆盖正常返回与 Content-Type 透传、`voice` / `response_format` 省略与下发、上游 4xx 错误信息提取、ctx 取消、`/v1` 补齐。
- `internal/api/handler/speech_test.go`：请求体非法、`input` 空/超长、`response_format` 非法、模型解析三类错误映射 `invalid_model`、模型取用顺序（JSON → 头 → 默认）、上游错误映射 502、成功时音频体与 `X-Model-Name` 头。
- `internal/setting`：`Speech` / `SetSpeech` 读写与默认值。
- `internal/api/handler/setting_test.go`：speech 设置接口的读写与 `voice` 长度校验。
- 既有 `voice` → `asr` 重命名涉及的测试同步更新。

Python 系统测试（`tests/python/`，用户运行）：`/audio/speech` 的鉴权、参数校验、默认模型缺失时的 400。

`tests/TEST_CASES.md` 增加对应条目。

### 1.9 留给实施者的决定

- 切句正则的具体写法，以及英文缩写（如 `e.g.`）误切的处理程度。实施者可接受少量误切。
- 喇叭图标选用 Element Plus 图标集中的哪一个，以及关闭态如何表现斜线。
- `HTMLAudioElement` 与 Web Audio API 的选择。实施者优先用前者，满足需求即可。
- 预取数 2 与超时 120 秒作为常量放置的位置。

## 二、迭代说明

### 2.1 与上一版差异

上一版见 `2026-09-30-model-default-types-design.md` 与 `2026-10-01-voice-model-visibility-design.md`。

- 新增：默认类型 `DefaultTTS`（位 8，标识 `tts`）；`POST /audio/speech` 与 `POST /web/audio/speech`；`internal/llm.Speech`；`internal/setting` 的 `speech.voice` 配置与 `/web/settings/speech` 接口；设置页「语音朗读」分组；聊天窗口朗读总开关、流式分句朗读、消息底部点播。
- 调整：默认类型 `DefaultVoice` 重命名为 `DefaultASR`，字符串标识 `voice` 改为 `asr`，`DefaultModels.Voice` 改为 `ASR`，前端 `DefaultType` 联合类型与界面文案「默认语音」改为「默认 ASR」。
- 保留：配置键 `voice.model`、`voice.auto_send` 与 `/web/settings/voice` 不变，它们表示「语音输入」功能分组而非模型类型；设置页「语音输入」分组的「识别模型」字段不变。
- 不涉及：对外接口与示例代码（`examples/`）此前未暴露 `voice` 标识，无兼容处理。数据库 `default_flags` 位 2 的含义不变（仍为 ASR），已有数据无需迁移。

### 2.2 本轮补充（同日）

- 新增：合成接口可选字段 `speaker`、`language`、`instruct`，原样透传上游；`speaker` 为 Qwen 等模型对音色的命名，与 OpenAI 的 `voice` 并存。
- 新增：配置键 `speech.speaker`、`speech.language`、`speech.instruct`；`/web/settings/speech` 扩为四字段整体读写；设置页「语音朗读」分组扩为四个输入框。
- 调整：Web 朗读请求把四项设置中非空者一并带上。
- 背景：Qwen3-TTS 不接受空 `voice`，且其音色字段名为 `speaker`，仅靠 `voice` 无法驱动此类模型。

### 2.3 本轮再次调整（同日）

- 移除：配置键 `speech.speaker`、`speech.language`；`/web/settings/speech` 收窄为 `voice`、`instruct` 两字段整体读写；设置页「语音朗读」分组去掉「说话人」「语言」两个输入框。
- 调整：Web 朗读请求把 `speech.voice` 同时填入 `voice` 与 `speaker`，不再下发 `language`。
- 不涉及：对外合成接口 `POST /audio/speech` 的 `speaker`、`language` 透传参数保留不变。
- 背景：Web 端同时维护「音色」「说话人」两个字段让使用者困惑该填哪个，而各模型只认其中一个名字，同值下发两者即可；语种由模型按文本判断已足够，无需手动配置。
- 方案取舍：另一选项是只隐藏界面、后端字段照旧保留，否决——会留下只能改数据库才能触到的隐藏配置，接口与文档也和界面不一致。配置表中已存在的 `speech.speaker`、`speech.language` 行不做迁移，读写均不再触及，留存无副作用。

### 2.4 字段名对齐 OpenAI 规范（同日）

- 调整：合成接口与上游请求体的风格指令字段由 `instruct` 改为 `instructions`（OpenAI 规范字段名，vLLM-Omni 等官方兼容服务采用同名）；配置键 `speech.instruct` 改为 `speech.instructions`，`/web/settings/speech` 与前端字段同步改名。
- 移除：合成接口字段 `speaker`，以及 Web 朗读请求把音色同时填入 `voice` 与 `speaker` 的双填逻辑。音色统一用规范字段 `voice`。
- 不涉及：`language` 字段保留（vLLM-Omni 的合法扩展字段），取值说明改为由上游定义。
- 背景：实际部署的 Qwen3-TTS 服务为 vLLM-Omni，它只识别 `voice` 与 `instructions`；此前按部分社区实现的命名下发 `speaker` 与 `instruct`，前者多余，后者被上游当作未知字段忽略，导致界面配置的风格指令完全无效。
- 配置表中遗留的 `speech.instruct` 行与 `speech.speaker`、`speech.language` 同样处理：不迁移、不读取、不触碰；用户需在设置页重新填写风格指令。

### 2.5 方案取舍

| 方案 | 说明 | 结论 |
|---|---|---|
| A. 无状态合成接口 + Web 前端分句 | 后端只提供文本进音频出的接口；Web 前端在 SSE 流上切句、排队、播放 | **采用**。接口语义干净、与 `/chat` 解耦，对外可直接用；播放节奏由浏览器掌控 |
| B. 合成嵌入聊天 SSE | `/chat` 后端边生成边合成，音频片段混入事件流 | 否决。`/chat` 要感知 TTS 开关与音色，影响全部 API 调用方；音频 base64 膨胀；用户关闭朗读后端仍在合成 |
| C. 浏览器 speechSynthesis | 不走服务端 | 否决。不满足「使用 OpenAI 接口与模型」的要求，音色不可控 |

分句与排队放在 Web 前端而非 Web 后端：SSE 文本流本就由前端消费渲染，音频在浏览器播放，播放顺序与中断只有浏览器知道；Web 后端介入需再开一条通道推音频，等于把方案 B 的耦合搬到 Web 层。

朗读总开关状态存浏览器本地而非服务端配置表：它不是服务端提供的能力，属于每位使用者在每台设备上的偏好，且「默认开启」要求首次进入一定为开。
