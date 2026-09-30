package setting

import (
	"context"
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// TestAssembleConfig 静态项来自 bootstrap，业务项来自配置表，
// 表中缺失的业务项为代码默认值。
func TestAssembleConfig(t *testing.T) {
	b := config.Bootstrap{}
	b.Server = config.ServerConfig{Host: "127.0.0.1", Port: 9090}
	b.Schedule = config.ScheduleBootstrap{MaxConcurrentTasks: 8, SyncInterval: "15s"}
	b.Security.RateLimit.CleanupInterval = "2m"

	r := newFakeRepo()
	s := New(b, r)
	ctx := context.Background()
	if err := r.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyMemoryHistoryWindow, Value: "50"},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyAuthSecret, Value: "deadbeef"},
	); err != nil {
		t.Fatalf("预置表值: %v", err)
	}

	cfg, err := s.AssembleConfig(ctx)
	if err != nil {
		t.Fatalf("AssembleConfig: %v", err)
	}
	// 静态项
	if cfg.Server.Port != 9090 {
		t.Errorf("静态项未透传: %+v", cfg.Server)
	}
	if cfg.Schedule.MaxConcurrentTasks != 8 || cfg.Schedule.SyncInterval != "15s" {
		t.Errorf("schedule 静态项未透传: %+v", cfg.Schedule)
	}
	if cfg.Security.RateLimit.CleanupInterval != "2m" {
		t.Errorf("cleanup_interval = %q", cfg.Security.RateLimit.CleanupInterval)
	}
	// 表项与默认值
	if cfg.Memory.HistoryWindow != 50 {
		t.Errorf("表项未合入: history_window = %d", cfg.Memory.HistoryWindow)
	}
	if cfg.Security.Auth.Secret != "deadbeef" {
		t.Errorf("密钥未合入: %q", cfg.Security.Auth.Secret)
	}
	if cfg.React.MaxIterations != 20 {
		t.Errorf("表中缺失的项应为默认值: %d", cfg.React.MaxIterations)
	}
}
