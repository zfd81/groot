package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
)

// ToolsHandler 处理 GET /tools；通过 X-Agent-Name header 选择主 Agent 或子 Agent 的 MCP Manager。
//
// 路由约定（与 Task 13 chat.go、Task 15 SkillsHandler 保持一致）：
//   - 不传 / X-Agent-Name == "groot" → 主 Agent mcpManager
//   - 非空 + 非 "groot" → 查 registry，未注册返 400 unknown_agent
//   - registry 为 nil（不应在生产路径出现）→ 同 400，并 log.Info 警示配置异常
type ToolsHandler struct {
	mcpManager *mcp.Manager
	registry   *agent.SubAgentRegistry
	logger     *logger.Logger
}

// NewToolsHandler 构造 ToolsHandler。
// log 为 nil 时使用 logger.NewNop() 兜底（不应在生产路径出现）。
func NewToolsHandler(mcpMgr *mcp.Manager, reg *agent.SubAgentRegistry, log *logger.Logger) *ToolsHandler {
	if log == nil {
		log = logger.NewNop()
	}
	return &ToolsHandler{
		mcpManager: mcpMgr,
		registry:   reg,
		logger:     log,
	}
}

// Serve 处理 tools 请求。
func (h *ToolsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	mgr := h.mcpManager
	requestedAgent := string(rc.GetHeader("X-Agent-Name"))
	if requestedAgent == agent.MainAgentName {
		requestedAgent = "" // 标准化：传主 Agent 名等价于不传
	}
	if requestedAgent != "" {
		if h.registry == nil {
			h.logger.Info("X-Agent-Name 校验失败：SubAgentRegistry 未初始化",
				zap.String("requested_agent", requestedAgent))
			rc.JSON(400, utils.H{"status": "unknown_agent", "message": "Unknown agent: " + requestedAgent})
			return
		}
		entry, ok := h.registry.Get(requestedAgent)
		if !ok {
			rc.JSON(400, utils.H{"status": "unknown_agent", "message": "Unknown agent: " + requestedAgent})
			return
		}
		mgr = entry.MCPManager
	}

	h.logger.Info("API request: /tools", zap.String("agent", coalesceAgentName(requestedAgent)))

	// 按 MCP 分组（主 Agent + 各子 Agent 共享同一个响应形态）
	grouped := make(map[string]types.ToolsGroup)

	// 当 mgr 非 nil 时填充 MCP 工具：
	// - 主 Agent mgr == nil 不应在生产出现；
	// - Solo 模式下子 Agent 没声明 MCP 时 mgr 可能为 nil（合法），跳过 MCP 段即可。
	if mgr != nil {
		mcpTools := mgr.ListTools()
		for _, t := range mcpTools {
			group := grouped[t.MCP]
			group.Tools = append(group.Tools, types.ToolInfo{
				Name:        t.Name,
				Description: t.Description,
			})
			group.Total++
			grouped[t.MCP] = group
		}

		// 确保所有 MCP 都显示（包括工具数为 0 的），并回填 MCP 定义中的
		// type/description（供前端在分组标题处展示类型标签与描述）。
		mcpInfos := mgr.ListWithToolCount()
		for _, info := range mcpInfos {
			group := grouped[info.Name]
			if group.Tools == nil {
				group.Tools = []types.ToolInfo{}
			}
			group.Type = string(info.Type)
			group.Description = info.Description
			grouped[info.Name] = group
			// Log MCPs with errors
			if info.Error != "" {
				h.logger.Error("MCP has discovery error",
					zap.String("name", info.Name),
					zap.String("error", info.Error))
			}
		}
	}

	// 主 Agent 路径下，把 call_agent 作为合成 group 暴露给 /tools。
	// 与 Executor.Execute 的 ExtraTools 注入逻辑保持一致：仅当
	// requestedAgent 为空（主 Agent）且 registry 非 nil 时挂载。
	// Solo 模式（X-Agent-Name 指向子 Agent）不挂载——与 executor.go 的「Solo 模式不挂 call_agent」保持一致。
	if requestedAgent == "" && h.registry != nil {
		grouped["_builtin"] = types.ToolsGroup{
			Tools: []types.ToolInfo{
				{
					Name:        "call_agent",
					Description: h.registry.BuildDescription(),
				},
			},
			Total: 1,
		}
	}

	rc.JSON(200, grouped)
}
