package config

import (
	"os"
	"strings"
)

// Config is the root configuration structure
type Config struct {
	Agent      AgentConfig      `yaml:"agent"`
	Server     ServerConfig     `yaml:"server"`
	Memory     MemoryConfig     `yaml:"memory"`
	React      ReactConfig      `yaml:"react"`
	Attachment AttachmentConfig `yaml:"attachment"`
	Schedule   ScheduleConfig   `yaml:"schedule"`
	Message    MessageConfig    `yaml:"message"`
	SubAgent   SubAgentConfig   `yaml:"subagent"`
	Security   SecurityConfig   `yaml:"security"`
	Logging    LoggingConfig    `yaml:"logging"`
	Database   *DatabaseConfig  `yaml:"-"` // loaded from env.yaml, not config.yaml
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

// MemoryConfig 记忆模块配置
type MemoryConfig struct {
	HistoryWindow int `yaml:"history_window"` // LLM 上下文窗口（轮次），-1 不限制
}

// ScheduleConfig 定时任务调度配置
type ScheduleConfig struct {
	Enabled            bool   `yaml:"enabled"`              // 是否允许在对话中创建定时任务（默认关闭）
	MaxConcurrentTasks int    `yaml:"max_concurrent_tasks"` // 最大并发执行数
	SyncInterval       string `yaml:"sync_interval"`        // 目录同步间隔
}

// MessageConfig 消息通知配置
type MessageConfig struct {
	QueueSize int                   `yaml:"queue_size"` // 发送队列容量
	Workers   int                   `yaml:"workers"`    // 发送工作协程数
	Senders   map[string]SenderConf `yaml:"senders"`    // 发送器配置
}

// SenderConf 单个发送器配置
type SenderConf struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url,omitempty"`
	SMTPHost string `yaml:"smtp_host,omitempty"`
	SMTPPort int    `yaml:"smtp_port,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
	From     string `yaml:"from,omitempty"`
}

// SubAgentConfig 子 Agent 调度配置
type SubAgentConfig struct {
	MaxConcurrency  int    `yaml:"max_concurrency"`   // 全局 semaphore 大小
	ExecTimeout     string `yaml:"exec_timeout"`      // 排队结束后开始计时，e.g. "5m"
	MaxTaskLength   int    `yaml:"max_task_length"`   // task 参数最大字符数
	MaxResultLength int    `yaml:"max_result_length"` // 子 Agent 返回文本截断长度
}

// ReactConfig holds ReAct execution limits
type ReactConfig struct {
	MaxIterations int `yaml:"max_iterations"` // ReAct 循环最大迭代次数
	StepTimeout   int `yaml:"step_timeout"`   // 单步 LLM 调用超时（秒）
	ErrorRetry    int `yaml:"error_retry"`    // 单步 LLM 调用失败重试次数
}

// AttachmentConfig holds attachment settings
type AttachmentConfig struct {
	MaxSize      int      `yaml:"max_size"`
	MaxTotalSize int      `yaml:"max_total_size"`
	MaxCount     int      `yaml:"max_count"`
	AllowedTypes []string `yaml:"allowed_types"`
}

// SecurityConfig holds security settings
type SecurityConfig struct {
	Auth      AuthConfig      `yaml:"auth"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

// RateLimitConfig holds rate limiting settings
type RateLimitConfig struct {
	Enabled            bool    `yaml:"enabled"`
	GlobalQPS          float64 `yaml:"global_qps"`
	GlobalConcurrency  int     `yaml:"global_concurrency"`
	DefaultQPS         float64 `yaml:"default_qps"`
	DefaultConcurrency int     `yaml:"default_concurrency"`
	CleanupInterval    string  `yaml:"cleanup_interval"`
}

// AuthConfig holds authentication settings
type AuthConfig struct {
	Enabled bool         `yaml:"enabled"`
	Type    string       `yaml:"type"`
	APIKey  APIKeyConfig `yaml:"api_key"`
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
	Level  string        `yaml:"level"`
	Format string        `yaml:"format"`
	Output []string      `yaml:"output"`
	File   LogFileConfig `yaml:"file"`
}

// LogFileConfig holds log file settings
type LogFileConfig struct {
	Directory       string `yaml:"directory"`
	FilenamePattern string `yaml:"filename_pattern"`
	MaxAge          int    `yaml:"max_age"`
}

// DatabaseConfig 数据库连接配置（来自 env.yaml）
type DatabaseConfig struct {
	Driver          string `yaml:"driver"`            // "sqlite" | "mysql" | "postgres"
	DSN             string `yaml:"dsn"`               // 连接字符串，支持 ${ENV_VAR}
	MaxOpenConns    int    `yaml:"max_open_conns"`    // 默认 20
	MaxIdleConns    int    `yaml:"max_idle_conns"`    // 默认 5
	ConnMaxLifetime string `yaml:"conn_max_lifetime"` // 默认 "30m"
}

// ExpandEnv replaces ${VAR_NAME} with environment variable values
func ExpandEnv(value string) string {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		envVar := value[2 : len(value)-1]
		return os.Getenv(envVar)
	}
	return value
}
