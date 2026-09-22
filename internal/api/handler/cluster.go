package handler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/lifecycle"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// restartMessageTTL 重启指令的有效期：超过即使未被消费也不再投递。
const restartMessageTTL = 2 * time.Minute

// RestartSender 是投递重启指令所需的最小能力，由 *cluster.MessageService 满足。
type RestartSender interface {
	SendMessage(ctx context.Context, msg cluster.Message) error
}

// ClusterHandler 处理集群管理端点：列出成员、重启指定实例。
//
// 成员数据直接来自 cluster_members 表：过期成员由 leader 的心跳循环清理，
// 因此这里读到的即为当前存活成员，无需在查询侧再按超时过滤。
type ClusterHandler struct {
	members          repo.MemberRepo
	selfID           func() string
	sender           RestartSender
	restartSupported bool
	log              *logger.Logger
}

// NewClusterHandler 构造 ClusterHandler。
//   - members 为 nil（未启用集群）时列表为空、重启一律 404
//   - selfID 返回本实例 reg_id；nil 视为未注册（空串）
//   - sender 为 nil 时重启端点返回 500；调用方须传 nil 接口而非 nil 的 *cluster.MessageService（typed-nil 会绕过判空）
//   - restartSupported 为本实例是否支持重启（工作进程模式为 true）
//   - log 为 nil 时用 NewNop() 兜底
func NewClusterHandler(members repo.MemberRepo, selfID func() string, sender RestartSender, restartSupported bool, log *logger.Logger) *ClusterHandler {
	if log == nil {
		log = logger.NewNop()
	}
	if selfID == nil {
		selfID = func() string { return "" }
	}
	return &ClusterHandler{members: members, selfID: selfID, sender: sender, restartSupported: restartSupported, log: log}
}

// Serve 输出 200 JSON：{"members":[{reg_id,role,address,pid,heartbeat_at,created_at}],"self":"<reg_id>"}
// address 为 IP:PORT 形式；时间字段是毫秒时间戳，由前端按本地时区格式化。
// leader 排在首位，其余按 reg_id 升序（即加入顺序）。
func (h *ClusterHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	resp := types.ClusterResponse{Members: []types.ClusterMemberInfo{}, Self: h.selfID()}
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

// Restart 处理 POST /web/cluster/:reg_id/restart：向目标实例投递点对点重启指令。
// 本机重启同样走消息投递，不另开直接调用路径（单一链路、消费记录即审计日志）。
func (h *ClusterHandler) Restart(ctx context.Context, rc *app.RequestContext) {
	regID := rc.Param("reg_id")
	if h.members == nil {
		rc.JSON(404, utils.H{"status": "error", "code": "member_not_found", "message": "实例不存在"})
		return
	}
	// 仅校验存在性：成员在投递前消失也无妨——消息 2 分钟过期，且重启后的新 reg_id 不会匹配旧目标。
	if _, err := h.members.Get(ctx, regID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			rc.JSON(404, utils.H{"status": "error", "code": "member_not_found", "message": "实例不存在"})
			return
		}
		h.log.Error("查询集群成员失败", zap.String("reg_id", regID), zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "读取集群成员失败"})
		return
	}

	self := regID == h.selfID()
	if self && !h.restartSupported {
		rc.JSON(409, utils.H{"status": "error", "code": "restart_unsupported", "message": "实例以单进程模式运行，不支持重启"})
		return
	}
	if h.sender == nil {
		h.log.Error("集群消息服务未挂接，无法投递重启指令")
		rc.JSON(500, utils.H{"status": "error", "message": "集群消息服务不可用"})
		return
	}

	requestedBy, _ := rc.Get("web_user_id")
	if requestedBy == nil {
		requestedBy = "" // 未经会话中间件注入时不写入字面量 "<nil>"
	}
	msg := cluster.Message{
		Type:           lifecycle.MessageTypeRestart,
		TargetInstance: regID,
		TargetModule:   lifecycle.ModuleName,
		Payload:        map[string]any{"reason": "web", "requested_by": fmt.Sprint(requestedBy)},
		Priority:       1,
		ExpiresAt:      time.Now().Add(restartMessageTTL),
	}
	if err := h.sender.SendMessage(ctx, msg); err != nil {
		h.log.Error("投递重启指令失败", zap.String("reg_id", regID), zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "投递重启指令失败"})
		return
	}
	h.log.Info("已投递重启指令", zap.String("reg_id", regID), zap.Bool("self", self), zap.Any("requested_by", requestedBy))
	rc.JSON(202, types.RestartResponse{Status: "accepted", RegID: regID, Self: self})
}
