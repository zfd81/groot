package agent

import "sync"

// TokenUsage 三元组。
type TokenUsage struct {
	Prompt     int
	Completion int
	Total      int
}

// TokenAccumulators 按 chatID 聚合子 Agent token；线程安全。
type TokenAccumulators struct {
	mu sync.Mutex
	m  map[string]*TokenUsage
}

// NewTokenAccumulators 创建空的 token 累加器。
func NewTokenAccumulators() *TokenAccumulators {
	return &TokenAccumulators{m: make(map[string]*TokenUsage)}
}

// Add 累加一次 LLM 响应的 token；total 自动 = prompt + completion 以避免外部传错。
// 调用方需保证 prompt >= 0 && completion >= 0；负值会被当作减量记入，调用方自负其责。
func (a *TokenAccumulators) Add(chatID string, prompt, completion int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.m[chatID]
	if !ok {
		cur = &TokenUsage{}
		a.m[chatID] = cur
	}
	cur.Prompt += prompt
	cur.Completion += completion
	cur.Total = cur.Prompt + cur.Completion
}

// PopAndDelete 取出并清理（在 CallAgentTool.InvokableRun 收尾时调用）。
// 不存在的 chatID 返回零值 TokenUsage{}，调用方无需关心是否存在。
func (a *TokenAccumulators) PopAndDelete(chatID string) TokenUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.m[chatID]
	if !ok {
		return TokenUsage{}
	}
	delete(a.m, chatID)
	return *cur
}
