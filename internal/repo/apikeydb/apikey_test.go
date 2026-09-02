package apikeydb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepoDB(t *testing.T) (repo.APIKeyRepo, *sqlx.DB) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect), sqlxDB
}

func newTestRepo(t *testing.T) repo.APIKeyRepo {
	t.Helper()
	r, _ := newTestRepoDB(t)
	return r
}

func newKey(id, name string) *repo.APIKey {
	// 固定时间，毫秒位非零，保证毫秒往返断言真正生效
	created := time.UnixMilli(1756815045123)
	return &repo.APIKey{
		ID:          id,
		Name:        name,
		Permissions: []string{"chat", "status"},
		ExpiresAt:   created.AddDate(0, 0, 7),
		CreatedAt:   created,
	}
}

func TestAPIKeyRepo_CreateAndGet(t *testing.T) {
	r := newTestRepo(t)
	k := newKey("20260902120000", "svc-a")
	if err := r.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := r.GetByID(context.Background(), "20260902120000")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "svc-a" || len(got.Permissions) != 2 || got.Permissions[0] != "chat" {
		t.Errorf("unexpected row: %+v", got)
	}
	// 毫秒级时间戳往返无损
	if !got.CreatedAt.Equal(k.CreatedAt) || !got.ExpiresAt.Equal(k.ExpiresAt) {
		t.Errorf("timestamps not round-tripped: got %v/%v want %v/%v",
			got.CreatedAt, got.ExpiresAt, k.CreatedAt, k.ExpiresAt)
	}
	byName, err := r.GetByName(context.Background(), "svc-a")
	if err != nil || byName.ID != "20260902120000" {
		t.Errorf("GetByName: %v, %+v", err, byName)
	}
}

func TestAPIKeyRepo_GetNotFound(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.GetByID(context.Background(), "20000101000000"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("GetByID missing should be ErrNotFound, got %v", err)
	}
	if _, err := r.GetByName(context.Background(), "nope"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("GetByName missing should be ErrNotFound, got %v", err)
	}
}

func TestAPIKeyRepo_DuplicateIDAndName(t *testing.T) {
	r := newTestRepo(t)
	if err := r.Create(context.Background(), newKey("20260902120000", "svc-a")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 主键冲突
	if err := r.Create(context.Background(), newKey("20260902120000", "svc-b")); err == nil {
		t.Error("duplicate id should fail")
	}
	// 名称唯一冲突
	if err := r.Create(context.Background(), newKey("20260902120001", "svc-a")); err == nil {
		t.Error("duplicate name should fail")
	}
}

func TestAPIKeyRepo_ListOrder(t *testing.T) {
	r := newTestRepo(t)
	old := newKey("20260901000000", "old")
	old.CreatedAt = old.CreatedAt.Add(-time.Hour)
	if err := r.Create(context.Background(), old); err != nil {
		t.Fatalf("Create old: %v", err)
	}
	if err := r.Create(context.Background(), newKey("20260902120000", "new")); err != nil {
		t.Fatalf("Create new: %v", err)
	}
	list, err := r.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %v, len=%d", err, len(list))
	}
	if list[0].Name != "new" {
		t.Errorf("List should be created_at DESC, got first=%s", list[0].Name)
	}
}

func TestAPIKeyRepo_Delete(t *testing.T) {
	r := newTestRepo(t)
	if err := r.Create(context.Background(), newKey("20260902120000", "svc-a")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.DeleteByID(context.Background(), "20260902120000"); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	if _, err := r.GetByID(context.Background(), "20260902120000"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("deleted key should be ErrNotFound, got %v", err)
	}
	if err := r.DeleteByID(context.Background(), "20260902120000"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("delete missing should be ErrNotFound, got %v", err)
	}
}

func TestAPIKeyRepo_NilPermissions(t *testing.T) {
	r := newTestRepo(t)
	k := newKey("20260902120000", "svc-a")
	k.Permissions = nil // permsJSON 应写入 '[]'
	if err := r.Create(context.Background(), k); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := r.GetByID(context.Background(), "20260902120000")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Permissions == nil || len(got.Permissions) != 0 {
		t.Errorf("nil permissions should round-trip as empty slice, got %+v", got.Permissions)
	}
}

func TestAPIKeyRepo_CorruptPermissions(t *testing.T) {
	r, sqlxDB := newTestRepoDB(t)
	// 绕过 repo 直接注入损坏的 permissions JSON
	_, err := sqlxDB.Exec(`INSERT INTO api_keys (id, name, permissions, expires_at, created_at)
		VALUES ('20260902120000', 'bad', '{not json', 0, 0)`)
	if err != nil {
		t.Fatalf("inject corrupt row: %v", err)
	}
	got, err := r.GetByID(context.Background(), "20260902120000")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Permissions == nil || len(got.Permissions) != 0 {
		t.Errorf("corrupt permissions should degrade to empty slice, got %+v", got.Permissions)
	}
}
