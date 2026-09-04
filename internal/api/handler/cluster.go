package handler

import (
	"context"
	"fmt"
	"sort"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// ClusterHandler 处理 GET /web/cluster 请求，列出集群中所有已注册实例。
//
// 数据直接来自 cluster_members 表：过期成员由 leader 的心跳循环清理，
// 因此这里读到的即为当前存活成员，无需在查询侧再按超时过滤。
type ClusterHandler struct {
	members repo.MemberRepo
	log     *logger.Logger
}

// NewClusterHandler 构造 ClusterHandler。
// members 为 nil 时（未启用集群）接口返回空列表；log 为 nil 时用 NewNop() 兜底。
func NewClusterHandler(members repo.MemberRepo, log *logger.Logger) *ClusterHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &ClusterHandler{members: members, log: log}
}

// Serve 输出 200 JSON：{"members":[{reg_id,role,address,pid,heartbeat_at,created_at}]}
// address 为 IP:PORT 形式；时间字段是毫秒时间戳，由前端按本地时区格式化。
// leader 排在首位，其余按 reg_id 升序（即加入顺序）。
func (h *ClusterHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	resp := types.ClusterResponse{Members: []types.ClusterMemberInfo{}}
	if h.members == nil {
		rc.JSON(200, resp)
		return
	}

	list, err := h.members.ListAll(ctx)
	if err != nil {
		h.log.Error("列出集群成员失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "读取集群成员失败"})
		return
	}

	for _, m := range list {
		resp.Members = append(resp.Members, types.ClusterMemberInfo{
			RegID:       m.RegID,
			Role:        m.Role,
			Address:     fmt.Sprintf("%s:%d", m.Host, m.Port),
			Pid:         m.Pid,
			HeartbeatAt: m.HeartbeatAt.UnixMilli(),
			CreatedAt:   m.CreatedAt.UnixMilli(),
		})
	}

	sort.SliceStable(resp.Members, func(i, j int) bool {
		li := resp.Members[i].Role == cluster.RoleLeader
		lj := resp.Members[j].Role == cluster.RoleLeader
		if li != lj {
			return li
		}
		return resp.Members[i].RegID < resp.Members[j].RegID
	})

	rc.JSON(200, resp)
}
