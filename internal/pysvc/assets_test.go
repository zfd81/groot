package pysvc

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestServicesDir(t *testing.T) {
	home := t.TempDir()

	want := filepath.Join(home, "services")
	if got := ServicesDir(home); got != want {
		t.Errorf("ServicesDir = %q, want %q", got, want)
	}
}

func TestEnsureLayout_CreatesEverything(t *testing.T) {
	home := t.TempDir()

	created, err := EnsureLayout(home)
	if err != nil {
		t.Fatalf("EnsureLayout 失败: %v", err)
	}

	wantPaths := []string{
		"services",
		"services/.sdk",
		"services/_template/app/main.py",
		"services/service.yaml",
		"services/requirements.txt",
	}
	for _, rel := range wantPaths {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("期望存在 %s, stat 失败: %v", path, err)
		}
		if !slices.Contains(created, path) {
			t.Errorf("返回值缺少新建路径 %s, got %v", path, created)
		}
	}
}

func TestEnsureLayout_Idempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureLayout(home); err != nil {
		t.Fatalf("首次 EnsureLayout 失败: %v", err)
	}

	configPath := filepath.Join(ServicesDir(home), "service.yaml")
	custom := "# 自定义配置\nstartup_timeout: 300s\n"
	if err := os.WriteFile(configPath, []byte(custom), 0o644); err != nil {
		t.Fatalf("写入自定义配置失败: %v", err)
	}

	created, err := EnsureLayout(home)
	if err != nil {
		t.Fatalf("再次 EnsureLayout 失败: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("已存在布局时返回的新建路径应为空, got %v", created)
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	if string(got) != custom {
		t.Errorf("service.yaml 被覆盖: got %q, want %q", string(got), custom)
	}
}

func TestEnsureLayout_TemplateContent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout 失败: %v", err)
	}

	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(ServicesDir(home), filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", rel, err)
		}
		return string(data)
	}

	wantYAML := "# Python 服务全局配置；取消注释即生效，缺省值如下。\n" +
		"# startup_timeout: 180s\n" +
		"# request_timeout: 120s\n" +
		"# permission:\n" +
		"#   asr-router: chat\n"
	if got := read("service.yaml"); got != wantYAML {
		t.Errorf("service.yaml = %q, want %q", got, wantYAML)
	}

	wantReq := "fastapi>=0.110\nuvicorn[standard]>=0.29\npython-multipart>=0.0.9\n"
	if got := read("requirements.txt"); got != wantReq {
		t.Errorf("requirements.txt = %q, want %q", got, wantReq)
	}
}

func TestExtractSDK(t *testing.T) {
	home := t.TempDir()

	root, err := ExtractSDK(home)
	if err != nil {
		t.Fatalf("ExtractSDK 失败: %v", err)
	}
	wantRoot := filepath.Join(ServicesDir(home), ".sdk", SDKVersion)
	if root != wantRoot {
		t.Errorf("SDK 根目录 = %q, want %q", root, wantRoot)
	}

	initPath := filepath.Join(root, "groot_service_sdk", "__init__.py")
	first, err := os.ReadFile(initPath)
	if err != nil {
		t.Fatalf("SDK 未解压出 %s: %v", initPath, err)
	}

	root2, err := ExtractSDK(home)
	if err != nil {
		t.Fatalf("再次 ExtractSDK 失败: %v", err)
	}
	if root2 != wantRoot {
		t.Errorf("再次 ExtractSDK 根目录 = %q, want %q", root2, wantRoot)
	}

	second, err := os.ReadFile(initPath)
	if err != nil {
		t.Fatalf("再次读取 SDK 失败: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("重复解压改变了 SDK 内容: first %q, second %q", first, second)
	}
}

func TestExtractSDK_NoBytecodeCache(t *testing.T) {
	home := t.TempDir()

	root, err := ExtractSDK(home)
	if err != nil {
		t.Fatalf("ExtractSDK 失败: %v", err)
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "__pycache__" {
			t.Errorf("解压出的 SDK 不应包含 __pycache__: %s", path)
		}
		if !d.IsDir() && (strings.HasSuffix(d.Name(), ".pyc") || strings.HasSuffix(d.Name(), ".pyo")) {
			t.Errorf("解压出的 SDK 不应包含字节码文件: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 SDK 目录失败: %v", err)
	}
}

func TestTemplateMain(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout 失败: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(ServicesDir(home), "_template", "app", "main.py"))
	if err != nil {
		t.Fatalf("读取模板 main.py 失败: %v", err)
	}
	content := string(data)
	for _, want := range []string{"async def handle", "METHODS", "async def startup", "async def shutdown"} {
		if !strings.Contains(content, want) {
			t.Errorf("模板 main.py 缺少 %q:\n%s", want, content)
		}
	}
}

func TestExtractSDK_RootIsWorldTraversable(t *testing.T) {
	// MkdirTemp 创建的目录是 0700；若同一 GROOT_HOME 被多个系统用户的 Groot 实例共享，
	// 其他用户的 Python 进程将无法 import SDK。解压完成后根目录须为 0755。
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 Unix 权限位")
	}
	home := t.TempDir()
	root, err := ExtractSDK(home)
	if err != nil {
		t.Fatalf("ExtractSDK 失败: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("SDK 根目录权限 = %o, want 755", got)
	}
}
