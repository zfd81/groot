package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMcpFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantSub   string
		wantError bool
		errMsg    string
	}{
		{
			name:    "list subcommand",
			args:    []string{"list"},
			wantSub: "list",
		},
		{
			name:      "no arguments",
			args:      []string{},
			wantError: true,
			errMsg:    "缺少子命令: list",
		},
		{
			name:      "unknown subcommand",
			args:      []string{"delete"},
			wantError: true,
			errMsg:    "未知子命令: delete (可用: list)",
		},
		{
			name:      "list with unexpected arg",
			args:      []string{"list", "extra"},
			wantError: true,
			errMsg:    "unexpected argument: extra",
		},
		{
			name:      "unknown flag",
			args:      []string{"list", "--invalid"},
			wantError: true,
			errMsg:    "unknown flag: --invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, err := ParseMcpFlags(tt.args)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if err.Error() != tt.errMsg {
					t.Errorf("expected error '%s' but got '%s'", tt.errMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if flags.Subcommand != tt.wantSub {
				t.Errorf("expected subcommand '%s' but got '%s'", tt.wantSub, flags.Subcommand)
			}
		})
	}
}

func TestMcpList(t *testing.T) {
	tmpDir := t.TempDir()
	mcpDir := filepath.Join(tmpDir, "mcp")
	os.MkdirAll(mcpDir, 0755)

	// Valid active MCP config
	os.WriteFile(filepath.Join(mcpDir, "web-search.json"), []byte(`{
  "name": "web-search",
  "type": "stdio",
  "description": "基于 SearXNG 的网页搜索",
  "isActive": true,
  "command": "node",
  "args": ["/path/to/server.js"]
}`), 0644)

	// Valid inactive MCP config
	os.WriteFile(filepath.Join(mcpDir, "database.json"), []byte(`{
  "name": "database",
  "type": "streamable_http",
  "description": "数据库查询服务",
  "isActive": false
}`), 0644)

	// Invalid JSON (parse failure)
	os.WriteFile(filepath.Join(mcpDir, "broken.json"), []byte(`{invalid json`), 0644)

	// A non-JSON file should be ignored
	os.WriteFile(filepath.Join(mcpDir, "readme.txt"), []byte("hello"), 0644)

	// A directory should be ignored
	os.MkdirAll(filepath.Join(mcpDir, "subdir"), 0755)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := mcpList(mcpDir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	checks := []string{
		"NAME",
		"TYPE",
		"STATUS",
		"LAST_UPDATED",
		"DESCRIPTION",
		"web-search",
		"stdio",
		"active",
		"基于 SearXNG 的网页搜索",
		"database",
		"streamable_http",
		"inactive",
		"数据库查询服务",
		"broken",
		"配置解析失败",
		"共 3 个 MCP Server",
		"1 个活跃",
		"1 个未激活",
		"1 个异常",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain '%s', got:\n%s", check, output)
		}
	}
}

func TestMcpList_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	mcpDir := filepath.Join(tmpDir, "mcp")
	os.MkdirAll(mcpDir, 0755)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := mcpList(mcpDir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "未配置任何 MCP Server") {
		t.Errorf("output should contain '未配置任何 MCP Server', got:\n%s", output)
	}
}

func TestMcpList_NonexistentDir(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := mcpList("/nonexistent/path/mcp")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "未配置任何 MCP Server") {
		t.Errorf("output should contain '未配置任何 MCP Server', got:\n%s", output)
	}
}

func TestMcpList_OnlyNonJsonFiles(t *testing.T) {
	tmpDir := t.TempDir()
	mcpDir := filepath.Join(tmpDir, "mcp")
	os.MkdirAll(mcpDir, 0755)

	os.WriteFile(filepath.Join(mcpDir, "readme.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(mcpDir, "notes.md"), []byte("notes"), 0644)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := mcpList(mcpDir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "未配置任何 MCP Server") {
		t.Errorf("output should contain '未配置任何 MCP Server', got:\n%s", output)
	}
}

func TestRunMcp_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	mcpDir := filepath.Join(tmpDir, "mcp")
	os.MkdirAll(mcpDir, 0755)

	// Create a valid MCP config
	os.WriteFile(filepath.Join(mcpDir, "test-server.json"), []byte(`{
  "name": "test-server",
  "type": "sse",
  "description": "Test SSE server",
  "isActive": true
}`), 0644)

	oldHome := os.Getenv("GROOT_HOME")
	os.Setenv("GROOT_HOME", tmpDir)
	defer os.Setenv("GROOT_HOME", oldHome)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := RunMcp(&McpFlags{Subcommand: "list"})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "test-server") {
		t.Errorf("output should contain 'test-server', got:\n%s", output)
	}
	if !strings.Contains(output, "Test SSE server") {
		t.Errorf("output should contain description, got:\n%s", output)
	}
}
