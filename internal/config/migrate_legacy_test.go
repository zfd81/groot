package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatalf("写入 %s 失败: %v", name, err)
	}
}

// TestMigrateLegacy_NoLegacyFiles 干净目录上不生成任何文件
func TestMigrateLegacy_NoLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	lb, err := MigrateLegacy(dir)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if lb != nil {
		t.Errorf("无老文件时应返回 nil, got %+v", lb)
	}
	if _, err := os.Stat(filepath.Join(dir, BootstrapFileName)); !os.IsNotExist(err) {
		t.Error("无老文件时不应生成 bootstrap.yaml")
	}
}

// TestMigrateLegacy_AlreadyMigrated bootstrap.yaml 已存在则原样不动
func TestMigrateLegacy_AlreadyMigrated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, BootstrapFileName, "server:\n  port: 7777\n")
	writeFile(t, dir, "config.yaml", "server:\n  port: 9999\n")

	lb, err := MigrateLegacy(dir)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if lb != nil {
		t.Errorf("已迁移过应返回 nil, got %+v", lb)
	}
	data, _ := os.ReadFile(filepath.Join(dir, BootstrapFileName))
	if !strings.Contains(string(data), "7777") {
		t.Errorf("已存在的 bootstrap.yaml 被覆盖: %s", data)
	}
}

// TestMigrateLegacy_BootstrapFields bootstrap 项落入 bootstrap.yaml
func TestMigrateLegacy_BootstrapFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", `
agent:
  name: myagent
server:
  host: 127.0.0.1
  port: 9090
logging:
  level: debug
  file:
    directory: /var/log/groot
    max_age: 30
message:
  queue_size: 512
  workers: 4
schedule:
  max_concurrent_tasks: 8
  sync_interval: 15s
security:
  rate_limit:
    cleanup_interval: 2m
`)
	writeFile(t, dir, "env.yaml", `
database:
  driver: mysql
  dsn: "u:p@tcp(h:3306)/groot"
  max_open_conns: 50
`)

	if _, err := MigrateLegacy(dir); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}

	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Agent.Name != "myagent" {
		t.Errorf("agent.name = %q, want myagent", b.Agent.Name)
	}
	if b.Server.Host != "127.0.0.1" || b.Server.Port != 9090 {
		t.Errorf("server = %+v", b.Server)
	}
	if b.Logging.Level != "debug" || b.Logging.File.Directory != "/var/log/groot" || b.Logging.File.MaxAge != 30 {
		t.Errorf("logging = %+v", b.Logging)
	}
	// 老文件的 message 节已无对应配置，不应带进 bootstrap.yaml
	if data, _ := os.ReadFile(filepath.Join(dir, BootstrapFileName)); strings.Contains(string(data), "message:") {
		t.Errorf("bootstrap.yaml 不应包含 message 节:\n%s", data)
	}
	if b.Schedule.MaxConcurrentTasks != 8 || b.Schedule.SyncInterval != "15s" {
		t.Errorf("schedule = %+v", b.Schedule)
	}
	if b.Security.RateLimit.CleanupInterval != "2m" {
		t.Errorf("cleanup_interval = %q", b.Security.RateLimit.CleanupInterval)
	}
	if b.Database == nil || b.Database.Driver != "mysql" || b.Database.MaxOpenConns != 50 {
		t.Fatalf("database 未从 env.yaml 迁入: %+v", b.Database)
	}
	// 权限 0600：文件不再含密钥，但仍含数据库凭据
	info, err := os.Stat(filepath.Join(dir, BootstrapFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("bootstrap.yaml 权限 = %o, want 0600", perm)
	}
}

// TestMigrateLegacy_BusinessFields 业务项经返回值交出，不写进文件
func TestMigrateLegacy_BusinessFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", `
memory:
  history_window: 50
react:
  max_iterations: 30
  step_timeout: 90
attachment:
  max_size: 200
  allowed_types: [".pdf", ".txt"]
schedule:
  enabled: true
security:
  auth:
    secret: "deadbeef"
    header_name: X-Groot-Key
  rate_limit:
    enabled: true
    default_qps: 25
message:
  senders:
    webhook:
      enabled: true
      url: "https://hook.example.com"
`)

	lb, err := MigrateLegacy(dir)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if lb == nil {
		t.Fatal("应返回待迁入配置表的业务项")
	}
	if lb.Memory.HistoryWindow != 50 || lb.React.MaxIterations != 30 || lb.React.StepTimeout != 90 {
		t.Errorf("memory/react 未交出: %+v %+v", lb.Memory, lb.React)
	}
	if lb.Attachment.MaxSize != 200 || len(lb.Attachment.AllowedTypes) != 2 {
		t.Errorf("attachment 未交出: %+v", lb.Attachment)
	}
	if !lb.ScheduleEnabled {
		t.Error("schedule.enabled=true 未交出")
	}
	if lb.Auth.Secret != "deadbeef" || lb.Auth.HeaderName != "X-Groot-Key" {
		t.Errorf("auth 未交出: %+v", lb.Auth)
	}
	if !lb.RateLimit.Enabled || lb.RateLimit.DefaultQPS != 25 {
		t.Errorf("rate_limit 未交出: %+v", lb.RateLimit)
	}

	// 业务项不得出现在 bootstrap.yaml 中
	data, _ := os.ReadFile(filepath.Join(dir, BootstrapFileName))
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("生成的 bootstrap.yaml 非法: %v", err)
	}
	for _, banned := range []string{"memory", "react", "attachment", "subagent"} {
		if _, ok := raw[banned]; ok {
			t.Errorf("bootstrap.yaml 不应包含业务节 %q", banned)
		}
	}
	if !strings.Contains(string(data), "# 本文件由") {
		t.Error("迁移生成的文件应带说明性头注释")
	}
}

// TestMigrateLegacy_AllCommentedLegacy 全注释的老 config.yaml 迁移后
// 不把零值写进文件，加载结果为全套缺省值
func TestMigrateLegacy_AllCommentedLegacy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "# 全注释\n")
	writeFile(t, dir, "env.yaml", "# 全注释\n")

	if _, err := MigrateLegacy(dir); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Server.Port != 8080 || b.Logging.Level != "info" || b.Schedule.MaxConcurrentTasks != 3 {
		t.Errorf("全注释迁移后应为缺省值: port=%d level=%q max_concurrent=%d",
			b.Server.Port, b.Logging.Level, b.Schedule.MaxConcurrentTasks)
	}
	if b.Database != nil {
		t.Errorf("env.yaml 全注释时 database 应为 nil, got %+v", b.Database)
	}
}

// TestMigrateLegacy_KeepsLegacyFiles 老文件原地保留，不删不改
func TestMigrateLegacy_KeepsLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	const original = "server:\n  port: 9090\n"
	writeFile(t, dir, "config.yaml", original)

	if _, err := MigrateLegacy(dir); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("老 config.yaml 被删除: %v", err)
	}
	if string(data) != original {
		t.Errorf("老 config.yaml 被改动:\n%s", data)
	}
}
