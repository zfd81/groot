package apitool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// Manager API工具管理器
type Manager struct {
	tools    map[string]*APIToolConfig
	executor *Executor
	log      *logger.Logger
	mu       sync.RWMutex
}

// NewManager 创建管理器
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		tools:    make(map[string]*APIToolConfig),
		executor: NewExecutor(log),
		log:      log,
	}
}

// GetExecutor 获取执行器
func (m *Manager) GetExecutor() *Executor {
	return m.executor
}

// Register 注册工具
func (m *Manager) Register(config *APIToolConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tools[config.Name] = config
	m.log.Info("注册API工具", zap.String("name", config.Name))
}

// Get 获取工具配置
func (m *Manager) Get(name string) (*APIToolConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	config, ok := m.tools[name]
	return config, ok
}

// List 列出所有工具
func (m *Manager) List() []*APIToolConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*APIToolConfig, 0, len(m.tools))
	for _, config := range m.tools {
		result = append(result, config)
	}
	return result
}

// ListToolNames 列出所有工具名称
func (m *Manager) ListToolNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, 0, len(m.tools))
	for name := range m.tools {
		result = append(result, name)
	}
	return result
}

// Count 工具数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tools)
}

// LoadAll 加载目录下所有配置
func (m *Manager) LoadAll(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			path := filepath.Join(dir, entry.Name())
			if err := m.Load(path); err != nil {
				return fmt.Errorf("加载 %s 失败: %w", path, err)
			}
		}
	}

	return nil
}

// Load 加载单个配置文件
func (m *Manager) Load(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var config APIToolConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return fmt.Errorf("解析配置失败: %w", err)
	}

	if config.Name == "" {
		return fmt.Errorf("缺少必填字段: name")
	}

	if config.Description == "" {
		return fmt.Errorf("缺少必填字段: description")
	}

	if config.URL == "" {
		return fmt.Errorf("缺少必填字段: url")
	}

	if config.Method == "" {
		return fmt.Errorf("缺少必填字段: method")
	}

	// 校验环境变量
	if err := ValidateEnvVars(&config); err != nil {
		return err
	}

	m.Register(&config)
	return nil
}

// ValidateAndLoad 校验后加载（用于启动时检查）
func (m *Manager) ValidateAndLoad(dir string, existingToolNames []string) error {
	// 先加载所有配置（不注册）
	configs, err := m.loadConfigsOnly(dir)
	if err != nil {
		return err
	}

	// 校验所有环境变量
	if err := ValidateAllEnvVars(configs); err != nil {
		return err
	}

	// 检查命名冲突
	if err := CheckToolNameConflict(configs, existingToolNames); err != nil {
		return err
	}

	// 注册所有工具
	for _, config := range configs {
		m.Register(config)
	}

	return nil
}

// loadConfigsOnly 仅加载配置文件，不注册
func (m *Manager) loadConfigsOnly(dir string) ([]*APIToolConfig, error) {
	configs := []*APIToolConfig{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return configs, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			path := filepath.Join(dir, entry.Name())
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
			}

			var config APIToolConfig
			if err := json.Unmarshal(content, &config); err != nil {
				return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
			}

			if config.Name == "" {
				return nil, fmt.Errorf("%s 缺少必填字段: name", path)
			}

			configs = append(configs, &config)
		}
	}

	return configs, nil
}