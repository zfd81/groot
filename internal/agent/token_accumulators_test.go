package agent

import (
	"reflect"
	"sync"
	"testing"
)

func TestTokenAccumulators_AddPop(t *testing.T) {
	a := NewTokenAccumulators()
	a.Add("chat_a", 10, 20)
	a.Add("chat_a", 5, 7)
	a.Add("chat_b", 1, 2)

	gotA := a.PopAndDelete("chat_a").Tokens
	if gotA.Prompt != 15 || gotA.Completion != 27 || gotA.Total != 42 {
		t.Errorf("chat_a: %+v", gotA)
	}
	// 再 pop 同一个 → 全 0
	gotA2 := a.PopAndDelete("chat_a").Tokens
	if gotA2.Prompt != 0 || gotA2.Completion != 0 || gotA2.Total != 0 {
		t.Errorf("chat_a after pop should be zero: %+v", gotA2)
	}
	gotB := a.PopAndDelete("chat_b").Tokens
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
	got := a.PopAndDelete("chat_x").Tokens
	if got.Prompt != 100 || got.Completion != 200 || got.Total != 300 {
		t.Errorf("concurrent add: %+v", got)
	}
}

// TestTokenAccumulators_PopMissing 显式覆盖「未 Add 直接 Pop」契约：返回零值。
func TestTokenAccumulators_PopMissing(t *testing.T) {
	a := NewTokenAccumulators()
	got := a.PopAndDelete("never-added")
	if !reflect.DeepEqual(got, ChatAggregate{}) {
		t.Errorf("expected zero ChatAggregate, got %+v", got)
	}
}

// TestTokenAccumulators_AddZero 验证 Add(0,0) 也会建立 entry，
// 之后 PopAndDelete 取回 {0,0,0} 而非「不存在」。这是规约的一部分：
// Add 仅依据是否调用过来判定 entry 存在性，不依据数值是否为 0。
func TestTokenAccumulators_AddZero(t *testing.T) {
	a := NewTokenAccumulators()
	a.Add("chat_zero", 0, 0)
	got := a.PopAndDelete("chat_zero").Tokens
	if got.Prompt != 0 || got.Completion != 0 || got.Total != 0 {
		t.Errorf("expected all-zero, got %+v", got)
	}
	// 再 pop 应返回零值（已删除）
	got2 := a.PopAndDelete("chat_zero")
	if !reflect.DeepEqual(got2, ChatAggregate{}) {
		t.Errorf("after pop, expected zero ChatAggregate, got %+v", got2)
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
		got := a.PopAndDelete(k).Tokens
		if got.Prompt != 2*perKey || got.Completion != 3*perKey || got.Total != 5*perKey {
			t.Errorf("key %s: expected {%d,%d,%d}, got %+v", k, 2*perKey, 3*perKey, 5*perKey, got)
		}
	}
}

// TestTokenAccumulators_StepsAndModel 覆盖新增的 step 序列与 model 字段累积/取出。
func TestTokenAccumulators_StepsAndModel(t *testing.T) {
	a := NewTokenAccumulators()
	a.AppendStep("c1", StepRecord{StepID: "s1", Type: "tool", Name: "foo", Status: StatusRunning})
	a.AppendStep("c1", StepRecord{StepID: "s2", Type: "tool", Name: "bar", Status: StatusRunning})
	a.CompleteStep("c1", "s1")
	a.SetModel("c1", "gpt-4")
	a.Add("c1", 10, 20)

	got := a.PopAndDelete("c1")
	if got.Model != "gpt-4" {
		t.Errorf("model: %q", got.Model)
	}
	if got.Tokens.Total != 30 {
		t.Errorf("tokens.total: %d", got.Tokens.Total)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("steps len: %d", len(got.Steps))
	}
	if got.Steps[0].Status != StatusCompleted {
		t.Errorf("step s1 should be completed, got %s", got.Steps[0].Status)
	}
	if got.Steps[1].Status != StatusRunning {
		t.Errorf("step s2 should remain running, got %s", got.Steps[1].Status)
	}
}

func TestTokenAccumulators_RoundBasic(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.Add("main", 10, 5)
	a.RegisterChild("main", "coder", "child1")
	if got := a.ChildFor("main", "coder"); got != "child1" {
		t.Fatalf("ChildFor = %q", got)
	}
	a.Add("child1", 100, 50)
	agg := a.PopChild("main", "coder", "child1")
	if agg.Tokens != (TokenUsage{100, 50, 150}) {
		t.Fatalf("PopChild tokens = %+v", agg.Tokens)
	}
	// PopChild 不注销登记：子 Agent 结束后引擎可能仍有尾部 usage chunk 需按登记归属
	if got := a.ChildFor("main", "coder"); got != "child1" {
		t.Fatalf("ChildFor after pop = %q, want child1", got)
	}
	main, round := a.PopRound("main")
	if main.Tokens != (TokenUsage{10, 5, 15}) {
		t.Errorf("main = %+v", main.Tokens)
	}
	if round != (TokenUsage{110, 55, 165}) {
		t.Errorf("round = %+v", round)
	}
	if _, round2 := a.PopRound("main"); round2 != (TokenUsage{}) {
		t.Errorf("second PopRound should be zero")
	}
}

func TestTokenAccumulators_PopRound_SumsRetiredAndLive(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.Add("main", 1, 1)
	a.RegisterChild("main", "coder", "c1")
	a.Add("c1", 10, 10)
	a.PopChild("main", "coder", "c1") // 第一次调用已归还
	a.RegisterChild("main", "coder", "c2")
	a.Add("c2", 20, 20) // 第二次调用仍在飞
	a.RegisterChild("main", "db", "c3")
	a.Add("c3", 5, 5)
	_, round := a.PopRound("main")
	if round != (TokenUsage{36, 36, 72}) {
		t.Fatalf("round = %+v", round)
	}
	// 未归还子聚合也已被删除
	if a.PopAndDelete("c2").Tokens != (TokenUsage{}) || a.PopAndDelete("c3").Tokens != (TokenUsage{}) {
		t.Fatal("live children should be removed by PopRound")
	}
}

func TestTokenAccumulators_ClosedRoundDropsLateWrites(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.RegisterChild("main", "coder", "c1")
	a.Add("c1", 10, 10)
	a.PopRound("main")
	a.RegisterChild("main", "coder", "c9") // 轮次已关闭：丢弃
	if a.ChildFor("main", "coder") != "" {
		t.Fatal("late RegisterChild must be dropped")
	}
	agg := a.PopChild("main", "coder", "c1") // 已被 PopRound 删除：零值，不重建
	if agg.Tokens != (TokenUsage{}) {
		t.Fatalf("late PopChild = %+v", agg.Tokens)
	}
	a.mu.Lock()
	nRounds, nM := len(a.rounds), len(a.m)
	a.mu.Unlock()
	if nRounds != 0 || nM != 0 {
		t.Fatalf("leak: rounds=%d m=%d", nRounds, nM)
	}
}

func TestTokenAccumulators_RegisterChildWithoutRoundIgnored(t *testing.T) {
	a := NewTokenAccumulators()
	a.RegisterChild("main", "coder", "c1")
	if a.ChildFor("main", "coder") != "" {
		t.Fatal("RegisterChild without OpenRound must be ignored")
	}
}

func TestTokenAccumulators_PopChildOutsideRoundStillDeletes(t *testing.T) {
	a := NewTokenAccumulators()
	a.Add("c1", 3, 4)
	agg := a.PopChild("nope", "x", "c1")
	if agg.Tokens != (TokenUsage{3, 4, 7}) {
		t.Fatalf("got %+v", agg.Tokens)
	}
	if a.PopAndDelete("c1").Tokens != (TokenUsage{}) {
		t.Fatal("c1 should be deleted")
	}
}

// TestTokenAccumulators_AddAfterPopChildCountedInRound 覆盖 PopChild 与引擎 accumulateUsage 的竞争：
// 子 Agent 已 PopChild 之后仍有迟到的 Add（最后一个 usage chunk），PopRound 必须把它计入全轮合计，
// 且事后不留任何残余 entry。
func TestTokenAccumulators_AddAfterPopChildCountedInRound(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.RegisterChild("main", "coder", "c1")
	a.Add("c1", 10, 10)
	agg := a.PopChild("main", "coder", "c1")
	if agg.Tokens != (TokenUsage{10, 10, 20}) {
		t.Fatalf("PopChild = %+v", agg.Tokens)
	}
	a.Add("c1", 5, 5) // 迟到写入：登记仍在，ChildFor 仍解析到 c1
	if got := a.ChildFor("main", "coder"); got != "c1" {
		t.Fatalf("ChildFor after PopChild = %q, want c1", got)
	}
	_, round := a.PopRound("main")
	if round != (TokenUsage{15, 15, 30}) {
		t.Fatalf("round = %+v, want {15 15 30}", round)
	}
	a.mu.Lock()
	nRounds, nM := len(a.rounds), len(a.m)
	a.mu.Unlock()
	if nRounds != 0 || nM != 0 {
		t.Fatalf("leak: rounds=%d m=%d", nRounds, nM)
	}
}

// TestTokenAccumulators_PopChildUnregisteredStillRetires 轮次存在但 childChatID 未经 RegisterChild 登记：
// PopChild 仍把其 token 计入 retired，并由 PopRound 汇总。
func TestTokenAccumulators_PopChildUnregisteredStillRetires(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.Add("orphan", 7, 3)
	a.PopChild("main", "coder", "orphan")
	if _, round := a.PopRound("main"); round != (TokenUsage{7, 3, 10}) {
		t.Fatalf("round = %+v, want {7 3 10}", round)
	}
}

// TestTokenAccumulators_RetiredIDReregisteredNoDoubleCount 同一 agentName 第二次调用覆盖登记后，
// 旧 id 在 retiredIDs、新 id 在 children，各自只计一次。
func TestTokenAccumulators_RetiredIDReregisteredNoDoubleCount(t *testing.T) {
	a := NewTokenAccumulators()
	a.OpenRound("main")
	a.RegisterChild("main", "coder", "c1")
	a.Add("c1", 10, 10)
	a.PopChild("main", "coder", "c1")
	a.Add("c1", 1, 1) // 迟到
	a.RegisterChild("main", "coder", "c2")
	if got := a.ChildFor("main", "coder"); got != "c2" {
		t.Fatalf("ChildFor = %q, want c2", got)
	}
	a.Add("c2", 20, 20)
	_, round := a.PopRound("main")
	if round != (TokenUsage{31, 31, 62}) {
		t.Fatalf("round = %+v, want {31 31 62}", round)
	}
	a.mu.Lock()
	nM := len(a.m)
	a.mu.Unlock()
	if nM != 0 {
		t.Fatalf("leak: m=%d", nM)
	}
}
