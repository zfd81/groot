package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/apitool"
	"github.com/zfd81/groot/internal/cmd"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/grootmd"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/skill"
)

var (
	homeDir     string
	port        int
	showHelp    bool
	showVersion bool
)

func init() {
	flag.StringVar(&homeDir, "H", "", "工作目录 (默认 ~/.groot)")
	flag.StringVar(&homeDir, "home", "", "工作目录 (默认 ~/.groot)")
	flag.IntVar(&port, "p", 0, "HTTP端口 (默认配置文件值)")
	flag.IntVar(&port, "port", 0, "HTTP端口 (默认配置文件值)")
	flag.BoolVar(&showHelp, "h", false, "显示帮助")
	flag.BoolVar(&showHelp, "help", false, "显示帮助")
	flag.BoolVar(&showVersion, "v", false, "显示版本")
	flag.BoolVar(&showVersion, "version", false, "显示版本")
}

func main() {
	flag.Parse()

	if showHelp {
		printHelp()
		return
	}

	if showVersion {
		fmt.Println("Groot Agent v1.0.0")
		return
	}

	// Get remaining arguments after flag parsing
	args := flag.Args()

	// Handle subcommands
	if len(args) > 0 {
		command := args[0]
		switch command {
		case "tail":
			handleTailCommand(args[1:])
		default:
			fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", command)
			printHelp()
			os.Exit(1)
		}
		return
	}

	// No subcommand, start server
	// Determine home directory
	if homeDir == "" {
		homeDir = os.Getenv("GROOT_HOME")
		if homeDir == "" {
			homeDir = filepath.Join(os.Getenv("HOME"), ".groot")
		}
	}

	startServer(homeDir, port)
}

func handleTailCommand(args []string) {
	flags, err := cmd.ParseTailFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}

	if err := cmd.RunTail(flags); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
}

func startServer(homeDir string, port int) {
	// Ensure home directory exists
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "无法创建工作目录: %s\n", err)
		os.Exit(1)
	}

	// Load configuration
	cfg, err := config.Load(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法加载配置: %s\n", err)
		os.Exit(1)
	}

	// Override port if specified
	if port > 0 {
		cfg.Server.Port = port
	}

	// Resolve log directory path (before logger initialization)
	cfg.Logging.File.Directory = config.ResolvePath(cfg.Logging.File.Directory, homeDir)

	// Initialize logger
	log := logger.New(cfg.Logging)
	defer log.Sync()

	log.Info("Groot Agent 启动中...",
		zap.String("home", homeDir),
		zap.String("config", filepath.Join(homeDir, "config.yaml")),
	)

	// Initialize skills registry
	skillsRegistry := skill.NewRegistry()
	skillLoader := skill.NewLoader(skillsRegistry)

	// Load skills
	skillsDir := config.ResolvePath(cfg.Skills.Directory, homeDir)
	if err := skillLoader.LoadAll(skillsDir); err != nil {
		log.Error("无法加载Skills", zap.Error(err))
	}
	log.Info("Skills 加载完成", zap.Int("count", skillsRegistry.Count()), zap.String("dir", skillsDir))

	// Start skills watcher
	skillWatcher := skill.NewWatcher(skillLoader, cfg.Skills, log)
	if err := skillWatcher.Start(skillsDir); err != nil {
		log.Error("无法启动Skills watcher", zap.Error(err))
	}

	// Initialize MCP manager
	mcpMgr := mcp.NewManager(log)

	// Load MCP configs
	mcpDir := config.ResolvePath(cfg.MCP.Directory, homeDir)
	if err := mcpMgr.LoadAll(mcpDir); err != nil {
		log.Error("无法加载MCP配置", zap.Error(err))
	}
	log.Info("MCP 加载完成", zap.Int("count", mcpMgr.Count()), zap.String("dir", mcpDir))

	// Initialize API tool manager
	apiMgr := apitool.NewManager(log)

	// Load API tool configs
	apiDir := config.ResolvePath(cfg.APITools.Directory, homeDir)
	// 先获取MCP工具名称列表用于冲突检查
	mcpToolNames := []string{}
	for _, tool := range mcpMgr.ListTools() {
		mcpToolNames = append(mcpToolNames, tool.Name)
	}
	// 校验并加载API工具
	if err := apiMgr.ValidateAndLoad(apiDir, mcpToolNames); err != nil {
		log.Error("无法加载API工具配置", zap.Error(err))
		fmt.Fprintf(os.Stderr, "加载API工具失败: %s\n", err)
		os.Exit(1)
	}
	log.Info("API工具 加载完成", zap.Int("count", apiMgr.Count()), zap.String("dir", apiDir))

	// Initialize memory manager
	memoryDir := config.ResolvePath(cfg.Memory.Directory, homeDir)
	memMgr := memory.NewManager(memoryDir, cfg.Memory.RetentionDays, log)
	log.Info("Memory 初始化完成", zap.String("dir", memoryDir))

	// Initialize runtime state
	runtimeState := agent.NewRuntimeState()

	// Start GROOT.md watcher (unconditionally)
	grootMdWatcher := grootmd.NewWatcher(homeDir, log)
	if err := grootMdWatcher.Start(); err != nil {
		log.Error("无法启动 GROOT.md watcher", zap.Error(err))
	}

	// Start cleanup scheduler
	cleanupScheduler := memory.NewCleanupScheduler(memMgr, cfg.Memory.CleanupSchedule, log)
	cleanupScheduler.Start()
	log.Info("清理调度器已启动", zap.String("schedule", cfg.Memory.CleanupSchedule))

	// Resolve temp directory path
	cfg.Attachment.TempDirectory = config.ResolvePath(cfg.Attachment.TempDirectory, homeDir)

	// Create API server
	srv := api.NewServer(*cfg, homeDir, log, memMgr, runtimeState, skillsRegistry, mcpMgr, apiMgr)

	// Setup graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Info("收到信号，准备关闭", zap.String("signal", sig.String()))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Stop server
		srv.Stop(ctx)

		// Stop watchers
		grootMdWatcher.Stop()
		skillWatcher.Stop()

		// Stop cleanup scheduler
		cleanupScheduler.Stop()

		// Close MCP executor (terminate running processes)
		mcpMgr.GetExecutor().Close()

		log.Info("Groot Agent 已关闭")
	}()

	// Start server
	log.Info("API 服务启动",
		zap.String("host", cfg.Server.Host),
		zap.Int("port", cfg.Server.Port),
	)
	if err := srv.Start(); err != nil {
		log.Error("服务启动失败", zap.Error(err))
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println("用法: groot [选项] <子命令>")
	fmt.Println()
	fmt.Println("子命令:")
	fmt.Println("  tail              实时日志查看")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -H, --home <dir>  工作目录 (默认 ~/.groot)")
	fmt.Println("  -p, --port <port> HTTP端口 (默认配置文件值)")
	fmt.Println("  -h, --help        显示帮助")
	fmt.Println("  -v, --version     显示版本")
	fmt.Println()
	fmt.Println("tail 子命令选项:")
	fmt.Println("  -n <N>            显示最近 N 行日志 (默认 100)")
	fmt.Println("  -l <level>        按日志级别过滤 (error/warn/info/debug)")
	fmt.Println("  -k <keyword>      按关键词过滤")
	fmt.Println("  -H, --home <dir>  工作目录 (默认 ~/.groot)")
	fmt.Println("  -h, --help        显示 tail 子命令帮助")
	fmt.Println()
	fmt.Println("环境变量:")
	fmt.Println("  GROOT_HOME        工作目录")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot                         # 使用默认配置启动服务")
	fmt.Println("  groot -H /opt/groot            # 指定工作目录启动服务")
	fmt.Println("  groot -p 9090                  # 指定端口启动服务")
	fmt.Println("  groot tail                     # 显示最近 100 行日志")
	fmt.Println("  groot tail -n 50 -l error     # 显示最近 50 行错误日志")
}