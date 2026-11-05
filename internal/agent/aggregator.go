package agent

import (
	"strings"
	"sync"
)

// Aggregator 把事件流聚合为一次性结果，供非流式（stream=false）请求组装 chat.completion。
// 执行器可能在 goroutine 里回调事件方法，因此所有可变状态都由 mu 保护。
type Aggregator struct {
	mu sync.Mutex

	content          strings.Builder
	reasoningContent strings.Builder
	finishReason     string
	usage            TokenUsage
	errMsg           string
}

// 编译期确认 Aggregator 实现了 EventWriter。
var _ EventWriter = (*Aggregator)(nil)

// NewAggregator 创建空聚合器。
func NewAggregator() *Aggregator { return &Aggregator{} }

// Result 是非流式聚合结果，由 handler 组装成 chat.completion。
type Result struct {
	Content          string
	ReasoningContent string
	FinishReason     string
	Usage            TokenUsage
	Error            string
}

// Result 在锁内拷贝聚合状态后返回值类型快照，调用方可安全读取。
// 返回值而非指针：结果一经取定即不再随后续事件变化。
func (a *Aggregator) Result() Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Result{
		Content:          a.content.String(),
		ReasoningContent: a.reasoningContent.String(),
		FinishReason:     a.finishReason,
		Usage:            a.usage,
		Error:            a.errMsg,
	}
}

// SetModel 与 SetAgentModel 不参与非流式聚合：chat.completion 只有一个 model 字段，
// 由 handler 按请求所用的主模型填写。
func (a *Aggregator) SetModel(string)              {}
func (a *Aggregator) SetAgentModel(string, string) {}

// WriteThinking 追加 reasoning_content。agentName 不参与聚合：
// 主 Agent 与子 Agent 的思考按到达顺序拼接。
func (a *Aggregator) WriteThinking(_, content string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reasoningContent.WriteString(content)
	return nil
}

// WriteMessage 追加正文 content；agentName 处理同上。
func (a *Aggregator) WriteMessage(_, content string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.content.WriteString(content)
	return nil
}

// WriteToolCalls 不参与非流式聚合：工具调用过程对最终 chat.completion 不可见。
func (a *Aggregator) WriteToolCalls(string, []ToolCall) error { return nil }

// WriteFinish 仅在 reason 非空时覆盖 FinishReason：空 reason 不覆盖已有值，
// 保证最后一个真正给出结束原因的事件生效。
func (a *Aggregator) WriteFinish(_, reason string) error {
	if reason == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finishReason = reason
	return nil
}

// WriteToolResult 不参与非流式聚合。
func (a *Aggregator) WriteToolResult(string, string, string, string, bool) error { return nil }

// WriteError 记录错误信息（后到者覆盖）。
func (a *Aggregator) WriteError(_, message string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errMsg = message
	return nil
}

// WriteUsage 覆盖式记录 token 用量：usage 是整次请求的汇总值，不做累加。
func (a *Aggregator) WriteUsage(usage TokenUsage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.usage = usage
	return nil
}

// WriteDone 无聚合语义。
func (a *Aggregator) WriteDone() error { return nil }
