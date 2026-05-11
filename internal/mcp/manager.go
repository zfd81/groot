package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/cloudwego/eino/components/tool"
	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"

	"github.com/zfd81/groot/internal/logger"
)

// Manager manages all MCP configurations and tool registry
type Manager struct {
	mcps         map[string]*MCPConfig
	clients      map[string]client.MCPClient    // mcp-go clients per MCP
	einoTools    map[string]tool.BaseTool       // eino tools from MCP servers
	builtinTools map[string]tool.BaseTool       // built-in tools (e.g., schedule)
	toolInfos    map[string]*ToolInfo           // tool metadata (for API)
	errors       map[string]string              // MCP discovery errors
	logger       *logger.Logger
	mu           sync.RWMutex
}

// NewManager creates a new MCP manager
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		mcps:         make(map[string]*MCPConfig),
		clients:      make(map[string]client.MCPClient),
		einoTools:    make(map[string]tool.BaseTool),
		builtinTools: make(map[string]tool.BaseTool),
		toolInfos:    make(map[string]*ToolInfo),
		errors:       make(map[string]string),
		logger:       log,
	}
}

// GetTools returns all eino tools (MCP + builtin) for engine usage
func (m *Manager) GetTools() []tool.BaseTool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]tool.BaseTool, 0, len(m.einoTools)+len(m.builtinTools))
	for _, t := range m.einoTools {
		result = append(result, t)
	}
	for _, t := range m.builtinTools {
		result = append(result, t)
	}
	return result
}

// RegisterBuiltinTools registers built-in tools (e.g., schedule tools)
func (m *Manager) RegisterBuiltinTools(tools map[string]tool.BaseTool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, t := range tools {
		m.builtinTools[name] = t
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		m.toolInfos[name] = &ToolInfo{
			Name:        info.Name,
			Description: info.Desc,
			MCP:         "schedule",
		}
	}

	m.logger.Info("注册内置工具", zap.Int("count", len(tools)))
}

// registerLocked stores MCP config and tool metadata (caller must hold m.mu.Lock)
func (m *Manager) registerLocked(config *MCPConfig, tools []ToolDefinition, discoveryError string) {
	m.mcps[config.Name] = config

	if discoveryError != "" {
		m.errors[config.Name] = discoveryError
	} else {
		delete(m.errors, config.Name)
	}

	for _, td := range tools {
		m.toolInfos[td.Name] = &ToolInfo{
			Name:        td.Name,
			Description: td.Description,
			InputSchema: td.InputSchema,
			MCP:         config.Name,
		}
	}
}

// Register adds an MCP configuration with tools
func (m *Manager) Register(config *MCPConfig, tools []ToolDefinition, discoveryError string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.registerLocked(config, tools, discoveryError)
	m.logger.Info("Registered MCP tools", zap.String("name", config.Name), zap.Int("tools", len(tools)))
}

// Unregister removes an MCP configuration
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.mcps[name]; ok {
		// Remove tools belonging to this MCP
		for toolName, info := range m.toolInfos {
			if info.MCP == name {
				delete(m.einoTools, toolName)
				delete(m.toolInfos, toolName)
			}
		}
		// Close client
		if cli, ok := m.clients[name]; ok {
			cli.Close()
			delete(m.clients, name)
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
	tool, ok := m.toolInfos[name]
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

// ListTools returns all registered tools metadata
func (m *Manager) ListTools() []*ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*ToolInfo, 0, len(m.toolInfos))
	for _, tool := range m.toolInfos {
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
	Error       string
}

// ListWithToolCount returns all MCPs with their tool counts
func (m *Manager) ListWithToolCount() []MCPInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]MCPInfo, 0, len(m.mcps))
	for _, config := range m.mcps {
		toolCount := 0
		for _, tool := range m.toolInfos {
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
	return len(m.toolInfos)
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
			}
		}
	}

	return nil
}

// Load parses a single MCP config file, creates client, and discovers tools via eino-ext
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

	// Create client and initialize (same for both auto-discovery and name-filtered modes)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cli, err := m.createAndInitClient(ctx, &config)
	if err != nil {
		m.Register(&config, nil, err.Error())
		return nil
	}

	// Build mcpp.Config; if tools are pre-defined, use ToolNameList to filter
	mcppConf := &mcpp.Config{
		Cli:           cli,
		CustomHeaders: expandHeaders(config.Headers),
	}
	if len(config.Tools) > 0 {
		toolNames := make([]string, len(config.Tools))
		for i, t := range config.Tools {
			toolNames[i] = t.Name
		}
		mcppConf.ToolNameList = toolNames
	}

	// Discover tools via eino-ext GetTools
	einoTools, err := mcpp.GetTools(ctx, mcppConf)
	if err != nil {
		m.Register(&config, nil, err.Error())
		return nil
	}

	// Extract tool metadata from eino tools
	toolDefs := make([]ToolDefinition, 0, len(einoTools))
	for _, t := range einoTools {
		info, _ := t.Info(ctx)
		if info != nil {
			td := ToolDefinition{
				Name:        info.Name,
				Description: info.Desc,
			}
			if info.ParamsOneOf != nil {
				if js, e := info.ParamsOneOf.ToJSONSchema(); e == nil && js != nil {
					data, _ := json.Marshal(js)
					var schema map[string]interface{}
					json.Unmarshal(data, &schema)
					td.InputSchema = schema
				}
			}
			toolDefs = append(toolDefs, td)
		}
	}

	// Store everything: common via registerLocked, then clients+einoTools
	m.mu.Lock()
	m.registerLocked(&config, toolDefs, "")
	m.clients[config.Name] = cli
	for i, t := range einoTools {
		m.einoTools[toolDefs[i].Name] = t
	}
	m.mu.Unlock()

	m.logger.Info("MCP loaded via eino-ext", zap.String("name", config.Name), zap.Int("tools", len(einoTools)))
	return nil
}

// createAndInitClient creates a mcp-go client and performs initialization handshake
func (m *Manager) createAndInitClient(ctx context.Context, config *MCPConfig) (client.MCPClient, error) {
	var cli *client.Client
	var err error

	switch config.Type {
	case MCPTypeStdio:
		cli, err = m.createStdioClient(config)
	case MCPTypeSSE:
		cli, err = m.createSSEClient(ctx, config)
	case MCPTypeStreamableHTTP:
		cli, err = m.createStreamableHTTPClient(ctx, config)
	default:
		return nil, fmt.Errorf("unsupported MCP type: %s", config.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}

	// Initialize handshake
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "groot",
		Version: "1.0.0",
	}

	_, err = cli.Initialize(ctx, initReq)
	if err != nil {
		cli.Close()
		return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	return cli, nil
}

// createStdioClient creates a stdio-based MCP client
func (m *Manager) createStdioClient(config *MCPConfig) (*client.Client, error) {
	expandedArgs := expandArgs(config.Args)
	env := buildEnv(config.Env)
	return client.NewStdioMCPClient(config.Command, env, expandedArgs...)
}

// createSSEClient creates an SSE-based MCP client
func (m *Manager) createSSEClient(ctx context.Context, config *MCPConfig) (*client.Client, error) {
	cli, err := client.NewSSEMCPClient(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start SSE client: %w", err)
	}
	return cli, nil
}

// createStreamableHTTPClient creates a streamable HTTP MCP client
func (m *Manager) createStreamableHTTPClient(ctx context.Context, config *MCPConfig) (*client.Client, error) {
	cli, err := client.NewStreamableHttpClient(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start streamable HTTP client: %w", err)
	}
	return cli, nil
}

// Close closes all MCP clients
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, cli := range m.clients {
		if err := cli.Close(); err != nil {
			m.logger.Error("Failed to close MCP client", zap.String("name", name), zap.Error(err))
		}
	}
	m.clients = make(map[string]client.MCPClient)
}

// expandHeaders expands environment variables in header values
func expandHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	result := make(map[string]string, len(headers))
	for k, v := range headers {
		result[k] = os.ExpandEnv(v)
	}
	return result
}

// expandArgs expands environment variables and home dir in args
func expandArgs(args []string) []string {
	expanded := make([]string, len(args))
	for i, arg := range args {
		s := os.ExpandEnv(arg)
		if strings.HasPrefix(s, "~") {
			homeDir, _ := os.UserHomeDir()
			s = homeDir + s[1:]
		}
		expanded[i] = s
	}
	return expanded
}

// buildEnv converts env map to []string format (merged with OS env)
func buildEnv(envMap map[string]string) []string {
	if len(envMap) == 0 {
		return nil
	}
	result := os.Environ()
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		result = append(result, k+"="+os.ExpandEnv(envMap[k]))
	}
	return result
}
