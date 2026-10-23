package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestEngine_AccumulateUsage 验证按事件源 Agent 把 Usage 累加到正确的 chatID：
// 主 Agent 事件 → mainChatID，子 Agent 事件 → childChatID。
func TestEngine_AccumulateUsage(t *testing.T) {
	cases := []struct {
		name           string
		engineAgent    string
		eventAgent     string
		mainChatID     string
		childChatID    string
		meta           *schema.ResponseMeta
		wantPrompt     int
		wantCompletion int
		wantOnMain     bool
	}{
		{
			name:           "main agent event accumulates on mainChatID",
			engineAgent:    MainAgentName,
			eventAgent:     MainAgentName,
			mainChatID:     "main_x",
			meta:           &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 20}},
			wantPrompt:     10,
			wantCompletion: 20,
			wantOnMain:     true,
		},
		{
			name:        "nil meta ignored",
			engineAgent: MainAgentName,
			eventAgent:  "db-agent",
			childChatID: "chat_x",
			meta:        nil,
		},
		{
			name:        "nil usage ignored",
			engineAgent: MainAgentName,
			eventAgent:  "db-agent",
			childChatID: "chat_x",
			meta:        &schema.ResponseMeta{},
		},
		{
			name:        "missing ctx childChatID ignored",
			engineAgent: MainAgentName,
			eventAgent:  "db-agent",
			childChatID: "",
			meta:        &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 2}},
		},
		{
			name:           "child event with full info accumulated to childChatID",
			engineAgent:    MainAgentName,
			eventAgent:     "db-agent",
			childChatID:    "chat_x",
			meta:           &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 20}},
			wantPrompt:     10,
			wantCompletion: 20,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			acc := NewTokenAccumulators()
			e := &Engine{agentName: c.engineAgent, tokenAccumulators: acc}
			ctx := context.Background()
			if c.mainChatID != "" {
				ctx = WithMainChatID(ctx, c.mainChatID)
			}
			if c.childChatID != "" {
				ctx = WithChildChatID(ctx, c.childChatID)
			}
			e.accumulateUsage(ctx, c.eventAgent, c.meta)
			lookupKey := c.childChatID
			if c.wantOnMain {
				lookupKey = c.mainChatID
			}
			if lookupKey == "" {
				lookupKey = "chat_x"
			}
			got := acc.PopAndDelete(lookupKey).Tokens
			if got.Prompt != c.wantPrompt || got.Completion != c.wantCompletion {
				t.Errorf("got %+v, want prompt=%d completion=%d", got, c.wantPrompt, c.wantCompletion)
			}
		})
	}
}

// TestEngine_AccumulateUsage_NilAccumulators 没有 tokenAccumulators 时不应 panic。
func TestEngine_AccumulateUsage_NilAccumulators(t *testing.T) {
	e := &Engine{agentName: MainAgentName, tokenAccumulators: nil}
	ctx := WithChildChatID(context.Background(), "chat_x")
	e.accumulateUsage(ctx, "db-agent", &schema.ResponseMeta{
		Usage: &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 2},
	})
}

// TestEngine_BuildSystemInstruction_AgentMdReplacesGroot 验证 Solo 模式核心契约：
// agentMdContent 非空时用 agent.md 完全替换 GROOT.md，反之回退到 GROOT.md。
// 设计 6.2 节的关键不变量；防止以后误把 agent.md 拼在 GROOT.md 后面。
func TestEngine_BuildSystemInstruction_AgentMdReplacesGroot(t *testing.T) {
	e := &Engine{}
	cases := []struct {
		name             string
		prompt           string
		sessionMdContent string
		agentMdContent   string
		wantContains     []string
		wantNotContains  []string
	}{
		{
			name:           "solo: agent.md 替换 GROOT.md",
			prompt:         "PROMPT_X",
			agentMdContent: "AGENT_MD_BODY",
			wantContains:   []string{"AGENT_MD_BODY", "PROMPT_X"},
		},
		{
			name:             "solo: 含 SESSION.md 时按顺序拼接",
			prompt:           "PROMPT_X",
			sessionMdContent: "SESSION_MD",
			agentMdContent:   "AGENT_MD_BODY",
			wantContains:     []string{"AGENT_MD_BODY", "SESSION_MD", "PROMPT_X"},
		},
		{
			name:           "编排：agentMd 为空不影响（不触发 grootmd 加载，结果至少包含 prompt）",
			prompt:         "PROMPT_Y",
			agentMdContent: "",
			// GROOT.md 内容由 grootmd 包决定，可能为空，所以只断言 prompt 出现
			wantContains: []string{"PROMPT_Y"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := e.buildSystemInstruction(c.prompt, c.sessionMdContent, c.agentMdContent)
			for _, s := range c.wantContains {

				if !strings.Contains(got, s) {
					t.Errorf("expected %q in result, got: %q", s, got)
				}
			}
			for _, s := range c.wantNotContains {
				if strings.Contains(got, s) {
					t.Errorf("did NOT expect %q in result, got: %q", s, got)
				}
			}
		})
	}
}

func TestStripThinkingTags(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no tags",
			input: "plain answer",
			want:  "plain answer",
		},
		{
			name:  "single tag",
			input: "<think>reasoning</think>answer",
			want:  "answer",
		},
		{
			name:  "multiple tags interleaved",
			input: "<think>A</think>reply1<think>B</think>reply2",
			want:  "reply1reply2",
		},
		{
			name:  "multiline think block",
			input: "<think>\nline1\nline2\n</think>final answer",
			want:  "final answer",
		},
		{
			name:  "leading/trailing whitespace trimmed",
			input: "  <think>x</think>  answer  ",
			want:  "answer",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only think block no answer",
			input: "<think>only thinking</think>",
			want:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripThinkingTags(c.input)
			if got != c.want {
				t.Errorf("stripThinkingTags(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// TestClassifyRunError 验证事件循环的错误分类纯函数，优先级与原 if-chain 顺序一致：
// mcp/command_not_allowed → 连接类 → NodeRunError → 其他。
func TestClassifyRunError(t *testing.T) {
	cases := []struct {
		in   string
		want runErrorKind
	}{
		{"mcp tool failed", runErrMCP},
		{"command_not_allowed: rm", runErrMCP},
		{"dial tcp 1.2.3.4: connection refused", runErrFatalConn},
		{"no such host", runErrFatalConn},
		{"request timeout", runErrFatalConn},
		{"NodeRunError: something broke", runErrRecoverable},
		{"unknown garbage", runErrIgnored},
		{"", runErrIgnored},
		{"mcp NodeRunError", runErrMCP},                     // mcp 优先于 NodeRunError
		{"mcp dial tcp connection refused", runErrMCP},      // mcp 优先于连接类
		{"NodeRunError: dial tcp timeout", runErrFatalConn}, // 连接类优先于 NodeRunError
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := classifyRunError(c.in); got != c.want {
				t.Errorf("classifyRunError(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestAgentErrorToolName 锁定可恢复错误 tool_result 所用的工具名常量。
func TestAgentErrorToolName(t *testing.T) {
	if AgentErrorToolName != "agent_error" {
		t.Errorf("AgentErrorToolName = %q, want %q", AgentErrorToolName, "agent_error")
	}
}

func TestEngine_AccumulateUsage_ChildViaRegistry(t *testing.T) {
	acc := NewTokenAccumulators()
	acc.OpenRound("main_x")
	acc.RegisterChild("main_x", "db-agent", "child_reg")
	e := &Engine{agentName: MainAgentName, tokenAccumulators: acc}
	ctx := WithMainChatID(context.Background(), "main_x") // 不注入 childChatID：模拟生产路径
	e.accumulateUsage(ctx, "db-agent", &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 7, CompletionTokens: 3}})
	if got := acc.PopAndDelete("child_reg").Tokens; got != (TokenUsage{7, 3, 10}) {
		t.Fatalf("got %+v", got)
	}
}

func TestEngine_AccumulateUsage_RegistryBeatsCtx(t *testing.T) {
	acc := NewTokenAccumulators()
	acc.OpenRound("main_x")
	acc.RegisterChild("main_x", "db-agent", "child_reg")
	e := &Engine{agentName: MainAgentName, tokenAccumulators: acc}
	ctx := WithChildChatID(WithMainChatID(context.Background(), "main_x"), "child_ctx")
	e.accumulateUsage(ctx, "db-agent", &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 1}})
	if acc.PopAndDelete("child_reg").Tokens != (TokenUsage{1, 1, 2}) {
		t.Fatal("registry must win")
	}
	if acc.PopAndDelete("child_ctx").Tokens != (TokenUsage{}) {
		t.Fatal("ctx child must be untouched")
	}
}

func TestEngine_FinishRound_SumsAndCleans(t *testing.T) {
	acc := NewTokenAccumulators()
	e := &Engine{agentName: MainAgentName, tokenAccumulators: acc}
	ctx := WithMainChatID(context.Background(), "main_x")
	acc.OpenRound("main_x")
	acc.Add("main_x", 10, 5)
	acc.RegisterChild("main_x", "coder", "c1")
	acc.Add("c1", 20, 10)
	main, round := e.finishRound(ctx)
	if main.Tokens != (TokenUsage{10, 5, 15}) {
		t.Errorf("main = %+v", main.Tokens)
	}
	if round != (TokenUsage{30, 15, 45}) {
		t.Errorf("round = %+v", round)
	}
}

func TestEngine_FinishRound_NilAccumulators(t *testing.T) {
	e := &Engine{agentName: MainAgentName}
	main, round := e.finishRound(WithMainChatID(context.Background(), "m"))
	if main.Tokens != (TokenUsage{}) || round != (TokenUsage{}) {
		t.Fatal("expected zero values")
	}
}
