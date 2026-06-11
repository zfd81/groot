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
