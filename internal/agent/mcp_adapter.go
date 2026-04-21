package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
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
	// Convert MCP inputSchema (JSON Schema) to eino's ParameterInfo format
	params := t.convertInputSchema(t.toolInfo.InputSchema)

	return &schema.ToolInfo{
		Name:        t.toolInfo.Name,
		Desc:        t.toolInfo.Description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

// convertInputSchema converts MCP JSON Schema to eino's ParameterInfo format
func (t *MCPToolAdapter) convertInputSchema(inputSchema map[string]interface{}) map[string]*schema.ParameterInfo {
	params := map[string]*schema.ParameterInfo{}

	if inputSchema == nil {
		return params
	}

	// Get properties from JSON Schema
	properties, ok := inputSchema["properties"].(map[string]interface{})
	if !ok {
		return params
	}

	// Get required fields list
	var requiredFields []string
	if required, ok := inputSchema["required"].([]interface{}); ok {
		for _, r := range required {
			if rs, ok := r.(string); ok {
				requiredFields = append(requiredFields, rs)
			}
		}
	}

	// Convert each property to ParameterInfo
	for name, prop := range properties {
		propMap, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}

		paramInfo := &schema.ParameterInfo{
			Type:     t.getSchemaType(propMap["type"]),
			Desc:     t.getDescription(propMap),
			Required: t.isRequired(name, requiredFields),
		}

		params[name] = paramInfo
	}

	return params
}

// getSchemaType converts JSON Schema type to eino's schema.DataType
func (t *MCPToolAdapter) getSchemaType(typeVal interface{}) schema.DataType {
	if typeVal == nil {
		return schema.String // Default to string
	}

	typeStr, ok := typeVal.(string)
	if !ok {
		return schema.String
	}

	switch typeStr {
	case "string":
		return schema.String
	case "number", "integer":
		return schema.Number
	case "boolean":
		return schema.Boolean
	case "array":
		return schema.Array
	case "object":
		return schema.Object
	default:
		return schema.String
	}
}

// getDescription extracts description from property
func (t *MCPToolAdapter) getDescription(propMap map[string]interface{}) string {
	if desc, ok := propMap["description"].(string); ok {
		return desc
	}
	return ""
}

// isRequired checks if a field is in the required list
func (t *MCPToolAdapter) isRequired(name string, requiredFields []string) bool {
	for _, r := range requiredFields {
		if r == name {
			return true
		}
	}
	return false
}

// InvokableRun executes the tool (eino interface)
func (t *MCPToolAdapter) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	// Get MCP config for this tool
	mcpConfig, ok := t.mcpManager.Get(t.toolInfo.MCP)
	if !ok {
		return "", fmt.Errorf("MCP %s not found", t.toolInfo.MCP)
	}

	// Resolve relative paths to absolute paths for filesystem MCPs
	if mcpConfig.WorkspaceRoot != "" {
		argsJSON = t.resolvePaths(argsJSON, mcpConfig.WorkspaceRoot)
	}

	// Execute based on MCP type
	var result string
	var err error
	switch mcpConfig.Type {
	case mcp.MCPTypeStdio:
		result, err = t.executeStdio(ctx, mcpConfig, argsJSON)
	case mcp.MCPTypeSSE:
		result, err = t.executeSSE(ctx, mcpConfig, argsJSON)
	case mcp.MCPTypeStreamableHTTP:
		result, err = t.executeHTTP(ctx, mcpConfig, argsJSON)
	default:
		err = fmt.Errorf("unsupported MCP type: %s", mcpConfig.Type)
	}

	return result, err
}

// resolvePaths resolves relative paths in arguments to absolute paths
func (t *MCPToolAdapter) resolvePaths(argsJSON string, workspaceRoot string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return argsJSON // Return original if parsing fails
	}

	// Check for path argument and resolve if relative
	if path, ok := args["path"].(string); ok {
		if !filepath.IsAbs(path) {
			// Resolve relative path against workspace root
			absPath := filepath.Join(workspaceRoot, path)
			args["path"] = absPath
		}
	}

	// Also check for paths array (for read_multiple_files)
	if paths, ok := args["paths"].([]interface{}); ok {
		for i, p := range paths {
			if pathStr, ok := p.(string); ok && !filepath.IsAbs(pathStr) {
				paths[i] = filepath.Join(workspaceRoot, pathStr)
			}
		}
	}

	// Also check for source and destination (for move_file)
	for key := range args {
		if key == "source" || key == "destination" {
			if path, ok := args[key].(string); ok && !filepath.IsAbs(path) {
				args[key] = filepath.Join(workspaceRoot, path)
			}
		}
	}

	// Also check for path in nested structures (for search_files, list_directory etc)
	// Already handled above for simple path argument

	// Marshal back to JSON
	result, err := json.Marshal(args)
	if err != nil {
		return argsJSON
	}
	return string(result)
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