package setting

import "testing"

// TestDefaults 断言各分类的代码默认值。数值取自迁移前 config.applyDefaults
// 中的同名项，保证未在表中设置过任何值的部署行为不变。
func TestDefaults(t *testing.T) {
	if got := defaultMemory(); got.HistoryWindow != 20 {
		t.Errorf("memory.history_window = %d, want 20", got.HistoryWindow)
	}

	r := defaultReact()
	if r.MaxIterations != 20 || r.StepTimeout != 60 || r.ErrorRetry != 2 {
		t.Errorf("react = %+v, want 20/60/2", r)
	}

	s := defaultSubAgent()
	if s.MaxConcurrency != 5 || s.ExecTimeout != "5m" {
		t.Errorf("subagent 并发/超时 = %d/%q, want 5/5m", s.MaxConcurrency, s.ExecTimeout)
	}
	if s.MaxTaskLength != 16000 || s.MaxResultLength != 8000 {
		t.Errorf("subagent 长度 = %d/%d, want 16000/8000", s.MaxTaskLength, s.MaxResultLength)
	}

	a := defaultAttachment()
	if a.MaxSize != 50 || a.MaxTotalSize != 100 || a.MaxCount != 10 {
		t.Errorf("attachment = %+v, want 50/100/10", a)
	}
	if len(a.AllowedTypes) != 0 {
		t.Errorf("attachment.allowed_types 默认应为空（允许所有）, got %v", a.AllowedTypes)
	}

	rl := defaultRateLimit()
	if rl.Enabled {
		t.Error("限流默认应关闭")
	}
	if rl.DefaultQPS != 10 || rl.DefaultConcurrency != 5 {
		t.Errorf("限流默认配额 = %v/%d, want 10/5", rl.DefaultQPS, rl.DefaultConcurrency)
	}
	if rl.GlobalQPS != 0 || rl.GlobalConcurrency != 0 {
		t.Errorf("全局限流默认应为 0（不限制）, got %v/%d", rl.GlobalQPS, rl.GlobalConcurrency)
	}

	if defaultScheduleEnabled() {
		t.Error("定时任务默认应关闭")
	}

	if got := defaultAuthHeaderName(); got != "X-API-Key" {
		t.Errorf("auth.header_name = %q, want X-API-Key", got)
	}
}
