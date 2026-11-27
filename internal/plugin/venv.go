package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// 共享虚拟环境要求的最低 Python 版本。
const (
	MinPythonMajor = 3
	MinPythonMinor = 9
)

// versionScript 让候选解释器把自身主次版本号打印为 major.minor。
const versionScript = "import sys; print('%d.%d' % sys.version_info[:2])"

// 外部命令超时：解释器挂住（损坏的 pyenv shim 等）时 groot plugins init 不能永远卡住。
const (
	pythonProbeTimeout = 30 * time.Second
	venvCreateTimeout  = 3 * time.Minute
)

// CommandRunner 抽象探测与创建虚拟环境时需要的命令操作，便于测试注入。
type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// VenvOptions 控制 EnsureVenv 的行为。GOOS 与 Runner 为空时分别取 runtime.GOOS
// 与真实命令执行。
type VenvOptions struct {
	Python string
	GOOS   string
	Runner CommandRunner
}

// VenvResult 描述共享虚拟环境的探测或创建结果。
type VenvResult struct {
	Interpreter string
	Version     string
	Created     bool
}

// DepsResult 描述基础依赖安装的结果；失败时 Tail 保留输出末尾若干行，便于提示。
type DepsResult struct {
	OK   bool
	Tail string
}

// InstallBaseDeps 在共享虚拟环境中安装 plugins 目录的基础依赖。安装失败不返回
// error，而是通过 DepsResult.Tail 提供输出末尾若干行供调用方展示。
func InstallBaseDeps(ctx context.Context, venvPython, requirementsPath string, runner CommandRunner) DepsResult {
	if runner == nil {
		runner = venvExecRunner{}
	}
	args := []string{"-m", "pip", "install", "--retries", "1", "--timeout", "10", "-r", requirementsPath}
	out, err := runner.Run(ctx, venvPython, args...)
	if err != nil {
		return DepsResult{OK: false, Tail: tailLines(string(out), 20)}
	}
	return DepsResult{OK: true}
}

// tailLines 返回 s 去掉尾部空行后的最后 n 行，行间以换行连接。CRLF 先归一化为 LF。
func tailLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if n < 0 {
		n = 0
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// venvExecRunner 是用真实进程执行命令的默认 CommandRunner。
type venvExecRunner struct{}

func (venvExecRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (venvExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (o VenvOptions) goos() string {
	if o.GOOS != "" {
		return o.GOOS
	}
	return runtime.GOOS
}

func (o VenvOptions) runner() CommandRunner {
	if o.Runner != nil {
		return o.Runner
	}
	return venvExecRunner{}
}

// pythonCandidate 是一个待探测的解释器候选；prefix 是调用该解释器时需前置的参数
// （Windows 的 py 启动器使用 -3 选择 Python 3）。
type pythonCandidate struct {
	name   string
	prefix []string
}

func (c pythonCandidate) label() string {
	if len(c.prefix) == 0 {
		return c.name
	}
	return c.name + " " + strings.Join(c.prefix, " ")
}

// interpreterPath 返回给定平台下共享虚拟环境中的 Python 解释器路径。
func interpreterPath(pluginsDir, goos string) string {
	if goos == "windows" {
		return filepath.Join(pluginsDir, ".venv", "Scripts", "python.exe")
	}
	return filepath.Join(pluginsDir, ".venv", "bin", "python")
}

// venvPipPath 返回给定平台下共享虚拟环境中的 pip 路径。
func venvPipPath(pluginsDir, goos string) string {
	if goos == "windows" {
		return filepath.Join(pluginsDir, ".venv", "Scripts", "pip.exe")
	}
	return filepath.Join(pluginsDir, ".venv", "bin", "pip")
}

// VenvPipPath 返回当前平台的共享虚拟环境 pip 路径。
func VenvPipPath(pluginsDir string) string {
	return venvPipPath(pluginsDir, runtime.GOOS)
}

// EnsureVenv 确保 pluginsDir 下存在可用的共享虚拟环境：已存在且解释器可用时直接
// 返回；否则按候选顺序找到满足最低版本的 Python 并创建 .venv。
func EnsureVenv(pluginsDir string, opts VenvOptions) (VenvResult, error) {
	goos := opts.goos()
	runner := opts.runner()
	interp := interpreterPath(pluginsDir, goos)
	venvDir := filepath.Join(pluginsDir, ".venv")

	info, statErr := os.Stat(interp)
	switch {
	case statErr == nil && !info.IsDir():
		version := ""
		if _, _, v, err := queryPythonVersion(runner, interp, nil); err == nil {
			version = v
		}
		return VenvResult{Interpreter: interp, Version: version, Created: false}, nil
	case statErr == nil:
		return VenvResult{}, venvBrokenError(goos, interp, venvDir, "解释器路径是目录")
	case !os.IsNotExist(statErr):
		return VenvResult{}, venvBrokenError(goos, interp, venvDir, fmt.Sprintf("无法访问解释器: %v", statErr))
	}

	if _, err := os.Stat(venvDir); err == nil {
		return VenvResult{}, venvBrokenError(goos, interp, venvDir, "未找到该文件")
	} else if !os.IsNotExist(err) {
		return VenvResult{}, venvBrokenError(goos, interp, venvDir, fmt.Sprintf("无法访问 %s: %v", venvDir, err))
	}

	python, prefix, version, err := resolvePython(goos, opts, runner)
	if err != nil {
		return VenvResult{}, err
	}

	args := append(append([]string{}, prefix...), "-m", "venv", venvDir)
	ctx, cancel := context.WithTimeout(context.Background(), venvCreateTimeout)
	defer cancel()
	out, runErr := runner.Run(ctx, python, args...)
	if runErr != nil {
		msg := fmt.Sprintf("创建虚拟环境失败: %s: %v", python, runErr)
		if output := strings.TrimSpace(string(out)); output != "" {
			msg += "\n" + output
		}
		if strings.Contains(string(out), "ensurepip") {
			msg += "\n提示：当前 Python 缺少 venv/ensurepip 模块，Ubuntu/Debian 可尝试: sudo apt install python3-venv"
			msg += fmt.Sprintf("\n如目录已部分创建，先执行 %s 再重试", removeDirCommand(goos, venvDir))
		}
		msg += fmt.Sprintf("\n手动创建命令: %s -m venv %s", python, venvDir)
		return VenvResult{}, errors.New(msg)
	}

	if created, statErr := os.Stat(interp); statErr != nil || created.IsDir() {
		return VenvResult{}, fmt.Errorf(
			"创建后未找到解释器: %s\n手动创建命令: %s -m venv %s\n如目录已部分创建，先执行 %s 再重试",
			interp, python, venvDir, removeDirCommand(goos, venvDir),
		)
	}

	return VenvResult{Interpreter: interp, Version: version, Created: true}, nil
}

// RemoveDirCommand 返回在目标平台上删除目录的命令行，用于错误提示。
func RemoveDirCommand(goos, dir string) string {
	return removeDirCommand(goos, dir)
}

func removeDirCommand(goos, dir string) string {
	if goos == "windows" {
		return "rmdir /s /q " + dir
	}
	return "rm -rf " + dir
}

// venvBrokenError 说明虚拟环境目录存在但解释器不可用，并提示手动清理方式。
func venvBrokenError(goos, interp, venvDir, detail string) error {
	return fmt.Errorf(
		"共享虚拟环境已存在但缺少解释器: %s（%s）；如需重建，请删除该虚拟环境: %s",
		interp, detail, removeDirCommand(goos, venvDir),
	)
}

// resolvePython 按候选顺序找到第一个满足最低版本要求的 Python，返回其可执行路径、
// 调用前缀与版本号。runner 由调用方解析一次后传入。
func resolvePython(goos string, opts VenvOptions, runner CommandRunner) (string, []string, string, error) {
	var candidates []pythonCandidate
	if opts.Python != "" {
		candidates = []pythonCandidate{{name: opts.Python}}
	} else {
		candidates = pythonCandidates(goos)
	}

	var failures []string
	for _, c := range candidates {
		path, err := runner.LookPath(c.name)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: 未找到（%v）", c.label(), err))
			continue
		}
		major, minor, version, err := queryPythonVersion(runner, path, c.prefix)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", c.label(), err))
			continue
		}
		if major < MinPythonMajor || (major == MinPythonMajor && minor < MinPythonMinor) {
			failures = append(failures, fmt.Sprintf(
				"%s: 版本 %s 低于最低要求 %d.%d", c.label(), version, MinPythonMajor, MinPythonMinor,
			))
			continue
		}
		return path, c.prefix, version, nil
	}

	return "", nil, "", pythonUnavailableError(goos, opts.Python != "", failures)
}

// pythonCandidates 返回给定平台的默认解释器候选，按优先级排列。
func pythonCandidates(goos string) []pythonCandidate {
	if goos == "windows" {
		return []pythonCandidate{
			{name: "py", prefix: []string{"-3"}},
			{name: "python3"},
			{name: "python"},
		}
	}
	return []pythonCandidate{
		{name: "python3"},
		{name: "python"},
	}
}

// pythonUnavailableError 汇总每个候选的失败原因。自动探测时按平台追加安装指引；
// 显式指定解释器（explicit）时只说明该解释器不可用及其原因，不追加安装指引或 --py 提示。
func pythonUnavailableError(goos string, explicit bool, failures []string) error {
	var b strings.Builder
	if explicit {
		b.WriteString("指定的 Python 解释器不可用")
		for _, f := range failures {
			fmt.Fprintf(&b, "\n  - %s", f)
		}
		return errors.New(b.String())
	}

	fmt.Fprintf(&b, "未找到可用的 Python 解释器（最低要求 %d.%d）：", MinPythonMajor, MinPythonMinor)
	for _, f := range failures {
		fmt.Fprintf(&b, "\n  - %s", f)
	}
	b.WriteString("\n")
	b.WriteString(pythonInstallHint(goos))
	b.WriteString("也可通过 --py <路径> 指定 Python 解释器")
	return errors.New(b.String())
}

// pythonInstallHint 返回按平台区分的 Python 安装指引。
func pythonInstallHint(goos string) string {
	switch goos {
	case "windows":
		return "请从 https://www.python.org/downloads/ 安装 Python 3.9 或更高版本，安装时勾选 “Add Python to PATH”；"
	case "linux":
		return "请安装 Python 3.9 或更高版本（Ubuntu/Debian: sudo apt install python3 python3-venv）；"
	default:
		return "请安装 Python 3.9 或更高版本（macOS: brew install python@3.12，或从 https://www.python.org/downloads/ 下载安装）；"
	}
}

// queryPythonVersion 执行解释器获取版本，同时返回解析出的主次版本号和原始
// major.minor 字符串。执行失败与输出无法解析分别给出可展示的原因。
func queryPythonVersion(runner CommandRunner, python string, prefix []string) (major, minor int, version string, err error) {
	args := append(append([]string{}, prefix...), "-c", versionScript)
	ctx, cancel := context.WithTimeout(context.Background(), pythonProbeTimeout)
	defer cancel()
	out, err := runner.Run(ctx, python, args...)
	if err != nil {
		return 0, 0, "", fmt.Errorf("无法获取版本（执行 %s 失败: %v）", python, err)
	}
	version = strings.TrimSpace(string(out))
	major, minor, err = parsePythonVersion(version)
	if err != nil {
		return 0, 0, "", err
	}
	return major, minor, version, nil
}

// parsePythonVersion 解析 major.minor 形式的版本号。
func parsePythonVersion(version string) (int, int, error) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("无法解析 Python 版本: %q", version)
	}
	major, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("无法解析 Python 版本: %q", version)
	}
	minor, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("无法解析 Python 版本: %q", version)
	}
	return major, minor, nil
}
