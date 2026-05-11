package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/zfd81/groot/internal/logger"
)

func newTestManager() *Manager {
	return NewManager(logger.NewNop())
}

func TestManager_Register(t *testing.T) {
	mgr := newTestManager()

	cfg := &MCPConfig{Name: "test", Type: MCPTypeStdio, Description: "test mcp", IsActive: true}
	tools := []ToolDefinition{
		{Name: "tool1", Description: "Tool 1"},
		{Name: "tool2", Description: "Tool 2"},
	}

	mgr.Register(cfg, tools, "")

	if mgr.Count() != 1 {
		t.Errorf("expected 1 MCP, got %d", mgr.Count())
	}
	if mgr.ToolCount() != 2 {
		t.Errorf("expected 2 tools, got %d", mgr.ToolCount())
	}

	c, ok := mgr.Get("test")
	if !ok || c.Name != "test" {
		t.Errorf("expected to find MCP 'test'")
	}

	tool, ok := mgr.GetTool("tool1")
	if !ok || tool.MCP != "test" || tool.Description != "Tool 1" {
		t.Errorf("expected tool1 metadata, got %+v", tool)
	}
}

func TestManager_RegisterWithError(t *testing.T) {
	mgr := newTestManager()

	cfg := &MCPConfig{Name: "broken", Type: MCPTypeStdio, Description: "broken mcp", IsActive: true}
	mgr.Register(cfg, nil, "connection refused")

	errMsg := mgr.GetError("broken")
	if errMsg != "connection refused" {
		t.Errorf("expected error 'connection refused', got '%s'", errMsg)
	}

	infos := mgr.ListWithToolCount()
	if len(infos) != 1 {
		t.Fatalf("expected 1 MCP in ListWithToolCount, got %d", len(infos))
	}
	if infos[0].Error != "connection refused" {
		t.Errorf("expected error in MCPInfo, got '%s'", infos[0].Error)
	}
	if infos[0].ToolCount != 0 {
		t.Errorf("expected 0 tools for broken MCP, got %d", infos[0].ToolCount)
	}
}

func TestManager_RegisterBuiltinTools(t *testing.T) {
	mgr := newTestManager()

	// Create a simple tool that implements tool.BaseTool via a mock approach is complex,
	// so we test with nil and verify it doesn't panic.
	// RegisterBuiltinTools with empty map should be safe.
	mgr.RegisterBuiltinTools(map[string]tool.BaseTool{})

	// GetTools should return empty list (no MCP tools, no builtin tools)
	tools := mgr.GetTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestManager_ListTools(t *testing.T) {
	mgr := newTestManager()

	cfg := &MCPConfig{Name: "mcp1", Type: MCPTypeStdio, Description: "mcp1", IsActive: true}
	tools := []ToolDefinition{
		{Name: "read", Description: "Read file"},
		{Name: "write", Description: "Write file"},
	}
	mgr.Register(cfg, tools, "")

	list := mgr.ListTools()
	if len(list) != 2 {
		t.Errorf("expected 2 tools, got %d", len(list))
	}

	names := make(map[string]bool)
	for _, ti := range list {
		names[ti.Name] = true
		if ti.MCP != "mcp1" {
			t.Errorf("expected MCP 'mcp1' for tool %s, got '%s'", ti.Name, ti.MCP)
		}
	}
	if !names["read"] || !names["write"] {
		t.Errorf("expected tools 'read' and 'write', got %v", names)
	}
}

func TestManager_ListWithToolCount(t *testing.T) {
	mgr := newTestManager()

	mgr.Register(&MCPConfig{Name: "mcp1", Type: MCPTypeStdio, Description: "mcp1", IsActive: true},
		[]ToolDefinition{{Name: "a"}, {Name: "b"}}, "")
	mgr.Register(&MCPConfig{Name: "mcp2", Type: MCPTypeSSE, Description: "mcp2", IsActive: false},
		[]ToolDefinition{{Name: "c"}}, "")
	mgr.Register(&MCPConfig{Name: "mcp3", Type: MCPTypeStreamableHTTP, Description: "mcp3", IsActive: true},
		nil, "timeout")

	infos := mgr.ListWithToolCount()
	if len(infos) != 3 {
		t.Fatalf("expected 3 MCP infos, got %d", len(infos))
	}

	for _, info := range infos {
		switch info.Name {
		case "mcp1":
			if info.ToolCount != 2 || info.Error != "" || !info.IsActive {
				t.Errorf("mcp1: unexpected state %+v", info)
			}
		case "mcp2":
			if info.ToolCount != 1 || info.IsActive {
				t.Errorf("mcp2: unexpected state %+v", info)
			}
		case "mcp3":
			if info.ToolCount != 0 || info.Error != "timeout" {
				t.Errorf("mcp3: unexpected state %+v", info)
			}
		}
	}
}

func TestManager_Unregister(t *testing.T) {
	mgr := newTestManager()

	cfg := &MCPConfig{Name: "to_remove", Type: MCPTypeStdio, Description: "temp", IsActive: true}
	tools := []ToolDefinition{{Name: "t1"}, {Name: "t2"}}
	mgr.Register(cfg, tools, "")

	if mgr.Count() != 1 {
		t.Fatalf("expected 1 MCP before unregister, got %d", mgr.Count())
	}

	mgr.Unregister("to_remove")

	if mgr.Count() != 0 {
		t.Errorf("expected 0 MCP after unregister, got %d", mgr.Count())
	}
	if mgr.ToolCount() != 0 {
		t.Errorf("expected 0 tools after unregister, got %d", mgr.ToolCount())
	}
	if _, ok := mgr.Get("to_remove"); ok {
		t.Error("expected MCP 'to_remove' to be removed")
	}
}

func TestManager_GetTools_Empty(t *testing.T) {
	mgr := newTestManager()

	tools := mgr.GetTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools from empty manager, got %d", len(tools))
	}
}

func TestManager_List_Empty(t *testing.T) {
	mgr := newTestManager()

	list := mgr.List()
	if len(list) != 0 {
		t.Errorf("expected 0 MCPs, got %d", len(list))
	}
}

func TestConfigParsing(t *testing.T) {
	// Test with tools array (pre-defined / name-filtered)
	jsonData := `{
		"name": "test_mcp",
		"type": "stdio",
		"description": "Test MCP Server",
		"isActive": true,
		"command": "test-server",
		"args": ["--verbose"],
		"env": {"DEBUG": "true"},
		"tools": [
			{"name": "tool_a", "description": "Tool A"},
			{"name": "tool_b"}
		]
	}`

	var cfg MCPConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if cfg.Name != "test_mcp" {
		t.Errorf("expected name 'test_mcp', got '%s'", cfg.Name)
	}
	if cfg.Type != MCPTypeStdio {
		t.Errorf("expected type stdio, got '%s'", cfg.Type)
	}
	if cfg.Description != "Test MCP Server" {
		t.Errorf("unexpected description: '%s'", cfg.Description)
	}
	if !cfg.IsActive {
		t.Error("expected isActive=true")
	}
	if cfg.Command != "test-server" {
		t.Errorf("expected command 'test-server', got '%s'", cfg.Command)
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "--verbose" {
		t.Errorf("unexpected args: %v", cfg.Args)
	}
	if cfg.Env["DEBUG"] != "true" {
		t.Errorf("unexpected env: %v", cfg.Env)
	}

	// Verify tools are parsed correctly
	if len(cfg.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(cfg.Tools))
	}
	if cfg.Tools[0].Name != "tool_a" || cfg.Tools[0].Description != "Tool A" {
		t.Errorf("unexpected tool 0: %+v", cfg.Tools[0])
	}
	if cfg.Tools[1].Name != "tool_b" {
		t.Errorf("unexpected tool 1: %+v", cfg.Tools[1])
	}
}

func TestConfigParsing_WithoutTools(t *testing.T) {
	// Test without tools (auto-discovery mode)
	jsonData := `{
		"name": "auto_mcp",
		"type": "sse",
		"description": "Auto discovery MCP",
		"isActive": true,
		"baseUrl": "https://example.com/mcp"
	}`

	var cfg MCPConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if cfg.Name != "auto_mcp" {
		t.Errorf("expected name 'auto_mcp', got '%s'", cfg.Name)
	}
	if cfg.Type != MCPTypeSSE {
		t.Errorf("expected type sse, got '%s'", cfg.Type)
	}
	if cfg.BaseURL != "https://example.com/mcp" {
		t.Errorf("unexpected baseUrl: '%s'", cfg.BaseURL)
	}
	if len(cfg.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(cfg.Tools))
	}
}

func TestConfigParsing_Inactive(t *testing.T) {
	jsonData := `{
		"name": "disabled_mcp",
		"type": "stdio",
		"description": "Disabled MCP",
		"isActive": false
	}`

	var cfg MCPConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if cfg.IsActive {
		t.Error("expected isActive=false")
	}
	if cfg.Command != "" {
		t.Errorf("expected empty command, got '%s'", cfg.Command)
	}
}

func TestConfigParsing_StreamableHTTP(t *testing.T) {
	jsonData := `{
		"name": "http_mcp",
		"type": "streamable_http",
		"description": "HTTP MCP Server",
		"isActive": true,
		"baseUrl": "https://api.example.com/mcp",
		"headers": {
			"Authorization": "Bearer token123",
			"X-Custom": "value"
		}
	}`

	var cfg MCPConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if cfg.Type != MCPTypeStreamableHTTP {
		t.Errorf("expected type streamable_http, got '%s'", cfg.Type)
	}
	if cfg.BaseURL != "https://api.example.com/mcp" {
		t.Errorf("unexpected baseUrl: '%s'", cfg.BaseURL)
	}
	if cfg.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("unexpected header Authorization: '%s'", cfg.Headers["Authorization"])
	}
	if cfg.Headers["X-Custom"] != "value" {
		t.Errorf("unexpected header X-Custom: '%s'", cfg.Headers["X-Custom"])
	}
}

func TestConfigParsing_MissingName(t *testing.T) {
	jsonData := `{"type": "stdio", "description": "No name", "isActive": true}`

	var cfg MCPConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("JSON parse should succeed even without name: %v", err)
	}
	if cfg.Name != "" {
		t.Errorf("expected empty name, got '%s'", cfg.Name)
	}
}

func TestLoadAll_DirectoryNotExists(t *testing.T) {
	mgr := newTestManager()

	err := mgr.LoadAll("/nonexistent/dir/path")
	if err != nil {
		t.Errorf("LoadAll should return nil for non-existent dir, got: %v", err)
	}
	if mgr.Count() != 0 {
		t.Errorf("expected 0 MCPs, got %d", mgr.Count())
	}
}

func TestLoadAll_EmptyDirectory(t *testing.T) {
	mgr := newTestManager()

	dir := t.TempDir()
	err := mgr.LoadAll(dir)
	if err != nil {
		t.Errorf("LoadAll should succeed for empty dir, got: %v", err)
	}
	if mgr.Count() != 0 {
		t.Errorf("expected 0 MCPs, got %d", mgr.Count())
	}
}

func TestLoadAll_IgnoresNonJSON(t *testing.T) {
	mgr := newTestManager()

	dir := t.TempDir()
	// Create a non-JSON file
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	// Create a subdirectory (should be ignored)
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	err := mgr.LoadAll(dir)
	if err != nil {
		t.Errorf("LoadAll should succeed: %v", err)
	}
	if mgr.Count() != 0 {
		t.Errorf("expected 0 MCPs (non-JSON files ignored), got %d", mgr.Count())
	}
}

func TestLoadAll_InvalidJSON(t *testing.T) {
	mgr := newTestManager()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Should not error — LoadAll skips invalid files and logs
	err := mgr.LoadAll(dir)
	if err != nil {
		t.Errorf("LoadAll should succeed (skip invalid files), got: %v", err)
	}
	if mgr.Count() != 0 {
		t.Errorf("expected 0 MCPs (invalid JSON skipped), got %d", mgr.Count())
	}
}

func TestManager_Close(t *testing.T) {
	mgr := newTestManager()

	// Close on empty manager should not panic
	mgr.Close()
}

func TestToolDefinition(t *testing.T) {
	td := ToolDefinition{
		Name:        "my_tool",
		Description: "My custom tool",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
		},
	}

	if td.Name != "my_tool" {
		t.Errorf("unexpected name: %s", td.Name)
	}
	if td.Description != "My custom tool" {
		t.Errorf("unexpected description: %s", td.Description)
	}
	if td.InputSchema["type"] != "object" {
		t.Errorf("unexpected inputSchema type: %v", td.InputSchema["type"])
	}
}

func TestToolInfo(t *testing.T) {
	ti := &ToolInfo{
		Name:        "read_file",
		Description: "Read a file",
		MCP:         "filesystem",
	}

	if ti.Name != "read_file" {
		t.Errorf("unexpected name: %s", ti.Name)
	}
	if ti.MCP != "filesystem" {
		t.Errorf("unexpected MCP: %s", ti.MCP)
	}
}

func TestMCPTypeConstants(t *testing.T) {
	if MCPTypeStdio != "stdio" {
		t.Errorf("expected 'stdio', got '%s'", MCPTypeStdio)
	}
	if MCPTypeSSE != "sse" {
		t.Errorf("expected 'sse', got '%s'", MCPTypeSSE)
	}
	if MCPTypeStreamableHTTP != "streamable_http" {
		t.Errorf("expected 'streamable_http', got '%s'", MCPTypeStreamableHTTP)
	}
}

// Test that buildEnv correctly expands environment variables
func TestBuildEnv(t *testing.T) {
	os.Setenv("TEST_MCP_VAR", "test_value")
	defer os.Unsetenv("TEST_MCP_VAR")

	envMap := map[string]string{
		"CUSTOM_VAR": "${TEST_MCP_VAR}",
	}

	result := buildEnv(envMap)
	if len(result) == 0 {
		t.Fatal("expected non-empty env")
	}

	found := false
	for _, e := range result {
		if e == "CUSTOM_VAR=test_value" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find 'CUSTOM_VAR=test_value' in env, got %v", result)
	}
}

func TestBuildEnv_Empty(t *testing.T) {
	result := buildEnv(nil)
	if result != nil {
		t.Errorf("expected nil for empty env, got %v", result)
	}

	result = buildEnv(map[string]string{})
	if result != nil {
		t.Errorf("expected nil for empty env map, got %v", result)
	}
}

func TestExpandArgs(t *testing.T) {
	os.Setenv("TEST_ARG_VAR", "expanded")
	defer os.Unsetenv("TEST_ARG_VAR")

	args := []string{"--path", "${TEST_ARG_VAR}/data", "--flag"}
	result := expandArgs(args)

	if len(result) != 3 {
		t.Fatalf("expected 3 args, got %d", len(result))
	}
	if result[1] != "expanded/data" {
		t.Errorf("expected 'expanded/data', got '%s'", result[1])
	}
}

func TestExpandHeaders(t *testing.T) {
	os.Setenv("TEST_HEADER_VAR", "secret123")
	defer os.Unsetenv("TEST_HEADER_VAR")

	headers := map[string]string{
		"Authorization": "Bearer ${TEST_HEADER_VAR}",
		"X-Static":      "static-value",
	}

	result := expandHeaders(headers)

	if result["Authorization"] != "Bearer secret123" {
		t.Errorf("expected 'Bearer secret123', got '%s'", result["Authorization"])
	}
	if result["X-Static"] != "static-value" {
		t.Errorf("expected 'static-value', got '%s'", result["X-Static"])
	}
}

func TestExpandHeaders_Empty(t *testing.T) {
	result := expandHeaders(nil)
	if result != nil {
		t.Errorf("expected nil for nil headers, got %v", result)
	}

	result = expandHeaders(map[string]string{})
	if len(result) != 0 {
		t.Errorf("expected empty map for empty headers, got %v", result)
	}
}

func TestManager_RegisterLocked(t *testing.T) {
	mgr := newTestManager()

	cfg := &MCPConfig{Name: "locked_test", Type: MCPTypeStdio, Description: "test", IsActive: true}
	tools := []ToolDefinition{
		{Name: "tool_x", Description: "X", InputSchema: map[string]interface{}{"type": "object"}},
	}

	// registerLocked is called internally; verify via Register
	mgr.Register(cfg, tools, "")

	c, ok := mgr.Get("locked_test")
	if !ok || c.Name != "locked_test" {
		t.Errorf("expected to find MCP 'locked_test'")
	}

	ti, ok := mgr.GetTool("tool_x")
	if !ok {
		t.Errorf("expected to find tool 'tool_x'")
	}
	if ti.InputSchema == nil || ti.InputSchema["type"] != "object" {
		t.Errorf("expected InputSchema with type=object, got %v", ti.InputSchema)
	}
}

func TestLoad_MissingNameField(t *testing.T) {
	mgr := newTestManager()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "noname.json")
	config := `{"type": "stdio", "description": "No name field", "isActive": true}`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := mgr.Load(configPath)
	if err == nil {
		t.Error("expected error for missing name field")
	}
}

func TestLoad_InactiveConfig(t *testing.T) {
	mgr := newTestManager()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "inactive.json")
	config := `{"name": "inactive_mcp", "type": "stdio", "description": "Inactive", "isActive": false}`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := mgr.Load(configPath)
	if err != nil {
		t.Errorf("expected no error for inactive MCP, got: %v", err)
	}
	if mgr.Count() != 0 {
		t.Errorf("expected 0 MCPs (inactive skipped), got %d", mgr.Count())
	}
}

func TestLoad_NonexistentFile(t *testing.T) {
	mgr := newTestManager()

	err := mgr.Load("/nonexistent/file.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestMCPInfoStruct(t *testing.T) {
	info := MCPInfo{
		Name:        "test",
		Type:        MCPTypeStdio,
		Description: "desc",
		IsActive:    true,
		ToolCount:   5,
		Error:       "",
	}

	if info.ToolCount != 5 {
		t.Errorf("expected ToolCount=5, got %d", info.ToolCount)
	}
	if info.Error != "" {
		t.Errorf("expected empty error, got '%s'", info.Error)
	}
}

// Test that Config struct correctly serializes/deserializes the tools field
func TestConfigToolsFieldRoundTrip(t *testing.T) {
	cfg := MCPConfig{
		Name:        "filtered",
		Type:        MCPTypeStdio,
		Description: "Filtered MCP",
		IsActive:    true,
		Command:     "server",
		Tools: []ToolDefinition{
			{Name: "tool_a"},
			{Name: "tool_b", Description: "custom desc"},
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed MCPConfig
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(parsed.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(parsed.Tools))
	}
	if parsed.Tools[0].Name != "tool_a" {
		t.Errorf("expected tool_a, got %s", parsed.Tools[0].Name)
	}
	if parsed.Tools[1].Name != "tool_b" {
		t.Errorf("expected tool_b, got %s", parsed.Tools[1].Name)
	}
	if parsed.Tools[1].Description != "custom desc" {
		t.Errorf("expected 'custom desc', got '%s'", parsed.Tools[1].Description)
	}
}

// Ensure that RegisterBuiltinTools doesn't crash with nil logger
func TestNewManager_NilLogger(t *testing.T) {
	mgr := newTestManager()
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.Count() != 0 {
		t.Errorf("expected 0 MCPs in fresh manager, got %d", mgr.Count())
	}
}

// Context-based test helper for GetTools type assertion
func TestManager_GetTools_Type(t *testing.T) {
	mgr := newTestManager()
	tools := mgr.GetTools()
	// GetTools returns []tool.BaseTool; verify it's a slice
	if tools == nil {
		t.Error("GetTools should return non-nil slice")
	}
}

// Test that multiple registrations with same MCP name overwrite
func TestManager_RegisterOverwrite(t *testing.T) {
	mgr := newTestManager()

	cfg1 := &MCPConfig{Name: "same", Type: MCPTypeStdio, Description: "first", IsActive: true}
	mgr.Register(cfg1, []ToolDefinition{{Name: "t1"}}, "")

	cfg2 := &MCPConfig{Name: "same", Type: MCPTypeSSE, Description: "second", IsActive: true}
	mgr.Register(cfg2, []ToolDefinition{{Name: "t2"}}, "")

	if mgr.Count() != 1 {
		t.Errorf("expected 1 MCP (overwritten), got %d", mgr.Count())
	}

	c, _ := mgr.Get("same")
	if c.Description != "second" {
		t.Errorf("expected description 'second', got '%s'", c.Description)
	}
	if c.Type != MCPTypeSSE {
		t.Errorf("expected type sse, got '%s'", c.Type)
	}
}
