package resourcedb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
)

func newRepo(t *testing.T) repo.ResourceRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(sqlxDB, dialect)
}

func TestPutAndGet(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	content := []byte("# Weather Skill")
	res := &repo.Resource{
		Path: "skills/weather/SKILL.md", Content: content,
		ContentType: "text/markdown", Size: int64(len(content)),
		ContentHash: SHA1Hex(content), UpdatedAt: time.Now(),
	}
	if err := r.Put(ctx, res); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := r.Get(ctx, "skills/weather/SKILL.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Content) != "# Weather Skill" {
		t.Errorf("unexpected content: %s", got.Content)
	}
}

func TestStat_NotFound(t *testing.T) {
	r := newRepo(t)
	_, err := r.Stat(context.Background(), "nonexistent")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestList_Prefix(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	for _, p := range []string{"skills/a/SKILL.md", "skills/b/SKILL.md", "mcp/server.json"} {
		c := []byte("content")
		r.Put(ctx, &repo.Resource{Path: p, Content: c, Size: int64(len(c)), ContentHash: SHA1Hex(c), UpdatedAt: time.Now()})
	}
	entries, err := r.List(ctx, "skills/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries under skills/, got %d", len(entries))
	}
}

func TestPut_Idempotent(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	c := []byte("v1")
	r.Put(ctx, &repo.Resource{Path: "a.md", Content: c, Size: int64(len(c)), ContentHash: SHA1Hex(c), UpdatedAt: time.Now()})
	c2 := []byte("v2")
	if err := r.Put(ctx, &repo.Resource{Path: "a.md", Content: c2, Size: int64(len(c2)), ContentHash: SHA1Hex(c2), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	got, _ := r.Get(ctx, "a.md")
	if string(got.Content) != "v2" {
		t.Errorf("expected v2, got %s", got.Content)
	}
}

func put(t *testing.T, r repo.ResourceRepo, path, content string) {
	t.Helper()
	err := r.Put(context.Background(), &repo.Resource{
		Path:        path,
		Content:     []byte(content),
		Size:        int64(len(content)),
		ContentHash: SHA1Hex([]byte(content)),
		UpdatedAt:   time.UnixMilli(1757734800000),
	})
	if err != nil {
		t.Fatalf("put %s: %v", path, err)
	}
}

func TestDelete_SoftDeletesRecord(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	put(t, r, "skills/weather/SKILL.md", "hello")

	if err := r.Delete(ctx, "skills/weather/SKILL.md"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := r.Get(ctx, "skills/weather/SKILL.md"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
	if _, err := r.Stat(ctx, "skills/weather/SKILL.md"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("Stat after delete: err = %v, want ErrNotFound", err)
	}
	active, err := r.List(ctx, "skills/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("List after delete returned %d entries, want 0", len(active))
	}

	all, err := r.ListWithDeleted(ctx, "skills/")
	if err != nil {
		t.Fatalf("list with deleted: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListWithDeleted returned %d entries, want 1", len(all))
	}
	if all[0].Status != repo.ResourceStatusDeleted {
		t.Fatalf("status = %q, want %q", all[0].Status, repo.ResourceStatusDeleted)
	}
	if all[0].Size != 0 || all[0].ContentHash != "" {
		t.Fatalf("tombstone should clear content: size=%d hash=%q", all[0].Size, all[0].ContentHash)
	}
}

// Put 复活一个已删除路径：状态必须回到 active。
// 这是 UpsertSuffix 未包含 status 列时会静默出错的场景。
func TestPut_RevivesDeletedRecord(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	put(t, r, "config.yaml", "old")
	if err := r.Delete(ctx, "config.yaml"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	put(t, r, "config.yaml", "new")

	res, err := r.Get(ctx, "config.yaml")
	if err != nil {
		t.Fatalf("Get after revive: %v", err)
	}
	if string(res.Content) != "new" {
		t.Fatalf("content = %q, want \"new\"", res.Content)
	}
	if res.Status != repo.ResourceStatusActive {
		t.Fatalf("status = %q, want %q", res.Status, repo.ResourceStatusActive)
	}
	// List 的 active 过滤与 Get 的是两处独立 SQL，复活后必须都能看到这条记录。
	active, err := r.List(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 1 || active[0].Path != "config.yaml" {
		t.Fatalf("List after revive = %+v, want 1 entry config.yaml", active)
	}
}

// TestDelete_IsIdempotent 重复删除同一路径不报错，且墓碑的 updated_at 保持
// 首次删除的时刻——同步比较依赖这个时间戳判断本地文件是否该删，被刷新会误删
// 用户重新创建的文件。这条断言是 Delete 的 status='active' 条件的唯一防线。
func TestDelete_IsIdempotent(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	put(t, r, "skills/weather/SKILL.md", "hello")
	if err := r.Delete(ctx, "skills/weather/SKILL.md"); err != nil {
		t.Fatalf("first delete: %v", err)
	}

	tombstone := func() *repo.ResourceEntry {
		t.Helper()
		all, err := r.ListWithDeleted(ctx, "skills/")
		if err != nil {
			t.Fatalf("list with deleted: %v", err)
		}
		if len(all) != 1 {
			t.Fatalf("ListWithDeleted returned %d entries, want 1", len(all))
		}
		return all[0]
	}

	first := tombstone()
	if first.Status != repo.ResourceStatusDeleted {
		t.Fatalf("status after first delete = %q, want %q", first.Status, repo.ResourceStatusDeleted)
	}

	if err := r.Delete(ctx, "skills/weather/SKILL.md"); err != nil {
		t.Fatalf("second delete: %v", err)
	}

	second := tombstone()
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("second delete moved tombstone time: got %v, want %v (unchanged)",
			second.UpdatedAt, first.UpdatedAt)
	}
	if second.Status != repo.ResourceStatusDeleted {
		t.Fatalf("status after second delete = %q, want %q", second.Status, repo.ResourceStatusDeleted)
	}
}

func TestDelete_MissingPathIsNoError(t *testing.T) {
	r := newRepo(t)
	if err := r.Delete(context.Background(), "mcp/nonexistent/config.json"); err != nil {
		t.Fatalf("delete missing path: %v", err)
	}
}

func TestListWithDeleted_EmptyPrefixReturnsAll(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	put(t, r, "config.yaml", "a")
	put(t, r, "GROOT.md", "b")
	if err := r.Delete(ctx, "GROOT.md"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	active, err := r.List(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("List(\"\") returned %d, want 1", len(active))
	}
	all, err := r.ListWithDeleted(ctx, "")
	if err != nil {
		t.Fatalf("list with deleted: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListWithDeleted(\"\") returned %d, want 2", len(all))
	}
}
