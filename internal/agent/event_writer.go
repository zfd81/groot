package agent

// EventWriter 是对话执行期间的事件写出接口，SSEWriter 与 Aggregator 都实现它。
// 执行器只依赖该接口，从而同一套对话流程既能以 SSE 流式输出，也能聚合为非流式结果。
type EventWriter interface {
	SetModel(model string)
	SetAgentModel(agentName, model string)
	WriteThinking(agentName, content string) error
	WriteMessage(agentName, content string) error
	WriteToolCalls(agentName string, toolCalls []ToolCall) error
	WriteFinish(agentName, reason string) error
	WriteToolResult(agentName, toolCallID, toolName, content string, isError bool) error
	WriteError(agentName, message string) error
	WriteUsage(usage TokenUsage) error
	WriteDone() error
}

// 编译期确认：SSEWriter 的签名必须与接口完全一致（多出的 ClearAgentModel 不影响实现）。
var _ EventWriter = (*SSEWriter)(nil)

// nopEventWriter 供不需要事件输出的执行路径（定时任务、Web 静默调用）使用。
type nopEventWriter struct{}

// NopEventWriter 返回一个丢弃全部事件的 EventWriter。
func NopEventWriter() EventWriter { return nopEventWriter{} }

func (nopEventWriter) SetModel(string)                    {}
func (nopEventWriter) SetAgentModel(string, string)       {}
func (nopEventWriter) WriteThinking(string, string) error { return nil }

func (nopEventWriter) WriteMessage(string, string) error { return nil }

func (nopEventWriter) WriteToolCalls(string, []ToolCall) error { return nil }

func (nopEventWriter) WriteFinish(string, string) error { return nil }

func (nopEventWriter) WriteToolResult(string, string, string, string, bool) error { return nil }

func (nopEventWriter) WriteError(string, string) error { return nil }

func (nopEventWriter) WriteUsage(TokenUsage) error { return nil }

func (nopEventWriter) WriteDone() error { return nil }
