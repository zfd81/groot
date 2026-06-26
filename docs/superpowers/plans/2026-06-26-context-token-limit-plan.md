# 上下文窗口 Token 限制功能实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为上下文窗口控制增加按 token 总量截断的能力，在现有按轮数截断基础上新增按 token 预算截断

**Architecture:** 
- 新增独立的 token 估算器模块（混合策略：中文按 1.5 token/字符，英文按 4 字符/token）
- Memory Manager 新增方法支持两层截断（先轮数后 token）
- ModelConfig 新增 MaxContextTokens 字段，Handler 调用时传入

**Tech Stack:** Go 1.21+, 现有 memory 模块和 config 模块

---

## 文件结构

**新增文件：**
- `internal/memory/token_estimator.go` - Token 估算器接口和默认实现
- `internal/memory/token_estimator_test.go` - Token 估算器单元测试

**修改文件：**
- `internal/config/config.go` - ModelConfig 新增 MaxContextTokens 字段
- `internal/config/defaults.go` - 默认配置初始化 MaxContextTokens
- `internal/config/template.go` - 配置模板添加注释示例
- `internal/memory/manager.go` - 新增 GetContextMessagesWithTokenLimit 方法
- `internal/memory/memory.go` - 接口新增方法签名
- `internal/memory/memory_test.go` - 新增 token 截断逻辑测试
- `internal/api/handler/chat.go` - 调用新方法并传入 token 预算
- `internal/config/config_test.go` - 配置字段测试

---

### Task 1: Token 估算器 - 接口和实现

**Files:**
- Create: `internal/memory/token_estimator.go`

- [ ] **Step 1: 写 Token 估算器接口和默认实现的失败测试**

创建测试文件并编写测试用例（先写测试，代码还不存在会失败）

```go
// internal/memory/token_estimator_test.go
package memory

import "testing"

func TestDefaultTokenEstimator_Estimate_PureEnglish(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "Hello world"  // 11 字符，预期 11/4 = 2 tokens
	result := estimator.Estimate(text)
	expected := 2
	if result != expected {
		t.Errorf("纯英文估算错误: got %d, want %d", result, expected)
	}
}

func TestDefaultTokenEstimator_Estimate_PureChinese(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "你好世界"  // 4 个中文字符，预期 4*1.5 = 6 tokens
	result := estimator.Estimate(text)
	expected := 6
	if result != expected {
		t.Errorf("纯中文估算错误: got %d, want %d", result, expected)
	}
}

func TestDefaultTokenEstimator_Estimate_Mixed(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "Hello 世界"  // 6 英文字符(1 token) + 2 中文字符(3 tokens) = 4 tokens
	result := estimator.Estimate(text)
	expected := 4
	if result != expected {
		t.Errorf("中英混合估算错误: got %d, want %d", result, expected)
	}
}

func TestDefaultTokenEstimator_Estimate_Empty(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	result := estimator.Estimate("")
	if result != 0 {
		t.Errorf("空字符串应返回 0: got %d", result)
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

运行命令：
```bash
go test ./internal/memory -run TestDefaultTokenEstimator -v
```

预期输出：`undefined: DefaultTokenEstimator`

- [ ] **Step 3: 实现 Token 估算器**

```go
// internal/memory/token_estimator.go
package memory

// TokenEstimator 估算文本的 token 数量
type TokenEstimator interface {
	Estimate(text string) int
}

// DefaultTokenEstimator 默认实现：混合策略
type DefaultTokenEstimator struct{}

// Estimate 估算文本 token 数
// 中日韩字符: 1.5 token/字符
// 其他字符: 4 字符/token
func (e *DefaultTokenEstimator) Estimate(text string) int {
	if text == "" {
		return 0
	}

	cjkCount := 0
	otherCount := 0

	for _, r := range text {
		if isCJK(r) {
			cjkCount++
		} else {
			otherCount++
		}
	}

	return int(float64(cjkCount)*1.5) + otherCount/4
}

// isCJK 判断是否为中日韩字符
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3040 && r <= 0x309F) || // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) || // Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Hangul
}
```

- [ ] **Step 4: 运行测试验证通过**

运行命令：
```bash
go test ./internal/memory -run TestDefaultTokenEstimator -v
```

预期输出：所有测试 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/memory/token_estimator.go internal/memory/token_estimator_test.go
git commit -m "feat: 添加 token 估算器接口和默认实现

- 新增 TokenEstimator 接口
- 实现 DefaultTokenEstimator（中文 1.5 token/字，英文 4 字符/token）
- 覆盖纯英文、纯中文、混合、空字符串场景

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 2: 配置层 - 新增 MaxContextTokens 字段

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/template.go`

- [ ] **Step 1: 写配置字段解析的失败测试**

```go
// internal/config/config_test.go - 在文件末尾添加
func TestModelConfig_MaxContextTokens_Default(t *testing.T) {
	cfg := DefaultConfig()
	defaultModel := cfg.LLM.GetDefaultModel()
	if defaultModel == nil {
		t.Fatal("默认模型不存在")
	}
	if defaultModel.MaxContextTokens != 0 {
		t.Errorf("MaxContextTokens 默认值错误: got %d, want 0", defaultModel.MaxContextTokens)
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

运行命令：
```bash
go test ./internal/config -run TestModelConfig_MaxContextTokens -v
```

预期输出：`unknown field 'MaxContextTokens'`

- [ ] **Step 3: 在 ModelConfig 中添加 MaxContextTokens 字段**

```go
// internal/config/config.go:43-56（ModelConfig 结构体）
type ModelConfig struct {
	BaseURL             string   `yaml:"base_url"`
	APIKey              string   `yaml:"api_key"`
	Model               string   `yaml:"model"`
	MaxCompletionTokens int      `yaml:"max_completion_tokens"`
	MaxContextTokens    int      `yaml:"max_context_tokens"` // 新增：输入上下文 token 预算
	Temperature         float64  `yaml:"temperature"`
	TopP                float64  `yaml:"top_p"`
	FrequencyPenalty    float64  `yaml:"frequency_penalty"`
	PresencePenalty     float64  `yaml:"presence_penalty"`
	Seed                int      `yaml:"seed"`
	Stop                []string `yaml:"stop"`
	Thinking            bool     `yaml:"thinking"`
}
```

- [ ] **Step 4: 更新 DefaultConfig 初始化 MaxContextTokens**

```go
// internal/config/defaults.go:16-27（DefaultConfig 中的 Models 部分）
Models: map[string]ModelConfig{
	"gpt-4o": {
		BaseURL:              "https://api.openai.com/v1",
		APIKey:               "${OPENAI_API_KEY}",
		Model:                "gpt-4o",
		MaxCompletionTokens:  4096,
		MaxContextTokens:     0, // 新增：0 表示不限制
		Temperature:          0.7,
		TopP:                 1.0,
		FrequencyPenalty:     0.0,
		PresencePenalty:      0.0,
	},
},
```

- [ ] **Step 5: 更新配置模板添加注释**

```go
// internal/config/template.go:19-21（在 max_completion_tokens 后面添加）
      max_completion_tokens: 4096            # 最大输出 Token 数
      max_context_tokens: 0                  # 输入上下文 Token 预算（0 表示不限制）
      temperature: 0.7
```

- [ ] **Step 6: 运行测试验证通过**

运行命令：
```bash
go test ./internal/config -run TestModelConfig_MaxContextTokens -v
```

预期输出：PASS

- [ ] **Step 7: 提交**

```bash
git add internal/config/config.go internal/config/defaults.go internal/config/template.go internal/config/config_test.go
git commit -m "feat(config): 为 ModelConfig 添加 MaxContextTokens 字段

- 新增 max_context_tokens 配置项（输入预算）
- 默认值为 0（不限制，向后兼容）
- 配置模板添加注释说明

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 3: Memory 层 - 实现 Token 截断逻辑

**Files:**
- Modify: `internal/memory/memory.go`
- Modify: `internal/memory/manager.go`
- Test: `internal/memory/memory_test.go`

- [ ] **Step 1: 写 GetContextMessagesWithTokenLimit 的失败测试**

```go
// internal/memory/memory_test.go - 在文件末尾添加
func TestManager_GetContextMessagesWithTokenLimit_OnlyTokenLimit(t *testing.T) {
	mgr := setupTestManager(t)
	sessionID := "test-session-token-limit"

	// 创建会话
	if err := mgr.CreateSession(sessionID, "user1"); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	// 添加 3 轮消息
	// 轮1: "A" (1 token)
	mgr.AppendMessage(sessionID, &Message{Round: 1, Instruction: "A", Result: "A"})
	// 轮2: "BBBB" (1 token)
	mgr.AppendMessage(sessionID, &Message{Round: 2, Instruction: "BBBB", Result: "BBBB"})
	// 轮3: "CCCCCCCC" (2 tokens)
	mgr.AppendMessage(sessionID, &Message{Round: 3, Instruction: "CCCCCCCC", Result: "CCCCCCCC"})

	// 限制 3 tokens：应该返回轮2和轮3（1+2=3 tokens）
	msgs, err := mgr.GetContextMessagesWithTokenLimit(sessionID, -1, 3)
	if err != nil {
		t.Fatalf("GetContextMessagesWithTokenLimit() 失败: %v", err)
	}

	if len(msgs) != 2 {
		t.Errorf("期望返回 2 条消息（轮2+轮3），实际 %d 条", len(msgs))
	}
	if len(msgs) > 0 && msgs[0].Round != 2 {
		t.Errorf("第一条消息应该是轮2，实际 %d", msgs[0].Round)
	}
}

func TestManager_GetContextMessagesWithTokenLimit_BothLimits(t *testing.T) {
	mgr := setupTestManager(t)
	sessionID := "test-session-both-limits"

	if err := mgr.CreateSession(sessionID, "user1"); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	// 添加 5 轮消息
	for i := 1; i <= 5; i++ {
		mgr.AppendMessage(sessionID, &Message{
			Round:       i,
			Instruction: "AAAA", // 每轮 1 token
			Result:      "AAAA",
		})
	}

	// windowSize=3 先截到最近 3 轮（轮3、4、5）
	// maxContextTokens=2 再截到 2 tokens（轮4、5）
	msgs, err := mgr.GetContextMessagesWithTokenLimit(sessionID, 3, 2)
	if err != nil {
		t.Fatalf("GetContextMessagesWithTokenLimit() 失败: %v", err)
	}

	if len(msgs) != 2 {
		t.Errorf("期望返回 2 条消息（轮4+轮5），实际 %d 条", len(msgs))
	}
	if len(msgs) > 0 && msgs[0].Round != 4 {
		t.Errorf("第一条消息应该是轮4，实际 %d", msgs[0].Round)
	}
}

func TestManager_GetContextMessagesWithTokenLimit_MinGuarantee(t *testing.T) {
	mgr := setupTestManager(t)
	sessionID := "test-session-min-guarantee"

	if err := mgr.CreateSession(sessionID, "user1"); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	// 添加 1 轮，内容很长（超过预算）
	longText := string(make([]byte, 100)) // 100 字符 = 25 tokens
	mgr.AppendMessage(sessionID, &Message{Round: 1, Instruction: longText, Result: longText})

	// 预算只有 10 tokens，但应该至少返回最近一轮
	msgs, err := mgr.GetContextMessagesWithTokenLimit(sessionID, -1, 10)
	if err != nil {
		t.Fatalf("GetContextMessagesWithTokenLimit() 失败: %v", err)
	}

	if len(msgs) != 1 {
		t.Errorf("即使超预算，应至少返回最近一轮，实际 %d 条", len(msgs))
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

运行命令：
```bash
go test ./internal/memory -run TestManager_GetContextMessagesWithTokenLimit -v
```

预期输出：`undefined: GetContextMessagesWithTokenLimit`

- [ ] **Step 3: 在 Memory 接口中添加新方法签名**

```go
// internal/memory/memory.go:15-16（在 GetContextMessages 后添加）
GetContextMessages(sessionID string, windowSize int) ([]Message, error)
GetContextMessagesWithTokenLimit(sessionID string, windowSize int, maxContextTokens int) ([]Message, error)
```

- [ ] **Step 4: 实现 GetContextMessagesWithTokenLimit 方法**

```go
// internal/memory/manager.go - 在 GetContextMessages 方法后添加
// GetContextMessagesWithTokenLimit 返回用于 LLM 上下文构建的历史消息（两层截断）
// 第一层：按 windowSize 截轮数（<= 0 表示不限制轮数）
// 第二层：按 maxContextTokens 截 token（<= 0 表示不限制 token）
func (m *Manager) GetContextMessagesWithTokenLimit(sessionID string, windowSize int, maxContextTokens int) ([]Message, error) {
	// 1. 获取完整历史
	history, err := m.GetHistory(sessionID)
	if err != nil {
		return nil, err
	}

	messages := history.Messages

	// 2. 第一层截断：按轮数
	if windowSize > 0 && len(messages) > windowSize {
		messages = messages[len(messages)-windowSize:]
	}

	// 3. 第二层截断：按 token
	if maxContextTokens <= 0 {
		// 不限制 token，直接返回
		return messages, nil
	}

	// 按轮分组（从最新往旧遍历）
	estimator := &DefaultTokenEstimator{}
	var result []Message
	accumulated := 0

	// 从最新一轮往前累加
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		// 估算这条消息的 token
		instructionTokens := estimator.Estimate(msg.Instruction)
		resultTokens := estimator.Estimate(msg.Result)
		msgTokens := instructionTokens + resultTokens

		// 判断是否超预算
		if accumulated+msgTokens > maxContextTokens && len(result) > 0 {
			// 超预算且已有消息，停止
			break
		}

		// 加入结果（头插）
		result = append([]Message{msg}, result...)
		accumulated += msgTokens
	}

	// 最小保障：至少保留最近一轮
	if len(result) == 0 && len(messages) > 0 {
		result = []Message{messages[len(messages)-1]}
	}

	return result, nil
}
```

- [ ] **Step 5: 运行测试验证通过**

运行命令：
```bash
go test ./internal/memory -run TestManager_GetContextMessagesWithTokenLimit -v
```

预期输出：所有测试 PASS

- [ ] **Step 6: 提交**

```bash
git add internal/memory/memory.go internal/memory/manager.go internal/memory/memory_test.go
git commit -m "feat(memory): 实现按 token 预算截断上下文

- 新增 GetContextMessagesWithTokenLimit 方法
- 支持两层截断：先轮数后 token
- 从最新一轮往前累加，超预算停止
- 最小保障：至少保留最近一轮

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 4: Handler 层 - 调用新方法传入 Token 预算

**Files:**
- Modify: `internal/api/handler/chat.go`

- [ ] **Step 1: 修改 chat.go 调用新方法**

找到 `internal/api/handler/chat.go` 中调用 `GetContextMessages` 的地方（约 177 行），替换为：

```go
// internal/api/handler/chat.go:173-182（修改前后对比）
// 修改前：
// historyMessages, err = h.memory.GetContextMessages(sessionID, h.config.Memory.HistoryWindow)

// 修改后：
// 获取当前请求使用的模型配置
modelConfig := h.config.LLM.GetModelByName(req.Model)
if modelConfig == nil {
	modelConfig = h.config.LLM.GetDefaultModel()
}

// 使用新方法，传入 token 预算
historyMessages, err = h.memory.GetContextMessagesWithTokenLimit(
	sessionID,
	h.config.Memory.HistoryWindow,
	modelConfig.MaxContextTokens,
)
```

- [ ] **Step 2: 编译验证无语法错误**

运行命令：
```bash
go build -o bin/groot ./cmd
```

预期输出：编译成功，无错误

- [ ] **Step 3: 提交**

```bash
git add internal/api/handler/chat.go
git commit -m "feat(handler): 使用 token 预算截断上下文

- 根据请求模型获取 MaxContextTokens 配置
- 调用 GetContextMessagesWithTokenLimit 传入预算
- 向后兼容：MaxContextTokens=0 时行为不变

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 5: 完整性测试与文档更新

**Files:**
- Test: All modified files
- Modify: `README.md` (如果需要更新用户手册)

- [ ] **Step 1: 运行所有单元测试**

运行命令：
```bash
go test ./internal/config/... ./internal/memory/... -v
```

预期输出：所有测试 PASS

- [ ] **Step 2: 编译并运行程序验证**

运行命令：
```bash
go build -o bin/groot ./cmd
./bin/groot init
```

检查生成的 `~/.groot/config.yaml` 是否包含 `max_context_tokens: 0` 注释

- [ ] **Step 3: 手动测试一个配置场景**

编辑 `~/.groot/config.yaml`，设置：
```yaml
models:
  gpt-4o:
    max_context_tokens: 100  # 设置一个很小的值便于测试
```

启动服务并发送多轮对话，观察上下文是否按预期截断

- [ ] **Step 4: 更新用户手册（如果需要）**

如果 README.md 中有配置说明章节，添加 `max_context_tokens` 的说明：

```markdown
### 模型配置

每个模型可配置以下参数：
- `max_completion_tokens`: 输出 token 数量上限
- `max_context_tokens`: 输入上下文 token 预算（0 表示不限制，默认值）
- ...
```

- [ ] **Step 5: 最终提交**

```bash
git add README.md  # 如果修改了
git commit -m "docs: 更新配置说明，补充 max_context_tokens

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## 实施完成检查清单

完成所有 Task 后，验证以下内容：

- [ ] 所有单元测试通过（`go test ./internal/...`）
- [ ] 编译无错误（`go build -o bin/groot ./cmd`）
- [ ] 配置文件模板包含 `max_context_tokens` 注释
- [ ] DefaultConfig 初始化 `MaxContextTokens: 0`
- [ ] Token 估算器测试覆盖中英文、混合、空字符串
- [ ] Memory 截断逻辑测试覆盖：只限轮数、只限 token、两者都限、最小保障
- [ ] Handler 正确获取模型配置并传入 token 预算
- [ ] 向后兼容：不配置 `max_context_tokens` 时行为和之前一致

---

## 预期产出

**代码文件：**
- `internal/memory/token_estimator.go` (新增，约 40 行)
- `internal/memory/token_estimator_test.go` (新增，约 50 行)
- `internal/config/config.go` (修改，+1 字段)
- `internal/config/defaults.go` (修改，+1 行)
- `internal/config/template.go` (修改，+1 行注释)
- `internal/memory/memory.go` (修改，+1 方法签名)
- `internal/memory/manager.go` (修改，+40 行实现)
- `internal/memory/memory_test.go` (修改，+80 行测试)
- `internal/api/handler/chat.go` (修改，~10 行调用改动)
- `internal/config/config_test.go` (修改，+10 行测试)

**提交数量：** 5 个提交（每个 Task 一个）

**测试覆盖：**
- Token 估算器：4 个测试用例
- Memory 截断逻辑：3 个测试用例
- Config 字段：1 个测试用例

