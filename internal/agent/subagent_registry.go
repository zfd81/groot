// Package agent 子 Agent 注册表与并发控制。
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"golang.org/x/sync/semaphore"

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
func (r *SubAgentRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, e := range r.entries {
		if e.MCPManager != nil {
			e.MCPManager.Close()
		}
		_ = name
	}
	return nil
}
