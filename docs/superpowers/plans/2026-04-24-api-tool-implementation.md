# API 工具实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 groot 添加 API 工具能力，通过 JSON 配置文件定义 HTTP API 调用，自动转换为 eino 工具。

**Architecture:** 创建 internal/apitool 包，包含配置结构、管理器、适配器和执行器；修改 engine.go 集成 API 工具；在 main.go 启动时加载并校验。

**Tech Stack:** Go, eino framework, net/http, JSON

---

## 文件结构

**新建文件：**
- `internal/apitool/config.go` - API 工具配置结构定义
- `internal/apitool/manager.go` - API 工具管理器（加载、注册）
- `internal/apitool/adapter.go` - eino 工具适配器
- `internal/apitool/executor.go` - HTTP 请求执行与变量替换
- `internal/apitool/validator.go` - 环境变量提取与校验

**修改文件：**
- `internal/config/config.go` - 添加 APITools 目录配置
- `internal/config/defaults.go` - 添加默认配置
- `internal/agent/engine.go` - buildTools 方法集成 API 工具
- `cmd/groot/main.go` - 启动时加载 API 工具并校验

---

## Task 1: 创建配置结构

**Files:**
- Create: `internal/apitool/config.go`

- [ ] **Step 1: 创建配置结构定义**

```go
package apitool

// AuthType 认证类型
type AuthType string

const (
	AuthTypeNone    AuthType = "none"
	AuthTypeBearer  AuthType = "bearer"
	AuthTypeBasic   AuthType = "basic"
	AuthTypeAPIKey  AuthType = "apikey"
)

// AuthConfig 认证配置
type AuthConfig struct {
	Type     AuthType `json:"type"`
	Token    string   `json:"token,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	Key      string   `json:"key,omitempty"`
	Location string   `json:"location,omitempty"` // header 或 query
	Name     string   `json:"name,omitempty"`     // header名或query参数名
}

// Parameter 参数定义
type Parameter struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`      // string/int/float/bool/array/object
	Required    bool        `json:"required"`
	Default     interface{} `json:"default,omitempty"`
	Description string      `json:"description"`
}

// APIToolConfig API工具配置
type APIToolConfig struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	URL         string                 `json:"url"`
	Method      string                 `json:"method"`
	Auth        *AuthConfig            `json:"auth,omitempty"`
	Headers     map[string]string      `json:"headers,omitempty"`
	Query       map[string]string      `json:"query,omitempty"`
	Body        map[string]interface{} `json:"body,omitempty"`
	BodyType    string                 `json:"bodyType,omitempty"` // json 或 form
	Timeout     int                    `json:"timeout,omitempty"`
	Parameters  []Parameter            `json:"parameters,omitempty"`
}

// DefaultTimeout 默认超时时间
const DefaultTimeout = 30

// GetTimeout 获取超时时间，未配置则返回默认值
func (c *APIToolConfig) GetTimeout() int {
	if c.Timeout <= 0 {
		return DefaultTimeout
	}
	return c.Timeout
}
```

- [ ] **Step 2: 提交**

```bash
git add internal/apitool/config.go
git commit -m "feat: 添加 API 工具配置结构定义"
```

---

## Task 2: 创建环境变量校验器

**Files:**
- Create: `internal/apitool/validator.go`

- [ ] **Step 1: 创建环境变量提取和校验函数**

```go
package apitool

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// EnvVarPattern 环境变量模式 $${VAR_NAME}
const EnvVarPattern = `\$\$\{([^}]+)\}`

// ExtractEnvVars 从配置中提取所有环境变量引用
func ExtractEnvVars(config *APIToolConfig) []string {
	envVars := []string{}
	pattern := regexp.MustCompile(EnvVarPattern)

	// 从URL提取
	matches := pattern.FindAllStringSubmatch(config.URL, -1)
	for _, match := range matches {
		if len(match) > 1 {
			envVars = append(envVars, match[1])
		}
	}

	// 从Headers提取
	for _, value := range config.Headers {
		matches := pattern.FindAllStringSubmatch(value, -1)
		for _, match := range matches {
			if len(match) > 1 {
				envVars = append(envVars, match[1])
			}
		}
	}

	// 从Query提取
	for _, value := range config.Query {
		matches := pattern.FindAllStringSubmatch(value, -1)
		for _, match := range matches {
			if len(match) > 1 {
				envVars = append(envVars, match[1])
			}
		}
	}

	// 从Body提取（递归处理）
	extractEnvVarsFromBody(config.Body, pattern, &envVars)

	// 从Auth提取
	if config.Auth != nil {
		if config.Auth.Token != "" {
			matches := pattern.FindAllStringSubmatch(config.Auth.Token, -1)
			for _, match := range matches {
				if len(match) > 1 {
					envVars = append(envVars, match[1])
				}
			}
		}
		if config.Auth.Password != "" {
			matches := pattern.FindAllStringSubmatch(config.Auth.Password, -1)
			for _, match := range matches {
				if len(match) > 1 {
					envVars = append(envVars, match[1])
				}
			}
		}
		if config.Auth.Key != "" {
			matches := pattern.FindAllStringSubmatch(config.Auth.Key, -1)
			for _, match := range matches {
				if len(match) > 1 {
					envVars = append(envVars, match[1])
				}
			}
		}
	}

	// 去重
	return uniqueStrings(envVars)
}

// extractEnvVarsFromBody 从Body中递归提取环境变量
func extractEnvVarsFromBody(body map[string]interface{}, pattern *regexp.Regexp, envVars *[]string) {
	if body == nil {
		return
	}
	for _, value := range body {
		switch v := value.(type) {
		case string:
			matches := pattern.FindAllStringSubmatch(v, -1)
			for _, match := range matches {
				if len(match) > 1 {
					*envVars = append(*envVars, match[1])
				}
			}
		case map[string]interface{}:
			extractEnvVarsFromBody(v, pattern, envVars)
		case []interface{}:
			for _, item := range v {
				if str, ok := item.(string); ok {
					matches := pattern.FindAllStringSubmatch(str, -1)
					for _, match := range matches {
						if len(match) > 1 {
							*envVars = append(*envVars, match[1])
						}
					}
				}
				if nested, ok := item.(map[string]interface{}); ok {
					extractEnvVarsFromBody(nested, pattern, envVars)
				}
			}
		}
	}
}

// uniqueStrings 去重
func uniqueStrings(strs []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// ValidateEnvVars 校验所有环境变量是否已设置
func ValidateEnvVars(config *APIToolConfig) error {
	envVars := ExtractEnvVars(config)
	for _, envVar := range envVars {
		value := os.Getenv(envVar)
		if value == "" {
			return fmt.Errorf("环境变量 %s 未设置，工具 %s 无法加载", envVar, config.Name)
		}
	}
	return nil
}

// CheckToolNameConflict 检查工具名称冲突
func CheckToolNameConflict(apiTools []*APIToolConfig, existingToolNames []string) error {
	for _, apiTool := range apiTools {
		for _, existingName := range existingToolNames {
			if apiTool.Name == existingName {
				return fmt.Errorf("工具名称冲突: %s 在 MCP 和 API 工具中都定义了", apiTool.Name)
			}
		}
	}
	return nil
}

// GetAllEnvVars 从多个配置中提取所有环境变量
func GetAllEnvVars(configs []*APIToolConfig) []string {
	allEnvVars := []string{}
	for _, config := range configs {
		allEnvVars = append(allEnvVars, ExtractEnvVars(config))
	}
	return uniqueStrings(allEnvVars)
}

// ValidateAllEnvVars 校验多个配置的所有环境变量
func ValidateAllEnvVars(configs []*APIToolConfig) error {
	for _, config := range configs {
		if err := ValidateEnvVars(config); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 2: 提交**

```bash
git add internal/apitool/validator.go
git commit -m "feat: 添加 API 工具环境变量校验器"
```

---

## Task 3: 创建 HTTP 执行器

**Files:**
- Create: `internal/apitool/executor.go`

- [ ] **Step 1: 创建 HTTP 请求执行器**

```go
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
```

注意：需要添加 `import "os"` 到文件开头。

- [ ] **Step 2: 修复 import**

在 executor.go 文件开头的 import 块中添加 `"os"`：

```go
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"  // 添加这个
	"regexp"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/logger"
)
```

- [ ] **Step 3: 提交**

```bash
git add internal/apitool/executor.go
git commit -m "feat: 添加 API 工具 HTTP 执行器"
```

---

## Task 4: 创建管理器

**Files:**
- Create: `internal/apitool/manager.go`

- [ ] **Step 1: 创建 API 工具管理器**

```go
package apitool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// Manager API工具管理器
type Manager struct {
	tools    map[string]*APIToolConfig
	executor *Executor
	log      *logger.Logger
	mu       sync.RWMutex
}

// NewManager 创建管理器
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		tools:    make(map[string]*APIToolConfig),
		executor: NewExecutor(log),
		log:      log,
	}
}

// GetExecutor 获取执行器
func (m *Manager) GetExecutor() *Executor {
	return m.executor
}

// Register 注册工具
func (m *Manager) Register(config *APIToolConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tools[config.Name] = config
	m.log.Info("注册API工具", zap.String("name", config.Name))
}

// Get 获取工具配置
func (m *Manager) Get(name string) (*APIToolConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	config, ok := m.tools[name]
	return config, ok
}

// List 列出所有工具
func (m *Manager) List() []*APIToolConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*APIToolConfig, 0, len(m.tools))
	for _, config := range m.tools {
		result = append(result, config)
	}
	return result
}

// ListToolNames 列出所有工具名称
func (m *Manager) ListToolNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, 0, len(m.tools))
	for name := range m.tools {
		result = append(result, name)
	}
	return result
}

// Count 工具数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tools)
}

// LoadAll 加载目录下所有配置
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
				return fmt.Errorf("加载 %s 失败: %w", path, err)
			}
		}
	}

	return nil
}

// Load 加载单个配置文件
func (m *Manager) Load(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var config APIToolConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return fmt.Errorf("解析配置失败: %w", err)
	}

	if config.Name == "" {
		return fmt.Errorf("缺少必填字段: name")
	}

	if config.Description == "" {
		return fmt.Errorf("缺少必填字段: description")
	}

	if config.URL == "" {
		return fmt.Errorf("缺少必填字段: url")
	}

	if config.Method == "" {
		return fmt.Errorf("缺少必填字段: method")
	}

	// 校验环境变量
	if err := ValidateEnvVars(&config); err != nil {
		return err
	}

	m.Register(&config)
	return nil
}

// ValidateAndLoad 校验后加载（用于启动时检查）
func (m *Manager) ValidateAndLoad(dir string, existingToolNames []string) error {
	// 先加载所有配置（不注册）
	configs, err := m.loadConfigsOnly(dir)
	if err != nil {
		return err
	}

	// 校验所有环境变量
	if err := ValidateAllEnvVars(configs); err != nil {
		return err
	}

	// 检查命名冲突
	if err := CheckToolNameConflict(configs, existingToolNames); err != nil {
		return err
	}

	// 注册所有工具
	for _, config := range configs {
		m.Register(config)
	}

	return nil
}

// loadConfigsOnly 仅加载配置文件，不注册
func (m *Manager) loadConfigsOnly(dir string) ([]*APIToolConfig, error) {
	configs := []*APIToolConfig{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return configs, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			path := filepath.Join(dir, entry.Name())
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
			}

			var config APIToolConfig
			if err := json.Unmarshal(content, &config); err != nil {
				return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
			}

			if config.Name == "" {
				return nil, fmt.Errorf("%s 缺少必填字段: name", path)
			}

			configs = append(configs, &config)
		}
	}

	return configs, nil
}
```

- [ ] **Step 2: 提交**

```bash
git add internal/apitool/manager.go
git commit -m "feat: 添加 API 工具管理器"
```

---

## Task 5: 创建 eino 适配器

**Files:**
- Create: `internal/apitool/adapter.go`

- [ ] **Step 1: 创建 API 工具适配器**

```go
package apitool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/logger"
)

// APIToolAdapter 将API工具适配到eino的InvokableTool接口
type APIToolAdapter struct {
	config   *APIToolConfig
	manager  *Manager
	log      *logger.Logger
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

	t.log.Info("API工具执行成功", "tool", t.config.Name, "resultLen", len(result))
	return result, nil
}
```

- [ ] **Step 2: 提交**

```bash
git add internal/apitool/adapter.go
git commit -m "feat: 添加 API 工具 eino 适配器"
```

---

## Task 6: 修改系统配置

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/defaults.go`

- [ ] **Step 1: 在 config.go 中添加 APITools 配置**

在 `Config` 结构体中添加 `APITools` 字段（第21行之后）：

```go
type Config struct {
	Agent      AgentConfig      `yaml:"agent"`
	Server     ServerConfig     `yaml:"server"`
	LLM        LLMConfig        `yaml:"llm"`
	Skills     SkillsConfig     `yaml:"skills"`
	MCP        MCPConfig        `yaml:"mcp"`
	APITools   APIToolsConfig   `yaml:"api_tools"` // 添加这一行
	Memory     MemoryConfig     `yaml:"memory"`
	React      ReactConfig      `yaml:"react"`
	Attachment AttachmentConfig `yaml:"attachment"`
	Security   SecurityConfig   `yaml:"security"`
	Logging    LoggingConfig    `yaml:"logging"`
}
```

在 config.go 文件末尾添加 APIToolsConfig 结构体：

```go
// APIToolsConfig API工具配置
type APIToolsConfig struct {
	Directory string `yaml:"directory"` // API工具配置目录
}
```

- [ ] **Step 2: 在 defaults.go 中添加默认值**

读取 defaults.go 文件，在默认配置中添加 APITools 配置：

```go
// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		// ... 其他默认值 ...
		MCP: MCPConfig{
			Directory: "mcp",
		},
		APITools: APIToolsConfig{
			Directory: "api",
		},
		// ... 其他默认值 ...
	}
}
```

- [ ] **Step 3: 提交**

```bash
git add internal/config/config.go internal/config/defaults.go
git commit -m "feat: 添加 API 工具目录配置"
```

---

## Task 7: 集成到 Agent Engine

**Files:**
- Modify: `internal/agent/engine.go`

- [ ] **Step 1: 修改 Engine 结构体添加 apiManager**

在 Engine 结构体中添加 apiManager 字段（约第33-39行）：

```go
type Engine struct {
	llmConfig      config.LLMConfig
	skillsRegistry *skill.Registry
	mcpManager     *mcp.Manager
	apiManager     *apitool.Manager  // 添加这一行
	reactConfig    config.ReactConfig
	log            *logger.Logger
}
```

- [ ] **Step 2: 修改 NewEngine 函数参数**

修改 NewEngine 函数（约第41-56行）：

```go
func NewEngine(
	cfg config.LLMConfig,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	apiMgr *apitool.Manager,  // 添加这一行
	reactCfg config.ReactConfig,
	log *logger.Logger,
) *Engine {
	return &Engine{
		llmConfig:      cfg,
		skillsRegistry: skills,
		mcpManager:     mcpMgr,
		apiManager:     apiMgr,  // 添加这一行
		reactConfig:    reactCfg,
		log:            log,
	}
}
```

- [ ] **Step 3: 添加 import**

在 engine.go 文件开头添加 apitool 包导入：

```go
import (
	// ... 其他导入 ...
	"github.com/zfd81/groot/internal/apitool"
	// ... 其他导入 ...
)
```

- [ ] **Step 4: 修改 buildTools 方法**

修改 buildTools 方法（约第350-360行），添加 API 工具：

```go
func (e *Engine) buildTools() []tool.BaseTool {
	tools := []tool.BaseTool{}

	// MCP 工具
	for _, toolInfo := range e.mcpManager.ListTools() {
		t := NewMCPToolAdapter(toolInfo, e.mcpManager, e.log)
		tools = append(tools, t)
	}

	// API 工具
	for _, apiConfig := range e.apiManager.List() {
		t := apitool.NewAPIToolAdapter(apiConfig, e.apiManager, e.log)
		tools = append(tools, t)
	}

	return tools
}
```

- [ ] **Step 5: 提交**

```bash
git add internal/agent/engine.go
git commit -m "feat: 集成 API 工具到 Agent Engine"
```

---

## Task 8: 修改 Executor 初始化

**Files:**
- Modify: `internal/agent/executor.go`

- [ ] **Step 1: 修改 Executor 结构体**

在 Executor 结构体中添加 apiManager 字段（约第91-98行）：

```go
type Executor struct {
	memoryManager *memory.Manager
	skillRegistry *skill.Registry
	mcpManager    *mcp.Manager
	apiManager    *apitool.Manager  // 添加这一行
	config        config.Config
	logger        *logger.Logger
	runningTasks  sync.Map
}
```

- [ ] **Step 2: 修改 NewExecutor 函数参数**

修改 NewExecutor 函数（约第100-115行）：

```go
func NewExecutor(
	memMgr *memory.Manager,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	apiMgr *apitool.Manager,  // 添加这一行
	cfg config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		memoryManager: memMgr,
		skillRegistry: skills,
		mcpManager:    mcpMgr,
		apiManager:    apiMgr,  // 添加这一行
		config:        cfg,
		logger:        log,
	}
}
```

- [ ] **Step 3: 添加 import**

在 executor.go 文件开头添加 apitool 包导入：

```go
import (
	// ... 其他导入 ...
	"github.com/zfd81/groot/internal/apitool"
	// ... 其他导入 ...
)
```

- [ ] **Step 4: 修改 Engine 创建**

修改 NewEngine 调用，传入 apiManager（约第136-143行）：

```go
engine := NewEngine(
	e.config.LLM,
	e.skillRegistry,
	e.mcpManager,
	e.apiManager,  // 添加这一行
	e.config.React,
	e.logger,
)
```

- [ ] **Step 5: 提交**

```bash
git add internal/agent/executor.go
git commit -m "feat: Executor 支持 API 工具管理器"
```

---

## Task 9: 修改主程序启动流程

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 添加 import**

在 main.go 文件开头添加 apitool 包导入：

```go
import (
	// ... 其他导入 ...
	"github.com/zfd81/groot/internal/apitool"
	// ... 其他导入 ...
)
```

- [ ] **Step 2: 初始化 API 工具管理器**

在 startServer 函数中，MCP 加载之后添加 API 工具初始化（约第148-156行之后）：

```go
// Initialize MCP manager
mcpMgr := mcp.NewManager(log)

// Load MCP configs
mcpDir := config.ResolvePath(cfg.MCP.Directory, homeDir)
if err := mcpMgr.LoadAll(mcpDir); err != nil {
	log.Error("无法加载MCP配置", zap.Error(err))
}
log.Info("MCP 加载完成", zap.Int("count", mcpMgr.Count()), zap.String("dir", mcpDir))

// Initialize API tool manager
apiMgr := apitool.NewManager(log)

// Load API tool configs
apiDir := config.ResolvePath(cfg.APITools.Directory, homeDir)
// 先获取MCP工具名称列表用于冲突检查
mcpToolNames := []string{}
for _, tool := range mcpMgr.ListTools() {
	mcpToolNames = append(mcpToolNames, tool.Name)
}
// 校验并加载API工具
if err := apiMgr.ValidateAndLoad(apiDir, mcpToolNames); err != nil {
	log.Error("无法加载API工具配置", zap.Error(err))
	fmt.Fprintf(os.Stderr, "加载API工具失败: %s\n", err)
	os.Exit(1)
}
log.Info("API工具 加载完成", zap.Int("count", apiMgr.Count()), zap.String("dir", apiDir))
```

- [ ] **Step 3: 修改 NewExecutor 调用**

修改 NewServer 调用，需要先修改 api/server.go 中的 NewServer 函数，但这里先修改 NewExecutor 创建。

找到 NewExecutor 的调用位置并修改：

```go
runtimeState := agent.NewRuntimeState()

// Initialize runtime state
runtimeState := agent.NewRuntimeState()

// 修改 NewExecutor 调用，传入 apiMgr
// （注意：NewExecutor 在 api/server.go 中被调用）
```

实际上需要修改 `internal/api/server.go` 中的 NewServer 函数。

- [ ] **Step 4: 修改 api/server.go**

读取 `internal/api/server.go`，找到 NewServer 函数：

```go
func NewServer(cfg config.Config, homeDir string, log *logger.Logger, memMgr *memory.Manager, runtimeState *agent.RuntimeState, skillsRegistry *skill.Registry, mcpMgr *mcp.Manager) *Server {
```

修改为：

```go
func NewServer(cfg config.Config, homeDir string, log *logger.Logger, memMgr *memory.Manager, runtimeState *agent.RuntimeState, skillsRegistry *skill.Registry, mcpMgr *mcp.Manager, apiMgr *apitool.Manager) *Server {
```

并修改内部 Executor 创建：

```go
executor := agent.NewExecutor(memMgr, skillsRegistry, mcpMgr, apiMgr, cfg, log)
```

添加 import：

```go
import (
	// ... 其他导入 ...
	"github.com/zfd81/groot/internal/apitool"
)
```

- [ ] **Step 5: 修改 main.go 中的 NewServer 调用**

```go
srv := api.NewServer(*cfg, homeDir, log, memMgr, runtimeState, skillsRegistry, mcpMgr, apiMgr)
```

- [ ] **Step 6: 提交**

```bash
git add cmd/groot/main.go internal/api/server.go
git commit -m "feat: 启动时加载 API 工具并校验"
```

---

## Task 10: 编译验证

**Files:**
- 无新建文件

- [ ] **Step 1: 编译项目**

```bash
cd /Users/zhangfengda/workspace/groot
go build -o bin/groot ./cmd
```

预期：编译成功，无错误

- [ ] **Step 2: 检查编译产物**

```bash
ls -la bin/groot
```

预期：存在 bin/groot 文件

- [ ] **Step 3: 提交（如有修复）**

如果编译过程中发现问题并修复，提交修复：

```bash
git add <修改的文件>
git commit -m "fix: 修复编译问题"
```

---

## Task 11: 创建示例配置文件

**Files:**
- Create: `tests/api/example_tool.json`（测试示例，不提交到生产）

- [ ] **Step 1: 创建示例配置**

```json
{
  "name": "example_weather",
  "description": "示例天气API工具（仅供测试）",

  "url": "https://api.example.com/weather/${city}",
  "method": "GET",

  "query": {
    "unit": "${unit}"
  },

  "timeout": 30,

  "parameters": [
    {"name": "city", "type": "string", "required": true, "description": "城市名称"},
    {"name": "unit", "type": "string", "required": false, "default": "celsius", "description": "温度单位"}
  ]
}
```

此文件仅供测试参考，实际使用时用户会在 `~/.groot/api/` 目录创建配置。

---

## 自检

**1. Spec coverage:**
- ✅ 配置格式和字段定义 - Task 1
- ✅ 变量语法 - Task 3
- ✅ 参数处理逻辑 - Task 3
- ✅ 认证配置 - Task 3
- ✅ 请求体格式 - Task 3
- ✅ 启动检查（环境变量、命名冲突） - Task 2, Task 9
- ✅ 返回行为 - Task 3
- ✅ 错误处理 - Task 3
- ✅ 工具适配 - Task 5
- ✅ 工具注册 - Task 7, Task 8, Task 9

**2. Placeholder scan:**
- 无 TBD/TODO
- 所有代码步骤都有完整代码

**3. Type consistency:**
- APIToolConfig 在 config.go 定义，在 manager.go 和 adapter.go 中使用
- Manager 在 manager.go 定义，在 engine.go 和 main.go 中使用
- Executor 在 executor.go 定义，在 adapter.go 中使用