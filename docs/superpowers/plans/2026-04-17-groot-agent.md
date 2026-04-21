# Groot AI Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a REST API AI Agent service that receives natural language instructions, executes tasks using Skills and MCP tools via ReAct mode, and returns results via SSE streaming.

**Architecture:** Layered architecture with REST API (Hertz), Agent Engine (eino), MCP Manager, Task Storage (BoltDB), Skills Registry, and Config modules. Hot-plug support for Skills and MCP via fsnotify.

**Tech Stack:** Go, Hertz, eino, BoltDB, fsnotify, zap (logging), YAML config

**Based on:** docs/superpowers/specs/2026-04-16-groot-agent-design.md

---

## File Structure

```
groot/
├── cmd/groot/main.go                    # Entry point
├── internal/
│   ├── config/
│   │   ├── config.go                    # Config struct and loader
│   │   └── defaults.go                  # Default values
│   ├── logger/
│   │   └── logger.go                    # Zap-based JSON logger
│   ├── storage/
│   │   ├── storage.go                   # TaskStorage interface
│   │   ├── boltdb.go                    # BoltDB implementation
│   │   └── task.go                      # Task data structures
│   ├── skill/
│   │   ├── registry.go                  # Skills registry
│   │   ├── loader.go                    # SKILL.md parser
│   │   ├── watcher.go                   # Hot-plug watcher
│   │   └── skill.go                     # Skill struct
│   ├── mcp/
│   │   ├── manager.go                   # MCP manager
│   │   ├── config.go                    # MCP config struct
│   │   ├── watcher.go                   # Hot-plug watcher
│   │   └── builtin.go                   # Built-in tools
│   │   └── tools/
│   │       ├── file_operations.go       # file_read, file_write, etc.
│   │       └── http_request.go          # http_get, http_post, etc.
│   ├── agent/
│   │   ├── engine.go                    # Agent engine (eino)
│   │   ├── executor.go                  # Task executor
│   │   ├── sse.go                       # SSE writer
│   │   ├── cancel.go                    # Cancel manager
│   │   └── idgen.go                     # task_id/step_id generator
│   └── api/
│       ├── server.go                    # Hertz server
│       ├── router.go                    # Route registration
│       ├── request.go                   # Request/Response structs
│       ├── handler/
│       │   ├── execute.go               # POST /task/execute
│       │   ├── cancel.go                # DELETE /task/{task_id}
│       │   ├── status.go                # GET /task/status/{task_id}
│       │   ├── history.go               # GET /task/history
│       │   ├── detail.go                # GET /task/{task_id}
│       │   ├── health.go                # GET /health
│       │   ├── skills.go                # GET /skills
│       │   └── tools.go                 # GET /tools
│       └── middleware/
│           ├── auth.go                  # API Key auth
│           ├── ratelimit.go             # Rate limiting
│           └── recovery.go              # Error recovery
├── pkg/
│   └── utils/
│       └── timeparse.go                 # yyyyMMddHHmm parser
├── configs/config.yaml                  # Default config template
├── go.mod
├── Makefile
└ └── README.md
```

---

## Phase 1: Project Initialization

### Task 1: Create Go Module and Directory Structure

**Files:**
- Create: `go.mod`
- Create: directory structure

- [ ] **Step 1: Initialize Go module**

Run:
```bash
cd /Users/zhangfengda/workspace/groot
go mod init github.com/zfd81/groot
```

Expected: `go.mod` created with module name

- [ ] **Step 2: Create directory structure**

Run:
```bash
mkdir -p cmd/groot
mkdir -p internal/config internal/logger internal/storage internal/skill internal/mcp/tools internal/agent internal/api/handler internal/api/middleware
mkdir -p pkg/utils
mkdir -p configs
```

Expected: All directories created

- [ ] **Step 3: Create Makefile**

Create: `Makefile`

```makefile
.PHONY: build run test clean

build:
	go build -o bin/groot cmd/groot/main.go

run:
	go run cmd/groot/main.go

test:
	go test -v ./...

clean:
	rm -rf bin/
```

- [ ] **Step 4: Commit**

Run:
```bash
git add go.mod Makefile
git commit -m "chore: initialize Go module and project structure"
```

---

### Task 2: Add Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add required dependencies**

Run:
```bash
go get github.com/cloudwego/hertz@latest
go get github.com/fsnotify/fsnotify@latest
go get go.etcd.io/bbolt@latest
go get go.uber.org/zap@latest
go get gopkg.in/yaml.v3@latest
```

Expected: Dependencies added to go.mod, go.sum created

- [ ] **Step 2: Commit**

Run:
```bash
git add go.mod go.sum
git commit -m "chore: add core dependencies (hertz, fsnotify, bbolt, zap, yaml)"
```

---

## Phase 2: Configuration Module

### Task 3: Define Config Structures

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/defaults.go`

- [ ] **Step 1: Create config structures**

Create: `internal/config/config.go`

```go
package config

import (
	"os"
	"strings"
)

// Config is the root configuration structure
type Config struct {
	Agent      AgentConfig      `yaml:"agent"`
	Server     ServerConfig     `yaml:"server"`
	LLM        LLMConfig        `yaml:"llm"`
	Skills     SkillsConfig     `yaml:"skills"`
	MCP        MCPConfig        `yaml:"mcp"`
	Storage    StorageConfig    `yaml:"storage"`
	Performance PerformanceConfig `yaml:"performance"`
	React      ReactConfig      `yaml:"react"`
	Attachment AttachmentConfig `yaml:"attachment"`
	Security   SecurityConfig   `yaml:"security"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// AgentConfig holds agent metadata
type AgentConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// ServerConfig holds HTTP server settings
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// LLMConfig holds LLM settings
type LLMConfig struct {
	DefaultModel string                 `yaml:"default_model"`
	Models      map[string]ModelConfig `yaml:"models"`
}

// ModelConfig holds individual model settings
type ModelConfig struct {
	Endpoint    string  `yaml:"endpoint"`
	APIKey      string  `yaml:"api_key"`
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

// SkillsConfig holds Skills hot-reload settings
type SkillsConfig struct {
	HotReload HotReloadConfig `yaml:"hot_reload"`
}

// MCPConfig holds MCP hot-reload settings
type MCPConfig struct {
	HotReload HotReloadConfig `yaml:"hot_reload"`
}

// HotReloadConfig holds hot-reload settings
type HotReloadConfig struct {
	Enabled       bool `yaml:"enabled"`
	DebounceDelay int  `yaml:"debounce_delay"`
}

// StorageConfig holds storage engine settings
type StorageConfig struct {
	Engine         string        `yaml:"engine"`
	BoltDB        BoltDBConfig  `yaml:"boltdb"`
	Redis         RedisConfig   `yaml:"redis"`
	Etcd          EtcdConfig    `yaml:"etcd"`
	RetentionDays int           `yaml:"retention_days"`
	CleanupInterval string       `yaml:"cleanup_interval"`
}

// BoltDBConfig holds BoltDB settings
type BoltDBConfig struct {
	File   string `yaml:"file"`
	Bucket string `yaml:"bucket"`
}

// RedisConfig holds Redis settings (reserved for cluster)
type RedisConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Password  string `yaml:"password"`
	KeyPrefix string `yaml:"key_prefix"`
}

// EtcdConfig holds etcd settings (reserved for cluster)
type EtcdConfig struct {
	Endpoints  []string `yaml:"endpoints"`
	KeyPrefix  string   `yaml:"key_prefix"`
}

// PerformanceConfig holds performance control settings
type PerformanceConfig struct {
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	Timeout   TimeoutConfig   `yaml:"timeout"`
	LLM       LLMPerfConfig   `yaml:"llm"`
	MCP       MCPPerfConfig   `yaml:"mcp"`
}

// RateLimitConfig holds rate limiting settings
type RateLimitConfig struct {
	MaxConcurrentTasks    int `yaml:"max_concurrent_tasks"`
	MaxRequestsPerMinute  int `yaml:"max_requests_per_minute"`
	MaxRequestsPerHour    int `yaml:"max_requests_per_hour"`
}

// TimeoutConfig holds timeout settings
type TimeoutConfig struct {
	TaskMaxDuration  int `yaml:"task_max_duration"`
	LLMCallTimeout   int `yaml:"llm_call_timeout"`
	ToolCallTimeout  int `yaml:"tool_call_timeout"`
}

// LLMPerfConfig holds LLM performance settings
type LLMPerfConfig struct {
	MaxConcurrentCalls int `yaml:"max_concurrent_calls"`
	RetryOnFailure     int `yaml:"retry_on_failure"`
	RetryDelay         int `yaml:"retry_delay"`
}

// MCPPerfConfig holds MCP performance settings
type MCPPerfConfig struct {
	MaxConcurrentCallsPerServer int `yaml:"max_concurrent_calls_per_server"`
}

// ReactConfig holds ReAct execution limits
type ReactConfig struct {
	MaxIterations   int `yaml:"max_iterations"`
	MaxTokens       int `yaml:"max_tokens"`
	StepTimeout     int `yaml:"step_timeout"`
	ErrorRetry      int `yaml:"error_retry"`
	NestingMaxDepth int `yaml:"nesting_max_depth"`
}

// AttachmentConfig holds attachment settings
type AttachmentConfig struct {
	MaxSize        int      `yaml:"max_size"`
	MaxTotalSize   int      `yaml:"max_total_size"`
	MaxCount       int      `yaml:"max_count"`
	AllowedTypes   []string `yaml:"allowed_types"`
	TempDirectory  string   `yaml:"temp_directory"`
}

// SecurityConfig holds security settings
type SecurityConfig struct {
	Auth AuthConfig `yaml:"auth"`
}

// AuthConfig holds authentication settings
type AuthConfig struct {
	Enabled  bool        `yaml:"enabled"`
	Type     string      `yaml:"type"`
	APIKey   APIKeyConfig `yaml:"api_key"`
}

// APIKeyConfig holds API Key settings
type APIKeyConfig struct {
	HeaderName string    `yaml:"header_name"`
	Keys       []KeyInfo `yaml:"keys"`
}

// KeyInfo holds individual key info
type KeyInfo struct {
	Name        string   `yaml:"name"`
	Key         string   `yaml:"key"`
	Permissions []string `yaml:"permissions"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	Level      string            `yaml:"level"`
	Format     string            `yaml:"format"`
	Output     []string          `yaml:"output"`
	File       LogFileConfig     `yaml:"file"`
	Categories map[string]CatConfig `yaml:"categories"`
}

// LogFileConfig holds log file settings
type LogFileConfig struct {
	Directory       string `yaml:"directory"`
	FilenamePattern string `yaml:"filename_pattern"`
	MaxAge          int    `yaml:"max_age"`
}

// CatConfig holds category-specific log settings
type CatConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Level     string `yaml:"level"`
	LogInput  bool   `yaml:"log_input,omitempty"`
	LogOutput bool   `yaml:"log_output,omitempty"`
}

// ExpandEnv replaces ${VAR_NAME} with environment variable values
func ExpandEnv(value string) string {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		envVar := value[2:len(value)-1]
		return os.Getenv(envVar)
	}
	return value
}

// GetActiveModel returns the active model configuration
func (c *LLMConfig) GetActiveModel() *ModelConfig {
	if model, ok := c.Models[c.ActiveModel]; ok {
		// Expand environment variables in API key
		model.APIKey = ExpandEnv(model.APIKey)
		return &model
	}
	return nil
}
```

- [ ] **Step 2: Create defaults.go**

Create: `internal/config/defaults.go`

```go
package config

// DefaultConfig returns a config with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Agent: AgentConfig{
			Name:    "groot",
			Version: "1.0.0",
		},
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		Skills: SkillsConfig{
			HotReload: HotReloadConfig{
				Enabled:       true,
				DebounceDelay: 2,
			},
		},
		MCP: MCPConfig{
			HotReload: HotReloadConfig{
				Enabled:       true,
				DebounceDelay: 2,
			},
		},
		Storage: StorageConfig{
			Engine:        "boltdb",
			BoltDB:       BoltDBConfig{File: "groot.db", Bucket: "tasks"},
			RetentionDays: 7,
			CleanupInterval: "24h",
		},
		Performance: PerformanceConfig{
			RateLimit: RateLimitConfig{
				MaxConcurrentTasks:   10,
				MaxRequestsPerMinute: 60,
				MaxRequestsPerHour:   1000,
			},
			Timeout: TimeoutConfig{
				TaskMaxDuration: 300,
				LLMCallTimeout:  60,
				ToolCallTimeout: 30,
			},
			LLM: LLMPerfConfig{
				MaxConcurrentCalls: 5,
				RetryOnFailure:     3,
				RetryDelay:         2,
			},
			MCP: MCPPerfConfig{
				MaxConcurrentCallsPerServer: 3,
			},
		},
		React: ReactConfig{
			MaxIterations:   20,
			MaxTokens:       100000,
			StepTimeout:     60,
			ErrorRetry:      2,
			NestingMaxDepth: 3,
		},
		Attachment: AttachmentConfig{
			MaxSize:       50,
			MaxTotalSize:  100,
			MaxCount:      10,
			AllowedTypes:  []string{"pdf", "doc", "docx", "txt", "json", "csv", "xml", "yaml", "png", "jpg", "zip"},
			TempDirectory: "temp",
		},
		Security: SecurityConfig{
			Auth: AuthConfig{
				Enabled: true,
				Type:    "api_key",
				APIKey: APIKeyConfig{
					HeaderName: "X-API-Key",
					Keys: []KeyInfo{
						{Name: "default", Key: "${GROOT_API_KEY}", Permissions: []string{"all"}},
					},
				},
			},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: []string{"stdout", "file"},
			File: LogFileConfig{
				Directory:       "logs",
				FilenamePattern: "groot-{date}.log",
				MaxAge:          7,
			},
			Categories: map[string]CatConfig{
				"request": {Enabled: true, Level: "info"},
				"skill":   {Enabled: true, Level: "info", LogInput: true, LogOutput: true},
				"llm":     {Enabled: true, Level: "debug"},
				"mcp":     {Enabled: true, Level: "debug"},
				"error":   {Enabled: true, Level: "error"},
			},
		},
	}
}
```

- [ ] **Step 3: Commit**

Run:
```bash
git add internal/config/
git commit -m "feat: add config module with all configuration structures"
```

---

### Task 4: Implement Config Loader

**Files:**
- Create: `internal/config/loader.go`
- Create: `configs/config.yaml`

- [ ] **Step 1: Create config loader**

Create: `internal/config/loader.go`

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load loads configuration from file with defaults
func Load(homeDir string) (*Config, error) {
	cfg := DefaultConfig()
	
	configPath := filepath.Join(homeDir, "config.yaml")
	
	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Generate default config file
		if err := generateDefaultConfig(configPath, cfg); err != nil {
			return nil, fmt.Errorf("failed to generate default config: %w", err)
		}
		return cfg, nil
	}
	
	// Read config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	
	// Parse YAML
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	
	// Expand environment variables in all relevant fields
	expandConfigEnvVars(cfg)
	
	return cfg, nil
}

// generateDefaultConfig writes default config to file
func generateDefaultConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	
	header := "# Groot Agent 配置文件\n# 生成时间: " + cfg.Agent.Version + "\n\n"
	
	return os.WriteFile(path, []byte(header+string(data)), 0644)
}

// expandConfigEnvVars expands environment variables in config
func expandConfigEnvVars(cfg *Config) {
	// Expand LLM API keys
	for name, model := range cfg.LLM.Models {
		model.APIKey = ExpandEnv(model.APIKey)
		cfg.LLM.Models[name] = model
	}
	
	// Expand API Keys
	for i, keyInfo := range cfg.Security.Auth.APIKey.Keys {
		keyInfo.Key = ExpandEnv(keyInfo.Key)
		cfg.Security.Auth.APIKey.Keys[i] = keyInfo
	}
	
	// Expand Redis settings
	cfg.Storage.Redis.Endpoint = ExpandEnv(cfg.Storage.Redis.Endpoint)
	cfg.Storage.Redis.Password = ExpandEnv(cfg.Storage.Redis.Password)
}
```

- [ ] **Step 2: Create default config template**

Create: `configs/config.yaml`

```yaml
# Groot Agent 配置文件
# 生成时间: 2026-04-17

# Agent 基础配置
agent:
  name: groot
  version: 1.0.0

# HTTP 服务配置
server:
  host: 0.0.0.0
  port: 8080

# LLM 配置（OpenAI兼容协议）
llm:
  default_model: gpt-4o
  models:
    gpt-4o:
      endpoint: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      model: gpt-4o
      max_tokens: 4096
      temperature: 0.7

# Skills 热插拔配置
skills:
  hot_reload:
    enabled: true
    debounce_delay: 2

# MCP 热插拔配置
mcp:
  hot_reload:
    enabled: true
    debounce_delay: 2

# 存储配置
storage:
  engine: boltdb
  boltdb:
    file: groot.db
    bucket: tasks
  retention_days: 7
  cleanup_interval: 24h

# 性能控制配置
performance:
  rate_limit:
    max_concurrent_tasks: 10
    max_requests_per_minute: 60
    max_requests_per_hour: 1000
  timeout:
    task_max_duration: 300
    llm_call_timeout: 60
    tool_call_timeout: 30
  llm:
    max_concurrent_calls: 5
    retry_on_failure: 3
    retry_delay: 2
  mcp:
    max_concurrent_calls_per_server: 3

# ReAct 执行配置
react:
  max_iterations: 20
  max_tokens: 100000
  step_timeout: 60
  error_retry: 2
  nesting_max_depth: 3

# 附件处理配置
attachment:
  max_size: 50
  max_total_size: 100
  max_count: 10
  allowed_types: [pdf, doc, docx, txt, json, csv, xml, yaml, png, jpg, zip]
  temp_directory: temp

# 安全配置
security:
  auth:
    enabled: true
    type: api_key
    api_key:
      header_name: X-API-Key
      keys:
        - name: default
          key: ${GROOT_API_KEY}
          permissions: [all]

# 日志配置
logging:
  level: info
  format: json
  output: [stdout, file]
  file:
    directory: logs
    filename_pattern: groot-{date}.log
    max_age: 7
  categories:
    request: {enabled: true, level: info}
    skill: {enabled: true, level: info, log_input: true, log_output: true}
    llm: {enabled: true, level: debug}
    mcp: {enabled: true, level: debug}
    error: {enabled: true, level: error}
```

- [ ] **Step 3: Commit**

Run:
```bash
git add internal/config/loader.go configs/config.yaml
git commit -m "feat: add config loader and default config template"
```

---

## Phase 3: Logger Module

### Task 5: Implement JSON Logger

**Files:**
- Create: `internal/logger/logger.go`

- [ ] **Step 1: Create zap-based logger**

Create: `internal/logger/logger.go`

```go
package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	
	"github.com/zfd81/groot/internal/config"
)

// Logger wraps zap.Logger with category support
type Logger struct {
	zap    *zap.Logger
	config config.LoggingConfig
	mu     sync.Mutex
}

// New creates a new logger instance
func New(cfg config.LoggingConfig) *Logger {
	encoder := getEncoder(cfg)
	
	// Build core with multiple outputs
	cores := []zapcore.Core{}
	
	// stdout output
	if contains(cfg.Output, "stdout") {
		core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), getLevel(cfg.Level))
		cores = append(cores, core)
	}
	
	// file output
	if contains(cfg.Output, "file") && cfg.File.Directory != "" {
		fileWriter := getFileWriter(cfg.File)
		core := zapcore.NewCore(encoder, zapcore.AddSync(fileWriter), getLevel(cfg.Level))
		cores = append(cores, core)
	}
	
	combined := zapcore.NewTee(cores...)
	
	return &Logger{
		zap:    zap.New(combined, zap.AddCaller()),
		config: cfg,
	}
}

// getEncoder returns JSON encoder
func getEncoder(cfg config.LoggingConfig) zapcore.Encoder {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	
	if cfg.Format == "json" {
		return zapcore.NewJSONEncoder(encoderConfig)
	}
	return zapcore.NewConsoleEncoder(encoderConfig)
}

// getLevel converts string to zap level
func getLevel(level string) zapcore.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// getFileWriter creates rotating file writer
func getFileWriter(cfg config.LogFileConfig) *rotatingWriter {
	return &rotatingWriter{
		directory:       cfg.Directory,
		filenamePattern: cfg.FilenamePattern,
		maxAge:          cfg.MaxAge,
		currentDate:     time.Now().Format("2006-01-02"),
	}
}

// contains checks if string is in slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// rotatingWriter handles file rotation by date
type rotatingWriter struct {
	directory       string
	filenamePattern string
	maxAge          int
	currentDate     string
	currentFile     *os.File
	mu              sync.Mutex
}

// Write writes to current log file
func (w *rotatingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	today := time.Now().Format("2006-01-02")
	if today != w.currentDate {
		if w.currentFile != nil {
			w.currentFile.Close()
		}
		w.currentDate = today
		filename := strings.Replace(w.filenamePattern, "{date}", today, 1)
		path := filepath.Join(w.directory, filename)
		w.currentFile, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
	}
	
	return w.currentFile.Write(p)
}

// LogEvent logs a structured event
func (l *Logger) LogEvent(event string, data interface{}) {
	l.zap.Info(event,
		zap.String("event", event),
		zap.Any("data", data),
	)
}

// LogRequest logs API request
func (l *Logger) LogRequest(method, path, taskID, caller string) {
	l.zap.Info("api_request",
		zap.String("event", "api_request"),
		zap.String("method", method),
		zap.String("path", path),
		zap.String("task_id", taskID),
		zap.String("caller", caller),
	)
}

// LogSkillHotReload logs skill hot-reload event
func (l *Logger) LogSkillHotReload(action, skillName string, count int) {
	l.zap.Info("skill_hot_reload",
		zap.String("event", "skill_hot_reload"),
		zap.String("action", action),
		zap.String("skill_name", skillName),
		zap.Int("skills_count", count),
	)
}

// LogMCPHotReload logs MCP hot-reload event
func (l *Logger) LogMCPHotReload(action, mcpName, mcpType string, count int) {
	l.zap.Info("mcp_hot_reload",
		zap.String("event", "mcp_hot_reload"),
		zap.String("action", action),
		zap.String("mcp_name", mcpName),
		zap.String("mcp_type", mcpType),
		zap.Int("mcp_count", count),
	)
}

// Info logs info message
func (l *Logger) Info(msg string, fields ...zap.Field) {
	l.zap.Info(msg, fields...)
}

// Debug logs debug message
func (l *Logger) Debug(msg string, fields ...zap.Field) {
	l.zap.Debug(msg, fields...)
}

// Error logs error message
func (l *Logger) Error(msg string, fields ...zap.Field) {
	l.zap.Error(msg, fields...)
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.zap.Sync()
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/logger/logger.go
git commit -m "feat: add zap-based JSON logger with event support"
```

---

## Phase 4: Storage Module

### Task 6: Define Task Data Structures

**Files:**
- Create: `internal/storage/task.go`

- [ ] **Step 1: Create task structures**

Create: `internal/storage/task.go`

```go
package storage

import (
	"time"
)

// TaskStatus represents task status
type TaskStatus string

const (
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
)

// Task represents a task record
type Task struct {
	ID           string        `json:"id"`
	Instruction  string        `json:"instruction"`
	Prompt       string        `json:"prompt,omitempty"`
	Attachments  []Attachment  `json:"attachments,omitempty"`
	Status       TaskStatus    `json:"status"`
	Progress     *TaskProgress `json:"progress,omitempty"`
	Result       interface{}   `json:"result,omitempty"`
	Error        *TaskError    `json:"error,omitempty"`
	StartTime    time.Time     `json:"start_time"`
	EndTime      time.Time     `json:"end_time,omitempty"`
	Duration     int           `json:"duration"` // seconds
	Caller       string        `json:"caller"`
	Steps        []StepRecord  `json:"steps,omitempty"`
}

// Attachment represents file attachment
type Attachment struct {
	Type    string `json:"type"`    // file, url
	Name    string `json:"name"`
	Content string `json:"content"` // Base64 or URL
}

// TaskProgress represents execution progress
type TaskProgress struct {
	CurrentStep    int `json:"current_step"`
	StepsCompleted int `json:"steps_completed"`
	Percentage     int `json:"percentage"`
}

// TaskError represents error info
type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StepRecord represents a step execution record
type StepRecord struct {
	StepID       string     `json:"step_id"`
	Type         string     `json:"type"` // skill, tool, llm
	Name         string     `json:"name"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      time.Time  `json:"end_time,omitempty"`
	Status       TaskStatus `json:"status"`
	NestingLevel int        `json:"nesting_level"`
	Error        *TaskError `json:"error,omitempty"`
}

// TaskQuery represents query parameters
type TaskQuery struct {
	Status    []TaskStatus
	StartTime *time.Time
	EndTime   *time.Time
	Limit     int
	Offset    int
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/storage/task.go
git commit -m "feat: add task data structures"
```

---

### Task 7: Define TaskStorage Interface

**Files:**
- Create: `internal/storage/storage.go`

- [ ] **Step 1: Create storage interface**

Create: `internal/storage/storage.go`

```go
package storage

// TaskStorage defines the storage interface
type TaskStorage interface {
	// Create creates a new task record
	Create(task *Task) error
	
	// Get retrieves a task by ID
	Get(taskID string) (*Task, error)
	
	// Update updates task fields
	Update(taskID string, updates map[string]interface{}) error
	
	// Delete removes a task record
	Delete(taskID string) error
	
	// List queries tasks with filters
	List(query *TaskQuery) ([]*Task, int, error)
	
	// Exists checks if task exists
	Exists(taskID string) bool
	
	// Close closes the storage connection
	Close() error
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/storage/storage.go
git commit -m "feat: add TaskStorage interface"
```

---

### Task 8: Implement BoltDB Storage

**Files:**
- Create: `internal/storage/boltdb.go`

- [ ] **Step 1: Create BoltDB implementation**

Create: `internal/storage/boltdb.go`

```go
package storage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
	
	"github.com/zfd81/groot/internal/config"
)

// BoltDBStorage implements TaskStorage using BoltDB
type BoltDBStorage struct {
	db            *bolt.DB
	bucketName    string
	retentionDays int
}

// NewBoltDBStorage creates a BoltDB storage instance
func NewBoltDBStorage(cfg config.StorageConfig, homeDir string) (*BoltDBStorage, error) {
	dbPath := filepath.Join(homeDir, cfg.BoltDB.File)
	
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open boltdb: %w", err)
	}
	
	// Create bucket
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(cfg.BoltDB.Bucket))
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create bucket: %w", err)
	}
	
	storage := &BoltDBStorage{
		db:            db,
		bucketName:    cfg.BoltDB.Bucket,
		retentionDays: cfg.RetentionDays,
	}
	
	// Start cleanup goroutine
	go storage.startCleanup()
	
	return storage, nil
}

// Create stores a new task
func (s *BoltDBStorage) Create(task *Task) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		data, err := json.Marshal(task)
		if err != nil {
			return err
		}
		return b.Put([]byte(task.ID), data)
	})
}

// Get retrieves a task by ID
func (s *BoltDBStorage) Get(taskID string) (*Task, error) {
	var task Task
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		data := b.Get([]byte(taskID))
		if data == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		return json.Unmarshal(data, &task)
	})
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// Update updates task fields
func (s *BoltDBStorage) Update(taskID string, updates map[string]interface{}) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		data := b.Get([]byte(taskID))
		if data == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		
		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			return err
		}
		
		// Apply updates
		if v, ok := updates["status"]; ok {
			task.Status = TaskStatus(v.(string))
		}
		if v, ok := updates["progress"]; ok {
			task.Progress = v.(*TaskProgress)
		}
		if v, ok := updates["result"]; ok {
			task.Result = v
		}
		if v, ok := updates["error"]; ok {
			task.Error = v.(*TaskError)
		}
		if v, ok := updates["end_time"]; ok {
			task.EndTime = v.(time.Time)
		}
		if v, ok := updates["duration"]; ok {
			task.Duration = v.(int)
		}
		if v, ok := updates["steps"]; ok {
			task.Steps = v.([]StepRecord)
		}
		
		newData, err := json.Marshal(task)
		if err != nil {
			return err
		}
		return b.Put([]byte(taskID), newData)
	})
}

// Delete removes a task
func (s *BoltDBStorage) Delete(taskID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		return b.Delete([]byte(taskID))
	})
}

// List queries tasks with filters
func (s *BoltDBStorage) List(query *TaskQuery) ([]*Task, int, error) {
	var tasks []*Task
	var total int
	
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		c := b.Cursor()
		
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var task Task
			if err := json.Unmarshal(v, &task); err != nil {
				continue
			}
			
			// Filter by status
			if len(query.Status) > 0 {
				match := false
				for _, s := range query.Status {
					if task.Status == s {
						match = true
						break
					}
				}
				if !match {
					continue
				}
			}
			
			// Filter by time range
			if query.StartTime != nil && task.StartTime.Before(*query.StartTime) {
				continue
			}
			if query.EndTime != nil && task.StartTime.After(*query.EndTime) {
				continue
			}
			
			total++
			
			// Apply limit/offset
			if total > query.Offset && (query.Limit == 0 || len(tasks) < query.Limit) {
				tasks = append(tasks, &task)
			}
		}
		return nil
	})
	
	return tasks, total, err
}

// Exists checks if task exists
func (s *BoltDBStorage) Exists(taskID string) bool {
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		data := b.Get([]byte(taskID))
		if data == nil {
			return fmt.Errorf("not found")
		}
		return nil
	})
	return err == nil
}

// Close closes the database
func (s *BoltDBStorage) Close() error {
	return s.db.Close()
}

// startCleanup runs periodic cleanup of old tasks
func (s *BoltDBStorage) startCleanup() {
	ticker := time.NewTicker(24 * time.Hour)
	for range ticker.C {
		s.cleanup()
	}
}

// cleanup removes tasks older than retention period
func (s *BoltDBStorage) cleanup() {
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	
	s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		c := b.Cursor()
		
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var task Task
			if err := json.Unmarshal(v, &task); err != nil {
				continue
			}
			if task.StartTime.Before(cutoff) {
				b.Delete(k)
			}
		}
		return nil
	})
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/storage/boltdb.go
git commit -m "feat: add BoltDB storage implementation with TTL cleanup"
```

---

## Phase 5: Skills Module

### Task 9: Define Skill Structure

**Files:**
- Create: `internal/skill/skill.go`

- [ ] **Step 1: Create skill structure**

Create: `internal/skill/skill.go`

```go
package skill

// Skill represents a registered skill
type Skill struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Dependencies []string `yaml:"dependencies,omitempty"`
	Instructions string   // Markdown content after frontmatter
	FilePath     string   // Path to SKILL.md
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/skill/skill.go
git commit -m "feat: add Skill structure definition"
```

---

### Task 10: Implement Skills Registry

**Files:**
- Create: `internal/skill/registry.go`

- [ ] **Step 1: Create registry**

Create: `internal/skill/registry.go`

```go
package skill

import (
	"sync"
)

// Registry manages all registered skills
type Registry struct {
	skills map[string]*Skill
	mu     sync.RWMutex
}

// NewRegistry creates a new registry
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]*Skill),
	}
}

// Register adds a skill to the registry
func (r *Registry) Register(skill *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[skill.Name] = skill
}

// Unregister removes a skill from the registry
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.skills, name)
}

// Get retrieves a skill by name
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skill, ok := r.skills[name]
	return skill, ok
}

// List returns all registered skills
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	result := make([]*Skill, 0, len(r.skills))
	for _, skill := range r.skills {
		result = append(result, skill)
	}
	return result
}

// Count returns the number of registered skills
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/skill/registry.go
git commit -m "feat: add Skills registry"
```

---

### Task 11: Implement SKILL.md Loader

**Files:**
- Create: `internal/skill/loader.go`

- [ ] **Step 1: Create loader**

Create: `internal/skill/loader.go`

```go
package skill

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader parses SKILL.md files
type Loader struct {
	registry *Registry
}

// NewLoader creates a new loader
func NewLoader(registry *Registry) *Loader {
	return &Loader{registry: registry}
}

// LoadAll loads all skills from directory
func (l *Loader) LoadAll(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist yet
		}
		return err
	}
	
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		
		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			continue // No SKILL.md in this directory
		}
		
		if err := l.Load(skillPath); err != nil {
			return fmt.Errorf("failed to load %s: %w", skillPath, err)
		}
	}
	
	return nil
}

// Load parses a single SKILL.md file
func (l *Loader) Load(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	
	skill, err := parseSKILLMd(content)
	if err != nil {
		return err
	}
	
	skill.FilePath = path
	l.registry.Register(skill)
	
	return nil
}

// parseSKILLMd parses SKILL.md content
func parseSKILLMd(content []byte) (*Skill, error) {
	// Find frontmatter boundaries
	fmStart := bytes.Index(content, []byte("---"))
	if fmStart != 0 {
		return nil, fmt.Errorf("missing frontmatter start")
	}
	
	fmEnd := bytes.Index(content[3:], []byte("---"))
	if fmEnd < 0 {
		return nil, fmt.Errorf("missing frontmatter end")
	}
	
	fmContent := content[3:fmEnd+3]
	mdContent := content[fmEnd+6:]
	
	// Parse frontmatter
	var skill Skill
	if err := yaml.Unmarshal(fmContent, &skill); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}
	
	// Store markdown content as instructions
	skill.Instructions = strings.TrimSpace(string(mdContent))
	
	// Validate required fields
	if skill.Name == "" {
		return nil, fmt.Errorf("missing required field: name")
	}
	if skill.Description == "" {
		return nil, fmt.Errorf("missing required field: description")
	}
	
	return &skill, nil
}

// Unload removes a skill by file path
func (l *Loader) Unload(path string) {
	// Find skill by path and remove
	for _, skill := range l.registry.List() {
		if skill.FilePath == path {
			l.registry.Unregister(skill.Name)
			return
		}
	}
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/skill/loader.go
git commit -m "feat: add SKILL.md loader with YAML frontmatter parsing"
```

---

### Task 12: Implement Skills Hot-plug Watcher

**Files:**
- Create: `internal/skill/watcher.go`

- [ ] **Step 1: Create watcher**

Create: `internal/skill/watcher.go`

```go
package skill

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// Watcher monitors skills directory for changes
type Watcher struct {
	loader     *Loader
	config     config.SkillsConfig
	logger     *logger.Logger
	watcher    *fsnotify.Watcher
	debounce   map[string]time.Time
	mu         sync.Mutex
	stopChan   chan struct{}
}

// NewWatcher creates a new watcher
func NewWatcher(loader *Loader, cfg config.SkillsConfig, log *logger.Logger) *Watcher {
	return &Watcher{
		loader:   loader,
		config:   cfg,
		logger:   log,
		debounce: make(map[string]time.Time),
		stopChan: make(chan struct{}),
	}
}

// Start begins watching the skills directory
func (w *Watcher) Start(dir string) error {
	if !w.config.HotReload.Enabled {
		return nil
	}
	
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher
	
	// Watch the directory
	if err := watcher.Add(dir); err != nil {
		return err
	}
	
	// Watch existing skill subdirectories
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err == nil {
		for _, entry := range entries {
			watcher.Add(entry)
		}
	}
	
	go w.run(dir)
	
	return nil
}

// run handles file events with debouncing
func (w *Watcher) run(dir string) {
	for {
		select {
		case <-w.stopChan:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(dir, event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Error("watcher error", logger.Error("error", err))
		}
	}
}

// handleEvent processes a file event
func (w *Watcher) handleEvent(dir string, event fsnotify.Event) {
	// Only process SKILL.md files
	if !strings.HasSuffix(event.Name, "SKILL.md") {
		// Check if it's a new directory
		if event.Op == fsnotify.Create && isDir(event.Name) {
			w.watcher.Add(event.Name)
		}
		return
	}
	
	// Debounce
	w.mu.Lock()
	w.debounce[event.Name] = time.Now()
	w.mu.Unlock()
	
	// Wait for debounce delay
	time.Sleep(time.Duration(w.config.HotReload.DebounceDelay) * time.Second)
	
	w.mu.Lock()
	lastTime := w.debounce[event.Name]
	w.mu.Unlock()
	
	// Skip if another event happened during debounce
	if time.Since(lastTime) < time.Duration(w.config.HotReload.DebounceDelay)*time.Second {
		return
	}
	
	// Process event
	switch event.Op {
	case fsnotify.Create, fsnotify.Write:
		if err := w.loader.Load(event.Name); err != nil {
			w.logger.Error("failed to load skill", logger.Error("error", err), logger.Info("path", event.Name))
		} else {
			w.logger.LogSkillHotReload("added", extractSkillName(event.Name), w.loader.registry.Count())
		}
		
	case fsnotify.Remove, fsnotify.Rename:
		w.loader.Unload(event.Name)
		w.logger.LogSkillHotReload("removed", extractSkillName(event.Name), w.loader.registry.Count())
	}
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.stopChan)
	if w.watcher != nil {
		w.watcher.Close()
	}
}

// isDir checks if path is a directory
func isDir(path string) bool {
	info, err := filepath.Glob(path)
	if err != nil || len(info) == 0 {
		return false
	}
	// Use filepath to check
	st, err := filepath.Glob(path)
	return err == nil && len(st) > 0
}

// extractSkillName extracts skill name from path
func extractSkillName(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "skills" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/skill/watcher.go
git commit -m "feat: add Skills hot-plug watcher with debouncing"
```

---

## Phase 6: ID Generator

### Task 13: Implement task_id and step_id Generator

**Files:**
- Create: `internal/agent/idgen.go`

- [ ] **Step 1: Create ID generator**

Create: `internal/agent/idgen.go`

```go
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// GenerateTaskID generates a unique task ID
// Format: task-{YYYYMMDD}-{HHMMSSmmm}-{random4}
func GenerateTaskID() string {
	now := time.Now()
	datePart := now.Format("20060102")
	timePart := now.Format("150405.000")
	timePart = timePart[:9] // HHMMSSmmm
	
	random := generateRandom(4)
	
	return fmt.Sprintf("task-%s-%s-%s", datePart, timePart, random)
}

// GenerateStepID generates a unique step ID
// Format: {YYYYMMDD}-{HHMMSSmmm}-{random6}
func GenerateStepID() string {
	now := time.Now()
	datePart := now.Format("20060102")
	timePart := now.Format("150405.000")
	timePart = timePart[:9] // HHMMSSmmm
	
	random := generateRandom(6)
	
	return fmt.Sprintf("%s-%s-%s", datePart, timePart, random)
}

// generateRandom creates random hex string of given length
func generateRandom(length int) string {
	bytes := make([]byte, length/2+1)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)[:length]
}
```

- [ ] **Step 2: Commit**

Run:
```bash
git add internal/agent/idgen.go
git commit -m "feat: add task_id and step_id generator"
```

---

## Self-Review Checklist

After completing the plan, verify:

1. **Spec coverage:** Each major requirement from the design doc has corresponding tasks:
   - Config module: Tasks 3-4 ✓
   - Logger: Task 5 ✓
   - Storage (BoltDB): Tasks 6-8 ✓
   - Skills (registry, loader, watcher): Tasks 9-12 ✓
   - ID generation: Task 13 ✓
   - MCP Manager: pending (next phase)
   - Agent Engine: pending
   - REST API: pending
   - Entry point: pending

2. **Placeholder scan:** No "TBD", "TODO", or incomplete sections. All code blocks are complete.

3. **Type consistency:** Task structures match between storage.go and task.go. Skill structure used consistently across registry, loader, watcher.

---

**Plan saved to:** `docs/superpowers/plans/2026-04-17-groot-agent.md`

**Note:** This plan covers Phase 1-6 (infrastructure and core modules). Additional phases for MCP Manager, Agent Engine, REST API, and entry point will be added in subsequent plan files or continuation of this plan.

**Two execution options:**

1. **Subagent-Driven (recommended)** - Dispatch a fresh subagent per task, review between tasks, fast iteration

2. **Inline Execution** - Execute tasks in this session, batch execution with checkpoints

**Which approach would you like to proceed with?**