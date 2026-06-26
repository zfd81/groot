# 上下文窗口 Token 限制功能设计文档

## 一、功能概述

为上下文窗口控制增加按 token 总量截断的能力。在现有"按轮数截断"（`history_window`）的基础上，新增"按 token 预算截断"（`max_context_tokens`），两层截断串联生效：

1. **第一层（轮数截断）**：按 `history_window` 配置，从完整历史中取最近 N 轮
2. **第二层（token 截断）**：
   - 把第一层结果**按轮分组**（一轮 = 一对 user + assistant message）
   - **从最新一轮往前**遍历每一轮
   - 对每一轮，逐条估算该轮内所有 message 的 token 并求和，得到"这一轮的 token"
   - 每次累加前先判断：`当前累计 + 这一轮的总 token <= 预算？`
     - **是** → 加入这一轮，继续往前
     - **否** → 停止，**不加入这一轮**，返回已加入的所有完整轮
   - 返回的是**完整轮的集合**（不会切半轮）

**示例**（假设预算 8000 token）：
```
轮5: 500 token  ← 累计 500   ✅ 加入
轮4: 3000 token ← 累计 3500  ✅ 加入
轮3: 2500 token ← 累计 6000  ✅ 加入
轮2: 2500 token ← 累计 8500  ❌ 超预算，停止
轮1: 2000 token ← 不再检查

返回: 轮3 + 轮4 + 轮5（共 6000 token，完整）
```

**最小保障**：即使最近一轮超出预算，也至少保留最近一轮（避免返回空历史导致对话无法继续）。

这样可以让不同模型根据自己的上下文窗口能力，独立控制输入预算，避免超出模型限制或浪费昂贵的上下文容量。

---

## 二、配置设计

### 2.1 配置项定义

在 `ModelConfig` 结构体中新增 `MaxContextTokens` 字段，和现有的 `MaxCompletionTokens` 平级：

```go
// ModelConfig holds individual model settings
type ModelConfig struct {
    BaseURL             string   `yaml:"base_url"`
    APIKey              string   `yaml:"api_key"`
    Model               string   `yaml:"model"`
    MaxCompletionTokens int      `yaml:"max_completion_tokens"` // 现有：输出预算
    MaxContextTokens    int      `yaml:"max_context_tokens"`    // 新增：输入预算
    Temperature         float64  `yaml:"temperature"`
    TopP                float64  `yaml:"top_p"`
    // ...其他字段
}
```

### 2.2 配置示例

```yaml
llm:
  default_model: "gpt-4"
  models:
    gpt-4:
      base_url: "https://api.openai.com/v1"
      api_key: "${OPENAI_API_KEY}"
      model: "gpt-4-turbo"
      max_completion_tokens: 4096   # 输出预算
      max_context_tokens: 8000      # 输入预算（新增）
      
    claude-3:
      base_url: "https://api.anthropic.com/v1"
      api_key: "${ANTHROPIC_API_KEY}"
      model: "claude-3-sonnet"
      max_completion_tokens: 4096
      max_context_tokens: 12000     # Claude 可以设更大
```

### 2.3 默认值与语义

- **默认值**：`0` 或 `-1`，表示"不限制 token，只按 `history_window` 截轮数"
- **向后兼容**：现有配置不填 `max_context_tokens` 时，行为和之前一致（只按轮数截断）
- **两层独立**：
  - `history_window: 20, max_context_tokens: -1` → 取最近 20 轮，不限 token
  - `history_window: -1, max_context_tokens: 8000` → 不限轮数，按 token 从新往旧累加到 8000
  - `history_window: 20, max_context_tokens: 8000` → 先取最近 20 轮，再从中按 token 筛选

---

## 三、Token 估算策略

### 3.1 估算器设计

新增独立的 token 估算模块 `internal/memory/token_estimator.go`，采用混合策略：

- **中文/日文/韩文字符**：按 `字符数 × 1.5` 估算
- **英文/数字/符号**：按 `len(text) / 4` 估算

### 3.2 实现接口

```go
// TokenEstimator 估算文本的 token 数量
type TokenEstimator interface {
    Estimate(text string) int
}

// DefaultTokenEstimator 默认实现：混合策略
type DefaultTokenEstimator struct{}

func (e *DefaultTokenEstimator) Estimate(text string) int {
    cjkCount := 0      // 中日韩字符数
    otherCount := 0    // 其他字符数
    
    for _, r := range text {
        if isCJK(r) {
            cjkCount++
        } else {
            otherCount++
        }
    }
    
    // 中日韩: 1.5 token/字符
    // 其他: 4 字符/token
    return int(float64(cjkCount)*1.5) + otherCount/4
}

func isCJK(r rune) bool {
    // Unicode 范围：中文、日文假名、韩文
    return (r >= 0x4E00 && r <= 0x9FFF) ||   // CJK Unified Ideographs
           (r >= 0x3040 && r <= 0x309F) ||   // Hiragana
           (r >= 0x30A0 && r <= 0x30FF) ||   // Katakana
           (r >= 0xAC00 && r <= 0xD7AF)      // Hangul
}
```

### 3.3 扩展性

- 接口设计预留扩展空间，后续可替换为 tiktoken 或模型专用估算器
- 估算器实例化在 `Manager` 初始化时注入，便于测试和替换

---

## 四、核心逻辑设计

### 4.1 新增方法签名

在 `internal/memory/manager.go` 中新增方法：

```go
// GetContextMessagesWithTokenLimit 返回用于 LLM 上下文构建的历史消息
// 先按 windowSize 截轮数，再按 maxContextTokens 截 token
// windowSize: 保留最近 N 轮，<= 0 表示不限制轮数
// maxContextTokens: token 预算上限，<= 0 表示不限制 token
func (m *Manager) GetContextMessagesWithTokenLimit(
    sessionID string, 
    windowSize int, 
    maxContextTokens int,
) ([]Message, error)
```

### 4.2 执行流程

```
1. 调用现有的 GetHistory(sessionID) 获取完整历史

2. 第一层截断：按 windowSize 截轮数
   - 如果 windowSize <= 0，跳过此步
   - 否则取最近 windowSize 轮

3. 第二层截断：按 maxContextTokens 截 token
   - 如果 maxContextTokens <= 0，跳过此步，直接返回第一层结果
   - 否则执行以下逻辑：
   
   a. 按轮分组（通过 round 字段分组）
   b. 从最新一轮往前遍历：
      - 估算这一轮内所有 message 的 token 总和
      - 判断：当前累计 + 这一轮 <= maxContextTokens？
        - 是 → 加入结果集，继续往前
        - 否 → 停止遍历
   c. 最小保障：如果结果集为空，至少保留最近一轮
   d. 返回结果集（按时间正序排列）

4. 返回截断后的 messages
```

### 4.3 边界处理

- **两层都不限制**：`windowSize <= 0` 且 `maxContextTokens <= 0`，返回全量历史
- **只限轮数**：`windowSize > 0` 且 `maxContextTokens <= 0`，行为同现有 `GetContextMessages`
- **只限 token**：`windowSize <= 0` 且 `maxContextTokens > 0`，全量历史按 token 截断
- **最近一轮超预算**：即使最近一轮的 token 超过 `maxContextTokens`，也至少返回最近一轮（避免返回空列表）

### 4.4 默认值初始化

在 `internal/config/defaults.go` 的 `DefaultConfig()` 中：

- `MemoryConfig.HistoryWindow` 保持现有值 `20`
- `ModelConfig.MaxContextTokens` 初始化为 `0`（不限制，向后兼容）

用户可通过修改 `config.yaml` 中的 `max_context_tokens` 字段启用 token 截断。

---

## 五、调用层改动

### 5.1 Handler 层调用逻辑

在处理聊天请求构建上下文时，需要做以下调整：

1. **获取模型配置**：根据请求中指定的模型名称，获取对应的模型配置；如果未指定或模型不存在，则使用默认模型配置
2. **调用新方法**：使用新的上下文获取方法，传入三个参数：
   - 会话 ID
   - 全局轮数限制（来自 `MemoryConfig.HistoryWindow`）
   - 模型的 token 预算（来自 `ModelConfig.MaxContextTokens`）

### 5.2 向后兼容

- 保留现有的按轮数截断方法，供不需要 token 控制的场景使用
- 新方法是增强版本，兼容现有参数语义
- 现有调用点可以逐步迁移，也可以并存

---

## 六、测试策略

### 6.1 单元测试（Go）

**Token 估算器测试** (`internal/memory/token_estimator_test.go`)
- 纯英文文本估算准确性
- 纯中文文本估算准确性
- 中英混合文本估算准确性
- 空字符串边界情况
- 特殊字符（emoji、标点）处理

**上下文截断逻辑测试** (`internal/memory/manager_test.go`)
- 只按轮数截断（`max_context_tokens <= 0`）
- 只按 token 截断（`history_window <= 0`）
- 两层都生效（先轮数后 token）
- 最小保障：最近一轮超预算仍保留
- 边界情况：空历史、单轮历史、全轮都超预算

**配置加载测试** (`internal/config/config_test.go`)
- `MaxContextTokens` 默认值为 0
- 配置文件正确解析 `max_context_tokens` 字段
- 未配置时向后兼容（不限制 token）

### 6.2 系统测试（Python）

由用户自行执行 `tests/python/` 下的集成测试，验证：
- 不同模型使用各自的 `max_context_tokens` 配置
- 超长对话场景下截断结果符合预期
- 配置修改后立即生效
