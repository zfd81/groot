package skills

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// 事件分类常量；避免在 classifySkillChange/debounce 中使用裸字面量。
const (
	skillKindMain     = "main"
	skillKindSubAgent = "subagent"
)

// Watcher 监听主 Agent 与子 Agent skills 目录变化，并在 debounce 后触发对应回调。
//
// 路径分流策略：
//   - 主 Agent skills（skillsDir 下）→ reload()
//   - 子 Agent skills（subAgentsBaseDir/<name>/skills/ 下）→ onSubAgentSkillReload(<name>)
//   - 子 Agent 其它文件（agent.md、mcp/*）→ 不感兴趣，事件丢弃
type Watcher struct {
	skillsDir             string
	subAgentsBaseDir      string // 主 Agent subagents/ 目录；空字符串则不监听子 Agent
	fsWatcher             *fsnotify.Watcher
	stopChan              chan struct{}
	log                   *logger.Logger
	cfg                   config.HotReloadConfig
	debounceTimer         *time.Timer
	mu                    sync.Mutex
	onSubAgentSkillReload func(agentName string) // 子 Agent skill 变更回调；nil 时仅 log
}

// NewWatcher 创建 Watcher。
//
// 参数：
//   - skillsDir：主 Agent skills 目录（通常 ~/.groot/skills）
//   - subAgentsBaseDir：子 Agent 根目录（通常 ~/.groot/subagents）；传空串则不监听子 Agent
//   - cfg：热插拔配置（含开关与 debounce 延迟）
//   - log：日志器
//   - onSubAgentSkillReload：子 Agent skills 变更触发的回调；可传 nil
func NewWatcher(skillsDir, subAgentsBaseDir string, cfg config.HotReloadConfig, log *logger.Logger,
	onSubAgentSkillReload func(string)) *Watcher {
	return &Watcher{
		skillsDir:             skillsDir,
		subAgentsBaseDir:      subAgentsBaseDir,
		stopChan:              make(chan struct{}),
		log:                   log,
		cfg:                   cfg,
		onSubAgentSkillReload: onSubAgentSkillReload,
	}
}

// Start 启动 watcher，注册主 skills 目录与所有 subagents/*/skills/ 子目录。
//
// 子目录可能在启动时尚未存在；缺失视为非错误，仅 log 后跳过。
func (w *Watcher) Start() error {
	if !w.cfg.Enabled {
		w.log.Info("Skills 热插拔已禁用")
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		w.log.Error("无法创建 Skills watcher", zap.Error(err))
		return err
	}
	w.fsWatcher = watcher

	if err := watcher.Add(w.skillsDir); err != nil {
		w.log.Error("无法监听 Skills 目录", zap.String("dir", w.skillsDir), zap.Error(err))
		watcher.Close()
		w.fsWatcher = nil
		return err
	}

	// 同时监听 subagents/*/skills/——目录可能在启动时尚未存在；缺失视为非错误。
	if w.subAgentsBaseDir != "" {
		if subDirs, globErr := filepath.Glob(filepath.Join(w.subAgentsBaseDir, "*/skills")); globErr == nil {
			for _, d := range subDirs {
				if addErr := watcher.Add(d); addErr != nil {
					w.log.Info("无法监听子 Agent skills 目录（忽略，不阻塞启动）",
						zap.String("dir", d), zap.Error(addErr))
					continue
				}
			}
		}
	}

	go w.run()
	w.log.Info("Skills watcher 已启动",
		zap.String("dir", w.skillsDir),
		zap.String("sub_agents_dir", w.subAgentsBaseDir))
	return nil
}

// Stop 停止 watcher。重复调用安全。
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	select {
	case <-w.stopChan:
		return
	default:
		close(w.stopChan)
	}

	if w.fsWatcher != nil {
		w.fsWatcher.Close()
	}
	w.log.Info("Skills watcher 已停止")
}

// run 处理文件事件，按分类丢入 debounce。
func (w *Watcher) run() {
	for {
		select {
		case <-w.stopChan:
			return
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			if kind, agentName := w.classifySkillChange(event); kind != "" {
				w.debounce(kind, agentName)
			}
		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			w.log.Error("Skills watcher 错误", zap.Error(err))
		}
	}
}

// classifySkillChange 解析事件路径决定它属于：
//   - "main": 主 Agent skills 目录下的 .md 变更
//   - "subagent": 子 Agent skills 目录下的 .md 变更，agentName 为子 Agent 名
//   - "": 不感兴趣（agent.md / mcp/ / 非 .md 等）
//
// 触发条件：SKILL.md 直接命中，或目录创建/删除/重命名（用户安装/卸载 skill 时常见）。
func (w *Watcher) classifySkillChange(event fsnotify.Event) (kind string, agentName string) {
	// 关心的事件：SKILL.md 文件变更，或目录级别的 Create/Remove/Rename
	base := filepath.Base(event.Name)
	isSkillFile := base == "SKILL.md"
	isStructuralOp := event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0
	if !isSkillFile && !isStructuralOp {
		return "", ""
	}

	// 优先判断子 Agent 路径（更具体）
	if w.subAgentsBaseDir != "" {
		if name, ok := extractSubAgentNameForSkill(event.Name, w.subAgentsBaseDir); ok {
			return skillKindSubAgent, name
		}
	}
	// 主 Agent skills 目录（边界用 Rel 判断，避免 /x/skillsbackup 误命中）
	if w.skillsDir != "" {
		if rel, err := filepath.Rel(w.skillsDir, event.Name); err == nil && !strings.HasPrefix(rel, "..") {
			return skillKindMain, ""
		}
	}
	return "", ""
}

// extractSubAgentNameForSkill 判断事件路径是否属于 subagents/<name>/skills/ 下：
//   - 是：返回 <name>, true
//   - 否（含 subagents/<n>/agent.md、subagents/<n>/mcp/、非 subagents 路径）：返回 "", false
//
// 路径形态：path 必须 rel 到 subAgentsBaseDir 之后形如 "<name>/skills/..."。
// 跨目录边界（rel 以 ".." 开头）一律视为不在 base 下。
func extractSubAgentNameForSkill(path, subAgentsBaseDir string) (string, bool) {
	if path == "" || subAgentsBaseDir == "" {
		return "", false
	}
	rel, err := filepath.Rel(subAgentsBaseDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	// 形态: <agent>/skills/... 至少 3 段
	if len(parts) < 3 || parts[1] != "skills" {
		return "", false
	}
	return parts[0], true
}

// debounce 在 cfg.DebounceDelay 时间窗内合并多次事件，到点触发对应回调。
// kind="main" → 触发主 Agent reload；kind="subagent" → 调用 onSubAgentSkillReload；
// 同一窗口内若先后来主/子事件，**最后一次胜出**（替换 timer），符合「就近一次」语义。
func (w *Watcher) debounce(kind, agentName string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(time.Duration(w.cfg.DebounceDelay)*time.Second, func() {
		switch kind {
		case skillKindMain:
			w.reload()
		case skillKindSubAgent:
			if w.onSubAgentSkillReload != nil {
				w.onSubAgentSkillReload(agentName)
			} else {
				w.log.Info("子 Agent skills 变更（无回调）", zap.String("agent", agentName))
			}
		}
	})
}

// reload 重扫主 Agent skills 目录并记录事件。
func (w *Watcher) reload() {
	entries, err := filepath.Glob(filepath.Join(w.skillsDir, "*/SKILL.md"))
	if err != nil {
		w.log.Error("Skills 重载失败", zap.Error(err))
		return
	}

	count := len(entries)
	w.log.LogSkillHotReload("reloaded", "", count)
}

// NewSubAgentReloadCallback 构造一个标准的子 Agent skills 重载回调；
// nameKnown 用于过滤未注册的子 Agent（通常传 subAgentReg.Get 的适配器）；
// 第一期仅记录日志，真实 backend rescan 由后续优化补齐。
func NewSubAgentReloadCallback(log *logger.Logger, nameKnown func(string) bool) func(string) {
	return func(agentName string) {
		if nameKnown != nil && !nameKnown(agentName) {
			return
		}
		log.Info("子 Agent skills 变更触发重新加载（暂未实现 backend rescan）",
			zap.String("agent", agentName))
	}
}
