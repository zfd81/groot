package settingdb

import (
	"context"
	"errors"
	"testing"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.SettingRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func TestSettingRepo_UpsertAndGet(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	err := r.Upsert(ctx, &repo.Setting{
		Scope: repo.ScopeGlobal, Name: "voice.model", Value: "whisper-1",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := r.Get(ctx, repo.ScopeGlobal, "", "voice.model")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != "whisper-1" {
		t.Errorf("Value = %q, want %q", got.Value, "whisper-1")
	}
	if got.UpdatedAt == 0 {
		t.Error("UpdatedAt 未写入")
	}
}

func TestSettingRepo_UpsertOverwrites(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	must(r.Upsert(ctx, &repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.model", Value: "a"}))
	must(r.Upsert(ctx, &repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.model", Value: "b"}))

	got, err := r.Get(ctx, repo.ScopeGlobal, "", "voice.model")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != "b" {
		t.Errorf("Value = %q, want %q（主键冲突应覆盖而非报错）", got.Value, "b")
	}
}

func TestSettingRepo_GetNotFound(t *testing.T) {
	r := newTestRepo(t)
	_, err := r.Get(context.Background(), repo.ScopeGlobal, "", "voice.model")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSettingRepo_UpsertRejectsInvalid(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	cases := []struct {
		name string
		item *repo.Setting
	}{
		{"name 为空", &repo.Setting{Scope: repo.ScopeGlobal, Name: "", Value: "x"}},
		{"未知作用域", &repo.Setting{Scope: repo.Scope("team"), ScopeID: "t1", Name: "voice.model", Value: "x"}},
		{"global 带 scope_id", &repo.Setting{Scope: repo.ScopeGlobal, ScopeID: "u1", Name: "voice.model", Value: "x"}},
		{"user 缺 scope_id", &repo.Setting{Scope: repo.ScopeUser, ScopeID: "", Name: "voice.model", Value: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := r.Upsert(ctx, tc.item); err == nil {
				t.Errorf("%+v 应被拒绝，得到 nil", tc.item)
			}
		})
	}
}

func TestSettingRepo_UpsertAtomic(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	valid := &repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.model", Value: "w"}
	invalid := &repo.Setting{Scope: repo.ScopeGlobal, Name: "", Value: "x"}
	if err := r.Upsert(ctx, valid, invalid); err == nil {
		t.Fatal("包含非法项的 Upsert 应返回错误")
	}
	if _, err := r.Get(ctx, repo.ScopeGlobal, "", valid.Name); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("整体回滚后 Get err = %v, want ErrNotFound（不应写入任何一条）", err)
	}
}

func TestSettingRepo_ListByScope(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	err := r.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.model", Value: "w"},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: "voice.enabled", Value: "true"},
		&repo.Setting{Scope: repo.ScopeUser, ScopeID: "u1", Name: "voice.model", Value: "x"},
	)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	items, err := r.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2（不应含 user 作用域的行）", len(items))
	}
	// 按 name 升序：voice.enabled 在 voice.model 之前
	if items[0].Name != "voice.enabled" {
		t.Errorf("items[0].Name = %q, want voice.enabled", items[0].Name)
	}
}

func TestSettingRepo_ListByScopeEmpty(t *testing.T) {
	r := newTestRepo(t)
	items, err := r.ListByScope(context.Background(), repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len = %d, want 0", len(items))
	}
}

func TestSettingRepo_Delete(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if err := r.Upsert(ctx, &repo.Setting{
		Scope: repo.ScopeGlobal, Name: "voice.model", Value: "w",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := r.Delete(ctx, repo.ScopeGlobal, "", "voice.model"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Get(ctx, repo.ScopeGlobal, "", "voice.model"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("删除后 Get err = %v, want ErrNotFound", err)
	}
	// 删除不存在的键不报错
	if err := r.Delete(ctx, repo.ScopeGlobal, "", "nope"); err != nil {
		t.Errorf("删除不存在的键应返回 nil，得到 %v", err)
	}
}
