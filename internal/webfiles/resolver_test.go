package webfiles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// newResolverForTest 在临时目录上构造 Resolver。
func newResolverForTest(t *testing.T) (*Resolver, string) {
	t.Helper()
	home := t.TempDir()
	r, err := NewResolver(home)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r, home
}

// TestResolver_Normalize 验证路径清洗：".." 无法越过根，反斜杠统一为斜杠。
func TestResolver_Normalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"skills/foo", "skills/foo"},
		{"/skills/foo/", "skills/foo"},
		{"../../etc/passwd", "etc/passwd"}, // 锚定后穿越被折叠进根
		{"skills/../mcp", "mcp"},
		{"a\\b", "a/b"},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if err != nil || got != c.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := Normalize("a\x00b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("含 NUL 的路径应返回 ErrNotFound, got %v", err)
	}
}

// TestResolver_Resolve_Basic 验证正常路径解析到 home 内的绝对路径。
func TestResolver_Resolve_Basic(t *testing.T) {
	r, home := newResolverForTest(t)
	abs, n, err := r.Resolve("skills/foo")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	realHome, _ := filepath.EvalSymlinks(home)
	want := filepath.Join(realHome, "skills", "foo")
	if abs != want || n != "skills/foo" {
		t.Errorf("Resolve = %q, %q; want %q, skills/foo", abs, n, want)
	}
}

// TestResolver_Resolve_SymlinkEscape 验证指向 home 外的符号链接被拒绝。
func TestResolver_Resolve_SymlinkEscape(t *testing.T) {
	r, home := newResolverForTest(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "evil")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, _, err := r.Resolve("evil"); !errors.Is(err, ErrNotFound) {
		t.Errorf("符号链接逃逸应返回 ErrNotFound, got %v", err)
	}
	// 链接下的子路径同样拒绝
	if _, _, err := r.Resolve("evil/sub.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("符号链接子路径应返回 ErrNotFound, got %v", err)
	}
}

// TestResolver_Hidden 验证 groot.db* 隐藏规则只作用于根目录。
func TestResolver_Hidden(t *testing.T) {
	r, _ := newResolverForTest(t)
	for _, n := range []string{"groot.db", "groot.db-wal", "groot.db-shm"} {
		if !r.Hidden(n) {
			t.Errorf("Hidden(%q) = false, want true", n)
		}
		if _, _, err := r.Resolve(n); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) 应返回 ErrNotFound, got %v", n, err)
		}
	}
	if r.Hidden("skills/groot.db") {
		t.Error("子目录中的 groot.db 不应隐藏")
	}
	if r.Hidden("") {
		t.Error("根路径不应隐藏")
	}
}

// TestResolver_ReadOnly 验证只读规则只覆盖根目录的两个配置文件。
func TestResolver_ReadOnly(t *testing.T) {
	r, _ := newResolverForTest(t)
	if !r.ReadOnly("env.yaml") || !r.ReadOnly("config.yaml") {
		t.Error("env.yaml/config.yaml 应为只读")
	}
	if r.ReadOnly("skills/config.yaml") || r.ReadOnly("GROOT.md") {
		t.Error("非根配置文件不应只读")
	}
}

// TestResolver_CanUpload 验证 home 内任意目录（含根）均允许上传。
func TestResolver_CanUpload(t *testing.T) {
	r, _ := newResolverForTest(t)
	for _, dir := range []string{
		"", "skills", "mcp", "subagents", "logs", "api", "exports",
		"skills/my-skill", "skills/my-skill/scripts",
		"subagents/weather", "subagents/weather/skills/get-weather",
	} {
		if !r.CanUpload(dir) {
			t.Errorf("CanUpload(%q) = false, want true", dir)
		}
	}
}

// TestResolver_CaseInsensitive 验证隐藏/只读规则大小写不敏感（macOS/Windows 文件系统大小写不敏感）。
func TestResolver_CaseInsensitive(t *testing.T) {
	r, _ := newResolverForTest(t)
	for _, n := range []string{"GROOT.DB", "Groot.Db-wal"} {
		if !r.Hidden(n) {
			t.Errorf("Hidden(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"Env.yaml", "CONFIG.YAML"} {
		if !r.ReadOnly(n) {
			t.Errorf("ReadOnly(%q) = false, want true", n)
		}
	}
}

// TestResolver_Resolve_FileAsDir 验证把文件当目录穿越（ENOTDIR）返回 ErrNotFound 而非裸错误。
func TestResolver_Resolve_FileAsDir(t *testing.T) {
	r, home := newResolverForTest(t)
	if err := os.WriteFile(filepath.Join(home, "env.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := r.Resolve("env.yaml/sub"); !errors.Is(err, ErrNotFound) {
		t.Errorf("文件下的子路径应返回 ErrNotFound, got %v", err)
	}
}

// TestResolver_Normalize_SpecialNames 验证 Windows 特殊文件名（ADS 冒号、尾点、尾空格）被拒绝。
func TestResolver_Normalize_SpecialNames(t *testing.T) {
	for _, in := range []string{"mcp/a:b.json", "skills/x/name.", "skills/x/name "} {
		if _, err := Normalize(in); !errors.Is(err, ErrNotFound) {
			t.Errorf("Normalize(%q) 应返回 ErrNotFound, got %v", in, err)
		}
	}
}

// TestResolver_CanUpload_Defensive 验证未归一化输入被防御性拒绝。
func TestResolver_CanUpload_Defensive(t *testing.T) {
	r, _ := newResolverForTest(t)
	for _, dir := range []string{"skills/../mcp", "./skills"} {
		if r.CanUpload(dir) {
			t.Errorf("CanUpload(%q) 应为 false", dir)
		}
	}
}

// TestResolver_Resolve_NeighborPrefix 验证同名前缀的邻居目录不被误判为 home 内。
func TestResolver_Resolve_NeighborPrefix(t *testing.T) {
	r, home := newResolverForTest(t)
	neighbor := home + "X"
	if err := os.Mkdir(neighbor, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Symlink(neighbor, filepath.Join(home, "link")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, _, err := r.Resolve("link"); !errors.Is(err, ErrNotFound) {
		t.Errorf("邻居前缀目录应返回 ErrNotFound, got %v", err)
	}
}

// TestResolver_Resolve_InternalSymlink 验证指向 home 内部的符号链接可正常解析。
func TestResolver_Resolve_InternalSymlink(t *testing.T) {
	r, home := newResolverForTest(t)
	if err := os.Mkdir(filepath.Join(home, "target"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(home, "target"), filepath.Join(home, "link")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, _, err := r.Resolve("link"); err != nil {
		t.Errorf("home 内部符号链接应解析成功, got %v", err)
	}
}

// TestNewResolver_NotDir 验证 home 指向普通文件时报错。
func TestNewResolver_NotDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := NewResolver(f); err == nil {
		t.Error("NewResolver(普通文件) 应返回错误")
	}
}
