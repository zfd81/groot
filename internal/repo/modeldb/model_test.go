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
	if !got.Enabled || got.DefaultFlags != 0 {
		t.Errorf("字段错误: enabled=%v default_flags=%v", got.Enabled, got.DefaultFlags)
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

// flagsOf 读回指定模型的默认类型位掩码
func flagsOf(t *testing.T, r repo.ModelRepo, name string) repo.DefaultFlag {
	t.Helper()
	m, err := r.GetByName(context.Background(), name)
	if err != nil {
		t.Fatalf("GetByName %s: %v", name, err)
	}
	return m.DefaultFlags
}

func TestModelRepo_SetDefault(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for _, n := range []string{"m1", "m2"} {
		if err := r.Create(ctx, newModel(n)); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}

	for _, f := range repo.AllDefaultFlags {
		if _, err := r.GetDefault(ctx, f); !errors.Is(err, repo.ErrNotFound) {
			t.Errorf("无 %s 默认时 GetDefault 应返回 ErrNotFound, got %v", f, err)
		}
	}

	if err := r.SetDefault(ctx, "m1", repo.DefaultChat); err != nil {
		t.Fatalf("SetDefault m1 chat: %v", err)
	}
	if d, err := r.GetDefault(ctx, repo.DefaultChat); err != nil || d.Name != "m1" {
		t.Fatalf("GetDefault chat: %v, %+v", err, d)
	}

	// 转移对话默认：全表仍只有一个模型持有 chat
	if err := r.SetDefault(ctx, "m2", repo.DefaultChat); err != nil {
		t.Fatalf("SetDefault m2 chat: %v", err)
	}
	list, _ := r.List(ctx)
	count := 0
	for _, m := range list {
		if m.Has(repo.DefaultChat) {
			count++
			if m.Name != "m2" {
				t.Errorf("默认对话模型应为 m2, got %s", m.Name)
			}
		}
	}
	if count != 1 {
		t.Errorf("默认对话模型应有且只有 1 个, got %d", count)
	}

	// 重复设置同一类型结果不变，不会溢出到其他位
	for i := 0; i < 2; i++ {
		if err := r.SetDefault(ctx, "m2", repo.DefaultChat); err != nil {
			t.Fatalf("重复 SetDefault m2 chat: %v", err)
		}
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultChat {
		t.Errorf("重复设置后 m2 flags = %d, want %d", got, repo.DefaultChat)
	}

	// 一模型多默认：m2 同时持有 chat 与 vision
	if err := r.SetDefault(ctx, "m2", repo.DefaultVision); err != nil {
		t.Fatalf("SetDefault m2 vision: %v", err)
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultChat|repo.DefaultVision {
		t.Errorf("m2 default_flags = %d, want 5", got)
	}

	// 类型互不影响：m1 设为 voice 不动 m2 的 chat、vision
	if err := r.SetDefault(ctx, "m1", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault m1 voice: %v", err)
	}
	if got := flagsOf(t, r, "m1"); got != repo.DefaultVoice {
		t.Errorf("m1 default_flags = %d, want 2", got)
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultChat|repo.DefaultVision {
		t.Errorf("m2 default_flags = %d, want 5", got)
	}
	if d, err := r.GetDefault(ctx, repo.DefaultVoice); err != nil || d.Name != "m1" {
		t.Errorf("GetDefault voice: %v, %+v", err, d)
	}

	if err := r.SetDefault(ctx, "ghost", repo.DefaultChat); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("SetDefault 不存在模型应返回 ErrNotFound, got %v", err)
	}
	// 目标不存在时事务回滚，原持有者不受影响
	if d, err := r.GetDefault(ctx, repo.DefaultChat); err != nil || d.Name != "m2" {
		t.Errorf("SetDefault 失败后对话默认应仍为 m2: %v, %+v", err, d)
	}
}

func TestModelRepo_ClearDefault(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for _, n := range []string{"m1", "m2"} {
		if err := r.Create(ctx, newModel(n)); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	_ = r.SetDefault(ctx, "m1", repo.DefaultChat)
	_ = r.SetDefault(ctx, "m1", repo.DefaultVision)
	_ = r.SetDefault(ctx, "m2", repo.DefaultVoice)

	// 只清目标位
	if err := r.ClearDefault(ctx, "m1", repo.DefaultVision); err != nil {
		t.Fatalf("ClearDefault m1 vision: %v", err)
	}
	if got := flagsOf(t, r, "m1"); got != repo.DefaultChat {
		t.Errorf("m1 default_flags = %d, want 1", got)
	}
	if _, err := r.GetDefault(ctx, repo.DefaultVision); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("清除后 vision 应无默认, got %v", err)
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultVoice {
		t.Errorf("m2 不应受影响, default_flags = %d", got)
	}

	// 清除未持有的位：无操作、不报错
	if err := r.ClearDefault(ctx, "m1", repo.DefaultVoice); err != nil {
		t.Errorf("清除未持有的位应成功, got %v", err)
	}
	if got := flagsOf(t, r, "m1"); got != repo.DefaultChat {
		t.Errorf("m1 default_flags = %d, want 1", got)
	}
	if got := flagsOf(t, r, "m2"); got != repo.DefaultVoice {
		t.Errorf("清除 m1 未持有的 voice 不应影响 m2, got %d", got)
	}

	if err := r.ClearDefault(ctx, "ghost", repo.DefaultVoice); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("ClearDefault 不存在模型应返回 ErrNotFound, got %v", err)
	}
}

// TestModelRepo_CreateAndUpdateDefaultFlags Create 原样写入 default_flags；Update 不改动它。
func TestModelRepo_CreateAndUpdateDefaultFlags(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	m := newModel("m1")
	m.DefaultFlags = repo.DefaultChat | repo.DefaultVoice
	if err := r.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := flagsOf(t, r, "m1"); got != 3 {
		t.Errorf("Create 后 default_flags = %d, want 3", got)
	}

	upd := newModel("m1")
	upd.DefaultFlags = 0
	upd.Model = "gpt-4o-mini"
	if err := r.Update(ctx, "m1", upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := r.GetByName(ctx, "m1")
	if got.Model != "gpt-4o-mini" || got.DefaultFlags != 3 {
		t.Errorf("Update 应改 model 且不改 default_flags: %+v", got)
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
