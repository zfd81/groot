package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
)

// StatusFlags holds the parsed flags for the status command
type StatusFlags struct {
	Port int // -p: port to connect to
}

// ParseStatusFlags parses command line arguments for the status command
func ParseStatusFlags(args []string) (*StatusFlags, error) {
	flags := &StatusFlags{}

	i := 0
	for i < len(args) {
		arg := args[i]

		switch arg {
		case "-p":
			if i+1 >= len(args) {
				return nil, errors.New("-p requires a value")
			}
			i++
			var port int
			if _, err := fmt.Sscanf(args[i], "%d", &port); err != nil {
				return nil, fmt.Errorf("invalid value for -p: %s", args[i])
			}
			if port < 1 || port > 65535 {
				return nil, errors.New("port must be 1-65535")
			}
			flags.Port = port
		case "-h", "--help":
			PrintStatusHelp()
			os.Exit(0)
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
			return nil, fmt.Errorf("unexpected argument: %s", arg)
		}
		i++
	}

	return flags, nil
}

// PrintStatusHelp prints help for the status command
func PrintStatusHelp() {
	fmt.Println("用法: groot status [选项]")
	fmt.Println()
	fmt.Println("显示运行中 Groot 实例的状态信息")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -p <port>      指定 Groot 服务端口 (默认从配置读取)")
	fmt.Println("  -h, --help     显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot status            # 查看默认端口实例状态")
	fmt.Println("  groot status -p 9090    # 查看 9090 端口实例状态")
}

// RunStatus is the main entry point for the status command
func RunStatus(flags *StatusFlags) error {
	homeDir := GetDefaultHome()

	// Determine port
	port := flags.Port
	if port == 0 {
		cfg, err := config.Load(homeDir)
		if err != nil {
			return fmt.Errorf("无法加载配置: %w", err)
		}
		port = cfg.Server.Port
	}

	// Fetch health status
	health, err := fetchHealthStatus(port)
	if err != nil {
		printNotRunning(port)
		return nil
	}

	printStatusOutput(health, port)
	return nil
}

// fetchHealthStatus makes an HTTP GET to /health and parses the response
func fetchHealthStatus(port int) (*types.HealthResponse, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var health types.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &health, nil
}

// printNotRunning prints a friendly "instance not found" message
func printNotRunning(port int) {
	fmt.Printf("未检测到运行中的 Groot 实例（端口 %d）\n", port)
	fmt.Println("提示: 请确认 Groot 是否已启动，或使用 -p 指定其他端口")
}

// printStatusOutput formats and prints the health response
func printStatusOutput(health *types.HealthResponse, port int) {
	fmt.Println("Groot 实例状态")
	fmt.Println()
	fmt.Printf("状态:      %s\n", health.Status)
	fmt.Printf("版本:      %s\n", health.Version)
	fmt.Printf("运行时间:  %s\n", health.Uptime)
	fmt.Printf("端口:      %d\n", port)
	fmt.Println()
	fmt.Println("组件状态:")

	// LLM
	llmCheck, ok := health.Checks["llm"]
	if ok {
		modelName := ""
		errMsg := ""
		if info, ok := llmCheck.Info.(map[string]interface{}); ok {
			if m, ok := info["model"].(string); ok {
				modelName = m
			}
			if e, ok := info["error"].(string); ok && e != "" {
				errMsg = e
			}
		}
		detail := modelName
		if errMsg != "" {
			if detail != "" {
				detail += ", "
			}
			detail += errMsg
		}
		if detail != "" {
			detail = " (" + detail + ")"
		}
		fmt.Printf("  %-12s %s%s\n", "LLM:", llmCheck.Status, detail)
	}

	// MCP Servers
	mcpCheck, ok := health.Checks["mcp_servers"]
	if ok {
		count := 0
		if info, ok := mcpCheck.Info.([]interface{}); ok {
			count = len(info)
		}
		fmt.Printf("  %-12s %s (%d 个)\n", "MCP Servers:", mcpCheck.Status, count)
	}

	// Skills
	skillsCheck, ok := health.Checks["skills"]
	if ok {
		count := 0
		if info, ok := skillsCheck.Info.(map[string]interface{}); ok {
			if c, ok := info["count"].(float64); ok {
				count = int(c)
			}
		}
		fmt.Printf("  %-12s %s (%d 个)\n", "Skills:", skillsCheck.Status, count)
	}

	// Memory
	memCheck, ok := health.Checks["memory"]
	if ok {
		sessions := 0
		if info, ok := memCheck.Info.(map[string]interface{}); ok {
			if s, ok := info["sessions"].(float64); ok {
				sessions = int(s)
			}
		}
		fmt.Printf("  %-12s %s (%d 个会话)\n", "Memory:", memCheck.Status, sessions)
	}

	fmt.Println()

	// Running chats
	running := 0
	if chatsRunning, ok := health.Metrics["chats_running"].(float64); ok {
		running = int(chatsRunning)
	}
	fmt.Printf("活跃对话:  %d\n", running)
}
