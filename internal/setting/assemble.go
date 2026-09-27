// internal/setting/assemble.go
// 启动视图组装：把 bootstrap.yaml 的静态项与配置表的业务项合成一个
// config.Config，供 api.NewServer 等按整体接收配置的构造路径使用。
//
// 这是启动时的一次快照。业务项的在线生效不经过它 —— 限流参数经
// RateLimiter.Reconfigure、发送渠道经 Layer.SetSender、调度开关经门控读表。
package setting

import (
	"context"

	"github.com/zfd81/groot/internal/config"
)

// AssembleConfig 组装完整的 config.Config。
func (s *Settings) AssembleConfig(ctx context.Context) (*config.Config, error) {
	rt, err := s.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	auth, err := s.Auth(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := s.Message(ctx)
	if err != nil {
		return nil, err
	}

	return &config.Config{
		Agent:      s.static.Agent,
		Server:     s.static.Server,
		Logging:    s.static.Logging,
		Database:   s.static.Database,
		Memory:     rt.Memory,
		React:      rt.React,
		SubAgent:   rt.SubAgent,
		Attachment: rt.Attachment,
		Schedule:   rt.Schedule,
		Message:    msg,
		Security: config.SecurityConfig{
			Auth:      auth,
			RateLimit: rt.RateLimit,
		},
	}, nil
}
