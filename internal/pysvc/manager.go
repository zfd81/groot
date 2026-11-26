package pysvc

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/logger"
)

// 进程未就绪时的状态说明。
const (
	// noServiceReason 是没有任何有效子服务时的进程状态说明。
	noServiceReason = "没有任何有效子服务"
	// missingResultReason 是就绪行没有报告某个子服务加载结果时的说明。
	missingResultReason = "启动器未报告加载结果"
	// failedResultReason 是加载结果只有 error 前缀、没有具体原因时的说明。
	failedResultReason = "启动器报告加载失败"
)

// Options 是构造 Manager 所需的运行参数。
type Options struct {
	HomeDir  string
	Log      *logger.Logger
	LogLevel string
}

// Manager 编排 Python 服务：发现子服务、按需拉起并监督 Python 进程、解析子服务权限、
// 提供代理 handler 与状态汇总。发现与配置加载在构造时完成，进程在 Start 时拉起。
type Manager struct {
	opts        Options
	log         *logger.Logger
	servicesDir string
	interpreter string
	cfg         GlobalConfig
	services    []Service
	runner      childRunner

	mu      sync.Mutex
	sup     *supervisor
	process ProcessStatus
	started bool
	stopped bool
}

// NewManager 构造 Manager：读取 services/service.yaml（失败时记录错误并使用默认值），
// 发现子服务并保存解释器路径与全局配置，但不启动任何进程。
func NewManager(opts Options) *Manager {
	log := opts.Log
	if log == nil {
		log = logger.NewNop()
	}

	servicesDir := ServicesDir(opts.HomeDir)
	cfg, err := LoadGlobalConfig(filepath.Join(servicesDir, "service.yaml"))
	if err != nil {
		log.Error("加载 Python 服务全局配置失败，使用默认值", zap.Error(err))
		cfg = DefaultGlobalConfig()
	}

	m := &Manager{
		opts:        opts,
		log:         log,
		servicesDir: servicesDir,
		interpreter: InterpreterPath(servicesDir),
		cfg:         cfg,
		services:    Discover(servicesDir, cfg),
	}
	known := make(map[string]bool, len(m.services))
	for _, svc := range m.services {
		known[svc.Name] = true
	}
	for name := range cfg.Permission {
		if !known[name] {
			log.Warn("Python 服务权限配置中的子服务不存在，该项不会生效",
				zap.String("service", name))
		}
	}
	for _, svc := range m.services {
		if svc.Invalid != "" {
			// 无效目录不注册路由，记录原因便于使用者修复。
			log.Error("Python 子服务不可用",
				zap.String("service", svc.Name),
				zap.String("reason", svc.Invalid))
			continue
		}
		if p, ok := cfg.Permission[svc.Name]; ok && !ValidPermission(p) {
			// Discover 已回退为 all，这里提示配置写错。
			log.Warn("Python 子服务权限点未知，已回退为 all",
				zap.String("service", svc.Name),
				zap.String("permission", p))
		}
	}
	if len(m.validServices()) == 0 {
		m.process = ProcessStatus{Status: "disabled", Error: m.disabledReason()}
	} else {
		m.process = ProcessStatus{Status: "starting"}
	}
	return m
}

// newManagerWithRunner 构造注入自定义子进程拉起实现的 Manager，供测试使用。
func newManagerWithRunner(opts Options, runner childRunner) *Manager {
	m := NewManager(opts)
	m.runner = runner
	return m
}

// Services 返回全部已识别的子服务（含无效项）的副本。
func (m *Manager) Services() []Service {
	out := make([]Service, len(m.services))
	copy(out, m.services)
	return out
}

// ServicePermission 返回子服务声明的权限点；名字不存在或子服务无效时 ok 为 false。
func (m *Manager) ServicePermission(name string) (string, bool) {
	for _, svc := range m.services {
		if svc.Name != name {
			continue
		}
		if svc.Invalid != "" {
			return "", false
		}
		return svc.Permission, true
	}
	return "", false
}

// Proxy 返回转发 /services/<name> 的 handler。名字不合法或未注册时返回直接 404 的
// handler，与访问任意不存在路径的行为一致。
func (m *Manager) Proxy(name string) app.HandlerFunc {
	if _, ok := m.ServicePermission(name); !ok {
		return func(_ context.Context, rc *app.RequestContext) {
			errbody.Write(rc, http.StatusNotFound,
				"接口不存在："+string(rc.URI().Path()),
				errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		}
	}
	return newProxy(name, m.target, m.cfg.RequestTimeout, m.log, m.token)
}

// Start 拉起 Python 服务进程。没有有效子服务或共享解释器缺失时保持停用，不解压 SDK
// 也不拉起进程；SDK 安装失败时进程状态为 failed。重复调用只有第一次生效。
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started || m.stopped {
		m.mu.Unlock()
		return
	}
	m.started = true
	if len(m.validServices()) == 0 {
		m.process = ProcessStatus{Status: "disabled", Error: m.disabledReason()}
		m.mu.Unlock()
		return
	}
	interpreter := m.interpreter
	homeDir := m.opts.HomeDir
	m.mu.Unlock()

	if info, err := os.Stat(interpreter); err != nil || info.IsDir() {
		reason := fmt.Sprintf("共享解释器缺失: %s（请运行 groot services init 创建虚拟环境并安装依赖）", interpreter)
		m.fail("disabled", reason)
		m.log.Error("未找到共享 Python 解释器，Python 服务保持停用",
			zap.String("interpreter", interpreter))
		return
	}

	sdkPath, err := ExtractSDK(homeDir)
	if err != nil {
		m.fail("failed", fmt.Sprintf("安装 Python SDK 失败: %v", err))
		m.log.Error("安装 Python SDK 失败，Python 服务启动失败", zap.Error(err))
		return
	}

	sup := newSupervisor(supervisorConfig{
		ServicesDir: m.servicesDir,
		Interpreter: interpreter,
		SDKPath:     sdkPath,
		Services:    m.validServices(),
		Config:      m.cfg,
		Log:         m.log,
		LogLevel:    m.opts.LogLevel,
		Runner:      m.runner,
	})

	m.mu.Lock()
	m.sup = sup
	// 进程状态改由 supervisor 提供，清空启动前的占位状态。
	m.process = ProcessStatus{}
	stopped := m.stopped
	m.mu.Unlock()

	if stopped {
		// Stop 已在监督器创建前返回：此时 run 从未开始，stop 是安全且幂等的。
		sup.stop()
		return
	}
	go sup.run(ctx)
}

// fail 在监督器创建前记录启动结果，进程状态说明同时作为有效子服务的失败原因。
func (m *Manager) fail(status, reason string) {
	m.mu.Lock()
	m.process = ProcessStatus{Status: status, Error: reason}
	m.mu.Unlock()
}

// Stop 停止监督器并等待其结束。可在 Start 之前调用，也可重复调用；
// supervisor.stop 自身最多等待 10 秒。
func (m *Manager) Stop(ctx context.Context) {
	m.mu.Lock()
	m.stopped = true
	sup := m.sup
	m.mu.Unlock()
	if sup != nil {
		// 具体等待上限由 supervisor.stop 的 stopTimeout 约束，ctx 仅表示调用方意图。
		sup.stop()
	}
}

// Status 合并发现结果与监督器状态，返回进程与全部子服务的状态快照。
func (m *Manager) Status() Status {
	m.mu.Lock()
	sup := m.sup
	process := m.process
	services := m.services
	m.mu.Unlock()

	var results map[string]string
	if sup != nil {
		process = sup.snapshot()
		// 进程未运行时残留的加载结果不可信，只按进程状态归类子服务。
		if process.Status == "running" {
			results = sup.serviceResults()
		}
	}

	out := make([]ServiceStatus, 0, len(services))
	for _, svc := range services {
		out = append(out, mergeServiceStatus(svc, process, results, sup == nil))
	}
	return Status{Process: process, Services: out}
}

// mergeServiceStatus 根据发现结果、进程状态与加载结果得出单个子服务状态。
// preSupervisor 表示监督器尚未创建：此时的进程失败（解释器缺失、SDK 安装失败）
// 直接作为有效子服务的失败原因。
func mergeServiceStatus(svc Service, process ProcessStatus, results map[string]string, preSupervisor bool) ServiceStatus {
	st := ServiceStatus{Name: svc.Name}
	switch {
	case svc.Invalid != "":
		st.Status = "invalid"
		st.Error = svc.Invalid
	case process.Status == "running":
		res, ok := results[svc.Name]
		switch {
		case !ok:
			st.Status = "failed"
			st.Error = missingResultReason
		case res == "ok":
			st.Status = "running"
		default:
			st.Status = "failed"
			st.Error = loadResultReason(res)
		}
	case preSupervisor && process.Error != "" &&
		(process.Status == "disabled" || process.Status == "failed"):
		st.Status = "failed"
		st.Error = process.Error
	default:
		st.Status = "pending"
	}
	return st
}

// loadResultReason 把就绪行中的加载结果转成展示用原因，去掉 error: 前缀。
func loadResultReason(res string) string {
	reason := strings.TrimSpace(strings.TrimPrefix(res, "error:"))
	if reason == "" {
		return failedResultReason
	}
	return reason
}

// target 返回当前可代理的环回端口；监督器未创建或进程未就绪时 ok 为 false。
func (m *Manager) target() (int, bool) {
	if sup := m.currentSupervisor(); sup != nil {
		return sup.target()
	}
	return 0, false
}

// token 返回当前子进程的内部令牌；监督器未创建时为空串。
func (m *Manager) token() string {
	if sup := m.currentSupervisor(); sup != nil {
		return sup.token()
	}
	return ""
}

// currentSupervisor 返回当前监督器，尚未创建时为 nil。
func (m *Manager) currentSupervisor() *supervisor {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sup
}

// disabledReason 返回进程停用时的状态说明：目录不存在时提示先初始化；路径存在但
// 不是目录、或无法访问时说明具体原因；正常目录则说明没有任何有效子服务。
func (m *Manager) disabledReason() string {
	info, err := os.Stat(m.servicesDir)
	switch {
	case err == nil && info.IsDir():
		return noServiceReason
	case err == nil:
		return fmt.Sprintf("Python 服务目录 %s 不是目录（请检查配置，或运行 groot services init）", m.servicesDir)
	case os.IsNotExist(err):
		return fmt.Sprintf("Python 服务未初始化: 目录 %s 不存在（请运行 groot services init）", m.servicesDir)
	default:
		return fmt.Sprintf("无法访问 Python 服务目录 %s: %v（请检查权限，或运行 groot services init）", m.servicesDir, err)
	}
}

// validServices 返回通过目录校验的子服务。
func (m *Manager) validServices() []Service {
	out := make([]Service, 0, len(m.services))
	for _, svc := range m.services {
		if svc.Invalid == "" {
			out = append(out, svc)
		}
	}
	return out
}
