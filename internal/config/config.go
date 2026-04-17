package config

import (
	"os"
	"strings"
)

// Config is the root configuration structure
type Config struct {
	Agent       AgentConfig       `yaml:"agent"`
	Server      ServerConfig      `yaml:"server"`
	LLM         LLMConfig         `yaml:"llm"`
	Skills      SkillsConfig      `yaml:"skills"`
	MCP         MCPConfig         `yaml:"mcp"`
	Storage     StorageConfig     `yaml:"storage"`
	Performance PerformanceConfig `yaml:"performance"`
	React       ReactConfig       `yaml:"react"`
	Attachment  AttachmentConfig  `yaml:"attachment"`
	Security    SecurityConfig    `yaml:"security"`
	Logging     LoggingConfig     `yaml:"logging"`
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
	ActiveModel string                 `yaml:"active_model"`
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
	Engine          string       `yaml:"engine"`
	BoltDB          BoltDBConfig `yaml:"boltdb"`
	Redis           RedisConfig  `yaml:"redis"`
	Etcd            EtcdConfig   `yaml:"etcd"`
	RetentionDays   int          `yaml:"retention_days"`
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
	Endpoints []string `yaml:"endpoints"`
	KeyPrefix string   `yaml:"key_prefix"`
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
	MaxConcurrentTasks   int `yaml:"max_concurrent_tasks"`
	MaxRequestsPerMinute int `yaml:"max_requests_per_minute"`
	MaxRequestsPerHour   int `yaml:"max_requests_per_hour"`
}

// TimeoutConfig holds timeout settings
type TimeoutConfig struct {
	TaskMaxDuration int `yaml:"task_max_duration"`
	LLMCallTimeout  int `yaml:"llm_call_timeout"`
	ToolCallTimeout int `yaml:"tool_call_timeout"`
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
	MaxSize       int      `yaml:"max_size"`
	MaxTotalSize  int      `yaml:"max_total_size"`
	MaxCount      int      `yaml:"max_count"`
	AllowedTypes  []string `yaml:"allowed_types"`
	TempDirectory string   `yaml:"temp_directory"`
}

// SecurityConfig holds security settings
type SecurityConfig struct {
	Auth AuthConfig `yaml:"auth"`
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
	Level      string               `yaml:"level"`
	Format     string               `yaml:"format"`
	Output     []string             `yaml:"output"`
	File       LogFileConfig        `yaml:"file"`
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
		envVar := value[2 : len(value)-1]
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
