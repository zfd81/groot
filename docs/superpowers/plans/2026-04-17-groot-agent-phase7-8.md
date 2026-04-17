# Groot AI Agent Implementation Plan (Phase 7-10)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Groot AI Agent service - MCP Manager, Built-in Tools, Agent Engine, REST API, and Entry Point.

**Architecture:** Layered architecture with REST API (Hertz), Agent Engine (eino), MCP Manager, Task Storage (BoltDB), Skills Registry, and Config modules.

**Tech Stack:** Go, Hertz, eino, BoltDB, fsnotify, zap (logging), YAML config

**Based on:** docs/superpowers/specs/2026-04-16-groot-agent-design.md

**Prerequisites:** Phase 1-6 completed (config, logger, storage, skills modules)

---

## File Structure (Continued)

```
groot/
├── cmd/groot/main.go                    # Entry point (NEW)
├── internal/
│   ├── config/                          # ✅ Completed
│   ├── logger/                          # ✅ Completed
│   ├── storage/                         # ✅ Completed
│   ├── skill/                           # ✅ Completed
│   ├── mcp/
│   │   ├── manager.go                   # MCP manager (NEW)
│   │   ├── config.go                    # MCP config struct (NEW)
│   │   ├── watcher.go                   # Hot-plug watcher (NEW)
│   │   ├── builtin.go                   # Built-in tools registry (NEW)
│   │   └── tools/
│   │       ├── file_operations.go       # file_read, file_write, etc. (NEW)
│   │       └── http_request.go          # http_get, http_post, etc. (NEW)
│   ├── agent/
│   │   ├── idgen.go                     # ✅ Completed
│   │   ├── engine.go                    # Agent engine (NEW)
│   │   ├── executor.go                  # Task executor (NEW)
│   │   ├── sse.go                       # SSE writer (NEW)
│   │   └── cancel.go                    # Cancel manager (NEW)
│   └── api/
│       ├── server.go                    # Hertz server (NEW)
│       ├── router.go                    # Route registration (NEW)
│       ├── request.go                   # Request/Response structs (NEW)
│       ├── handler/
│       │   ├── execute.go               # POST /task/execute (NEW)
│       │   ├── cancel.go                # DELETE /task/{task_id} (NEW)
│       │   ├── status.go                # GET /task/status/{task_id} (NEW)
│       │   ├── history.go               # GET /task/history (NEW)
│       │   ├── detail.go                # GET /task/{task_id} (NEW)
│       │   ├── health.go                # GET /health (NEW)
│       │   ├── skills.go                # GET /skills (NEW)
│       │   └── tools.go                 # GET /tools (NEW)
│       └── middleware/
│       ├── auth.go                      # API Key auth (NEW)
│       ├── ratelimit.go                 # Rate limiting (NEW)
│       └── recovery.go                  # Error recovery (NEW)
├── pkg/
│   └── utils/
│       └── timeparse.go                 # yyyyMMddHHmm parser (NEW)
├── configs/config.yaml                  # ✅ Completed
├── go.mod                               # ✅ Completed
├── Makefile                             # ✅ Completed
```

---

## Phase 7: MCP Manager Module

### Task 14: Define MCP Config Structures

**Files:**
- Create: `internal/mcp/config.go`

**Complete code:**

```go
package mcp

// MCPType represents MCP connection type
type MCPType string

const (
	MCPTypeStdio         MCPType = "stdio"
	MCPTypeSSE           MCPType = "sse"
	MCPTypeStreamableHTTP MCPType = "streamable_http"
	MCPTypeBuiltin       MCPType = "builtin"
)

// MCPConfig represents a single MCP configuration
type MCPConfig struct {
	Name        string                 `json:"name"`
	Type        MCPType                `json:"type"`
	Description string                 `json:"description"`
	IsActive    bool                   `json:"isActive"`
	Command     string                 `json:"command,omitempty"`
	Args        []string               `json:"args,omitempty"`
	Env         map[string]string      `json:"env,omitempty"`
	BaseURL     string                 `json:"baseUrl,omitempty"`
	Headers     map[string]string      `json:"headers,omitempty"`
	Tools       []string               `json:"tools,omitempty"`
	Restrictions *MCPRestrictions      `json:"restrictions,omitempty"`
}

// MCPRestrictions holds security restrictions for builtin tools
type MCPRestrictions struct {
	AllowedPaths      []string `json:"allowed_paths,omitempty"`
	DeniedOperations  []string `json:"denied_operations,omitempty"`
	DeniedDomains     []string `json:"denied_domains,omitempty"`
	Timeout           int      `json:"timeout,omitempty"`
	MaxResponseSize   int      `json:"max_response_size,omitempty"`
	Sandbox           bool     `json:"sandbox,omitempty"`
	NetworkAccess     bool     `json:"network_access,omitempty"`
}

// ToolInfo represents a tool's metadata
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MCP         string `json:"mcp"`
}
```

**Commit message:** `feat: add MCP config structures`

- [ ] **Step 1: Create MCP config structures**
- [ ] **Step 2: Run `gofmt -w ./internal/mcp/config.go`**
- [ ] **Step 3: Run `go build ./internal/mcp/`**
- [ ] **Step 4: Commit**

---

### Task 15: Implement MCP Manager

**Files:**
- Create: `internal/mcp/manager.go`

**Complete code:**

```go
package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/zfd81/groot/internal/logger"
)

// Manager manages all MCP configurations and tool registry
type Manager struct {
	mcps    map[string]*MCPConfig
	tools   map[string]*ToolInfo
	logger  *logger.Logger
	mu      sync.RWMutex
}

// NewManager creates a new MCP manager
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		mcps:   make(map[string]*MCPConfig),
		tools:  make(map[string]*ToolInfo),
		logger: log,
	}
}

// Register adds an MCP configuration
func (m *Manager) Register(config *MCPConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mcps[config.Name] = config

	// Register tools from this MCP
	for _, toolName := range config.Tools {
		m.tools[toolName] = &ToolInfo{
			Name:        toolName,
			Description: getToolDescription(toolName),
			MCP:         config.Name,
		}
	}
}

// Unregister removes an MCP configuration
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if config, ok := m.mcps[name]; ok {
		// Remove tools from this MCP
		for _, toolName := range config.Tools {
			delete(m.tools, toolName)
		}
		delete(m.mcps, name)
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
	tool, ok := m.tools[name]
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

// ListTools returns all registered tools
func (m *Manager) ListTools() []*ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*ToolInfo, 0, len(m.tools))
	for _, tool := range m.tools {
		result = append(result, tool)
	}
	return result
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
	return len(m.tools)
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
				return fmt.Errorf("failed to load %s: %w", path, err)
			}
		}
	}

	return nil
}

// Load parses a single MCP config file
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
		return nil // Skip inactive MCPs
	}

	m.Register(&config)
	return nil
}

// getToolDescription returns description for builtin tools
func getToolDescription(name string) string {
	switch name {
	case "file_read":
		return "读取文件内容"
	case "file_write":
		return "写入文件内容"
	case "file_delete":
		return "删除文件"
	case "file_search":
		return "搜索文件"
	case "directory_list":
		return "列出目录内容"
	case "directory_create":
		return "创建目录"
	case "file_exists":
		return "检查文件是否存在"
	case "file_info":
		return "获取文件信息"
	case "http_get":
		return "发送HTTP GET请求"
	case "http_post":
		return "发送HTTP POST请求"
	case "http_put":
		return "发送HTTP PUT请求"
	case "http_delete":
		return "发送HTTP DELETE请求"
	default:
		return name
	}
}
```

**Commit message:** `feat: add MCP manager with tool registry`

- [ ] **Step 1: Create MCP manager**
- [ ] **Step 2: Run `gofmt -w ./internal/mcp/manager.go`**
- [ ] **Step 3: Run `go build ./internal/mcp/`**
- [ ] **Step 4: Commit**

---

### Task 16: Implement MCP Hot-plug Watcher

**Files:**
- Create: `internal/mcp/watcher.go`

**Complete code:**

```go
package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// Watcher monitors MCP directory for hot-reload
type Watcher struct {
	manager   *Manager
	config    config.MCPConfig
	logger    *logger.Logger
	watcher   *fsnotify.Watcher
	debounce  map[string]time.Time
	mu        sync.Mutex
	stopChan  chan struct{}
}

// NewWatcher creates a new MCP watcher
func NewWatcher(manager *Manager, cfg config.MCPConfig, log *logger.Logger) *Watcher {
	return &Watcher{
		manager:  manager,
		config:   cfg,
		logger:   log,
		debounce: make(map[string]time.Time),
		stopChan: make(chan struct{}),
	}
}

// Start begins watching the MCP directory
func (w *Watcher) Start(dir string) error {
	if !w.config.HotReload.Enabled {
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher

	if err := watcher.Add(dir); err != nil {
		return err
	}

	go w.run(dir)
	return nil
}

// run handles file events with debouncing
func (w *Watcher) run(dir string) {
	debounceDelay := time.Duration(w.config.HotReload.DebounceDelay) * time.Second

	for {
		select {
		case <-w.stopChan:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event, debounceDelay)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Error("MCP watcher error", zap.Error(err))
		}
	}
}

// handleEvent processes a file event
func (w *Watcher) handleEvent(event fsnotify.Event, debounceDelay time.Duration) {
	// Only process .json files
	if !strings.HasSuffix(event.Name, ".json") {
		return
	}

	// Debounce
	w.mu.Lock()
	w.debounce[event.Name] = time.Now()
	w.mu.Unlock()

	time.Sleep(debounceDelay)

	w.mu.Lock()
	lastTime := w.debounce[event.Name]
	delete(w.debounce, event.Name)
	w.mu.Unlock()

	if time.Since(lastTime) < debounceDelay {
		return
	}

	// Process event
	switch event.Op {
	case fsnotify.Create, fsnotify.Write:
		if err := w.manager.Load(event.Name); err != nil {
			w.logger.Error("failed to load MCP", zap.Error(err), zap.String("path", event.Name))
		} else {
			mcpName := extractMCPName(event.Name)
			w.logger.LogMCPHotReload("added", mcpName, "config", w.manager.Count())
		}

	case fsnotify.Remove, fsnotify.Rename:
		mcpName := extractMCPName(event.Name)
		w.manager.Unregister(mcpName)
		w.logger.LogMCPHotReload("removed", mcpName, "config", w.manager.Count())
	}
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.stopChan)
	if w.watcher != nil {
		w.watcher.Close()
	}
}

// extractMCPName extracts MCP name from path
func extractMCPName(path string) string {
	filename := filepath.Base(path)
	return strings.TrimSuffix(filename, ".json")
}
```

**Commit message:** `feat: add MCP hot-plug watcher`

- [ ] **Step 1: Create MCP watcher**
- [ ] **Step 2: Run `gofmt -w ./internal/mcp/watcher.go`**
- [ ] **Step 3: Run `go build ./internal/mcp/`**
- [ ] **Step 4: Commit**

---

### Task 17: Implement Built-in MCP Registry

**Files:**
- Create: `internal/mcp/builtin.go`

**Complete code:**

```go
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
```

**Commit message:** `feat: add built-in MCP tools registry`

- [ ] **Step 1: Create builtin tools registry**
- [ ] **Step 2: Run `gofmt -w ./internal/mcp/builtin.go`**
- [ ] **Step 3: Run `go build ./internal/mcp/`**
- [ ] **Step 4: Commit**

---

### Task 18: Implement file_operations Tools

**Files:**
- Create: `internal/mcp/tools/file_operations.go`

**Complete code:**

```go
package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileOperations handles file and directory operations
type FileOperations struct {
	allowedPaths []string
}

// NewFileOperations creates a new file operations handler
func NewFileOperations(allowedPaths []string) *FileOperations {
	return &FileOperations{allowedPaths: allowedPaths}
}

// isPathAllowed checks if path is within allowed directories
func (f *FileOperations) isPathAllowed(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	for _, allowed := range f.allowedPaths {
		absAllowed, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		if strings.HasPrefix(absPath, absAllowed) {
			return true
		}
	}
	return false
}

// FileRead reads file content
func (f *FileOperations) FileRead(path string) (string, error) {
	if !f.isPathAllowed(path) {
		return "", fmt.Errorf("path not allowed: %s", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return string(content), nil
}

// FileWrite writes content to file
func (f *FileOperations) FileWrite(path, content string) error {
	if !f.isPathAllowed(path) {
		return fmt.Errorf("path not allowed: %s", path)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

// FileSearch searches for files matching pattern
func (f *FileOperations) FileSearch(pattern, directory string) ([]string, error) {
	if !f.isPathAllowed(directory) {
		return nil, fmt.Errorf("directory not allowed: %s", directory)
	}

	var matches []string
	err := filepath.WalkDir(directory, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), pattern) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	return matches, nil
}

// DirectoryList lists directory contents
func (f *FileOperations) DirectoryList(path string) ([]map[string]interface{}, error) {
	if !f.isPathAllowed(path) {
		return nil, fmt.Errorf("path not allowed: %s", path)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	result := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, map[string]interface{}{
			"name":  entry.Name(),
			"type":  entry.Type().String(),
			"size":  info.Size(),
			"is_dir": entry.IsDir(),
		})
	}
	return result, nil
}

// DirectoryCreate creates a directory
func (f *FileOperations) DirectoryCreate(path string) error {
	if !f.isPathAllowed(path) {
		return fmt.Errorf("path not allowed: %s", path)
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

// FileExists checks if file exists
func (f *FileOperations) FileExists(path string) (bool, error) {
	if !f.isPathAllowed(path) {
		return false, fmt.Errorf("path not allowed: %s", path)
	}

	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check file: %w", err)
	}
	return true, nil
}

// FileInfo returns file information
func (f *FileOperations) FileInfo(path string) (map[string]interface{}, error) {
	if !f.isPathAllowed(path) {
		return nil, fmt.Errorf("path not allowed: %s", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	return map[string]interface{}{
		"name":      info.Name(),
		"size":      info.Size(),
		"mode":      info.Mode().String(),
		"modified":  info.ModTime().Format("2006-01-02 15:04:05"),
		"is_dir":    info.IsDir(),
	}, nil
}
```

**Commit message:** `feat: add file_operations built-in tools`

- [ ] **Step 1: Create file_operations.go**
- [ ] **Step 2: Run `gofmt -w ./internal/mcp/tools/file_operations.go`**
- [ ] **Step 3: Run `go build ./internal/mcp/tools/`**
- [ ] **Step 4: Commit**

---

### Task 19: Implement http_request Tools

**Files:**
- Create: `internal/mcp/tools/http_request.go`

**Complete code:**

```go
package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPRequest handles HTTP request operations
type HTTPRequest struct {
	deniedDomains   []string
	timeout         int
	maxResponseSize int
}

// NewHTTPRequest creates a new HTTP request handler
func NewHTTPRequest(deniedDomains []string, timeout, maxResponseSize int) *HTTPRequest {
	return &HTTPRequest{
		deniedDomains:   deniedDomains,
		timeout:         timeout,
		maxResponseSize: maxResponseSize,
	}
}

// isDomainDenied checks if domain is in denied list
func (h *HTTPRequest) isDomainDenied(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return true
	}

	host := parsed.Hostname()
	for _, denied := range h.deniedDomains {
		if host == denied || strings.HasPrefix(host, denied+".") {
			return true
		}
		// Check for IP patterns
		if strings.HasPrefix(denied, host[:strings.Index(host, ".")+1]) {
			return true
		}
	}
	return false
}

// HTTPGet sends GET request
func (h *HTTPRequest) HTTPGet(rawURL string, headers map[string]string) (map[string]interface{}, error) {
	if h.isDomainDenied(rawURL) {
		return nil, fmt.Errorf("domain denied: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(h.timeout) * time.Second}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Limit response size
	limitedReader := io.LimitReader(resp.Body, int64(h.maxResponseSize)*1024*1024)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(body),
	}, nil
}

// HTTPPost sends POST request
func (h *HTTPRequest) HTTPPost(rawURL string, body interface{}, headers map[string]string) (map[string]interface{}, error) {
	if h.isDomainDenied(rawURL) {
		return nil, fmt.Errorf("domain denied: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(h.timeout) * time.Second}

	// Serialize body
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest("POST", rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, int64(h.maxResponseSize)*1024*1024)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
	}, nil
}

// HTTPPut sends PUT request
func (h *HTTPRequest) HTTPPut(rawURL string, body interface{}, headers map[string]string) (map[string]interface{}, error) {
	if h.isDomainDenied(rawURL) {
		return nil, fmt.Errorf("domain denied: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(h.timeout) * time.Second}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest("PUT", rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, int64(h.maxResponseSize)*1024*1024)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
	}, nil
}

// HTTPDelete sends DELETE request
func (h *HTTPRequest) HTTPDelete(rawURL string, headers map[string]string) (map[string]interface{}, error) {
	if h.isDomainDenied(rawURL) {
		return nil, fmt.Errorf("domain denied: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(h.timeout) * time.Second}

	req, err := http.NewRequest("DELETE", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, int64(h.maxResponseSize)*1024*1024)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
	}, nil
}
```

**Commit message:** `feat: add http_request built-in tools`

- [ ] **Step 1: Create http_request.go**
- [ ] **Step 2: Run `gofmt -w ./internal/mcp/tools/http_request.go`**
- [ ] **Step 3: Run `go build ./internal/mcp/tools/`**
- [ ] **Step 4: Commit**

---

## Phase 8: REST API Module

### Task 20: Define Request/Response Structures

**Files:**
- Create: `internal/api/request.go`

**Complete code:**

```go
package api

import (
	"time"
)

// ExecuteRequest represents task execute request
type ExecuteRequest struct {
	Instruction  string       `json:"instruction"`
	Prompt       string       `json:"prompt,omitempty"`
	Attachments  []Attachment `json:"attachments,omitempty"`
}

// Attachment represents file attachment
type Attachment struct {
	Type    string `json:"type"`    // file, url
	Name    string `json:"name"`
	Content string `json:"content"` // Base64 or URL
}

// ExecuteResponse represents SSE event response
type ExecuteResponse struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// IntentEvent represents intent SSE event
type IntentEvent struct {
	Timestamp string `json:"timestamp"`
}

// StepStartEvent represents step_start SSE event
type StepStartEvent struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	StepID       string `json:"step_id"`
	Timestamp    string `json:"timestamp"`
	NestingLevel int    `json:"nesting_level,omitempty"`
	Params       map[string]interface{} `json:"params,omitempty"`
}

// StepEndEvent represents step_end SSE event
type StepEndEvent struct {
	StepID    string     `json:"step_id"`
	Timestamp string     `json:"timestamp"`
	Status    string     `json:"status"`
	Error     *ErrorInfo `json:"error,omitempty"`
}

// ProgressEvent represents progress SSE event
type ProgressEvent struct {
	StepID    string `json:"step_id,omitempty"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// CompletedEvent represents completed SSE event
type CompletedEvent struct {
	Status    string     `json:"status"`
	Timestamp string     `json:"timestamp"`
	Duration  string     `json:"duration"`
	Result    interface{} `json:"result,omitempty"`
	Error     *ErrorInfo  `json:"error,omitempty"`
	Message   string      `json:"message,omitempty"`
}

// ErrorInfo represents error information
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CancelResponse represents cancel response
type CancelResponse struct {
	Status  string `json:"status"`
	TaskID  string `json:"task_id"`
	Message string `json:"message"`
}

// StatusResponse represents status response
type StatusResponse struct {
	Status     string        `json:"status"`
	TaskID     string        `json:"task_id"`
	TaskStatus string        `json:"task_status,omitempty"`
	Progress   *ProgressInfo `json:"progress,omitempty"`
	StartedAt  string        `json:"started_at,omitempty"`
	ElapsedTime string       `json:"elapsed_time,omitempty"`
	Message    string        `json:"message,omitempty"`
}

// ProgressInfo represents task progress
type ProgressInfo struct {
	CurrentStep    int `json:"current_step"`
	StepsCompleted int `json:"steps_completed"`
	Percentage     int `json:"percentage"`
}

// HistoryResponse represents history response
type HistoryResponse struct {
	Status string           `json:"status"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
	Tasks  []TaskSummary    `json:"tasks"`
}

// TaskSummary represents task summary for history
type TaskSummary struct {
	ID          string    `json:"id"`
	Instruction string    `json:"instruction"`
	Status      string    `json:"status"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time,omitempty"`
	Duration    int       `json:"duration"`
	Caller      string    `json:"caller"`
}

// DetailResponse represents task detail response
type DetailResponse struct {
	Status string      `json:"status"`
	Task   *TaskDetail `json:"task,omitempty"`
	Message string     `json:"message,omitempty"`
}

// TaskDetail represents full task detail
type TaskDetail struct {
	ID          string           `json:"id"`
	Instruction string           `json:"instruction"`
	Prompt      string           `json:"prompt,omitempty"`
	Status      string           `json:"status"`
	StartTime   time.Time        `json:"start_time"`
	EndTime     time.Time        `json:"end_time,omitempty"`
	Duration    int              `json:"duration"`
	Caller      string           `json:"caller"`
	Result      interface{}      `json:"result,omitempty"`
	Error       *ErrorInfo       `json:"error,omitempty"`
	Steps       []StepDetail     `json:"steps,omitempty"`
}

// StepDetail represents step detail
type StepDetail struct {
	StepID       string     `json:"step_id"`
	Type         string     `json:"type"`
	Name         string     `json:"name"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      time.Time  `json:"end_time,omitempty"`
	Status       string     `json:"status"`
	NestingLevel int        `json:"nesting_level"`
	Error        *ErrorInfo `json:"error,omitempty"`
}

// HealthResponse represents health check response
type HealthResponse struct {
	Status   string                 `json:"status"`
	Version  string                 `json:"version"`
	Uptime   string                 `json:"uptime"`
	Checks   map[string]CheckInfo   `json:"checks"`
	Metrics  map[string]interface{} `json:"metrics"`
}

// CheckInfo represents health check info
type CheckInfo struct {
	Status string      `json:"status"`
	Info   interface{} `json:"info,omitempty"`
}

// SkillsResponse represents skills list response
type SkillsResponse struct {
	Skills []SkillInfo `json:"skills"`
	Total  int         `json:"total"`
}

// SkillInfo represents skill information
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolsResponse represents tools list response
type ToolsResponse struct {
	Tools []ToolInfo `json:"tools"`
	Total int        `json:"total"`
}

// ToolInfo represents tool information
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MCP         string `json:"mcp"`
}

// ErrorResponse represents error response
type ErrorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}
```

**Commit message:** `feat: add API request/response structures`

- [ ] **Step 1: Create request.go**
- [ ] **Step 2: Run `gofmt -w ./internal/api/request.go`**
- [ ] **Step 3: Run `go build ./internal/api/`**
- [ ] **Step 4: Commit**

---

### Task 21: Implement Time Parser Utility

**Files:**
- Create: `pkg/utils/timeparse.go`

**Complete code:**

```go
package utils

import (
	"fmt"
	"strconv"
	"time"
)

// ParseTime parses yyyyMMddHHmm format to time.Time
func ParseTime(s string) (time.Time, error) {
	if len(s) != 12 {
		return time.Time{}, fmt.Errorf("invalid time format: %s (expected yyyyMMddHHmm)", s)
	}

	year, err := strconv.Atoi(s[0:4])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid year: %s", s[0:4])
	}

	month, err := strconv.Atoi(s[4:6])
	if err != nil || month < 1 || month > 12 {
		return time.Time{}, fmt.Errorf("invalid month: %s", s[4:6])
	}

	day, err := strconv.Atoi(s[6:8])
	if err != nil || day < 1 || day > 31 {
		return time.Time{}, fmt.Errorf("invalid day: %s", s[6:8])
	}

	hour, err := strconv.Atoi(s[8:10])
	if err != nil || hour < 0 || hour > 23 {
		return time.Time{}, fmt.Errorf("invalid hour: %s", s[8:10])
	}

	minute, err := strconv.Atoi(s[10:12])
	if err != nil || minute < 0 || minute > 59 {
		return time.Time{}, fmt.Errorf("invalid minute: %s", s[10:12])
	}

	return time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.UTC), nil
}
```

**Commit message:** `feat: add yyyyMMddHHmm time parser`

- [ ] **Step 1: Create timeparse.go**
- [ ] **Step 2: Run `gofmt -w ./pkg/utils/timeparse.go`**
- [ ] **Step 3: Run `go build ./pkg/utils/`**
- [ ] **Step 4: Commit**

---

## Self-Review Checklist

After completing Phase 7-8:

1. **Spec coverage:**
   - MCP Manager: Tasks 14-17 ✓
   - Built-in Tools: Tasks 18-19 ✓
   - REST API structures: Task 20 ✓
   - Time parser: Task 21 ✓
   - Agent Engine: pending (Phase 9)
   - API handlers/middleware: pending (Phase 9)
   - Entry point: pending (Phase 10)

2. **Placeholder scan:** No "TBD", "TODO", or incomplete sections.

---

**Plan saved to:** `docs/superpowers/plans/2026-04-17-groot-agent-phase7-8.md`

**Note:** This plan covers Phase 7-8 (MCP module and API structures). Phase 9 (Agent Engine, handlers, middleware) and Phase 10 (Entry point) will be added in subsequent plan files.

**Two execution options:**

1. **Subagent-Driven (recommended)** - Dispatch a fresh subagent per task, review between tasks
2. **Inline Execution** - Execute tasks in this session

**Which approach would you like to proceed with?**