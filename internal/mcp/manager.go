package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/zfd81/groot/internal/logger"
)

// Manager manages all MCP configurations and tool registry
type Manager struct {
	mcps   map[string]*MCPConfig
	tools  map[string]*ToolInfo
	logger *logger.Logger
	mu     sync.RWMutex
}

// NewManager creates a new MCP manager
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		mcps:   make(map[string]*MCPConfig),
		tools:  make(map[string]*ToolInfo),
		logger: log,
	}
}

// Register adds an MCP configuration
func (m *Manager) Register(config *MCPConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mcps[config.Name] = config

	// Register tools from this MCP
	for _, toolName := range config.Tools {
		m.tools[toolName] = &ToolInfo{
			Name:        toolName,
			Description: getToolDescription(toolName),
			MCP:         config.Name,
		}
	}
}

// Unregister removes an MCP configuration
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if config, ok := m.mcps[name]; ok {
		// Remove tools from this MCP
		for _, toolName := range config.Tools {
			delete(m.tools, toolName)
		}
		delete(m.mcps, name)
	}
}

// Get retrieves an MCP by name
func (m *Manager) Get(name string) (*MCPConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	config, ok := m.mcps[name]
	return config, ok
}

// GetTool retrieves a tool by name
func (m *Manager) GetTool(name string) (*ToolInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tool, ok := m.tools[name]
	return tool, ok
}

// List returns all registered MCPs
func (m *Manager) List() []*MCPConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*MCPConfig, 0, len(m.mcps))
	for _, config := range m.mcps {
		result = append(result, config)
	}
	return result
}

// ListTools returns all registered tools
func (m *Manager) ListTools() []*ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*ToolInfo, 0, len(m.tools))
	for _, tool := range m.tools {
		result = append(result, tool)
	}
	return result
}

// Count returns the number of registered MCPs
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.mcps)
}

// ToolCount returns the number of registered tools
func (m *Manager) ToolCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tools)
}

// LoadAll loads all MCP configs from directory
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
				return fmt.Errorf("failed to load %s: %w", path, err)
			}
		}
	}

	return nil
}

// Load parses a single MCP config file
func (m *Manager) Load(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var config MCPConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return fmt.Errorf("failed to parse MCP config: %w", err)
	}

	if config.Name == "" {
		return fmt.Errorf("missing required field: name")
	}

	if !config.IsActive {
		return nil // Skip inactive MCPs
	}

	m.Register(&config)
	return nil
}

// getToolDescription returns description for builtin tools
func getToolDescription(name string) string {
	switch name {
	case "file_read":
		return "读取文件内容"
	case "file_write":
		return "写入文件内容"
	case "file_delete":
		return "删除文件"
	case "file_search":
		return "搜索文件"
	case "directory_list":
		return "列出目录内容"
	case "directory_create":
		return "创建目录"
	case "file_exists":
		return "检查文件是否存在"
	case "file_info":
		return "获取文件信息"
	case "http_get":
		return "发送HTTP GET请求"
	case "http_post":
		return "发送HTTP POST请求"
	case "http_put":
		return "发送HTTP PUT请求"
	case "http_delete":
		return "发送HTTP DELETE请求"
	default:
		return name
	}
}
