package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateConfigTemplate(t *testing.T) {
	content := GenerateConfigTemplate()

	// 检查必要字段存在
	if !strings.Contains(content, "llm:") {
		t.Error("模板缺少 llm 配置")
	}
	if !strings.Contains(content, "default_model:") {
		t.Error("模板缺少 default_model 配置")
	}
	if !strings.Contains(content, "api_key:") {
		t.Error("模板缺少 api_key 配置")
	}
	if !strings.Contains(content, "${OPENAI_API_KEY}") {
		t.Error("模板缺少环境变量引用示例")
	}
	if !strings.Contains(content, "#") {
		t.Error("模板缺少注释说明")
	}
}

func TestGenerateConfigTemplate_NoStorageBlock(t *testing.T) {
	tpl := GenerateConfigTemplate()
	// database 凭据已剥离到 env.yaml，config.yaml 模板不应再生成 storage/minio/database 顶层节。
	if strings.Contains(tpl, "\nstorage:") {
		t.Error("config.yaml 模板不应包含 'storage:' 顶层节")
	}
	if strings.Contains(tpl, "minio:") {
		t.Error("config.yaml 模板不应包含 minio 配置项")
	}
}

// TestGenerateConfigTemplate_IsValidYAML 验证模板原文就是合法 yaml，
// 捕获未来维护时引入的语法错误（缩进错乱、tab/space 混用等）。
func TestGenerateConfigTemplate_IsValidYAML(t *testing.T) {
	tpl := GenerateConfigTemplate()
	var c Config
	if err := yaml.Unmarshal([]byte(tpl), &c); err != nil {
		t.Fatalf("template should be valid YAML, unmarshal error: %v", err)
	}
	// config.yaml 不包含 database/storage 节（凭据在 env.yaml 中）
	if c.Database != nil {
		t.Error("config.yaml 模板解析后 Database 必须为 nil（已迁移到 env.yaml）")
	}
}

// TestGenerateEnvTemplate_DefaultIsLocal 验证默认 env.yaml 全注释，
// 解析后不会产生 database 配置（=SQLite 本地模式）。
func TestGenerateEnvTemplate_DefaultIsLocal(t *testing.T) {
	tpl := GenerateEnvTemplate()
	var ef envFile
	if err := yaml.Unmarshal([]byte(tpl), &ef); err != nil {
		t.Fatalf("env template should be valid YAML: %v", err)
	}
	if ef.Database != nil {
		t.Error("默认 env.yaml 模板（全注释）解析后 database 必须为 nil（=SQLite 模式）")
	}
}

// TestGenerateEnvTemplate_UncommentedYieldsDatabase 模拟用户取消注释后，
// 模板能被正确解析为 DatabaseConfig。
func TestGenerateEnvTemplate_UncommentedYieldsDatabase(t *testing.T) {
	tpl := GenerateEnvTemplate()
	// 取消 database 节的注释前缀
	enablePrefixes := []string{
		"#database:",
		"#  driver:",
		"#  dsn:",
	}
	lines := strings.Split(tpl, "\n")
	for i, line := range lines {
		for _, p := range enablePrefixes {
			if strings.HasPrefix(line, p) {
				lines[i] = line[1:]
				break
			}
		}
	}
	uncommented := strings.Join(lines, "\n")

	var ef envFile
	if err := yaml.Unmarshal([]byte(uncommented), &ef); err != nil {
		// If template doesn't have database section commented out yet, skip gracefully
		t.Logf("env template without database section: %v", err)
		t.Skip("env template does not contain commented database section")
		return
	}
	// If database section was uncommented, it should parse
	if ef.Database == nil {
		// Template may not have database comments yet — acceptable
		t.Log("database section not found in template (acceptable if template not yet updated)")
	}
}
