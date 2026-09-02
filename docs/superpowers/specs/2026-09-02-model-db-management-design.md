# 模型配置数据库化管理设计文档

## 一、功能设计

### 1.1 功能概述

模型配置管理功能以数据库作为 LLM 模型配置的唯一存储，提供完整的模型生命周期管理能力。用户通过 WebUI 的设置界面即可查看、创建、修改、删除模型，切换默认模型，启用或禁用模型，并在保存前测试模型连通性。所有变更立即生效，无需重启服务。

该功能解决的问题：模型配置的管理入口统一到 Web 界面，配置变更即时可用，多节点共享数据库（MySQL/PostgreSQL）部署时各节点天然读到一致的模型配置。

### 1.2 能力清单

- **查看模型**：设置界面列出全部模型，展示名称、模型 ID、Base URL、默认标记、启用状态；API Key 以脱敏形式展示（只保留尾 4 位，如 `sk-****abcd`）。
- **创建模型**：填写名称、Base URL、API Key、模型 ID 及高级生成参数，保存后即可在聊天中使用。
- **修改模型**：编辑任意字段；API Key 留空表示保持原值不变。名称可重命名（保持全局唯一）。
- **删除模型**：二次确认后删除；默认模型禁止删除。
- **设为默认**：任意启用状态的模型可一键设为默认模型；全库有且至多一个默认模型。
- **启用/禁用**：禁用的模型不出现在聊天的模型下拉框中，配置保留；默认模型禁止禁用。
- **连接测试**：创建或编辑时可发起连通性测试，后端用所填配置发送一次最小 chat 请求并返回结果；测试不阻塞保存。
- **立即生效**：模型配置在每次使用时从数据库实时读取，任何增删改在下一次请求即生效。
- **零模型启动**：系统允许在没有任何模型配置的情况下启动，聊天请求在无可用模型时返回明确的引导性错误。
- **环境变量引用**：API Key 字段支持 `${ENV_VAR}` 形式引用环境变量，使用时展开。

### 1.3 整体架构

```
WebUI 设置-模型页（SettingsModal.vue）
      │  /web/models CRUD API（WebSession 认证）
      ▼
ModelService（internal/llm）
  ├─ 参数校验、默认模型规则、API Key 脱敏
  ├─ 连接测试
  ├─ 按名称构建 eino ChatModel
      │
      ▼
ModelRepo（internal/repo/model.go 接口 + internal/repo/modeldb/ 实现）
      │
      ▼
models 表（internal/db/migrate.go，sqlite/mysql/postgres 三方言 DDL）
```

- **ModelRepo**：数据访问层，接口定义在 `internal/repo/model.go`，实现在 `internal/repo/modeldb/`，与 users 表的 Repo 模式一致。
- **ModelService**：业务层，封装参数校验、默认模型的事务规则、连接测试、ChatModel 构建。engine、executor、subagent_registry、chat handler 持有 ModelService 接口，每次需要模型配置时按名称直查数据库（模型配置读频率低——每次 chat 请求读一次，SQLite 本地读为微秒级，不设缓存，多节点场景天然一致）。

### 1.4 数据模型

`models` 表：

| 字段 | 类型 | 说明 |
|------|------|------|
| id | 自增主键 | |
| name | text，唯一索引 | 模型逻辑名称，聊天请求按此引用 |
| base_url | text | OpenAI 兼容接口地址 |
| api_key | text | 明文存储，支持 `${ENV_VAR}` 引用 |
| model | text | 实际模型 ID |
| max_completion_tokens | int | 生效参数：调用 LLM 时透传 |
| max_context_tokens | int | 预留字段，暂不下发 |
| temperature | real | 生效参数：调用 LLM 时透传 |
| top_p | real | 预留字段，暂不下发 |
| frequency_penalty | real | 预留字段，暂不下发 |
| presence_penalty | real | 预留字段，暂不下发 |
| seed | int | 预留字段，暂不下发 |
| stop | text | 停止序列，字符串数组 JSON 序列化存储；预留字段，暂不下发 |
| thinking | bool | 生效参数：为真时以 ExtraFields 下发 |
| is_default | bool | 全表至多一条为真 |
| enabled | bool | 默认 true |
| created_at / updated_at | timestamp | |

**业务规则**：

- 默认模型唯一性由应用层保证：设为默认时在同一事务内先清除全表 is_default 再设置目标行。
- 创建首个模型时（库中原本没有任何模型），该模型自动成为默认模型。
- 默认模型必须处于启用状态；删除或禁用默认模型的请求被拒绝，需先将其他模型设为默认。
- 名称全局唯一，冲突时创建/重命名失败并返回明确错误。
- 会话与模型之间按名称松耦合：每次请求实时按名解析，重命名不影响后续请求的正确性（引用旧名的请求会得到"模型不存在"错误）。
- 调用 LLM（构建 ChatModel）时只透传 temperature、max_completion_tokens、thinking 三个采样参数；其余参数字段完整存储于数据库并通过 API 读写，作为未来扩展的预留能力，暂不下发给模型服务。

### 1.5 Web API 设计

所有端点挂在 WebSession 认证组下（登录后可用）：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/web/models` | 模型列表，含全部配置字段，api_key 脱敏；聊天下拉框复用此接口并由前端过滤 enabled |
| POST | `/web/models` | 创建模型 |
| PUT | `/web/models/:name` | 更新模型；api_key 为空字符串时保持库中原值 |
| DELETE | `/web/models/:name` | 删除模型；默认模型返回错误 |
| PUT | `/web/models/:name/default` | 设为默认模型 |
| POST | `/web/models/test` | 连接测试：请求体携带 base_url/api_key/model；编辑已有模型且未改 key 时可只传 name，由后端取库中 key |

创建与更新执行统一参数校验（base_url/api_key/model 非空、temperature ∈ [0,2]、top_p ∈ (0,1] 等，校验规则位于 ModelService）。

### 1.6 前端界面设计

改造 `web/src/components/settings/SettingsModal.vue` 的模型页：

- **列表视图**：每行展示名称、模型 ID、Base URL；默认模型显示 tag；行内提供启用开关、"设为默认"、编辑、删除按钮；顶部提供"新建模型"按钮。
- **创建/编辑表单**（内嵌面板）：编辑时表单以灰底面板嵌在该模型卡片内部展开，新建时挂在列表末尾的独立卡片中；必填项为 name、base_url、api_key（编辑时可留空）、model；temperature、max_completion_tokens、thinking 收进"高级参数"折叠区；底部提供"测试连接"按钮，以成功/失败提示反馈结果。
- **删除**：弹出二次确认；默认模型的删除与禁用控件置灰并提示原因。
- **聊天模型下拉框**（ChatInput.vue）：只显示 enabled 的模型。

### 1.7 错误处理

- chat 请求指定了不存在或已禁用的模型：返回 400，错误信息包含模型名。
- chat 请求未指定模型且库中无默认模型：返回"尚未配置模型"错误，前端引导用户前往设置页创建。
- 健康检查 `CheckConnection`：无默认模型时报告"未配置"状态而非失败。
- 创建/重命名时名称冲突：返回 409 语义的明确错误。
- 删除/禁用默认模型：返回明确的拒绝原因。

### 1.8 配置文件与启动行为

- `~/.groot/config.yaml` 不包含任何模型配置；`groot init` 生成的模板中没有 `llm` 段。
- 启动流程不校验模型存在性，models 表为空时正常启动。
- 首次使用时用户通过 WebUI 设置界面创建模型。

### 1.9 测试设计

- **Go 单元测试**（与源码同目录）：
  - `modeldb`：CRUD、名称唯一约束、is_default 事务切换。
  - `ModelService`：参数校验边界、默认模型删除/禁用保护、api_key 脱敏与"留空不改"逻辑、`${ENV_VAR}` 展开。
- **Python 系统测试**（`tests/python/`，用户执行）：models API 端到端增删改查、默认切换、连接测试端点、聊天请求引用被删除/禁用模型的报错路径。

## 二、迭代说明

### 2.1 与上一版差异

- 移除：`config.yaml` 中的 `llm` 段（`LLMConfig`/`ModelConfig` 的 YAML 加载、`groot init` 模板中的 llm 部分、启动时的 `ValidateLLMConfig` 强制校验）。
- 新增：`models` 数据库表及三方言 DDL；`ModelRepo` 接口与 `modeldb` 实现；`ModelService` 业务层；`/web/models` 的创建、更新、删除、设默认、连接测试端点。
- 调整：engine、executor、subagent_registry、chat handler 由持有 `config.LLMConfig` 值拷贝改为持有 `ModelService` 接口，模型配置按需实时读库；`GET /web/models` 由只读三字段扩展为返回全部配置字段（api_key 脱敏）；设置界面模型页由只读展示改为完整管理界面；聊天模型下拉框改为只显示启用的模型。
- 新增能力：默认模型界面化切换（is_default 入库）、启用/禁用开关、连接测试。
- 不做存量迁移：既有 `config.yaml` 中的模型配置不自动导入数据库，升级后用户在 WebUI 中手动重建。
- 调整（参数收敛）：调用 LLM 时由透传全部采样参数（top_p、frequency_penalty、presence_penalty、seed、stop 等）收敛为只透传 temperature、max_completion_tokens、thinking 三项；创建/编辑表单的"高级参数"折叠区同步只保留这三项。数据表结构与 API 字段不变，未透传的字段保留为扩展预留。
