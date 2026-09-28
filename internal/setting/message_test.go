// internal/setting/message_test.go
package setting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/settingdb"
)

// messageStaticCfg 构造消息层的 bootstrap 构造参数；发送渠道的基准值是代码默认值。
func messageStaticCfg() config.Bootstrap {
	return config.Bootstrap{
		Message: config.MessageBootstrap{QueueSize: 100, Workers: 4},
	}
}

// newMessageSettings 建一个带真实配置表的配置对象，同时返回仓库供测试直接读写表。
func newMessageSettings(t *testing.T) (*Settings, repo.SettingRepo) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	store := settingdb.New(sqlxDB, dialect)
	return New(messageStaticCfg(), store), store
}

func TestMessage_EmptyTableUsesDefaults(t *testing.T) {
	s, _ := newMessageSettings(t)

	got, err := s.Message(context.Background())
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	w := got.Senders["webhook"]
	if w.Enabled || w.URL != "" {
		t.Errorf("webhook = %+v, 表为空时应为零值（关闭、无地址）", w)
	}
	if len(got.Senders) != 1 {
		t.Errorf("Senders = %+v, 只应有 webhook 一个可配置渠道", got.Senders)
	}
	if got.QueueSize != 100 || got.Workers != 4 {
		t.Errorf("队列参数不进表，应保持 bootstrap 值: %+v", got)
	}
}

func TestSetMessage_RoundTrip(t *testing.T) {
	s, _ := newMessageSettings(t)
	ctx := context.Background()

	err := s.SetMessage(ctx, map[string]config.SenderConf{
		"webhook": {Enabled: true, URL: "https://table.example.com/hook"},
	})
	if err != nil {
		t.Fatalf("SetMessage: %v", err)
	}

	got, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	w := got.Senders["webhook"]
	if !w.Enabled || w.URL != "https://table.example.com/hook" {
		t.Errorf("webhook 回读 = %+v", w)
	}
	if got.QueueSize != 100 {
		t.Errorf("QueueSize = %d, 不应被 SetMessage 改动", got.QueueSize)
	}
}

func TestSetMessage_Validates(t *testing.T) {
	s, _ := newMessageSettings(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		confs map[string]config.SenderConf
	}{
		{"未知渠道", map[string]config.SenderConf{
			"telegram": {Enabled: true},
		}},
		{"已下线的 email 渠道", map[string]config.SenderConf{
			"email": {Enabled: true, URL: "https://example.com/hook"},
		}},
		{"已下线的 stdout 渠道", map[string]config.SenderConf{
			"stdout": {Enabled: true},
		}},
		{"启用 webhook 但地址为空", map[string]config.SenderConf{
			"webhook": {Enabled: true, URL: "   "},
		}},
		{"webhook 地址不是 http", map[string]config.SenderConf{
			"webhook": {Enabled: true, URL: "ftp://example.com/hook"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := s.SetMessage(ctx, c.confs)
			if err == nil {
				t.Fatal("应当被拒绝，却通过了校验")
			}
			if !errors.Is(err, ErrInvalidSetting) {
				t.Errorf("错误未包装 ErrInvalidSetting: %v", err)
			}
		})
	}
}

func TestSetMessage_DisabledSkipsFieldChecks(t *testing.T) {
	// 关闭的渠道不校验其参数：使用者要能在参数不全时先把渠道关掉
	s, _ := newMessageSettings(t)

	err := s.SetMessage(context.Background(), map[string]config.SenderConf{
		"webhook": {Enabled: false, URL: ""},
	})
	if err != nil {
		t.Fatalf("关闭渠道不应校验参数: %v", err)
	}
}

func TestMessage_NoStore(t *testing.T) {
	s := New(messageStaticCfg(), nil)

	got, err := s.Message(context.Background())
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.QueueSize != 100 || got.Workers != 4 {
		t.Errorf("无配置表时队列参数应保持 bootstrap 值，得到 %+v", got)
	}
	if len(got.Senders) != 0 {
		t.Errorf("无配置表时 Senders 应为空 map，得到 %+v", got.Senders)
	}
}

func TestMessage_DirtyValueFallsBack(t *testing.T) {
	// 表中的脏数据退回基准值，不使整次取值失败
	s, store := newMessageSettings(t)
	ctx := context.Background()

	err := store.Upsert(ctx, &repo.Setting{
		Scope: repo.ScopeGlobal, Name: "message.senders.webhook.enabled", Value: "abc",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Senders["webhook"].Enabled {
		t.Error("Enabled = true, 脏数据应退回默认值 false")
	}
}

func TestSetMessage_NoStore(t *testing.T) {
	s := New(messageStaticCfg(), nil)

	err := s.SetMessage(context.Background(), map[string]config.SenderConf{
		"webhook": {Enabled: true, URL: "https://example.com/hook"},
	})
	if !errors.Is(err, ErrNoSettingStore) {
		t.Errorf("err = %v, want ErrNoSettingStore", err)
	}
}

func TestSetMessage_WritesOnlyKnownSenders(t *testing.T) {
	s, store := newMessageSettings(t)
	ctx := context.Background()

	err := s.SetMessage(ctx, map[string]config.SenderConf{
		"webhook": {Enabled: true, URL: "https://example.com/hook"},
	})
	if err != nil {
		t.Fatalf("SetMessage: %v", err)
	}

	items, err := store.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	for _, it := range items {
		if strings.HasPrefix(it.Name, "message.senders.") &&
			!strings.HasPrefix(it.Name, "message.senders.webhook.") {
			t.Errorf("表中出现非 webhook 渠道的配置行 %s", it.Name)
		}
	}
}

// TestConfigurableSenders 验证导出的渠道列表内容正确，且返回副本——
// 调用方修改返回值不应污染包内状态。
func TestConfigurableSenders(t *testing.T) {
	got := ConfigurableSenders()
	if len(got) != 1 || got[0] != SenderWebhook {
		t.Fatalf("ConfigurableSenders() = %v, want [%s]", got, SenderWebhook)
	}

	got[0] = "tampered"
	again := ConfigurableSenders()
	if again[0] != SenderWebhook {
		t.Errorf("返回值被外部修改后污染了包内状态: %v", again)
	}
}
