package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/plugin"
	"gopkg.in/yaml.v3"
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

// resolveStatusPort 解析服务端口：bootstrap.yaml 优先，缺失时回落老
// config.yaml（服务从未启动过的老部署），两者都没有时用默认端口。
// status 只是探活，不因配置文件缺失而失败。
func resolveStatusPort(homeDir string) (int, error) {
	if b, err := config.LoadBootstrap(homeDir); err == nil {
		return b.Server.Port, nil
	}
	data, err := os.ReadFile(filepath.Join(homeDir, "config.yaml"))
	if err != nil {
		return 8080, nil
	}
	var legacy struct {
		Server struct {
			Port int `yaml:"port"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil || legacy.Server.Port == 0 {
		return 8080, nil
	}
	return legacy.Server.Port, nil
}

// RunStatus is the main entry point for the status command
func RunStatus(flags *StatusFlags) error {
	homeDir := GetDefaultHome()

	// Determine port
	port := flags.Port
	if port == 0 {
		port, _ = resolveStatusPort(homeDir)
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

// fetchHealthStatus makes an HTTP GET to /web/health and parses the response
func fetchHealthStatus(port int) (*types.HealthResponse, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/web/health", port)

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
	// 进程模式与 PID（environment 检查项；旧版后端缺失时跳过）
	if env, ok := health.Checks["environment"]; ok {
		if info, ok := env.Info.(map[string]interface{}); ok {
			if mode, ok := info["process_mode"].(string); ok && mode != "" {
				fmt.Printf("进程模式:  %s\n", processModeLabel(mode))
			}
			if pid, ok := info["pid"].(string); ok && pid != "" {
				fmt.Printf("PID:       %s\n", pid)
			}
		}
	}
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

	// Python 插件（旧版后端缺失 plugins 项时跳过）
	if pluginsCheck, ok := health.Checks["plugins"]; ok {
		renderPlugins(os.Stdout, pluginsCheck)
	}

	fmt.Println()

	// Running chats
	running := 0
	if chatsRunning, ok := health.Metrics["chats_running"].(float64); ok {
		running = int(chatsRunning)
	}
	fmt.Printf("活跃对话:  %d\n", running)
}

// processModeLabel 把健康检查的 process_mode 换成中文说明；未知值原样返回。
func processModeLabel(mode string) string {
	switch mode {
	case "supervised":
		return "监督进程 + 工作进程（支持重启）"
	case "single":
		return "单进程（不支持重启）"
	default:
		return mode
	}
}

// renderPlugins 渲染 Python 插件检查项：先输出汇总行，再逐行列出非 running 的插件。
// 格式见设计文档 1.6.5。
func renderPlugins(w io.Writer, check types.CheckInfo) {
	st, ok := pluginStatusFromInfo(check.Info)
	if !ok {
		// 无法解析状态时退化为一句话，避免整段缺失。
		fmt.Fprintf(w, "  %-12s %s\n", "Plugins:", check.Status)
		return
	}

	fmt.Fprintf(w, "  %-12s %s %s\n", "Plugins:", check.Status, pluginsSummary(st))
	for _, p := range st.Plugins {
		if p.Status == "running" {
			continue
		}
		if p.Error != "" {
			fmt.Fprintf(w, "    %-12s%-11s%s\n", p.Name, p.Status, p.Error)
		} else {
			fmt.Fprintf(w, "    %-12s%s\n", p.Name, p.Status)
		}
	}
}

// pluginsSummary 生成插件汇总括号内容：
//   - 没有插件：(0 个)
//   - 进程 running：(运行中/总数 运行中, 进程 pid <pid>)
//   - 进程非 running：(进程 <status>: <error>, 已重启 <n> 次)
func pluginsSummary(st plugin.Status) string {
	if len(st.Plugins) == 0 {
		return "(0 个)"
	}
	if st.Process.Status == "running" {
		running := 0
		for _, p := range st.Plugins {
			if p.Status == "running" {
				running++
			}
		}
		return fmt.Sprintf("(%d/%d 运行中, 进程 pid %d)", running, len(st.Plugins), st.Process.PID)
	}
	detail := st.Process.Status
	if st.Process.Error != "" {
		detail += ": " + st.Process.Error
	}
	return fmt.Sprintf("(进程 %s, 已重启 %d 次)", detail, st.Process.Restarts)
}

// pluginStatusFromInfo 把检查项的 Info 还原为 plugin.Status。
// 直接注入时为结构体值；经 HTTP JSON 往返后为 map，需重新序列化再解析。
func pluginStatusFromInfo(info interface{}) (plugin.Status, bool) {
	switch v := info.(type) {
	case plugin.Status:
		return v, true
	case *plugin.Status:
		if v == nil {
			return plugin.Status{}, false
		}
		return *v, true
	case map[string]interface{}:
		data, err := json.Marshal(v)
		if err != nil {
			return plugin.Status{}, false
		}
		var st plugin.Status
		if err := json.Unmarshal(data, &st); err != nil {
			return plugin.Status{}, false
		}
		return st, true
	default:
		return plugin.Status{}, false
	}
}
