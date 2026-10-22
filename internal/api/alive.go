package api

import (
	"context"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// memberAliveTimeout 成员心跳超时阈值，与 internal/cluster/cluster.go 的 heartbeatTimeout 保持一致：
// 心跳距今超过该时长的成员视为已离线。
const memberAliveTimeout = 7 * time.Second

// othersAliveProbe 返回"集群中是否存在除本节点外、心跳未超时的其他成员"的探测函数。
// 成员列表读取失败时按"没有其他存活节点"处理，避免对不可达的集群做无意义的广播与等待。
func othersAliveProbe(members repo.MemberRepo, selfID func() string, timeout time.Duration) func(context.Context) bool {
	return func(ctx context.Context) bool {
		list, err := members.ListAll(ctx)
		if err != nil {
			return false
		}
		self := selfID()
		now := time.Now()
		for _, m := range list {
			if m.RegID != self && now.Sub(m.HeartbeatAt) < timeout {
				return true
			}
		}
		return false
	}
}
