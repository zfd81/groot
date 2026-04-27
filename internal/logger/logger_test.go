package logger

import (
	"testing"

	"github.com/zfd81/groot/internal/config"
	"go.uber.org/zap/zapcore"
)

func TestGetLevel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected zapcore.Level
	}{
		{
			name:     "debug level",
			input:    "debug",
			expected: zapcore.DebugLevel,
		},
		{
			name:     "info level",
			input:    "info",
			expected: zapcore.InfoLevel,
		},
		{
			name:     "warn level",
			input:    "warn",
			expected: zapcore.WarnLevel,
		},
		{
			name:     "error level",
			input:    "error",
			expected: zapcore.ErrorLevel,
		},
		{
			name:     "uppercase DEBUG",
			input:    "DEBUG",
			expected: zapcore.DebugLevel,
		},
		{
			name:     "mixed case Info",
			input:    "Info",
			expected: zapcore.InfoLevel,
		},
		{
			name:     "unknown level defaults to info",
			input:    "unknown",
			expected: zapcore.InfoLevel,
		},
		{
			name:     "empty defaults to info",
			input:    "",
			expected: zapcore.InfoLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getLevel(tt.input)
			if result != tt.expected {
				t.Errorf("getLevel(%s) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		item     string
		expected bool
	}{
		{
			name:     "item found",
			slice:    []string{"stdout", "file"},
			item:     "stdout",
			expected: true,
		},
		{
			name:     "item not found",
			slice:    []string{"stdout", "file"},
			item:     "stderr",
			expected: false,
		},
		{
			name:     "empty slice",
			slice:    []string{},
			item:     "stdout",
			expected: false,
		},
		{
			name:     "nil slice",
			slice:    nil,
			item:     "stdout",
			expected: false,
		},
		{
			name:     "exact match",
			slice:    []string{"stdout"},
			item:     "stdout",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contains(tt.slice, tt.item)
			if result != tt.expected {
				t.Errorf("contains(%v, %s) = %v, want %v", tt.slice, tt.item, result, tt.expected)
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	cfg := config.LoggingConfig{
		Level:  "debug",
		Format: "json",
		Output: []string{"stdout"},
	}

	log := New(cfg)
	if log == nil {
		t.Fatal("New() returned nil")
	}

	// 测试基本日志方法
	log.Info("test info message")
	log.Debug("test debug message")
	log.Error("test error message")

	// Sync for stdout may fail on some systems, ignore the error
	_ = log.Sync()
}

func TestNewLogger_ConsoleFormat(t *testing.T) {
	cfg := config.LoggingConfig{
		Level:  "info",
		Format: "console",
		Output: []string{"stdout"},
	}

	log := New(cfg)
	if log == nil {
		t.Fatal("New() returned nil for console format")
	}

	log.Info("console format test")
}

func TestNewLogger_InvalidLevel(t *testing.T) {
	cfg := config.LoggingConfig{
		Level:  "invalid_level",
		Format: "json",
		Output: []string{"stdout"},
	}

	log := New(cfg)
	if log == nil {
		t.Fatal("New() returned nil for invalid level")
	}

	// 无效级别应默认为 info
	log.Info("test with invalid level defaults to info")
}

func TestLogEvent(t *testing.T) {
	cfg := config.LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: []string{"stdout"},
	}

	log := New(cfg)

	data := map[string]string{"key": "value"}
	log.LogEvent("test_event", data)
}

func TestLogRequest(t *testing.T) {
	cfg := config.LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: []string{"stdout"},
	}

	log := New(cfg)

	log.LogRequest("GET", "/api/chat", "task_001", "user_001")
}

func TestLogSkillHotReload(t *testing.T) {
	cfg := config.LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: []string{"stdout"},
	}

	log := New(cfg)

	log.LogSkillHotReload("loaded", "test_skill", 5)
}

func TestLogMCPHotReload(t *testing.T) {
	cfg := config.LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: []string{"stdout"},
	}

	log := New(cfg)

	log.LogMCPHotReload("loaded", "test_mcp", "stdio", 3)
}

func TestRotatingWriter_Write(t *testing.T) {
	tmpDir := t.TempDir()

	writer := &rotatingWriter{
		directory:       tmpDir,
		filenamePattern: "test-{date}.log",
		currentDate:     "",
	}

	// 第一次写入应创建文件
	data := []byte("test log message\n")
	n, err := writer.Write(data)
	if err != nil {
		t.Fatalf("Write() failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("Write() returned %d bytes, want %d", n, len(data))
	}

	// 第二次写入应追加到同一文件
	n, err = writer.Write([]byte("second message\n"))
	if err != nil {
		t.Fatalf("Second Write() failed: %v", err)
	}
	if n != 15 {
		t.Errorf("Second Write() returned %d bytes, want 15", n)
	}

	// 验证文件存在
	if writer.currentFile == nil {
		t.Error("currentFile should not be nil after Write")
	}
}

func TestRotatingWriter_MultipleWrites(t *testing.T) {
	tmpDir := t.TempDir()

	writer := &rotatingWriter{
		directory:       tmpDir,
		filenamePattern: "app-{date}.log",
	}

	// 连续多次写入
	for i := 0; i < 5; i++ {
		_, err := writer.Write([]byte("message\n"))
		if err != nil {
			t.Errorf("Write() iteration %d failed: %v", i, err)
		}
	}

	// 验证 currentDate 已设置
	if writer.currentDate == "" {
		t.Error("currentDate should be set after writes")
	}
}