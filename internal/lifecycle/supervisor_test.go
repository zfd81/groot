package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHelperProcess 是被 Supervisor 拉起的"工作进程"。只有设置了 GROOT_TEST_HELPER=1 才执行。
// 模式（GROOT_TEST_MODE）：
//
//	exit      按 GROOT_TEST_EXIT_SEQ（逗号分隔）中第 N 次拉起对应的码退出；越界取最后一个
//	wait      阻塞读 stdin 直到 EOF，然后 exit 0
//	hang      忽略 stdin，睡眠 60 秒
//	env       stdout 打印 GROOT_SUPERVISED 的值后 exit 0
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GROOT_TEST_HELPER") != "1" {
		return
	}
	defer os.Exit(0)

	n := bumpCounter(os.Getenv("GROOT_TEST_COUNTER"))
	switch os.Getenv("GROOT_TEST_MODE") {
	case "exit":
		seq := strings.Split(os.Getenv("GROOT_TEST_EXIT_SEQ"), ",")
		idx := n - 1
		if idx >= len(seq) {
			idx = len(seq) - 1
		}
		code, _ := strconv.Atoi(strings.TrimSpace(seq[idx]))
		os.Exit(code)
	case "wait":
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "hang":
		time.Sleep(60 * time.Second)
		os.Exit(0)
	case "env":
		fmt.Fprint(os.Stdout, os.Getenv(EnvSupervised))
		os.Exit(0)
	}
}

// bumpCounter 读取计数文件、加一写回，返回加一后的值。文件不存在视为 0。
func bumpCounter(path string) int {
	n := 0
	if b, err := os.ReadFile(path); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	n++
	os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644)
	return n
}

func readCounter(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// fastBackoff 让测试跑得快：窗口 5 秒、最多 3 次快速失败、延迟 10ms。
func fastBackoff() Backoff {
	return Backoff{
		FastFailWindow: 5 * time.Second,
		MaxFastFails:   3,
		Delays:         []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
		MinInterval:    0,
	}
}

// lockedBuffer 是加锁的字符串缓冲：Run 可能仍在写日志时 Cleanup 已开始读取。
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// newTestSupervisor 构造指向测试二进制自身的 Supervisor。
func newTestSupervisor(t *testing.T, mode string, extraEnv ...string) (*Supervisor, string) {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "counter")
	env := append([]string{
		"GROOT_TEST_HELPER=1",
		"GROOT_TEST_MODE=" + mode,
		"GROOT_TEST_COUNTER=" + counter,
	}, extraEnv...)
	out := &lockedBuffer{}
	s := NewSupervisor(Options{
		Exe:         os.Args[0],
		Args:        []string{"-test.run=^TestHelperProcess$"},
		ExtraEnv:    env,
		Backoff:     fastBackoff(),
		StopTimeout: 2 * time.Second,
		Log:         out,
		Stdout:      io.Discard,
		Stderr:      io.Discard,
	})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("supervisor log:\n%s", out.String())
		}
	})
	return s, counter
}

func TestSupervisor_RestartsOnExitRestart(t *testing.T) {
	s, counter := newTestSupervisor(t, "exit", "GROOT_TEST_EXIT_SEQ=3,0")
	code := s.Run(context.Background())
	if code != 0 {
		t.Errorf("Run() = %d, want 0", code)
	}
	if n := readCounter(t, counter); n != 2 {
		t.Errorf("子进程应被拉起 2 次（exit 3 → 重启 → exit 0），got %d", n)
	}
}

func TestSupervisor_ExitOKStopsLoop(t *testing.T) {
	s, counter := newTestSupervisor(t, "exit", "GROOT_TEST_EXIT_SEQ=0")
	code := s.Run(context.Background())
	if code != 0 {
		t.Errorf("Run() = %d, want 0", code)
	}
	if n := readCounter(t, counter); n != 1 {
		t.Errorf("exit 0 后不应再拉起，got %d 次", n)
	}
}

func TestSupervisor_GivesUpAfterMaxFastFails(t *testing.T) {
	s, counter := newTestSupervisor(t, "exit", "GROOT_TEST_EXIT_SEQ=1")
	start := time.Now()
	code := s.Run(context.Background())
	if code != 1 {
		t.Errorf("连续崩溃放弃后 Run() = %d, want 1", code)
	}
	if n := readCounter(t, counter); n != 3 {
		t.Errorf("MaxFastFails=3 时应拉起 3 次后放弃，got %d", n)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("退避延迟过长: %v", time.Since(start))
	}
}

func TestSupervisor_LongRunResetsFastFailCounter(t *testing.T) {
	s, counter := newTestSupervisor(t, "exit", "GROOT_TEST_EXIT_SEQ=1,1,1,1,0")
	// 窗口设为 0：任何存活时长都算"长跑"，计数每次归零，永远不会触发放弃
	b := fastBackoff()
	b.FastFailWindow = 0
	s.opts.Backoff = b
	code := s.Run(context.Background())
	if code != 0 {
		t.Errorf("Run() = %d, want 0", code)
	}
	if n := readCounter(t, counter); n != 5 {
		t.Errorf("计数归零后应一直拉起到 exit 0，got %d 次", n)
	}
}

func TestSupervisor_CancelClosesPipeAndChildExits(t *testing.T) {
	s, counter := newTestSupervisor(t, "wait")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- s.Run(ctx) }()

	// 等子进程起来（计数文件出现）
	deadline := time.Now().Add(15 * time.Second)
	for readCounter(t, counter) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("子进程未启动")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("管道关闭后子进程 exit 0，Run() = %d, want 0", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("取消后 Run 未返回")
	}
	if n := readCounter(t, counter); n != 1 {
		t.Errorf("关闭中不应重新拉起，got %d 次", n)
	}
}

func TestSupervisor_KillsHungChildAfterStopTimeout(t *testing.T) {
	s, counter := newTestSupervisor(t, "hang")
	s.opts.StopTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- s.Run(ctx) }()

	deadline := time.Now().Add(15 * time.Second)
	for readCounter(t, counter) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("子进程未启动")
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	cancel()

	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("被强制 Kill 的子进程视为异常，Run() = %d, want 1", code)
		}
		if el := time.Since(start); el < 250*time.Millisecond || el > 3*time.Second {
			t.Errorf("应在 StopTimeout 后 Kill，耗时 %v", el)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("取消后 Run 未返回（Kill 未生效）")
	}
}

func TestSupervisor_SetsSupervisedEnv(t *testing.T) {
	s, _ := newTestSupervisor(t, "env")
	var stdout strings.Builder
	s.opts.Stdout = &stdout
	if code := s.Run(context.Background()); code != 0 {
		t.Fatalf("Run() = %d, want 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "1" {
		t.Errorf("子进程看到的 %s = %q, want \"1\"", EnvSupervised, got)
	}
}

func TestSupervisor_SpawnFailureCountsAsFastFail(t *testing.T) {
	s, _ := newTestSupervisor(t, "exit")
	s.opts.Exe = filepath.Join(t.TempDir(), "no-such-binary")
	code := s.Run(context.Background())
	if code != 1 {
		t.Errorf("可执行文件不存在时应在退避后放弃，Run() = %d, want 1", code)
	}
}

func TestBackoff_Delay(t *testing.T) {
	b := DefaultBackoff()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := b.Delay(i + 1); got != w {
			t.Errorf("Delay(%d) = %v, want %v", i+1, got, w)
		}
	}
	if b.FastFailWindow != 60*time.Second || b.MaxFastFails != 10 || b.MinInterval != time.Second {
		t.Errorf("DefaultBackoff 参数与设计不符: %+v", b)
	}
}

func TestExitCodeOf(t *testing.T) {
	if got := exitCodeOf(nil); got != 0 {
		t.Errorf("exitCodeOf(nil) = %d, want 0", got)
	}
	if got := exitCodeOf(errors.New("spawn failed")); got != 1 {
		t.Errorf("非 ExitError 应映射为 1，got %d", got)
	}
}

func TestNewSupervisor_PartialBackoffDefaults(t *testing.T) {
	// 只给 Delays：MaxFastFails 必须补默认值，否则首次崩溃就会放弃
	s := NewSupervisor(Options{Backoff: Backoff{Delays: []time.Duration{time.Millisecond}}})
	if s.opts.Backoff.MaxFastFails != 10 {
		t.Errorf("MaxFastFails = %d, want 10", s.opts.Backoff.MaxFastFails)
	}
	if len(s.opts.Backoff.Delays) != 1 || s.opts.Backoff.Delays[0] != time.Millisecond {
		t.Errorf("调用方给的 Delays 不应被覆盖: %v", s.opts.Backoff.Delays)
	}
	// 只给 MinInterval：其他字段补默认，MinInterval 保留
	s = NewSupervisor(Options{Backoff: Backoff{MinInterval: 2 * time.Second}})
	if s.opts.Backoff.MinInterval != 2*time.Second || s.opts.Backoff.MaxFastFails != 10 || len(s.opts.Backoff.Delays) != 6 {
		t.Errorf("部分填充 Backoff 未按字段补默认: %+v", s.opts.Backoff)
	}
	// 空 Delays + 零 MinInterval：加最小节流，避免紧密循环
	s = NewSupervisor(Options{Backoff: Backoff{Delays: []time.Duration{}, MaxFastFails: 3}})
	if s.opts.Backoff.MinInterval != 100*time.Millisecond {
		t.Errorf("空 Delays 且无 MinInterval 时应设 100ms 下限，got %v", s.opts.Backoff.MinInterval)
	}
}
