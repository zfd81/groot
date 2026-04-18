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
			ActiveModel: "gpt-4o",
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
		MCP: MCPConfig{
			HotReload: HotReloadConfig{
				Enabled:       true,
				DebounceDelay: 2,
			},
		},
		Storage: StorageConfig{
			Engine:          "memory", // memory module will be implemented in Phase 2-4
			RetentionDays:   7,
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
