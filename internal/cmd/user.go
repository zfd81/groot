package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zfd81/groot/internal/repo"
	isync "github.com/zfd81/groot/internal/sync"
)

// UserFlags holds parsed flags for the user command.
type UserFlags struct {
	Sub string // 子操作，目前仅支持 reset
	Yes bool   // -y / --yes: 跳过确认
}

// ParseUserFlags 解析 groot user 子命令参数。
func ParseUserFlags(args []string) (*UserFlags, error) {
	flags := &UserFlags{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printUserHelp()
			os.Exit(0)
		case "-y", "--yes":
			flags.Yes = true
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
			if flags.Sub != "" {
				return nil, fmt.Errorf("unexpected argument: %s", arg)
			}
			flags.Sub = arg
		}
	}
	if flags.Sub == "" {
		printUserHelp()
		os.Exit(0)
	}
	if flags.Sub != "reset" {
		return nil, fmt.Errorf("unknown subcommand: user %s", flags.Sub)
	}
	return flags, nil
}

func printUserHelp() {
	fmt.Println("用法: groot user reset [-y]")
	fmt.Println()
	fmt.Println("重置 Web 登录用户：删除用户表中的全部数据。")
	fmt.Println("重置后再次访问 Web 界面将重新进入创建用户流程。")
	fmt.Println("注意：正在运行的服务需重启后（或原会话过期后）重置才对已登录会话生效。")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -y, --yes   跳过交互确认，直接执行")
	fmt.Println("  -h, --help  显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot user reset      # 交互确认后删除全部用户")
	fmt.Println("  groot user reset -y   # 跳过确认直接删除")
}

// RunUserReset 执行 groot user reset。users 由 main.go 注入，
// in/out 为交互确认的输入输出（便于测试注入）。
func RunUserReset(flags *UserFlags, users repo.UserRepo, in io.Reader, out io.Writer) error {
	ctx := context.Background()

	n, err := users.Count(ctx)
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}
	if n == 0 {
		fmt.Fprintln(out, "用户表为空，无需重置。")
		return nil
	}

	fmt.Fprintf(out, "警告: 将删除全部 %d 个用户，重置后 Web 界面需重新创建用户。\n", n)
	if !flags.Yes {
		if !isync.ConfirmContinue(in, out) {
			fmt.Fprintln(out, "已取消。")
			return nil
		}
	}

	deleted, err := users.DeleteAll(ctx)
	if err != nil {
		return fmt.Errorf("删除用户失败: %w", err)
	}
	fmt.Fprintf(out, "已删除 %d 个用户。如服务正在运行，请重启服务使已登录会话失效。\n", deleted)
	return nil
}
