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

	"github.com/zfd81/groot/internal/plugin"
)

// pluginsFakeRunner 是测试用的命令执行器：记录探测与调用，Run 的行为由 run 指定。
type pluginsFakeRunner struct {
	paths   map[string]string
	calls   []string
	lookups []string
	run     func(name string, args ...string) ([]byte, error)
}

func (f *pluginsFakeRunner) LookPath(file string) (string, error) {
	f.lookups = append(f.lookups, file)
	if p, ok := f.paths[file]; ok {
		return p, nil
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", file)
}

func (f *pluginsFakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.run == nil {
		return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
	}
	return f.run(name, args...)
}

// pluginsVenvInterpreterRel 返回当前平台下虚拟环境解释器的相对路径。
func pluginsVenvInterpreterRel() string {
	if runtime.GOOS == "windows" {
		return filepath.Join("Scripts", "python.exe")
	}
	return filepath.Join("bin", "python")
}

// pluginsTestInterpreterPath 返回 homeDir 下共享虚拟环境解释器的路径。
func pluginsTestInterpreterPath(homeDir string) string {
	return filepath.Join(homeDir, "plugins", ".venv", pluginsVenvInterpreterRel())
}

// pluginsArgsContain 报告 args 中是否连续出现 want。
func pluginsArgsContain(args []string, want ...string) bool {
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

// newPluginsFakeRunner 返回一个模拟可用 Python 的执行器：查询版本返回 3.12，
// 创建虚拟环境时写出解释器文件，pip 安装成功。
func newPluginsFakeRunner() *pluginsFakeRunner {
	f := &pluginsFakeRunner{paths: map[string]string{"python3": "/usr/bin/python3"}}
	f.run = func(name string, args ...string) ([]byte, error) {
		switch {
		case pluginsArgsContain(args, "-c"):
			return []byte("3.12\n"), nil
		case pluginsArgsContain(args, "-m", "venv"):
			venvDir := args[len(args)-1]
			interp := filepath.Join(venvDir, pluginsVenvInterpreterRel())
			if err := os.MkdirAll(filepath.Dir(interp), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(interp, nil, 0o755); err != nil {
				return nil, err
			}
			return nil, nil
		case pluginsArgsContain(args, "-m", "pip", "install"):
			return []byte("Successfully installed fastapi uvicorn\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
		}
	}
	return f
}

func TestParsePluginsInitFlags(t *testing.T) {
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
			flags, err := ParsePluginsInitFlags(tt.args)

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

func TestRunPluginsInit_CreatesLayoutVenvAndInstallsDeps(t *testing.T) {
	homeDir := t.TempDir()
	runner := newPluginsFakeRunner()

	if err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, rel := range []string{
		"plugins/plugin.yaml",
		"plugins/requirements.txt",
		"plugins/_template/app/main.py",
		"plugins/.sdk",
	} {
		if _, err := os.Stat(filepath.Join(homeDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
	if _, err := os.Stat(pluginsTestInterpreterPath(homeDir)); err != nil {
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

func TestRunPluginsInit_CreatesHomeWhenMissing(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "groot")
	runner := newPluginsFakeRunner()

	if err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(homeDir); err != nil {
		t.Errorf("expected home directory to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "plugins")); err != nil {
		t.Errorf("expected plugins directory to exist: %v", err)
	}
}

func TestRunPluginsInit_Idempotent(t *testing.T) {
	homeDir := t.TempDir()
	runner := newPluginsFakeRunner()

	if err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	pluginYAML := filepath.Join(homeDir, "plugins", "plugin.yaml")
	marker := "# user-customized-marker\n"
	if err := os.WriteFile(pluginYAML, []byte(marker), 0o644); err != nil {
		t.Fatalf("write marker failed: %v", err)
	}

	if err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("second run failed: %v", err)
	}

	data, err := os.ReadFile(pluginYAML)
	if err != nil {
		t.Fatalf("read plugin.yaml failed: %v", err)
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

// TestRunPluginsInit_SuccessOutputHasNoSubPluginHint 验证基础依赖安装成功时，
// 输出不含插件业务依赖提示（占位符 <插件名>），且解释器信息与收尾状态仍在。
func TestRunPluginsInit_SuccessOutputHasNoSubPluginHint(t *testing.T) {
	homeDir := t.TempDir()
	runner := newPluginsFakeRunner()

	var out bytes.Buffer
	if err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: &out}); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "<插件名>") {
		t.Errorf("输出不应包含插件依赖提示，实际输出:\n%s", got)
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

func TestRunPluginsInit_DepsFailureReturnsNil(t *testing.T) {
	homeDir := t.TempDir()
	runner := newPluginsFakeRunner()
	baseRun := runner.run
	runner.run = func(name string, args ...string) ([]byte, error) {
		if pluginsArgsContain(args, "-m", "pip", "install") {
			return []byte("ERROR: network unreachable\nCould not install packages\n"), fmt.Errorf("exit status 1")
		}
		return baseRun(name, args...)
	}

	if err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: io.Discard}); err != nil {
		t.Fatalf("expected nil error on deps failure, got: %v", err)
	}

	for _, rel := range []string{"plugins/plugin.yaml", "plugins/requirements.txt"} {
		if _, err := os.Stat(filepath.Join(homeDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
	if _, err := os.Stat(pluginsTestInterpreterPath(homeDir)); err != nil {
		t.Errorf("expected interpreter to exist: %v", err)
	}
}

// TestRunPluginsInit_DepsFailureWritesManualCommands 验证基础依赖安装失败时，
// 输出写入注入的 Out，并给出完整的手动安装指引。
func TestRunPluginsInit_DepsFailureWritesManualCommands(t *testing.T) {
	homeDir := t.TempDir()
	runner := newPluginsFakeRunner()
	baseRun := runner.run
	runner.run = func(name string, args ...string) ([]byte, error) {
		if pluginsArgsContain(args, "-m", "pip", "install") {
			return []byte("Traceback (most recent call last):\nModuleNotFoundError: No module named pip\n"), fmt.Errorf("exit status 1")
		}
		return baseRun(name, args...)
	}

	var out bytes.Buffer
	if err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: &out}); err != nil {
		t.Fatalf("expected nil error on deps failure, got: %v", err)
	}

	got := out.String()
	pluginsDir := filepath.Join(homeDir, "plugins")
	requirements := filepath.Join(pluginsDir, "requirements.txt")
	venvPip := plugin.VenvPipPath(pluginsDir)

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

func TestRunPluginsInit_PythonMissingReturnsError(t *testing.T) {
	homeDir := t.TempDir()
	runner := &pluginsFakeRunner{}

	err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: io.Discard})
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

func TestRunPluginsInit_BrokenVenvReturnsError(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "plugins", ".venv"), 0o755); err != nil {
		t.Fatalf("prepare broken venv failed: %v", err)
	}
	runner := &pluginsFakeRunner{}

	err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: io.Discard})
	if err == nil {
		t.Fatal("expected error for broken venv")
	}
	if !strings.Contains(err.Error(), "rm -rf") {
		t.Errorf("expected error to mention rm -rf, got: %v", err)
	}
}

func TestRunPluginsInit_UsesExplicitPython(t *testing.T) {
	homeDir := t.TempDir()
	runner := newPluginsFakeRunner()
	runner.paths = map[string]string{"/opt/py3": "/opt/py3"}

	if err := RunPluginsInit(homeDir, PluginsInitOptions{Python: "/opt/py3", Runner: runner, Out: io.Discard}); err != nil {
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
