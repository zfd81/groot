package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBootstrap 在 dir 写入 bootstrap.yaml
func writeBootstrap(t *testing.T, dir, content string) {
	t.Helper()
	p := filepath.Join(dir, BootstrapFileName)
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("写入 bootstrap.yaml 失败: %v", err)
	}
}

// TestLoadBootstrap_Missing 文件缺失时提示先运行 init
func TestLoadBootstrap_Missing(t *testing.T) {
	_, err := LoadBootstrap(t.TempDir())
	if err == nil {
		t.Fatal("文件缺失应报错")
	}
	if !strings.Contains(err.Error(), "groot init") {
		t.Errorf("错误信息应提示运行 groot init, got %q", err.Error())
	}
}

// TestLoadBootstrap_AllCommented 全注释文件应等价于全套缺省值
func TestLoadBootstrap_AllCommented(t *testing.T) {
	dir := t.TempDir()
	writeBootstrap(t, dir, "# 全注释\n")

	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Agent.Name != "groot" || b.Agent.Version != "1.0.0" {
		t.Errorf("agent = %+v, want groot/1.0.0", b.Agent)
	}
	if b.Server.Host != "0.0.0.0" || b.Server.Port != 8080 {
		t.Errorf("server = %+v, want 0.0.0.0:8080", b.Server)
	}
	if b.Message.QueueSize != 256 || b.Message.Workers != 2 {
		t.Errorf("message = %+v, want 256/2", b.Message)
	}
	if b.Schedule.MaxConcurrentTasks != 3 || b.Schedule.SyncInterval != "30s" {
		t.Errorf("schedule = %+v, want 3/30s", b.Schedule)
	}
	if b.Security.RateLimit.CleanupInterval != "5m" {
		t.Errorf("cleanup_interval = %q, want 5m", b.Security.RateLimit.CleanupInterval)
	}
	if b.Logging.Level != "info" || b.Logging.Format != "json" {
		t.Errorf("logging = %+v, want info/json", b.Logging)
	}
	if b.Logging.File.Directory != "logs" || b.Logging.File.MaxAge != 7 {
		t.Errorf("logging.file = %+v, want logs/7", b.Logging.File)
	}
	if len(b.Logging.Output) != 2 || b.Logging.Output[0] != "stdout" || b.Logging.Output[1] != "file" {
		t.Errorf("logging.output = %v, want [stdout file]", b.Logging.Output)
	}
	if b.Logging.File.FilenamePattern != "groot-{date}.log" {
		t.Errorf("filename_pattern = %q, want groot-{date}.log", b.Logging.File.FilenamePattern)
	}
	if b.Database != nil {
		t.Errorf("无 database 节时应为 nil, got %+v", b.Database)
	}
}

// TestLoadBootstrap_Values 显式值覆盖缺省值
func TestLoadBootstrap_Values(t *testing.T) {
	dir := t.TempDir()
	writeBootstrap(t, dir, `
server:
  host: 127.0.0.1
  port: 9090
logging:
  level: debug
  file:
    directory: /var/log/groot
message:
  queue_size: 512
schedule:
  sync_interval: 10s
security:
  rate_limit:
    cleanup_interval: 1m
`)

	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Server.Host != "127.0.0.1" || b.Server.Port != 9090 {
		t.Errorf("server = %+v", b.Server)
	}
	if b.Logging.Level != "debug" || b.Logging.File.Directory != "/var/log/groot" {
		t.Errorf("logging = %+v", b.Logging)
	}
	// 同节内未给出的字段仍取缺省值
	if b.Logging.Format != "json" || b.Message.Workers != 2 {
		t.Errorf("同节内缺省值未填充: format=%q workers=%d", b.Logging.Format, b.Message.Workers)
	}
	if b.Message.QueueSize != 512 || b.Schedule.SyncInterval != "10s" {
		t.Errorf("message/schedule = %+v / %+v", b.Message, b.Schedule)
	}
	if b.Security.RateLimit.CleanupInterval != "1m" {
		t.Errorf("cleanup_interval = %q", b.Security.RateLimit.CleanupInterval)
	}
}

// TestLoadBootstrap_DatabaseEnvExpand DSN 中的 ${VAR} 展开为环境变量值
func TestLoadBootstrap_DatabaseEnvExpand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GROOT_TEST_DSN", "user:pw@tcp(h:3306)/groot")
	writeBootstrap(t, dir, `
database:
  driver: mysql
  dsn: ${GROOT_TEST_DSN}
`)

	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Database == nil {
		t.Fatal("database 节应被解析")
	}
	if b.Database.Driver != "mysql" {
		t.Errorf("driver = %q, want mysql", b.Database.Driver)
	}
	if b.Database.DSN != "user:pw@tcp(h:3306)/groot" {
		t.Errorf("DSN 未展开: %q", b.Database.DSN)
	}
}

// TestLoadBootstrap_Malformed 非法 yaml 应报错而非静默取缺省值
func TestLoadBootstrap_Malformed(t *testing.T) {
	dir := t.TempDir()
	writeBootstrap(t, dir, "server:\n  port: [不是数字\n")
	if _, err := LoadBootstrap(dir); err == nil {
		t.Fatal("非法 yaml 应报错")
	}
}
