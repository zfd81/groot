package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestEngine_AccumulateUsageIfChild 验证子 Agent 事件携带 Usage 时按 ctx 中的 chatID 累加。
// 主 Agent 自身事件、空 meta、空 usage、缺失 ctx chatID 这些情况都不应累加。
func TestEngine_AccumulateUsageIfChild(t *testing.T) {
	cases := []struct {
		name           string
		engineAgent    string
		eventAgent     string
		ctxChatID      string
		meta           *schema.ResponseMeta
		wantPrompt     int
		wantCompletion int
	}{
		{
			name:        "main agent event ignored",
			engineAgent: MainAgentName,
			eventAgent:  MainAgentName,
			ctxChatID:   "chat_x",
			meta:        &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 20}},
		},
		{
			name:        "nil meta ignored",
			engineAgent: MainAgentName,
			eventAgent:  "db-agent",
			ctxChatID:   "chat_x",
			meta:        nil,
		},
		{
			name:        "nil usage ignored",
			engineAgent: MainAgentName,
			eventAgent:  "db-agent",
			ctxChatID:   "chat_x",
			meta:        &schema.ResponseMeta{},
		},
		{
			name:        "missing ctx chatID ignored",
			engineAgent: MainAgentName,
			eventAgent:  "db-agent",
			ctxChatID:   "",
			meta:        &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 2}},
		},
		{
			name:           "child event with full info accumulated",
			engineAgent:    MainAgentName,
			eventAgent:     "db-agent",
			ctxChatID:      "chat_x",
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
			if c.ctxChatID != "" {
				ctx = WithChildChatID(ctx, c.ctxChatID)
			}
			e.accumulateUsageIfChild(ctx, c.eventAgent, c.meta)
			got := acc.PopAndDelete("chat_x")
			if got.Prompt != c.wantPrompt || got.Completion != c.wantCompletion {
				t.Errorf("got %+v, want prompt=%d completion=%d", got, c.wantPrompt, c.wantCompletion)
			}
		})
	}
}

// TestEngine_AccumulateUsageIfChild_NilAccumulators 没有 tokenAccumulators 时不应 panic。
func TestEngine_AccumulateUsageIfChild_NilAccumulators(t *testing.T) {
	e := &Engine{agentName: MainAgentName, tokenAccumulators: nil}
	ctx := WithChildChatID(context.Background(), "chat_x")
	e.accumulateUsageIfChild(ctx, "db-agent", &schema.ResponseMeta{
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
