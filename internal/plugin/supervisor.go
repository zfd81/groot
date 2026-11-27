package plugin

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// readyPrefix 是子进程就绪行的前缀，其后紧跟一段 JSON。
const readyPrefix = "GROOT_PLUGIN_READY "

const (
	// internalTokenBytes 是内部令牌的随机字节数，十六进制编码后为 64 个字符。
	internalTokenBytes = 32
	// defaultStopTimeout 是优雅停止的等待上限，超时后强制杀死子进程。
	defaultStopTimeout = 10 * time.Second

	// pumpDrainTimeout 是子进程退出后等待 stdout/stderr 读完剩余输出的上限。
	pumpDrainTimeout = 2 * time.Second
	// scannerInitialBuf 是扫描子进程输出行的初始缓冲大小。
	scannerInitialBuf = 64 * 1024
	// scannerMaxBuf 是单行输出保留上限；更长的行被截断，但管道继续被读取。
	scannerMaxBuf = 1 << 20
)

// defaultBackoff 是重启退避序列，用尽后保持最后一档。
var defaultBackoff = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
}

const (
	// defaultFastFailWindow 是判定快速失败的存活时间阈值。
	defaultFastFailWindow = 60 * time.Second
	// defaultMaxFastFails 是连续快速失败后放弃重启的次数。
	defaultMaxFastFails = 10
)

// ProcessStatus 是 Python 插件进程的状态快照。
type ProcessStatus struct {
	Status    string `json:"status"`
	PID       int    `json:"pid,omitempty"`
	Port      int    `json:"port,omitempty"`
	Restarts  int    `json:"restarts"`
	StartedAt string `json:"started_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

// launchSpec 是拉起子进程所需的全部参数。
type launchSpec struct {
	Interpreter   string
	WorkDir       string
	SDKPath       string
	LogLevel      string
	Plugins       []string
	InternalToken string
}

// childProcess 是运行中的子进程抽象；生产实现由 execRunner 提供，测试注入假实现。
type childProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	Wait() error
	// Terminate 请求子进程优雅停止（Unix 发送 SIGTERM，Windows 不发信号）；
	// 进程已被 Wait 回收时必须安全返回，不得向可能被复用的 pid 发信号。
	Terminate() error
	Kill() error
	Pid() int
}

// childRunner 拉起一个子进程。
type childRunner interface {
	Start(ctx context.Context, spec launchSpec) (childProcess, error)
}

// supervisorConfig 是监督循环的全部输入。
type supervisorConfig struct {
	PluginsDir  string
	Interpreter string
	SDKPath     string
	Plugins     []Plugin
	Config      GlobalConfig
	Log         *logger.Logger
	LogLevel    string
	Runner      childRunner

	// 以下字段生产环境留空取默认值，测试注入小值加速。
	Backoff        []time.Duration
	FastFailWindow time.Duration
	MaxFastFails   int
	Sleep          func(time.Duration)
	Now            func() time.Time
	// StopTimeout 是优雅停止的等待上限（SIGTERM 后等待，再 Kill 后等待各一次）。
	StopTimeout time.Duration
}

// readyMessage 是就绪行的 JSON 结构。
type readyMessage struct {
	Port    int               `json:"port"`
	PID     int               `json:"pid"`
	SDK     string            `json:"sdk"`
	Plugins map[string]string `json:"plugins"`
}

// startSignal 是 stdout 扫描协程交给监督循环的就绪结果。
type startSignal struct {
	ready readyMessage
	err   error
}

// attemptOutcome 描述一次子进程生命周期的结束方式。
type attemptOutcome struct {
	stopped bool
	err     error
}

// supervisor 负责一个 Python 插件的拉起、就绪握手、崩溃重启与优雅停止。
type supervisor struct {
	cfg    supervisorConfig
	log    *logger.Logger
	runner childRunner

	backoff        []time.Duration
	fastFailWindow time.Duration
	maxFastFails   int
	stopTimeout    time.Duration
	sleep          func(time.Duration)
	now            func() time.Time

	stopCh   chan struct{}
	stopOnce sync.Once
	runDone  chan struct{}
	started  atomic.Bool

	mu            sync.Mutex
	status        ProcessStatus
	results       map[string]string
	internalToken string
	child         childProcess
	stdin         io.WriteCloser
}

// newSupervisor 构造一个尚未启动的监督器。
func newSupervisor(cfg supervisorConfig) *supervisor {
	s := &supervisor{
		cfg:            cfg,
		log:            cfg.Log,
		runner:         cfg.Runner,
		backoff:        append([]time.Duration(nil), cfg.Backoff...),
		fastFailWindow: cfg.FastFailWindow,
		maxFastFails:   cfg.MaxFastFails,
		stopTimeout:    cfg.StopTimeout,
		sleep:          cfg.Sleep,
		now:            cfg.Now,
		stopCh:         make(chan struct{}),
		runDone:        make(chan struct{}),
		status:         ProcessStatus{Status: "starting"},
		results:        map[string]string{},
	}
	if s.log == nil {
		s.log = logger.NewNop()
	}
	if s.runner == nil {
		s.runner = execRunner{}
	}
	if len(s.backoff) == 0 {
		s.backoff = defaultBackoff
	}
	if s.fastFailWindow <= 0 {
		s.fastFailWindow = defaultFastFailWindow
	}
	if s.maxFastFails <= 0 {
		s.maxFastFails = defaultMaxFastFails
	}
	if s.stopTimeout <= 0 {
		s.stopTimeout = defaultStopTimeout
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// run 是监督循环：拉起子进程、等待就绪、退出后按退避重启，直到 ctx 结束、stop 被调用
// 或连续快速失败达到上限。重复调用 run 只有第一次生效。
func (s *supervisor) run(ctx context.Context) {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	defer close(s.runDone)

	if s.isStopping(ctx) {
		s.markStopped()
		return
	}
	s.mu.Lock()
	s.status.Status = "starting"
	s.mu.Unlock()

	backoffAttempt := 0
	fastFails := 0
	for {
		if s.isStopping(ctx) {
			s.markStopped()
			return
		}

		startedAt := s.now()
		out := s.runOnce(ctx)
		// stop/ctx 驱动的退出不是崩溃：不计数、不重启，直接收尾。
		if out.stopped || s.isStopping(ctx) {
			s.markStopped()
			return
		}

		uptime := s.now().Sub(startedAt)
		if uptime < s.fastFailWindow {
			fastFails++
		} else {
			// 长时间健康运行后的崩溃不属于连续失败：计数与退避档位都回到起点。
			fastFails = 0
			backoffAttempt = 0
		}
		s.log.Warn("Python 插件异常退出",
			zap.String("error", errText(out.err)),
			zap.Duration("uptime", uptime),
			zap.Int("fast_failures", fastFails),
		)

		if fastFails >= s.maxFastFails {
			s.markFailed(out.err, fastFails)
			s.log.Error("Python 插件连续快速失败，已停止重启",
				zap.Int("fast_failures", fastFails))
			return
		}

		delay := s.backoff[min(backoffAttempt, len(s.backoff)-1)]
		backoffAttempt++
		s.markRestarting(out.err)
		if !s.pause(ctx, delay) {
			s.markStopped()
			return
		}
	}
}

// runOnce 完成一次子进程的完整生命周期，返回本次失败原因或因 stop/ctx 退出。
func (s *supervisor) runOnce(ctx context.Context) attemptOutcome {
	token, err := newToken()
	if err != nil {
		return attemptOutcome{err: fmt.Errorf("生成内部令牌失败: %w", err)}
	}

	names := pluginNames(s.cfg.Plugins)
	spec := launchSpec{
		Interpreter:   s.cfg.Interpreter,
		WorkDir:       s.cfg.PluginsDir,
		SDKPath:       s.cfg.SDKPath,
		LogLevel:      s.cfg.LogLevel,
		Plugins:       names,
		InternalToken: token,
	}

	child, err := s.runner.Start(ctx, spec)
	if err != nil {
		return attemptOutcome{err: fmt.Errorf("拉起 Python 插件失败: %w", err)}
	}

	stdin := child.Stdin()
	s.mu.Lock()
	s.child = child
	s.stdin = stdin
	s.internalToken = token
	s.mu.Unlock()
	defer func() {
		_ = stdin.Close()
		s.mu.Lock()
		s.child = nil
		s.stdin = nil
		s.mu.Unlock()
	}()
	pumpsDone := make(chan struct{}, 2)
	// 子进程退出后关闭管道读端，避免重启次数增长时泄漏文件描述符。
	// 关闭前先等两个 pump 把内核缓冲区里剩余的输出（通常是崩溃的 traceback）读完；
	// 若孙进程仍持有写端导致 pump 不结束，到 pumpDrainTimeout 后照常关闭。
	defer func() {
		timer := time.NewTimer(pumpDrainTimeout)
		defer timer.Stop()
		for i := 0; i < 2; i++ {
			select {
			case <-pumpsDone:
			case <-timer.C:
				i = 2
			}
		}
		for _, r := range []io.Reader{child.Stdout(), child.Stderr()} {
			if closer, ok := r.(io.Closer); ok {
				_ = closer.Close()
			}
		}
	}()

	if err := writeHandshake(stdin, token, names); err != nil {
		s.log.Warn("写入 Python 插件握手失败",
			zap.Int("plugin_pid", child.Pid()), zap.Error(err))
	}

	readyCh := make(chan startSignal, 1)
	go func() { s.pumpStdout(child, readyCh); pumpsDone <- struct{}{} }()
	go func() { s.pumpStderr(child); pumpsDone <- struct{}{} }()

	waitDone := make(chan error, 1)
	go func() { waitDone <- child.Wait() }()

	startupTimeout := s.cfg.Config.StartupTimeout
	if startupTimeout <= 0 {
		startupTimeout = DefaultStartupTimeout
	}
	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()

	var ready readyMessage
	select {
	case sig := <-readyCh:
		if sig.err != nil {
			s.abort(child, waitDone)
			return attemptOutcome{err: sig.err}
		}
		ready = sig.ready
	case err := <-waitDone:
		return attemptOutcome{err: fmt.Errorf("进程在就绪前退出: %s", errText(err))}
	case <-timer.C:
		s.abort(child, waitDone)
		return attemptOutcome{err: fmt.Errorf("就绪行超时: %s 内未收到就绪行", startupTimeout)}
	case <-ctx.Done():
		s.shutdown(child, waitDone)
		return attemptOutcome{stopped: true}
	case <-s.stopCh:
		s.shutdown(child, waitDone)
		return attemptOutcome{stopped: true}
	}

	if s.isStopping(ctx) {
		s.shutdown(child, waitDone)
		return attemptOutcome{stopped: true}
	}
	s.markRunning(ready, child.Pid())
	s.log.Info("Python 插件已就绪",
		zap.Int("port", ready.Port),
		zap.Int("plugin_pid", ready.PID),
	)

	select {
	case err := <-waitDone:
		// 关闭 stdin 触发的退出属于主动停止，不能当作崩溃重启。
		if s.isStopping(ctx) {
			return attemptOutcome{stopped: true}
		}
		return attemptOutcome{err: fmt.Errorf("进程异常退出: %s", errText(err))}
	case <-ctx.Done():
		s.shutdown(child, waitDone)
		return attemptOutcome{stopped: true}
	case <-s.stopCh:
		s.shutdown(child, waitDone)
		return attemptOutcome{stopped: true}
	}
}

// readLines 逐行读取 r 并对每行调用 fn，直到 EOF 或读错误。
// 单行超过 scannerMaxBuf 时只保留前 scannerMaxBuf 字节并丢弃其余部分，但继续读取后续行：
// 不能因为一条超长日志就停止消费管道，否则子进程会在管道缓冲区写满后永久阻塞。
func readLines(r io.Reader, fn func(line string)) error {
	br := bufio.NewReaderSize(r, scannerInitialBuf)
	for {
		line, isPrefix, err := br.ReadLine()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		buf := append([]byte(nil), line...)
		truncated := false
		for isPrefix {
			line, isPrefix, err = br.ReadLine()
			if err != nil {
				break
			}
			if room := scannerMaxBuf - len(buf); room > 0 {
				if len(line) > room {
					line = line[:room]
				}
				buf = append(buf, line...)
			} else {
				truncated = true
			}
		}
		if truncated {
			buf = append(buf, "...(超长行已截断)"...)
		}
		fn(string(buf))
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// pumpStdout 逐行读取子进程 stdout：第一行就绪行解析后交给 readyCh，其余行写入日志。
func (s *supervisor) pumpStdout(child childProcess, readyCh chan<- startSignal) {
	sent := false
	err := readLines(child.Stdout(), func(line string) {
		if !sent && strings.HasPrefix(line, readyPrefix) {
			sent = true
			msg, err := parseReady(strings.TrimPrefix(line, readyPrefix))
			switch {
			case err != nil:
				readyCh <- startSignal{err: err}
			case msg.SDK != SDKVersion:
				readyCh <- startSignal{err: fmt.Errorf("SDK 版本不一致: 插件报告 %q, 宿主要求 %q", msg.SDK, SDKVersion)}
			default:
				readyCh <- startSignal{ready: msg}
			}
			return
		}
		s.log.Info(line, zap.Int("plugin_pid", child.Pid()))
	})
	if err != nil {
		s.log.Warn("读取 Python 插件 stdout 失败",
			zap.Int("plugin_pid", child.Pid()), zap.Error(err))
	}
}

// pumpStderr 把子进程 stderr 逐行写入日志。
func (s *supervisor) pumpStderr(child childProcess) {
	err := readLines(child.Stderr(), func(line string) {
		s.log.Warn(line, zap.Int("plugin_pid", child.Pid()))
	})
	if err != nil {
		s.log.Warn("读取 Python 插件 stderr 失败",
			zap.Int("plugin_pid", child.Pid()), zap.Error(err))
	}
}

// abort 用于启动失败场景：强制杀死子进程并等待其退出。
func (s *supervisor) abort(child childProcess, waitDone <-chan error) {
	_ = child.Kill()
	if !s.waitExit(waitDone) {
		s.log.Warn("杀死 Python 插件后仍未退出",
			zap.Int("plugin_pid", child.Pid()))
	}
}

// shutdown 优雅停止子进程：关闭 stdin 生命线、发送终止信号，超时后强制杀死。
func (s *supervisor) shutdown(child childProcess, waitDone <-chan error) {
	s.closeStdin()
	_ = child.Terminate()
	if !s.waitExit(waitDone) {
		_ = child.Kill()
		s.waitExit(waitDone)
	}
}

// waitExit 在 stopTimeout 内等待子进程退出。
func (s *supervisor) waitExit(waitDone <-chan error) bool {
	timer := time.NewTimer(s.stopTimeout)
	defer timer.Stop()
	select {
	case <-waitDone:
		return true
	case <-timer.C:
		return false
	}
}

// pause 等待退避时长；期间收到停止信号时返回 false。
func (s *supervisor) pause(ctx context.Context, d time.Duration) bool {
	if s.sleep != nil {
		s.sleep(d)
	} else {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-s.stopCh:
			return false
		case <-ctx.Done():
			return false
		}
	}
	select {
	case <-s.stopCh:
		return false
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

// markStopped 把进程状态置为 stopped：停止后 target() 立即不可用，快照中不保留错误。
func (s *supervisor) markStopped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Status = "stopped"
	s.status.PID = 0
	s.status.Port = 0
	s.status.StartedAt = ""
	s.status.Error = ""
}

// isStopping 报告是否已收到 stop 或 ctx 取消。
func (s *supervisor) isStopping(ctx context.Context) bool {
	select {
	case <-s.stopCh:
		return true
	default:
	}
	return ctx.Err() != nil
}

// closeStdin 关闭当前子进程的 stdin 写端；没有子进程时为空操作且可重复调用。
func (s *supervisor) closeStdin() {
	s.mu.Lock()
	stdin := s.stdin
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
}

// markRunning 记录就绪后的进程状态与各插件的加载结果。
func (s *supervisor) markRunning(msg readyMessage, pid int) {
	if msg.PID != 0 {
		pid = msg.PID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = ProcessStatus{
		Status:    "running",
		PID:       pid,
		Port:      msg.Port,
		Restarts:  s.status.Restarts,
		StartedAt: s.now().UTC().Format(time.RFC3339),
	}
	s.results = cloneResults(msg.Plugins)
}

// markRestarting 记录一次失败并进入退避等待。
func (s *supervisor) markRestarting(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Status = "starting"
	s.status.PID = 0
	s.status.Port = 0
	s.status.StartedAt = ""
	s.status.Error = errText(err)
	s.status.Restarts++
	s.results = map[string]string{}
}

// markFailed 记录放弃重启。
func (s *supervisor) markFailed(err error, fastFails int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Status = "failed"
	s.status.PID = 0
	s.status.Port = 0
	s.status.StartedAt = ""
	s.status.Error = fmt.Sprintf("%s；连续 %d 次快速失败，已停止重启", errText(err), fastFails)
	s.results = map[string]string{}
}

// stop 请求监督循环停止并关闭当前子进程的 stdin。
// 可在 run 之前调用，也可重复调用；若 run 已开始，会等待其结束。等待上限为 2*stopTimeout，
// 覆盖 shutdown 中「SIGTERM 等 stopTimeout → Kill 再等 stopTimeout」的最长路径。
func (s *supervisor) stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.markStopped()

	s.mu.Lock()
	child, stdin := s.child, s.stdin
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if child != nil {
		_ = child.Terminate()
	}

	if !s.started.Load() {
		return
	}
	timer := time.NewTimer(2 * s.stopTimeout)
	defer timer.Stop()
	select {
	case <-s.runDone:
	case <-timer.C:
	}
}

// snapshot 返回进程状态快照，可与 run 并发调用。
func (s *supervisor) snapshot() ProcessStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// pluginResults 返回各插件的加载结果副本，可与 run 并发调用。
func (s *supervisor) pluginResults() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneResults(s.results)
}

// target 返回可代理的环回端口；进程未就绪时 ok 为 false。
func (s *supervisor) target() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.Status != "running" || s.status.Port <= 0 {
		return 0, false
	}
	return s.status.Port, true
}

// token 返回当前子进程的内部令牌；尚未拉起子进程时为空串。
// 每次重启会生成新令牌，代理按请求读取，确保始终使用最新值。
func (s *supervisor) token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.internalToken
}

// parseReady 解析就绪行前缀之后的 JSON。
func parseReady(body string) (readyMessage, error) {
	var msg readyMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		return readyMessage{}, fmt.Errorf("解析就绪行失败: %w", err)
	}
	if msg.Port <= 0 {
		return readyMessage{}, fmt.Errorf("就绪行端口无效: %d", msg.Port)
	}
	return msg, nil
}

// newToken 生成 32 字节随机令牌的十六进制编码。
func newToken() (string, error) {
	buf := make([]byte, internalTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// writeHandshake 向子进程 stdin 写入两行：令牌与插件名 JSON 数组；写端保持打开。
func writeHandshake(w io.Writer, token string, names []string) error {
	payload, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("编码插件清单失败: %w", err)
	}
	_, err = io.WriteString(w, token+"\n"+string(payload)+"\n")
	return err
}

// pluginNames 按原顺序取出插件名。
func pluginNames(plugins []Plugin) []string {
	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		names = append(names, p.Name)
	}
	return names
}

// cloneResults 复制插件加载结果，返回非 nil map。
func cloneResults(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// errText 把 error 转成可展示的文本，nil 时给出占位原因。
func errText(err error) string {
	if err == nil {
		return "未知原因"
	}
	return err.Error()
}

// execRunner 是 childRunner 的生产实现，直接 exec 解释器并连接三条管道。
type execRunner struct{}

// Start 以 spec 拉起子进程；stdin 为写管道，stdout/stderr 为读管道。
func (execRunner) Start(_ context.Context, spec launchSpec) (childProcess, error) {
	// 不用 exec.CommandContext：它在 ctx 取消时直接 SIGKILL，会绕过 supervisor 的
	// 「关 stdin → SIGTERM → 等待 → Kill」优雅停止流程。生命周期完全由 supervisor 管理。
	cmd := exec.Command(spec.Interpreter, "-m", "groot_plugin_sdk.launcher")
	cmd.Dir = spec.WorkDir
	cmd.Env = childEnv(spec.SDKPath, spec.LogLevel)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stdin 管道失败: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("创建 stdout 管道失败: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, fmt.Errorf("创建 stderr 管道失败: %w", err)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
		return nil, fmt.Errorf("启动解释器 %s 失败: %w", spec.Interpreter, err)
	}
	// 父进程只保留读端；写端交给子进程，Start 之后必须关闭自己的副本，读端才会 EOF。
	_ = stdoutW.Close()
	_ = stderrW.Close()

	return &execProcess{cmd: cmd, stdin: stdin, stdout: stdoutR, stderr: stderrR}, nil
}

// execProcess 是 childProcess 的生产实现。
type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *os.File
	stderr *os.File
}

func (p *execProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execProcess) Stdout() io.Reader     { return p.stdout }
func (p *execProcess) Stderr() io.Reader     { return p.stderr }
func (p *execProcess) Wait() error           { return p.cmd.Wait() }

func (p *execProcess) Terminate() error {
	if p.cmd.Process == nil {
		return nil
	}
	// os.Process.Signal 在 Wait 之后返回 ErrProcessDone，不会向被复用的 pid 发信号。
	return terminate(p.cmd.Process)
}

func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (p *execProcess) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// childEnv 继承当前进程环境，覆盖 PYTHONPATH、PYTHONUNBUFFERED 与 GROOT_LOG_LEVEL。
// PYTHONPATH 前置 SDK 目录，已有值保留在其后。
func childEnv(sdkPath, logLevel string) []string {
	if logLevel == "" {
		logLevel = "info"
	}
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(key) {
		case "PYTHONPATH", "PYTHONUNBUFFERED", "GROOT_LOG_LEVEL":
			continue
		}
		env = append(env, kv)
	}
	pythonPath := sdkPath
	if existing := os.Getenv("PYTHONPATH"); existing != "" {
		pythonPath = sdkPath + string(os.PathListSeparator) + existing
	}
	return append(env,
		"PYTHONPATH="+pythonPath,
		"PYTHONUNBUFFERED=1",
		"GROOT_LOG_LEVEL="+logLevel,
	)
}
