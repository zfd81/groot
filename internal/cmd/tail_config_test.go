// internal/cmd/tail_config_test.go
// tail 命令读取日志目录配置的回落顺序：bootstrap.yaml 优先，
// 缺失时回落老 config.yaml，都没有时使用代码默认值。
package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTailLoadConfig_BootstrapFirst bootstrap.yaml 优先于老 config.yaml
func TestTailLoadConfig_BootstrapFirst(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "bootstrap.yaml"), []byte("logging:\n  file:\n    directory: /var/log/new\n"), 0600); err != nil {
		t.Fatalf("写入 bootstrap.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("logging:\n  file:\n    directory: /var/log/old\n"), 0600); err != nil {
		t.Fatalf("写入 config.yaml: %v", err)
	}

	cfg, err := loadConfig(home)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Logging.File.Directory != "/var/log/new" {
		t.Errorf("directory = %q, want /var/log/new（bootstrap 应优先）", cfg.Logging.File.Directory)
	}
}

// TestTailLoadConfig_LegacyFallback bootstrap 缺失时回落老 config.yaml
func TestTailLoadConfig_LegacyFallback(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("logging:\n  file:\n    directory: /var/log/old\n"), 0600); err != nil {
		t.Fatalf("写入 config.yaml: %v", err)
	}

	cfg, err := loadConfig(home)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Logging.File.Directory != "/var/log/old" {
		t.Errorf("directory = %q, want /var/log/old", cfg.Logging.File.Directory)
	}
}

// TestTailLoadConfig_NoFiles 都没有时返回默认日志目录
func TestTailLoadConfig_NoFiles(t *testing.T) {
	cfg, err := loadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Logging.File.Directory != "logs" {
		t.Errorf("directory = %q, want logs", cfg.Logging.File.Directory)
	}
}
