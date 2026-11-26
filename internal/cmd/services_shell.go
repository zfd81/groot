package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zfd81/groot/internal/pysvc"
)

// 环境变量键名常量。
const (
	envVirtualEnv = "VIRTUAL_ENV"
	envPath       = "PATH"
)

// servicesShellEnv 构造启动交互式 shell 的环境变量：在 parent 基础上设置
// VIRTUAL_ENV，并把虚拟环境的 bin 目录前置到 PATH。parent 中已有的
// VIRTUAL_ENV/PATH 条目会被替换（windows 下键名比较不区分大小写），
// 不含 "=" 的条目原样保留。
func servicesShellEnv(parent []string, venvDir, venvBin, goos string) []string {
	windows := goos == "windows"
	sep := ":"
	if windows {
		sep = ";"
	}

	env := make([]string, 0, len(parent)+2)
	origPath := ""
	for _, entry := range parent {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			env = append(env, entry)
			continue
		}
		if keyMatches(key, envVirtualEnv, windows) {
			continue
		}
		if keyMatches(key, envPath, windows) {
			if origPath == "" {
				origPath = value
			}
			continue
		}
		env = append(env, entry)
	}

	env = append(env, envVirtualEnv+"="+venvDir)
	if origPath == "" {
		env = append(env, envPath+"="+venvBin)
	} else {
		env = append(env, envPath+"="+venvBin+sep+origPath)
	}
	return env
}

// keyMatches 报告环境变量键名 key 是否匹配 want；caseInsensitive 为 true 时
// 忽略大小写。
func keyMatches(key, want string, caseInsensitive bool) bool {
	if caseInsensitive {
		return strings.EqualFold(key, want)
	}
	return key == want
}

// servicesShell 返回指定平台上可用于交互式 shell 的程序名：
// windows 取 COMSPEC（回退 cmd.exe），其它平台取 SHELL（回退 /bin/sh）。
func servicesShell(goos string, env []string) string {
	if goos == "windows" {
		if name := envValue(env, "COMSPEC", true); name != "" {
			return name
		}
		return "cmd.exe"
	}
	if name := envValue(env, "SHELL", false); name != "" {
		return name
	}
	return "/bin/sh"
}

// shellLaunch 描述启动交互式 shell 的命令；Cleanup 在子进程结束后删除临时垫片。
type shellLaunch struct {
	Name    string
	Args    []string
	Env     []string
	Cleanup func()
}

// servicesShellLaunch 在 servicesShell 的基础上，为 Unix 的 zsh/bash 注入
// “先加载用户配置、再激活共享虚拟环境”的垫片；其它 shell 只做环境变量激活。
func servicesShellLaunch(goos string, env []string, venvDir string) shellLaunch {
	name := servicesShell(goos, env)
	fallback := shellLaunch{Name: name, Env: env}
	if goos == "windows" {
		return fallback
	}

	switch filepath.Base(name) {
	case "zsh":
		return zshShellLaunch(name, env, venvDir, fallback)
	case "bash":
		return bashShellLaunch(name, env, venvDir, fallback)
	default:
		return fallback
	}
}

// zshShellLaunch 用临时 ZDOTDIR 垫片启动 zsh：垫片先加载用户原配置，最后
// 重新激活共享虚拟环境，避免用户 rc 中激活的其它虚拟环境覆盖 VIRTUAL_ENV。
func zshShellLaunch(name string, env []string, venvDir string, fallback shellLaunch) shellLaunch {
	tmp, err := os.MkdirTemp("", "groot-shell-")
	if err != nil {
		return fallback
	}

	// 用户原本的 ZDOTDIR；为空表示未设置，此时 zsh 从 HOME 读配置，垫片要把它恢复为未设置。
	origZDotDir := envValue(env, "ZDOTDIR", false)
	configDir := origZDotDir
	if configDir == "" {
		configDir = envValue(env, "HOME", false)
	}
	files := map[string]string{
		".zshenv":   zshSourceFile(configDir, ".zshenv", venvDir),
		".zprofile": zshSourceFile(configDir, ".zprofile", venvDir),
		".zshrc":    zshRcFile(origZDotDir, configDir, venvDir),
	}
	for file, content := range files {
		if err := os.WriteFile(filepath.Join(tmp, file), []byte(content), 0o644); err != nil {
			os.RemoveAll(tmp)
			return fallback
		}
	}

	return shellLaunch{
		Name: name,
		Env:  setEnvValue(env, "ZDOTDIR", tmp),
		Cleanup: func() {
			os.RemoveAll(tmp)
		},
	}
}

// zshSourceFile 生成 zsh 垫片中的 .zshenv/.zprofile：先加载用户原文件（orig
// 非空时），最后激活共享虚拟环境。
func zshSourceFile(orig, file, venvDir string) string {
	var b strings.Builder
	if orig != "" {
		src := shellQuote(filepath.Join(orig, file))
		fmt.Fprintf(&b, "if [ -f %s ]; then . %s; fi\n", src, src)
	}
	b.WriteString(activateGuard(venvDir))
	return b.String()
}

// zshRcFile 生成 zsh 垫片的 .zshrc：先还原用户 ZDOTDIR（原本未设置则 unset），
// 再加载 configDir 下的用户 .zshrc，最后激活共享虚拟环境。
func zshRcFile(origZDotDir, configDir, venvDir string) string {
	var b strings.Builder
	if origZDotDir != "" {
		fmt.Fprintf(&b, "ZDOTDIR=%s\n", shellQuote(origZDotDir))
	} else {
		b.WriteString("unset ZDOTDIR\n")
	}
	if configDir != "" {
		src := shellQuote(filepath.Join(configDir, ".zshrc"))
		fmt.Fprintf(&b, "if [ -f %s ]; then . %s; fi\n", src, src)
	}
	b.WriteString(activateGuard(venvDir))
	// 退出方式在用户配置与激活之后设置，避免被用户 rc 覆盖：提示符处 Ctrl+C 退出，
	// 运行命令时 Ctrl+C 仍只中断该命令；Ctrl+D 不再退出。zsh 在前台命令被中断后
	// 会向自身重发 SIGINT，因此用 preexec/precmd 记录是否正在运行命令，只有处于
	// 提示符时才在 TRAPINT 中退出；add-zsh-hook 不会覆盖用户已有的钩子。
	b.WriteString("setopt MONITOR\n")
	b.WriteString("setopt IGNORE_EOF\n")
	b.WriteString("autoload -Uz add-zsh-hook\n")
	b.WriteString("__groot_running=0\n")
	b.WriteString("__groot_preexec() { __groot_running=1 }\n")
	b.WriteString("__groot_precmd() { __groot_running=0 }\n")
	b.WriteString("add-zsh-hook preexec __groot_preexec\n")
	b.WriteString("add-zsh-hook precmd __groot_precmd\n")
	b.WriteString("TRAPINT() { (( __groot_running )) || exit 130 }\n")
	return b.String()
}

// bashShellLaunch 用临时 --rcfile 垫片启动 bash：垫片先加载用户 ~/.bashrc，
// 最后重新激活共享虚拟环境。
func bashShellLaunch(name string, env []string, venvDir string, fallback shellLaunch) shellLaunch {
	tmp, err := os.MkdirTemp("", "groot-shell-")
	if err != nil {
		return fallback
	}

	var b strings.Builder
	if home := envValue(env, "HOME", false); home != "" {
		src := shellQuote(filepath.Join(home, ".bashrc"))
		fmt.Fprintf(&b, "if [ -f %s ]; then . %s; fi\n", src, src)
	}
	b.WriteString(activateGuard(venvDir))
	// 与 zsh 垫片一致：提示符处 Ctrl+C 退出，Ctrl+D 不再直接退出；作业控制保证
	// 运行命令时 Ctrl+C 不会触发 shell 的 INT trap。
	b.WriteString("set -m\n")
	b.WriteString("set -o ignoreeof\n")
	b.WriteString("trap 'exit 130' INT\n")

	rc := filepath.Join(tmp, ".bashrc")
	if err := os.WriteFile(rc, []byte(b.String()), 0o644); err != nil {
		os.RemoveAll(tmp)
		return fallback
	}

	return shellLaunch{
		Name: name,
		Args: []string{"--rcfile", rc},
		Env:  env,
		Cleanup: func() {
			os.RemoveAll(tmp)
		},
	}
}

// activateGuard 返回以 source 激活 venvDir 的 shell 语句；文件不存在时跳过。
func activateGuard(venvDir string) string {
	src := shellQuote(filepath.Join(venvDir, "bin", "activate"))
	return fmt.Sprintf("if [ -f %s ]; then . %s; fi\n", src, src)
}

// shellQuote 用单引号包裹 s 并转义内嵌的单引号，供 POSIX shell 使用。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// setEnvValue 返回把 key 替换为 value 后的环境副本；已有同名键（不区分大小写）
// 的条目会被移除。
func setEnvValue(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		k, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(k, key) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, key+"="+value)
}

// envValue 返回 env 中第一个键为 key 的条目的值；找不到时返回空字符串。
func envValue(env []string, key string, caseInsensitive bool) string {
	for _, entry := range env {
		k, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if keyMatches(k, key, caseInsensitive) {
			return value
		}
	}
	return ""
}

// promptYes 输出 prompt 并读取一行的确认回答：y/yes（不区分大小写）返回
// true，空行、读取失败或其它回答返回 false。提示文本由调用方传入，本函数不改写。
func promptYes(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)

	var answer string
	if _, err := fmt.Fscanln(in, &answer); err != nil {
		return false
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// ShellCommand 描述要启动的交互式 shell：程序名、参数、工作目录、环境变量与
// 三个标准流。由 RunServicesShell 组装，默认交给 runShellCommand 执行。
type ShellCommand struct {
	Name string
	Args []string
	Dir  string
	Env  []string
	In   io.Reader
	Out  io.Writer
	Err  io.Writer
}

// ServicesShellOptions 控制 RunServicesShell 的行为；零值使用默认实现
// （os.Stdin/os.Stdout/os.Stderr、os.Environ、runtime.GOOS、真实终端检测、
// exec 执行、RunServicesInit）。字段均为零值时也可注入，便于测试。
type ServicesShellOptions struct {
	In          io.Reader
	Out         io.Writer
	Err         io.Writer
	Env         []string
	GOOS        string
	Interactive func() bool
	Run         func(cmd ShellCommand) (int, error)
	Init        func(home string, out io.Writer) error
}

// RunServicesShell 进入已激活共享虚拟环境的交互式 shell。共享虚拟环境缺失时
// 询问是否调用 init，初始化后仍缺少解释器则报错。返回 shell 的退出码。
func RunServicesShell(home string, opts ServicesShellOptions) (int, error) {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = os.Stderr
	}
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	interactive := opts.Interactive
	if interactive == nil {
		interactive = interactiveTerminal
	}
	run := opts.Run
	if run == nil {
		run = runShellCommand
	}
	init := opts.Init
	if init == nil {
		init = func(home string, out io.Writer) error {
			return RunServicesInit(home, ServicesInitOptions{Out: out})
		}
	}

	if !interactive() {
		return 1, errors.New("groot services 需要在交互终端中运行；初始化环境请执行: groot services init")
	}

	servicesDir := pysvc.ServicesDir(home)
	venvDir := filepath.Join(servicesDir, ".venv")
	interp := pysvc.InterpreterPath(servicesDir)

	info, err := os.Stat(interp)
	switch {
	case err == nil && info.IsDir():
		return 1, fmt.Errorf("共享虚拟环境已存在但缺少解释器: %s；如需重建，请删除该虚拟环境: %s", interp, pysvc.RemoveDirCommand(runtime.GOOS, venvDir))
	case err == nil:
		// 解释器存在，继续。
	case os.IsNotExist(err):
		ok := promptYes(in, errOut, fmt.Sprintf("共享虚拟环境不存在: %s\n是否现在初始化？[y/N] ", venvDir))
		if !ok {
			return 1, errors.New("未初始化服务环境：请先执行 groot services init")
		}
		if err := init(home, out); err != nil {
			return 1, err
		}
		if info, err := os.Stat(interp); err != nil || info.IsDir() {
			return 1, fmt.Errorf("初始化后仍未找到共享解释器: %s（请运行 groot services init 查看详情）", interp)
		}
	default:
		return 1, fmt.Errorf("无法访问共享解释器 %s: %w", interp, err)
	}

	env = servicesShellEnv(env, venvDir, filepath.Dir(interp), goos)
	launch := servicesShellLaunch(goos, env, venvDir)
	if launch.Cleanup != nil {
		defer launch.Cleanup()
	}

	fmt.Fprintf(errOut, "Services环境: %s\n", shortenPath(venvDir, true))
	fmt.Fprintf(errOut, "工作目录: %s（输入 exit 或 Ctrl+C 退出）\n", shortenPath(servicesDir, true))

	code, runErr := run(ShellCommand{
		Name: launch.Name,
		Args: launch.Args,
		Dir:  servicesDir,
		Env:  launch.Env,
		In:   in,
		Out:  out,
		Err:  errOut,
	})
	if runErr != nil {
		return 1, runErr
	}
	return code, nil
}

// runShellCommand 是 RunServicesShell 的默认执行实现：启动子进程并转发标准流。
// 子进程非零退出返回其退出码且不返回错误；被信号终止返回 1；其它错误按 1 返回。
func runShellCommand(cmd ShellCommand) (int, error) {
	c := exec.Command(cmd.Name, cmd.Args...)
	c.Dir = cmd.Dir
	c.Env = cmd.Env
	c.Stdin = cmd.In
	c.Stdout = cmd.Out
	c.Stderr = cmd.Err

	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code := ee.ExitCode()
			if code < 0 {
				// 被信号终止时 ExitCode 为负数，按约定返回 1。
				code = 1
			}
			return code, nil
		}
		return 1, err
	}
	return 0, nil
}

// interactiveTerminal 报告 stdin 与 stdout 是否都是字符设备（交互终端）。
func interactiveTerminal() bool {
	in, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	out, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return in.Mode()&os.ModeCharDevice != 0 && out.Mode()&os.ModeCharDevice != 0
}
