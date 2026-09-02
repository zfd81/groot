package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load loads configuration from file with defaults
func Load(homeDir string) (*Config, error) {
	configPath := filepath.Join(homeDir, "config.yaml")

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("配置文件不存在，请先运行 'groot init' 初始化")
	}

	// Config file exists: parse user config (don't merge with defaults)
	cfg := &Config{}

	// Read config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML into empty config (user config takes full control)
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// 基础设施凭据（如 storage.minio）只通过 env.yaml 注入：
	// 即便 config.yaml 里残留了 storage.minio 节也不再生效。
	if err := loadEnvFile(cfg, homeDir); err != nil {
		return nil, fmt.Errorf("failed to load env file: %w", err)
	}

	// Apply defaults for missing optional fields
	applyDefaults(cfg)

	// Expand environment variables in all relevant fields
	expandConfigEnvVars(cfg)

	return cfg, nil
}

// applyDefaults fills in default values for optional fields that user didn't specify
func applyDefaults(cfg *Config) {
	// Agent defaults
	if cfg.Agent.Name == "" {
		cfg.Agent.Name = "groot"
	}
	if cfg.Agent.Version == "" {
		cfg.Agent.Version = "1.0.0"
	}

	// Server defaults
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}

	// Memory defaults（-1 表示不限制轮数，保持原值）
	if cfg.Memory.HistoryWindow == 0 {
		cfg.Memory.HistoryWindow = 20
	}

	// Schedule defaults
	if cfg.Schedule.MaxConcurrentTasks == 0 {
		cfg.Schedule.MaxConcurrentTasks = 3
	}
	if cfg.Schedule.SyncInterval == "" {
		cfg.Schedule.SyncInterval = "30s"
	}

	// SubAgent defaults
	if cfg.SubAgent.MaxConcurrency == 0 {
		cfg.SubAgent.MaxConcurrency = 5
	}
	if cfg.SubAgent.ExecTimeout == "" {
		cfg.SubAgent.ExecTimeout = "5m"
	}
	if cfg.SubAgent.MaxTaskLength == 0 {
		cfg.SubAgent.MaxTaskLength = 16000
	}
	if cfg.SubAgent.MaxResultLength == 0 {
		cfg.SubAgent.MaxResultLength = 8000
	}

	// Message defaults
	if cfg.Message.QueueSize == 0 {
		cfg.Message.QueueSize = 256
	}
	if cfg.Message.Workers == 0 {
		cfg.Message.Workers = 2
	}

	// React defaults
	if cfg.React.MaxIterations == 0 {
		cfg.React.MaxIterations = 20
	}
	if cfg.React.StepTimeout == 0 {
		cfg.React.StepTimeout = 60
	}
	if cfg.React.ErrorRetry == 0 {
		cfg.React.ErrorRetry = 2
	}

	// Attachment defaults
	if cfg.Attachment.MaxSize == 0 {
		cfg.Attachment.MaxSize = 50
	}
	if cfg.Attachment.MaxTotalSize == 0 {
		cfg.Attachment.MaxTotalSize = 100
	}
	if cfg.Attachment.MaxCount == 0 {
		cfg.Attachment.MaxCount = 10
	}

	// Auth defaults（认证始终开启，只需 header 名默认值；secret 由 EnsureAuthSecret 兜底）
	if cfg.Security.Auth.HeaderName == "" {
		cfg.Security.Auth.HeaderName = "X-API-Key"
	}

	// Logging defaults
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
	if len(cfg.Logging.Output) == 0 {
		cfg.Logging.Output = []string{"stdout", "file"}
	}
	if cfg.Logging.File.Directory == "" {
		cfg.Logging.File.Directory = "logs"
	}
	if cfg.Logging.File.FilenamePattern == "" {
		cfg.Logging.File.FilenamePattern = "groot-{date}.log"
	}
	if cfg.Logging.File.MaxAge == 0 {
		cfg.Logging.File.MaxAge = 7
	}
}

// expandConfigEnvVars expands environment variables in config
func expandConfigEnvVars(cfg *Config) {
	// Expand database DSN
	if cfg.Database != nil {
		cfg.Database.DSN = ExpandEnv(cfg.Database.DSN)
	}
}
