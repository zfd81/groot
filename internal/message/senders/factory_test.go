package senders

import (
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/setting"
)

// TestNew_Webhook 验证已知渠道返回可用实例，且实例自报的名字与传入的渠道名一致。
// 名字不一致会让消息层按渠道名路由时找错发送器。
func TestNew_Webhook(t *testing.T) {
	s := New(setting.SenderWebhook, config.SenderConf{Enabled: true, URL: "http://example.com/hook"})
	if s == nil {
		t.Fatalf("New(%q) 返回 nil，应返回 webhook 发送器", setting.SenderWebhook)
	}
	if s.Name() != setting.SenderWebhook {
		t.Errorf("Name() = %q, want %q", s.Name(), setting.SenderWebhook)
	}
}

// TestNew_UnknownReturnsNil 验证未知渠道返回 nil。
// 调用方靠 nil 判断跳过，返回非 nil 的零值实例会让未实现的渠道看起来可用。
func TestNew_UnknownReturnsNil(t *testing.T) {
	for _, name := range []string{"", "email", "slack", "WEBHOOK"} {
		if s := New(name, config.SenderConf{}); s != nil {
			t.Errorf("New(%q) = %T, want nil", name, s)
		}
	}
}

// TestNew_CoversAllConfigurableSenders 是这组测试里最要紧的一条：
// setting.ConfigurableSenders 声明「有哪些渠道可配置」，New 负责「把名字映射到实现」。
// 两者必须同步——只在前者加名字而忘了在 New 里加 case，该渠道会在 UI 上可配置
// 却永远不被注册，且没有任何报错。
func TestNew_CoversAllConfigurableSenders(t *testing.T) {
	for _, name := range setting.ConfigurableSenders() {
		if s := New(name, config.SenderConf{URL: "http://example.com/hook"}); s == nil {
			t.Errorf("渠道 %q 在 ConfigurableSenders 中声明，但 New 未提供实现", name)
		}
	}
}
