package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/zfd81/groot/internal/config"
	"gopkg.in/yaml.v3"
)

// TailFlags holds the parsed flags for the tail command
type TailFlags struct {
	NLines  int    // -n: Number of lines to display
	Level   string // -l: Log level filter (error/warn/info/debug)
	Keyword string // -k: Keyword filter
	HomeDir string // -H/--home: Working directory
}

// ParseTailFlags parses command line arguments for the tail command
// args should be the arguments after "tail" subcommand (e.g., ["-n", "100", "-l", "error"])
func ParseTailFlags(args []string) (*TailFlags, error) {
	flags := &TailFlags{
		NLines:  100, // default to 100 lines
		Level:   "",  // no level filter by default
		Keyword: "",  // no keyword filter by default
		HomeDir: "",  // will be set by getDefaultHome if not specified
	}

	i := 0
	for i < len(args) {
		arg := args[i]

		switch arg {
		case "-n":
			if i+1 >= len(args) {
				return nil, errors.New("-n requires a value")
			}
			i++
			var err error
			var n int
			if _, err = fmt.Sscanf(args[i], "%d", &n); err != nil {
				return nil, fmt.Errorf("invalid value for -n: %s", args[i])
			}
			if n <= 0 {
				return nil, errors.New("-n must be a positive integer")
			}
			flags.NLines = n

		case "-l":
			if i+1 >= len(args) {
				return nil, errors.New("-l requires a value")
			}
			i++
			level, err := validateLevel(args[i])
			if err != nil {
				return nil, err
			}
			flags.Level = level

		case "-k":
			if i+1 >= len(args) {
				return nil, errors.New("-k requires a value")
			}
			i++
			flags.Keyword = args[i]

		case "-H", "--home":
			if i+1 >= len(args) {
				return nil, errors.New("-H/--home requires a value")
			}
			i++
			flags.HomeDir = args[i]

		case "-h", "--help":
			PrintTailHelp()
			os.Exit(0)

		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
			// Unknown positional argument
			return nil, fmt.Errorf("unexpected argument: %s", arg)
		}
		i++
	}

	// Set default home directory if not specified
	if flags.HomeDir == "" {
		flags.HomeDir = getDefaultHome()
	}

	return flags, nil
}

// validateLevel normalizes and validates the log level
// Returns normalized level or error if invalid
// Normalization: error/err -> error, warn/warning -> warn, info -> info, debug -> debug
func validateLevel(level string) (string, error) {
	lowerLevel := strings.ToLower(level)

	switch lowerLevel {
	case "error", "err":
		return "error", nil
	case "warn", "warning":
		return "warn", nil
	case "info":
		return "info", nil
	case "debug":
		return "debug", nil
	default:
		return "", fmt.Errorf("invalid level: %s (valid levels: error, warn, info, debug)", level)
	}
}

// LogConfig holds the log-related configuration
type LogConfig struct {
	Logging struct {
		File struct {
			Directory string `yaml:"directory"`
		} `yaml:"file"`
	} `yaml:"logging"`
}

// loadConfig reads config.yaml from homeDir and parses it into LogConfig
// If the file doesn't exist, returns default config
func loadConfig(homeDir string) (*LogConfig, error) {
	configPath := filepath.Join(homeDir, "config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return getDefaultLogConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg LogConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
}

// getDefaultLogConfig returns a LogConfig with default values
func getDefaultLogConfig() *LogConfig {
	return &LogConfig{
		Logging: struct {
			File struct {
				Directory string `yaml:"directory"`
			} `yaml:"file"`
		}{
			File: struct {
				Directory string `yaml:"directory"`
			}{
				Directory: "logs",
			},
		},
	}
}

// resolveLogDir resolves the log directory to an absolute path
// If the directory in config is empty, uses "logs" as default
func resolveLogDir(cfg *LogConfig, homeDir string) string {
	dir := cfg.Logging.File.Directory
	if dir == "" {
		dir = "logs"
	}
	return config.ResolvePath(dir, homeDir)
}

// getDefaultHome returns the default groot home directory
// Priority: GROOT_HOME env var > ~/.groot
func getDefaultHome() string {
	homeDir := os.Getenv("GROOT_HOME")
	if homeDir != "" {
		return homeDir
	}

	home := os.Getenv("HOME")
	if home == "" {
		// Fallback for Windows
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		// Last resort
		return ".groot"
	}

	return filepath.Join(home, ".groot")
}

// RunTail is the main entry point for the tail command
// It reads log files, applies filters, and displays formatted output
func RunTail(flags *TailFlags) error {
	// 1. Load config
	cfg, err := loadConfig(flags.HomeDir)
	if err != nil {
		return fmt.Errorf("无法加载配置: %w", err)
	}

	// 2. Get log directory
	logDir := resolveLogDir(cfg, flags.HomeDir)

	// 3. Find latest log file
	logFile, err := findLatestLogFile(logDir)
	if err != nil {
		return err
	}

	// 4. Create formatter and filter
	formatter := NewFormatter()
	filter := NewFilter(flags.Level, flags.Keyword)

	// 5. Show history if -n specified
	if flags.NLines > 0 {
		lines, err := readLastNLines(logFile, flags.NLines)
		if err != nil {
			return fmt.Errorf("读取历史日志失败: %w", err)
		}
		for _, line := range lines {
			if filter.Match(line) {
				fmt.Println(formatter.Format(line))
			}
		}
	}

	// 6. Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\n退出...")
		cancel()
	}()

	// 7. Create and start watcher
	watcher := NewFileWatcher(ctx, logDir, logFile, formatter, filter)

	fmt.Printf("跟踪日志文件: %s\n", logFile)
	fmt.Println("按 Ctrl+C 退出")
	fmt.Println("----------------------------------------")

	return watcher.Start()
}

// PrintTailHelp prints the help message for the tail command
func PrintTailHelp() {
	fmt.Println("Groot Tail - 实时日志查看")
	fmt.Println()
	fmt.Println("用法: groot tail [选项]")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -n <N>           显示最近 N 行日志 (默认 100)")
	fmt.Println("  -l <level>       按日志级别过滤 (error/warn/info/debug)")
	fmt.Println("  -k <keyword>     按关键词过滤")
	fmt.Println("  -H, --home <dir> 工作目录 (默认 ~/.groot)")
	fmt.Println("  -h, --help       显示帮助")
	fmt.Println()
	fmt.Println("日志级别:")
	fmt.Println("  error, err       错误级别")
	fmt.Println("  warn, warning    警告级别")
	fmt.Println("  info             信息级别")
	fmt.Println("  debug            调试级别")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot tail                    # 显示最近 100 行日志")
	fmt.Println("  groot tail -n 50              # 显示最近 50 行日志")
	fmt.Println("  groot tail -l error           # 只显示错误级别日志")
	fmt.Println("  groot tail -k \"timeout\"      # 过滤包含 timeout 的日志")
	fmt.Println("  groot tail -l warn -n 200     # 显示最近 200 行警告级别日志")
}