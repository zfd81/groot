package agent

import (
	"sync"
	"testing"
)

// 主 Agent（agentName 为空串）与子 Agent 的 content 按到达顺序拼接。
func TestAggregator_ConcatenatesMainAndSubAgent(t *testing.T) {
	a := NewAggregator()
	for _, ev := range []struct {
		agentName string
		content   string
	}{
		{"", "a"},
		{"sub", "sub"},
		{"", "b"},
	} {
		if err := a.WriteMessage(ev.agentName, ev.content); err != nil {
			t.Fatalf("WriteMessage(%q, %q) error = %v", ev.agentName, ev.content, err)
		}
	}
	if got := a.Result().Content; got != "asubb" {
		t.Fatalf("Result().Content = %q, want %q", got, "asubb")
	}
}

// 无 thinking 事件时 ReasoningContent 为空；finish 只被非空 reason 覆盖，最后一个非空值生效。
func TestAggregator_ReasoningAndFinish(t *testing.T) {
	a := NewAggregator()
	if got := a.Result().ReasoningContent; got != "" {
		t.Fatalf("ReasoningContent = %q, want empty", got)
	}

	if err := a.WriteThinking("", "think-1"); err != nil {
		t.Fatalf("WriteThinking error = %v", err)
	}
	if err := a.WriteThinking("sub", "think-2"); err != nil {
		t.Fatalf("WriteThinking error = %v", err)
	}
	if got := a.Result().ReasoningContent; got != "think-1think-2" {
		t.Fatalf("ReasoningContent = %q, want %q", got, "think-1think-2")
	}

	if err := a.WriteFinish("", "length"); err != nil {
		t.Fatalf("WriteFinish error = %v", err)
	}
	if err := a.WriteFinish("", ""); err != nil {
		t.Fatalf("WriteFinish error = %v", err)
	}
	if got := a.Result().FinishReason; got != "length" {
		t.Fatalf("FinishReason after empty reason = %q, want %q", got, "length")
	}
	if err := a.WriteFinish("sub", "stop"); err != nil {
		t.Fatalf("WriteFinish error = %v", err)
	}
	if got := a.Result().FinishReason; got != "stop" {
		t.Fatalf("FinishReason = %q, want %q", got, "stop")
	}
}

// WriteError 记录错误信息。
func TestAggregator_Error(t *testing.T) {
	a := NewAggregator()
	if err := a.WriteError("", "boom"); err != nil {
		t.Fatalf("WriteError error = %v", err)
	}
	if got := a.Result().Error; got == "" {
		t.Fatal("Result().Error is empty, want non-empty")
	}
}

// NopEventWriter 的每个方法都返回 nil，不 panic。
func TestNopEventWriter_NoPanic(t *testing.T) {
	w := NopEventWriter()
	checks := []error{
		w.WriteThinking("", "x"),
		w.WriteMessage("", "x"),
		w.WriteToolCalls("", nil),
		w.WriteFinish("", "stop"),
		w.WriteToolResult("", "id", "name", "content", false),
		w.WriteError("", "x"),
		w.WriteUsage(TokenUsage{}),
		w.WriteDone(),
	}
	for i, err := range checks {
		if err != nil {
			t.Fatalf("NopEventWriter method #%d returned %v, want nil", i, err)
		}
	}
}

// 执行器可能在 goroutine 里回调事件方法，Aggregator 必须并发安全（配合 -race）。
func TestAggregator_ConcurrentWrites(t *testing.T) {
	a := NewAggregator()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := a.WriteMessage("", "m"); err != nil {
					t.Errorf("WriteMessage error = %v", err)
					return
				}
				if err := a.WriteThinking("sub", "t"); err != nil {
					t.Errorf("WriteThinking error = %v", err)
					return
				}
				if err := a.WriteFinish("", "stop"); err != nil {
					t.Errorf("WriteFinish error = %v", err)
					return
				}
				if err := a.WriteUsage(TokenUsage{Prompt: 1}); err != nil {
					t.Errorf("WriteUsage error = %v", err)
					return
				}
				_ = a.Result()
			}
		}()
	}
	wg.Wait()
	if got := len(a.Result().Content); got != 800 {
		t.Fatalf("Content length = %d, want 800", got)
	}
}
