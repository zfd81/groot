package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/repo/resourcelocal"
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

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"config.yaml"})
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

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"GROOT.md"})
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

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"config.yaml"})
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

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Modified) != 1 {
		t.Errorf("expected Modified=[GROOT.md], got %+v", result)
	}
}

// TestComputeDiff_Modified_SameSizeDiffContent 验证 size 相同但内容不同时
// (hash 不同)判为 Modified。
func TestComputeDiff_Modified_SameSizeDiffContent(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	// 相同长度,不同内容 → hash 不同 → Modified
	makeFile(t, localDir, "GROOT.md", "aaaa\n")
	makeFile(t, remoteDir, "GROOT.md", "bbbb\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Modified) != 1 {
		t.Errorf("expected Modified=[GROOT.md] (same size, diff hash), got %+v", result)
	}
}

func TestComputeDiff_RecursiveDir(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "skills/weather/SKILL.md", "weather skill\n")
	makeFile(t, localDir, "skills/weather/handler.go", "package main\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"skills/weather"})
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

// TestComputeDiff_SkipsTmpFiles 验证 ComputeDiff 跳过 *.tmp 残留文件。
// 它们是 sync 工具自己用作原子写中转的临时产物,不应被列入 diff
// (否则在 diff/pull 输出里会展示为"差异",甚至被 push 推到远端)。
func TestComputeDiff_SkipsTmpFiles(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "skills/weather/SKILL.md", "weather\n")
	makeFile(t, localDir, "skills/weather/SKILL.md.tmp", "stale residue\n")
	makeFile(t, localDir, "skills/weather/probe.tmp", "another residue\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"skills"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "skills/weather/SKILL.md" {
		t.Errorf("expected Added=[skills/weather/SKILL.md] (no .tmp), got %+v", result.Added)
	}
}
