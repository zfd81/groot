package modeldb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newTestRepo(t *testing.T) repo.ModelRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func newModel(name string) *repo.Model {
	now := time.Now()
	return &repo.Model{
		Name:                name,
		BaseURL:             "https://api.openai.com/v1",
		APIKey:              "sk-test-1234abcd",
		Model:               "gpt-4o",
		MaxCompletionTokens: 4096,
		Temperature:         0.7,
		TopP:                1.0,
		Stop:                []string{},
		Enabled:             true,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func TestModelRepo_CreateAndGet(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	m := newModel("gpt-4o")
	m.Stop = []string{"\n\n", "END"}
	m.Thinking = true
	if err := r.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := r.GetByName(ctx, "gpt-4o")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.Name != "gpt-4o" || got.BaseURL != m.BaseURL || got.APIKey != m.APIKey {
		t.Errorf("GetByName mismatch: %+v", got)
	}
	if len(got.Stop) != 2 || got.Stop[0] != "\n\n" {
		t.Errorf("Stop 反序列化错误: %v", got.Stop)
	}
	if !got.Enabled || got.IsDefault {
		t.Errorf("bool 字段错误: enabled=%v is_default=%v", got.Enabled, got.IsDefault)
	}
	if !got.Thinking {
		t.Errorf("Thinking 应读回 true, got %v", got.Thinking)
	}
}

func TestModelRepo_GetByName_NotFound(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.GetByName(context.Background(), "nope"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestModelRepo_UniqueName(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.Create(ctx, newModel("dup")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Create(ctx, newModel("dup")); err == nil {
		t.Error("重名创建应当失败")
	}
}

func TestModelRepo_ListOrdered(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.Create(ctx, newModel("b-model")); err != nil {
		t.Fatalf("Create b-model: %v", err)
	}
	if err := r.Create(ctx, newModel("a-model")); err != nil {
		t.Fatalf("Create a-model: %v", err)
	}
	list, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Name != "a-model" {
		t.Errorf("List 应按 name 升序: %v", list)
	}
}

func TestModelRepo_UpdateAndRename(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.Create(ctx, newModel("old-name")); err != nil {
		t.Fatalf("Create old-name: %v", err)
	}

	m := newModel("new-name")
	m.Temperature = 1.5
	if err := r.Update(ctx, "old-name", m); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := r.GetByName(ctx, "old-name"); !errors.Is(err, repo.ErrNotFound) {
		t.Error("旧名称应当查不到")
	}
	got, err := r.GetByName(ctx, "new-name")
	if err != nil || got.Temperature != 1.5 {
		t.Errorf("重命名后查询失败: %v, %+v", err, got)
	}

	if err := r.Update(ctx, "ghost", newModel("x")); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("更新不存在的模型应返回 ErrNotFound, got %v", err)
	}
}

func TestModelRepo_Delete(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.Create(ctx, newModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	if err := r.Delete(ctx, "m1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.GetByName(ctx, "m1"); !errors.Is(err, repo.ErrNotFound) {
		t.Error("删除后应查不到")
	}
	if err := r.Delete(ctx, "m1"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("删除不存在的模型应返回 ErrNotFound, got %v", err)
	}
}

func TestModelRepo_SetDefault(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.Create(ctx, newModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	if err := r.Create(ctx, newModel("m2")); err != nil {
		t.Fatalf("Create m2: %v", err)
	}

	if _, err := r.GetDefault(ctx); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("无默认时 GetDefault 应返回 ErrNotFound, got %v", err)
	}

	if err := r.SetDefault(ctx, "m1"); err != nil {
		t.Fatalf("SetDefault m1: %v", err)
	}
	d, err := r.GetDefault(ctx)
	if err != nil || d.Name != "m1" {
		t.Fatalf("GetDefault: %v, %+v", err, d)
	}

	// 切换默认：全表仍只有一个 is_default
	if err := r.SetDefault(ctx, "m2"); err != nil {
		t.Fatalf("SetDefault m2: %v", err)
	}
	list, _ := r.List(ctx)
	count := 0
	for _, m := range list {
		if m.IsDefault {
			count++
			if m.Name != "m2" {
				t.Errorf("默认模型应为 m2, got %s", m.Name)
			}
		}
	}
	if count != 1 {
		t.Errorf("默认模型应有且只有 1 个, got %d", count)
	}

	if err := r.SetDefault(ctx, "ghost"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("SetDefault 不存在模型应返回 ErrNotFound, got %v", err)
	}
}

func TestModelRepo_Count(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	n, _ := r.Count(ctx)
	if n != 0 {
		t.Errorf("初始 Count 应为 0, got %d", n)
	}
	if err := r.Create(ctx, newModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	n, _ = r.Count(ctx)
	if n != 1 {
		t.Errorf("Count 应为 1, got %d", n)
	}
}
