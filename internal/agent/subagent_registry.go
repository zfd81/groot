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
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/filesystem"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
)

// SubAgentEntry 单个子 Agent 的运行时数据（启动期一次性构建，运行时只读）。
//
// v3.8 架构变更：不再预构建 ChatModel/ChatModelAgent/AgentTool。原因——
// 启动期构建的 ChatModel 在运行时无法跟随父 Agent 当前 model 切换，导致
// 主 Agent 切到新 model 时子 Agent 仍然连旧的（默认）端点。改为每次
// call_agent 调用时由 BuildAgentTool 按实际 modelName 现场组装 ChatModel
// + ChatModelAgent + AgentTool。
//
// model 选择优先级（在 BuildAgentTool 内实现）：
//  1. AgentMdModel —— agent.md 显式声明（钉死特定模型，覆盖一切）
//  2. parent modelName —— 父任务运行时 model（编排模式默认行为）
//  3. llmCfg.DefaultModel —— 配置默认值兜底
type SubAgentEntry struct {
	Name        string
	Description string
	Instruction string            // agent.md 正文，Solo 模式 + BuildAgentTool 都使用
	MCPManager  *mcp.Manager      // 持有 MCP 连接生命周期
	SkillBK     einoskill.Backend // 供 /agents、/skills 查询；Watcher 热更新入口

	// 构建子 ChatModelAgent 所需的纯配置；BuildAgentTool 每次现场用这些拼装。
	AgentMdModel  string                       // agent.md 中显式声明的 model；空字符串表示跟随父 Agent
	MaxIterations int                          // 已应用默认值（>=1）
	RetryConfig   *adk.ModelRetryConfig        // 可空
	SkillMW       adk.ChatModelAgentMiddleware // 已构建的 skill middleware；可空
	StepTimeout   time.Duration                // 单步 LLM 调用超时
	LLMCfg        config.LLMConfig             // ChatModel 实例化材料

	// testTool 仅供 _test.go 注入预制 InvokableTool 跳过 BuildAgentTool 的真实
	// LLM dial。生产路径绝不写入；BuildAgentTool 在非 nil 时直接返回它。
	testTool tool.InvokableTool
}

// SubAgentRegistry 全局单例：所有子 Agent 的注册表 + 并发控制。
type SubAgentRegistry struct {
	entries map[string]*SubAgentEntry
	sem     *semaphore.Weighted
	log     *logger.Logger
	mu      sync.RWMutex
}

// NewRegistryForTest 仅供 _test.go 文件使用，跳过启动期扫描。
func NewRegistryForTest(maxConc int) *SubAgentRegistry {
	if maxConc <= 0 {
		maxConc = 1
	}
	return &SubAgentRegistry{
		entries: make(map[string]*SubAgentEntry),
		sem:     semaphore.NewWeighted(int64(maxConc)),
	}
}

// SetEntryForTest 仅供测试代码注入预构建 SubAgentEntry，跳过启动期扫描与
// MCP/Skill 后端构建。生产路径绝不调用，命名以 ForTest 结尾警示越权。
func (r *SubAgentRegistry) SetEntryForTest(name string, e *SubAgentEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[name] = e
}

// SetToolForTest 仅供测试：把预制的 InvokableTool 注入到 SubAgentEntry，
// 让 BuildAgentTool 直接返回它而不去 dial 真实 LLM。
// 生产路径绝不调用，命名以 ForTest 结尾警示越权。
func (e *SubAgentEntry) SetToolForTest(t tool.InvokableTool) {
	e.testTool = t
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
//  2. 不是目录（含「指向目录的符号链接被解析失败」）
//  3. 缺失 agent.md 文件
//  4. parseAgentMd 失败（缺 description 等）
//
// 关于符号链接：os.DirEntry.IsDir() 对「指向目录的符号链接」返回 false，
// 因此通过 os.Stat 解析后再判定，使 `ln -s` 共享子 Agent 模板的常见用法生效。
//
// 错误处理原则：dir 不存在时静默返回 nil（与 mcp.LoadAll 一致），
// 这是合法状态——用户尚未创建任何子 Agent。
func scanSubAgentDirs(dir string, log *logger.Logger) []parsedSubAgent {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		log.Error("read subagents dir failed", zap.String("dir", dir), zap.Error(err))
		return nil
	}

	var result []parsedSubAgent
	for _, entry := range entries {
		name := entry.Name()
		subDir := filepath.Join(dir, name)
		// 用 os.Stat 而非 entry.IsDir()，让指向目录的符号链接也能被识别
		info, statErr := os.Stat(subDir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		// 与主 Agent 同名的子目录直接跳过（防止冒名顶替）
		if name == MainAgentName {
			log.Error("subagent dir name conflicts with main agent, skip", zap.String("name", name))
			continue
		}
		mdPath := filepath.Join(subDir, "agent.md")
		// 必须存在 agent.md
		if st, statErr := os.Stat(mdPath); statErr != nil || st.IsDir() {
			log.Error("subagent missing agent.md, skip", zap.String("name", name))
			continue
		}
		md, parseErr := parseAgentMd(mdPath)
		if parseErr != nil {
			log.Error("parse agent.md failed, skip", zap.String("name", name), zap.Error(parseErr))
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
// 设计原则：所有错误（含目录不存在、单个子 Agent 构建失败）在内部消化，
// 总是返回非 nil 的 *SubAgentRegistry（即使 entries 为空），供 main.go 注册到 Executor。
// 因此不返回 error——避免调用方写出永远为死代码的 if err != nil。
func BuildSubAgentRegistry(
	ctx context.Context,
	dir string,
	reactCfg config.ReactConfig,
	subCfg config.SubAgentConfig,
	llmCfg config.LLMConfig,
	log *logger.Logger,
) *SubAgentRegistry {
	reg := &SubAgentRegistry{
		entries: make(map[string]*SubAgentEntry),
		sem:     semaphore.NewWeighted(int64(max(subCfg.MaxConcurrency, 5))),
		log:     log,
	}

	for _, p := range scanSubAgentDirs(dir, log) {
		entry, err := buildSubAgentEntry(ctx, p, reactCfg, llmCfg, log)
		if err != nil {
			log.Error("build subagent failed, skip", zap.String("name", p.name), zap.Error(err))
			continue
		}
		reg.entries[p.name] = entry
	}
	return reg
}

// buildSubAgentEntry 装配单个子 Agent 的运行时材料。任何步骤失败都返回错误，
// 由调用方 BuildSubAgentRegistry 负责跳过；本函数自身保证：
//   - 出错时已分配的 MCPManager 会被 Close（防资源泄漏）
//
// v3.8 架构：不再启动期构建 ChatModel/ChatModelAgent/AgentTool。这些资源
// 由 BuildAgentTool 在每次 call_agent 调用时按运行时 modelName 现场组装，
// 以让子 Agent 在编排模式下自动跟随父 Agent 当前选定的 model。
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
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("create skills dir: %w", err)
	}
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

	// 3. ChatModelAgent 装配材料（不立即构建——见 SubAgentEntry 注释）
	maxIter := reactCfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	var retryCfg *adk.ModelRetryConfig
	if reactCfg.ErrorRetry > 0 {
		retryCfg = &adk.ModelRetryConfig{MaxRetries: reactCfg.ErrorRetry}
	}
	stepTimeout := time.Duration(reactCfg.StepTimeout) * time.Second

	return &SubAgentEntry{
		Name:          p.name,
		Description:   p.md.Description,
		Instruction:   p.md.Content,
		MCPManager:    mcpMgr,
		SkillBK:       skillBK,
		AgentMdModel:  p.md.Model,
		MaxIterations: maxIter,
		RetryConfig:   retryCfg,
		SkillMW:       skillMW,
		StepTimeout:   stepTimeout,
		LLMCfg:        llmCfg,
	}, nil
}

// BuildAgentTool 按运行时 parentModelName 为子 Agent 现场组装一次 ChatModel +
// ChatModelAgent + AgentTool。每次 call_agent 调用都重新构造，以保证子 Agent
// 跟随主 Agent 当前选定的 model（除非 agent.md 显式声明 model）。
//
// model 选择优先级：
//  1. e.AgentMdModel    — agent.md 显式声明（钉死特定模型）
//  2. parentModelName   — 父任务运行时 model（编排模式默认）
//  3. e.LLMCfg.DefaultModel — 配置默认值兜底
//
// BuildAgentTool 构造子 Agent 工具实例，model 选择优先级：
//  1. e.AgentMdModel    — agent.md 显式声明（钉死特定模型）
//  2. parentModelName   — 父任务运行时 model（编排模式默认）
//  3. e.LLMCfg.DefaultModel — 配置默认值兜底
//
// 返回 InvokableTool 供 call_agent.InvokableRun 直接调用。
func (e *SubAgentEntry) BuildAgentTool(ctx context.Context, parentModelName string, extraTools ...tool.BaseTool) (tool.InvokableTool, error) {
	// 单测注入路径：跳过真实 LLM 构造
	if e.testTool != nil {
		return e.testTool, nil
	}

	modelName := e.AgentMdModel
	if modelName == "" {
		modelName = parentModelName
	}
	if modelName == "" {
		modelName = e.LLMCfg.DefaultModel
	}

	chatModel, err := llm.NewChatModel(ctx, e.LLMCfg, modelName, e.StepTimeout)
	if err != nil {
		return nil, fmt.Errorf("chat model: %w", err)
	}

	// 子 Agent 工具集 = MCP 工具 + 调用方透传的额外工具（可空）。
	tools := append([]tool.BaseTool{}, extraTools...)
	tools = append(tools, e.MCPManager.GetTools()...)

	agentCfg := &adk.ChatModelAgentConfig{
		Name:          e.Name,
		Description:   e.Description,
		Instruction:   e.Instruction,
		Model:         chatModel,
		MaxIterations: e.MaxIterations,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
			// 叶子节点（子 Agent）不需要 EmitInternalEvents，由父 Agent 透出
		},
	}
	if e.SkillMW != nil {
		agentCfg.Handlers = []adk.ChatModelAgentMiddleware{e.SkillMW}
	}
	if e.RetryConfig != nil {
		agentCfg.ModelRetryConfig = e.RetryConfig
	}
	cmAgent, err := adk.NewChatModelAgent(ctx, agentCfg)
	if err != nil {
		return nil, fmt.Errorf("chat model agent: %w", err)
	}

	agentTool := adk.NewAgentTool(ctx, cmAgent)
	invokableTool, ok := agentTool.(tool.InvokableTool)
	if !ok {
		return nil, fmt.Errorf("agent tool does not implement InvokableTool")
	}
	return invokableTool, nil
}
