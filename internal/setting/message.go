// internal/setting/message.go
// 消息发送器配置：保存即生效的发送渠道参数。
//
// 键名多一层渠道名：message.senders.<渠道>.<字段>，镜像配置的层级路径。
// 渠道是可枚举的固定集合，
// 不接受任意名字——注册表里没有对应实现的渠道，存进表也无从投递。
//
// 队列容量与工作协程数不进表：它们决定启动时建好的 channel 大小与协程数量，
// 改动需重启才生效，恒取 bootstrap.yaml 的值。

package setting

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// SenderWebhook 是允许配置的渠道名。
const SenderWebhook = "webhook"

// configurableSenders 限定可配置的渠道，顺序固定以便接口回读稳定。
var configurableSenders = []string{SenderWebhook}

// senderKey 拼出某渠道某字段在配置表中的键名。
func senderKey(sender, field string) string {
	return "message.senders." + sender + "." + field
}

// 发送器配置的字段名。
const (
	fieldEnabled = "enabled"
	fieldURL     = "url"
)

// Message 读取消息通知配置。QueueSize 与 Workers 来自 bootstrap.yaml
// （消息层构造参数），发送渠道参数来自配置表。
func (s *Settings) Message(ctx context.Context) (config.MessageConfig, error) {
	base := config.MessageConfig{
		QueueSize: s.static.Message.QueueSize,
		Workers:   s.static.Message.Workers,
		Senders:   map[string]config.SenderConf{},
	}
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.MessageConfig{}, err
	}

	out := base
	out.Senders = make(map[string]config.SenderConf, len(configurableSenders))
	for _, name := range configurableSenders {
		out.Senders[name] = senderConfFrom(config.SenderConf{}, name, vals)
	}
	return out, nil
}

// senderConfFrom 以 base 为基准叠加表中存在的字段。
// 脏数据退回基准值，不使整次取值失败：一行坏数据不应让消息层无法投递。
func senderConfFrom(base config.SenderConf, sender string, vals map[string]string) config.SenderConf {
	if raw, ok := vals[senderKey(sender, fieldEnabled)]; ok {
		base.Enabled = parseBool(raw, base.Enabled)
	}
	if raw, ok := vals[senderKey(sender, fieldURL)]; ok {
		base.URL = raw
	}
	return base
}

// SetMessage 保存发送器配置。校验不通过即整次拒绝，不做部分写入。
//
// 只写入 confs 中出现的渠道：界面可以只提交被改动的那一个。
func (s *Settings) SetMessage(ctx context.Context, confs map[string]config.SenderConf) error {
	for name, conf := range confs {
		if err := validateSenderConf(name, conf); err != nil {
			return err
		}
	}
	if s.repo == nil {
		return ErrNoSettingStore
	}

	var rows []*repo.Setting
	for _, name := range configurableSenders {
		conf, ok := confs[name]
		if !ok {
			continue
		}
		rows = append(rows, senderRows(name, conf)...)
	}
	if len(rows) == 0 {
		return nil
	}
	return s.repo.Upsert(ctx, rows...)
}

// validateSenderConf 校验单个渠道的参数。
// 渠道关闭时只校验渠道名：参数不全也要能先把它关掉。
func validateSenderConf(name string, conf config.SenderConf) error {
	known := false
	for _, n := range configurableSenders {
		if n == name {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("%w: 未知的发送渠道 %q，可配置的为 %v",
			ErrInvalidSetting, name, configurableSenders)
	}
	if !conf.Enabled {
		return nil
	}

	if name == SenderWebhook {
		addr := strings.TrimSpace(conf.URL)
		if addr == "" {
			return fmt.Errorf("%w: 启用 webhook 需填写推送地址", ErrInvalidSetting)
		}
		u, err := url.Parse(addr)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: webhook 地址应是完整的 http 或 https 链接，当前为 %q",
				ErrInvalidSetting, conf.URL)
		}
	}
	return nil
}

// senderRows 把单个渠道摊平成配置表的行。
// 该渠道的全部字段一次写入，与运行时配置按分类整体保存的做法一致。
func senderRows(name string, conf config.SenderConf) []*repo.Setting {
	kv := []struct {
		field string
		value string
	}{
		{fieldEnabled, strconv.FormatBool(conf.Enabled)},
		{fieldURL, strings.TrimSpace(conf.URL)},
	}
	out := make([]*repo.Setting, 0, len(kv))
	for _, it := range kv {
		out = append(out, &repo.Setting{
			Scope: repo.ScopeGlobal,
			Name:  senderKey(name, it.field),
			Value: it.value,
		})
	}
	return out
}
