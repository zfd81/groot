# 模型默认类型设计文档

## 一、功能设计

### 1.1 功能概述

模型管理为每个模型维护一组「默认类型」标记，分别表示该模型是否为系统的默认对话模型、默认语音模型、默认视觉模型。调用方不指定模型时，系统按用途取对应类型的默认模型：对话取默认对话模型，音频转录取默认语音模型，图片理解取默认视觉模型。

一个模型可以同时承担多种默认类型，例如同一个多模态模型既是默认对话模型又是默认视觉模型，无需为同一上游模型重复录入配置。

默认类型是对外接口与内部预处理的共同基础：

- 对外接口（对话、音频转录，以及后续的图片理解）请求中不带模型名时，使用对应类型的默认模型。
- 内部预处理（先把音频或图片识别成文字，再交给对话模型）按类型取默认模型完成识别。

解决的问题：

- 转录、图片理解等非对话能力需要各自的缺省模型，单一的默认标记无法表达。
- 缺省模型的维护入口需要统一在模型管理中，与模型的增删改放在一起。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 三种默认类型 | 默认对话（chat）、默认语音（voice）、默认视觉（vision） |
| 一模型多默认 | 同一模型可同时持有多种默认类型 |
| 每类型唯一 | 每种默认类型全库至多一个模型持有 |
| 设为默认 | 任意启用的模型可设为某一类型的默认，原持有者自动失去该类型 |
| 取消默认 | 语音、视觉默认可直接取消；对话默认只能通过把其他模型设为默认来转移 |
| 默认保护 | 持有任一默认类型的模型不可删除、不可禁用 |
| 首个模型 | 库中第一个模型自动成为默认对话模型并强制启用；语音、视觉默认不自动分配 |
| 按类型解析 | 服务层按「指定名称 → 对应类型默认模型」解析模型 |
| 转录缺省模型 | 转录请求不带模型名时使用默认语音模型 |
| UI 识别模型 | Web 界面语音输入的识别模型独立配置，首次读取时以默认语音模型为初值 |

### 1.3 数据模型

`models` 表以一个整数列 `default_flags` 按位记录默认类型：

```sql
default_flags INTEGER NOT NULL DEFAULT 0
```

| 位 | 值 | 类型 | 标识 |
|---|---|---|---|
| bit0 | 1 | 默认对话模型 | `chat` |
| bit1 | 2 | 默认语音模型 | `voice` |
| bit2 | 4 | 默认视觉模型 | `vision` |

例如 `default_flags = 5` 表示该模型同时是默认对话模型与默认视觉模型。新增默认类型时分配新的位，表结构不变。三种数据库方言（SQLite、MySQL、PostgreSQL）统一使用整数类型。

Go 侧定义：

```go
// DefaultFlag 模型默认类型，按位组合
type DefaultFlag int

const (
    DefaultChat   DefaultFlag = 1 << iota // 1 默认对话模型
    DefaultVoice                          // 2 默认语音模型
    DefaultVision                         // 4 默认视觉模型
)
```

`repo.Model` 持有 `DefaultFlags DefaultFlag` 字段，并提供 `Has(flag DefaultFlag) bool` 判断是否持有某一类型。`DefaultFlag` 与字符串标识 `chat`、`voice`、`vision` 互相转换，供 HTTP 层使用。

### 1.4 数据访问层

`ModelRepo` 中与默认类型相关的方法：

| 方法 | 说明 |
|---|---|
| `GetDefault(ctx, flag)` | `WHERE (default_flags & ?) <> 0 ORDER BY id LIMIT 1`，未找到返回 `ErrNotFound` |
| `SetDefault(ctx, name, flag)` | 事务内先清除全表该位，再为目标行置位；目标不存在返回 `ErrNotFound` |
| `ClearDefault(ctx, name, flag)` | 清除目标行的该位；目标不存在返回 `ErrNotFound` |

清位使用减法而非按位取反：

```sql
UPDATE models SET default_flags = default_flags - ? WHERE (default_flags & ?) <> 0
```

MySQL 的 `~` 运算结果为无符号 64 位整数，与 SQLite、PostgreSQL 行为不一致；条件已保证该位为 1，减去该位值与清位等价，三种方言结果一致。

`Update` 不修改 `default_flags`，默认类型只经 `SetDefault` 与 `ClearDefault` 变更。

### 1.5 业务层规则

`ModelService` 提供：

| 方法 | 规则 |
|---|---|
| `Resolve(ctx, name, flag)` | `name` 非空按名称取；为空取 `flag` 类型的默认模型。模型不存在返回 `ErrModelNotFound`，禁用返回 `ErrModelDisabled`，无该类型默认返回 `ErrNoDefaultModel`（错误信息区分类型）。APIKey 中的 `${ENV_VAR}` 展开 |
| `GetByName(ctx, name)` | 等价于 `Resolve(ctx, name, DefaultChat)`，对话与子 Agent 使用 |
| `Create` | 库中无模型时，新模型获得 `DefaultChat` 并强制启用 |
| `Update` | 持有任一默认类型的模型不可禁用，返回 `ErrDefaultProtected` |
| `Delete` | 持有任一默认类型的模型不可删除，返回 `ErrDefaultProtected` |
| `SetDefault(ctx, name, flag)` | 禁用的模型返回 `ErrModelDisabled` |
| `ClearDefault(ctx, name, flag)` | `flag` 为 `DefaultChat` 时返回 `ErrChatDefaultRequired`；模型未持有该类型时直接成功 |

错误信息：

- `ErrDefaultProtected`：「默认模型不允许删除或禁用，请先取消其默认标记」
- `ErrChatDefaultRequired`：「默认对话模型不能取消，请将其他模型设为默认对话模型」
- `ErrNoDefaultModel` 按类型给出：对话为「尚未配置模型，请在设置中创建模型」，语音为「未配置默认语音模型，请在模型管理中设置」，视觉为「未配置默认视觉模型，请在模型管理中设置」

### 1.6 模型管理接口

以下为 Web 界面自用接口（`/web` 前缀，WebSession 认证）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/web/models` | 列出模型 |
| PUT | `/web/models/:name/default?type=chat\|voice\|vision` | 设为该类型默认，`type` 省略时为 `chat` |
| DELETE | `/web/models/:name/default?type=voice\|vision` | 取消该类型默认 |

`type` 取值非法时返回 `400 invalid_request`；取消对话默认返回 `400 default_chat_required`。

列表响应：

```json
{
  "models": [
    {
      "name": "gpt-4o",
      "default_types": ["chat", "vision"],
      "enabled": true
    }
  ],
  "defaults": { "chat": "gpt-4o", "voice": "whisper-1", "vision": "gpt-4o" },
  "total": 2
}
```

- `default_types`：该模型持有的默认类型，按 chat、voice、vision 顺序排列，未持有时为空数组。
- `defaults`：各类型默认模型的名称，未设置时为空串。

### 1.7 音频转录

`/audio/transcriptions`（对外，API Key 认证）与 `/web/audio/transcriptions`（Web，WebSession 认证）使用同一套模型解析规则：

1. 表单字段 `model`
2. 请求头 `X-Model-Name`
3. 默认语音模型
4. 以上均无，返回 `400 invalid_model`「未配置默认语音模型，请在模型管理中设置」

显式指定的模型不存在或已禁用时返回 `400 invalid_model`，不回落到默认语音模型。

转录接口不读取配置表中的语音配置。Web 界面在发起转录时，通过请求头 `X-Model-Name` 传递设置中的识别模型。

### 1.8 语音设置

配置表 `voice.model` 表示 Web 界面语音输入使用的识别模型，空串表示不启用语音输入。首次读取时以默认语音模型为初值写入，此后与默认语音模型相互独立。读取、保存规则与界面交互见 `2026-10-01-voice-model-visibility-design.md`。

### 1.9 前端界面

#### 1.9.1 模型管理面板（ModelsPanel）

- 模型卡片标题右侧按持有的默认类型显示标签：「默认对话」「默认语音」「默认视觉」。
- 「···」菜单中，每种默认类型按模型当前状态只显示一项：未持有时显示「设为默认 X」，已持有时显示「取消默认 X」。

| 默认类型 | 未持有时 | 已持有时 |
|---|---|---|
| 对话 | 设为默认对话（模型已禁用时不可用） | 不显示（对话默认只能转移） |
| 语音 | 设为默认语音（模型已禁用时不可用） | 取消默认语音 |
| 视觉 | 设为默认视觉（模型已禁用时不可用） | 取消默认视觉 |

| 其他菜单项 | 禁用条件 |
|---|---|
| 编辑 | — |
| 启用 / 禁用 | 模型启用且持有任一默认类型时，「禁用」不可用 |
| 删除 | 持有任一默认类型 |

- 设为默认或取消默认成功后刷新模型列表，并同步刷新聊天页使用的模型元数据。

#### 1.9.2 模型元数据（meta store）

- 保存 `defaults`（chat、voice、vision 三个默认模型名）。
- 聊天输入区的模型下拉框以 `defaults.chat` 标注「默认」。

#### 1.9.3 语音设置分区与聊天输入区

见 `2026-10-01-voice-model-visibility-design.md`。

### 1.10 范围

本设计提供默认视觉模型的标识、维护与按类型解析能力（`Resolve(ctx, "", DefaultVision)`）。图片预处理流程与对外图片理解接口基于此能力另行设计。

### 1.11 测试

Go 单元测试：

- `internal/repo/modeldb`：`SetDefault` 置位并清除原持有者；一个模型持有多种类型；`ClearDefault` 只清目标位；`GetDefault` 按类型查询。三种类型互不影响。
- `internal/llm`：首个模型自动获得对话默认；持有任一默认类型时删除、禁用被拒；禁用模型不可设为默认；取消对话默认被拒；`Resolve` 的名称优先与按类型回落。
- `internal/api/handler`：转录模型解析顺序（表单 → 请求头 → 默认语音 → 报错）；显式指定失效模型不回落；模型接口 `type` 参数校验与响应中的 `default_types`、`defaults`。

## 二、迭代说明

### 2.1 与上一版差异

- 调整：`models.is_default`（布尔）替换为 `default_flags`（整数位掩码），建表语句直接使用新列，不做数据迁移（开发阶段）。
- 新增：默认语音模型、默认视觉模型两种默认类型；`ClearDefault` 取消默认操作及 `DELETE /web/models/:name/default` 接口。
- 调整：`PUT /web/models/:name/default` 增加 `type` 查询参数。
- 调整：模型列表响应 `is_default` 改为 `default_types`，`default` 改为 `defaults`。
- 调整：默认保护从「默认对话模型」扩展到「持有任一默认类型的模型」。
- 调整：转录接口不再读取配置表 `voice.model`，缺省时使用默认语音模型；Web 界面改为通过 `X-Model-Name` 传递设置中选定的模型。
- 调整：语音设置的识别模型与默认语音模型解耦，见 `2026-10-01-voice-model-visibility-design.md`。
- 调整：模型管理面板、语音设置分区、聊天输入区按上述接口变化更新。
- 调整：README 中转录接口的模型取用顺序说明。
