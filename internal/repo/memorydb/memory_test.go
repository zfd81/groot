package memorydb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/memory"
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
	sessions, err := r.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestSaveChatIncreasesRound(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s1", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	rec := &memory.ChatRecord{
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

func TestSaveChat_SessionNotFound(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	rec := &memory.ChatRecord{
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
	rec := &memory.ChatRecord{
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
	r.SaveChat(ctx, &memory.ChatRecord{
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

	r.SaveChat(ctx, &memory.ChatRecord{
		ChatID:    "20260610143022020",
		SessionID: "s-lh",
		Status:    "completed",
		StartedAt: time.Now(),
	})
	r.SaveChat(ctx, &memory.ChatRecord{
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
	r.SaveChat(ctx, &memory.ChatRecord{
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

func TestDeleteExpiredSessions(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()

	old := &repo.Session{
		SessionID: "old-sess",
		CreatedAt: time.Now().Add(-48 * time.Hour),
		UpdatedAt: time.Now().Add(-48 * time.Hour),
	}
	r.CreateSession(ctx, old)

	// recent session should not be deleted
	r.CreateSession(ctx, &repo.Session{
		SessionID: "new-sess",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	n, err := r.DeleteExpiredSessions(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}

	exists, _ := r.ExistsSession(ctx, "new-sess")
	if !exists {
		t.Error("new session should still exist")
	}
}

func TestSaveChat_WithErrorAndSteps(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	r.CreateSession(ctx, &repo.Session{SessionID: "s-err", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	rec := &memory.ChatRecord{
		ChatID:    "20260610143022030",
		SessionID: "s-err",
		Status:    "failed",
		StartedAt: time.Now(),
		EndedAt:   time.Now().Add(time.Second),
		Error:     &memory.Error{Code: "ERR_001", Message: "something went wrong"},
		Steps: []memory.Step{
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
