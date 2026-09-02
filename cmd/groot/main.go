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

	"github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/cmd"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/filesystem"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/message"
	"github.com/zfd81/groot/internal/message/senders"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/repofactory"
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
		case "tail":
			handleTailCommand(args[1:])
			return
		case "push":
			handlePushCommand(args[1:])
			return
		case "pull":
			handlePullCommand(args[1:])
			return
		case "diff":
			handleDiffCommand(args[1:])
			return
		case "user":
			handleUserCommand(args[1:])
			return
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

// openRepos 为需要访问数据库的子命令加载配置并打开数据库，返回全部 Repos。
func openRepos(homeDir string) *repofactory.Repos {
	cfg, err := config.Load(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %s\n", err)
		os.Exit(1)
	}
	sqlxDB, dbDialect, err := db.Open(cfg.Database, homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化数据库失败: %s\n", err)
		os.Exit(1)
	}
	// Note: sqlxDB is intentionally not closed here; the process exits after the command.
	return repofactory.NewRepos(sqlxDB, dbDialect, homeDir)
}

// openSyncRepo 为 push/pull/diff 子命令加载配置并打开数据库,返回 ResourceRepo。
// SQLite 模式下 ResourceRepo 使用本地文件系统实现,此时 sync 命令会因
// NewSyncManager 内的 disabledSyncManager 返回 ErrSyncDisabled。
func openSyncRepo(homeDir string) repo.ResourceRepo {
	return openRepos(homeDir).Resource
}

func handleUserCommand(args []string) {
	flags, err := cmd.ParseUserFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
	homeDir := cmd.GetDefaultHome()
	repos := openRepos(homeDir)
	if err := cmd.RunUserReset(flags, repos.User, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
}

func handlePushCommand(args []string) {
	flags, err := cmd.ParsePushFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
	homeDir := cmd.GetDefaultHome()
	r := openSyncRepo(homeDir)
	if err := cmd.RunPush(flags, r); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
}

func handlePullCommand(args []string) {
	flags, err := cmd.ParsePullFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
	homeDir := cmd.GetDefaultHome()
	r := openSyncRepo(homeDir)
	if err := cmd.RunPull(flags, r); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
}

func handleDiffCommand(args []string) {
	flags, err := cmd.ParseDiffFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
	homeDir := cmd.GetDefaultHome()
	r := openSyncRepo(homeDir)
	if err := cmd.RunDiff(flags, r); err != nil {
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

	// 认证始终开启：secret 缺失（老版本升级）时自动生成并回写 config.yaml
	if err := config.EnsureAuthSecret(homeDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "初始化认证密钥失败: %s\n", err)
		os.Exit(1)
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
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		log.Error("无法创建 Skills 目录", zap.Error(err))
	}

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

	// Initialize database and repositories
	sqlxDB, dbDialect, err := db.Open(cfg.Database, homeDir)
	if err != nil {
		log.Error("无法初始化数据库", zap.Error(err))
		os.Exit(1)
	}
	defer sqlxDB.Close()
	repos := repofactory.NewRepos(sqlxDB, dbDialect, homeDir)
	log.Info("数据库初始化完成", zap.Int("dialect", int(dbDialect)))

	// 模型配置业务层：模型配置唯一存储于数据库，每次使用实时读取
	modelService := llm.NewModelService(repos.Model)

	// Initialize memory manager
	memMgr := memory.NewManager(log, repos.Memory)
	log.Info("Memory 初始化完成")

	// Initialize runtime state
	runtimeState := agent.NewRuntimeState()

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

	// Load sub-agents (fixed directory: {GROOT_HOME}/subagents)
	subAgentDir := filepath.Join(homeDir, "subagents")
	subAgentReg := agent.BuildSubAgentRegistry(context.Background(), subAgentDir, cfg.React, cfg.SubAgent, modelService, log)
	log.Info("SubAgents 加载完成", zap.Strings("agents", subAgentReg.Names()))

	// Create executor (used by both API server and schedule runner)
	exec := agent.NewExecutor(homeDir, memMgr, []adk.ChatModelAgentMiddleware{skillMiddleware}, mcpMgr, subAgentReg, runtimeState, modelService, *cfg, log)

	// Declare schedule module variables (used by leader callbacks and API server)
	var sched *scheduler.Scheduler
	var scheduleMgr *schedule.Manager
	var scheduleEngine *schedule.Engine
	var scheduleStorage *schedule.Storage
	var scheduleRunner *schedule.Runner

	// Initialize schedule module (storage and runner needed regardless of leader status)
	scheduleStorage = schedule.NewStorage(repos.Schedule, log)
	scheduleRunner = schedule.NewRunner(exec, memMgr, msgLayer, scheduleStorage, log)

	// Define leader task callbacks
	startLeaderTasks := func() {
		maxConcurrent := cfg.Schedule.MaxConcurrentTasks
		if maxConcurrent <= 0 {
			maxConcurrent = 10
		}
		var err error
		sched, err = scheduler.New(log, maxConcurrent)
		if err != nil {
			log.Error("无法创建调度器", zap.Error(err))
			return
		}

		scheduleEngine = schedule.NewEngine(sched, scheduleRunner, scheduleStorage, log)
		if err := scheduleEngine.Start(); err != nil {
			log.Error("无法启动调度引擎", zap.Error(err))
			return
		}

		// Register sync task
		syncInterval, _ := time.ParseDuration(cfg.Schedule.SyncInterval)
		if syncInterval <= 0 {
			syncInterval = 30 * time.Second
		}
		sched.AddDuration(syncInterval, gocron.NewTask(schedule.NewSyncTask(scheduleEngine, scheduleStorage, log)), "system-sync", "sync")

		// Register schedule tools if enabled
		if cfg.Schedule.Enabled {
			scheduleMgr = schedule.NewManager(scheduleStorage, scheduleEngine, scheduleRunner, log)
			scheduleTools := schedule.NewScheduleTools(scheduleMgr)
			mcpMgr.RegisterBuiltinTools(scheduleTools)
			log.Info("调度工具已注册", zap.Int("count", len(scheduleTools)))
		}

		sched.Start()
		log.Info("统一调度器已启动 (Leader)",
			zap.Int("max_concurrent", maxConcurrent),
		)
	}

	stopLeaderTasks := func() {
		if sched != nil {
			if err := sched.Stop(); err != nil {
				log.Error("无法停止调度器", zap.Error(err))
			}
			sched = nil
		}
		if scheduleMgr != nil {
			mcpMgr.UnregisterBuiltinTools()
			scheduleMgr = nil
		}
	}

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

	// Initialize cluster
	clusterInst := cluster.New(cfg.Server.Host, cfg.Server.Port, log, repos.Member)
	clusterInst.SetCallbacks(startLeaderTasks, stopLeaderTasks)

	if err := clusterInst.Join(context.Background()); err != nil {
		log.Error("加入集群失败", zap.Error(err))
	}

	log.Info("集群状态",
		zap.String("role", clusterInst.Role()),
		zap.String("reg_id", clusterInst.RegID()),
	)

	// Create API server
	srv := api.NewServer(*cfg, homeDir, log, memMgr, runtimeState, skillBackend, skillMiddleware, mcpMgr, exec, subAgentReg, &scheduleMgr, repos.User, modelService, repos.APIKey)

	// Setup graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Info("收到信号，准备关闭", zap.String("signal", sig.String()))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Leave cluster before shutting down
		clusterInst.Leave()

		// Stop server
		srv.Stop(ctx)

		// Stop message layer
		msgLayer.Stop()

		// Close MCP clients
		mcpMgr.Close()

		// Close sub-agent registry (closes per-agent MCP managers)
		if subAgentReg != nil {
			subAgentReg.Close()
		}

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
	fmt.Println("  init              初始化工作目录")
	fmt.Println("  status            查看运行中实例的状态")
	fmt.Println("  tail              实时日志查看")
	fmt.Println("  push              将本地配置推送到数据库（MySQL/PG 模式）")
	fmt.Println("  pull              从数据库拉取配置到本地（MySQL/PG 模式）")
	fmt.Println("  diff              显示本地与数据库的配置差异（MySQL/PG 模式）")
	fmt.Println("  user              管理 Web 登录用户（reset）")
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
	fmt.Println("user 子命令:")
	fmt.Println("  reset             重置 Web 登录用户（删除用户表全部数据，-y 跳过确认）")
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
	fmt.Println("  groot -p 9090                 # 指定端口启动服务")
	fmt.Println("  groot tail                    # 显示最近 100 行日志")
	fmt.Println("  groot tail -n 50 -l error     # 显示最近 50 行错误日志")
}
