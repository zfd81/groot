// internal/setting/runtime_test.go
package setting

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/zfd81/groot/internal/config"
)

// staticCfg 构造一份 bootstrap 静态层：只承载不进表的构造参数，
// 业务项的基准层是代码默认值。
func staticCfg() config.Bootstrap {
	return config.Bootstrap{
		Schedule: config.ScheduleBootstrap{
			MaxConcurrentTasks: 5, SyncInterval: "30s",
		},
		Security: config.SecurityBootstrap{
			RateLimit: config.RateLimitBootstrap{CleanupInterval: "5m"},
		},
	}
}

// 表为空时各分类都应返回代码默认值，bootstrap 只补齐不进表的构造参数。
func TestRuntime_EmptyTableUsesDefaults(t *testing.T) {
	s := New(staticCfg(), newFakeRepo())
	got, err := s.Runtime(context.Background())
	if err != nil {
		t.Fatalf("Runtime: %v", err)
	}
	if got.Memory != defaultMemory() {
		t.Errorf("Memory = %+v, want 默认值 %+v", got.Memory, defaultMemory())
	}
	if got.React != defaultReact() {
		t.Errorf("React = %+v, want 默认值", got.React)
	}
	if got.SubAgent != defaultSubAgent() {
		t.Errorf("SubAgent = %+v, want 默认值", got.SubAgent)
	}
	if got.Attachment.MaxSize != 50 || len(got.Attachment.AllowedTypes) != 0 {
		t.Errorf("Attachment = %+v, want 默认值", got.Attachment)
	}
	wantRL := defaultRateLimit()
	wantRL.CleanupInterval = "5m" // 回收周期来自 bootstrap
	if got.RateLimit != wantRL {
		t.Errorf("RateLimit = %+v, want %+v", got.RateLimit, wantRL)
	}
	wantSched := config.ScheduleConfig{Enabled: false, MaxConcurrentTasks: 5, SyncInterval: "30s"}
	if got.Schedule != wantSched {
		t.Errorf("Schedule = %+v, want %+v（Enabled 默认关闭，其余来自 bootstrap）", got.Schedule, wantSched)
	}
}

// 表中存在的键覆盖默认值，缺失的键保持默认值。
func TestRuntime_TableOverridesDefaults(t *testing.T) {
	f := newFakeRepo()
	s := New(staticCfg(), f)
	ctx := context.Background()

	// 只改两项，其余保持默认值
	f.data[key("global", "", KeyReactMaxIterations)] = "42"
	f.data[key("global", "", KeyAttachmentAllowedTypes)] = `["image/jpeg","application/pdf"]`

	got, err := s.Runtime(ctx)
	if err != nil {
		t.Fatalf("Runtime: %v", err)
	}
	if got.React.MaxIterations != 42 {
		t.Errorf("MaxIterations = %d, want 42", got.React.MaxIterations)
	}
	if got.React.StepTimeout != 60 {
		t.Errorf("StepTimeout = %d, want 60（表中无此键应保持默认值）", got.React.StepTimeout)
	}
	want := []string{"image/jpeg", "application/pdf"}
	if len(got.Attachment.AllowedTypes) != 2 ||
		got.Attachment.AllowedTypes[0] != want[0] || got.Attachment.AllowedTypes[1] != want[1] {
		t.Errorf("AllowedTypes = %v, want %v", got.Attachment.AllowedTypes, want)
	}
}

// 脏数据不使整次取值失败，单项退回默认值。
func TestRuntime_DirtyValueFallsBack(t *testing.T) {
	f := newFakeRepo()
	s := New(staticCfg(), f)
	f.data[key("global", "", KeyReactMaxIterations)] = "abc"
	f.data[key("global", "", KeySubAgentExecTimeout)] = "5 分钟"
	f.data[key("global", "", KeyAttachmentAllowedTypes)] = "not-json"
	f.data[key("global", "", KeyScheduleEnabled)] = "maybe"

	got, err := s.Runtime(context.Background())
	if err != nil {
		t.Fatalf("Runtime 不应因脏数据失败: %v", err)
	}
	if got.React.MaxIterations != 20 {
		t.Errorf("MaxIterations = %d, want 默认值 20", got.React.MaxIterations)
	}
	if got.SubAgent.ExecTimeout != "5m" {
		t.Errorf("ExecTimeout = %q, want 默认值 \"5m\"", got.SubAgent.ExecTimeout)
	}
	if len(got.Attachment.AllowedTypes) != 0 {
		t.Errorf("AllowedTypes = %v, want 保持默认的空", got.Attachment.AllowedTypes)
	}
	if got.Schedule.Enabled {
		t.Error("Schedule.Enabled 脏数据应退回默认值 false")
	}
}

// 保存后读回应当一致，全部字段都持久化到配置表。
func TestSetRuntime_RoundTrip(t *testing.T) {
	f := newFakeRepo()
	s := New(staticCfg(), f)
	ctx := context.Background()

	in := RuntimeSettings{
		Memory:   config.MemoryConfig{HistoryWindow: -1},
		React:    config.ReactConfig{MaxIterations: 30, StepTimeout: 120, ErrorRetry: 0},
		SubAgent: config.SubAgentConfig{MaxConcurrency: 8, ExecTimeout: "10m", MaxTaskLength: 100, MaxResultLength: 200},
		Attachment: config.AttachmentConfig{
			MaxSize: 20, MaxTotalSize: 60, MaxCount: 5, AllowedTypes: nil,
		},
		RateLimit: config.RateLimitConfig{
			Enabled: false, GlobalQPS: 12.5, GlobalConcurrency: 7, DefaultQPS: 0.5, DefaultConcurrency: 0,
		},
	}
	if err := s.SetRuntime(ctx, in); err != nil {
		t.Fatalf("SetRuntime: %v", err)
	}

	got, err := s.Runtime(ctx)
	if err != nil {
		t.Fatalf("Runtime: %v", err)
	}
	if got.Memory.HistoryWindow != -1 {
		t.Errorf("HistoryWindow = %d, want -1", got.Memory.HistoryWindow)
	}
	if got.React.MaxIterations != 30 || got.React.ErrorRetry != 0 {
		t.Errorf("React = %+v, want 写入值", got.React)
	}
	if got.SubAgent.ExecTimeout != "10m" {
		t.Errorf("ExecTimeout = %q, want \"10m\"", got.SubAgent.ExecTimeout)
	}
	// nil 切片写入后读回为空数组，语义是「不限制类型」
	if len(got.Attachment.AllowedTypes) != 0 {
		t.Errorf("AllowedTypes = %v, want 空", got.Attachment.AllowedTypes)
	}
	if got.SubAgent.MaxConcurrency != 8 {
		t.Errorf("MaxConcurrency = %d, want 8", got.SubAgent.MaxConcurrency)
	}
	if v := f.data[key("global", "", "subagent.max_concurrency")]; v != "8" {
		t.Errorf("表内 subagent.max_concurrency = %q, want \"8\"", v)
	}
	rl := got.RateLimit
	if rl.Enabled != false {
		t.Errorf("RateLimit.Enabled = %v, want false", rl.Enabled)
	}
	if rl.GlobalQPS != 12.5 {
		t.Errorf("RateLimit.GlobalQPS = %v, want 12.5", rl.GlobalQPS)
	}
	if rl.GlobalConcurrency != 7 {
		t.Errorf("RateLimit.GlobalConcurrency = %d, want 7", rl.GlobalConcurrency)
	}
	if rl.DefaultQPS != 0.5 {
		t.Errorf("RateLimit.DefaultQPS = %v, want 0.5", rl.DefaultQPS)
	}
	if rl.DefaultConcurrency != 0 {
		t.Errorf("RateLimit.DefaultConcurrency = %d, want 0", rl.DefaultConcurrency)
	}
	// CleanupInterval 不进表，应保持 bootstrap.yaml 值
	if rl.CleanupInterval != "5m" {
		t.Errorf("RateLimit.CleanupInterval = %q, want \"5m\"", rl.CleanupInterval)
	}
	if v := f.data[key("global", "", "security.rate_limit.global_qps")]; v != "12.5" {
		t.Errorf("表内 security.rate_limit.global_qps = %q, want \"12.5\"", v)
	}
}

// validCfg 返回一份全部字段合法的运行时配置，供越界用例逐项破坏。
func validCfg() RuntimeSettings {
	return RuntimeSettings{
		Memory:     config.MemoryConfig{HistoryWindow: 10},
		React:      config.ReactConfig{MaxIterations: 20, StepTimeout: 300, ErrorRetry: 2},
		SubAgent:   config.SubAgentConfig{MaxConcurrency: 3, ExecTimeout: "5m", MaxTaskLength: 5000, MaxResultLength: 8000},
		Attachment: config.AttachmentConfig{MaxSize: 50, MaxTotalSize: 100, MaxCount: 10},
		RateLimit:  config.RateLimitConfig{Enabled: true, DefaultQPS: 100, DefaultConcurrency: 10},
		Schedule:   config.ScheduleConfig{Enabled: false},
	}
}

func TestValidate_Accepts(t *testing.T) {
	if err := validCfg().Validate(); err != nil {
		t.Fatalf("合法配置被拒绝: %v", err)
	}
	// HistoryWindow 为 -1 表示不限制，属合法取值
	r := validCfg()
	r.Memory.HistoryWindow = MinHistoryWindow
	if err := r.Validate(); err != nil {
		t.Errorf("HistoryWindow=-1 应被接受: %v", err)
	}
}

func TestValidate_Rejects(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*RuntimeSettings)
	}{
		{"history_window 低于 -1", func(r *RuntimeSettings) { r.Memory.HistoryWindow = -2 }},
		{"history_window 过大", func(r *RuntimeSettings) { r.Memory.HistoryWindow = MaxHistoryWindow + 1 }},
		{"max_iterations 为 0 会使对话立刻终止", func(r *RuntimeSettings) { r.React.MaxIterations = 0 }},
		{"step_timeout 为 0 会使每步即刻取消", func(r *RuntimeSettings) { r.React.StepTimeout = 0 }},
		{"error_retry 为负", func(r *RuntimeSettings) { r.React.ErrorRetry = -1 }},
		{"max_concurrency 为 0 会使子 Agent 永远排队", func(r *RuntimeSettings) { r.SubAgent.MaxConcurrency = 0 }},
		{"max_concurrency 过大", func(r *RuntimeSettings) { r.SubAgent.MaxConcurrency = MaxSubAgentMaxConcurrency + 1 }},
		{"exec_timeout 不是时长", func(r *RuntimeSettings) { r.SubAgent.ExecTimeout = "5 分钟" }},
		{"exec_timeout 过短", func(r *RuntimeSettings) { r.SubAgent.ExecTimeout = "10ms" }},
		{"exec_timeout 过长", func(r *RuntimeSettings) { r.SubAgent.ExecTimeout = "48h" }},
		{"max_task_length 为 0", func(r *RuntimeSettings) { r.SubAgent.MaxTaskLength = 0 }},
		{"max_result_length 过大", func(r *RuntimeSettings) { r.SubAgent.MaxResultLength = MaxSubAgentTextLength + 1 }},
		{"attachment max_size 为 0", func(r *RuntimeSettings) { r.Attachment.MaxSize = 0 }},
		{"max_count 为 0", func(r *RuntimeSettings) { r.Attachment.MaxCount = 0 }},
		{"总量小于单个上限", func(r *RuntimeSettings) { r.Attachment.MaxSize = 80; r.Attachment.MaxTotalSize = 60 }},
		{"allowed_types 含空白项", func(r *RuntimeSettings) { r.Attachment.AllowedTypes = []string{"image/png", "  "} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validCfg()
			c.mut(&r)
			err := r.Validate()
			if err == nil {
				t.Fatal("应当被拒绝，却通过了校验")
			}
			if !errors.Is(err, ErrInvalidSetting) {
				t.Errorf("错误未包装 ErrInvalidSetting: %v", err)
			}
		})
	}
}

// 校验不通过时不应写入任何一行，避免半套配置落库。
func TestSetRuntime_InvalidWritesNothing(t *testing.T) {
	f := newFakeRepo()
	s := New(staticCfg(), f)
	r := validCfg()
	r.React.MaxIterations = 0

	err := s.SetRuntime(context.Background(), r)
	if !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("err = %v, want ErrInvalidSetting", err)
	}
	if len(f.data) != 0 {
		t.Errorf("表中写入了 %d 行，应为 0 行", len(f.data))
	}
}

// 未装配配置表时读取返回代码默认值（加 bootstrap 构造参数），写入返回 ErrNoSettingStore。
func TestRuntime_NoStore(t *testing.T) {
	s := New(staticCfg(), nil)
	got, err := s.Runtime(context.Background())
	if err != nil {
		t.Fatalf("Runtime: %v", err)
	}
	if got.React.MaxIterations != 20 {
		t.Errorf("MaxIterations = %d, want 20", got.React.MaxIterations)
	}
	if err := s.SetRuntime(context.Background(), validCfg()); !errors.Is(err, ErrNoSettingStore) {
		t.Errorf("err = %v, want ErrNoSettingStore", err)
	}
}

// TestRuntimeSettings_ValidateRateLimit 验证限流字段的边界。
func TestRuntimeSettings_ValidateRateLimit(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RuntimeSettings)
		wantErr bool
	}{
		{"合法", func(r *RuntimeSettings) {}, false},
		{"QPS 为零表示不限速", func(r *RuntimeSettings) { r.RateLimit.DefaultQPS = 0 }, false},
		{"QPS 为负", func(r *RuntimeSettings) { r.RateLimit.DefaultQPS = -1 }, true},
		{"QPS 超上限", func(r *RuntimeSettings) { r.RateLimit.DefaultQPS = MaxRateLimitQPS + 1 }, true},
		{"并发为零表示不限并发", func(r *RuntimeSettings) { r.RateLimit.DefaultConcurrency = 0 }, false},
		{"并发为负", func(r *RuntimeSettings) { r.RateLimit.DefaultConcurrency = -1 }, true},
		{"并发超上限", func(r *RuntimeSettings) {
			r.RateLimit.DefaultConcurrency = MaxRateLimitConcurrency + 1
		}, true},
		{"全局 QPS 为负", func(r *RuntimeSettings) { r.RateLimit.GlobalQPS = -1 }, true},
		{"全局并发超上限", func(r *RuntimeSettings) {
			r.RateLimit.GlobalConcurrency = MaxRateLimitConcurrency + 1
		}, true},
		{"全局并发为负", func(r *RuntimeSettings) { r.RateLimit.GlobalConcurrency = -1 }, true},
		{"全局 QPS 超上限", func(r *RuntimeSettings) { r.RateLimit.GlobalQPS = MaxRateLimitQPS + 1 }, true},
		{"QPS 为 NaN", func(r *RuntimeSettings) { r.RateLimit.DefaultQPS = math.NaN() }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validCfg()
			c.mutate(&r)
			err := r.Validate()
			if c.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if c.wantErr && !errors.Is(err, ErrInvalidSetting) {
				t.Errorf("error should wrap ErrInvalidSetting, got %v", err)
			}
		})
	}
}

// TestRateLimitFrom_FallsBackToBase 验证表内缺键时保持基准值，脏数据退回基准值。
func TestRateLimitFrom_FallsBackToBase(t *testing.T) {
	base := config.RateLimitConfig{
		Enabled: true, GlobalQPS: 100, GlobalConcurrency: 50,
		DefaultQPS: 10, DefaultConcurrency: 5, CleanupInterval: "5m",
	}

	got := rateLimitFrom(base, map[string]string{})
	if got != base {
		t.Errorf("空表应保持基准值，得到 %+v", got)
	}

	got = rateLimitFrom(base, map[string]string{
		KeyRateLimitEnabled:    "false",
		KeyRateLimitDefaultQPS: "不是数字",
		KeyRateLimitGlobalQPS:  "NaN",
	})
	if got.Enabled {
		t.Error("Enabled 应被表内的 false 覆盖")
	}
	if got.DefaultQPS != 10 {
		t.Errorf("DefaultQPS = %v, 脏数据应退回基准值 10", got.DefaultQPS)
	}
	if got.GlobalQPS != 100 {
		t.Errorf("GlobalQPS = %v, NaN 应退回基准值 100", got.GlobalQPS)
	}
	if got.CleanupInterval != "5m" {
		t.Errorf("CleanupInterval 不在表内，应保持 %q", base.CleanupInterval)
	}
}

// TestRuntimeSettings_RowsIncludeRateLimit 验证限流五键随整体保存写入。
func TestRuntimeSettings_RowsIncludeRateLimit(t *testing.T) {
	r := validCfg()
	r.RateLimit.DefaultQPS = 12.5
	r.RateLimit.Enabled = true

	found := map[string]string{}
	for _, row := range r.rows() {
		found[row.Name] = row.Value
	}
	if found[KeyRateLimitEnabled] != "true" {
		t.Errorf("%s = %q, want true", KeyRateLimitEnabled, found[KeyRateLimitEnabled])
	}
	if found[KeyRateLimitDefaultQPS] != "12.5" {
		t.Errorf("%s = %q, want 12.5", KeyRateLimitDefaultQPS, found[KeyRateLimitDefaultQPS])
	}
	for _, k := range []string{
		KeyRateLimitGlobalQPS, KeyRateLimitGlobalConcurrency, KeyRateLimitDefaultConcurrency,
	} {
		if _, ok := found[k]; !ok {
			t.Errorf("缺少键 %s", k)
		}
	}
	if _, ok := found["security.rate_limit.cleanup_interval"]; ok {
		t.Error("cleanup_interval 留在 YAML，不应写表")
	}
}

// TestRuntime_ScheduleEnabledRoundTrip 验证调度开关经配置表往返后保持，
// 尤其是开启：基准值是代码默认的 false，true 必须能写进表并读回。
func TestRuntime_ScheduleEnabledRoundTrip(t *testing.T) {
	s := New(staticCfg(), newFakeRepo())
	ctx := context.Background()

	cur, err := s.Runtime(ctx)
	if err != nil {
		t.Fatalf("Runtime: %v", err)
	}
	if cur.Schedule.Enabled {
		t.Fatalf("表为空时应回落到默认的 false，得到 %v", cur.Schedule.Enabled)
	}

	cur.Schedule.Enabled = true
	if err := s.SetRuntime(ctx, cur); err != nil {
		t.Fatalf("SetRuntime 打开: %v", err)
	}
	got, err := s.Runtime(ctx)
	if err != nil {
		t.Fatalf("Runtime 回读: %v", err)
	}
	if !got.Schedule.Enabled {
		t.Error("schedule.enabled 应已打开：true 必须能写进表")
	}

	got.Schedule.Enabled = false
	if err := s.SetRuntime(ctx, got); err != nil {
		t.Fatalf("SetRuntime 关闭: %v", err)
	}
	again, err := s.Runtime(ctx)
	if err != nil {
		t.Fatalf("Runtime 二次回读: %v", err)
	}
	if again.Schedule.Enabled {
		t.Error("schedule.enabled 应已关闭：false 必须能写进表")
	}

	// Schedule() 单独读取也应看到表值：它是 mcp 门控每次求值时调用的方法
	got.Schedule.Enabled = true
	if err := s.SetRuntime(ctx, got); err != nil {
		t.Fatalf("SetRuntime 再次打开: %v", err)
	}
	sc, err := s.Schedule(ctx)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if !sc.Enabled || sc.MaxConcurrentTasks != 5 || sc.SyncInterval != "30s" {
		t.Errorf("Schedule() = %+v, want Enabled=true 且其余两项保持 bootstrap 值", sc)
	}
}

// TestNormalizeAllowedTypes 验证写入侧归一化：去空白、去前导点、转小写、丢空项。
func TestNormalizeAllowedTypes(t *testing.T) {
	got := normalizeAllowedTypes([]string{" .PNG ", "PDF", ".", "", ".tar.gz"})
	want := []string{"png", "pdf", "tar.gz"}
	if len(got) != len(want) {
		t.Fatalf("归一化结果 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("结果[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestNormalizeAllowedTypes_NilStaysNil 验证 nil 保持 nil，
// 交由 encodeAllowedTypes 编码为 "[]"（语义：不限制类型）。
func TestNormalizeAllowedTypes_NilStaysNil(t *testing.T) {
	if got := normalizeAllowedTypes(nil); got != nil {
		t.Errorf("normalizeAllowedTypes(nil) = %v, want nil", got)
	}
}

// TestValidateRuntime_RejectsDotOnlyAllowedType 验证只含点号的白名单项在保存
// 环节就被拒绝，不会落库后归一化为空再被丢弃。
func TestValidateRuntime_RejectsDotOnlyAllowedType(t *testing.T) {
	for _, bad := range []string{".", "..", " . "} {
		rt := validCfg()
		rt.Attachment.AllowedTypes = []string{bad}
		err := rt.Validate()
		if err == nil {
			t.Errorf("白名单项 %q 应被拒绝，实际通过校验", bad)
			continue
		}
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("白名单项 %q 的错误应包装 ErrInvalidSetting，实际 %v", bad, err)
		}
	}
}

// TestValidateRuntime_AcceptsDottedAllowedType 验证带点的正常扩展名（UI 提示
// 的格式）仍然通过校验，收紧校验没有误伤。
func TestValidateRuntime_AcceptsDottedAllowedType(t *testing.T) {
	rt := validCfg()
	rt.Attachment.AllowedTypes = []string{".png", "pdf", ".tar.gz"}
	if err := rt.Validate(); err != nil {
		t.Errorf("带点扩展名应通过校验，实际报错: %v", err)
	}
}

// TestNormalizeAllowedTypes_Dedup 验证归一化后去重且保持首次出现的顺序。
// ".PNG" 与 "png" 归一化后是同一扩展名，重复入库会让界面回读显示两个相同条目，
// 并多占 MaxAttachmentTypeCount 的额度。
func TestNormalizeAllowedTypes_Dedup(t *testing.T) {
	got := normalizeAllowedTypes([]string{".PNG", "png", " .Png ", "pdf", ".pdf", "txt"})
	want := []string{"png", "pdf", "txt"}
	if len(got) != len(want) {
		t.Fatalf("normalizeAllowedTypes 去重后 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项 = %q, want %q（应保持首次出现的顺序）", i, got[i], want[i])
		}
	}
}
