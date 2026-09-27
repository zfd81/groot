package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// BootstrapFileName 是 Groot 唯一的 YAML 配置文件名。
const BootstrapFileName = "bootstrap.yaml"

// Bootstrap 是 bootstrap.yaml 的结构，承载启动路径上读取的配置。
//
// 判据是「读取点在数据库连接建立之前，或不依赖数据库」：日志要先就绪才能
// 记录数据库打开失败，服务端口要在监听前确定，groot status 与 groot tail
// 不连数据库也要取到端口与日志目录。其余业务配置存放于数据库配置表。
type Bootstrap struct {
	Agent    AgentConfig       `yaml:"agent,omitempty"`
	Server   ServerConfig      `yaml:"server,omitempty"`
	Logging  LoggingConfig     `yaml:"logging,omitempty"`
	Database *DatabaseConfig   `yaml:"database,omitempty"`
	Message  MessageBootstrap  `yaml:"message,omitempty"`
	Schedule ScheduleBootstrap `yaml:"schedule,omitempty"`
	Security SecurityBootstrap `yaml:"security,omitempty"`
}

// MessageBootstrap 消息层的构造参数。发送渠道参数在配置表中。
type MessageBootstrap struct {
	QueueSize int `yaml:"queue_size,omitempty"`
	Workers   int `yaml:"workers,omitempty"`
}

// ScheduleBootstrap 调度器的构造参数。enabled 开关在配置表中。
type ScheduleBootstrap struct {
	MaxConcurrentTasks int    `yaml:"max_concurrent_tasks,omitempty"`
	SyncInterval       string `yaml:"sync_interval,omitempty"`
}

// SecurityBootstrap 只承载限流空闲桶的回收周期（后台协程的定时参数）。
// auth 与限流的五项阈值在配置表中。
type SecurityBootstrap struct {
	RateLimit RateLimitBootstrap `yaml:"rate_limit,omitempty"`
}

// RateLimitBootstrap 限流的启动期参数。
type RateLimitBootstrap struct {
	CleanupInterval string `yaml:"cleanup_interval,omitempty"`
}

// LoadBootstrap 读取并解析 bootstrap.yaml，填充缺省值并展开 DSN 中的环境变量。
// 文件缺失视为未初始化，提示先运行 groot init。
func LoadBootstrap(homeDir string) (*Bootstrap, error) {
	path := filepath.Join(homeDir, BootstrapFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("配置文件 %s 不存在，请先运行 'groot init' 初始化", BootstrapFileName)
		}
		return nil, fmt.Errorf("failed to read bootstrap file: %w", err)
	}

	b := &Bootstrap{}
	if err := yaml.Unmarshal(data, b); err != nil {
		return nil, fmt.Errorf("failed to parse bootstrap file: %w", err)
	}

	applyBootstrapDefaults(b)
	if b.Database != nil {
		b.Database.DSN = ExpandEnv(b.Database.DSN)
	}
	return b, nil
}

// applyBootstrapDefaults 为未给出的字段填充缺省值，
// 使全注释的 bootstrap.yaml 即为一份可用配置。
func applyBootstrapDefaults(b *Bootstrap) {
	if b.Agent.Name == "" {
		b.Agent.Name = "groot"
	}
	if b.Agent.Version == "" {
		b.Agent.Version = "1.0.0"
	}

	if b.Server.Host == "" {
		b.Server.Host = "0.0.0.0"
	}
	if b.Server.Port == 0 {
		b.Server.Port = 8080
	}

	if b.Message.QueueSize == 0 {
		b.Message.QueueSize = 256
	}
	if b.Message.Workers == 0 {
		b.Message.Workers = 2
	}

	if b.Schedule.MaxConcurrentTasks == 0 {
		b.Schedule.MaxConcurrentTasks = 3
	}
	if b.Schedule.SyncInterval == "" {
		b.Schedule.SyncInterval = "30s"
	}

	if b.Security.RateLimit.CleanupInterval == "" {
		b.Security.RateLimit.CleanupInterval = "5m"
	}

	if b.Logging.Level == "" {
		b.Logging.Level = "info"
	}
	if b.Logging.Format == "" {
		b.Logging.Format = "json"
	}
	if len(b.Logging.Output) == 0 {
		b.Logging.Output = []string{"stdout", "file"}
	}
	if b.Logging.File.Directory == "" {
		b.Logging.File.Directory = "logs"
	}
	if b.Logging.File.FilenamePattern == "" {
		b.Logging.File.FilenamePattern = "groot-{date}.log"
	}
	if b.Logging.File.MaxAge == 0 {
		b.Logging.File.MaxAge = 7
	}
}
