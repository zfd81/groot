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
}
