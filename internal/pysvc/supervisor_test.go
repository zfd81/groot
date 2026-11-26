package pysvc

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/zfd81/groot/internal/logger"
)

const (
	readyLine = `GROOT_SERVICE_READY {"port":53124,"pid":123,"sdk":"1.0.0","services":{"a":"ok"}}`
	mismatch  = `GROOT_SERVICE_READY {"port":53125,"pid":124,"sdk":"0.9.0","services":{"a":"ok"}}`
)

// waitFor 轮询直到 cond 为真，超时则让测试失败。
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

// testSupervisorConfig 返回一套面向测试的配置：退避与 sleep 都被加速，终止信号不真正发送。
func testSupervisorConfig(t *testing.T, runner childRunner) supervisorConfig {
	t.Helper()
	return supervisorConfig{
		ServicesDir: t.TempDir(),
		Interpreter: "/nonexistent/python",
		SDKPath:     "/nonexistent/sdk",
		Services:    []Service{{Name: "a"}, {Name: "b"}},
		Config: GlobalConfig{
			StartupTimeout: 2 * time.Second,
			RequestTimeout: time.Second,
			Permission:     map[string]string{},
		},
		Log:            logger.NewNop(),
		LogLevel:       "info",
		Runner:         runner,
		Backoff:        []time.Duration{time.Millisecond, 2 * time.Millisecond},
		FastFailWindow: time.Minute,
		MaxFastFails:   10,
		Sleep:          func(time.Duration) {},
		Now:            time.Now,
	}
}

// fakeStdin 模拟子进程 stdin：写入从不阻塞，Close 可被观测且幂等。
type fakeStdin struct {
	p *fakeProcess
}

func (w *fakeStdin) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	closed := w.p.stdinIsClosed
	w.p.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	return len(b), nil
}

func (w *fakeStdin) Close() error {
	w.p.mu.Lock()
	w.p.stdinIsClosed = true
	w.p.mu.Unlock()
	w.p.stdinOnce.Do(func() { close(w.p.stdinClosed) })
	return nil
}

// fakeProcess 是可配置的假子进程：向 stdout 写出就绪行、立即退出或保持运行直到 stdin 关闭/被杀死。
type fakeProcess struct {
	pid int

	stdin *fakeStdin

	stdoutR io.ReadCloser
	stdoutW io.WriteCloser
	stderrR io.ReadCloser
	stderrW io.WriteCloser

	mu            sync.Mutex
	stdinIsClosed bool
	waitErr       error
	terminated    int

	stdinClosed chan struct{}
	stdinOnce   sync.Once
	killCh      chan struct{}
	killOnce    sync.Once
	done        chan struct{}
	doneOnce    sync.Once
}

func newFakeProcess(pid int) *fakeProcess {
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	p := &fakeProcess{
		pid:         pid,
		stdoutR:     stdoutR,
		stdoutW:     stdoutW,
		stderrR:     stderrR,
		stderrW:     stderrW,
		stdinClosed: make(chan struct{}),
		killCh:      make(chan struct{}),
		done:        make(chan struct{}),
	}
	p.stdin = &fakeStdin{p: p}
	return p
}

// behave 启动假子进程的行为：可选先写出一行 ready，随后立即退出或等 stdin 关闭/被杀死。
func (p *fakeProcess) behave(line string, exitNow bool) *fakeProcess {
	go func() {
		if line != "" {
			_, _ = io.WriteString(p.stdoutW, line+"\n")
		}
		if exitNow {
			p.finish(errors.New("子进程立即退出"))
			return
		}
		select {
		case <-p.killCh:
		case <-p.stdinClosed:
		}
		p.finish(nil)
	}()
	return p
}

func (p *fakeProcess) finish(err error) {
	p.doneOnce.Do(func() {
		p.mu.Lock()
		p.waitErr = err
		p.mu.Unlock()
		_ = p.stdoutW.Close()
		_ = p.stderrW.Close()
		close(p.done)
	})
}

func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *fakeProcess) Stdout() io.Reader     { return p.stdoutR }
func (p *fakeProcess) Stderr() io.Reader     { return p.stderrR }
func (p *fakeProcess) Pid() int              { return p.pid }

func (p *fakeProcess) Wait() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *fakeProcess) Kill() error {
	p.killOnce.Do(func() { close(p.killCh) })
	p.finish(errors.New("killed"))
	return nil
}

// Terminate 只记录调用次数，不结束进程（真实子进程收到 SIGTERM 后才自行退出）。
func (p *fakeProcess) Terminate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.terminated++
	return nil
}

func (p *fakeProcess) terminateCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminated
}

// readyHoldingProcess 写出就绪行后保持运行，直到 stdin 被关闭或被杀死。
func readyHoldingProcess(pid int, line string) *fakeProcess {
	return newFakeProcess(pid).behave(line, false)
}

// silentProcess 从不写就绪行，保持运行直到 stdin 被关闭或被杀死。
func silentProcess(pid int) *fakeProcess {
	return newFakeProcess(pid).behave("", false)
}

// exitingProcess 不写就绪行，启动后立即退出。
func exitingProcess(pid int) *fakeProcess {
	return newFakeProcess(pid).behave("", true)
}

// longLineThenReadyProcess 先写一条超长日志行，再写就绪行，随后保持运行。
func longLineThenReadyProcess(pid int, line string, length int) *fakeProcess {
	p := newFakeProcess(pid)
	go func() {
		_, _ = io.WriteString(p.stdoutW, strings.Repeat("x", length)+"\n")
		_, _ = io.WriteString(p.stdoutW, line+"\n")
		select {
		case <-p.killCh:
		case <-p.stdinClosed:
		}
		p.finish(nil)
	}()
	return p
}

// fakeRunner 记录每次启动参数，并按测试提供的 spawn 返回假子进程。
type fakeRunner struct {
	mu    sync.Mutex
	specs []launchSpec
	spawn func(idx int) (childProcess, error)
}

func (f *fakeRunner) Start(_ context.Context, spec launchSpec) (childProcess, error) {
	f.mu.Lock()
	idx := len(f.specs)
	f.specs = append(f.specs, spec)
	spawn := f.spawn
	f.mu.Unlock()
	if spawn == nil {
		return nil, errors.New("fakeRunner 未设置 spawn")
	}
	return spawn(idx)
}

func (f *fakeRunner) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.specs)
}

func (f *fakeRunner) specAt(i int) launchSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.specs[i]
}

func TestSupervisor_ReadyThenRunning(t *testing.T) {
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return readyHoldingProcess(123, readyLine), nil
	}}
	sup := newSupervisor(testSupervisorConfig(t, runner))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "进程进入 running", func() bool {
		return sup.snapshot().Status == "running"
	})

	status := sup.snapshot()
	if status.Port != 53124 {
		t.Errorf("Port = %d, want 53124", status.Port)
	}
	if status.PID != 123 {
		t.Errorf("PID = %d, want 123", status.PID)
	}
	port, ok := sup.target()
	if !ok || port != 53124 {
		t.Errorf("target() = (%d, %v), want (53124, true)", port, ok)
	}
	if got := sup.serviceResults()["a"]; got != "ok" {
		t.Errorf("serviceResults()[a] = %q, want ok", got)
	}

	// snapshot/target/serviceResults 允许与 run 并发调用。
	stopPolling := make(chan struct{})
	defer close(stopPolling)
	go func() {
		for {
			select {
			case <-stopPolling:
				return
			default:
				_ = sup.snapshot()
				_, _ = sup.target()
				_ = sup.serviceResults()
				time.Sleep(time.Millisecond)
			}
		}
	}()
}

func TestSupervisor_LongLogLineBeforeReady(t *testing.T) {
	// 一行超过 bufio.Scanner 默认 64KB 上限的日志不能阻断后续就绪行。
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return longLineThenReadyProcess(125, readyLine, 128*1024), nil
	}}
	sup := newSupervisor(testSupervisorConfig(t, runner))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "长日志行后进程仍能进入 running", func() bool {
		return sup.snapshot().Status == "running"
	})
}

func TestSupervisor_WritesHandshake(t *testing.T) {
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return readyHoldingProcess(123, readyLine), nil
	}}
	sup := newSupervisor(testSupervisorConfig(t, runner))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "进程进入 running", func() bool {
		return sup.snapshot().Status == "running"
	})

	spec := runner.specAt(0)
	if len(spec.InternalToken) != 64 {
		t.Errorf("Token 长度 = %d, want 64: %q", len(spec.InternalToken), spec.InternalToken)
	}
	if _, err := hex.DecodeString(spec.InternalToken); err != nil {
		t.Errorf("Token 不是十六进制: %q (%v)", spec.InternalToken, err)
	}
	if want := []string{"a", "b"}; !slices.Equal(spec.Services, want) {
		t.Errorf("Services = %v, want %v", spec.Services, want)
	}
}

func TestSupervisor_SDKVersionMismatch(t *testing.T) {
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return readyHoldingProcess(124, mismatch), nil
	}}
	sup := newSupervisor(testSupervisorConfig(t, runner))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "SDK 版本不一致后进入重启", func() bool {
		status := sup.snapshot()
		return status.Status != "running" && status.Restarts >= 1 && strings.Contains(status.Error, "SDK 版本不一致")
	})
	if _, ok := sup.target(); ok {
		t.Error("SDK 版本不一致时 target() 不应可用")
	}
}

func TestSupervisor_StartupTimeout(t *testing.T) {
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return silentProcess(125), nil
	}}
	cfg := testSupervisorConfig(t, runner)
	cfg.Config.StartupTimeout = 50 * time.Millisecond
	sup := newSupervisor(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "记录就绪行超时", func() bool {
		status := sup.snapshot()
		return status.Status == "starting" && strings.Contains(status.Error, "就绪行超时")
	})
}

func TestSupervisor_RestartBackoff(t *testing.T) {
	var mu sync.Mutex
	var sleeps []time.Duration
	record := func(d time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
	}

	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return exitingProcess(126), nil
	}}
	cfg := testSupervisorConfig(t, runner)
	cfg.Backoff = []time.Duration{3 * time.Millisecond, 7 * time.Millisecond}
	cfg.MaxFastFails = 4
	cfg.Sleep = record
	sup := newSupervisor(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "连续快速失败后停止重启", func() bool {
		return sup.snapshot().Status == "failed"
	})

	status := sup.snapshot()
	if status.Restarts != 3 {
		t.Errorf("Restarts = %d, want 3", status.Restarts)
	}
	if got := runner.startCount(); got != 4 {
		t.Errorf("Runner 启动次数 = %d, want 4", got)
	}
	mu.Lock()
	got := append([]time.Duration(nil), sleeps...)
	mu.Unlock()
	want := []time.Duration{3 * time.Millisecond, 7 * time.Millisecond, 7 * time.Millisecond}
	if !slices.Equal(got, want) {
		t.Errorf("退避序列 = %v, want %v", got, want)
	}
}

func TestSupervisor_GivesUpAfterFastFailures(t *testing.T) {
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return exitingProcess(127), nil
	}}
	cfg := testSupervisorConfig(t, runner)
	cfg.MaxFastFails = 10
	sup := newSupervisor(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "连续 10 次快速失败后状态 failed", func() bool {
		return sup.snapshot().Status == "failed"
	})
	if got := runner.startCount(); got != 10 {
		t.Fatalf("Runner 启动次数 = %d, want 10", got)
	}

	time.Sleep(20 * time.Millisecond)
	if got := runner.startCount(); got != 10 {
		t.Errorf("状态 failed 后仍在调用 Runner, 启动次数 = %d", got)
	}
}

func TestSupervisor_StopClosesStdin(t *testing.T) {
	proc := silentProcess(128)
	runner := &fakeRunner{spawn: func(int) (childProcess, error) { return proc, nil }}
	cfg := testSupervisorConfig(t, runner)
	cfg.Config.StartupTimeout = 5 * time.Second // 保证 stop 发生时就绪等待仍在进行

	// 未启动时可安全调用，且可重复调用；停止后的运行循环不会再拉起子进程。
	notStarted := newSupervisor(cfg)
	notStarted.stop()
	notStarted.stop()
	notStarted.run(context.Background())
	if got := runner.startCount(); got != 0 {
		t.Fatalf("stop 之后 run 不应拉起子进程, 启动次数 = %d", got)
	}

	sup := newSupervisor(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)

	waitFor(t, 2*time.Second, "子进程已拉起", func() bool { return runner.startCount() == 1 })

	sup.stop()
	waitFor(t, 2*time.Second, "stop 关闭子进程 stdin", func() bool {
		select {
		case <-proc.stdinClosed:
			return true
		default:
			return false
		}
	})
	if got := proc.terminateCount(); got == 0 {
		t.Error("stop 应通过子进程对象发送终止信号")
	}

	// 重复调用依然安全。
	sup.stop()
}

func TestSupervisor_StopClearsTarget(t *testing.T) {
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return readyHoldingProcess(131, readyLine), nil
	}}
	sup := newSupervisor(testSupervisorConfig(t, runner))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)

	waitFor(t, 2*time.Second, "进程进入 running", func() bool {
		return sup.snapshot().Status == "running"
	})
	if _, ok := sup.target(); !ok {
		t.Fatal("running 时 target() 应可用")
	}

	sup.stop() // stop 返回时 run 已结束

	status := sup.snapshot()
	if status.Status != "stopped" {
		t.Errorf("Status = %q, want stopped", status.Status)
	}
	if status.Port != 0 {
		t.Errorf("Port = %d, want 0", status.Port)
	}
	if port, ok := sup.target(); ok {
		t.Errorf("stop 后 target() = (%d, true), want false", port)
	}
}

func TestSupervisor_StopDoesNotRestart(t *testing.T) {
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return readyHoldingProcess(132, readyLine), nil
	}}
	sup := newSupervisor(testSupervisorConfig(t, runner))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)

	waitFor(t, 2*time.Second, "进程进入 running", func() bool {
		return sup.snapshot().Status == "running"
	})

	sup.stop()

	if got := runner.startCount(); got != 1 {
		t.Errorf("stop 导致的退出不应重启, 启动次数 = %d, want 1", got)
	}
	status := sup.snapshot()
	if status.Restarts != 0 {
		t.Errorf("Restarts = %d, want 0", status.Restarts)
	}
	if status.Status != "stopped" {
		t.Errorf("Status = %q, want stopped", status.Status)
	}
}

func TestSupervisor_InvalidReadyPort(t *testing.T) {
	line := `GROOT_SERVICE_READY {"port":0,"pid":133,"sdk":"1.0.0","services":{"a":"ok"}}`
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return readyHoldingProcess(133, line), nil
	}}
	sup := newSupervisor(testSupervisorConfig(t, runner))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "端口无效按启动失败处理", func() bool {
		status := sup.snapshot()
		return status.Status != "running" && strings.Contains(status.Error, "端口无效")
	})
	if port, ok := sup.target(); ok {
		t.Errorf("端口无效时 target() = (%d, true), want false", port)
	}
}

// crashWithStderrProcess 向 stderr 写一段 traceback 后立刻退出，不写就绪行。
// stderr 用真实 OS 管道：数据留在内核缓冲区里，与真实子进程崩溃时的时序一致
// （io.Pipe 是同步的，写端返回即表示读端已消费，模拟不出这个场景）。
func crashWithStderrProcess(t *testing.T, pid int, stderr string) *fakeProcess {
	t.Helper()
	p := newFakeProcess(pid)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	_ = p.stderrR.Close()
	_ = p.stderrW.Close()
	p.stderrR, p.stderrW = r, w
	go func() {
		_, _ = io.WriteString(w, stderr)
		p.finish(errors.New("exit status 1"))
	}()
	return p
}

func TestSupervisor_StderrOfCrashedProcessIsLogged(t *testing.T) {
	// 子进程在就绪前崩溃时，它写到 stderr 的 traceback 必须完整进入日志。
	const traceback = "Traceback (most recent call last):\n  File \"x\"\nModuleNotFoundError: No module named 'foo'\n"
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return crashWithStderrProcess(t, 130, traceback), nil
	}}
	cfg := testSupervisorConfig(t, runner)
	log, logs := logger.NewObserved()
	cfg.Log = log
	cfg.MaxFastFails = 1
	sup := newSupervisor(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "进程进入 failed", func() bool {
		return sup.snapshot().Status == "failed"
	})

	var got []string
	for _, e := range logs.All() {
		if e.Level == zapcore.WarnLevel {
			got = append(got, e.Message)
		}
	}
	for _, want := range strings.Split(strings.TrimSpace(traceback), "\n") {
		if !slices.Contains(got, want) {
			t.Errorf("日志中缺少 stderr 行 %q; 实际 Warn 日志: %q", want, got)
		}
	}
}

func TestSupervisor_OversizedLogLineDoesNotStallPipe(t *testing.T) {
	// 一行超过 scannerMaxBuf 的日志会让 bufio.Scanner 停止；之后 stdout 必须继续被读取
	// （否则子进程写满管道缓冲区后阻塞），就绪行仍能到达。
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return longLineThenReadyProcess(126, readyLine, scannerMaxBuf+1024), nil
	}}
	sup := newSupervisor(testSupervisorConfig(t, runner))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()

	waitFor(t, 2*time.Second, "超长日志行后进程仍能进入 running", func() bool {
		return sup.snapshot().Status == "running"
	})
}

func TestReadLines_TruncatesOversizedLineAndContinues(t *testing.T) {
	long := strings.Repeat("a", scannerMaxBuf+100)
	input := "first\n" + long + "\nlast"
	var lines []string
	if err := readLines(strings.NewReader(input), func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("readLines: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("行数 = %d, want 3", len(lines))
	}
	if lines[0] != "first" || lines[2] != "last" {
		t.Errorf("首尾行 = %q, %q", lines[0], lines[2])
	}
	if !strings.HasPrefix(lines[1], strings.Repeat("a", scannerMaxBuf)) || !strings.HasSuffix(lines[1], "(超长行已截断)") {
		t.Errorf("超长行未按预期截断: len=%d, 末尾=%q", len(lines[1]), lines[1][len(lines[1])-20:])
	}
	if len(lines[1]) > scannerMaxBuf+64 {
		t.Errorf("截断后仍超过上限: %d", len(lines[1]))
	}
}

func TestSupervisor_BackoffResetsAfterHealthyRun(t *testing.T) {
	// 两次快速失败把退避推到第二档；随后一次长时间健康运行再崩溃，退避应回到第一档。
	var mu sync.Mutex
	var sleeps []time.Duration
	record := func(d time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
	}
	now := time.Now()
	var clockMu sync.Mutex
	clock := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
	advance := func(d time.Duration) { clockMu.Lock(); now = now.Add(d); clockMu.Unlock() }

	var procs []*fakeProcess
	var procMu sync.Mutex
	runner := &fakeRunner{spawn: func(idx int) (childProcess, error) {
		var p *fakeProcess
		switch idx {
		case 0, 1, 3:
			p = exitingProcess(140 + idx)
		default:
			p = readyHoldingProcess(140+idx, readyLine)
		}
		procMu.Lock()
		procs = append(procs, p)
		procMu.Unlock()
		return p, nil
	}}
	cfg := testSupervisorConfig(t, runner)
	cfg.Backoff = []time.Duration{3 * time.Millisecond, 7 * time.Millisecond}
	cfg.FastFailWindow = time.Minute
	cfg.MaxFastFails = 10
	cfg.Sleep = record
	cfg.Now = clock
	sup := newSupervisor(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	defer sup.stop()
	waitFor(t, 2*time.Second, "第三次启动进入 running", func() bool {
		return sup.snapshot().Status == "running" && runner.startCount() == 3
	})
	advance(2 * time.Minute) // 健康运行超过快速失败窗口
	procMu.Lock()
	procs[2].Kill()
	procMu.Unlock()
	waitFor(t, 2*time.Second, "第五次启动", func() bool { return runner.startCount() >= 5 })

	mu.Lock()
	got := append([]time.Duration(nil), sleeps...)
	mu.Unlock()
	// 前两次快速失败：3ms、7ms；健康运行后崩溃：回到 3ms；紧接着再次快速失败：7ms。
	want := []time.Duration{3 * time.Millisecond, 7 * time.Millisecond, 3 * time.Millisecond, 7 * time.Millisecond}
	if !slices.Equal(got, want) {
		t.Errorf("退避序列 = %v, want %v", got, want)
	}
}

// stubbornProcess 写出就绪行后无视 stdin 关闭与终止信号，只有 Kill 才会退出。
func stubbornProcess(pid int, line string) *fakeProcess {
	p := newFakeProcess(pid)
	go func() {
		_, _ = io.WriteString(p.stdoutW, line+"\n")
		<-p.killCh
		p.finish(errors.New("killed"))
	}()
	return p
}

func TestSupervisor_StopEscalatesToKill(t *testing.T) {
	// 子进程在 StopTimeout 内不响应优雅停止时，supervisor 必须升级为 Kill，而不是无限等待。
	proc := stubbornProcess(150, readyLine)
	runner := &fakeRunner{spawn: func(int) (childProcess, error) { return proc, nil }}
	cfg := testSupervisorConfig(t, runner)
	cfg.StopTimeout = 20 * time.Millisecond
	sup := newSupervisor(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)
	waitFor(t, 2*time.Second, "进程进入 running", func() bool {
		return sup.snapshot().Status == "running"
	})

	start := time.Now()
	sup.stop()
	elapsed := time.Since(start)

	select {
	case <-proc.done:
	default:
		t.Fatal("stop 返回后子进程仍未结束（未升级到 Kill）")
	}
	if proc.terminateCount() == 0 {
		t.Error("升级到 Kill 之前应先尝试 Terminate")
	}
	if elapsed > time.Second {
		t.Errorf("stop 耗时 %s，应在 StopTimeout 量级内完成", elapsed)
	}
	if sup.snapshot().Status != "stopped" {
		t.Errorf("Status = %q, want stopped", sup.snapshot().Status)
	}
}
