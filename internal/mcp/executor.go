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
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.Reader
	reader      *bufio.Reader // Persistent reader to avoid buffer loss
	stderr      io.Reader     // stderr to prevent buffer blocking
	initialized bool
}

// NewToolExecutor creates a new tool executor
func NewToolExecutor(log *logger.Logger) *ToolExecutor {
	return &ToolExecutor{
		processes: make(map[string]*Process),
		log:       log,
	}
}

// DiscoverTools discovers tools from MCP server via tools/list
func (e *ToolExecutor) DiscoverTools(ctx context.Context, config *MCPConfig) ([]ToolDefinition, error) {
	switch config.Type {
	case MCPTypeStdio:
		return e.discoverStdio(ctx, config)
	case MCPTypeSSE:
		return e.discoverSSE(ctx, config)
	case MCPTypeStreamableHTTP:
		return e.discoverHTTP(ctx, config)
	default:
		return nil, fmt.Errorf("unsupported MCP type: %s", config.Type)
	}
}

// initializeStdio performs MCP initialization handshake for stdio
func (e *ToolExecutor) initializeStdio(ctx context.Context, proc *Process, config *MCPConfig) error {
	// Send initialize request
	initRequest := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"roots":    map[string]interface{}{"listChanged": true},
				"sampling": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    "groot",
				"version": "1.0.0",
			},
		},
	}

	reqBytes, err := json.Marshal(initRequest)
	if err != nil {
		return err
	}
	reqBytes = append(reqBytes, '\n')

	if _, err := proc.stdin.Write(reqBytes); err != nil {
		return fmt.Errorf("failed to send initialize: %w", err)
	}

	// Read initialize response
	reader := proc.reader
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read initialize response: %w", err)
	}

	var initResp map[string]interface{}
	if err := json.Unmarshal([]byte(line), &initResp); err != nil {
		return fmt.Errorf("failed to parse initialize response: %w", err)
	}

	if initResp["error"] != nil {
		return fmt.Errorf("initialize error: %v", initResp["error"])
	}

	// Send initialized notification
	notifRequest := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}

	notifBytes, err := json.Marshal(notifRequest)
	if err != nil {
		return err
	}
	notifBytes = append(notifBytes, '\n')

	if _, err := proc.stdin.Write(notifBytes); err != nil {
		return fmt.Errorf("failed to send initialized notification: %w", err)
	}

	e.log.Info("MCP initialized notification sent")

	// Mark as initialized - don't block waiting for roots/list
	// roots/list is an optional request from server, handle during tool calls if needed
	proc.initialized = true
	e.log.Info("MCP initialization complete")

	return nil
}

// discoverStdio discovers tools from stdio MCP via JSON-RPC tools/list
func (e *ToolExecutor) discoverStdio(ctx context.Context, config *MCPConfig) ([]ToolDefinition, error) {
	e.mu.Lock()

	// Start process if not running
	proc, ok := e.processes[config.Name]
	if !ok {
		// Use context.Background() for the process to avoid being killed when discovery context is cancelled
		// The process should persist for tool execution after discovery
		cmd := exec.CommandContext(context.Background(), config.Command, config.Args...)

		cmd.Env = os.Environ()
		if config.Env != nil {
			for k, v := range config.Env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, os.ExpandEnv(v)))
			}
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
		}

		// Drain stderr to prevent blocking
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				e.log.Info("[MCP stderr] " + scanner.Text())
			}
		}()

		if err := cmd.Start(); err != nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("failed to start process: %w", err)
		}

		proc = &Process{
			cmd:         cmd,
			stdin:       stdin,
			stdout:      stdout,
			reader:      bufio.NewReader(stdout),
			stderr:      stderr,
			initialized: false,
		}
		e.processes[config.Name] = proc

		e.log.Info("Started MCP process for discovery", zap.String("name", config.Name), zap.String("command", config.Command))
	}
	e.mu.Unlock()

	// Initialize if not done
	if !proc.initialized {
		if err := e.initializeStdio(ctx, proc, config); err != nil {
			return nil, err
		}
	}

	// Send tools/list request
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	reqBytes = append(reqBytes, '\n')

	if _, err := proc.stdin.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("failed to write to stdin: %w", err)
	}

	// Read responses until we get the tools/list response (id=2)
	reader := proc.reader

	var toolsResponse struct {
		Result struct {
			Tools []struct {
				Name        string                 `json:"name"`
				Description string                 `json:"description"`
				InputSchema map[string]interface{} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ID     float64 `json:"id"`
		Method string  `json:"method"`
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		e.log.Info("Discovery response: " + strings.TrimSpace(line))

		if err := json.Unmarshal([]byte(line), &toolsResponse); err != nil {
			continue
		}

		// If it's our tools/list response (id=2), we're done
		if toolsResponse.ID == 2 {
			break
		}
	}

	if toolsResponse.Error != nil {
		return nil, fmt.Errorf("MCP error: %d - %s", toolsResponse.Error.Code, toolsResponse.Error.Message)
	}

	// Convert to ToolDefinition
	tools := make([]ToolDefinition, len(toolsResponse.Result.Tools))
	for i, t := range toolsResponse.Result.Tools {
		tools[i] = ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}

	e.log.Info("Discovered tools from MCP", zap.String("name", config.Name), zap.Int("count", len(tools)))

	return tools, nil
}

// discoverSSE discovers tools from SSE MCP via HTTP
func (e *ToolExecutor) discoverSSE(ctx context.Context, config *MCPConfig) ([]ToolDefinition, error) {
	// Build initialize request
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "groot",
				"version": "1.0.0",
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", config.BaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range config.Headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read SSE stream and collect response
	scanner := bufio.NewScanner(resp.Body)
	var resultData string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			resultData += data
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Parse response
	var response struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(resultData), &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	tools := make([]ToolDefinition, len(response.Result.Tools))
	for i, t := range response.Result.Tools {
		tools[i] = ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
		}
	}

	e.log.Info("Discovered tools from SSE MCP", zap.String("name", config.Name), zap.Int("count", len(tools)))

	return tools, nil
}

// discoverHTTP discovers tools from streamable_http MCP
func (e *ToolExecutor) discoverHTTP(ctx context.Context, config *MCPConfig) ([]ToolDefinition, error) {
	// Build request URL
	url := config.BaseURL
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}
	url += "tools/list"

	// Build request body
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range config.Headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(bodyResp))
	}

	var response struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}

	if err := json.Unmarshal(bodyResp, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	tools := make([]ToolDefinition, len(response.Result.Tools))
	for i, t := range response.Result.Tools {
		tools[i] = ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
		}
	}

	e.log.Info("Discovered tools from HTTP MCP", zap.String("name", config.Name), zap.Int("count", len(tools)))

	return tools, nil
}

// ExecuteStdio runs a stdio MCP tool via JSON-RPC
func (e *ToolExecutor) ExecuteStdio(ctx context.Context, config *MCPConfig, toolName, argsJSON string) (string, error) {
	// Reuse the discovery process (same key as discovery)
	// This avoids creating a new process for each tool call
	e.mu.Lock()
	proc, ok := e.processes[config.Name]
	if !ok {
		// Start new process if discovery process doesn't exist
		// Use context.Background() to avoid process being killed by request context
		cmd := exec.CommandContext(context.Background(), config.Command, config.Args...)

		cmd.Env = os.Environ()
		if config.Env != nil {
			for k, v := range config.Env {
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
		stderr, err := cmd.StderrPipe()
		if err != nil {
			e.mu.Unlock()
			return "", fmt.Errorf("failed to create stderr pipe: %w", err)
		}

		// Drain stderr to prevent blocking
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				e.log.Info("[MCP stderr] " + scanner.Text())
			}
		}()

		if err := cmd.Start(); err != nil {
			e.mu.Unlock()
			return "", fmt.Errorf("failed to start process: %w", err)
		}

		proc = &Process{
			cmd:         cmd,
			stdin:       stdin,
			stdout:      stdout,
			reader:      bufio.NewReader(stdout),
			stderr:      stderr,
			initialized: false,
		}
		e.processes[config.Name] = proc

		e.log.Info("Started MCP process for tool execution", zap.String("key", config.Name), zap.String("command", config.Command))
	}
	e.mu.Unlock()

	// Initialize if not done
	if !proc.initialized {
		if err := e.initializeStdio(ctx, proc, config); err != nil {
			return "", err
		}
	}

	// Parse argsJSON to ensure it's valid JSON
	var args map[string]interface{}
	if argsJSON != "" && argsJSON != "{}" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			e.log.Error("Failed to parse argsJSON: " + err.Error())
			args = map[string]interface{}{}
		}
	} else {
		args = map[string]interface{}{}
	}

	// Send JSON-RPC tools/call request
	// Use simple incrementing ID instead of timestamp for better compatibility
	requestID := 3 // Simple ID like in working test script

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	if _, err := proc.stdin.Write(reqBytes); err != nil {
		return "", fmt.Errorf("failed to write to stdin: %w", err)
	}

	// Give the MCP server a moment to process
	time.Sleep(10 * time.Millisecond)

	// Read responses until we get the tools/call response
	reader := proc.reader

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			e.log.Error("Failed to read response: " + err.Error())
			return "", fmt.Errorf("failed to read response: %w", err)
		}

		e.log.Info("Received line: " + strings.TrimSpace(line))

		var response map[string]interface{}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			e.log.Info("Failed to parse line, continuing...")
			continue
		}

		// Check if this is our response (matching request ID)
		respID := response["id"]
		if respID != nil {
			// Convert IDs to compare (handle float64 from JSON)
			respIDFloat, ok := respID.(float64)
			if ok && int(respIDFloat) == requestID {
				// This is our tools/call response
				if result, ok := response["result"]; ok {
					// Extract content from result
					if resultMap, ok := result.(map[string]interface{}); ok {
						if content, ok := resultMap["content"]; ok {
							if contentList, ok := content.([]interface{}); ok && len(contentList) > 0 {
								if firstContent, ok := contentList[0].(map[string]interface{}); ok {
									if text, ok := firstContent["text"].(string); ok {
										return text, nil
									}
								}
							}
						}
					}
					content, _ := json.Marshal(result)
					return string(content), nil
				}

				if errMsg, ok := response["error"]; ok {
					return "", fmt.Errorf("MCP error: %v", errMsg)
				}

				return "", fmt.Errorf("invalid response format")
			}
		}
	}
}

// ExecuteSSE runs a SSE MCP tool via HTTP with Server-Sent Events
func (e *ToolExecutor) ExecuteSSE(ctx context.Context, config *MCPConfig, toolName, argsJSON string) (string, error) {
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

	req, err := http.NewRequestWithContext(ctx, "POST", config.BaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range config.Headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

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
func (e *ToolExecutor) ExecuteHTTP(ctx context.Context, config *MCPConfig, toolName, argsJSON string) (string, error) {
	url := config.BaseURL
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}
	url += "tools/" + toolName

	bodyBytes := []byte(argsJSON)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range config.Headers {
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