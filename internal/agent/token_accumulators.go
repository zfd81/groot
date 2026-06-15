package agent

import "sync"

// TokenUsage 三元组。
type TokenUsage struct {
	Prompt     int
	Completion int
	Total      int
}

// ChatAggregate 单次主/子 Agent 执行内的运行时聚合：
// token 用量 + step 序列 + 实际使用的 model 名。
// 所有写入由 Engine 事件循环完成，CallAgentTool / Executor 在结束时 PopAndDelete 取出。
type ChatAggregate struct {
	Tokens TokenUsage
	Steps  []StepRecord
	Model  string
}

// TokenAccumulators 按 chatID 聚合主/子 Agent 的 token / steps / model；线程安全。
type TokenAccumulators struct {
	mu sync.Mutex
	m  map[string]*ChatAggregate
}

// NewTokenAccumulators 创建空的累加器。
func NewTokenAccumulators() *TokenAccumulators {
	return &TokenAccumulators{m: make(map[string]*ChatAggregate)}
}

// Add 累加一次 LLM 响应的 token；total 自动 = prompt + completion 以避免外部传错。
// 调用方需保证 prompt >= 0 && completion >= 0；负值会被当作减量记入，调用方自负其责。
func (a *TokenAccumulators) Add(chatID string, prompt, completion int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur := a.getOrCreate(chatID)
	cur.Tokens.Prompt += prompt
	cur.Tokens.Completion += completion
	cur.Tokens.Total = cur.Tokens.Prompt + cur.Tokens.Completion
}

// AppendStep 在 chatID 的 step 序列后追加一条记录。
func (a *TokenAccumulators) AppendStep(chatID string, step StepRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur := a.getOrCreate(chatID)
	cur.Steps = append(cur.Steps, step)
}

// CompleteStep 把已追加的 step（按 StepID 匹配）状态翻为 completed。
func (a *TokenAccumulators) CompleteStep(chatID, stepID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.m[chatID]
	if !ok {
		return
	}
	for i := range cur.Steps {
		if cur.Steps[i].StepID == stepID {
			cur.Steps[i].Status = StatusCompleted
		}
	}
}

// SetModel 记录本次执行实际使用的 model 名（首次写入生效，后续相同覆盖；不同也允许覆盖以反映最后一次）。
func (a *TokenAccumulators) SetModel(chatID, model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur := a.getOrCreate(chatID)
	cur.Model = model
}

// PopAndDelete 取出并清理（在 CallAgentTool.InvokableRun / Executor 收尾时调用）。
// 不存在的 chatID 返回零值，调用方无需关心是否存在。
func (a *TokenAccumulators) PopAndDelete(chatID string) ChatAggregate {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.m[chatID]
	if !ok {
		return ChatAggregate{}
	}
	delete(a.m, chatID)
	return *cur
}

func (a *TokenAccumulators) getOrCreate(chatID string) *ChatAggregate {
	cur, ok := a.m[chatID]
	if !ok {
		cur = &ChatAggregate{}
		a.m[chatID] = cur
	}
	return cur
}
