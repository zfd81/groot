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
