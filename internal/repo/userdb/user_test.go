package userdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.UserRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func newUser(id, username string) *repo.User {
	now := time.Now()
	return &repo.User{
		ID:           id,
		Username:     username,
		PasswordHash: "$2a$10$fakehashfakehashfakehashfakehashfakehashfakehashfake",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func TestUserRepo_CreateAndGet(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	u := newUser("20260901120000", "admin")
	if err := r.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := r.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if got.ID != u.ID || got.Username != u.Username || got.PasswordHash != u.PasswordHash {
		t.Errorf("GetByUsername mismatch: got %+v", got)
	}
	if got.LastLoginAt != nil {
		t.Errorf("LastLoginAt should be nil for new user, got %v", got.LastLoginAt)
	}

	byID, err := r.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.Username != "admin" {
		t.Errorf("GetByID username = %s, want admin", byID.Username)
	}
}

func TestUserRepo_DuplicateUsername(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if err := r.Create(ctx, newUser("20260901120000", "admin")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := r.Create(ctx, newUser("20260901120001", "admin")); err == nil {
		t.Errorf("duplicate username should fail")
	}
}

func TestUserRepo_NotFound(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if _, err := r.GetByUsername(ctx, "nobody"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("GetByUsername: want ErrNotFound, got %v", err)
	}
	if _, err := r.GetByID(ctx, "nope"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("GetByID: want ErrNotFound, got %v", err)
	}
	if err := r.UpdatePassword(ctx, "nope", "hash"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("UpdatePassword: want ErrNotFound, got %v", err)
	}
	if err := r.UpdateLastLogin(ctx, "nope", time.Now()); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("UpdateLastLogin: want ErrNotFound, got %v", err)
	}
}

func TestUserRepo_Count(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	n, err := r.Count(ctx)
	if err != nil || n != 0 {
		t.Fatalf("Count on empty table = %d, %v; want 0, nil", n, err)
	}
	if err := r.Create(ctx, newUser("20260901120000", "admin")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	n, err = r.Count(ctx)
	if err != nil || n != 1 {
		t.Errorf("Count = %d, %v; want 1, nil", n, err)
	}
}

func TestUserRepo_UpdatePassword(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	u := newUser("20260901120000", "admin")
	u.CreatedAt = time.Now().Add(-time.Hour)
	u.UpdatedAt = u.CreatedAt
	if err := r.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := r.UpdatePassword(ctx, u.ID, "newhash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	got, err := r.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PasswordHash != "newhash" {
		t.Errorf("PasswordHash = %s, want newhash", got.PasswordHash)
	}
	if !got.UpdatedAt.After(u.UpdatedAt) {
		t.Errorf("UpdatedAt should be refreshed: %v <= %v", got.UpdatedAt, u.UpdatedAt)
	}
}

func TestUserRepo_UpdateLastLogin(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	u := newUser("20260901120000", "admin")
	if err := r.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	at := time.Now()
	if err := r.UpdateLastLogin(ctx, u.ID, at); err != nil {
		t.Fatalf("UpdateLastLogin: %v", err)
	}
	got, err := r.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.LastLoginAt == nil {
		t.Fatalf("LastLoginAt should not be nil")
	}
	if got.LastLoginAt.UnixMilli() != at.UnixMilli() {
		t.Errorf("LastLoginAt = %v, want %v", got.LastLoginAt, at)
	}
}

func TestUserRepo_DeleteAll(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if err := r.Create(ctx, newUser("20260901120000", "admin")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Create(ctx, newUser("20260901120001", "alice")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	n, err := r.DeleteAll(ctx)
	if err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteAll returned %d, want 2", n)
	}
	cnt, _ := r.Count(ctx)
	if cnt != 0 {
		t.Errorf("Count after DeleteAll = %d, want 0", cnt)
	}
}
