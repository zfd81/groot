package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// Backoff 是工作进程异常退出后的重拉策略。
type Backoff struct {
	// FastFailWindow 存活不足此时长即异常退出，计为一次快速失败；0 表示任何存活时长都不算快速失败。
	FastFailWindow time.Duration
	// MaxFastFails 连续快速失败达到此次数后监督进程放弃并退出 1；≤0 时使用默认值 10。
	MaxFastFails int
	// Delays 第 n 次连续快速失败后的等待时长（n 从 1 起）；越界取最后一项。nil 时使用默认序列；空切片表示不等待。
	Delays []time.Duration
	// MinInterval 任意两次拉起之间的最小间隔，对退出码 3 同样生效。
	MinInterval time.Duration
}

// DefaultBackoff 返回设计文档 1.3.5 节的默认策略。
func DefaultBackoff() Backoff {
	return Backoff{
		FastFailWindow: 60 * time.Second,
		MaxFastFails:   10,
		Delays:         []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second},
		MinInterval:    time.Second,
	}
}

// Delay 返回第 n 次连续快速失败后应等待的时长。
func (b Backoff) Delay(n int) time.Duration {
	if len(b.Delays) == 0 {
		return 0
	}
	if n < 1 {
		n = 1
	}
	if n > len(b.Delays) {
		n = len(b.Delays)
	}
	return b.Delays[n-1]
}

// Options 是监督进程的配置。
type Options struct {
	Exe         string        // 工作进程可执行文件路径
	Args        []string      // 传给工作进程的参数（不含程序名）
	ExtraEnv    []string      // 追加到子进程环境的变量（KEY=VALUE）
	Backoff     Backoff       // 退避策略；零值时使用 DefaultBackoff()
	StopTimeout time.Duration // 关闭管道后等待工作进程退出的时长，超时强制 Kill；零值 35 秒
	Log         io.Writer     // 监督进程自身日志；nil 则 os.Stderr
	Stdout      io.Writer     // 工作进程 stdout；nil 则 os.Stdout
	Stderr      io.Writer     // 工作进程 stderr；nil 则 os.Stderr
}

// Supervisor 拉起工作进程并按其退出码决定下一步：0 结束，3 立即重拉，其他退避重拉。
// 它不加载任何业务资源。
type Supervisor struct {
	opts Options
}

// NewSupervisor 创建监督进程；零值字段填入默认值。
func NewSupervisor(o Options) *Supervisor {
	def := DefaultBackoff()
	if o.Backoff.MaxFastFails <= 0 {
		o.Backoff.MaxFastFails = def.MaxFastFails
	}
	if o.Backoff.Delays == nil {
		o.Backoff.Delays = def.Delays
	}
	// FastFailWindow 与 MinInterval 的 0 有明确语义（不计快速失败 / 无最小间隔），保持调用方的值
	// Delays 为空且 MinInterval 为 0 时，崩溃子进程会被毫秒级无休止重拉；给一个最小节流。
	if len(o.Backoff.Delays) == 0 && o.Backoff.MinInterval <= 0 {
		o.Backoff.MinInterval = 100 * time.Millisecond
	}
	if o.StopTimeout == 0 {
		o.StopTimeout = 35 * time.Second
	}
	if o.Log == nil {
		o.Log = os.Stderr
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return &Supervisor{opts: o}
}

func (s *Supervisor) logf(format string, args ...any) {
	fmt.Fprintf(s.opts.Log, "[groot-supervisor] %s "+format+"\n",
		append([]any{time.Now().Format("2006-01-02 15:04:05")}, args...)...)
}

// Run 运行监督循环直到工作进程正常结束、ctx 被取消或连续快速失败达到上限。
// 返回监督进程应使用的退出码。
func (s *Supervisor) Run(ctx context.Context) int {
	fastFails := 0
	var lastSpawn time.Time

	for {
		if ctx.Err() != nil {
			return 0
		}
		if !lastSpawn.IsZero() {
			if wait := s.opts.Backoff.MinInterval - time.Since(lastSpawn); wait > 0 {
				if !sleepCtx(ctx, wait) {
					return 0
				}
			}
		}

		lastSpawn = time.Now()
		code, spawnErr := s.runOnce(ctx)
		alive := time.Since(lastSpawn)

		if ctx.Err() != nil {
			// 外部要求关闭：不再拉起。子进程干净退出（0 或请求重启的 3）则 0，否则 1。
			if spawnErr == nil && (code == ExitOK || code == ExitRestart) {
				return 0
			}
			return 1
		}

		switch {
		case spawnErr == nil && code == ExitOK:
			s.logf("工作进程正常退出，监督进程结束")
			return 0
		case spawnErr == nil && code == ExitRestart:
			s.logf("工作进程请求重启（存活 %s）", alive.Round(time.Millisecond))
			fastFails = 0
			continue
		}

		// 异常退出或拉起失败
		if spawnErr != nil {
			s.logf("拉起工作进程失败: %v", spawnErr)
		} else {
			s.logf("工作进程异常退出，退出码 %d（存活 %s）", code, alive.Round(time.Millisecond))
		}
		if s.opts.Backoff.FastFailWindow > 0 && alive < s.opts.Backoff.FastFailWindow {
			fastFails++
		} else {
			fastFails = 0
		}
		if fastFails >= s.opts.Backoff.MaxFastFails {
			s.logf("连续 %d 次快速失败，放弃拉起，监督进程以退出码 1 结束", fastFails)
			return 1
		}
		delay := s.opts.Backoff.Delay(fastFails) // Delay(0) 取 Delays[0]
		if fastFails > 0 {
			s.logf("第 %d 次快速失败，%s 后重新拉起", fastFails, delay)
		} else {
			s.logf("非快速失败，%s 后重新拉起", delay)
		}
		if !sleepCtx(ctx, delay) {
			return 1
		}
	}
}

// runOnce 拉起一个工作进程并等待其结束。ctx 取消时关闭 stdin 管道通知其退出，
// 超过 StopTimeout 仍未退出则强制 Kill。返回退出码；拉起失败返回 spawnErr。
func (s *Supervisor) runOnce(ctx context.Context) (code int, spawnErr error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return 1, fmt.Errorf("创建管道失败: %w", err)
	}
	defer pr.Close()
	defer pw.Close()

	cmd := exec.Command(s.opts.Exe, s.opts.Args...)
	cmd.Stdin = pr
	cmd.Stdout = s.opts.Stdout
	cmd.Stderr = s.opts.Stderr
	cmd.Env = append(append(os.Environ(), EnvSupervised+"=1"), s.opts.ExtraEnv...)
	// Stdout/Stderr 非 *os.File 时 exec 会用管道拷贝；工作进程被 Kill 后若其孙进程（MCP stdio）仍持有管道写端，
	// Wait 会一直等。WaitDelay 让 Wait 在进程退出后最多再等 5 秒就放弃拷贝。
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Start(); err != nil {
		return 1, err
	}
	s.logf("已拉起工作进程 PID %d", cmd.Process.Pid)
	// 子进程已继承读端，父进程的副本立即关闭，保证关闭写端时子进程能读到 EOF。
	pr.Close()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	select {
	case err := <-waitDone:
		return exitCodeOf(err), nil
	case <-ctx.Done():
	}

	s.logf("收到关闭请求，通知工作进程 PID %d 退出", cmd.Process.Pid)
	pw.Close()
	select {
	case err := <-waitDone:
		return exitCodeOf(err), nil
	case <-time.After(s.opts.StopTimeout):
		s.logf("工作进程 %s 内未退出，强制终止", s.opts.StopTimeout)
		_ = cmd.Process.Kill()
		return exitCodeOf(<-waitDone), nil // 被 Kill 时 ExitCode 为 -1，exitCodeOf 归一为 1
	}
}

// exitCodeOf 把 cmd.Wait 的错误换算为退出码：nil→0；ExitError 取其码（被信号杀死时为 -1，按 1 处理）；其他错误→1。
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if c := ee.ExitCode(); c >= 0 {
			return c
		}
	}
	return 1
}

// sleepCtx 等待 d；ctx 先取消则返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
