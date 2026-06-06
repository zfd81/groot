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

func TestGenerateConfigTemplate_HasStorageBlock(t *testing.T) {
	tpl := GenerateConfigTemplate()
	if !strings.Contains(tpl, "# 存储抽象层配置") {
		t.Error("missing storage section header")
	}
	if !strings.Contains(tpl, "storage:") {
		t.Error("missing storage: key")
	}
	if !strings.Contains(tpl, "  #minio:") {
		t.Error("expected '  #minio:' (2-space indent then #) so removing # yields '  minio:'")
	}
	if !strings.Contains(tpl, "  #  endpoint:") {
		t.Error("expected '  #  endpoint:' (2-space indent then # then 2 more spaces) so removing # yields '    endpoint:' (4-space)")
	}
	if !strings.Contains(tpl, "${MINIO_ACCESS_KEY}") {
		t.Error("missing minio access_key env placeholder")
	}
	if !strings.Contains(tpl, "${MINIO_SECRET_KEY}") {
		t.Error("missing minio secret_key env placeholder")
	}
}

// TestGenerateConfigTemplate_StorageMinioUncommented 验证模板格式：用户取消
// 注释（删掉行首的 # 字符）后能得到合法的 yaml 缩进，并被正确解析为 MinioConfig。
func TestGenerateConfigTemplate_StorageMinioUncommented(t *testing.T) {
	tpl := GenerateConfigTemplate()
	// 模拟"用户取消 minio 块的注释"——按行匹配，把以"2 空格 + #"开头的行
	// 的 # 删掉。整个模板里只有 storage 块的 minio 子节用了"行首 2 空格缩进
	// 后再加 #"的格式（其他注释块如 #agent: / #server: 都是 # 顶格；LLM 块
	// 里的内联注释如 "value    # 说明"虽含"2 空格 + #"但不在行首），所以这
	// 种按行首匹配的替换不会误伤其他位置。给 storage 加新字段时，只要遵循
	// "  #  field:" 格式，测试自动覆盖，无需同步修改。
	lines := strings.Split(tpl, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "  #") {
			lines[i] = "  " + line[3:]
		}
	}
	uncommented := strings.Join(lines, "\n")

	var c Config
	if err := yaml.Unmarshal([]byte(uncommented), &c); err != nil {
		t.Fatalf("unmarshal uncommented template: %v", err)
	}
	if c.Storage.Minio == nil {
		t.Fatal("expected Storage.Minio to be set after uncommenting")
	}
	if c.Storage.Minio.Endpoint != "localhost:9000" {
		t.Errorf("Endpoint = %q, want localhost:9000", c.Storage.Minio.Endpoint)
	}
	if c.Storage.Minio.Bucket != "groot" {
		t.Errorf("Bucket = %q, want groot", c.Storage.Minio.Bucket)
	}
}

// TestGenerateConfigTemplate_IsValidYAML 验证模板原文（未取消注释时）就是
// 合法的 yaml，捕获未来维护时引入的语法错误（缩进错乱、tab/space 混用等）。
func TestGenerateConfigTemplate_IsValidYAML(t *testing.T) {
	tpl := GenerateConfigTemplate()
	// 模板未取消注释时，storage: 顶格 + minio 子节全是注释，应是合法 yaml
	var c Config
	if err := yaml.Unmarshal([]byte(tpl), &c); err != nil {
		t.Fatalf("template should be valid YAML, unmarshal error: %v", err)
	}
	// 默认情况下 minio 子节被注释，Storage.Minio 应为 nil
	if c.Storage.Minio != nil {
		t.Error("with all minio lines commented, Storage.Minio should be nil")
	}
}
