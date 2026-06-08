package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/storage"
)

// makeFile 在 dir 下创建文件,内容为 content,返回绝对路径。
func makeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestComputeDiff_Added(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "config.yaml", "agent:\n  name: groot\n")

	store := storage.NewLocal()
	result, err := ComputeDiff(store, localDir, remoteDir, []string{"config.yaml"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "config.yaml" {
		t.Errorf("expected Added=[config.yaml], got %+v", result)
	}
	if len(result.Modified) != 0 || len(result.Removed) != 0 {
		t.Errorf("unexpected changes: %+v", result)
	}
}

func TestComputeDiff_Removed(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	// 只在远端有文件
	makeFile(t, remoteDir, "GROOT.md", "# GROOT\n")

	store := storage.NewLocal()
	result, err := ComputeDiff(store, localDir, remoteDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "GROOT.md" {
		t.Errorf("expected Removed=[GROOT.md], got %+v", result)
	}
}

func TestComputeDiff_Same(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	content := "agent:\n  name: groot\n"
	makeFile(t, localDir, "config.yaml", content)
	makeFile(t, remoteDir, "config.yaml", content)

	// 同步两侧 mtime
	now := time.Now().Truncate(time.Second)
	for _, dir := range []string{localDir, remoteDir} {
		path := filepath.Join(dir, "config.yaml")
		_ = os.Chtimes(path, now, now)
	}

	store := storage.NewLocal()
	result, err := ComputeDiff(store, localDir, remoteDir, []string{"config.yaml"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Same) != 1 {
		t.Errorf("expected Same=[config.yaml], got %+v", result)
	}
}

func TestComputeDiff_Modified_SizeDiff(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "GROOT.md", "# v2\n")
	makeFile(t, remoteDir, "GROOT.md", "# v1 (longer content)\n")

	store := storage.NewLocal()
	result, err := ComputeDiff(store, localDir, remoteDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Modified) != 1 {
		t.Errorf("expected Modified=[GROOT.md], got %+v", result)
	}
}

func TestComputeDiff_Modified_MtimeDiff(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	content := "same content\n"
	makeFile(t, localDir, "GROOT.md", content)
	makeFile(t, remoteDir, "GROOT.md", content)

	// 本地文件 mtime 比远端早 5s (超过 1s 容差)
	older := time.Now().Add(-5 * time.Second).Truncate(time.Second)
	newer := time.Now().Truncate(time.Second)
	_ = os.Chtimes(filepath.Join(localDir, "GROOT.md"), older, older)
	_ = os.Chtimes(filepath.Join(remoteDir, "GROOT.md"), newer, newer)

	store := storage.NewLocal()
	result, err := ComputeDiff(store, localDir, remoteDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Modified) != 1 {
		t.Errorf("expected Modified=[GROOT.md], got %+v", result)
	}
}

func TestComputeDiff_MtimeTolerance(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	content := "same content\n"
	makeFile(t, localDir, "GROOT.md", content)
	makeFile(t, remoteDir, "GROOT.md", content)

	// 本地与远端 mtime 差 < 1s → 判为 Same
	base := time.Now().Truncate(time.Second)
	_ = os.Chtimes(filepath.Join(localDir, "GROOT.md"), base, base)
	_ = os.Chtimes(filepath.Join(remoteDir, "GROOT.md"), base, base)

	store := storage.NewLocal()
	result, err := ComputeDiff(store, localDir, remoteDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Same) != 1 {
		t.Errorf("expected Same (within tolerance), got %+v", result)
	}
}

func TestComputeDiff_RecursiveDir(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "skills/weather/SKILL.md", "weather skill\n")
	makeFile(t, localDir, "skills/weather/handler.go", "package main\n")

	store := storage.NewLocal()
	result, err := ComputeDiff(store, localDir, remoteDir, []string{"skills/weather"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Added) != 2 {
		t.Errorf("expected 2 Added files, got %d: %v", len(result.Added), result.Added)
	}
	for _, f := range result.Added {
		if !strings.HasPrefix(f, "skills/weather/") {
			t.Errorf("unexpected path: %s", f)
		}
	}
}
