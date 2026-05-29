package agent

import (
	"sync"
	"testing"
)

func TestTokenAccumulators_AddPop(t *testing.T) {
	a := NewTokenAccumulators()
	a.Add("chat_a", 10, 20)
	a.Add("chat_a", 5, 7)
	a.Add("chat_b", 1, 2)

	gotA := a.PopAndDelete("chat_a")
	if gotA.Prompt != 15 || gotA.Completion != 27 || gotA.Total != 42 {
		t.Errorf("chat_a: %+v", gotA)
	}
	// 再 pop 同一个 → 全 0
	gotA2 := a.PopAndDelete("chat_a")
	if gotA2.Prompt != 0 || gotA2.Completion != 0 || gotA2.Total != 0 {
		t.Errorf("chat_a after pop should be zero: %+v", gotA2)
	}
	gotB := a.PopAndDelete("chat_b")
	if gotB.Prompt != 1 || gotB.Completion != 2 || gotB.Total != 3 {
		t.Errorf("chat_b: %+v", gotB)
	}
}

func TestTokenAccumulators_Concurrent(t *testing.T) {
	a := NewTokenAccumulators()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Add("chat_x", 1, 2)
		}()
	}
	wg.Wait()
	got := a.PopAndDelete("chat_x")
	if got.Prompt != 100 || got.Completion != 200 || got.Total != 300 {
		t.Errorf("concurrent add: %+v", got)
	}
}

// TestTokenAccumulators_PopMissing 显式覆盖「未 Add 直接 Pop」契约：返回零值。
func TestTokenAccumulators_PopMissing(t *testing.T) {
	a := NewTokenAccumulators()
	got := a.PopAndDelete("never-added")
	if got != (TokenUsage{}) {
		t.Errorf("expected zero TokenUsage, got %+v", got)
	}
}

// TestTokenAccumulators_AddZero 验证 Add(0,0) 也会建立 entry，
// 之后 PopAndDelete 取回 {0,0,0} 而非「不存在」。这是规约的一部分：
// Add 仅依据是否调用过来判定 entry 存在性，不依据数值是否为 0。
func TestTokenAccumulators_AddZero(t *testing.T) {
	a := NewTokenAccumulators()
	a.Add("chat_zero", 0, 0)
	got := a.PopAndDelete("chat_zero")
	if got.Prompt != 0 || got.Completion != 0 || got.Total != 0 {
		t.Errorf("expected all-zero, got %+v", got)
	}
	// 再 pop 应返回零值（已删除）—— 与上面用例的零值在语义上区分不开，但路径不同
	got2 := a.PopAndDelete("chat_zero")
	if got2 != (TokenUsage{}) {
		t.Errorf("after pop, expected zero, got %+v", got2)
	}
}

// TestTokenAccumulators_ConcurrentMultiKey 多 chatID 并发交叉写，
// 验证 mutex 串行化效果及 key 之间互不污染。
func TestTokenAccumulators_ConcurrentMultiKey(t *testing.T) {
	a := NewTokenAccumulators()
	const perKey = 50
	keys := []string{"chat_p", "chat_q", "chat_r"}
	var wg sync.WaitGroup
	for _, k := range keys {
		for i := 0; i < perKey; i++ {
			wg.Add(1)
			go func(key string) {
				defer wg.Done()
				a.Add(key, 2, 3)
			}(k)
		}
	}
	wg.Wait()
	for _, k := range keys {
		got := a.PopAndDelete(k)
		if got.Prompt != 2*perKey || got.Completion != 3*perKey || got.Total != 5*perKey {
			t.Errorf("key %s: expected {%d,%d,%d}, got %+v", k, 2*perKey, 3*perKey, 5*perKey, got)
		}
	}
}
