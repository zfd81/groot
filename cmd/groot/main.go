package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
	"github.com/zfd81/groot/internal/lifecycle"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/message"
	"github.com/zfd81/groot/internal/message/senders"
	"github.com/zfd81/groot/internal/repo/repofactory"
	"github.com/zfd81/groot/internal/schedule"
	"github.com/zfd81/groot/internal/scheduler"
)

var (
	port          int
	showHelp      bool
	showVersion   bool
	singleProcess bool
)

func init() {
	flag.IntVar(&port, "p", 0, "HTTP端口 (默认配置文件值)")
	flag.IntVar(&port, "port", 0, "HTTP端口 (默认配置文件值)")
	flag.BoolVar(&showHelp, "h", false, "显示帮助")
	flag.BoolVar(&showHelp, "help", false, "显示帮助")
	flag.BoolVar(&showVersion, "v", false, "显示版本")
	flag.BoolVar(&showVersion, "version", false, "显示版本")
	flag.BoolVar(&singleProcess, "single-process", false, "单进程运行（无监督进程，不支持重启）")
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

	// No subcommand: 按角色启动
	role := lifecycle.DetectRole(os.Getenv(lifecycle.EnvSupervised), singleProcess)
	if role == lifecycle.RoleSupervisor {
		os.Exit(runSupervisor())
	}
	startServer(cmd.GetDefaultHome(), port, role)
}

// runSupervisor 以监督进程身份运行：拉起同一二进制作为工作进程，并把关闭信号转交给它。
// 工作进程通过环境变量 GROOT_SUPERVISED=1 识别身份，命令行参数原样传递。
func runSupervisor() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法确定可执行文件路径: %s\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	sup := lifecycle.NewSupervisor(lifecycle.Options{
		Exe:  exe,
		Args: os.Args[1:],
	})
	return sup.Run(ctx)
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

func startServer(homeDir string, port int, role lifecycle.Role) {
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
	logger.SetDefault(log)
	defer log.Sync()

	log.Info("Groot Agent 启动中...",
		zap.String("home", homeDir),
		zap.String("config", filepath.Join(homeDir, "config.yaml")),
	)
	log.Info("进程模式", zap.String("mode", role.ProcessMode()), zap.Int("pid", os.Getpid()))
	startedAt := time.Now()
	ctrl := lifecycle.NewController(role)

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
	// 集群消息服务：在 clusterInst 创建后赋值，startLeaderTasks 闭包中引用
	var clusterMsg *cluster.MessageService

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

		// Register cluster message cleanup (daily 03:00, leader only; retention 见 cluster.messageRetention)
		if clusterMsg != nil {
			sched.AddDaily(3, 0, gocron.NewTask(cluster.NewMessageCleanupTask(clusterMsg, log)),
				"system-cluster-message-cleanup", "cleanup")
		} else {
			// 正常接线下不可达：clusterMsg 必须在 clusterInst.Join 之前赋值
			log.Error("集群消息服务未挂接,跳过清理任务注册")
		}

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
	addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
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
	// server.host 是监听地址，0.0.0.0/:: 不可作为成员地址登记，需解析为本机真实 IP
	clusterInst := cluster.New(cluster.ResolveAdvertiseHost(cfg.Server.Host), cfg.Server.Port, log, repos.Member)
	clusterInst.SetCallbacks(startLeaderTasks, stopLeaderTasks)

	// 集群消息服务必须在 Join 之前挂接：Join 会同步触发 register → onBecomeLeader，
	// 且心跳 goroutine 启动后不再允许修改 Cluster 的挂接字段。
	clusterMsg = cluster.NewMessageService(repos.Message, log, clusterInst.RegID)
	clusterInst.SetMessageService(clusterMsg)
	// 生命周期指令处理器（重启）：模块名 lifecycle，早于 startedAt 的指令被忽略
	clusterMsg.RegisterHandler(lifecycle.ModuleName, lifecycle.NewClusterHandler(ctrl, startedAt, log))

	if err := clusterInst.Join(context.Background()); err != nil {
		log.Error("加入集群失败", zap.Error(err))
	}

	log.Info("集群状态",
		zap.String("role", clusterInst.Role()),
		zap.String("reg_id", clusterInst.RegID()),
	)

	// Create API server
	srv := api.NewServer(*cfg, homeDir, log, memMgr, runtimeState, skillBackend, skillMiddleware, mcpMgr, exec, subAgentReg, &scheduleMgr, repos.User, modelService, repos.APIKey, repos.Member, repos.SyncResource, clusterInst, role)

	// 停止来源 1：操作系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		// 按信号计数而不是查 ctrl.Stopping()：受监督模式下 Ctrl+C 会同时打到两个进程，
		// 工作进程可能先经管道 EOF 进入停止态，此时它收到的第一个信号不应被当作"再次收到"。
		n := 0
		for sig := range sigCh {
			n++
			if n > 1 {
				// 优雅关闭进行中再次收到信号：视为使用者要求立刻退出，不再等待
				log.Warn("关闭过程中再次收到信号，强制退出", zap.String("signal", sig.String()))
				log.Sync()
				os.Exit(1)
			}
			log.Info("收到信号，准备关闭", zap.String("signal", sig.String()))
			ctrl.RequestStop(lifecycle.ReasonSignal) // 已在停止中则为 no-op
		}
	}()

	// 停止来源 2：监督进程关闭了 stdin 管道（仅受监督模式；单进程的 stdin 可能是终端或 /dev/null）
	if role == lifecycle.RoleWorker {
		lifecycle.WatchStdin(os.Stdin, func() {
			log.Info("监督进程已关闭管道，准备关闭")
			ctrl.RequestStop(lifecycle.ReasonSupervisorClosed)
		})
	}

	// 停止来源 3：重启指令，由 lifecycle.ClusterHandler 调用 ctrl.RequestRestart()

	// 唯一的关闭流程：等第一个停止原因，然后按固定顺序释放资源
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		reason := <-ctrl.Done()
		log.Info("开始关闭", zap.String("reason", reason.String()), zap.Int("exit_code", reason.ExitCode()))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Leave cluster before shutting down
		clusterInst.Leave()

		// Stop server（使 srv.Start() 返回）
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
	err = srv.Start()

	// 已有停止原因：Start 的返回值是关闭的副产物，不作为错误处理；
	// 等关闭流程跑完，显式刷新日志、关库，再以该原因的退出码退出（os.Exit 不执行 defer）。
	if ctrl.Stopping() {
		<-shutdownDone
		code := ctrl.Reason().ExitCode()
		log.Info("进程退出", zap.Int("exit_code", code))
		log.Sync()
		sqlxDB.Close()
		os.Exit(code)
	}
	if err != nil {
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
	fmt.Println("  user              管理 Web 登录用户（reset）")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -p, --port <port> HTTP端口 (默认配置文件值)")
	fmt.Println("  --single-process  单进程运行（无监督进程，不支持 Web 重启；调试或容器托管场景使用）")
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
	fmt.Println("  groot --single-process        # 单进程运行，不启动监督进程")
}
