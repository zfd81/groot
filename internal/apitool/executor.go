package apitool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/logger"
)

// Executor HTTP请求执行器
type Executor struct {
	log *logger.Logger
}

// NewExecutor 创建执行器
func NewExecutor(log *logger.Logger) *Executor {
	return &Executor{log: log}
}

// ParamVarPattern 参数变量模式 ${param_name}
var ParamVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// EnvVarPatternCompiled 环境变量模式 $${ENV_VAR}
var EnvVarPatternCompiled = regexp.MustCompile(`\$\$\{([^}]+)\}`)

// Execute 执行HTTP请求
func (e *Executor) Execute(ctx context.Context, config *APIToolConfig, args map[string]interface{}) (string, error) {
	// 1. 参数校验
	if err := e.validateParameters(config, args); err != nil {
		return "", err
	}

	// 2. 合并参数（传入值 + 默认值）
	params := e.mergeParameters(config, args)

	// 3. 替换变量
	finalURL := e.replaceVariables(config.URL, params)
	finalHeaders := e.replaceVariablesInMap(config.Headers, params)
	finalQuery := e.replaceVariablesInMap(config.Query, params)
	finalBody := e.replaceVariablesInBody(config.Body, params)

	// 4. 处理认证
	authHeaders, authQuery := e.buildAuth(config)
	for k, v := range authHeaders {
		finalHeaders[k] = v
	}
	for k, v := range authQuery {
		finalQuery[k] = v
	}

	// 5. 构建完整URL（添加query参数）
	if len(finalQuery) > 0 {
		queryStr := e.buildQueryString(finalQuery)
		if strings.Contains(finalURL, "?") {
			finalURL = finalURL + "&" + queryStr
		} else {
			finalURL = finalURL + "?" + queryStr
		}
	}

	// 6. 构建请求体
	var bodyReader io.Reader
	if len(finalBody) > 0 && (config.Method == "POST" || config.Method == "PUT" || config.Method == "PATCH") {
		bodyReader = e.buildBody(finalBody, config.BodyType)
	}

	// 7. 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, config.Method, finalURL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	// 8. 设置请求头
	for k, v := range finalHeaders {
		req.Header.Set(k, v)
	}

	// 设置Content-Type（如果有body）
	if bodyReader != nil {
		switch config.BodyType {
		case "json":
			req.Header.Set("Content-Type", "application/json")
		case "form":
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}

	// 9. 执行请求
	client := &http.Client{
		Timeout: time.Duration(config.GetTimeout()) * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "context deadline exceeded") {
			return "", fmt.Errorf("请求超时（%d秒）", config.GetTimeout())
		}
		return "", fmt.Errorf("网络请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 10. 读取响应
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	// 11. 处理响应
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return string(bodyBytes), nil
	}

	return "", fmt.Errorf("HTTP错误: 状态码%d, 响应内容: %s", resp.StatusCode, string(bodyBytes))
}

// validateParameters 校验必填参数
func (e *Executor) validateParameters(config *APIToolConfig, args map[string]interface{}) error {
	for _, param := range config.Parameters {
		if param.Required {
			value, exists := args[param.Name]
			if !exists && param.Default == nil {
				return fmt.Errorf("缺少必填参数: %s", param.Name)
			}
			if exists && value == nil && param.Default == nil {
				return fmt.Errorf("缺少必填参数: %s", param.Name)
			}
		}
	}
	return nil
}

// mergeParameters 合并传入参数和默认值
func (e *Executor) mergeParameters(config *APIToolConfig, args map[string]interface{}) map[string]interface{} {
	params := map[string]interface{}{}

	// 先设置默认值
	for _, param := range config.Parameters {
		if param.Default != nil {
			params[param.Name] = param.Default
		}
	}

	// 再用传入值覆盖
	for k, v := range args {
		params[k] = v
	}

	return params
}

// replaceVariables 替换字符串中的变量
func (e *Executor) replaceVariables(s string, params map[string]interface{}) string {
	// 先替换环境变量 $${ENV_VAR}
	result := EnvVarPatternCompiled.ReplaceAllStringFunc(s, func(match string) string {
		envVar := EnvVarPatternCompiled.FindStringSubmatch(match)[1]
		return os.Getenv(envVar)
	})

	// 再替换参数变量 ${param}
	result = ParamVarPattern.ReplaceAllStringFunc(result, func(match string) string {
		paramName := ParamVarPattern.FindStringSubmatch(match)[1]
		if val, ok := params[paramName]; ok {
			return fmt.Sprintf("%v", val)
		}
		return match // 未找到则保留原样
	})

	return result
}

// replaceVariablesInMap 替换map中的变量
func (e *Executor) replaceVariablesInMap(m map[string]string, params map[string]interface{}) map[string]string {
	result := map[string]string{}
	for k, v := range m {
		result[k] = e.replaceVariables(v, params)
	}
	return result
}

// replaceVariablesInBody 替换body中的变量
func (e *Executor) replaceVariablesInBody(body map[string]interface{}, params map[string]interface{}) map[string]interface{} {
	if body == nil {
		return nil
	}
	return e.replaceInBodyRecursive(body, params)
}

// replaceInBodyRecursive 递归替换body中的变量
func (e *Executor) replaceInBodyRecursive(body map[string]interface{}, params map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}
	for k, v := range body {
		switch val := v.(type) {
		case string:
			result[k] = e.replaceVariables(val, params)
		case map[string]interface{}:
			result[k] = e.replaceInBodyRecursive(val, params)
		case []interface{}:
			result[k] = e.replaceInArrayRecursive(val, params)
		default:
			result[k] = v
		}
	}
	return result
}

// replaceInArrayRecursive 递归替换数组中的变量
func (e *Executor) replaceInArrayRecursive(arr []interface{}, params map[string]interface{}) []interface{} {
	result := []interface{}{}
	for _, item := range arr {
		switch val := item.(type) {
		case string:
			result = append(result, e.replaceVariables(val, params))
		case map[string]interface{}:
			result = append(result, e.replaceInBodyRecursive(val, params))
		case []interface{}:
			result = append(result, e.replaceInArrayRecursive(val, params))
		default:
			result = append(result, item)
		}
	}
	return result
}

// buildAuth 构建认证信息
func (e *Executor) buildAuth(config *APIToolConfig) (headers map[string]string, query map[string]string) {
	headers = map[string]string{}
	query = map[string]string{}

	if config.Auth == nil || config.Auth.Type == AuthTypeNone {
		return headers, query
	}

	switch config.Auth.Type {
	case AuthTypeBearer:
		token := e.replaceVariables(config.Auth.Token, nil)
		headers["Authorization"] = "Bearer " + token
	case AuthTypeBasic:
		username := config.Auth.Username
		password := e.replaceVariables(config.Auth.Password, nil)
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		headers["Authorization"] = "Basic " + credentials
	case AuthTypeAPIKey:
		key := e.replaceVariables(config.Auth.Key, nil)
		if config.Auth.Location == "header" {
			headers[config.Auth.Name] = key
		} else {
			query[config.Auth.Name] = key
		}
	}

	return headers, query
}

// buildQueryString 构建查询字符串
func (e *Executor) buildQueryString(query map[string]string) string {
	values := url.Values{}
	for k, v := range query {
		values.Set(k, v)
	}
	return values.Encode()
}

// buildBody 构建请求体
func (e *Executor) buildBody(body map[string]interface{}, bodyType string) io.Reader {
	if bodyType == "form" {
		values := url.Values{}
		for k, v := range body {
			values.Set(k, fmt.Sprintf("%v", v))
		}
		return bytes.NewBufferString(values.Encode())
	}

	// 默认json
	jsonBytes, _ := json.Marshal(body)
	return bytes.NewBuffer(jsonBytes)
}