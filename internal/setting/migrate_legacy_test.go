// internal/setting/migrate_legacy_test.go
package setting

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// TestImportLegacy_WritesNonZero 非零业务项写入表，随后可读回
func TestImportLegacy_WritesNonZero(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	lb := &config.LegacyBusiness{
		Memory: config.MemoryConfig{HistoryWindow: 50},
		React:  config.ReactConfig{MaxIterations: 30},
		Auth:   config.AuthConfig{Secret: "deadbeef", HeaderName: "X-Groot-Key"},
		RateLimit: config.RateLimitConfig{
			Enabled:    true,
			DefaultQPS: 25,
		},
		ScheduleEnabled: true,
		Senders: map[string]config.SenderConf{
			"webhook": {Enabled: true, URL: "https://hook.example.com"},
		},
	}
	if err := s.ImportLegacy(ctx, lb); err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}

	mem, _ := s.Memory(ctx)
	if mem.HistoryWindow != 50 {
		t.Errorf("history_window = %d, want 50", mem.HistoryWindow)
	}
	r, _ := s.React(ctx)
	if r.MaxIterations != 30 {
		t.Errorf("max_iterations = %d, want 30", r.MaxIterations)
	}
	// react 其余字段未设置，保持默认值
	if r.StepTimeout != 60 {
		t.Errorf("step_timeout = %d, want 默认 60", r.StepTimeout)
	}
	a, _ := s.Auth(ctx)
	if a.Secret != "deadbeef" || a.HeaderName != "X-Groot-Key" {
		t.Errorf("auth = %+v", a)
	}
	sec, _ := s.Security(ctx)
	if !sec.RateLimit.Enabled || sec.RateLimit.DefaultQPS != 25 {
		t.Errorf("rate_limit = %+v", sec.RateLimit)
	}
	sch, _ := s.Schedule(ctx)
	if !sch.Enabled {
		t.Error("schedule.enabled 未迁入")
	}
	msg, _ := s.Message(ctx)
	if w := msg.Senders["webhook"]; !w.Enabled || w.URL != "https://hook.example.com" {
		t.Errorf("webhook = %+v", w)
	}
}

// TestImportLegacy_DoesNotOverwrite 表内已有值不被老 YAML 覆盖
func TestImportLegacy_DoesNotOverwrite(t *testing.T) {
	r := newFakeRepo()
	s := New(config.Bootstrap{}, r)
	ctx := context.Background()

	// 使用者已在界面上把窗口改成 100
	if err := r.Upsert(ctx, &repo.Setting{
		Scope: repo.ScopeGlobal, Name: KeyMemoryHistoryWindow, Value: "100",
	}); err != nil {
		t.Fatalf("预置表值: %v", err)
	}

	lb := &config.LegacyBusiness{Memory: config.MemoryConfig{HistoryWindow: 50}}
	if err := s.ImportLegacy(ctx, lb); err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}

	mem, _ := s.Memory(ctx)
	if mem.HistoryWindow != 100 {
		t.Errorf("表内已有值被覆盖: %d, want 100", mem.HistoryWindow)
	}
}

// TestImportLegacy_SkipsZero 零值不写表（老模板全注释的常态）
func TestImportLegacy_SkipsZero(t *testing.T) {
	r := newFakeRepo()
	s := New(config.Bootstrap{}, r)
	ctx := context.Background()

	if err := s.ImportLegacy(ctx, &config.LegacyBusiness{}); err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}
	items, err := r.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("零值不应写表, got %d 行: %+v", len(items), items)
	}
}

// TestImportLegacy_Nil nil 输入是显式的空操作
func TestImportLegacy_Nil(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	if err := s.ImportLegacy(context.Background(), nil); err != nil {
		t.Errorf("nil 输入应为空操作, got %v", err)
	}
}

// TestImportLegacy_EndToEndFromLegacyFile 打通迁移的两段衔接：
// 老 config.yaml 文件 → config.MigrateLegacy 解析出 LegacyBusiness →
// setting.ImportLegacy 写配置表 → Settings 公开方法逐分类读回。
// 目的是锁住「config 侧解析 → setting 侧写表」的键名与字段映射，
// 防止将来新增字段时两侧脱节。
func TestImportLegacy_EndToEndFromLegacyFile(t *testing.T) {
	dir := t.TempDir()
	// 全部业务项均设为非默认值，任一侧漏掉映射都会在读回时暴露
	legacy := `
memory:
  history_window: 50
react:
  max_iterations: 30
  step_timeout: 90
  error_retry: 5
subagent:
  max_concurrency: 7
  exec_timeout: 9m
  max_task_length: 1234
  max_result_length: 5678
attachment:
  max_size: 200
  max_total_size: 500
  max_count: 9
  allowed_types: [".pdf", ".txt"]
schedule:
  enabled: true
security:
  auth:
    secret: "deadbeef"
    header_name: X-Groot-Key
  rate_limit:
    enabled: true
    global_qps: 12.5
    global_concurrency: 40
    default_qps: 25
    default_concurrency: 8
message:
  senders:
    webhook:
      enabled: true
      url: "https://hook.example.com"
    email:
      enabled: true
      smtp_host: smtp.example.com
      smtp_port: 2525
      username: bot
      password: s3cret
      from: bot@example.com
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(legacy), 0600); err != nil {
		t.Fatalf("写入老 config.yaml: %v", err)
	}

	lb, err := config.MigrateLegacy(dir)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if lb == nil {
		t.Fatal("MigrateLegacy 应返回待写表的业务项")
	}

	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()
	if err := s.ImportLegacy(ctx, lb); err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}

	// 经 Settings 公开方法逐分类读回断言
	mem, err := s.Memory(ctx)
	if err != nil {
		t.Fatalf("Memory: %v", err)
	}
	if mem.HistoryWindow != 50 {
		t.Errorf("memory.history_window = %d, want 50", mem.HistoryWindow)
	}

	r, err := s.React(ctx)
	if err != nil {
		t.Fatalf("React: %v", err)
	}
	if r.MaxIterations != 30 || r.StepTimeout != 90 || r.ErrorRetry != 5 {
		t.Errorf("react = %+v, want {30 90 5}", r)
	}

	sa, err := s.SubAgent(ctx)
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	if sa.MaxConcurrency != 7 || sa.ExecTimeout != "9m" ||
		sa.MaxTaskLength != 1234 || sa.MaxResultLength != 5678 {
		t.Errorf("subagent = %+v", sa)
	}

	a, err := s.Attachment(ctx)
	if err != nil {
		t.Fatalf("Attachment: %v", err)
	}
	if a.MaxSize != 200 || a.MaxTotalSize != 500 || a.MaxCount != 9 {
		t.Errorf("attachment = %+v", a)
	}
	if len(a.AllowedTypes) != 2 || a.AllowedTypes[0] != ".pdf" || a.AllowedTypes[1] != ".txt" {
		t.Errorf("attachment.allowed_types = %v, want [.pdf .txt]", a.AllowedTypes)
	}

	sec, err := s.Security(ctx)
	if err != nil {
		t.Fatalf("Security: %v", err)
	}
	rl := sec.RateLimit
	if !rl.Enabled || rl.GlobalQPS != 12.5 || rl.GlobalConcurrency != 40 ||
		rl.DefaultQPS != 25 || rl.DefaultConcurrency != 8 {
		t.Errorf("rate_limit = %+v", rl)
	}
	if sec.Auth.Secret != "deadbeef" || sec.Auth.HeaderName != "X-Groot-Key" {
		t.Errorf("auth = %+v", sec.Auth)
	}

	sch, err := s.Schedule(ctx)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if !sch.Enabled {
		t.Error("schedule.enabled 未迁入")
	}

	msg, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	w := msg.Senders[SenderWebhook]
	if !w.Enabled || w.URL != "https://hook.example.com" {
		t.Errorf("webhook = %+v", w)
	}
	e := msg.Senders[SenderEmail]
	if !e.Enabled || e.SMTPHost != "smtp.example.com" || e.SMTPPort != 2525 ||
		e.Username != "bot" || e.Password != "s3cret" || e.From != "bot@example.com" {
		t.Errorf("email = %+v", e)
	}
}
