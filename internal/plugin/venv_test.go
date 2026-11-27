package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// venvFakeRunner 是测试用的命令执行器：按需返回 LookPath 结果并记录所有调用。
type venvFakeRunner struct {
	paths   map[string]string
	calls   []string
	lookups []string
	// ctxHasDeadline 记录每次 Run 收到的 ctx 是否带超时。
	ctxHasDeadline []bool
	run            func(name string, args ...string) ([]byte, error)
}

func (f *venvFakeRunner) LookPath(file string) (string, error) {
	f.lookups = append(f.lookups, file)
	if p, ok := f.paths[file]; ok {
		return p, nil
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", file)
}

func (f *venvFakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	_, hasDeadline := ctx.Deadline()
	f.ctxHasDeadline = append(f.ctxHasDeadline, hasDeadline)
	if f.run != nil {
		return f.run(name, args...)
	}
	return nil, nil
}

// hasArg 判断参数列表中是否包含给定参数。
func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// venvCommandCalls 返回所有包含 -m venv 的调用记录。
func venvCommandCalls(calls []string) []string {
	var out []string
	for _, c := range calls {
		if strings.Contains(c, "-m venv") {
			out = append(out, c)
		}
	}
	return out
}

// writeInterpreter 在 path 处创建可执行文件（自动建父目录）。
func writeInterpreter(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(""), 0o755); err != nil {
		t.Fatalf("写入解释器失败: %v", err)
	}
}

func TestEnsureVenv_ExistingVenv(t *testing.T) {
	pluginsDir := t.TempDir()
	interp := InterpreterPath(pluginsDir)
	writeInterpreter(t, interp)

	runner := &venvFakeRunner{run: func(name string, args ...string) ([]byte, error) {
		return []byte("3.12\n"), nil
	}}

	res, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err != nil {
		t.Fatalf("EnsureVenv() error = %v, want nil", err)
	}
	if res.Created {
		t.Error("Created = true, want false")
	}
	if res.Interpreter != interp {
		t.Errorf("Interpreter = %q, want %q", res.Interpreter, interp)
	}
	if res.Version != "3.12" {
		t.Errorf("Version = %q, want %q", res.Version, "3.12")
	}
	if got := venvCommandCalls(runner.calls); len(got) != 0 {
		t.Errorf("已有虚拟环境时不应创建，venv 调用 = %v", got)
	}
}

func TestEnsureVenv_ExistingVenvVersionUnknown(t *testing.T) {
	pluginsDir := t.TempDir()
	writeInterpreter(t, InterpreterPath(pluginsDir))

	runner := &venvFakeRunner{run: func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("版本查询失败")
	}}

	res, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err != nil {
		t.Fatalf("EnsureVenv() error = %v, want nil", err)
	}
	if res.Created {
		t.Error("Created = true, want false")
	}
	if res.Version != "" {
		t.Errorf("Version = %q, want 空", res.Version)
	}
}

func TestEnsureVenv_Creates(t *testing.T) {
	pluginsDir := t.TempDir()
	interp := InterpreterPath(pluginsDir)

	runner := &venvFakeRunner{
		paths: map[string]string{"python3": "/usr/bin/python3"},
	}
	runner.run = func(name string, args ...string) ([]byte, error) {
		if hasArg(args, "-m") && hasArg(args, "venv") {
			writeInterpreter(t, interp)
			return nil, nil
		}
		return []byte("3.12"), nil
	}

	res, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err != nil {
		t.Fatalf("EnsureVenv() error = %v, want nil", err)
	}
	if !res.Created {
		t.Error("Created = false, want true")
	}
	if res.Interpreter != interp {
		t.Errorf("Interpreter = %q, want %q", res.Interpreter, interp)
	}
	if res.Version != "3.12" {
		t.Errorf("Version = %q, want %q", res.Version, "3.12")
	}
	if _, err := os.Stat(interp); err != nil {
		t.Errorf("解释器文件不存在: %v", err)
	}
	want := "/usr/bin/python3 -m venv " + filepath.Join(pluginsDir, ".venv")
	found := false
	for _, c := range runner.calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("calls = %v, want 含 %q", runner.calls, want)
	}
}

func TestEnsureVenv_BrokenVenv(t *testing.T) {
	pluginsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginsDir, ".venv"), 0o755); err != nil {
		t.Fatalf("创建 .venv 失败: %v", err)
	}

	runner := &venvFakeRunner{}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	msg := err.Error()
	if !strings.Contains(msg, "已存在") {
		t.Errorf("error = %q, want 含 %q", msg, "已存在")
	}
	if !strings.Contains(msg, "rm -rf") {
		t.Errorf("error = %q, want 含 %q", msg, "rm -rf")
	}
	if len(runner.lookups) != 0 {
		t.Errorf("虚拟环境残留时不应查找 Python，lookups = %v", runner.lookups)
	}
}

func TestEnsureVenv_InterpreterIsDirectory(t *testing.T) {
	pluginsDir := t.TempDir()
	interp := InterpreterPath(pluginsDir)
	if err := os.MkdirAll(interp, 0o755); err != nil {
		t.Fatalf("创建解释器目录失败: %v", err)
	}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: &venvFakeRunner{}})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	msg := err.Error()
	if !strings.Contains(msg, "已存在") {
		t.Errorf("error = %q, want 含 %q", msg, "已存在")
	}
	if !strings.Contains(msg, "rm -rf") {
		t.Errorf("error = %q, want 含 %q", msg, "rm -rf")
	}
}

func TestEnsureVenv_NoPython(t *testing.T) {
	pluginsDir := t.TempDir()

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: &venvFakeRunner{}})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	msg := err.Error()
	for _, want := range []string{"python3", "python", "3.9"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want 含 %q", msg, want)
		}
	}
	hinted := false
	for _, hint := range []string{"brew install", "python.org", "apt install"} {
		if strings.Contains(msg, hint) {
			hinted = true
		}
	}
	if !hinted {
		t.Errorf("error = %q, want 含安装指引", msg)
	}
}

func TestEnsureVenv_VersionTooLow(t *testing.T) {
	pluginsDir := t.TempDir()

	runner := &venvFakeRunner{
		paths: map[string]string{"python3": "/usr/bin/python3"},
		run: func(name string, args ...string) ([]byte, error) {
			return []byte("3.8"), nil
		},
	}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	msg := err.Error()
	if !strings.Contains(msg, "3.8") {
		t.Errorf("error = %q, want 含 %q", msg, "3.8")
	}
	if !strings.Contains(msg, "3.9") {
		t.Errorf("error = %q, want 含 %q", msg, "3.9")
	}
}

func TestEnsureVenv_FallbackToSecondCandidate(t *testing.T) {
	pluginsDir := t.TempDir()
	interp := InterpreterPath(pluginsDir)

	runner := &venvFakeRunner{
		paths: map[string]string{
			"python3": "/usr/bin/python3",
			"python":  "/usr/local/bin/python",
		},
	}
	runner.run = func(name string, args ...string) ([]byte, error) {
		if hasArg(args, "-m") && hasArg(args, "venv") {
			writeInterpreter(t, interp)
			return nil, nil
		}
		if name == "/usr/bin/python3" {
			return []byte("3.8"), nil
		}
		return []byte("3.11"), nil
	}

	res, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err != nil {
		t.Fatalf("EnsureVenv() error = %v, want nil", err)
	}
	if !res.Created {
		t.Error("Created = false, want true")
	}
	if res.Version != "3.11" {
		t.Errorf("Version = %q, want %q", res.Version, "3.11")
	}
	want := "/usr/local/bin/python -m venv " + filepath.Join(pluginsDir, ".venv")
	found := false
	for _, c := range runner.calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("calls = %v, want 含 %q", runner.calls, want)
	}
}

func TestEnsureVenv_ExplicitPythonNoFallback(t *testing.T) {
	pluginsDir := t.TempDir()
	runner := &venvFakeRunner{}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Python: "/opt/py3", Runner: runner})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/opt/py3") {
		t.Errorf("error = %q, want 含 %q", msg, "/opt/py3")
	}
	if !strings.Contains(msg, "指定的 Python 解释器不可用") {
		t.Errorf("error = %q, want 含 %q", msg, "指定的 Python 解释器不可用")
	}
	for _, forbidden := range []string{"也可通过 --py", "brew install", "apt install"} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("error = %q, want 不含 %q", msg, forbidden)
		}
	}
	for _, l := range runner.lookups {
		if l == "python3" || l == "python" {
			t.Errorf("指定 --py 后不应回退到默认候选，lookups = %v", runner.lookups)
		}
	}
}

func TestEnsureVenv_VersionQueryFails(t *testing.T) {
	pluginsDir := t.TempDir()

	// python3 可找到但查询版本失败；python 不存在，因此没有可用候选。
	runner := &venvFakeRunner{
		paths: map[string]string{"python3": "/usr/bin/python3"},
		run: func(name string, args ...string) ([]byte, error) {
			return []byte("boom"), errors.New("exit status 1")
		},
	}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	if !strings.Contains(err.Error(), "无法获取版本") {
		t.Errorf("error = %q, want 含 %q", err.Error(), "无法获取版本")
	}
}

func TestEnsureVenv_VersionUnparseable(t *testing.T) {
	pluginsDir := t.TempDir()

	runner := &venvFakeRunner{
		paths: map[string]string{"python3": "/usr/bin/python3"},
		run: func(name string, args ...string) ([]byte, error) {
			return []byte("abc"), nil
		},
	}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	if !strings.Contains(err.Error(), "无法解析 Python 版本") {
		t.Errorf("error = %q, want 含 %q", err.Error(), "无法解析 Python 版本")
	}
}

func TestEnsureVenv_VenvPathIsFile(t *testing.T) {
	pluginsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginsDir, ".venv"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("创建 .venv 文件失败: %v", err)
	}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: &venvFakeRunner{}})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	msg := err.Error()
	for _, want := range []string{"已存在", "rm -rf"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want 含 %q", msg, want)
		}
	}
}

func TestEnsureVenv_VenvCreationFails(t *testing.T) {
	pluginsDir := t.TempDir()

	runner := &venvFakeRunner{
		paths: map[string]string{"python3": "/usr/bin/python3"},
	}
	runner.run = func(name string, args ...string) ([]byte, error) {
		if hasArg(args, "-m") && hasArg(args, "venv") {
			return []byte("Error: ensurepip is not available\n"), errors.New("exit status 1")
		}
		return []byte("3.12"), nil
	}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Error: ensurepip is not available") {
		t.Errorf("error = %q, want 含命令输出", msg)
	}
	if !strings.Contains(msg, "apt install python3-venv") {
		t.Errorf("error = %q, want 含 %q", msg, "apt install python3-venv")
	}
	for _, want := range []string{"手动创建命令:", "-m venv", "rm -rf"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want 含 %q", msg, want)
		}
	}
}

func TestEnsureVenv_MissingInterpreterAfterCreate(t *testing.T) {
	pluginsDir := t.TempDir()

	runner := &venvFakeRunner{
		paths: map[string]string{"python3": "/usr/bin/python3"},
		run: func(name string, args ...string) ([]byte, error) {
			return []byte("3.12"), nil
		},
	}

	_, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	if !strings.Contains(err.Error(), "创建后未找到解释器") {
		t.Errorf("error = %q, want 含 %q", err.Error(), "创建后未找到解释器")
	}
	msg := err.Error()
	for _, want := range []string{"手动创建命令:", "-m venv", "rm -rf"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want 含 %q", msg, want)
		}
	}
}

func TestEnsureVenv_WindowsCandidates(t *testing.T) {
	pluginsDir := t.TempDir()
	interp := filepath.Join(pluginsDir, ".venv", "Scripts", "python.exe")

	runner := &venvFakeRunner{
		paths: map[string]string{"py": `C:\py.exe`},
	}
	runner.run = func(name string, args ...string) ([]byte, error) {
		if hasArg(args, "-m") && hasArg(args, "venv") {
			writeInterpreter(t, interp)
			return nil, nil
		}
		return []byte("3.12"), nil
	}

	res, err := EnsureVenv(pluginsDir, VenvOptions{GOOS: "windows", Runner: runner})
	if err != nil {
		t.Fatalf("EnsureVenv() error = %v, want nil", err)
	}
	if !res.Created {
		t.Error("Created = false, want true")
	}
	if res.Interpreter != interp {
		t.Errorf("Interpreter = %q, want %q", res.Interpreter, interp)
	}
	want := `C:\py.exe -3 -m venv ` + filepath.Join(pluginsDir, ".venv")
	found := false
	for _, c := range runner.calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("calls = %v, want 含 %q", runner.calls, want)
	}
}

func TestInstallBaseDeps_Success(t *testing.T) {
	runner := &venvFakeRunner{run: func(name string, args ...string) ([]byte, error) {
		return nil, nil
	}}

	res := InstallBaseDeps(context.Background(), "/venv/bin/python", "/plugins/requirements.txt", runner)
	if !res.OK {
		t.Error("OK = false, want true")
	}
	if res.Tail != "" {
		t.Errorf("Tail = %q, want 空", res.Tail)
	}
	want := "/venv/bin/python -m pip install --retries 1 --timeout 10 -r /plugins/requirements.txt"
	if len(runner.calls) != 1 || runner.calls[0] != want {
		t.Errorf("calls = %v, want 恰为 [%q]", runner.calls, want)
	}
}

func TestInstallBaseDeps_FailureKeepsTail(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	runner := &venvFakeRunner{run: func(name string, args ...string) ([]byte, error) {
		return []byte(b.String()), errors.New("exit status 1")
	}}

	res := InstallBaseDeps(context.Background(), "/venv/bin/python", "/plugins/requirements.txt", runner)
	if res.OK {
		t.Error("OK = true, want false")
	}
	if !strings.Contains(res.Tail, "line 6") {
		t.Errorf("Tail = %q, want 含 %q", res.Tail, "line 6")
	}
	if !strings.Contains(res.Tail, "line 25") {
		t.Errorf("Tail = %q, want 含 %q", res.Tail, "line 25")
	}
	if strings.Contains(res.Tail, "line 5") {
		t.Errorf("Tail = %q, want 不含 %q", res.Tail, "line 5")
	}
}

func TestInstallBaseDeps_EmptyOutput(t *testing.T) {
	runner := &venvFakeRunner{run: func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	}}

	res := InstallBaseDeps(context.Background(), "/venv/bin/python", "/plugins/requirements.txt", runner)
	if res.OK {
		t.Error("OK = true, want false")
	}
	if res.Tail != "" {
		t.Errorf("Tail = %q, want 空", res.Tail)
	}
}

func TestTailLines(t *testing.T) {
	const n = 20
	lines := func(count int) string {
		var b strings.Builder
		for i := 1; i <= count; i++ {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return b.String()
	}
	last := func(from, to int) string {
		var parts []string
		for i := from; i <= to; i++ {
			parts = append(parts, fmt.Sprintf("line %d", i))
		}
		return strings.Join(parts, "\n")
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空字符串", "", ""},
		{"0 行（仅换行）", "\n\n", ""},
		{"少于 n 行", lines(3), last(1, 3)},
		{"恰好 n 行", lines(n), last(1, n)},
		{"多于 n 行", lines(25), last(6, 25)},
		{"无尾部换行", "line 1\nline 2", "line 1\nline 2"},
		{"尾部多余换行", "line 1\nline 2\n\n", "line 1\nline 2"},
		{"CRLF 换行", "line 1\r\nline 2\r\n", "line 1\nline 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tailLines(tc.in, n); got != tc.want {
				t.Errorf("tailLines(%q, %d) = %q, want %q", tc.in, n, got, tc.want)
			}
		})
	}
}

func TestVenvPipPath(t *testing.T) {
	pluginsDir := t.TempDir()

	cases := []struct {
		goos string
		want string
	}{
		{"darwin", filepath.Join(pluginsDir, ".venv", "bin", "pip")},
		{"linux", filepath.Join(pluginsDir, ".venv", "bin", "pip")},
		{"windows", filepath.Join(pluginsDir, ".venv", "Scripts", "pip.exe")},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			if got := venvPipPath(pluginsDir, tc.goos); got != tc.want {
				t.Errorf("venvPipPath(%q) = %q, want %q", tc.goos, got, tc.want)
			}
		})
	}
}

func TestEnsureVenv_CommandsHaveTimeout(t *testing.T) {
	// 解释器挂住（损坏的 pyenv shim 等）时 groot plugins init 不能永远卡住：
	// 查版本与创建 venv 的每次外部命令都必须带超时。
	pluginsDir := t.TempDir()
	interp := InterpreterPath(pluginsDir)
	runner := &venvFakeRunner{paths: map[string]string{"python3": "/usr/bin/python3"}}
	runner.run = func(name string, args ...string) ([]byte, error) {
		if hasArg(args, "-m") && hasArg(args, "venv") {
			writeInterpreter(t, interp)
			return nil, nil
		}
		return []byte("3.12"), nil
	}

	if _, err := EnsureVenv(pluginsDir, VenvOptions{Runner: runner}); err != nil {
		t.Fatalf("EnsureVenv() error = %v", err)
	}
	if len(runner.ctxHasDeadline) == 0 {
		t.Fatal("未执行任何外部命令")
	}
	for i, ok := range runner.ctxHasDeadline {
		if !ok {
			t.Errorf("第 %d 次命令 %q 的 ctx 没有超时", i+1, runner.calls[i])
		}
	}
}

func TestEnsureVenv_BrokenVenvHintUsesWindowsCommand(t *testing.T) {
	// Windows 上没有 rm -rf，删除目录的提示要用 rmdir /s /q。
	pluginsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginsDir, ".venv"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := EnsureVenv(pluginsDir, VenvOptions{GOOS: "windows", Runner: &venvFakeRunner{}})
	if err == nil {
		t.Fatal("EnsureVenv() error = nil, want 非空")
	}
	msg := err.Error()
	if strings.Contains(msg, "rm -rf") {
		t.Errorf("Windows 提示不应含 rm -rf: %q", msg)
	}
	if !strings.Contains(msg, "rmdir /s /q") {
		t.Errorf("Windows 提示应含 rmdir /s /q: %q", msg)
	}
}

func TestRemoveDirCommand(t *testing.T) {
	if got := removeDirCommand("windows", `C:\x\.venv`); got != `rmdir /s /q C:\x\.venv` {
		t.Errorf("windows = %q", got)
	}
	if got := removeDirCommand("darwin", "/x/.venv"); got != "rm -rf /x/.venv" {
		t.Errorf("darwin = %q", got)
	}
}
