package mcp

// RegisterBuiltinTools registers built-in MCP tools
func RegisterBuiltinTools(manager *Manager) {
	// file_operations MCP
	manager.Register(&MCPConfig{
		Name:        "file_operations",
		Type:        MCPTypeBuiltin,
		Description: "文件读写和目录操作",
		IsActive:    true,
		Tools: []string{
			"file_read",
			"file_write",
			"file_search",
			"directory_list",
			"directory_create",
			"file_exists",
			"file_info",
		},
	})

	// http_request MCP
	manager.Register(&MCPConfig{
		Name:        "http_request",
		Type:        MCPTypeBuiltin,
		Description: "HTTP请求发送",
		IsActive:    true,
		Tools: []string{
			"http_get",
			"http_post",
			"http_put",
			"http_delete",
		},
	})
}
