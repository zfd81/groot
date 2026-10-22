package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/cluster"
)

type fakeSender struct {
	msgs []cluster.Message
	err  error
}

func (f *fakeSender) SendMessage(_ context.Context, m cluster.Message) error {
	f.msgs = append(f.msgs, m)
	return f.err
}

func alive(v bool) func(context.Context) bool { return func(context.Context) bool { return v } }

func TestClusterCanceller_LocalHitDoesNotBroadcast(t *testing.T) {
	rt := NewRuntimeState()
	chat, err := rt.Register(context.Background(), "s1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeSender{}
	res, err := NewClusterCanceller(rt, fs, alive(true)).Cancel(context.Background(), "s1", "c1")
	if res != CancelLocal || err != nil || len(fs.msgs) != 0 {
		t.Fatalf("res=%v err=%v msgs=%d", res, err, len(fs.msgs))
	}
	select {
	case <-chat.Context().Done():
	default:
		t.Fatal("local chat not cancelled")
	}
}

func TestClusterCanceller_MissBroadcasts(t *testing.T) {
	rt := NewRuntimeState()
	fs := &fakeSender{}
	res, err := NewClusterCanceller(rt, fs, alive(true)).Cancel(context.Background(), "s1", "c1")
	if res != CancelBroadcast || err != nil || len(fs.msgs) != 1 {
		t.Fatalf("res=%v err=%v msgs=%d", res, err, len(fs.msgs))
	}
	m := fs.msgs[0]
	if m.Type != MessageTypeCancel || m.TargetModule != ClusterModuleName || m.TargetInstance != "" {
		t.Errorf("%+v", m)
	}
	if m.Payload["session_id"] != "s1" || m.Payload["chat_id"] != "c1" {
		t.Errorf("payload %+v", m.Payload)
	}
	if d := time.Until(m.ExpiresAt); d < 25*time.Second || d > 31*time.Second {
		t.Errorf("expires in %v", d)
	}
}

func TestClusterCanceller_ChatIDMismatchBroadcasts(t *testing.T) {
	rt := NewRuntimeState()
	rt.Register(context.Background(), "s1", "c1")
	fs := &fakeSender{}
	res, err := NewClusterCanceller(rt, fs, alive(true)).Cancel(context.Background(), "s1", "c-other")
	if res != CancelBroadcast || err != nil || len(fs.msgs) != 1 {
		t.Fatalf("res=%v err=%v msgs=%d", res, err, len(fs.msgs))
	}
}

func TestClusterCanceller_NilSenderDoesNotBroadcast(t *testing.T) {
	res, err := NewClusterCanceller(NewRuntimeState(), nil, alive(true)).Cancel(context.Background(), "s1", "c1")
	if res != CancelNotRunning || err != nil {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestClusterCanceller_SendError(t *testing.T) {
	fs := &fakeSender{err: errors.New("db down")}
	res, err := NewClusterCanceller(NewRuntimeState(), fs, alive(true)).Cancel(context.Background(), "s1", "c1")
	if res != CancelNotRunning || err == nil {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestClusterCanceller_NoOthersAliveDoesNotBroadcast(t *testing.T) {
	for name, probe := range map[string]func(context.Context) bool{
		"probe false": alive(false),
		"probe nil":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			fs := &fakeSender{}
			c := NewClusterCanceller(NewRuntimeState(), fs, probe)
			if c.OthersAlive(context.Background()) {
				t.Fatal("OthersAlive should be false")
			}
			res, err := c.Cancel(context.Background(), "s1", "c1")
			if res != CancelNotRunning || err != nil || len(fs.msgs) != 0 {
				t.Fatalf("res=%v err=%v msgs=%d", res, err, len(fs.msgs))
			}
		})
	}
}

func TestClusterCanceller_OthersAliveBroadcasts(t *testing.T) {
	fs := &fakeSender{}
	c := NewClusterCanceller(NewRuntimeState(), fs, alive(true))
	if !c.OthersAlive(context.Background()) {
		t.Fatal("OthersAlive should be true")
	}
	res, err := c.Cancel(context.Background(), "s1", "c1")
	if res != CancelBroadcast || err != nil || len(fs.msgs) != 1 {
		t.Fatalf("res=%v err=%v msgs=%d", res, err, len(fs.msgs))
	}
}

func TestCancelResult_String(t *testing.T) {
	cases := map[CancelResult]string{
		CancelLocal:      "local",
		CancelBroadcast:  "broadcast",
		CancelNotRunning: "not_running",
		CancelResult(99): "CancelResult(99)",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("%d: got %q want %q", int(r), got, want)
		}
	}
}
