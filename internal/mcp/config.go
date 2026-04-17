package mcp

// MCPType represents MCP connection type
type MCPType string

const (
	MCPTypeStdio          MCPType = "stdio"
	MCPTypeSSE            MCPType = "sse"
	MCPTypeStreamableHTTP MCPType = "streamable_http"
	MCPTypeBuiltin        MCPType = "builtin"
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
	Tools        []string          `json:"tools,omitempty"`
	Restrictions *MCPRestrictions  `json:"restrictions,omitempty"`
}

// MCPRestrictions holds security restrictions for builtin tools
type MCPRestrictions struct {
	AllowedPaths     []string `json:"allowed_paths,omitempty"`
	DeniedOperations []string `json:"denied_operations,omitempty"`
	DeniedDomains    []string `json:"denied_domains,omitempty"`
	Timeout          int      `json:"timeout,omitempty"`
	MaxResponseSize  int      `json:"max_response_size,omitempty"`
	Sandbox          bool     `json:"sandbox,omitempty"`
	NetworkAccess    bool     `json:"network_access,omitempty"`
}

// ToolInfo represents a tool's metadata
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MCP         string `json:"mcp"`
}
