package senders

import (
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/message"
	"github.com/zfd81/groot/internal/setting"
)

// New 按渠道名构造发送器。未知渠道返回 nil，由调用方跳过——
// 渠道集合由 setting.ConfigurableSenders 定义，此处负责把名字映射到实现。
func New(name string, conf config.SenderConf) message.Sender {
	switch name {
	case setting.SenderWebhook:
		return NewWebhook(conf.URL)
	default:
		return nil
	}
}
