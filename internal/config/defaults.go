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
					BaseURL:              "https://api.openai.com/v1",
					APIKey:               "${OPENAI_API_KEY}",
					Model:                "gpt-4o",
					MaxCompletionTokens:  4096,
					Temperature:          0.7,
					TopP:                 1.0,
					FrequencyPenalty:     0.0,
					PresencePenalty:      0.0,
				},
			},
		},
		Memory: MemoryConfig{
			Directory:     "memory",
			HistoryWindow: 20,
		},
		Schedule: ScheduleConfig{
			Enabled:            false,
			MaxConcurrentTasks: 3,
			SyncInterval:       "30s",
		},
		Message: MessageConfig{
			QueueSize: 256,
			Workers:   2,
			Senders: map[string]SenderConf{
				"webhook": {Enabled: false},
				"email":   {Enabled: false, SMTPPort: 587},
			},
		},
		SubAgent: SubAgentConfig{
			MaxConcurrency:  5,     // 全局 semaphore 大小
			ExecTimeout:     "5m",  // 子 Agent 执行超时（排队不计入）
			MaxTaskLength:   16000, // task 参数最大字符数
			MaxResultLength: 8000,  // 子 Agent 返回文本截断长度
		},
		React: ReactConfig{
			MaxIterations:   20,
			MaxTokens:       100000,
			StepTimeout:     60,
			ErrorRetry:      2,
			NestingMaxDepth: 3,
		},
		Attachment: AttachmentConfig{
			MaxSize:      50,
			MaxTotalSize: 100,
			MaxCount:     10,
			AllowedTypes: []string{}, // 空数组表示允许所有类型
		},
		Security: SecurityConfig{
			RateLimit: RateLimitConfig{
				Enabled:            false,
				GlobalQPS:          0,
				GlobalConcurrency:  0,
				DefaultQPS:         10,
				DefaultConcurrency: 5,
				CleanupInterval:    "5m",
			},
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