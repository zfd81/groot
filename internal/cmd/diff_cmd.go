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

// DiffFlags holds parsed flags for the diff command.
type DiffFlags struct {
	Paths []string
}

// ParseDiffFlags 解析 groot diff 子命令参数。
func ParseDiffFlags(args []string) (*DiffFlags, error) {
	flags := &DiffFlags{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printDiffHelp()
			os.Exit(0)
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
			flags.Paths = append(flags.Paths, arg)
		}
	}
	return flags, nil
}

func printDiffHelp() {
	fmt.Println("用法: groot diff [path...]")
	fmt.Println()
	fmt.Println("显示本地 HOME 与 MinIO 之间的集群共享配置差异（只读，不修改）。")
	fmt.Println("仅在 minio 模式下可用。")
	fmt.Println()
	fmt.Println("参数:")
	fmt.Println("  path...   要比较的资源路径（可多个），省略时比较全部白名单资源")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -h, --help  显示帮助")
}

// RunDiff 执行 groot diff。
func RunDiff(flags *DiffFlags) error {
	homeDir := GetDefaultHome()
	cfg, err := config.Load(homeDir)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if cfg.Storage.Minio == nil {
		return errors.New("groot diff 仅在 minio 模式下可用\n请在 ~/.groot/env.yaml 中配置 minio 节")
	}
	store, err := storage.New(cfg.Storage)
	if err != nil {
		return fmt.Errorf("初始化存储失败: %w", err)
	}

	mgr := isync.NewSyncManager(homeDir, "", store)

	diff, err := mgr.Diff(flags.Paths)
	if err != nil {
		return err
	}

	fmt.Print(isync.FormatDiff(diff, "diff"))
	return nil
}
