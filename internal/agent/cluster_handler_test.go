package agent

import (
	"context"
	"testing"

	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/logger"
)

func cancelMsg(payload map[string]any) cluster.Message {
	return cluster.Message{Type: MessageTypeCancel, TargetModule: ClusterModuleName, Payload: payload}
}

func TestClusterHandler_CancelsLocal(t *testing.T) {
	rt := NewRuntimeState()
	chat, err := rt.Register(context.Background(), "s1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	h := NewClusterHandler(rt, logger.NewNop())
	if err := h.Handle(context.Background(), cancelMsg(map[string]any{"session_id": "s1", "chat_id": "c1"})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-chat.Context().Done():
	default:
		t.Fatal("not cancelled")
	}
	if got := chat.StatusSnapshot(); got != "cancelled" {
		t.Errorf("status %q", got)
	}
}

func TestClusterHandler_MissIsNotError(t *testing.T) {
	h := NewClusterHandler(NewRuntimeState(), logger.NewNop())
	if err := h.Handle(context.Background(), cancelMsg(map[string]any{"session_id": "s1", "chat_id": "c1"})); err != nil {
		t.Fatalf("miss should not be an error: %v", err)
	}
}

func TestClusterHandler_ChatIDMismatchIsNotError(t *testing.T) {
	rt := NewRuntimeState()
	chat, _ := rt.Register(context.Background(), "s1", "c1")
	h := NewClusterHandler(rt, logger.NewNop())
	if err := h.Handle(context.Background(), cancelMsg(map[string]any{"session_id": "s1", "chat_id": "c-other"})); err != nil {
		t.Fatalf("mismatch should not be an error: %v", err)
	}
	select {
	case <-chat.Context().Done():
		t.Fatal("wrong chat was cancelled")
	default:
	}
}

func TestClusterHandler_EmptyChatIDMatchesAny(t *testing.T) {
	for name, payload := range map[string]map[string]any{
		"empty":      {"session_id": "s1", "chat_id": ""},
		"absent":     {"session_id": "s1"},
		"non-string": {"session_id": "s1", "chat_id": 42},
	} {
		t.Run(name, func(t *testing.T) {
			rt := NewRuntimeState()
			chat, _ := rt.Register(context.Background(), "s1", "c1")
			h := NewClusterHandler(rt, logger.NewNop())
			if err := h.Handle(context.Background(), cancelMsg(payload)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-chat.Context().Done():
			default:
				t.Fatal("not cancelled")
			}
		})
	}
}

func TestClusterHandler_BadPayload(t *testing.T) {
	cases := map[string]cluster.Message{
		"missing session_id":    cancelMsg(map[string]any{"chat_id": "c1"}),
		"nil payload":           cancelMsg(nil),
		"session_id non-string": cancelMsg(map[string]any{"session_id": 1, "chat_id": "c1"}),
		"session_id empty":      cancelMsg(map[string]any{"session_id": "", "chat_id": "c1"}),
		"wrong type":            {Type: "restart", TargetModule: ClusterModuleName, Payload: map[string]any{"session_id": "s1"}},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			rt := NewRuntimeState()
			chat, _ := rt.Register(context.Background(), "s1", "c1")
			h := NewClusterHandler(rt, logger.NewNop())
			if err := h.Handle(context.Background(), msg); err == nil {
				t.Fatal("expected error")
			}
			select {
			case <-chat.Context().Done():
				t.Fatal("chat must not be cancelled on bad message")
			default:
			}
		})
	}
}

func TestClusterHandler_NilLoggerDefaultsToNop(t *testing.T) {
	h := NewClusterHandler(NewRuntimeState(), nil)
	if err := h.Handle(context.Background(), cancelMsg(map[string]any{"session_id": "s1"})); err != nil {
		t.Fatal(err)
	}
}
