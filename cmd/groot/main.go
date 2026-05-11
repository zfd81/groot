package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/go-co-op/gocron/v2"

	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino-ext/adk/backend/local"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/cmd"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/filesystem"
	"github.com/zfd81/groot/internal/grootmd"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/message"
	"github.com/zfd81/groot/internal/message/senders"
	"github.com/zfd81/groot/internal/schedule"
	"github.com/zfd81/groot/internal/scheduler"
)

var (
	port        int
	showHelp    bool
	showVersion bool
)

func init() {
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
		case "init":
			handleInitCommand(args[1:])
			return
		case "status":
			handleStatusCommand(args[1:])
			return
		case "skills":
			handleSkillsCommand(args[1:])
			return
		case "mcp":
				handleMcpCommand(args[1:])
				return
		case "schedule":
			handleScheduleCommand(args[1:])
			return
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
	startServer(cmd.GetDefaultHome(), port)
}

func handleStatusCommand(args []string) {
	flags, err := cmd.ParseStatusFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}

	if err := cmd.RunStatus(flags); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
}

func handleSkillsCommand(args []string) {
	flags, err := cmd.ParseSkillsFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}

	if err := cmd.RunSkills(flags); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
}

func handleMcpCommand(args []string) {
	flags, err := cmd.ParseMcpFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}

	if err := cmd.RunMcp(flags); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
}

func handleScheduleCommand(args []string) {
	flags, err := cmd.ParseScheduleFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}

	if err := cmd.RunSchedule(flags); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
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

func handleInitCommand(args []string) {
	_, err := cmd.ParseInitFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}

	if err := cmd.RunInit(cmd.GetDefaultHome()); err != nil {
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

	// Initialize skills via eino skill middleware
	skillsDir := filepath.Join(homeDir, "skills")

	// Create local filesystem backend
	localBackend, err := local.NewBackend(context.Background(), &local.Config{})
	if err != nil {
		log.Error("无法创建文件系统后端", zap.Error(err))
	}

	// Create skill backend (scans {skillsDir}/*/SKILL.md)
	// Wrap local backend with symlink support for skill directories
	symlinkBackend := filesystem.NewSymlinkBackend(localBackend)
	skillBackend, err := einoskill.NewBackendFromFilesystem(context.Background(), &einoskill.BackendFromFilesystemConfig{
		Backend: symlinkBackend,
		BaseDir: skillsDir,
	})
	if err != nil {
		log.Error("无法创建Skill后端", zap.Error(err))
	}

	// Create skill middleware with custom system prompt
	// Skill metadata (name + description) is injected into the system prompt so the LLM
	// always sees available skills. Full skill content is still loaded on demand via the
	// skill tool, preserving progressive disclosure.
	skillMiddleware, err := einoskill.NewMiddleware(context.Background(), &einoskill.Config{
		Backend: skillBackend,
		CustomSystemPrompt: func(ctx context.Context, toolName string) string {
			matters, err := skillBackend.List(ctx)
			if err != nil || len(matters) == 0 {
				return ""
			}

			var b strings.Builder
			b.WriteString("## 可用 Skill\n\n")
			b.WriteString("以下 Skill 提供专业能力和结构化工作流程。")
			b.WriteString("当用户请求与某个 Skill 描述匹配时，必须使用 `" + toolName + "` 工具加载完整指令后执行。\n\n")
			b.WriteString("| Skill | 描述 |\n")
			b.WriteString("|-------|------|\n")
			for _, m := range matters {
				b.WriteString("| **" + m.Name + "** | " + m.Description + " |\n")
			}
			b.WriteString("\n")
			b.WriteString("**重要**：以上仅为概要，完整操作指令需通过 `" + toolName + "(\"<名称>\")` 工具获取。")
			b.WriteString("匹配到 Skill 时必须先加载再执行，不要跳过。\n")
			return b.String()
		},
	})
	if err != nil {
		log.Error("无法创建Skill中间件", zap.Error(err))
	}

	// Log skill count
	if skillBackend != nil {
		matters, listErr := skillBackend.List(context.Background())
		if listErr == nil {
			log.Info("Skills 加载完成", zap.Int("count", len(matters)), zap.String("dir", skillsDir))
		}
	}

	// Initialize MCP manager
	mcpMgr := mcp.NewManager(log)

	// Load MCP configs (fixed directory: {GROOT_HOME}/mcp)
	mcpDir := filepath.Join(homeDir, "mcp")
	if err := mcpMgr.LoadAll(mcpDir); err != nil {
		log.Error("无法加载MCP配置", zap.Error(err))
	}
	log.Info("MCP 加载完成", zap.Int("count", mcpMgr.Count()), zap.String("dir", mcpDir))

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

	// Initialize message layer
	msgLayer := message.NewLayer(cfg.Message, log)
	// Register all senders
	if cfg.Message.Senders["webhook"].Enabled {
		msgLayer.Register("webhook", senders.NewWebhook(cfg.Message.Senders["webhook"].URL), cfg.Message.Senders["webhook"])
	}
	if cfg.Message.Senders["email"].Enabled {
		sc := cfg.Message.Senders["email"]
		msgLayer.Register("email", senders.NewEmail(sc.SMTPHost, sc.SMTPPort, sc.Username, sc.Password, sc.From), sc)
	}
	msgLayer.Register("stdout", senders.NewStdout(), config.SenderConf{Enabled: true})
	msgLayer.Start()
	log.Info("消息层已启动")

	// Create executor (used by both API server and schedule runner)
	exec := agent.NewExecutor(memMgr, []adk.ChatModelAgentMiddleware{skillMiddleware}, mcpMgr, *cfg, log)

	// Create unified scheduler
	maxConcurrent := cfg.Schedule.MaxConcurrentTasks
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}
	sched, err := scheduler.New(log, maxConcurrent)

	// Declare scheduleMgr outside if block for use in api.NewServer
	var scheduleMgr *schedule.Manager

	if err != nil {
		log.Error("无法创建调度器", zap.Error(err))
	} else {
		// Initialize schedule module
		scheduleDir := filepath.Join(homeDir, "schedules")
		scheduleStorage := schedule.NewStorage(scheduleDir, log)
		if err := scheduleStorage.EnsureDirs(); err != nil {
			log.Error("无法创建调度目录", zap.Error(err))
		}

		// Create runner
		scheduleRunner := schedule.NewRunner(exec, memMgr, msgLayer, scheduleStorage, log)

		// Create engine and load active tasks
		scheduleEngine := schedule.NewEngine(sched, scheduleRunner, scheduleStorage, log)
		if err := scheduleEngine.Start(); err != nil {
			log.Error("无法启动调度引擎", zap.Error(err))
		}

		// Create manager
		scheduleMgr = schedule.NewManager(scheduleStorage, scheduleEngine, scheduleRunner, log)

		// Register built-in schedule tools
		scheduleTools := schedule.NewScheduleTools(scheduleMgr)
		mcpMgr.RegisterBuiltinTools(scheduleTools)
		log.Info("调度工具已注册", zap.Int("count", len(scheduleTools)))

		// Register cleanup task
		cleanupHour, cleanupMinute := schedule.ParseCleanupTime(cfg.Memory.CleanupSchedule)
		sched.AddDaily(cleanupHour, cleanupMinute, gocron.NewTask(memory.NewCleanupTask(memMgr, log)), "system-cleanup", "cleanup")

		// Register sync task
		syncInterval, _ := time.ParseDuration(cfg.Schedule.SyncInterval)
		if syncInterval <= 0 {
			syncInterval = 30 * time.Second
		}
		sched.AddDuration(syncInterval, gocron.NewTask(schedule.NewSyncTask(scheduleEngine, scheduleStorage, log)), "system-sync", "sync")

		// Start scheduler
		sched.Start()
		log.Info("统一调度器已启动",
			zap.Int("max_concurrent", maxConcurrent),
			zap.Int("cleanup_hour", cleanupHour),
			zap.Int("cleanup_minute", cleanupMinute),
		)
	}

	// Create API server
	srv := api.NewServer(*cfg, homeDir, memoryDir, log, memMgr, runtimeState, skillBackend, skillMiddleware, mcpMgr, exec, scheduleMgr)

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

		// Stop scheduler
		if sched != nil {
			sched.Stop()
		}

		// Stop message layer
		msgLayer.Stop()

		// Close MCP clients
		mcpMgr.Close()

		log.Info("Groot Agent 已关闭")
	}()

	// Check if port is available before starting
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	conn, err := net.Dial("tcp", addr)
	if err == nil {
		conn.Close()
		log.Error("端口已被占用",
			zap.String("host", cfg.Server.Host),
			zap.Int("port", cfg.Server.Port))
		fmt.Fprintf(os.Stderr, "错误: 端口 %d 已被占用\n", cfg.Server.Port)
		fmt.Fprintf(os.Stderr, "提示: 请检查是否有其他 Groot 进程运行，或使用 -p 指定其他端口\n")
		os.Exit(1)
	}

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
	fmt.Println("  init              初始化工作目录")
	fmt.Println("  status            查看运行中实例的状态")
	fmt.Println("  skills            管理 Skills（list/install/uninstall）")
	fmt.Println("  mcp               管理 MCP Servers（list）")
	fmt.Println("  schedule          管理定时任务（list/inspect/history 等）")
	fmt.Println("  tail              实时日志查看")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -p, --port <port> HTTP端口 (默认配置文件值)")
	fmt.Println("  -h, --help        显示帮助")
	fmt.Println("  -v, --version     显示版本")
	fmt.Println()
	fmt.Println("init 子命令选项:")
	fmt.Println("  -h, --help        显示 init 子命令帮助")
	fmt.Println()
	fmt.Println("status 子命令选项:")
	fmt.Println("  -p <port>        指定 Groot 服务端口")
	fmt.Println("  -h, --help        显示 status 子命令帮助")
	fmt.Println()
	fmt.Println("skills 子命令:")
	fmt.Println("  list              列出所有已安装的 Skills")
	fmt.Println("  install <path>    安装 Skill")
	fmt.Println("  uninstall <name>  卸载 Skill")
	fmt.Println()
	fmt.Println("mcp 子命令:")
	fmt.Println("  list              列出所有已配置的 MCP Servers")
	fmt.Println()
	fmt.Println("schedule 子命令:")
	fmt.Println("  list              列出所有定时任务")
	fmt.Println("  inspect <id>      查看任务详情")
	fmt.Println("  history <id>      查看执行历史")
	fmt.Println("  delete <id>       删除任务")
	fmt.Println("  disable <id>      禁用任务")
	fmt.Println("  enable <id>       启用任务")
	fmt.Println("  archive <id>      归档任务")
	fmt.Println()
	fmt.Println("tail 子命令选项:")
	fmt.Println("  -n <N>            显示最近 N 行日志 (默认 100)")
	fmt.Println("  -l <level>        按日志级别过滤 (error/warn/info/debug)")
	fmt.Println("  -k <keyword>      按关键词过滤")
	fmt.Println("  -h, --help        显示 tail 子命令帮助")
	fmt.Println()
	fmt.Println("环境变量:")
	fmt.Println("  GROOT_HOME        工作目录")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot                         # 使用默认配置启动服务")
	fmt.Println("  groot init                    # 初始化默认工作目录 ~/.groot")
	fmt.Println("  groot status                  # 查看实例状态")
	fmt.Println("  groot status -p 9090         # 查看 9090 端口实例状态")
	fmt.Println("  groot skills list             # 列出所有 Skills")
	fmt.Println("  groot skills install ./my-skill  # 安装 Skill")
	fmt.Println("  groot skills uninstall my-skill  # 卸载 Skill")
	fmt.Println("  groot mcp list                  # 列出所有 MCP Servers")
	fmt.Println("  groot -p 9090                 # 指定端口启动服务")
	fmt.Println("  groot tail                    # 显示最近 100 行日志")
	fmt.Println("  groot tail -n 50 -l error     # 显示最近 50 行错误日志")
}