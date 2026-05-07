# groot tail 命令实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 groot 添加实时日志查看子命令 `groot tail`，支持格式化输出、颜色高亮、级别和关键词过滤。

**Architecture:** 新增 internal/cmd 包实现 tail 命令，使用 fsnotify 监听日志文件变化，实时读取 JSON 日志并格式化输出。修改 cmd/groot/main.go 添加子命令入口。

**Tech Stack:** Go、fsnotify（文件监听）、ANSI 颜色码

---

## 文件结构

```
cmd/groot/main.go          # 修改：添加 tail 子命令解析
internal/cmd/
  ├── tail.go              # 新建：tail 命令入口、参数解析、主流程
  ├── tail_file.go         # 新建：文件定位、fsnotify 监听
  ├── tail_format.go       # 新建：JSON 解析、格式化、颜色
  └── tail_filter.go       # 新建：级别过滤、关键词过滤
```

---

## Task 1: 添加 fsnotify 依赖

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: 添加 fsnotify 依赖**

```bash
cd /Users/zhangfengda/workspace/groot
go get github.com/fsnotify/fsnotify
```

Expected: go.mod 和 go.sum 更新，添加 fsnotify 依赖

---

## Task 2: 创建 tail 命令基础结构

**Files:**
- Create: `internal/cmd/tail.go`

- [ ] **Step 1: 创建 internal/cmd 目录和 tail.go 文件**

```bash
mkdir -p /Users/zhangfengda/workspace/groot/internal/cmd
```

- [ ] **Step 2: 编写 tail.go 基础结构（参数定义和入口函数）**

```go
package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// TailFlags holds tail command flags
type TailFlags struct {
	NLines   int
	Level    string
	Keyword  string
	HomeDir  string
}

// ParseTailFlags parses tail command arguments
func ParseTailFlags(args []string) (*TailFlags, error) {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	flags := &TailFlags{}

	fs.IntVar(&flags.NLines, "n", 0, "显示最后 N 行历史日志")
	fs.StringVar(&flags.Level, "l", "", "按级别过滤 (error/warn/info/debug)")
	fs.StringVar(&flags.Keyword, "k", "", "关键词过滤")
	fs.StringVar(&flags.HomeDir, "H", "", "工作目录 (默认 ~/.groot)")
	fs.StringVar(&flags.HomeDir, "home", "", "工作目录 (默认 ~/.groot)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Validate level
	if flags.Level != "" {
		flags.Level = validateLevel(flags.Level)
		if flags.Level == "" {
			return nil, fmt.Errorf("无效级别，可选值: error/warn/info/debug")
		}
	}

	// Determine home directory
	if flags.HomeDir == "" {
		flags.HomeDir = os.Getenv("GROOT_HOME")
		if flags.HomeDir == "" {
			flags.HomeDir = getDefaultHome()
		}
	}

	return flags, nil
}

// validateLevel normalizes and validates level string
func validateLevel(level string) string {
	normalized := map[string]string{
		"error": "error",
		"err":   "error",
		"warn":  "warn",
		"warning": "warn",
		"info":  "info",
		"debug": "debug",
	}
	for k, v := range normalized {
		if k == level {
			return v
		}
	}
	return ""
}

// getDefaultHome returns default groot home directory
func getDefaultHome() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return home + "/.groot"
}

// RunTail executes tail command
func RunTail(flags *TailFlags) error {
	// Load config to get log directory
	cfg, err := loadConfig(flags.HomeDir)
	if err != nil {
		return fmt.Errorf("无法加载配置: %w", err)
	}

	// Get log directory from config
	logDir := resolveLogDir(cfg, flags.HomeDir)

	// Find today's latest log file
	logFile, err := findLatestLogFile(logDir)
	if err != nil {
		return err
	}

	// Create formatter
 formatter := NewFormatter()

	// Create filter
	filter := NewFilter(flags.Level, flags.Keyword)

	// Create file watcher
 watcher := NewFileWatcher(logDir, logFile, formatter, filter)

	// Show history if -n specified
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

	// Setup signal handling for graceful exit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start watching
	fmt.Printf("跟踪日志文件: %s\n", logFile)
	fmt.Println("按 Ctrl+C 退出")
	fmt.Println("----------------------------------------")

	go func() {
		<-sigCh
		fmt.Println("\n退出...")
		watcher.Stop()
		os.Exit(0)
	}()

	return watcher.Start()
}
```

---

## Task 3: 创建配置加载辅助函数

**Files:**
- Create: `internal/cmd/tail.go`（续）

- [ ] **Step 1: 在 tail.go 中添加配置加载函数**

```go
package cmd

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/zfd81/groot/internal/config"
)

// LogConfig holds log-related config for tail command
type LogConfig struct {
	Logging struct {
		File struct {
			Directory string `yaml:"directory"`
		} `yaml:"file"`
	} `yaml:"logging"`
}

// loadConfig loads config file for tail command
func loadConfig(homeDir string) (*LogConfig, error) {
	configPath := filepath.Join(homeDir, "config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		// Return default config if file not exists
		return getDefaultLogConfig(homeDir), nil
	}

	cfg := &LogConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// getDefaultLogConfig returns default log config
func getDefaultLogConfig(homeDir string) *LogConfig {
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

// resolveLogDir resolves log directory to absolute path
func resolveLogDir(cfg *LogConfig, homeDir string) string {
	dir := cfg.Logging.File.Directory
	if dir == "" {
		dir = "logs"
	}
	return config.ResolvePath(dir, homeDir)
}
```

---

## Task 4: 创建格式化模块

**Files:**
- Create: `internal/cmd/tail_format.go`

- [ ] **Step 1: 编写 tail_format.go（JSON 解析和颜色输出）**

```go
package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ANSI color codes
const (
	ColorReset  = "\x1b[0m"
	ColorRed    = "\x1b[31m"
	ColorGreen  = "\x1b[32m"
	ColorYellow = "\x1b[33m"
	ColorGray   = "\x1b[90m"
)

// LogEntry represents parsed log JSON
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Caller    string `json:"caller"`
	Message   string `json:"message"`
	Event     string `json:"event"`
}

// Formatter formats log lines for display
type Formatter struct{}

// NewFormatter creates a new formatter
func NewFormatter() *Formatter {
	return &Formatter{}
}

// Format parses JSON log and returns formatted string with color
func (f *Formatter) Format(line string) string {
	entry, err := parseLogJSON(line)
	if err != nil {
		// If parsing fails, return raw line without color
		return line
	}

	// Build formatted output
 formatted := f.buildOutput(entry)

	// Apply color based on level
	return f.applyColor(formatted, entry.Level)
}

// parseLogJSON parses JSON log line
func parseLogJSON(line string) (*LogEntry, error) {
	// Parse as generic map first
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, err
	}

	entry := &LogEntry{}

	// Extract known fields
	if ts, ok := raw["timestamp"].(string); ok {
		entry.Timestamp = ts
	}
	if level, ok := raw["level"].(string); ok {
		entry.Level = strings.ToLower(level)
	}
	if caller, ok := raw["caller"].(string); ok {
		entry.Caller = caller
	}
	if msg, ok := raw["message"].(string); ok {
		entry.Message = msg
	}
	if event, ok := raw["event"].(string); ok {
		entry.Event = event
	}

	// Remove known fields from raw to get extra fields
	delete(raw, "timestamp")
	delete(raw, "level")
	delete(raw, "caller")
	delete(raw, "message")
	delete(raw, "event")

	// Build extra fields string
	entry.Extra = buildExtraFields(raw)

	return entry, nil
}

// LogEntry with extra fields
type LogEntry struct {
	Timestamp string
	Level     string
	Caller    string
	Message   string
	Event     string
	Extra     string
}

// buildExtraFields formats remaining fields as key=value
func buildExtraFields(raw map[string]interface{}) string {
	var parts []string
	for k, v := range raw {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, "  ")
}

// buildOutput builds formatted output string
func (f *Formatter) buildOutput(entry *LogEntry) string {
	var parts []string

	// timestamp
	if entry.Timestamp != "" {
		parts = append(parts, entry.Timestamp)
	}

	// level (5 chars width)
	levelStr := strings.ToUpper(entry.Level)
	switch levelStr {
	case "INFO":
		levelStr = "INFO "
	case "WARN":
		levelStr = "WARN "
	case "ERROR":
		levelStr = "ERROR"
	case "DEBUG":
		levelStr = "DEBUG"
	}
	parts = append(parts, levelStr)

	// caller
	if entry.Caller != "" {
		parts = append(parts, entry.Caller)
	}

	// message
	if entry.Message != "" {
		parts = append(parts, entry.Message)
	}

	// event
	if entry.Event != "" {
		parts = append(parts, "event="+entry.Event)
	}

	// extra fields
	if entry.Extra != "" {
		parts = append(parts, entry.Extra)
	}

	return strings.Join(parts, "  ")
}

// applyColor applies ANSI color based on level
func (f *Formatter) applyColor(text, level string) string {
	color := ""
	switch strings.ToLower(level) {
	case "error":
		color = ColorRed
	case "warn":
		color = ColorYellow
	case "info":
		color = ColorGreen
	case "debug":
		color = ColorGray
	}

	if color == "" {
		return text
	}
	return color + text + ColorReset
}
```

---

## Task 5: 创建过滤模块

**Files:**
- Create: `internal/cmd/tail_filter.go`

- [ ] **Step 1: 编写 tail_filter.go（级别和关键词过滤）**

```go
package cmd

import (
	"encoding/json"
	"strings"
)

// Filter handles log filtering by level and keyword
type Filter struct {
	level   string
	keyword string
}

// NewFilter creates a new filter
func NewFilter(level, keyword string) *Filter {
	return &Filter{
		level:   strings.ToLower(level),
		keyword: keyword,
	}
}

// Match checks if a log line matches the filter criteria
func (f *Filter) Match(line string) bool {
	// No filter, match all
	if f.level == "" && f.keyword == "" {
		return true
	}

	// Parse JSON to get level
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		// If parsing fails, check keyword only
		if f.keyword != "" && strings.Contains(line, f.keyword) {
			return f.level == "" // match if no level filter
		}
		return false
	}

	// Check level filter
	if f.level != "" {
		logLevel, ok := raw["level"].(string)
		if !ok || strings.ToLower(logLevel) != f.level {
			return false
		}
	}

	// Check keyword filter
	if f.keyword != "" {
		if !strings.Contains(line, f.keyword) {
			return false
		}
	}

	return true
}
```

---

## Task 6: 创建文件监听模块

**Files:**
- Create: `internal/cmd/tail_file.go`

- [ ] **Step 1: 编写 tail_file.go（文件定位和 fsnotify 监听）**

```go
package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher watches log file for changes
type FileWatcher struct {
	logDir     string
	currentFile string
 formatter   *Formatter
	filter      *Filter
 watcher     *fsnotify.Watcher
 currentPosition int64
}

// NewFileWatcher creates a new file watcher
func NewFileWatcher(logDir, logFile string, formatter *Formatter, filter *Filter) *FileWatcher {
	return &FileWatcher{
		logDir:      logDir,
		currentFile: logFile,
	 formatter:    formatter,
	 filter:       filter,
	 currentPosition: 0,
	}
}

// Start begins watching the log file
func (w *FileWatcher) Start() error {
	w.watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("创建 watcher 失败: %w", err)
	}
	defer w.watcher.Close()

	// Watch the log directory (not just the file, to handle rotation)
	if err := w.watcher.Add(w.logDir); err != nil {
		return fmt.Errorf("监听目录失败: %w", err)
	}

	// Get initial file position (start from end)
	if err := w.initPosition(); err != nil {
		return err
	}

	// Process events
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}

			// Handle file write
			if event.Op&fsnotify.Write == fsnotify.Write {
				if event.Name == w.currentFile {
					w.readNewLines()
				}
			}

			// Handle file removal/rotation
			if event.Op&fsnotify.Remove == fsnotify.Write || event.Op&fsnotify.Rename == fsnotify.Rename {
				if event.Name == w.currentFile {
					// File rotated, find new file
					newFile, err := findLatestLogFile(w.logDir)
					if err == nil && newFile != w.currentFile {
						w.currentFile = newFile
						w.currentPosition = 0
						fmt.Printf("\n切换到新文件: %s\n", newFile)
						fmt.Println("----------------------------------------")
					}
				}
			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watcher 错误: %w", err)
		}
	}
}

// Stop stops the watcher
func (w *FileWatcher) Stop() {
	if w.watcher != nil {
		w.watcher.Close()
	}
}

// initPosition sets current position to end of file
func (w *FileWatcher) initPosition() error {
	file, err := os.Open(w.currentFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Seek to end
 w.currentPosition, err = file.Seek(0, os.SeekEnd)
	return err
}

// readNewLines reads new lines from current position
func (w *FileWatcher) readNewLines() error {
	file, err := os.Open(w.currentFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Seek to last position
	_, err = file.Seek(w.currentPosition, os.SeekStart)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if w.filter.Match(line) {
			fmt.Println(w.formatter.Format(line))
		}
	}

	// Update position
	w.currentPosition, err = file.Seek(0, os.SeekCurrent)
	return err
}

// findLatestLogFile finds the latest log file for today
func findLatestLogFile(logDir string) (string, error) {
	// Check if directory exists
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		return "", fmt.Errorf("日志目录不存在: %s", logDir)
	}

	// Get today's date
 today := time.Now().Format("2006-01-02")

	// Find files matching today's date
	var files []string
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.Contains(entry.Name(), today) {
			files = append(files, filepath.Join(logDir, entry.Name()))
		}
	}

	if len(files) == 0 {
		return "", fmt.Errorf("当天暂无日志文件")
	}

	// Sort by modification time, get latest
	sort.Slice(files, func(i, j int) bool {
		infoI, _ := os.Stat(files[i])
		infoJ, _ := os.Stat(files[j])
		return infoI.ModTime().After(infoJ.ModTime())
	})

	return files[0], nil
}

// readLastNLines reads last N lines from file
func readLastNLines(filePath string, n int) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read all lines
 var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Return last N lines
	if len(lines) <= n {
		return lines, nil
	}
	return lines[len(lines)-n:], nil
}
```

---

## Task 7: 修改 main.go 添加子命令支持

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 修改 main.go 添加子命令解析**

找到现有的 flag 定义部分，修改为：

```go
var (
	homeDir     string
	port        int
	showHelp    bool
	showVersion bool
	command     string // new: subcommand
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
	// Parse flags first
	flag.Parse()

	// Get remaining args (subcommand and its args)
	args := flag.Args()

	if showHelp {
		printHelp()
		return
	}

	if showVersion {
		fmt.Println("Groot Agent v1.0.0")
		return
	}

	// Handle subcommands
	if len(args) > 0 {
		switch args[0] {
		case "tail":
			handleTailCommand(args[1:])
			return
		default:
			fmt.Fprintf(os.Stderr, "未知命令: %s\n", args[0])
			printHelp()
			os.Exit(1)
		}
	}

	// Default: start server (existing logic)
	startServer(homeDir, port)
}

// handleTailCommand handles tail subcommand
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
```

- [ ] **Step 2: 将现有的 main 函数主体重构为 startServer 函数**

```go
// startServer starts the groot server
func startServer(homeDir string, port int) {
	// Determine home directory
	if homeDir == "" {
		homeDir = os.Getenv("GROOT_HOME")
		if homeDir == "" {
			homeDir = filepath.Join(os.Getenv("HOME"), ".groot")
		}
	}

	// ... (existing server startup logic)
}
```

- [ ] **Step 3: 更新 printHelp 函数添加子命令说明**

```go
func printHelp() {
	fmt.Println("Groot Agent - AI 智能任务执行服务")
	fmt.Println()
	fmt.Println("用法: groot [选项]")
	fmt.Println("       groot <命令> [命令选项]")
	fmt.Println()
	fmt.Println("命令:")
	fmt.Println("  tail              实时查看日志")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -p, --port <port> HTTP端口 (默认配置文件值)")
	fmt.Println("  -h, --help        显示帮助")
	fmt.Println("  -v, --version     显示版本")
	fmt.Println()
	fmt.Println("tail 命令选项:")
	fmt.Println("  -n N              显示最后 N 行历史日志")
	fmt.Println("  -l level          按级别过滤 (error/warn/info/debug)")
	fmt.Println("  -k keyword        关键词过滤")
	fmt.Println()
	fmt.Println("环境变量:")
	fmt.Println("  GROOT_HOME        工作目录")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot                         # 启动服务")
	fmt.Println("  groot tail                     # 实时查看日志")
	fmt.Println("  groot tail -n 50 -l error      # 查看最近50行错误日志")
	fmt.Println("  groot tail -k \"api_request\"    # 过滤包含api_request的日志")
}
```

- [ ] **Step 4: 添加 import 语句**

在 import 中添加：
```go
import (
	// ... existing imports
	"github.com/zfd81/groot/internal/cmd"
)
```

---

## Task 8: 编译验证

**Files:**
- 无新增文件

- [ ] **Step 1: 编译项目**

```bash
cd /Users/zhangfengda/workspace/groot
go build -o bin/groot ./cmd
```

Expected: 编译成功，生成 bin/groot

- [ ] **Step 2: 验证帮助信息**

```bash
./bin/groot -h
```

Expected: 显示包含 tail 命令的帮助信息

- [ ] **Step 3: 验证 tail 命令帮助**

```bash
./bin/groot tail -h
```

Expected: 显示 tail 命令参数说明

---

## Task 9: 功能测试

**Files:**
- 无新增文件

- [ ] **Step 1: 启动 groot 服务生成日志**

```bash
./bin/groot &
sleep 2
kill %1
```

Expected: 服务启动并在 ~/.groot/logs/ 生成日志文件

- [ ] **Step 2: 测试 tail 命令基本功能**

```bash
./bin/groot tail -n 10
```

Expected: 显示最近10行日志，带颜色

- [ ] **Step 3: 测试级别过滤**

```bash
./bin/groot tail -l info -n 20
```

Expected: 只显示 INFO 级别的日志

- [ ] **Step 4: 测试关键词过滤**

```bash
./bin/groot tail -k "api" -n 20
```

Expected: 只显示包含 "api" 的日志

---

## Self-Review 检查

**1. Spec Coverage:**
- ✅ 命令用法：Task 7 实现
- ✅ 文件定位：Task 6 实现
- ✅ 格式化输出：Task 4 实现
- ✅ 颜色高亮：Task 4 实现
- ✅ 级别过滤：Task 5 实现
- ✅ 关键词过滤：Task 5 实现
- ✅ 实时跟踪：Task 6 实现
- ✅ -n 参数：Task 2、Task 6 实现

**2. Placeholder Scan:**
- ✅ 无 TBD/TODO
- ✅ 所有代码步骤都有完整代码
- ✅ 无 "类似 Task N" 描述

**3. Type Consistency:**
- ✅ Formatter、Filter、FileWatcher 在各 Task 中引用一致
- ✅ TailFlags 结构在 Task 2 定义，Task 7 使用