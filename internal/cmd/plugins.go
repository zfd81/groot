package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zfd81/groot/internal/plugin"
)

// PluginsInitFlags holds the parsed flags for the "plugins init" command.
type PluginsInitFlags struct {
	Python string // --py: explicit Python interpreter path
}

// PluginsInitOptions controls RunPluginsInit.
type PluginsInitOptions struct {
	Python string
	Runner plugin.CommandRunner
	Out    io.Writer // 运行输出目标；为 nil 时写入 os.Stdout
}

// ParsePluginsInitFlags parses command line arguments for the "plugins init" command.
func ParsePluginsInitFlags(args []string) (*PluginsInitFlags, error) {
	flags := &PluginsInitFlags{}

	i := 0
	for i < len(args) {
		arg := args[i]

		switch arg {
		case "-h", "--help":
			PrintPluginsInitHelp()
			os.Exit(0)
		case "--py":
			if i+1 >= len(args) {
				return nil, errors.New("--py requires a value")
			}
			i++
			flags.Python = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
			return nil, fmt.Errorf("unexpected argument: %s", arg)
		}
		i++
	}

	return flags, nil
}

// PrintPluginsHelp prints the help message for the plugins command.
func PrintPluginsHelp() {
	fmt.Println("用法: groot plugins [子命令]")
	fmt.Println()
	fmt.Println("进入插件环境：不带子命令时在 {GROOT_HOME}/plugins 下打开已激活共享虚拟环境的交互式 shell，输入 exit 或 Ctrl+C 退出。")
	fmt.Println()
	fmt.Println("子命令:")
	fmt.Println("  init            初始化插件环境（目录布局、共享虚拟环境、基础依赖）")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -h, --help      显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot plugins                     # 进入插件环境（exit 或 Ctrl+C 退出）")
	fmt.Println("  groot plugins init                # 初始化环境")
	fmt.Println("  groot plugins init --py /usr/local/bin/python3.12")
}

// PrintPluginsInitHelp prints the help message for the "plugins init" command.
func PrintPluginsInitHelp() {
	fmt.Println("用法: groot plugins init [选项]")
	fmt.Println()
	fmt.Println("初始化 {GROOT_HOME}/plugins：创建固定目录布局、共享虚拟环境 .venv 并安装基础依赖")
	fmt.Println()
	fmt.Println("Python 要求 3.9+；基础依赖安装失败只提示手动安装，不视为初始化失败")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  --py <路径>    指定 Python 解释器（默认自动探测 python3/python）")
	fmt.Println("  -h, --help     显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot plugins init")
	fmt.Println("  groot plugins init --py /usr/local/bin/python3.12")
}

// RunPluginsInit prepares {GROOT_HOME}/plugins to host Python plugins:
// it creates the fixed layout, the shared virtual environment and installs base
// dependencies. It is idempotent and never overwrites existing user content.
func RunPluginsInit(homeDir string, opts PluginsInitOptions) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	fmt.Fprintln(out, "初始化插件环境...")
	fmt.Fprintln(out)

	homeCreated := false
	if _, err := os.Stat(homeDir); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("检查工作目录 %s 失败: %w", homeDir, err)
		}
		// 不调用 createDir：它直接写 stdout，这里需要写入可注入的 out。
		if err := os.MkdirAll(homeDir, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", homeDir, err)
		}
		fmt.Fprintf(out, "工作目录 %s 创建成功\n", shortenPath(homeDir, true))
		homeCreated = true
	}

	created, err := plugin.EnsureLayout(homeDir)
	if err != nil {
		return fmt.Errorf("创建插件目录布局失败: %w", err)
	}
	if len(created) == 0 {
		fmt.Fprintln(out, "插件目录布局已存在，跳过创建")
	} else {
		for _, p := range created {
			fmt.Fprintf(out, "插件 %s 创建成功\n", shortenPath(p, false))
		}
	}

	pluginsDir := plugin.PluginsDir(homeDir)
	venv, err := plugin.EnsureVenv(pluginsDir, plugin.VenvOptions{Python: opts.Python, Runner: opts.Runner})
	if err != nil {
		return err
	}
	venvDir := filepath.Join(pluginsDir, ".venv")
	if venv.Created {
		fmt.Fprintf(out, "共享虚拟环境 %s 创建成功\n", shortenPath(venvDir, true))
	} else {
		fmt.Fprintln(out, "共享虚拟环境 .venv 已存在，跳过创建")
	}

	requirements := filepath.Join(pluginsDir, "requirements.txt")
	venvPip := plugin.VenvPipPath(pluginsDir)

	fmt.Fprintln(out, "正在安装基础依赖（第一次可能需要几分钟）...")
	deps := plugin.InstallBaseDeps(context.Background(), venv.Interpreter, requirements, opts.Runner)
	if deps.OK {
		fmt.Fprintln(out, "基础依赖安装完成")
	} else {
		fmt.Fprintln(out, "基础依赖未安装成功，请手动安装")
		if deps.Tail != "" {
			fmt.Fprintln(out, deps.Tail)
		}
		fmt.Fprintf(out, "%s install -r %s\n", venvPip, requirements)
		fmt.Fprintf(out, "%s install --no-index --find-links <wheel 目录> -r %s\n", venvPip, requirements)
		if strings.Contains(deps.Tail, "No module named pip") {
			fmt.Fprintln(out, "缺少 pip 时可执行 apt install python3-venv（Debian/Ubuntu）")
		}
	}

	if venv.Version != "" {
		fmt.Fprintf(out, "解释器: %s (%s)\n", venv.Interpreter, venv.Version)
	} else {
		fmt.Fprintf(out, "解释器: %s\n", venv.Interpreter)
	}

	if homeCreated {
		fmt.Fprintln(out, "工作目录其余内容由 groot init 准备（skills/mcp/subagents/logs、bootstrap.yaml、GROOT.md）")
	}

	fmt.Fprintln(out)
	if deps.OK {
		fmt.Fprintln(out, "初始化完成")
	} else {
		fmt.Fprintln(out, "初始化完成（基础依赖需手动安装）")
	}

	return nil
}
