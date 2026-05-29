package chat

import (
	"testing"
)

// TestExtractSubAgentName 覆盖 call_agent 工具入参的子 Agent 名提取。
// CallAgentTool 入参格式约定见 internal/agent/call_agent.go:18。
func TestExtractSubAgentName(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "正常入参",
			raw:  `{"agent_name":"echo-agent","task":"复述苹果"}`,
			want: "echo-agent",
		},
		{
			name: "agent_name 排在后",
			raw:  `{"task":"x","agent_name":"db-agent"}`,
			want: "db-agent",
		},
		{
			name: "缺 agent_name 字段",
			raw:  `{"task":"x"}`,
			want: `{"task":"x"}`,
		},
		{
			name: "JSON 不完整（流式 partial）短串原样返回",
			raw:  `{"agent_name":"ec`,
			want: `{"agent_name":"ec`,
		},
		{
			name: "JSON 不完整（流式 partial）长串截断",
			raw:  `{"agent_name":"this-is-a-very-very-very-long-fake-agent-name-that-overflows`,
			want: `{"agent_name":"this-is-a-very-very-very-long-fa...`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractSubAgentName(c.raw)
			if got != c.want {
				t.Errorf("extractSubAgentName(%q) = %q; want %q", c.raw, got, c.want)
			}
		})
	}
}

// TestExtractSkillName 顺手也给 extractSkillName 补一份基础覆盖（之前一直没测）。
func TestExtractSkillName(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"skill":"hello"}`, "hello"},
		{`{"name":"x"}`, "x"},
		{`{"skill_name":"y"}`, "y"},
		{`{"unknown":1}`, `{"unknown":1}`},
	}
	for _, c := range cases {
		got := extractSkillName(c.raw)
		if got != c.want {
			t.Errorf("extractSkillName(%q) = %q; want %q", c.raw, got, c.want)
		}
	}
}
