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
		Memory: MemoryConfig{
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
			MaxIterations: 20,
			StepTimeout:   60,
			ErrorRetry:    2,
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
				HeaderName: "X-API-Key",
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
