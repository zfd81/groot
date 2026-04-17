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
			Engine:          "boltdb",
			BoltDB:          BoltDBConfig{File: "groot.db", Bucket: "tasks"},
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
