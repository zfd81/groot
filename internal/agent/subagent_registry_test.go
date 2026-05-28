// Package agent SubAgentRegistry 单元测试。
package agent

import (
	"context"
	"testing"
	"time"
)

// TestSubAgentRegistry_GetReturnsRegisteredEntry 验证 Get 能取回已注册的子 Agent。
func TestSubAgentRegistry_GetReturnsRegisteredEntry(t *testing.T) {
	r := newEmptyRegistry(2)
	want := &SubAgentEntry{Name: "db-agent", Description: "数据库专家"}
	r.entries["db-agent"] = want

	got, ok := r.Get("db-agent")
	if !ok || got != want {
		t.Fatalf("Get returned %v, %v", got, ok)
	}
}

// TestSubAgentRegistry_GetMissing 验证未注册时 Get 返回 false。
func TestSubAgentRegistry_GetMissing(t *testing.T) {
	r := newEmptyRegistry(2)
	if _, ok := r.Get("nope"); ok {
		t.Fatal("expected miss")
	}
}

// TestSubAgentRegistry_AcquireRelease 验证并发名额的获取/释放语义：
// 容量 1 时第二次 Acquire 在 ctx 超时前应阻塞，超时后返回错误；
// Release 后再次 Acquire 应立即成功。
func TestSubAgentRegistry_AcquireRelease(t *testing.T) {
	r := newEmptyRegistry(1)
	ctx := context.Background()
	if err := r.Acquire(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	timed, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := r.Acquire(timed); err == nil {
		t.Fatal("second acquire should fail due to ctx timeout")
	}
	r.Release()
	if err := r.Acquire(ctx); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	r.Release()
}

// TestSubAgentRegistry_BuildDescription 验证拼接出的描述按字典序包含每个子 Agent。
func TestSubAgentRegistry_BuildDescription(t *testing.T) {
	r := newEmptyRegistry(1)
	r.entries["db-agent"] = &SubAgentEntry{Name: "db-agent", Description: "数据库专家"}
	r.entries["weather-agent"] = &SubAgentEntry{Name: "weather-agent", Description: "天气查询"}
	desc := r.BuildDescription()
	if !contains(desc, "- db-agent: 数据库专家") {
		t.Errorf("missing db-agent line: %s", desc)
	}
	if !contains(desc, "- weather-agent: 天气查询") {
		t.Errorf("missing weather-agent line: %s", desc)
	}
}

// TestSubAgentRegistry_BuildDescriptionEmpty 验证无子 Agent 时 fallback 文案。
func TestSubAgentRegistry_BuildDescriptionEmpty(t *testing.T) {
	r := newEmptyRegistry(1)
	desc := r.BuildDescription()
	if !contains(desc, "无可用子 Agent") {
		t.Errorf("expected '无可用子 Agent' fallback, got: %s", desc)
	}
}

// newEmptyRegistry 仅用于测试，跳过启动期扫描。
func newEmptyRegistry(maxConc int) *SubAgentRegistry {
	return newRegistryForTest(maxConc)
}
