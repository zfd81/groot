# 语音输入按钮可用性设计文档

## 一、功能设计

### 1.1 功能概述

Web 聊天页的话筒按钮由识别模型配置决定是否出现：识别模型有值时话筒始终显示，识别模型为空时不显示。话筒能否真正开始录音，取决于运行环境（页面是否为安全上下文、浏览器是否提供麦克风 API、设备与授权状态），这些条件不参与「是否显示」的判断，只由点击时的提示告知使用者具体原因。

这样设计的目的是让话筒的可见性只反映一项明确的配置，而把「不能用」的各种原因统一收敛到点击这一动作上。使用者看到话筒，就知道识别模型已配置；点不动或点了没有进入录音，就能立即得到一句可执行的提示，而不是面对一个凭空消失的图标去猜是配置坏了还是环境不支持。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 配置决定可见性 | 识别模型非空时始终显示话筒；识别模型为空时不显示 |
| 点击时统一校验 | 点击话筒时才检查环境、设备与授权，不改变按钮外观 |
| 环境原因提示 | 非安全上下文下点击提示「需通过 HTTPS 或 localhost 访问」 |
| 浏览器能力提示 | 浏览器缺少麦克风或 Web Audio 能力时提示「当前浏览器不支持录音」 |
| 设备与授权提示 | 未检测到麦克风、授权被拒、录音失败分别给出对应提示 |
| 模型失效提示 | 配置的识别模型已被禁用或删除时，点击提示前往设置重新选择 |
| 禁用模型清空配置 | 模型被禁用或删除后，识别模型配置同步清空，设置面板回到「未选择」 |
| 重命名模型跟随 | 模型改名后，识别模型配置同步改为新名称 |
| 外观恒定 | 只要能显示，按钮外观在任何环境下一致，不因权限被拒或模型失效而变色或置灰 |

### 1.3 设计细节

#### 1.3.1 前端：可见性与可用性分离

可见性判断只依赖识别模型配置：

```ts
const showMic = computed(() => !!voice.value.model)
```

可用性判断在录音器内部完成，返回具体原因而非布尔值：

```ts
export type RecordingUnavailable = 'insecure' | 'unsupported'

// 返回 null 表示可以录音；否则返回不可用的原因
export function recordingUnavailable(): RecordingUnavailable | null
```

判定顺序：

| 条件 | 结果 |
|---|---|
| 存在 `navigator.mediaDevices.getUserMedia` 且存在 AudioContext | `null`（可用） |
| 缺少上述能力，且 `window.isSecureContext === false` | `'insecure'` |
| 缺少上述能力，且处于安全上下文 | `'unsupported'` |

`start()` 在申请麦克风之前先调用 `recordingUnavailable()`，不可用则抛出对应原因。`RecorderError` 增加 `'insecure'`，与已有的 `permission_denied`、`no_device`、`unsupported`、`failed` 一起由调用方映射为提示文案：

| 原因 | 文案键 |
|---|---|
| `insecure` | `chat.recordInsecure`（新增） |
| `unsupported` | `chat.recordUnsupported`（已有） |
| `no_device` | `chat.recordNoDevice`（已有） |
| `permission_denied` | `chat.recordNoPermission`（已有） |
| `failed` | `chat.recordFailed`（已有） |

识别模型已失效时，点击话筒直接提示 `chat.recordNoModel`，不进入录音流程。

按钮的 `disabled` 只用于两个真实进行中的状态：正在识别（`transcribing`）与正在发送（`sending`）。权限被拒、模型失效、环境不支持都不改变按钮的禁用状态与样式。

新增 i18n 文案：

```ts
// zh-cn
recordInsecure: '语音输入需通过 HTTPS 或 localhost 访问，当前页面环境不允许使用麦克风'
// en
recordInsecure: 'Voice input requires HTTPS or localhost; the current page environment does not allow microphone access'
```

#### 1.3.2 后端：模型变更时同步识别模型配置

模型的禁用、删除、重命名通过 `ModelsHandler.Update` 与 `ModelsHandler.Delete` 进入。两个处理函数在模型操作成功之后同步识别模型配置：

| 模型操作 | 识别模型配置的处理 |
|---|---|
| 重命名（`name` → `m.Name`） | 原值等于旧名称时改为新名称 |
| 禁用（`enabled=false`） | 原值等于该名称时写入空串 |
| 删除 | 原值等于该名称时写入空串 |

同一操作同时涉及重命名与禁用时，以清空为准。

配置对象新增两个条件写入方法，语义自包含，便于单元测试：

```go
// ClearVoiceModelIf 当 voice.model 等于 model 时写入空串。
func (s *Settings) ClearVoiceModelIf(ctx context.Context, model string) error

// RenameVoiceModelIf 当 voice.model 等于 oldName 时改为 newName。
func (s *Settings) RenameVoiceModelIf(ctx context.Context, oldName, newName string) error
```

写入采用空串而不是删除配置行：`voice.model` 行不存在表示「从未确定过识别模型」，下次读取会以默认 ASR 模型写入初值；行存在且值为空串表示「确定不启用语音输入」，不会被自动填充。

`ModelsHandler` 增加 `*setting.Settings` 依赖，由 `server.go` 注入。

#### 1.3.3 失败处理

配置同步发生在模型操作成功之后。若同步写入失败，记录错误日志并仍向调用方返回成功：模型的变更已经落库，返回失败会误导调用方重试已完成的模型操作。识别模型配置会在下一次模型禁用、删除或重命名时再次有机会被纠正；在此之前，前端加载模型列表后仍能通过「模型失效」提示告知使用者。

条件写入采用「先读当前值再写」的方式，读与写之间存在窄竞态：若使用者在两个动作之间刚好改动了识别模型，可能清空或改名一个刚设置的值。该窗口与 `GetVoice` 首次读取写入初值的已知竞态同类，范围很窄，予以接受。

## 二、迭代说明

### 2.1 与上一版差异

- 新增：`recordingUnavailable()` 以原因替代布尔判断；`RecorderError` 增加 `insecure`；i18n 增加 `chat.recordInsecure`。
- 新增：模型禁用、删除时清空识别模型配置，重命名时同步新名称。
- 移除：`isRecordingSupported()` 布尔检测；`recordSupported` 参与可见性判断；`micDenied` 状态与权限被拒后置灰的做法；`.mic-warn` 警告色样式。
- 调整：`showMic` 由「识别模型非空且浏览器支持」改为只看「识别模型非空」；话筒悬浮文案不再因模型失效而改变，统一为「语音输入」，失效原因在点击时提示。
- 调整：`ModelsHandler` 构造参数增加配置对象。

### 2.2 方案选择

发现「识别模型指向已被禁用或删除的模型」这一状态时，有两种处理思路：

1. **前端把失效值视为未配置**：不改数据库，前端发现识别模型不在启用列表时按空处理，话筒隐藏。代价是数据库残留失效值，设置面板仍需单独处理才能不显示它，同一条规则要在两处维护；其他客户端读取配置时仍会看到不存在的模型。
2. **模型变更时清空配置**（采用）：在禁用、删除、重命名的入口处同步配置，数据库始终与实际情况一致，设置面板自然回到「未选择」，所有客户端看到同一状态，前端可见性判断也最简单。

选择方案 2。代价是临时禁用一个模型会永久丢掉语音配置，重新启用后需要重选；这是「配置必须反映真实可用状态」这一要求的直接结果，予以接受。
