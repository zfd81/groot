package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino-ext/adk/backend/local"
	tea "charm.land/bubbletea/v2"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/cmd/chat"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/filesystem"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/message"
	"github.com/zfd81/groot/internal/message/senders"
	"github.com/zfd81/groot/internal/storage"
)

// RunChat starts the chat TUI.
func RunChat() error {
	homeDir := GetDefaultHome()

	// 1. Load config
	cfg, err := config.Load(homeDir)
	if err != nil {
		return fmt.Errorf("配置文件不存在，请先执行 groot init 初始化配置")
	}

	baseURL := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)

	// 2. Check if service is running
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serviceRunning := checkService(ctx, baseURL)

	// 3. Start embedded service if needed
	if serviceRunning {
		fmt.Printf("检测到已有服务运行 (端口 %d)\n", cfg.Server.Port)
	} else {
		fmt.Println("未检测到运行中的服务，正在启动嵌入服务...")
		srv, err := startEmbedServer(cfg, homeDir)
		if err != nil {
			return fmt.Errorf("无法启动嵌入服务: %w", err)
		}
		defer srv.Stop(context.Background())
		fmt.Printf("嵌入服务已启动 (端口 %d)\n", cfg.Server.Port)
	}

	// 4. Create and run TUI
	model := chat.NewModel(cfg, baseURL)

	p := tea.NewProgram(model)
	_, runErr := p.Run()

	if !serviceRunning {
		fmt.Println("嵌入服务已关闭")
	}
	if runErr != nil {
		return fmt.Errorf("TUI 运行错误: %w", runErr)
	}
	return nil
}

func checkService(ctx context.Context, baseURL string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// startEmbedServer bootstraps a full groot API server in-process.
// Mirrors cmd/groot/main.go startServer() with stdout logging suppressed.
func startEmbedServer(cfg *config.Config, homeDir string) (*api.Server, error) {
	// Suppress stdout: override logging output to file-only
	logCfg := cfg.Logging
	logCfg.Output = []string{"file"}
	logCfg.File.Directory = config.ResolvePath(logCfg.File.Directory, homeDir)
	log := logger.New(logCfg)
	hlog.SetOutput(io.Discard) // 禁止 Hertz 内部日志输出到 stderr，防止破坏 TUI 渲染

	// Resolve directories
	skillsDir := filepath.Join(homeDir, "skills")
	mcpDir := filepath.Join(homeDir, "mcp")

	os.MkdirAll(skillsDir, 0755)
	os.MkdirAll(mcpDir, 0755)

	// Skills backend
	localBackend, err := local.NewBackend(context.Background(), &local.Config{})
	if err != nil {
		return nil, fmt.Errorf("无法创建文件系统后端: %w", err)
	}
	symlinkBackend := filesystem.NewSymlinkBackend(localBackend)
	skillBackend, err := einoskill.NewBackendFromFilesystem(context.Background(), &einoskill.BackendFromFilesystemConfig{
		Backend: symlinkBackend,
		BaseDir: skillsDir,
	})
	if err != nil {
		return nil, fmt.Errorf("无法创建Skill后端: %w", err)
	}

	// Skill middleware
	skillMiddleware, err := einoskill.NewMiddleware(context.Background(), &einoskill.Config{
		Backend: skillBackend,
	})
	if err != nil {
		return nil, fmt.Errorf("无法创建Skill中间件: %w", err)
	}

	// MCP manager
	mcpMgr := mcp.NewManager(log)
	if err := mcpMgr.LoadAll(mcpDir); err != nil {
		return nil, fmt.Errorf("无法加载MCP配置: %w", err)
	}

	// Storage backend
	store, err := storage.New(cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("无法初始化存储后端: %w", err)
	}

	// 按 storage 类型计算 memory basePath:
	//   local 模式:绝对路径(${homeDir}/memory),向后兼容
	//   minio 模式:相对 object-key 前缀("memory")
	var memoryBaseDir string
	if cfg.Storage.Minio != nil {
		memoryBaseDir = "memory"
	} else {
		memoryBaseDir = config.ResolvePath(cfg.Memory.Directory, homeDir)
		os.MkdirAll(memoryBaseDir, 0755)
	}

	// Memory manager
	memMgr := memory.NewManager(memoryBaseDir, cfg.Memory.RetentionDays, log, store)

	// Runtime state
	runtimeState := agent.NewRuntimeState()

	// Message layer
	msgLayer := message.NewLayer(cfg.Message, log)
	if cfg.Message.Senders["webhook"].Enabled {
		msgLayer.Register("webhook", senders.NewWebhook(cfg.Message.Senders["webhook"].URL), cfg.Message.Senders["webhook"])
	}
	if cfg.Message.Senders["email"].Enabled {
		sc := cfg.Message.Senders["email"]
		msgLayer.Register("email", senders.NewEmail(sc.SMTPHost, sc.SMTPPort, sc.Username, sc.Password, sc.From), sc)
	}
	// 注意：不注册 stdout sender，因为 TUI 模式下 fmt.Printf 会破坏终端渲染
	msgLayer.Start()

	// Load sub-agents (fixed directory: {GROOT_HOME}/subagents)
	subAgentDir := filepath.Join(homeDir, "subagents")
	subAgentReg := agent.BuildSubAgentRegistry(context.Background(), subAgentDir, cfg.React, cfg.SubAgent, cfg.LLM, log)

	// Create executor
	exec := agent.NewExecutor(homeDir, memMgr, []adk.ChatModelAgentMiddleware{skillMiddleware}, mcpMgr, subAgentReg, runtimeState, store, *cfg, log)

	// Create API server (schedule disabled in embed mode)
	srv := api.NewServer(*cfg, homeDir, memoryBaseDir, log, memMgr, runtimeState, skillBackend, skillMiddleware, mcpMgr, exec, subAgentReg, nil)

	// Start server in goroutine — hertz.Run() blocks, so we need to start it
	// in the background and then poll for health.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			errCh <- err
		}
	}()

	// Wait for health check (or startup error)
	baseURL := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	if err := waitForHealth(baseURL, 10*time.Second, errCh); err != nil {
		srv.Stop(context.Background())
		return nil, fmt.Errorf("嵌入服务启动失败: %w", err)
	}

	return srv, nil
}

func waitForHealth(baseURL string, timeout time.Duration, errCh <-chan error) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			return fmt.Errorf("服务启动失败: %w", err)
		default:
		}
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("health 检查超时")
}
