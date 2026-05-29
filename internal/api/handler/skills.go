package handler

import (
	"context"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/logger"
)

// SkillsHandler 处理 GET /skills；通过 X-Agent-Name header 选择主 Agent 或子 Agent 的 skills 后端。
//
// 路由约定（与 Task 13 chat.go 保持一致）：
//   - 不传 / X-Agent-Name == "groot" → 主 Agent backend
//   - 非空 + 非 "groot" → 查 registry，未注册返 400 unknown_agent
//   - registry 为 nil（不应在生产路径出现）→ 同 400，并 log.Info 警示配置异常
type SkillsHandler struct {
	backend  skill.Backend
	registry *agent.SubAgentRegistry
	log      *logger.Logger
}

// NewSkillsHandler 构造 SkillsHandler。
// log 为 nil 时使用 logger.NewNop() 兜底（不应在生产路径出现）。
func NewSkillsHandler(backend skill.Backend, reg *agent.SubAgentRegistry, log *logger.Logger) *SkillsHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &SkillsHandler{backend: backend, registry: reg, log: log}
}

// Serve 处理 skills 请求。
func (h *SkillsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	backend := h.backend
	requestedAgent := string(rc.GetHeader("X-Agent-Name"))
	if requestedAgent == agent.MainAgentName {
		requestedAgent = "" // 标准化：传主 Agent 名等价于不传
	}
	if requestedAgent != "" {
		if h.registry == nil {
			// 配置缺失：服务端未注入 SubAgentRegistry。生产路径不应出现。
			h.log.Info("X-Agent-Name 校验失败：SubAgentRegistry 未初始化",
				zap.String("requested_agent", requestedAgent))
			rc.JSON(400, utils.H{"status": "unknown_agent", "message": "Unknown agent: " + requestedAgent})
			return
		}
		entry, ok := h.registry.Get(requestedAgent)
		if !ok {
			rc.JSON(400, utils.H{"status": "unknown_agent", "message": "Unknown agent: " + requestedAgent})
			return
		}
		backend = entry.SkillBK
	}
	if backend == nil {
		rc.JSON(200, types.SkillsResponse{Skills: []types.SkillInfo{}, Total: 0})
		return
	}
	matters, err := backend.List(ctx)
	if err != nil {
		// 失败降级：返回 200 + 空数组，与 SkillsResponse 的标准形态保持一致；
		// 故障细节通过日志暴露给运维。
		h.log.Info("列举 skills 失败，返回空数组",
			zap.String("agent", coalesceAgentName(requestedAgent)),
			zap.Error(err))
		rc.JSON(200, types.SkillsResponse{Skills: []types.SkillInfo{}, Total: 0})
		return
	}
	skillInfos := make([]types.SkillInfo, len(matters))
	for i, m := range matters {
		skillInfos[i] = types.SkillInfo{
			Name:        m.Name,
			Description: m.Description,
		}
	}
	rc.JSON(200, types.SkillsResponse{Skills: skillInfos, Total: len(skillInfos)})
}

// coalesceAgentName 把空字符串映射为主 Agent 名，便于日志可读。
// 该 helper 由 skills.go 与 tools.go 共同使用，仅声明一次。
func coalesceAgentName(name string) string {
	if name == "" {
		return agent.MainAgentName
	}
	return name
}
