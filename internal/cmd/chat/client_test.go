package chat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestClassifyEvent(t *testing.T) {
	tests := []struct {
		name  string
		event SseEvent
		want  string
	}{
		{
			name:  "thinking",
			event: SseEvent{Reasoning: "Let me think..."},
			want:  "thinking",
		},
		{
			name:  "tool_calls",
			event: SseEvent{ToolCalls: []ToolCall{{ID: "1", Function: FunctionCall{Name: "read"}}}},
			want:  "tool_calls",
		},
		{
			name:  "tool_result",
			event: SseEvent{Role: "tool", ToolName: "read", Content: "file content"},
			want:  "tool_result",
		},
		{
			name:  "message",
			event: SseEvent{Content: "Hello!"},
			want:  "message",
		},
		{
			name:  "finish_reason",
			event: SseEvent{FinishReason: "stop"},
			want:  "finish_reason",
		},
		{
			name:  "error",
			event: SseEvent{Event: "error", Message: "something went wrong"},
			want:  "error",
		},
		{
			name:  "thinking over message",
			event: SseEvent{Reasoning: "thinking...", Content: "text"},
			want:  "thinking",
		},
	}

	for _, tt := range tests {
		got := classifyEvent(tt.event)
		if got != tt.want {
			t.Errorf("classifyEvent(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNewClientDefaults(t *testing.T) {
	client := NewClient("http://localhost:8080/", "gpt-4o")
	if client.baseURL != "http://localhost:8080" {
		t.Errorf("baseURL = %q, want 'http://localhost:8080'", client.baseURL)
	}
	if client.modelName != "gpt-4o" {
		t.Errorf("modelName = %q, want 'gpt-4o'", client.modelName)
	}
}

// TestSendChatStream_AgentHeader 验证 X-Agent-Name 请求头按 Agent 名条件发送：
// 主 Agent（空串或 MainAgentName="groot"）不发，子 Agent 才发。
func TestSendChatStream_AgentHeader(t *testing.T) {
	cases := []struct {
		name       string
		agent      string
		wantHeader string // 空串 = 不应该有 header
	}{
		{"empty agent omits header", "", ""},
		{"main agent name omits header", "groot", ""},
		{"sub agent sends header", "db-agent", "db-agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotHeader string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHeader = r.Header.Get("X-Agent-Name")
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Session-ID", "sess-test")
				w.WriteHeader(200)
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()

			c := NewClient(srv.URL, "gpt-4")
			c.SetAgent(tc.agent)

			events := make(chan tea.Msg, 8)
			cancelCh := make(chan struct{})
			c.SendChatStream("hi", nil, events, cancelCh)
			// drain channel until SendChatStream goroutine closes it
			for range events {
			}
			if gotHeader != tc.wantHeader {
				t.Errorf("X-Agent-Name = %q, want %q", gotHeader, tc.wantHeader)
			}
		})
	}
}
