package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// ToolExecutor executes MCP tools via different transport types
type ToolExecutor struct {
	processes map[string]*Process
	mu        sync.Mutex
	log       *logger.Logger
}

// Process represents a running stdio MCP process
type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
}

// NewToolExecutor creates a new tool executor
func NewToolExecutor(log *logger.Logger) *ToolExecutor {
	return &ToolExecutor{
		processes: make(map[string]*Process),
		log:       log,
	}
}

// ExecuteStdio runs a stdio MCP tool via JSON-RPC
func (e *ToolExecutor) ExecuteStdio(ctx context.Context, tool *MCPConfig, toolName, argsJSON string) (string, error) {
	e.mu.Lock()
	proc, ok := e.processes[tool.Name]
	if !ok {
		// Start new process
		cmd := exec.CommandContext(ctx, tool.Command, tool.Args...)

		// Set environment variables
		cmd.Env = os.Environ()
		if tool.Env != nil {
			for k, v := range tool.Env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, os.ExpandEnv(v)))
			}
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			e.mu.Unlock()
			return "", fmt.Errorf("failed to create stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			e.mu.Unlock()
			return "", fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			e.mu.Unlock()
			return "", fmt.Errorf("failed to start process: %w", err)
		}

		proc = &Process{
			cmd:    cmd,
			stdin:  stdin,
			stdout: stdout,
		}
		e.processes[tool.Name] = proc

		e.log.Info("Started MCP process", zap.String("name", tool.Name), zap.String("command", tool.Command))
	}
	e.mu.Unlock()

	// Send JSON-RPC request
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": json.RawMessage(argsJSON),
		},
		"id": time.Now().UnixNano(),
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	reqBytes = append(reqBytes, '\n')

	if _, err := proc.stdin.Write(reqBytes); err != nil {
		return "", fmt.Errorf("failed to write to stdin: %w", err)
	}

	// Read response
	reader := bufio.NewReader(proc.stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var response map[string]interface{}
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if result, ok := response["result"]; ok {
		content, _ := json.Marshal(result)
		return string(content), nil
	}

	if errMsg, ok := response["error"]; ok {
		return "", fmt.Errorf("MCP error: %v", errMsg)
	}

	return "", fmt.Errorf("invalid response format")
}

// ExecuteSSE runs a SSE MCP tool via HTTP with Server-Sent Events
func (e *ToolExecutor) ExecuteSSE(ctx context.Context, tool *MCPConfig, toolName, argsJSON string) (string, error) {
	// Build request body for MCP protocol
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": json.RawMessage(argsJSON),
		},
		"id": time.Now().UnixNano(),
	}
	bodyBytes, _ := json.Marshal(body)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", tool.BaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for k, v := range tool.Headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}

	// Execute request
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read SSE stream
	scanner := bufio.NewScanner(resp.Body)
	var result string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			result += data
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return result, nil
}

// ExecuteHTTP runs a streamable_http MCP tool via HTTP
func (e *ToolExecutor) ExecuteHTTP(ctx context.Context, tool *MCPConfig, toolName, argsJSON string) (string, error) {
	// Build request URL
	url := tool.BaseURL
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}
	url += "tools/" + toolName

	// Build request body
	bodyBytes := []byte(argsJSON)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range tool.Headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

// Close terminates all running MCP processes
func (e *ToolExecutor) Close() {
	e.mu.Lock()
	for name, proc := range e.processes {
		proc.stdin.Close()
		proc.cmd.Wait()
		e.log.Info("Stopped MCP process", zap.String("name", name))
	}
	e.processes = make(map[string]*Process)
	e.mu.Unlock()
}

// ListProcesses returns names of running processes
func (e *ToolExecutor) ListProcesses() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	names := make([]string, 0, len(e.processes))
	for name := range e.processes {
		names = append(names, name)
	}
	return names
}

// ProcessCount returns number of running processes
func (e *ToolExecutor) ProcessCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.processes)
}