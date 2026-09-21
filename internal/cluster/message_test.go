// internal/cluster/message_test.go
package cluster

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/memberdb"
	"github.com/zfd81/groot/internal/repo/messagedb"
)

// newTestDB 建一个临时 SQLite 库。多个 MessageService 共用同一个 *sqlx.DB
// 即可模拟多实例共享数据库。
func newTestDB(t *testing.T) (*sqlx.DB, db.Dialect) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return sqlxDB, dialect
}

func newTestMessageService(t *testing.T, sqlxDB *sqlx.DB, dialect db.Dialect, instanceID string) *MessageService {
	t.Helper()
	return NewMessageService(messagedb.New(sqlxDB, dialect), newTestLogger(),
		func() string { return instanceID })
}

// flakyMessageRepo 让前 failures 次 Insert 返回错误，用于验证 SendMessage 的重试。
type flakyMessageRepo struct {
	repo.MessageRepo
	failures int32
	calls    int32
}

func (r *flakyMessageRepo) Insert(ctx context.Context, m *repo.ClusterMessage) error {
	atomic.AddInt32(&r.calls, 1)
	if atomic.AddInt32(&r.failures, -1) >= 0 {
		return errors.New("db down")
	}
	return r.MessageRepo.Insert(ctx, m)
}

func validMessage() Message {
	return Message{
		Type:         "sync_resource",
		TargetModule: "resource_sync",
		Payload:      map[string]any{"version": "v1.2.3"},
	}
}

// ---------- SendMessage ----------

func TestSendMessage_PersistsWithDefaults(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	before := time.Now()

	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	pending, err := ms.repo.ListPending(context.Background(), "inst-B", time.Now(), 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("len = %d, want 1", len(pending))
	}
	m := pending[0]
	if m.Type != "sync_resource" || m.TargetModule != "resource_sync" || m.TargetInstance != "" {
		t.Errorf("routing fields mismatch: %+v", m)
	}
	if m.SourceInstance != "inst-A" {
		t.Errorf("source = %q, want inst-A", m.SourceInstance)
	}
	if m.Priority != defaultPriority {
		t.Errorf("priority = %d, want default %d", m.Priority, defaultPriority)
	}
	if m.Payload != `{"version":"v1.2.3"}` {
		t.Errorf("payload = %s", m.Payload)
	}
	ttl := m.ExpiresAt.Sub(before)
	if ttl < defaultMessageTTL-time.Minute || ttl > defaultMessageTTL+time.Minute {
		t.Errorf("default TTL not applied: expires in %v", ttl)
	}
}

func TestSendMessage_NilPayloadStoredAsEmptyObject(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	msg := validMessage()
	msg.Payload = nil
	if err := ms.SendMessage(context.Background(), msg); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	pending, _ := ms.repo.ListPending(context.Background(), "inst-B", time.Now(), 10)
	if len(pending) != 1 {
		t.Fatalf("len = %d, want 1", len(pending))
	}
	if pending[0].Payload != "{}" {
		t.Errorf("payload = %q, want {}", pending[0].Payload)
	}
}

func TestSendMessage_Validation(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	ctx := context.Background()

	noType := validMessage()
	noType.Type = ""
	if err := ms.SendMessage(ctx, noType); !errors.Is(err, ErrMessageTypeRequired) {
		t.Errorf("empty type: err = %v, want ErrMessageTypeRequired", err)
	}

	noModule := validMessage()
	noModule.TargetModule = ""
	if err := ms.SendMessage(ctx, noModule); !errors.Is(err, ErrTargetModuleRequired) {
		t.Errorf("empty module: err = %v, want ErrTargetModuleRequired", err)
	}

	past := validMessage()
	past.ExpiresAt = time.Now().Add(-time.Second)
	if err := ms.SendMessage(ctx, past); !errors.Is(err, ErrExpiresInPast) {
		t.Errorf("past expires: err = %v, want ErrExpiresInPast", err)
	}

	big := validMessage()
	big.Payload = map[string]any{"blob": strings.Repeat("x", maxPayloadBytes+1)}
	if err := ms.SendMessage(ctx, big); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("oversized payload: err = %v, want ErrPayloadTooLarge", err)
	}

	pending, _ := ms.repo.ListPending(ctx, "inst-B", time.Now(), 10)
	if len(pending) != 0 {
		t.Errorf("invalid messages must not be persisted, got %d", len(pending))
	}
}

func TestSendMessage_RetriesOnceThenSucceeds(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	flaky := &flakyMessageRepo{MessageRepo: messagedb.New(sqlxDB, dialect), failures: 1}
	ms := NewMessageService(flaky, newTestLogger(), func() string { return "inst-A" })

	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("expected success after one retry, got %v", err)
	}
	if atomic.LoadInt32(&flaky.calls) != 2 {
		t.Errorf("Insert calls = %d, want 2", atomic.LoadInt32(&flaky.calls))
	}
}

func TestSendMessage_FailsAfterSecondError(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	flaky := &flakyMessageRepo{MessageRepo: messagedb.New(sqlxDB, dialect), failures: 2}
	ms := NewMessageService(flaky, newTestLogger(), func() string { return "inst-A" })

	err := ms.SendMessage(context.Background(), validMessage())
	if err == nil {
		t.Fatal("expected error after two failed inserts")
	}
	if !strings.Contains(err.Error(), "db down") {
		t.Errorf("error should wrap underlying cause, got %v", err)
	}
	if atomic.LoadInt32(&flaky.calls) != 2 {
		t.Errorf("Insert calls = %d, want exactly 2 (no third attempt)", atomic.LoadInt32(&flaky.calls))
	}
}

// ---------- RegisterHandler ----------

func TestRegisterHandler_LookupAndOverwrite(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")

	if ms.handler("mod") != nil {
		t.Fatal("unregistered module should return nil handler")
	}
	first := HandlerFunc(func(ctx context.Context, msg Message) error { return errors.New("first") })
	second := HandlerFunc(func(ctx context.Context, msg Message) error { return errors.New("second") })
	ms.RegisterHandler("mod", first)
	ms.RegisterHandler("mod", second)

	err := ms.handler("mod").Handle(context.Background(), Message{})
	if err == nil || err.Error() != "second" {
		t.Errorf("later registration should win, got %v", err)
	}
}

// ---------- Poll / processMessage ----------

// recorder 记录处理器收到的消息。
type recorder struct {
	mu   sync.Mutex
	got  []Message
	fail error
}

func (r *recorder) Handle(ctx context.Context, msg Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, msg)
	return r.fail
}

func (r *recorder) messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.got...)
}

func consumersOf(t *testing.T, ms *MessageService, instanceID string) []*repo.MessageConsumer {
	t.Helper()
	// 找到库里所有消息（用一个从未消费过的假实例视角）
	all, err := ms.repo.ListPending(context.Background(), "__probe__", time.Now(), 100)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	var out []*repo.MessageConsumer
	for _, m := range all {
		cs, err := ms.repo.ListConsumers(context.Background(), m.ID)
		if err != nil {
			t.Fatalf("ListConsumers: %v", err)
		}
		for _, c := range cs {
			if c.InstanceID == instanceID {
				out = append(out, c)
			}
		}
	}
	return out
}

func TestPoll_DeliversToHandlerAndRecordsSuccess(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	rec := &recorder{}
	ms.RegisterHandler("resource_sync", rec)

	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	ms.Poll(context.Background())

	got := rec.messages()
	if len(got) != 1 {
		t.Fatalf("handler called %d times, want 1", len(got))
	}
	if got[0].Type != "sync_resource" || got[0].Payload["version"] != "v1.2.3" || got[0].ID == 0 {
		t.Errorf("handler received %+v", got[0])
	}
	cs := consumersOf(t, ms, "inst-A")
	if len(cs) != 1 || cs[0].Status != repo.ConsumeStatusSuccess || cs[0].ErrorMessage != "" {
		t.Errorf("consumer records = %+v", cs)
	}
}

func TestPoll_DoesNotRedeliverConsumedMessage(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	rec := &recorder{}
	ms.RegisterHandler("resource_sync", rec)
	ms.SendMessage(context.Background(), validMessage())

	ms.Poll(context.Background())
	ms.Poll(context.Background())
	ms.Poll(context.Background())

	if n := len(rec.messages()); n != 1 {
		t.Errorf("handler called %d times across 3 polls, want 1", n)
	}
}

func TestPoll_HandlerErrorRecordedAsFailedNoRetry(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	rec := &recorder{fail: errors.New("sync exploded")}
	ms.RegisterHandler("resource_sync", rec)
	ms.SendMessage(context.Background(), validMessage())

	ms.Poll(context.Background())
	ms.Poll(context.Background())

	if n := len(rec.messages()); n != 1 {
		t.Errorf("failed message must not be retried, handler called %d times", n)
	}
	cs := consumersOf(t, ms, "inst-A")
	if len(cs) != 1 || cs[0].Status != repo.ConsumeStatusFailed || cs[0].ErrorMessage != "sync exploded" {
		t.Errorf("consumer records = %+v", cs)
	}
}

func TestPoll_HandlerPanicRecordedAsFailed(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	ms.RegisterHandler("resource_sync", HandlerFunc(func(ctx context.Context, msg Message) error {
		panic("boom")
	}))
	ms.SendMessage(context.Background(), validMessage())

	ms.Poll(context.Background()) // 不能让 panic 逃出

	cs := consumersOf(t, ms, "inst-A")
	if len(cs) != 1 || cs[0].Status != repo.ConsumeStatusFailed || !strings.Contains(cs[0].ErrorMessage, "panic") {
		t.Errorf("consumer records = %+v", cs)
	}
}

func TestPoll_UnknownModuleRecordedAsFailed(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	// 不注册任何处理器
	ms.SendMessage(context.Background(), validMessage())

	ms.Poll(context.Background())

	cs := consumersOf(t, ms, "inst-A")
	if len(cs) != 1 || cs[0].Status != repo.ConsumeStatusFailed || !strings.Contains(cs[0].ErrorMessage, "resource_sync") {
		t.Errorf("consumer records = %+v", cs)
	}
}

func TestPoll_BroadcastReachesEveryInstance(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	a := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	b := newTestMessageService(t, sqlxDB, dialect, "inst-B")
	recA, recB := &recorder{}, &recorder{}
	a.RegisterHandler("resource_sync", recA)
	b.RegisterHandler("resource_sync", recB)

	a.SendMessage(context.Background(), validMessage()) // 广播
	a.Poll(context.Background())
	b.Poll(context.Background())

	if len(recA.messages()) != 1 || len(recB.messages()) != 1 {
		t.Fatalf("broadcast: A got %d, B got %d, want 1/1", len(recA.messages()), len(recB.messages()))
	}
	if recB.messages()[0].SourceInstance != "inst-A" {
		t.Errorf("source = %q, want inst-A", recB.messages()[0].SourceInstance)
	}
}

func TestPoll_PointToPointReachesOnlyTarget(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	a := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	b := newTestMessageService(t, sqlxDB, dialect, "inst-B")
	recA, recB := &recorder{}, &recorder{}
	a.RegisterHandler("restart", recA)
	b.RegisterHandler("restart", recB)

	msg := Message{Type: "restart", TargetModule: "restart", TargetInstance: "inst-B", Priority: 1}
	a.SendMessage(context.Background(), msg)
	a.Poll(context.Background())
	b.Poll(context.Background())

	if len(recA.messages()) != 0 || len(recB.messages()) != 1 {
		t.Errorf("point-to-point: A got %d, B got %d, want 0/1", len(recA.messages()), len(recB.messages()))
	}
}

func TestPoll_SkipsWhenInstanceIDEmpty(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	sender := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	sender.SendMessage(context.Background(), validMessage())

	unregistered := newTestMessageService(t, sqlxDB, dialect, "")
	rec := &recorder{}
	unregistered.RegisterHandler("resource_sync", rec)
	unregistered.Poll(context.Background())

	if len(rec.messages()) != 0 {
		t.Errorf("instance without reg_id must not consume, got %d", len(rec.messages()))
	}
}

func TestPoll_RespectsPriorityOrder(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	rec := &recorder{}
	ms.RegisterHandler("m", rec)

	ms.SendMessage(context.Background(), Message{Type: "low", TargetModule: "m", Priority: 9})
	ms.SendMessage(context.Background(), Message{Type: "high", TargetModule: "m", Priority: 1})
	ms.Poll(context.Background())

	got := rec.messages()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Type != "high" || got[1].Type != "low" {
		t.Errorf("order = [%s %s], want [high low]", got[0].Type, got[1].Type)
	}
}

// ---------- Cleanup ----------

func TestCleanup_RemovesMessagesOlderThan30Days(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	ctx := context.Background()

	old := &repo.ClusterMessage{Type: "old", Payload: "{}", TargetModule: "m", Priority: 5,
		CreatedAt: time.Now().Add(-31 * 24 * time.Hour), ExpiresAt: time.Now().Add(time.Hour)}
	if err := ms.repo.Insert(ctx, old); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	ms.SendMessage(ctx, Message{Type: "fresh", TargetModule: "m"})

	dm, dc, err := ms.Cleanup(ctx)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if dm != 1 || dc != 0 {
		t.Errorf("deleted messages=%d consumers=%d, want 1/0", dm, dc)
	}
	remaining, _ := ms.repo.ListPending(ctx, "__probe__", time.Now(), 10)
	if len(remaining) != 1 || remaining[0].Type != "fresh" {
		t.Errorf("remaining = %+v", remaining)
	}
}

// ---------- fromRepoMessage ----------

func TestFromRepoMessage_InvalidJSONReturnsError(t *testing.T) {
	_, err := fromRepoMessage(&repo.ClusterMessage{ID: 1, Type: "t", TargetModule: "m", Payload: `{not json`})
	if err == nil {
		t.Fatal("expected error for invalid payload JSON")
	}
	msg, err := fromRepoMessage(&repo.ClusterMessage{ID: 2, Type: "t", TargetModule: "m", Payload: ""})
	if err != nil {
		t.Fatalf("empty payload should be accepted: %v", err)
	}
	if msg.Payload == nil || len(msg.Payload) != 0 {
		t.Errorf("empty payload should become empty map, got %v", msg.Payload)
	}
}

// ---------- NewMessageCleanupTask ----------

func TestNewMessageCleanupTask_RunsCleanup(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	ms := newTestMessageService(t, sqlxDB, dialect, "inst-A")
	ctx := context.Background()

	old := &repo.ClusterMessage{Type: "old", Payload: "{}", TargetModule: "m", Priority: 5,
		CreatedAt: time.Now().Add(-40 * 24 * time.Hour), ExpiresAt: time.Now().Add(time.Hour)}
	if err := ms.repo.Insert(ctx, old); err != nil {
		t.Fatalf("insert old: %v", err)
	}

	task := NewMessageCleanupTask(ms, newTestLogger())
	task()

	remaining, _ := ms.repo.ListPending(ctx, "__probe__", time.Now(), 10)
	if len(remaining) != 0 {
		t.Errorf("cleanup task should have removed old message, %d remain", len(remaining))
	}
}

// ---------- Cluster 集成 ----------

func TestCluster_TriggerPoll_DeliversMessage(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	c := newManualCluster(t, 8080, memberdb.New(sqlxDB, dialect))
	ms := NewMessageService(messagedb.New(sqlxDB, dialect), newTestLogger(), c.RegID)
	c.SetMessageService(ms)

	done := make(chan Message, 1)
	ms.RegisterHandler("resource_sync", HandlerFunc(func(ctx context.Context, msg Message) error {
		done <- msg
		return nil
	}))
	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	c.triggerPoll()

	select {
	case msg := <-done:
		if msg.SourceInstance != c.RegID() {
			t.Errorf("source = %q, want own reg_id %q", msg.SourceInstance, c.RegID())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler not invoked after triggerPoll")
	}
}

func TestCluster_TriggerPoll_SkipsWhilePreviousRoundInProgress(t *testing.T) {
	sqlxDB, dialect := newTestDB(t)
	c := newManualCluster(t, 8080, memberdb.New(sqlxDB, dialect))
	ms := NewMessageService(messagedb.New(sqlxDB, dialect), newTestLogger(), c.RegID)
	c.SetMessageService(ms)

	started := make(chan struct{}, 2) // 容量 2：即使错误地跑了第二轮也不会阻塞处理器
	release := make(chan struct{})
	var calls int32
	ms.RegisterHandler("resource_sync", HandlerFunc(func(ctx context.Context, msg Message) error {
		atomic.AddInt32(&calls, 1)
		started <- struct{}{}
		<-release
		return nil
	}))
	if err := ms.SendMessage(context.Background(), validMessage()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	c.triggerPoll()
	<-started       // 第一轮已进入处理器，此时 polling 必为 1
	c.triggerPoll() // 同步返回；CAS 失败即跳过
	if atomic.LoadInt32(&c.polling) != 1 {
		t.Fatal("polling flag should be 1 while first round in progress")
	}
	close(release)

	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&c.polling) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("polling flag not reset after round completes")
		}
		time.Sleep(time.Millisecond)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("handler called %d times, want 1", n)
	}
}

func TestCluster_TriggerPoll_NoServiceIsNoop(t *testing.T) {
	c := newManualCluster(t, 8080, newTestRepo(t))
	c.triggerPoll() // 不应 panic
	if atomic.LoadInt32(&c.polling) != 0 {
		t.Error("polling flag must stay 0 without message service")
	}
}
