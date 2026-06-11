package resourcelocal

import (
	"context"
	"errors"
	"testing"

	"github.com/zfd81/groot/internal/repo"
)

func newLocalRepo(t *testing.T) repo.ResourceRepo {
	t.Helper()
	return New(t.TempDir())
}

func TestPutAndGet(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	content := []byte("hello world")
	res := &repo.Resource{
		Path: "skills/test/SKILL.md", Content: content,
		Size: int64(len(content)), ContentHash: sha1Hex(content),
	}
	if err := r.Put(ctx, res); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := r.Get(ctx, "skills/test/SKILL.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Content) != "hello world" {
		t.Errorf("unexpected content: %s", got.Content)
	}
}

func TestGet_NotFound(t *testing.T) {
	r := newLocalRepo(t)
	_, err := r.Get(context.Background(), "nonexistent.md")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestList(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	for _, p := range []string{"skills/a/SKILL.md", "skills/b/SKILL.md"} {
		c := []byte("x")
		r.Put(ctx, &repo.Resource{Path: p, Content: c, Size: int64(len(c)), ContentHash: sha1Hex(c)})
	}
	entries, err := r.List(ctx, "skills/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2, got %d", len(entries))
	}
}

func TestDelete(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	c := []byte("data")
	r.Put(ctx, &repo.Resource{Path: "file.md", Content: c, Size: int64(len(c)), ContentHash: sha1Hex(c)})
	if err := r.Delete(ctx, "file.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// delete nonexistent should not error
	if err := r.Delete(ctx, "file.md"); err != nil {
		t.Errorf("delete nonexistent should succeed, got %v", err)
	}
}
