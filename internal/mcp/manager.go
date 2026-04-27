package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// Manager manages all MCP configurations and tool registry
type Manager struct {
	mcps      map[string]*MCPConfig
	tools     map[string]*ToolInfo
	errors    map[string]string // MCP discovery errors
	executor  *ToolExecutor
	logger    *logger.Logger
	mu        sync.RWMutex
}

// NewManager creates a new MCP manager
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		mcps:     make(map[string]*MCPConfig),
		tools:    make(map[string]*ToolInfo),
		errors:   make(map[string]string),
		executor: NewToolExecutor(log),
		logger:   log,
	}
}

// GetExecutor returns the tool executor
func (m *Manager) GetExecutor() *ToolExecutor {
	return m.executor
}

// Register adds an MCP configuration with tools
func (m *Manager) Register(config *MCPConfig, tools []ToolDefinition, discoveryError string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mcps[config.Name] = config

	// Store error if any
	if discoveryError != "" {
		m.errors[config.Name] = discoveryError
	} else {
		delete(m.errors, config.Name)
	}

	// Register tools
	for _, tool := range tools {
		m.tools[tool.Name] = &ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
			MCP:         config.Name,
		}
	}

	m.logger.Info("Registered MCP", zap.String("name", config.Name), zap.Int("tools", len(tools)))
}

// Unregister removes an MCP configuration
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if config, ok := m.mcps[name]; ok {
		// Remove tools from this MCP
		for _, tool := range config.Tools {
			delete(m.tools, tool.Name)
		}
		delete(m.mcps, name)
		m.logger.Info("Unregistered MCP", zap.String("name", name))
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

// MCPInfo represents MCP status with tool count
type MCPInfo struct {
	Name        string
	Type        MCPType
	Description string
	IsActive    bool
	ToolCount   int
	Error       string // Discovery error if any
}

// ListWithToolCount returns all MCPs with their tool counts
func (m *Manager) ListWithToolCount() []MCPInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]MCPInfo, 0, len(m.mcps))
	for _, config := range m.mcps {
		// Count tools from this MCP
		toolCount := 0
		for _, tool := range m.tools {
			if tool.MCP == config.Name {
				toolCount++
			}
		}
		result = append(result, MCPInfo{
			Name:        config.Name,
			Type:        config.Type,
			Description: config.Description,
			IsActive:    config.IsActive,
			ToolCount:   toolCount,
			Error:       m.errors[config.Name],
		})
	}
	return result
}

// GetError returns the discovery error for an MCP
func (m *Manager) GetError(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.errors[name]
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
				m.logger.Error("Failed to load MCP config", zap.String("path", path), zap.Error(err))
				// Continue loading other configs instead of failing
			}
		}
	}

	return nil
}

// Load parses a single MCP config file and discovers tools
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
		m.logger.Info("Skipping inactive MCP", zap.String("name", config.Name))
		return nil
	}

	// Discover tools if not specified in config
	tools := config.Tools
	var discoveryError string
	if len(tools) == 0 {
		// Auto-discover tools via tools/list
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		discoveredTools, err := m.executor.DiscoverTools(ctx, &config)
		if err != nil {
			m.logger.Error("Failed to discover tools from MCP",
				zap.String("name", config.Name),
				zap.Error(err))
			tools = []ToolDefinition{}
			discoveryError = err.Error()
		} else {
			tools = discoveredTools
		}
	}

	m.Register(&config, tools, discoveryError)
	return nil
}