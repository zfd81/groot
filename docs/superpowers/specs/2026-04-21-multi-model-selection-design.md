---
name: multi-model-selection-design
description: 支持通过 HTTP header 动态选择 LLM 模型的设计规格
type: project
---

# 多模型选择功能设计规格

**日期**: 2026-04-21
**状态**: 待审核

## 1. 功能概述

### 1.1 目标

支持在 `/chat` SSE 接口请求时，通过 HTTP header 动态指定使用的 LLM 模型，实现同一会话的不同对话可以使用不同模型。

### 1.2 使用场景

1. **同一会话不同模型**：用户先调用视觉模型解析图片，再调用其他模型做后续处理
2. **不同客户/场景**：不同任务使用不同模型（复杂任务用 GPT-4，简单任务用 GPT-3.5）

### 1.3 设计决策

- **方案选择**：按请求创建模型实例（无缓存）
- **兼容性策略**：严格迁移，不兼容旧配置
- **错误处理**：严格校验，不存在则返回 400 错误

## 2. 配置文件格式变更

### 2.1 字段改名

将 `active_model` 改名为 `default_model`：

```yaml
llm:
  default_model: gpt-4o           # 默认模型名称（对应 models 中的某个 key）
  models:
    gpt-4o:                       # 模型配置名称（自定义）
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4o               # 实际调用时的模型名称
      max_completion_tokens: 4096
      temperature: 0.7
    claude-3.5:
      base_url: https://api.anthropic.com/v1
      api_key: ${ANTHROPIC_API_KEY}
      model: claude-3-5-sonnet-20241022
      max_completion_tokens: 4096
      temperature: 0.7
```

### 2.2 兼容性处理

**严格迁移策略**：
- 不兼容旧配置 `active_model`
- 服务启动时检查配置文件，如果存在 `active_model` 字段则报错退出
- 提示用户必须更新为 `default_model`

### 2.3 影响范围

需要更新的文件：
- 所有配置文件（`~/.groot/config.yaml`、测试配置等）
- 文档（`README.md`、`docs/superpowers/specs/*.md`）
- 代码引用（详见第5节）
- 测试用例（`tests/python/*.py`）

## 3. API 接口变更

### 3.1 新增 Header 参数

在 `/chat` SSE 接口新增可选 header `X-Model-Name`：

```
POST /chat
Headers:
  X-Session-ID: <session_id>      (可选，已存在)
  X-Model-Name: <model_name>      (可选，新增)
  X-API-Key: <api_key>            (可选，认证相关)
```

### 3.2 处理逻辑

1. **提取模型名称**：从 `X-Model-Name` header 提取值
2. **决定使用模型**：
   - 有 `X-Model-Name` 且有效 → 使用指定模型
   - 无 `X-Model-Name` 或为空 → 使用 `default_model`
3. **验证模型名称**：
   - 检查是否存在于 `models` 配置中
   - 不存在 → 返回 400 错误：`{"status": "invalid_model", "message": "模型 'xxx' 不存在"}`

### 3.3 使用示例

```bash
# 使用默认模型
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{"instruction": "你好"}'

# 指定使用 claude-3.5 模型
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -H "X-Model-Name: claude-3.5" \
  -d '{"instruction": "你好"}'

# 指定不存在的模型（返回错误）
curl -X POST http://localhost:8080/chat \
  -H "X-Model-Name: unknown-model" \
  -d '{"instruction": "你好"}'
# 返回 400: {"status": "invalid_model", "message": "模型 'unknown-model' 不存在"}
```

## 4. 数据流和组件交互

### 4.1 完整流程

```
用户请求 (带 X-Model-Name header)
    ↓
ChatHandler.Handle()
    ├─ 1. 提取 X-Model-Name header
    ├─ 2. 验证模型名称存在性
    │   ├─ 不存在 → 返回 400 错误
    │   └─ 存在或为空 → 继续
    ├─ 3. 确定最终使用的模型名称
    │   ├─ 有 X-Model-Name → 使用指定的模型
    │   └─ 无 X-Model-Name → 使用 default_model
    └─ 4. 创建 Task 对象（包含 modelName）
        ↓
Executor.Execute(sessionID, task, sseWriter, cancelCh)
    ↓
Engine.Run(ctx, instruction, prompt, attachments, history, modelName, cb)
    ├─ 5. 调用 llm.NewChatModel(ctx, llmConfig, modelName)
    │   └─ LLMConfig.GetModelByName(modelName) → ModelConfig
    ├─ 6. 创建 ChatModel 实例
    ├─ 7. 构建系统指令和消息列表
    └─ 8. 执行 Agent 并返回结果
```

### 4.2 组件职责

| 组件 | 职责 |
|------|------|
| ChatHandler | 提取和验证模型名称，决定最终使用的模型 |
| Executor | 传递模型名称到 Engine |
| Engine | 根据模型名称创建 ChatModel，执行 Agent |
| LLMConfig | 提供模型配置查询能力（`GetModelByName`、`GetDefaultModel`） |

## 5. 代码实现细节

### 5.1 internal/config/config.go

**结构体字段改名**：

```go
type LLMConfig struct {
    DefaultModel string                 `yaml:"default_model"`  // 改名：ActiveModel → DefaultModel
    Models       map[string]ModelConfig `yaml:"models"`
}
```

**方法改名和新增**：

```go
// GetDefaultModel：改名（原 GetActiveModel）
func (c *LLMConfig) GetDefaultModel() *ModelConfig {
    if model, ok := c.Models[c.DefaultModel]; ok {
        model.APIKey = ExpandEnv(model.APIKey)
        return &model
    }
    return nil
}

// GetModelByName：新增方法，支持按名称获取模型配置
func (c *LLMConfig) GetModelByName(name string) *ModelConfig {
    if name == "" {
        return c.GetDefaultModel()
    }
    if model, ok := c.Models[name]; ok {
        model.APIKey = ExpandEnv(model.APIKey)
        return &model
    }
    return nil
}

// ValidateModel：新增方法，验证模型名称是否存在
func (c *LLMConfig) ValidateModel(name string) bool {
    if name == "" {
        return true  // 空值合法，将使用默认模型
    }
    _, exists := c.Models[name]
    return exists
}
```

**新增配置验证函数**：

```go
// ValidateLLMConfig：启动时验证配置有效性
func ValidateLLMConfig(cfg *LLMConfig) error {
    if len(cfg.Models) == 0 {
        return fmt.Errorf("models 配置不能为空")
    }

    if cfg.DefaultModel == "" {
        // 警告日志，使用第一个模型作为默认
        for name := range cfg.Models {
            cfg.DefaultModel = name
            break
        }
    }

    if !cfg.ValidateModel(cfg.DefaultModel) {
        return fmt.Errorf("default_model '%s' 不存在于 models 配置中", cfg.DefaultModel)
    }

    return nil
}
```

### 5.2 internal/config/defaults.go

```go
LLM: LLMConfig{
    DefaultModel: "gpt-4o",  // 改名：ActiveModel → DefaultModel
    Models: map[string]ModelConfig{
        // ...
    },
},
```

### 5.3 internal/llm/chatmodel.go

**函数签名改动**：

```go
// NewChatModel：新增 modelName 参数
func NewChatModel(ctx context.Context, cfg config.LLMConfig, modelName string) (model.BaseChatModel, error) {
    // 使用 GetModelByName 替代 GetActiveModel
    modelCfg := cfg.GetModelByName(modelName)
    if modelCfg == nil {
        if modelName == "" {
            return nil, fmt.Errorf("default model not found in config")
        }
        return nil, fmt.Errorf("model '%s' not found in config", modelName)
    }

    // 后续逻辑不变...
    timeout := time.Duration(modelCfg.MaxTokens) * time.Second
    if timeout < 30*time.Second {
        timeout = 30 * time.Second
    }

    chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
        Model:   modelCfg.Model,
        APIKey:  modelCfg.APIKey,
        BaseURL: modelCfg.BaseURL,
        Timeout: timeout,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to create chat model: %w", err)
    }

    return chatModel, nil
}
```

### 5.4 internal/agent/engine.go

**Run 方法签名改动**：

```go
func (e *Engine) Run(
    ctx context.Context,
    instruction string,
    prompt string,
    attachmentPaths []AttachmentPath,
    historyMessages []memory.Message,
    modelName string,  // 新增参数
    cb *ProgressCallback,
) (*RunResult, error) {
    // 1. Create ChatModel with modelName
    chatModel, err := llm.NewChatModel(ctx, e.llmConfig, modelName)
    if err != nil {
        return nil, fmt.Errorf("failed to create chat model: %w", err)
    }

    // 后续逻辑不变...
}
```

### 5.5 internal/agent/executor.go

**Execute 方法改动**（需要检查实际代码）：

```go
func (e *Executor) Execute(
    sessionID string,
    task *Task,
    sseWriter *SSEWriter,
    cancelCh chan struct{},
) {
    // 传递 modelName 到 Engine.Run
    result, err := e.engine.Run(
        ctx,
        task.Instruction,
        task.Prompt,
        attachmentPaths,
        task.HistoryMessages,
        task.ModelName,  // 新增
        cb,
    )
}
```

### 5.6 internal/agent/types.go (Task 结构体)

**新增字段**：

```go
type Task struct {
    ID              string
    Instruction     string
    Prompt          string
    Status          Status
    StartTime       time.Time
    Steps           []StepRecord
    Progress        *ProgressInfo
    Round           int
    HistoryMessages []memory.Message
    Attachments     []Attachment
    ModelName       string  // 新增字段
}
```

### 5.7 internal/api/handler/chat.go

**新增处理逻辑**：

```go
func (h *ChatHandler) Handle(ctx context.Context, rc *app.RequestContext) {
    // ... 现有代码 ...

    // 新增：提取 X-Model-Name header
    modelName := string(rc.GetHeader("X-Model-Name"))

    // 新增：验证模型名称
    if modelName != "" && !h.config.LLM.ValidateModel(modelName) {
        rc.JSON(400, utils.H{
            "status":  "invalid_model",
            "message": fmt.Sprintf("模型 '%s' 不存在", modelName),
        })
        return
    }

    // ... 现有代码 ...

    // Task 结构体新增 modelName 字段
    task := &agent.Task{
        ID:              chatID,
        Instruction:     req.Instruction,
        Prompt:          req.Prompt,
        Status:          agent.StatusRunning,
        StartTime:       time.Now(),
        Steps:           []agent.StepRecord{},
        Progress:        &agent.ProgressInfo{},
        Round:           round,
        HistoryMessages: historyMessages,
        ModelName:       modelName,  // 新增
    }

    // ... 现有代码 ...
}
```

## 6. 错误处理和边界情况

### 6.1 错误场景处理

| 场景 | 处理方式 | 错误码 | 错误信息 |
|------|----------|--------|----------|
| `X-Model-Name` 指定不存在的模型 | 返回 400，拒绝请求 | `invalid_model` | `模型 'xxx' 不存在` |
| `default_model` 配置不存在 | 启动时验证，报错退出 | - | `default_model 'xxx' 不存在于 models 配置中` |
| `models` 配置为空 | 启动时验证，报错退出 | - | `models 配置不能为空` |
| `X-Model-Name` 为空字符串 | 视为未指定，使用默认模型 | 无错误 | - |
| `X-Model-Name` header 不存在 | 使用默认模型 | 无错误 | - |

### 6.2 边界情况

1. **配置文件无 `default_model`**：
   - 启动时自动使用第一个 models key 作为默认
   - 记录警告日志

2. **模型名称大小写**：
   - 严格匹配，`gpt-4o` 和 `GPT-4O` 视为不同
   - 用户需要精确传入配置中的 key 名称

3. **并发请求**：
   - 每个请求独立创建 ChatModel，无共享状态
   - 不同会话/对话可以同时使用不同模型，互不影响

## 7. 测试策略

### 7.1 功能测试

| 测试场景 | 测试内容 | 预期结果 |
|----------|----------|----------|
| 使用默认模型 | 不传 `X-Model-Name` header | 正常使用 `default_model` 配置的模型 |
| 指定有效模型 | 传 `X-Model-Name: claude-3.5` | 正常使用 claude-3.5 模型 |
| 指定不存在模型 | 传 `X-Model-Name: unknown` | 返回 400，错误码 `invalid_model` |
| 同会话切换模型 | 同一 `X-Session-ID`，不同对话用不同模型 | 每个对话使用各自指定的模型 |

### 7.2 配置迁移测试

| 测试场景 | 测试内容 | 预期结果 |
|----------|----------|----------|
| 使用旧配置 `active_model` | 启动服务 | 报错退出，提示必须使用 `default_model` |
| 使用新配置 `default_model` | 启动服务 | 正常启动 |
| 配置同时有 `active_model` 和 `default_model` | 启动服务 | 报错提示配置冲突 |

### 7.3 边界测试

| 测试场景 | 测试内容 | 预期结果 |
|----------|----------|----------|
| `default_model` 不存在 | 启动服务 | 报错退出 |
| `models` 为空 | 启动服务 | 报错退出 |
| `X-Model-Name` 为空字符串 | 发送请求 | 使用默认模型 |

## 8. 文档更新

需要更新的文档：

1. **README.md**：
   - 配置示例中的 `active_model` → `default_model`
   - API 文档中新增 `X-Model-Name` header 说明

2. **docs/superpowers/specs/2026-04-18-groot-agent-design.md**：
   - 更新所有 `active_model` 引用

3. **测试用例文档**：
   - `tests/reports/test-cases.md` 中更新配置示例

## 9. 实现步骤概览

1. 修改 `internal/config/config.go`：字段改名、新增方法
2. 修改 `internal/config/defaults.go`：字段改名
3. 修改 `internal/llm/chatmodel.go`：函数签名改动
4. 修改 `internal/agent/engine.go`：Run 方法签名改动
5. 修改 `internal/agent/types.go`：Task 新增字段
6. 修改 `internal/api/handler/chat.go`：新增模型提取和验证逻辑
7. 更新测试配置文件
8. 更新文档
9. 编写测试用例