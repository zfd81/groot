package plugin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultStartupTimeout 插件启动等待的默认超时时间。
	DefaultStartupTimeout = 180 * time.Second
	// DefaultRequestTimeout 代理请求的默认超时时间。
	DefaultRequestTimeout = 120 * time.Second
)

// GlobalConfig 是 Python 插件托管的全局配置。
type GlobalConfig struct {
	StartupTimeout time.Duration     `yaml:"startup_timeout"`
	RequestTimeout time.Duration     `yaml:"request_timeout"`
	Permission     map[string]string `yaml:"permission"`
}

// Plugin 描述一个被托管的 Python 插件。
type Plugin struct {
	Name       string
	Dir        string
	Permission string
	Invalid    string
}

// DefaultGlobalConfig 返回全局配置的默认值。
func DefaultGlobalConfig() GlobalConfig {
	return GlobalConfig{
		StartupTimeout: DefaultStartupTimeout,
		RequestTimeout: DefaultRequestTimeout,
		Permission:     map[string]string{},
	}
}

// LoadGlobalConfig 从 path 加载全局配置。
// 文件不存在时返回默认配置且不报错；YAML 语法错误、未知字段、
// 非正数超时都会返回默认配置与错误。permission 的值不做校验，
// 原样保留，由使用方在发现插件时回退。
func LoadGlobalConfig(path string) (GlobalConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultGlobalConfig(), nil
		}
		return DefaultGlobalConfig(), fmt.Errorf("打开全局配置 %s 失败: %w", path, err)
	}
	defer f.Close()

	cfg := DefaultGlobalConfig()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		// 全注释模板（groot plugins init 写入）没有 YAML 文档，等价于全部默认值。
		if errors.Is(err, io.EOF) {
			return cfg, nil
		}
		return DefaultGlobalConfig(), fmt.Errorf("解析全局配置 %s 失败: %w", path, err)
	}

	// 显式写出 `permission:` 且无值会把预置的空 map 解为 nil，恢复为非 nil 空 map。
	if cfg.Permission == nil {
		cfg.Permission = map[string]string{}
	}

	if cfg.StartupTimeout <= 0 {
		return DefaultGlobalConfig(), fmt.Errorf("全局配置 %s 无效: startup_timeout 必须大于 0, got %v", path, cfg.StartupTimeout)
	}
	if cfg.RequestTimeout <= 0 {
		return DefaultGlobalConfig(), fmt.Errorf("全局配置 %s 无效: request_timeout 必须大于 0, got %v", path, cfg.RequestTimeout)
	}

	return cfg, nil
}

var validPermissions = map[string]struct{}{
	"chat":     {},
	"status":   {},
	"detail":   {},
	"history":  {},
	"delete":   {},
	"session":  {},
	"schedule": {},
	"all":      {},
}

// ValidPermission 报告 p 是否为已知的 permission 值。
func ValidPermission(p string) bool {
	_, ok := validPermissions[p]
	return ok
}
