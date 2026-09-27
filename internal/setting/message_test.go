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
	if got.Senders["email"].SMTPPort != 587 {
		t.Errorf("email SMTPPort = %d, want 默认值 587", got.Senders["email"].SMTPPort)
	}
	if got.QueueSize != 100 || got.Workers != 4 {
		t.Errorf("队列参数不进表，应保持 bootstrap 值: %+v", got)
	}
}

func TestSetMessage_RoundTrip(t *testing.T) {
	s, _ := newMessageSettings(t)
	ctx := context.Background()

	err := s.SetMessage(ctx, map[string]config.SenderConf{
		"webhook": {Enabled: false, URL: "https://table.example.com/hook"},
		"email": {
			Enabled: true, SMTPHost: "smtp.table.test", SMTPPort: 465,
			Username: "table-user", Password: "table-pass", From: "table@test",
		},
	})
	if err != nil {
		t.Fatalf("SetMessage: %v", err)
	}

	got, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	w := got.Senders["webhook"]
	if w.Enabled || w.URL != "https://table.example.com/hook" {
		t.Errorf("webhook 回读 = %+v", w)
	}
	e := got.Senders["email"]
	if !e.Enabled || e.SMTPHost != "smtp.table.test" || e.SMTPPort != 465 {
		t.Errorf("email 回读 = %+v", e)
	}
	if e.Password != "table-pass" {
		t.Errorf("Password = %q, want table-pass", e.Password)
	}
	if got.QueueSize != 100 {
		t.Errorf("QueueSize = %d, 不应被 SetMessage 改动", got.QueueSize)
	}
}

func TestSetMessage_EmptyPasswordKeepsStored(t *testing.T) {
	// 空串表示「不改密码」：界面回读时密码是脱敏值，
	// 原样提交会把星号存成真密码
	s, _ := newMessageSettings(t)
	ctx := context.Background()

	err := s.SetMessage(ctx, map[string]config.SenderConf{
		"email": {Enabled: true, SMTPHost: "smtp.a.test", SMTPPort: 25, From: "a@test", Password: "first-pass"},
	})
	if err != nil {
		t.Fatalf("首次保存: %v", err)
	}
	err = s.SetMessage(ctx, map[string]config.SenderConf{
		"email": {Enabled: true, SMTPHost: "smtp.b.test", SMTPPort: 25, From: "a@test", Password: ""},
	})
	if err != nil {
		t.Fatalf("二次保存: %v", err)
	}

	got, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Senders["email"].Password != "first-pass" {
		t.Errorf("Password = %q, 空串应保留原密码", got.Senders["email"].Password)
	}
	if got.Senders["email"].SMTPHost != "smtp.b.test" {
		t.Errorf("SMTPHost = %q, 其余字段应已更新", got.Senders["email"].SMTPHost)
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
		{"启用 webhook 但地址为空", map[string]config.SenderConf{
			"webhook": {Enabled: true, URL: "   "},
		}},
		{"webhook 地址不是 http", map[string]config.SenderConf{
			"webhook": {Enabled: true, URL: "ftp://example.com/hook"},
		}},
		{"启用 email 但主机为空", map[string]config.SenderConf{
			"email": {Enabled: true, SMTPHost: "", SMTPPort: 587},
		}},
		{"email 端口越界", map[string]config.SenderConf{
			"email": {Enabled: true, SMTPHost: "smtp.test", SMTPPort: 70000},
		}},
		{"启用 email 但发件人为空", map[string]config.SenderConf{
			"email": {Enabled: true, SMTPHost: "smtp.test", SMTPPort: 587, From: "  "},
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
		"email":   {Enabled: false, SMTPHost: "", SMTPPort: 0},
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
		Scope: repo.ScopeGlobal, Name: "message.senders.email.smtp_port", Value: "abc",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Senders["email"].SMTPPort != 587 {
		t.Errorf("SMTPPort = %d, 脏数据应退回默认值 587", got.Senders["email"].SMTPPort)
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

func TestSetMessage_WritesOnlySubmittedSender(t *testing.T) {
	s, store := newMessageSettings(t)
	ctx := context.Background()

	err := s.SetMessage(ctx, map[string]config.SenderConf{
		"email": {Enabled: true, SMTPHost: "smtp.test", SMTPPort: 587, From: "a@test"},
	})
	if err != nil {
		t.Fatalf("SetMessage: %v", err)
	}

	items, err := store.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	for _, it := range items {
		if strings.HasPrefix(it.Name, "message.senders.webhook.") {
			t.Errorf("未提交的 webhook 不应写入表，却出现 %s", it.Name)
		}
	}

	got, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	w := got.Senders["webhook"]
	if w.Enabled || w.URL != "" {
		t.Errorf("webhook 应保持基准零值（未提交不写表），得到 %+v", w)
	}
}

func TestSetMessage_EmptyMapIsNoop(t *testing.T) {
	s, store := newMessageSettings(t)
	ctx := context.Background()

	if err := s.SetMessage(ctx, map[string]config.SenderConf{}); err != nil {
		t.Fatalf("空 map 应为 no-op，得到错误: %v", err)
	}
	items, err := store.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("空 map 不应写入任何行，表中有 %d 行", len(items))
	}
}
