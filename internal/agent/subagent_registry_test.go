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
	release, err := r.Acquire(ctx)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	timed, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := r.Acquire(timed); err == nil {
		t.Fatal("second acquire should fail due to ctx timeout")
	}
	release()
	release2, err := r.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

// TestSubAgentRegistry_ReleaseIsIdempotent 验证重复调用释放函数不会多还名额。
// 多还会让 semaphore 的计数变成负债，后续获取凭空超出上限。
func TestSubAgentRegistry_ReleaseIsIdempotent(t *testing.T) {
	r := newEmptyRegistry(1)
	ctx := context.Background()
	release, err := r.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()
	release()

	// 容量仍应是 1：占满后第二次获取必须阻塞到超时
	held, err := r.Acquire(ctx)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	defer held()
	timed, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := r.Acquire(timed); err == nil {
		t.Fatal("capacity leaked: acquire should have blocked")
	}
}

// TestSubAgentRegistry_SetMaxConcurrency 验证调整上限对随后的获取生效，
// 且旧名额的释放不会把容量还到新 semaphore 上。
func TestSubAgentRegistry_SetMaxConcurrency(t *testing.T) {
	r := newEmptyRegistry(1)
	ctx := context.Background()

	// 占满旧上限
	releaseOld, err := r.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire before resize: %v", err)
	}

	r.SetMaxConcurrency(2)
	if got := r.MaxConcurrency(); got != 2 {
		t.Fatalf("MaxConcurrency() = %d, want 2", got)
	}

	// 新上限为 2，应能再取两个名额
	r1, err := r.Acquire(ctx)
	if err != nil {
		t.Fatalf("first acquire after resize: %v", err)
	}
	r2, err := r.Acquire(ctx)
	if err != nil {
		t.Fatalf("second acquire after resize: %v", err)
	}

	// 第三个应被新上限挡住
	timed, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := r.Acquire(timed); err == nil {
		t.Fatal("third acquire should be blocked by new limit")
	}

	// 归还旧名额：它属于旧 semaphore，不应放宽新上限
	releaseOld()
	timed2, cancel2 := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel2()
	if _, err := r.Acquire(timed2); err == nil {
		t.Fatal("old release leaked capacity into the new semaphore")
	}

	r1()
	r2()
	r3, err := r.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire after releasing new holders: %v", err)
	}
	r3()
}

// TestSubAgentRegistry_SetMaxConcurrencyIgnoresNonPositive 验证非正数不改动上限。
func TestSubAgentRegistry_SetMaxConcurrencyIgnoresNonPositive(t *testing.T) {
	r := newEmptyRegistry(3)
	r.SetMaxConcurrency(0)
	r.SetMaxConcurrency(-1)
	if got := r.MaxConcurrency(); got != 3 {
		t.Fatalf("MaxConcurrency() = %d, want 3", got)
	}
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
	return NewRegistryForTest(maxConc)
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
	root := filepath.Join(t.TempDir(), "definitely-missing")
	parsed := scanSubAgentDirs(root, log)
	if len(parsed) != 0 {
		t.Errorf("expected empty result for missing root, got %d", len(parsed))
	}
}

// TestScanSubAgentDirs_SymlinkToDir 验证「指向目录的符号链接」也被识别为合法子 Agent。
// 用户可能用 ln -s 共享子 Agent 模板，os.DirEntry.IsDir() 对符号链接返回 false，
// 必须通过 os.Stat 解析后才能正确接受。
func TestScanSubAgentDirs_SymlinkToDir(t *testing.T) {
	root := t.TempDir()
	// 真实目录放在 root 之外，以模拟 ln -s 共享场景
	realDir := filepath.Join(t.TempDir(), "shared-agent")
	mustMkdir(t, realDir)
	mustWrite(t, filepath.Join(realDir, "agent.md"), `---
description: 共享子 Agent
---
正文
`)
	link := filepath.Join(root, "linked-agent")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	log := newTestLogger(t)
	parsed := scanSubAgentDirs(root, log)
	if len(parsed) != 1 || parsed[0].name != "linked-agent" {
		t.Fatalf("expected 1 entry 'linked-agent', got: %+v", parsed)
	}
	if parsed[0].md.Description != "共享子 Agent" {
		t.Errorf("description mismatch: %q", parsed[0].md.Description)
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
