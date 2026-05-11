package mcp

// MCPType represents MCP connection type
type MCPType string

const (
	MCPTypeStdio          MCPType = "stdio"
	MCPTypeSSE            MCPType = "sse"
	MCPTypeStreamableHTTP MCPType = "streamable_http"
)

// MCPConfig represents a single MCP configuration
type MCPConfig struct {
	Name         string            `json:"name"`
	Type         MCPType           `json:"type"`
	Description  string            `json:"description"`
	IsActive     bool              `json:"isActive"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	BaseURL      string            `json:"baseUrl,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Tools        []ToolDefinition  `json:"tools,omitempty"`
}

// ToolDefinition represents a tool's definition in MCP config
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// ToolInfo represents a tool's metadata for registry
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
	MCP         string                 `json:"mcp"`
}