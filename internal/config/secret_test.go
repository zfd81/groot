package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAuthSecret(t *testing.T) {
	s1, err := GenerateAuthSecret()
	if err != nil {
		t.Fatalf("GenerateAuthSecret: %v", err)
	}
	if len(s1) != 64 {
		t.Errorf("secret length = %d, want 64 hex chars", len(s1))
	}
	s2, _ := GenerateAuthSecret()
	if s1 == s2 {
		t.Error("two secrets should differ")
	}
}

// TestWriteFileAtomic 验证原子写入：内容正确、权限 0600、不留 .tmp 残留。
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bootstrap.yaml")
	content := []byte("server:\n  port: 8080\n")

	if err := writeFileAtomic(p, content); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content = %q, want %q", data, content)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file perm = %o, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("临时文件 .tmp 不应残留")
	}

	// 覆盖写入：已有文件被完整替换
	if err := writeFileAtomic(p, []byte("replaced")); err != nil {
		t.Fatalf("writeFileAtomic overwrite: %v", err)
	}
	data, _ = os.ReadFile(p)
	if string(data) != "replaced" {
		t.Errorf("overwrite content = %q, want %q", data, "replaced")
	}
}
