package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/storage"
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
	fmt.Println("将 MinIO 的集群共享配置镜像拉取到本地 HOME。")
	fmt.Println("仅在 minio 模式下可用。")
	fmt.Println()
	fmt.Println("参数:")
	fmt.Println("  path...   要拉取的资源路径（可多个），省略时拉取全部白名单资源")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -y, --yes   跳过交互确认，直接执行")
	fmt.Println("  -h, --help  显示帮助")
}

// RunPull 执行 groot pull。
func RunPull(flags *PullFlags) error {
	homeDir := GetDefaultHome()
	cfg, err := config.Load(homeDir)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if cfg.Storage.Minio == nil {
		return errors.New("groot pull 仅在 minio 模式下可用\n请在 ~/.groot/env.yaml 中配置 minio 节")
	}
	store, err := storage.New(cfg.Storage)
	if err != nil {
		return fmt.Errorf("初始化存储失败: %w", err)
	}

	mgr := isync.NewSyncManager(homeDir, "", store)

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
