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
		LLM: LLMConfig{
			DefaultModel: "gpt-4o",
			Models: map[string]ModelConfig{
				"gpt-4o": {
					BaseURL:     "https://api.openai.com/v1",
					APIKey:      "${OPENAI_API_KEY}",
					Model:       "gpt-4o",
					MaxTokens:   4096,
					Temperature: 0.7,
				},
			},
		},
		Skills: SkillsConfig{
			HotReload: HotReloadConfig{
				Enabled:       true,
				DebounceDelay: 2,
			},
		},
		Memory: MemoryConfig{
			Directory:       "memory",
			RetentionDays:   7,
			CleanupSchedule: "02:00",
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
			AllowedTypes:  []string{}, // 空数组表示允许所有类型
			TempDirectory: "temp",
		},
		Security: SecurityConfig{
			Auth: AuthConfig{
				Enabled: false, // 默认关闭认证，方便测试
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
		},
	}
}