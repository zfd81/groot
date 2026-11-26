package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// servicesCountEntry 返回 env 中等于 want 的条目数。
func servicesCountEntry(env []string, want string) int {
	n := 0
	for _, e := range env {
		if e == want {
			n++
		}
	}
	return n
}

func TestServicesShellEnv_SetsVirtualEnvAndPrependsPath(t *testing.T) {
	parent := []string{"PATH=/usr/bin:/bin", "HOME=/u", "VIRTUAL_ENV=/old/venv"}

	env := servicesShellEnv(parent, "/g/services/.venv", "/g/services/.venv/bin", "linux")

	if got := servicesCountEntry(env, "VIRTUAL_ENV=/g/services/.venv"); got != 1 {
		t.Errorf("VIRTUAL_ENV 条目数 = %d, want 1; env=%v", got, env)
	}
	if got := servicesCountEntry(env, "PATH=/g/services/.venv/bin:/usr/bin:/bin"); got != 1 {
		t.Errorf("PATH 条目数 = %d, want 1; env=%v", got, env)
	}
	if got := servicesCountEntry(env, "HOME=/u"); got != 1 {
		t.Errorf("HOME 条目数 = %d, want 1; env=%v", got, env)
	}
	if got := servicesCountEntry(env, "VIRTUAL_ENV=/old/venv"); got != 0 {
		t.Errorf("旧 VIRTUAL_ENV 条目数 = %d, want 0; env=%v", got, env)
	}
}

func TestServicesShellEnv_MissingPath(t *testing.T) {
	parent := []string{"HOME=/u"}

	env := servicesShellEnv(parent, "/g/services/.venv", "/g/services/.venv/bin", "linux")

	if got := servicesCountEntry(env, "PATH=/g/services/.venv/bin"); got != 1 {
		t.Errorf("PATH 条目数 = %d, want 1; env=%v", got, env)
	}
}

func TestServicesShellEnv_WindowsSeparatorAndCase(t *testing.T) {
	parent := []string{"Path=C:\\Win", "VIRTUAL_ENV=old"}

	env := servicesShellEnv(parent, `C:\g\services\.venv`, `C:\g\services\.venv\Scripts`, "windows")

	if got := servicesCountEntry(env, `PATH=C:\g\services\.venv\Scripts;C:\Win`); got != 1 {
		t.Errorf("PATH 条目数 = %d, want 1; env=%v", got, env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "Path=") {
			t.Errorf("不应出现 Path= 条目: %q", e)
		}
	}
	if got := servicesCountEntry(env, `VIRTUAL_ENV=C:\g\services\.venv`); got != 1 {
		t.Errorf("VIRTUAL_ENV 条目数 = %d, want 1; env=%v", got, env)
	}
}

func TestServicesShellEnv_PreservesEntryWithoutEquals(t *testing.T) {
	parent := []string{"WEIRD", "PATH=/usr/bin"}

	env := servicesShellEnv(parent, "/g/services/.venv", "/g/services/.venv/bin", "linux")

	if got := servicesCountEntry(env, "WEIRD"); got != 1 {
		t.Errorf("WEIRD 条目数 = %d, want 1; env=%v", got, env)
	}
}

func TestServicesShell_Unix(t *testing.T) {
	name := servicesShell("linux", []string{"SHELL=/bin/zsh"})
	if name != "/bin/zsh" {
		t.Errorf("name = %q, want %q", name, "/bin/zsh")
	}

	name = servicesShell("linux", nil)
	if name != "/bin/sh" {
		t.Errorf("无 SHELL 时 name = %q, want %q", name, "/bin/sh")
	}
}

func TestServicesShell_Windows(t *testing.T) {
	name := servicesShell("windows", []string{`COMSPEC=C:\Windows\system32\cmd.exe`})
	if name != `C:\Windows\system32\cmd.exe` {
		t.Errorf("name = %q, want %q", name, `C:\Windows\system32\cmd.exe`)
	}

	name = servicesShell("windows", nil)
	if name != "cmd.exe" {
		t.Errorf("无 COMSPEC 时 name = %q, want %q", name, "cmd.exe")
	}
}

func TestPromptYes(t *testing.T) {
	const prompt = "继续? [y/N] "

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "y", input: "y\n", want: true},
		{name: "Y", input: "Y\n", want: true},
		{name: "yes", input: "yes\n", want: true},
		{name: "带空格的大写 YES", input: " YES \n", want: true},
		{name: "n", input: "n\n", want: false},
		{name: "空行", input: "\n", want: false},
		{name: "空行后跟 n", input: "\nn\n", want: false},
		{name: "EOF", input: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			got := promptYes(strings.NewReader(tt.input), &out, prompt)
			if got != tt.want {
				t.Errorf("promptYes(%q) = %v, want %v", tt.input, got, tt.want)
			}
			if !strings.Contains(out.String(), prompt) {
				t.Errorf("输出 %q 未包含提示文本 %q", out.String(), prompt)
			}
		})
	}
}

// writeServicesInterpreter 在 home 下共享虚拟环境中写出可执行解释器文件。
func writeServicesInterpreter(t *testing.T, home string) {
	t.Helper()
	interp := servicesTestInterpreterPath(home)
	if err := os.MkdirAll(filepath.Dir(interp), 0o755); err != nil {
		t.Fatalf("创建解释器目录失败: %v", err)
	}
	if err := os.WriteFile(interp, nil, 0o755); err != nil {
		t.Fatalf("写入解释器失败: %v", err)
	}
}

func TestRunServicesShell_NonInteractiveReturnsError(t *testing.T) {
	home := t.TempDir()
	runCalled := false
	initCalled := false

	code, err := RunServicesShell(home, ServicesShellOptions{
		Interactive: func() bool { return false },
		Run: func(cmd ShellCommand) (int, error) {
			runCalled = true
			return 0, nil
		},
		Init: func(home string, out io.Writer) error {
			initCalled = true
			return nil
		},
	})

	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "交互终端") {
		t.Errorf("err = %q, want 含 交互终端", err.Error())
	}
	if !strings.Contains(err.Error(), "groot services init") {
		t.Errorf("err = %q, want 含 groot services init", err.Error())
	}
	if runCalled {
		t.Error("Run 不应被调用")
	}
	if initCalled {
		t.Error("Init 不应被调用")
	}
}

func TestRunServicesShell_EntersActivatedVenv(t *testing.T) {
	home := t.TempDir()
	writeServicesInterpreter(t, home)
	servicesDir := filepath.Join(home, "services")
	venvDir := filepath.Join(servicesDir, ".venv")
	venvBin := filepath.Dir(servicesTestInterpreterPath(home))

	in := strings.NewReader("")
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	var got ShellCommand
	runCalled := false

	code, err := RunServicesShell(home, ServicesShellOptions{
		In:          in,
		Out:         &outBuf,
		Err:         &errBuf,
		Env:         []string{"PATH=/usr/bin", "SHELL=/bin/zsh"},
		GOOS:        "linux",
		Interactive: func() bool { return true },
		Run: func(cmd ShellCommand) (int, error) {
			runCalled = true
			got = cmd
			return 0, nil
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !runCalled {
		t.Fatal("Run 应被调用")
	}
	if got.Name != "/bin/zsh" {
		t.Errorf("Name = %q, want %q", got.Name, "/bin/zsh")
	}
	if got.Dir != servicesDir {
		t.Errorf("Dir = %q, want %q", got.Dir, servicesDir)
	}
	if v := envValue(got.Env, envVirtualEnv, false); v != venvDir {
		t.Errorf("Env VIRTUAL_ENV = %q, want %q", v, venvDir)
	}
	if p := envValue(got.Env, envPath, false); !strings.HasPrefix(p, venvBin) {
		t.Errorf("Env PATH = %q, want 前缀 %q", p, venvBin)
	}
	if got.In != in {
		t.Errorf("In = %v, want 传入的 reader", got.In)
	}
	if got.Out != io.Writer(&outBuf) {
		t.Errorf("Out = %v, want 传入的 writer", got.Out)
	}
	if got.Err != io.Writer(&errBuf) {
		t.Errorf("Err = %v, want 传入的 writer", got.Err)
	}

	errOut := errBuf.String()
	for _, want := range []string{"已进入 Python 服务环境", venvDir, "工作目录", "Ctrl+C"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("Err 输出缺少 %q，实际:\n%s", want, errOut)
		}
	}
}

func TestRunServicesShell_PropagatesExitCode(t *testing.T) {
	home := t.TempDir()
	writeServicesInterpreter(t, home)

	code, err := RunServicesShell(home, ServicesShellOptions{
		In:          strings.NewReader(""),
		Out:         io.Discard,
		Err:         io.Discard,
		Env:         []string{"SHELL=/bin/sh"},
		GOOS:        "linux",
		Interactive: func() bool { return true },
		Run: func(cmd ShellCommand) (int, error) {
			return 7, nil
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 7 {
		t.Errorf("code = %d, want 7", code)
	}
}

func TestRunServicesShell_RunFailureReturnsError(t *testing.T) {
	home := t.TempDir()
	writeServicesInterpreter(t, home)

	code, err := RunServicesShell(home, ServicesShellOptions{
		In:          strings.NewReader(""),
		Out:         io.Discard,
		Err:         io.Discard,
		Env:         []string{"SHELL=/bin/sh"},
		GOOS:        "linux",
		Interactive: func() bool { return true },
		Run: func(cmd ShellCommand) (int, error) {
			return 0, errors.New("boom")
		},
	})

	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want 含 boom", err)
	}
}

func TestRunServicesShell_MissingVenvDeclined(t *testing.T) {
	home := t.TempDir()
	var errBuf bytes.Buffer
	initCalled := false

	code, err := RunServicesShell(home, ServicesShellOptions{
		In:          strings.NewReader("n\n"),
		Out:         io.Discard,
		Err:         &errBuf,
		Env:         []string{"SHELL=/bin/sh"},
		GOOS:        "linux",
		Interactive: func() bool { return true },
		Run:         func(cmd ShellCommand) (int, error) { return 0, nil },
		Init: func(home string, out io.Writer) error {
			initCalled = true
			return nil
		},
	})

	if initCalled {
		t.Error("Init 不应被调用")
	}
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if err == nil || !strings.Contains(err.Error(), "groot services init") {
		t.Errorf("err = %v, want 含 groot services init", err)
	}
	if got := errBuf.String(); !strings.Contains(got, "共享虚拟环境不存在") || !strings.Contains(got, "[y/N]") {
		t.Errorf("Err 输出 = %q, want 含 共享虚拟环境不存在 与 [y/N]", got)
	}
}

func TestRunServicesShell_MissingVenvInitialized(t *testing.T) {
	home := t.TempDir()
	var outBuf bytes.Buffer
	initCalls := 0
	gotHome := ""
	gotWriter := false
	runCalled := false

	code, err := RunServicesShell(home, ServicesShellOptions{
		In:          strings.NewReader("y\n"),
		Out:         &outBuf,
		Err:         io.Discard,
		Env:         []string{"SHELL=/bin/sh"},
		GOOS:        "linux",
		Interactive: func() bool { return true },
		Run: func(cmd ShellCommand) (int, error) {
			runCalled = true
			return 0, nil
		},
		Init: func(h string, out io.Writer) error {
			initCalls++
			gotHome = h
			gotWriter = out == io.Writer(&outBuf)
			writeServicesInterpreter(t, h)
			return nil
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if initCalls != 1 {
		t.Errorf("Init 调用次数 = %d, want 1", initCalls)
	}
	if gotHome != home {
		t.Errorf("Init home = %q, want %q", gotHome, home)
	}
	if !gotWriter {
		t.Error("Init 未收到 Out writer")
	}
	if !runCalled {
		t.Error("Run 应在初始化后被调用")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
}

func TestRunServicesShell_InitFailureReturnsError(t *testing.T) {
	home := t.TempDir()
	runCalled := false

	code, err := RunServicesShell(home, ServicesShellOptions{
		In:          strings.NewReader("y\n"),
		Out:         io.Discard,
		Err:         io.Discard,
		Env:         []string{"SHELL=/bin/sh"},
		GOOS:        "linux",
		Interactive: func() bool { return true },
		Run: func(cmd ShellCommand) (int, error) {
			runCalled = true
			return 0, nil
		},
		Init: func(home string, out io.Writer) error {
			return errors.New("初始化失败，请 rm -rf 后重试")
		},
	})

	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if err == nil || !strings.Contains(err.Error(), "rm -rf") {
		t.Errorf("err = %v, want 含 rm -rf", err)
	}
	if runCalled {
		t.Error("Run 不应被调用")
	}
}

func TestRunServicesShell_BrokenVenvInterpreterIsDir(t *testing.T) {
	home := t.TempDir()
	interp := servicesTestInterpreterPath(home)
	if err := os.MkdirAll(interp, 0o755); err != nil {
		t.Fatalf("准备目录解释器失败: %v", err)
	}
	initCalled := false

	code, err := RunServicesShell(home, ServicesShellOptions{
		In:          strings.NewReader(""),
		Out:         io.Discard,
		Err:         io.Discard,
		Env:         []string{"SHELL=/bin/sh"},
		GOOS:        "linux",
		Interactive: func() bool { return true },
		Run:         func(cmd ShellCommand) (int, error) { return 0, nil },
		Init: func(string, io.Writer) error {
			initCalled = true
			return nil
		},
	})

	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if err == nil || !strings.Contains(err.Error(), "缺少解释器") || !strings.Contains(err.Error(), "rm -rf") {
		t.Errorf("err = %v, want 含 缺少解释器 与 rm -rf", err)
	}
	if initCalled {
		t.Error("Init 不应被调用")
	}
}

func TestRunServicesShell_InitSucceedsButInterpreterStillMissing(t *testing.T) {
	home := t.TempDir()
	initCalls := 0
	runCalled := false

	code, err := RunServicesShell(home, ServicesShellOptions{
		In:          strings.NewReader("y\n"),
		Out:         io.Discard,
		Err:         io.Discard,
		Env:         []string{"SHELL=/bin/sh"},
		GOOS:        "linux",
		Interactive: func() bool { return true },
		Run: func(cmd ShellCommand) (int, error) {
			runCalled = true
			return 0, nil
		},
		Init: func(home string, out io.Writer) error {
			initCalls++
			return nil
		},
	})

	if initCalls != 1 {
		t.Errorf("Init 调用次数 = %d, want 1", initCalls)
	}
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if err == nil || !strings.Contains(err.Error(), "初始化后仍未找到共享解释器") {
		t.Errorf("err = %v, want 含 初始化后仍未找到共享解释器", err)
	}
	if runCalled {
		t.Error("Run 不应被调用")
	}
}

func readServicesTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(data)
}

// servicesTestVenv 创建含 bin/activate 的假虚拟环境，返回虚拟环境目录。
func servicesTestVenv(t *testing.T) string {
	t.Helper()
	venvDir := filepath.Join(t.TempDir(), ".venv")
	if err := os.MkdirAll(filepath.Join(venvDir, "bin"), 0o755); err != nil {
		t.Fatalf("创建假虚拟环境失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(venvDir, "bin", "activate"), nil, 0o644); err != nil {
		t.Fatalf("写入假 activate 失败: %v", err)
	}
	return venvDir
}

func TestServicesShellLaunch_ZshShim(t *testing.T) {
	venvDir := servicesTestVenv(t)
	activate := filepath.Join(venvDir, "bin", "activate")
	env := []string{"HOME=/home/u", "SHELL=/bin/zsh"}

	launch := servicesShellLaunch("linux", env, venvDir)

	if launch.Name != "/bin/zsh" {
		t.Errorf("Name = %q, want %q", launch.Name, "/bin/zsh")
	}
	if len(launch.Args) != 0 {
		t.Errorf("Args = %v, want empty", launch.Args)
	}
	if launch.Cleanup == nil {
		t.Fatal("zsh 垫片应提供 Cleanup")
	}
	tmp := envValue(launch.Env, "ZDOTDIR", false)
	if tmp == "" {
		t.Fatalf("Env 缺少 ZDOTDIR: %v", launch.Env)
	}
	if got := servicesCountEntry(launch.Env, "ZDOTDIR="+tmp); got != 1 {
		t.Errorf("ZDOTDIR 条目数 = %d, want 1; env=%v", got, launch.Env)
	}
	if info, err := os.Stat(tmp); err != nil || !info.IsDir() {
		t.Fatalf("ZDOTDIR %q 不是目录: %v", tmp, err)
	}
	if home := envValue(launch.Env, "HOME", false); home != "/home/u" {
		t.Errorf("HOME = %q, want %q", home, "/home/u")
	}

	rc := readServicesTestFile(t, filepath.Join(tmp, ".zshrc"))
	if !strings.Contains(rc, "/home/u/.zshrc") {
		t.Errorf(".zshrc 垫片未加载用户配置:\n%s", rc)
	}
	// 用户原本没有设置 ZDOTDIR：垫片应恢复为「未设置」，而不是固化成 HOME。
	if !strings.Contains(rc, "unset ZDOTDIR") {
		t.Errorf(".zshrc 垫片应 unset ZDOTDIR:\n%s", rc)
	}
	if strings.Contains(rc, "ZDOTDIR='/home/u'") {
		t.Errorf(".zshrc 垫片把未设置的 ZDOTDIR 固化为 HOME:\n%s", rc)
	}
	if !strings.Contains(rc, activate) {
		t.Errorf(".zshrc 垫片未激活共享虚拟环境:\n%s", rc)
	}
	if !strings.Contains(rc, "setopt IGNORE_EOF") {
		t.Errorf(".zshrc 垫片应忽略 Ctrl+D（setopt IGNORE_EOF）:\n%s", rc)
	}
	if !strings.Contains(rc, "setopt MONITOR") {
		t.Errorf(".zshrc 垫片应启用作业控制（setopt MONITOR），避免运行命令时 Ctrl+C 退出 shell:\n%s", rc)
	}
	if !strings.Contains(rc, "TRAPINT() { (( __groot_running )) || exit 130 }") {
		t.Errorf(".zshrc 垫片应让提示符处的 Ctrl+C 退出（exit 130），运行命令时不退出:\n%s", rc)
	}
	if !strings.Contains(rc, "add-zsh-hook preexec __groot_preexec") ||
		!strings.Contains(rc, "add-zsh-hook precmd __groot_precmd") {
		t.Errorf(".zshrc 垫片应通过 preexec/precmd 钩子记录是否正在运行命令:\n%s", rc)
	}
	if got, want := strings.Index(rc, "TRAPINT()"), strings.Index(rc, "/home/u/.zshrc"); got < want {
		t.Errorf("退出设置应在用户配置之后生效，TRAPINT 位置 %d，用户配置位置 %d:\n%s", got, want, rc)
	}
	zshenv := readServicesTestFile(t, filepath.Join(tmp, ".zshenv"))
	if !strings.Contains(zshenv, "/home/u/.zshenv") {
		t.Errorf(".zshenv 垫片未加载用户配置:\n%s", zshenv)
	}

	launch.Cleanup()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("Cleanup 后 %q 仍存在: %v", tmp, err)
	}
}

func TestServicesShellLaunch_ZshShimUsesOriginalZDOTDIR(t *testing.T) {
	venvDir := servicesTestVenv(t)
	env := []string{"HOME=/home/u", "ZDOTDIR=/home/u/.config/zsh", "SHELL=/bin/zsh"}

	launch := servicesShellLaunch("linux", env, venvDir)
	defer launch.Cleanup()

	tmp := envValue(launch.Env, "ZDOTDIR", false)
	rc := readServicesTestFile(t, filepath.Join(tmp, ".zshrc"))
	if !strings.Contains(rc, "ZDOTDIR='/home/u/.config/zsh'") {
		t.Errorf(".zshrc 垫片未还原用户 ZDOTDIR:\n%s", rc)
	}
	if !strings.Contains(rc, "/home/u/.config/zsh/.zshrc") {
		t.Errorf(".zshrc 垫片未加载用户 ZDOTDIR 下的配置:\n%s", rc)
	}
}

func TestServicesShellLaunch_BashRcfile(t *testing.T) {
	venvDir := servicesTestVenv(t)
	activate := filepath.Join(venvDir, "bin", "activate")
	env := []string{"HOME=/home/u", "SHELL=/bin/bash"}

	launch := servicesShellLaunch("linux", env, venvDir)

	if launch.Name != "/bin/bash" {
		t.Errorf("Name = %q, want %q", launch.Name, "/bin/bash")
	}
	if len(launch.Args) != 2 || launch.Args[0] != "--rcfile" {
		t.Fatalf("Args = %v, want [--rcfile <file>]", launch.Args)
	}
	if launch.Cleanup == nil {
		t.Fatal("bash 垫片应提供 Cleanup")
	}
	rcPath := launch.Args[1]
	rc := readServicesTestFile(t, rcPath)
	homeIdx := strings.Index(rc, "/home/u/.bashrc")
	activateIdx := strings.Index(rc, activate)
	if homeIdx < 0 {
		t.Errorf(".bashrc 垫片未加载用户配置:\n%s", rc)
	}
	if activateIdx < 0 {
		t.Errorf(".bashrc 垫片未激活共享虚拟环境:\n%s", rc)
	}
	if homeIdx >= 0 && activateIdx >= 0 && homeIdx > activateIdx {
		t.Errorf("共享虚拟环境应在用户配置之后激活:\n%s", rc)
	}
	if !strings.Contains(rc, "set -o ignoreeof") {
		t.Errorf(".bashrc 垫片应忽略 Ctrl+D（set -o ignoreeof）:\n%s", rc)
	}
	if !strings.Contains(rc, "set -m") {
		t.Errorf(".bashrc 垫片应启用作业控制（set -m），避免运行命令时 Ctrl+C 退出 shell:\n%s", rc)
	}
	if !strings.Contains(rc, "trap 'exit 130' INT") {
		t.Errorf(".bashrc 垫片应让提示符处的 Ctrl+C 退出（exit 130）:\n%s", rc)
	}
	if got := envValue(launch.Env, "HOME", false); got != "/home/u" {
		t.Errorf("HOME = %q, want %q", got, "/home/u")
	}

	launch.Cleanup()
	if _, err := os.Stat(filepath.Dir(rcPath)); !os.IsNotExist(err) {
		t.Errorf("Cleanup 后 %q 仍存在: %v", filepath.Dir(rcPath), err)
	}
}

func TestServicesShellLaunch_OtherShellEnvOnly(t *testing.T) {
	venvDir := servicesTestVenv(t)
	env := []string{"HOME=/home/u", "SHELL=/bin/sh"}

	launch := servicesShellLaunch("linux", env, venvDir)

	if launch.Name != "/bin/sh" {
		t.Errorf("Name = %q, want %q", launch.Name, "/bin/sh")
	}
	if len(launch.Args) != 0 {
		t.Errorf("Args = %v, want empty", launch.Args)
	}
	if !slices.Equal(launch.Env, env) {
		t.Errorf("Env = %v, want 原样 %v", launch.Env, env)
	}
	if launch.Cleanup != nil {
		t.Error("其它 shell 不应提供 Cleanup")
	}
}

func TestServicesShellLaunch_WindowsEnvOnly(t *testing.T) {
	venvDir := servicesTestVenv(t)
	env := []string{`COMSPEC=C:\Windows\system32\cmd.exe`}

	launch := servicesShellLaunch("windows", env, venvDir)

	if launch.Name != `C:\Windows\system32\cmd.exe` {
		t.Errorf("Name = %q", launch.Name)
	}
	if len(launch.Args) != 0 {
		t.Errorf("Args = %v, want empty", launch.Args)
	}
	if !slices.Equal(launch.Env, env) {
		t.Errorf("Env = %v, want 原样 %v", launch.Env, env)
	}
	if launch.Cleanup != nil {
		t.Error("Windows 不应提供 Cleanup")
	}
}

func TestServicesShellLaunch_QuotesSingleQuoteInPath(t *testing.T) {
	venvDir := servicesTestVenv(t)
	env := []string{"HOME=/home/u'x", "SHELL=/bin/zsh"}

	launch := servicesShellLaunch("linux", env, venvDir)
	defer launch.Cleanup()

	tmp := envValue(launch.Env, "ZDOTDIR", false)
	rc := readServicesTestFile(t, filepath.Join(tmp, ".zshrc"))
	if !strings.Contains(rc, `'\''`) {
		t.Errorf(".zshrc 垫片未转义路径中的单引号:\n%s", rc)
	}
}

func TestRunShellCommand_SignalExitReturnsOne(t *testing.T) {
	code, err := runShellCommand(ShellCommand{
		Name: "/bin/sh",
		Args: []string{"-c", "kill -TERM $$"},
		Out:  io.Discard,
		Err:  io.Discard,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}
