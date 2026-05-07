package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zfd81/groot/internal/api/types"
)

func TestParseStatusFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantPort  int
		wantError bool
		errMsg    string
	}{
		{
			name:      "default values",
			args:      []string{},
			wantPort:  0,
			wantError: false,
		},
		{
			name:      "valid port short",
			args:      []string{"-p", "8080"},
			wantPort:  8080,
			wantError: false,
		},
		{
			name:      "port invalid string",
			args:      []string{"-p", "abc"},
			wantError: true,
			errMsg:    "invalid value for -p: abc",
		},
		{
			name:      "port negative",
			args:      []string{"-p", "-5"},
			wantError: true,
			errMsg:    "port must be 1-65535",
		},
		{
			name:      "port zero",
			args:      []string{"-p", "0"},
			wantError: true,
			errMsg:    "port must be 1-65535",
		},
		{
			name:      "port out of range",
			args:      []string{"-p", "99999"},
			wantError: true,
			errMsg:    "port must be 1-65535",
		},
		{
			name:      "-p without value",
			args:      []string{"-p"},
			wantError: true,
			errMsg:    "-p requires a value",
		},
		{
			name:      "unknown flag",
			args:      []string{"--invalid"},
			wantError: true,
			errMsg:    "unknown flag: --invalid",
		},
		{
			name:      "unexpected argument",
			args:      []string{"unexpected"},
			wantError: true,
			errMsg:    "unexpected argument: unexpected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, err := ParseStatusFlags(tt.args)

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

			if flags.Port != tt.wantPort {
				t.Errorf("expected port %d but got %d", tt.wantPort, flags.Port)
			}
		})
	}
}

func TestFetchHealthStatus_Success(t *testing.T) {
	expected := types.HealthResponse{
		Status:  "healthy",
		Version: "1.0.0",
		Uptime:  "2h35m",
		Checks: map[string]types.CheckInfo{
			"llm": {
				Status: "healthy",
				Info:   map[string]interface{}{"model": "gpt-4o"},
			},
			"mcp_servers": {
				Status: "healthy",
				Info:   []interface{}{},
			},
			"skills": {
				Status: "healthy",
				Info:   map[string]interface{}{"count": float64(5)},
			},
			"memory": {
				Status: "healthy",
				Info:   map[string]interface{}{"sessions": float64(12)},
			},
		},
		Metrics: map[string]interface{}{"chats_running": float64(1)},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(expected)
	}))
	defer server.Close()

	var port int
	fmt.Sscanf(server.URL, "http://127.0.0.1:%d", &port)

	health, err := fetchHealthStatus(port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if health.Status != "healthy" {
		t.Errorf("expected status 'healthy' but got '%s'", health.Status)
	}
	if health.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0' but got '%s'", health.Version)
	}
	if health.Uptime != "2h35m" {
		t.Errorf("expected uptime '2h35m' but got '%s'", health.Uptime)
	}
}

func TestFetchHealthStatus_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	var port int
	fmt.Sscanf(server.URL, "http://127.0.0.1:%d", &port)

	_, err := fetchHealthStatus(port)
	if err == nil {
		t.Fatalf("expected error for non-200 response")
	}
}

func TestFetchHealthStatus_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	var port int
	fmt.Sscanf(server.URL, "http://127.0.0.1:%d", &port)

	_, err := fetchHealthStatus(port)
	if err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}

func TestPrintNotRunning(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printNotRunning(8080)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if output == "" {
		t.Error("expected non-empty output")
	}
	if !bytes.Contains([]byte(output), []byte("8080")) {
		t.Error("output should contain port number 8080")
	}
}

func TestPrintStatusOutput_Healthy(t *testing.T) {
	health := &types.HealthResponse{
		Status:  "healthy",
		Version: "1.0.0",
		Uptime:  "2h35m",
		Checks: map[string]types.CheckInfo{
			"llm": {
				Status: "healthy",
				Info:   map[string]interface{}{"model": "gpt-4o"},
			},
			"mcp_servers": {
				Status: "healthy",
				Info:   []interface{}{map[string]interface{}{"name": "test-mcp"}, map[string]interface{}{"name": "test-mcp-2"}},
			},
			"skills": {
				Status: "healthy",
				Info:   map[string]interface{}{"count": float64(5)},
			},
			"memory": {
				Status: "healthy",
				Info:   map[string]interface{}{"sessions": float64(12)},
			},
		},
		Metrics: map[string]interface{}{"chats_running": float64(2)},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printStatusOutput(health, 8080)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	checks := []string{
		"Groot 实例状态",
		"healthy",
		"1.0.0",
		"2h35m",
		"8080",
		"LLM:",
		"gpt-4o",
		"MCP Servers:",
		"2 个",
		"Skills:",
		"5 个",
		"Memory:",
		"12 个会话",
		"活跃对话",
	}

	for _, check := range checks {
		if !bytes.Contains([]byte(output), []byte(check)) {
			t.Errorf("output should contain '%s', got:\n%s", check, output)
		}
	}
}

func TestPrintStatusOutput_LLMUnhealthy(t *testing.T) {
	health := &types.HealthResponse{
		Status:  "healthy",
		Version: "1.0.0",
		Uptime:  "10m",
		Checks: map[string]types.CheckInfo{
			"llm": {
				Status: "unhealthy",
				Info:   map[string]interface{}{"model": "gpt-4o", "error": "connection refused"},
			},
			"mcp_servers": {
				Status: "healthy",
				Info:   []interface{}{},
			},
			"skills": {
				Status: "healthy",
				Info:   map[string]interface{}{"count": float64(0)},
			},
			"memory": {
				Status: "healthy",
				Info:   map[string]interface{}{"sessions": float64(0)},
			},
		},
		Metrics: map[string]interface{}{"chats_running": float64(0)},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printStatusOutput(health, 9090)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("unhealthy")) {
		t.Error("output should contain unhealthy LLM status")
	}
	if !bytes.Contains([]byte(output), []byte("connection refused")) {
		t.Error("output should contain LLM error message")
	}
}

func TestRunStatus_IntegrationLike(t *testing.T) {
	// Create test health server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.HealthResponse{
			Status:  "healthy",
			Version: "1.0.0",
			Uptime:  "5m",
			Checks: map[string]types.CheckInfo{
				"llm": {
					Status: "healthy",
					Info:   map[string]interface{}{"model": "test-model"},
				},
				"mcp_servers": {
					Status: "healthy",
					Info:   []interface{}{},
				},
				"skills": {
					Status: "healthy",
					Info:   map[string]interface{}{"count": float64(3)},
				},
				"memory": {
					Status: "healthy",
					Info:   map[string]interface{}{"sessions": float64(5)},
				},
			},
			Metrics: map[string]interface{}{"chats_running": float64(0)},
		})
	}))
	defer server.Close()

	var port int
	fmt.Sscanf(server.URL, "http://127.0.0.1:%d", &port)

	// Create temp config with minimal LLM config to pass validation
	tmpDir := t.TempDir()
	configYAML := fmt.Sprintf("server:\n  port: %d\nllm:\n  default_model: test\n  models:\n    test:\n      base_url: http://localhost:11434/v1\n      api_key: test-key\n      model: test-model\n", port)
	os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(configYAML), 0644)

	// Override home dir for this test
	oldHome := os.Getenv("GROOT_HOME")
	os.Setenv("GROOT_HOME", tmpDir)
	defer os.Setenv("GROOT_HOME", oldHome)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := RunStatus(&StatusFlags{Port: 0})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("healthy")) {
		t.Error("output should contain healthy status")
	}
}

func TestRunStatus_NoInstance(t *testing.T) {
	// Use a port that's likely not in use
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := RunStatus(&StatusFlags{Port: 19999})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("未检测到")) {
		t.Error("output should contain '未检测到'")
	}
}
