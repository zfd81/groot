package chat

import (
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input string
		want  *CommandMsg
	}{
		{"/exit", &CommandMsg{Cmd: "/exit", Args: ""}},
		{"/model gpt-4o", &CommandMsg{Cmd: "/model", Args: "gpt-4o"}},
		{"/skills list", &CommandMsg{Cmd: "/skills", Args: "list"}},
		{"/mcp list", &CommandMsg{Cmd: "/mcp", Args: "list"}},
		{"hello world", nil},
		{"", nil},
		{"  /exit  ", &CommandMsg{Cmd: "/exit", Args: ""}},
	}

	for _, tt := range tests {
		got := ParseCommand(tt.input)
		if tt.want == nil && got != nil {
			t.Errorf("ParseCommand(%q) = %v, want nil", tt.input, got)
			continue
		}
		if tt.want != nil && got == nil {
			t.Errorf("ParseCommand(%q) = nil, want %v", tt.input, tt.want)
			continue
		}
		if got != nil {
			if got.Cmd != tt.want.Cmd {
				t.Errorf("ParseCommand(%q).Cmd = %q, want %q", tt.input, got.Cmd, tt.want.Cmd)
			}
			if got.Args != tt.want.Args {
				t.Errorf("ParseCommand(%q).Args = %q, want %q", tt.input, got.Args, tt.want.Args)
			}
		}
	}
}

func TestExecuteCommandRouting(t *testing.T) {
	tests := []struct {
		msg  CommandMsg
		want string
	}{
		{CommandMsg{Cmd: "/exit"}, "quit"},
		{CommandMsg{Cmd: "/clear"}, "clear"},
		{CommandMsg{Cmd: "/help"}, "render"},
		{CommandMsg{Cmd: "/model"}, "model_popup"},
		{CommandMsg{Cmd: "/model", Args: "gpt-4o"}, "switch_model"},
		{CommandMsg{Cmd: "/skills", Args: "list"}, "skills_popup"},
		{CommandMsg{Cmd: "/mcp", Args: "list"}, "fetch"},
		{CommandMsg{Cmd: "/export"}, "export"},
		{CommandMsg{Cmd: "/unknown"}, "render"},
	}

	for _, tt := range tests {
		result := ExecuteCommand(tt.msg)
		if result.Action != tt.want {
			t.Errorf("ExecuteCommand(%v).Action = %q, want %q", tt.msg, result.Action, tt.want)
		}
	}
}
