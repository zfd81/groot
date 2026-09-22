package lifecycle

import (
	"errors"
	"sync"
)

// 工作进程退出码约定：监督进程据此决定下一步动作。
const (
	// ExitOK 正常结束，监督进程随之退出。
	ExitOK = 0
	// ExitRestart 请求重启，监督进程立即重新拉起。
	// 取 3：1 是通用错误，2 是 Go 运行时 panic 与 flag 解析失败的退出码。
	ExitRestart = 3
)

// ErrRestartUnsupported 在没有监督进程的模式下请求重启时返回：
// 此时以 ExitRestart 退出等同于停机，必须拒绝。
var ErrRestartUnsupported = errors.New("lifecycle: 实例以单进程模式运行，不支持重启")

// Reason 是工作进程停止的原因。
type Reason int

const (
	ReasonNone             Reason = iota // 尚未请求停止
	ReasonSignal                         // 收到 SIGINT / SIGTERM
	ReasonSupervisorClosed               // 监督进程关闭了 stdin 管道
	ReasonRestart                        // 收到重启指令
)

// String 返回用于日志的原因标识。
func (r Reason) String() string {
	switch r {
	case ReasonSignal:
		return "signal"
	case ReasonSupervisorClosed:
		return "supervisor_closed"
	case ReasonRestart:
		return "restart"
	default:
		return "none"
	}
}

// ExitCode 返回该原因对应的进程退出码。
func (r Reason) ExitCode() int {
	if r == ReasonRestart {
		return ExitRestart
	}
	return ExitOK
}

// Controller 把工作进程的所有停止来源收敛为唯一出口：
// 只有第一个到达的原因生效，之后的请求被忽略，保证关闭流程不会重入。
type Controller struct {
	role   Role
	once   sync.Once
	mu     sync.RWMutex
	reason Reason
	done   chan Reason
}

// NewController 创建控制器。role 决定是否接受重启请求。
func NewController(role Role) *Controller {
	return &Controller{role: role, done: make(chan Reason, 1)}
}

// RequestStop 请求以给定原因停止。只有第一次调用生效。传入 ReasonNone 会被忽略。
func (c *Controller) RequestStop(r Reason) {
	if r == ReasonNone {
		return
	}
	c.once.Do(func() {
		c.mu.Lock()
		c.reason = r
		c.mu.Unlock()
		c.done <- r
	})
}

// RequestRestart 请求重启。非工作进程模式返回 ErrRestartUnsupported 且不改变状态。
func (c *Controller) RequestRestart() error {
	if !c.role.RestartSupported() {
		return ErrRestartUnsupported
	}
	c.RequestStop(ReasonRestart)
	return nil
}

// RestartSupported 报告当前角色是否接受重启请求（供消息处理器在调度前判断）。
func (c *Controller) RestartSupported() bool {
	return c.role.RestartSupported()
}

// Done 返回一个通道，首次停止请求到达时收到其原因；只会发送一次。
// 通道容量 1、只发送一次，因此只能有一个接收者；其他地方请用 Stopping()/Reason() 查询。
func (c *Controller) Done() <-chan Reason {
	return c.done
}

// Reason 返回已记录的停止原因；未请求停止时为 ReasonNone。
func (c *Controller) Reason() Reason {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reason
}

// Stopping 报告是否已有停止请求。
func (c *Controller) Stopping() bool {
	return c.Reason() != ReasonNone
}
