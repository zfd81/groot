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
	"github.com/zfd81/groot/internal/plugin"
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
		if r.URL.Path != "/web/health" {
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

func TestPrintStatusOutput_ProcessInfo(t *testing.T) {
	health := &types.HealthResponse{
		Status:  "healthy",
		Version: "1.0.0",
		Uptime:  "1m",
		Checks: map[string]types.CheckInfo{
			"environment": {
				Status: "healthy",
				Info:   map[string]interface{}{"pid": "4242", "process_mode": "supervised"},
			},
		},
		Metrics: map[string]interface{}{},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	printStatusOutput(health, 8080)
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	for _, want := range []string{"进程模式:", "监督进程 + 工作进程", "PID:", "4242"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
}

func TestProcessModeLabel(t *testing.T) {
	cases := map[string]string{
		"supervised": "监督进程 + 工作进程（支持重启）",
		"single":     "单进程（不支持重启）",
		"":           "",
		"weird":      "weird",
	}
	for in, want := range cases {
		if got := processModeLabel(in); got != want {
			t.Errorf("processModeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStatusPort_FromBootstrap 端口取自 bootstrap.yaml
func TestStatusPort_FromBootstrap(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "bootstrap.yaml"), []byte("server:\n  port: 9191\n"), 0600)

	port, err := resolveStatusPort(home)
	if err != nil {
		t.Fatalf("resolveStatusPort: %v", err)
	}
	if port != 9191 {
		t.Errorf("port = %d, want 9191", port)
	}
}

// TestStatusPort_LegacyFallback bootstrap 缺失时回落老 config.yaml
func TestStatusPort_LegacyFallback(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("server:\n  port: 9292\n"), 0600)

	port, err := resolveStatusPort(home)
	if err != nil {
		t.Fatalf("resolveStatusPort: %v", err)
	}
	if port != 9292 {
		t.Errorf("port = %d, want 9292", port)
	}
}

// TestStatusPort_NoFiles 两个文件都没有时取默认端口
func TestStatusPort_NoFiles(t *testing.T) {
	port, err := resolveStatusPort(t.TempDir())
	if err != nil {
		t.Fatalf("resolveStatusPort: %v", err)
	}
	if port != 8080 {
		t.Errorf("port = %d, want 8080", port)
	}
}

// renderPluginsToString 用 renderPlugins 渲染单个检查项。
func renderPluginsToString(check types.CheckInfo) string {
	var buf bytes.Buffer
	renderPlugins(&buf, check)
	return buf.String()
}

// TestRenderPlugins_AllRunning 全部 running 时只显示汇总行。
func TestRenderPlugins_AllRunning(t *testing.T) {
	out := renderPluginsToString(types.CheckInfo{
		Status: "healthy",
		Info: plugin.Status{
			Process: plugin.ProcessStatus{Status: "running", PID: 1234},
			Plugins: []plugin.PluginStatus{
				{Name: "a", Status: "running"},
				{Name: "b", Status: "running"},
				{Name: "c", Status: "running"},
			},
		},
	})

	for _, want := range []string{"Plugins:", "healthy", "3/3 运行中", "进程 pid 1234"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
	// 全部 running 时不列明细：只有汇总行一行。
	if lines := bytes.Count([]byte(out), []byte("\n")); lines != 1 {
		t.Errorf("output should have exactly 1 line, got %d:\n%s", lines, out)
	}
}

// TestRenderPlugins_DegradedListsFailures 非 running 插件逐行列出名称、状态与原因。
func TestRenderPlugins_DegradedListsFailures(t *testing.T) {
	out := renderPluginsToString(types.CheckInfo{
		Status: "degraded",
		Info: plugin.Status{
			Process: plugin.ProcessStatus{Status: "running", PID: 1234},
			Plugins: []plugin.PluginStatus{
				{Name: "asr-router", Status: "running"},
				{Name: "ocr", Status: "failed", Error: "No module named 'torch'"},
				{Name: "broken", Status: "invalid", Error: "缺少 app/main.py"},
			},
		},
	})

	for _, want := range []string{
		"Plugins:",
		"degraded",
		"1/3 运行中",
		"进程 pid 1234",
		"    ocr         failed     No module named 'torch'",
		"    broken      invalid    缺少 app/main.py",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
	// running 的插件不出现在明细中
	if bytes.Contains([]byte(out), []byte("    asr-router")) {
		t.Errorf("running plugin should not be listed, got:\n%s", out)
	}
}

// TestRenderPlugins_NoPlugins 没有插件时显示 (0 个)。
func TestRenderPlugins_NoPlugins(t *testing.T) {
	out := renderPluginsToString(types.CheckInfo{
		Status: "healthy",
		Info:   plugin.Status{Process: plugin.ProcessStatus{Status: "disabled"}},
	})

	for _, want := range []string{"Plugins:", "healthy", "(0 个)"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
}

// TestRenderPlugins_ProcessNotRunning 进程未运行时汇总行显示进程状态、原因与重启次数。
func TestRenderPlugins_ProcessNotRunning(t *testing.T) {
	out := renderPluginsToString(types.CheckInfo{
		Status: "degraded",
		Info: plugin.Status{
			Process: plugin.ProcessStatus{Status: "starting", Restarts: 2, Error: "等待就绪行超时"},
			Plugins: []plugin.PluginStatus{
				{Name: "ocr", Status: "pending"},
			},
		},
	})

	for _, want := range []string{
		"Plugins:",
		"degraded",
		"进程 starting: 等待就绪行超时, 已重启 2 次",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
}

// TestRenderPlugins_JSONDecodedInfo 处理 JSON 解码后的 map 形态（生产路径）。
func TestRenderPlugins_JSONDecodedInfo(t *testing.T) {
	out := renderPluginsToString(types.CheckInfo{
		Status: "degraded",
		Info: map[string]interface{}{
			"process": map[string]interface{}{
				"status":   "running",
				"pid":      float64(1234),
				"restarts": float64(0),
			},
			"plugins": []interface{}{
				map[string]interface{}{"name": "ocr", "status": "failed", "error": "boom"},
			},
		},
	})

	for _, want := range []string{
		"Plugins:",
		"degraded",
		"0/1 运行中",
		"进程 pid 1234",
		"ocr",
		"failed",
		"boom",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
}
