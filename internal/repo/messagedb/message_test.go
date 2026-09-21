// internal/repo/messagedb/message_test.go
package messagedb

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.MessageRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func newMsg(typ, target string, priority int, ttl time.Duration) *repo.ClusterMessage {
	now := time.Now()
	return &repo.ClusterMessage{
		Type:           typ,
		Payload:        `{"k":"v"}`,
		TargetInstance: target,
		TargetModule:   "mod",
		Priority:       priority,
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
		SourceInstance: "sender",
	}
}

func mustInsert(t *testing.T, r repo.MessageRepo, m *repo.ClusterMessage) {
	t.Helper()
	if err := r.Insert(context.Background(), m); err != nil {
		t.Fatalf("Insert: %v", err)
	}
}

func TestInsertAndListPending_Broadcast(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))

	for _, inst := range []string{"inst-A", "inst-B"} {
		got, err := r.ListPending(ctx, inst, time.Now(), 10)
		if err != nil {
			t.Fatalf("ListPending(%s): %v", inst, err)
		}
		if len(got) != 1 {
			t.Fatalf("ListPending(%s) len = %d, want 1", inst, len(got))
		}
		m := got[0]
		if m.ID == 0 {
			t.Error("ID should be populated from db")
		}
		if m.Type != "sync" || m.Payload != `{"k":"v"}` || m.TargetModule != "mod" ||
			m.Priority != 5 || m.SourceInstance != "sender" || m.TargetInstance != "" {
			t.Errorf("round-trip mismatch: %+v", m)
		}
		if m.ExpiresAt.Sub(m.CreatedAt) < 59*time.Minute {
			t.Errorf("timestamps not preserved: created=%v expires=%v", m.CreatedAt, m.ExpiresAt)
		}
	}
}

func TestListPending_PointToPointOnlyTarget(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("restart", "inst-B", 5, time.Hour))

	a, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	b, _ := r.ListPending(ctx, "inst-B", time.Now(), 10)
	if len(a) != 0 {
		t.Errorf("inst-A should not see point-to-point message for inst-B, got %d", len(a))
	}
	if len(b) != 1 {
		t.Errorf("inst-B should see its message, got %d", len(b))
	}
}

func TestListPending_ExcludesExpired(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("old", "", 5, -time.Minute)) // 已过期
	mustInsert(t, r, newMsg("fresh", "", 5, time.Hour))

	got, err := r.ListPending(ctx, "inst-A", time.Now(), 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 1 || got[0].Type != "fresh" {
		t.Errorf("expected only fresh message, got %+v", got)
	}
}

func TestListPending_ExcludesConsumedBySelfOnly(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	first, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	if len(first) != 1 {
		t.Fatalf("setup: expected 1 pending, got %d", len(first))
	}

	err := r.RecordConsumption(ctx, &repo.MessageConsumer{
		MessageID: first[0].ID, InstanceID: "inst-A",
		ConsumedAt: time.Now(), Status: repo.ConsumeStatusSuccess,
	})
	if err != nil {
		t.Fatalf("RecordConsumption: %v", err)
	}

	a, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	b, _ := r.ListPending(ctx, "inst-B", time.Now(), 10)
	if len(a) != 0 {
		t.Errorf("inst-A already consumed, got %d pending", len(a))
	}
	if len(b) != 1 {
		t.Errorf("inst-B has not consumed, got %d pending", len(b))
	}
}

func TestListPending_OrderByPriorityThenCreated(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	base := time.Now()
	insertAt := func(typ string, prio int, at time.Time) {
		m := newMsg(typ, "", prio, time.Hour)
		m.CreatedAt = at
		mustInsert(t, r, m)
	}
	insertAt("low-late", 9, base.Add(2*time.Second))
	insertAt("high-late", 1, base.Add(3*time.Second))
	insertAt("low-early", 9, base.Add(1*time.Second))
	insertAt("high-early", 1, base)

	got, err := r.ListPending(ctx, "inst-A", time.Now(), 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	want := []string{"high-early", "high-late", "low-early", "low-late"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Type != w {
			t.Errorf("pos %d = %s, want %s", i, got[i].Type, w)
		}
	}
}

func TestListPending_Limit(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	}
	got, err := r.ListPending(ctx, "inst-A", time.Now(), 3)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestRecordConsumption_DuplicateFails(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	pending, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)

	c := &repo.MessageConsumer{MessageID: pending[0].ID, InstanceID: "inst-A",
		ConsumedAt: time.Now(), Status: repo.ConsumeStatusSuccess}
	if err := r.RecordConsumption(ctx, c); err != nil {
		t.Fatalf("first RecordConsumption: %v", err)
	}
	if err := r.RecordConsumption(ctx, c); err == nil {
		t.Error("second RecordConsumption with same (message_id, instance_id) should fail")
	}
}

func TestListConsumers(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	pending, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	id := pending[0].ID

	r.RecordConsumption(ctx, &repo.MessageConsumer{MessageID: id, InstanceID: "inst-B",
		ConsumedAt: time.Now(), Status: repo.ConsumeStatusFailed, ErrorMessage: "boom"})
	r.RecordConsumption(ctx, &repo.MessageConsumer{MessageID: id, InstanceID: "inst-A",
		ConsumedAt: time.Now(), Status: repo.ConsumeStatusSuccess})

	got, err := r.ListConsumers(ctx, id)
	if err != nil {
		t.Fatalf("ListConsumers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].InstanceID != "inst-A" || got[0].Status != repo.ConsumeStatusSuccess || got[0].ErrorMessage != "" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].InstanceID != "inst-B" || got[1].Status != repo.ConsumeStatusFailed || got[1].ErrorMessage != "boom" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestDeleteBefore(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	old := newMsg("old", "", 5, time.Hour)
	old.CreatedAt = time.Now().Add(-40 * 24 * time.Hour)
	mustInsert(t, r, old)
	mustInsert(t, r, newMsg("fresh", "", 5, time.Hour))

	all, _ := r.ListPending(ctx, "inst-A", time.Now(), 10)
	if len(all) != 2 {
		t.Fatalf("setup: expected 2 messages, got %d", len(all))
	}
	for _, m := range all {
		r.RecordConsumption(ctx, &repo.MessageConsumer{MessageID: m.ID, InstanceID: "inst-A",
			ConsumedAt: time.Now(), Status: repo.ConsumeStatusSuccess})
	}

	dm, dc, err := r.DeleteBefore(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if dm != 1 || dc != 1 {
		t.Errorf("deleted messages=%d consumers=%d, want 1/1", dm, dc)
	}

	remaining, _ := r.ListPending(ctx, "inst-B", time.Now(), 10)
	if len(remaining) != 1 || remaining[0].Type != "fresh" {
		t.Errorf("expected only fresh remaining, got %+v", remaining)
	}
}

func TestListPending_NonPositiveLimitReturnsEmpty(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustInsert(t, r, newMsg("sync", "", 5, time.Hour))
	for _, limit := range []int{0, -1} {
		got, err := r.ListPending(ctx, "inst-A", time.Now(), limit)
		if err != nil {
			t.Fatalf("ListPending(limit=%d): %v", limit, err)
		}
		if len(got) != 0 {
			t.Errorf("limit=%d: len = %d, want 0", limit, len(got))
		}
	}
}
