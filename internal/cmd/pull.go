package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/zfd81/groot/internal/repo"
	isync "github.com/zfd81/groot/internal/sync"
)

// PullFlags holds parsed flags for the pull command.
type PullFlags struct {
	Paths []string
	Yes   bool
}

// ParsePullFlags 解析 groot pull 子命令参数。
func ParsePullFlags(args []string) (*PullFlags, error) {
	flags := &PullFlags{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printPullHelp()
			os.Exit(0)
		case "-y", "--yes":
			flags.Yes = true
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
			flags.Paths = append(flags.Paths, arg)
		}
	}
	return flags, nil
}

func printPullHelp() {
	fmt.Println("用法: groot pull [path...] [-y]")
	fmt.Println()
	fmt.Println("将数据库的集群共享配置镜像拉取到本地 HOME。")
	fmt.Println("仅在 MySQL/PostgreSQL 模式下可用。")
	fmt.Println()
	fmt.Println("参数:")
	fmt.Println("  path...   要拉取的资源路径（可多个），省略时拉取全部白名单资源")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -y, --yes   跳过交互确认，直接执行")
	fmt.Println("  -h, --help  显示帮助")
}

// RunPull 执行 groot pull。r 为 ResourceRepo,由 main.go 注入。
func RunPull(flags *PullFlags, r repo.ResourceRepo) error {
	homeDir := GetDefaultHome()

	mgr := isync.NewSyncManager(homeDir, r)

	// 先清理 *.tmp 残留(上次 pull 中途崩溃可能留下),
	// 否则后续 Diff 会把它们当成 "本地多余文件" 错误地展示给用户。
	// CleanTmpResidue 是 best-effort,失败不阻塞 pull 继续。
	_ = mgr.CleanTmpResidue(flags.Paths)

	fmt.Println("Scanning differences...")
	diff, err := mgr.Diff(flags.Paths)
	if err != nil {
		return err
	}

	fmt.Print(isync.FormatDiff(diff, "pull"))
	if diff.IsEmpty() {
		return nil
	}

	if !flags.Yes {
		if !isync.ConfirmContinue(os.Stdin, os.Stdout) {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	if err := mgr.Pull(flags.Paths); err != nil {
		return fmt.Errorf("pull 失败: %w", err)
	}
	fmt.Println("Pull complete.")
	return nil
}
