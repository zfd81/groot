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
	Skills     SkillsConfig     `yaml:"skills"`
	Memory     MemoryConfig     `yaml:"memory"`
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
	Models       map[string]ModelConfig `yaml:"models"`
}

// ModelConfig holds individual model settings
type ModelConfig struct {
	BaseURL     string  `yaml:"base_url"`
	APIKey      string  `yaml:"api_key"`
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

// SkillsConfig holds Skills hot-reload settings
type SkillsConfig struct {
	HotReload HotReloadConfig `yaml:"hot_reload"`
}

// HotReloadConfig holds hot-reload settings
type HotReloadConfig struct {
	Enabled       bool `yaml:"enabled"`
	DebounceDelay int  `yaml:"debounce_delay"`
}

// MemoryConfig 记忆模块配置
type MemoryConfig struct {
	Directory       string `yaml:"directory"`        // 记忆目录
	RetentionDays   int    `yaml:"retention_days"`   // 保留天数
	CleanupSchedule string `yaml:"cleanup_schedule"` // 清理时间 HH:MM
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
	}

	return nil
}
