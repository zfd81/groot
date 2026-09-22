package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/logger"
)

func restartMsg(createdAt time.Time) cluster.Message {
	return cluster.Message{
		ID:             1,
		Type:           MessageTypeRestart,
		TargetInstance: "reg-1",
		TargetModule:   ModuleName,
		Payload:        map[string]any{"reason": "web", "requested_by": "u1"},
		CreatedAt:      createdAt,
	}
}

func expectDone(t *testing.T, c *Controller, want Reason, within time.Duration) {
	t.Helper()
	select {
	case r := <-c.Done():
		if r != want {
			t.Fatalf("Done() = %v, want %v", r, want)
		}
	case <-time.After(within):
		t.Fatalf("%v 内未收到停止原因", within)
	}
}

func expectNotDone(t *testing.T, c *Controller, within time.Duration) {
	t.Helper()
	select {
	case r := <-c.Done():
		t.Fatalf("不应触发停止，got %v", r)
	case <-time.After(within):
	}
}

func TestClusterHandler_FreshMessageTriggersRestart(t *testing.T) {
	startedAt := time.Now().Add(-time.Minute)
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, startedAt, logger.NewNop())
	h.delay = 20 * time.Millisecond

	if err := h.Handle(context.Background(), restartMsg(time.Now())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// 返回时尚未触发（延迟中），随后才触发
	expectNotDone(t, c, 5*time.Millisecond)
	expectDone(t, c, ReasonRestart, time.Second)
}

func TestClusterHandler_IgnoresMessageOlderThanStart(t *testing.T) {
	startedAt := time.Now()
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, startedAt, logger.NewNop())
	h.delay = 10 * time.Millisecond

	if err := h.Handle(context.Background(), restartMsg(startedAt.Add(-time.Second))); err != nil {
		t.Fatalf("过期消息应返回成功（记消费）而不是错误: %v", err)
	}
	expectNotDone(t, c, 100*time.Millisecond)
}

func TestClusterHandler_SingleModeRejects(t *testing.T) {
	c := NewController(RoleSingle)
	h := NewClusterHandler(c, time.Now().Add(-time.Minute), logger.NewNop())
	h.delay = 10 * time.Millisecond

	err := h.Handle(context.Background(), restartMsg(time.Now()))
	if !errors.Is(err, ErrRestartUnsupported) {
		t.Fatalf("Handle = %v, want ErrRestartUnsupported", err)
	}
	expectNotDone(t, c, 100*time.Millisecond)
}

func TestClusterHandler_DuplicateTriggersOnce(t *testing.T) {
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, time.Now().Add(-time.Minute), logger.NewNop())
	h.delay = 10 * time.Millisecond

	for i := 0; i < 3; i++ {
		if err := h.Handle(context.Background(), restartMsg(time.Now())); err != nil {
			t.Fatalf("Handle #%d: %v", i, err)
		}
	}
	expectDone(t, c, ReasonRestart, time.Second)
	// done 通道容量 1 且 Once 保证只发一次：再等一小段不应再收到
	expectNotDone(t, c, 50*time.Millisecond)
}

func TestClusterHandler_UnknownTypeIsError(t *testing.T) {
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, time.Now().Add(-time.Minute), logger.NewNop())
	m := restartMsg(time.Now())
	m.Type = "shutdown"
	if err := h.Handle(context.Background(), m); err == nil {
		t.Fatal("未知类型应返回错误")
	}
	expectNotDone(t, c, 50*time.Millisecond)
}

func TestClusterHandler_NilLoggerDoesNotPanic(t *testing.T) {
	c := NewController(RoleWorker)
	h := NewClusterHandler(c, time.Now().Add(-time.Minute), nil)
	h.delay = 10 * time.Millisecond
	if err := h.Handle(context.Background(), restartMsg(time.Now())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	expectDone(t, c, ReasonRestart, time.Second)
}
