// Package agent 子 Agent 注册表与并发控制。
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"golang.org/x/sync/semaphore"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/filesystem"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
)

// SubAgentEntry 单个子 Agent 的运行时数据（启动期一次性构建，运行时只读）。
type SubAgentEntry struct {
	Name        string
	Description string
	Instruction string             // agent.md 正文，Solo 模式使用
	Tool        tool.InvokableTool // 启动期 adk.NewTypedAgentTool 预构建
	MCPManager  *mcp.Manager       // 持有 MCP 连接生命周期
	SkillBK     einoskill.Backend  // 供 /agents、/skills 查询；Watcher 热更新入口
}

// SubAgentRegistry 全局单例：所有子 Agent 的注册表 + 并发控制。
type SubAgentRegistry struct {
	entries map[string]*SubAgentEntry
	sem     *semaphore.Weighted
	log     *logger.Logger
	mu      sync.RWMutex
}

// newRegistryForTest 仅供 _test.go 文件使用，跳过启动期扫描。
func newRegistryForTest(maxConc int) *SubAgentRegistry {
	if maxConc <= 0 {
		maxConc = 1
	}
	return &SubAgentRegistry{
		entries: make(map[string]*SubAgentEntry),
		sem:     semaphore.NewWeighted(int64(maxConc)),
	}
}

// Get 查找子 Agent。
func (r *SubAgentRegistry) Get(name string) (*SubAgentEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	return e, ok
}

// Acquire 占用一个并发名额；ctx 取消时立即返回错误。
func (r *SubAgentRegistry) Acquire(ctx context.Context) error {
	return r.sem.Acquire(ctx, 1)
}

// Release 释放并发名额。
func (r *SubAgentRegistry) Release() {
	r.sem.Release(1)
}

// BuildDescription 拼接 call_agent 工具描述（启动期或测试期调用，运行时不再变化）。
func (r *SubAgentRegistry) BuildDescription() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("调用指定的子 Agent 执行任务。可用的子 Agent：\n\n")
	if len(r.entries) == 0 {
		sb.WriteString("（无可用子 Agent）\n")
	} else {
		names := make([]string, 0, len(r.entries))
		for n := range r.entries {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&sb, "- %s: %s\n", n, r.entries[n].Description)
		}
	}
	sb.WriteString("\n参数:\n  - agent_name: 子 Agent 名称（必填）\n  - task: 任务描述（必填）\n")
	return sb.String()
}

// Names 返回所有已注册子 Agent 名（按字典序）。
func (r *SubAgentRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for n := range r.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Close 关闭所有子 Agent 的 MCP 连接（main.go shutdown hook 调用）。
//
// 并发安全策略（修复 Task 6 reviewer 的 Important 反馈）：
//  1. 在锁内 detach：拿到 entries snapshot，并把字段重置为新空 map
//  2. 释放锁后才串行调用 MCPManager.Close()，避免持锁阻塞 reader
//
// 这样 shutdown 期间的 Get/Names/BuildDescription 立即看到「空注册表」，
// 不会再返回指向已关闭资源的 entry，消除 use-after-close 风险。
func (r *SubAgentRegistry) Close() error {
	r.mu.Lock()
	snapshot := r.entries
	r.entries = make(map[string]*SubAgentEntry)
	r.mu.Unlock()

	for _, e := range snapshot {
		if e.MCPManager != nil {
			e.MCPManager.Close()
		}
	}
	return nil
}

// parsedSubAgent 是 scanSubAgentDirs 的中间产物：
// 仅包含文件系统层面的解析结果，不涉及 LLM/MCP/Skill 等运行时资源。
// 把扫描层与构建层分离，使扫描层可独立测试。
type parsedSubAgent struct {
	name string   // 子 Agent 名（= 目录名，已校验非主 Agent 同名）
	dir  string   // 子 Agent 目录绝对路径
	md   *AgentMd // 已解析的 agent.md（description 必非空）
}

// scanSubAgentDirs 遍历 dir 下的一级子目录，对每个候选目录解析其中的 agent.md。
//
// 跳过规则（任一命中即跳过该目录，并记录 error 日志）：
//  1. 与主 Agent 同名（MainAgentName="groot"）
//  2. 缺失 agent.md 文件
//  3. parseAgentMd 失败（缺 description 等）
//
// 错误处理原则：dir 不存在时静默返回 nil（与 mcp.LoadAll 一致），
// 这是合法状态——用户尚未创建任何子 Agent。
func scanSubAgentDirs(dir string, log *logger.Logger) []parsedSubAgent {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		log.Error("read subagents dir failed: dir=" + dir + " err=" + err.Error())
		return nil
	}

	var result []parsedSubAgent
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 与主 Agent 同名的子目录直接跳过（防止冒名顶替）
		if name == MainAgentName {
			log.Error("subagent dir name conflicts with main agent, skip: name=" + name)
			continue
		}
		subDir := filepath.Join(dir, name)
		mdPath := filepath.Join(subDir, "agent.md")
		// 必须存在 agent.md
		if st, statErr := os.Stat(mdPath); statErr != nil || st.IsDir() {
			log.Error("subagent missing agent.md, skip: name=" + name)
			continue
		}
		md, parseErr := parseAgentMd(mdPath)
		if parseErr != nil {
			log.Error("parse agent.md failed, skip: name=" + name + " err=" + parseErr.Error())
			continue
		}
		result = append(result, parsedSubAgent{
			name: name,
			dir:  subDir,
			md:   md,
		})
	}
	return result
}

// BuildSubAgentRegistry 启动期一次性构建子 Agent 注册表。
//
// dir 通常是 {GROOT_HOME}/subagents。流程：
//  1. scanSubAgentDirs 文件系统遍历 + agent.md 解析
//  2. 对每个 parsedSubAgent 调 buildSubAgentEntry 装配 MCP/Skill/ChatModel/AgentTool
//  3. 任意子 Agent 构建失败仅跳过该项 + log.Error，不影响其他子 Agent
//
// 返回非 nil 的 *SubAgentRegistry（即使 entries 为空），供 main.go 注册到 Executor。
func BuildSubAgentRegistry(
	ctx context.Context,
	dir string,
	reactCfg config.ReactConfig,
	subCfg config.SubAgentConfig,
	llmCfg config.LLMConfig,
	log *logger.Logger,
) (*SubAgentRegistry, error) {
	reg := &SubAgentRegistry{
		entries: make(map[string]*SubAgentEntry),
		sem:     semaphore.NewWeighted(int64(maxOr(subCfg.MaxConcurrency, 5))),
		log:     log,
	}

	for _, p := range scanSubAgentDirs(dir, log) {
		entry, err := buildSubAgentEntry(ctx, p, reactCfg, llmCfg, log)
		if err != nil {
			log.Error("build subagent failed, skip: name=" + p.name + " err=" + err.Error())
			continue
		}
		reg.entries[p.name] = entry
	}
	return reg, nil
}

// buildSubAgentEntry 装配单个子 Agent 的运行时资源。任何步骤失败都返回错误，
// 由调用方 BuildSubAgentRegistry 负责跳过；本函数自身保证：
//   - 出错时已分配的 MCPManager 会被 Close（防资源泄漏）
//   - 出错时不会泄漏 ChatModelAgent 持有的 LLM client（adk.NewChatModelAgent
//     失败前不会 dial 远端，构造失败即整体失败，无需额外 cleanup）
//
// Skill/MCP 子目录均「专属」于该子 Agent；目录不存在视为合法状态：
//   - mcp.LoadAll 对不存在目录返回 nil（无需预创建）
//   - skillsDir 用 os.MkdirAll 兜底创建，避免 einoskill 扫描报错
func buildSubAgentEntry(
	ctx context.Context,
	p parsedSubAgent,
	reactCfg config.ReactConfig,
	llmCfg config.LLMConfig,
	log *logger.Logger,
) (*SubAgentEntry, error) {
	// 1. MCP Manager（专属，可空）
	mcpMgr := mcp.NewManager(log)
	mcpDir := filepath.Join(p.dir, "mcp")
	if err := mcpMgr.LoadAll(mcpDir); err != nil {
		return nil, fmt.Errorf("load mcp: %w", err)
	}

	// 2. Skill Backend（专属，可空）
	skillsDir := filepath.Join(p.dir, "skills")
	_ = os.MkdirAll(skillsDir, 0755)
	localBE, err := local.NewBackend(ctx, &local.Config{})
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("local backend: %w", err)
	}
	symBE := filesystem.NewSymlinkBackend(localBE)
	skillBK, err := einoskill.NewBackendFromFilesystem(ctx, &einoskill.BackendFromFilesystemConfig{
		Backend: symBE,
		BaseDir: skillsDir,
	})
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("skill backend: %w", err)
	}
	skillMW, err := einoskill.NewMiddleware(ctx, &einoskill.Config{Backend: skillBK})
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("skill middleware: %w", err)
	}

	// 3. 模型：agent.md.model（若指定） → llm.default_model
	modelName := p.md.Model
	if modelName == "" {
		modelName = llmCfg.DefaultModel
	}
	stepTimeout := time.Duration(reactCfg.StepTimeout) * time.Second
	chatModel, err := llm.NewChatModel(ctx, llmCfg, modelName, stepTimeout)
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("chat model: %w", err)
	}

	// 4. ChatModelAgent：包装为可被父 Agent 调用的 agent tool
	maxIter := reactCfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	agentCfg := &adk.ChatModelAgentConfig{
		Name:          p.name,
		Description:   p.md.Description,
		Instruction:   p.md.Content,
		Model:         chatModel,
		MaxIterations: maxIter,
		Handlers:      []adk.ChatModelAgentMiddleware{skillMW},
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: mcpMgr.GetTools()},
			// 叶子节点（子 Agent）不需要 EmitInternalEvents，由父 Agent 透出
		},
	}
	if reactCfg.ErrorRetry > 0 {
		agentCfg.ModelRetryConfig = &adk.ModelRetryConfig{MaxRetries: reactCfg.ErrorRetry}
	}
	cmAgent, err := adk.NewChatModelAgent(ctx, agentCfg)
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("chat model agent: %w", err)
	}

	// 5. 包装为 agent tool。adk.NewAgentTool 返回 tool.BaseTool，
	// 但 SubAgentEntry.Tool 是 tool.InvokableTool，做安全断言。
	agentTool := adk.NewAgentTool(ctx, cmAgent)
	invokableTool, ok := agentTool.(tool.InvokableTool)
	if !ok {
		mcpMgr.Close()
		return nil, fmt.Errorf("agent tool does not implement InvokableTool")
	}

	return &SubAgentEntry{
		Name:        p.name,
		Description: p.md.Description,
		Instruction: p.md.Content,
		Tool:        invokableTool,
		MCPManager:  mcpMgr,
		SkillBK:     skillBK,
	}, nil
}

// maxOr 返回 v 与 fallback 中较大的非零值。
// 用于子 Agent 配置项的兜底——如 SubAgentConfig.MaxConcurrency=0 时回退到 5。
// applyDefaults 路径下 v 通常已 >= 5，但 DefaultConfig 直接构造的路径需要兜底。
func maxOr(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
