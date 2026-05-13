package chat

import (
	"testing"
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
