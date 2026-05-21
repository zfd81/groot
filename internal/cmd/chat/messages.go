package chat

// SseEvent represents a parsed SSE data line from the chat stream.
// The API sends JSON events without SSE "event:" lines; type
// is determined by which JSON fields are present.
type SseEvent struct {
	Role         string     `json:"role"`
	Content      string     `json:"content,omitempty"`
	Reasoning    string     `json:"reasoning_content,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string     `json:"tool_call_id,omitempty"`
	ToolName     string     `json:"tool_name,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Event        string     `json:"event,omitempty"`
	Code         string     `json:"code,omitempty"`
	Message      string     `json:"message,omitempty"`
	IsError      bool       `json:"error,omitempty"`
	RawText      string     `json:"-"`
}

// ToolCall represents a tool call in OpenAI format
type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents function call details
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Bubble Tea message types (each wraps data to carry context)

// SseEventMsg carries a parsed SSE event from the stream goroutine
type SseEventMsg SseEvent

// SessionIDMsg carries the session ID extracted from X-Session-ID response header
type SessionIDMsg string

// StreamDoneMsg signals the SSE stream has ended normally
type StreamDoneMsg struct{}

// StreamErrorMsg signals an error during SSE streaming
type StreamErrorMsg struct{ Err error }

// CancelChatMsg signals a user-requested cancellation (ESC during streaming)
type CancelChatMsg struct{}

// SkillsListMsg carries the skill list fetched from the API
type SkillsListMsg struct {
	Skills []CompletionItem
}

// ModelsListMsg carries the model list fetched from the /models API
type ModelsListMsg struct {
	Models  []CompletionItem
	Default string
}

// CommandMsg carries a parsed system command from user input
type CommandMsg struct {
	Cmd  string
	Args string
}

// CompletionItem is a single entry in the completion popup.
type CompletionItem struct {
	Name        string
	Description string
}

// modelPopupMsg triggers the model selection popup
type modelPopupMsg struct {
	models []CompletionItem
}
