package apitool

import (
	"fmt"
	"os"
	"regexp"
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
		allEnvVars = append(allEnvVars, ExtractEnvVars(config)...)
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