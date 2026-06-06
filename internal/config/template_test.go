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
}

// TestGenerateConfigTemplate_StorageMinioUncommented 验证模板格式：用户取消
// 注释（删掉 # 字符）后能得到合法的 yaml 缩进，并被正确解析为 MinioConfig。
func TestGenerateConfigTemplate_StorageMinioUncommented(t *testing.T) {
	tpl := GenerateConfigTemplate()
	// 模拟"用户取消 minio 块的注释"——把 #minio: 替换为 minio:，
	// 把 #  endpoint 等替换为   endpoint
	uncommented := strings.NewReplacer(
		"  #minio:", "  minio:",
		"  #  endpoint:", "    endpoint:",
		"  #  access_key:", "    access_key:",
		"  #  secret_key:", "    secret_key:",
		"  #  bucket:", "    bucket:",
		"  #  use_ssl:", "    use_ssl:",
	).Replace(tpl)

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
