package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/mcp/tools"
)

// MCPToolAdapter adapts MCP tools to eino's InvokableTool interface
type MCPToolAdapter struct {
	toolInfo   *mcp.ToolInfo
	mcpManager *mcp.Manager
	log        *logger.Logger
}

// NewMCPToolAdapter creates a new adapter
func NewMCPToolAdapter(info *mcp.ToolInfo, mgr *mcp.Manager, log *logger.Logger) *MCPToolAdapter {
	return &MCPToolAdapter{
		toolInfo:   info,
		mcpManager: mgr,
		log:        log,
	}
}

// Info returns tool metadata for eino
func (t *MCPToolAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	// Build parameter info for the tool
	params := buildParameterInfo(t.toolInfo.Name)

	return &schema.ToolInfo{
		Name:        t.toolInfo.Name,
		Desc:        t.toolInfo.Description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

// buildParameterInfo builds ParameterInfo map for the tool
func buildParameterInfo(name string) map[string]*schema.ParameterInfo {
	switch name {
	case "file_read":
		return map[string]*schema.ParameterInfo{
			"path": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "文件路径",
				Required: true,
			},
		}
	case "file_write":
		return map[string]*schema.ParameterInfo{
			"path": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "文件路径",
				Required: true,
			},
			"content": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "文件内容",
				Required: true,
			},
		}
	case "file_search":
		return map[string]*schema.ParameterInfo{
			"pattern": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "搜索模式",
				Required: true,
			},
			"directory": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "目录路径",
				Required: true,
			},
		}
	case "directory_list":
		return map[string]*schema.ParameterInfo{
			"path": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "目录路径",
				Required: true,
			},
		}
	case "directory_create":
		return map[string]*schema.ParameterInfo{
			"path": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "目录路径",
				Required: true,
			},
		}
	case "file_exists":
		return map[string]*schema.ParameterInfo{
			"path": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "文件路径",
				Required: true,
			},
		}
	case "file_info":
		return map[string]*schema.ParameterInfo{
			"path": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "文件路径",
				Required: true,
			},
		}
	case "http_get":
		return map[string]*schema.ParameterInfo{
			"url": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "请求URL",
				Required: true,
			},
			"headers": &schema.ParameterInfo{
				Type:     schema.Object,
				Desc:     "请求头",
				Required: false,
			},
		}
	case "http_post":
		return map[string]*schema.ParameterInfo{
			"url": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "请求URL",
				Required: true,
			},
			"body": &schema.ParameterInfo{
				Type:     schema.Object,
				Desc:     "请求体",
				Required: false,
			},
			"headers": &schema.ParameterInfo{
				Type:     schema.Object,
				Desc:     "请求头",
				Required: false,
			},
		}
	case "http_put":
		return map[string]*schema.ParameterInfo{
			"url": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "请求URL",
				Required: true,
			},
			"body": &schema.ParameterInfo{
				Type:     schema.Object,
				Desc:     "请求体",
				Required: false,
			},
			"headers": &schema.ParameterInfo{
				Type:     schema.Object,
				Desc:     "请求头",
				Required: false,
			},
		}
	case "http_delete":
		return map[string]*schema.ParameterInfo{
			"url": &schema.ParameterInfo{
				Type:     schema.String,
				Desc:     "请求URL",
				Required: true,
			},
			"headers": &schema.ParameterInfo{
				Type:     schema.Object,
				Desc:     "请求头",
				Required: false,
			},
		}
	default:
		return nil // No parameters
	}
}

// InvokableRun executes the tool (eino interface)
func (t *MCPToolAdapter) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	// Get MCP config for this tool
	mcpConfig, ok := t.mcpManager.Get(t.toolInfo.MCP)
	if !ok {
		return "", fmt.Errorf("MCP %s not found", t.toolInfo.MCP)
	}

	// Execute based on MCP type
	switch mcpConfig.Type {
	case mcp.MCPTypeBuiltin:
		return t.executeBuiltin(ctx, mcpConfig, argsJSON)
	case mcp.MCPTypeStdio:
		return t.executeStdio(ctx, mcpConfig, argsJSON)
	case mcp.MCPTypeSSE:
		return t.executeSSE(ctx, mcpConfig, argsJSON)
	case mcp.MCPTypeStreamableHTTP:
		return t.executeHTTP(ctx, mcpConfig, argsJSON)
	default:
		return "", fmt.Errorf("unsupported MCP type: %s", mcpConfig.Type)
	}
}

// executeBuiltin runs builtin tools directly
func (t *MCPToolAdapter) executeBuiltin(ctx context.Context, cfg *mcp.MCPConfig, argsJSON string) (string, error) {
	// Parse arguments
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Get restrictions
	var allowedPaths []string
	var deniedDomains []string
	var timeout int = 30
	var maxResponseSize int = 10

	if cfg.Restrictions != nil {
		allowedPaths = cfg.Restrictions.AllowedPaths
		deniedDomains = cfg.Restrictions.DeniedDomains
		timeout = cfg.Restrictions.Timeout
		if timeout <= 0 {
			timeout = 30
		}
		maxResponseSize = cfg.Restrictions.MaxResponseSize
		if maxResponseSize <= 0 {
			maxResponseSize = 10
		}
	}

	// Execute using builtin handlers
	switch t.toolInfo.Name {
	case "file_read":
		path, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing path argument")
		}
		handler := tools.NewFileOperations(allowedPaths)
		return handler.FileRead(path)

	case "file_write":
		path, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing path argument")
		}
		content, ok := args["content"].(string)
		if !ok {
			return "", fmt.Errorf("missing content argument")
		}
		handler := tools.NewFileOperations(allowedPaths)
		err := handler.FileWrite(path, content)
		if err != nil {
			return "", err
		}
		return "文件写入成功", nil

	case "file_search":
		pattern, ok := args["pattern"].(string)
		if !ok {
			return "", fmt.Errorf("missing pattern argument")
		}
		directory, ok := args["directory"].(string)
		if !ok {
			return "", fmt.Errorf("missing directory argument")
		}
		handler := tools.NewFileOperations(allowedPaths)
		result, err := handler.FileSearch(pattern, directory)
		if err != nil {
			return "", err
		}
		resultJSON, _ := json.Marshal(result)
		return string(resultJSON), nil

	case "directory_list":
		path, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing path argument")
		}
		handler := tools.NewFileOperations(allowedPaths)
		result, err := handler.DirectoryList(path)
		if err != nil {
			return "", err
		}
		resultJSON, _ := json.Marshal(result)
		return string(resultJSON), nil

	case "directory_create":
		path, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing path argument")
		}
		handler := tools.NewFileOperations(allowedPaths)
		err := handler.DirectoryCreate(path)
		if err != nil {
			return "", err
		}
		return "目录创建成功", nil

	case "file_exists":
		path, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing path argument")
		}
		handler := tools.NewFileOperations(allowedPaths)
		result, err := handler.FileExists(path)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%v", result), nil

	case "file_info":
		path, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing path argument")
		}
		handler := tools.NewFileOperations(allowedPaths)
		result, err := handler.FileInfo(path)
		if err != nil {
			return "", err
		}
		resultJSON, _ := json.Marshal(result)
		return string(resultJSON), nil

	case "http_get":
		url, ok := args["url"].(string)
		if !ok {
			return "", fmt.Errorf("missing url argument")
		}
		headers := convertHeaders(args["headers"])
		handler := tools.NewHTTPRequest(deniedDomains, timeout, maxResponseSize)
		result, err := handler.HTTPGet(url, headers)
		if err != nil {
			return "", err
		}
		resultJSON, _ := json.Marshal(result)
		return string(resultJSON), nil

	case "http_post":
		url, ok := args["url"].(string)
		if !ok {
			return "", fmt.Errorf("missing url argument")
		}
		body := args["body"]
		headers := convertHeaders(args["headers"])
		handler := tools.NewHTTPRequest(deniedDomains, timeout, maxResponseSize)
		result, err := handler.HTTPPost(url, body, headers)
		if err != nil {
			return "", err
		}
		resultJSON, _ := json.Marshal(result)
		return string(resultJSON), nil

	case "http_put":
		url, ok := args["url"].(string)
		if !ok {
			return "", fmt.Errorf("missing url argument")
		}
		body := args["body"]
		headers := convertHeaders(args["headers"])
		handler := tools.NewHTTPRequest(deniedDomains, timeout, maxResponseSize)
		result, err := handler.HTTPPut(url, body, headers)
		if err != nil {
			return "", err
		}
		resultJSON, _ := json.Marshal(result)
		return string(resultJSON), nil

	case "http_delete":
		url, ok := args["url"].(string)
		if !ok {
			return "", fmt.Errorf("missing url argument")
		}
		headers := convertHeaders(args["headers"])
		handler := tools.NewHTTPRequest(deniedDomains, timeout, maxResponseSize)
		result, err := handler.HTTPDelete(url, headers)
		if err != nil {
			return "", err
		}
		resultJSON, _ := json.Marshal(result)
		return string(resultJSON), nil

	default:
		return "", fmt.Errorf("unknown builtin tool: %s", t.toolInfo.Name)
	}
}

// executeStdio runs stdio MCP tools via ToolExecutor
func (t *MCPToolAdapter) executeStdio(ctx context.Context, cfg *mcp.MCPConfig, argsJSON string) (string, error) {
	executor := t.mcpManager.GetExecutor()
	return executor.ExecuteStdio(ctx, cfg, t.toolInfo.Name, argsJSON)
}

// executeSSE runs SSE MCP tools via ToolExecutor
func (t *MCPToolAdapter) executeSSE(ctx context.Context, cfg *mcp.MCPConfig, argsJSON string) (string, error) {
	executor := t.mcpManager.GetExecutor()
	return executor.ExecuteSSE(ctx, cfg, t.toolInfo.Name, argsJSON)
}

// executeHTTP runs streamable_http MCP tools via ToolExecutor
func (t *MCPToolAdapter) executeHTTP(ctx context.Context, cfg *mcp.MCPConfig, argsJSON string) (string, error) {
	executor := t.mcpManager.GetExecutor()
	return executor.ExecuteHTTP(ctx, cfg, t.toolInfo.Name, argsJSON)
}

// convertHeaders converts map[string]interface{} to map[string]string
func convertHeaders(h interface{}) map[string]string {
	result := map[string]string{}
	if h == nil {
		return result
	}
	if m, ok := h.(map[string]interface{}); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
	}
	return result
}