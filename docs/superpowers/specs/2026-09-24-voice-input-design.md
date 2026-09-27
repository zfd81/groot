# 语音输入与配置表设计文档

## 一、功能设计

### 1.1 功能概述

Groot 支持用语音向 Agent 下达指令。在 Web 聊天页的输入框旁有一个话筒按钮，点击开始录音，再点一次结束，浏览器采集到的音频交由服务端转录成文字后填入输入框，使用者确认无误后回车发送。

转录能力同时以 HTTP 接口的形式对外开放，使用者可以在自己的程序里把音频文件提交给 Groot 换回文字，接口签名与 OpenAI 的转录规范一致。

语音相关的配置保存在数据库的配置表中，通过 Web 界面的设置面板维护，保存后立即生效，无需重启服务。配置表是一张通用的键值表，承载所有「改动后不需要重启即可生效」的配置项。

Agent 的运行参数一并由配置表承载：对话历史窗口、ReAct 循环的迭代与超时、子 Agent 的并发与时限、附件上传的限额，都在设置面板的「配置」分区调整，保存后下一次对话即采用新值。

程序内部通过统一的配置对象读取全部配置。配置对象按分类提供方法，调用方从方法返回的结构体上取属性，不关心某一项配置究竟来自 YAML 文件还是数据库。

解决的问题：

- 长指令逐字敲入输入框效率低，移动端尤其吃力。
- 需要在自有程序中把语音转成文字时，缺少可直接调用的接口。
- 界面上维护的配置保存在浏览器本地，换浏览器即丢失，多节点部署时各节点不一致。
- 配置的读取方式分散，调用方需要知道每一项配置的存放位置。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 聊天页语音输入 | 话筒按钮录音，转录结果填入输入框，可编辑后发送 |
| 自动发送 | 开启后转录完成即发送，无需手动回车 |
| 对外转录接口 | `POST /audio/transcriptions`，multipart 上传音频，返回文字 |
| 语音模型选择 | 在设置面板的「通用」分区从已有模型列表中指定用于转录的模型 |
| 配置表 | 通用键值表，支持全局与用户等多种作用域，按作用域回落取值 |
| 统一配置对象 | 按分类提供方法，屏蔽 YAML 与数据库的来源差异 |
| 代码内置默认值 | 配置项的默认值定义在代码中，表内只保存使用者明确修改过的值 |
| 运行时配置 | 在设置面板的「配置」分区调整 Agent 运行参数，保存后即刻生效 |
| 并发上限即时生效 | 子 Agent 并发上限保存后对新发起的调用生效，已发起的调用按旧上限走完 |

### 1.3 配置表

#### 1.3.1 表结构

```sql
CREATE TABLE settings (
  scope      TEXT    NOT NULL DEFAULT 'global',
  scope_id   TEXT    NOT NULL DEFAULT '',
  name       TEXT    NOT NULL,
  value      TEXT    NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (scope, scope_id, name)
);
```

| 列 | 含义 |
|---|---|
| `scope` | 作用域类型，当前取值 `global`、`user` |
| `scope_id` | 作用域实体标识，`global` 时为空串，`user` 时为用户 ID |
| `name` | 配置键，点号分层，镜像 YAML 的层级路径，如 `voice.model` |
| `value` | 配置值，统一以文本保存，类型转换由配置对象负责 |
| `updated_at` | 最后修改时间，Unix 毫秒 |

`scope` 与 `scope_id` 分为两列，使「作用域类型」与「实体标识」各自占据独立的值域：按作用域类型批量查询可以表达，删除某实体的配置不会误伤同名的其他实体，实体标识与 `global` 这类保留值不会冲突。新增一种作用域时只需在 `scope` 中启用一个新取值，表结构与主键均不变动。

`scope` 位于主键首位，按作用域批量查询可命中主键索引的前缀。

列名取 `name` 而非 `key`：`KEY` 是 MySQL 的保留字，且 `name` 与既有表的命名习惯一致。

#### 1.3.2 取值规则

一次取值按「由具体到通用」的顺序命中第一个存在的来源：

1. 用户级：`scope='user' AND scope_id=<当前用户>`
2. 全局级：`scope='global' AND scope_id=''`
3. 代码内置默认值

优先级由 Go 代码中的显式优先级表决定，不依赖数据库对 `scope` 字符串的排序结果。

本次迭代的语音配置只有全局级，实际取值为全局级与代码默认值两层。用户级作为预留作用域，优先级表已在代码中定义，后续接入用户级配置时只需在读取路径加入按用户查询，不改表结构。

表在创建后为空，不写入初始数据。表内只保存使用者明确修改过的值，其余项由代码默认值提供。因此新增配置项在新版本启动后即刻生效，无需为已有数据库补写初始数据；「恢复默认」等价于删除对应行。

`scope='global'` 时 `scope_id` 必须为空串，此约束由写入层校验，不使用 SQL 的 CHECK 约束。

#### 1.3.3 配置项

| 键 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `voice.enabled` | bool | `false` | 聊天页是否显示话筒按钮 |
| `voice.model` | string | `""` | 用于转录的模型名 |
| `voice.auto_send` | bool | `false` | 转录完成后是否自动发送 |

### 1.4 配置对象

#### 1.4.1 定位

`internal/setting` 包中的 `Settings` 是程序读取配置的唯一入口。它私有地持有 YAML 的解析结果与配置表仓库，对外只按分类暴露方法：

```go
type Settings struct {
    static config.Config
    repo   repo.SettingRepo
}

func (s *Settings) Server(ctx) (config.ServerConfig, error)
func (s *Settings) Attachment(ctx) (config.AttachmentConfig, error)
func (s *Settings) Voice(ctx) (VoiceSettings, error)
func (s *Settings) SetVoice(ctx, VoiceSettings) error
```

调用方持有 `*setting.Settings`，先取分类结构体再取属性：

```go
port := settings.Server(ctx).Port
voice, err := settings.Voice(ctx)
model := voice.Model
```

某一项配置存放在 YAML 还是数据库，属于方法内部的实现细节。分类结构体复用 `internal/config` 中已有的类型，不重复定义。

`internal/config` 承担 YAML 的加载与解析，把文件读成结构体交给 `Settings`。配置对象独立成包而非并入 `config`：`internal/config` 处于依赖链底层，而配置表仓库需要引用它完成环境变量展开，配置对象置于两者之上可避免循环导入。

#### 1.4.2 方法签名

全部分类方法统一为 `Xxx(ctx) (T, error)`。来自 YAML 的分类在实现中直接返回解析结果，`error` 恒为 `nil`。统一签名使某一分类从 YAML 迁移到配置表时，调用方无需改动。

#### 1.4.3 默认值

默认值集中定义在 `internal/setting/defaults.go`，一个分类一组。读取配置表时，表内缺失的字段由此处填充。

#### 1.4.4 读取策略

来自配置表的分类每次调用查询数据库，不做内存缓存。设置面板保存后当次请求即可读到新值，多节点共享同一数据库时各节点取值一致。

语音配置的读取时机为打开聊天页、点击话筒、转录接口被调用，均为按作用域的单次范围查询（命中主键前缀），不位于热路径。

### 1.5 转录能力

#### 1.5.1 上游调用

`internal/llm/transcription.go` 提供转录客户端：

```go
func Transcribe(ctx context.Context, m *repo.Model, file io.Reader,
    filename, language string) (string, error)
```

客户端将音频组装为 multipart 表单，请求 `{base_url}/audio/transcriptions`，以 `Authorization: Bearer` 头鉴权，与既有 Chat 模型共用 `repo.Model` 中的 `BaseURL` 与 `APIKey`。`BaseURL` 缺少 `/v1` 后缀时自动补齐，与连通性探测的处理一致。文件部分以流式写入上游请求体，不在内存中完整展开音频。

#### 1.5.2 对外接口

```
POST /audio/transcriptions
Content-Type: multipart/form-data
```

| 参数 | 位置 | 必填 | 说明 |
|---|---|---|---|
| `file` | 表单文件 | 是 | 音频文件 |
| `model` | 表单字段 | 否 | 模型名，缺省时取 `voice.model` |
| `language` | 表单字段 | 否 | 语言提示，如 `zh` |
| `X-Model-Name` | 请求头 | 否 | 模型名，与 `model` 同义 |

模型名的取用顺序为表单 `model`、请求头 `X-Model-Name`、配置表 `voice.model`。请求头形式与 `/chat` 的既有约定一致。

成功响应：

```json
{ "text": "帮我看一下登录接口的日志", "model": "whisper-1" }
```

该结构是 OpenAI 转录响应的超集，`model` 字段告知调用方实际使用的模型。

两条路由共用同一 handler 方法：

| 路由 | 鉴权 | 用途 |
|---|---|---|
| `POST /audio/transcriptions` | API Key，需 `chat` 权限（与 `/chat` 一致），含限流 | 对外调用 |
| `POST /web/audio/transcriptions` | Web 会话 | 聊天页调用 |

`voice.enabled` 只控制界面是否显示话筒按钮，不影响对外接口的可用性，接口只要求 `voice.model` 有值。

#### 1.5.3 设置接口

| 路由 | 说明 |
|---|---|
| `GET /web/settings/voice` | 读取语音配置 |
| `PUT /web/settings/voice` | 整体保存三个字段 |

接口按分类而非按单键暴露，与设置面板中语音分组的三项一一对应。保存时若 `enabled` 为真且 `model` 非空，则 `model` 必须是已存在且启用的模型；`enabled` 为假时不校验 `model`，这样所选模型被删除或禁用后仍能关闭语音输入。

### 1.6 运行时配置

#### 1.6.1 范围

运行时配置是保存后即刻生效、无需重启的 Agent 运行参数，分为四类：对话历史、ReAct 循环、子 Agent、附件限制。

归入这一类的前提是「读取点位于请求路径上」：每次对话开始时才读取的参数，改动后下一次对话即采用新值。读取点位于启动路径上的配置项（服务端口、日志、数据库连接、调度器参数）存入配置表后仍需重启才生效，因此保留在 YAML 中。

#### 1.6.2 配置项

| 键 | 类型 | 取值范围 | 含义 |
|---|---|---|---|
| `memory.history_window` | int | -1 ~ 500 | 提交给模型的对话轮次，-1 表示不限制 |
| `react.max_iterations` | int | 1 ~ 200 | 单次任务内「思考并调用工具」的最大轮次 |
| `react.step_timeout` | int | 1 ~ 3600 | 单步 LLM 调用的超时秒数 |
| `react.error_retry` | int | 0 ~ 10 | 步骤失败后的重试次数，0 表示不重试 |
| `subagent.max_concurrency` | int | 1 ~ 100 | 同时运行的子 Agent 数上限 |
| `subagent.exec_timeout` | duration | 1s ~ 24h | 单个子 Agent 一次执行的时限，Go 时长字面量 |
| `subagent.max_task_length` | int | 1 ~ 200000 | 交给子 Agent 的任务字符数上限 |
| `subagent.max_result_length` | int | 1 ~ 200000 | 子 Agent 返回结果的字符数上限，超出部分截断 |
| `attachment.max_size` | int | 1 ~ 1024 | 单个附件的兆字节上限 |
| `attachment.max_total_size` | int | 1 ~ 4096 | 单次上传合计的兆字节上限 |
| `attachment.max_count` | int | 1 ~ 100 | 单次上传的附件数上限 |
| `attachment.allowed_types` | []string | 最多 50 项 | 允许的扩展名，空数组表示不限制类型 |

取值范围在写入前校验。下界存在的理由是避免存进会让 Agent 无法工作的值：迭代上限为 0 会使每次对话立刻终止，步超时为 0 会使每步 LLM 调用即刻取消。

`attachment.max_total_size` 不小于 `attachment.max_size` 是一项跨字段约束：总量小于单个上限时，单文件校验永远先被总量卡住，等于单个上限失效。

#### 1.6.3 接口

| 路由 | 说明 |
|---|---|
| `GET /web/settings/runtime` | 读取四个分类的当次生效值 |
| `PUT /web/settings/runtime` | 整体保存四个分类 |

请求体与响应体同构：界面按分区整体提交，回读的字段与提交的字段一一对应，前端无需为两个方向维护两套结构。保存成功后响应体回显生效值，界面无需再发一次读取请求。

任一项越界即整次拒绝，不做部分写入：半套生效的配置比拒绝更难排查。

#### 1.6.4 并发上限的生效方式

`subagent.max_concurrency` 是全局 semaphore 的容量，生效方式与其余项不同：其余项在每次读取时取当前值，而 semaphore 是启动时建好的对象，需要替换。

保存成功后，服务端以新容量建立一个 semaphore 并替换注册表中的引用。此后新发起的子 Agent 调用在新 semaphore 上排队；正在执行与已在排队的调用持有旧 semaphore 的引用，按旧上限走完全程。

已发起的调用走完旧上限，是因为中途打断会丢掉子 Agent 尚未返回的工作，而这份工作已消耗 token 与时间。名额的归还随之绑定到取得名额时的那个 semaphore，归还不会落到新 semaphore 上，使新上限自始至终得到遵守。重复归还同一个名额只计一次，避免名额凭空超出上限。

写表在前、替换 semaphore 在后。配置表是持久来源，semaphore 是生效载体，两者都要更新：只写表则要等重启才生效，只替换 semaphore 则重启后回到表中的旧值。写表失败时直接返回，不留下「semaphore 已改、表内仍是旧值」的状态。

#### 1.6.5 设置面板

「配置」分区自上而下为四个分组，与四个分类一一对应：记忆、推理循环、子 Agent、附件。每个分组内一项一行，标题下附一行说明文字。分区底部一个保存按钮，一次提交全部四个分组。

并发上限一项的说明文字写明其对保存后新发起的调用生效，使这一项与其余项在生效时机上的差别在界面上直接可见。

### 1.7 界面交互

#### 1.7.1 录音流程

话筒按钮位于聊天输入框发送按钮的左侧，`voice.enabled` 为真时显示。

1. 点击话筒，浏览器原生的 `MediaRecorder` 开始采集，按钮转为红色停止态并显示录音计时。首次使用时浏览器弹出麦克风授权提示，被拒绝则按钮置灰并给出提示。
2. 再次点击结束录音，得到 `audio/wav` 音频数据。时长不足 0.5 秒的录音直接丢弃，不发起请求。

   采集经 Web Audio 取原始 PCM，在浏览器内封成 16 位单声道 WAV。WAV 是未压缩格式，转录服务可直接解码；webm、m4a 等压缩容器需要服务侧装有 ffmpeg 才能读取，而该依赖不在本项目的部署范围内。
3. 前端以 `FormData` 提交至 `/web/audio/transcriptions`，不传 `model`，由服务端回落到 `voice.model`。请求期间话筒显示加载态，输入框禁止编辑以避免竞态。
4. 转录文字追加到输入框已有内容的末尾，光标聚焦到文字尾部。
5. `voice.auto_send` 为真时直接触发发送。

转录结果默认填入输入框而非直接发送：文字将作为指令交由 Agent 执行，而 Agent 具备读写文件、执行命令等能力，识别偏差会导致执行错误的操作，且额外消耗一轮完整的执行与 token。技术术语、路径、英文缩写是识别偏差的高发内容，而它们在本场景中出现频率高。使用者确认识别质量满足预期后，可开启自动发送省去确认动作。

#### 1.7.2 设置面板

语音分组位于设置面板的「通用」分区，在外观之后，自上而下为三项：语音模型下拉、语音输入开关、转录后自动发送开关。模型下拉的数据来自已有的模型列表接口。

语音归入「通用」而非「配置」分区：这三项决定聊天页话筒按钮的可见性与转录后的行为，属于界面交互偏好，与「配置」分区承载的 Agent 运行参数（记忆窗口、ReAct 迭代上限、SubAgent 超时、附件限制）不同类。其中 `voice.model` 同时被对外的转录接口用作缺省模型，故仍存于配置表而非浏览器本地。

模型下拉排在开关之前，与实际操作顺序一致：开启语音输入要求先选定模型，未选模型时开关无法保存成功。把前置条件放在上方，使这一依赖关系在界面上直接可见。

语音配置在前端由一个共享状态持有，设置面板负责写、聊天输入框负责读。保存成功后话筒按钮的显示与隐藏立即生效，无需刷新页面或新建会话。

模型列表不区分用途，聊天与语音共用同一份列表。选用了不匹配的模型时，上游会明确拒绝请求，服务端将上游的错误信息透传到界面。

### 1.8 错误处理

| 场景 | 状态码 | 标识 | 处理 |
|---|---|---|---|
| `voice.model` 为空 | 400 | `invalid_model` | 提示前往设置页配置语音模型 |
| 模型不存在或已禁用 | 400 | `invalid_model` | 消息中给出模型名 |
| 缺少 `file` 参数 | 400 | `invalid_request` | 说明缺失的参数 |
| 文件超过附件配置的单文件上限 | 400 | `file_too_large` | 沿用附件模块的状态标识 |
| 扩展名不在音频白名单 | 400 | `unsupported_type` | 列出受支持的扩展名 |
| 上游连接超时、鉴权失败、模型拒绝 | 502 | `upstream_error` | 透传上游返回的错误原文 |

错误响应沿用项目既有结构 `{"status": "...", "message": "..."}`。

前端在 `voice.enabled` 为真但 `voice.model` 为空时，话筒显示警告态并在悬浮提示中引导至设置页。

### 1.9 改动清单

| 文件 | 说明 |
|---|---|
| `internal/repo/setting.go` | `Setting` 结构与 `SettingRepo` 接口 |
| `internal/repo/settingdb/` | 配置表的三方言实现与建表语句 |
| `internal/setting/settings.go` | `Settings` 配置对象与分类方法 |
| `internal/setting/defaults.go` | 各分类的默认值 |
| `internal/setting/runtime.go` | 运行时配置的键名、取值范围、校验与编解码 |
| `internal/llm/transcription.go` | 转录客户端 |
| `internal/api/handler/transcription.go` | 转录 handler |
| `internal/api/handler/setting.go` | 设置读写 handler |
| `internal/agent/subagent_registry.go` | semaphore 的替换与容量读取 |
| `internal/api/router.go` | 注册六条新路由 |
| `internal/api/server.go` | 把子 Agent 注册表交给设置 handler |
| `web/src/components/settings/SettingsModal.vue` | 「通用」分区中的语音分组、「配置」分区中的四个运行时分组 |
| `web/src/components/chat/ChatInput.vue` | 话筒按钮与录音逻辑 |
| `README.md` | 对外转录接口说明 |
| `examples/python/`、`examples/java/` | 转录接口调用示例 |

模型表不做改动。现有模块继续从 `config.Config` 读取配置，不在本次切换。

## 二、迭代说明

### 2.1 与上一版差异

- 新增：`settings` 配置表及其三方言实现，承载改动后无需重启即可生效的配置。
- 新增：`internal/setting` 包与 `Settings` 配置对象，按分类暴露方法，屏蔽 YAML 与数据库的来源差异。
- 新增：转录客户端 `internal/llm/transcription.go`。
- 新增：`POST /audio/transcriptions` 与 `POST /web/audio/transcriptions` 两条路由，共用同一 handler。
- 新增：`GET`、`PUT /web/settings/voice` 两条设置路由。
- 新增：`GET`、`PUT /web/settings/runtime` 两条设置路由，承载对话历史、ReAct 循环、子 Agent、附件四个分类。
- 新增：`SubAgentRegistry` 的 semaphore 替换能力，使子 Agent 并发上限保存后即刻对新发起的调用生效。
- 新增：设置面板「配置」分区中的四个运行时分组与统一保存按钮。
- 新增：聊天输入框的话筒按钮与浏览器录音逻辑，设置面板「通用」分区中的语音分组。
- 调整：语音分组由「配置」分区移入「通用」分区。「配置」分区自此只承载 Agent 运行参数。
- 保持不变：模型表结构、`/chat` 及其余既有接口、现有模块读取 YAML 配置的方式。

### 2.2 后续独立迭代

`Settings` 对象本次完整建立，但仅新增的转录与设置 handler 依赖它。现有模块从 `config.Config` 切换到 `Settings` 涉及几乎所有包的构造函数，留作独立一次重构。重构完成前，两种配置读取方式并存。

后续把 YAML 配置项迁移到配置表时，只迁移改动后无需重启即可生效的项。服务端口、数据库连接等启动即固定的配置保留在 YAML 中。迁移时键名沿用配置表既定的点号分层形式，与 YAML 的层级路径对应。

## 三、测试

### 3.1 单元测试（Go）

| 测试对象 | 覆盖点 |
|---|---|
| `internal/repo/settingdb` | 三方言的读写、按作用域批量查询、主键冲突时的覆盖写、`global` 与 `scope_id` 搭配的写入校验、删除幂等 |
| `internal/setting` | 默认值填充、部分键覆盖、布尔解析与回落、类型转换、仓库不可用时读用默认值写返回错误、仓库错误透传、运行时配置的逐项越界拒绝与跨字段约束、校验失败时不写入任何一行 |
| `internal/agent` | 名额的取得与归还、重复归还只计一次、替换容量后新上限即刻生效、旧名额归还不放宽新上限、非正数不改动容量 |
| `internal/llm` | 以 `httptest` 模拟上游，验证 multipart 组装、`/v1` 补齐、错误映射 |
| `internal/api/handler` | 参数校验、模型名取用顺序、模型缺失与禁用的响应、运行时配置的往返一致、表内缺失时回落到 YAML、并发上限同步到 semaphore、越界请求不改动 semaphore、注册表缺失时仍可保存 |

运行：`go test ./internal/setting/... ./internal/repo/... ./internal/llm/... ./internal/api/... -v`

### 3.2 系统测试（Python）

`tests/python/` 下补充对外转录接口的用例：正常转录、缺少文件、模型未配置、超大文件、API Key 鉴权失败。由使用者自行运行。
