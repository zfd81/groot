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
	// MinIO 凭据已剥离到 env.yaml，config.yaml 模板不应再生成 storage: 顶层节
	// 或 minio 注释行（避免用户在 config.yaml 里填了被静默忽略）。
	if strings.Contains(tpl, "\nstorage:") {
		t.Error("config.yaml 模板不应包含 'storage:' 顶层节（已迁移到 env.yaml）")
	}
	if strings.Contains(tpl, "minio:") {
		t.Error("config.yaml 模板不应包含 minio 配置项（已迁移到 env.yaml）")
	}
	// 但应保留指向 env.yaml 的引导注释，让用户知道去哪开启 MinIO
	if !strings.Contains(tpl, "env.yaml") {
		t.Error("config.yaml 模板应在 storage 说明处提到 env.yaml")
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
	// 模板里不再写 storage 节，Storage.Minio 必为 nil
	if c.Storage.Minio != nil {
		t.Error("config.yaml 模板解析后 Storage.Minio 必须为 nil（已迁移到 env.yaml）")
	}
}

// TestGenerateEnvTemplate_DefaultIsLocal 验证默认 env.yaml 全注释，
// 解析后不会产生 minio 配置（=本地磁盘模式）。
func TestGenerateEnvTemplate_DefaultIsLocal(t *testing.T) {
	tpl := GenerateEnvTemplate()
	var ef envFile
	if err := yaml.Unmarshal([]byte(tpl), &ef); err != nil {
		t.Fatalf("env template should be valid YAML: %v", err)
	}
	if ef.Minio != nil {
		t.Error("默认 env.yaml 模板（全注释）解析后 minio 必须为 nil")
	}
}

// TestGenerateEnvTemplate_UncommentedYieldsMinio 模拟用户取消注释后，
// 模板能被正确解析为 MinioConfig（凭据填好即生效）。
func TestGenerateEnvTemplate_UncommentedYieldsMinio(t *testing.T) {
	tpl := GenerateEnvTemplate()
	// 精确白名单：只匹配 minio 节本身的几行（顶层 #minio: + 子字段 #  field:）。
	// 不能用通配前缀 "#  " 之类——那会误伤模板中带 "#   - " 的说明性列表项，
	// 让原本是注释的说明文字被取消注释后变成 yaml 序列。
	enablePrefixes := []string{
		"#minio:",
		"#  endpoint:",
		"#  access_key:",
		"#  secret_key:",
		"#  bucket:",
		"#  use_ssl:",
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
		t.Fatalf("uncommenting env template should yield valid YAML: %v\n---\n%s", err, uncommented)
	}
	if ef.Minio == nil {
		t.Fatal("取消注释后 minio 必须被解析出来")
	}
	if ef.Minio.Endpoint != "localhost:9000" {
		t.Errorf("Endpoint = %q, want localhost:9000", ef.Minio.Endpoint)
	}
	if ef.Minio.Bucket != "groot" {
		t.Errorf("Bucket = %q, want groot", ef.Minio.Bucket)
	}
	if ef.Minio.AccessKey != "${MINIO_ACCESS_KEY}" {
		t.Errorf("AccessKey = %q, want ${MINIO_ACCESS_KEY}", ef.Minio.AccessKey)
	}
}
