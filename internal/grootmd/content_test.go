package grootmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetContent_FileExists(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "GROOT.md")

	content := "# Test GROOT.md\n\nHello world\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got := GetContent(tmpDir)
	if got != content {
		t.Errorf("GetContent() = %q, want %q", got, content)
	}
}

func TestGetContent_FileNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	// 不创建 GROOT.md

	got := GetContent(tmpDir)
	if got != "" {
		t.Errorf("GetContent() = %q, want empty (file not exists)", got)
	}
}

func TestGetContent_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "GROOT.md")

	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	got := GetContent(tmpDir)
	if got != "" {
		t.Errorf("GetContent() = %q, want empty (empty file)", got)
	}
}

func TestGetContent_Multiline(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "GROOT.md")

	multiline := `# GROOT.md

This is a test content.
Multiple lines.

## Section
Content here.
`
	if err := os.WriteFile(path, []byte(multiline), 0644); err != nil {
		t.Fatal(err)
	}

	got := GetContent(tmpDir)
	if got != multiline {
		t.Errorf("GetContent() = %q, want %q", got, multiline)
	}
}

func TestGetContent_UnreadableFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "GROOT.md")

	// 创建文件后移除读权限
	if err := os.WriteFile(path, []byte("secret"), 0000); err != nil {
		t.Fatal(err)
	}

	got := GetContent(tmpDir)
	if got != "" {
		t.Errorf("GetContent() = %q, want empty (unreadable file)", got)
	}
}
