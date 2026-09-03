package handler

import (
	"context"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/logger"
)

// AgentsHandler 处理 GET /agents 请求。
//
// 该接口给前端/客户端枚举所有可调用的 Agent：
//   - 主 Agent "groot"（始终首位）
//   - 子 Agent 列表（按 SubAgentRegistry.Names() 字典序）
//
// 每个 Agent 携带 skills 摘要（仅 name/description，无 prompt 正文）。
//
// 持有 *logger.Logger 是为了在 Backend.List 失败的降级路径打日志，
// 让运维能从日志定位是哪个 Agent 的 skill backend 出了问题，而不是
// 看到一个空数组就以为没配 skill。
// 之所以用 Info 而非 Error：失败已被降级（返回空 slice），接口仍 200，
// 与 server.go 中限流器初始化失败的处理一致——属于「降级提示」而非
// 影响请求语义的错误。logger.Logger 当前未暴露 Warn 等级。
type AgentsHandler struct {
	registry    *agent.SubAgentRegistry
	mainSkillBE skill.Backend
	homeDir     string // GROOT_HOME，Definition 接口据此定位 GROOT.md / subagents/<name>/agent.md
	log         *logger.Logger
}

// NewAgentsHandler 构造 AgentsHandler。
// registry 为 nil 时仅返回主 Agent；mainSkillBE 为 nil 时主 Agent 的 skills 为空数组。
// homeDir 是 GROOT_HOME 目录，Definition 接口用于读取定义文件。
// log 为 nil 时使用 logger.NewNop() 兜底（不应在生产路径出现 nil；测试可显式传 NewNop()）。
func NewAgentsHandler(reg *agent.SubAgentRegistry, mainSkillBE skill.Backend, homeDir string, log *logger.Logger) *AgentsHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &AgentsHandler{registry: reg, mainSkillBE: mainSkillBE, homeDir: homeDir, log: log}
}

// Serve 输出 200 JSON：{"agents":[{name,description,skills:[{name,description}]}]}
// 主 Agent 的 description 是固定文案；skills 列表读取失败时降级为空数组（不返 5xx）。
func (h *AgentsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	resp := types.AgentsResponse{}
	// 主 Agent 始终位于 Agents[0]
	resp.Agents = append(resp.Agents, types.AgentInfo{
		Name:        agent.MainAgentName,
		Description: "默认 Agent（全局配置）",
		Skills:      h.listAgentSkills(ctx, agent.MainAgentName, h.mainSkillBE),
	})
	if h.registry != nil {
		for _, name := range h.registry.Names() {
			e, ok := h.registry.Get(name)
			if !ok || e == nil {
				// 防御 Close 与 Names 并发期：Names 给的快照可能晚于 entries 重置
				continue
			}
			resp.Agents = append(resp.Agents, types.AgentInfo{
				Name:        e.Name,
				Description: e.Description,
				Skills:      h.listAgentSkills(ctx, e.Name, e.SkillBK),
			})
		}
	}
	rc.JSON(200, resp)
}

// Definition 处理 GET /web/agents/:name/definition，返回 Agent 定义文件原文。
//
// 规则：
//   - name == 主 Agent（groot）：读 {homeDir}/GROOT.md
//   - 其他 name：必须命中 SubAgentRegistry，读 {homeDir}/subagents/{name}/agent.md。
//     「必须命中注册表」同时兜住了路径穿越——注册表键来自启动期目录扫描，
//     不可能包含路径分隔符，未注册的任意 name 直接 404。
//
// 文件读取失败（不存在/无权限）统一 404，不向客户端暴露磁盘细节；
// 具体原因进日志供运维排查。每次请求实时读盘，保证看到当前生效的定义。
func (h *AgentsHandler) Definition(ctx context.Context, rc *app.RequestContext) {
	name := rc.Param("name")

	var path, file string
	if name == agent.MainAgentName {
		file = "GROOT.md"
		path = filepath.Join(h.homeDir, file)
	} else {
		if h.registry == nil {
			rc.JSON(404, utils.H{"status": "not_found", "message": "Agent 不存在"})
			return
		}
		if _, ok := h.registry.Get(name); !ok {
			rc.JSON(404, utils.H{"status": "not_found", "message": "Agent 不存在"})
			return
		}
		file = "agent.md"
		path = filepath.Join(h.homeDir, "subagents", name, file)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		h.log.Info("读取 Agent 定义文件失败",
			zap.String("agent", name),
			zap.String("path", path),
			zap.Error(err))
		rc.JSON(404, utils.H{"status": "not_found", "message": "Agent 定义文件不存在"})
		return
	}

	rc.JSON(200, types.AgentDefinitionResponse{
		Name:    name,
		File:    file,
		Content: string(content),
	})
}

// listAgentSkills 把 skill.Backend.List 的结果转换成 types.SkillInfo 列表。
//
// 契约：
//   - be == nil 或 List 失败时返回空 slice（保持响应可解析；不返 5xx，与 SkillsHandler 一致）。
//   - 失败路径会打日志带上 agent name 让运维能定位故障来源；空数组不再静默。
//
// 之所以失败也返回空而不是 5xx：/agents 是聚合枚举接口，单个子 Agent
// 的 skill 加载抖动不应影响其它 Agent 的可见性；运维通过 GET /skills
// 与日志再去精细排查。
func (h *AgentsHandler) listAgentSkills(ctx context.Context, agentName string, be skill.Backend) []types.SkillInfo {
	if be == nil {
		return []types.SkillInfo{}
	}
	matters, err := be.List(ctx)
	if err != nil {
		// 降级而非 5xx：单个子 Agent 的 skill 列表抖动不该影响 /agents 整体响应。
		// 用 Info 与项目内其它「失败已降级」日志（如限流器初始化失败）保持一致。
		h.log.Info("列举子 Agent skills 失败，返回空数组",
			zap.String("agent", agentName),
			zap.Error(err))
		return []types.SkillInfo{}
	}
	out := make([]types.SkillInfo, len(matters))
	for i, m := range matters {
		out[i] = types.SkillInfo{
			Name:        m.Name,
			Description: m.Description,
		}
	}
	return out
}
