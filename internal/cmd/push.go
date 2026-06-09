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

// PushFlags holds parsed flags for the push command.
type PushFlags struct {
	Paths []string // 要推送的相对路径列表，nil 表示全部
	Yes   bool     // -y / --yes: 跳过确认
}

// ParsePushFlags 解析 groot push 子命令参数。
func ParsePushFlags(args []string) (*PushFlags, error) {
	flags := &PushFlags{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printPushHelp()
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

func printPushHelp() {
	fmt.Println("用法: groot push [path...] [-y]")
	fmt.Println()
	fmt.Println("将本地 HOME 的集群共享配置镜像推送到 MinIO。")
	fmt.Println("仅在 minio 模式下可用（需配置 ~/.groot/env.yaml 中的 minio 节）。")
	fmt.Println()
	fmt.Println("参数:")
	fmt.Println("  path...   要推送的资源路径（可多个），省略时推送全部白名单资源")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -y, --yes   跳过交互确认，直接执行")
	fmt.Println("  -h, --help  显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot push                       # 推送全部")
	fmt.Println("  groot push config.yaml           # 推送主配置")
	fmt.Println("  groot push skills/weather        # 推送单个 skill")
	fmt.Println("  groot push skills subagents mcp  # 推送多个类别")
	fmt.Println("  groot push -y skills             # 跳过确认直接推送")
}

// RunPush 执行 groot push。
func RunPush(flags *PushFlags) error {
	homeDir := GetDefaultHome()
	cfg, err := config.Load(homeDir)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if cfg.Storage.Minio == nil {
		return errors.New("groot push 仅在 minio 模式下可用\n请在 ~/.groot/env.yaml 中配置 minio 节")
	}
	store, err := storage.New(cfg.Storage)
	if err != nil {
		return fmt.Errorf("初始化存储失败: %w", err)
	}

	// minio 模式下 remoteBase = "" 表示 bucket 根
	mgr := isync.NewSyncManager(homeDir, "", store)

	fmt.Println("Scanning differences...")
	diff, err := mgr.Diff(flags.Paths)
	if err != nil {
		return err
	}

	fmt.Print(isync.FormatDiff(diff, "push"))
	if diff.IsEmpty() {
		return nil
	}

	if !flags.Yes {
		if !isync.ConfirmContinue(os.Stdin, os.Stdout) {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	if err := mgr.Push(flags.Paths); err != nil {
		return fmt.Errorf("push 失败: %w", err)
	}
	fmt.Println("Push complete.")
	return nil
}
