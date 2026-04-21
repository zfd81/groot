# 多模型选择功能实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 支持通过 HTTP header `X-Model-Name` 动态选择 LLM 模型，实现同一会话的不同对话可以使用不同模型。

**Architecture:** 按请求创建模型实例，无缓存。配置字段改名 `active_model` → `default_model`，新增方法 `GetModelByName()`、`ValidateModel()`，数据流改造从 ChatHandler → Executor → Engine → LLM，逐层传递模型名称。

**Tech Stack:** Go, eino-ext (OpenAI client), Hertz HTTP framework, YAML configuration

---

## File Structure

**修改的文件：**
- `internal/config/config.go` - LLMConfig 字段改名、新增方法
- `internal/config/defaults.go` - DefaultConfig 字段改名
- `internal/llm/chatmodel.go` - NewChatModel 函数签名改动
- `internal/agent/engine.go` - Run 方法签名改动
- `internal/agent/executor.go` - Task 结构体新增字段、Execute 方法改动
- `internal/api/handler/chat.go` - 新增模型提取和验证逻辑
- `tests/python/conftest.py` - 测试配置更新
- `README.md` - 文档更新

---

### Task 1: 修改配置结构体和方法

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: 修改 LLMConfig 结构体字段名**

将 `ActiveModel` 改名为 `DefaultModel`：

```go
// LLMConfig holds LLM settings
type LLMConfig struct {
	DefaultModel string                 `yaml:"default_model"` // 改名：ActiveModel → DefaultModel
	Models       map[string]ModelConfig `yaml:"models"`
}
```

修改位置：`internal/config/config.go:35-38`

- [ ] **Step 2: 将 GetActiveModel 方法改名为 GetDefaultModel**

```go
// GetDefaultModel returns the default model configuration
func (c *LLMConfig) GetDefaultModel() *ModelConfig {
	if model, ok := c.Models[c.DefaultModel]; ok {
		// Expand environment variables in API key
		model.APIKey = ExpandEnv(model.APIKey)
		return &model
	}
	return nil
}
```

修改位置：`internal/config/config.go:149-157`，替换整个方法

- [ ] **Step 3: 新增 GetModelByName 方法**

在 `GetDefaultModel()` 方法后面新增：

```go
// GetModelByName returns the model configuration by name
// If name is empty, returns the default model
func (c *LLMConfig) GetModelByName(name string) *ModelConfig {
	if name == "" {
		return c.GetDefaultModel()
	}
	if model, ok := c.Models[name]; ok {
		// Expand environment variables in API key
		model.APIKey = ExpandEnv(model.APIKey)
		return &model
	}
	return nil
}

// ValidateModel checks if a model name exists in config
// Empty name is valid (will use default model)
func (c *LLMConfig) ValidateModel(name string) bool {
	if name == "" {
		return true // Empty is valid, will use default model
	}
	_, exists := c.Models[name]
	return exists
}
```

插入位置：`internal/config/config.go` 在 `GetDefaultModel()` 方法之后

- [ ] **Step 4: 新增 ValidateLLMConfig 函数**

在文件末尾新增配置验证函数：

```go
// ValidateLLMConfig validates LLM configuration at startup
func ValidateLLMConfig(cfg *LLMConfig) error {
	if len(cfg.Models) == 0 {
		return fmt.Errorf("models 配置不能为空")
	}

	if cfg.DefaultModel == "" {
		// Use first model as default if not specified
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

插入位置：`internal/config/config.go` 文件末尾

需要确保文件开头有 `"fmt"` import。

- [ ] **Step 5: 验证修改**

运行编译检查语法错误：

```bash
cd /Users/zhangfengda/workspace/groot && go build ./internal/config
```

预期：编译成功，无错误

- [ ] **Step 6: 提交**

```bash
git add internal/config/config.go
git commit -m "refactor: rename active_model to default_model, add GetModelByName and ValidateModel methods"
```

---

### Task 2: 修改默认配置

**Files:**
- Modify: `internal/config/defaults.go`

- [ ] **Step 1: 修改 DefaultConfig 中的 LLM 字段名**

```go
LLM: LLMConfig{
	DefaultModel: "gpt-4o", // 改名：ActiveModel → DefaultModel
	Models: map[string]ModelConfig{
		"gpt-4o": {
			BaseURL:     "https://api.openai.com/v1",
			APIKey:      "${OPENAI_API_KEY}",
			Model:       "gpt-4o",
			MaxTokens:   4096,
			Temperature: 0.7,
		},
	},
},
```

修改位置：`internal/config/defaults.go:14-24`

- [ ] **Step 2: 验证修改**

运行编译检查：

```bash
go build ./internal/config
```

预期：编译成功

- [ ] **Step 3: 提交**

```bash
git add internal/config/defaults.go
git commit -m "refactor: update DefaultConfig to use default_model field name"
```

---

### Task 3: 修改 LLM ChatModel 创建函数

**Files:**
- Modify: `internal/llm/chatmodel.go`

- [ ] **Step 1: 修改 NewChatModel 函数签名和实现**

```go
// NewChatModel creates an OpenAI-compatible ChatModel using eino-ext
// modelName parameter: if empty, uses default model; otherwise uses specified model
func NewChatModel(ctx context.Context, cfg config.LLMConfig, modelName string) (model.BaseChatModel, error) {
	// Get model config by name
	modelCfg := cfg.GetModelByName(modelName)
	if modelCfg == nil {
		if modelName == "" {
			return nil, fmt.Errorf("default model not found in config")
		}
		return nil, fmt.Errorf("model '%s' not found in config", modelName)
	}

	// Create OpenAI ChatModel with timeout based on max_tokens
	timeout := time.Duration(modelCfg.MaxTokens) * time.Second
	if timeout < 30*time.Second {
		timeout = 30 * time.Second // minimum 30s
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

替换整个函数：`internal/llm/chatmodel.go:15-38`

- [ ] **Step 2: 验证修改**

运行编译检查：

```bash
go build ./internal/llm
```

预期：编译成功

- [ ] **Step 3: 提交**

```bash
git add internal/llm/chatmodel.go
git commit -m "feat: add modelName parameter to NewChatModel function"
```

---

### Task 4: 修改 Agent Engine Run 方法

**Files:**
- Modify: `internal/agent/engine.go`

- [ ] **Step 1: 修改 Run 方法签名**

在参数列表中新增 `modelName string` 参数：

```go
// Run executes a task using eino's ChatModelAgent
func (e *Engine) Run(
	ctx context.Context,
	instruction string,
	prompt string,
	attachmentPaths []AttachmentPath,
	historyMessages []memory.Message,
	modelName string, // 新增参数
	cb *ProgressCallback,
) (*RunResult, error) {
```

修改位置：`internal/agent/engine.go:59-66`

- [ ] **Step 2: 修改 NewChatModel 调用**

将原来的调用改为传递 modelName：

```go
	// 1. Create ChatModel
	chatModel, err := llm.NewChatModel(ctx, e.llmConfig, modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}
```

修改位置：`internal/agent/engine.go:68-71`

- [ ] **Step 3: 验证修改**

运行编译检查：

```bash
go build ./internal/agent
```

预期：编译成功（此时 executor.go 还未修改，会有调用错误）

注意：此时编译会失败，因为 executor.go 中调用 Run 方法还未传递 modelName 参数。这是预期的，将在 Task 5 中修复。

- [ ] **Step 4: 提交**

```bash
git add internal/agent/engine.go
git commit -m "feat: add modelName parameter to Engine.Run method"
```

---

### Task 5: 修改 Task 结构体和 Executor Execute 方法

**Files:**
- Modify: `internal/agent/executor.go`

- [ ] **Step 1: 在 Task 结构体中新增 ModelName 字段**

```go
// Task represents a task (temporary definition until memory module)
type Task struct {
	ID              string
	Instruction     string
	Prompt          string
	Status          TaskStatus
	StartTime       time.Time
	EndTime         *time.Time
	Duration        int
	Result          string
	Error           *TaskError
	Steps           []StepRecord
	Attachments     []Attachment
	Caller          string
	Progress        *ProgressInfo
	Round           int
	HistoryMessages []memory.Message
	ModelName       string // 新增字段
}
```

修改位置：`internal/agent/executor.go:27-43`

- [ ] **Step 2: 修改 Execute 方法中调用 engine.Run 的部分**

在调用 `engine.Run()` 时传递 `task.ModelName` 参数：

```go
		// Run engine with simplified progress callback
		result, err := engine.Run(
			ctx,
			task.Instruction,
			task.Prompt,
			attachmentPaths,
			task.HistoryMessages,
			task.ModelName, // 新增参数
			&ProgressCallback{
				WriteThinking: func(content string) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
						return sse.WriteThinking(content)
					}
				},
				WriteMessage: func(content string) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
						return sse.WriteMessage(content)
					}
				},
				WriteToolCalls: func(toolCalls []ToolCall) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
						return sse.WriteToolCalls(toolCalls)
					}
				},
				WriteFinish: func(reason string) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
						return sse.WriteFinish(reason)
					}
				},
				WriteToolResult: func(toolCallID, toolName, content string) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
						return sse.WriteToolResult(toolCallID, toolName, content)
					}
				},
				WriteDone: func() error {
					return sse.WriteDone()
				},
			},
		)
```

修改位置：`internal/agent/executor.go:155-207`

- [ ] **Step 3: 验证修改**

运行编译检查，现在应该能够成功：

```bash
go build ./internal/agent
```

预期：编译成功

- [ ] **Step 4: 提交**

```bash
git add internal/agent/executor.go
git commit -m "feat: add ModelName field to Task struct and pass it to Engine.Run"
```

---

### Task 6: 修改 ChatHandler 添加模型提取和验证逻辑

**Files:**
- Modify: `internal/api/handler/chat.go`

- [ ] **Step 1: 在 Handle 方法开头提取 X-Model-Name header**

在解析请求之后（约第78行之后）新增：

```go
	// 2.5. 提取 X-Model-Name header
	modelName := string(rc.GetHeader("X-Model-Name"))

	// 2.6. 验证模型名称
	if modelName != "" && !h.config.LLM.ValidateModel(modelName) {
		rc.JSON(400, utils.H{
			"status":  "invalid_model",
			"message": fmt.Sprintf("模型 '%s' 不存在", modelName),
		})
		return
	}
```

插入位置：`internal/api/handler/chat.go` 在 `// 2. 检查 instruction 是否为空` 检查之后，`// 3. 提取 X-Session-ID` 之前（约第85行）

需要添加 `"fmt"` import（文件开头应该已经有了）

- [ ] **Step 2: 在创建 Task 对象时设置 ModelName 字段**

在 Task 结构体初始化时新增 `ModelName` 字段：

```go
	// 13. 构建 Task 对象
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
		ModelName:       modelName, // 新增字段
	}
```

修改位置：`internal/api/handler/chat.go:252-262`

- [ ] **Step 3: 验证修改**

运行编译检查：

```bash
go build ./internal/api/handler
```

预期：编译成功

- [ ] **Step 4: 提交**

```bash
git add internal/api/handler/chat.go
git commit -m "feat: add X-Model-Name header extraction and validation in ChatHandler"
```

---

### Task 7: 更新测试配置文件

**Files:**
- Modify: `tests/python/conftest.py`

- [ ] **Step 1: 查找配置中的 active_model 字段**

使用 grep 查找：

```bash
grep -n "active_model" tests/python/conftest.py
```

预期输出：找到配置位置

- [ ] **Step 2: 将 active_model 改为 default_model**

根据 grep 结果，将 `active_model` 字段名改为 `default_model`：

```python
"active_model": "mock-model"  # 改为
"default_model": "mock-model"
```

修改位置：根据 grep 输出确定具体行号（约第207行）

- [ ] **Step 3: 验证修改**

检查其他测试文件中是否也有 active_model：

```bash
grep -r "active_model" tests/python/
```

如果有其他文件也需要修改，确保全部更新。

- [ ] **Step 4: 提交**

```bash
git add tests/python/conftest.py tests/python/*.py
git commit -m "refactor: update test config to use default_model field name"
```

---

### Task 8: 更新 README 文档

**Files:**
- Modify: `README.md`

- [ ] **Step 1: 查找所有 active_model 引用**

```bash
grep -n "active_model" README.md
```

预期输出：找到多处引用

- [ ] **Step 2: 替换所有 active_model 为 default_model**

使用 sed 批量替换（或手动编辑）：

```bash
sed -i '' 's/active_model/default_model/g' README.md
```

注意：macOS 的 sed 需要 `-i ''` 参数

- [ ] **Step 3: 更新注释说明**

检查替换后的文档，确保注释说明也更新：

- `当前激活的模型` → `默认模型`
- `当前激活的模型名称` → `默认模型名称`

可以再次使用 sed：

```bash
sed -i '' 's/当前激活的模型/默认模型/g' README.md
sed -i '' 's/当前激活的模型名称/默认模型名称/g' README.md
```

- [ ] **Step 4: 新增 X-Model-Name header 说明**

在 API 文档部分（如果有的话）新增 `X-Model-Name` header 说明：

找到 `/chat` 接口文档部分，新增：

```
- `X-Model-Name` (可选): 指定使用的模型名称，对应配置中的 models key。如果不传则使用 default_model。
```

如果文档中没有 API header 说明部分，可以在合适位置添加。

- [ ] **Step 5: 验证修改**

检查文档内容是否正确：

```bash
grep -n "default_model\|X-Model-Name" README.md
```

预期：看到 default_model 和 X-Model-Name 的正确引用

- [ ] **Step 6: 提交**

```bash
git add README.md
git commit -m "docs: update README to use default_model and document X-Model-Name header"
```

---

### Task 9: 更新其他设计文档

**Files:**
- Modify: `docs/superpowers/specs/2026-04-18-groot-agent-design.md`

- [ ] **Step 1: 查找所有 active_model 引用**

```bash
grep -n "active_model" docs/superpowers/specs/2026-04-18-groot-agent-design.md
```

预期输出：找到多处引用

- [ ] **Step 2: 批量替换**

```bash
sed -i '' 's/active_model/default_model/g' docs/superpowers/specs/2026-04-18-groot-agent-design.md
sed -i '' 's/当前激活的模型/默认模型/g' docs/superpowers/specs/2026-04-18-groot-agent-design.md
sed -i '' 's/当前激活的模型名称/默认模型名称/g' docs/superpowers/specs/2026-04-18-groot-agent-design.md
```

- [ ] **Step 3: 检查其他文档**

```bash
grep -r "active_model" docs/
```

如果有其他文档也需要修改，确保全部更新。

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/specs/2026-04-18-groot-agent-design.md docs/superpowers/plans/*.md
git commit -m "docs: update design specs to use default_model field name"
```

---

### Task 10: 运行完整编译和基础测试

**Files:**
- None (verification task)

- [ ] **Step 1: 完整编译项目**

```bash
go build ./cmd/groot
```

预期：编译成功，生成 `bin/groot` 二进制文件

- [ ] **Step 2: 运行 Python 测试**

确保测试环境配置正确后运行基础测试：

```bash
cd tests/python && pytest test_supplementary.py::TestLLMConfig::test_active_model_field -v
```

注意：此测试可能需要更新测试名称（active_model_field → default_model_field）

如果测试失败，检查测试代码是否也需要更新字段名。

- [ ] **Step 3: 手动启动服务测试**

启动服务（使用测试配置）：

```bash
./bin/groot --config tests/abs_path_test/groot_home/config.yaml
```

预期：服务正常启动，日志中无错误

- [ ] **Step 4: 测试 X-Model-Name header 功能**

发送测试请求：

```bash
# 测试默认模型
curl -X POST http://localhost:8180/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-api-key-2026" \
  -d '{"instruction": "测试"}'

# 测试指定模型
curl -X POST http://localhost:8180/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-api-key-2026" \
  -H "X-Model-Name: mock-model" \
  -d '{"instruction": "测试"}'

# 测试不存在的模型（应返回 400）
curl -X POST http://localhost:8180/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-api-key-2026" \
  -H "X-Model-Name: unknown-model" \
  -d '{"instruction": "测试"}'
```

预期：
- 默认模型：正常响应
- 指定 mock-model：正常响应
- unknown-model：返回 400 错误，`{"status": "invalid_model", "message": "模型 'unknown-model' 不存在"}`

- [ ] **Step 5: 停止服务并清理**

停止测试服务，清理临时文件。

---

### Task 11: 最终提交和验证

**Files:**
- None (final verification)

- [ ] **Step 1: 检查所有修改文件**

```bash
git status
```

预期：所有修改都已提交，无未提交的更改

- [ ] **Step 2: 检查 git 历史**

```bash
git log --oneline -11
```

预期：看到 11 个连续的 commit，从 "refactor: rename active_model..." 到最后的 commit

- [ ] **Step 3: 最终编译验证**

```bash
go build ./...
```

预期：所有包编译成功，无错误

- [ ] **Step 4: 创建最终汇总 commit（可选）**

如果需要，可以创建一个汇总标签或分支：

```bash
git tag -a v1.x.x-model-selection -m "Multi-model selection feature complete"
```

---

## Self-Review Checklist

**1. Spec coverage:**
- ✅ 配置字段改名 active_model → default_model（Task 1, 2）
- ✅ 新增 GetModelByName, ValidateModel 方法（Task 1）
- ✅ NewChatModel 函数签名改动（Task 3）
- ✅ Engine.Run 方法签名改动（Task 4）
- ✅ Task 结构体新增 ModelName 字段（Task 5）
- ✅ Executor.Execute 传递 modelName（Task 5）
- ✅ ChatHandler 提取和验证 X-Model-Name header（Task 6）
- ✅ 更新测试配置（Task 7）
- ✅ 更新文档（Task 8, 9）
- ✅ 错误处理：模型不存在返回 400（Task 6）

**2. Placeholder scan:**
- ✅ 无 "TBD"、"TODO"、"implement later" 等占位符
- ✅ 所有代码步骤都有完整代码
- ✅ 所有命令都有完整内容

**3. Type consistency:**
- ✅ Task.ModelName 字段类型为 string（Task 5）
- ✅ Engine.Run modelName 参数类型为 string（Task 4）
- ✅ NewChatModel modelName 参数类型为 string（Task 3）
- ✅ ChatHandler 提取的 modelName 类型为 string（Task 6）
- ✅ LLMConfig.DefaultModel 字段类型为 string（Task 1）
- ✅ GetModelByName 返回 *ModelConfig（Task 1）
- ✅ ValidateModel 返回 bool（Task 1）