// Package agent SubAgentRegistry 单元测试。
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
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

// TestScanSubAgentDirs_HappyPath 验证扫描层能正确识别合法子 Agent 并跳过非法目录：
//   - db-agent: 合法，应被识别
//   - no-desc: 缺 description，应跳过
//   - no-md: 缺 agent.md，应跳过
//   - groot: 与主 Agent 同名，应跳过
func TestScanSubAgentDirs_HappyPath(t *testing.T) {
	root := t.TempDir()
	// db-agent: 合法
	mustMkdir(t, filepath.Join(root, "db-agent"))
	mustWrite(t, filepath.Join(root, "db-agent", "agent.md"), `---
description: 数据库专家
---
正文
`)
	// no-desc: 缺 description，跳过
	mustMkdir(t, filepath.Join(root, "no-desc"))
	mustWrite(t, filepath.Join(root, "no-desc", "agent.md"), `---
model: gpt-4
---
body
`)
	// no-md: 缺 agent.md，跳过
	mustMkdir(t, filepath.Join(root, "no-md"))
	// groot: 与主 Agent 同名，跳过
	mustMkdir(t, filepath.Join(root, MainAgentName))
	mustWrite(t, filepath.Join(root, MainAgentName, "agent.md"), `---
description: 冒名顶替
---
`)

	log := newTestLogger(t)
	parsed := scanSubAgentDirs(root, log)
	names := make(map[string]bool)
	for _, p := range parsed {
		names[p.name] = true
	}
	if !names["db-agent"] {
		t.Errorf("db-agent should be parsed: %v", names)
	}
	if names["no-desc"] || names["no-md"] || names[MainAgentName] {
		t.Errorf("invalid agents should be skipped: %v", names)
	}
}

// TestScanSubAgentDirs_MissingRoot 验证扫描根目录不存在时静默返回空切片，不报错。
func TestScanSubAgentDirs_MissingRoot(t *testing.T) {
	log := newTestLogger(t)
	parsed := scanSubAgentDirs("/nonexistent/subagents", log)
	if len(parsed) != 0 {
		t.Errorf("expected empty result for missing root, got %d", len(parsed))
	}
}

// mustMkdir 测试 helper：创建目录，失败立即 t.Fatal。
func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
}

// mustWrite 测试 helper：写文件，失败立即 t.Fatal。
func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// newTestLogger 测试 helper：构造一个只输出 error 级别到 stdout 的 console logger。
func newTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	return logger.New(config.LoggingConfig{Level: "error", Format: "console", Output: []string{"stdout"}})
}
