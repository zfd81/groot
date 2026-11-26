package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/pysvc"
)

// servicesFakeRunner 是测试用的命令执行器：记录探测与调用，Run 的行为由 run 指定。
type servicesFakeRunner struct {
	paths   map[string]string
	calls   []string
	lookups []string
	run     func(name string, args ...string) ([]byte, error)
}

func (f *servicesFakeRunner) LookPath(file string) (string, error) {
	f.lookups = append(f.lookups, file)
	if p, ok := f.paths[file]; ok {
		return p, nil
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", file)
}

func (f *servicesFakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.run == nil {
		return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
	}
	return f.run(name, args...)
}

// servicesVenvInterpreterRel 返回当前平台下虚拟环境解释器的相对路径。
func servicesVenvInterpreterRel() string {
	if runtime.GOOS == "windows" {
		return filepath.Join("Scripts", "python.exe")
	}
	return filepath.Join("bin", "python")
}

// servicesTestInterpreterPath 返回 homeDir 下共享虚拟环境解释器的路径。
func servicesTestInterpreterPath(homeDir string) string {
	return filepath.Join(homeDir, "services", ".venv", servicesVenvInterpreterRel())
}

// servicesArgsContain 报告 args 中是否连续出现 want。
func servicesArgsContain(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		match := true
		for j := range want {
			if args[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// newServicesFakeRunner 返回一个模拟可用 Python 的执行器：查询版本返回 3.12，
// 创建虚拟环境时写出解释器文件，pip 安装成功。
func newServicesFakeRunner() *servicesFakeRunner {
	f := &servicesFakeRunner{paths: map[string]string{"python3": "/usr/bin/python3"}}
	f.run = func(name string, args ...string) ([]byte, error) {
		switch {
		case servicesArgsContain(args, "-c"):
			return []byte("3.12\n"), nil
		case servicesArgsContain(args, "-m", "venv"):
			venvDir := args[len(args)-1]
			interp := filepath.Join(venvDir, servicesVenvInterpreterRel())
			if err := os.MkdirAll(filepath.Dir(interp), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(interp, nil, 0o755); err != nil {
				return nil, err
			}
			return nil, nil
		case servicesArgsContain(args, "-m", "pip", "install"):
			return []byte("Successfully installed fastapi uvicorn\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
		}
	}
	return f
}

func TestParseServicesInitFlags(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantPython string
		wantError  string
	}{
		{
			name: "default values",
			args: []string{},
		},
		{
			name:       "explicit python",
			args:       []string{"--py", "/opt/python3.12"},
			wantPython: "/opt/python3.12",
		},
		{
			name:      "missing value",
			args:      []string{"--py"},
			wantError: "--py requires a value",
		},
		{
			name:      "unknown flag",
			args:      []string{"--invalid"},
			wantError: "unknown flag: --invalid",
		},
		{
			name:      "unexpected argument",
			args:      []string{"extra"},
			wantError: "unexpected argument: extra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, err := ParseServicesInitFlags(tt.args)

			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("expected error %q but got nil", tt.wantError)
				}
				if err.Error() != tt.wantError {
					t.Fatalf("expected error %q but got %q", tt.wantError, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if flags.Python != tt.wantPython {
				t.Errorf("expected Python %q but got %q", tt.wantPython, flags.Python)
			}
		})
	}
}

func TestRunServicesInit_CreatesLayoutVenvAndInstallsDeps(t *testing.T) {
	homeDir := t.TempDir()
	runner := newServicesFakeRunner()

	if err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, rel := range []string{
		"services/service.yaml",
		"services/requirements.txt",
		"services/_template/app/main.py",
		"services/.sdk",
	} {
		if _, err := os.Stat(filepath.Join(homeDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
	if _, err := os.Stat(servicesTestInterpreterPath(homeDir)); err != nil {
		t.Errorf("expected interpreter to exist: %v", err)
	}

	joined := strings.Join(runner.calls, "\n")
	if !strings.Contains(joined, "-m venv") {
		t.Errorf("expected a venv creation call, got calls:\n%s", joined)
	}
	if !strings.Contains(joined, "-m pip install") {
		t.Errorf("expected a pip install call, got calls:\n%s", joined)
	}
}

func TestRunServicesInit_CreatesHomeWhenMissing(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "groot")
	runner := newServicesFakeRunner()

	if err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(homeDir); err != nil {
		t.Errorf("expected home directory to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "services")); err != nil {
		t.Errorf("expected services directory to exist: %v", err)
	}
}

func TestRunServicesInit_Idempotent(t *testing.T) {
	homeDir := t.TempDir()
	runner := newServicesFakeRunner()

	if err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	serviceYAML := filepath.Join(homeDir, "services", "service.yaml")
	marker := "# user-customized-marker\n"
	if err := os.WriteFile(serviceYAML, []byte(marker), 0o644); err != nil {
		t.Fatalf("write marker failed: %v", err)
	}

	if err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("second run failed: %v", err)
	}

	data, err := os.ReadFile(serviceYAML)
	if err != nil {
		t.Fatalf("read service.yaml failed: %v", err)
	}
	if string(data) != marker {
		t.Errorf("expected user marker to be preserved, got %q", string(data))
	}

	venvCalls := 0
	pipCalls := 0
	for _, call := range runner.calls {
		if strings.Contains(call, "-m venv") {
			venvCalls++
		}
		if strings.Contains(call, "-m pip install") {
			pipCalls++
		}
	}
	if venvCalls != 1 {
		t.Errorf("expected 1 venv creation call, got %d (calls:\n%s)", venvCalls, strings.Join(runner.calls, "\n"))
	}
	if pipCalls != 2 {
		t.Errorf("expected 2 pip install calls, got %d (calls:\n%s)", pipCalls, strings.Join(runner.calls, "\n"))
	}
}

// TestRunServicesInit_SuccessOutputHasNoSubServiceHint 验证基础依赖安装成功时，
// 输出不含子服务业务依赖提示（占位符 <子服务名>），且解释器信息与收尾状态仍在。
func TestRunServicesInit_SuccessOutputHasNoSubServiceHint(t *testing.T) {
	homeDir := t.TempDir()
	runner := newServicesFakeRunner()

	var out bytes.Buffer
	if err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: &out}); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "<子服务名>") {
		t.Errorf("输出不应包含子服务依赖提示，实际输出:\n%s", got)
	}
	if !strings.Contains(got, "解释器: ") {
		t.Errorf("输出缺少解释器信息，实际输出:\n%s", got)
	}
	if strings.Contains(got, "install -r") {
		t.Errorf("成功路径不应输出 pip 安装命令，实际输出:\n%s", got)
	}
	if !strings.Contains(got, "初始化完成") {
		t.Errorf("输出缺少收尾状态，实际输出:\n%s", got)
	}
	if strings.Contains(got, "需手动安装") {
		t.Errorf("成功路径不应出现失败分支的收尾文案，实际输出:\n%s", got)
	}
}

func TestRunServicesInit_DepsFailureReturnsNil(t *testing.T) {
	homeDir := t.TempDir()
	runner := newServicesFakeRunner()
	baseRun := runner.run
	runner.run = func(name string, args ...string) ([]byte, error) {
		if servicesArgsContain(args, "-m", "pip", "install") {
			return []byte("ERROR: network unreachable\nCould not install packages\n"), fmt.Errorf("exit status 1")
		}
		return baseRun(name, args...)
	}

	if err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("expected nil error on deps failure, got: %v", err)
	}

	for _, rel := range []string{"services/service.yaml", "services/requirements.txt"} {
		if _, err := os.Stat(filepath.Join(homeDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
	if _, err := os.Stat(servicesTestInterpreterPath(homeDir)); err != nil {
		t.Errorf("expected interpreter to exist: %v", err)
	}
}

// TestRunServicesInit_DepsFailureWritesManualCommands 验证基础依赖安装失败时，
// 输出写入注入的 Out，并给出完整的手动安装指引。
func TestRunServicesInit_DepsFailureWritesManualCommands(t *testing.T) {
	homeDir := t.TempDir()
	runner := newServicesFakeRunner()
	baseRun := runner.run
	runner.run = func(name string, args ...string) ([]byte, error) {
		if servicesArgsContain(args, "-m", "pip", "install") {
			return []byte("Traceback (most recent call last):\nModuleNotFoundError: No module named pip\n"), fmt.Errorf("exit status 1")
		}
		return baseRun(name, args...)
	}

	var out bytes.Buffer
	if err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: &out}); err != nil {
		t.Fatalf("expected nil error on deps failure, got: %v", err)
	}

	got := out.String()
	servicesDir := filepath.Join(homeDir, "services")
	requirements := filepath.Join(servicesDir, "requirements.txt")
	venvPip := pysvc.VenvPipPath(servicesDir)

	for _, want := range []string{
		"基础依赖未安装成功",
		"No module named pip",
		venvPip + " install -r " + requirements,
		"--no-index --find-links",
		"apt install python3-venv",
		"初始化完成（基础依赖需手动安装）",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("输出缺少 %q，实际输出:\n%s", want, got)
		}
	}
}

func TestRunServicesInit_PythonMissingReturnsError(t *testing.T) {
	homeDir := t.TempDir()
	runner := &servicesFakeRunner{}

	err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: io.Discard})
	if err == nil {
		t.Fatal("expected error when no Python is available")
	}
	if !strings.Contains(err.Error(), "python") {
		t.Errorf("expected error to mention python, got: %v", err)
	}
	if !strings.Contains(err.Error(), "3.9") {
		t.Errorf("expected error to mention 3.9, got: %v", err)
	}
}

func TestRunServicesInit_BrokenVenvReturnsError(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "services", ".venv"), 0o755); err != nil {
		t.Fatalf("prepare broken venv failed: %v", err)
	}
	runner := &servicesFakeRunner{}

	err := RunServicesInit(homeDir, ServicesInitOptions{Runner: runner, Out: io.Discard})
	if err == nil {
		t.Fatal("expected error for broken venv")
	}
	if !strings.Contains(err.Error(), "rm -rf") {
		t.Errorf("expected error to mention rm -rf, got: %v", err)
	}
}

func TestRunServicesInit_UsesExplicitPython(t *testing.T) {
	homeDir := t.TempDir()
	runner := newServicesFakeRunner()
	runner.paths = map[string]string{"/opt/py3": "/opt/py3"}

	if err := RunServicesInit(homeDir, ServicesInitOptions{Python: "/opt/py3", Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := strings.Join(runner.calls, "\n")
	if !strings.Contains(joined, "/opt/py3 -m venv") {
		t.Errorf("expected venv creation with explicit python, got calls:\n%s", joined)
	}
	for _, lookup := range runner.lookups {
		if lookup == "python3" {
			t.Errorf("expected explicit python to skip python3 lookup, got lookups: %v", runner.lookups)
		}
	}
}
