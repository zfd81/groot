package logger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/config"
)

// newFileLogger 创建写入 dir 的 JSON 文件 logger
func newFileLogger(t *testing.T, dir string) *Logger {
	t.Helper()
	cfg := config.LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: []string{"file"},
		File: config.LogFileConfig{
			Directory:       dir,
			FilenamePattern: "test-{date}.log",
		},
	}
	return New(cfg)
}

// readLogLines 读取 dir 下唯一的日志文件，返回去除空行后的每行内容。
// 用 glob 匹配而非自行推算日期，避免跨午夜时文件名不一致
func readLogLines(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "test-*.log"))
	if err != nil {
		t.Fatalf("查找日志文件失败: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("期望 1 个日志文件，实际 %d 个: %v", len(matches), matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestWith_SessionIDInJSONOutput(t *testing.T) {
	dir := t.TempDir()
	l := newFileLogger(t, dir)

	sessionLog := l.With(zap.String("session_id", "sess_test_123"))
	sessionLog.Info("hello with session")
	l.Info("hello without session")
	// rotatingWriter 直写文件无缓冲，无需 Sync flush

	lines := readLogLines(t, dir)
	if len(lines) != 2 {
		t.Fatalf("期望 2 行日志，实际 %d 行", len(lines))
	}
	if !strings.Contains(lines[0], `"session_id":"sess_test_123"`) {
		t.Errorf("With 派生的日志应包含 session_id 字段: %s", lines[0])
	}
	if strings.Contains(lines[1], "session_id") {
		t.Errorf("原 logger 的日志不应包含 session_id 字段: %s", lines[1])
	}
}

// TestCaller_PointsToCallSite 验证 caller 指向调用方而非 logger 包内部
func TestCaller_PointsToCallSite(t *testing.T) {
	dir := t.TempDir()
	l := newFileLogger(t, dir)

	l.Info("caller check")

	lines := readLogLines(t, dir)
	if len(lines) != 1 {
		t.Fatalf("期望 1 行日志，实际 %d 行", len(lines))
	}
	if !strings.Contains(lines[0], "context_test.go") {
		t.Errorf("caller 应指向调用方 context_test.go: %s", lines[0])
	}
	if strings.Contains(lines[0], "logger.go") {
		t.Errorf("caller 不应指向 logger 包内部 logger.go: %s", lines[0])
	}
}

func TestFromContext_ReturnsStoredLogger(t *testing.T) {
	l := NewNop()
	ctx := NewContext(context.Background(), l)
	if got := FromContext(ctx); got != l {
		t.Errorf("FromContext 应返回 NewContext 放入的 logger")
	}
}

func TestFromContext_FallbackToDefault(t *testing.T) {
	prev := defaultLogger.Load()
	defer defaultLogger.Store(prev)

	l := NewNop()
	SetDefault(l)
	if got := FromContext(context.Background()); got != l {
		t.Errorf("ctx 中无 logger 时应回退到 SetDefault 设置的默认 logger")
	}
}

func TestFromContext_NeverNil(t *testing.T) {
	if got := FromContext(context.Background()); got == nil {
		t.Fatal("FromContext 任何情况下都不应返回 nil")
	}
}

// TestFromContext_NilContext 覆盖 ctx 为 nil 的防御分支
func TestFromContext_NilContext(t *testing.T) {
	if got := FromContext(nil); got == nil {
		t.Fatal("ctx 为 nil 时应返回默认 logger 而非 nil")
	}
}
