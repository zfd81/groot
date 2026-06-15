// Package agent CallAgentTool: 主 Agent 调度子 Agent 的请求级工具入口。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
)

// CallAgentArgument 工具入参。
type CallAgentArgument struct {
	AgentName string `json:"agent_name"`
	Task      string `json:"task"`
}

// parentModelKey 在 ctx 里携带「父任务运行时 model 名」，由 Engine 注入，
// CallAgentTool.InvokableRun 取出后透传给 SubAgentEntry.BuildAgentTool。
// 在编排模式下保证子 Agent 跟随父 Agent 当前选定的 model。
type parentModelKey struct{}

// WithParentModel 把父任务的 modelName 放进 ctx，供 CallAgentTool 读取。
// 由 Engine.Run 在创建 ChatModelAgent 前调用一次。
func WithParentModel(ctx context.Context, modelName string) context.Context {
	return context.WithValue(ctx, parentModelKey{}, modelName)
}

// ParentModelFromContext 取出父任务 modelName；不存在返回空字符串
// （由 BuildAgentTool 进一步退到 LLMCfg.DefaultModel）。
func ParentModelFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(parentModelKey{}).(string); ok {
		return v
	}
	return ""
}

// CallAgentTool 主 Agent 调度子 Agent 的入口。请求级实例。
type CallAgentTool struct {
	registry          *SubAgentRegistry
	parentChatID      string
	sessionID         string
	maxTaskLen        int
	maxResultLen      int
	execTimeout       time.Duration
	memory            *memory.Manager
	runtimeState      *RuntimeState
	tokenAccumulators *TokenAccumulators
	log               *logger.Logger
	parentRound       int
}

// CallAgentToolConfig 是 NewCallAgentTool 的命名参数集合。
type CallAgentToolConfig struct {
	Registry          *SubAgentRegistry
	ParentChatID      string
	SessionID         string
	MaxTaskLen        int
	MaxResultLen      int
	ExecTimeout       time.Duration
	Memory            *memory.Manager
	RuntimeState      *RuntimeState
	TokenAccumulators *TokenAccumulators
	Log               *logger.Logger
	ParentRound       int
}

// NewCallAgentTool 构造一个请求级 CallAgentTool。
func NewCallAgentTool(cfg CallAgentToolConfig) *CallAgentTool {
	return &CallAgentTool{
		registry:          cfg.Registry,
		parentChatID:      cfg.ParentChatID,
		sessionID:         cfg.SessionID,
		maxTaskLen:        cfg.MaxTaskLen,
		maxResultLen:      cfg.MaxResultLen,
		execTimeout:       cfg.ExecTimeout,
		memory:            cfg.Memory,
		runtimeState:      cfg.RuntimeState,
		tokenAccumulators: cfg.TokenAccumulators,
		log:               cfg.Log,
		parentRound:       cfg.ParentRound,
	}
}

// Info 满足 tool.InvokableTool。工具描述启动期由 BuildDescription 拼接。
func (t *CallAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "call_agent",
		Desc: t.registry.BuildDescription(),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"agent_name": {Desc: "子 Agent 名称", Required: true, Type: schema.String},
			"task":       {Desc: "任务描述", Required: true, Type: schema.String},
		}),
	}, nil
}

// InvokableRun 委托链路：参数校验 → semaphore → 独立超时 ctx → 子 chatID 注入 →
// 调用子 Agent tool → 结果截断 → ChatRecord 写入。错误对外按 tool 标准方式返回。
func (t *CallAgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var input CallAgentArgument
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("failed to unmarshal call_agent input: %w", err)
	}

	entry, ok := t.registry.Get(input.AgentName)
	if !ok {
		return "", fmt.Errorf("未知的子 Agent: %s，请检查 call_agent 工具描述中的可用列表", input.AgentName)
	}
	if t.maxTaskLen > 0 && len(input.Task) > t.maxTaskLen {
		return "", fmt.Errorf("task 长度超过 %d 字符上限", t.maxTaskLen)
	}

	if err := t.registry.Acquire(ctx); err != nil {
		return "", fmt.Errorf("acquire subagent semaphore: %w", err)
	}
	defer t.registry.Release()

	execCtx, cancel := context.WithTimeout(ctx, t.execTimeout)
	defer cancel()

	childChatID := memory.GenerateChildChatID(t.parentChatID, input.AgentName)
	execCtx = context.WithValue(execCtx, childChatIDKey{}, childChatID)

	// 把当前子 Agent 标记为运行中；defer 在调用结束（含错误/超时）后移除。
	if t.runtimeState != nil {
		t.runtimeState.AddSubAgent(t.sessionID, input.AgentName)
		defer t.runtimeState.RemoveSubAgent(t.sessionID, input.AgentName)
	}

	startTime := time.Now()
	params, _ := json.Marshal(map[string]string{"request": input.Task})

	// 现场按运行时父 model 构造子 Agent Tool —— 让子 Agent 跟随主 Agent 当前 model。
	// 详细优先级见 SubAgentEntry.BuildAgentTool 注释。
	parentModel := ParentModelFromContext(ctx)
	subTool, resolvedModel, buildErr := entry.BuildAgentTool(execCtx, parentModel)
	if buildErr != nil {
		return "", fmt.Errorf("build subagent tool: %w", buildErr)
	}

	// 把本次实际选用的 model 名提前写入累加器，结束时随 token / steps 一起 PopAndDelete。
	if t.tokenAccumulators != nil && resolvedModel != "" {
		t.tokenAccumulators.SetModel(childChatID, resolvedModel)
	}

	result, runErr := subTool.InvokableRun(execCtx, string(params), opts...)
	endTime := time.Now()

	if t.maxResultLen > 0 && len(result) > t.maxResultLen {
		result = truncateWithWarning(result, t.maxResultLen)
	}

	// 累加器无条件 pop：与持久化解耦——即便 t.memory == nil，
	// 也要释放本次调用占用的累加项，避免长期运行时 map 缓慢膨胀。
	var aggregate ChatAggregate
	if t.tokenAccumulators != nil {
		aggregate = t.tokenAccumulators.PopAndDelete(childChatID)
	}

	// 写 ChatRecord（错误吞掉，不影响主 Agent 返回）
	if t.memory != nil {
		t.saveChildChatRecord(childChatID, input, result, runErr, startTime, endTime, aggregate)
	}
	return result, runErr
}

// saveChildChatRecord 把子 Agent 一次执行落盘为 ChatRecord。错误仅记录日志。
func (t *CallAgentTool) saveChildChatRecord(childChatID string, input CallAgentArgument, result string, runErr error, startTime, endTime time.Time, aggregate ChatAggregate) {
	status := "completed"
	var memErr *memory.Error
	if runErr != nil {
		status = "failed"
		memErr = &memory.Error{Code: "execution_error", Message: runErr.Error()}
	}
	durationMs := endTime.Sub(startTime).Milliseconds()
	record := &memory.ChatRecord{
		ChatID:           childChatID,
		SessionID:        t.sessionID,
		Round:            t.parentRound,
		Timestamp:        endTime,
		StartedAt:        startTime,
		EndedAt:          endTime,
		Instruction:      input.Task,
		Result:           result,
		Status:           status,
		Duration:         int(endTime.Sub(startTime).Seconds()),
		DurationMs:       durationMs,
		AgentName:        input.AgentName,
		Model:            aggregate.Model,
		Steps:            convertSteps(aggregate.Steps),
		PromptTokens:     aggregate.Tokens.Prompt,
		CompletionTokens: aggregate.Tokens.Completion,
		TotalTokens:      aggregate.Tokens.Total,
		Error:            memErr,
	}
	if err := t.memory.SaveChatRecord(t.sessionID, record); err != nil && t.log != nil {
		t.log.Error("save subagent chat record failed: " + err.Error())
	}
}

// truncateWithWarning 超过 maxLen 时截断，并在结果前添加警告横幅。
func truncateWithWarning(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return fmt.Sprintf(
		"⚠️ 结果已被截断（原始长度: %d 字符，仅显示前 %d 字符）。如需完整数据，请缩小 task 范围或指定输出字段。\n──────────────────\n%s",
		len(s), maxLen, s[:maxLen],
	)
}
