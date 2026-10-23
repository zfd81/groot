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
// 所有写入由 Engine 事件循环完成；子 Agent 结束时由 CallAgentTool 以 PopChild 取出，
// 主 Agent 一轮结束时由 Engine 以 PopRound 取出。
type ChatAggregate struct {
	Tokens TokenUsage
	Steps  []StepRecord
	Model  string
}

// roundState 一轮主 Agent 执行的轮次状态：
// children 记录本轮登记过的子 Agent（agentName → 当前 childChatID），
// retired 累加已归还（PopChild）子 Agent 的 token，
// retiredIDs 记录已归还的 childChatID，供 PopRound 兜底扫描迟到写入。
type roundState struct {
	children   map[string]string
	retired    TokenUsage
	retiredIDs []string
}

// TokenAccumulators 按 chatID 聚合主/子 Agent 的 token / steps / model；
// 同时以主 chatID 为键维护轮次状态，用于汇总主 Agent 及其全部子 Agent 的 token。线程安全。
type TokenAccumulators struct {
	mu     sync.Mutex
	m      map[string]*ChatAggregate
	rounds map[string]*roundState
}

// NewTokenAccumulators 创建空的累加器。
func NewTokenAccumulators() *TokenAccumulators {
	return &TokenAccumulators{
		m:      make(map[string]*ChatAggregate),
		rounds: make(map[string]*roundState),
	}
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

// PopAndDelete 按单个 chatID 取出并清理聚合，不涉及轮次状态。
// 生产路径由 PopChild / PopRound 收尾，本方法保留用于直接的单 chatID 清理（测试使用）。
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

// OpenRound 开启 mainChatID 的一轮执行；幂等，已开启的轮次不会被重置。
func (a *TokenAccumulators) OpenRound(mainChatID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.rounds[mainChatID]; ok {
		return
	}
	a.rounds[mainChatID] = &roundState{children: make(map[string]string)}
}

// RegisterChild 在轮次内登记一个子 Agent 调用（agentName → childChatID）。
// 同一 agentName 再次登记会覆盖映射（被覆盖的旧 id 若已 PopChild 则在 retiredIDs 中，
// PopRound 仍会扫描到它，不会泄漏）。轮次不存在（未 OpenRound 或已 PopRound）时直接丢弃。
func (a *TokenAccumulators) RegisterChild(mainChatID, agentName, childChatID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.rounds[mainChatID]
	if !ok {
		return
	}
	r.children[agentName] = childChatID
}

// ChildFor 返回轮次内 agentName 当前登记的 childChatID；未登记或轮次不存在返回空串。
func (a *TokenAccumulators) ChildFor(mainChatID, agentName string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.rounds[mainChatID]
	if !ok {
		return ""
	}
	return r.children[agentName]
}

// PopChild 子 Agent 结束时取出并删除其聚合。无论轮次是否存在都会删除 childChatID 的聚合；
// 轮次存在时把其 token 计入 retired，并把 childChatID 记入 retiredIDs。
// 登记（children[agentName]）保持不变：子 Agent 的最后一个 usage chunk 可能在 PopChild 之后
// 才被引擎事件循环处理，此时 ChildFor 仍需解析到该 childChatID，迟到的 Add 会重建其聚合，
// 由 PopRound 兜底扫描计入全轮合计。不存在的 childChatID 返回零值，不会重建 entry。
func (a *TokenAccumulators) PopChild(mainChatID, agentName, childChatID string) ChatAggregate {
	a.mu.Lock()
	defer a.mu.Unlock()
	var agg ChatAggregate
	if cur, ok := a.m[childChatID]; ok {
		agg = *cur
		delete(a.m, childChatID)
	}
	r, ok := a.rounds[mainChatID]
	if !ok {
		return agg
	}
	r.retired = addTokens(r.retired, agg.Tokens)
	r.retiredIDs = append(r.retiredIDs, childChatID)
	return agg
}

// PopRound 主 Agent 结束时取出主聚合，并汇总本轮全部子 Agent 的 token：
// 主聚合自身 + 已归还的 retired + 扫描 children 与 retiredIDs 的并集中仍存在的聚合
// （仍在飞的子 Agent，以及 PopChild 之后迟到 Add 重建的聚合），取出并删除。
// 同一 id 若同时出现在两处，只计一次（首次扫描即删除）。之后删除轮次。轮次不存在时 roundTokens 为零值。
func (a *TokenAccumulators) PopRound(mainChatID string) (main ChatAggregate, roundTokens TokenUsage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cur, ok := a.m[mainChatID]; ok {
		main = *cur
		delete(a.m, mainChatID)
	}
	r, ok := a.rounds[mainChatID]
	if !ok {
		return main, TokenUsage{}
	}
	roundTokens = addTokens(main.Tokens, r.retired)
	sweep := func(childChatID string) {
		if cur, ok := a.m[childChatID]; ok {
			roundTokens = addTokens(roundTokens, cur.Tokens)
			delete(a.m, childChatID)
		}
	}
	for _, childChatID := range r.children {
		sweep(childChatID)
	}
	for _, childChatID := range r.retiredIDs {
		sweep(childChatID)
	}
	delete(a.rounds, mainChatID)
	return main, roundTokens
}

// addTokens 两个 TokenUsage 相加，Total 恒为 Prompt + Completion。
func addTokens(x, y TokenUsage) TokenUsage {
	p, c := x.Prompt+y.Prompt, x.Completion+y.Completion
	return TokenUsage{Prompt: p, Completion: c, Total: p + c}
}

func (a *TokenAccumulators) getOrCreate(chatID string) *ChatAggregate {
	cur, ok := a.m[chatID]
	if !ok {
		cur = &ChatAggregate{}
		a.m[chatID] = cur
	}
	return cur
}
