package apitool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// APIToolAdapter 将API工具适配到eino的InvokableTool接口
type APIToolAdapter struct {
	config  *APIToolConfig
	manager *Manager
	log     *logger.Logger
}

// NewAPIToolAdapter 创建适配器
func NewAPIToolAdapter(config *APIToolConfig, manager *Manager, log *logger.Logger) *APIToolAdapter {
	return &APIToolAdapter{
		config:  config,
		manager: manager,
		log:     log,
	}
}

// Info 返回工具元信息
func (t *APIToolAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	params := t.convertParameters(t.config.Parameters)

	return &schema.ToolInfo{
		Name:        t.config.Name,
		Desc:        t.config.Description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

// convertParameters 将API工具参数转换为eino格式
func (t *APIToolAdapter) convertParameters(params []Parameter) map[string]*schema.ParameterInfo {
	result := map[string]*schema.ParameterInfo{}

	for _, p := range params {
		paramInfo := &schema.ParameterInfo{
			Type:     t.convertType(p.Type),
			Desc:     p.Description,
			Required: p.Required,
		}
		result[p.Name] = paramInfo
	}

	return result
}

// convertType 转换参数类型
func (t *APIToolAdapter) convertType(typeStr string) schema.DataType {
	switch typeStr {
	case "string":
		return schema.String
	case "int", "integer":
		return schema.Number
	case "float", "number":
		return schema.Number
	case "bool", "boolean":
		return schema.Boolean
	case "array":
		return schema.Array
	case "object":
		return schema.Object
	default:
		return schema.String
	}
}

// InvokableRun 执行工具调用
func (t *APIToolAdapter) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	// 解析参数
	var args map[string]interface{}
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("解析参数失败: %w", err)
		}
	} else {
		args = map[string]interface{}{}
	}

	// 获取执行器
	executor := t.manager.GetExecutor()

	// 执行HTTP请求
	result, err := executor.Execute(ctx, t.config, args)
	if err != nil {
		t.log.Error("API工具执行失败: " + err.Error())
		return "", err
	}

	t.log.Info("API工具执行成功", zap.String("tool", t.config.Name), zap.Int("resultLen", len(result)))
	return result, nil
}