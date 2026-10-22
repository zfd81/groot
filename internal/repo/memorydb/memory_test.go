package memorydb

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newMemRepo(t *testing.T) repo.MemoryRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func TestCreateAndGetSession(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	s := &repo.Session{SessionID: "sess-001", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := r.CreateSession(ctx, s); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := r.GetSession(ctx, "sess-001")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Round != 0 {
		t.Errorf("expected round=0, got %d", got.Round)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	r := newMemRepo(t)
	_, err := r.GetSession(context.Background(), "nonexistent")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestExistsSession(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	exists, err := r.ExistsSession(ctx, "no-such")
	if err != nil {
		t.Fatalf("ExistsSession: %v", err)
	}
	if exists {
		t.Error("should not exist")
	}
	r.CreateSession(ctx, &repo.Session{SessionID: "s-exists", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	exists, err = r.ExistsSession(ctx, "s-exists")
	if err != nil {
		t.Fatalf("ExistsSession: %v", err)
	}
	if !exists {
		t.Error("should exist")
	}
}

func TestListSessions(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "ls-1", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	r.CreateSession(ctx, &repo.Session{SessionID: "ls-2", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	sessions, err := r.ListSessions(ctx, nil, 100, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestListSessions_TitleFromColumn(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "lt-1", Title: "列标题", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	// 存一条主 Agent 对话；其指令不应再被当作 title（title 来自列而非现算）
	if err := r.SaveChat(ctx, &repo.ChatRecord{
		ChatID:      "20260901100000001",
		SessionID:   "lt-1",
		Instruction: "你是谁?",
		Status:      "completed",
		StartedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("SaveChat round 1: %v", err)
	}

	sessions, err := r.ListSessions(ctx, nil, 100, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Title != "列标题" {
		t.Errorf("expected title=列标题, got %q", sessions[0].Title)
	}
}

func TestListSessions_TitleEmptyWhenCreatedEmpty(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "lt-empty", Title: "", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	sessions, err := r.ListSessions(ctx, nil, 100, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Title != "" {
		t.Errorf("expected empty title, got %q", sessions[0].Title)
	}
}

func TestSession_TitleRoundTrip(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	s := &repo.Session{SessionID: "s-t", TenantID: "crm", UserID: "u1", Title: "第一轮问题", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := r.CreateSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetSession(ctx, "s-t")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "第一轮问题" {
		t.Errorf("Title=%q", got.Title)
	}
	list, _ := r.ListSessions(ctx, nil, 100, 0)
	if len(list) != 1 || list[0].Title != "第一轮问题" {
		t.Errorf("list title %+v", list)
	}
}

func TestUpdateTitle(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	created := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	_ = r.CreateSession(ctx, &repo.Session{SessionID: "s1", Title: "old", CreatedAt: created, UpdatedAt: created})
	if err := r.UpdateTitle(ctx, "s1", "new"); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetSession(ctx, "s1")
	if got.Title != "new" {
		t.Errorf("Title=%q", got.Title)
	}
	if !got.UpdatedAt.Equal(created) {
		t.Errorf("updated_at must not change: %v vs %v", got.UpdatedAt, created)
	}
	// 相同标题再写一次：MySQL 的 RowsAffected 只计"变更"行，会返回 0，
	// 实现须靠存在性检查兜底，仍返回 nil 而非 ErrNotFound。
	if err := r.UpdateTitle(ctx, "s1", "new"); err != nil {
		t.Errorf("identical title must be a no-op success, got %v", err)
	}
	if err := r.UpdateTitle(ctx, "missing", "x"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSaveChatIncreasesRound(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s1", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	rec := &repo.ChatRecord{
		ChatID:      "20260610143022001",
		SessionID:   "s1",
		Instruction: "hello",
		Status:      "completed",
		StartedAt:   time.Now(),
	}
	if err := r.SaveChat(ctx, rec); err != nil {
		t.Fatalf("SaveChat: %v", err)
	}
	sess, _ := r.GetSession(ctx, "s1")
	if sess.Round != 1 {
		t.Errorf("expected round=1, got %d", sess.Round)
	}
}

func TestSaveChat_SubAgentDoesNotAdvanceRound(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s-sub", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	// 主 Agent 第 1 轮
	if err := r.SaveChat(ctx, &repo.ChatRecord{
		ChatID:    "20260610143022100",
		SessionID: "s-sub",
		Status:    "completed",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("main SaveChat: %v", err)
	}
	// 子 Agent，挂在父轮次 1 上
	if err := r.SaveChat(ctx, &repo.ChatRecord{
		ChatID:    "20260610143022100_143022500_aaaa_db-agent",
		SessionID: "s-sub",
		Status:    "completed",
		StartedAt: time.Now(),
		AgentName: "db-agent",
		Round:     1,
	}); err != nil {
		t.Fatalf("child SaveChat: %v", err)
	}
	// 主 Agent 第 2 轮
	if err := r.SaveChat(ctx, &repo.ChatRecord{
		ChatID:    "20260610143023000",
		SessionID: "s-sub",
		Status:    "completed",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("main SaveChat 2: %v", err)
	}

	sess, err := r.GetSession(ctx, "s-sub")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Round != 2 {
		t.Errorf("expected session round=2 (sub-agent should not advance), got %d", sess.Round)
	}
	// 子 Agent 行 round 应回写父轮次（1）
	got, err := r.GetChat(ctx, "20260610143022100_143022500_aaaa_db-agent")
	if err != nil {
		t.Fatalf("GetChat sub: %v", err)
	}
	if got.Round != 1 {
		t.Errorf("expected child round=1, got %d", got.Round)
	}
}

func TestSaveChat_ModelRoundTrip(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s-mod", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	rec := &repo.ChatRecord{
		ChatID:    "20260610143022200",
		SessionID: "s-mod",
		Status:    "completed",
		Model:     "gpt-4-turbo",
		StartedAt: time.Now(),
	}
	if err := r.SaveChat(ctx, rec); err != nil {
		t.Fatalf("SaveChat: %v", err)
	}
	got, err := r.GetChat(ctx, "20260610143022200")
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if got.Model != "gpt-4-turbo" {
		t.Errorf("model round-trip mismatch: %q", got.Model)
	}
}

func TestSaveChat_TokensRoundTrip(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s-tok", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	rec := &repo.ChatRecord{
		ChatID:           "20260610143022300",
		SessionID:        "s-tok",
		Status:           "completed",
		StartedAt:        time.Now(),
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
		DurationMs:       2345,
	}
	if err := r.SaveChat(ctx, rec); err != nil {
		t.Fatalf("SaveChat: %v", err)
	}
	got, err := r.GetChat(ctx, "20260610143022300")
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if got.PromptTokens != 1000 || got.CompletionTokens != 500 || got.TotalTokens != 1500 {
		t.Errorf("token round-trip: prompt=%d completion=%d total=%d", got.PromptTokens, got.CompletionTokens, got.TotalTokens)
	}
	if got.DurationMs != 2345 {
		t.Errorf("DurationMs round-trip: %d", got.DurationMs)
	}
}

func TestSaveChat_SessionNotFound(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	rec := &repo.ChatRecord{
		ChatID:    "20260610143022099",
		SessionID: "no-such-session",
		Status:    "completed",
		StartedAt: time.Now(),
	}
	err := r.SaveChat(ctx, rec)
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetChat(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s-gc", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	rec := &repo.ChatRecord{
		ChatID:      "20260610143022010",
		SessionID:   "s-gc",
		Instruction: "test instruction",
		Result:      "test result",
		Status:      "completed",
		DurationMs:  1500,
		StartedAt:   time.Now(),
	}
	if err := r.SaveChat(ctx, rec); err != nil {
		t.Fatalf("SaveChat: %v", err)
	}
	got, err := r.GetChat(ctx, "20260610143022010")
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if got.Instruction != "test instruction" {
		t.Errorf("Instruction mismatch: %s", got.Instruction)
	}
	if got.DurationMs != 1500 {
		t.Errorf("DurationMs mismatch: %d", got.DurationMs)
	}
}

func TestGetChat_NotFound(t *testing.T) {
	r := newMemRepo(t)
	_, err := r.GetChat(context.Background(), "nonexistent-chat")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLoadHistory_ExcludesSubAgents(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s2", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	// main agent chat (agent_name="")
	r.SaveChat(ctx, &repo.ChatRecord{
		ChatID:      "20260610143022002",
		SessionID:   "s2",
		Instruction: "main",
		Status:      "completed",
		StartedAt:   time.Now(),
	})
	history, err := r.LoadHistory(ctx, "s2")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(history))
	}
}

func TestLoadHistory_ExcludesFailedChats(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s-lh", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	r.SaveChat(ctx, &repo.ChatRecord{
		ChatID:    "20260610143022020",
		SessionID: "s-lh",
		Status:    "completed",
		StartedAt: time.Now(),
	})
	r.SaveChat(ctx, &repo.ChatRecord{
		ChatID:    "20260610143022021",
		SessionID: "s-lh",
		Status:    "failed",
		StartedAt: time.Now(),
	})

	history, err := r.LoadHistory(ctx, "s-lh")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 entry (only success), got %d", len(history))
	}
}

func TestDeleteSession(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s3", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	r.SaveChat(ctx, &repo.ChatRecord{
		ChatID:    "20260610143022003",
		SessionID: "s3",
		Status:    "completed",
		StartedAt: time.Now(),
	})
	if err := r.DeleteSession(ctx, "s3"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	exists, _ := r.ExistsSession(ctx, "s3")
	if exists {
		t.Error("session should be deleted")
	}
}

func TestSaveChat_WithErrorAndSteps(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s-err", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	rec := &repo.ChatRecord{
		ChatID:    "20260610143022030",
		SessionID: "s-err",
		Status:    "failed",
		StartedAt: time.Now(),
		EndedAt:   time.Now().Add(time.Second),
		Error:     &repo.Error{Code: "ERR_001", Message: "something went wrong"},
		Steps: []repo.Step{
			{StepID: "step-1", Type: "tool", Name: "my_tool", Status: "success"},
		},
	}
	if err := r.SaveChat(ctx, rec); err != nil {
		t.Fatalf("SaveChat: %v", err)
	}
	got, err := r.GetChat(ctx, "20260610143022030")
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if got.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if got.Error.Code != "ERR_001" {
		t.Errorf("error code mismatch: %s", got.Error.Code)
	}
	if len(got.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(got.Steps))
	}
	if got.Steps[0].Name != "my_tool" {
		t.Errorf("step name mismatch: %s", got.Steps[0].Name)
	}
	if got.EndedAt.IsZero() {
		t.Error("EndedAt should not be zero")
	}
}

func TestSession_TenantRoundTrip(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	s := &repo.Session{SessionID: "s-t1", TenantID: "crm", UserID: "u1", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := r.CreateSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetSession(ctx, "s-t1")
	if got.TenantID != "crm" || got.UserID != "u1" {
		t.Errorf("got %+v", got)
	}
}

func TestListSessions_OwnerFilter(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	mk := func(id, tenant, user string) {
		_ = r.CreateSession(ctx, &repo.Session{SessionID: id, TenantID: tenant, UserID: user, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	}
	mk("a1", "crm", "u1")
	mk("a2", "crm", "u2")
	mk("b1", "erp", "u1")
	mk("sched", "", "")
	all, _ := r.ListSessions(ctx, nil, 100, 0)
	if len(all) != 4 {
		t.Errorf("nil owner: want 4 got %d", len(all))
	}
	crmU1, _ := r.ListSessions(ctx, &repo.SessionOwner{TenantID: "crm", UserID: "u1"}, 100, 0)
	if len(crmU1) != 1 || crmU1[0].SessionID != "a1" {
		t.Errorf("crm/u1: %+v", crmU1)
	}
	if len(crmU1) == 1 && (crmU1[0].TenantID != "crm" || crmU1[0].UserID != "u1") {
		t.Errorf("ListSessions must fill TenantID/UserID: %+v", crmU1[0])
	}
	empty, _ := r.ListSessions(ctx, &repo.SessionOwner{}, 100, 0)
	if len(empty) != 1 || empty[0].SessionID != "sched" {
		t.Errorf("empty owner must match only sched: %+v", empty)
	}
}

// --- SQL 分页 ---

func seedSessions(t *testing.T, r repo.MemoryRepo, owner repo.SessionOwner, n int) {
	t.Helper()
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		if err := r.CreateSession(context.Background(), &repo.Session{
			SessionID: fmt.Sprintf("%s-%s-%02d", owner.TenantID, owner.UserID, i),
			TenantID:  owner.TenantID, UserID: owner.UserID,
			CreatedAt: ts, UpdatedAt: ts,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestListSessions_Pagination(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	owner := repo.SessionOwner{TenantID: "crm", UserID: "u1"}
	seedSessions(t, r, owner, 5) // 00..04，04 最新

	page1, err := r.ListSessions(ctx, &owner, 2, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].SessionID != "crm-u1-04" || page1[1].SessionID != "crm-u1-03" {
		t.Errorf("page1 = %v", ids(page1))
	}
	page3, err := r.ListSessions(ctx, &owner, 2, 4)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 || page3[0].SessionID != "crm-u1-00" {
		t.Errorf("page3 = %v", ids(page3))
	}
	beyond, err := r.ListSessions(ctx, &owner, 2, 10)
	if err != nil {
		t.Fatalf("beyond: %v", err)
	}
	if len(beyond) != 0 {
		t.Errorf("beyond = %v", ids(beyond))
	}
}

func TestListSessions_NonPositiveLimitReturnsEmpty(t *testing.T) {
	r := newMemRepo(t)
	owner := repo.SessionOwner{TenantID: "crm", UserID: "u1"}
	seedSessions(t, r, owner, 2)
	for _, lim := range []int{0, -1} {
		got, err := r.ListSessions(context.Background(), &owner, lim, 0)
		if err != nil {
			t.Fatalf("limit=%d: %v", lim, err)
		}
		if len(got) != 0 {
			t.Errorf("limit=%d: expected empty, got %v", lim, ids(got))
		}
	}
}

func TestListSessions_NegativeOffsetTreatedAsZero(t *testing.T) {
	r := newMemRepo(t)
	owner := repo.SessionOwner{TenantID: "crm", UserID: "u1"}
	seedSessions(t, r, owner, 3)
	got, err := r.ListSessions(context.Background(), &owner, 10, -5)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3, got %v", ids(got))
	}
}

func TestCountSessions(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	seedSessions(t, r, repo.SessionOwner{TenantID: "crm", UserID: "u1"}, 3)
	seedSessions(t, r, repo.SessionOwner{TenantID: "erp", UserID: "u1"}, 2)

	all, err := r.CountSessions(ctx, nil)
	if err != nil {
		t.Fatalf("CountSessions(nil): %v", err)
	}
	if all != 5 {
		t.Errorf("all = %d, want 5", all)
	}
	crm, err := r.CountSessions(ctx, &repo.SessionOwner{TenantID: "crm", UserID: "u1"})
	if err != nil {
		t.Fatalf("CountSessions(crm): %v", err)
	}
	if crm != 3 {
		t.Errorf("crm = %d, want 3", crm)
	}
	none, err := r.CountSessions(ctx, &repo.SessionOwner{TenantID: "none", UserID: "x"})
	if err != nil {
		t.Fatalf("CountSessions(none): %v", err)
	}
	if none != 0 {
		t.Errorf("none = %d, want 0", none)
	}
}

func ids(ss []*repo.Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.SessionID
	}
	return out
}
