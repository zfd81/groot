// Package agent CallAgentTool 单元测试。
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/logger"
)

// fakeAgentTool 用于测试的子 Agent tool 桩；实现 tool.InvokableTool 接口。
type fakeAgentTool struct {
	result string
	err    error
	sleep  time.Duration
}

func (f *fakeAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "fake", Desc: "fake"}, nil
}
func (f *fakeAgentTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	if f.sleep > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(f.sleep):
		}
	}
	return f.result, f.err
}

// newTestCallAgentTool 构造一个最小化的 CallAgentTool 实例，用于单元测试。
// memory / runtimeState 留 nil，测试只走简化路径。
func newTestCallAgentTool(reg *SubAgentRegistry, maxTask, maxResult int) *CallAgentTool {
	return &CallAgentTool{
		registry:          reg,
		parentChatID:      "chat_p",
		sessionID:         "sess_x",
		maxTaskLen:        maxTask,
		maxResultLen:      maxResult,
		execTimeout:       2 * time.Second,
		tokenAccumulators: NewTokenAccumulators(),
		log:               logger.NewNop(),
	}
}

// newFakeEntry 构造一个测试用 SubAgentEntry，注入预制 InvokableTool 跳过
// BuildAgentTool 的真实 LLM 构造。
func newFakeEntry(name string, tool *fakeAgentTool) *SubAgentEntry {
	e := &SubAgentEntry{Name: name}
	e.SetToolForTest(tool)
	return e
}

// TestCallAgentTool_UnknownAgent 验证调用未知子 Agent 时返回带有该名称的错误。
func TestCallAgentTool_UnknownAgent(t *testing.T) {
	reg := NewRegistryForTest(2)
	tool := newTestCallAgentTool(reg, 100, 100)
	_, err := tool.InvokableRun(context.Background(), `{"agent_name":"nope","task":"do it"}`)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected unknown agent error, got: %v", err)
	}
}

// TestCallAgentTool_TaskTooLong 验证 task 长度超限时返回错误。
func TestCallAgentTool_TaskTooLong(t *testing.T) {
	reg := NewRegistryForTest(2)
	reg.entries["fake"] = newFakeEntry("fake", &fakeAgentTool{result: "ok"})
	tool := newTestCallAgentTool(reg, 5, 100)
	_, err := tool.InvokableRun(context.Background(), `{"agent_name":"fake","task":"too long task"}`)
	if err == nil || !strings.Contains(err.Error(), "task") {
		t.Fatalf("expected task length error, got: %v", err)
	}
}

// TestCallAgentTool_TruncatesLongResult 验证结果超限时被截断且带有警告前缀和长度信息。
func TestCallAgentTool_TruncatesLongResult(t *testing.T) {
	reg := NewRegistryForTest(2)
	long := strings.Repeat("x", 1000)
	reg.entries["fake"] = newFakeEntry("fake", &fakeAgentTool{result: long})
	tool := newTestCallAgentTool(reg, 100, 50)
	out, err := tool.InvokableRun(context.Background(), `{"agent_name":"fake","task":"t"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "⚠️") {
		t.Errorf("truncation warning should lead: %q", out[:20])
	}
	if !strings.Contains(out, "1000") || !strings.Contains(out, "50") {
		t.Errorf("warning should mention sizes: %q", out)
	}
}

// TestCallAgentTool_ConcurrencyAcquireBlocks 验证容量为 1 时第二个调用在
// ctx 超时前因 Acquire 阻塞而失败，错误链应携带 context.DeadlineExceeded。
func TestCallAgentTool_ConcurrencyAcquireBlocks(t *testing.T) {
	reg := NewRegistryForTest(1)
	reg.entries["fake"] = newFakeEntry("fake", &fakeAgentTool{result: "ok", sleep: 100 * time.Millisecond})
	toolA := newTestCallAgentTool(reg, 100, 100)
	toolB := newTestCallAgentTool(reg, 100, 100)

	done := make(chan error, 1)
	go func() {
		_, err := toolA.InvokableRun(context.Background(), `{"agent_name":"fake","task":"t"}`)
		done <- err
	}()

	// B 在 A 释放前应被 ctx 取消
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := toolB.InvokableRun(ctx, `{"agent_name":"fake","task":"t"}`)
	if err == nil {
		t.Fatal("expected B to fail due to ctx timeout while A holds the semaphore")
	}
	// 必须携带 DeadlineExceeded（Acquire 失败链或子 Agent 内 ctx 超时均合规），
	// 排除「假阳性 nil-but-passing」或不相关错误的情况。
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded in error chain, got: %v", err)
	}
	<-done
}

// TestCallAgentTool_PropagatesErr 验证子 Agent 返回的错误被透传。
func TestCallAgentTool_PropagatesErr(t *testing.T) {
	reg := NewRegistryForTest(2)
	reg.entries["fake"] = newFakeEntry("fake", &fakeAgentTool{err: errors.New("boom")})
	tool := newTestCallAgentTool(reg, 100, 100)
	_, err := tool.InvokableRun(context.Background(), `{"agent_name":"fake","task":"t"}`)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected propagated error, got: %v", err)
	}
}

// TestCallAgentTool_InvalidJSON 验证非法 JSON 入参被 Unmarshal 守卫拦截。
func TestCallAgentTool_InvalidJSON(t *testing.T) {
	reg := NewRegistryForTest(2)
	tool := newTestCallAgentTool(reg, 100, 100)
	_, err := tool.InvokableRun(context.Background(), `not a json`)
	if err == nil || !strings.Contains(err.Error(), "unmarshal call_agent input") {
		t.Fatalf("expected unmarshal error, got: %v", err)
	}
}

// TestCallAgentTool_ExecTimeoutFires 验证 execTimeout 真的触发：
// 子 Agent 沉睡 200ms，工具的 execTimeout 设为 20ms，应在子 Agent 自然返回前被取消。
func TestCallAgentTool_ExecTimeoutFires(t *testing.T) {
	reg := NewRegistryForTest(1)
	reg.entries["fake"] = newFakeEntry("fake", &fakeAgentTool{sleep: 200 * time.Millisecond})
	tool := newTestCallAgentTool(reg, 100, 100)
	tool.execTimeout = 20 * time.Millisecond
	start := time.Now()
	_, err := tool.InvokableRun(context.Background(), `{"agent_name":"fake","task":"t"}`)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// 给 50ms 余量；如果远超 200ms，说明 execTimeout 没生效
	if elapsed > 100*time.Millisecond {
		t.Errorf("expected execTimeout to fire within ~20ms+overhead, but took %v", elapsed)
	}
}
