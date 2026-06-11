package config

import (
	"fmt"
	"os"
	"strings"
)

// Config is the root configuration structure
type Config struct {
	Agent      AgentConfig      `yaml:"agent"`
	Server     ServerConfig     `yaml:"server"`
	LLM        LLMConfig        `yaml:"llm"`
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

// LLMConfig holds LLM settings
type LLMConfig struct {
	DefaultModel string                 `yaml:"default_model"`
	Models       map[string]ModelConfig `yaml:"models"`
}

// ModelConfig holds individual model settings
type ModelConfig struct {
	BaseURL             string   `yaml:"base_url"`
	APIKey              string   `yaml:"api_key"`
	Model               string   `yaml:"model"`
	MaxCompletionTokens int      `yaml:"max_completion_tokens"`
	Temperature         float64  `yaml:"temperature"`
	TopP                float64  `yaml:"top_p"`
	FrequencyPenalty    float64  `yaml:"frequency_penalty"`
	PresencePenalty     float64  `yaml:"presence_penalty"`
	Seed                int      `yaml:"seed"`
	Stop                []string `yaml:"stop"`
	Thinking            bool     `yaml:"thinking"`
}

// MemoryConfig 记忆模块配置
type MemoryConfig struct {
	Directory       string `yaml:"directory"`        // 记忆目录
	RetentionDays   int    `yaml:"retention_days"`   // 保留天数
	CleanupSchedule string `yaml:"cleanup_schedule"` // 清理时间 HH:MM
	HistoryWindow   int    `yaml:"history_window"`   // LLM 上下文窗口（轮次），-1 不限制
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
	MaxIterations   int `yaml:"max_iterations"`
	MaxTokens       int `yaml:"max_tokens"`
	StepTimeout     int `yaml:"step_timeout"`
	ErrorRetry      int `yaml:"error_retry"`
	NestingMaxDepth int `yaml:"nesting_max_depth"`
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
	Enabled           bool    `yaml:"enabled"`
	GlobalQPS         float64 `yaml:"global_qps"`
	GlobalConcurrency int     `yaml:"global_concurrency"`
	DefaultQPS        float64 `yaml:"default_qps"`
	DefaultConcurrency int    `yaml:"default_concurrency"`
	CleanupInterval   string  `yaml:"cleanup_interval"`
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

// GetDefaultModel returns the default model configuration
func (c *LLMConfig) GetDefaultModel() *ModelConfig {
	if model, ok := c.Models[c.DefaultModel]; ok {
		// Expand environment variables in API key
		model.APIKey = ExpandEnv(model.APIKey)
		return &model
	}
	return nil
}

// GetModelByName returns the model configuration by name
// If name is empty, returns the default model
func (c *LLMConfig) GetModelByName(name string) *ModelConfig {
	if name == "" {
		return c.GetDefaultModel()
	}
	if model, ok := c.Models[name]; ok {
		// Expand environment variables in API key
		model.APIKey = ExpandEnv(model.APIKey)
		return &model
	}
	return nil
}

// ValidateModel checks if a model name exists in config
// Empty name is valid (will use default model)
func (c *LLMConfig) ValidateModel(name string) bool {
	if name == "" {
		return true // Empty is valid, will use default model
	}
	_, exists := c.Models[name]
	return exists
}

// validateModelParams checks parameter ranges for a model configuration
func validateModelParams(name string, model *ModelConfig) error {
	if model.Temperature < 0.0 || model.Temperature > 2.0 {
		return fmt.Errorf("模型 %s 的 temperature 超出范围：%.1f（有效范围 0.0~2.0）", name, model.Temperature)
	}
	if model.TopP < 0.0 || model.TopP > 1.0 {
		return fmt.Errorf("模型 %s 的 top_p 超出范围：%.1f（有效范围 0.0~1.0）", name, model.TopP)
	}
	if model.FrequencyPenalty < -2.0 || model.FrequencyPenalty > 2.0 {
		return fmt.Errorf("模型 %s 的 frequency_penalty 超出范围：%.1f（有效范围 -2.0~2.0）", name, model.FrequencyPenalty)
	}
	if model.PresencePenalty < -2.0 || model.PresencePenalty > 2.0 {
		return fmt.Errorf("模型 %s 的 presence_penalty 超出范围：%.1f（有效范围 -2.0~2.0）", name, model.PresencePenalty)
	}
	return nil
}

// ValidateLLMConfig validates LLM configuration at startup.
// Returns detailed error messages to help users fix configuration issues.
func ValidateLLMConfig(cfg *LLMConfig) error {
	if len(cfg.Models) == 0 {
		return fmt.Errorf("LLM models 配置为空，请编辑 config.yaml 添加模型配置")
	}

	if cfg.DefaultModel == "" {
		// Use first model as default if not specified
		for name := range cfg.Models {
			cfg.DefaultModel = name
			break
		}
	}

	if !cfg.ValidateModel(cfg.DefaultModel) {
		return fmt.Errorf("default_model '%s' 不存在于 models 配置中", cfg.DefaultModel)
	}

	// Check each model's configuration
	for name, model := range cfg.Models {
		if model.BaseURL == "" {
			return fmt.Errorf("模型 %s 的 base_url 为空，请编辑 config.yaml", name)
		}
		if model.APIKey == "" {
			return fmt.Errorf("模型 %s 的 api_key 为空，请编辑 config.yaml 或设置对应的环境变量", name)
		}
		// Check if APIKey is an env var reference that's not set
		if strings.HasPrefix(model.APIKey, "${") && strings.HasSuffix(model.APIKey, "}") {
			envVar := model.APIKey[2 : len(model.APIKey)-1]
			if os.Getenv(envVar) == "" {
				return fmt.Errorf("环境变量 %s 未设置，请设置后重试\n\n提示: export %s=\"your-api-key\"\n      或在 config.yaml 中直接填写 api_key", envVar, envVar)
			}
		}
		// Validate parameter ranges
		if err := validateModelParams(name, &model); err != nil {
			return err
		}
	}

	return nil
}
