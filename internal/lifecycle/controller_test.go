package lifecycle

import (
	"errors"
	"testing"
	"time"
)

func TestController_FirstReasonWins(t *testing.T) {
	c := NewController(RoleWorker)
	c.RequestStop(ReasonSignal)
	if err := c.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart 在工作进程模式下不应报错: %v", err)
	}
	c.RequestStop(ReasonSupervisorClosed)

	select {
	case r := <-c.Done():
		if r != ReasonSignal {
			t.Errorf("Done() = %v, want ReasonSignal（第一个原因生效）", r)
		}
	case <-time.After(time.Second):
		t.Fatal("Done() 未收到停止原因")
	}
	if got := c.Reason(); got != ReasonSignal {
		t.Errorf("Reason() = %v, want ReasonSignal", got)
	}
	if !c.Stopping() {
		t.Error("Stopping() 应为 true")
	}
}

func TestController_RestartReason(t *testing.T) {
	c := NewController(RoleWorker)
	if err := c.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart: %v", err)
	}
	select {
	case r := <-c.Done():
		if r != ReasonRestart {
			t.Errorf("Done() = %v, want ReasonRestart", r)
		}
	case <-time.After(time.Second):
		t.Fatal("Done() 未收到停止原因")
	}
}

func TestController_NotStoppingInitially(t *testing.T) {
	c := NewController(RoleWorker)
	if c.Stopping() {
		t.Error("初始 Stopping() 应为 false")
	}
	if got := c.Reason(); got != ReasonNone {
		t.Errorf("初始 Reason() = %v, want ReasonNone", got)
	}
	select {
	case r := <-c.Done():
		t.Fatalf("未请求停止时 Done() 不应返回，got %v", r)
	default:
	}
}

func TestController_RequestStopNoneIsIgnored(t *testing.T) {
	c := NewController(RoleWorker)
	c.RequestStop(ReasonNone)
	if c.Stopping() {
		t.Fatal("RequestStop(ReasonNone) 不应进入停止状态")
	}
	c.RequestStop(ReasonSignal)
	if got := c.Reason(); got != ReasonSignal {
		t.Fatalf("Reason() = %v, want ReasonSignal（ReasonNone 不应消耗 Once）", got)
	}
}

func TestController_SingleRejectsRestart(t *testing.T) {
	c := NewController(RoleSingle)
	err := c.RequestRestart()
	if !errors.Is(err, ErrRestartUnsupported) {
		t.Fatalf("RequestRestart = %v, want ErrRestartUnsupported", err)
	}
	if c.Stopping() {
		t.Error("单进程模式拒绝重启后不应进入停止状态")
	}
	if c.RestartSupported() {
		t.Error("单进程模式 RestartSupported() 应为 false")
	}
	if !NewController(RoleWorker).RestartSupported() {
		t.Error("工作进程模式 RestartSupported() 应为 true")
	}
}

func TestReason_ExitCode(t *testing.T) {
	cases := []struct {
		reason Reason
		want   int
	}{
		{ReasonNone, ExitOK},
		{ReasonSignal, ExitOK},
		{ReasonSupervisorClosed, ExitOK},
		{ReasonRestart, ExitRestart},
	}
	for _, c := range cases {
		if got := c.reason.ExitCode(); got != c.want {
			t.Errorf("%v.ExitCode() = %d, want %d", c.reason, got, c.want)
		}
	}
	if ExitRestart != 3 {
		t.Errorf("ExitRestart = %d, want 3（避开 Go 运行时 panic 的退出码 2）", ExitRestart)
	}
}

func TestReason_String(t *testing.T) {
	cases := map[Reason]string{
		ReasonNone:             "none",
		ReasonSignal:           "signal",
		ReasonSupervisorClosed: "supervisor_closed",
		ReasonRestart:          "restart",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(r), got, want)
		}
	}
}
