// internal/config/migrate_legacy.go
// 老配置文件（config.yaml + env.yaml）到 bootstrap.yaml 的一次性迁移。
//
// bootstrap 项写入 bootstrap.yaml；业务项打包为 LegacyBusiness 返回，
// 由调用方在数据库就绪后写入配置表。老文件原地保留、不再被读取，
// 使用者确认无误后可自行删除。
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LegacyBusiness 是老 config.yaml 中待迁入配置表的业务项。
// 各字段为解析原文件的结果，零值表示原文件未设置该项 ——
// 写表方（setting 包）遵循「只迁非零值、表内已有值不覆盖」。
type LegacyBusiness struct {
	Memory          MemoryConfig
	React           ReactConfig
	SubAgent        SubAgentConfig
	Attachment      AttachmentConfig
	RateLimit       RateLimitConfig
	ScheduleEnabled bool
	Auth            AuthConfig
}

// legacyConfigFile 描述老 config.yaml 的顶层结构（bootstrap 项与业务项的并集）。
// 不复用 Config：这里是老格式解析的冻结快照，与将来会被删除的 Config
// 加载路径解耦，Config 的字段演进不影响老文件的迁移语义。
type legacyConfigFile struct {
	Agent      AgentConfig      `yaml:"agent"`
	Server     ServerConfig     `yaml:"server"`
	Memory     MemoryConfig     `yaml:"memory"`
	React      ReactConfig      `yaml:"react"`
	Attachment AttachmentConfig `yaml:"attachment"`
	Schedule   ScheduleConfig   `yaml:"schedule"`
	SubAgent   SubAgentConfig   `yaml:"subagent"`
	Security   SecurityConfig   `yaml:"security"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// legacyEnvFile 描述老 env.yaml 的顶层结构。
type legacyEnvFile struct {
	Database *DatabaseConfig `yaml:"database"`
}

// MigrateLegacy 检测 homeDir 下的老配置文件并执行文件侧迁移。
//
// 返回值语义：
//   - (nil, nil)：无需迁移（bootstrap.yaml 已存在，或没有任何老文件）
//   - (lb, nil)：迁移完成，lb 为待写入配置表的业务项
//
// 迁移只在 bootstrap.yaml 不存在时发生一次，失败时不留下半成品文件。
func MigrateLegacy(homeDir string) (*LegacyBusiness, error) {
	bootstrapPath := filepath.Join(homeDir, BootstrapFileName)
	if _, err := os.Stat(bootstrapPath); err == nil {
		return nil, nil // 已迁移过
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("检查 bootstrap.yaml 失败: %w", err)
	}

	legacyCfg, cfgExists, err := readLegacyConfig(homeDir)
	if err != nil {
		return nil, err
	}
	legacyDB, envExists, err := readLegacyEnv(homeDir)
	if err != nil {
		return nil, err
	}
	if !cfgExists && !envExists {
		return nil, nil // 全新安装，交给 groot init
	}

	b := buildBootstrapFromLegacy(legacyCfg, legacyDB)
	data, err := marshalBootstrapWithHeader(b)
	if err != nil {
		return nil, err
	}
	// 0600：文件含数据库凭据。原子写：写半途失败不会留下半成品文件
	// 被下次启动误判为「已迁移」。
	if err := writeFileAtomic(bootstrapPath, data); err != nil {
		return nil, fmt.Errorf("写入 bootstrap.yaml 失败: %w", err)
	}

	return &LegacyBusiness{
		Memory:          legacyCfg.Memory,
		React:           legacyCfg.React,
		SubAgent:        legacyCfg.SubAgent,
		Attachment:      legacyCfg.Attachment,
		RateLimit:       legacyCfg.Security.RateLimit,
		ScheduleEnabled: legacyCfg.Schedule.Enabled,
		Auth:            legacyCfg.Security.Auth,
	}, nil
}

// readLegacyConfig 读取老 config.yaml；不存在时返回零值与 false。
func readLegacyConfig(homeDir string) (legacyConfigFile, bool, error) {
	var lc legacyConfigFile
	data, err := os.ReadFile(filepath.Join(homeDir, "config.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return lc, false, nil
		}
		return lc, false, fmt.Errorf("读取老 config.yaml 失败: %w", err)
	}
	if err := yaml.Unmarshal(data, &lc); err != nil {
		return lc, false, fmt.Errorf("解析老 config.yaml 失败: %w", err)
	}
	return lc, true, nil
}

// readLegacyEnv 读取老 env.yaml 的 database 节；不存在时返回 nil 与 false。
func readLegacyEnv(homeDir string) (*DatabaseConfig, bool, error) {
	data, err := os.ReadFile(filepath.Join(homeDir, EnvFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("读取老 env.yaml 失败: %w", err)
	}
	var le legacyEnvFile
	if err := yaml.Unmarshal(data, &le); err != nil {
		return nil, false, fmt.Errorf("解析老 env.yaml 失败: %w", err)
	}
	return le.Database, true, nil
}

// buildBootstrapFromLegacy 从老文件的解析结果组装 Bootstrap。零值字段
// 依赖 omitempty 不落盘（老模板几乎全注释，零值写进新文件会变成无效配置，
// 如 port: 0），加载时由缺省值兜底。
func buildBootstrapFromLegacy(lc legacyConfigFile, db *DatabaseConfig) *Bootstrap {
	return &Bootstrap{
		Agent:    lc.Agent,
		Server:   lc.Server,
		Logging:  lc.Logging,
		Database: db,
		Schedule: ScheduleBootstrap{
			MaxConcurrentTasks: lc.Schedule.MaxConcurrentTasks,
			SyncInterval:       lc.Schedule.SyncInterval,
		},
		Security: SecurityBootstrap{RateLimit: RateLimitBootstrap{
			CleanupInterval: lc.Security.RateLimit.CleanupInterval,
		}},
	}
}

// marshalBootstrapWithHeader 序列化 Bootstrap 并加说明性头注释。
func marshalBootstrapWithHeader(b *Bootstrap) ([]byte, error) {
	body, err := yaml.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("序列化 bootstrap.yaml 失败: %w", err)
	}
	header := `# 本文件由 Groot 从 config.yaml 与 env.yaml 自动迁移生成。
# 老文件已不再被读取，确认本文件内容无误后可自行删除它们。
# 业务配置（模型、限流阈值等）已迁入数据库，请在 Web 设置面板中维护。

`
	return append([]byte(header), body...), nil
}
