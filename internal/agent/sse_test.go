package agent

import (
	"bytes"
	"strings"
	"testing"
)

// bufFlusher 是测试用的 flushWriter：把 SSE 字节流缓冲到内存。
// 注意 Flush 用指针接收器，避免与 *bytes.Buffer 的 Write（指针接收器）混搭引发误用。
type bufFlusher struct{ bytes.Buffer }

func (b *bufFlusher) Flush() error { return nil }

func TestSSEWriter_WriteThinkingWithAgentName(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1)
	if err := w.WriteThinking("db-agent", "thinking..."); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, `"agent_name":"db-agent"`) {
		t.Errorf("missing agent_name: %s", s)
	}
	if !strings.Contains(s, `"reasoning_content":"thinking..."`) {
		t.Errorf("missing reasoning: %s", s)
	}
}

func TestSSEWriter_WriteThinkingWithoutAgentName(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1)
	if err := w.WriteThinking("", "x"); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if strings.Contains(s, "agent_name") {
		t.Errorf("agent_name should be omitted: %s", s)
	}
}

// TestSSEWriter_AgentNameInjection 表驱动覆盖 6 个 Write* 方法的 agent_name 注入逻辑：
//   - agentName 非空时 JSON 必含 "agent_name":"..."
//   - agentName 为空时 JSON 必无 agent_name 键（向后兼容主 Agent 事件）
//   - 同时验证各方法特有的 payload 关键字段（避免笔误漏测）
func TestSSEWriter_AgentNameInjection(t *testing.T) {
	cases := []struct {
		name     string
		write    func(w *SSEWriter, agent string) error
		mustHave string // 该方法 payload 必含的关键字段（非 agent_name）
	}{
		{
			name:     "thinking",
			write:    func(w *SSEWriter, a string) error { return w.WriteThinking(a, "x") },
			mustHave: `"reasoning_content":"x"`,
		},
		{
			name:     "message",
			write:    func(w *SSEWriter, a string) error { return w.WriteMessage(a, "hi") },
			mustHave: `"content":"hi"`,
		},
		{
			name: "tool_calls",
			write: func(w *SSEWriter, a string) error {
				return w.WriteToolCalls(a, []ToolCall{{ID: "tc_1", Type: "function"}})
			},
			mustHave: `"tool_calls"`,
		},
		{
			name:     "finish",
			write:    func(w *SSEWriter, a string) error { return w.WriteFinish(a, "stop") },
			mustHave: `"finish_reason":"stop"`,
		},
		{
			name: "tool_result",
			write: func(w *SSEWriter, a string) error {
				return w.WriteToolResult(a, "tc_1", "fs.read", "ok", false)
			},
			mustHave: `"tool_call_id":"tc_1"`,
		},
		{
			name:     "error",
			write:    func(w *SSEWriter, a string) error { return w.WriteError(a, "boom") },
			mustHave: `"message":"boom"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name+"/with_agent", func(t *testing.T) {
			buf := &bufFlusher{}
			w := NewSSEWriter(buf, "sess", "chat", 1)
			if err := c.write(w, "db-agent"); err != nil {
				t.Fatal(err)
			}
			s := buf.String()
			if !strings.Contains(s, `"agent_name":"db-agent"`) {
				t.Errorf("missing agent_name: %s", s)
			}
			if !strings.Contains(s, c.mustHave) {
				t.Errorf("missing %s: %s", c.mustHave, s)
			}
		})

		t.Run(c.name+"/without_agent", func(t *testing.T) {
			buf := &bufFlusher{}
			w := NewSSEWriter(buf, "sess", "chat", 1)
			if err := c.write(w, ""); err != nil {
				t.Fatal(err)
			}
			s := buf.String()
			if strings.Contains(s, "agent_name") {
				t.Errorf("agent_name should be omitted: %s", s)
			}
			if !strings.Contains(s, c.mustHave) {
				t.Errorf("missing %s: %s", c.mustHave, s)
			}
		})
	}
}
