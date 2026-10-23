package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// bufFlusher 是测试用的 flushWriter：把 SSE 字节流缓冲到内存。
// 注意 Flush 用指针接收器，避免与 *bytes.Buffer 的 Write（指针接收器）混搭引发误用。
type bufFlusher struct{ bytes.Buffer }

func (b *bufFlusher) Flush() error { return nil }

func TestSSEWriter_WriteThinkingWithAgentName(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
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
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteThinking("", "x"); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if strings.Contains(s, "agent_name") {
		t.Errorf("agent_name should be omitted: %s", s)
	}
}

// decodeChunk 把 buf 中最后一条 "data: {...}" 解成 map，供断言用
func decodeChunk(t *testing.T, s string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(s), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "data: ") {
		t.Fatalf("last line is not a data line: %q", last)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(last, "data: ")), &m); err != nil {
		t.Fatalf("decode chunk: %v (%s)", err, last)
	}
	return m
}

// firstChoice 返回 choices[0]，并校验 choices 长度恰为 1
func firstChoice(t *testing.T, chunk map[string]any) map[string]any {
	t.Helper()
	choices, ok := chunk["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices should have exactly 1 element: %v", chunk["choices"])
	}
	return choices[0].(map[string]any)
}

// chunkDelta 返回 choices[0].delta
func chunkDelta(t *testing.T, chunk map[string]any) map[string]any {
	t.Helper()
	delta, ok := firstChoice(t, chunk)["delta"].(map[string]any)
	if !ok {
		t.Fatalf("delta should be an object: %v", chunk)
	}
	return delta
}

func TestSSEWriter_ChunkEnvelope(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteMessage("", "hi"); err != nil {
		t.Fatal(err)
	}
	chunk := decodeChunk(t, buf.String())
	if chunk["id"] != "chat" {
		t.Errorf("id = %v, want chat", chunk["id"])
	}
	if chunk["object"] != "chat.completion.chunk" {
		t.Errorf("object = %v", chunk["object"])
	}
	if chunk["created"] != float64(1760947200) {
		t.Errorf("created = %v", chunk["created"])
	}
	if chunk["model"] != "qwen-max" {
		t.Errorf("model = %v", chunk["model"])
	}
	choice := firstChoice(t, chunk)
	if choice["index"] != float64(0) {
		t.Errorf("index = %v", choice["index"])
	}
	if fr, ok := choice["finish_reason"]; !ok || fr != nil {
		t.Errorf("finish_reason should be present and null: %v (present=%v)", fr, ok)
	}
	delta := chunkDelta(t, chunk)
	if delta["role"] != "assistant" || delta["content"] != "hi" {
		t.Errorf("delta = %v", delta)
	}
	if _, ok := delta["agent_name"]; ok {
		t.Errorf("agent_name should be omitted: %v", delta)
	}
}

func TestSSEWriter_FinishMovesToChoice(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteFinish("", "stop"); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, `"delta":{}`) {
		t.Errorf("delta should serialize as empty object: %s", s)
	}
	chunk := decodeChunk(t, s)
	if firstChoice(t, chunk)["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", firstChoice(t, chunk)["finish_reason"])
	}
	delta := chunkDelta(t, chunk)
	if _, ok := delta["finish_reason"]; ok {
		t.Errorf("finish_reason must not be in delta: %v", delta)
	}
	if len(delta) != 0 {
		t.Errorf("delta should be empty: %v", delta)
	}
}

func TestSSEWriter_FinishWithAgentName(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteFinish("coder", "tool_calls"); err != nil {
		t.Fatal(err)
	}
	chunk := decodeChunk(t, buf.String())
	if firstChoice(t, chunk)["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v", firstChoice(t, chunk)["finish_reason"])
	}
	delta := chunkDelta(t, chunk)
	if len(delta) != 1 || delta["agent_name"] != "coder" {
		t.Errorf("delta = %v, want {agent_name: coder}", delta)
	}
}

func TestSSEWriter_ToolResultInDelta(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteToolResult("", "tc_1", "fs.read", "ok", true); err != nil {
		t.Fatal(err)
	}
	delta := chunkDelta(t, decodeChunk(t, buf.String()))
	want := map[string]any{
		"role": "tool", "tool_call_id": "tc_1", "tool_name": "fs.read", "content": "ok", "error": true,
	}
	for k, v := range want {
		if delta[k] != v {
			t.Errorf("delta[%s] = %v, want %v", k, delta[k], v)
		}
	}
}

func TestSSEWriter_ErrorEnvelope(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteError("", "boom"); err != nil {
		t.Fatal(err)
	}
	chunk := decodeChunk(t, buf.String())
	if _, ok := chunk["choices"]; ok {
		t.Errorf("error event must not have choices: %v", chunk)
	}
	e, ok := chunk["error"].(map[string]any)
	if !ok {
		t.Fatalf("error should be an object: %v", chunk)
	}
	if e["message"] != "boom" || e["type"] != "server_error" {
		t.Errorf("error = %v", e)
	}
	if code, ok := e["code"]; !ok || code != nil {
		t.Errorf("code should be null: %v", e)
	}
	if param, ok := e["param"]; !ok || param != nil {
		t.Errorf("param should be null: %v", e)
	}
	if _, ok := e["agent_name"]; ok {
		t.Errorf("agent_name should be omitted: %v", e)
	}

	buf.Reset()
	if err := w.WriteError("coder", "boom"); err != nil {
		t.Fatal(err)
	}
	e = decodeChunk(t, buf.String())["error"].(map[string]any)
	if e["agent_name"] != "coder" {
		t.Errorf("error.agent_name = %v, want coder", e["agent_name"])
	}
}

func TestSSEWriter_DoneUnchanged(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteDone(); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "data: [DONE]\n\n" {
		t.Errorf("WriteDone output = %q", got)
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
			w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
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
			// agent_name 必须位于 delta 内（error 事件位于 error 对象内）
			chunk := decodeChunk(t, s)
			if c.name == "error" {
				if got := chunk["error"].(map[string]any)["agent_name"]; got != "db-agent" {
					t.Errorf("error.agent_name = %v, want db-agent", got)
				}
			} else if got := chunkDelta(t, chunk)["agent_name"]; got != "db-agent" {
				t.Errorf("delta.agent_name = %v, want db-agent", got)
			}
		})

		t.Run(c.name+"/without_agent", func(t *testing.T) {
			buf := &bufFlusher{}
			w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
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

// TestSSEWriter_ModelForSubAgent 登记子 Agent 模型后，其事件的 model 反映子模型，
// 主 Agent（agentName 为空）事件仍为主模型。
func TestSSEWriter_ModelForSubAgent(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	w.SetAgentModel("coder", "deepseek-r1")

	if err := w.WriteMessage("coder", "x"); err != nil {
		t.Fatal(err)
	}
	if got := decodeChunk(t, buf.String())["model"]; got != "deepseek-r1" {
		t.Errorf("sub-agent chunk model = %v, want deepseek-r1", got)
	}

	buf.Reset()
	if err := w.WriteMessage("", "x"); err != nil {
		t.Fatal(err)
	}
	if got := decodeChunk(t, buf.String())["model"]; got != "qwen-max" {
		t.Errorf("main chunk model = %v, want qwen-max", got)
	}
}

// TestSSEWriter_ModelFallbackWhenUnregistered 未登记、已清除、登记为空串三种情况均回退主模型。
func TestSSEWriter_ModelFallbackWhenUnregistered(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)

	// 未登记
	if err := w.WriteMessage("other", "x"); err != nil {
		t.Fatal(err)
	}
	if got := decodeChunk(t, buf.String())["model"]; got != "qwen-max" {
		t.Errorf("unregistered model = %v, want qwen-max", got)
	}

	// 登记后清除
	w.SetAgentModel("coder", "deepseek-r1")
	w.ClearAgentModel("coder")
	buf.Reset()
	if err := w.WriteMessage("coder", "x"); err != nil {
		t.Fatal(err)
	}
	if got := decodeChunk(t, buf.String())["model"]; got != "qwen-max" {
		t.Errorf("cleared model = %v, want qwen-max", got)
	}

	// 登记为空串视为未登记
	w.SetAgentModel("coder", "deepseek-r1")
	w.SetAgentModel("coder", "")
	buf.Reset()
	if err := w.WriteMessage("coder", "x"); err != nil {
		t.Fatal(err)
	}
	if got := decodeChunk(t, buf.String())["model"]; got != "qwen-max" {
		t.Errorf("empty-registered model = %v, want qwen-max", got)
	}
}

// TestSSEWriter_ModelMapConcurrent 并发 Set/Write/Clear 不 panic、无数据竞争（需 -race）。
func TestSSEWriter_ModelMapConcurrent(t *testing.T) {
	w := NewSSEWriter(&bufFlusher{}, "sess", "chat", 1, "qwen-max", 1760947200)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("agent-%d", i)
			for j := 0; j < 50; j++ {
				w.SetAgentModel(name, "m")
				_ = w.modelFor(name)
				w.ClearAgentModel(name)
			}
		}(i)
	}
	wg.Wait()
	if got := w.modelFor("agent-0"); got != "qwen-max" {
		t.Errorf("after clear model = %q, want qwen-max", got)
	}
}

// TestSSEWriter_SetModel SetModel 覆盖主模型名（Solo 模式下由 agent.md 钉死的模型），
// 之后主 Agent（agentName 为空）事件的 model 反映新值；空串为 no-op。
func TestSSEWriter_SetModel(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)

	w.SetModel("deepseek-r1")
	if err := w.WriteMessage("", "x"); err != nil {
		t.Fatal(err)
	}
	if got := decodeChunk(t, buf.String())["model"]; got != "deepseek-r1" {
		t.Errorf("after SetModel chunk model = %v, want deepseek-r1", got)
	}

	// 空串为 no-op，保持上一次设置
	buf.Reset()
	w.SetModel("")
	if err := w.WriteMessage("", "x"); err != nil {
		t.Fatal(err)
	}
	if got := decodeChunk(t, buf.String())["model"]; got != "deepseek-r1" {
		t.Errorf("after SetModel(\"\") chunk model = %v, want deepseek-r1 (no-op)", got)
	}
}

func TestSSEWriter_OrdinaryChunkHasNullUsage(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteMessage("", "hi"); err != nil {
		t.Fatal(err)
	}
	chunk := decodeChunk(t, buf.String())
	u, ok := chunk["usage"]
	if !ok || u != nil {
		t.Fatalf("usage should be present and null, got %v (present=%v)", u, ok)
	}
}

func TestSSEWriter_WriteUsage(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 1760947200)
	if err := w.WriteUsage(TokenUsage{Prompt: 120, Completion: 45, Total: 165}); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()
	if !strings.Contains(raw, `"choices":[]`) {
		t.Fatalf("choices must serialize as []: %s", raw)
	}
	chunk := decodeChunk(t, raw)
	if chunk["id"] != "chat" || chunk["object"] != "chat.completion.chunk" || chunk["created"] != float64(1760947200) || chunk["model"] != "qwen-max" {
		t.Errorf("envelope = %v", chunk)
	}
	u := chunk["usage"].(map[string]any)
	if u["prompt_tokens"] != float64(120) || u["completion_tokens"] != float64(45) || u["total_tokens"] != float64(165) {
		t.Errorf("usage = %v", u)
	}
	if len(u) != 3 {
		t.Errorf("usage must have exactly 3 keys: %v", u)
	}
}

func TestSSEWriter_WriteUsageModelFollowsSetModel(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "qwen-max", 0)
	w.SetAgentModel("coder", "deepseek-r1")
	w.SetModel("solo-model")
	if err := w.WriteUsage(TokenUsage{1, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if got := decodeChunk(t, buf.String())["model"]; got != "solo-model" {
		t.Errorf("model = %v, want solo-model", got)
	}
}

func TestSSEWriter_ErrorHasNoUsage(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1, "m", 0)
	if err := w.WriteError("", "boom"); err != nil {
		t.Fatal(err)
	}
	chunk := decodeChunk(t, buf.String())
	if _, ok := chunk["usage"]; ok {
		t.Errorf("error event must not carry usage: %v", chunk)
	}
}
