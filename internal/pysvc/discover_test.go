package pysvc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mkFiles 在 root/name 下创建给定的相对路径文件（自动建父目录）。
func mkFiles(t *testing.T, root, name string, files ...string) {
	t.Helper()
	base := filepath.Join(root, name)
	for _, rel := range files {
		path := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("创建目录失败: %v", err)
		}
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatalf("写入文件 %s 失败: %v", path, err)
		}
	}
}

// mkDirs 在 root 下创建给定的相对目录。
func mkDirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, rel := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatalf("创建目录 %s 失败: %v", rel, err)
		}
	}
}

// findService 按名字查找发现结果中的一项。
func findService(t *testing.T, services []Service, name string) Service {
	t.Helper()
	for _, s := range services {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("未找到子服务 %q, 结果为 %+v", name, services)
	return Service{}
}

func TestDiscover_ValidService(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, "asr-router", "app/__init__.py", "app/main.py", "requirements.txt")
	// 根目录下的普通文件不是子服务。
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(""), 0o644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}

	services := Discover(root, DefaultGlobalConfig())

	if len(services) != 1 {
		t.Fatalf("发现 %d 个子服务, want 1: %+v", len(services), services)
	}
	s := services[0]
	if s.Name != "asr-router" {
		t.Errorf("Name = %q, want %q", s.Name, "asr-router")
	}
	if s.Dir != filepath.Join(root, "asr-router") {
		t.Errorf("Dir = %q, want %q", s.Dir, filepath.Join(root, "asr-router"))
	}
	if s.Invalid != "" {
		t.Errorf("Invalid = %q, want 空", s.Invalid)
	}
	if s.Permission != "all" {
		t.Errorf("Permission = %q, want %q", s.Permission, "all")
	}
}

func TestDiscover_PermissionFromConfig(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, "asr-router", "app/__init__.py", "app/main.py", "requirements.txt")

	cfg := DefaultGlobalConfig()
	cfg.Permission["asr-router"] = "chat"

	services := Discover(root, cfg)

	s := findService(t, services, "asr-router")
	if s.Permission != "chat" {
		t.Errorf("Permission = %q, want %q", s.Permission, "chat")
	}
}

func TestDiscover_MissingFiles(t *testing.T) {
	root := t.TempDir()
	// broken 有 app/ 但没有 main.py；nofile 什么都没有。
	mkDirs(t, root, "broken/app", "nofile")

	services := Discover(root, DefaultGlobalConfig())

	if len(services) != 2 {
		t.Fatalf("发现 %d 个子服务, want 2: %+v", len(services), services)
	}
	// 结果按 Name 升序。
	if services[0].Name != "broken" || services[1].Name != "nofile" {
		t.Errorf("顺序 = [%q %q], want [broken nofile]", services[0].Name, services[1].Name)
	}

	broken := findService(t, services, "broken")
	if broken.Invalid == "" {
		t.Error("broken.Invalid 为空, want 非空原因")
	}
	if !strings.Contains(broken.Invalid, "app/main.py") {
		t.Errorf("broken.Invalid = %q, want 含 %q", broken.Invalid, "app/main.py")
	}

	nofile := findService(t, services, "nofile")
	if nofile.Invalid == "" {
		t.Error("nofile.Invalid 为空, want 非空原因")
	}
	if !strings.Contains(nofile.Invalid, "requirements.txt") {
		t.Errorf("nofile.Invalid = %q, want 含 %q", nofile.Invalid, "requirements.txt")
	}
}

func TestDiscover_IgnoreDotAndUnderscore(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, ".sdk", "app/__init__.py", "app/main.py", "requirements.txt")
	mkFiles(t, root, "_template", "app/__init__.py", "app/main.py", "requirements.txt")

	services := Discover(root, DefaultGlobalConfig())

	if len(services) != 0 {
		t.Errorf("发现 %d 个子服务, want 0: %+v", len(services), services)
	}
}

func TestDiscover_DuplicateAfterNormalize(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, "asr-router", "app/__init__.py", "app/main.py", "requirements.txt")
	mkFiles(t, root, "asr_router", "app/__init__.py", "app/main.py", "requirements.txt")

	services := Discover(root, DefaultGlobalConfig())

	if len(services) != 2 {
		t.Fatalf("发现 %d 个子服务, want 2: %+v", len(services), services)
	}

	kept := findService(t, services, "asr-router")
	if kept.Invalid != "" {
		t.Errorf("asr-router.Invalid = %q, want 空（字典序在前，保留）", kept.Invalid)
	}

	dup := findService(t, services, "asr_router")
	if dup.Invalid == "" {
		t.Error("asr_router.Invalid 为空, want 非空原因")
	}
	if !strings.Contains(dup.Invalid, "重名") {
		t.Errorf("asr_router.Invalid = %q, want 含 %q", dup.Invalid, "重名")
	}
}

func TestDiscover_InvalidPermissionFallsBackToAll(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, "asr-router", "app/__init__.py", "app/main.py", "requirements.txt")

	cfg := DefaultGlobalConfig()
	cfg.Permission["asr-router"] = "nope"

	services := Discover(root, cfg)

	s := findService(t, services, "asr-router")
	if s.Permission != "all" {
		t.Errorf("Permission = %q, want %q", s.Permission, "all")
	}
	if s.Invalid != "" {
		t.Errorf("Invalid = %q, want 空（权限值非法不影响服务可用性）", s.Invalid)
	}
}

func TestDiscover_InvalidName(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, "ASR", "app/__init__.py", "app/main.py", "requirements.txt")

	services := Discover(root, DefaultGlobalConfig())

	if len(services) != 1 {
		t.Fatalf("发现 %d 个子服务, want 1: %+v", len(services), services)
	}
	s := services[0]
	if s.Invalid == "" {
		t.Fatal("Invalid 为空, want 非空原因")
	}
	if !strings.Contains(s.Invalid, "命名规则") {
		t.Errorf("Invalid = %q, want 含 %q", s.Invalid, "命名规则")
	}
}

func TestDiscover_SymlinkedServiceDir(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	mkFiles(t, realRoot, "asr-router", "app/__init__.py", "app/main.py", "requirements.txt")

	root := filepath.Join(base, "services")
	mkDirs(t, base, "services")
	if err := os.Symlink(filepath.Join(realRoot, "asr-router"), filepath.Join(root, "asr-router")); err != nil {
		t.Skipf("当前文件系统不支持符号链接: %v", err)
	}

	services := Discover(root, DefaultGlobalConfig())

	if len(services) != 1 {
		t.Fatalf("发现 %d 个子服务, want 1: %+v", len(services), services)
	}
	s := services[0]
	if s.Invalid != "" {
		t.Errorf("符号链接目录应视为有效子服务, Invalid = %q", s.Invalid)
	}
	if s.Dir != filepath.Join(root, "asr-router") {
		t.Errorf("Dir = %q, want 链接路径 %q", s.Dir, filepath.Join(root, "asr-router"))
	}
}

func TestDiscover_BrokenSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "ghost")); err != nil {
		t.Skipf("当前文件系统不支持符号链接: %v", err)
	}

	services := Discover(root, DefaultGlobalConfig())

	if len(services) != 1 {
		t.Fatalf("发现 %d 个子服务, want 1: %+v", len(services), services)
	}
	s := services[0]
	if s.Name != "ghost" {
		t.Errorf("Name = %q, want ghost", s.Name)
	}
	if s.Invalid == "" {
		t.Fatal("断链应记为无效项并给出原因")
	}
	if !strings.Contains(s.Invalid, "符号链接") {
		t.Errorf("Invalid = %q, want 含 %q", s.Invalid, "符号链接")
	}
}

func TestDiscover_ReservedHealthz(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, "healthz", "app/__init__.py", "app/main.py", "requirements.txt")

	services := Discover(root, DefaultGlobalConfig())

	if len(services) != 1 {
		t.Fatalf("发现 %d 个子服务, want 1: %+v", len(services), services)
	}
	s := services[0]
	if s.Invalid == "" {
		t.Fatal("healthz 是保留名称，应记为无效项")
	}
	if !strings.Contains(s.Invalid, "保留名称") {
		t.Errorf("Invalid = %q, want 含 %q", s.Invalid, "保留名称")
	}
}

func TestDiscover_MissingAppInit(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, "noinit", "app/main.py", "requirements.txt")

	services := Discover(root, DefaultGlobalConfig())

	s := findService(t, services, "noinit")
	if s.Invalid == "" {
		t.Fatal("缺少 app/__init__.py 应记为无效项")
	}
	if !strings.Contains(s.Invalid, "app/__init__.py") {
		t.Errorf("Invalid = %q, want 含 %q", s.Invalid, "app/__init__.py")
	}
}

func TestInterpreterPath(t *testing.T) {
	dir := t.TempDir()

	got := InterpreterPath(dir)

	want := filepath.Join(dir, ".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		want = filepath.Join(dir, ".venv", "Scripts", "python.exe")
	}
	if got != want {
		t.Errorf("InterpreterPath() = %q, want %q", got, want)
	}
}
